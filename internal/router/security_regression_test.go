package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
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
