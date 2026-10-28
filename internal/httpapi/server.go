package httpapi

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flyboard/internal/auth"
	"flyboard/internal/config"
	"flyboard/internal/control"
	"flyboard/internal/devices"
	"flyboard/internal/ownerlock"

	"flyboard/internal/pull"
	"flyboard/internal/ratelimit"
	"flyboard/internal/secretbox"
	"flyboard/internal/store"
	"fmt"
	"github.com/redis/go-redis/v9"
	qrcode "github.com/skip2/go-qrcode"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	C       config.Config
	S       *store.Store
	Log     *slog.Logger
	Box     *secretbox.Box
	Owners  ownerlock.Service
	Control *control.Hub
	Limit   ratelimit.Limiter
}
type ctxKey int

const adminKey ctxKey = 1

func New(c config.Config, s *store.Store, l *slog.Logger) http.Handler {
	b, e := secretbox.Load(c.MasterKeyFile)
	if e != nil {
		panic("load master key: " + e.Error())
	}
	opt, e := redis.ParseURL(c.ValkeyURL)
	if e != nil {
		panic(e)
	}
	rv := redis.NewClient(opt)
	dv := devices.Valkey{R: rv}
	ds := devices.Service{Store: dv, Window: 120 * time.Second, MissingThreshold: 2}
	x := &Server{C: c, S: s, Log: l, Box: b, Limit: ratelimit.Limiter{R: rv, Prefix: "flyboard:rate:"}}
	x.Owners = ownerlock.Service{Store: ownerlock.Postgres{DB: s.DB}, Window: 120 * time.Second, MigrationGrace: 120 * time.Second, Release: func(ctx context.Context, u int64, o ownerlock.Owner) error { return dv.Remove(ctx, u, "", o.IP) }}
	x.Control = control.New(s.DB, ds)
	go x.dispatchOutbox()
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", x.live)
	m.HandleFunc("GET /health/ready", x.ready)
	m.HandleFunc("POST /bootstrap", x.bootstrap)
	m.Handle("POST /login", x.rate("login", c.LoginRate, http.HandlerFunc(x.login)))
	m.Handle("GET /flyboard/sub/{token}", x.rate("subscription", c.SubscriptionRate, http.HandlerFunc(x.subscription)))
	m.HandleFunc("GET /flyboard/sub/{token}/qr", x.qr)
	m.Handle("GET /s/{token}", x.rate("subscription", c.SubscriptionRate, http.HandlerFunc(x.mainSubscription)))
	m.Handle("GET /api/v1/node/devices", x.rate("node", c.NodeRate, http.HandlerFunc(x.Control.DevicesHTTP)))
	m.Handle("GET /api/v1/node/ws", x.rate("node", c.NodeRate, http.HandlerFunc(x.Control.WS)))
	p := "/api/v2/" + c.AdminPath + "/flyboard/"
	m.Handle(p, x.rate("admin", c.AdminRate, x.require(http.HandlerFunc(x.api))))
	m.HandleFunc("GET /"+c.AdminPath+"/flyboard", x.shell)
	m.HandleFunc("GET /"+c.AdminPath+"/app.css", x.asset)
	m.HandleFunc("GET /"+c.AdminPath+"/app.js", x.asset)
	m.HandleFunc("GET /"+c.AdminPath+"/safety.js", x.asset)
	return security(m)
}

