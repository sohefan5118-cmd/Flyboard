package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestDisabledAndFailurePolicySignal(t *testing.T) {
	l := Limiter{}
	ok, retry, err := l.Allow(context.Background(), "login", "ip", 0, time.Minute)
	if !ok || retry != 0 || err != nil {
		t.Fatalf("disabled: %v %d %v", ok, retry, err)
	}
	ok, _, err = l.Allow(context.Background(), "login", "ip", 1, time.Minute)
	if ok || err == nil {
		t.Fatalf("failure must be surfaced for caller fail-closed policy")
	}
}
