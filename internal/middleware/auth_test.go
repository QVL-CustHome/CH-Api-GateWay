package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const validAuthJSON = `{"user_id":"123e4567-e89b-12d3-a456-426614174000","role":"admin"}`

// US-06 — validation syntaxique locale de l'en-tête Authorization.
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

// authBackend simule le microservice d'authentification (Rust) : il capture
// l'en-tête Authorization reçu et répond avec le statut et le corps donnés.
func authBackend(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		if body != "" {
			w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &receivedAuth
}

// serveAuth exécute une requête à travers le middleware d'auth.
// extraHeaders est appliqué à la requête entrante (ex: en-têtes forgés).
// Retourne la réponse, si le handler suivant a été appelé, et les en-têtes
// de la requête tels que vus par ce handler (le backend cible).
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
	AuthMiddleware(NewAuthClient(authURL), next).ServeHTTP(rec, req)
	return rec, nextCalled, nextHeaders
}

// US-05 Scénario 1 — Token valide : le service d'auth reçoit le token,
// répond 200, la requête est transférée au backend cible.
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

// US-05 Scénario 2 — Token absent ou format incorrect : 401 direct, le service
// d'authentification n'est jamais appelé.
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

// US-05 Scénario 3 — Token invalide ou expiré : l'erreur du service d'auth
// (401/403) est retransmise au client, le backend cible n'est jamais joint.
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

// US-05 Scénario 4 — Service d'authentification injoignable : 503 au client.
func TestAuthServiceUnreachable(t *testing.T) {
	// Serveur fermé immédiatement : connexion refusée.
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

// US-05 Scénario 4 (bis) — Service d'authentification trop lent : timeout → 503.
func TestAuthServiceTimeout(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(authTimeout + 200*time.Millisecond) // dépasse le timeout du client
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

// URL d'auth inconstructible (cas défensif, normalement bloqué par la
// validation de la config) : 500 sans atteindre le backend.
func TestAuthInvalidAuthURL(t *testing.T) {
	rec, nextCalled, _ := serveAuth(t, "http://invalid%zz", "Bearer token", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("statut = %d, want 500", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend")
	}
}

// Réponse inattendue du service d'auth (ex: 500) : rien ne passe, 503.
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

// US-07 Scénario 1 — Injection du contexte utilisateur : le backend reçoit
// X-User-Id et X-User-Role issus de la réponse du service d'auth.
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

// US-07 Scénario 2 — Anti-spoofing : les en-têtes X-User-* forgés par le
// client sont écrasés, seules les valeurs légitimes atteignent le backend.
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

// US-07 Scénario 2 (bis) — En-têtes forgés sans token valide : la requête
// est rejetée et n'atteint jamais le backend.
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

// US-07 Scénario 3 — Réponse 200 du service d'auth mais JSON invalide ou
// incomplet : 500, la requête n'est pas transmise au backend.
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

// US-07 — Rôle absent de la réponse : X-User-Id injecté, X-User-Role omis
// (et jamais une valeur forgée résiduelle).
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
