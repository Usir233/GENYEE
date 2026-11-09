package service

import (
	"context"
	"errors"
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

type mockCompensationTokenRepo struct {
	createCalls   int
	createdTokens []*types.AuthToken
	createErrs    []error
	revokeCalls   int
	revokedIDs    []string
	revokeErr     error
	revokeCtxErr  error
}

func (m *mockCompensationTokenRepo) CreateToken(_ context.Context, token *types.AuthToken) error {
	m.createCalls++
	m.createdTokens = append(m.createdTokens, token)
	if m.createCalls <= len(m.createErrs) {
		if err := m.createErrs[m.createCalls-1]; err != nil {
			return err
		}
	}
	return nil
}

func (m *mockCompensationTokenRepo) RevokeToken(ctx context.Context, id string) error {
	m.revokeCalls++
	m.revokedIDs = append(m.revokedIDs, id)
	m.revokeCtxErr = ctx.Err()
	return m.revokeErr
}

func (m *mockCompensationTokenRepo) GetTokenByValue(context.Context, string) (*types.AuthToken, error) {
	return nil, nil
}
func (m *mockCompensationTokenRepo) GetTokenByID(context.Context, string) (*types.AuthToken, error) {
	return nil, nil
}
func (m *mockCompensationTokenRepo) GetTokensByUserID(context.Context, string) ([]*types.AuthToken, error) {
	return nil, nil
}
func (m *mockCompensationTokenRepo) UpdateToken(context.Context, *types.AuthToken) error {
	return nil
}
func (m *mockCompensationTokenRepo) DeleteToken(context.Context, string) error {
	return nil
}
func (m *mockCompensationTokenRepo) DeleteExpiredTokens(context.Context) error {
	return nil
}
func (m *mockCompensationTokenRepo) RevokeTokensByUserID(context.Context, string) error {
	return nil
}

func TestGenerateTokens_CompensationOnRefreshTokenFailure(t *testing.T) {
	t.Run("first CreateToken succeeds, second fails -> token revoked and empty tokens returned", func(t *testing.T) {
		mockRepo := &mockCompensationTokenRepo{
			createErrs: []error{nil, errors.New("db insert refresh token error")},
		}
		svc := &userService{tokenRepo: mockRepo}
		user := &types.User{ID: "u1", TenantID: 1, CanAccessAllTenants: true}

		access, refresh, err := svc.GenerateTokens(context.Background(), user)
		require.Error(t, err)
		require.Contains(t, err.Error(), "persist refresh token")
		require.Empty(t, access)
		require.Empty(t, refresh)

		require.Equal(t, 2, mockRepo.createCalls)
		require.Equal(t, 1, mockRepo.revokeCalls)
		require.Len(t, mockRepo.createdTokens, 2)
		require.Equal(t, mockRepo.createdTokens[0].ID, mockRepo.revokedIDs[0])
	})

	t.Run("both second CreateToken and RevokeToken fail -> errors joined", func(t *testing.T) {
		mockRepo := &mockCompensationTokenRepo{
			createErrs: []error{nil, errors.New("fail refresh token insert")},
			revokeErr:  errors.New("db connection lost during revoke"),
		}
		svc := &userService{tokenRepo: mockRepo}
		user := &types.User{ID: "u2", TenantID: 1, CanAccessAllTenants: true}

		access, refresh, err := svc.GenerateTokens(context.Background(), user)
		require.Error(t, err)
		require.Contains(t, err.Error(), "persist refresh token")
		require.Contains(t, err.Error(), "compensate access token revocation")
		require.Empty(t, access)
		require.Empty(t, refresh)

		require.Equal(t, 1, mockRepo.revokeCalls)
	})

	t.Run("canceled parent context -> compensation succeeds with active detached context", func(t *testing.T) {
		mockRepo := &mockCompensationTokenRepo{
			createErrs: []error{nil, errors.New("context canceled during refresh insert")},
		}
		svc := &userService{tokenRepo: mockRepo}
		user := &types.User{ID: "u3", TenantID: 1, CanAccessAllTenants: true}

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		access, refresh, err := svc.GenerateTokens(canceledCtx, user)
		require.Error(t, err)
		require.Empty(t, access)
		require.Empty(t, refresh)

		require.Equal(t, 1, mockRepo.revokeCalls)
		require.NoError(t, mockRepo.revokeCtxErr, "revoke context must not inherit parent cancellation")
	})
}
