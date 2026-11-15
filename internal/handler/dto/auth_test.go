package dto

import (
	"encoding/json"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthLoginResponse_ViewerOmitsActiveTenantSecrets(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewAuthLoginResponse(&types.LoginResponse{
		Success:      true,
		ActiveTenant: tenant,
		Memberships: []types.Membership{{
			TenantID: tenant.ID,
			Role:     types.TenantRoleViewer,
		}},
	})
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "tenant-api-key-123")
	assert.NotContains(t, s, "parser-secret-123")
}

func TestAuthLoginResponse_OwnerOmitsLegacyTenantAPIKey(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewAuthLoginResponse(&types.LoginResponse{
		Success:      true,
		ActiveTenant: tenant,
		Memberships: []types.Membership{{
			TenantID: tenant.ID,
			Role:     types.TenantRoleOwner,
		}},
	})
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, `"api_key"`)
	assert.NotContains(t, s, "legacy-search-secret-999")
	assert.Contains(t, s, "web_search_config")
}

func TestAuthOIDCCallbackResponse_ViewerOmitsTenantSecrets(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewAuthOIDCCallbackResponse(&types.OIDCCallbackResponse{
		Success: true,
		Tenant:  tenant,
		Memberships: []types.Membership{{
			TenantID: tenant.ID,
			Role:     types.TenantRoleViewer,
		}},
	})
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "tenant-api-key-123")
}

func TestAuthResponses_StripEmbeddedUserTenantSecrets(t *testing.T) {
	secretTenant := sampleSecretTenant()
	user := &types.User{
		ID:       "user-1",
		Username: "alice",
		TenantID: secretTenant.ID,
		Tenant:   secretTenant,
	}

	loginResp := NewAuthLoginResponse(&types.LoginResponse{
		Success:      true,
		User:         user,
		ActiveTenant: secretTenant,
		Memberships:  []types.Membership{{TenantID: secretTenant.ID, Role: types.TenantRoleViewer}},
	})
	loginBody, err := json.Marshal(loginResp)
	require.NoError(t, err)
	assert.NotContains(t, string(loginBody), "minio-secret-789")
	assert.NotContains(t, string(loginBody), "parser-secret-123")
	assert.NotContains(t, string(loginBody), "legacy-search-secret-999")
	assert.NotNil(t, user.Tenant, "input User struct must not be mutated in place")

	oidcResp := NewAuthOIDCCallbackResponse(&types.OIDCCallbackResponse{
		Success:     true,
		User:        user,
		Tenant:      secretTenant,
		Memberships: []types.Membership{{TenantID: secretTenant.ID, Role: types.TenantRoleViewer}},
	})
	oidcBody, err := json.Marshal(oidcResp)
	require.NoError(t, err)
	assert.NotContains(t, string(oidcBody), "minio-secret-789")
	assert.NotContains(t, string(oidcBody), "parser-secret-123")
}
