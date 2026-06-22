package app

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
)

type expectedHeader struct {
	name  string
	value string
}

func scrum179ExpectedHeaders() []expectedHeader {
	return []expectedHeader{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
		{"Permissions-Policy", "geolocation=(), microphone=(), camera=()"},
		{"Content-Security-Policy", "frame-ancestors 'none'; base-uri 'self'; object-src 'none'"},
	}
}

func assertSecurityHeaders(t *testing.T, resp *http.Response) {
	t.Helper()
	for _, h := range scrum179ExpectedHeaders() {
		got := resp.Header.Get(h.name)
		if got != h.value {
			t.Errorf("%s = %q, want %q", h.name, got, h.value)
		}
	}
	if got := resp.Header.Get("X-XSS-Protection"); got != "" {
		t.Errorf("X-XSS-Protection = %q, want absent", got)
	}
}

func newHTTPGateway(t *testing.T, cfg *config.GatewayConfig) *httptest.Server {
	t.Helper()
	handler, onShutdown, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	for _, fn := range onShutdown {
		t.Cleanup(fn)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	return gateway
}

func TestSecurityHeadersOnProxiedRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(backend.Close)

	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: backend.URL})
	gateway := newHTTPGateway(t, cfg)

	resp, err := http.Get(gateway.URL + "/api/users/42")
	if err != nil {
		t.Fatalf("requête route proxifiée: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut = %d, want 200", resp.StatusCode)
	}
	assertSecurityHeaders(t, resp)
}

func TestSecurityHeadersOnHealth(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	gateway := newHTTPGateway(t, cfg)

	resp, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatalf("requête /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut /health = %d, want 200", resp.StatusCode)
	}
	assertSecurityHeaders(t, resp)
}

func TestSecurityHeadersOnNotFound(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	gateway := newHTTPGateway(t, cfg)

	resp, err := http.Get(gateway.URL + "/route-inconnue")
	if err != nil {
		t.Fatalf("requête route inconnue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("statut = %d, want 404", resp.StatusCode)
	}
	assertSecurityHeaders(t, resp)
}

func TestSecurityHeadersOnPayloadTooLarge(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<10)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: backend.URL})
	cfg.Server.MaxBodyBytes = 16
	gateway := newHTTPGateway(t, cfg)

	body := strings.NewReader(strings.Repeat("A", 4096))
	resp, err := http.Post(gateway.URL+"/api/users", "application/octet-stream", body)
	if err != nil {
		t.Fatalf("requête body trop gros: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("statut = %d, want 413", resp.StatusCode)
	}
	assertSecurityHeaders(t, resp)
}

func TestSecurityHeadersOnRateLimited(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	cfg.Server.RateLimit.Enabled = true
	cfg.Server.RateLimit.RequestsPerSecond = 1
	cfg.Server.RateLimit.Burst = 1
	gateway := newHTTPGateway(t, cfg)

	var throttled *http.Response
	for i := 0; i < 5; i++ {
		resp, err := http.Get(gateway.URL + "/route-inconnue")
		if err != nil {
			t.Fatalf("requête: %v", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			throttled = resp
			break
		}
		resp.Body.Close()
	}
	if throttled == nil {
		t.Fatal("aucune réponse 429 obtenue malgré le rate limiting")
	}
	defer throttled.Body.Close()
	assertSecurityHeaders(t, throttled)
}

func TestHSTSAbsentOverHTTP(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	gateway := newHTTPGateway(t, cfg)

	resp, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatalf("requête HTTP: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q en HTTP, want absent", got)
	}
}

func TestHSTSPresentOverHTTPS(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	handler, onShutdown, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	for _, fn := range onShutdown {
		t.Cleanup(fn)
	}
	gateway := httptest.NewTLSServer(handler)
	t.Cleanup(gateway.Close)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := client.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatalf("requête HTTPS: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains" {
		t.Errorf("Strict-Transport-Security = %q, want \"max-age=63072000; includeSubDomains\"", got)
	}
	assertSecurityHeaders(t, resp)
}
