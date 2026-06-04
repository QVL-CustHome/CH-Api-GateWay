package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

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

	// US-11 : logger global structuré JSON sur stdout, verbosité issue de la config.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	slog.SetDefault(logger)

	logger.Info("configuration chargée",
		slog.Int("routes", len(cfg.Routes)),
		slog.String("log_level", cfg.Server.LogLevel),
	)
	for _, r := range cfg.Routes {
		logger.Info("route",
			slog.String("path_prefix", r.PathPrefix),
			slog.String("destination_url", r.DestinationURL),
			slog.Bool("strip_prefix", r.StripPrefix),
			slog.Bool("require_auth", r.RequireAuth),
		)
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
		logger.Error("démarrage impossible", slog.String("error", err.Error()))
		os.Exit(1)
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
		logger.Info("rate limiting actif",
			slog.Float64("requests_per_second", cfg.Server.RateLimit.RequestsPerSecond),
			slog.Int("burst", cfg.Server.RateLimit.Burst),
		)
	}

	// US-11 : access log JSON pour chaque requête, statut réel inclus.
	handler = middleware.LoggingMiddleware(logger, handler)

	// US-10 : le Correlation ID est initié tout en haut de la chaîne,
	// avant même le logging et le rate limiting, pour tracer y compris
	// les requêtes rejetées.
	handler = middleware.CorrelationIDMiddleware(handler)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("API Gateway en écoute", slog.String("addr", addr))
	if err := http.ListenAndServe(addr, handler); err != nil {
		logger.Error("arrêt du serveur", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
