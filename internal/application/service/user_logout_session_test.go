package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func logoutSessionService(t *testing.T) (*userService, *types.User, *gorm.DB) {
	t.Helper()
	db, err := setupTestSQLiteDB(t)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	user := &types.User{
		ID: "session-user", Username: "session-user", Email: "session@example.com",
		PasswordHash: "unused", IsActive: true, CanAccessAllTenants: true, TenantID: 1,
	}
	svc := &userService{
		userRepo: repository.NewUserRepository(db), tokenRepo: repository.NewAuthTokenRepository(db),
		config: &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}},
	}
	require.NoError(t, svc.userRepo.CreateUser(context.Background(), user))
	return svc, user, db
}

func TestLogoutRevokesWholeRotatedSessionAndPreservesOtherLogin(t *testing.T) {
	ctx := context.Background()
	svc, user, _ := logoutSessionService(t)
	access, refresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	otherAccess, otherRefresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	allAccess, allRefresh := []string{access}, []string{refresh}
	firstRecord, err := svc.tokenRepo.GetTokenByValue(ctx, access)
	require.NoError(t, err)
	require.NotEmpty(t, firstRecord.SessionID)
	for range 3 {
		access, refresh, err = svc.RefreshToken(ctx, refresh)
		require.NoError(t, err)
		for _, value := range []string{access, refresh} {
			record, err := svc.tokenRepo.GetTokenByValue(ctx, value)
			require.NoError(t, err)
			require.Equal(t, firstRecord.SessionID, record.SessionID)
		}
		allAccess, allRefresh = append(allAccess, access), append(allRefresh, refresh)
	}
	// An unexpired access token from before rotation still identifies the same session.
	require.NoError(t, svc.Logout(ctx, allAccess[0]))
	for _, value := range allAccess {
		_, _, err := svc.ValidateToken(ctx, value)
		require.Error(t, err)
	}
	for _, value := range allRefresh {
		newAccess, newRefresh, err := svc.RefreshToken(ctx, value)
		require.Error(t, err)
		require.Empty(t, newAccess)
		require.Empty(t, newRefresh)
	}
	require.Error(t, svc.Logout(ctx, allAccess[0]), "a revoked access token cannot log out again")
	_, _, err = svc.ValidateToken(ctx, otherAccess)
	require.NoError(t, err)
	_, _, err = svc.RefreshToken(ctx, otherRefresh)
	require.NoError(t, err, "another independent login must remain refreshable")
}

func TestSwitchTenantRotatesInSameLogoutSession(t *testing.T) {
	ctx := context.Background()
	svc, user, _ := logoutSessionService(t)
	svc.tenantService = &provisioningTenantService{}
	access, refresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	resp, err := svc.SwitchTenant(ctx, user, 42, refresh)
	require.NoError(t, err)
	_, tenantID, err := svc.ValidateToken(ctx, resp.Token)
	require.NoError(t, err)
	require.EqualValues(t, 42, tenantID)
	_, _, err = svc.RefreshToken(ctx, refresh)
	require.Error(t, err)
	require.NoError(t, svc.Logout(ctx, access))
	_, _, err = svc.RefreshToken(ctx, resp.RefreshToken)
	require.Error(t, err, "switching workspaces must not escape session logout")
}

func TestSwitchTenantRejectsOtherUsersRefreshBeforePreferenceWrite(t *testing.T) {
	ctx := context.Background()
	svc, user, _ := logoutSessionService(t)
	svc.tenantService = &provisioningTenantService{}
	other := &types.User{
		ID:           "other-user",
		Username:     "other",
		Email:        "other@example.com",
		PasswordHash: "unused",
		IsActive:     true,
	}
	require.NoError(t, svc.userRepo.CreateUser(ctx, other))
	_, refresh, err := svc.GenerateTokens(ctx, other)
	require.NoError(t, err)
	resp, err := svc.SwitchTenant(ctx, user, 42, refresh)
	require.ErrorContains(t, err, "different user")
	require.Nil(t, resp)
	stored, err := svc.userRepo.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	require.Nil(t, stored.Preferences.LastActiveTenantID)
	_, _, err = svc.RefreshToken(ctx, refresh)
	require.NoError(t, err)
}

type failingSessionRepo struct {
	interfaces.AuthTokenRepository
	err error
}

func (r *failingSessionRepo) RotateRefreshToken(
	context.Context,
	*types.AuthToken,
	*types.AuthToken,
	*types.AuthToken,
) error {
	return r.err
}

func (r *failingSessionRepo) RevokeTokenSession(context.Context, *types.AuthToken) error {
	return r.err
}

