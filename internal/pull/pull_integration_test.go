package pull

import (
	"context"
	"database/sql"
	"flyboard/internal/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"testing"
)

func TestEnqueueDedupIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = migrations.Up(ctx, db); e != nil {
		t.Fatal(e)
	}
	var up int64
	e = db.QueryRowContext(ctx, `insert into upstreams(name,subscribe_url_ciphertext) values('integration',decode('00','hex')) returning id`).Scan(&up)
	if e != nil {
		t.Fatal(e)
	}
	defer db.ExecContext(ctx, "delete from upstreams where id=$1", up)
	a, dup, e := Enqueue(ctx, db, up)
	if e != nil || dup {
		t.Fatal(a, dup, e)
	}
	b, dup, e := Enqueue(ctx, db, up)
	if e != nil || !dup || a != b {
		t.Fatal(a, b, dup, e)
	}
	var status string
	if e = db.QueryRowContext(ctx, "select status from upstream_pull_executions where id=$1", a).Scan(&status); e != nil || status != "pending" {
		t.Fatal(status, e)
	}
}
