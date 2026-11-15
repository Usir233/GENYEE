package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type tenantClaimUserService struct {
	interfaces.UserService
	user *types.User
}

func (s *tenantClaimUserService) ValidateToken(context.Context, string) (*types.User, uint64, error) {
	return s.user, 42, nil
}

func TestAuthRevalidatesForeignTenantClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                                    string
		superuser, crossTenant, member, enforce bool
		want                                    int
	}{
		{"disabled cross-workspace flag", true, false, false, true, http.StatusForbidden},
		{"disabled flag with dormant RBAC", true, false, false, false, http.StatusForbidden},
		{"revoked ordinary membership with dormant RBAC", false, false, false, false, http.StatusForbidden},
		{"revoked ordinary membership", false, false, false, true, http.StatusForbidden},
		{"active member with cross-workspace flag disabled", false, false, true, true, http.StatusNoContent},
		{"active superuser membership with flag disabled", true, false, true, true, http.StatusNoContent},
		{"enabled cross-workspace superuser", true, true, false, true, http.StatusNoContent},
	} {
		for _, header := range []string{"", "42"} {
			t.Run(tc.name+"/header="+header, func(t *testing.T) {
				cfg := cfgWithRBAC(tc.enforce)
				cfg.Tenant.EnableCrossTenantAccess = tc.crossTenant
				members := newFakeMemberService()
				if tc.member {
					members.seedActive("alice", 42, types.TenantRoleViewer)
				}
				users := &tenantClaimUserService{user: &types.User{ID: "alice", TenantID: 7, IsActive: true, CanAccessAllTenants: tc.superuser}}
				r := gin.New()
				r.Use(Auth(&fakeTenantService{tenant: &types.Tenant{ID: 42}}, users, members, nil, cfg))
				reached := false
				r.GET("/api/v1/knowledge-bases", func(c *gin.Context) {
					reached = true
					caller := types.CallerFromContext(c.Request.Context())
					if caller.TenantID != 42 {
						t.Errorf("tenant = %d, want 42", caller.TenantID)
					}
					if tc.member && caller.Role != types.TenantRoleViewer {
						t.Errorf("membership role = %s, want viewer", caller.Role)
					}
					c.Status(http.StatusNoContent)
				})
				req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)
				req.Header.Set("Authorization", "Bearer existing-session")
				if header != "" {
					req.Header.Set("X-Tenant-ID", header)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Fatalf("status=%d want=%d; reached=%v body=%s", w.Code, tc.want, reached, w.Body.String())
				}
				if reached != (tc.want == http.StatusNoContent) {
					t.Fatalf("unexpected handler reached=%v", reached)
				}
				if len(members.addCalls) != 0 {
					t.Fatal("foreign workspace must never be auto-claimed")
				}
			})
		}
	}
}

func TestAuthRejectsInactiveWorkspaceWhileAllowingTenantlessEscapeAndSuperuser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, status := range []string{"inactive", "disabled", "suspended", "INACTIVE"} {
		t.Run("jwt_workspace_route_"+status, func(t *testing.T) {
			cfg := cfgWithRBAC(true)
			members := newFakeMemberService()
			members.seedActive("alice", 42, types.TenantRoleOwner)
			users := &tenantClaimUserService{user: &types.User{ID: "alice", TenantID: 42, IsActive: true}}

			r := gin.New()
			r.Use(Auth(&fakeTenantService{tenant: &types.Tenant{ID: 42, Status: status}}, users, members, nil, cfg))
			r.GET("/api/v1/knowledge-bases", func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			r.GET("/api/v1/auth/me", func(c *gin.Context) {
				if _, hasTenant := types.TenantIDFromContext(c.Request.Context()); hasTenant {
					t.Error("expected tenantless fallback context on /api/v1/auth/me when default workspace is inactive")
				}
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)
			req.Header.Set("Authorization", "Bearer tok")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 for %s workspace (body=%s)", w.Code, status, w.Body.String())
			}

			// User can still reach tenant-optional identity routes without X-Tenant-ID.
			meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			meReq.Header.Set("Authorization", "Bearer tok")
			meW := httptest.NewRecorder()
			r.ServeHTTP(meW, meReq)
			if meW.Code != http.StatusOK {
				t.Fatalf("/api/v1/auth/me status = %d, want 200 (body=%s)", meW.Code, meW.Body.String())
			}
		})
	}

	t.Run("system_admin_allowed_into_inactive_workspace", func(t *testing.T) {
		cfg := cfgWithRBAC(true)
		cfg.Tenant.EnableCrossTenantAccess = true
		members := newFakeMemberService()
		users := &tenantClaimUserService{user: &types.User{
			ID: "sysadmin", TenantID: 1, IsActive: true, IsSystemAdmin: true, CanAccessAllTenants: true,
		}}
		r := gin.New()
		r.Use(Auth(&fakeTenantService{tenant: &types.Tenant{ID: 42, Status: "inactive"}}, users, members, nil, cfg))
		r.GET("/api/v1/knowledge-bases", func(c *gin.Context) { c.Status(http.StatusNoContent) })

		req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)
		req.Header.Set("Authorization", "Bearer tok")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("system admin status = %d, want 204", w.Code)
		}
	})

	t.Run("api_key_nil_tenant_and_inactive_workspace", func(t *testing.T) {
		// Nil tenant without error must return 401 instead of panicking.
		wNil := httptest.NewRecorder()
		cNil, _ := gin.CreateTestContext(wNil)
		cNil.Request = httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)
		attachAPIKeyAuthContext(cNil, &fakeTenantService{tenant: nil}, nil, 42, &types.TenantAPIKey{ID: 1})
		if wNil.Code != http.StatusUnauthorized {
			t.Fatalf("nil tenant status = %d, want 401", wNil.Code)
		}

		// Workspace API key rejected on disabled workspace.
		wDis := httptest.NewRecorder()
		cDis, _ := gin.CreateTestContext(wDis)
		cDis.Request = httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)
		attachAPIKeyAuthContext(
			cDis,
			&fakeTenantService{tenant: &types.Tenant{ID: 42, Status: "disabled"}},
			nil, 42,
			&types.TenantAPIKey{ID: 2, FullAccess: true},
		)
		if wDis.Code != http.StatusForbidden {
			t.Fatalf("workspace API key on disabled tenant status = %d, want 403", wDis.Code)
		}

		// Platform API key remains allowed for control-plane inspection.
		wPlat := httptest.NewRecorder()
		cPlat, _ := gin.CreateTestContext(wPlat)
		cPlat.Request = httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)
		attachAPIKeyAuthContext(
			cPlat,
			&fakeTenantService{tenant: &types.Tenant{ID: 42, Status: "disabled"}},
			nil, 42,
			&types.TenantAPIKey{ID: 3, ScopeType: types.APIKeyScopePlatform},
		)
		if cPlat.IsAborted() {
			t.Fatalf("platform API key unexpectedly aborted with %d", wPlat.Code)
		}
	})
}