// rate is deliberately fail-closed: loss of shared Valkey must never turn an
// authentication or abuse-control boundary into unlimited per-replica access.
func (s *Server) rate(scope string, limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientIP(r)
		if scope == "subscription" {
			key += ":" + r.PathValue("token")
		}
		if scope == "node" {
			key += ":" + r.Header.Get("X-Flyboard-Node")
		}
		ok, retry, err := s.Limit.Allow(r.Context(), scope, fmt.Sprintf("%x", auth.Digest(key)), limit, s.C.RateWindow)
		if err != nil {
			w.Header().Set("Retry-After", "1")
			jsonOut(w, 503, map[string]any{"error": "rate limiter unavailable"})
			return
		}
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			jsonOut(w, 429, map[string]any{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func security(n http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("X-Frame-Options", "DENY")
		n.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"status": "ok"})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if e := s.S.DB.PingContext(r.Context()); e != nil {
		jsonOut(w, 503, map[string]any{"status": "unready"})
		return
	}
	jsonOut(w, 200, map[string]any{"status": "ready"})
}
func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	var q struct{ Token, Email, Password string }
	if decode(r, &q) != nil || len(q.Password) < 12 {
		jsonOut(w, 422, map[string]any{"error": "invalid input"})
		return
	}
	id, e := s.S.Bootstrap(r.Context(), q.Email, auth.Password(q.Password), auth.Digest(q.Token))
	if e != nil {
		jsonOut(w, 403, map[string]any{"error": "bootstrap unavailable"})
		return
	}
	jsonOut(w, 201, map[string]any{"id": id})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var q struct{ Email, Password string }
	if decode(r, &q) != nil {
		jsonOut(w, 422, map[string]any{"error": "invalid input"})
		return
	}
	a, e := s.S.AdminByEmail(r.Context(), q.Email)
	if e != nil || !auth.Check(a.PasswordHash, q.Password) {
		jsonOut(w, 401, map[string]any{"error": "invalid credentials"})
		return
	}
	tok, csrf := auth.Token(), auth.Token()
	if e := s.S.Session(r.Context(), a.ID, auth.Digest(tok), csrf, time.Now().Add(s.C.SessionTTL)); e != nil {
		jsonOut(w, 503, map[string]any{"error": "login unavailable"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "flyboard_session", Value: tok, Path: "/", HttpOnly: true, Secure: s.C.CookieSecure != "false", SameSite: http.SameSiteStrictMode, MaxAge: int(s.C.SessionTTL.Seconds())})
	jsonOut(w, 200, map[string]any{"csrf_token": csrf, "admin_path": "/" + s.C.AdminPath + "/flyboard"})
}
func (s *Server) require(n http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("flyboard_session")
		if e != nil {
			jsonOut(w, 401, map[string]any{"error": "authentication required"})
			return
		}
		a, csrf, e := s.S.SessionAdmin(r.Context(), auth.Digest(c.Value))
		if e != nil {
			jsonOut(w, 401, map[string]any{"error": "authentication required"})
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 || !validOrigin(r) {
				jsonOut(w, 403, map[string]any{"error": "csrf validation failed"})
				return
			}
			if a.Role == "auditor" {
				jsonOut(w, 403, map[string]any{"error": "read-only role"})
				return
			}
			if a.Role != "owner" && (strings.Contains(r.URL.Path, "/cidrs") || strings.Contains(r.URL.Path, "/settings") || strings.Contains(r.URL.Path, "/nodes")) {
				jsonOut(w, 403, map[string]any{"error": "owner role required"})
				return
			}
		}
		if !s.managementIPAllowed(r) {
			jsonOut(w, 403, map[string]any{"error": "management network denied"})
			return
		}
		n.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminKey, a)))
	})
}

func validOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}
func (s *Server) shell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b, _ := webFS.ReadFile("web/index.html")
	w.Write(b)
}
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := "web/" + strings.TrimPrefix(r.URL.Path, "/"+s.C.AdminPath+"/")
	b, e := webFS.ReadFile(name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css")
	} else {
		w.Header().Set("Content-Type", "text/javascript")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	base := "/api/v2/" + s.C.AdminPath + "/flyboard/"
	part := strings.TrimPrefix(r.URL.Path, base)
	if part == "overview" && r.Method == "GET" {
		s.overview(w, r)
		return
	}
	if part == "pulls" && r.Method == "GET" {
		s.rows(w, r, `select row_to_json(x) from (select id,upstream_id,status,error_code,created_at,started_at,finished_at from upstream_pull_executions order by id desc limit 100)x`)
		return
	}
	if part == "upstream-nodes" && r.Method == "GET" {
		s.rows(w, r, `select row_to_json(x) from (select id,upstream_id,name,type,address,port,enabled,updated_at from upstream_nodes order by id desc limit 200)x`)
		return
	}
	if part == "devices" && r.Method == "GET" {
		s.rows(w, r, `select row_to_json(x) from (select user_id,node_id,ip,seen_at from device_display order by seen_at desc limit 200)x`)
		return
	}
	if part == "whitelist" {
		s.special(w, r, "main_user_whitelist")
		return
	}
	if part == "cidrs" {
		s.special(w, r, "panel_cidrs")
		return
	}
	if part == "audit" && r.Method == "GET" {
		s.rows(w, r, `select row_to_json(x) from (select id,admin_id,action,resource,resource_id,ip,detail,created_at from audit_logs order by id desc limit 200)x`)
		return
	}
	if part == "access-logs" && r.Method == "GET" {
		s.rows(w, r, `select row_to_json(x) from (select id,user_id,client_id,ip,user_agent,action,reason,path,created_at from access_logs order by id desc limit 200)x`)
		return
	}
	if part == "settings" {
		s.settings(w, r)
		return
	}
	if strings.HasPrefix(part, "nodes/") && strings.HasSuffix(part, "/rotate-token") && r.Method == "POST" {
		s.rotateToken(w, r, part, "nodes")
		return
	}
	if strings.HasPrefix(part, "users/") && strings.HasSuffix(part, "/rotate-token") && r.Method == "POST" {
		s.rotateToken(w, r, part, "users")
		return
	}
	if strings.HasPrefix(part, "clients/") && strings.HasSuffix(part, "/rotate-token") && r.Method == "POST" {
		s.rotateToken(w, r, part, "clients")
		return
	}
	if strings.HasPrefix(part, "users/") && strings.HasSuffix(part, "/devices/clear") && r.Method == "POST" {
		s.clearDevices(w, r, part)
		return
	}
	if part == "upstreams/pull" && r.Method == "POST" {
		s.enqueuePull(w, r)
		return
	}
	table := ""
	switch strings.Split(part, "/")[0] {
	case "upstreams":
		table = "upstreams"
	case "clients":
		table = "clients"
	case "users":
		table = "users"
	case "nodes":
		table = "nodes"
	default:
		jsonOut(w, 404, map[string]any{"error": "not found"})
		return
	}
	switch r.Method {
	case "GET":
		s.list(w, r, table)
	case "POST":
		s.create(w, r, table)
	case "PUT":
		s.update(w, r, table)
	case "DELETE":
		s.drop(w, r, table)
	default:
		w.WriteHeader(405)
	}
}

