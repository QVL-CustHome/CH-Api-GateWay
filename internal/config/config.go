// Package config charge et valide la configuration statique de routage
// du gateway au démarrage (US-01 / SCRUM-5).
package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RouteConfig associe un préfixe de chemin exposé à l'URL d'un microservice cible.
// StripPrefix (US-03) supprime le préfixe de routage de l'URL avant transfert
// au backend ; désactivé par défaut.
// RequireAuth (US-05) exige la validation du token auprès du service
// d'authentification avant transfert ; désactivé par défaut (route publique).
type RouteConfig struct {
	PathPrefix     string `yaml:"path_prefix" json:"path_prefix"`
	DestinationURL string `yaml:"destination_url" json:"destination_url"`
	StripPrefix    bool   `yaml:"strip_prefix" json:"strip_prefix"`
	RequireAuth    bool   `yaml:"require_auth" json:"require_auth"`
}

// CORSConfig définit la politique CORS globale appliquée par le gateway
// (US-04) : seules les origines listées reçoivent les en-têtes CORS.
type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins" json:"allowed_origins"`
	AllowedMethods []string `yaml:"allowed_methods" json:"allowed_methods"`
	AllowedHeaders []string `yaml:"allowed_headers" json:"allowed_headers"`
}

// GatewayConfig est la configuration complète du gateway, chargée une seule
// fois au lancement de l'exécutable.
// AuthServiceURL (US-05) est l'endpoint de validation du microservice
// d'authentification ; obligatoire dès qu'une route a require_auth: true.
type GatewayConfig struct {
	Server struct {
		Port int        `yaml:"port" json:"port"`
		CORS CORSConfig `yaml:"cors" json:"cors"`
	} `yaml:"server" json:"server"`
	AuthServiceURL string        `yaml:"auth_service_url" json:"auth_service_url"`
	Routes         []RouteConfig `yaml:"routes" json:"routes"`
}

// Load lit le fichier YAML à l'emplacement donné, le parse en structs
// strictement typés et valide son contenu. Toute erreur (fichier absent,
// syntaxe invalide, données non conformes) est retournée à l'appelant,
// qui doit interrompre le démarrage.
func Load(path string) (*GatewayConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture du fichier de configuration %q: %w", path, err)
	}

	var cfg GatewayConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // rejette les champs inconnus : parsing strictement typé
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing du fichier de configuration %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("configuration invalide dans %q: %w", path, err)
	}
	return &cfg, nil
}

// validate vérifie la cohérence de la configuration : port valide,
// au moins une route, préfixes bien formés et uniques, URL cibles conformes,
// et présence de auth_service_url dès qu'une route est protégée.
func (c *GatewayConfig) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port doit être compris entre 1 et 65535, reçu %d", c.Server.Port)
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("au moins une route doit être définie")
	}

	if c.AuthServiceURL != "" {
		if err := validateHTTPURL(c.AuthServiceURL); err != nil {
			return fmt.Errorf("auth_service_url: %w", err)
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
	}
	return nil
}

// validateHTTPURL vérifie qu'une URL est parsable, en http(s) et avec un hôte.
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
