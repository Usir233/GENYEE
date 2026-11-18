package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestAuditSSEEndpointOriginAndUserinfoPinning(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	t.Run("rejects endpoint event with embedded userinfo", func(t *testing.T) {
		var postHits atomic.Int32
		var srvURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/sse" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				rc := http.NewResponseController(w)
				withUser := strings.Replace(srvURL, "http://", "http://attacker:leak@", 1) + "/messages"
				_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", withUser)
				_ = rc.Flush()
				<-r.Context().Done()
				return
			}
			postHits.Add(1)
			w.WriteHeader(http.StatusAccepted)
		}))
		srvURL = srv.URL
		t.Cleanup(srv.Close)

		sseURL := srv.URL + "/sse"
		c, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:            "sse-userinfo",
				Name:          "sse-userinfo",
				Enabled:       true,
				TransportType: types.MCPTransportSSE,
				URL:           &sseURL,
				AuthConfig: &types.MCPAuthConfig{
					AuthType: types.MCPAuthAPIKey,
					APIKey:   "secret-api-key",
				},
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Disconnect() })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = c.Connect(ctx)
		if err == nil {
			_, err = c.Initialize(ctx)
		}
		require.Error(t, err, "SSE endpoint containing userinfo must be rejected")
		require.Zero(t, postHits.Load(), "credentials must never be posted to a userinfo-bearing endpoint")
	})

	t.Run("rejects endpoint event with changed scheme on same host", func(t *testing.T) {
		var srvURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/sse" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				rc := http.NewResponseController(w)
				wrongScheme := strings.Replace(srvURL, "http://", "https://", 1) + "/messages"
				_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", wrongScheme)
				_ = rc.Flush()
				<-r.Context().Done()
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		srvURL = srv.URL
		t.Cleanup(srv.Close)

		sseURL := srv.URL + "/sse"
		c, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:            "sse-scheme-mismatch",
				Name:          "sse-scheme-mismatch",
				Enabled:       true,
				TransportType: types.MCPTransportSSE,
				URL:           &sseURL,
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Disconnect() })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = c.Connect(ctx)
		require.Error(t, err, "Connect itself must reject an SSE endpoint whose scheme differs from baseURL")
	})

	t.Run("blocks mid-stream endpoint update to userinfo URL before sending credentials", func(t *testing.T) {
		var leakedHits atomic.Int32
		secondEndpointSent := make(chan struct{})
		var srvURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/sse" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				rc := http.NewResponseController(w)
				_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", srvURL)
				_ = rc.Flush()
				time.Sleep(30 * time.Millisecond)
				poisoned := strings.Replace(srvURL, "http://", "http://attacker:leak@", 1) + "/leaked"
				_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", poisoned)
				_ = rc.Flush()
				close(secondEndpointSent)
				<-r.Context().Done()
				return
			}
			if r.URL.Path == "/leaked" {
				leakedHits.Add(1)
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		srvURL = srv.URL
		t.Cleanup(srv.Close)

		sseURL := srv.URL + "/sse"
		c, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:            "sse-midstream",
				Name:          "sse-midstream",
				Enabled:       true,
				TransportType: types.MCPTransportSSE,
				URL:           &sseURL,
				AuthConfig: &types.MCPAuthConfig{
					AuthType: types.MCPAuthBearer,
					Token:    "top-secret-bearer",
				},
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Disconnect() })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, c.Connect(ctx))
		<-secondEndpointSent
		time.Sleep(30 * time.Millisecond)
		_, err = c.Initialize(ctx)
		require.Error(t, err)
		require.Zero(t, leakedHits.Load(), "mid-stream poisoned endpoint must never receive authenticated POSTs")
	})
}

