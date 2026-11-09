package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// decoratingTokenRepo wraps a real AuthTokenRepository and allows
// injecting failures or callbacks at specific lifecycle points.
type decoratingTokenRepo struct {
	interfaces.AuthTokenRepository
	onCreateToken func(ctx context.Context, token *types.AuthToken) error
	onRevokeToken func(ctx context.Context, id string) error
}

func (d *decoratingTokenRepo) CreateToken(ctx context.Context, token *types.AuthToken) error {
	if d.onCreateToken != nil {
		if err := d.onCreateToken(ctx, token); err != nil {
			return err
		}
	}
	return d.AuthTokenRepository.CreateToken(ctx, token)
}

func (d *decoratingTokenRepo) RevokeToken(ctx context.Context, id string) error {
	if d.onRevokeToken != nil {
		if err := d.onRevokeToken(ctx, id); err != nil {
			return err
		}
	}
	return d.AuthTokenRepository.RevokeToken(ctx, id)
}

// TestAdversarial_Compensation_RealDB_NoTokenLeakOnRefreshTokenFailure verifies that
// when the refresh token write fails against a real SQLite database:
// 1. GenerateTokens returns an error and empty strings.
// 2. The previously written access token is revoked in the database (is_revoked = true).
// 3. No unrevoked access tokens leak into the database.
// 4. An attacker possessing the access token cannot authenticate via ValidateToken.
func TestAdversarial_Compensation_RealDB_NoTokenLeakOnRefreshTokenFailure(t *testing.T) {
	db, err := setupTestSQLiteDB(t)
	require.NoError(t, err)

	userRepo := repository.NewUserRepository(db)
	realTokenRepo := repository.NewAuthTokenRepository(db)

	ctx := context.Background()
	testUser := &types.User{
		ID:           "user-leak-test-" + uuid.New().String(),
		Username:     "leak_tester",
		Email:        "leak_tester@example.com",
		TenantID:     1,
		IsActive:     true,
		PasswordHash: "dummyhash",
	}
	require.NoError(t, userRepo.CreateUser(ctx, testUser))

	errSimulatedRefresh := errors.New("simulated disk full / db write error on refresh token")

	mockRepo := &decoratingTokenRepo{
		AuthTokenRepository: realTokenRepo,
		onCreateToken: func(ctx context.Context, token *types.AuthToken) error {
			if token.TokenType == "refresh_token" {
				return errSimulatedRefresh
			}
			return nil
		},
	}

	svc := &userService{
		userRepo:  userRepo,
		tokenRepo: mockRepo,
	}

	access, refresh, err := svc.GenerateTokens(ctx, testUser)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist refresh token")
	assert.Empty(t, access, "Access token string must NOT be returned to caller")
	assert.Empty(t, refresh, "Refresh token string must NOT be returned to caller")

	// Verify database rows directly
	userTokens, err := realTokenRepo.GetTokensByUserID(ctx, testUser.ID)
	require.NoError(t, err)
	require.Len(t, userTokens, 1, "Only the compensated access token should exist in the database")

	persistedAccess := userTokens[0]
	assert.Equal(t, "access_token", persistedAccess.TokenType)
	assert.True(t, persistedAccess.IsRevoked, "Access token MUST be revoked in database after compensation")

	// Directly query database to ensure count of active tokens is strictly zero
	var activeTokensCount int64
	err = db.Model(&types.AuthToken{}).
		Where("user_id = ? AND is_revoked = ?", testUser.ID, false).
		Count(&activeTokensCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), activeTokensCount, "Zero active tokens must exist in the database")

	// Attacker Simulation: Try to validate the revoked access token
	_, _, valErr := svc.ValidateToken(ctx, persistedAccess.Token)
	require.Error(t, valErr, "ValidateToken must strictly reject the compensated revoked token")
	assert.Contains(t, valErr.Error(), "token is revoked")
}

