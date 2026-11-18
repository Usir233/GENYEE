package config

import "testing"

// TestApplyAuthAndTenantDefaults_RegistrationModeEnv pins down the canonical
// GENYEE_AUTH_REGISTRATION_MODE env-over-YAML contract ("self_serve",
// "invite_register", "invite_only") and ensures retired legacy
// DISABLE_REGISTRATION has no silent side effect.
func TestApplyAuthAndTenantDefaults_RegistrationModeEnv(t *testing.T) {
	cases := []struct {
		name     string
		envMode  string
		cfgMode  string
		expected string
	}{
		{"env invite_only overrides empty YAML", AuthRegistrationModeInviteOnly, "", AuthRegistrationModeInviteOnly},
		{
			"env invite_register overrides explicit self_serve YAML",
			AuthRegistrationModeInviteRegister,
			AuthRegistrationModeSelfServe,
			AuthRegistrationModeInviteRegister,
		},
		{
			"env self_serve overrides explicit invite_only YAML",
			AuthRegistrationModeSelfServe,
			AuthRegistrationModeInviteOnly,
			AuthRegistrationModeSelfServe,
		},
		{"unset falls back to default self_serve", "", "", AuthRegistrationModeSelfServe},
		{"unset keeps explicit invite_only YAML", "", AuthRegistrationModeInviteOnly, AuthRegistrationModeInviteOnly},
		{
			"unset keeps invitation registration YAML",
			"",
			AuthRegistrationModeInviteRegister,
			AuthRegistrationModeInviteRegister,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GENYEE_AUTH_REGISTRATION_MODE", tc.envMode)
			t.Setenv("GENYEE_TENANT_ENABLE_RBAC", "")
			t.Setenv("GENYEE_TENANT_MAX_OWNED_PER_USER", "")
			t.Setenv("GENYEE_TENANT_SELF_SERVICE_CREATION_ENABLED", "")

			cfg := &Config{Auth: &AuthConfig{RegistrationMode: tc.cfgMode}}
			applyAuthAndTenantDefaults(cfg)

			if cfg.Auth.RegistrationMode != tc.expected {
				t.Fatalf("registration_mode = %q, want %q", cfg.Auth.RegistrationMode, tc.expected)
			}
		})
	}

	t.Run("invalid GENYEE_AUTH_REGISTRATION_MODE fails validation", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_REGISTRATION_MODE", "open_for_all")
		cfg := &Config{Auth: &AuthConfig{}}
		applyAuthAndTenantDefaults(cfg)
		if err := ValidateConfig(cfg); err == nil {
			t.Fatal("ValidateConfig unexpectedly accepted an invalid registration mode")
		}
	})

	t.Run("retired DISABLE_REGISTRATION is ignored", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_REGISTRATION_MODE", "")
		t.Setenv("DISABLE_REGISTRATION", "true")
		cfg := &Config{Auth: &AuthConfig{RegistrationMode: AuthRegistrationModeSelfServe}}
		applyAuthAndTenantDefaults(cfg)
		if cfg.Auth.RegistrationMode != AuthRegistrationModeSelfServe {
			t.Fatalf("retired DISABLE_REGISTRATION mutated registration_mode to %q", cfg.Auth.RegistrationMode)
		}
	})
}

func TestApplyAuthAndTenantDefaults_SelfServiceTenantCreation(t *testing.T) {
	t.Run("defaults enabled", func(t *testing.T) {
		t.Setenv("GENYEE_TENANT_SELF_SERVICE_CREATION_ENABLED", "")
		cfg := &Config{Tenant: &TenantConfig{}}

		applyAuthAndTenantDefaults(cfg)

		if !cfg.Tenant.IsSelfServiceCreationEnabled() {
			t.Fatal("self-service tenant creation should default to enabled")
		}
	})

	t.Run("environment disables yaml default", func(t *testing.T) {
		t.Setenv("GENYEE_TENANT_SELF_SERVICE_CREATION_ENABLED", "false")
		on := true
		cfg := &Config{Tenant: &TenantConfig{SelfServiceCreationEnabled: &on}}

		applyAuthAndTenantDefaults(cfg)

		if cfg.Tenant.IsSelfServiceCreationEnabled() {
			t.Fatal("environment override should disable self-service tenant creation")
		}
	})
}

func TestApplyAuthAndTenantDefaults_DefaultTenantMode(t *testing.T) {
	t.Run("historical default creates a personal tenant", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_DEFAULT_TENANT_MODE", "")
		cfg := &Config{Auth: &AuthConfig{}}

		applyAuthAndTenantDefaults(cfg)

		if cfg.Auth.DefaultTenantMode != AuthDefaultTenantModeCreatePersonal {
			t.Fatalf(
				"default_tenant_mode = %q, want %q",
				cfg.Auth.DefaultTenantMode,
				AuthDefaultTenantModeCreatePersonal,
			)
		}
	})

	t.Run("environment overrides yaml", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_DEFAULT_TENANT_MODE", AuthDefaultTenantModeTenantless)
		cfg := &Config{Auth: &AuthConfig{DefaultTenantMode: AuthDefaultTenantModeCreatePersonal}}

		applyAuthAndTenantDefaults(cfg)

		if cfg.Auth.DefaultTenantMode != AuthDefaultTenantModeTenantless {
			t.Fatalf("default_tenant_mode = %q, want %q", cfg.Auth.DefaultTenantMode, AuthDefaultTenantModeTenantless)
		}
	})

	t.Run("invalid environment value fails validation", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_DEFAULT_TENANT_MODE", "create_magic")
		cfg := &Config{Auth: &AuthConfig{}}

		applyAuthAndTenantDefaults(cfg)

		if err := ValidateConfig(cfg); err == nil {
			t.Fatal("ValidateConfig unexpectedly accepted an invalid default tenant mode")
		}
	})
}

