package config

import (
	"strings"
	"testing"
)

func findRoute(t *testing.T, cfg *GatewayConfig, prefix string) *RouteConfig {
	t.Helper()
	for i := range cfg.Routes {
		if cfg.Routes[i].PathPrefix == prefix {
			return &cfg.Routes[i]
		}
	}
	t.Fatalf("route %q absente de la configuration", prefix)
	return nil
}

func TestShippedConfigLoadsWithBudgyAndCorrectedRoutes(t *testing.T) {
	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatalf("Load(config.yaml) doit réussir, erreur: %v", err)
	}

	budgy := findRoute(t, cfg, "/api/budgy")
	if budgy.DestinationURL != "http://localhost:8183" {
		t.Errorf("/api/budgy destination = %q, want http://localhost:8183", budgy.DestinationURL)
	}
	if !budgy.StripPrefix {
		t.Error("/api/budgy doit avoir strip_prefix = true")
	}
	if !budgy.RequireAuth {
		t.Error("/api/budgy doit exiger require_auth")
	}
	if budgy.Portal != "portail_budgy" {
		t.Errorf("/api/budgy portal = %q, want portail_budgy", budgy.Portal)
	}

	drive := findRoute(t, cfg, "/api/drive")
	if drive.Portal != "portail_drive" {
		t.Errorf("/api/drive portal = %q, want portail_drive", drive.Portal)
	}

	auth := findRoute(t, cfg, "/api/auth")
	if auth.RequireAuth {
		t.Error("/api/auth doit avoir require_auth = false")
	}
	if auth.Portal != "portail_home" {
		t.Errorf("/api/auth portal = %q, want portail_home", auth.Portal)
	}
}

func TestShippedConfigKeepsAdminAndDriveRouting(t *testing.T) {
	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatalf("Load(config.yaml) doit réussir, erreur: %v", err)
	}

	admin := findRoute(t, cfg, "/api/admin")
	if !admin.RequireAuth {
		t.Error("/api/admin doit rester protégée")
	}
	if admin.Portal != "portail_admin" {
		t.Errorf("/api/admin portal = %q, want portail_admin", admin.Portal)
	}
	if !admin.StripPrefix {
		t.Error("/api/admin doit conserver strip_prefix")
	}
	if admin.DestinationURL != "http://localhost:8181" {
		t.Errorf("/api/admin destination = %q, want http://localhost:8181", admin.DestinationURL)
	}

	drive := findRoute(t, cfg, "/api/drive")
	if !drive.RequireAuth {
		t.Error("/api/drive doit rester protégée")
	}
	if !drive.StripPrefix {
		t.Error("/api/drive doit conserver strip_prefix")
	}
	if drive.DestinationURL != "http://localhost:8182" {
		t.Errorf("/api/drive destination = %q, want http://localhost:8182", drive.DestinationURL)
	}
}

func TestLoadRejectsProtectedRouteWithNonPrefixedPortal(t *testing.T) {
	cases := []struct {
		name   string
		portal string
	}{
		{"custhome", "custhome"},
		{"drive", "drive"},
		{"vide", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
server:
  port: 8080
auth_service_url: "http://localhost:8181/validate"
routes:
  - path_prefix: "/api/protected"
    destination_url: "http://localhost:8083"
    require_auth: true
    portal: ` + tc.portal + `
`
			_, err := Load(writeTempConfig(t, yaml))
			if err == nil {
				t.Fatalf("Load() devrait rejeter une route protégée avec portal %q", tc.portal)
			}
			if !strings.Contains(err.Error(), "portal") {
				t.Errorf("l'erreur doit mentionner le portal, reçu: %v", err)
			}
		})
	}
}

func TestLoadAcceptsProtectedRouteWithPrefixedPortal(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_url: "http://localhost:8181/validate"
routes:
  - path_prefix: "/api/budgy"
    destination_url: "http://localhost:8183"
    require_auth: true
    portal: "portail_budgy"
`
	if _, err := Load(writeTempConfig(t, yaml)); err != nil {
		t.Fatalf("Load() ne doit pas rejeter un portal préfixé portail_, erreur: %v", err)
	}
}

func TestLoadIgnoresPortalRuleOnPublicRoutes(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{"portail non préfixé", `    portal: "custhome"`},
		{"portail vide", `    portal: ""`},
		{"sans portail", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8181"
    require_auth: false
` + tc.block + "\n"
			if _, err := Load(writeTempConfig(t, yaml)); err != nil {
				t.Errorf("Load() ne doit pas appliquer la règle portail_ à une route publique, erreur: %v", err)
			}
		})
	}
}
