package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// Erreurs de validation syntaxique locale de l'en-tête Authorization (US-06).
var (
	ErrMissingAuthHeader = errors.New("missing authorization header")
	ErrInvalidAuthFormat = errors.New("invalid authorization format")
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
// US-06 (Fast-Fail) : la validation syntaxique locale (extractBearerToken)
// est exécutée en premier — en-tête absent ou mal formaté → 401 immédiat,
// sans aucun appel réseau vers le service d'authentification.
// US-05 : le service répond 200 → la requête continue vers le backend ;
// 401/403 → l'erreur est retransmise telle quelle ; service injoignable
// ou timeout → 503 Service Unavailable.
func AuthMiddleware(authClient *AuthClient, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := extractBearerToken(r)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, authClient.authURL, nil)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)

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

// extractBearerToken effectue la validation syntaxique locale de l'en-tête
// Authorization (US-06 / SCRUM-10), sans aucun appel réseau ni regex :
// l'en-tête doit être de la forme exacte "Bearer <token>" avec un token
// non vide. Le token est retourné nettoyé des espaces périphériques.
func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrMissingAuthHeader
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", ErrInvalidAuthFormat
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	if token == "" {
		return "", ErrInvalidAuthFormat
	}

	return token, nil
}
