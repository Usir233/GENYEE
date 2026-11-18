package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSandboxIDFromDataPlaneHost(t *testing.T) {
	// envd is addressed as "<port>-<sandboxID>.<domain>".
	require.Equal(t, "sb-123", sandboxIDFromDataPlaneHost("49983-sb-123.cube.app"))
	require.Equal(t, "i9x8", sandboxIDFromDataPlaneHost("80-i9x8.e2b.app"))
	require.Equal(t, "sb-1", sandboxIDFromDataPlaneHost("49983-sb-1.gateway.internal:8443"))
	// Control-plane and malformed authorities carry no sandbox.
	require.Empty(t, sandboxIDFromDataPlaneHost("api.e2b.app"))
	require.Empty(t, sandboxIDFromDataPlaneHost("notaport-sb-1.cube.app"))
	require.Empty(t, sandboxIDFromDataPlaneHost("49983-.cube.app"))
	require.Empty(t, sandboxIDFromDataPlaneHost(""))
}

func TestInboundTokenRegistryLifecycle(t *testing.T) {
	registry := NewInboundTokenRegistry()
	require.Empty(t, registry.Get("sb-1"))

	registry.Put("sb-1", "token-1")
	require.Equal(t, "token-1", registry.Get("sb-1"))

	// An empty token update explicitly clears stale credentials instead of
	// reusing the old token on a recycled or open-traffic sandbox ID.
	registry.Put("sb-1", "")
	require.Empty(t, registry.Get("sb-1"))

	registry.Put("sb-2", "token-2")
	registry.Delete("sb-2")
	require.Empty(t, registry.Get("sb-2"))
}

func TestInboundTokenRegistryCrossTenantIsolation(t *testing.T) {
	var seenHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = append(seenHeaders, r.Header.Get(InboundTokenHeader))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pool := NewSandboxGatewayTransportPoolWithPolicy(
		server.Client().Transport,
		OutboundURLPolicy{AllowPrivate: true},
	)

	tenantACfg := &Config{
		Type:                  SandboxTypeE2B,
		ConfigTenantID:        101,
		ConfigID:              "cfg-a",
		E2BProxyURL:           server.URL,
		E2BSandboxDomain:      "e2b.app",
		AllowPrivateEndpoints: true,
	}
	tenantBCfg := &Config{
		Type:                  SandboxTypeE2B,
		ConfigTenantID:        202,
		ConfigID:              "cfg-b",
		E2BProxyURL:           server.URL,
		E2BSandboxDomain:      "e2b.app",
		AllowPrivateEndpoints: true,
	}

	pool.ScopedInboundTokens(tenantACfg).Put("sb-shared", "victim-secret-token")

	// Tenant B cannot observe or overwrite Tenant A's token even with the same
	// gateway and sandbox ID.
	require.Empty(t, pool.ScopedInboundTokens(tenantBCfg).Get("sb-shared"))
	pool.ScopedInboundTokens(tenantBCfg).Put("sb-shared", "")
	pool.ScopedInboundTokens(tenantBCfg).Delete("sb-shared")
	require.Equal(t, "victim-secret-token", pool.ScopedInboundTokens(tenantACfg).Get("sb-shared"))

	reqB, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	reqB.Host = "49983-sb-shared.e2b.app"
	reqB.URL.Host = "49983-sb-shared.e2b.app"
	respB, err := pool.RoundTripperFor(tenantBCfg).RoundTrip(reqB)
	require.NoError(t, err)
	_ = respB.Body.Close()

	reqA, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	reqA.Host = "49983-sb-shared.e2b.app"
	reqA.URL.Host = "49983-sb-shared.e2b.app"
	respA, err := pool.RoundTripperFor(tenantACfg).RoundTrip(reqA)
	require.NoError(t, err)
	_ = respA.Body.Close()

	require.Equal(t, []string{"", "victim-secret-token"}, seenHeaders)
}

