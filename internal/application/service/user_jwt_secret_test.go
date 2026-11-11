package service

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func isolateJWTSecret(t *testing.T) {
	t.Helper()
	previous := jwtSecretValue
	jwtSecretValue = sync.OnceValues(loadJWTSecret)
	t.Cleanup(func() { jwtSecretValue = previous })
	for _, key := range []string{"GIN_MODE", "APP_ENV", "ENV", "JWT_SECRET", "SYSTEM_AES_KEY"} {
		t.Setenv(key, "")
	}
}

func TestJWTSecretProductionFailureRemainsClosed(t *testing.T) {
	for _, placeholder := range []string{"", "weknora-jwt-secret", "CHANGE-ME-jwt-secret", "too-short-jwt-secret"} {
		t.Run(placeholder, func(t *testing.T) {
			isolateJWTSecret(t)
			t.Setenv("GIN_MODE", "release")
			t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
			t.Setenv("JWT_SECRET", placeholder)
			// A recovered first-request panic must not poison initialization and
			// allow later token operations to use an empty HMAC signing key.
			for range 3 {
				require.Panics(t, func() { _ = getJwtSecret() })
			}
			require.Panics(t, func() { NewUserService(nil, nil, nil, nil, nil, nil) })
		})
	}
}

func TestReleaseModeRejectsMissingOrPlaceholderAESKey(t *testing.T) {
	for _, badAES := range []string{"", "short", "weknora-system-aes-key-32bytes!!", "your-32-byte-long-encryption-key!"} {
		t.Run(badAES, func(t *testing.T) {
			isolateJWTSecret(t)
			t.Setenv("GIN_MODE", "release")
			t.Setenv("JWT_SECRET", "configured-production-signing-key-32bytes!")
			t.Setenv("SYSTEM_AES_KEY", badAES)
			require.Panics(t, func() { NewUserService(nil, nil, nil, nil, nil, nil) })
		})
	}
}

func TestJWTSecretValidatedWhenConstructingUserService(t *testing.T) {
	isolateJWTSecret(t)
	t.Setenv("APP_ENV", "production")
	require.Panics(t, func() { NewUserService(nil, nil, nil, nil, nil, nil) })
}

func TestJWTSecretDevelopmentKeyIsNonemptyAndStable(t *testing.T) {
	isolateJWTSecret(t)
	first := getJwtSecret()
	require.NotEmpty(t, first)
	require.Equal(t, first, getJwtSecret())
	// Process configuration is captured once; later environment changes
	// must not rotate the key and invalidate already issued tokens.
	t.Setenv("JWT_SECRET", "changed-after-initialization")
	require.Equal(t, first, getJwtSecret())
}

func TestJWTSecretProductionUsesConfiguredKey(t *testing.T) {
	isolateJWTSecret(t)
	t.Setenv("GIN_MODE", "release")
	t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
	t.Setenv("JWT_SECRET", " configured-production-signing-key-32b ")
	require.NotNil(t, NewUserService(nil, nil, nil, nil, nil, nil))
	require.Equal(t, "configured-production-signing-key-32b", getJwtSecret())
}
