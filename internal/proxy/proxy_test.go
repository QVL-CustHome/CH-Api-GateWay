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

// newGatewayRouter construit le routeur du gateway avec les routes données.
func newGatewayRouter(t *testing.T, routes []config.RouteConfig) http.Handler {
	t.Helper()
	cfg := &config.GatewayConfig{Routes: routes}
	cfg.Server.Port = 8080
	router, err := NewRouter(cfg)
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

func TestNewProxyHandlerInvalidURL(t *testing.T) {
	if _, err := NewProxyHandler("http://invalid%zz"); err == nil {
		t.Fatal("NewProxyHandler() devrait échouer sur une URL incohérente")
	}
}

func TestNewRouterInvalidDestination(t *testing.T) {
	cfg := &config.GatewayConfig{Routes: []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: "http://invalid%zz"},
	}}
	cfg.Server.Port = 8080
	if _, err := NewRouter(cfg); err == nil {
		t.Fatal("NewRouter() devrait échouer sur une destination invalide")
	}
}
