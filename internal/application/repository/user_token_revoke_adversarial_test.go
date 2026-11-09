package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Usir233/GENYEE/internal/types"
)

// setupRepoTestDB initializes an isolated in-memory SQLite database for repository tests.
func setupRepoTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.New().String())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&types.AuthToken{}, &types.User{}); err != nil {
		return nil, err
	}
	return db, nil
}

// TestAdversarial_RevokeToken_ConcurrentCASRace tests 100 concurrent goroutines
// racing to call RevokeToken on the exact same active token record.
// CAS update (WHERE id = ? AND is_revoked = false) must guarantee that
// revocation is atomic, safe, idempotent, and leaves the database in a cleanly revoked state.
func TestAdversarial_RevokeToken_ConcurrentCASRace(t *testing.T) {
	db, err := setupRepoTestDB(t)
	require.NoError(t, err)

	repo := NewAuthTokenRepository(db)
	ctx := context.Background()

	tokenID := "tok-cas-race-" + uuid.New().String()
	token := &types.AuthToken{
		ID:        tokenID,
		UserID:    "user-cas-1",
		Token:     "jwt-cas-token-" + tokenID,
		TokenType: "access_token",
		IsRevoked: false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateToken(ctx, token))

	const concurrency = 100
	var wg sync.WaitGroup
	startCh := make(chan struct{})
	var successCount int64
	var errCount int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh // Align all goroutines to the same instant

			err := repo.RevokeToken(ctx, tokenID)
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errCount, 1)
			}
		}()
	}

	close(startCh)
	wg.Wait()

	// RevokeToken is idempotent and must not error under concurrency
	assert.Equal(t, int64(concurrency), successCount, "All concurrent RevokeToken calls should return nil error")
	assert.Equal(t, int64(0), errCount, "No RevokeToken call should fail with an unhandled error")

	// Verify database state: token is strictly revoked
	stored, err := repo.GetTokenByID(ctx, tokenID)
	require.NoError(t, err)
	assert.True(t, stored.IsRevoked, "Token in database must be revoked")

	// Additional assertion: direct SQL query to verify row count
	var revokedCount int64
	err = db.Model(&types.AuthToken{}).Where("id = ? AND is_revoked = ?", tokenID, true).Count(&revokedCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), revokedCount)

	var activeCount int64
	err = db.Model(&types.AuthToken{}).Where("id = ? AND is_revoked = ?", tokenID, false).Count(&activeCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), activeCount)
}

// TestAdversarial_RevokeToken_IdempotentAndNonExistent tests edge cases:
// 1. Repeated sequential revocations on an already-revoked token.
// 2. Calling RevokeToken on a non-existent token ID.
// Neither should produce SQL syntax or runtime panics.
func TestAdversarial_RevokeToken_IdempotentAndNonExistent(t *testing.T) {
	db, err := setupRepoTestDB(t)
	require.NoError(t, err)

	repo := NewAuthTokenRepository(db)
	ctx := context.Background()

	// 1. Revoke non-existent token: returns nil (idempotent no-op)
	nonExistentID := "non-existent-" + uuid.New().String()
	err = repo.RevokeToken(ctx, nonExistentID)
	assert.NoError(t, err, "Revoking non-existent token must not return an error")

	// 2. Create token and revoke multiple times
	tokenID := "tok-idempotent-" + uuid.New().String()
	token := &types.AuthToken{
		ID:        tokenID,
		UserID:    "user-idem-1",
		Token:     "jwt-idem-" + tokenID,
		TokenType: "access_token",
		IsRevoked: false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateToken(ctx, token))

	for i := 0; i < 5; i++ {
		err = repo.RevokeToken(ctx, tokenID)
		assert.NoError(t, err, "Repeated RevokeToken call #%d must succeed", i+1)
	}

	stored, err := repo.GetTokenByID(ctx, tokenID)
	require.NoError(t, err)
	assert.True(t, stored.IsRevoked)
}

// TestAdversarial_RevokeToken_ContextCancellation tests context behavior:
// 1. Calling RevokeToken with a pre-canceled context fails with context.Canceled.
// 2. Using context.WithoutCancel(canceledCtx) detaches cancellation and allows RevokeToken to succeed.
func TestAdversarial_RevokeToken_ContextCancellation(t *testing.T) {
	db, err := setupRepoTestDB(t)
	require.NoError(t, err)

	repo := NewAuthTokenRepository(db)

	tokenID := "tok-ctx-" + uuid.New().String()
	token := &types.AuthToken{
		ID:        tokenID,
		UserID:    "user-ctx-1",
		Token:     "jwt-ctx-" + tokenID,
		TokenType: "access_token",
		IsRevoked: false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateToken(context.Background(), token))

	// Canceled context -> GORM returns context.Canceled
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err = repo.RevokeToken(canceledCtx, tokenID)
	require.Error(t, err, "Pre-canceled context should fail DB operation")
	assert.True(t, strings.Contains(err.Error(), "context canceled"), "Error should indicate context cancellation")

	// Detached context via context.WithoutCancel -> succeeds
	detachedCtx := context.WithoutCancel(canceledCtx)
	err = repo.RevokeToken(detachedCtx, tokenID)
	require.NoError(t, err, "RevokeToken must succeed with context.WithoutCancel")

	stored, err := repo.GetTokenByID(context.Background(), tokenID)
	require.NoError(t, err)
	assert.True(t, stored.IsRevoked)
}

// TestAdversarial_RevokeToken_ConcurrentRevokeAndReadRace runs concurrent readers
// (GetTokenByID, GetTokenByValue) against concurrent RevokeToken calls under the Go race detector.
func TestAdversarial_RevokeToken_ConcurrentRevokeAndReadRace(t *testing.T) {
	db, err := setupRepoTestDB(t)
	require.NoError(t, err)

	repo := NewAuthTokenRepository(db)
	ctx := context.Background()

	tokenID := "tok-race-read-" + uuid.New().String()
	tokenValue := "jwt-val-" + tokenID
	token := &types.AuthToken{
		ID:        tokenID,
		UserID:    "user-read-race",
		Token:     tokenValue,
		TokenType: "access_token",
		IsRevoked: false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateToken(ctx, token))

	const readers = 25
	const revokers = 25
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	for i := 0; i < revokers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			_ = repo.RevokeToken(ctx, tokenID)
		}()
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			tok, err := repo.GetTokenByID(ctx, tokenID)
			if err == nil && tok != nil {
				// tok.IsRevoked is either true or false, never invalid memory
				_ = tok.IsRevoked
			}
			tokVal, err := repo.GetTokenByValue(ctx, tokenValue)
			if err == nil && tokVal != nil {
				_ = tokVal.IsRevoked
			}
		}()
	}

	close(startCh)
	wg.Wait()

	finalTok, err := repo.GetTokenByID(ctx, tokenID)
	require.NoError(t, err)
	assert.True(t, finalTok.IsRevoked)
}