func TestAuditManagerIsolatesBuiltinOAuthConnectionsAcrossTenants(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	var mu sync.Mutex
	var seenAuth []string

	mcpSrv := sdkserver.NewMCPServer("builtin-oauth-test", "1.0.0")
	mcpSrv.AddTool(
		sdkmcp.NewTool("echo"),
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultText("ok"), nil
		},
	)
	streamSrv := sdkserver.NewStreamableHTTPServer(mcpSrv, sdkserver.WithStateLess(true))
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var rpc struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &rpc) == nil && rpc.Method == string(sdkmcp.MethodToolsCall) {
			mu.Lock()
			seenAuth = append(seenAuth, r.Header.Get("Authorization"))
			mu.Unlock()
		}
		streamSrv.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)

	repo := newFakeOAuthRepo()
	alice := webUser("alice")
	builtinSvc := &types.MCPService{
		ID:            "builtin-oauth-svc",
		Name:          "Builtin OAuth",
		Enabled:       true,
		IsBuiltin:     true,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &httpSrv.URL,
		AuthConfig:    &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth},
	}

	for _, tc := range []struct {
		tenantID uint64
		token    string
	}{
		{tenantID: 7, token: "token-tenant-7"},
		{tenantID: 8, token: "token-tenant-8"},
	} {
		require.NoError(t, repo.SaveTokenForPrincipal(context.Background(), &types.MCPOAuthToken{
			TenantID:      tc.tenantID,
			PrincipalType: alice.Type,
			PrincipalID:   alice.ID,
			ServiceID:     builtinSvc.ID,
			AccessToken:   tc.token,
			TokenType:     "Bearer",
			ExpiresAt:     time.Now().Add(time.Hour),
		}))
	}

	mgr := NewMCPManager(repo)
	t.Cleanup(mgr.Shutdown)

	ctx7 := types.WithPrincipal(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)), alice)
	ctx8 := types.WithPrincipal(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(8)), alice)

	conn7, err := mgr.GetOrCreateClient(ctx7, builtinSvc)
	require.NoError(t, err)
	conn8, err := mgr.GetOrCreateClient(ctx8, builtinSvc)
	require.NoError(t, err)
	require.NotSame(t, conn7, conn8, "same user in different tenants must never share an OAuth MCP client")

	_, err = conn7.CallTool(ctx7, "echo", nil)
	require.NoError(t, err)
	_, err = conn8.CallTool(ctx8, "echo", nil)
	require.NoError(t, err)

	mu.Lock()
	gotAuth := append([]string(nil), seenAuth...)
	mu.Unlock()
	require.Equal(t, []string{"Bearer token-tenant-7", "Bearer token-tenant-8"}, gotAuth)

	// Re-authorizing in tenant 7 must recycle tenant 7's connection without disconnecting tenant 8's.
	require.NoError(t, mgr.CloseClientsAfterAuthorization(context.Background(), 7, builtinSvc.ID, alice))
	require.False(t, conn7.IsConnected(), "tenant 7 connection must be recycled")
	require.True(t, conn8.IsConnected(), "tenant 8 connection must remain connected")

	// Non-builtin tenant-owned service must reject cross-tenant callers.
	tenant7Owned := &types.MCPService{
		ID:            "tenant-7-only",
		TenantID:      7,
		Name:          "Tenant 7 Private MCP",
		Enabled:       true,
		IsBuiltin:     false,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &httpSrv.URL,
	}
	_, err = mgr.GetOrCreateClient(ctx8, tenant7Owned)
	require.Error(t, err, "non-builtin service owned by tenant 7 must reject tenant 8 context")
}