func (s *Server) rows(w http.ResponseWriter, r *http.Request, q string) {
	rows, e := s.S.DB.QueryContext(r.Context(), q)
	if e != nil {
		jsonOut(w, 500, map[string]any{"error": "database error"})
		return
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) == nil {
			out = append(out, b)
		}
	}
	jsonOut(w, 200, map[string]any{"data": out})
}
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	out := map[string]int64{}
	for k, q := range map[string]string{"主用户": "users", "上游": "upstreams", "独立客户端": "clients", "注册节点": "nodes"} {
		var n int64
		_ = s.S.DB.QueryRowContext(r.Context(), "select count(*) from "+q).Scan(&n)
		out[k] = n
	}
	jsonOut(w, 200, map[string]any{"data": out})
}
func (s *Server) special(w http.ResponseWriter, r *http.Request, t string) {
	if r.Method == "GET" {
		s.rows(w, r, "select row_to_json(x) from (select * from "+t+" order by 1 desc limit 200)x")
		return
	}
	if r.Method == "DELETE" {
		if t == "main_user_whitelist" {
			id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
			if e != nil {
				jsonOut(w, 422, map[string]any{"error": "id required"})
				return
			}
			res, e := s.S.DB.ExecContext(r.Context(), `delete from main_user_whitelist where user_id=$1`, id)
			if e != nil {
				jsonOut(w, 409, map[string]any{"error": "delete failed"})
				return
			}
			n, _ := res.RowsAffected()
			jsonOut(w, 200, map[string]any{"deleted": n})
			return
		}
		s.drop(w, r, t)
		return
	}
	var q map[string]any
	if decode(r, &q) != nil {
		jsonOut(w, 422, map[string]any{"error": "invalid input"})
		return
	}
	var e error
	if t == "panel_cidrs" {
		_, e = s.S.DB.ExecContext(r.Context(), `insert into panel_cidrs(cidr,note) values($1,$2)`, q["cidr"], q["note"])
	} else {
		enabled, ok := q["enabled"].(bool)
		if !ok {
			enabled = true
		}
		_, e = s.S.DB.ExecContext(r.Context(), `insert into main_user_whitelist(user_id,enabled,note) values($1,$2,$3) on conflict(user_id) do update set enabled=excluded.enabled,note=excluded.note,updated_at=now()`, number(q["user_id"]), enabled, q["note"])
	}
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "save failed"})
		return
	}
	jsonOut(w, 201, map[string]any{"status": "saved"})
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s.rows(w, r, `select row_to_json(x) from (select key,value,version,updated_at from settings order by key)x`)
		return
	}
	var q struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if decode(r, &q) != nil || q.Key == "" {
		jsonOut(w, 422, map[string]any{"error": "invalid input"})
		return
	}
	_, e := s.S.DB.ExecContext(r.Context(), `insert into settings(key,value) values($1,$2) on conflict(key) do update set value=excluded.value,version=settings.version+1,updated_at=now()`, q.Key, mustJSON(q.Value))
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "save failed"})
		return
	}
	jsonOut(w, 200, map[string]any{"status": "saved"})
}
func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request, part, table string) {
	bits := strings.Split(part, "/")
	id, e := strconv.ParseInt(bits[1], 10, 64)
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "invalid node"})
		return
	}
	tok := auth.Token()
	res, e := s.S.DB.ExecContext(r.Context(), `update `+table+` set token_digest=$2,updated_at=now() where id=$1`, id, auth.Digest(tok))
	n, _ := res.RowsAffected()
	if e != nil || n != 1 {
		jsonOut(w, 404, map[string]any{"error": "not found"})
		return
	}
	jsonOut(w, 200, map[string]any{"token": tok})
}
func (s *Server) list(w http.ResponseWriter, r *http.Request, t string) {
	query := fmt.Sprintf("select row_to_json(x) from (select * from %s order by id desc limit 100) x", t)
	switch t {
	case "upstreams":
		query = `select row_to_json(x) from (select id,name,email,enabled,upload,download,transfer_enable,expires_at,last_node_count,last_pulled_at,last_error,created_at,updated_at from upstreams order by id desc limit 100) x`
	case "users":
		query = `select row_to_json(x) from (select id,email,banned,expires_at,device_limit,online_count,traffic_limit,traffic_used,created_at,updated_at from users order by id desc limit 100) x`
	case "clients":
		query = `select row_to_json(x) from (select id,name,enabled,expires_at,traffic_limit,traffic_used,device_limit,device_window,access_count,last_access_at,note,created_at,updated_at from clients order by id desc limit 100) x`
	case "nodes":
		query = `select row_to_json(x) from (select id,name,address,enabled,config,created_at,updated_at from nodes order by id desc limit 100) x`
	}
	rows, e := s.S.DB.QueryContext(r.Context(), query)
	if e != nil {
		jsonOut(w, 500, map[string]any{"error": "database error"})
		return
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		out = append(out, b)
	}
	jsonOut(w, 200, map[string]any{"data": out})
}
func (s *Server) create(w http.ResponseWriter, r *http.Request, t string) {
	var q map[string]any
	if decode(r, &q) != nil {
		jsonOut(w, 422, map[string]any{"error": "invalid input"})
		return
	}
	var id int64
	var e error
	switch t {
	case "users":
		tok := auth.Token()
		id, e = insert(r, s, "insert into users(email,device_limit,token_digest,banned,expires_at,traffic_limit,traffic_used) values($1,$2,$3,coalesce($4,false),$5::timestamptz,$6,$7) returning id", q["email"], number(q["device_limit"]), auth.Digest(tok), q["banned"], nullableString(q["expires_at"]), number(q["traffic_limit"]), number(q["traffic_used"]))
		if e == nil {
			jsonOut(w, 201, map[string]any{"id": id, "token": tok})
			return
		}
	case "nodes":
		tok := auth.Token()
		id, e = insert(r, s, "insert into nodes(name,address,token_digest) values($1,$2,$3) returning id", q["name"], q["address"], auth.Digest(tok))
		if e == nil {
			jsonOut(w, 201, map[string]any{"id": id, "token": tok})
			return
		}
	case "clients":
		tok := auth.Token()
		id, e = insert(r, s, "insert into clients(name,token_digest,enabled,expires_at,traffic_limit,traffic_used,device_limit,device_window,note) values($1,$2,coalesce($3,true),$4::timestamptz,$5,$6,$7,$8,$9) returning id", q["name"], auth.Digest(tok), q["enabled"], nullableString(q["expires_at"]), number(q["traffic_limit"]), number(q["traffic_used"]), number(q["device_limit"]), defaultNum(q["device_window"], 120), q["note"])
		if e == nil {
			jsonOut(w, 201, map[string]any{"id": id, "token": tok})
			return
		}
	case "upstreams":
		name, _ := q["name"].(string)
		raw, _ := q["subscribe_url"].(string)
		if name == "" || raw == "" {
			e = errors.New("required")
		} else {
			var next int64
			e = s.S.DB.QueryRowContext(r.Context(), `select nextval(pg_get_serial_sequence('upstreams','id'))`).Scan(&next)
			if e == nil {
				var enc, credentials []byte
				enc, e = s.Box.Seal([]byte(raw), fmt.Sprintf("upstream:%d", next))
				if credential, ok := q["credentials"].(string); e == nil && ok && credential != "" {
					credentials, e = s.Box.Seal([]byte(credential), fmt.Sprintf("upstream-credentials:%d", next))
				}
				if e == nil {
					e = s.S.DB.QueryRowContext(r.Context(), `insert into upstreams(id,name,email,subscribe_url_ciphertext,credentials_ciphertext,enabled) values($1,$2,$3,$4,$5,coalesce($6,true)) returning id`, next, name, q["email"], enc, credentials, q["enabled"]).Scan(&id)
				}
			}
		}
	}
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "create failed"})
		return
	}
	jsonOut(w, 201, map[string]any{"id": id})
}
func insert(r *http.Request, s *Server, q string, args ...any) (int64, error) {
	var id int64
	e := s.S.DB.QueryRowContext(r.Context(), q, args...).Scan(&id)
	return id, e
}
func number(v any) int64 {
	if n, ok := v.(float64); ok {
		return int64(n)
	}
	return 0
}
func defaultNum(v any, d int64) int64 {
	n := number(v)
	if n == 0 {
		return d
	}
	return n
}
func (s *Server) update(w http.ResponseWriter, r *http.Request, t string) {
	id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "id required"})
		return
	}
	var q map[string]any
	if decode(r, &q) != nil {
		jsonOut(w, 422, map[string]any{"error": "invalid input"})
		return
	}
	var res sql.Result
	switch t {
	case "upstreams":
		var enc, credentials any
		if raw, ok := q["subscribe_url"].(string); ok && raw != "" {
			enc, e = s.Box.Seal([]byte(raw), fmt.Sprintf("upstream:%d", id))
		}
		if credential, ok := q["credentials"].(string); e == nil && ok && credential != "" {
			credentials, e = s.Box.Seal([]byte(credential), fmt.Sprintf("upstream-credentials:%d", id))
		}
		if e == nil {
			res, e = s.S.DB.ExecContext(r.Context(), "update upstreams set name=coalesce($2,name),email=coalesce($3,email),enabled=coalesce($4,enabled),subscribe_url_ciphertext=coalesce($5,subscribe_url_ciphertext),credentials_ciphertext=coalesce($6,credentials_ciphertext),updated_at=now() where id=$1", id, q["name"], q["email"], q["enabled"], enc, credentials)
		}
	case "users":
		res, e = s.S.DB.ExecContext(r.Context(), "update users set email=coalesce($2,email),device_limit=coalesce($3,device_limit),banned=coalesce($4,banned),expires_at=coalesce($5::timestamptz,expires_at),traffic_limit=coalesce($6,traffic_limit),traffic_used=coalesce($7,traffic_used),updated_at=now() where id=$1", id, q["email"], nullableNum(q["device_limit"]), q["banned"], nullableString(q["expires_at"]), nullableNum(q["traffic_limit"]), nullableNum(q["traffic_used"]))
	case "clients":
		res, e = s.S.DB.ExecContext(r.Context(), "update clients set name=coalesce($2,name),enabled=coalesce($3,enabled),note=coalesce($4,note),expires_at=coalesce($5::timestamptz,expires_at),traffic_limit=coalesce($6,traffic_limit),traffic_used=coalesce($7,traffic_used),device_limit=coalesce($8,device_limit),device_window=coalesce($9,device_window),updated_at=now() where id=$1", id, q["name"], q["enabled"], q["note"], nullableString(q["expires_at"]), nullableNum(q["traffic_limit"]), nullableNum(q["traffic_used"]), nullableNum(q["device_limit"]), nullableNum(q["device_window"]))
	case "nodes":
		res, e = s.S.DB.ExecContext(r.Context(), "update nodes set name=coalesce($2,name),address=coalesce($3,address),enabled=coalesce($4,enabled),updated_at=now() where id=$1", id, q["name"], q["address"], q["enabled"])
	default:
		e = errors.New("unsupported")
	}
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "update failed"})
		return
	}
	n, _ := res.RowsAffected()
	jsonOut(w, 200, map[string]any{"updated": n})
}
func nullableNum(v any) any {
	if v == nil {
		return nil
	}
	return number(v)
}
func nullableString(v any) any {
	if v == nil {
		return nil
	}
	x, ok := v.(string)
	if !ok || strings.TrimSpace(x) == "" {
		return nil
	}
	return x
}
func (s *Server) drop(w http.ResponseWriter, r *http.Request, t string) {
	id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "id required"})
		return
	}
	res, e := s.S.DB.ExecContext(r.Context(), fmt.Sprintf("delete from %s where id=$1", t), id)
	if e != nil {
		jsonOut(w, 409, map[string]any{"error": "delete failed"})
		return
	}
	n, _ := res.RowsAffected()
	jsonOut(w, 200, map[string]any{"deleted": n})
}
func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	if len(raw) < 20 {
		w.Header().Set("Cache-Control", "no-store")
		jsonOut(w, 404, map[string]any{"error": "not found"})
		return
	}
	var id int64
	var used, limit int64
	var deviceLimit, deviceWindow int
	var enabled bool
	var exp sql.NullTime
	e := s.S.DB.QueryRowContext(r.Context(), `select id,enabled,expires_at,traffic_used,traffic_limit,device_limit,device_window from clients where token_digest=$1`, auth.Digest(raw)).Scan(&id, &enabled, &exp, &used, &limit, &deviceLimit, &deviceWindow)
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		s.access(r, 0, 0, "blocked", "not_found")
		jsonOut(w, 404, map[string]any{"error": "not found"})
		return
	}
	if !enabled || (exp.Valid && exp.Time.Before(time.Now())) || (limit > 0 && used >= limit) {
		s.access(r, 0, id, "blocked", "unavailable")
		jsonOut(w, 403, map[string]any{"error": "subscription unavailable"})
		return
	}
	if ok := s.clientOwner(r, id, deviceLimit, deviceWindow); !ok {
		s.access(r, 0, id, "blocked", "device_limit")
		jsonOut(w, 403, map[string]any{"error": "device limit exceeded"})
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		ua := strings.ToLower(r.UserAgent())
		if strings.Contains(ua, "clash") || strings.Contains(ua, "sing-box") {
			format = "raw"
		} else {
			format = "base64"
		}
	}
	if format != "raw" && format != "base64" && format != "clash" {
		s.access(r, 0, id, "blocked", "invalid_format")
		jsonOut(w, 422, map[string]any{"error": "unsupported format"})
		return
	}
	rows, _ := s.S.DB.QueryContext(r.Context(), `select coalesce(raw_uri,type||'://' || address || coalesce(':'||port::text,'')) from upstream_nodes where enabled order by id`)
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var x string
		rows.Scan(&x)
		lines = append(lines, x)
	}
	body := strings.Join(lines, "\n")
	if format == "base64" {
		body = base64.StdEncoding.EncodeToString([]byte(body))
	} else if format == "clash" {
		body = "proxies:\n"
		for i, line := range lines {
			body += fmt.Sprintf("  - {name: node-%d, type: socks5, server: 127.0.0.1, port: %d} # %s\n", i+1, 1080+i, strings.ReplaceAll(line, "\n", ""))
		}
		body += "proxy-groups:\n  - {name: Flyboard, type: select, proxies: ["
		for i := range lines {
			if i > 0 {
				body += ", "
			}
			body += fmt.Sprintf("node-%d", i+1)
		}
		body += "]}\nrules:\n  - MATCH,Flyboard\n"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=%d; total=%d; expire=0", used, limit))
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Profile-Title", base64.StdEncoding.EncodeToString([]byte("Flyboard")))
	w.Write([]byte(body))
	s.S.DB.ExecContext(r.Context(), "update clients set access_count=access_count+1,last_access_at=now() where id=$1", id)
	s.access(r, 0, id, "allowed", "")
}

