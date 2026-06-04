package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/custhome/ch-api-gateway/internal/config"
	"github.com/custhome/ch-api-gateway/internal/middleware"
)

type capturedRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
	Header http.Header
}

func newBackend(t *testing.T, status int, respBody string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("lecture du body côté backend: %v", err)
		}
		*captured = capturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Body:   string(body),
			Header: r.Header.Clone(),
		}
		w.Header().Set("X-Backend", "auth-service")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func newGatewayRouter(t *testing.T, routes []config.RouteConfig) http.Handler {
	t.Helper()
	cfg := &config.GatewayConfig{Routes: routes}
	cfg.Server.Port = 8080
	cfg.Server.TimeoutSeconds = config.DefaultTimeoutSeconds
	router, err := NewRouter(cfg, nil)
	if err != nil {
		t.Fatalf("NewRouter() erreur inattendue: %v", err)
	}
	return router
}

func TestProxyForwardsRequestToMatchingBackend(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, `{"token":"abc"}`)
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Post(
		gateway.URL+"/api/auth/login?remember=true&lang=fr",
		"application/json",
		strings.NewReader(`{"user":"martin"}`),
	)
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if captured.Method != http.MethodPost {
		t.Errorf("méthode reçue par le backend = %q, want POST", captured.Method)
	}
	if captured.Path != "/api/auth/login" {
		t.Errorf("path reçu par le backend = %q, want /api/auth/login", captured.Path)
	}
	if captured.Query != "remember=true&lang=fr" {
		t.Errorf("query reçue par le backend = %q, want remember=true&lang=fr", captured.Query)
	}
	if captured.Body != `{"user":"martin"}` {
		t.Errorf("body reçu par le backend = %q", captured.Body)
	}
	if ct := captured.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type reçu par le backend = %q, want application/json", ct)
	}
}

func TestProxyTransmitsBackendResponse(t *testing.T) {
	backend, _ := newBackend(t, http.StatusBadRequest, `{"error":"invalid credentials"}`)
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/auth/login")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("statut retransmis = %d, want 400", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Backend"); got != "auth-service" {
		t.Errorf("header X-Backend retransmis = %q, want auth-service", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type retransmis = %q, want application/json", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"error":"invalid credentials"}` {
		t.Errorf("body retransmis = %q", string(body))
	}
}

func TestRouterNotFoundWithoutCallingBackend(t *testing.T) {
	backendCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backendCalled = true
	}))
	t.Cleanup(backend.Close)

	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/unknown")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("statut = %d, want 404", resp.StatusCode)
	}
	if backendCalled {
		t.Error("le backend ne doit jamais être appelé quand aucune route ne correspond")
	}
}

func TestRouterMatchesExactPrefix(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut = %d, want 200", resp.StatusCode)
	}
	if captured.Path != "/api/users" {
		t.Errorf("path reçu par le backend = %q, want /api/users", captured.Path)
	}
}

func TestRouterDispatchesToCorrectBackend(t *testing.T) {
	authBackend, authCaptured := newBackend(t, http.StatusOK, "auth")
	usersBackend, usersCaptured := newBackend(t, http.StatusOK, "users")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: authBackend.URL},
		{PathPrefix: "/api/users", DestinationURL: usersBackend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	if _, err := http.Get(gateway.URL + "/api/users/42"); err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}

	if usersCaptured.Path != "/api/users/42" {
		t.Errorf("path reçu par le backend users = %q, want /api/users/42", usersCaptured.Path)
	}
	if authCaptured.Path != "" {
		t.Errorf("le backend auth ne devait pas être appelé, a reçu %q", authCaptured.Path)
	}
}

