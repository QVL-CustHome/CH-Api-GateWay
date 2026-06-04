package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestLoadTimeoutSeconds(t *testing.T) {
	yaml := `
server:
  port: 8080
  timeout_seconds: 7
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.Server.TimeoutSeconds != 7 {
		t.Errorf("TimeoutSeconds = %d, want 7", cfg.Server.TimeoutSeconds)
	}
}

func TestLoadTimeoutSecondsDefault(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.Server.TimeoutSeconds != DefaultTimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d, want défaut %d", cfg.Server.TimeoutSeconds, DefaultTimeoutSeconds)
	}
}

func TestLoadNegativeTimeoutSeconds(t *testing.T) {
	yaml := `
server:
  port: 8080
  timeout_seconds: -3
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un timeout_seconds négatif")
	}
}

func TestLoadLogLevel(t *testing.T) {
	cases := []struct {
		name      string
		yamlLevel string
		want      string
		wantSlog  slog.Level
	}{
		{"défaut si absent", "", "INFO", slog.LevelInfo},
		{"DEBUG", "DEBUG", "DEBUG", slog.LevelDebug},
		{"minuscules normalisées", "warn", "WARN", slog.LevelWarn},
		{"ERROR", "ERROR", "ERROR", slog.LevelError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			level := ""
			if tc.yamlLevel != "" {
				level = "\n  log_level: \"" + tc.yamlLevel + "\""
			}
			yaml := `
server:
  port: 8080` + level + `
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
			cfg, err := Load(writeTempConfig(t, yaml))
			if err != nil {
				t.Fatalf("Load() erreur inattendue: %v", err)
			}
			if cfg.Server.LogLevel != tc.want {
				t.Errorf("LogLevel = %q, want %q", cfg.Server.LogLevel, tc.want)
			}
			if got := cfg.SlogLevel(); got != tc.wantSlog {
				t.Errorf("SlogLevel() = %v, want %v", got, tc.wantSlog)
			}
		})
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	yaml := `
server:
  port: 8080
  log_level: "VERBOSE"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un log_level inconnu")
	}
}

func TestLoadRateLimitConfig(t *testing.T) {
	yaml := `
server:
  port: 8080
  rate_limit:
    enabled: true
    requests_per_second: 10
    burst: 20
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	rl := cfg.Server.RateLimit
	if !rl.Enabled || rl.RequestsPerSecond != 10 || rl.Burst != 20 {
		t.Errorf("RateLimit = %+v, want enabled/10/20", rl)
	}
}

func TestLoadInvalidRateLimitConfig(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{"rps nul", "enabled: true\n    requests_per_second: 0\n    burst: 20"},
		{"rps négatif", "enabled: true\n    requests_per_second: -5\n    burst: 20"},
		{"burst nul", "enabled: true\n    requests_per_second: 10\n    burst: 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := `
server:
  port: 8080
  rate_limit:
    ` + tc.block + `
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
			if _, err := Load(writeTempConfig(t, yaml)); err == nil {
				t.Error("Load() devrait rejeter cette configuration de rate limit")
			}
		})
	}
}

func TestLoadDisabledRateLimitSkipsValidation(t *testing.T) {
	yaml := `
server:
  port: 8080
  rate_limit:
    enabled: false
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err != nil {
		t.Errorf("Load() erreur inattendue: %v", err)
	}
}

func TestLoadAuthConfig(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_url: "http://localhost:8081/validate"
routes:
  - path_prefix: "/api/public"
    destination_url: "http://localhost:8082"
    require_auth: false
  - path_prefix: "/api/protected"
    destination_url: "http://localhost:8083"
    require_auth: true
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthServiceURL != "http://localhost:8081/validate" {
		t.Errorf("AuthServiceURL = %q", cfg.AuthServiceURL)
	}
	if cfg.Routes[0].RequireAuth {
		t.Error("Routes[0].RequireAuth = true, want false")
	}
	if !cfg.Routes[1].RequireAuth {
		t.Error("Routes[1].RequireAuth = false, want true")
	}
}

func TestLoadRequireAuthWithoutAuthServiceURL(t *testing.T) {
	yaml := `
server:
  port: 8080
routes:
  - path_prefix: "/api/protected"
    destination_url: "http://localhost:8083"
    require_auth: true
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait échouer quand require_auth est actif sans auth_service_url")
	}
}

func TestLoadInvalidAuthServiceURL(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_url: "pas-une-url"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter une auth_service_url invalide")
	}
}

func TestLoadCORSConfig(t *testing.T) {
	yaml := `
server:
  port: 8080
  cors:
    allowed_origins:
      - "http://localhost:3000"
    allowed_methods:
      - "GET"
      - "POST"
    allowed_headers:
      - "Authorization"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	cors := cfg.Server.CORS
	if len(cors.AllowedOrigins) != 1 || cors.AllowedOrigins[0] != "http://localhost:3000" {
		t.Errorf("AllowedOrigins = %v", cors.AllowedOrigins)
	}
	if len(cors.AllowedMethods) != 2 {
		t.Errorf("AllowedMethods = %v, want 2 éléments", cors.AllowedMethods)
	}
	if len(cors.AllowedHeaders) != 1 || cors.AllowedHeaders[0] != "Authorization" {
		t.Errorf("AllowedHeaders = %v", cors.AllowedHeaders)
	}
}

func TestLoadStripPrefix(t *testing.T) {
	yaml := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
    strip_prefix: true
  - path_prefix: "/api/users"
    destination_url: "http://localhost:8082"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if !cfg.Routes[0].StripPrefix {
		t.Error("Routes[0].StripPrefix = false, want true")
	}
	if cfg.Routes[1].StripPrefix {
		t.Error("Routes[1].StripPrefix = true, want false (défaut)")
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "inexistant.yaml"))
	if err == nil {
		t.Fatal("Load() devrait échouer quand le fichier est absent")
	}
	if !strings.Contains(err.Error(), "lecture du fichier") {
		t.Errorf("erreur inattendue: %v", err)
	}
}

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
