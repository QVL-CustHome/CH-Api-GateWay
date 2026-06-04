package health

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	got := Check()
	if got.Status != "ok" {
		t.Errorf("Check().Status = %q, want %q", got.Status, "ok")
	}
}

func TestHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	Handler(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body Status
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("body.status = %q, want %q", body.Status, "ok")
	}
}
