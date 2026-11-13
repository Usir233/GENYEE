package access

import (
	"context"

	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
)

// IsCrossTenantSuperuser reports whether the caller's user attribute and the
// cluster-wide flag both allow bypassing workspace role gates. API-key and
// resource-scope authorization remain separate checks.
func IsCrossTenantSuperuser(ctx context.Context, cfg *config.Config) bool {
	u, _ := ctx.Value(types.UserContextKey).(*types.User)
	return UserCanAccessAllTenants(u, cfg)
}

// UserCanAccessAllTenants applies the same deployment flag before a user's
// identity has been attached to a request, including login and token issuance.
func UserCanAccessAllTenants(user *types.User, cfg *config.Config) bool {
	if cfg == nil || cfg.Tenant == nil || !cfg.Tenant.EnableCrossTenantAccess {
		return false
	}
	return user != nil && user.CanAccessAllTenants
}
