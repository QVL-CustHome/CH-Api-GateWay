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

	"github.com/custhome/ch-api-gateway/internal/app"
	"github.com/custhome/ch-api-gateway/internal/config"
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
	if cfg.Server.RateLimit.Enabled {
		logger.Info("rate limiting actif",
			slog.Float64("requests_per_second", cfg.Server.RateLimit.RequestsPerSecond),
			slog.Int("burst", cfg.Server.RateLimit.Burst),
			slog.Int("trusted_proxies", len(cfg.Server.RateLimit.TrustedProxies)),
		)
	}

	handler, onShutdown, err := app.BuildHandler(cfg, logger)
	if err != nil {
		logger.Error("démarrage impossible", slog.String("error", err.Error()))
		os.Exit(1)
	}

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
