package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/custhome/ch-api-gateway/internal/config"
	"github.com/custhome/ch-api-gateway/internal/health"
	"github.com/custhome/ch-api-gateway/internal/middleware"
	"github.com/custhome/ch-api-gateway/internal/proxy"
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

	// US-02 : le routeur reverse proxy traite tout le trafic ;
	// /health reste servi en direct par le gateway.
	router, err := proxy.NewRouter(cfg)
	if err != nil {
		log.Fatalf("démarrage impossible: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Handler)
	mux.Handle("/", router)

	// US-04 : la politique CORS est centralisée dans un middleware global
	// qui englobe tout le pipeline (preflight intercepté avant le routeur).
	handler := middleware.CORSMiddleware(cfg.Server.CORS, mux)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("API Gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
