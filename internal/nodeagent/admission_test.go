package nodeagent

import (
	"testing"
	"time"
)

func TestFailPolicy(t *testing.T) {
	c := New(time.Minute)
	now := time.Now()
	c.now = func() time.Time { return now }
	if c.Admit("u", "1", false).Allowed {
		t.Fatal("new device fail-open")
	}
	c.Apply("u", "1", true, now.Add(time.Minute))
	if !c.Admit("u", "1", false).Allowed {
		t.Fatal("verified device denied in grace")
	}
	now = now.Add(2 * time.Minute)
	if c.Admit("u", "1", false).Allowed {
		t.Fatal("expired device fail-open")
	}
}