func TestStripPrefixEnabled(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL, StripPrefix: true},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Post(
		gateway.URL+"/api/users/profile?fields=name",
		"application/json",
		strings.NewReader(`{"id":42}`),
	)
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if captured.Path != "/profile" {
		t.Errorf("path reçu par le backend = %q, want /profile", captured.Path)
	}
	if captured.Query != "fields=name" {
		t.Errorf("query reçue par le backend = %q, want fields=name", captured.Query)
	}
	if captured.Body != `{"id":42}` {
		t.Errorf("body reçu par le backend = %q", captured.Body)
	}
	if ct := captured.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type reçu par le backend = %q, want application/json", ct)
	}
}

func TestStripPrefixOnExactRootBecomesSlash(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL, StripPrefix: true},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if captured.Path != "/" {
		t.Errorf("path reçu par le backend = %q, want /", captured.Path)
	}
}

func TestStripPrefixDisabledKeepsFullPath(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/auth/login")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	resp.Body.Close()

	if captured.Path != "/api/auth/login" {
		t.Errorf("path reçu par le backend = %q, want /api/auth/login", captured.Path)
	}
}

func TestTimeoutFastBackendPasses(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	t.Cleanup(backend.Close)

	proxyHandler, err := NewProxyHandler(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: backend.URL})
	if err != nil {
		t.Fatalf("NewProxyHandler(): %v", err)
	}
	gateway := httptest.NewServer(TimeoutMiddleware(500*time.Millisecond, proxyHandler))
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("statut = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", string(body))
	}
}

func TestTimeoutSlowBackendReturns504(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	proxyHandler, err := NewProxyHandler(config.RouteConfig{PathPrefix: "/api/users", DestinationURL: backend.URL})
	if err != nil {
		t.Fatalf("NewProxyHandler(): %v", err)
	}
	gateway := httptest.NewServer(TimeoutMiddleware(100*time.Millisecond, proxyHandler))
	t.Cleanup(gateway.Close)

	start := time.Now()
	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("statut = %d, want 504", resp.StatusCode)
	}

	if elapsed := time.Since(start); elapsed >= 450*time.Millisecond {
		t.Errorf("réponse en %v : le client n'a pas été libéré à l'échéance du timeout", elapsed)
	}
}

func TestBackendDownReturns502Immediately(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	backendURL := backend.URL
	backend.Close()

	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backendURL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	start := time.Now()
	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("statut = %d, want 502", resp.StatusCode)
	}

	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Errorf("réponse en %v : le 502 aurait dû être immédiat", elapsed)
	}
}

func TestRouterAppliesConfiguredTimeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL},
	}}
	cfg.Server.Port = 8080
	cfg.Server.TimeoutSeconds = 1
	router, err := NewRouter(cfg, nil)
	if err != nil {
		t.Fatalf("NewRouter(): %v", err)
	}
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("statut = %d, want 504", resp.StatusCode)
	}
}

func TestStripPrefixWithDestinationBasePath(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL + "/v1", StripPrefix: true},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/users/profile")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	resp.Body.Close()

	if captured.Path != "/v1/profile" {
		t.Errorf("path reçu par le backend = %q, want /v1/profile", captured.Path)
	}
}

func TestNoStripPrefixWithDestinationBasePath(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL + "/v1"},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/users/profile")
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	resp.Body.Close()

	if captured.Path != "/v1/api/users/profile" {
		t.Errorf("path reçu par le backend = %q, want /v1/api/users/profile", captured.Path)
	}
}

func TestProxySetsXForwardedHeaders(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/api/users", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	resp.Body.Close()

	if got := captured.Header.Get("X-Forwarded-For"); got != "127.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want 127.0.0.1 (le XFF entrant forgé est remplacé)", got)
	}
	if got := captured.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want http", got)
	}
	wantHost := strings.TrimPrefix(gateway.URL, "http://")
	if got := captured.Header.Get("X-Forwarded-Host"); got != wantHost {
		t.Errorf("X-Forwarded-Host = %q, want %q", got, wantHost)
	}
}

