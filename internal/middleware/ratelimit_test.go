package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func fireRequests(t *testing.T, rl *RateLimiter, n int, remoteAddr, forwardedFor string) (passed, rejected int) {
	t.Helper()
	backendCalls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware(next)

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		req.RemoteAddr = remoteAddr
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		switch rec.Code {
		case http.StatusOK:
			passed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Fatalf("statut inattendu: %d", rec.Code)
		}
	}

	if backendCalls != passed {
		t.Errorf("le backend a été appelé %d fois pour %d requêtes acceptées", backendCalls, passed)
	}
	return passed, rejected
}

func TestRateLimitUnderQuota(t *testing.T) {
	rl := NewRateLimiter(10, 20, newExtractor(t))
	t.Cleanup(rl.Stop)

	passed, rejected := fireRequests(t, rl, 5, "203.0.113.7:54321", "")

	if passed != 5 || rejected != 0 {
		t.Errorf("passées = %d, rejetées = %d ; want 5 / 0", passed, rejected)
	}
}

func TestRateLimitThrottlingOverQuota(t *testing.T) {
	rl := NewRateLimiter(10, 20, newExtractor(t))
	t.Cleanup(rl.Stop)

	passed, rejected := fireRequests(t, rl, 25, "203.0.113.7:54321", "")

	if passed < 20 || passed > 21 {
		t.Errorf("passées = %d, want 20 (±1 jeton régénéré)", passed)
	}
	if rejected != 25-passed {
		t.Errorf("rejetées = %d, want %d", rejected, 25-passed)
	}
	if rejected < 4 {
		t.Errorf("rejetées = %d : le throttling n'a pas eu lieu", rejected)
	}
}

func TestRateLimitIsPerIP(t *testing.T) {
	rl := NewRateLimiter(1, 2, newExtractor(t))
	t.Cleanup(rl.Stop)

	fireRequests(t, rl, 5, "203.0.113.7:1111", "")

	passed, rejected := fireRequests(t, rl, 2, "198.51.100.9:2222", "")
	if passed != 2 || rejected != 0 {
		t.Errorf("seconde IP : passées = %d, rejetées = %d ; want 2 / 0", passed, rejected)
	}
}

func TestRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	rl := NewRateLimiter(1, 2, newExtractor(t))
	t.Cleanup(rl.Stop)

	attacker := "203.0.113.7:1111"
	total := 0
	for i := 0; i < 6; i++ {
		_, rejected := fireRequests(t, rl, 1, attacker, "1.2.3."+string(rune('0'+i)))
		total += rejected
	}

	if total == 0 {
		t.Error("un XFF différent par requête ne doit pas contourner le rate limiting")
	}
	if got := rl.visitorCount(); got != 1 {
		t.Errorf("visitorCount = %d, want 1 (un seul bucket pour l'attaquant)", got)
	}
}

func TestRateLimitUsesForwardedClientIPBehindTrustedProxy(t *testing.T) {
	rl := NewRateLimiter(1, 2, newExtractor(t, "10.0.0.1"))
	t.Cleanup(rl.Stop)

	lbAddr := "10.0.0.1:80"

	_, rejected := fireRequests(t, rl, 3, lbAddr, "198.51.100.9")
	if rejected == 0 {
		t.Error("le premier client aurait dû être throttlé")
	}

	passed, rejected := fireRequests(t, rl, 2, lbAddr, "203.0.113.50")
	if passed != 2 || rejected != 0 {
		t.Errorf("second client : passées = %d, rejetées = %d ; want 2 / 0", passed, rejected)
	}
}

func TestRateLimitExemptPath(t *testing.T) {
	rl := NewRateLimiter(1, 1, newExtractor(t), "/health")
	t.Cleanup(rl.Stop)

	fireRequests(t, rl, 3, "203.0.113.7:1111", "")

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.RemoteAddr = "203.0.113.7:1111"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut /health = %d (itération %d), want 200 même quota épuisé", rec.Code, i)
		}
	}
}

func TestRateLimitUsesClientIPFromContext(t *testing.T) {
	rl := NewRateLimiter(1, 2, newExtractor(t))
	t.Cleanup(rl.Stop)
	extractor := newExtractor(t, "10.0.0.1")

	handler := extractor.Middleware(rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	for _, clientIP := range []string{"198.51.100.9", "203.0.113.50"} {
		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		req.RemoteAddr = "10.0.0.1:80"
		req.Header.Set("X-Forwarded-For", clientIP)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("statut = %d pour %s, want 200", rec.Code, clientIP)
		}
	}

	if got := rl.visitorCount(); got != 2 {
		t.Errorf("visitorCount = %d, want 2 (un bucket par IP du contexte)", got)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	rl := NewRateLimiter(1, 1, newExtractor(t))

	rl.Stop()
	rl.Stop()
}

func TestRemoveStaleVisitors(t *testing.T) {
	rl := newRateLimiter(rate.Limit(10), 20, 3*time.Minute, time.Minute, newExtractor(t))
	t.Cleanup(rl.Stop)

	rl.getVisitor("203.0.113.7")
	rl.getVisitor("198.51.100.9")

	rl.mu.Lock()
	rl.visitors["203.0.113.7"].lastSeen = time.Now().Add(-4 * time.Minute)
	rl.mu.Unlock()

	rl.removeStaleVisitors()

	rl.mu.RLock()
	_, oldExists := rl.visitors["203.0.113.7"]
	_, recentExists := rl.visitors["198.51.100.9"]
	rl.mu.RUnlock()

	if oldExists {
		t.Error("l'IP inactive depuis plus de 3 minutes devait être purgée")
	}
	if !recentExists {
		t.Error("l'IP récente ne devait pas être purgée")
	}
}

func TestCleanupGoroutine(t *testing.T) {
	rl := newRateLimiter(rate.Limit(10), 20, 30*time.Millisecond, 10*time.Millisecond, newExtractor(t))
	t.Cleanup(rl.Stop)

	rl.getVisitor("203.0.113.7")
	if got := rl.visitorCount(); got != 1 {
		t.Fatalf("visitorCount = %d, want 1", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for rl.visitorCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := rl.visitorCount(); got != 0 {
		t.Errorf("visitorCount = %d après expiration du TTL, want 0", got)
	}
}