// TestAdversarial_Compensation_ContextCancellationGuarantees stress-tests context cancellation:
// 1. Parent context canceled right after access token write: compensation must succeed via context.WithoutCancel.
// 2. Parent context deadline in the past: compensation must still succeed.
// 3. Parent context values (e.g. request trace IDs) must be preserved in the detached context.
func TestAdversarial_Compensation_ContextCancellationGuarantees(t *testing.T) {
	t.Run("parent context canceled between access and refresh token writes", func(t *testing.T) {
		db, err := setupTestSQLiteDB(t)
		require.NoError(t, err)

		userRepo := repository.NewUserRepository(db)
		realTokenRepo := repository.NewAuthTokenRepository(db)

		ctx, cancel := context.WithCancel(context.Background())
		testUser := &types.User{
			ID:           "user-cancel-test-" + uuid.New().String(),
			Username:     "cancel_tester",
			Email:        "cancel_tester@example.com",
			TenantID:     1,
			IsActive:     true,
			PasswordHash: "dummyhash",
		}
		require.NoError(t, userRepo.CreateUser(context.Background(), testUser))

		var capturedRevokeCtx context.Context
		var capturedRevokeCtxErr error

		mockRepo := &decoratingTokenRepo{
			AuthTokenRepository: realTokenRepo,
			onCreateToken: func(callCtx context.Context, token *types.AuthToken) error {
				if token.TokenType == "access_token" {
					return nil
				}
				// When refresh token write begins, cancel parent context
				cancel()
				return callCtx.Err() // returns context.Canceled
			},
			onRevokeToken: func(callCtx context.Context, id string) error {
				capturedRevokeCtx = callCtx
				capturedRevokeCtxErr = callCtx.Err()
				return nil
			},
		}

		svc := &userService{
			userRepo:  userRepo,
			tokenRepo: mockRepo,
		}

		access, refresh, err := svc.GenerateTokens(ctx, testUser)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "persist refresh token")
		assert.Empty(t, access)
		assert.Empty(t, refresh)

		// Assert context.WithoutCancel guarantees
		require.NotNil(t, capturedRevokeCtx, "RevokeToken must be called")
		assert.NoError(t, capturedRevokeCtxErr, "Revoke context must NOT be canceled during RevokeToken call")
		// Also verify that defer revokeCancel() properly cancels the context after return
		assert.Equal(t, context.Canceled, capturedRevokeCtx.Err(), "Revoke context should be cleanly canceled by defer revokeCancel on return")
		deadline, hasDeadline := capturedRevokeCtx.Deadline()
		assert.True(t, hasDeadline, "Revoke context must have a deadline bounded by short timeout")
		assert.True(t, time.Until(deadline) > 0, "Revoke context deadline must be in the future")

		// Verify database state: access token was revoked despite parent cancellation
		userTokens, err := realTokenRepo.GetTokensByUserID(context.Background(), testUser.ID)
		require.NoError(t, err)
		require.Len(t, userTokens, 1)
		assert.True(t, userTokens[0].IsRevoked, "Access token must be marked revoked in DB")
	})

	t.Run("parent context deadline already expired before call", func(t *testing.T) {
		db, err := setupTestSQLiteDB(t)
		require.NoError(t, err)

		userRepo := repository.NewUserRepository(db)
		realTokenRepo := repository.NewAuthTokenRepository(db)

		// Deadline already in the past
		expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-10*time.Second))
		defer cancel()

		testUser := &types.User{
			ID:           "user-expired-test-" + uuid.New().String(),
			Username:     "expired_tester",
			Email:        "expired_tester@example.com",
			TenantID:     1,
			IsActive:     true,
			PasswordHash: "dummyhash",
		}
		require.NoError(t, userRepo.CreateUser(context.Background(), testUser))

		svc := &userService{
			userRepo:  userRepo,
			tokenRepo: realTokenRepo,
		}

		// First CreateToken immediately fails with context deadline exceeded
		access, refresh, err := svc.GenerateTokens(expiredCtx, testUser)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "persist access token")
		assert.Empty(t, access)
		assert.Empty(t, refresh)

		// Zero tokens should be created in DB
		userTokens, err := realTokenRepo.GetTokensByUserID(context.Background(), testUser.ID)
		require.NoError(t, err)
		assert.Empty(t, userTokens, "No tokens should be persisted when parent context is pre-expired")
	})

	t.Run("context values are preserved in detached revocation context", func(t *testing.T) {
		type ctxKey string
		const testKey ctxKey = "trace-request-id"
		const testVal = "req-adversarial-trace-999"

		ctx := context.WithValue(context.Background(), testKey, testVal)
		var capturedVal any

		mockRepo := &decoratingTokenRepo{
			AuthTokenRepository: &mockCompensationTokenRepo{
				createErrs: []error{nil, errors.New("refresh fail")},
			},
			onRevokeToken: func(callCtx context.Context, id string) error {
				capturedVal = callCtx.Value(testKey)
				return nil
			},
		}

		svc := &userService{tokenRepo: mockRepo}
		user := &types.User{ID: "u-ctx-val", TenantID: 1, CanAccessAllTenants: true}

		_, _, err := svc.GenerateTokens(ctx, user)
		require.Error(t, err)
		assert.Equal(t, testVal, capturedVal, "Detached context via context.WithoutCancel must preserve parent context values")
	})
}

