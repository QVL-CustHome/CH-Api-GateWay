package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type accessLog struct {
	Time          string  `json:"time"`
	Level         string  `json:"level"`
	Msg           string  `json:"msg"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	Status        int     `json:"status"`
	Duration      float64 `json:"duration"`
	Bytes         int     `json:"bytes"`
	IP            string  `json:"ip"`
	CorrelationID string  `json:"correlation_id"`
}

func serveLogging(t *testing.T, handler http.HandlerFunc, correlationIn string) accessLog {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	chain := CorrelationIDMiddleware(LoggingMiddleware(logger, handler))

	req := httptest.NewRequest(http.MethodPost, "/api/users/42?fields=name", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	if correlationIn != "" {
		req.Header.Set(CorrelationHeader, correlationIn)
	}
	chain.ServeHTTP(httptest.NewRecorder(), req)

	var entry accessLog
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("le log émis n'est pas du JSON valide: %v\nlog: %s", err, buf.String())
	}
	return entry
}

func TestAccessLogSuccess(t *testing.T) {
	entry := serveLogging(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, "hello")
	}, "trace-123")

	if entry.Time == "" {
		t.Error("timestamp absent du log")
	}
	if entry.Level != "INFO" {
		t.Errorf("level = %q, want INFO", entry.Level)
	}
	if entry.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", entry.Method)
	}
	if entry.Path != "/api/users/42" {
		t.Errorf("path = %q, want /api/users/42", entry.Path)
	}
	if entry.Status != http.StatusOK {
		t.Errorf("status = %d, want 200 (implicite sans WriteHeader)", entry.Status)
	}

	if entry.Duration < float64(10*time.Millisecond) {
		t.Errorf("duration = %v ns, want >= 10ms", entry.Duration)
	}
	if entry.Bytes != len("hello") {
		t.Errorf("bytes = %d, want %d", entry.Bytes, len("hello"))
	}
	if entry.IP != "203.0.113.7" {
		t.Errorf("ip = %q, want 203.0.113.7", entry.IP)
	}
	if entry.CorrelationID != "trace-123" {
		t.Errorf("correlation_id = %q, want trace-123", entry.CorrelationID)
	}
}

func TestAccessLogErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			entry := serveLogging(t, func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}, "")

			if entry.Status != status {
				t.Errorf("status loggué = %d, want %d", entry.Status, status)
			}
			if entry.Level != "INFO" {
				t.Errorf("level = %q, want INFO (access log, pas un log d'erreur)", entry.Level)
			}
		})
	}
}

func TestAccessLogGeneratedCorrelationID(t *testing.T) {
	entry := serveLogging(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "")

	if entry.CorrelationID == "" {
		t.Error("correlation_id absent du log alors que le middleware amont en génère un")
	}
}

func TestResponseRecorderAccumulatesSize(t *testing.T) {
	entry := serveLogging(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "part1-")
		io.WriteString(w, "part2")
	}, "")

	if want := len("part1-part2"); entry.Bytes != want {
		t.Errorf("bytes = %d, want %d", entry.Bytes, want)
	}
}

func TestAccessLogUsesClientIPFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	extractor := newExtractor(t, "10.0.0.1")

	chain := extractor.Middleware(LoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.RemoteAddr = "10.0.0.1:80"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	chain.ServeHTTP(httptest.NewRecorder(), req)

	var entry accessLog
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log JSON invalide: %v", err)
	}
	if entry.IP != "198.51.100.9" {
		t.Errorf("ip = %q, want 198.51.100.9 (résolue via le contexte)", entry.IP)
	}
}

func TestAccessLogSuppressedAboveInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	chain := LoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/users", nil))

	if buf.Len() != 0 {
		t.Errorf("aucun log attendu au niveau WARN, reçu: %s", buf.String())
	}
}
