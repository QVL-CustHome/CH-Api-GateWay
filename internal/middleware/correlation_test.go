package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func serveCorrelation(t *testing.T, incomingID string, nextStatus int) (rec *httptest.ResponseRecorder, forwardedID, contextID string) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedID = r.Header.Get(CorrelationHeader)
		contextID = CorrelationIDFromContext(r.Context())
		w.WriteHeader(nextStatus)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	if incomingID != "" {
		req.Header.Set(CorrelationHeader, incomingID)
	}
	rec = httptest.NewRecorder()
	CorrelationIDMiddleware(next).ServeHTTP(rec, req)
	return rec, forwardedID, contextID
}

func TestCorrelationIDGeneratedWhenAbsent(t *testing.T) {
	rec, forwardedID, contextID := serveCorrelation(t, "", http.StatusOK)

	parsed, err := uuid.Parse(forwardedID)
	if err != nil {
		t.Fatalf("l'ID propagé %q n'est pas un UUID valide: %v", forwardedID, err)
	}
	if parsed.Version() != 4 {
		t.Errorf("UUID version = %d, want 4", parsed.Version())
	}
	if contextID != forwardedID {
		t.Errorf("ID du contexte = %q, want %q (identique à l'en-tête)", contextID, forwardedID)
	}
	if got := rec.Header().Get(CorrelationHeader); got != forwardedID {
		t.Errorf("ID renvoyé au client = %q, want %q", got, forwardedID)
	}
}

func TestCorrelationIDUniquePerRequest(t *testing.T) {
	_, first, _ := serveCorrelation(t, "", http.StatusOK)
	_, second, _ := serveCorrelation(t, "", http.StatusOK)

	if first == second {
		t.Errorf("deux requêtes ont reçu le même Correlation ID %q", first)
	}
}

func TestCorrelationIDPassThrough(t *testing.T) {
	const existing = "front-abc-123"

	rec, forwardedID, contextID := serveCorrelation(t, existing, http.StatusOK)

	if forwardedID != existing {
		t.Errorf("ID propagé = %q, want %q (pass-through)", forwardedID, existing)
	}
	if contextID != existing {
		t.Errorf("ID du contexte = %q, want %q", contextID, existing)
	}
	if got := rec.Header().Get(CorrelationHeader); got != existing {
		t.Errorf("ID renvoyé au client = %q, want %q", got, existing)
	}
}

func TestCorrelationIDReturnedOnError(t *testing.T) {
	rec, forwardedID, _ := serveCorrelation(t, "", http.StatusInternalServerError)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("statut = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get(CorrelationHeader); got == "" || got != forwardedID {
		t.Errorf("ID renvoyé au client sur erreur = %q, want %q", got, forwardedID)
	}
}

func TestCorrelationIDFromEmptyContext(t *testing.T) {
	if got := CorrelationIDFromContext(context.Background()); got != "" {
		t.Errorf("CorrelationIDFromContext() = %q, want \"\"", got)
	}
}
