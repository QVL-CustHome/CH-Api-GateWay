package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
}

func NewAuthClient(url string, timeout time.Duration) *AuthClient {
	return &AuthClient{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		authURL: url,
	}
}

func AuthMiddleware(authClient *AuthClient, portal string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

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
		case http.StatusUnauthorized, http.StatusForbidden:

			http.Error(w, http.StatusText(resp.StatusCode), resp.StatusCode)
		default:

			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}
	})
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