// TestApplyAuthAndTenantDefaults_CrossTenantAccess is a regression test for the
// env-binding gap: viper.AutomaticEnv has no SetEnvPrefix, so
// GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS is never bound to the nested struct
// automatically. applyAuthAndTenantDefaults must read it explicitly (like RBAC);
// without that, only config.yaml's enable_cross_tenant_access would take effect
// and the documented env override would be silently ignored.
func TestApplyAuthAndTenantDefaults_CrossTenantAccess(t *testing.T) {
	t.Run("environment true enables cross-tenant access", func(t *testing.T) {
		t.Setenv("GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS", "true")
		cfg := &Config{Tenant: &TenantConfig{EnableCrossTenantAccess: false}}

		applyAuthAndTenantDefaults(cfg)

		if !cfg.Tenant.EnableCrossTenantAccess {
			t.Fatal("GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS=true should enable cross-tenant access")
		}
	})

	t.Run("environment false overrides yaml true", func(t *testing.T) {
		t.Setenv("GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS", "false")
		cfg := &Config{Tenant: &TenantConfig{EnableCrossTenantAccess: true}}

		applyAuthAndTenantDefaults(cfg)

		if cfg.Tenant.EnableCrossTenantAccess {
			t.Fatal("GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS=false should disable cross-tenant access")
		}
	})

	t.Run("case-insensitive TRUE also enables", func(t *testing.T) {
		t.Setenv("GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS", "TRUE")
		cfg := &Config{Tenant: &TenantConfig{EnableCrossTenantAccess: false}}

		applyAuthAndTenantDefaults(cfg)

		if !cfg.Tenant.EnableCrossTenantAccess {
			t.Fatal(
				"GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS=TRUE should enable cross-tenant access",
			)
		}
	})

	t.Run("unset leaves yaml value untouched", func(t *testing.T) {
		t.Setenv("GENYEE_TENANT_ENABLE_CROSS_TENANT_ACCESS", "")
		cfg := &Config{Tenant: &TenantConfig{EnableCrossTenantAccess: true}}

		applyAuthAndTenantDefaults(cfg)

		if !cfg.Tenant.EnableCrossTenantAccess {
			t.Fatal("empty env should leave the YAML-provided cross-tenant access value untouched")
		}
	})
}

func TestApplyAuthAndTenantDefaults_ComplexPasswordEnabledEnv(t *testing.T) {
	t.Run("1 enables via ParseBool", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_COMPLEX_PASSWORD_ENABLED", "1")
		cfg := &Config{Auth: &AuthConfig{}}
		applyAuthAndTenantDefaults(cfg)
		if !cfg.Auth.ComplexPasswordEnabled {
			t.Fatal("GENYEE_AUTH_COMPLEX_PASSWORD_ENABLED=1 should enable complex passwords")
		}
	})
	t.Run("false disables", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_COMPLEX_PASSWORD_ENABLED", "false")
		cfg := &Config{Auth: &AuthConfig{ComplexPasswordEnabled: true}}
		applyAuthAndTenantDefaults(cfg)
		if cfg.Auth.ComplexPasswordEnabled {
			t.Fatal("GENYEE_AUTH_COMPLEX_PASSWORD_ENABLED=false should disable complex passwords")
		}
	})
	t.Run("unset leaves yaml", func(t *testing.T) {
		t.Setenv("GENYEE_AUTH_COMPLEX_PASSWORD_ENABLED", "")
		cfg := &Config{Auth: &AuthConfig{ComplexPasswordEnabled: true}}
		applyAuthAndTenantDefaults(cfg)
		if !cfg.Auth.ComplexPasswordEnabled {
			t.Fatal("empty env should leave YAML complex-password flag untouched")
		}
	})
}

func TestValidateInvitationRegistrationMode(t *testing.T) {
	cfg := &Config{Auth: &AuthConfig{RegistrationMode: AuthRegistrationModeInviteRegister}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("invitation registration mode rejected: %v", err)
	}
}

func TestApplyAuthAndTenantDefaults_EnableRBACFailsClosedOnTruthyOrInvalidEnv(t *testing.T) {
	cases := []struct {
		name    string
		envVal  string
		yamlVal *bool
		want    bool
	}{
		{"unset defaults to enforced", "", nil, true},
		{"1 keeps RBAC enforced", "1", nil, true},
		{"true keeps RBAC enforced", "true", nil, true},
		{"0 disables RBAC explicitly", "0", nil, false},
		{"false disables RBAC explicitly", "false", nil, false},
		{"malformed typo preserves default enforcement", "ture", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GENYEE_TENANT_ENABLE_RBAC", tc.envVal)
			t.Setenv("GENYEE_AUTH_REGISTRATION_MODE", "")
			cfg := &Config{Tenant: &TenantConfig{EnableRBAC: tc.yamlVal}}
			applyAuthAndTenantDefaults(cfg)
			if got := cfg.Tenant.IsRBACEnforced(); got != tc.want {
				t.Fatalf("GENYEE_TENANT_ENABLE_RBAC=%q: IsRBACEnforced()=%v, want %v", tc.envVal, got, tc.want)
			}
		})
	}

	t.Run("FRONTEND_BASE_URL populates cfg.FrontendBaseURL", func(t *testing.T) {
		t.Setenv("FRONTEND_BASE_URL", "https://portal.genyee.example")
		cfg := &Config{}
		applyAuthAndTenantDefaults(cfg)
		if cfg.FrontendBaseURL != "https://portal.genyee.example" {
			t.Fatalf("FrontendBaseURL = %q, want https://portal.genyee.example", cfg.FrontendBaseURL)
		}
	})
}
