package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

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

func LoggingMiddleware(logger *slog.Logger, extractor *IPExtractor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rec := &responseRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		next.ServeHTTP(rec, r)

		logger.Info("HTTP Request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.Int("bytes", rec.size),
			slog.String("ip", extractor.ClientIP(r)),
			slog.String("correlation_id", CorrelationIDFromContext(r.Context())),
		)
	})
}
