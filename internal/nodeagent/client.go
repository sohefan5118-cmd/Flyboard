package nodeagent

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"flyboard/internal/devices"
	"github.com/coder/websocket"
)

type diskState struct {
	Revisions map[string]int64     `json:"revisions"`
	Verified  map[string]time.Time `json:"verified"`
}
type Durable struct {
	mu    sync.Mutex
	path  string
	state diskState
	cache *Cache
}

func OpenDurable(path string, grace time.Duration) (*Durable, error) {
	d := &Durable{path: path, cache: New(grace), state: diskState{Revisions: map[string]int64{}, Verified: map[string]time.Time{}}}
	b, e := os.ReadFile(path)
	if e == nil {
		if e = json.Unmarshal(b, &d.state); e != nil {
			return nil, e
		}
		d.cache.verified = d.state.Verified
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	return d, nil
}
func (d *Durable) persist() error {
	if e := os.MkdirAll(filepath.Dir(d.path), 0700); e != nil {
		return e
	}
	b, e := json.Marshal(d.state)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(d.path), ".agent-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if x := f.Close(); e == nil {
		e = x
	}
	if e == nil {
		e = os.Rename(name, d.path)
	}
	return e
}
func (d *Durable) ApplySnapshot(s devices.Snapshot, revision int64, lease time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := fmt.Sprintf("devices:%d", s.UserID)
	if revision > 0 && revision <= d.state.Revisions[key] {
		return nil
	}
	now := time.Now()
	prefix := strconv.FormatInt(s.UserID, 10) + "\x00"
	for k := range d.state.Verified {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(d.state.Verified, k)
		}
	}
	for _, x := range s.Devices {
		d.state.Verified[prefix+x.IP] = now.Add(lease)
	}
	if revision > 0 {
		d.state.Revisions[key] = revision
	}
	d.cache.mu.Lock()
	d.cache.verified = clone(d.state.Verified)
	d.cache.mu.Unlock()
	return d.persist()
}
func clone(x map[string]time.Time) map[string]time.Time {
	y := make(map[string]time.Time, len(x))
	for k, v := range x {
		y[k] = v
	}
	return y
}
func (d *Durable) Cache() *Cache { return d.cache }

type Client struct {
	URL, Token string
	NodeID     int64
	State      *Durable
	Lease      time.Duration
	Available  func(bool)
}

func (x *Client) Run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		if e := x.once(ctx); e == nil {
			delay = time.Second
		}
		if x.Available != nil {
			x.Available(false)
		}
		jitter := time.Duration(rand.Int63n(int64(delay/2 + 1)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay + jitter):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}
func signature(token, ts, method, path string) string {
	h := hmac.New(sha256.New, []byte(token))
	fmt.Fprintf(h, "%s\n%s\n%s\n", ts, method, path)
	return hex.EncodeToString(h.Sum(nil))
}
func (x *Client) once(ctx context.Context) error {
	u, e := url.Parse(x.URL)
	if e != nil {
		return e
	}
	c, _, e := websocket.Dial(ctx, x.URL, &websocket.DialOptions{HTTPClient: http.DefaultClient})
	if e != nil {
		return e
	}
	defer c.CloseNow()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	a := map[string]any{"node_id": x.NodeID, "token": x.Token, "timestamp": ts, "signature": signature(x.Token, ts, "WS", u.Path)}
	b, _ := json.Marshal(a)
	if e = c.Write(ctx, websocket.MessageText, b); e != nil {
		return e
	}
	for {
		_, b, e = c.Read(ctx)
		if e != nil {
			return e
		}
		var m struct {
			Version, Type string
			Revision      int64
			Data          json.RawMessage
		}
		if json.Unmarshal(b, &m) != nil || m.Version != "1" {
			continue
		}
		switch m.Type {
		case "auth.success":
			if x.Available != nil {
				x.Available(true)
			}
		case "ping":
			p, _ := json.Marshal(map[string]any{"version": "1", "type": "pong"})
			_ = c.Write(ctx, websocket.MessageText, p)
		case "sync.devices":
			var s devices.Snapshot
			if json.Unmarshal(m.Data, &s) == nil {
				if e = x.State.ApplySnapshot(s, m.Revision, x.Lease); e != nil {
					return e
				}
			}
		}
	}
}
