package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/application/service"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
)

// PromoteUserToSystemAdminRequest defines the request for promoting a user to system admin.
type PromoteUserToSystemAdminRequest struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// PromoteUserToSystemAdmin godoc
// @Summary      Promote a user to system administrator
// @Description  Grant system administrator privileges to a user (SystemAdmin only).
// @Description  Idempotent: re-promoting an existing system admin returns 200 with no DB write.
// @Description  Identify the user by email (preferred for human operators) or user_id (UUID, for API clients).
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body PromoteUserToSystemAdminRequest true "User promotion request"
// @Success      200  {object}  types.UserInfo  "User promoted successfully"
// @Failure      400  {object}  apperrors.AppError  "Bad request"
// @Failure      403  {object}  apperrors.AppError  "Forbidden: not a system admin"
// @Failure      404  {object}  apperrors.AppError  "User not found"
// @Router       /system/admin/promote [post]
func (h *SystemHandler) PromoteUserToSystemAdmin(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req PromoteUserToSystemAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid request: "+err.Error()))
		return
	}

	userID := strings.TrimSpace(req.UserID)
	email := strings.TrimSpace(req.Email)
	if userID == "" && email == "" {
		writeAppError(c, apperrors.NewBadRequestError("Either user_id or email is required"))
		return
	}

	var (
		user *types.User
		err  error
	)
	switch {
	case userID != "":
		user, err = h.userSvc.GetUserByID(ctx, userID)
	default:
		user, err = h.userSvc.GetUserByEmail(ctx, email)
	}
	if err != nil {
		logger.Errorf(ctx, "Error fetching user (id=%q email=%q): %v", userID, email, err)
		writeAppError(c, apperrors.NewNotFoundError("User not found"))
		return
	}
	if user == nil {
		writeAppError(c, apperrors.NewNotFoundError("User not found"))
		return
	}

	if user.IsSystemAdmin {
		h.emitAdminAudit(ctx, types.AuditActionSystemAdminPromoted, user, map[string]any{
			"target_email":    user.Email,
			"target_username": user.Username,
			"idempotent":      true,
		})
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	}
	user.IsSystemAdmin = true
	if err := h.userSvc.UpdateUser(ctx, user); err != nil {
		logger.Errorf(ctx, "Error promoting user %s to system admin: %v", req.UserID, err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to promote user"))
		return
	}

	logger.Infof(ctx, "User %s (ID: %s) promoted to system admin", user.Username, user.ID)
	h.emitAdminAudit(ctx, types.AuditActionSystemAdminPromoted, user, map[string]any{
		"target_email":    user.Email,
		"target_username": user.Username,
		"idempotent":      false,
	})
	c.JSON(http.StatusOK, user.ToUserInfo())
}

// RevokeSystemAdminRequest defines the request for revoking system admin privileges
type RevokeSystemAdminRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

