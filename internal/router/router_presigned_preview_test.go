package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

func servePresignedPreviewForTest(t *testing.T, catalog interfaces.ResourceCatalog, filePath string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	engine := gin.New()
	servePresignedPreview(engine, nil, nil, catalog)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/files/presigned-preview?file_path="+url.QueryEscape(filePath), nil)
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42})
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 42, UserID: "admin", Role: types.TenantRoleAdmin})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req.WithContext(ctx))
	return recorder.Code
}

// A known handle of another tenant's resource must not be exchangeable for an
// anonymous /r/ grant (the stub catalog panics if a grant is minted).
func TestPresignedPreviewRejectsCrossTenantResource(t *testing.T) {
	catalog := &stubResourceCatalog{resource: &types.StoredResource{
		TenantID: 7, PhysicalPath: "local://7/exports/a.png",
	}}
	code := servePresignedPreviewForTest(t, catalog, "resource://AbCdEfGhIjKlMnOpQrStUv")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", code, http.StatusForbidden)
	}
}

func TestPresignedPreviewRejectsCrossTenantPath(t *testing.T) {
	for _, filePath := range []string{"local://7/knowledge/secret.pdf", "local://docs/example.txt"} {
		if code := servePresignedPreviewForTest(t, &stubResourceCatalog{}, filePath); code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want %d", filePath, code, http.StatusForbidden)
		}
	}
}

// Without a catalog a resource reference cannot be attributed to a tenant, so
// it is refused rather than signed.
func TestPresignedPreviewRejectsUnresolvableResource(t *testing.T) {
	if code := servePresignedPreviewForTest(t, nil, "resource://AbCdEfGhIjKlMnOpQrStUv"); code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", code, http.StatusForbidden)
	}
}

func TestPresignedPreviewAllowsOwnTenantPath(t *testing.T) {
	code := servePresignedPreviewForTest(t, &stubResourceCatalog{}, "local://42/exports/a.png")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
}

type grantMintingResourceCatalog struct {
	stubResourceCatalog
	grant string
	err   error
}

func (g *grantMintingResourceCatalog) CreateAccessGrant(context.Context, string, time.Duration) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return g.grant, nil
}

type failingStorageResolver struct {
	interfaces.StorageBackendResolver
	err error
}

func (f *failingStorageResolver) ResolveFileService(
	context.Context, *types.Tenant, string, string, string,
) (interfaces.FileService, string, error) {
	return nil, "s3", f.err
}

func TestPresignedPreviewResolvesOwnTenantResourceAndRedactsInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	t.Setenv("APP_EXTERNAL_URL", "https://app.example.com")

	catalog := &grantMintingResourceCatalog{
		stubResourceCatalog: stubResourceCatalog{
			resource: &types.StoredResource{
				ID:           "res-42",
				TenantID:     42,
				PhysicalPath: "local://42/exports/a.png",
			},
		},
		grant: "grant-tok-123",
	}
	engine := gin.New()
	servePresignedPreview(engine, nil, nil, catalog)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/files/presigned-preview?file_path="+url.QueryEscape("resource://AbCdEfGhIjKlMnOpQrStUv"),
		nil,
	)
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42})
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 42, UserID: "admin", Role: types.TenantRoleAdmin})
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("own-tenant resource status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if payload["provider"] != "local" {
		t.Fatalf("provider = %v, want local", payload["provider"])
	}
	if payload["url"] != "https://app.example.com/r/grant-tok-123" {
		t.Fatalf("url = %v, want https://app.example.com/r/grant-tok-123", payload["url"])
	}

	// Internal storage resolver / signing errors must not leak backend credentials in JSON.
	errEngine := gin.New()
	servePresignedPreview(
		errEngine,
		nil,
		&failingStorageResolver{err: errors.New("dial s3: secret_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")},
		catalog,
	)
	errReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/files/presigned-preview?file_path="+url.QueryEscape("local://42/exports/a.png"),
		nil,
	)
	errRec := httptest.NewRecorder()
	errEngine.ServeHTTP(errRec, errReq.WithContext(ctx))
	if errRec.Code != http.StatusBadRequest {
		t.Fatalf("resolver error status = %d, want 400", errRec.Code)
	}
	if strings.Contains(errRec.Body.String(), "wJalrXUtnFEMI") {
		t.Fatalf("presigned-preview leaked raw backend error secret: %s", errRec.Body.String())
	}
}
