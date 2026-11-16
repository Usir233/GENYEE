package vlm

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	secutils "github.com/Usir233/GENYEE/internal/utils"
)

var (
	vlmUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	vlmQuerySecretRE = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`)
)

func redactVLMText(s string) string {
	out := vlmUserinfoRE.ReplaceAllString(s, "${1}:[REDACTED]@")
	return vlmQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
}

func validateVLMBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		msg := fmt.Sprintf("base URL SSRF check failed: %v", err)
		if redacted := redactVLMText(msg); redacted != msg {
			return errors.New(redacted)
		}
		return fmt.Errorf("base URL SSRF check failed: %w", err)
	}
	return nil
}

func newVLMHTTPClient(timeout time.Duration) *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return secutils.NewSSRFSafeHTTPClient(cfg)
}
