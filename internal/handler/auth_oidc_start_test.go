package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// stubOIDCStartUserService only implements GetOIDCAuthorizationURL; every
// other UserService call panics via the nil interface embedding. Keeps the
// test focused on the OIDCStart handler's branching logic.
type stubOIDCStartUserService struct {
	interfaces.UserService
	getOIDCAuthorizationURL func(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error)
}

func (s *stubOIDCStartUserService) GetOIDCAuthorizationURL(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error) {
	return s.getOIDCAuthorizationURL(ctx, redirectURI)
}

func newOIDCStartTestRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.GET("/auth/oidc/start", h.OIDCStart)
	return r
}

// TestOIDCStart_RedirectsToAuthProvider: on success the handler must 302
// to the IdP authorization URL and bind the nonce via cookie (CSRF/replay
// defence), exactly like /auth/oidc/url.
func TestOIDCStart_RedirectsToAuthProvider(t *testing.T) {
	const authURL = "http://idp.example.com/authorize?client_id=genyee"
	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(context.Context, string) (*types.OIDCAuthURLResponse, error) {
			return &types.OIDCAuthURLResponse{
				Success:          true,
				AuthorizationURL: authURL,
				State:            "state123",
				Nonce:            "nonce456",
			}, nil
		},
	}
	h := NewAuthHandler(&config.Config{FrontendBaseURL: "https://genyee.example.com"}, us, nil, nil, nil)
	r := newOIDCStartTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	req.Host = "genyee.example.com"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != authURL {
		t.Errorf("Location = %q, want %q", loc, authURL)
	}
	var hasNonce bool
	for _, ck := range w.Result().Cookies() {
		if ck.Name == oidcNonceCookieName && ck.Value == "nonce456" {
			hasNonce = true
		}
	}
	if !hasNonce {
		t.Errorf("expected %s cookie to be set with the nonce", oidcNonceCookieName)
	}
}

// TestOIDCStart_RejectsUntrustedHostWithoutFrontendBaseURL: when FRONTEND_BASE_URL
// is unset, non-loopback Host headers must be rejected to prevent Host-header
// redirect_uri poisoning, while loopback dev hosts remain usable outside release mode.
func TestOIDCStart_RejectsUntrustedHostWithoutFrontendBaseURL(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "")
	t.Setenv("GIN_MODE", "test")
	var captured string
	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(_ context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error) {
			captured = redirectURI
			return &types.OIDCAuthURLResponse{Success: true, AuthorizationURL: "http://idp", Nonce: "n"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)
	r := newOIDCStartTestRouter(h)

	// Spoofed external Host must be rejected before calling UserService.
	reqSpoof := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	reqSpoof.Host = "attacker.example.com"
	reqSpoof.Header.Set("X-Forwarded-Proto", "https")
	wSpoof := httptest.NewRecorder()
	r.ServeHTTP(wSpoof, reqSpoof)
	if wSpoof.Code == http.StatusFound || captured != "" {
		t.Fatalf("untrusted Host should be rejected, got status=%d captured=%q", wSpoof.Code, captured)
	}

	// Loopback host in non-release mode is allowed.
	reqLocal := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	reqLocal.Host = "127.0.0.1:8080"
	wLocal := httptest.NewRecorder()
	r.ServeHTTP(wLocal, reqLocal)
	const want = "http://127.0.0.1:8080/api/v1/auth/oidc/callback"
	if captured != want {
		t.Errorf("callback URL passed to IdP = %q, want %q", captured, want)
	}
	if wLocal.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", wLocal.Code)
	}
}

// TestOIDCStart_ServiceErrorReturnsNonRedirect: if the IdP URL cannot be
// built, never 302 — surface the error the same way /auth/oidc/url does.
func TestOIDCStart_ServiceErrorReturnsNonRedirect(t *testing.T) {
	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(context.Context, string) (*types.OIDCAuthURLResponse, error) {
			return nil, fmt.Errorf("idp unavailable")
		},
	}
	h := NewAuthHandler(&config.Config{FrontendBaseURL: "https://app.example.com"}, us, nil, nil, nil)
	r := newOIDCStartTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusFound {
		t.Fatalf("status = 302, want non-redirect on service error")
	}
}

func TestOIDCStart_BuildsCallbackURLWithSubpath(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "")

	var captured string

	us := &stubOIDCStartUserService{
		getOIDCAuthorizationURL: func(
			_ context.Context,
			redirectURI string,
		) (*types.OIDCAuthURLResponse, error) {
			captured = redirectURI

			return &types.OIDCAuthURLResponse{
				Success:          true,
				AuthorizationURL: "https://idp.example.com/authorize",
				Nonce:            "nonce123",
			}, nil
		},
	}

	h := NewAuthHandler(
		&config.Config{
			FrontendBaseURL: "https://example.com/genyee",
		},
		us, nil, nil, nil,
	)

	r := newOIDCStartTestRouter(h)

	req := httptest.NewRequest(
		http.MethodGet,
		"/auth/oidc/start",
		nil,
	)
	req.Host = "internal.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	const want = "https://example.com/genyee/api/v1/auth/oidc/callback"

	if captured != want {
		t.Errorf("callback URL = %q, want %q", captured, want)
	}

	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want 302", w.Code)
	}
}