func TestInboundTokenRegistryTTLExpiry(t *testing.T) {
	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	registry := NewInboundTokenRegistryWithTTL(5 * time.Minute)
	registry.now = func() time.Time { return now }

	scope := InboundTokenScope{TenantID: 7, ConfigID: "cfg-1", GatewayOrigin: "https://gw.example.com:443"}
	registry.PutScoped(scope, "sb-ttl", "tok-ttl")
	require.Equal(t, "tok-ttl", registry.GetScoped(scope, "sb-ttl"))

	now = now.Add(6 * time.Minute)
	require.Empty(t, registry.GetScoped(scope, "sb-ttl"))
}

func TestCubeRemoteClientDeleteClearsInboundToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sandboxID":"sb-cube-1","domain":"cube.app"}`))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "sb-cube-1"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	pool := NewSandboxGatewayTransportPoolWithPolicy(nil, OutboundURLPolicy{AllowPrivate: true})
	cfg := &Config{
		Type:                  SandboxTypeCube,
		ConfigTenantID:        9,
		ConfigID:              "cfg-cube",
		CubeAPIURL:            srv.URL,
		CubeProxyURL:          srv.URL,
		CubeSandboxDomain:     "cube.app",
		CubeTemplate:          "tpl-1",
		AllowPrivateEndpoints: true,
	}
	client, err := NewCubeRemoteClientWithPool(cfg, pool)
	require.NoError(t, err)

	client.registerInboundToken("sb-cube-1", "cube-tok-1")
	require.Equal(t, "cube-tok-1", pool.ScopedInboundTokens(cfg).Get("sb-cube-1"))

	err = client.Delete(context.Background(), "sb-cube-1")
	require.NoError(t, err)
	require.Empty(t, pool.ScopedInboundTokens(cfg).Get("sb-cube-1"))
}

func TestWrapWithStdinRandomDelimiterPreventsNestedBreakout(t *testing.T) {
	adversarial := strings.Join([]string{
		"GENYEE_STDIN_GENYEE_STDIN_EOF_EOF",
		"GENYEE_STDIN_EOF",
		"GENYEE_STDIN_EOF_FALLBACK",
		"; echo PWNED #",
	}, "\n")

	wrapped1 := wrapWithStdin("python3 -c 'import sys; print(sys.stdin.read())'", adversarial)
	wrapped2 := wrapWithStdin("python3 -c 'import sys; print(sys.stdin.read())'", adversarial)
	require.NotEqual(t, wrapped1, wrapped2, "each call must draw a fresh random delimiter")

	firstNewline := strings.IndexByte(wrapped1, '\n')
	lastNewline := strings.LastIndexByte(wrapped1, '\n')
	require.Greater(t, firstNewline, 0)
	require.Greater(t, lastNewline, firstNewline)

	closingDelim := wrapped1[lastNewline+1:]
	body := wrapped1[firstNewline+1 : lastNewline]
	require.Equal(t, adversarial, body, "stdin payload must be preserved verbatim")
	require.NotContains(t, body, closingDelim, "closing heredoc delimiter must never appear inside stdin")
	require.True(t, strings.HasPrefix(wrapped1, "cat <<'"+closingDelim+"' | "))
}

func TestInboundTokenRegistryNormalizesSandboxIDCase(t *testing.T) {
	registry := NewInboundTokenRegistry()
	registry.Put("Sb-MiXeD", "token-1")

	require.Equal(t, "token-1", registry.Get("Sb-MiXeD"))
	sandboxID := sandboxIDFromDataPlaneHost("49983-SB-MIXED.e2b.app")
	require.Equal(t, "sb-mixed", sandboxID)
	require.Equal(t, "token-1", registry.Get(sandboxID))

	registry.Delete("SB-MIXED")
	require.Empty(t, registry.Get("sb-mixed"))
}

