package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const clientIPKey contextKey = "client_ip"

type IPExtractor struct {
	trusted []netip.Prefix
}

func NewIPExtractor(trustedProxies []string) (*IPExtractor, error) {
	prefixes := make([]netip.Prefix, 0, len(trustedProxies))
	for _, entry := range trustedProxies {
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			prefixes = append(prefixes, prefix)
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies: %q n'est ni une IP ni un CIDR valide", entry)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return &IPExtractor{trusted: prefixes}, nil
}

func (e *IPExtractor) isTrusted(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range e.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (e *IPExtractor) ClientIP(r *http.Request) string {
	remote := remoteHost(r.RemoteAddr)
	if len(e.trusted) == 0 || !e.isTrusted(remote) {
		return remote
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return remote
	}

	parts := strings.Split(forwarded, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if ip != "" && !e.isTrusted(ip) {
			return ip
		}
	}
	return remote
}

func (e *IPExtractor) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), clientIPKey, e.ClientIP(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ClientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey).(string)
	return ip
}