func (r *failingSessionRepo) IssueSessionAccessToken(context.Context, *types.AuthToken, *types.AuthToken) error {
	return r.err
}

func TestSessionMutationErrorsDoNotReportSuccess(t *testing.T) {
	ctx := context.Background()
	svc, user, _ := logoutSessionService(t)
	access, refresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	failure := errors.New("session transaction unavailable")
	svc.tokenRepo = &failingSessionRepo{AuthTokenRepository: svc.tokenRepo, err: failure}
	require.ErrorIs(t, svc.Logout(ctx, access), failure)
	newAccess, newRefresh, err := svc.RefreshToken(ctx, refresh)
	require.ErrorIs(t, err, failure)
	require.Empty(t, newAccess)
	require.Empty(t, newRefresh)
	svc.tenantService = &provisioningTenantService{}
	resp, err := svc.SwitchTenant(ctx, user, 42, refresh)
	require.ErrorIs(t, err, failure)
	require.Nil(t, resp)
	for _, value := range []string{access, refresh} {
		record, err := svc.tokenRepo.GetTokenByValue(ctx, value)
		require.NoError(t, err)
		require.False(t, record.IsRevoked)
	}
}

func TestLogoutRejectsStoredExpiredAccessWithoutRevokingRefresh(t *testing.T) {
	ctx := context.Background()
	svc, user, db := logoutSessionService(t)
	access, refresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.AuthToken{}).Where("token = ?", access).
		Update("expires_at", time.Now().Add(-time.Hour)).Error)
	require.Error(t, svc.Logout(ctx, access))
	_, _, err = svc.RefreshToken(ctx, refresh)
	require.NoError(t, err)
}

func TestSwitchTenantWithoutRefreshInheritsSessionAndExpiryAndHonorsLogout(t *testing.T) {
	ctx := context.Background()
	svc, user, db := logoutSessionService(t)
	svc.tenantService = &provisioningTenantService{}

	initialAccess, initialRefresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)

	shortExpiry := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, db.Model(&types.AuthToken{}).Where("token = ?", initialAccess).
		Update("expires_at", shortExpiry).Error)
	initialRecord, err := svc.tokenRepo.GetTokenByValue(ctx, initialAccess)
	require.NoError(t, err)

	switchCtx := access.WithPresentedAccessToken(ctx, initialAccess)
	resp, err := svc.SwitchTenant(switchCtx, user, 42, "")
	require.NoError(t, err)
	require.NotEmpty(t, resp.Token)
	require.Empty(t, resp.RefreshToken, "switching without refresh_token must not mint a new refresh token")

	_, tenantID, err := svc.ValidateToken(ctx, resp.Token)
	require.NoError(t, err)
	require.EqualValues(t, 42, tenantID)

	switchedRecord, err := svc.tokenRepo.GetTokenByValue(ctx, resp.Token)
	require.NoError(t, err)
	require.Equal(
		t,
		initialRecord.SessionID,
		switchedRecord.SessionID,
		"switched access token must stay in original session",
	)
	require.WithinDuration(
		t,
		shortExpiry,
		switchedRecord.ExpiresAt,
		time.Second,
		"switched access token must inherit original session expiry",
	)

	var refreshCount int64
	require.NoError(t, db.Model(&types.AuthToken{}).Where("token_type = ?", "refresh_token").Count(&refreshCount).Error)
	require.EqualValues(t, 1, refreshCount, "no second refresh_token row may be created")

	// Logout with original access token immediately revokes both the switched access token and the session refresh
	// token.
	require.NoError(t, svc.Logout(ctx, initialAccess))
	_, _, err = svc.ValidateToken(ctx, resp.Token)
	require.Error(t, err, "switched access token must not survive session logout")
	_, _, err = svc.RefreshToken(ctx, initialRefresh)
	require.Error(t, err, "session refresh token must be revoked by logout")

	// A revoked access token cannot switch workspaces again.
	_, err = svc.SwitchTenant(switchCtx, user, 42, "")
	require.Error(t, err)

	// Missing presented access token without refresh_token fails closed.
	_, err = svc.SwitchTenant(ctx, user, 42, "")
	require.Error(t, err)

	// Disabled user fails closed even if caller passes a stale in-memory user struct with IsActive=true.
	freshAccess, _, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.User{}).Where("id = ?", user.ID).Update("is_active", false).Error)
	_, err = svc.SwitchTenant(access.WithPresentedAccessToken(ctx, freshAccess), user, 42, "")
	require.Error(t, err)
}
