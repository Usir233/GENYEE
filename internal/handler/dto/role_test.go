package dto

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
)

func TestCanViewIntegrationSecretsAdminRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleAdmin)
	if !CanViewIntegrationSecrets(ctx) {
		t.Fatal("admin should view integration secrets")
	}
}

func TestCanViewIntegrationSecretsViewerDenied(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleViewer)
	if CanViewIntegrationSecrets(ctx) {
		t.Fatal("viewer should not view integration secrets")
	}
}

func TestCanViewIntegrationSecretsScopedAPIKeyWithManageTenantSettings(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityManageTenantSettings)},
	})
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
	if !CanViewIntegrationSecrets(ctx) {
		t.Fatal("manage_tenant_settings API key should view integration secrets")
	}
}

func TestCanViewIntegrationSecretsScopedAPIKeyWithoutCapabilityDenied(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
	})
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
	if CanViewIntegrationSecrets(ctx) {
		t.Fatal("chat-only API key should not view integration secrets")
	}
}

func TestCanViewIntegrationSecretsScopedAPIKeyOverridesAdminRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleOwner)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
	})
	if CanViewIntegrationSecrets(ctx) {
		t.Fatal("narrowed API key scope must not inherit Admin/Owner integration secret access")
	}
	if CanViewTenantAPIKey(ctx) {
		t.Fatal("narrowed API key scope must not inherit Owner tenant API key access")
	}
}

func TestCanViewIntegrationSecretsPlatformScopeRequiresCapability(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		ScopeType:  types.APIKeyScopePlatform,
		FullAccess: true,
	})
	if CanViewIntegrationSecrets(ctx) {
		t.Fatal("platform API key without manage_tenant_settings should not get full-access tenant secrets")
	}
	if CanViewTenantAPIKey(ctx) {
		t.Fatal("platform API key should not view raw tenant API key")
	}
}
