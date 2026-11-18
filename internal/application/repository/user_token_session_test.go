package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func sessionTestPair(userID, sessionID string) (*types.AuthToken, *types.AuthToken) {
	makeToken := func(kind string) *types.AuthToken {
		id := uuid.NewString()
		return &types.AuthToken{
			ID: id, UserID: userID, SessionID: sessionID, Token: id,
			TokenType: kind, ExpiresAt: time.Now().Add(time.Hour),
		}
	}
	return makeToken("access_token"), makeToken("refresh_token")
}

func TestAuthSessionLogoutSerializesWithConcurrentRotation(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "session-owner", false, true)
		repo := NewAuthTokenRepository(db)
		otherAccess, otherRefresh := sessionTestPair("session-owner", uuid.NewString())
		require.NoError(t, repo.CreateToken(ctx, otherAccess))
		require.NoError(t, repo.CreateToken(ctx, otherRefresh))

		// Each iteration races real, separate database transactions. PostgreSQL
		// uses multiple pooled connections; SQLite uses a file with a busy timeout.
		for range 20 {
			access, refresh := sessionTestPair("session-owner", uuid.NewString())
			require.NoError(t, repo.CreateToken(ctx, access))
			require.NoError(t, repo.CreateToken(ctx, refresh))
			start := make(chan struct{})
			var wg sync.WaitGroup
			var logoutErr error
			const contenders = 8
			rotationErrs := make([]error, contenders)
			wg.Add(contenders + 1)
			go func() {
				defer wg.Done()
				<-start
				logoutErr = repo.RevokeTokenSession(ctx, access)
			}()
			for i := range contenders {
				go func() {
					defer wg.Done()
					newAccess, newRefresh := sessionTestPair(access.UserID, access.SessionID)
					<-start
					rotationErrs[i] = repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh)
				}()
			}
			close(start)
			wg.Wait()
			require.NoError(t, logoutErr)
			successes := 0
			for _, err := range rotationErrs {
				if err == nil {
					successes++
				} else {
					require.ErrorContains(t, err, "revoked")
				}
			}
			require.LessOrEqual(t, successes, 1, "a refresh token may be consumed only once")
			var live int64
			require.NoError(t, db.Model(&types.AuthToken{}).
				Where("session_id = ? AND is_revoked = ?", access.SessionID, false).Count(&live).Error)
			require.Zero(t, live, "no rotation may survive completed logout")
			newAccess, newRefresh := sessionTestPair(access.UserID, access.SessionID)
			require.Error(t, repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh))
		}
		for _, token := range []*types.AuthToken{otherAccess, otherRefresh} {
			stored, err := repo.GetTokenByID(ctx, token.ID)
			require.NoError(t, err)
			require.False(t, stored.IsRevoked, "other login sessions must remain live")
		}
	})
}

func TestAuthSessionRotationRollsBackFailedPairAndRechecksSource(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "rollback-owner", false, true)
		repo := NewAuthTokenRepository(db)
		access, refresh := sessionTestPair("rollback-owner", uuid.NewString())
		require.NoError(t, repo.CreateToken(ctx, access))
		require.NoError(t, repo.CreateToken(ctx, refresh))
		newAccess, newRefresh := sessionTestPair(access.UserID, access.SessionID)
		newRefresh.ID = access.ID // second insert fails after consumption and first insert
		require.Error(t, repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh))
		stored, err := repo.GetTokenByID(ctx, refresh.ID)
		require.NoError(t, err)
		require.False(t, stored.IsRevoked, "transaction rollback must preserve a usable refresh token")
		_, err = repo.GetTokenByID(ctx, newAccess.ID)
		require.ErrorIs(t, err, ErrTokenNotFound, "failed rotation must leave no access token behind")

		// The caller holds a formerly valid copy; persisted expiry must win.
		require.NoError(t, db.Model(&types.AuthToken{}).Where("id = ?", access.ID).
			Update("expires_at", time.Now().Add(-time.Hour)).Error)
		require.ErrorContains(t, repo.RevokeTokenSession(ctx, access), "expired")
		stored, err = repo.GetTokenByID(ctx, refresh.ID)
		require.NoError(t, err)
		require.False(t, stored.IsRevoked)

		newAccess, newRefresh = sessionTestPair(access.UserID, access.SessionID)
		require.NoError(t, db.Model(&types.AuthToken{}).Where("id = ?", refresh.ID).
			Update("expires_at", time.Now().Add(-time.Hour)).Error)
		require.ErrorContains(t, repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh), "expired")
		_, err = repo.GetTokenByID(ctx, newAccess.ID)
		require.ErrorIs(t, err, ErrTokenNotFound)
	})
}

func TestAuthSessionRejectsMissingOrMismatchedSession(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "binding-owner", false, true)
		repo := NewAuthTokenRepository(db)
		access, refresh := sessionTestPair("binding-owner", uuid.NewString())
		require.NoError(t, repo.CreateToken(ctx, access))
		require.NoError(t, repo.CreateToken(ctx, refresh))
		newAccess, newRefresh := sessionTestPair(access.UserID, uuid.NewString())
		require.Error(t, repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh))
		invalid := *access
		invalid.SessionID = ""
		require.Error(t, repo.RevokeTokenSession(ctx, &invalid))
		invalid.SessionID = uuid.NewString()
		require.Error(t, repo.RevokeTokenSession(ctx, &invalid))
		invalid = *access
		invalid.TokenType = "refresh_token"
		require.Error(t, repo.RevokeTokenSession(ctx, &invalid))
		stored, err := repo.GetTokenByID(ctx, refresh.ID)
		require.NoError(t, err)
		require.False(t, stored.IsRevoked)
	})
}

