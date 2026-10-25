package subparse

import (
	"encoding/base64"
	"testing"
)

func TestParseURIAndBase64(t *testing.T) {
	raw := "trojan://pw@example.com:443#hello\nvless://id@host.test:8443#two"
	for _, x := range []string{raw, base64.StdEncoding.EncodeToString([]byte(raw))} {
		n, e := Parse([]byte(x))
		if e != nil || len(n) != 2 || n[0].Address != "example.com" {
			t.Fatalf("%v %#v", e, n)
		}
	}
}
func TestRejectUnknown(t *testing.T) {
	if _, e := Parse([]byte("https://example.com")); e == nil {
		t.Fatal("accepted")
	}
}
