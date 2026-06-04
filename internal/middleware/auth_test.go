package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const validAuthJSON = `{"user_id":"123e4567-e89b-12d3-a456-426614174000","role":"admin"}`

// Portail utilisé par défaut dans les tests du middleware (US-09).
const testPortal = "portail_test"

// Cookie porteur du token dans les tests (US-11).
const testCookieName = "ch_token"

func TestExtractBearerToken(t *testing.T) {
	cases := []struct {
		name      string
		header    string
		wantToken string
		wantErr   error
	}{
		{"en-tête absent", "", "", ErrMissingAuthHeader},
		{"mauvais schéma Basic", "Basic 1234", "", ErrInvalidAuthFormat},
		{"token brut sans schéma", "abc123", "", ErrInvalidAuthFormat},
		{"bearer en minuscules", "bearer abc123", "", ErrInvalidAuthFormat},
		{"Bearer sans token", "Bearer ", "", ErrInvalidAuthFormat},
		{"Bearer avec espaces seuls", "Bearer    ", "", ErrInvalidAuthFormat},
		{"token valide", "Bearer abc123", "abc123", nil},
		{"token JWT valide", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig", "eyJhbGciOiJIUzI1NiJ9.payload.sig", nil},
		{"espaces multiples avant le token", "Bearer   abc123", "abc123", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			token, err := extractBearerToken(req)

			if !errors.Is(err, tc.wantErr) {
				t.Errorf("erreur = %v, want %v", err, tc.wantErr)
			}
			if token != tc.wantToken {
				t.Errorf("token = %q, want %q", token, tc.wantToken)
			}
		})
	}
}

func authBackend(t *testing.T, status int, body string) (*httptest.Server, *string) {
	srv, receivedAuth, _ := authBackendWithPortal(t, status, body)
	return srv, receivedAuth
}

func authBackendWithPortal(t *testing.T, status int, body string) (*httptest.Server, *string, *string) {
	t.Helper()
	var receivedAuth, receivedPortal string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedPortal = r.Header.Get(HeaderPortal)
		w.WriteHeader(status)
		if body != "" {
			w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &receivedAuth, &receivedPortal
}

func serveAuth(t *testing.T, authURL, authorization string, extraHeaders map[string]string) (*httptest.ResponseRecorder, bool, http.Header) {
	t.Helper()
	nextCalled := false
	var nextHeaders http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		nextHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/protected/data", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	AuthMiddleware(NewAuthClient(authURL, 100*time.Millisecond, testCookieName), testPortal, next).ServeHTTP(rec, req)
	return rec, nextCalled, nextHeaders
}

// US-11 : matrice cookie/header — le header prime, le cookie est le fallback.
func TestAuthTokenFromCookieOrHeader(t *testing.T) {
	cases := []struct {
		name          string
		authorization string
		cookie        string
		wantStatus    int
		wantAuthSent  string // Authorization attendu côté service d'auth
	}{
		{"cookie seul", "", "token-cookie", http.StatusOK, "Bearer token-cookie"},
		{"header seul", "Bearer token-header", "", http.StatusOK, "Bearer token-header"},
		{"les deux : le header prime", "Bearer token-header", "token-cookie", http.StatusOK, "Bearer token-header"},
		{"aucun des deux", "", "", http.StatusUnauthorized, ""},
		{"cookie vide", "", "   ", http.StatusUnauthorized, ""},
		{"header malformé : pas de repli sur le cookie", "Basic abc", "token-cookie", http.StatusUnauthorized, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, receivedAuth := authBackend(t, http.StatusOK, validAuthJSON)

			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			req := httptest.NewRequest(http.MethodGet, "/api/protected/data", nil)
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: testCookieName, Value: tc.cookie})
			}
			rec := httptest.NewRecorder()
			AuthMiddleware(NewAuthClient(auth.URL, 100*time.Millisecond, testCookieName), testPortal, next).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("statut = %d, want %d", rec.Code, tc.wantStatus)
			}
			if *receivedAuth != tc.wantAuthSent {
				t.Errorf("Authorization envoyé au service d'auth = %q, want %q", *receivedAuth, tc.wantAuthSent)
			}
		})
	}
}

