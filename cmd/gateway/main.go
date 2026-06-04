package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/custhome/ch-api-gateway/internal/config"
	"github.com/custhome/ch-api-gateway/internal/health"
	"github.com/custhome/ch-api-gateway/internal/middleware"
	"github.com/custhome/ch-api-gateway/internal/proxy"
	"github.com/custhome/ch-api-gateway/internal/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "chemin du fichier de configuration de routage")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("démarrage impossible: %v", err)
	}

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

	var protect func(http.Handler) http.Handler
	if cfg.AuthServiceURL != "" {
		authClient := middleware.NewAuthClient(cfg.AuthServiceURL)
		protect = func(next http.Handler) http.Handler {
			return middleware.AuthMiddleware(authClient, next)
		}
	}

	router, err := proxy.NewRouter(cfg, protect)
	if err != nil {
		logger.Error("démarrage impossible", slog.String("error", err.Error()))
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Handler)
	mux.Handle("/", router)

	handler := middleware.CORSMiddleware(cfg.Server.CORS, mux)

	handler = middleware.MaxBodyBytesMiddleware(cfg.Server.MaxBodyBytes, handler)

	onShutdown := []func(){}
	if cfg.Server.RateLimit.Enabled {
		rl := middleware.NewRateLimiter(cfg.Server.RateLimit.RequestsPerSecond, cfg.Server.RateLimit.Burst)
		handler = rl.Middleware(handler)
		onShutdown = append(onShutdown, rl.Stop)
		logger.Info("rate limiting actif",
			slog.Float64("requests_per_second", cfg.Server.RateLimit.RequestsPerSecond),
			slog.Int("burst", cfg.Server.RateLimit.Burst),
		)
	}

	handler = middleware.LoggingMiddleware(logger, handler)

	handler = middleware.CorrelationIDMiddleware(handler)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	backendTimeout := time.Duration(cfg.Server.TimeoutSeconds) * time.Second
	srv := server.New(addr, handler, backendTimeout)

	logger.Info("API Gateway en écoute", slog.String("addr", addr))
	if err := server.Run(ctx, srv, 10*time.Second, onShutdown...); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("arrêt du serveur", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("arrêt propre du gateway")
}
