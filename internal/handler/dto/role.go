package dto

import (
	"context"

	"github.com/Usir233/GENYEE/internal/types"
)

// RoleFromContext returns the caller's tenant role from ctx.
func RoleFromContext(ctx context.Context) types.TenantRole {
	return types.TenantRoleFromContext(ctx)
}

// CanViewIntegrationSecrets is true for Admin+ tenant members and for API keys
// with full tenant access or the manage_tenant_settings capability. When a
// TenantAPIKeyScope is attached to ctx, its scope is authoritative and never
// falls back to RoleFromContext.
func CanViewIntegrationSecrets(ctx context.Context) bool {
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		if scope.FullAccess && !scope.IsPlatform() {
			return true
		}
		return scope.HasCapability(types.APIKeyCapabilityManageTenantSettings)
	}
	return RoleFromContext(ctx).HasPermission(types.TenantRoleAdmin)
}

// RoleCanViewTenantAPIKey is true for Owner+ only.
func RoleCanViewTenantAPIKey(role types.TenantRole) bool {
	return role.HasPermission(types.TenantRoleOwner)
}

// CanViewTenantAPIKey is true for Owner+ human members and full-access tenant
// API keys; narrowed or platform API key scopes never inherit Owner access.
func CanViewTenantAPIKey(ctx context.Context) bool {
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		return scope.FullAccess && !scope.IsPlatform()
	}
	return RoleCanViewTenantAPIKey(RoleFromContext(ctx))
}