// RevokeSystemAdmin godoc
// @Summary      Revoke system administrator privileges from a user
// @Description  Remove system administrator privileges from a user (SystemAdmin only).
// @Description  Two safety guards: the caller cannot revoke their own privileges,
// @Description  and revoking the last remaining system admin is rejected.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body RevokeSystemAdminRequest true "User revocation request"
// @Success      200  {object}  types.UserInfo  "Privileges revoked successfully"
// @Failure      400  {object}  apperrors.AppError  "Bad request / would remove last admin / self-revoke"
// @Failure      403  {object}  apperrors.AppError  "Forbidden: not a system admin"
// @Failure      404  {object}  apperrors.AppError  "User not found"
// @Router       /system/admin/revoke [post]
func (h *SystemHandler) RevokeSystemAdmin(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req RevokeSystemAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid request: "+err.Error()))
		return
	}

	callerID, _ := types.UserIDFromContext(ctx)
	user, err := h.userSvc.RevokeSystemAdmin(ctx, req.UserID, callerID)
	switch {
	case err == nil:
		logger.Infof(ctx, "System admin privileges revoked from user %s (ID: %s)", user.Username, user.ID)
		h.emitAdminAudit(ctx, types.AuditActionSystemAdminRevoked, user, map[string]any{
			"target_email":    user.Email,
			"target_username": user.Username,
			"changed":         true,
		})
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	case errors.Is(err, types.ErrUserNotSystemAdmin):
		if user == nil {
			logger.Errorf(
				ctx,
				"RevokeSystemAdmin returned nil user with ErrUserNotSystemAdmin for %s",
				req.UserID,
			)
			writeAppError(c, apperrors.NewInternalServerError("Failed to revoke system admin privileges"))
			return
		}
		logger.Infof(ctx, "Revoke noop (user %s was not a system admin)", user.ID)
		h.emitAdminAudit(ctx, types.AuditActionSystemAdminRevoked, user, map[string]any{
			"target_email":    user.Email,
			"target_username": user.Username,
			"changed":         false,
		})
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	case errors.Is(err, types.ErrCannotRevokeSelf):
		writeAppError(c, apperrors.NewBadRequestError("Cannot revoke your own system admin privileges"))
		return
	case errors.Is(err, types.ErrLastSystemAdmin):
		writeAppError(c, apperrors.NewBadRequestError("Cannot revoke the last remaining system administrator"))
		return
	case errors.Is(err, types.ErrUserNotFound):
		writeAppError(c, apperrors.NewNotFoundError("User not found"))
		return
	default:
		logger.Errorf(ctx, "Error revoking system admin from user %s: %v", req.UserID, err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to revoke system admin privileges"))
		return
	}
}

// ListSystemAdminsResponse defines the response structure for listing system admins.
type ListSystemAdminsResponse struct {
	Total  int64             `json:"total" extensions:"x-agent"`
	Admins []*types.UserInfo `json:"admins"`
}

// ListSystemAdmins godoc
// @Summary      查询系统管理员（List system administrators）
// @Description  Retrieve a paginated list of users with system administrator privileges (SystemAdmin only).
// @Tags         System Admin
// @Produce      json
// @Param        offset query int false "Page offset" default(0) minimum(0) extensions(x-agent,x-agent-maximum=1000000)
// @Param        limit  query int false "Page size (max 200)" default(50) minimum(1) maximum(200) extensions(x-agent,x-agent-maximum=50)
// @Success      200  {object}  ListSystemAdminsResponse  "System admins retrieved successfully"
// @Failure      403  {object}  apperrors.AppError  "Forbidden: not a system admin"
// @x-agent {"id":"system_admin.list_admins","risk":"read","system_admin":true,"root":"admins","max_items":50}
// @x-agent-hint "Lists platform system administrators across workspaces. Paginate with offset/limit until total is covered. These privileges differ from workspace roles."
// @Router       /system/admin/list [get]
func (h *SystemHandler) ListSystemAdmins(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	offset := 0
	limit := 50
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	users, total, err := h.userSvc.ListSystemAdmins(ctx, offset, limit)
	if err != nil {
		logger.Errorf(ctx, "Error listing system admins: %v", err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to list system admins"))
		return
	}

	infos := make([]*types.UserInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, u.ToUserInfo())
	}

	c.JSON(http.StatusOK, ListSystemAdminsResponse{
		Total:  total,
		Admins: infos,
	})
}

// ListAllUsersResponse is the paginated payload for the platform user list.
type ListAllUsersResponse struct {
	Users   []*types.UserInfo `json:"users"`
	Offset  int               `json:"offset" extensions:"x-agent"`
	Limit   int               `json:"limit" extensions:"x-agent"`
	HasMore bool              `json:"has_more" extensions:"x-agent"`
}