func (s *Server) access(r *http.Request, userID, clientID int64, action, reason string) {
	var u, c any
	if userID > 0 {
		u = userID
	}
	if clientID > 0 {
		c = clientID
	}
	ip := clientIP(r)
	fp := auth.Digest(ip + "\x00" + r.UserAgent())
	_, _ = s.S.DB.ExecContext(r.Context(), `insert into access_logs(user_id,client_id,ip,user_agent,fingerprint,action,reason,path) values($1,$2,$3,$4,$5,$6,$7,$8)`, u, c, ip, r.UserAgent(), fp, action, reason, r.URL.Path)
}

func (s *Server) clientOwner(r *http.Request, id int64, limit, window int) bool {
	if limit == 0 {
		return true
	}
	ip := clientIP(r)
	fp := fmt.Sprintf("%x", auth.Digest(ip+"\x00"+r.UserAgent()))
	tx, e := s.S.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return false
	}
	defer tx.Rollback()
	tx.ExecContext(r.Context(), `select pg_advisory_xact_lock($1)`, id+900000000)
	tx.ExecContext(r.Context(), `delete from client_owner_locks where client_id=$1 and seen_at < now()-($2*interval '1 second')`, id, window)
	var n int
	tx.QueryRowContext(r.Context(), `select count(*) from client_owner_locks where client_id=$1`, id).Scan(&n)
	var exists bool
	tx.QueryRowContext(r.Context(), `select exists(select 1 from client_owner_locks where client_id=$1 and fingerprint=$2)`, id, fp).Scan(&exists)
	if !exists && n >= limit {
		return false
	}
	_, e = tx.ExecContext(r.Context(), `insert into client_owner_locks(client_id,fingerprint,ip,user_agent,seen_at) values($1,$2,$3,$4,now()) on conflict(client_id,fingerprint) do update set seen_at=now(),ip=excluded.ip`, id, fp, ip, r.UserAgent())
	return e == nil && tx.Commit() == nil
}
func clientIP(r *http.Request) string {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return h
	}
	return r.RemoteAddr
}

