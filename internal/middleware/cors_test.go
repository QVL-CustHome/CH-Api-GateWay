package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
)

func testCORSConfig() config.CORSConfig {
	return config.CORSConfig{
		AllowedOrigins: []string{"http://localhost:3000", "https://mon-app-front.com"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type", "X-Correlation-ID"},
	}
}

func serve(t *testing.T, cfg config.CORSConfig, method, origin string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(method, "/api/users/profile", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	CORSMiddleware(cfg, next).ServeHTTP(rec, req)
	return rec, nextCalled
}

func TestPreflightAllowedOrigin(t *testing.T) {
	rec, nextCalled := serve(t, testCORSConfig(), http.MethodOptions, "http://localhost:3000")

	if rec.Code != http.StatusNoContent {
		t.Errorf("statut = %d, want 204", rec.Code)
	}
	if nextCalled {
		t.Error("la requête OPTIONS ne doit jamais être transmise au backend")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Allow-Origin = %q, want http://localhost:3000", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, DELETE, OPTIONS" {
		t.Errorf("Allow-Methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, X-Correlation-ID" {
		t.Errorf("Allow-Headers = %q", got)
	}
}

func TestStandardRequestAllowedOrigin(t *testing.T) {
	rec, nextCalled := serve(t, testCORSConfig(), http.MethodGet, "https://mon-app-front.com")

	if !nextCalled {
		t.Error("la requête standard doit être transmise au handler suivant")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("statut = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://mon-app-front.com" {
		t.Errorf("Allow-Origin = %q, want https://mon-app-front.com", got)
	}
}

func TestDisallowedOriginGetsNoAllowOrigin(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			rec, _ := serve(t, testCORSConfig(), method, "https://evil.com")
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Allow-Origin = %q, want vide pour une origine non autorisée", got)
			}
		})
	}
}

func TestDisallowedOriginStillForwarded(t *testing.T) {
	_, nextCalled := serve(t, testCORSConfig(), http.MethodGet, "https://evil.com")
	if !nextCalled {
		t.Error("la requête standard doit être transmise même si l'origine n'est pas autorisée")
	}
}

func TestNoOriginHeader(t *testing.T) {
	rec, nextCalled := serve(t, testCORSConfig(), http.MethodGet, "")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want vide sans en-tête Origin", got)
	}
	if !nextCalled {
		t.Error("la requête sans Origin doit être transmise normalement")
	}
}

func TestWildcardOrigin(t *testing.T) {
	cfg := testCORSConfig()
	cfg.AllowedOrigins = []string{"*"}
	rec, _ := serve(t, cfg, http.MethodGet, "https://nimporte-ou.com")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want *", got)
	}
}

func TestVaryOriginHeader(t *testing.T) {
	rec, _ := serve(t, testCORSConfig(), http.MethodGet, "http://localhost:3000")
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

func TestEmptyCORSConfig(t *testing.T) {
	rec, nextCalled := serve(t, config.CORSConfig{}, http.MethodGet, "http://localhost:3000")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want vide", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Allow-Methods = %q, want vide", got)
	}
	if !nextCalled {
		t.Error("la requête doit être transmise normalement")
	}
}
