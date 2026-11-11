package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestFrontendRedirectIsApplicationRelative(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "")
	t.Setenv("FRONTEND_BASE_URL", "")
	for _, raw := range []string{
		"https://evil.example/", "//evil.example/", `/\evil.example/`, "/%2fexample.com",
		"/%2f%2fevil.com", "/%2F%2Fevil.com", "/%5cexample.com", "/%5c%5cevil.com",
		"javascript:alert(1)", " /example", "/\nexample",
	} {
		_, err := validateFrontendRedirect(raw)
		require.Error(t, err, raw)
	}
	for _, raw := range []string{"/", "/embed/channel?theme=dark", "/prefix/settings"} {
		result, err := validateFrontendRedirect(raw)
		require.NoError(t, err)
		require.Equal(t, raw, result)
	}
}

func TestFrontendRedirectAllowsOnlyConfiguredOrigin(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "https://app.example/prefix")
	t.Setenv("FRONTEND_BASE_URL", "")
	_, err := validateFrontendRedirect("https://app.example/prefix/settings")
	require.NoError(t, err)
	for _, raw := range []string{
		"https://app.example.evil.example/", "http://app.example/", "https://app.example:444/",
	} {
		_, err = validateFrontendRedirect(raw)
		require.Error(t, err)
	}
}

func TestResolveOAuthCallbackURIRejectsUntrustedOriginsAndEnforcesFixedPath(t *testing.T) {
	t.Run("configured APP_EXTERNAL_URL forces fixed callback path and blocks attacker origin", func(t *testing.T) {
		t.Setenv("APP_EXTERNAL_URL", "https://app.example/subpath")
		t.Setenv("FRONTEND_BASE_URL", "")

		cb, err := ResolveOAuthCallbackURI("", true)
		require.NoError(t, err)
		require.Equal(t, "https://app.example/subpath/api/v1/mcp-oauth/callback", cb)

		cb, err = ResolveOAuthCallbackURI("https://app.example/custom/ignored", true)
		require.NoError(t, err)
		require.Equal(t, "https://app.example/subpath/api/v1/mcp-oauth/callback", cb)

		for _, bad := range []string{
			"https://evil.example/api/v1/mcp-oauth/callback",
			"http://app.example/api/v1/mcp-oauth/callback",
			"javascript:alert(1)",
			"https://user:pass@app.example/api/v1/mcp-oauth/callback",
		} {
			_, err := ResolveOAuthCallbackURI(bad, true)
			require.ErrorIs(t, err, ErrInvalidOAuthRedirectURI, bad)
		}
	})

	t.Run("FRONTEND_BASE_URL fallback", func(t *testing.T) {
		t.Setenv("APP_EXTERNAL_URL", "")
		t.Setenv("FRONTEND_BASE_URL", "https://portal.example")

		cb, err := ResolveOAuthCallbackURI("", true)
		require.NoError(t, err)
		require.Equal(t, "https://portal.example/api/v1/mcp-oauth/callback", cb)

		_, err = ResolveOAuthCallbackURI("https://attacker.example/cb", true)
		require.ErrorIs(t, err, ErrInvalidOAuthRedirectURI)
	})

	t.Run("unconfigured base URL rejects external domains and release mode", func(t *testing.T) {
		t.Setenv("APP_EXTERNAL_URL", "")
		t.Setenv("FRONTEND_BASE_URL", "")
		t.Setenv("GIN_MODE", "debug")

		_, err := ResolveOAuthCallbackURI("https://attacker.example/cb", true)
		require.ErrorIs(t, err, ErrInvalidOAuthRedirectURI)

		cb, err := ResolveOAuthCallbackURI("http://localhost:5173/anything", true)
		require.NoError(t, err)
		require.Equal(t, "http://localhost:5173/api/v1/mcp-oauth/callback", cb)

		t.Setenv("GIN_MODE", "release")
		_, err = ResolveOAuthCallbackURI("http://localhost:5173/api/v1/mcp-oauth/callback", true)
		require.ErrorIs(t, err, ErrInvalidOAuthRedirectURI)
	})
}

func TestStartAuthorizationBlocksRedirectPoisoningAndUnsafeMetadataEndpoints(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("APP_EXTERNAL_URL", "https://genyee.example.com")

	var authEndpoint, tokenEndpoint string
	var regCount int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": authEndpoint,
				"token_endpoint":         tokenEndpoint,
				"registration_endpoint":  srv.URL + "/register",
			})
		case "/register":
			regCount++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":     "cid-1",
				"client_secret": "csec-1",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	serviceURL := srv.URL + "/mcp"
	service := &types.MCPService{
		ID:            "svc-sec",
		TenantID:      7,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &serviceURL,
		AuthConfig: &types.MCPAuthConfig{
			AuthType:              types.MCPAuthOAuth,
			AuthServerMetadataURL: srv.URL + "/.well-known/oauth-authorization-server",
		},
	}
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "42"}

	// 1. Poisoned redirect_uri must be rejected before dynamic client registration.
	authEndpoint = srv.URL + "/authorize"
	tokenEndpoint = srv.URL + "/token"
	repo := newFakeOAuthRepo()
	mgr := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)
	_, _, err := mgr.StartAuthorization(context.Background(), service, 7, principal, "https://evil.attacker.com/cb", "/")
	require.ErrorIs(t, err, ErrInvalidOAuthRedirectURI)
	require.Zero(t, regCount)
	stored, _ := repo.GetClient(context.Background(), 7, service.ID)
	require.Nil(t, stored)

	// 2. Dangerous scheme or non-whitelisted private IP in authorization_endpoint / token_endpoint must fail.
	for _, tc := range []struct {
		name string
		auth string
		tok  string
	}{
		{"javascript auth endpoint", "javascript:alert(1)", srv.URL + "/token"},
		{"metadata SSRF auth endpoint", "http://169.254.169.254/latest/meta-data/", srv.URL + "/token"},
		{"private IP token endpoint", srv.URL + "/authorize", "http://10.0.0.1/token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authEndpoint = tc.auth
			tokenEndpoint = tc.tok
			r := newFakeOAuthRepo()
			m := NewOAuthManager(r, fakeMCPServiceRepo{service: service}, nil)
			_, _, err := m.StartAuthorization(context.Background(), service, 7, principal, "", "/")
			require.Error(t, err)
		})
	}

	// 3. Valid call with omitted redirect_uri registers fixed server callback path.
	authEndpoint = srv.URL + "/authorize"
	tokenEndpoint = srv.URL + "/token"
	authURL, _, err := mgr.StartAuthorization(context.Background(), service, 7, principal, "", "/")
	require.NoError(t, err)
	parsedAuth, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, "https://genyee.example.com/api/v1/mcp-oauth/callback", parsedAuth.Query().Get("redirect_uri"))
	stored, err = repo.GetClient(context.Background(), 7, service.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, "https://genyee.example.com/api/v1/mcp-oauth/callback", stored.RedirectURI)
}
