package browserskill

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// inProcessConnectedManager builds a Manager with a connected device backed by
// an in-process Unix socket RPC stub so security tests never skip when the
// optional external bsk binary is absent.
func inProcessConnectedManager(t *testing.T, scope Scope) (*Manager, chan map[string]any) {
	t.Helper()
	base, err := os.MkdirTemp("", "bsk-sec-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	innerDir := filepath.Join(base, "b", "run")
	require.NoError(t, os.MkdirAll(innerDir, 0o700))
	innerSock := filepath.Join(innerDir, "daemon.sock")
	innerLn, err := net.Listen("unix", innerSock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = innerLn.Close() })

	upstreamCalls := make(chan map[string]any, 32)
	go func() {
		for {
			conn, acceptErr := innerLn.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				var req map[string]any
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				upstreamCalls <- req
				id, _ := req["id"].(string)
				method, _ := req["method"].(string)
				res := json.RawMessage(`{"ok":true}`)
				if method == "session.start" {
					res = json.RawMessage(`{"session_id":"native-session-1"}`)
				}
				_ = json.NewEncoder(c).Encode(rpcReply{ID: id, Result: res})
			}(conn)
		}
	}()

	secret, err := generateDaemonSecret()
	require.NoError(t, err)
	outerSock := filepath.Join(base, "run", "daemon.sock")
	gk, err := startDaemonSocketGatekeeper(outerSock, innerSock, secret)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gk.Close() })

	store := testStore(t)
	m := NewManager(store)
	m.binary = "bsk"
	m.publicURL = "wss://example.com/api/v1/local-browser/extension"

	ctx := context.Background()
	link, err := m.Pair(ctx, scope, "")
	require.NoError(t, err)
	pairToken := strings.Split(link, "#")[1]
	nextToken := randomToken()
	now := time.Now()
	_, err = store.exchange(ctx, tokenHash(pairToken), DeviceRecord{
		ID:         randomID(),
		Label:      "Chrome",
		TokenHash:  tokenHash(nextToken),
		ExpiresAt:  now.Add(deviceLifetime),
		RenewAfter: now.Add(renewalInterval),
		CreatedAt:  now,
		LastSeenAt: now,
	})
	require.NoError(t, err)
	rec, err := store.authenticate(ctx, tokenHash(nextToken))
	require.NoError(t, err)
	require.NoError(t, store.claim(ctx, rec, m.nodeID, m.internalURL, randomID()))

	rt := &daemon{home: base, secret: secret, gatekeeper: gk}
	m.mu.Lock()
	m.daemon = rt
	m.mu.Unlock()
	d, err := m.ensureDevice(ctx, scope)
	require.NoError(t, err)
	d.mu.Lock()
	d.conn = &websocket.Conn{}
	d.ready = true
	d.expires = now.Add(time.Hour)
	d.runtime = rt
	d.mu.Unlock()
	return m, upstreamCalls
}

func TestCallRejectsDangerousNavigationAndTabCreateSchemes(t *testing.T) {
	ctx := context.Background()
	scope := Scope{1, "alice"}
	m, upstreamCalls := inProcessConnectedManager(t, scope)

	require.NoError(t, m.Control(ctx, scope, "chat", "select"))

	badURLs := []any{
		"file:///etc/passwd",
		"FILE:///Users/alice/.ssh/id_rsa",
		"javascript:alert(1)",
		"JavaScript:fetch('https://evil.example')",
		"chrome://settings",
		"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/options.html",
		"data:text/html,<script>alert(1)</script>",
		"devtools://devtools/bundled/inspector.html",
		"view-source:https://example.com",
		"about:blank", // navigate requires http(s); about:blank is only for tab_create placeholder
		"/relative/path",
		"https://",
		"https://example.com/\x00bad",
		"",
		123,
	}
	for _, bad := range badURLs {
		_, err := m.Call(ctx, scope, "chat", "navigate", map[string]any{"url": bad})
		require.Error(t, err, "navigate must reject %v", bad)
	}

	for _, bad := range []any{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"chrome://extensions",
		"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/options.html",
		"data:text/html,hello",
		"devtools://devtools/bundled/inspector.html",
		"view-source:https://example.com",
		"/relative/path",
		"http://",
		123,
	} {
		_, err := m.Call(ctx, scope, "chat", "tab_create", map[string]any{"url": bad})
		require.Error(t, err, "tab_create must reject %v", bad)
	}

	// Rejected URLs must neither auto-start a native session nor dispatch RPCs.
	require.Empty(t, m.Status(scope, "chat").SessionID)
	require.Empty(t, upstreamCalls)

	// Legitimate http(s) navigation, blank placeholder tab_create, and omitted url still work.
	_, err := m.Call(ctx, scope, "chat", "navigate", map[string]any{"url": "https://example.com/path"})
	require.NoError(t, err)
	require.Equal(t, "session.start", (<-upstreamCalls)["method"])
	navReq := <-upstreamCalls
	require.Equal(t, "tool.navigate", navReq["method"])
	require.Equal(t, "https://example.com/path", navReq["params"].(map[string]any)["url"])

	_, err = m.Call(ctx, scope, "chat", "tab_create", map[string]any{"url": "about:blank", "active": false})
	require.NoError(t, err)
	tabReq := <-upstreamCalls
	require.Equal(t, "tool.tab_create", tabReq["method"])
	require.Equal(t, "about:blank", tabReq["params"].(map[string]any)["url"])

	_, err = m.Call(ctx, scope, "chat", "tab_create", map[string]any{})
	require.NoError(t, err)
	emptyTabReq := <-upstreamCalls
	require.Equal(t, "tool.tab_create", emptyTabReq["method"])
	_, hasURL := emptyTabReq["params"].(map[string]any)["url"]
	require.False(t, hasURL)
}

