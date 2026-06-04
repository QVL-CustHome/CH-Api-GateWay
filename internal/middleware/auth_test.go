package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

// authBackend simule le microservice d'authentification (Rust) et capture
// l'en-tête Authorization reçu.
func authBackend(t *testing.T, status int) (*httptest.Server, *string) {
	t.Helper()
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &receivedAuth
}

// serveAuth exécute une requête à travers le middleware d'auth et indique
// si le handler suivant (backend cible) a été appelé.
func serveAuth(t *testing.T, authURL, authorization string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/protected/data", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	AuthMiddleware(NewAuthClient(authURL), next).ServeHTTP(rec, req)
	return rec, nextCalled
}

// Scénario 1 — Token valide : le service d'auth reçoit le token, répond 200,
// la requête est transférée au backend cible.
func TestAuthValidToken(t *testing.T) {
	auth, receivedAuth := authBackend(t, http.StatusOK)

	rec, nextCalled := serveAuth(t, auth.URL, "Bearer token-valide")

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

// Scénario 2 — Token absent ou format incorrect : 401 direct, le service
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

			rec, nextCalled := serveAuth(t, auth.URL, tc.authorization)

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

// Scénario 3 — Token invalide ou expiré : l'erreur du service d'auth (401/403)
// est retransmise au client, le backend cible n'est jamais joint.
func TestAuthInvalidTokenForwardsAuthError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			auth, _ := authBackend(t, status)

			rec, nextCalled := serveAuth(t, auth.URL, "Bearer token-expire")

			if rec.Code != status {
				t.Errorf("statut = %d, want %d (retransmis du service d'auth)", rec.Code, status)
			}
			if nextCalled {
				t.Error("la requête ne doit pas atteindre le backend avec un token rejeté")
			}
		})
	}
}

// Scénario 4 — Service d'authentification injoignable : 503 au client.
func TestAuthServiceUnreachable(t *testing.T) {
	// Serveur fermé immédiatement : connexion refusée.
	auth := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := auth.URL
	auth.Close()

	rec, nextCalled := serveAuth(t, url, "Bearer token")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("statut = %d, want 503", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend si l'auth est indisponible")
	}
}

// Scénario 4 (bis) — Service d'authentification trop lent : timeout → 503.
func TestAuthServiceTimeout(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(authTimeout + 200*time.Millisecond) // dépasse le timeout du client
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(auth.Close)

	rec, nextCalled := serveAuth(t, auth.URL, "Bearer token")

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
	rec, nextCalled := serveAuth(t, "http://invalid%zz", "Bearer token")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("statut = %d, want 500", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend")
	}
}

// Réponse inattendue du service d'auth (ex: 500) : rien ne passe, 503.
func TestAuthServiceUnexpectedStatus(t *testing.T) {
	auth, _ := authBackend(t, http.StatusInternalServerError)

	rec, nextCalled := serveAuth(t, auth.URL, "Bearer token")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("statut = %d, want 503", rec.Code)
	}
	if nextCalled {
		t.Error("la requête ne doit pas atteindre le backend sur une réponse d'auth inattendue")
	}
}
