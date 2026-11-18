package cmdutil

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	sdk "github.com/Usir233/GENYEE/client"
)

// AuthRetryTransport wraps a base http.RoundTripper with transparent
// recovery from a single 401 response. On a 401 from an authenticated
// (non-/auth/*) endpoint, it invokes refreshFn to obtain a new bearer
// token, replays the original request with the new Authorization header,
// and returns the replayed response.
//
// Behavior:
//   - 2xx / 4xx / 5xx other than 401: pass through unchanged.
//   - 401 on /api/v1/auth/login or /api/v1/auth/refresh: pass through
//     (otherwise a stale refresh token causes infinite recursion).
//   - 401 with no initial token configured (api-key profiles): pass through
//   - api-key credentials have no refresh semantic.
//   - 401 with non-replayable request body (req.GetBody == nil): pass
//     through. The SDK always uses bytes.Buffer bodies; this is a safety
//     net for hand-built requests.
//
// Concurrency: multiple goroutines may hit 401 simultaneously. The first
// one through acquires the mutex and refreshes; subsequent waiters observe
// the updated currentToken and replay without re-refreshing (singleflight).
//
// The transport intentionally shadows the SDK's bearerToken (set via
// WithBearerToken). After a refresh, the SDK is unaware of the new token -
// the transport's per-request override is the single source of truth for
// the Authorization header. Tokens persisted to the secrets store happen
// inside refreshFn (see cmdutil.RefreshAndPersist).
type AuthRetryTransport struct {
	base http.RoundTripper

	mu           sync.Mutex
	currentToken string
	originScheme string
	originHost   string
	originPath   string
	refreshFn    func(context.Context) (string, error)
}

// NewAuthRetryTransport builds a retry transport. initialToken seeds the
// in-memory token state so the first request's Authorization header
// agrees with whatever the SDK was constructed with.
//
// Pass an empty initialToken to indicate "no bearer credential configured"
// (e.g. an api-key profile). In that mode the transport never invokes
// refreshFn - a 401 is propagated as-is.
func NewAuthRetryTransport(
	base http.RoundTripper,
	initialToken string,
	refreshFn func(context.Context) (string, error),
) *AuthRetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &AuthRetryTransport{
		base:         base,
		currentToken: initialToken,
		refreshFn:    refreshFn,
	}
}

// NewAuthRetryTransportWithOrigin builds an AuthRetryTransport explicitly
// pinned to the configured API base URL's scheme://host.
func NewAuthRetryTransportWithOrigin(
	base http.RoundTripper,
	baseURL string,
	initialToken string,
	refreshFn func(context.Context) (string, error),
) *AuthRetryTransport {
	rt := NewAuthRetryTransport(base, initialToken, refreshFn)
	if u, err := url.Parse(strings.TrimSpace(baseURL)); err == nil && u.Scheme != "" && u.Host != "" {
		rt.originScheme = strings.ToLower(u.Scheme)
		rt.originHost = strings.ToLower(u.Host)
		rt.originPath = strings.TrimRight(u.Path, "/")
	}
	return rt
}

func redirectChainCrossedOrigin(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return true
	}
	cur := req
	for cur.Response != nil && cur.Response.Request != nil {
		prev := cur.Response.Request
		if prev.URL == nil || !sameOriginURL(prev.URL, req.URL) {
			return true
		}
		cur = prev
	}
	return false
}

func sameOriginURL(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (t *AuthRetryTransport) matchesTrustedOrigin(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	if redirectChainCrossedOrigin(req) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.originScheme == "" && t.originHost == "" {
		if req.URL.Scheme != "" && req.URL.Host != "" {
			t.originScheme = strings.ToLower(req.URL.Scheme)
			t.originHost = strings.ToLower(req.URL.Host)
		}
		return true
	}
	return strings.EqualFold(req.URL.Scheme, t.originScheme) &&
		strings.EqualFold(req.URL.Host, t.originHost)
}

// RoundTrip implements http.RoundTripper.
func (t *AuthRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.matchesTrustedOrigin(req) {
		// Cross-origin redirect or foreign host: clone headers so we never
		// mutate caller state, strip all credentials, and never replay/refresh.
		clean := req.Clone(req.Context())
		clean.Header.Del("Authorization")
		clean.Header.Del("X-API-Key")
		clean.Header.Del("X-Tenant-ID")
		clean.Header.Del("X-Request-ID")
		return t.base.RoundTrip(clean)
	}

	t.mu.Lock()
	token := t.currentToken
	t.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if token == "" {
		return resp, nil
	}
	if t.isAuthEndpoint(req.URL.Path) {
		return resp, nil
	}
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}

	// Singleflight: re-check whether another goroutine already refreshed.
	t.mu.Lock()
	if t.currentToken != token {
		token = t.currentToken
		t.mu.Unlock()
	} else {
		newToken, refreshErr := t.refreshFn(req.Context())
		if refreshErr != nil {
			t.mu.Unlock()
			drainAndClose(resp)
			return nil, refreshErr
		}
		t.currentToken = newToken
		token = newToken
		t.mu.Unlock()
	}

	drainAndClose(resp)

	replay := req.Clone(req.Context())
	if req.GetBody != nil {
		body, gberr := req.GetBody()
		if gberr != nil {
			return nil, gberr
		}
		replay.Body = body
	}
	replay.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(replay)
}

const maxAuthRetryDrainBytes = 64 << 10 // 64 KiB

// isAuthEndpoint reports whether path is one of the auth endpoints where
// 401 retry would either recurse (refresh) or duplicate work (login). The
// path constants are sourced from the SDK so the CLI and SDK can never
// drift on the canonical paths.
func (t *AuthRetryTransport) isAuthEndpoint(path string) bool {
	if strings.HasPrefix(path, sdk.PathAuthLogin) || strings.HasPrefix(path, sdk.PathAuthRefresh) {
		return true
	}
	t.mu.Lock()
	prefix := t.originPath
	t.mu.Unlock()
	if prefix != "" && strings.HasPrefix(path, prefix) {
		rel := strings.TrimPrefix(path, prefix)
		return strings.HasPrefix(rel, sdk.PathAuthLogin) || strings.HasPrefix(rel, sdk.PathAuthRefresh)
	}
	return false
}

// drainAndClose consumes up to maxAuthRetryDrainBytes of the response body
// so the underlying connection can be reused without unbounded reads, then closes it.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxAuthRetryDrainBytes))
	_ = resp.Body.Close()
}
