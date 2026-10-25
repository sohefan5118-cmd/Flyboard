package pull

import (
	"context"
	"database/sql"
	"errors"
	"flyboard/internal/safefetch"
	"flyboard/internal/secretbox"
	"flyboard/internal/subparse"
	"fmt"
	"time"
)

type Service struct {
	DB    *sql.DB
	Box   *secretbox.Box
	Fetch safefetch.Fetcher
}

func Enqueue(ctx context.Context, db *sql.DB, upstream int64) (int64, bool, error) {
	var id int64
	e := db.QueryRowContext(ctx, `insert into upstream_pull_executions(upstream_id,status,deadline_at) values($1,'pending',now()+interval '10 minutes') on conflict (upstream_id) where status in ('pending','running') do nothing returning id`, upstream).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		e = db.QueryRowContext(ctx, `select id from upstream_pull_executions where upstream_id=$1 and status in('pending','running')`, upstream).Scan(&id)
		return id, true, e
	}
	return id, false, e
}
func (s Service) RunOne(ctx context.Context) (bool, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	var id, up int64
	var cipher []byte
	e = tx.QueryRowContext(ctx, `select e.id,e.upstream_id,u.subscribe_url_ciphertext from upstream_pull_executions e join upstreams u on u.id=e.upstream_id where e.status='pending' and e.deadline_at>now() order by e.id for update of e skip locked limit 1`).Scan(&id, &up, &cipher)
	if errors.Is(e, sql.ErrNoRows) {
		tx.Rollback()
		return false, nil
	}
	if e != nil {
		tx.Rollback()
		return false, e
	}
	_, e = tx.ExecContext(ctx, `update upstream_pull_executions set status='running',started_at=now(),heartbeat_at=now() where id=$1`, id)
	if e != nil {
		tx.Rollback()
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	fail := func(code string, err error) (bool, error) {
		s.DB.ExecContext(context.Background(), `update upstream_pull_executions set status='error',error_code=$2,finished_at=now() where id=$1`, id, code)
		s.DB.ExecContext(context.Background(), `update upstreams set last_error=$2 where id=$1`, up, code)
		return true, err
	}
	plain, e := s.Box.Open(cipher, fmt.Sprintf("upstream:%d", up))
	if e != nil {
		return fail("decrypt_failed", e)
	}
	body, e := s.Fetch.Fetch(ctx, string(plain))
	if e != nil {
		return fail("fetch_failed", e)
	}
	nodes, e := subparse.Parse(body)
	if e != nil {
		return fail("parse_failed", e)
	}
	tx, e = s.DB.BeginTx(ctx, nil)
	if e != nil {
		return fail("database_failed", e)
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "delete from upstream_nodes where upstream_id=$1", up); e != nil {
		return fail("database_failed", e)
	}
	for _, n := range nodes {
		if _, e = tx.ExecContext(ctx, `insert into upstream_nodes(upstream_id,name,type,address,port,raw_uri) values($1,$2,$3,$4,nullif($5,0),$6)`, up, n.Name, n.Type, n.Address, n.Port, n.URI); e != nil {
			return fail("database_failed", e)
		}
	}
	if _, e = tx.ExecContext(ctx, `update upstreams set last_node_count=$2,last_pulled_at=now(),last_error=null,updated_at=now() where id=$1`, up, len(nodes)); e != nil {
		return fail("database_failed", e)
	}
	if _, e = tx.ExecContext(ctx, `update upstream_pull_executions set status='completed',finished_at=now() where id=$1`, id); e != nil {
		return fail("database_failed", e)
	}
	if e = tx.Commit(); e != nil {
		return fail("database_failed", e)
	}
	return true, nil
}
func Reap(ctx context.Context, db *sql.DB) (int64, error) {
	r, e := db.ExecContext(ctx, `update upstream_pull_executions set status='error',error_code='timed_out',finished_at=now() where status in('pending','running') and (deadline_at<now() or (status='running' and coalesce(heartbeat_at,started_at,created_at)<now()-interval '10 minutes'))`)
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}

var _ = time.Second
