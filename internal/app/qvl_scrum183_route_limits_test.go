package app

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/custhome/ch-api-gateway/internal/config"
)

func loadProductionConfig(t *testing.T) *config.GatewayConfig {
	t.Helper()
	cfg, err := config.Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatalf("chargement de config.yaml de production: %v", err)
	}
	return cfg
}

func effectiveBodyLimit(cfg *config.GatewayConfig, path string) int64 {
	limit := cfg.Server.MaxBodyBytes
	best := -1
	for _, route := range cfg.Routes {
		if route.MaxBodyBytes == nil {
			continue
		}
		if path == route.PathPrefix || strings.HasPrefix(path, route.PathPrefix+"/") {
			if len(route.PathPrefix) > best {
				best = len(route.PathPrefix)
				limit = *route.MaxBodyBytes
			}
		}
	}
	return limit
}

func samplePathForRoute(prefix string) string {
	if prefix == "/" {
		return "/probe"
	}
	return prefix + "/probe"
}

func newGatewayFromProductionConfig(t *testing.T) (*httptest.Server, *config.GatewayConfig) {
	t.Helper()
	cfg := loadProductionConfig(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	for i := range cfg.Routes {
		cfg.Routes[i].DestinationURL = backend.URL
		cfg.Routes[i].RequireAuth = false
	}
	cfg.AuthServiceURL = ""
	cfg.Server.RateLimit.Enabled = false

	handler, _, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler() avec la config de production: %v", err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	return gateway, cfg
}

func postBody(t *testing.T, url string, size int64) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(strings.Repeat("x", int(size))))
	if err != nil {
		t.Fatalf("construction de la requête: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("envoi de la requête: %v", err)
	}
	return resp
}

func TestAC1EveryRouteEnforcesAnExplicitBound(t *testing.T) {
	gateway, cfg := newGatewayFromProductionConfig(t)

	for _, route := range cfg.Routes {
		route := route
		t.Run(route.PathPrefix, func(t *testing.T) {
			limit := effectiveBodyLimit(cfg, samplePathForRoute(route.PathPrefix))
			if limit < 1 {
				t.Fatalf("la route %s a une limite de corps non bornée (%d)", route.PathPrefix, limit)
			}

			path := samplePathForRoute(route.PathPrefix)
			resp := postBody(t, gateway.URL+path, limit+1)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Errorf("route %s avec corps = limite+1 (%d) → statut %d, want 413 (borne effective)", route.PathPrefix, limit+1, resp.StatusCode)
			}
		})
	}
}

func TestAC1NoRouteConfiguredWithNonPositiveLimit(t *testing.T) {
	cfg := loadProductionConfig(t)

	if cfg.Server.MaxBodyBytes < 1 {
		t.Errorf("la limite serveur par défaut est non bornée (%d)", cfg.Server.MaxBodyBytes)
	}
	for _, route := range cfg.Routes {
		if route.MaxBodyBytes != nil && *route.MaxBodyBytes < 1 {
			t.Errorf("la route %s définit une limite non positive (%d)", route.PathPrefix, *route.MaxBodyBytes)
		}
	}
}

func TestAC2BodyOverRouteLimitReturns413(t *testing.T) {
	gateway, cfg := newGatewayFromProductionConfig(t)

	cases := []struct {
		name string
		path string
	}{
		{"route drive calibrée à 17 MiB", "/api/drive/files/1/chunks/0"},
		{"route au défaut serveur", "/api/auth/login"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit := effectiveBodyLimit(cfg, tc.path)

			under := postBody(t, gateway.URL+tc.path, limit)
			defer under.Body.Close()
			if under.StatusCode == http.StatusRequestEntityTooLarge {
				t.Errorf("corps = limite exacte (%d) → 413 inattendu sur %s", limit, tc.path)
			}

			over := postBody(t, gateway.URL+tc.path, limit+1)
			defer over.Body.Close()
			if over.StatusCode != http.StatusRequestEntityTooLarge {
				t.Errorf("corps = limite+1 (%d) sur %s → statut %d, want 413", limit+1, tc.path, over.StatusCode)
			}
		})
	}
}

func TestAC2DriveLimitIsExactlySeventeenMiB(t *testing.T) {
	gateway, cfg := newGatewayFromProductionConfig(t)

	const expected = 17825792
	limit := effectiveBodyLimit(cfg, "/api/drive/files/1/chunks/0")
	if limit != expected {
		t.Fatalf("limite effective de /api/drive = %d, want %d (17 MiB)", limit, expected)
	}

	atLimit := postBody(t, gateway.URL+"/api/drive/files/1/chunks/0", expected)
	defer atLimit.Body.Close()
	if atLimit.StatusCode == http.StatusRequestEntityTooLarge {
		t.Errorf("corps de 17 MiB pile → 413 inattendu (statut %d)", atLimit.StatusCode)
	}

	overLimit := postBody(t, gateway.URL+"/api/drive/files/1/chunks/0", expected+1)
	defer overLimit.Body.Close()
	if overLimit.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("corps de 17 MiB + 1 octet → statut %d, want 413", overLimit.StatusCode)
	}
}

func sendRawRequest(t *testing.T, gatewayURL, raw string) string {
	t.Helper()
	parsed, err := url.Parse(gatewayURL)
	if err != nil {
		t.Fatalf("parsing de l'URL de la gateway: %v", err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 3*time.Second)
	if err != nil {
		t.Fatalf("connexion TCP à la gateway: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatalf("écriture de la requête brute: %v", err)
	}

	statusLine, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("lecture de la ligne de statut: %v", err)
	}
	return strings.TrimSpace(statusLine)
}

func TestAC3OverstatedContentLengthIsRejected(t *testing.T) {
	gateway, cfg := newGatewayFromProductionConfig(t)

	path := "/api/auth/login"
	limit := effectiveBodyLimit(cfg, path)
	host := strings.TrimPrefix(gateway.URL, "http://")

	raw := fmt.Sprintf(
		"POST %s HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\nxxx",
		path, host, limit+1,
	)

	status := sendRawRequest(t, gateway.URL, raw)
	if !strings.Contains(status, "413") {
		t.Errorf("Content-Length annoncé (%d) au-dessus de la limite → réponse %q, want 413", limit+1, status)
	}
}

func TestAC3RangeHeaderIsPropagatedToBackend(t *testing.T) {
	cfg := loadProductionConfig(t)

	var seenRange string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	for i := range cfg.Routes {
		cfg.Routes[i].DestinationURL = backend.URL
		cfg.Routes[i].RequireAuth = false
	}
	cfg.AuthServiceURL = ""
	cfg.Server.RateLimit.Enabled = false

	handler, _, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/drive/files/1", nil)
	if err != nil {
		t.Fatalf("construction de la requête: %v", err)
	}
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("envoi de la requête: %v", err)
	}
	resp.Body.Close()

	if seenRange != "bytes=0-1023" {
		t.Errorf("Range reçu par le backend = %q, want \"bytes=0-1023\" (propagé)", seenRange)
	}
}
