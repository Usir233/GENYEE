package service

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSwitchTenantRequiresEnabledCrossTenantPrivilege(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cfg         *config.Config
		member      bool
		wantAllowed bool
	}{
		{"disabled", &config.Config{Tenant: &config.TenantConfig{}}, false, false},
		{"missing config", nil, false, false},
		{"missing tenant config", &config.Config{}, false, false},
		{"enabled", &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}}, false, true},
		{"member with flag disabled", &config.Config{Tenant: &config.TenantConfig{}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, user, db := logoutSessionService(t)
			svc.config = tc.cfg
			svc.tenantService = &provisioningTenantService{}
			members := &membershipLookupService{byTenant: map[uint64]*types.TenantMember{1: {TenantID: 1, Status: types.TenantMemberStatusActive}}}
			if tc.member {
				members.byTenant[42] = &types.TenantMember{TenantID: 42, Status: types.TenantMemberStatusActive}
			}
			svc.memberService = members
			initialAccess, _, err := svc.GenerateTokens(ctx, user)
			require.NoError(t, err)
			var beforeCount int64
			require.NoError(t, db.Model(&types.AuthToken{}).Count(&beforeCount).Error)

			switchCtx := access.WithPresentedAccessToken(ctx, initialAccess)
			resp, err := svc.SwitchTenant(switchCtx, user, 42, "")
			if tc.wantAllowed {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Empty(t, resp.RefreshToken)
				_, tenant, err := svc.ValidateToken(ctx, resp.Token)
				require.NoError(t, err)
				require.EqualValues(t, 42, tenant)
			} else {
				require.ErrorIs(t, err, ErrMembershipNotFound)
				require.Nil(t, resp)
				var afterCount int64
				require.NoError(t, db.Model(&types.AuthToken{}).Count(&afterCount).Error)
				require.Equal(t, beforeCount, afterCount)
				stored, err := svc.userRepo.GetUserByID(ctx, user.ID)
				require.NoError(t, err)
				require.Nil(t, stored.Preferences.LastActiveTenantID)
			}
		})
	}
}

func TestLoginAndRefreshDiscardRevokedCrossTenantPreference(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "login", true: "refresh"}[refresh], func(t *testing.T) {
			ctx := context.Background()
			svc, user, _ := logoutSessionService(t)
			svc.config = &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}}
			svc.tenantService = &provisioningTenantService{}
			svc.memberService = &membershipLookupService{byTenant: map[uint64]*types.TenantMember{1: {TenantID: 1, Status: types.TenantMemberStatusActive}}}
			preferred := uint64(42)
			user.Preferences.LastActiveTenantID = &preferred
			require.NoError(t, svc.userRepo.UpdateUser(ctx, user))
			_, oldRefresh, err := svc.GenerateTokens(ctx, user)
			require.NoError(t, err)
			svc.config.Tenant.EnableCrossTenantAccess = false
			var token string
			if refresh {
				token, _, err = svc.RefreshToken(ctx, oldRefresh)
			} else {
				token, _, err = svc.GenerateTokens(ctx, user)
			}
			require.NoError(t, err)
			_, tenant, err := svc.ValidateToken(ctx, token)
			require.NoError(t, err)
			require.EqualValues(t, 1, tenant)
			stored, err := svc.userRepo.GetUserByID(ctx, user.ID)
			require.NoError(t, err)
			require.Nil(t, stored.Preferences.LastActiveTenantID)
		})
	}
}
