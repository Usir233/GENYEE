package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/mark3labs/mcp-go/client/transport"
)

// MCPOAuthCallbackPath is the fixed server-side OAuth2 callback route.
const MCPOAuthCallbackPath = "/api/v1/mcp-oauth/callback"

var (
	// ErrInvalidOAuthRedirectURI indicates that the OAuth callback redirect_uri
	// is missing a trusted server base URL or does not match the server origin.
	ErrInvalidOAuthRedirectURI = errors.New("invalid or untrusted OAuth redirect_uri")

	// ErrInvalidOAuthEndpoint indicates that an authorization server endpoint
	// (authorization_endpoint, token_endpoint, registration_endpoint) uses a
	// dangerous scheme or fails SSRF validation.
	ErrInvalidOAuthEndpoint = errors.New("invalid or unsafe OAuth endpoint URL")
)

// ValidateServiceOutboundURLs validates every URL that the MCP transport or
// OAuth discovery flow may contact. It is intentionally called both at
// persistence boundaries and immediately before client construction so stale
// or imported rows cannot bypass the current SSRF policy.
func ValidateServiceOutboundURLs(service *types.MCPService) error {
	if service == nil {
		return fmt.Errorf("MCP service is required")
	}
	if service.URL != nil {
		serviceURL := strings.TrimSpace(*service.URL)
		if serviceURL != "" {
			if err := secutils.ValidateURLForSSRF(serviceURL); err != nil {
				return fmt.Errorf("MCP service URL failed SSRF validation: %w", err)
			}
		}
	}
	if service.AuthConfig != nil {
		metadataURL := strings.TrimSpace(service.AuthConfig.AuthServerMetadataURL)
		if metadataURL != "" {
			if err := secutils.ValidateURLForSSRF(metadataURL); err != nil {
				return fmt.Errorf("MCP OAuth metadata URL failed SSRF validation: %w", err)
			}
		}
	}
	return nil
}

// validateOAuthEndpointSyntax ensures an OAuth endpoint URL uses strictly
// http/https without embedded credentials, fragments, or control characters.
func validateOAuthEndpointSyntax(rawURL, fieldName string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("%w: %s is empty", ErrInvalidOAuthEndpoint, fieldName)
	}
	if strings.ContainsAny(trimmed, "\\\r\n\t") {
		return "", fmt.Errorf("%w: %s contains invalid control characters", ErrInvalidOAuthEndpoint, fieldName)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: malformed %s: %v", ErrInvalidOAuthEndpoint, fieldName, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf(
			"%w: %s must use http or https scheme, got %q",
			ErrInvalidOAuthEndpoint, fieldName, parsed.Scheme,
		)
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf(
			"%w: %s must have a valid host without userinfo or fragment",
			ErrInvalidOAuthEndpoint, fieldName,
		)
	}
	return trimmed, nil
}

// ValidateOAuthEndpointURL ensures an OAuth endpoint URL discovered from remote
// metadata (or returned as authorization_url) uses strictly http/https without
// embedded credentials/fragments and passes SSRF validation.
func ValidateOAuthEndpointURL(rawURL, fieldName string) error {
	trimmed, err := validateOAuthEndpointSyntax(rawURL, fieldName)
	if err != nil {
		return err
	}
	if err := secutils.ValidateURLForSSRF(trimmed); err != nil {
		return fmt.Errorf("%w: %s failed SSRF validation: %v", ErrInvalidOAuthEndpoint, fieldName, err)
	}
	return nil
}

// validateOAuthServerMetadata fetches and validates remote OAuth authorization
// server metadata endpoints before registration, redirect, or token exchange.
func validateOAuthServerMetadata(
	ctx context.Context,
	h *transport.OAuthHandler,
	requireAuthEndpoint, requireTokenEndpoint, requireRegistrationEndpoint bool,
) (*transport.AuthServerMetadata, error) {
	metadata, err := h.GetServerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get authorization server metadata: %w", err)
	}
	if metadata == nil {
		return nil, fmt.Errorf("%w: empty authorization server metadata", ErrInvalidOAuthEndpoint)
	}
	if requireAuthEndpoint || strings.TrimSpace(metadata.AuthorizationEndpoint) != "" {
		if err := ValidateOAuthEndpointURL(metadata.AuthorizationEndpoint, "authorization_endpoint"); err != nil {
			return nil, err
		}
	}
	if requireTokenEndpoint || strings.TrimSpace(metadata.TokenEndpoint) != "" {
		if err := ValidateOAuthEndpointURL(metadata.TokenEndpoint, "token_endpoint"); err != nil {
			return nil, err
		}
	}
	if requireRegistrationEndpoint || strings.TrimSpace(metadata.RegistrationEndpoint) != "" {
		if strings.TrimSpace(metadata.RegistrationEndpoint) == "" {
			return nil, fmt.Errorf("%w: registration_endpoint is empty", ErrInvalidOAuthEndpoint)
		}
		if err := ValidateOAuthEndpointURL(metadata.RegistrationEndpoint, "registration_endpoint"); err != nil {
			return nil, err
		}
	}
	return metadata, nil
}

