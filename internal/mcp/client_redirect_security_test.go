package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestMCPClientDoesNotFollowCrossOriginRedirectWithCredentials(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, transportType := range []types.MCPTransportType{types.MCPTransportSSE, types.MCPTransportHTTPStreamable} {
		t.Run(string(transportType), func(t *testing.T) {
			var targetRequests, originRequests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetRequests.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(target.Close)
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originRequests.Add(1)
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			t.Cleanup(origin.Close)
			client, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
				ID: "redirect-test", URL: &origin.URL, Enabled: true, TransportType: transportType,
				Headers: map[string]string{"Authorization": "Bearer fixture-token", "X-API-Key": "fixture-key"},
			}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Disconnect() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = client.Connect(ctx)
			if err == nil {
				_, err = client.Initialize(ctx)
			}
			require.Error(t, err, "the redirect must fail even when both origins pass SSRF validation")
			require.Positive(t, originRequests.Load(), "exercise the real SDK HTTP transport")
			require.Zero(t, targetRequests.Load(), "credentials and requests must never reach another origin")
		})
	}
}