// managementIPAllowed trusts forwarding headers only when the direct peer is
// in a configured proxy CIDR. panel_cidrs is also the optional allow-list;
// an empty table intentionally means unrestricted for first-run recovery.
func (s *Server) managementIPAllowed(r *http.Request) bool {
	direct := clientIP(r)
	var trusted bool
	_ = s.S.DB.QueryRowContext(r.Context(), `select exists(select 1 from panel_cidrs where $1::inet <<= cidr)`, direct).Scan(&trusted)
	ip := direct
	if trusted {
		if first := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(first) != nil {
			ip = first
		}
	}
	var count int
	if s.S.DB.QueryRowContext(r.Context(), `select count(*) from panel_cidrs`).Scan(&count) != nil || count == 0 {
		return true
	}
	var allowed bool
	_ = s.S.DB.QueryRowContext(r.Context(), `select exists(select 1 from panel_cidrs where $1::inet <<= cidr)`, ip).Scan(&allowed)
	return allowed
}
func (s *Server) qr(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	var yes bool
	s.S.DB.QueryRowContext(r.Context(), `select exists(select 1 from clients where token_digest=$1 and enabled and (expires_at is null or expires_at>now()) and (traffic_limit=0 or traffic_used<traffic_limit))`, auth.Digest(raw)).Scan(&yes)
	w.Header().Set("Cache-Control", "no-store")
	if !yes {
		http.NotFound(w, r)
		return
	}
	u := html.EscapeString("/flyboard/sub/" + raw)
	w.Header().Set("Content-Type", "image/svg+xml")
	b, e := qrcode.New(u, qrcode.Medium)
	if e != nil {
		http.Error(w, "qr unavailable", 500)
		return
	}
	b.DisableBorder = false
	png, _ := b.PNG(256)
	// SVG embeds a standards-generated QR bitmap; scanners consume the image,
	// while desc retains the exact relative subscription URL.
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="256" height="256"><desc>%s</desc><image width="256" height="256" href="data:image/png;base64,%s"/></svg>`, u, base64.StdEncoding.EncodeToString(png))
}

func (s *Server) enqueuePull(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UpstreamID int64 `json:"upstream_id"`
	}
	if decode(r, &q) != nil || q.UpstreamID < 1 {
		jsonOut(w, 422, map[string]any{"error": "upstream_id required"})
		return
	}
	id, dup, e := pull.Enqueue(r.Context(), s.S.DB, q.UpstreamID)
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "enqueue failed"})
		return
	}
	jsonOut(w, 202, map[string]any{"execution_id": id, "status": "pending", "duplicate": dup})
}

func (s *Server) mainSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	raw := r.PathValue("token")
	var id int64
	var limit int
	var used, quota int64
	var banned bool
	var exp sql.NullTime
	if s.S.DB.QueryRowContext(r.Context(), `select id,device_limit,banned,expires_at,traffic_used,traffic_limit from users where token_digest=$1`, auth.Digest(raw)).Scan(&id, &limit, &banned, &exp, &used, &quota) != nil {
		s.access(r, 0, 0, "blocked", "not_found")
		jsonOut(w, 404, map[string]any{"error": "not found"})
		return
	}
	if banned || (exp.Valid && exp.Time.Before(time.Now())) || (quota > 0 && used >= quota) {
		s.access(r, id, 0, "blocked", "unavailable")
		jsonOut(w, 403, map[string]any{"error": "subscription unavailable"})
		return
	}
	var exempt bool
	_ = s.S.DB.QueryRowContext(r.Context(), `select exists(select 1 from main_user_whitelist where user_id=$1 and enabled)`, id).Scan(&exempt)
	ok, e := true, error(nil)
	if !exempt {
		ok, e = s.Owners.Acquire(r.Context(), id, clientIP(r), r.UserAgent(), limit)
	}
	if e != nil {
		jsonOut(w, 503, map[string]any{"error": "owner lock unavailable"})
		return
	}
	if !ok {
		s.access(r, id, 0, "blocked", "device_limit")
		w.Header().Set("Retry-After", "120")
		jsonOut(w, 429, map[string]any{"error": "device limit exceeded"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", used, quota, expiryUnix(exp)))
	rows, err := s.S.DB.QueryContext(r.Context(), `select coalesce(raw_uri,type||'://' || address || coalesce(':'||port::text,'')) from upstream_nodes where enabled order by id`)
	if err != nil {
		jsonOut(w, 503, map[string]any{"error": "subscription unavailable"})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			io.WriteString(w, line+"\n")
		}
	}
	s.access(r, id, 0, "allowed", "")
}

func expiryUnix(t sql.NullTime) int64 {
	if t.Valid {
		return t.Time.Unix()
	}
	return 0
}

func (s *Server) clearDevices(w http.ResponseWriter, r *http.Request, part string) {
	bits := strings.Split(part, "/")
	if len(bits) != 4 {
		jsonOut(w, 404, map[string]any{"error": "not found"})
		return
	}
	u, e := strconv.ParseInt(bits[1], 10, 64)
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "invalid user"})
		return
	}
	if e = s.Control.Devices.Clear(r.Context(), u); e != nil {
		jsonOut(w, 503, map[string]any{"error": "clear failed"})
		return
	}
	tx, e := s.S.DB.BeginTx(r.Context(), nil)
	if e != nil {
		jsonOut(w, 503, map[string]any{"error": "clear failed"})
		return
	}
	defer tx.Rollback()
	var rev int64
	e = tx.QueryRowContext(r.Context(), `select (extract(epoch from clock_timestamp())*1000000)::bigint`).Scan(&rev)
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `delete from device_display where user_id=$1`, u)
	}
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `update users set online_count=0 where id=$1`, u)
	}
	a, _ := r.Context().Value(adminKey).(store.Admin)
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `insert into device_audit(user_id,action,reason,user_agent) values($1,'cleared','administrator',$2)`, u, fmt.Sprintf("admin:%d", a.ID))
	}
	empty := devices.Snapshot{UserID: u, Devices: []devices.Device{}, OnlineCount: 0}
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `insert into sync_outbox(aggregate_type,aggregate_id,revision,payload) values('devices',$1,$2,$3)`, u, rev, mustJSON(empty))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		jsonOut(w, 503, map[string]any{"error": "clear failed"})
		return
	}
	s.Control.Broadcast(control.Envelope{Version: control.Version, Type: "sync.devices", Revision: rev, Data: empty})
	jsonOut(w, 200, map[string]any{"data": empty, "revision": rev})
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Server) dispatchOutbox() {
	backoff := time.Second
	for {
		n, e := s.Control.Dispatch(context.Background(), 100)
		if e != nil {
			s.Log.Error("outbox dispatch", "error", e)
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if n == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
}
