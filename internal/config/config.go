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
type RouteConfig struct {
	PathPrefix     string `yaml:"path_prefix" json:"path_prefix"`
	DestinationURL string `yaml:"destination_url" json:"destination_url"`
	StripPrefix    bool   `yaml:"strip_prefix" json:"strip_prefix"`
}

// GatewayConfig est la configuration complète du gateway, chargée une seule
// fois au lancement de l'exécutable.
type GatewayConfig struct {
	Server struct {
		Port int `yaml:"port" json:"port"`
	} `yaml:"server" json:"server"`
	Routes []RouteConfig `yaml:"routes" json:"routes"`
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
// au moins une route, préfixes bien formés et uniques, URL cibles conformes.
func (c *GatewayConfig) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port doit être compris entre 1 et 65535, reçu %d", c.Server.Port)
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("au moins une route doit être définie")
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

		u, err := url.Parse(r.DestinationURL)
		if err != nil {
			return fmt.Errorf("routes[%d].destination_url %q n'est pas une URL valide: %w", i, r.DestinationURL, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("routes[%d].destination_url %q doit utiliser le schéma http ou https", i, r.DestinationURL)
		}
		if u.Host == "" {
			return fmt.Errorf("routes[%d].destination_url %q ne contient pas d'hôte", i, r.DestinationURL)
		}
	}
	return nil
}