func TestPublicRouteStripsForgedUserHeaders(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/public", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(middleware.StripUntrustedHeadersMiddleware(router))
	t.Cleanup(gateway.Close)

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/api/public/info", nil)
	req.Header.Set(middleware.HeaderUserID, "admin-forge")
	req.Header.Set(middleware.HeaderUserRole, "super-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	resp.Body.Close()

	if got := captured.Header.Get(middleware.HeaderUserID); got != "" {
		t.Errorf("le backend public a reçu X-User-Id = %q, want supprimé", got)
	}
	if got := captured.Header.Get(middleware.HeaderUserRole); got != "" {
		t.Errorf("le backend public a reçu X-User-Role = %q, want supprimé", got)
	}
}

func TestProxyOversizedChunkedBodyReturns413(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/users", DestinationURL: backend.URL},
	})
	gateway := httptest.NewServer(middleware.MaxBodyBytesMiddleware(10, router))
	t.Cleanup(gateway.Close)

	body := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 100))}
	resp, err := http.Post(gateway.URL+"/api/users", "text/plain", body)
	if err != nil {
		t.Fatalf("requête: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("statut = %d, want 413", resp.StatusCode)
	}
}

func TestNewProxyHandlerInvalidURL(t *testing.T) {
	route := config.RouteConfig{PathPrefix: "/api/auth", DestinationURL: "http://invalid%zz"}
	if _, err := NewProxyHandler(route); err == nil {
		t.Fatal("NewProxyHandler() devrait échouer sur une URL incohérente")
	}
}

func TestNewRouterInvalidDestination(t *testing.T) {
	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: "http://invalid%zz"},
	}}
	cfg.Server.Port = 8080
	if _, err := NewRouter(cfg, nil); err == nil {
		t.Fatal("NewRouter() devrait échouer sur une destination invalide")
	}
}

func TestRouterAppliesProtectionOnlyToProtectedRoutes(t *testing.T) {
	publicBackend, publicCaptured := newBackend(t, http.StatusOK, "public")
	protectedBackend, protectedCaptured := newBackend(t, http.StatusOK, "protected")

	protectCalls := 0
	protect := func(next http.Handler) http.Handler {
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
		{PathPrefix: "/api/public", DestinationURL: publicBackend.URL},
		{PathPrefix: "/api/protected", DestinationURL: protectedBackend.URL, RequireAuth: true},
	}}
	cfg.Server.Port = 8080
	router, err := NewRouter(cfg, protect)
	if err != nil {
		t.Fatalf("NewRouter() erreur inattendue: %v", err)
	}
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	if _, err := http.Get(gateway.URL + "/api/public/info"); err != nil {
		t.Fatalf("requête publique: %v", err)
	}
	if protectCalls != 0 {
		t.Errorf("le middleware d'auth ne doit pas intercepter une route publique (appels: %d)", protectCalls)
	}
	if publicCaptured.Path != "/api/public/info" {
		t.Errorf("backend public a reçu %q", publicCaptured.Path)
	}

	resp, err := http.Get(gateway.URL + "/api/protected/data")
	if err != nil {
		t.Fatalf("requête protégée sans token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("statut sans token = %d, want 401", resp.StatusCode)
	}
	if protectedCaptured.Path != "" {
		t.Errorf("le backend protégé ne devait pas être appelé, a reçu %q", protectedCaptured.Path)
	}

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/api/protected/data", nil)
	req.Header.Set("Authorization", "Bearer token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requête protégée avec token: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("statut avec token = %d, want 200", resp2.StatusCode)
	}
	if protectedCaptured.Path != "/api/protected/data" {
		t.Errorf("backend protégé a reçu %q, want /api/protected/data", protectedCaptured.Path)
	}
}

func TestNewRouterProtectedRouteWithoutMiddleware(t *testing.T) {
	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/protected", DestinationURL: "http://localhost:8083", RequireAuth: true},
	}}
	cfg.Server.Port = 8080
	if _, err := NewRouter(cfg, nil); err == nil {
		t.Fatal("NewRouter() devrait échouer si une route require_auth n'a pas de middleware")
	}
}