func TestAuditBoundedHTTPAndSSEResponsesAndOAuthRefreshMetadata(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	t.Run("rejects oversized HTTP JSON response body", func(t *testing.T) {
		prev := maxMCPResponseBytes
		maxMCPResponseBytes = 4096
		t.Cleanup(func() { maxMCPResponseBytes = prev })

		bigDescription := strings.Repeat("A", 8192)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(
				w,
				`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{},`+
					`"serverInfo":{"name":"big","version":"1.0","description":%q}}}`,
				bigDescription,
			)
		}))
		t.Cleanup(srv.Close)

		c, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:            "oversized-http",
				Name:          "oversized-http",
				Enabled:       true,
				TransportType: types.MCPTransportHTTPStreamable,
				URL:           &srv.URL,
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Disconnect() })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, c.Connect(ctx))
		_, err = c.Initialize(ctx)
		require.Error(t, err, "oversized HTTP JSON response must be rejected")
	})

	t.Run("rejects oversized single SSE event while allowing multiple bounded events", func(t *testing.T) {
		prev := maxMCPSSEEventBytes
		maxMCPSSEEventBytes = 4096
		t.Cleanup(func() { maxMCPSSEEventBytes = prev })

		// 1. Oversized single SSE line/event without blank line must fail Connect promptly.
		oversizedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			rc := http.NewResponseController(w)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("X", 8192))
			_ = rc.Flush()
		}))
		t.Cleanup(oversizedSrv.Close)

		cBad, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:             "oversized-sse",
				Name:           "oversized-sse",
				Enabled:        true,
				TransportType:  types.MCPTransportSSE,
				URL:            &oversizedSrv.URL,
				AdvancedConfig: &types.MCPAdvancedConfig{Timeout: 2},
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = cBad.Disconnect() })

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		start := time.Now()
		err = cBad.Connect(ctx)
		require.ErrorIs(
			t, err, ErrMCPResponseTooLarge, "oversized SSE event must fail Connect with ErrMCPResponseTooLarge",
		)
		require.Less(
			t, time.Since(start), time.Second,
			"oversized SSE event must abort immediately without waiting for timeout",
		)

		// 2. Multiple bounded SSE events whose cumulative bytes exceed maxMCPSSEEventBytes must succeed.
		mcpSrv := sdkserver.NewMCPServer("sse-multi", "1.0.0")
		pad := strings.Repeat("y", 1200)
		mcpSrv.AddTool(
			sdkmcp.NewTool("pad"),
			func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return sdkmcp.NewToolResultText(pad), nil
			},
		)
		sseSrv := sdkserver.NewSSEServer(mcpSrv)
		httpSSE := httptest.NewServer(sseSrv)
		t.Cleanup(httpSSE.Close)

		sseEndpointURL := httpSSE.URL + "/sse"
		cGood, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:            "multi-event-sse",
				Name:          "multi-event-sse",
				Enabled:       true,
				TransportType: types.MCPTransportSSE,
				URL:           &sseEndpointURL,
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = cGood.Disconnect() })

		require.NoError(t, cGood.Connect(ctx))
		_, err = cGood.Initialize(ctx)
		require.NoError(t, err)
		for range 5 {
			res, callErr := cGood.CallTool(ctx, "pad", nil)
			require.NoError(t, callErr, "sequential bounded SSE events exceeding single-event cap in sum must succeed")
			require.Len(t, res.Content, 1)
		}
	})

	t.Run("validates OAuth metadata token_endpoint before refresh in NewMCPClient", func(t *testing.T) {
		var tokenHits atomic.Int32
		var authSrvURL string
		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/metadata":
				poisonedToken := strings.Replace(authSrvURL, "http://", "http://user:pass@", 1) + "/token"
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                                authSrvURL,
					"authorization_endpoint":                authSrvURL + "/authorize",
					"token_endpoint":                        poisonedToken,
					"response_types_supported":              []string{"code"},
					"token_endpoint_auth_methods_supported": []string{"none"},
				})
			case "/token":
				tokenHits.Add(1)
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "should-never-issue",
					"token_type":   "Bearer",
					"expires_in":   3600,
				})
			default:
				http.NotFound(w, r)
			}
		}))
		authSrvURL = authSrv.URL
		t.Cleanup(authSrv.Close)

		mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(mcpHTTP.Close)

		repo := newLockedOAuthRepo()
		alice := webUser("alice")
		require.NoError(t, repo.SaveTokenForPrincipal(context.Background(), &types.MCPOAuthToken{
			TenantID:      7,
			PrincipalType: alice.Type,
			PrincipalID:   alice.ID,
			ServiceID:     "oauth-refresh-meta",
			AccessToken:   "expired-access",
			RefreshToken:  " precious-refresh-token ",
			TokenType:     "Bearer",
			ExpiresAt:     time.Now().Add(-time.Minute),
		}))

		c, err := NewMCPClient(&ClientConfig{
			Service: &types.MCPService{
				ID:            "oauth-refresh-meta",
				Name:          "oauth-refresh-meta",
				Enabled:       true,
				TransportType: types.MCPTransportHTTPStreamable,
				URL:           &mcpHTTP.URL,
				AuthConfig: &types.MCPAuthConfig{
					AuthType:              types.MCPAuthOAuth,
					ClientID:              "pre-reg-client",
					AuthServerMetadataURL: authSrv.URL + "/metadata",
				},
			},
			TenantID:  7,
			Principal: alice,
			OAuthRepo: repo,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Disconnect() })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = c.Connect(ctx)
		if err == nil {
			_, err = c.Initialize(ctx)
		}
		require.Error(t, err)
		require.Zero(t, tokenHits.Load(), "refresh token must never be posted to an unsafe token_endpoint")
	})
}