func TestAuthorizeHTTPRequiresBearerSchemeAndServeHTTPValidatesCanonicalToken(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	m := NewManager(store)
	m.binary = "bsk"
	m.publicURL = "wss://example.com/api/v1/local-browser/extension"

	scope := Scope{1, "alice"}
	link, err := m.Pair(ctx, scope, "wss://example.com/api/v1/local-browser/extension")
	require.NoError(t, err)
	pairToken := strings.Split(link, "#")[1]
	nextToken := randomToken()

	body, err := json.Marshal(map[string]any{
		"action":     "pair",
		"next_token": nextToken,
		"label":      "Chrome",
	})
	require.NoError(t, err)

	// Raw token without "Bearer " prefix must be rejected without consuming the pairing token.
	for _, badHeader := range []string{pairToken, "Basic " + pairToken, "bearer " + pairToken} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/authorize", bytes.NewReader(body))
		req.Header.Set("Origin", "chrome-extension://"+strings.Repeat("a", 32))
		req.Header.Set("Authorization", badHeader)
		rec := httptest.NewRecorder()
		m.AuthorizeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "header %q must be rejected", badHeader)
	}

	// Reusing the same token as next_token must be rejected.
	sameBody, _ := json.Marshal(map[string]any{"action": "pair", "next_token": pairToken})
	sameReq := httptest.NewRequest(http.MethodPost, "http://localhost/authorize", bytes.NewReader(sameBody))
	sameReq.Header.Set("Origin", "chrome-extension://"+strings.Repeat("a", 32))
	sameReq.Header.Set("Authorization", "Bearer "+pairToken)
	sameRec := httptest.NewRecorder()
	m.AuthorizeHTTP(sameRec, sameReq)
	require.Equal(t, http.StatusBadRequest, sameRec.Code)

	// Valid Bearer prefix still succeeds because the pairing token was not consumed above.
	okReq := httptest.NewRequest(http.MethodPost, "http://localhost/authorize", bytes.NewReader(body))
	okReq.Header.Set("Origin", "chrome-extension://"+strings.Repeat("a", 32))
	okReq.Header.Set("Authorization", "Bearer "+pairToken)
	okRec := httptest.NewRecorder()
	m.AuthorizeHTTP(okRec, okReq)
	require.Equal(t, http.StatusOK, okRec.Code)

	// ServeHTTP rejects a 43-char non-canonical base64url subprotocol before hitting storage.
	nonCanonical := strings.Repeat("!", 43)
	noStoreMgr := &Manager{binary: "bsk", publicURL: "wss://example.com"}
	wsReq := httptest.NewRequest(http.MethodGet, "http://localhost/extension", nil)
	wsReq.Header.Set("Origin", "chrome-extension://"+strings.Repeat("a", 32))
	wsReq.Header.Set("Sec-WebSocket-Protocol", AuthProtocol+nonCanonical)
	wsRec := httptest.NewRecorder()
	noStoreMgr.ServeHTTP(wsRec, wsReq)
	require.Equal(t, http.StatusUnauthorized, wsRec.Code)
}
