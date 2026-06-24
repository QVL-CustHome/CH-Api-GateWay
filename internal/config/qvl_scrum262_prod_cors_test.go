package config

import (
	"strings"
	"testing"
)

const prodWildcardYAML = `
environment: production
server:
  port: 8080
  cors:
    allowed_origins:
      - "*"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`

const prodExplicitYAML = `
environment: production
server:
  port: 8080
  cors:
    allowed_origins:
      - "https://app.custhome.fr"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`

const devWildcardYAML = `
server:
  port: 8080
  cors:
    allowed_origins:
      - "*"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`

func TestScrum262BootFailsInProductionWithWildcardInYAML(t *testing.T) {
	_, err := Load(writeTempConfig(t, prodWildcardYAML))
	if err == nil {
		t.Fatal("Load() devrait échouer en production avec un wildcard CORS")
	}
	if !strings.Contains(err.Error(), "allowed_origins") {
		t.Errorf("message d'erreur = %q, doit mentionner allowed_origins", err.Error())
	}
}

func TestScrum262BootFailsWhenGatewayEnvForcesProduction(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, devWildcardYAML))
	if err != nil {
		t.Fatalf("Load() ne devrait pas échouer en development: %v", err)
	}

	t.Setenv("GATEWAY_ENV", "production")
	if err := ApplyEnvOverrides(cfg); err == nil {
		t.Fatal("ApplyEnvOverrides() devrait échouer quand GATEWAY_ENV=production avec wildcard CORS")
	}
}

func TestScrum262BootFailsWhenWildcardInjectedViaEnv(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, prodExplicitYAML))
	if err != nil {
		t.Fatalf("Load() ne devrait pas échouer avec origine explicite: %v", err)
	}

	t.Setenv("CORS_ALLOWED_ORIGINS", "*")
	err = ApplyEnvOverrides(cfg)
	if err == nil {
		t.Fatal("ApplyEnvOverrides() devrait échouer quand un wildcard est injecté via CORS_ALLOWED_ORIGINS en production")
	}
	if !strings.Contains(err.Error(), "allowed_origins") {
		t.Errorf("message d'erreur = %q, doit mentionner allowed_origins", err.Error())
	}
}

func TestScrum262BootOKInProductionWithExplicitOrigin(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, prodExplicitYAML))
	if err != nil {
		t.Fatalf("Load() devrait réussir en production avec origine explicite: %v", err)
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, want true")
	}
	if len(cfg.Server.CORS.AllowedOrigins) != 1 || cfg.Server.CORS.AllowedOrigins[0] != "https://app.custhome.fr" {
		t.Errorf("AllowedOrigins = %v, want [https://app.custhome.fr]", cfg.Server.CORS.AllowedOrigins)
	}
	for _, o := range cfg.Server.CORS.AllowedOrigins {
		if o == "*" {
			t.Error("AllowedOrigins ne doit pas contenir de wildcard en production")
		}
	}
}

func TestScrum262WildcardAcceptedInDevelopmentByDefault(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, devWildcardYAML))
	if err != nil {
		t.Fatalf("Load() devrait accepter le wildcard en development: %v", err)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true, want false (development par défaut)")
	}
	if err := ApplyEnvOverrides(cfg); err != nil {
		t.Fatalf("ApplyEnvOverrides() devrait réussir en development: %v", err)
	}
	if len(cfg.Server.CORS.AllowedOrigins) != 1 || cfg.Server.CORS.AllowedOrigins[0] != "*" {
		t.Errorf("AllowedOrigins = %v, want [*]", cfg.Server.CORS.AllowedOrigins)
	}
}