func trustedOAuthBaseURL() string {
	if ext := strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")); ext != "" {
		return ext
	}
	return strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
}

// ResolveOAuthCallbackURI derives or validates the OAuth callback redirect_uri
// against the server's trusted base URL (APP_EXTERNAL_URL / FRONTEND_BASE_URL).
// External or mismatched origins are always rejected so attacker domains can
// never poison tenant-wide MCPOAuthClient registrations.
func ResolveOAuthCallbackURI(rawRedirectURI string, forceCallbackPathOnLoopback bool) (string, error) {
	rawTrimmed := strings.TrimSpace(rawRedirectURI)
	if strings.ContainsAny(rawTrimmed, "\\\r\n\t") {
		return "", fmt.Errorf("%w: redirect_uri contains invalid characters", ErrInvalidOAuthRedirectURI)
	}

	trustedRaw := trustedOAuthBaseURL()
	if trustedRaw != "" {
		trustedURL, err := parseTrustedBaseURL(trustedRaw)
		if err != nil {
			return "", fmt.Errorf("%w: invalid configured server base URL: %v", ErrInvalidOAuthRedirectURI, err)
		}
		fixedCallback := strings.TrimRight(trustedURL.String(), "/") + MCPOAuthCallbackPath
		if rawTrimmed != "" {
			candidate, err := parseCleanHTTPURL(rawTrimmed)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrInvalidOAuthRedirectURI, err)
			}
			if !sameHTTPOriginURL(candidate, trustedURL) {
				return "", fmt.Errorf(
					"%w: redirect_uri origin does not match configured server base URL",
					ErrInvalidOAuthRedirectURI,
				)
			}
		}
		return fixedCallback, nil
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("GIN_MODE")), "release") {
		return "", fmt.Errorf(
			"%w: APP_EXTERNAL_URL or FRONTEND_BASE_URL must be configured",
			ErrInvalidOAuthRedirectURI,
		)
	}
	if rawTrimmed == "" {
		return "", fmt.Errorf(
			"%w: APP_EXTERNAL_URL or FRONTEND_BASE_URL is required when redirect_uri is omitted",
			ErrInvalidOAuthRedirectURI,
		)
	}
	candidate, err := parseCleanHTTPURL(rawTrimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidOAuthRedirectURI, err)
	}
	if !strings.EqualFold(candidate.Scheme, "http") || !isLoopbackOAuthHost(candidate.Hostname()) {
		return "", fmt.Errorf(
			"%w: untrusted redirect_uri host %q (configure APP_EXTERNAL_URL or FRONTEND_BASE_URL)",
			ErrInvalidOAuthRedirectURI, candidate.Host,
		)
	}
	if !strings.HasPrefix(candidate.Path, "/") || strings.HasPrefix(candidate.Path, "//") {
		return "", fmt.Errorf("%w: redirect_uri must use an absolute path", ErrInvalidOAuthRedirectURI)
	}
	if forceCallbackPathOnLoopback {
		return "http://" + candidate.Host + MCPOAuthCallbackPath, nil
	}
	return candidate.String(), nil
}

func parseTrustedBaseURL(raw string) (*url.URL, error) {
	u, err := parseCleanHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Path != "" && strings.HasPrefix(u.Path, "//") {
		return nil, fmt.Errorf("invalid base URL path")
	}
	return u, nil
}

func parseCleanHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("malformed URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("scheme must be http or https")
	}
	if u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("URL must have host without userinfo, query, or fragment")
	}
	return u, nil
}

func sameHTTPOriginURL(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(canonicalOriginHostPort(a), canonicalOriginHostPort(b))
}

