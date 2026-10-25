package nodeagent

import (
	"flyboard/internal/devices"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableRevisionAndRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	d, e := OpenDurable(p, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	s := devices.Snapshot{UserID: 7, Devices: []devices.Device{{IP: "192.0.2.1"}}}
	if e = d.ApplySnapshot(s, 2, time.Hour); e != nil {
		t.Fatal(e)
	}
	if e = d.ApplySnapshot(devices.Snapshot{UserID: 7}, 1, time.Hour); e != nil {
		t.Fatal(e)
	}
	d2, e := OpenDurable(p, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if !d2.Cache().Admit("7", "192.0.2.1", false).Allowed {
		t.Fatal("cache/revision not recovered")
	}
}
