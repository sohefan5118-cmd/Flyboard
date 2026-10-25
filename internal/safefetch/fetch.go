package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Fetcher struct {
	Resolver     Resolver
	AllowHTTP    bool
	MaxBytes     int64
	Timeout      time.Duration
	MaxRedirects int
}

func IsPublic(a netip.Addr) bool {
	if !a.IsValid() || a.Is6() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	blocked := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("169.254.169.254/32"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4")}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
func (f Fetcher) validate(ctx context.Context, raw string) (*url.URL, []netip.Addr, error) {
	if len(raw) > 4096 {
		return nil, nil, errors.New("url too long")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" {
		return nil, nil, errors.New("invalid url")
	}
	if u.User != nil || u.Fragment != "" {
		return nil, nil, errors.New("userinfo and fragment forbidden")
	}
	if u.Scheme != "https" && !(f.AllowHTTP && u.Scheme == "http") {
		return nil, nil, errors.New("scheme forbidden")
	}
	if strings.Contains(u.Hostname(), ":") {
		return nil, nil, errors.New("ipv6 forbidden")
	}
	if p := u.Port(); p != "" && p != "443" && !(f.AllowHTTP && u.Scheme == "http" && p == "80") {
		return nil, nil, errors.New("port forbidden")
	}
	ips, e := f.Resolver.LookupNetIP(ctx, "ip", u.Hostname())
	if e != nil || len(ips) == 0 {
		return nil, nil, errors.New("dns failed")
	}
	for _, ip := range ips {
		if !IsPublic(ip) {
			return nil, nil, errors.New("dns contains forbidden address")
		}
	}
	return u, ips, nil
}
func (f Fetcher) Fetch(ctx context.Context, raw string) ([]byte, error) {
	if f.Resolver == nil {
		f.Resolver = net.DefaultResolver
	}
	if f.MaxBytes == 0 {
		f.MaxBytes = 4 << 20
	}
	if f.Timeout == 0 {
		f.Timeout = 45 * time.Second
	}
	if f.MaxRedirects == 0 {
		f.MaxRedirects = 3
	}
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	u, ips, e := f.validate(ctx, raw)
	if e != nil {
		return nil, e
	}
	redirects := 0
	for {
		ip := ips[0]
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		tr := &http.Transport{Proxy: nil, DisableCompression: false, DialContext: func(c context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second}
			return d.DialContext(c, "tcp", net.JoinHostPort(ip.String(), port))
		}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
		client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		req.Host = u.Host
		resp, e := client.Do(req)
		if e != nil {
			return nil, fmt.Errorf("fetch failed: %w", e)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			resp.Body.Close()
			if redirects >= f.MaxRedirects {
				return nil, errors.New("too many redirects")
			}
			loc, e := resp.Location()
			if e != nil {
				return nil, errors.New("invalid redirect")
			}
			u, ips, e = f.validate(ctx, loc.String())
			if e != nil {
				return nil, e
			}
			redirects++
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("http status %d", resp.StatusCode)
		}
		r := io.LimitReader(resp.Body, f.MaxBytes+1)
		b, e := io.ReadAll(r)
		resp.Body.Close()
		if e != nil {
			return nil, e
		}
		if int64(len(b)) > f.MaxBytes {
			return nil, errors.New("body too large")
		}
		return b, nil
	}
}

var _ = strconv.Itoa
