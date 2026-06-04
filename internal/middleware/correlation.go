package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey string

const CorrelationIDKey contextKey = "correlation_id"

const CorrelationHeader = "X-Correlation-ID"

const maxCorrelationIDLength = 128

func CorrelationIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get(CorrelationHeader)

		if !isValidCorrelationID(correlationID) {
			correlationID = uuid.New().String()
		}

		r.Header.Set(CorrelationHeader, correlationID)
		w.Header().Set(CorrelationHeader, correlationID)

		ctx := context.WithValue(r.Context(), CorrelationIDKey, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isValidCorrelationID(id string) bool {
	if len(id) == 0 || len(id) > maxCorrelationIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

func CorrelationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(CorrelationIDKey).(string)
	return id
}
