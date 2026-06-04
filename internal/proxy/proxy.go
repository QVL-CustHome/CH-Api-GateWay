package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/custhome/ch-api-gateway/internal/config"
)

type ProxyHandler struct {
	TargetURL    *url.URL
	ReverseProxy *httputil.ReverseProxy
	Prefix       string
	StripPrefix  bool
}

func NewProxyHandler(route config.RouteConfig) (*ProxyHandler, error) {
	parsedURL, err := url.Parse(route.DestinationURL)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(parsedURL)
	configureProxyErrorHandler(proxy)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if route.StripPrefix {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, route.PathPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
		}
	}

	return &ProxyHandler{
		TargetURL:    parsedURL,
		ReverseProxy: proxy,
		Prefix:       route.PathPrefix,
		StripPrefix:  route.StripPrefix,
	}, nil
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Host = h.TargetURL.Host
	h.ReverseProxy.ServeHTTP(w, r)
}

func configureProxyErrorHandler(proxy *httputil.ReverseProxy) {
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}
}

func TimeoutMiddleware(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func NewRouter(cfg *config.GatewayConfig, protect func(http.Handler) http.Handler) (http.Handler, error) {
	mux := http.NewServeMux()

	timeout := time.Duration(cfg.Server.TimeoutSeconds) * time.Second

	for _, route := range cfg.Routes {
		var handler http.Handler
		handler, err := NewProxyHandler(route)
		if err != nil {
			return nil, err
		}

		if timeout > 0 {
			handler = TimeoutMiddleware(timeout, handler)
		}

		if route.RequireAuth {
			if protect == nil {
				return nil, fmt.Errorf("la route %s exige require_auth mais aucun middleware d'authentification n'est fourni", route.PathPrefix)
			}
			handler = protect(handler)
		}

		mux.Handle(route.PathPrefix, handler)
		mux.Handle(route.PathPrefix+"/", handler)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})

	return mux, nil
}
