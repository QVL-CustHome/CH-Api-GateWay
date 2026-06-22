package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/custhome/ch-api-gateway/internal/config"
	"github.com/custhome/ch-api-gateway/internal/health"
	"github.com/custhome/ch-api-gateway/internal/middleware"
	"github.com/custhome/ch-api-gateway/internal/proxy"
)

func BuildHandler(cfg *config.GatewayConfig, logger *slog.Logger) (http.Handler, []func(), error) {
	var protect func(portal string, next http.Handler) http.Handler
	if cfg.AuthServiceURL != "" {
		timeout := time.Duration(cfg.AuthServiceTimeoutMs) * time.Millisecond
		authClient := middleware.NewAuthClient(cfg.AuthServiceURL, timeout, cfg.AuthCookieName, cfg.AuthFrontURL)
		protect = func(portal string, next http.Handler) http.Handler {
			return middleware.AuthMiddleware(authClient, portal, next)
		}
	}

	router, err := proxy.NewRouter(cfg, protect)
	if err != nil {
		return nil, nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Handler)
	mux.Handle("/", router)

	handler := middleware.CORSMiddleware(cfg.Server.CORS, mux)
	handler = middleware.StripUntrustedHeadersMiddleware(handler)
	handler = middleware.MaxBodyBytesMiddleware(cfg.Server.MaxBodyBytes, handler)

	extractor, err := middleware.NewIPExtractor(cfg.Server.RateLimit.TrustedProxies)
	if err != nil {
		return nil, nil, err
	}

	var onShutdown []func()
	if cfg.Server.RateLimit.Enabled {
		rl := middleware.NewRateLimiter(cfg.Server.RateLimit.RequestsPerSecond, cfg.Server.RateLimit.Burst, extractor, "/health")
		handler = rl.Middleware(handler)
		onShutdown = append(onShutdown, rl.Stop)
	}

	handler = middleware.LoggingMiddleware(logger, handler)
	handler = middleware.SecurityHeadersMiddleware(handler)
	handler = extractor.Middleware(handler)
	handler = middleware.CorrelationIDMiddleware(handler)

	return handler, onShutdown, nil
}
