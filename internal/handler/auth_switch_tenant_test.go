package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
)

func TestAuthHandlerSwitchTenantWithoutRefreshInheritsSessionAndHonorsLogout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "test-jwt-secret-for-auth-handler-switch-tenant")

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth-switch.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.User{}, &types.AuthToken{}, &types.TenantMember{}))

	tenantRepo := repository.NewTenantRepository(db)
	tenantSvc := service.NewTenantService(tenantRepo, nil, nil)
	homeTenant := &types.Tenant{Name: "Home", Status: "active"}
	targetTenant := &types.Tenant{Name: "Target", Status: "active"}
	require.NoError(t, tenantRepo.CreateTenant(t.Context(), homeTenant))
	require.NoError(t, tenantRepo.CreateTenant(t.Context(), targetTenant))

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewAuthTokenRepository(db)
	memberRepo := repository.NewTenantMemberRepository(db)
	memberSvc := service.NewTenantMemberService(memberRepo, nil, userRepo, tokenRepo)

	user := &types.User{
		ID:       "user-switch-http",
		Username: "alice",
		Email:    "alice@example.com",
		TenantID: homeTenant.ID,
		IsActive: true,
	}
	require.NoError(t, userRepo.CreateUser(t.Context(), user))
	require.NoError(t, db.Create(&types.TenantMember{
		TenantID: homeTenant.ID, UserID: user.ID, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive,
	}).Error)
	require.NoError(t, db.Create(&types.TenantMember{
		TenantID: targetTenant.ID, UserID: user.ID,
		Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive,
	}).Error)

	cfg := &config.Config{Tenant: &config.TenantConfig{}}
	userSvc := service.NewUserService(cfg, userRepo, tokenRepo, tenantSvc, memberSvc, nil)
	accessTok, refreshTok, err := userSvc.GenerateTokens(t.Context(), user)
	require.NoError(t, err)

	h := &AuthHandler{
		userService:   userSvc,
		tenantService: tenantSvc,
		configInfo:    cfg,
	}

	r := gin.New()
	r.Use(changePasswordErrorCapture())
	r.POST("/auth/switch-tenant", func(c *gin.Context) {
		// Simulate JWT auth middleware setting UserContextKey after validating Bearer token.
		tok := c.GetHeader("Authorization")
		if len(tok) > len("Bearer ") {
			u, _, vErr := userSvc.ValidateToken(c.Request.Context(), tok[len("Bearer "):])
			if vErr == nil && u != nil {
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.UserContextKey, u))
			}
		}
		h.SwitchTenant(c)
	})
	r.POST("/auth/logout", h.Logout)

	body, _ := json.Marshal(map[string]any{"tenant_id": targetTenant.ID})
	req := httptest.NewRequest(http.MethodPost, "/auth/switch-tenant", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var switchResp struct {
		Success      bool   `json:"success"`
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &switchResp))
	require.True(t, switchResp.Success)
	require.NotEmpty(t, switchResp.Token)
	require.Empty(t, switchResp.RefreshToken, "omitting refresh_token must not mint a new 7-day refresh_token")

	_, switchedTenantID, err := userSvc.ValidateToken(t.Context(), switchResp.Token)
	require.NoError(t, err)
	require.Equal(t, targetTenant.ID, switchedTenantID)

	// Logout using the switched access token revokes the entire login session (including original access & refresh).
	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+switchResp.Token)
	logoutW := httptest.NewRecorder()
	r.ServeHTTP(logoutW, logoutReq)
	require.Equal(t, http.StatusOK, logoutW.Code, logoutW.Body.String())

	_, _, err = userSvc.ValidateToken(t.Context(), switchResp.Token)
	require.Error(t, err)
	_, _, err = userSvc.ValidateToken(t.Context(), accessTok)
	require.Error(t, err)
	_, _, err = userSvc.RefreshToken(t.Context(), refreshTok)
	require.Error(t, err)
}
