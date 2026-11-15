package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrCannotDisableSelf prevents administrators from disabling their own account.
	ErrCannotDisableSelf = errors.New("cannot disable your own account")
	// ErrCannotDisableLastAdmin preserves at least one active system administrator.
	ErrCannotDisableLastAdmin = errors.New("cannot disable the last system administrator")
	// ErrUserNotFound indicates that the requested user does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrUserAlreadyExists indicates a duplicate user account.
	ErrUserAlreadyExists = errors.New("user already exists")
	// ErrTokenNotFound indicates that the requested token does not exist.
	ErrTokenNotFound = errors.New("token not found")
	// ErrCannotRevokeSelf prevents administrators from revoking their own role.
	ErrCannotRevokeSelf = errors.New("cannot revoke your own system admin privileges")
	// ErrLastSystemAdmin prevents revoking the last active system administrator's role.
	ErrLastSystemAdmin = errors.New("cannot revoke the last remaining system administrator")
	// ErrUserNotSystemAdmin indicates that the user has no system-admin role to revoke.
	ErrUserNotSystemAdmin = errors.New("user is not a system administrator")
)

// userRepository implements user repository interface
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new user repository
func NewUserRepository(db *gorm.DB) interfaces.UserRepository {
	return &userRepository{db: db}
}

// CreateUser creates a user
func (r *userRepository) CreateUser(ctx context.Context, user *types.User) error {
	// users.tenant_id is nullable in both PostgreSQL and SQLite. GORM would
	// otherwise serialise the uint64 zero value as 0, which violates the
	// PostgreSQL FK and loses the distinction between "not provisioned yet"
	// and a real tenant. Omitting the column stores SQL NULL; reads hydrate it
	// back as zero, the domain sentinel used by tenantless auth flows.
	if user != nil && user.TenantID == 0 {
		return r.db.WithContext(ctx).Omit("tenant_id").Create(user).Error
	}
	return r.db.WithContext(ctx).Create(user).Error
}

// GetUserByID gets a user by ID
func (r *userRepository) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUsersByIDs batch-fetches users by id with a single SELECT … WHERE id IN (…)
// and projects the result into a map keyed by user id. Returns an empty
// map for an empty input slice. Missing ids are silently absent from
// the result (consistent with the interface contract used by tenant
// member hydration).
func (r *userRepository) GetUsersByIDs(ctx context.Context, ids []string) (map[string]*types.User, error) {
	out := make(map[string]*types.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var users []*types.User
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

// GetUserByEmail gets a user by email
func (r *userRepository) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername gets a user by username
func (r *userRepository) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByTenantID gets the first user (owner) of a tenant
func (r *userRepository) GetUserByTenantID(ctx context.Context, tenantID uint64) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// UpdateUser updates user data while preserving account status. Status changes
// must go through SetUserActive so stale profile/password writes cannot undo
// an administrator's enable/disable action.
func (r *userRepository) UpdateUser(ctx context.Context, user *types.User) error {
	if user != nil && user.TenantID == 0 {
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Keep status and the nullable tenant column out of the struct
			// write, then explicitly store NULL for the tenant.
			// Writing uint64(0) would violate the PostgreSQL tenant FK.
			if err := tx.Omit("tenant_id", "is_active").Save(user).Error; err != nil {
				return err
			}
			return tx.Model(&types.User{}).
				Where("id = ?", user.ID).
				UpdateColumn("tenant_id", nil).Error
		})
	}
	return r.db.WithContext(ctx).Omit("is_active").Save(user).Error
}

// DeleteUser deletes a user
func (r *userRepository) DeleteUser(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.User{}).Error
}

// DeleteUserRetiringSessions locks the web user, retires every session visible
// under that lock, and soft-deletes the user in the same transaction. A user
// row that is already soft-deleted is still held for the re-read; its sessions
// are retired and the call returns nil.
func (r *userRepository) DeleteUserRetiringSessions(ctx context.Context, id string, at time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := lockLiveWebUser(tx, id)
		if err != nil && !errors.Is(err, errWebUserDeleted) {
			return err
		}
		ids, err := ownerSessionIDs(tx, id)
		if err != nil {
			return err
		}
		if _, err := RetireSessionsTx(tx, ids, at); err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&types.User{}).Error
	})
}

