package types

import (
	"os"
	"strings"
)

// WeKnoraCloudStatusResult 状态检查结果
type WeKnoraCloudStatusResult struct {
	Enabled     bool   `json:"enabled"`          // 是否已启用 WeKnoraCloud 集成
	HasModels   bool   `json:"has_models"`       // 是否已配置 WeKnoraCloud 凭证
	NeedsReinit bool   `json:"needs_reinit"`     // 是否需要重新初始化（凭证损坏）
	Reason      string `json:"reason,omitempty"` // 需要重新初始化的原因
}

// IsWeKnoraCloudEnabled reports whether the optional WeKnoraCloud integration
// is enabled via GENYEE_WEKNORACLOUD_ENABLED=true (default: false).
func IsWeKnoraCloudEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("GENYEE_WEKNORACLOUD_ENABLED")), "true")
}
