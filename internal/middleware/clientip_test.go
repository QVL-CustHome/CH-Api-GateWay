package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newExtractor(t *testing.T, trusted ...string) *IPExtractor {
	t.Helper()
	e, err := NewIPExtractor(trusted)
	if err != nil {
		t.Fatalf("NewIPExtractor(%v): %v", trusted, err)
	}
	return e
}

func clientIPRequest(remoteAddr, forwardedFor string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	return req
}

func TestClientIPWithoutTrustedProxies(t *testing.T) {
	e := newExtractor(t)
	cases := []struct {
		name         string
		remoteAddr   string
		forwardedFor string
		want         string
	}{
		{"RemoteAddr simple", "203.0.113.7:54321", "", "203.0.113.7"},
		{"RemoteAddr sans port", "203.0.113.7", "", "203.0.113.7"},
		{"XFF forgé ignoré", "203.0.113.7:54321", "1.2.3.4", "203.0.113.7"},
		{"XFF en chaîne ignoré", "203.0.113.7:54321", "1.2.3.4, 5.6.7.8", "203.0.113.7"},
		{"IPv6 RemoteAddr", "[2001:db8::1]:443", "", "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.ClientIP(clientIPRequest(tc.remoteAddr, tc.forwardedFor)); got != tc.want {
				t.Errorf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIPBehindTrustedProxy(t *testing.T) {
	e := newExtractor(t, "10.0.0.1", "10.1.0.0/16")
	cases := []struct {
		name         string
		remoteAddr   string
		forwardedFor string
		want         string
	}{
		{"XFF simple", "10.0.0.1:80", "198.51.100.9", "198.51.100.9"},
		{"chaîne avec proxies de confiance à droite", "10.0.0.1:80", "198.51.100.9, 10.1.2.3", "198.51.100.9"},
		{"première IP non-fiable en partant de la droite", "10.0.0.1:80", "1.2.3.4, 198.51.100.9, 10.1.2.3", "198.51.100.9"},
		{"espaces tolérés", "10.0.0.1:80", "  198.51.100.9 , 10.1.2.3 ", "198.51.100.9"},
		{"CIDR de confiance", "10.1.42.7:80", "198.51.100.9", "198.51.100.9"},
		{"XFF absent derrière proxy", "10.0.0.1:80", "", "10.0.0.1"},
		{"toutes les IP de confiance", "10.0.0.1:80", "10.1.2.3, 10.1.2.4", "10.0.0.1"},
		{"remote non-fiable : XFF ignoré", "203.0.113.7:1234", "198.51.100.9", "203.0.113.7"},
		{"entrée XFF non parsable traitée comme non-fiable", "10.0.0.1:80", "garbage-value", "garbage-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.ClientIP(clientIPRequest(tc.remoteAddr, tc.forwardedFor)); got != tc.want {
				t.Errorf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewIPExtractorInvalidEntry(t *testing.T) {
	for _, entry := range []string{"pas-une-ip", "10.0.0.0/99", "300.1.2.3"} {
		t.Run(entry, func(t *testing.T) {
			if _, err := NewIPExtractor([]string{entry}); err == nil {
				t.Errorf("NewIPExtractor(%q) devrait échouer", entry)
			}
		})
	}
}