// ListUsers godoc
// @Summary      查询平台用户（List platform users）
// @Description  Retrieve a paginated list of every user across tenants (SystemAdmin only).
// @Tags         System Admin
// @Produce      json
// @Param        offset query int false "Page offset" default(0) minimum(0) extensions(x-agent,x-agent-maximum=1000000)
// @Param        limit  query int false "Page size (max 200)" default(50) minimum(1) maximum(200) extensions(x-agent,x-agent-maximum=50)
// @Success      200  {object}  ListAllUsersResponse  "Users retrieved successfully"
// @Failure      403  {object}  apperrors.AppError  "Forbidden: not a system admin"
// @x-agent {"id":"system_admin.list_users","risk":"read","system_admin":true,"root":"users","max_items":50}
// @x-agent-hint "Lists platform users across workspaces, including account enabled state and system administrator flag. Follow has_more using offset + limit. tenant_id is the home workspace, not a list of memberships. No search or role filter is supported: paginate before drawing conclusions."
// @Router       /system/admin/users [get]
func (h *SystemHandler) ListUsers(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	offset := 0
	limit := 50
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	users, hasMore, err := h.userSvc.ListAllUsers(ctx, offset, limit)
	if err != nil {
		logger.Errorf(ctx, "Error listing users: %v", err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to list users"))
		return
	}

	infos := make([]*types.UserInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, u.ToUserInfo())
	}

	c.JSON(http.StatusOK, ListAllUsersResponse{
		Users:   infos,
		Offset:  offset,
		Limit:   limit,
		HasMore: hasMore,
	})
}

// SetUserStatusRequest toggles a user account enabled/disabled (SystemAdmin only).
type SetUserStatusRequest struct {
	UserID   string `json:"user_id" binding:"required"`
	IsActive *bool  `json:"is_active" binding:"required"`
}

// SetUserStatus godoc
// @Summary      Enable or disable a user account
// @Description  Disable revokes the target's active sessions immediately.
// @Description  Self-disable and disabling the last active system administrator are rejected.
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body SetUserStatusRequest true "Account status request"
// @Success      200  {object}  handler.SetUserStatusResponse  "Status updated"
// @Failure      400  {object}  apperrors.AppError  "Self-disable / last-admin guard"
// @Failure      404  {object}  apperrors.AppError  "User not found"
// @Router       /system/admin/users/status [post]
func (h *SystemHandler) SetUserStatus(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req SetUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid request"))
		return
	}

	callerID, _ := types.UserIDFromContext(ctx)
	user, err := h.userSvc.SetUserActive(ctx, strings.TrimSpace(req.UserID), callerID, *req.IsActive)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			writeAppError(c, apperrors.NewNotFoundError("User not found"))
		case errors.Is(err, service.ErrCannotDisableSelf),
			errors.Is(err, service.ErrCannotDisableLastAdmin):
			writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		default:
			logger.Errorf(ctx, "Error setting user status: %v", err)
			writeAppError(c, apperrors.NewInternalServerError("Failed to update user status"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "User status updated",
		"user":      user.ToUserInfo(),
		"is_active": user.IsActive,
	})
}

// ResetUserPasswordRequest defines the system-administrator password-reset payload.
type ResetUserPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	NewPassword string `json:"new_password" binding:"required"`
}

