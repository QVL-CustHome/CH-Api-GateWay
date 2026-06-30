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

	HeaderPortal = "X-Portal"

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

	cookieName string

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

		req.Header.Set(HeaderPortal, portal)

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
			r.Header.Set("Authorization", "Bearer "+token)
			next.ServeHTTP(w, r)
		case http.StatusUnauthorized:

			authClient.unauthorized(w, r)
		case http.StatusForbidden:

			http.Error(w, http.StatusText(resp.StatusCode), resp.StatusCode)
		default:

			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}
	})
}

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