func TestAuthSessionAllUserRevocationSerializesWithRotation(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "revoke-all-owner", false, true)
		repo := NewAuthTokenRepository(db)
		for range 20 {
			access, refresh := sessionTestPair("revoke-all-owner", uuid.NewString())
			require.NoError(t, repo.CreateToken(ctx, access))
			require.NoError(t, repo.CreateToken(ctx, refresh))
			newAccess, newRefresh := sessionTestPair(access.UserID, access.SessionID)
			start := make(chan struct{})
			var wg sync.WaitGroup
			var revokeErr, rotateErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				revokeErr = repo.RevokeTokensByUserID(ctx, access.UserID)
			}()
			go func() {
				defer wg.Done()
				<-start
				rotateErr = repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh)
			}()
			close(start)
			wg.Wait()
			require.NoError(t, revokeErr)
			if rotateErr != nil {
				require.ErrorContains(t, rotateErr, "revoked")
			}
			var live int64
			require.NoError(t, db.Model(&types.AuthToken{}).
				Where("user_id = ? AND is_revoked = ?", access.UserID, false).Count(&live).Error)
			require.Zero(t, live, "password/reset membership revocation must include racing rotations")
		}
	})
}

func TestAuthSessionLogoutRollsBackIfRefreshRevocationFails(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "revoke-rollback-owner", false, true)
		repo := NewAuthTokenRepository(db)
		access, refresh := sessionTestPair("revoke-rollback-owner", uuid.NewString())
		require.NoError(t, repo.CreateToken(ctx, access))
		require.NoError(t, repo.CreateToken(ctx, refresh))
		if db.Name() == "sqlite" {
			require.NoError(t, db.Exec(`CREATE TRIGGER reject_session_revoke BEFORE UPDATE OF is_revoked ON auth_tokens
				WHEN NEW.is_revoked = 1 AND OLD.token_type = 'refresh_token'
				BEGIN SELECT RAISE(ABORT, 'simulated session revoke failure'); END`).Error)
		} else {
			require.NoError(t, db.Exec(`CREATE FUNCTION reject_session_revoke() RETURNS trigger AS $$
				BEGIN
					IF NEW.is_revoked AND OLD.token_type = 'refresh_token' THEN
						RAISE EXCEPTION 'simulated session revoke failure';
					END IF;
					RETURN NEW;
				END; $$ LANGUAGE plpgsql`).Error)
			require.NoError(t, db.Exec(`CREATE TRIGGER reject_session_revoke BEFORE UPDATE OF is_revoked ON auth_tokens
				FOR EACH ROW EXECUTE FUNCTION reject_session_revoke()`).Error)
		}
		require.ErrorContains(t, repo.RevokeTokenSession(ctx, access), "simulated session revoke failure")
		for _, token := range []*types.AuthToken{access, refresh} {
			stored, err := repo.GetTokenByID(ctx, token.ID)
			require.NoError(t, err)
			require.False(t, stored.IsRevoked, "failed logout must roll back all partial session writes")
		}
	})
}

func TestAuthSessionAllUserRevocationAllowsDisabledUser(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "disabled-session-owner", false, false)
		repo := NewAuthTokenRepository(db)
		access, refresh := sessionTestPair("disabled-session-owner", uuid.NewString())
		require.NoError(t, repo.CreateToken(ctx, access))
		require.NoError(t, repo.CreateToken(ctx, refresh))
		newAccess, newRefresh := sessionTestPair(access.UserID, access.SessionID)
		require.ErrorContains(t, repo.RotateRefreshToken(ctx, refresh, newAccess, newRefresh), "disabled")
		require.NoError(t, repo.RevokeTokensByUserID(ctx, access.UserID))
		for _, token := range []*types.AuthToken{access, refresh} {
			stored, err := repo.GetTokenByID(ctx, token.ID)
			require.NoError(t, err)
			require.True(t, stored.IsRevoked)
		}
	})
}

func TestAuthSessionLogoutSerializesWithConcurrentSessionAccessIssue(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		seedStatusUser(t, db, "switch-session-owner", false, true)
		repo := NewAuthTokenRepository(db).(*authTokenRepository)
		for range 20 {
			access, refresh := sessionTestPair("switch-session-owner", uuid.NewString())
			require.NoError(t, repo.CreateToken(ctx, access))
			require.NoError(t, repo.CreateToken(ctx, refresh))

			start := make(chan struct{})
			var wg sync.WaitGroup
			var logoutErr error
			const contenders = 6
			issueErrs := make([]error, contenders)
			wg.Add(contenders + 1)
			go func() {
				defer wg.Done()
				<-start
				logoutErr = repo.RevokeTokenSession(ctx, access)
			}()
			for i := range contenders {
				go func() {
					defer wg.Done()
					replacement, _ := sessionTestPair(access.UserID, access.SessionID)
					replacement.ExpiresAt = access.ExpiresAt
					<-start
					issueErrs[i] = repo.IssueSessionAccessToken(ctx, access, replacement)
				}()
			}
			close(start)
			wg.Wait()
			require.NoError(t, logoutErr)
			for _, err := range issueErrs {
				if err != nil {
					require.ErrorContains(t, err, "revoked")
				}
			}
			var live int64
			require.NoError(t, db.Model(&types.AuthToken{}).
				Where("session_id = ? AND is_revoked = ?", access.SessionID, false).Count(&live).Error)
			require.Zero(t, live, "no session-scoped access token may survive completed logout")
		}
	})
}
