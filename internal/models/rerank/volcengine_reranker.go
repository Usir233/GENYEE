package rerank

import (
	"fmt"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/models/api/volcengineknowledge"
	modelruntime "github.com/Usir233/GENYEE/internal/models/runtime"
)

const (
	volcengineRerankDefaultModel       = volcengineknowledge.DefaultModel
	volcengineRerankDefaultRegion      = volcengineknowledge.DefaultRegion
	volcengineRerankDefaultInstruction = volcengineknowledge.DefaultInstruction
)

func newVolcengineClient(config *RerankerConfig, resolved *modelruntime.Resolved) (api.Reranker, error) {
	if config == nil {
		return nil, fmt.Errorf("rerank config is nil")
	}
	secret := strings.TrimSpace(config.AppSecret)
	if secret == "" {
		secret = config.ExtraConfig["secret_key"]
	}
	baseURL := config.BaseURL
	model := config.ModelName
	timeout := 30 * time.Second
	if resolved != nil {
		baseURL = resolved.BaseURL
		model = resolved.RemoteModel
		if resolved.Rerank.RequestTimeout > 0 {
			timeout = time.Duration(resolved.Rerank.RequestTimeout) * time.Second
		}
	}
	return volcengineknowledge.New(
		volcengineknowledge.Config{
			AccessKey:   config.APIKey,
			SecretKey:   secret,
			BaseURL:     baseURL,
			Model:       model,
			Region:      config.ExtraConfig["region"],
			Instruction: config.ExtraConfig["instruction"],
			Client:      newRerankHTTPClient(timeout),
		},
	)
}
