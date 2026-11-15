package datasource

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/utils"
)

// ValidateConnectorBaseURL checks a connector API base URL against the SSRF policy.
// Empty rawURL is allowed; callers apply their own default before issuing requests.
func ValidateConnectorBaseURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return errors.New("base_url SSRF validation failed: URL contains control characters")
		}
	}
	candidate := trimmed
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("base_url SSRF validation failed: invalid base_url")
	}
	if parsed.User != nil {
		return errors.New("base_url SSRF validation failed: embedded credentials are not allowed")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("base_url SSRF validation failed: query and fragment are not allowed")
	}
	if err := utils.ValidateURLForSSRF(candidate); err != nil {
		return fmt.Errorf("base_url SSRF validation failed: %w", err)
	}
	return nil
}

// NewConnectorHTTPClient returns an HTTP client with redirect and dial-time SSRF guards.
func NewConnectorHTTPClient(timeout time.Duration) *http.Client {
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return utils.NewSSRFSafeHTTPClient(cfg)
}
