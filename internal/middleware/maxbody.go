package middleware

import (
	"net/http"
	"sort"
	"strings"
)

func MaxBodyBytesMiddleware(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enforceMaxBody(w, r, limit, next)
	})
}

type BodyLimitOverride struct {
	PathPrefix string
	Limit      int64
}

func MaxBodyBytesPerRouteMiddleware(defaultLimit int64, overrides []BodyLimitOverride, next http.Handler) http.Handler {
	sorted := make([]BodyLimitOverride, len(overrides))
	copy(sorted, overrides)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].PathPrefix) > len(sorted[j].PathPrefix)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := defaultLimit
		for _, o := range sorted {
			if matchesPrefix(r.URL.Path, o.PathPrefix) {
				limit = o.Limit
				break
			}
		}
		enforceMaxBody(w, r, limit, next)
	})
}

func enforceMaxBody(w http.ResponseWriter, r *http.Request, limit int64, next http.Handler) {
	if r.ContentLength > limit {
		http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	next.ServeHTTP(w, r)
}

func matchesPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
