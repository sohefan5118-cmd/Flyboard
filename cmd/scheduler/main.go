package main

import (
	"context"
	"encoding/json"
	"flyboard/internal/config"
	"flyboard/internal/devices"
	"flyboard/internal/pull"
	"flyboard/internal/store"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log"
	"time"
)

func main() {
	c, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	s, e := store.Open(context.Background(), c.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer s.DB.Close()
	o, e := redis.ParseURL(c.ValkeyURL)
	if e != nil {
		log.Fatal(e)
	}
	v := devices.Valkey{R: redis.NewClient(o)}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		ctx := context.Background()
		if _, e = pull.Reap(ctx, s.DB); e != nil {
			log.Printf("reap: %v", e)
		}
		ids, e := v.Prune(ctx, 120*time.Second, time.Now())
		if e != nil {
			log.Printf("prune: %v", e)
			continue
		}
		for _, u := range ids {
			snap, e := v.Snapshot(ctx, u, 120*time.Second, time.Now())
			if e != nil {
				continue
			}
			tx, e := s.DB.BeginTx(ctx, nil)
			if e != nil {
				continue
			}
			_, e = tx.ExecContext(ctx, `delete from device_display where user_id=$1`, u)
			for _, d := range snap.Devices {
				var nid int64
				_, _ = fmt.Sscan(d.NodeID, &nid)
				if e == nil {
					_, e = tx.ExecContext(ctx, `insert into device_display(user_id,node_id,ip,seen_at) values($1,$2,$3,$4) on conflict do nothing`, u, nid, d.IP, d.SeenAt)
				}
			}
			if e == nil {
				_, e = tx.ExecContext(ctx, `update users set online_count=$2 where id=$1`, u, snap.OnlineCount)
			}
			if e == nil {
				_, e = tx.ExecContext(ctx, `insert into sync_outbox(aggregate_type,aggregate_id,revision,payload) values('devices',$1,(extract(epoch from clock_timestamp())*1000000)::bigint,$2)`, u, mustJSON(snap))
			}
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			if e != nil {
				log.Printf("projection: %v", e)
			}
		}
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
