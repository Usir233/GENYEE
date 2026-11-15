package container

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

type stubContainerTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r *stubContainerTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if r.tenant != nil && r.tenant.ID == id {
		return r.tenant, nil
	}
	return nil, nil
}

type stubContainerResourceCatalog struct {
	interfaces.ResourceCatalog
	physical string
	resource *types.StoredResource
}

func (c *stubContainerResourceCatalog) ResolvePath(_ context.Context, _ string) (string, *types.StoredResource, error) {
	return c.physical, c.resource, nil
}

type stubContainerFileService struct {
	interfaces.FileService
	open func() (io.ReadCloser, error)
}

func (s *stubContainerFileService) GetFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return s.open()
}

type stubContainerStorageResolver struct {
	interfaces.StorageBackendResolver
	lastBaseDir string
	svc         interfaces.FileService
}

func (r *stubContainerStorageResolver) ResolveFileService(
	_ context.Context, _ *types.Tenant, _, provider, baseDir string,
) (interfaces.FileService, string, error) {
	r.lastBaseDir = baseDir
	return r.svc, provider, nil
}

type endlessZeroReader struct {
	readBytes int64
}

func (r *endlessZeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	r.readBytes += int64(len(p))
	return len(p), nil
}

func TestRegisterChatLocalImageResolverDefaultsBaseDirAndBoundsRead(t *testing.T) {
	prev := api.LocalImageResolver
	t.Cleanup(func() { api.LocalImageResolver = prev })

	t.Setenv("LOCAL_STORAGE_BASE_DIR", "")

	fileSvc := &stubContainerFileService{
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("img-bytes"))), nil
		},
	}
	storageResolver := &stubContainerStorageResolver{svc: fileSvc}
	tenantRepo := &stubContainerTenantRepo{tenant: &types.Tenant{ID: 7}}
	catalog := &stubContainerResourceCatalog{
		physical: "local://7/images/pic.png",
		resource: &types.StoredResource{TenantID: 7, Provider: "local"},
	}

	registerChatLocalImageResolver(tenantRepo, storageResolver, catalog)

	got, ok := api.LocalImageResolver("resource://AbCdEfGhIjKlMnOpQrStUv")
	if !ok || string(got) != "img-bytes" {
		t.Fatalf("expected valid image bytes, got ok=%v len=%d", ok, len(got))
	}
	if storageResolver.lastBaseDir != "/data/files" {
		t.Fatalf("expected unset LOCAL_STORAGE_BASE_DIR to default to /data/files, got %q", storageResolver.lastBaseDir)
	}

	// Oversized stream must be bounded rather than read without limit into memory.
	endless := &endlessZeroReader{}
	fileSvc.open = func() (io.ReadCloser, error) {
		return io.NopCloser(io.LimitReader(endless, (64<<20))), nil
	}
	if data, ok := api.LocalImageResolver("resource://AbCdEfGhIjKlMnOpQrStUv"); ok || data != nil {
		t.Fatalf("expected oversized image stream to be rejected, got ok=%v len=%d", ok, len(data))
	}
	if endless.readBytes >= (60 << 20) {
		t.Fatalf("expected bounded read below 60 MiB, read %d bytes", endless.readBytes)
	}
}
