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

	// US-05 : les routes require_auth valident le token auprès du
	// microservice d'authentification avant tout transfert.
	var protect func(http.Handler) http.Handler
	if cfg.AuthServiceURL != "" {
		authClient := middleware.NewAuthClient(cfg.AuthServiceURL)
		protect = func(next http.Handler) http.Handler {
			return middleware.AuthMiddleware(authClient, next)
		}
	}

	// US-02 : le routeur reverse proxy traite tout le trafic ;
	// /health reste servi en direct par le gateway.
	router, err := proxy.NewRouter(cfg, protect)
	if err != nil {
		log.Fatalf("démarrage impossible: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Handler)
	mux.Handle("/", router)

	// US-04 : la politique CORS est centralisée dans un middleware global
	// qui englobe tout le pipeline (preflight intercepté avant le routeur).
	handler := middleware.CORSMiddleware(cfg.Server.CORS, mux)

	// US-08 : le rate limiting par IP est le bouclier le plus externe,
	// il protège l'ensemble du pipeline (CORS compris).
	if cfg.Server.RateLimit.Enabled {
		rl := middleware.NewRateLimiter(cfg.Server.RateLimit.RequestsPerSecond, cfg.Server.RateLimit.Burst)
		handler = rl.Middleware(handler)
		log.Printf("rate limiting actif: %.0f req/s, burst %d", cfg.Server.RateLimit.RequestsPerSecond, cfg.Server.RateLimit.Burst)
	}

	// US-10 : le Correlation ID est initié tout en haut de la chaîne,
	// avant même le rate limiting, pour tracer y compris les requêtes rejetées.
	handler = middleware.CorrelationIDMiddleware(handler)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("API Gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