// US-11 : un autre cookie que celui configuré ne donne jamais accès.
func TestAuthIgnoresOtherCookies(t *testing.T) {
	auth, receivedAuth := authBackend(t, http.StatusOK, validAuthJSON)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/protected/data", nil)
	req.AddCookie(&http.Cookie{Name: "session_marketing", Value: "token-pirate"})
	rec := httptest.NewRecorder()
	AuthMiddleware(NewAuthClient(auth.URL, 100*time.Millisecond, testCookieName), testPortal, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("statut = %d, want 401", rec.Code)
	}
	if *receivedAuth != "" {
		t.Errorf("le service d'auth a été appelé avec %q, want aucun appel", *receivedAuth)
	}
}

// US-09 : le portail de la route est transmis au service d'auth via X-Portal.
func TestAuthSendsPortalHeader(t *testing.T) {
	auth, _, receivedPortal := authBackendWithPortal(t, http.StatusOK, validAuthJSON)

	_, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-valide", nil)

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if *receivedPortal != testPortal {
		t.Errorf("X-Portal reçu par le service d'auth = %q, want %q", *receivedPortal, testPortal)
	}
}

// US-10 : l'IP client résolue (contexte IPExtractor) est transmise au /validate.
func TestAuthSendsClientIP(t *testing.T) {
	var receivedClientIP string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedClientIP = r.Header.Get(HeaderClientIP)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validAuthJSON))
	}))
	t.Cleanup(auth.Close)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	extractor, err := NewIPExtractor(nil)
	if err != nil {
		t.Fatalf("NewIPExtractor: %v", err)
	}
	handler := extractor.Middleware(
		AuthMiddleware(NewAuthClient(auth.URL, 100*time.Millisecond, testCookieName), testPortal, next),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/protected/data", nil)
	req.Header.Set("Authorization", "Bearer token-valide")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut = %d, want 200", rec.Code)
	}
	// httptest.NewRequest fixe RemoteAddr à 192.0.2.1:1234.
	if receivedClientIP != "192.0.2.1" {
		t.Errorf("X-Client-IP reçu par le service d'auth = %q, want 192.0.2.1", receivedClientIP)
	}
}

// US-10 : sans IP résolue en contexte, aucun X-Client-IP n'est envoyé au /validate.
func TestAuthOmitsClientIPWithoutContext(t *testing.T) {
	clientIPSent := false
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, clientIPSent = r.Header[http.CanonicalHeaderKey(HeaderClientIP)]
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validAuthJSON))
	}))
	t.Cleanup(auth.Close)

	_, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-valide", nil)

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if clientIPSent {
		t.Error("X-Client-IP ne doit pas être envoyé sans IP résolue en contexte")
	}
}

// US-09 : un X-Portal forgé par le client ne doit jamais remplacer celui de la route.
func TestSpoofedPortalHeaderIsIgnored(t *testing.T) {
	auth, _, receivedPortal := authBackendWithPortal(t, http.StatusOK, validAuthJSON)
	forged := map[string]string{HeaderPortal: "portail-forge"}

	_, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-valide", forged)

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if *receivedPortal != testPortal {
		t.Errorf("X-Portal reçu = %q : le portail forgé par le client a fuité (want %q)", *receivedPortal, testPortal)
	}
}

func TestAuthValidToken(t *testing.T) {
	auth, receivedAuth := authBackend(t, http.StatusOK, validAuthJSON)

	rec, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-valide", nil)

	if !nextCalled {
		t.Error("la requête authentifiée doit être transférée au backend")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("statut = %d, want 200", rec.Code)
	}
	if *receivedAuth != "Bearer token-valide" {
		t.Errorf("Authorization reçu par le service d'auth = %q, want Bearer token-valide", *receivedAuth)
	}
}

func TestAuthMissingOrMalformedToken(t *testing.T) {
	cases := []struct {
		name          string
		authorization string
	}{
		{"en-tête absent", ""},
		{"sans schéma Bearer", "token-brut"},
		{"mauvais schéma", "Basic dXNlcjpwYXNz"},
		{"Bearer sans token", "Bearer "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authCalled := false
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				authCalled = true
			}))
			t.Cleanup(auth.Close)

			rec, nextCalled, _ := serveAuth(t, auth.URL, tc.authorization, nil)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("statut = %d, want 401", rec.Code)
			}
			if authCalled {
				t.Error("le service d'auth ne doit pas être appelé sans token exploitable")
			}
			if nextCalled {
				t.Error("la requête ne doit pas atteindre le backend")
			}
		})
	}
}

