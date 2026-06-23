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
}

func NewProxyHandler(route config.RouteConfig) (*ProxyHandler, error) {
	parsedURL, err := url.Parse(route.DestinationURL)
	if err != nil {
		return nil, err
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			if route.StripPrefix {
				pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, route.PathPrefix)
				if pr.Out.URL.Path == "" {
					pr.Out.URL.Path = "/"
				}
				pr.Out.URL.RawPath = ""
			}
			pr.SetURL(parsedURL)
			pr.SetXForwarded()
		},
	}
	configureProxyErrorHandler(proxy)

	return &ProxyHandler{
		TargetURL:    parsedURL,
		ReverseProxy: proxy,
	}, nil
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ReverseProxy.ServeHTTP(w, r)
}

func configureProxyErrorHandler(proxy *httputil.ReverseProxy) {
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
		case errors.As(err, &maxBytesErr):
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		default:
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		}
	}
}

func TimeoutMiddleware(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func NewRouter(cfg *config.GatewayConfig, protect func(portal string, next http.Handler) http.Handler) (http.Handler, error) {
	mux := http.NewServeMux()

	for _, route := range cfg.Routes {
		var handler http.Handler
		handler, err := NewProxyHandler(route)
		if err != nil {
			return nil, err
		}

		timeout := time.Duration(route.EffectiveTimeoutSeconds(cfg.Server.TimeoutSeconds)) * time.Second
		if timeout > 0 {
			handler = TimeoutMiddleware(timeout, handler)
		}

		if route.RequireAuth {
			if protect == nil {
				return nil, fmt.Errorf("la route %s exige require_auth mais aucun middleware d'authentification n'est fourni", route.PathPrefix)
			}

			handler = protect(route.Portal, handler)
		}

		mux.Handle(route.PathPrefix, handler)
		mux.Handle(route.PathPrefix+"/", handler)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})

	return mux, nil
}
