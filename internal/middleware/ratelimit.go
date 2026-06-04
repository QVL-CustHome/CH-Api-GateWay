package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Paramètres du nettoyage mémoire (US-08 scénario 4) : une IP silencieuse
// depuis plus de visitorTTL est purgée de la map.
const (
	visitorTTL      = 3 * time.Minute
	cleanupInterval = time.Minute
)

// visitor porte le Token Bucket d'une IP et sa date de dernière activité.
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter limite le trafic par adresse IP cliente (US-08 / SCRUM-12)
// via l'algorithme du Token Bucket (golang.org/x/time/rate). L'accès à la
// map des visiteurs est protégé par mutex ; une goroutine d'arrière-plan
// purge les IP inactives pour éviter toute fuite mémoire.
type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
	ttl      time.Duration
	interval time.Duration
	done     chan struct{}
}

// NewRateLimiter construit un limiteur (jetons/seconde + rafale max) et
// démarre sa goroutine de nettoyage.
func NewRateLimiter(requestsPerSecond float64, burst int) *RateLimiter {
	return newRateLimiter(rate.Limit(requestsPerSecond), burst, visitorTTL, cleanupInterval)
}

// newRateLimiter permet d'injecter le TTL et l'intervalle de nettoyage
// (raccourcis dans les tests).
func newRateLimiter(r rate.Limit, b int, ttl, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     r,
		burst:    b,
		ttl:      ttl,
		interval: interval,
		done:     make(chan struct{}),
	}
	go rl.cleanupVisitors()
	return rl
}

// Stop arrête la goroutine de nettoyage (arrêt propre / tests).
func (rl *RateLimiter) Stop() {
	close(rl.done)
}

// getVisitor retourne le limiteur de l'IP donnée, en le créant au besoin,
// et rafraîchit sa date de dernière activité.
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

// cleanupVisitors purge périodiquement les IP inactives, jusqu'à Stop().
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

// removeStaleVisitors supprime les entrées plus anciennes que le TTL.
func (rl *RateLimiter) removeStaleVisitors() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, v := range rl.visitors {
		if time.Since(v.lastSeen) > rl.ttl {
			delete(rl.visitors, ip)
		}
	}
}

// visitorCount retourne le nombre d'IP actuellement suivies.
func (rl *RateLimiter) visitorCount() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.visitors)
}

// extractIP résout l'adresse IP cliente d'origine : X-Forwarded-For en
// priorité (première IP de la chaîne, le gateway pouvant être derrière un
// load balancer), sinon l'adresse TCP brute.
func extractIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr sans port : utilisée telle quelle.
		return r.RemoteAddr
	}
	return host
}

// Middleware applique la limitation : dépassement du quota → 429 immédiat,
// la requête n'est jamais transmise aux backends.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.getVisitor(extractIP(r)).Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
