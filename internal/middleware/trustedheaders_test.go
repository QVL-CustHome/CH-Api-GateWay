package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripUntrustedHeadersRemovesForgedUserHeaders(t *testing.T) {
	var seenUserID, seenUserRole, seenCustom string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUserID = r.Header.Get(HeaderUserID)
		seenUserRole = r.Header.Get(HeaderUserRole)
		seenCustom = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/public/info", nil)
	req.Header.Set(HeaderUserID, "admin-forge")
	req.Header.Set(HeaderUserRole, "super-admin")
	req.Header.Set("X-Custom", "conserve")
	rec := httptest.NewRecorder()
	StripUntrustedHeadersMiddleware(next).ServeHTTP(rec, req)

	if seenUserID != "" {
		t.Errorf("X-User-Id = %q, want supprimé", seenUserID)
	}
	if seenUserRole != "" {
		t.Errorf("X-User-Role = %q, want supprimé", seenUserRole)
	}
	if seenCustom != "conserve" {
		t.Errorf("X-Custom = %q, les autres en-têtes ne doivent pas être touchés", seenCustom)
	}
}

func TestAuthPropagatesCorrelationID(t *testing.T) {
	var receivedCorrelation string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCorrelation = r.Header.Get(CorrelationHeader)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validAuthJSON))
	}))
	t.Cleanup(auth.Close)

	_, nextCalled, _ := serveAuth(t, auth.URL, "Bearer token-valide", map[string]string{
		CorrelationHeader: "trace-42",
	})

	if !nextCalled {
		t.Fatal("la requête authentifiée doit être transférée au backend")
	}
	if receivedCorrelation != "trace-42" {
		t.Errorf("X-Correlation-ID reçu par le service d'auth = %q, want trace-42", receivedCorrelation)
	}
}
