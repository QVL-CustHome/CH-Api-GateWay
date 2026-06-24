package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
)

func TestBudgyRouteStripsPrefixToBackend(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	protect := func(_ string, next http.Handler) http.Handler {
		return next
	}
	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/budgy", DestinationURL: backend.URL, StripPrefix: true, RequireAuth: true, Portal: "portail_budgy"},
	}}
	cfg.Server.Port = 8080
	cfg.Server.TimeoutSeconds = config.DefaultTimeoutSeconds
	router, err := NewRouter(cfg, protect)
	if err != nil {
		t.Fatalf("NewRouter() erreur inattendue: %v", err)
	}
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/budgy/health")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	resp.Body.Close()

	if captured.Path != "/health" {
		t.Errorf("path reçu par le backend budgy = %q, want /health (prefix strippé)", captured.Path)
	}
}

func TestBudgyRouteTransmitsPortalAndProtects(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")

	var transmittedPortal string
	protectCalls := 0
	protect := func(portal string, next http.Handler) http.Handler {
		transmittedPortal = portal
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			protectCalls++
			if r.Header.Get("Authorization") == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/budgy", DestinationURL: backend.URL, StripPrefix: true, RequireAuth: true, Portal: "portail_budgy"},
	}}
	cfg.Server.Port = 8080
	cfg.Server.TimeoutSeconds = config.DefaultTimeoutSeconds
	router, err := NewRouter(cfg, protect)
	if err != nil {
		t.Fatalf("NewRouter() erreur inattendue: %v", err)
	}
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	if transmittedPortal != "portail_budgy" {
		t.Errorf("portail transmis au middleware = %q, want portail_budgy", transmittedPortal)
	}

	resp, err := http.Get(gateway.URL + "/api/budgy/accounts")
	if err != nil {
		t.Fatalf("requête sans token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("statut sans token = %d, want 401 (route protégée)", resp.StatusCode)
	}
	if captured.Path != "" {
		t.Errorf("le backend budgy ne devait pas être atteint sans token, a reçu %q", captured.Path)
	}

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/api/budgy/accounts", nil)
	req.Header.Set("Authorization", "Bearer token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requête avec token: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("statut avec token = %d, want 200", resp2.StatusCode)
	}
	if captured.Path != "/accounts" {
		t.Errorf("backend budgy a reçu %q, want /accounts", captured.Path)
	}
}
