package client

import (
	"testing"
)

func TestEffectiveForAddrStripsGlobalTokenOnUntrustedEndpoints(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "docreader:50051")

	base := &AuthConfig{
		TLSEnabled: false,
		AuthToken:  "global-docreader-secret",
	}

	if got := base.EffectiveForAddr("docreader:50051"); got.AuthToken != "global-docreader-secret" {
		t.Fatalf("expected configured DOCREADER_ADDR to retain AuthToken, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("203.0.113.10:50051"); got.AuthToken != "" {
		t.Fatalf("expected untrusted external plaintext endpoint to strip AuthToken, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("127.0.0.1:50051"); got.AuthToken != "" {
		t.Fatalf("expected non-matching endpoint to strip AuthToken when DOCREADER_ADDR is set, got %q", got.AuthToken)
	}
}

func TestEffectiveForAddrAllowsLoopbackWhenDocReaderAddrUnset(t *testing.T) {
	t.Setenv("DOCREADER_ADDR", "")

	base := &AuthConfig{
		TLSEnabled: false,
		AuthToken:  "local-dev-token",
	}

	if got := base.EffectiveForAddr("127.0.0.1:50051"); got.AuthToken != "local-dev-token" {
		t.Fatalf("expected loopback to retain token when DOCREADER_ADDR is unset, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("localhost:50051"); got.AuthToken != "local-dev-token" {
		t.Fatalf("expected localhost to retain token when DOCREADER_ADDR is unset, got %q", got.AuthToken)
	}
	if got := base.EffectiveForAddr("198.51.100.25:50051"); got.AuthToken != "" {
		t.Fatalf("expected external endpoint to strip token when DOCREADER_ADDR is unset, got %q", got.AuthToken)
	}
}
