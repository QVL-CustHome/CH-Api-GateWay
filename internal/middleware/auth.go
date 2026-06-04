package middleware

import (
	"encoding/json"
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

// En-têtes de confiance injectés par le gateway vers les microservices (US-07).
// Toute valeur fournie par le client est systématiquement supprimée.
const (
	HeaderUserID   = "X-User-Id"
	HeaderUserRole = "X-User-Role"
)

// AuthResponse est le payload strictement typé renvoyé par le microservice
// d'authentification (Rust) lors de la validation d'un token (US-07).
type AuthResponse struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

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
// US-07 (anti-spoofing) : les en-têtes X-User-* fournis par le client sont
// supprimés avant toute autre action ; seules les valeurs issues de la
// validation légitime sont transmises au backend.
// US-06 (Fast-Fail) : la validation syntaxique locale (extractBearerToken)
// est exécutée ensuite — en-tête absent ou mal formaté → 401 immédiat,
// sans aucun appel réseau vers le service d'authentification.
// US-05 : le service répond 200 → la requête continue vers le backend,
// enrichie du contexte utilisateur (US-07) ; 401/403 → l'erreur est
// retransmise telle quelle ; service injoignable ou timeout → 503 ;
// réponse 200 au JSON invalide ou incomplet → 500 (US-07).
func AuthMiddleware(authClient *AuthClient, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anti-spoofing : aucune donnée client ne doit polluer le contexte interne.
		r.Header.Del(HeaderUserID)
		r.Header.Del(HeaderUserRole)

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
			// US-07 : lecture du contexte utilisateur renvoyé par le service
			// d'auth et injection dans la requête proxyfiée.
			var authData AuthResponse
			if err := json.NewDecoder(resp.Body).Decode(&authData); err != nil || authData.UserID == "" {
				// JSON invalide ou incomplet : rien ne part vers le backend.
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			r.Header.Set(HeaderUserID, authData.UserID)
			if authData.Role != "" {
				r.Header.Set(HeaderUserRole, authData.Role)
			}
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
