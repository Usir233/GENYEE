package rerank

import (
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/models/api/tencentlkeap"
	modelruntime "github.com/Usir233/GENYEE/internal/models/runtime"
)

// Tencent action defaults retained for existing callers.
const (
	LKEAPRerankEndpoint     = tencentlkeap.LKEAPRerankEndpoint
	LKEAPDefaultRegion      = tencentlkeap.LKEAPDefaultRegion
	LKEAPDefaultRerankModel = tencentlkeap.LKEAPDefaultRerankModel
)

// Test composition may route the action SDK to a local stand-in when BaseURL is unset.
var lkeapEndpoint, lkeapScheme = LKEAPRerankEndpoint, "HTTPS"

func newLKEAPClient(config *RerankerConfig, resolved *modelruntime.Resolved) (api.Reranker, error) {
	secret := strings.TrimSpace(config.AppSecret)
	if secret == "" {
		secret = config.ExtraConfig["secret_key"]
	}
	var baseURL, model string
	var timeout time.Duration
	if resolved != nil {
		baseURL = resolved.BaseURL
		model = resolved.RemoteModel
		timeout = time.Duration(resolved.Rerank.RequestTimeout) * time.Second
	}
	return tencentlkeap.New(
		tencentlkeap.Config{
			SecretID:  config.APIKey,
			SecretKey: secret,
			Region:    config.ExtraConfig["region"],
			Model:     model,
			BaseURL:   baseURL,
			Endpoint:  lkeapEndpoint,
			Scheme:    lkeapScheme,
			Client:    newRerankHTTPClient(timeout),
		},
	)
}
