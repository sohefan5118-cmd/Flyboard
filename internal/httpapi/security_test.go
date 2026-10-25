package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	security(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, name := range []string{"Content-Security-Policy", "Permissions-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
		if rr.Header().Get(name) == "" {
			t.Errorf("missing %s", name)
		}
	}
}

func TestValidOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin, host string
		want         bool
	}{
		{"https://panel.example", "panel.example", true},
		{"http://panel.example", "panel.example", true},
		{"https://evil.example", "panel.example", false},
		{"", "panel.example", false},
		{"javascript:alert(1)", "panel.example", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "https://"+tc.host+"/", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		if got := validOrigin(r); got != tc.want {
			t.Errorf("validOrigin(%q, %q)=%v want %v", tc.origin, tc.host, got, tc.want)
		}
	}
}
