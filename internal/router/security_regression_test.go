package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKnowledgePreviewAlignsWithDownloadPermissionBoundary(t *testing.T) {
	t.Run("rejects tenant viewer on own workspace", func(t *testing.T) {
		engine := newKnowledgeDownloadRouteTestEngine(
			t,
			types.TenantRoleViewer,
			&types.Knowledge{ID: "knowledge-own", KnowledgeBaseID: "kb-own", TenantID: 1},
			&types.KnowledgeBase{ID: "kb-own", TenantID: 1},
			nil,
		)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/knowledge-own/preview", nil)
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	})

	t.Run("rejects read-only org-shared KB even for contributor", func(t *testing.T) {
		engine := newKnowledgeDownloadRouteTestEngine(
			t,
			types.TenantRoleContributor,
			&types.Knowledge{ID: "knowledge-shared", KnowledgeBaseID: "kb-shared", TenantID: 2},
			&types.KnowledgeBase{ID: "kb-shared", TenantID: 2},
			&downloadKBShareStub{permission: types.OrgRoleViewer, source: 2},
		)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/knowledge-shared/preview", nil)
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	})
}

func TestCORSDisablesCredentialsOnWildcardAndTrustedProxiesDefaultLoopback(t *testing.T) {
	t.Setenv("GENYEE_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	wildcardCfg := buildCORSConfig()
	require.False(t, wildcardCfg.AllowCredentials, "wildcard CORS must have AllowCredentials=false")
	require.Equal(t, []string{"*"}, wildcardCfg.AllowOrigins)

	t.Setenv("GENYEE_CORS_ALLOWED_ORIGINS", "https://app.example.com, https://admin.example.com")
	explicitCfg := buildCORSConfig()
	require.True(t, explicitCfg.AllowCredentials)
	require.Equal(t, []string{"https://app.example.com", "https://admin.example.com"}, explicitCfg.AllowOrigins)

	if prev, existed := os.LookupEnv("GENYEE_TRUSTED_PROXIES"); existed {
		t.Cleanup(func() { _ = os.Setenv("GENYEE_TRUSTED_PROXIES", prev) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv("GENYEE_TRUSTED_PROXIES") })
	}
	_ = os.Unsetenv("GENYEE_TRUSTED_PROXIES")
	proxies := trustedProxies()
	require.Equal(t, []string{"127.0.0.0/8", "::1/128"}, proxies)

	t.Setenv("GENYEE_TRUSTED_PROXIES", "")
	require.Empty(t, trustedProxies())
}

func TestTrustedProxiesSkipsMalformedEntriesAndNeverFailsOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("GENYEE_TRUSTED_PROXIES", "10.0.0.0/8, invalid-proxy, ::1/128, 192.168.1.10")
	require.Equal(t, []string{"10.0.0.0/8", "::1/128", "192.168.1.10"}, trustedProxies())

	// Even when every configured entry is malformed, Gin must NOT retain its
	// 0.0.0.0/0 default and honor spoofed X-Forwarded-For from arbitrary peers.
	t.Setenv("GENYEE_TRUSTED_PROXIES", "not-an-ip-or-cidr")
	r := gin.New()
	applyTrustedProxies(r)
	var gotClientIP string
	r.GET("/ip", func(c *gin.Context) {
		gotClientIP = c.ClientIP()
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.77")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, "203.0.113.10", gotClientIP, "malformed GENYEE_TRUSTED_PROXIES must not fail open to trusting XFF")
}

func TestBuildCORSConfigRejectsNullAndMalformedOriginsWithoutPanicking(t *testing.T) {
	t.Setenv("GENYEE_CORS_ALLOWED_ORIGINS", "null, app.example.com, https://valid.example.com/, https://evil.example.com/path")
	cfg := buildCORSConfig()
	require.True(t, cfg.AllowCredentials)
	require.Equal(t, []string{"https://valid.example.com"}, cfg.AllowOrigins)
	require.NotPanics(t, func() {
		_ = cors.New(cfg)
	})

	t.Setenv("GENYEE_CORS_ALLOWED_ORIGINS", "null, file://local")
	fallback := buildCORSConfig()
	require.False(t, fallback.AllowCredentials)
	require.Equal(t, []string{"*"}, fallback.AllowOrigins)
	require.NotPanics(t, func() {
		_ = cors.New(fallback)
	})
}
