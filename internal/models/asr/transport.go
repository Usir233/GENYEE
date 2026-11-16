package asr

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	secutils "github.com/Usir233/GENYEE/internal/utils"
)

var (
	asrUserinfoRE    = regexp.MustCompile(`(https?://[^/\s:@]+):([^@\s/]+)@`)
	asrQuerySecretRE = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|access_token|token|secret|password|sig|signature)=)[^&\s"']+`)
)

func redactASRText(s string) string {
	out := asrUserinfoRE.ReplaceAllString(s, "${1}:[REDACTED]@")
	return asrQuerySecretRE.ReplaceAllString(out, "${1}[REDACTED]")
}

func validateASRBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		msg := fmt.Sprintf("base URL SSRF check failed: %v", err)
		if redacted := redactASRText(msg); redacted != msg {
			return errors.New(redacted)
		}
		return fmt.Errorf("base URL SSRF check failed: %w", err)
	}
	return nil
}

func newASRHTTPClient(timeout time.Duration) *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return secutils.NewSSRFSafeHTTPClient(cfg)
}
