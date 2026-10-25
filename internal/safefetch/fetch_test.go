package safefetch

import (
	"context"
	"net/netip"
	"testing"
)

type resolver struct{ ips []netip.Addr }

func (r resolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.ips, nil
}
func TestIPClassification(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "100.64.1.1", "169.254.169.254", "192.0.2.1", "224.0.0.1", "::1", "2001:4860:4860::8888"} {
		if IsPublic(netip.MustParseAddr(s)) {
			t.Error(s)
		}
	}
	if !IsPublic(netip.MustParseAddr("8.8.8.8")) {
		t.Error("public rejected")
	}
}
func TestURLValidation(t *testing.T) {
	pub := resolver{[]netip.Addr{netip.MustParseAddr("8.8.8.8")}}
	f := Fetcher{Resolver: pub}
	for _, u := range []string{"http://example.com", "https://u:p@example.com", "https://example.com/#x", "https://[2001:db8::1]/", "https://example.com:444/"} {
		if _, _, e := f.validate(context.Background(), u); e == nil {
			t.Error(u)
		}
	}
	mixed := Fetcher{Resolver: resolver{[]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}}}
	if _, _, e := mixed.validate(context.Background(), "https://example.com"); e == nil {
		t.Error("mixed dns")
	}
}
