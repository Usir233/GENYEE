package interfaces

import (
	"context"

	"github.com/Usir233/GENYEE/internal/types"
)

// StorageBackendRepository defines persistence operations for tenant storage backends.
type StorageBackendRepository interface {
	Create(ctx context.Context, backend *types.StorageBackend) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.StorageBackend, error)
	List(ctx context.Context, tenantID uint64) ([]*types.StorageBackend, error)
	Update(ctx context.Context, backend *types.StorageBackend) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	FindLegacyAlias(ctx context.Context, tenantID uint64, provider string) (*types.StorageBackend, error)
}

// StorageBackendService manages tenant object storage backend configurations.
type StorageBackendService interface {
	Create(ctx context.Context, backend *types.StorageBackend) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.StorageBackend, error)
	List(ctx context.Context, tenantID uint64) ([]*types.StorageBackend, error)
	Update(ctx context.Context, backend *types.StorageBackend) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	SetDefault(ctx context.Context, tenantID uint64, id string) error
	Test(ctx context.Context, backend *types.StorageBackend) error
}

// StorageBackendResolver is the single runtime entry point for resolving one
// concrete storage instance. backendID wins; provider is a legacy fallback.
type StorageBackendResolver interface {
	ResolveFileService(
		ctx context.Context, tenant *types.Tenant, backendID, provider, localBaseDir string,
	) (FileService, string, error)
	ResolveBackend(ctx context.Context, tenant *types.Tenant, backendID, provider string) (*types.StorageBackend, error)
}
