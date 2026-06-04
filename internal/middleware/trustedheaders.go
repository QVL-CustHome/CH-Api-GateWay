package middleware

import "net/http"

func StripUntrustedHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(HeaderUserID)
		r.Header.Del(HeaderUserRole)
		// US-10 : X-Client-IP est un header de confiance — toute valeur venue
		// de l'extérieur est purgée, puis remplacée par l'IP réellement
		// résolue (trusted_proxies). Les backends proxifiés — dont
		// l'Authenticator pour /login — reçoivent ainsi l'IP client fiable.
		r.Header.Del(HeaderClientIP)
		if ip := ClientIPFromContext(r.Context()); ip != "" {
			r.Header.Set(HeaderClientIP, ip)
		}
		next.ServeHTTP(w, r)
	})
}
