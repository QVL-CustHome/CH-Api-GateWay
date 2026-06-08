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

func TestLoadMaxBodyBytes(t *testing.T) {
	yaml := `
server:
  port: 8080
  max_body_bytes: 1024
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.Server.MaxBodyBytes != 1024 {
		t.Errorf("MaxBodyBytes = %d, want 1024", cfg.Server.MaxBodyBytes)
	}
}

func TestLoadMaxBodyBytesDefault(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.Server.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want %d", cfg.Server.MaxBodyBytes, DefaultMaxBodyBytes)
	}
}

func TestLoadNegativeMaxBodyBytes(t *testing.T) {
	yaml := `
server:
  port: 8080
  max_body_bytes: -1
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un max_body_bytes négatif")
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

func TestLoadTrustedProxies(t *testing.T) {
	yaml := `
server:
  port: 8080
  rate_limit:
    enabled: true
    requests_per_second: 10
    burst: 20
    trusted_proxies:
      - "10.0.0.1"
      - "10.1.0.0/16"
      - "2001:db8::1"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if got := len(cfg.Server.RateLimit.TrustedProxies); got != 3 {
		t.Errorf("TrustedProxies = %d entrées, want 3", got)
	}
}

func TestLoadInvalidTrustedProxies(t *testing.T) {
	for _, entry := range []string{"pas-une-ip", "10.0.0.0/99"} {
		t.Run(entry, func(t *testing.T) {
			yaml := `
server:
  port: 8080
  rate_limit:
    trusted_proxies:
      - "` + entry + `"
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
			if _, err := Load(writeTempConfig(t, yaml)); err == nil {
				t.Errorf("Load() devrait rejeter trusted_proxies %q", entry)
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
    portal: "portail_clients"
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
	if cfg.Routes[1].Portal != "portail_clients" {
		t.Errorf("Routes[1].Portal = %q, want portail_clients", cfg.Routes[1].Portal)
	}
}

// US-11 : nom du cookie porteur du token — défaut ch_token, surchargeable.
func TestLoadAuthCookieName(t *testing.T) {
	base := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, base))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthCookieName != DefaultAuthCookieName {
		t.Errorf("AuthCookieName = %q, want défaut %q", cfg.AuthCookieName, DefaultAuthCookieName)
	}

	custom := base + "auth_cookie_name: \"mon_cookie\"\n"
	cfg, err = Load(writeTempConfig(t, custom))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthCookieName != "mon_cookie" {
		t.Errorf("AuthCookieName = %q, want mon_cookie", cfg.AuthCookieName)
	}
}

// US-12 : auth_front_url optionnelle mais validée si présente.
func TestLoadAuthFrontURL(t *testing.T) {
	base := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, base))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthFrontURL != "" {
		t.Errorf("AuthFrontURL = %q, want vide par défaut", cfg.AuthFrontURL)
	}

	valid := base + "auth_front_url: \"http://localhost:3000/login\"\n"
	cfg, err = Load(writeTempConfig(t, valid))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthFrontURL != "http://localhost:3000/login" {
		t.Errorf("AuthFrontURL = %q", cfg.AuthFrontURL)
	}

	invalid := base + "auth_front_url: \"pas-une-url\"\n"
	if _, err := Load(writeTempConfig(t, invalid)); err == nil {
		t.Fatal("Load() devrait rejeter une auth_front_url invalide")
	}
}

// US-09 : une route protégée sans portail est une erreur de configuration.
func TestLoadRequireAuthWithoutPortal(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_url: "http://localhost:8081/validate"
routes:
  - path_prefix: "/api/protected"
    destination_url: "http://localhost:8083"
    require_auth: true
`
	_, err := Load(writeTempConfig(t, yaml))
	if err == nil {
		t.Fatal("Load() devrait échouer quand require_auth est actif sans portal")
	}
	if !strings.Contains(err.Error(), "portal") {
		t.Errorf("l'erreur doit mentionner le champ portal, reçu : %v", err)
	}
}

// US-09 : un portail composé d'espaces est traité comme absent.
func TestLoadRequireAuthWithBlankPortal(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_url: "http://localhost:8081/validate"
routes:
  - path_prefix: "/api/protected"
    destination_url: "http://localhost:8083"
    require_auth: true
    portal: "   "
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un portal vide (espaces)")
	}
}

// US-09 : une route publique n'a pas besoin de portail.
func TestLoadPublicRouteWithoutPortal(t *testing.T) {
	yaml := `
server:
  port: 8080
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
    require_auth: false
`
	if _, err := Load(writeTempConfig(t, yaml)); err != nil {
		t.Errorf("Load() erreur inattendue pour une route publique sans portal: %v", err)
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

func TestLoadAuthServiceTimeout(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_timeout_ms: 250
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthServiceTimeoutMs != 250 {
		t.Errorf("AuthServiceTimeoutMs = %d, want 250", cfg.AuthServiceTimeoutMs)
	}
}

func TestLoadAuthServiceTimeoutDefault(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.AuthServiceTimeoutMs != DefaultAuthServiceTimeoutMs {
		t.Errorf("AuthServiceTimeoutMs = %d, want défaut %d", cfg.AuthServiceTimeoutMs, DefaultAuthServiceTimeoutMs)
	}
}

func TestLoadNegativeAuthServiceTimeout(t *testing.T) {
	yaml := `
server:
  port: 8080
auth_service_timeout_ms: -5
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un auth_service_timeout_ms négatif")
	}
}

func TestLoadCORSMaxAge(t *testing.T) {
	yaml := `
server:
  port: 8080
  cors:
    max_age_seconds: 600
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load() erreur inattendue: %v", err)
	}
	if cfg.Server.CORS.MaxAgeSeconds != 600 {
		t.Errorf("MaxAgeSeconds = %d, want 600", cfg.Server.CORS.MaxAgeSeconds)
	}
}

func TestLoadNegativeCORSMaxAge(t *testing.T) {
	yaml := `
server:
  port: 8080
  cors:
    max_age_seconds: -1
routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("Load() devrait rejeter un max_age_seconds négatif")
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

// US-8.4 : la configuration livrée doit être valide et exposer la route
// d'administration, protégée et rattachée au portail "portail_admin".
func TestLoadShippedConfigHasAdminRoute(t *testing.T) {
	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatalf("la configuration livrée (config.yaml) doit être valide: %v", err)
	}

	var admin *RouteConfig
	for i := range cfg.Routes {
		if cfg.Routes[i].PathPrefix == "/api/admin" {
			admin = &cfg.Routes[i]
			break
		}
	}
	if admin == nil {
		t.Fatal("la route /api/admin (portail d'administration) est absente de config.yaml")
	}
	if !admin.RequireAuth {
		t.Error("/api/admin doit exiger l'authentification (require_auth)")
	}
	if admin.Portal != "portail_admin" {
		t.Errorf("/api/admin portal = %q, want portail_admin", admin.Portal)
	}
	if !admin.StripPrefix {
		t.Error("/api/admin doit stripper son préfixe pour atteindre /users, /roles sur l'Authenticator")
	}
	if admin.DestinationURL != "http://localhost:8081" {
		t.Errorf("/api/admin destination = %q, want http://localhost:8081", admin.DestinationURL)
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
