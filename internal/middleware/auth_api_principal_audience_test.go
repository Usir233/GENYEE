package middleware

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/golang-jwt/jwt/v5"
)

// TestAudienceValidation_AdversarialChallenge verifies that resolveAPIPrincipal
// strictly enforces the accepted audience claim ("genyee") and rejects
// any invalid or forged audience claim (e.g., "fake", "", "unauthorized").
func TestAudienceValidation_AdversarialChallenge(t *testing.T) {
	secret := "test-secret-audience-challenger"
	tenant := &types.Tenant{
		ID: 42,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: secret,
		},
	}

	testCases := []struct {
		name          string
		audience      string
		shouldSucceed bool
	}{
		{
			name:          "Accepted audience: genyee",
			audience:      "genyee",
			shouldSucceed: true,
		},
		{
			name:          "Rejected audience: another product",
			audience:      "another-product",
			shouldSucceed: false,
		},
		{
			name:          "Rejected audience: fake",
			audience:      "fake",
			shouldSucceed: false,
		},
		{
			name:          "Rejected audience: empty string",
			audience:      "",
			shouldSucceed: false,
		},
		{
			name:          "Rejected audience: malicious attacker",
			audience:      "attacker-controlled-aud",
			shouldSucceed: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			claims := jwt.MapClaims{
				"sub":       "adversarial-user-1",
				"tenant_id": float64(42),
				"exp":       time.Now().Add(5 * time.Minute).Unix(),
			}
			if tc.audience != "" {
				claims["aud"] = tc.audience
			}

			token := signedExternalUserToken(t, secret, claims)
			header.Set("X-External-User-Token", token)

			principal, src, err := resolveAPIPrincipal(context.Background(), tenant, header)

			if tc.shouldSucceed {
				if err != nil {
					t.Fatalf("expected audience %q to be accepted, got error: %v", tc.audience, err)
				}
				if principal.Type != types.PrincipalAPIExternalUser || principal.ID != "42:adversarial-user-1" {
					t.Fatalf("unexpected principal: %#v", principal)
				}
				if src != types.AuthSourceAPISignedToken {
					t.Fatalf("unexpected auth source: %q", src)
				}
			} else {
				if err == nil {
					t.Fatalf("expected audience %q to be rejected, but it was accepted with principal %#v", tc.audience, principal)
				}
				t.Logf("audience %q successfully rejected with error: %v", tc.audience, err)
			}
		})
	}
}
