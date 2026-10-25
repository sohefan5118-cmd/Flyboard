package store

import (
	"context"
	"database/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"time"
)

type Store struct{ DB *sql.DB }

func Open(ctx context.Context, url string) (*Store, error) {
	d, e := sql.Open("pgx", url)
	if e != nil {
		return nil, e
	}
	d.SetMaxOpenConns(20)
	if e = d.PingContext(ctx); e != nil {
		return nil, e
	}
	return &Store{d}, nil
}

type Admin struct {
	ID                        int64
	Email, PasswordHash, Role string
}

func (s *Store) AdminByEmail(ctx context.Context, email string) (Admin, error) {
	var a Admin
	e := s.DB.QueryRowContext(ctx, `select id,email,password_hash,role from admins where lower(email)=lower($1)`, email).Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Role)
	return a, e
}
func (s *Store) Bootstrap(ctx context.Context, email, hash string, tokenDigest []byte) (int64, error) {
	var id int64
	e := s.DB.QueryRowContext(ctx, `with used as (update bootstrap_tokens set used_at=now() where digest=$1 and used_at is null returning 1) insert into admins(email,password_hash,role) select $2,$3,'owner' from used where not exists(select 1 from admins) returning id`, tokenDigest, email, hash).Scan(&id)
	return id, e
}
func (s *Store) Session(ctx context.Context, admin int64, digest []byte, csrf string, exp time.Time) error {
	_, e := s.DB.ExecContext(ctx, `insert into admin_sessions(token_digest,admin_id,csrf_token,expires_at) values($1,$2,$3,$4)`, digest, admin, csrf, exp)
	return e
}
func (s *Store) SessionAdmin(ctx context.Context, d []byte) (Admin, string, error) {
	var a Admin
	var csrf string
	e := s.DB.QueryRowContext(ctx, `select a.id,a.email,a.password_hash,a.role,s.csrf_token from admin_sessions s join admins a on a.id=s.admin_id where s.token_digest=$1 and s.expires_at>now()`, d).Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Role, &csrf)
	return a, csrf, e
}