// ResetUserPassword godoc
// @Summary      Reset another user's password
// @Description  Replace another user's local password and revoke all of their existing sessions (SystemAdmin only).
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body ResetUserPasswordRequest true "Password reset request"
// @Success      200  {object}  handler.ResetUserPasswordResponse  "Password reset successfully"
// @Failure      400  {object}  apperrors.AppError  "Invalid request, weak password, or self reset"
// @Failure      403  {object}  apperrors.AppError  "Forbidden: not a system admin"
// @Failure      404  {object}  apperrors.AppError  "User not found"
// @Router       /system/admin/users/reset-password [post]
func (h *SystemHandler) ResetUserPassword(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req ResetUserPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid password reset request"))
		return
	}
	req.Email = strings.TrimSpace(req.Email)

	if err := service.ValidatePasswordPolicy(req.NewPassword, h.complexPasswordEnabled(ctx)); err != nil {
		writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		return
	}

	user, err := h.userSvc.GetUserByEmail(ctx, req.Email)
	if err != nil || user == nil {
		writeAppError(c, apperrors.NewNotFoundError("User not found"))
		return
	}
	callerID, _ := types.UserIDFromContext(ctx)
	if callerID == user.ID {
		writeAppError(c, apperrors.NewBadRequestError("Cannot reset your own password here"))
		return
	}

	if err := h.userSvc.AdminResetPassword(ctx, user.ID, req.NewPassword); err != nil {
		if service.IsPasswordPolicyError(err) {
			writeAppError(c, apperrors.NewBadRequestError(err.Error()))
			return
		}
		logger.Errorf(ctx, "Failed to reset password for user %s: %v", user.ID, err)
		writeAppError(c, apperrors.NewInternalServerError("Failed to reset user password"))
		return
	}

	logger.Infof(ctx, "Password reset by system administrator for user ID: %s", user.ID)
	h.emitAdminAudit(ctx, types.AuditActionSystemUserPasswordReset, user, map[string]any{
		"target_email":     user.Email,
		"target_username":  user.Username,
		"sessions_revoked": true,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Password reset successfully"})
}

// CreateSystemUserResponse is the payload returned by POST /api/v1/system/admin/users/create.
type CreateSystemUserResponse struct {
	User              *types.UserInfo `json:"user"`
	GeneratedPassword string          `json:"generated_password,omitempty"`
}

// CreateSystemUser godoc
// @Summary      Create a new user (SystemAdmin)
// @Description  Provision a new local user account (SystemAdmin only).
// @Tags         System Admin
// @Accept       json
// @Produce      json
// @Param        request body types.AdminCreateUserRequest true "User creation request"
// @Success      201  {object}  CreateSystemUserResponse  "User created successfully"
// @Success      200  {object}  CreateSystemUserResponse  "Identity already exists, returns the existing user"
// @Failure      400  {object}  apperrors.AppError  "Invalid request or weak password"
// @Failure      403  {object}  apperrors.AppError  "Forbidden: not a system admin"
// @Failure      409  {object}  apperrors.AppError  "Email and username refer to conflicting identities"
// @Failure      500  {object}  apperrors.AppError  "Internal error"
// @Router       /system/admin/users/create [post]
func (h *SystemHandler) CreateSystemUser(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var req types.AdminCreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid user creation request"))
		return
	}
	req.Username = secutils.SanitizeForLog(strings.TrimSpace(req.Username))
	req.Email = secutils.SanitizeForLog(strings.TrimSpace(req.Email))
	if req.Username == "" || req.Email == "" {
		writeAppError(c, apperrors.NewBadRequestError("Username and email are required"))
		return
	}
	if n := utf8.RuneCountInString(req.Username); n < 2 || n > 50 {
		writeAppError(c, apperrors.NewBadRequestError("Username must be 2-50 characters"))
		return
	}

	user, generatedPassword, err := h.userSvc.AdminCreateUser(ctx, &req, h.resolveDefaultTenantMode(ctx))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserEmailExists) || errors.Is(err, service.ErrUserUsernameExists):
			if user == nil {
				logger.Errorf(ctx, "Duplicate identity error without a resolved user: %v", err)
				writeAppError(c, apperrors.NewInternalServerError("Failed to create user"))
				return
			}
			logger.Infof(ctx, "Create user noop (identity already exists, ID: %s)", user.ID)
			h.emitAdminAudit(ctx, types.AuditActionSystemUserCreated, user, map[string]any{
				"target_email":       user.Email,
				"target_username":    user.Username,
				"password_generated": false,
				"idempotent":         true,
			})
			c.JSON(http.StatusOK, CreateSystemUserResponse{User: user.ToUserInfo()})
		case errors.Is(err, service.ErrPasswordPolicy):
			writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		case errors.Is(err, service.ErrUserIdentityConflict):
			writeAppError(c, apperrors.NewConflictError(err.Error()))
		default:
			logger.Errorf(ctx, "Failed to create user: %v", err)
			writeAppError(c, apperrors.NewInternalServerError("Failed to create user"))
		}
		return
	}

	logger.Infof(ctx, "System admin created user %s (ID: %s)", user.Username, user.ID)
	h.emitAdminAudit(ctx, types.AuditActionSystemUserCreated, user, map[string]any{
		"target_email":       user.Email,
		"target_username":    user.Username,
		"password_generated": generatedPassword != "",
		"idempotent":         false,
	})
	c.JSON(http.StatusCreated, CreateSystemUserResponse{
		User:              user.ToUserInfo(),
		GeneratedPassword: generatedPassword,
	})
}