// ListUsers lists users with pagination
func (r *userRepository) ListUsers(ctx context.Context, offset, limit int) ([]*types.User, error) {
	var users []*types.User
	query := r.db.WithContext(ctx).Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	if offset > 0 {
		query = query.Offset(offset)
	}

	if err := query.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// ListSystemAdmins lists users where is_system_admin = true.
//
// Walks idx_users_is_system_admin (created in migration 000052), so the
// query stays cheap even on a large users table — only the small subset
// of system admins is scanned. Returns total count alongside the page so
// the management UI can render pagination without a second roundtrip.
//
// Ordered by created_at DESC for stable, newest-first listing; ties are
// further broken by id to keep paging deterministic across boundaries.
// limit <= 0 means "no limit" (matches ListUsers semantics); callers in
// production pass a sane page size.
func (r *userRepository) ListSystemAdmins(ctx context.Context, offset, limit int) ([]*types.User, int64, error) {
	var users []*types.User
	var total int64

	base := r.db.WithContext(ctx).Model(&types.User{}).Where("is_system_admin = ?", true)
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := base.Order("created_at DESC, id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// lockSystemAdmins uses the same lock order for status changes and role
// revocation: all admin rows by ID, then the target user. SQLite has no row
// locks, so acquire its writer lock before reading the administrator set.
func lockSystemAdmins(tx *gorm.DB) ([]types.User, error) {
	if tx.Name() == "sqlite" {
		if err := tx.Model(&types.User{}).Where("is_system_admin = ?", true).
			UpdateColumn("is_system_admin", true).Error; err != nil {
			return nil, err
		}
	}
	var admins []types.User
	err := lockUserRows(tx).Where("is_system_admin = ?", true).Order("id ASC").Find(&admins).Error
	return admins, err
}

func lockUserRows(tx *gorm.DB) *gorm.DB {
	switch tx.Name() {
	case "postgres", "mysql":
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return tx
	}
}

func activeSystemAdminCount(admins []types.User) int {
	n := 0
	for _, admin := range admins {
		if admin.IsActive {
			n++
		}
	}
	return n
}

// SetUserActive persists status and session revocation in one transaction.
// Locking the admin set also serializes this operation with role revocation.
func (r *userRepository) SetUserActive(ctx context.Context, userID, actorID string, active bool) (*types.User, error) {
	var user types.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		admins, err := lockSystemAdmins(tx)
		if err != nil {
			return err
		}
		if err := lockUserRows(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}
		if user.IsActive == active {
			return nil
		}
		if !active {
			if userID == actorID {
				return ErrCannotDisableSelf
			}
			if user.IsSystemAdmin && activeSystemAdminCount(admins) <= 1 {
				return ErrCannotDisableLastAdmin
			}
		}
		user.IsActive = active
		user.UpdatedAt = time.Now()
		if err := tx.Model(&user).Updates(map[string]any{
			"is_active": active, "updated_at": user.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		if !active {
			return tx.Model(&types.AuthToken{}).Where("user_id = ?", userID).
				Update("is_revoked", true).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// RevokeSystemAdmin revokes system-admin privileges inside a transaction.
// It locks the current admin rows before counting so concurrent revokes
// cannot both observe "two admins" and leave the platform with zero.
//
// Return contract:
//   - (user, nil): revoke actually happened; user.IsSystemAdmin == false
//   - (user, ErrUserNotSystemAdmin): target was already not an admin;
//     no row was written. Caller should treat as idempotent success but
//     MUST distinguish it from a real revoke for audit purposes — the
//     surfaced `user` is the unchanged DB row.
//   - (nil, ErrCannotRevokeSelf | ErrLastSystemAdmin | ErrUserNotFound | …):
//     hard rejection; no row written.
func (r *userRepository) RevokeSystemAdmin(ctx context.Context, userID, actorID string) (*types.User, error) {
	if userID == actorID {
		return nil, ErrCannotRevokeSelf
	}

	var revoked *types.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		admins, err := lockSystemAdmins(tx)
		if err != nil {
			return err
		}
		var user types.User
		if err := lockUserRows(tx).
			Where("id = ?", userID).
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}
		if !user.IsSystemAdmin {
			revoked = &user
			return ErrUserNotSystemAdmin
		}

		if len(admins) <= 1 || (user.IsActive && activeSystemAdminCount(admins) <= 1) {
			return ErrLastSystemAdmin
		}

		user.IsSystemAdmin = false
		if err := tx.Model(&user).Update("is_system_admin", false).Error; err != nil {
			return err
		}
		revoked = &user
		return nil
	})
	// Propagate ErrUserNotSystemAdmin up to the handler alongside the
	// (unchanged) user row. The handler treats it as idempotent success
	// but emits an audit row with changed=false so a probing pattern
	// ("revoke every random user id we know") still leaves a trail.
	if errors.Is(err, ErrUserNotSystemAdmin) {
		return revoked, err
	}
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

// SearchUsers searches users by username or email
func (r *userRepository) SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error) {
	var users []*types.User
	// Dialect-aware bits so the same query works on Postgres and SQLite (Lite
	// build), which has no ILIKE. The keyword is escaped either way: % and _
	// are LIKE wildcards, so an unescaped search for "a_b" also matches "axb"
	// and "%" returns every user.
	isPostgres := r.db.Name() == "postgres"
	userLike := "LOWER(username) LIKE LOWER(?) ESCAPE ?"
	mailLike := "LOWER(email) LIKE LOWER(?) ESCAPE ?"
	if isPostgres {
		userLike = "username ILIKE ? ESCAPE ?"
		mailLike = "email ILIKE ? ESCAPE ?"
	}
	escaped := escapeLikeKeyword(query)
	searchPattern := "%" + escaped + "%"

	dbQuery := r.db.WithContext(ctx).
		Where(userLike+" OR "+mailLike, searchPattern, likeEscapeChar, searchPattern, likeEscapeChar).
		Where("is_active = ?", true).
		Order("username ASC")

	if limit > 0 {
		dbQuery = dbQuery.Limit(limit)
	} else {
		dbQuery = dbQuery.Limit(20) // default limit
	}

	if err := dbQuery.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// authTokenRepository implements auth token repository interface
type authTokenRepository struct {
	db *gorm.DB
}

// NewAuthTokenRepository creates a new auth token repository
func NewAuthTokenRepository(db *gorm.DB) interfaces.AuthTokenRepository {
	return &authTokenRepository{db: db}
}

// CreateToken creates an auth token
func (r *authTokenRepository) CreateToken(ctx context.Context, token *types.AuthToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

// GetTokenByValue gets a token by its value
func (r *authTokenRepository) GetTokenByValue(ctx context.Context, tokenValue string) (*types.AuthToken, error) {
	var token types.AuthToken
	if err := r.db.WithContext(ctx).Where("token = ?", tokenValue).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	return &token, nil
}

// GetTokenByID gets a token by its primary key
func (r *authTokenRepository) GetTokenByID(ctx context.Context, id string) (*types.AuthToken, error) {
	var token types.AuthToken
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	return &token, nil
}

// GetTokensByUserID gets all tokens for a user
func (r *authTokenRepository) GetTokensByUserID(ctx context.Context, userID string) ([]*types.AuthToken, error) {
	var tokens []*types.AuthToken
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&tokens).Error; err != nil {
		return nil, err
	}
	return tokens, nil
}

// UpdateToken updates a token. When revoking a token, it enforces Compare-And-Swap (CAS)
// semantics (WHERE id = ? AND is_revoked = false) to prevent race conditions and token reuse.
func (r *authTokenRepository) UpdateToken(ctx context.Context, token *types.AuthToken) error {
	if token.IsRevoked {
		updatedAt := token.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = time.Now()
			token.UpdatedAt = updatedAt
		}
		result := r.db.WithContext(ctx).Model(&types.AuthToken{}).
			Where("id = ? AND is_revoked = ?", token.ID, false).
			Updates(map[string]interface{}{
				"is_revoked": true,
				"updated_at": updatedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("token already revoked or not found")
		}
		return nil
	}
	return r.db.WithContext(ctx).Save(token).Error
}

// DeleteToken deletes a token
func (r *authTokenRepository) DeleteToken(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.AuthToken{}).Error
}

// DeleteExpiredTokens deletes all expired tokens
func (r *authTokenRepository) DeleteExpiredTokens(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("expires_at < ?", time.Now()).Delete(&types.AuthToken{}).Error
}

// RevokeToken revokes a single token by its primary key
func (r *authTokenRepository) RevokeToken(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Model(&types.AuthToken{}).
		Where("id = ? AND is_revoked = ?", id, false).
		Updates(map[string]interface{}{
			"is_revoked": true,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// RevokeTokensByUserID revokes all tokens for a user
func (r *authTokenRepository) RevokeTokensByUserID(ctx context.Context, userID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Share the rotation/logout lock, including for disabled users. Without
		// it an UPDATE snapshot can miss a concurrent rotation's new tokens.
		if err := tx.Model(&types.User{}).Where("id = ?", userID).
			UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		return tx.Model(&types.AuthToken{}).Where("user_id = ?", userID).
			Updates(map[string]interface{}{"is_revoked": true, "updated_at": time.Now()}).Error
	})
}

// lockAuthUser serializes session rotation and logout before either reads or
// changes token state. A no-op write takes a row lock on PostgreSQL and a write
// lock on SQLite; unlike an in-process mutex it also protects multiple servers.
// Taking this lock first avoids a read-to-write lock upgrade on SQLite.
func lockAuthUser(tx *gorm.DB, userID string) error {
	result := tx.Model(&types.User{}).
		Where("id = ? AND is_active = ?", userID, true).
		UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("token user is missing or disabled")
	}
	return nil
}

// consumeLiveToken rechecks persisted identity, expiry and revocation under the
// user lock. A service-layer validation alone can become stale before commit.
func consumeLiveToken(tx *gorm.DB, token *types.AuthToken, tokenType string) error {
	result := tx.Model(&types.AuthToken{}).
		Where("id = ? AND user_id = ? AND session_id = ? AND token = ? AND token_type = ? AND is_revoked = ? AND expires_at > ?",
			token.ID, token.UserID, token.SessionID, token.Token, tokenType, false, time.Now()).
		Updates(map[string]interface{}{"is_revoked": true, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("token is revoked, expired or not found")
	}
	return nil
}

func validSessionToken(token *types.AuthToken, tokenType string) bool {
	return token != nil && token.ID != "" && token.UserID != "" && token.SessionID != "" &&
		token.Token != "" && token.TokenType == tokenType && !token.IsRevoked && token.ExpiresAt.After(time.Now())
}

// RotateRefreshToken commits consumption and the replacement pair together.
// Logout takes the same lock, so it either revokes the newly issued pair or
// wins first and causes this transaction to reject its revoked source token.
func (r *authTokenRepository) RotateRefreshToken(ctx context.Context, previous, access, refresh *types.AuthToken) error {
	if !validSessionToken(previous, "refresh_token") ||
		!validSessionToken(access, "access_token") || !validSessionToken(refresh, "refresh_token") ||
		previous.UserID != access.UserID || previous.UserID != refresh.UserID ||
		previous.SessionID != access.SessionID || previous.SessionID != refresh.SessionID ||
		previous.ID == access.ID || previous.ID == refresh.ID || access.ID == refresh.ID {
		return errors.New("invalid refresh token session")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthUser(tx, previous.UserID); err != nil {
			return err
		}
		if err := consumeLiveToken(tx, previous, "refresh_token"); err != nil {
			return err
		}
		if err := tx.Omit(clause.Associations).Create(access).Error; err != nil {
			return err
		}
		return tx.Omit(clause.Associations).Create(refresh).Error
	})
}

// RevokeTokenSession checks the presented access token again in the same
// transaction that revokes every access/refresh token in its login session.
func (r *authTokenRepository) RevokeTokenSession(ctx context.Context, access *types.AuthToken) error {
	if !validSessionToken(access, "access_token") {
		return errors.New("invalid access token session")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthUser(tx, access.UserID); err != nil {
			return err
		}
		if err := consumeLiveToken(tx, access, "access_token"); err != nil {
			return err
		}
		return tx.Model(&types.AuthToken{}).
			Where("user_id = ? AND session_id = ? AND is_revoked = ?", access.UserID, access.SessionID, false).
			Updates(map[string]interface{}{"is_revoked": true, "updated_at": time.Now()}).Error
	})
}

// IssueSessionAccessToken mints a tenant-scoped access token inside an existing
// login session without issuing a refresh token or starting a new session.
// Under lockAuthUser it re-verifies that currentAccess is still live so a
// concurrent Logout or SetUserActive(false) cannot leave a usable token behind.
func (r *authTokenRepository) IssueSessionAccessToken(ctx context.Context, currentAccess, replacementAccess *types.AuthToken) error {
	if !validSessionToken(currentAccess, "access_token") ||
		!validSessionToken(replacementAccess, "access_token") ||
		currentAccess.UserID != replacementAccess.UserID ||
		currentAccess.SessionID != replacementAccess.SessionID ||
		currentAccess.ID == replacementAccess.ID ||
		replacementAccess.ExpiresAt.After(currentAccess.ExpiresAt) {
		return errors.New("invalid access token session")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthUser(tx, currentAccess.UserID); err != nil {
			return err
		}
		var liveCount int64
		if err := tx.Model(&types.AuthToken{}).
			Where("id = ? AND user_id = ? AND session_id = ? AND token = ? AND token_type = ? AND is_revoked = ? AND expires_at > ?",
				currentAccess.ID, currentAccess.UserID, currentAccess.SessionID, currentAccess.Token, "access_token", false, time.Now()).
			Count(&liveCount).Error; err != nil {
			return err
		}
		if liveCount != 1 {
			return errors.New("token is revoked, expired or not found")
		}
		return tx.Omit(clause.Associations).Create(replacementAccess).Error
	})
}