func TestAuthInvalidTokenForwardsAuthError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			auth, _ := authBackend(t, status, "")

			rec, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-expire", nil)

			if rec.Code != status {
				t.Errorf("statut = %d, want %d (retransmis du service d'auth)", rec.Code, status)
			}
			if nextCalled {
				t.Error("la requête ne doit pas atteindre le backend avec un token rejeté")
			}
		})
	}
}

func TestAuthServiceUnreachable(t *testing.T) {

	auth := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := auth.URL
	auth.Close()

	rec, nextCalled, _ := serveAuth(t, url, "Bearer token", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("statut = %d, want 503", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend si l'auth est indisponible")
	}
}

func TestAuthServiceTimeout(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(auth.Close)

	rec, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("statut = %d, want 503", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend après un timeout d'auth")
	}
}

func TestAuthInvalidAuthURL(t *testing.T) {
	rec, nextCalled, _ := serveAuth(t, "http://invalid%zz", "Bearer token", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("statut = %d, want 500", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend")
	}
}

func TestAuthServiceUnexpectedStatus(t *testing.T) {
	auth, _ := authBackend(t, http.StatusInternalServerError, "")

	rec, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("statut = %d, want 503", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend sur une réponse d'auth inattendue")
	}
}

func TestUserContextInjection(t *testing.T) {
	auth, _ := authBackend(t, http.StatusOK, validAuthJSON)

	_, nextCalled, nextHeaders := serveAuth(t, auth.URL, "Bearer token-valide", nil)

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if got := nextHeaders.Get(HeaderUserID); got != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("X-User-Id = %q, want 123e4567-e89b-12d3-a456-426614174000", got)
	}
	if got := nextHeaders.Get(HeaderUserRole); got != "admin" {
		t.Errorf("X-User-Role = %q, want admin", got)
	}
}

func TestSpoofedUserHeadersAreOverwritten(t *testing.T) {
	auth, _ := authBackend(t, http.StatusOK, validAuthJSON)
	forged := map[string]string{
		HeaderUserID:   "admin-forge",
		HeaderUserRole: "super-admin",
	}

	_, nextCalled, nextHeaders := serveAuth(t, auth.URL, "Bearer token-valide", forged)

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if got := nextHeaders.Get(HeaderUserID); got != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("X-User-Id = %q : la valeur forgée n'a pas été écrasée", got)
	}
	if got := nextHeaders.Get(HeaderUserRole); got != "admin" {
		t.Errorf("X-User-Role = %q : la valeur forgée n'a pas été écrasée", got)
	}
	if vals := nextHeaders.Values(HeaderUserID); len(vals) != 1 {
		t.Errorf("X-User-Id a %d valeurs (%v), want 1 seule", len(vals), vals)
	}
}

func TestSpoofedUserHeadersWithoutToken(t *testing.T) {
	auth, _ := authBackend(t, http.StatusOK, validAuthJSON)
	forged := map[string]string{HeaderUserID: "admin-forge"}

	rec, nextCalled, _ := serveAuth(t, auth.URL, "", forged)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("statut = %d, want 401", rec.Code)
	}
	if nextCalled {
		t.Error("la requête forgée sans token ne doit pas atteindre le backend")
	}
}

func TestMalformedAuthResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"JSON invalide", `{"user_id": pas-du-json`},
		{"corps vide", ""},
		{"user_id manquant", `{"role":"admin"}`},
		{"user_id vide", `{"user_id":"","role":"admin"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, _ := authBackend(t, http.StatusOK, tc.body)

			rec, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-valide", nil)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("statut = %d, want 500", rec.Code)
			}
			if nextCalled {
				t.Error("la requête ne doit pas être transmise au backend sur une réponse d'auth illisible")
			}
		})
	}
}

func TestRoleAbsentOmitsRoleHeader(t *testing.T) {
	auth, _ := authBackend(t, http.StatusOK, `{"user_id":"user-1"}`)
	forged := map[string]string{HeaderUserRole: "super-admin"}

	_, nextCalled, nextHeaders := serveAuth(t, auth.URL, "Bearer token-valide", forged)

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if got := nextHeaders.Get(HeaderUserID); got != "user-1" {
		t.Errorf("X-User-Id = %q, want user-1", got)
	}
	if got := nextHeaders.Get(HeaderUserRole); got != "" {
		t.Errorf("X-User-Role = %q, want absent", got)
	}
}
