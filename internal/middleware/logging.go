package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// responseRecorder intercepte le ResponseWriter pour capturer le code de
// statut et la taille de la réponse, non exposés nativement après écriture
// (US-11).
type responseRecorder struct {
	http.ResponseWriter
	status int
	size   int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	size, err := r.ResponseWriter.Write(b)
	r.size += size
	return size, err
}

// LoggingMiddleware écrit un access log JSON structuré pour chaque requête
// traversant le gateway (US-11 / SCRUM-15), succès comme erreur, avec le
// statut réellement renvoyé au client. Le Correlation ID est lu depuis le
// contexte (clé typée de l'US-10) — à insérer juste après
// CorrelationIDMiddleware dans le pipeline.
func LoggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rec := &responseRecorder{
			ResponseWriter: w,
			status:         http.StatusOK, // statut implicite si WriteHeader n'est pas appelé
		}

		next.ServeHTTP(rec, r)

		logger.Info("HTTP Request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.Int("bytes", rec.size),
			slog.String("ip", extractIP(r)),
			slog.String("correlation_id", CorrelationIDFromContext(r.Context())),
		)
	})
}
