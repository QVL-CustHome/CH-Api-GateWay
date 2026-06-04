package middleware

import "net/http"

func StripUntrustedHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(HeaderUserID)
		r.Header.Del(HeaderUserRole)
		next.ServeHTTP(w, r)
	})
}
