// Package ratelimit provides a replica-consistent fixed-window limiter backed by Valkey.
package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var script = redis.NewScript(`
local n=redis.call('INCR',KEYS[1])
if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end
local ttl=redis.call('PTTL',KEYS[1])
return {n,ttl}
`)

type Limiter struct {
	R      redis.UniversalClient
	Prefix string
}

// Allow returns whether the request is permitted and the number of seconds to retry.
// limit<=0 disables the scope. Valkey errors are returned for the caller's explicit policy.
func (l Limiter) Allow(ctx context.Context, scope, key string, limit int64, window time.Duration) (bool, int, error) {
	if limit <= 0 {
		return true, 0, nil
	}
	if l.R == nil {
		return false, 0, errors.New("valkey unavailable")
	}
	if window < time.Second {
		window = time.Second
	}
	v, err := script.Run(ctx, l.R, []string{l.Prefix + scope + ":" + key}, window.Milliseconds()).Slice()
	if err != nil {
		return false, 0, err
	}
	n, ok1 := v[0].(int64)
	ms, ok2 := v[1].(int64)
	if !ok1 || !ok2 {
		return false, 0, errors.New("invalid valkey response")
	}
	if n <= limit {
		return true, 0, nil
	}
	retry := int((ms + 999) / 1000)
	if retry < 1 {
		retry = 1
	}
	return false, retry, nil
}
