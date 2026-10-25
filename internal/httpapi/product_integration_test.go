package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"flyboard/internal/auth"
	"flyboard/internal/config"
	"flyboard/internal/migrations"
	"flyboard/internal/store"
	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
)

// TestProductAPIRealServices is a black-box product/control-plane acceptance test.
// It is intentionally gated so ordinary unit runs need no services; compose.test.yaml enables it.
func TestProductAPIRealServices(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	vr := os.Getenv("TEST_VALKEY_URL")
	if dsn == "" || vr == "" {
		t.Skip("real PostgreSQL and Valkey not configured")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = migrations.Up(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	// Assert every shipped migration actually ran.
	vers, err := migrations.Status(ctx, s.DB)
	if err != nil || len(vers) != 4 {
		t.Fatalf("migrations=%v err=%v", vers, err)
	}
	op, _ := redis.ParseURL(vr)
	rv := redis.NewClient(op)
	defer rv.Close()
	if err = rv.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	rv.FlushDB(ctx)
	key := bytes.Repeat([]byte{0x5a}, 32)
	keyfile := t.TempDir() + "/master"
	if err = os.WriteFile(keyfile, []byte(base64.StdEncoding.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	c := config.Config{DatabaseURL: dsn, ValkeyURL: vr, AdminPath: "acceptance-secret-path", CookieSecure: "false", MasterKeyFile: keyfile, SessionTTL: time.Hour, LoginRate: 20, SubscriptionRate: 100, NodeRate: 100, AdminRate: 100, RateWindow: time.Minute}
	bootstrap := "bootstrap-secret-value"
	_, err = s.DB.Exec(`insert into bootstrap_tokens(digest) values($1)`, auth.Digest(bootstrap))
	if err != nil {
		t.Fatal(err)
	}
	h := New(c, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(h)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	base := "/api/v2/" + c.AdminPath + "/flyboard/"
	call := func(method, path string, body any, csrf, origin string) (int, http.Header, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, e := hc.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, b
	}
	status, _, _ := call("POST", "/bootstrap", map[string]any{"token": bootstrap, "email": "owner@example.test", "password": "correct horse battery"}, "", "")
	if status != 201 {
		t.Fatalf("bootstrap=%d", status)
	}
	status, _, _ = call("POST", "/bootstrap", map[string]any{"token": bootstrap, "email": "other@example.test", "password": "correct horse battery"}, "", "")
	if status != 403 {
		t.Fatalf("second bootstrap=%d", status)
	}
	status, _, b := call("POST", "/login", map[string]any{"email": "owner@example.test", "password": "correct horse battery"}, "", "")
	if status != 200 {
		t.Fatalf("login=%d %s", status, b)
	}
	var lr struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(b, &lr)
	if lr.CSRF == "" {
		t.Fatal("missing csrf")
	}
	// Role boundaries: auditor is read-only; operator cannot mutate owner-only node policy.
	for _, role := range []string{"auditor", "operator"} {
		_, err = s.DB.Exec(`insert into admins(email,password_hash,role) values($1,$2,$3)`, role+"@example.test", auth.Password("role password long"), role)
		if err != nil {
			t.Fatal(err)
		}
		rjar, _ := cookiejar.New(nil)
		rc := &http.Client{Jar: rjar}
		payload, _ := json.Marshal(map[string]any{"email": role + "@example.test", "password": "role password long"})
		rq, _ := http.NewRequest("POST", ts.URL+"/login", bytes.NewReader(payload))
		rq.Header.Set("Content-Type", "application/json")
		rp, e := rc.Do(rq)
		if e != nil {
			t.Fatal(e)
		}
		rb, _ := io.ReadAll(rp.Body)
		rp.Body.Close()
		var rl struct {
			CSRF string `json:"csrf_token"`
		}
		json.Unmarshal(rb, &rl)
		path := base + "users"
		if role == "operator" {
			path = base + "nodes"
		}
		rq, _ = http.NewRequest("POST", ts.URL+path, strings.NewReader(`{}`))
		rq.Header.Set("Content-Type", "application/json")
		rq.Header.Set("X-CSRF-Token", rl.CSRF)
		rq.Header.Set("Origin", ts.URL)
		rp, e = rc.Do(rq)
		if e != nil {
			t.Fatal(e)
		}
		rp.Body.Close()
		if rp.StatusCode != 403 {
			t.Fatalf("%s role=%d", role, rp.StatusCode)
		}
	}
	status, _, _ = call("POST", base+"users", map[string]any{"email": "x@example.test", "device_limit": 1}, "", ts.URL)
	if status != 403 {
		t.Fatalf("missing csrf=%d", status)
	}
	status, _, _ = call("POST", base+"users", map[string]any{"email": "x@example.test", "device_limit": 1}, lr.CSRF, "http://evil.test")
	if status != 403 {
		t.Fatalf("bad origin=%d", status)
	}
	create := func(kind string, v any) (int64, string) {
		status, _, b := call("POST", base+kind, v, lr.CSRF, ts.URL)
		if status != 201 {
			t.Fatalf("create %s=%d %s", kind, status, b)
		}
		var x struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		}
		json.Unmarshal(b, &x)
		if x.ID == 0 {
			t.Fatalf("create %s no id", kind)
		}
		return x.ID, x.Token
	}
	uid, utok := create("users", map[string]any{"email": "user@example.test", "device_limit": 1, "traffic_limit": 1000})
	cid, ctok := create("clients", map[string]any{"name": "client", "device_limit": 1, "traffic_limit": 1000})
	nid, ntok := create("nodes", map[string]any{"name": "node", "address": "127.0.0.1"})
	upID, _ := create("upstreams", map[string]any{"name": "up", "subscribe_url": "https://example.test/private", "credentials": "bearer-secret"})
	for kind, id := range map[string]int64{"users": uid, "clients": cid, "nodes": nid, "upstreams": upID} {
		status, _, b = call("PUT", fmt.Sprintf("%s%s?id=%d", base, kind, id), map[string]any{"name": "updated", "note": "updated"}, lr.CSRF, ts.URL)
		if status != 200 {
			t.Fatalf("update %s=%d %s", kind, status, b)
		}
	}
	// API listings must never disclose token digests or encrypted credentials.
	for _, kind := range []string{"users", "clients", "nodes", "upstreams"} {
		status, _, b = call("GET", base+kind, nil, "", "")
		if status != 200 || bytes.Contains(b, []byte("token_digest")) || bytes.Contains(b, []byte("ciphertext")) || bytes.Contains(b, []byte("bearer-secret")) {
			t.Fatalf("unsafe list %s: %d %s", kind, status, b)
		}
	}
	var td, enc []byte
	if err = s.DB.QueryRow(`select token_digest from users where id=$1`, uid).Scan(&td); err != nil || bytes.Equal(td, []byte(utok)) {
		t.Fatal("user token not digest-only")
	}
	if err = s.DB.QueryRow(`select subscribe_url_ciphertext from upstreams where id=$1`, upID).Scan(&enc); err != nil || bytes.Contains(enc, []byte("example.test")) {
		t.Fatal("upstream URL not encrypted")
	}
	_, err = s.DB.Exec(`insert into upstream_nodes(upstream_id,name,type,address,port,raw_uri) values($1,'fixture','ss','198.51.100.1',443,'ss://fixture')`, upID)
	if err != nil {
		t.Fatal(err)
	}
	// Pull enqueue is asynchronous and unique while active; fixture rows avoid weakening production SSRF policy.
	status, _, b = call("POST", base+"upstreams/pull", map[string]any{"upstream_id": upID}, lr.CSRF, ts.URL)
	if status != 202 {
		t.Fatal(status, string(b))
	}
	status, _, b = call("POST", base+"upstreams/pull", map[string]any{"upstream_id": upID}, lr.CSRF, ts.URL)
	if status != 202 || !bytes.Contains(b, []byte(`"duplicate":true`)) {
		t.Fatalf("pull dedupe %d %s", status, b)
	}
	// Raw/base64/Clash/QR plus response metadata and access logs.
	for _, f := range []string{"raw", "base64", "clash"} {
		status, hdr, b := call("GET", fmt.Sprintf("/flyboard/sub/%s?format=%s", ctok, f), nil, "", "")
		if status != 200 || hdr.Get("Subscription-Userinfo") == "" || hdr.Get("Cache-Control") != "no-store" {
			t.Fatalf("sub %s=%d", f, status)
		}
		if f == "base64" {
			if _, e := base64.StdEncoding.DecodeString(string(b)); e != nil {
				t.Fatal(e)
			}
		}
	}
	status, hdr, b := call("GET", "/flyboard/sub/"+ctok+"/qr", nil, "", "")
	if status != 200 || hdr.Get("Content-Type") != "image/svg+xml" || !bytes.Contains(b, []byte("<svg")) {
		t.Fatalf("qr %d", status)
	}
	// Client second fingerprint is forbidden; main-user Owner Lock uses 429, then whitelist bypasses.
	req, _ := http.NewRequest("GET", ts.URL+"/flyboard/sub/"+ctok+"?format=raw", nil)
	req.Header.Set("User-Agent", "second")
	resp, _ := hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("client second=%d", resp.StatusCode)
	}
	status, _, _ = call("GET", "/s/"+utok, nil, "", "")
	if status != 200 {
		t.Fatalf("owner first=%d", status)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/s/"+utok, nil)
	req.Header.Set("User-Agent", "second")
	resp, _ = hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("owner second=%d", resp.StatusCode)
	}
	status, _, _ = call("POST", base+"whitelist", map[string]any{"user_id": uid, "note": "test"}, lr.CSRF, ts.URL)
	if status != 201 {
		t.Fatal(status)
	}
	resp, _ = hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("whitelist=%d", resp.StatusCode)
	}
	var logs int
	s.DB.QueryRow(`select count(*) from access_logs`).Scan(&logs)
	if logs < 5 {
		t.Fatalf("access logs=%d", logs)
	}
	// Quota/expiry are fail closed.
	s.DB.Exec(`update clients set traffic_used=traffic_limit where id=$1`, cid)
	status, _, _ = call("GET", "/flyboard/sub/"+ctok, nil, "", "")
	if status != 403 {
		t.Fatalf("quota=%d", status)
	}
	s.DB.Exec(`update users set expires_at=now()-interval '1 minute' where id=$1`, uid)
	status, _, _ = call("GET", "/s/"+utok, nil, "", "")
	if status != 403 {
		t.Fatalf("expiry=%d", status)
	}
	s.DB.Exec(`update users set expires_at=null where id=$1`, uid)
	// Node HTTP auth and rotation invalidate the old credential.
	signed := func(tok string) int {
		p := "/api/v1/node/devices?user_id=" + fmt.Sprint(uid)
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		stamp := fmt.Sprint(time.Now().Unix())
		mac := hmac.New(sha256.New, []byte(tok))
		fmt.Fprintf(mac, "%s\nGET\n/api/v1/node/devices\n", stamp)
		req.Header.Set("X-Flyboard-Node", fmt.Sprint(nid))
		req.Header.Set("X-Flyboard-Token", tok)
		req.Header.Set("X-Flyboard-Timestamp", stamp)
		req.Header.Set("X-Flyboard-Signature", hex.EncodeToString(mac.Sum(nil)))
		r, e := hc.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if signed(ntok) != 200 {
		t.Fatal("node HTTP auth")
	}
	status, _, b = call("POST", fmt.Sprintf("%snodes/%d/rotate-token", base, nid), map[string]any{}, lr.CSRF, ts.URL)
	if status != 200 {
		t.Fatal(status)
	}
	var rot struct {
		Token string `json:"token"`
	}
	json.Unmarshal(b, &rot)
	if signed(ntok) != 401 || signed(rot.Token) != 200 {
		t.Fatal("node rotation")
	}
	// WebSocket authentication uses the same rotated token and signature.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/node/ws"
	wc, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	stamp := fmt.Sprint(time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(rot.Token))
	fmt.Fprintf(mac, "%s\nWS\n/api/v1/node/ws\n", stamp)
	wc.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"node_id":%d,"Token":%q,"Timestamp":%q,"Signature":%q}`, nid, rot.Token, stamp, hex.EncodeToString(mac.Sum(nil)))))
	_, msg, err := wc.Read(ctx)
	wc.Close(websocket.StatusNormalClosure, "")
	if err != nil || !bytes.Contains(msg, []byte("auth.success")) {
		t.Fatalf("ws auth %v %s", err, msg)
	}
	// Device clear creates durable audit/outbox state (dispatcher may deliver immediately).
	status, _, _ = call("POST", fmt.Sprintf("%susers/%d/devices/clear", base, uid), map[string]any{}, lr.CSRF, ts.URL)
	if status != 200 {
		t.Fatalf("clear=%d", status)
	}
	var audits, outbox int
	s.DB.QueryRow(`select count(*) from device_audit where user_id=$1 and action='cleared'`, uid).Scan(&audits)
	s.DB.QueryRow(`select count(*) from sync_outbox where aggregate_id=$1 and aggregate_type='devices'`, uid).Scan(&outbox)
	if audits != 1 || outbox != 1 {
		t.Fatalf("clear audit=%d outbox=%d", audits, outbox)
	}
	// Trusted proxy/CIDR allow-list: peer is trusted, forwarded outside address denied.
	status, _, _ = call("POST", base+"cidrs", map[string]any{"cidr": "127.0.0.0/8", "note": "proxy"}, lr.CSRF, ts.URL)
	if status != 201 {
		t.Fatal(status)
	}
	req, _ = http.NewRequest("GET", ts.URL+base+"overview", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, _ = hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("trusted proxy cidr=%d", resp.StatusCode)
	}
	// Complete CRUD coverage after dependent checks.
	for _, x := range []struct {
		kind string
		id   int64
	}{{"nodes", nid}, {"clients", cid}, {"users", uid}, {"upstreams", upID}} {
		status, _, b = call("DELETE", fmt.Sprintf("%s%s?id=%d", base, x.kind, x.id), nil, lr.CSRF, ts.URL)
		// CIDR policy now denies the httptest peer if represented differently; direct peer is loopback and remains allowed.
		if status != 200 {
			t.Fatalf("delete %s=%d %s", x.kind, status, b)
		}
	}
	// Management shared limiter is active.
	rv.FlushDB(ctx)
	c2 := c
	c2.AdminRate = 1
	ts2 := httptest.NewServer(New(c2, s, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer ts2.Close()
	u, _ := url.Parse(ts2.URL)
	for _, ck := range jar.Cookies(u) {
		jar.SetCookies(u, []*http.Cookie{ck})
	} // cookie domain differs; direct rate check still precedes auth
	r1, _ := http.Get(ts2.URL + base + "overview")
	r1.Body.Close()
	r2, _ := http.Get(ts2.URL + base + "overview")
	r2.Body.Close()
	if r2.StatusCode != 429 {
		t.Fatalf("admin rate=%d", r2.StatusCode)
	}
}
