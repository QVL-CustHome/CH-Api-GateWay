package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
)

// capturedRequest mémorise ce que le faux backend a réellement reçu.
type capturedRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
	Header http.Header
}

// newBackend monte un faux microservice qui capture la requête reçue et
// renvoie une réponse contrôlée.
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

// newGatewayRouter construit le routeur du gateway avec les routes données,
// sans middleware d'authentification.
func newGatewayRouter(t *testing.T, routes []config.RouteConfig) http.Handler {
	t.Helper()
	cfg := &config.GatewayConfig{Routes: routes}
	cfg.Server.Port = 8080
	router, err := NewRouter(cfg, nil)
	if err != nil {
		t.Fatalf("NewRouter() erreur inattendue: %v", err)
	}
	return router
}

// Scénario 1 — Correspondance de route : méthode, path, query et body préservés.
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

// Scénario 2 — La réponse du backend (statut, headers, body) est retransmise telle quelle.
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

// Scénario 3 — Aucune route correspondante : 404 direct, aucun appel backend.
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

// Le préfixe exact (sans slash final) est lui aussi routé.
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

// Le routage distingue les préfixes et atteint le bon microservice.
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

// US-03 Scénario 1 — strip_prefix: true : le préfixe est supprimé avant transfert,
// le reste de la requête (query, body, headers) est inchangé.
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

// US-03 Scénario 2 — strip_prefix sur la racine exacte : "" devient "/".
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

// US-03 Scénario 3 — strip_prefix absent ou false (valeur zéro Go) :
// path transmis tel quel.
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

// US-05 — le décorateur d'authentification n'est appliqué qu'aux routes
// require_auth: true ; les routes publiques restent directes.
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

	// Route publique : aucun passage par le middleware d'auth.
	if _, err := http.Get(gateway.URL + "/api/public/info"); err != nil {
		t.Fatalf("requête publique: %v", err)
	}
	if protectCalls != 0 {
		t.Errorf("le middleware d'auth ne doit pas intercepter une route publique (appels: %d)", protectCalls)
	}
	if publicCaptured.Path != "/api/public/info" {
		t.Errorf("backend public a reçu %q", publicCaptured.Path)
	}

	// Route protégée sans token : 401, backend jamais appelé.
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

	// Route protégée avec token : transférée au backend.
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

// US-05 — route protégée sans middleware fourni : erreur de construction.
func TestNewRouterProtectedRouteWithoutMiddleware(t *testing.T) {
	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/protected", DestinationURL: "http://localhost:8083", RequireAuth: true},
	}}
	cfg.Server.Port = 8080
	if _, err := NewRouter(cfg, nil); err == nil {
		t.Fatal("NewRouter() devrait échouer si une route require_auth n'a pas de middleware")
	}
}
