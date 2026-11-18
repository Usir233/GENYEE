package langfuse

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoadConfigFromEnv_AutoEnablesWithCredentials(t *testing.T) {
	t.Setenv("LANGFUSE_ENABLED", "")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("LANGFUSE_HOST", "https://example.langfuse.com")

	cfg := LoadConfigFromEnv()

	if !cfg.Enabled {
		t.Fatalf("expected Enabled=true when both keys are set, got false")
	}
	if cfg.Host != "https://example.langfuse.com" {
		t.Errorf("unexpected host: %q", cfg.Host)
	}
	if cfg.SampleRate != 1.0 {
		t.Errorf("expected default SampleRate=1.0, got %v", cfg.SampleRate)
	}
}

func TestLoadConfigFromEnv_CredentialsNeverSelectAnImplicitHost(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("LANGFUSE_HOST", "")
	for _, enabled := range []string{"", "false", "true"} {
		t.Setenv("LANGFUSE_ENABLED", enabled)
		cfg := LoadConfigFromEnv()
		if cfg.Enabled || cfg.Host != "" {
			t.Fatalf(
				"flag %q selected an implicit tracing destination: enabled=%v host=%q",
				enabled, cfg.Enabled, cfg.Host,
			)
		}
	}
}

func TestLoadConfigFromEnv_DisabledWithoutKeys(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	t.Setenv("LANGFUSE_ENABLED", "")

	cfg := LoadConfigFromEnv()
	if cfg.Enabled {
		t.Fatalf("expected Enabled=false when no keys set")
	}
}

func TestLoadConfigFromEnv_ExplicitDisableOverridesKeys(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")
	t.Setenv("LANGFUSE_ENABLED", "false")

	cfg := LoadConfigFromEnv()
	if cfg.Enabled {
		t.Fatalf("expected Enabled=false when LANGFUSE_ENABLED=false")
	}
}

func TestLoadConfigFromEnv_FlushIntervalAcceptsSecondsAndDuration(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")

	t.Setenv("LANGFUSE_FLUSH_INTERVAL", "500ms")
	cfg := LoadConfigFromEnv()
	if cfg.FlushInterval != 500*time.Millisecond {
		t.Errorf("expected 500ms, got %v", cfg.FlushInterval)
	}

	t.Setenv("LANGFUSE_FLUSH_INTERVAL", "7")
	cfg = LoadConfigFromEnv()
	if cfg.FlushInterval != 7*time.Second {
		t.Errorf("expected 7s (bare integer), got %v", cfg.FlushInterval)
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"disabled is always valid", Config{Enabled: false}, false},
		{"enabled without host fails", Config{Enabled: true, PublicKey: "pk", SecretKey: "sk"}, true},
		{"enabled without keys passes (keyless standard OTLP)", Config{Enabled: true, Host: "https://x"}, false},
		{"enabled with partial keys fails", Config{Enabled: true, Host: "https://x", PublicKey: "pk"}, true},
		{
			"enabled with all fields passes",
			Config{Enabled: true, Host: "https://x", PublicKey: "pk", SecretKey: "sk"},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestConfigValidateNormalizeAndReinitShutdown(t *testing.T) {
	badCases := []Config{
		{Enabled: true, Host: "ftp://langfuse.example.com"},
		{Enabled: true, Host: "://bad-url"},
		{Enabled: true, Host: "langfuse.example.com"},
		{Enabled: true, Host: "https://x", SampleRate: -0.1},
		{Enabled: true, Host: "https://x", SampleRate: 1.5},
		{Enabled: true, Host: "https://x", SampleRate: math.NaN()},
		{Enabled: true, Host: "https://x", SampleRate: math.Inf(1)},
	}
	for i, cfg := range badCases {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d (%+v): expected validation error, got nil", i, cfg)
		}
	}

	t.Setenv("LANGFUSE_HOST", "https://example.langfuse.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("LANGFUSE_QUEUE_SIZE", "999999999")
	t.Setenv("LANGFUSE_FLUSH_AT", "999999999")
	envCfg := LoadConfigFromEnv()
	if envCfg.QueueSize <= 0 || envCfg.QueueSize > 65536 {
		t.Fatalf("expected bounded QueueSize <= 65536, got %d", envCfg.QueueSize)
	}
	if envCfg.FlushAt <= 0 || envCfg.FlushAt > envCfg.QueueSize {
		t.Fatalf("expected bounded FlushAt <= QueueSize (%d), got %d", envCfg.QueueSize, envCfg.FlushAt)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m1, err := Init(Config{
		Enabled: true,
		Host:    srv.URL,
	})
	if err != nil {
		t.Fatalf("Init programmatic defaults failed: %v", err)
	}
	if m1.cfg.QueueSize != 2048 ||
		m1.cfg.FlushAt != 15 ||
		m1.cfg.SampleRate != 1.0 ||
		m1.cfg.FlushInterval != 3*time.Second {
		t.Fatalf("programmatic Init did not normalize zero-value defaults: %+v", m1.cfg)
	}
	if !m1.Enabled() {
		t.Fatal("expected m1 to be enabled")
	}

	// Re-initializing global singleton must shut down the replaced BatchSpanProcessor manager.
	_, err = Init(Config{Enabled: false})
	if err != nil {
		t.Fatalf("re-Init disabled failed: %v", err)
	}
	if m1.Enabled() {
		t.Fatal("expected replaced manager m1 to be shut down on re-Init")
	}
}
