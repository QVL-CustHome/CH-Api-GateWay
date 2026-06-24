package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/custhome/ch-api-gateway/internal/config"
)

func TestScrum261BlockInternalReturns404ForAuthStrippedPath(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL, StripPrefix: true},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/auth/internal/users")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if captured.Path != "" {
		t.Errorf("le backend a été appelé avec %q, want aucun appel", captured.Path)
	}
}

func TestScrum261BlockInternalForAllStripPrefixBackends(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		requestURL string
	}{
		{"admin", "/api/admin", "/api/admin/internal/reset"},
		{"drive", "/api/drive", "/api/drive/internal/purge"},
		{"budgy", "/api/budgy", "/api/budgy/internal/sync"},
		{"auth", "/api/auth", "/api/auth/internal/x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend, captured := newBackend(t, http.StatusOK, "ok")
			router := newGatewayRouter(t, []config.RouteConfig{
				{PathPrefix: tc.prefix, DestinationURL: backend.URL, StripPrefix: true},
			})
			gateway := httptest.NewServer(router)
			t.Cleanup(gateway.Close)

			resp, err := http.Get(gateway.URL + tc.requestURL)
			if err != nil {
				t.Fatalf("requête vers le gateway: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404", resp.StatusCode)
			}
			if captured.Path != "" {
				t.Errorf("le backend a été appelé avec %q, want aucun appel", captured.Path)
			}
		})
	}
}

func TestScrum261BlockInternalRootStrippedPath(t *testing.T) {
	backend, captured := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL, StripPrefix: true},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/auth/internal")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if captured.Path != "" {
		t.Errorf("le backend a été appelé avec %q, want aucun appel", captured.Path)
	}
}

func TestScrum261LegitimateRoutesStillForwarded(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		requestURL string
		wantPath   string
	}{
		{"auth_login", "/api/auth", "/api/auth/login", "/login"},
		{"admin_users", "/api/admin", "/api/admin/users", "/users"},
		{"drive_files", "/api/drive", "/api/drive/files/list", "/files/list"},
		{"budgy_accounts", "/api/budgy", "/api/budgy/accounts", "/accounts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend, captured := newBackend(t, http.StatusOK, "ok")
			router := newGatewayRouter(t, []config.RouteConfig{
				{PathPrefix: tc.prefix, DestinationURL: backend.URL, StripPrefix: true},
			})
			gateway := httptest.NewServer(router)
			t.Cleanup(gateway.Close)

			resp, err := http.Get(gateway.URL + tc.requestURL)
			if err != nil {
				t.Fatalf("requête vers le gateway: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
			if captured.Path != tc.wantPath {
				t.Errorf("path reçu par le backend = %q, want %q", captured.Path, tc.wantPath)
			}
		})
	}
}

func TestScrum261PathContainingInternalSubstringIsNotBlocked(t *testing.T) {
	cases := []struct {
		name       string
		requestURL string
		wantPath   string
	}{
		{"internalisation", "/api/x/internalisation", "/internalisation"},
		{"internal_suffix_word", "/api/x/myinternal", "/myinternal"},
		{"internals_plural", "/api/x/internals", "/internals"},
		{"internal_in_filename", "/api/x/docs/internal-policy.pdf", "/docs/internal-policy.pdf"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend, captured := newBackend(t, http.StatusOK, "ok")
			router := newGatewayRouter(t, []config.RouteConfig{
				{PathPrefix: "/api/x", DestinationURL: backend.URL, StripPrefix: true},
			})
			gateway := httptest.NewServer(router)
			t.Cleanup(gateway.Close)

			resp, err := http.Get(gateway.URL + tc.requestURL)
			if err != nil {
				t.Fatalf("requête vers le gateway: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200 (faux positif)", resp.StatusCode)
			}
			if captured.Path != tc.wantPath {
				t.Errorf("path reçu par le backend = %q, want %q", captured.Path, tc.wantPath)
			}
		})
	}
}

func TestScrum261UnknownRouteStill404(t *testing.T) {
	backend, _ := newBackend(t, http.StatusOK, "ok")
	router := newGatewayRouter(t, []config.RouteConfig{
		{PathPrefix: "/api/auth", DestinationURL: backend.URL, StripPrefix: true},
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/api/unknown/whatever")
	if err != nil {
		t.Fatalf("requête vers le gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
