// Package control implements Flyboard's versioned node control plane.
package control

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"flyboard/internal/auth"
	"flyboard/internal/devices"
	"github.com/coder/websocket"
)

const Version = "1"

type Envelope struct {
	Version   string `json:"version"`
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Data      any    `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
}
type Hub struct {
	DB      *sql.DB
	Devices devices.Service
	mu      sync.Mutex
	nodes   map[int64]*client
}
type client struct {
	c    *websocket.Conn
	q    chan Envelope
	done chan struct{}
}

func New(db *sql.DB, d devices.Service) *Hub {
	return &Hub{DB: db, Devices: d, nodes: map[int64]*client{}}
}
func sign(token, ts, method, path string, body []byte) string {
	h := hmac.New(sha256.New, []byte(token))
	fmt.Fprintf(h, "%s\n%s\n%s\n", ts, method, path)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
func (h *Hub) authenticate(r *http.Request, body []byte) (int64, error) {
	id, e := strconv.ParseInt(r.Header.Get("X-Flyboard-Node"), 10, 64)
	if e != nil {
		return 0, e
	}
	ts := r.Header.Get("X-Flyboard-Timestamp")
	n, e := strconv.ParseInt(ts, 10, 64)
	if e != nil || time.Since(time.Unix(n, 0)) > 10*time.Second || time.Until(time.Unix(n, 0)) > 10*time.Second {
		return 0, errors.New("expired authentication")
	}
	tok := r.Header.Get("X-Flyboard-Token")
	var digest []byte
	e = h.DB.QueryRowContext(r.Context(), `select token_digest from nodes where id=$1 and enabled`, id).Scan(&digest)
	if e != nil || !hmac.Equal(digest, auth.Digest(tok)) || !hmac.Equal([]byte(r.Header.Get("X-Flyboard-Signature")), []byte(sign(tok, ts, r.Method, r.URL.Path, body))) {
		return 0, errors.New("authentication failed")
	}
	return id, nil
}
func (h *Hub) Snapshot(ctx context.Context, user int64) (devices.Snapshot, error) {
	return h.Devices.Store.Snapshot(ctx, user, h.Devices.Window, time.Now())
}
func (h *Hub) DevicesHTTP(w http.ResponseWriter, r *http.Request) {
	if _, e := h.authenticate(r, nil); e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	u, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	s, e := h.Snapshot(r.Context(), u)
	if e != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"version": Version, "devices": s, "users": s, "data": s})
}
func (h *Hub) WS(w http.ResponseWriter, r *http.Request) {
	// Node agents are non-browser clients; reject browser-originated sockets.
	if r.Header.Get("Origin") != "" {
		http.Error(w, "origin forbidden", http.StatusForbidden)
		return
	}
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, b, e := c.Read(ctx)
	if e != nil {
		c.Close(websocket.StatusPolicyViolation, "authentication timeout")
		return
	}
	var a struct {
		NodeID                      int64 `json:"node_id"`
		Token, Timestamp, Signature string
	}
	if json.Unmarshal(b, &a) != nil {
		c.Close(websocket.StatusPolicyViolation, "bad auth")
		return
	}
	rr := r.Clone(r.Context())
	rr.Method = "WS"
	rr.Header.Set("X-Flyboard-Node", fmt.Sprint(a.NodeID))
	rr.Header.Set("X-Flyboard-Token", a.Token)
	rr.Header.Set("X-Flyboard-Timestamp", a.Timestamp)
	rr.Header.Set("X-Flyboard-Signature", a.Signature)
	rr.URL.Path = "/api/v1/node/ws"
	id, e := h.authenticate(rr, nil)
	if e != nil {
		c.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}
	cl := &client{c: c, q: make(chan Envelope, 64), done: make(chan struct{})}
	h.mu.Lock()
	if old := h.nodes[id]; old != nil {
		old.c.Close(websocket.StatusNormalClosure, "replaced")
	}
	h.nodes[id] = cl
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.nodes[id] == cl {
			delete(h.nodes, id)
		}
		h.mu.Unlock()
		close(cl.done)
		c.CloseNow()
	}()
	go h.writer(r.Context(), cl)
	h.send(cl, Envelope{Version: Version, Type: "auth.success"})
	h.fullSync(r.Context(), id, cl)
	for {
		_, b, e = c.Read(r.Context())
		if e != nil {
			return
		}
		var m Envelope
		if json.Unmarshal(b, &m) != nil || m.Version != Version {
			h.send(cl, Envelope{Version: Version, Type: "error", Error: "unsupported message"})
			continue
		}
		switch m.Type {
		case "pong", "node.status":
		case "request.devices":
			h.fullSync(r.Context(), id, cl)
		case "report.devices":
			var q devices.Report
			x, _ := json.Marshal(m.Data)
			if json.Unmarshal(x, &q) != nil {
				continue
			}
			q.NodeID = fmt.Sprint(id)
			s, e := h.Devices.Report(r.Context(), q)
			out := Envelope{Version: Version, Type: "report.devices.result", RequestID: m.RequestID, Data: s}
			if e != nil {
				out.Error = e.Error()
			}
			h.send(cl, out)
			if e == nil {
				h.Broadcast(Envelope{Version: Version, Type: "sync.devices", Data: s})
			}
		}
	}
}
func (h *Hub) writer(ctx context.Context, c *client) {
	t := time.NewTicker(55 * time.Second)
	defer t.Stop()
	for {
		select {
		case m := <-c.q:
			x, _ := json.Marshal(m)
			if e := c.c.Write(ctx, websocket.MessageText, x); e != nil {
				return
			}
		case <-t.C:
			h.send(c, Envelope{Version: Version, Type: "ping"})
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}
func (h *Hub) send(c *client, m Envelope) bool {
	select {
	case c.q <- m:
		return true
	default:
		c.c.Close(websocket.StatusPolicyViolation, "slow consumer")
		return false
	}
}
func (h *Hub) Broadcast(m Envelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.nodes {
		h.send(c, m)
	}
}
func (h *Hub) fullSync(ctx context.Context, id int64, c *client) {
	var cfg json.RawMessage
	_ = h.DB.QueryRowContext(ctx, `select config from nodes where id=$1`, id).Scan(&cfg)
	h.send(c, Envelope{Version: Version, Type: "sync.config", Data: cfg})
	rows, e := h.DB.QueryContext(ctx, `select id,device_limit,banned,expires_at from users order by id`)
	var users []map[string]any
	if e == nil {
		defer rows.Close()
		for rows.Next() {
			var id, lim int64
			var ban bool
			var exp sql.NullTime
			_ = rows.Scan(&id, &lim, &ban, &exp)
			users = append(users, map[string]any{"id": id, "device_limit": lim, "banned": ban, "expires_at": exp})
		}
	}
	h.send(c, Envelope{Version: Version, Type: "sync.users", Data: users})
	for _, u := range users {
		s, _ := h.Snapshot(ctx, u["id"].(int64))
		h.send(c, Envelope{Version: Version, Type: "sync.devices", Data: s})
	}
}
