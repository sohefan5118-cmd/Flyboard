// Package ownerlock implements subscription IP+UA ownership without physical-device claims.
package ownerlock

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net"
	"strings"
	"time"
)

type Owner struct {
	Fingerprint, IP, UA string
	Seen                time.Time
}
type Store interface {
	Update(context.Context, int64, Owner, int, time.Duration, func(Owner) bool) (bool, *Owner, error)
	Whitelist(context.Context, int64) (bool, error)
	Audit(context.Context, int64, string, string, Owner) error
}
type Service struct {
	Store                  Store
	Window, MigrationGrace time.Duration
	Now                    func() time.Time
	Release                func(context.Context, int64, Owner) error
}

func NormalizeIP(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return ""
	}
	return ip.String()
}
func Fingerprint(ip, ua string) string {
	h := sha256.Sum256([]byte(NormalizeIP(ip) + "\x00" + strings.TrimSpace(ua)))
	return hex.EncodeToString(h[:])
}
func family(ip string) int {
	p := net.ParseIP(ip)
	if p != nil && p.To4() != nil {
		return 4
	}
	if p != nil {
		return 6
	}
	return 0
}
func (s Service) Acquire(c context.Context, u int64, ip, ua string, limit int) (bool, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	o := Owner{Fingerprint: Fingerprint(ip, ua), IP: NormalizeIP(ip), UA: strings.TrimSpace(ua), Seen: now}
	if o.IP == "" {
		return false, nil
	}
	white, e := s.Store.Whitelist(c, u)
	if e != nil {
		return false, e
	}
	if white {
		_ = s.Store.Audit(c, u, "allowed", "whitelist", o)
		return true, nil
	}
	ok, old, e := s.Store.Update(c, u, o, limit, s.Window, func(x Owner) bool {
		return x.UA == o.UA && family(x.IP) == family(o.IP) && now.Sub(x.Seen) >= s.MigrationGrace
	})
	if e != nil {
		return false, e
	}
	if ok && old != nil && s.Release != nil {
		_ = s.Release(c, u, *old)
	}
	action, reason := "blocked", "owner_limit"
	if ok {
		action, reason = "allowed", "owner"
	}
	_ = s.Store.Audit(c, u, action, reason, o)
	return ok, nil
}

// Postgres serializes each user with a transaction advisory lock, making prune/admit/migrate atomic across panel replicas.
type Postgres struct{ DB *sql.DB }

func (p Postgres) Whitelist(c context.Context, u int64) (bool, error) {
	var b bool
	e := p.DB.QueryRowContext(c, `select coalesce((select enabled from main_user_whitelist where user_id=$1),false)`, u).Scan(&b)
	return b, e
}
func (p Postgres) Audit(c context.Context, u int64, a, r string, o Owner) error {
	_, e := p.DB.ExecContext(c, `insert into device_audit(user_id,action,reason,ip,user_agent) values($1,$2,$3,$4,$5)`, u, a, r, o.IP, o.UA)
	return e
}
func (p Postgres) Update(c context.Context, u int64, o Owner, limit int, w time.Duration, migrate func(Owner) bool) (bool, *Owner, error) {
	tx, e := p.DB.BeginTx(c, nil)
	if e != nil {
		return false, nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(c, `select pg_advisory_xact_lock($1)`, u); e != nil {
		return false, nil, e
	}
	_, e = tx.ExecContext(c, `delete from owner_locks where user_id=$1 and seen_at<$2`, u, o.Seen.Add(-w))
	if e != nil {
		return false, nil, e
	}
	res, e := tx.ExecContext(c, `update owner_locks set seen_at=$3,ip=$4,user_agent=$5 where user_id=$1 and fingerprint=$2`, u, o.Fingerprint, o.Seen, o.IP, o.UA)
	if e != nil {
		return false, nil, e
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil, tx.Commit()
	}
	rows, e := tx.QueryContext(c, `select fingerprint,ip::text,user_agent,seen_at from owner_locks where user_id=$1 order by seen_at`, u)
	if e != nil {
		return false, nil, e
	}
	var all []Owner
	for rows.Next() {
		var x Owner
		if e = rows.Scan(&x.Fingerprint, &x.IP, &x.UA, &x.Seen); e != nil {
			rows.Close()
			return false, nil, e
		}
		all = append(all, x)
	}
	rows.Close()
	var old *Owner
	if limit > 0 && len(all) >= limit {
		for i := range all {
			if migrate(all[i]) {
				x := all[i]
				old = &x
				break
			}
		}
		if old == nil {
			return false, nil, nil
		}
		_, e = tx.ExecContext(c, `delete from owner_locks where user_id=$1 and fingerprint=$2`, u, old.Fingerprint)
		if e != nil {
			return false, nil, e
		}
	}
	_, e = tx.ExecContext(c, `insert into owner_locks(user_id,fingerprint,ip,user_agent,seen_at) values($1,$2,$3,$4,$5)`, u, o.Fingerprint, o.IP, o.UA, o.Seen)
	if e != nil {
		return false, nil, e
	}
	return true, old, tx.Commit()
}