func TestGatewayTransportInjectsInboundToken(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("e2b-traffic-access-token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pool := NewSandboxGatewayTransportPoolWithPolicy(
		server.Client().Transport,
		OutboundURLPolicy{AllowPrivate: true},
	)
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BProxyURL = server.URL
	cfg.E2BSandboxDomain = "e2b.app"
	pool.ScopedInboundTokens(cfg).Put("sb-1", "token-1")
	client := &http.Client{Transport: pool.RoundTripperFor(cfg)}

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	req.Host = "49983-sb-1.e2b.app"
	req.URL.Host = "49983-sb-1.e2b.app"
	resp, err := client.Transport.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "token-1", gotToken,
		"go-e2b never sends this header, so the transport has to")
}

func TestGatewayTransportLeavesUnknownSandboxAlone(t *testing.T) {
	pool := NewSandboxGatewayTransportPool(nil)
	recorder := &roundTripRecorder{}
	transport := &gatewaySplitTransport{
		control:      recorder,
		inboundToken: pool.InboundTokens().ForScope(InboundTokenScope{}),
	}

	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-unknown.e2b.app/", nil)
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Empty(t, recorder.lastHeader)
	require.False(t, recorder.lastHeaderPresent)
}

func TestGatewayTransportInjectsInboundTokenOnControlPath(t *testing.T) {
	recorder := &roundTripRecorder{}
	pool := NewSandboxGatewayTransportPool(recorder)
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BSandboxDomain = "e2b.app"
	pool.ScopedInboundTokens(cfg).Put("sb-1", "token-1")
	transport := pool.RoundTripperFor(cfg)

	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-1.e2b.app/", nil)
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "token-1", recorder.lastHeader)
}

// Cube's SDK sets the header itself; the transport must not clobber it.
func TestGatewayTransportDoesNotOverwriteExistingToken(t *testing.T) {
	pool := NewSandboxGatewayTransportPool(nil)
	scoped := pool.InboundTokens().ForScope(InboundTokenScope{})
	scoped.Put("sb-1", "registry-token")
	recorder := &roundTripRecorder{}
	transport := &gatewaySplitTransport{
		control:      recorder,
		inboundToken: scoped,
	}

	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-1.cube.app/", nil)
	require.NoError(t, err)
	req.Header.Set("e2b-traffic-access-token", "sdk-token")
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "sdk-token", recorder.lastHeader)
}

func TestAttachInboundTokenTransportInjectsWithoutPool(t *testing.T) {
	recorder := &roundTripRecorder{}
	scoped := NewInboundTokenRegistry().ForScope(InboundTokenScope{})
	scoped.Put("sb-1", "token-1")

	transport := attachInboundTokenTransport(recorder, scoped)
	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-1.e2b.app/envd", nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "token-1", recorder.lastHeader,
		"NewE2BRemoteClient / WithTransport must still inject without a gateway pool")
}

func TestAttachInboundTokenTransportDoesNotDoubleWrapPoolSplit(t *testing.T) {
	pool := NewSandboxGatewayTransportPool(http.DefaultTransport)
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	split := pool.RoundTripperFor(cfg)

	require.Same(t, split, attachInboundTokenTransport(split, pool.ScopedInboundTokens(cfg)),
		"a pool split already injects from this scoped registry")

	otherTenantCfg := *cfg
	otherTenantCfg.ConfigTenantID = 42
	require.NotSame(t, split, attachInboundTokenTransport(split, pool.ScopedInboundTokens(&otherTenantCfg)),
		"different tenant scope must never reuse another tenant's split transport")
}

// roundTripRecorder stands in for the control transport and remembers what
// header the request carried by the time it got there.
type roundTripRecorder struct {
	lastHeader        string
	lastHeaderPresent bool
}

func (r *roundTripRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.lastHeader = req.Header.Get(InboundTokenHeader)
	_, r.lastHeaderPresent = req.Header[http.CanonicalHeaderKey(InboundTokenHeader)]
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    req,
	}, nil
}
