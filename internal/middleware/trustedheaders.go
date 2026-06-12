package middleware

import "net/http"

func StripUntrustedHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(HeaderUserID)
		r.Header.Del(HeaderUserRole)

		r.Header.Del(HeaderClientIP)
		if ip := ClientIPFromContext(r.Context()); ip != "" {
			r.Header.Set(HeaderClientIP, ip)
		}
		next.ServeHTTP(w, r)
	})
}
