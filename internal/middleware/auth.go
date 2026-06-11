package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrMissingAuthHeader = errors.New("missing authorization header")
	ErrInvalidAuthFormat = errors.New("invalid authorization format")
)

const (
	HeaderUserID   = "X-User-Id"
	HeaderUserRole = "X-User-Role"
	// US-09 : portail visé par la route, transmis à l'Authenticator
	// qui résout le rôle de l'utilisateur pour CE portail.
	HeaderPortal = "X-Portal"
	// US-10 : IP client réelle (résolue via trusted_proxies), transmise à
	// l'Authenticator pour la whitelist IP par utilisateur. Header de
	// confiance : purgé s'il vient de l'extérieur (trustedheaders.go).
	HeaderClientIP = "X-Client-IP"
)

type AuthResponse struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

const maxAuthResponseBytes = 64 << 10

type AuthClient struct {
	client  *http.Client
	authURL string
	// US-11 : nom du cookie HttpOnly porteur du token (fallback du header).
	cookieName string
	// US-12 : page de connexion du front d'auth — 302 pour les navigateurs
	// non authentifiés quand elle est définie, 401 sinon.
	authFrontURL string
}

func NewAuthClient(url string, timeout time.Duration, cookieName, authFrontURL string) *AuthClient {
	return &AuthClient{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		authURL:      url,
		cookieName:   cookieName,
		authFrontURL: authFrontURL,
	}
}

// US-12 : sur un 401, un navigateur est redirigé vers la page de connexion ;
// les appels API gardent le 401 brut. La cible de retour est transmise via
// un cookie ch_redirect (max 5 min) au lieu d'un query param, pour garder
// l'URL du login propre. Le middleware n'enveloppant que les routes
// protégées, les routes publiques (/api/auth…) sont structurellement hors
// du mécanisme — aucune boucle de redirection possible.
func (c *AuthClient) unauthorized(w http.ResponseWriter, r *http.Request) {
	if c.authFrontURL != "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
		if referer := r.Header.Get("Referer"); referer != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "ch_redirect",
				Value:    url.QueryEscape(referer),
				Path:     "/",
				MaxAge:   300,
				SameSite: http.SameSiteLaxMode,
			})
		}
		http.Redirect(w, r, c.authFrontURL, http.StatusFound)
		return
	}
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func AuthMiddleware(authClient *AuthClient, portal string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		r.Header.Del(HeaderUserID)
		r.Header.Del(HeaderUserRole)

		token, err := extractToken(r, authClient.cookieName)
		if err != nil {
			authClient.unauthorized(w, r)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, authClient.authURL, nil)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		// US-09 : l'Authenticator résout roles[X-Portal] → 403 si aucun rôle.
		req.Header.Set(HeaderPortal, portal)
		// US-10 : IP client réelle pour les comptes whitelist (claim ip du token).
		if clientIP := ClientIPFromContext(r.Context()); clientIP != "" {
			req.Header.Set(HeaderClientIP, clientIP)
		}
		if correlationID := r.Header.Get(CorrelationHeader); correlationID != "" {
			req.Header.Set(CorrelationHeader, correlationID)
		}

		resp, err := authClient.client.Do(req)
		if err != nil {

			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:

			var authData AuthResponse
			if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthResponseBytes)).Decode(&authData); err != nil || authData.UserID == "" {

				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			r.Header.Set(HeaderUserID, authData.UserID)
			if authData.Role != "" {
				r.Header.Set(HeaderUserRole, authData.Role)
			}
			next.ServeHTTP(w, r)
		case http.StatusUnauthorized:
			// US-12 : session absente/expirée → page de connexion pour un navigateur.
			authClient.unauthorized(w, r)
		case http.StatusForbidden:
			// Authentifié mais aucun rôle sur ce portail : rediriger vers le
			// login n'y changerait rien (et bouclerait), le 403 est conservé.
			http.Error(w, http.StatusText(resp.StatusCode), resp.StatusCode)
		default:

			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}
	})
}

// US-11 : le header Authorization prime ; en son absence, le token est lu
// depuis le cookie HttpOnly posé au login par l'Authenticator. Un header
// présent mais malformé reste une erreur — pas de repli silencieux.
// Dans les deux cas le token part en Authorization: Bearer vers /validate.
func extractToken(r *http.Request, cookieName string) (string, error) {
	if r.Header.Get("Authorization") != "" {
		return extractBearerToken(r)
	}

	if cookieName != "" {
		if cookie, err := r.Cookie(cookieName); err == nil {
			if token := strings.TrimSpace(cookie.Value); token != "" {
				return token, nil
			}
		}
	}

	return "", ErrMissingAuthHeader
}

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
