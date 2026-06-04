package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaxBodyRejectsDeclaredOversizedBody(t *testing.T) {
	nextCalled := false
	handler := MaxBodyBytesMiddleware(10, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(strings.Repeat("x", 50)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("statut = %d, want 413", rec.Code)
	}
	if nextCalled {
		t.Error("la requête trop grosse ne doit pas être transmise")
	}
}

func TestMaxBodyAllowsSmallBody(t *testing.T) {
	var received string
	handler := MaxBodyBytesMiddleware(100, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("lecture du body: %v", err)
		}
		received = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader("petit body"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("statut = %d, want 200", rec.Code)
	}
	if received != "petit body" {
		t.Errorf("body reçu = %q", received)
	}
}

func TestMaxBodyCapsChunkedBody(t *testing.T) {
	var readErr error
	handler := MaxBodyBytesMiddleware(10, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(strings.Repeat("x", 50)))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var maxBytesErr *http.MaxBytesError
	if !errors.As(readErr, &maxBytesErr) {
		t.Errorf("erreur de lecture = %v, want *http.MaxBytesError", readErr)
	}
}
