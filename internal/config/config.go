package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultTimeoutSeconds = 5

const DefaultLogLevel = "INFO"

const DefaultMaxBodyBytes = 10 << 20

const DefaultAuthServiceTimeoutMs = 100

type RouteConfig struct {
	PathPrefix     string `yaml:"path_prefix" json:"path_prefix"`
	DestinationURL string `yaml:"destination_url" json:"destination_url"`
	StripPrefix    bool   `yaml:"strip_prefix" json:"strip_prefix"`
	RequireAuth    bool   `yaml:"require_auth" json:"require_auth"`
	// US-09 (sprint Api Authenticator) : portail servi par cette route,
	// transmis à l'Authenticator via X-Portal pour résoudre le rôle.
	// Obligatoire dès que require_auth est actif.
	Portal string `yaml:"portal" json:"portal"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins" json:"allowed_origins"`
	AllowedMethods []string `yaml:"allowed_methods" json:"allowed_methods"`
	AllowedHeaders []string `yaml:"allowed_headers" json:"allowed_headers"`
	MaxAgeSeconds  int      `yaml:"max_age_seconds" json:"max_age_seconds"`
}

type RateLimitConfig struct {
	Enabled           bool     `yaml:"enabled" json:"enabled"`
	RequestsPerSecond float64  `yaml:"requests_per_second" json:"requests_per_second"`
	Burst             int      `yaml:"burst" json:"burst"`
	TrustedProxies    []string `yaml:"trusted_proxies" json:"trusted_proxies"`
}

type GatewayConfig struct {
	Server struct {
		Port int `yaml:"port" json:"port"`

		TimeoutSeconds int `yaml:"timeout_seconds" json:"timeout_seconds"`

		MaxBodyBytes int64 `yaml:"max_body_bytes" json:"max_body_bytes"`

		LogLevel  string          `yaml:"log_level" json:"log_level"`
		CORS      CORSConfig      `yaml:"cors" json:"cors"`
		RateLimit RateLimitConfig `yaml:"rate_limit" json:"rate_limit"`
	} `yaml:"server" json:"server"`
	AuthServiceURL       string        `yaml:"auth_service_url" json:"auth_service_url"`
	AuthServiceTimeoutMs int           `yaml:"auth_service_timeout_ms" json:"auth_service_timeout_ms"`
	Routes               []RouteConfig `yaml:"routes" json:"routes"`
}

func Load(path string) (*GatewayConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture du fichier de configuration %q: %w", path, err)
	}

	var cfg GatewayConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing du fichier de configuration %q: %w", path, err)
	}

	if cfg.Server.TimeoutSeconds == 0 {
		cfg.Server.TimeoutSeconds = DefaultTimeoutSeconds
	}

	if cfg.Server.MaxBodyBytes == 0 {
		cfg.Server.MaxBodyBytes = DefaultMaxBodyBytes
	}

	if cfg.AuthServiceTimeoutMs == 0 {
		cfg.AuthServiceTimeoutMs = DefaultAuthServiceTimeoutMs
	}

	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = DefaultLogLevel
	}
	cfg.Server.LogLevel = strings.ToUpper(cfg.Server.LogLevel)

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("configuration invalide dans %q: %w", path, err)
	}
	return &cfg, nil
}

func (c *GatewayConfig) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port doit être compris entre 1 et 65535, reçu %d", c.Server.Port)
	}
	if c.Server.TimeoutSeconds < 1 {
		return fmt.Errorf("server.timeout_seconds doit être >= 1, reçu %d", c.Server.TimeoutSeconds)
	}
	if c.Server.MaxBodyBytes < 1 {
		return fmt.Errorf("server.max_body_bytes doit être >= 1, reçu %d", c.Server.MaxBodyBytes)
	}
	if c.AuthServiceTimeoutMs < 1 {
		return fmt.Errorf("auth_service_timeout_ms doit être >= 1, reçu %d", c.AuthServiceTimeoutMs)
	}
	if c.Server.CORS.MaxAgeSeconds < 0 {
		return fmt.Errorf("server.cors.max_age_seconds doit être >= 0, reçu %d", c.Server.CORS.MaxAgeSeconds)
	}
	switch c.Server.LogLevel {
	case "DEBUG", "INFO", "WARN", "ERROR":
	default:
		return fmt.Errorf("server.log_level doit être DEBUG, INFO, WARN ou ERROR, reçu %q", c.Server.LogLevel)
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("au moins une route doit être définie")
	}

	if c.AuthServiceURL != "" {
		if err := validateHTTPURL(c.AuthServiceURL); err != nil {
			return fmt.Errorf("auth_service_url: %w", err)
		}
	}

	if rl := c.Server.RateLimit; rl.Enabled {
		if rl.RequestsPerSecond <= 0 {
			return fmt.Errorf("server.rate_limit.requests_per_second doit être > 0, reçu %v", rl.RequestsPerSecond)
		}
		if rl.Burst < 1 {
			return fmt.Errorf("server.rate_limit.burst doit être >= 1, reçu %d", rl.Burst)
		}
	}
	for _, entry := range c.Server.RateLimit.TrustedProxies {
		if _, errPrefix := netip.ParsePrefix(entry); errPrefix != nil {
			if _, errAddr := netip.ParseAddr(entry); errAddr != nil {
				return fmt.Errorf("server.rate_limit.trusted_proxies: %q n'est ni une IP ni un CIDR valide", entry)
			}
		}
	}

	seen := make(map[string]bool, len(c.Routes))
	for i, r := range c.Routes {
		if !strings.HasPrefix(r.PathPrefix, "/") {
			return fmt.Errorf("routes[%d].path_prefix %q doit commencer par \"/\"", i, r.PathPrefix)
		}
		if seen[r.PathPrefix] {
			return fmt.Errorf("routes[%d].path_prefix %q est défini plusieurs fois", i, r.PathPrefix)
		}
		seen[r.PathPrefix] = true

		if err := validateHTTPURL(r.DestinationURL); err != nil {
			return fmt.Errorf("routes[%d].destination_url: %w", i, err)
		}

		if r.RequireAuth && c.AuthServiceURL == "" {
			return fmt.Errorf("routes[%d] (%s) exige require_auth mais auth_service_url n'est pas défini", i, r.PathPrefix)
		}

		if r.RequireAuth && strings.TrimSpace(r.Portal) == "" {
			return fmt.Errorf("routes[%d] (%s) exige require_auth mais ne définit pas de portal : l'Authenticator ne pourrait pas résoudre le rôle (US-09)", i, r.PathPrefix)
		}
	}
	return nil
}

func (c *GatewayConfig) SlogLevel() slog.Level {
	switch c.Server.LogLevel {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q n'est pas une URL valide: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q doit utiliser le schéma http ou https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%q ne contient pas d'hôte", raw)
	}
	return nil
}
