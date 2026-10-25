package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
)

//go:embed sql/*.sql
var files embed.FS

func Up(ctx context.Context, db *sql.DB) error {
	if _, e := db.ExecContext(ctx, `create table if not exists flyboard_schema_migrations(version text primary key,applied_at timestamptz not null default now())`); e != nil {
		return e
	}
	names, e := files.ReadDir("sql")
	if e != nil {
		return e
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
	for _, n := range names {
		var yes bool
		e = db.QueryRowContext(ctx, "select exists(select 1 from flyboard_schema_migrations where version=$1)", n.Name()).Scan(&yes)
		if e != nil {
			return e
		}
		if yes {
			continue
		}
		b, _ := files.ReadFile("sql/" + n.Name())
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, string(b)); e == nil {
			_, e = tx.ExecContext(ctx, "insert into flyboard_schema_migrations(version) values($1)", n.Name())
		}
		if e != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", n.Name(), e)
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func Status(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, e := db.QueryContext(ctx, `select version from flyboard_schema_migrations order by version`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var x []string
	for rows.Next() {
		var s string
		if e = rows.Scan(&s); e != nil {
			return nil, e
		}
		x = append(x, s)
	}
	return x, rows.Err()
}
