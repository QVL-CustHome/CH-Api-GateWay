// Package proxy implémente la redirection dynamique par path vers les
// microservices cibles via un Reverse Proxy (US-02 / SCRUM-6), avec
// réécriture optionnelle du préfixe de routage (US-03 / SCRUM-7).
package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/custhome/ch-api-gateway/internal/config"
)

// ProxyHandler relaie les requêtes HTTP vers un microservice cible
// en préservant méthode, body, query parameters et réponse du backend.
type ProxyHandler struct {
	TargetURL    *url.URL
	ReverseProxy *httputil.ReverseProxy
	Prefix       string
	StripPrefix  bool
}

// NewProxyHandler construit un handler de reverse proxy pour la route donnée.
// Si StripPrefix est actif, le préfixe de routage est supprimé de l'URL à la
// volée (via le Director) avant transfert au backend ; un chemin vide après
// suppression est remplacé par "/".
func NewProxyHandler(route config.RouteConfig) (*ProxyHandler, error) {
	parsedURL, err := url.Parse(route.DestinationURL)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(parsedURL)

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

// ServeHTTP transfère la requête au backend cible.
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Host = h.TargetURL.Host
	h.ReverseProxy.ServeHTTP(w, r)
}

// NewRouter construit le multiplexeur principal du gateway : chaque
// path_prefix de la configuration est associé à son reverse proxy, et
// toute requête sans correspondance reçoit un 404 sans jamais être
// transmise à un microservice.
//
// protect est le décorateur d'authentification (US-05) appliqué uniquement
// aux routes configurées avec require_auth: true ; il est requis dès qu'une
// telle route existe.
func NewRouter(cfg *config.GatewayConfig, protect func(http.Handler) http.Handler) (http.Handler, error) {
	mux := http.NewServeMux()

	for _, route := range cfg.Routes {
		var handler http.Handler
		handler, err := NewProxyHandler(route)
		if err != nil {
			return nil, err
		}

		// US-05 : seules les routes protégées passent par la validation
		// d'authentification ; les routes publiques restent directes.
		if route.RequireAuth {
			if protect == nil {
				return nil, fmt.Errorf("la route %s exige require_auth mais aucun middleware d'authentification n'est fourni", route.PathPrefix)
			}
			handler = protect(handler)
		}

		// "/api/auth" matche le préfixe exact, "/api/auth/" tout le sous-arbre.
		mux.Handle(route.PathPrefix, handler)
		mux.Handle(route.PathPrefix+"/", handler)
	}

	// Catch-all : aucune route ne correspond → 404 immédiat.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})

	return mux, nil
}
