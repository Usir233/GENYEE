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
