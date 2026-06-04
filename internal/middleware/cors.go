// Package middleware regroupe les middlewares HTTP globaux du gateway.
package middleware

import (
	"net/http"
	"strings"

	"github.com/custhome/ch-api-gateway/internal/config"
)

// CORSMiddleware applique la politique CORS centralisée (US-04 / SCRUM-8).
// Les requêtes Preflight (OPTIONS) sont interceptées et reçoivent un 204
// sans jamais être transmises aux microservices. L'en-tête
// Access-Control-Allow-Origin n'est ajouté que si l'Origin de la requête
// figure dans la liste blanche (ou si "*" est configuré).
func CORSMiddleware(cfg config.CORSConfig, next http.Handler) http.Handler {
	methodsStr := strings.Join(cfg.AllowedMethods, ", ")
	headersStr := strings.Join(cfg.AllowedHeaders, ", ")

	allowedOrigins := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowedOrigins[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// L'Allow-Origin ne supporte qu'une seule valeur : elle est résolue
		// dynamiquement par comparaison avec la liste blanche.
		switch {
		case allowedOrigins["*"]:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case origin != "" && allowedOrigins[origin]:
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		// La réponse varie selon l'Origin : indispensable pour les caches.
		w.Header().Add("Vary", "Origin")

		if methodsStr != "" {
			w.Header().Set("Access-Control-Allow-Methods", methodsStr)
		}
		if headersStr != "" {
			w.Header().Set("Access-Control-Allow-Headers", headersStr)
		}

		// Preflight : réponse immédiate, jamais transmise aux backends.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
