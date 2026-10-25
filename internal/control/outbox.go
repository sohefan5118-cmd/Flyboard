package control

import (
	"context"
	"encoding/json"
)

// Dispatch claims with SKIP LOCKED and marks only after enqueue; duplicate delivery is expected and revisions make it idempotent.
func (h *Hub) Dispatch(ctx context.Context, limit int) (int, error) {
	tx, e := h.DB.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `select id,aggregate_type,aggregate_id,revision,payload from sync_outbox where delivered_at is null and next_attempt_at<=now() order by id for update skip locked limit $1`, limit)
	if e != nil {
		return 0, e
	}
	type x struct {
		id, aggregate, rev int64
		typ                string
		p                  json.RawMessage
	}
	var xs []x
	for rows.Next() {
		var v x
		if e = rows.Scan(&v.id, &v.typ, &v.aggregate, &v.rev, &v.p); e != nil {
			return 0, e
		}
		xs = append(xs, v)
	}
	rows.Close()
	for _, v := range xs {
		h.Broadcast(Envelope{Version: Version, Type: "sync." + v.typ, Revision: v.rev, Data: v.p})
		if _, e = tx.ExecContext(ctx, `update sync_outbox set delivered_at=now() where id=$1`, v.id); e != nil {
			return 0, e
		}
	}
	return len(xs), tx.Commit()
}
