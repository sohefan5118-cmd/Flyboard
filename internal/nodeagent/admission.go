package nodeagent

import (
	"sync"
	"time"
)

type Decision struct {
	Allowed bool
	Reason  string
}
type Cache struct {
	mu       sync.RWMutex
	grace    time.Duration
	now      func() time.Time
	verified map[string]time.Time
}

func New(grace time.Duration) *Cache {
	return &Cache{grace: grace, now: time.Now, verified: map[string]time.Time{}}
}
func (c *Cache) Apply(user, ip string, allowed bool, leaseUntil time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := user + "\x00" + ip
	if allowed {
		c.verified[k] = leaseUntil
	} else {
		delete(c.verified, k)
	}
}

// Admit must run before opening the upstream connection. New devices fail closed while control is unavailable.
func (c *Cache) Admit(user, ip string, controlAvailable bool) Decision {
	c.mu.RLock()
	until, ok := c.verified[user+"\x00"+ip]
	c.mu.RUnlock()
	now := c.now()
	if ok && now.Before(until) {
		return Decision{true, "verified"}
	}
	if !controlAvailable {
		return Decision{false, "control_unavailable_new_device"}
	}
	return Decision{false, "not_admitted"}
}
