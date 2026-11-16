package rerank

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	secutils "github.com/Usir233/GENYEE/internal/utils"
)

var (
	rerankUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	rerankQuerySecretRE = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`)
)

func redactRerankText(s string) string {
	out := rerankUserinfoRE.ReplaceAllString(s, "${1}:[REDACTED]@")
	return rerankQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
}

func validateRerankBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		msg := fmt.Sprintf("base URL SSRF check failed: %v", err)
		if redacted := redactRerankText(msg); redacted != msg {
			return errors.New(redacted)
		}
		return fmt.Errorf("base URL SSRF check failed: %w", err)
	}
	return nil
}

// sharedRerankHTTPTransport keeps a single SSRF-safe connection pool for all
// rerank clients. Rerankers are recreated on every GetRerankModel call, but
// their outbound connections can be safely reused, so the transport (and its
// keep-alive pool) is built once at package load.
var sharedRerankHTTPTransport = secutils.NewSSRFSafeTransport(
	secutils.DefaultSSRFSafeHTTPClientConfig(),
)

// defaultRerankRequestTimeout caps one rerank request when the catalog sets
// no timeout. Chat and agent contexts carry no deadline of their own, so a
// stalled self-hosted rerank server used to hold the turn open until the
// client disconnected. A failed call degrades to retrieval order.
const defaultRerankRequestTimeout = 60 * time.Second

// newRerankHTTPClient returns an HTTP client with connection-level SSRF
// protection and redirect validation. All clients share
// sharedRerankHTTPTransport so keep-alive connections are pooled globally,
// while each keeps its own timeout.
func newRerankHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultRerankRequestTimeout
	}
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return secutils.NewSSRFSafeHTTPClientWithTransport(cfg, sharedRerankHTTPTransport)
}
