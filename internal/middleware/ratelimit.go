package middleware

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	visitorTTL      = 3 * time.Minute
	cleanupInterval = time.Minute
)

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	visitors  map[string]*visitor
	mu        sync.RWMutex
	rate      rate.Limit
	burst     int
	ttl       time.Duration
	interval  time.Duration
	extractor *IPExtractor
	exempt    map[string]bool
	done      chan struct{}
}

func NewRateLimiter(requestsPerSecond float64, burst int, extractor *IPExtractor, exemptPaths ...string) *RateLimiter {
	return newRateLimiter(rate.Limit(requestsPerSecond), burst, visitorTTL, cleanupInterval, extractor, exemptPaths...)
}

func newRateLimiter(r rate.Limit, b int, ttl, interval time.Duration, extractor *IPExtractor, exemptPaths ...string) *RateLimiter {
	exempt := make(map[string]bool, len(exemptPaths))
	for _, p := range exemptPaths {
		exempt[p] = true
	}
	rl := &RateLimiter{
		visitors:  make(map[string]*visitor),
		rate:      r,
		burst:     b,
		ttl:       ttl,
		interval:  interval,
		extractor: extractor,
		exempt:    exempt,
		done:      make(chan struct{}),
	}
	go rl.cleanupVisitors()
	return rl
}

func (rl *RateLimiter) Stop() {
	close(rl.done)
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) cleanupVisitors() {
	ticker := time.NewTicker(rl.interval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.removeStaleVisitors()
		}
	}
}

func (rl *RateLimiter) removeStaleVisitors() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, v := range rl.visitors {
		if time.Since(v.lastSeen) > rl.ttl {
			delete(rl.visitors, ip)
		}
	}
}

func (rl *RateLimiter) visitorCount() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.visitors)
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl.exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if !rl.getVisitor(rl.extractor.ClientIP(r)).Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