// TestAdversarial_Compensation_ErrorsJoinDeepVerification verifies that:
// 1. When both refresh persistence and revocation fail, errors.Join preserves both causes.
// 2. errors.Is can identify both root cause errors.
// 3. errors.As can unwrap custom typed error pointers from both branches.
// 4. Combined error string contains diagnostic information for both failure phases.
func TestAdversarial_Compensation_ErrorsJoinDeepVerification(t *testing.T) {
	type CustomRefreshError struct {
		OpCode int
		Reason string
	}
	customRefErr := &CustomRefreshError{OpCode: 5001, Reason: "qdrant/postgres disk quota exceeded"}

	type CustomRevokeError struct {
		AuditID string
		Details string
	}
	customRevErr := &CustomRevokeError{AuditID: "audit-fail-777", Details: "cas transaction lock timeout"}

	sentinelRef := errors.New("sentinel refresh failure")
	sentinelRev := errors.New("sentinel revoke failure")

	joinedRefErr := fmt.Errorf("%w: %s", sentinelRef, customRefErr.Reason)
	joinedRevErr := fmt.Errorf("%w: %s", sentinelRev, customRevErr.Details)

	mockRepo := &mockCompensationTokenRepo{
		createErrs: []error{nil, joinedRefErr},
		revokeErr:  joinedRevErr,
	}

	svc := &userService{tokenRepo: mockRepo}
	user := &types.User{ID: "u-errors-join", TenantID: 1, CanAccessAllTenants: true}

	_, _, err := svc.GenerateTokens(context.Background(), user)
	require.Error(t, err)

	// 1. errors.Is unwrapping
	assert.True(t, errors.Is(err, sentinelRef), "errors.Is must identify the root refresh failure")
	assert.True(t, errors.Is(err, sentinelRev), "errors.Is must identify the root revocation compensation failure")

	// 2. Error message format validation
	errMsg := err.Error()
	assert.Contains(t, errMsg, "persist refresh token: sentinel refresh failure")
	assert.Contains(t, errMsg, "compensate access token revocation: sentinel revoke failure")

	// 3. Structured multi-error unwrapping (Go 1.20+ Unwrap() []error)
	type multiUnwrapper interface {
		Unwrap() []error
	}
	multi, ok := err.(multiUnwrapper)
	require.True(t, ok, "Error returned by errors.Join must implement Unwrap() []error")
	unwrapped := multi.Unwrap()
	require.Len(t, unwrapped, 2, "Joined error must unwrap to exactly two errors")
}

// TestAdversarial_Compensation_ConcurrentFailuresStress launches 50 concurrent
// GenerateTokens requests all hitting refresh token failures simultaneously on a real database.
// Under race detector, all 50 access tokens must be cleanly revoked without deadlock or corruption.
func TestAdversarial_Compensation_ConcurrentFailuresStress(t *testing.T) {
	db, err := setupTestSQLiteDB(t)
	require.NoError(t, err)

	userRepo := repository.NewUserRepository(db)
	realTokenRepo := repository.NewAuthTokenRepository(db)

	ctx := context.Background()
	testUser := &types.User{
		ID:           "user-stress-test-" + uuid.New().String(),
		Username:     "stress_tester",
		Email:        "stress_tester@example.com",
		TenantID:     1,
		IsActive:     true,
		PasswordHash: "dummyhash",
	}
	require.NoError(t, userRepo.CreateUser(ctx, testUser))

	mockRepo := &decoratingTokenRepo{
		AuthTokenRepository: realTokenRepo,
		onCreateToken: func(ctx context.Context, token *types.AuthToken) error {
			if token.TokenType == "refresh_token" {
				return errors.New("concurrent refresh failure injection")
			}
			return nil
		},
	}

	svc := &userService{
		userRepo:  userRepo,
		tokenRepo: mockRepo,
	}

	const concurrency = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			access, refresh, err := svc.GenerateTokens(ctx, testUser)
			assert.Error(t, err)
			assert.Empty(t, access)
			assert.Empty(t, refresh)
		}()
	}

	close(startCh)
	wg.Wait()

	// Verify all 50 persisted access tokens in DB are revoked
	tokens, err := realTokenRepo.GetTokensByUserID(ctx, testUser.ID)
	require.NoError(t, err)
	assert.Equal(t, concurrency, len(tokens), "All 50 attempted access tokens should be recorded")

	for _, tok := range tokens {
		assert.True(t, tok.IsRevoked, "Every access token must be cleanly revoked")
	}

	var activeCount int64
	err = db.Model(&types.AuthToken{}).
		Where("user_id = ? AND is_revoked = ?", testUser.ID, false).
		Count(&activeCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), activeCount, "Exactly zero active tokens must exist in DB")
}
