package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// contextKey est un type fort dédié aux clés de contexte du gateway,
// pour éviter toute collision avec d'autres middlewares (US-10).
type contextKey string

// CorrelationIDKey est la clé de contexte portant le Correlation ID.
const CorrelationIDKey contextKey = "correlation_id"

// CorrelationHeader est l'en-tête HTTP de traçage inter-services.
const CorrelationHeader = "X-Correlation-ID"

// CorrelationIDMiddleware initie le traçage distribué (US-10 / SCRUM-14) :
// chaque requête entrante sans X-Correlation-ID reçoit un UUID v4 neuf ;
// une valeur déjà présente est conservée telle quelle (pass-through).
// L'identifiant est propagé au backend (en-tête de requête), renvoyé
// systématiquement au client (en-tête de réponse) et injecté dans le
// context.Context pour le logger interne (US-11).
// À placer tout en haut de la chaîne d'exécution.
func CorrelationIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get(CorrelationHeader)

		if correlationID == "" {
			// uuid.New() s'appuie sur crypto/rand : UUID v4 cryptographiquement sûr.
			correlationID = uuid.New().String()
		}

		r.Header.Set(CorrelationHeader, correlationID)
		w.Header().Set(CorrelationHeader, correlationID)

		ctx := context.WithValue(r.Context(), CorrelationIDKey, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CorrelationIDFromContext retourne le Correlation ID porté par le contexte,
// ou une chaîne vide s'il n'y en a pas (requête hors middleware).
func CorrelationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(CorrelationIDKey).(string)
	return id
}
