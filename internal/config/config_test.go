package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig écrit un fichier de configuration temporaire et retourne son chemin.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("écriture du fichier de test: %v", err)
	}
	return path
}

const validYAML = `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
  - path_prefix: "/api/users"
    destination_url: "http://localhost:8082"
`

// Scénario 1 — Fichier valide et démarrage réussi.
func TestLoadValidFile(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", cfg.Server.Port)
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("len(Routes) = %d, want 2", len(cfg.Routes))
	}
	want := []RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: "http://localhost:8081"},
		{PathPrefix: "/api/users", DestinationURL: "http://localhost:8082"},
	}
	for i, r := range want {
		if cfg.Routes[i] != r {
			t.Errorf("Routes[%d] = %+v, want %+v", i, cfg.Routes[i], r)
		}
	}
}

// Scénario 2 — Fichier introuvable.
func TestLoadFileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "inexistant.yaml"))
	if err == nil {
		t.Fatal("Load() devrait échouer quand le fichier est absent")
	}
	if !strings.Contains(err.Error(), "lecture du fichier") {
		t.Errorf("erreur inattendue: %v", err)
	}
}

// Scénario 3 — Fichier malformé ou données invalides.
func TestLoadMalformedYAML(t *testing.T) {
	_, err := Load(writeTempConfig(t, "server: [port: 8080\nroutes"))
	if err == nil {
		t.Fatal("Load() devrait échouer sur un YAML malformé")
	}
}

func TestLoadUnknownField(t *testing.T) {
	yaml := `
server:
  port: 8080
  unknown_field: true
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter les champs inconnus (parsing strict)")
	}
}

func TestLoadInvalidDestinationURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"sans schéma", "localhost:8081"},
		{"schéma non supporté", "ftp://localhost:8081"},
		{"sans hôte", "http://"},
		{"URL incohérente", "http://exa mple.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "` + tc.url + `"
`
			if _, err := Load(writeTempConfig(t, yaml)); err == nil {
				t.Errorf("Load() devrait rejeter destination_url %q", tc.url)
			}
		})
	}
}

func TestLoadInvalidPort(t *testing.T) {
	for _, port := range []string{"0", "-1", "70000"} {
		t.Run("port "+port, func(t *testing.T) {
			yaml := `
server:
  port: ` + port + `
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
			if _, err := Load(writeTempConfig(t, yaml)); err == nil {
				t.Errorf("Load() devrait rejeter le port %s", port)
			}
		})
	}
}

func TestLoadNoRoutes(t *testing.T) {
	yaml := `
server:
  port: 8080
routes: []
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait échouer sans aucune route")
	}
}

func TestLoadPathPrefixWithoutLeadingSlash(t *testing.T) {
	yaml := `
server:
  port: 8080
routes:
  - path_prefix: "api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un path_prefix sans \"/\" initial")
	}
}

func TestLoadDuplicatePathPrefix(t *testing.T) {
	yaml := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8082"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter les path_prefix dupliqués")
	}
}
