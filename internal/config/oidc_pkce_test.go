package config

import "testing"

func TestOIDCPKCEEnvOverride(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		initial bool
		want    bool
	}{
		{"true enables", "true", false, true},
		{"false disables", "false", true, false},
		{"1 enables via ParseBool", "1", false, true},
		{"0 disables via ParseBool", "0", true, false},
		{"malformed value fails closed preserving enabled PKCE", "ture", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OIDC_AUTH_PKCE_ENABLED", tc.value)
			cfg := &Config{OIDCAuth: &OIDCAuthConfig{PKCEEnabled: tc.initial}}
			applyOIDCEnvOverrides(cfg)
			if cfg.OIDCAuth.PKCEEnabled != tc.want {
				t.Fatalf("OIDC_AUTH_PKCE_ENABLED=%q (initial=%v): got %v, want %v",
					tc.value, tc.initial, cfg.OIDCAuth.PKCEEnabled, tc.want)
			}
		})
	}

	t.Run("OIDC_AUTH_ENABLE=1 enables OIDC", func(t *testing.T) {
		t.Setenv("OIDC_AUTH_ENABLE", "1")
		cfg := &Config{OIDCAuth: &OIDCAuthConfig{}}
		applyOIDCEnvOverrides(cfg)
		if !cfg.OIDCAuth.Enable {
			t.Fatal("OIDC_AUTH_ENABLE=1 should enable OIDC")
		}
	})
}
