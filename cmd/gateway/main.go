package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/custhome/ch-api-gateway/internal/config"
	"github.com/custhome/ch-api-gateway/internal/health"
)

func main() {
	configPath := flag.String("config", "config.yaml", "chemin du fichier de configuration de routage")
	flag.Parse()

	// US-01 : la configuration est chargée une seule fois au démarrage.
	// Tout fichier absent, malformé ou invalide interrompt immédiatement le processus.
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("démarrage impossible: %v", err)
	}
	log.Printf("configuration chargée: %d route(s)", len(cfg.Routes))
	for _, r := range cfg.Routes {
		log.Printf("  route %s -> %s", r.PathPrefix, r.DestinationURL)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Handler)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("API Gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
