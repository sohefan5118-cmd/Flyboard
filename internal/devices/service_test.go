package devices

import (
	"context"
	"sync"
	"testing"
	"time"
)

type mem struct {
	sync.Mutex
	d    map[int64]map[string]Device
	miss map[string]int
}

func (m *mem) Admit(_ context.Context, u int64, l int, _ time.Duration, n, ip string, now time.Time) (bool, error) {
	m.Lock()
	defer m.Unlock()
	if m.d == nil {
		m.d = map[int64]map[string]Device{}
	}
	if m.d[u] == nil {
		m.d[u] = map[string]Device{}
	}
	f := field(n, ip)
	if _, ok := m.d[u][f]; !ok && l > 0 && len(m.d[u]) >= l {
		return false, nil
	}
	m.d[u][f] = Device{Field: f, NodeID: n, IP: ip, SeenAt: now}
	return true, nil
}
func (m *mem) Snapshot(_ context.Context, u int64, w time.Duration, now time.Time) (Snapshot, error) {
	m.Lock()
	defer m.Unlock()
	s := Snapshot{UserID: u}
	for f, d := range m.d[u] {
		if now.Sub(d.SeenAt) < w {
			s.Devices = append(s.Devices, d)
		} else {
			delete(m.d[u], f)
		}
	}
	s.OnlineCount = len(s.Devices)
	return s, nil
}
func (m *mem) Missing(_ context.Context, u int64, n string, p []string, t int) ([]string, error) {
	m.Lock()
	defer m.Unlock()
	if m.miss == nil {
		m.miss = map[string]int{}
	}
	seen := map[string]bool{}
	for _, ip := range p {
		seen[field(n, ip)] = true
	}
	for f := range m.d[u] {
		if !seen[f] {
			m.miss[f]++
			if m.miss[f] >= t {
				delete(m.d[u], f)
			}
		}
	}
	return nil, nil
}
func (m *mem) Clear(_ context.Context, u int64) error                           { delete(m.d, u); return nil }
func (m *mem) Prune(context.Context, time.Duration, time.Time) ([]int64, error) { return nil, nil }
func TestCrossNodeAtomic(t *testing.T) {
	m := &mem{}
	s := Service{Store: m, Window: time.Minute}
	var wg sync.WaitGroup
	ok := 0
	var mu sync.Mutex
	for _, n := range []string{"a", "b"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			x, _ := s.Report(context.Background(), Report{UserID: 1, Limit: 1, NodeID: n, IPs: []string{"192.0.2." + map[string]string{"a": "1", "b": "2"}[n]}})
			if x.OnlineCount == 1 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	snap, _ := m.Snapshot(context.Background(), 1, time.Minute, time.Now())
	if snap.OnlineCount != 1 || ok < 1 {
		t.Fatalf("%+v", snap)
	}
}
func TestTrafficDoesNotRenewAndTwoMissing(t *testing.T) {
	m := &mem{}
	now := time.Now()
	s := Service{Store: m, Window: time.Minute, Now: func() time.Time { return now }}
	s.Report(context.Background(), Report{UserID: 1, Limit: 1, NodeID: "n", IPs: []string{"192.0.2.1"}, Full: true})
	s.Report(context.Background(), Report{UserID: 1, NodeID: "n", Full: true})
	x, _ := s.Report(context.Background(), Report{UserID: 1, NodeID: "n", Kind: "traffic"})
	if x.OnlineCount != 1 {
		t.Fatal("single missing removed")
	}
	s.Report(context.Background(), Report{UserID: 1, NodeID: "n", Full: true})
	x, _ = m.Snapshot(context.Background(), 1, time.Minute, now)
	if x.OnlineCount != 0 {
		t.Fatal("second missing retained")
	}
}
