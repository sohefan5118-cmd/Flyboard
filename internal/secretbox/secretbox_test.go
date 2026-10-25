package secretbox

import (
	"bytes"
	"testing"
)

func TestRoundTripAndAAD(t *testing.T) {
	b, _ := New(bytes.Repeat([]byte{7}, 32))
	c, e := b.Seal([]byte("https://secret"), "upstream:1")
	if e != nil {
		t.Fatal(e)
	}
	p, e := b.Open(c, "upstream:1")
	if e != nil || string(p) != "https://secret" {
		t.Fatal(e, string(p))
	}
	if _, e = b.Open(c, "upstream:2"); e == nil {
		t.Fatal("AAD accepted")
	}
	if bytes.Contains(c, []byte("secret")) {
		t.Fatal("plaintext leaked")
	}
}
