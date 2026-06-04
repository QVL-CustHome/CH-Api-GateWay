package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
	"github.com/custhome/ch-api-gateway/internal/middleware"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func testConfig(routes ...config.RouteConfig) *config.GatewayConfig {
	cfg := &config.GatewayConfig{Routes: routes}
	cfg.Server.Port = 8080
	cfg.Server.TimeoutSeconds = 5
	cfg.Server.MaxBodyBytes = 1 << 20
	cfg.AuthServiceTimeoutMs = 100
	return cfg
}

func TestBuildHandlerHealthAndNotFound(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	handler, onShutdown, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	if len(onShutdown) != 0 {
		t.Errorf("onShutdown = %d hooks, want 0 sans rate limiting", len(onShutdown))
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatalf("requête /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut /health = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get(middleware.CorrelationHeader) == "" {
		t.Error("X-Correlation-ID absent de la réponse")
	}

	resp, err = http.Get(gateway.URL + "/inconnu")
	if err != nil {
		t.Fatalf("requête /inconnu: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("statut /inconnu = %d, want 404", resp.StatusCode)
	}
}

func TestBuildHandlerPipelineEndToEnd(t *testing.T) {
	var seenCorrelation, seenUserID string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCorrelation = r.Header.Get(middleware.CorrelationHeader)
		seenUserID = r.Header.Get(middleware.HeaderUserID)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: backend.URL})
	handler, _, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/api/users/42", nil)
	req.Header.Set(middleware.HeaderUserID, "admin-forge")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut = %d, want 200", resp.StatusCode)
	}
	if seenCorrelation == "" {
		t.Error("le backend n'a pas reçu de X-Correlation-ID")
	}
	if seenUserID != "" {
		t.Errorf("le backend a reçu X-User-Id = %q, want supprimé", seenUserID)
	}
}

func TestBuildHandlerRateLimitWithExemptHealth(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	cfg.Server.RateLimit.Enabled = true
	cfg.Server.RateLimit.RequestsPerSecond = 1
	cfg.Server.RateLimit.Burst = 1

	handler, onShutdown, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	if len(onShutdown) != 1 {
		t.Fatalf("onShutdown = %d hooks, want 1 avec rate limiting", len(onShutdown))
	}
	t.Cleanup(onShutdown[0])
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	throttled := false
	for i := 0; i < 3; i++ {
		resp, err := http.Get(gateway.URL + "/inconnu")
		if err != nil {
			t.Fatalf("requête: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			throttled = true
		}
	}
	if !throttled {
		t.Error("le rate limiting du pipeline n'a pas throttlé")
	}

	resp, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatalf("requête /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut /health = %d, want 200 même sous throttling", resp.StatusCode)
	}
}

func TestBuildHandlerProtectedRouteWiresAuth(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/protected", DestinationURL: "http://localhost:1", RequireAuth: true, Portal: "portail_test"})
	cfg.AuthServiceURL = "http://localhost:1/validate"

	handler, _, err := BuildHandler(cfg, testLogger())
	if err != nil {
		t.Fatalf("BuildHandler(): %v", err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/protected/data")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("statut sans token = %d, want 401 (middleware d'auth câblé)", resp.StatusCode)
	}
}

func TestBuildHandlerErrorProtectedRouteWithoutAuthURL(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/protected", DestinationURL: "http://localhost:1", RequireAuth: true, Portal: "portail_test"})

	if _, _, err := BuildHandler(cfg, testLogger()); err == nil {
		t.Fatal("BuildHandler() devrait échouer : route protégée sans auth_service_url")
	}
}

func TestBuildHandlerErrorInvalidTrustedProxy(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://localhost:1"})
	cfg.Server.RateLimit.TrustedProxies = []string{"pas-une-ip"}

	if _, _, err := BuildHandler(cfg, testLogger()); err == nil {
		t.Fatal("BuildHandler() devrait échouer sur un trusted proxy invalide")
	}
}

func TestBuildHandlerErrorInvalidRoute(t *testing.T) {
	cfg := testConfig(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: "http://invalid%zz"})

	if _, _, err := BuildHandler(cfg, testLogger()); err == nil {
		t.Fatal("BuildHandler() devrait échouer sur une destination invalide")
	}
}
