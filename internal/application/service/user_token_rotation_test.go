package service

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRefreshTokenImmediateRotationsAreDistinctAndSingleUse(t *testing.T) {
	db, err := setupTestSQLiteDB(t)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	users := repository.NewUserRepository(db)
	tokens := repository.NewAuthTokenRepository(db)
	svc := &userService{userRepo: users, tokenRepo: tokens}
	ctx := context.Background()
	user := &types.User{ID: "rotation-user", Username: "rotation", Email: "rotation@example.com", TenantID: 1, IsActive: true, PasswordHash: "unused"}
	require.NoError(t, users.CreateUser(ctx, user))
	access, refresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	seenAccess := map[string]bool{access: true}
	seenRefresh := map[string]bool{refresh: true}
	for range 5 {
		previous := refresh
		access, refresh, err = svc.RefreshToken(ctx, previous)
		require.NoError(t, err)
		require.False(t, seenAccess[access], "access JWTs must be distinct even within the same second")
		require.False(t, seenRefresh[refresh], "refresh rotation must issue a different JWT")
		seenAccess[access], seenRefresh[refresh] = true, true
		_, _, err = svc.RefreshToken(ctx, previous)
		require.ErrorContains(t, err, "revoked", "the previous token must remain unusable after rotation")
		_, _, err = svc.ValidateToken(ctx, access)
		require.NoError(t, err)
	}
}