func canonicalOriginHostPort(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (strings.EqualFold(u.Scheme, "http") && port == "80") ||
		(strings.EqualFold(u.Scheme, "https") && port == "443") {
		port = ""
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func isLoopbackOAuthHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func validatePinnedTransportURL(target, base *url.URL) error {
	if target == nil || base == nil {
		return fmt.Errorf("missing MCP transport target URL")
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("MCP transport target must use http or https scheme, got %q", target.Scheme)
	}
	if target.User != nil || target.Fragment != "" || strings.ContainsAny(target.String(), "\\\r\n\t") {
		return fmt.Errorf("MCP transport target must not contain userinfo, fragment, or control characters")
	}
	if !sameHTTPOriginURL(target, base) {
		return fmt.Errorf("MCP transport target origin does not match configured service origin")
	}
	return nil
}

type transportGuardRoundTripper struct {
	base          http.RoundTripper
	pinnedOrigin  *url.URL
	maxBodyBytes  func() int64
	maxSSEBytes   func() int64
	onStreamAbort func(error)
}

func newTransportGuardRoundTripper(
	base http.RoundTripper,
	pinnedOrigin *url.URL,
	maxBodyBytes func() int64,
	maxSSEBytes func() int64,
	onStreamAbort func(error),
) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transportGuardRoundTripper{
		base:          base,
		pinnedOrigin:  pinnedOrigin,
		maxBodyBytes:  maxBodyBytes,
		maxSSEBytes:   maxSSEBytes,
		onStreamAbort: onStreamAbort,
	}
}

func (g *transportGuardRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if g.pinnedOrigin != nil {
		if err := validatePinnedTransportURL(req.URL, g.pinnedOrigin); err != nil {
			return nil, err
		}
	}
	resp, err := g.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if isEventStreamResponse(resp) {
		if g.maxSSEBytes != nil {
			if limit := g.maxSSEBytes(); limit > 0 {
				resp.Body = newSSEBoundedReadCloser(resp.Body, limit, g.onStreamAbort)
			}
		}
		return resp, nil
	}
	if g.maxBodyBytes != nil {
		if limit := g.maxBodyBytes(); limit > 0 {
			if resp.ContentLength > limit {
				_ = resp.Body.Close()
				return nil, fmt.Errorf(
					"%w: content-length %d exceeds %d bytes",
					ErrMCPResponseTooLarge, resp.ContentLength, limit,
				)
			}
			resp.Body = newBoundedReadCloser(resp.Body, limit)
		}
	}
	return resp, nil
}

func isEventStreamResponse(resp *http.Response) bool {
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.HasPrefix(strings.ToLower(ct), "text/event-stream")
	}
	return strings.EqualFold(mediaType, "text/event-stream")
}

type boundedReadCloser struct {
	rc    io.ReadCloser
	limit int64
	read  int64
}

func newBoundedReadCloser(rc io.ReadCloser, limit int64) io.ReadCloser {
	return &boundedReadCloser{rc: rc, limit: limit}
}

func (b *boundedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return b.rc.Read(p)
	}
	remaining := b.limit - b.read
	if remaining < 0 {
		return 0, fmt.Errorf("%w: HTTP response exceeds %d bytes", ErrMCPResponseTooLarge, b.limit)
	}
	maxRead := remaining + 1
	if int64(len(p)) > maxRead {
		p = p[:maxRead]
	}
	n, err := b.rc.Read(p)
	b.read += int64(n)
	if b.read > b.limit {
		_ = b.rc.Close()
		return 0, fmt.Errorf("%w: HTTP response exceeds %d bytes", ErrMCPResponseTooLarge, b.limit)
	}
	return n, err
}

func (b *boundedReadCloser) Close() error {
	return b.rc.Close()
}

func readBoundedBody(rc io.Reader, limit int64) ([]byte, error) {
	lr := io.LimitReader(rc, limit+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: HTTP response exceeds %d bytes", ErrMCPResponseTooLarge, limit)
	}
	return data, nil
}

type sseBoundedReadCloser struct {
	rc          io.ReadCloser
	limit       int64
	onAbort     func(error)
	lineLen     int64
	eventBytes  int64
	lineNonCR   bool
	lineComment bool
}

func newSSEBoundedReadCloser(rc io.ReadCloser, limit int64, onAbort func(error)) io.ReadCloser {
	return &sseBoundedReadCloser{rc: rc, limit: limit, onAbort: onAbort}
}

func (s *sseBoundedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return s.rc.Read(p)
	}
	if int64(len(p)) > s.limit+1 {
		p = p[:s.limit+1]
	}
	n, err := s.rc.Read(p)
	for i := 0; i < n; i++ {
		b := p[i]
		if b == '\n' {
			if !s.lineNonCR {
				s.eventBytes = 0
			} else if !s.lineComment {
				s.eventBytes += s.lineLen + 1
				if s.eventBytes > s.limit {
					return s.fail()
				}
			}
			s.lineLen = 0
			s.lineNonCR = false
			s.lineComment = false
			continue
		}
		if s.lineLen == 0 && b == ':' {
			s.lineComment = true
		}
		if b != '\r' {
			s.lineNonCR = true
		}
		s.lineLen++
		if s.lineLen > s.limit || (!s.lineComment && s.eventBytes+s.lineLen > s.limit) {
			return s.fail()
		}
	}
	return n, err
}

func (s *sseBoundedReadCloser) fail() (int, error) {
	err := fmt.Errorf("%w: SSE event exceeds %d bytes", ErrMCPResponseTooLarge, s.limit)
	_ = s.rc.Close()
	if s.onAbort != nil {
		s.onAbort(err)
	}
	return 0, err
}

func (s *sseBoundedReadCloser) Close() error {
	return s.rc.Close()
}
