package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
)

// setupTestSQLiteDB initializes an isolated in-memory SQLite database for concurrency stress testing.
func setupTestSQLiteDB(t *testing.T) (*gorm.DB, error) {
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
	err = db.AutoMigrate(&types.AuthToken{}, &types.User{})
	if err != nil {
		return nil, err
	}
	return db, nil
}


// TestBE03_RepositoryCASConcurrencyRaceStress fires 50 concurrent goroutines
// attempting to revoke the same unrevoked token simultaneously via UpdateToken.
// Exactly ONE must succeed and 49 must fail with CAS error or lock rejection.
func TestBE03_RepositoryCASConcurrencyRaceStress(t *testing.T) {
	db, err := setupTestSQLiteDB(t)
	require.NoError(t, err)

	tokenRepo := repository.NewAuthTokenRepository(db)
	ctx := context.Background()

	tokenID := uuid.New().String()
	tokenValue := "refresh_token_test_cas_" + tokenID
	initialToken := &types.AuthToken{
		ID:        tokenID,
		UserID:    "user-be03-test",
		Token:     tokenValue,
		TokenType: "refresh_token",
		IsRevoked: false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err = tokenRepo.CreateToken(ctx, initialToken)
	require.NoError(t, err)

	const concurrency = 50
	var successCount int64
	var failureCount int64
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh // Align all goroutines at starting line for maximum concurrency contention

			tok := &types.AuthToken{
				ID:        tokenID,
				IsRevoked: true,
				UpdatedAt: time.Now(),
			}
			err := tokenRepo.UpdateToken(ctx, tok)
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failureCount, 1)
				// Rejection must be either CAS condition violation or SQLite database lock
				isExpectedError := strings.Contains(err.Error(), "already revoked or not found") ||
					strings.Contains(err.Error(), "database table is locked") ||
					strings.Contains(err.Error(), "busy")
				assert.True(t, isExpectedError, "Unexpected error: %v", err)
			}
		}()
	}


	// Release all 50 goroutines simultaneously
	close(startCh)
	wg.Wait()

	assert.Equal(t, int64(1), successCount, "Exactly ONE concurrent revocation must succeed under CAS")
	assert.Equal(t, int64(concurrency-1), failureCount, "All other concurrent revocations must fail with CAS violation")

	// Verify database state: token is revoked
	persisted, err := tokenRepo.GetTokenByID(ctx, tokenID)
	require.NoError(t, err)
	assert.True(t, persisted.IsRevoked, "Database row must be revoked")

	// Replay simulation: a subsequent revocation attempt must strictly fail
	replayErr := tokenRepo.UpdateToken(ctx, &types.AuthToken{
		ID:        tokenID,
		IsRevoked: true,
		UpdatedAt: time.Now(),
	})
	require.Error(t, replayErr)
	assert.Contains(t, replayErr.Error(), "already revoked or not found")
}

// TestBE03_ServiceRefreshTokenConcurrencyRaceAndReplay simulates real-world concurrent refresh
// requests by an attacker or race condition (e.g. 50 parallel RefreshToken calls).
// CAS must ensure exactly 1 rotation succeeds, zero double-issuance, and replay rejection.
func TestBE03_ServiceRefreshTokenConcurrencyRaceAndReplay(t *testing.T) {
	db, err := setupTestSQLiteDB(t)
	require.NoError(t, err)

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewAuthTokenRepository(db)
	ctx := context.Background()

	testUser := &types.User{
		ID:           "user-be03-race",
		Username:     "be03_tester",
		Email:        "be03@example.com",
		TenantID:     1,
		IsActive:     true,
		PasswordHash: "dummyhash",
	}
	err = userRepo.CreateUser(ctx, testUser)
	require.NoError(t, err)

	svc := &userService{
		userRepo:  userRepo,
		tokenRepo: tokenRepo,
	}

	// Sign a valid refresh token JWT
	refreshTokenJWT := signTestJWT(jwt.MapClaims{
		"user_id": testUser.ID,
		"type":    "refresh",
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
	})

	tokenID := uuid.New().String()
	tokenRecord := &types.AuthToken{
		ID:        tokenID,
		SessionID: uuid.NewString(),
		UserID:    testUser.ID,
		Token:     refreshTokenJWT,
		TokenType: "refresh_token",
		IsRevoked: false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err = tokenRepo.CreateToken(ctx, tokenRecord)
	require.NoError(t, err)

	const concurrency = 50
	var successCount int64
	var failureCount int64
	var issuedAccessTokens sync.Map
	var issuedRefreshTokens sync.Map
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh // Align all requests at the exact same microsecond

			newAccess, newRefresh, err := svc.RefreshToken(ctx, refreshTokenJWT)
			if err == nil {
				atomic.AddInt64(&successCount, 1)
				issuedAccessTokens.Store(newAccess, true)
				issuedRefreshTokens.Store(newRefresh, true)
			} else {
				atomic.AddInt64(&failureCount, 1)
			}
		}()
	}

	close(startCh)
	wg.Wait()

	// 1. Concurrency safety: EXACTLY 1 request succeeded
	assert.Equal(t, int64(1), successCount, "Exactly ONE RefreshToken request must succeed")
	assert.Equal(t, int64(concurrency-1), failureCount, "All other 49 concurrent RefreshToken requests must fail")

	// 2. Exactly 1 new token pair issued (no duplicate tokens generated or persisted)
	var accessCount, refreshCount int
	issuedAccessTokens.Range(func(_, _ interface{}) bool { accessCount++; return true })
	issuedRefreshTokens.Range(func(_, _ interface{}) bool { refreshCount++; return true })
	assert.Equal(t, 1, accessCount, "Exactly one new access token should be created")
	assert.Equal(t, 1, refreshCount, "Exactly one new refresh token should be created")

	// 3. Old refresh token is marked revoked in DB
	persistedRecord, err := tokenRepo.GetTokenByID(ctx, tokenID)
	require.NoError(t, err)
	assert.True(t, persistedRecord.IsRevoked, "Original refresh token must be revoked")

	// 4. Replay Attack Simulation: Sequential replay of the old refresh token MUST fail
	_, _, replayErr := svc.RefreshToken(ctx, refreshTokenJWT)
	require.Error(t, replayErr, "Replaying the old refresh token must be rejected")
	assert.Contains(t, replayErr.Error(), "refresh token is revoked")

	// 5. Subsequent valid rotation: The newly issued refresh token must work cleanly for the next rotation
	var validNewRefreshToken string
	issuedRefreshTokens.Range(func(key, _ interface{}) bool {
		validNewRefreshToken = key.(string)
		return false
	})
	nextAccess, nextRefresh, nextErr := svc.RefreshToken(ctx, validNewRefreshToken)
	require.NoError(t, nextErr, "Next rotation using newly issued refresh token must succeed")
	assert.NotEmpty(t, nextAccess)
	assert.NotEmpty(t, nextRefresh)
}
