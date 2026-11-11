package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// ValidateOAuthEndpointURL ensures an OAuth endpoint URL discovered from remote
// metadata (or returned as authorization_url) uses strictly http/https without
// embedded credentials/fragments and passes SSRF validation.
func ValidateOAuthEndpointURL(rawURL, fieldName string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidOAuthEndpoint, fieldName)
	}
	if strings.ContainsAny(trimmed, "\\\r\n\t") {
		return fmt.Errorf("%w: %s contains invalid control characters", ErrInvalidOAuthEndpoint, fieldName)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: malformed %s: %v", ErrInvalidOAuthEndpoint, fieldName, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: %s must use http or https scheme, got %q", ErrInvalidOAuthEndpoint, fieldName, parsed.Scheme)
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%w: %s must have a valid host without userinfo or fragment", ErrInvalidOAuthEndpoint, fieldName)
	}
	if err := secutils.ValidateURLForSSRF(trimmed); err != nil {
		return fmt.Errorf("%w: %s failed SSRF validation: %v", ErrInvalidOAuthEndpoint, fieldName, err)
	}
	return nil
}

// validateOAuthServerMetadata fetches and validates remote OAuth authorization
// server metadata endpoints before registration, redirect, or token exchange.
func validateOAuthServerMetadata(
	ctx context.Context, h *transport.OAuthHandler, requireAuthEndpoint, requireTokenEndpoint, requireRegistrationEndpoint bool,
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
				return "", fmt.Errorf("%w: redirect_uri origin does not match configured server base URL", ErrInvalidOAuthRedirectURI)
			}
		}
		return fixedCallback, nil
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("GIN_MODE")), "release") {
		return "", fmt.Errorf("%w: APP_EXTERNAL_URL or FRONTEND_BASE_URL must be configured", ErrInvalidOAuthRedirectURI)
	}
	if rawTrimmed == "" {
		return "", fmt.Errorf("%w: APP_EXTERNAL_URL or FRONTEND_BASE_URL is required when redirect_uri is omitted", ErrInvalidOAuthRedirectURI)
	}
	candidate, err := parseCleanHTTPURL(rawTrimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidOAuthRedirectURI, err)
	}
	if !strings.EqualFold(candidate.Scheme, "http") || !isLoopbackOAuthHost(candidate.Hostname()) {
		return "", fmt.Errorf("%w: untrusted redirect_uri host %q (configure APP_EXTERNAL_URL or FRONTEND_BASE_URL)", ErrInvalidOAuthRedirectURI, candidate.Host)
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
