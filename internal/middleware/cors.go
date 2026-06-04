package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/custhome/ch-api-gateway/internal/config"
)

func CORSMiddleware(cfg config.CORSConfig, next http.Handler) http.Handler {
	methodsStr := strings.Join(cfg.AllowedMethods, ", ")
	headersStr := strings.Join(cfg.AllowedHeaders, ", ")
	maxAgeStr := strconv.Itoa(cfg.MaxAgeSeconds)

	allowedOrigins := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowedOrigins[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		switch {
		case allowedOrigins["*"]:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case origin != "" && allowedOrigins[origin]:
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}

		w.Header().Set("Vary", "Origin")

		if methodsStr != "" {
			w.Header().Set("Access-Control-Allow-Methods", methodsStr)
		}
		if headersStr != "" {
			w.Header().Set("Access-Control-Allow-Headers", headersStr)
		}

		if r.Method == http.MethodOptions {
			if cfg.MaxAgeSeconds > 0 {
				w.Header().Set("Access-Control-Max-Age", maxAgeStr)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
