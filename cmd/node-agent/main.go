package main

import (
	"context"
	"encoding/json"
	"flyboard/internal/nodeagent"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

type request struct{ User, IP string }

func main() {
	grace, _ := time.ParseDuration(env("AGENT_GRACE", "2m"))
	state, e := nodeagent.OpenDurable(env("AGENT_STATE", "/var/lib/flyboard-agent/state.json"), grace)
	if e != nil {
		log.Fatal(e)
	}
	var available atomic.Bool
	if raw := os.Getenv("PANEL_WS_URL"); raw != "" {
		id, e := strconv.ParseInt(os.Getenv("NODE_ID"), 10, 64)
		if e != nil {
			log.Fatal("invalid NODE_ID")
		}
		go (&nodeagent.Client{URL: raw, Token: os.Getenv("NODE_TOKEN"), NodeID: id, State: state, Lease: grace, Available: available.Store}).Run(context.Background())
	}
	http.HandleFunc("POST /v1/admit", func(w http.ResponseWriter, r *http.Request) {
		var q request
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&q) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		d := state.Cache().Admit(q.User, q.IP, available.Load())
		if !d.Allowed {
			w.WriteHeader(403)
		}
		json.NewEncoder(w).Encode(d)
	})
	log.Printf("node-agent admission listening on %s", env("AGENT_LISTEN", ":9090"))
	log.Fatal(http.ListenAndServe(env("AGENT_LISTEN", ":9090"), nil))
}
func env(k, d string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return d
}
