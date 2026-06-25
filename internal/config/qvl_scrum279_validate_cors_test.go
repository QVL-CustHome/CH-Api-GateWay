package config

import "testing"

func newCORSConfig(environment string, origins []string) *GatewayConfig {
	cfg := &GatewayConfig{Environment: environment}
	cfg.Server.CORS.AllowedOrigins = origins
	return cfg
}

func TestScrum279ValidateCORSFailSafe(t *testing.T) {
	cases := []struct {
		name        string
		environment string
		origins     []string
		wantErr     bool
	}{
		{
			name:        "wildcard en development est autorise",
			environment: DefaultEnvironment,
			origins:     []string{WildcardOrigin},
			wantErr:     false,
		},
		{
			name:        "wildcard en production fait echouer le demarrage",
			environment: EnvironmentProduction,
			origins:     []string{WildcardOrigin},
			wantErr:     true,
		},
		{
			name:        "origine explicite en production est autorisee",
			environment: EnvironmentProduction,
			origins:     []string{"https://app.custhome.fr"},
			wantErr:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newCORSConfig(tc.environment, tc.origins)
			err := cfg.validateCORS()
			if tc.wantErr && err == nil {
				t.Fatalf("validateCORS() = nil, want error pour environnement %q et origines %v", tc.environment, tc.origins)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateCORS() = %v, want nil pour environnement %q et origines %v", err, tc.environment, tc.origins)
			}
		})
	}
}
