package middleware

import (
	"net/http"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
)

func TestScrum262ProdCORSReflectsWhitelistedOriginNotWildcard(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins: []string{"https://app.custhome.fr"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
	}

	rec, nextCalled := serve(t, cfg, http.MethodGet, "https://app.custhome.fr")

	if !nextCalled {
		t.Error("la requête whitelistée doit être transmise au handler suivant")
	}
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "https://app.custhome.fr" {
		t.Errorf("Allow-Origin = %q, want https://app.custhome.fr", got)
	}
	if got == "*" {
		t.Error("Allow-Origin ne doit jamais être un wildcard en production")
	}
}

func TestScrum262ProdCORSRejectsNonWhitelistedOrigin(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins: []string{"https://app.custhome.fr"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
	}

	rec, _ := serve(t, cfg, http.MethodGet, "https://evil.example.com")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "https://evil.example.com" || got == "*" {
		t.Errorf("Allow-Origin = %q, ne doit pas autoriser une origine non whitelistée", got)
	}
}
