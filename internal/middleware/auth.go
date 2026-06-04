package middleware

import (
	"net/http"
	"strings"
	"time"
)

// authTimeout borne chaque appel au service d'authentification pour ne pas
// créer de goulot d'étranglement si celui-ci est lent (US-05).
const authTimeout = 100 * time.Millisecond

// AuthClient interroge le microservice d'authentification pour valider
// les tokens. Les connexions TCP sont réutilisées (Keep-Alive) pour
// optimiser les appels récurrents.
type AuthClient struct {
	client  *http.Client
	authURL string
}

// NewAuthClient construit un client de validation pointant sur l'endpoint
// du service d'authentification (ex: http://localhost:8081/validate).
func NewAuthClient(url string) *AuthClient {
	return &AuthClient{
		client: &http.Client{
			Timeout: authTimeout,
			Transport: &http.Transport{
				// Pool de connexions persistantes vers le service d'auth :
				// le défaut (2 par hôte) est trop bas pour un trafic de gateway.
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		authURL: url,
	}
}

// AuthMiddleware protège une route : le token est validé auprès du service
// d'authentification avant tout transfert au microservice cible (US-05 / SCRUM-9).
//
// Scénario 2 : pas de token ou format incorrect → 401 direct, sans appel au service.
// Scénario 1 : le service répond 200 → la requête continue vers le backend.
// Scénario 3 : le service répond 401/403 → l'erreur est retransmise telle quelle.
// Scénario 4 : service injoignable ou timeout → 503 Service Unavailable.
func AuthMiddleware(authClient *AuthClient, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if !hasValidBearerFormat(token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, authClient.authURL, nil)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Authorization", token)

		resp, err := authClient.client.Do(req)
		if err != nil {
			// Panne, connexion refusée ou timeout du service d'auth.
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:
			next.ServeHTTP(w, r)
		case http.StatusUnauthorized, http.StatusForbidden:
			// L'erreur du service d'auth est retransmise au client.
			http.Error(w, http.StatusText(resp.StatusCode), resp.StatusCode)
		default:
			// Réponse inattendue du service d'auth : on ne laisse rien passer.
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}
	})
}

// hasValidBearerFormat vérifie que l'en-tête Authorization est de la forme
// "Bearer <token>" avec un token non vide.
func hasValidBearerFormat(header string) bool {
	const prefix = "Bearer "
	return strings.HasPrefix(header, prefix) && strings.TrimSpace(header[len(prefix):]) != ""
}
