package interfaces

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"

	"github.com/Usir233/GENYEE/internal/types"
)

// FileService is the interface for file services.
// FileService provides methods to save, retrieve, and delete files.
type FileService interface {
	// CheckConnectivity verifies that the storage backend is reachable and
	// properly configured (e.g. bucket exists, credentials valid).
	CheckConnectivity(ctx context.Context) error
	// SaveFile saves a file.
	SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error)
	// SaveBytes saves bytes data to a file and returns the file path.
	// If temp is true, the file will be saved to a temporary storage that may auto-expire.
	SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error)
	// GetFile retrieves a file.
	GetFile(ctx context.Context, filePath string) (io.ReadCloser, error)
	// GetFileURL returns a download URL for the file (if supported by the storage backend).
	GetFileURL(ctx context.Context, filePath string) (string, error)
	// DeleteFile deletes a file.
	DeleteFile(ctx context.Context, filePath string) error
	// CopyFile copies an existing stored object to a NEW object owned by
	// (tenantID, knowledgeID), returning the new provider:// path. The copy is
	// independent: deleting the source never affects it. Returns ErrCrossBackendCopy
	// when srcPath belongs to a different storage provider than this service.
	CopyFile(ctx context.Context, srcPath string, tenantID uint64, knowledgeID string) (string, error)
}

// OwnedBytesFileService distinguishes a new upload from a deduplicated object.
// A reused object belongs to another save and must never be rolled back by this one.
type OwnedBytesFileService interface {
	SaveBytesWithOwnership(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (reference string, created bool, err error)
}

type independentFileWritesKey struct{}

// WithIndependentFileWrites is for producers without a durable resource owner
// (for example attachment previews created before a message exists). Their
// bytes must neither reuse another owner's upload nor be offered for reuse.
func WithIndependentFileWrites(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, independentFileWritesKey{}, true)
}

func IndependentFileWrites(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	independent, _ := ctx.Value(independentFileWritesKey{}).(bool)
	return independent
}

func SaveBytesWithOwnership(ctx context.Context, svc FileService, data []byte, tenantID uint64, fileName string, temp bool) (string, bool, error) {
	if owned, ok := svc.(OwnedBytesFileService); ok {
		return owned.SaveBytesWithOwnership(ctx, data, tenantID, fileName, temp)
	}
	ref, err := svc.SaveBytes(ctx, data, tenantID, fileName, temp)
	return ref, err == nil, err
}

// UnboundFileService removes failed uploads without destroying another owner's
// resource. Catalog-backed implementations must serialize this with binding.
type UnboundFileService interface {
	DeleteUnboundFile(ctx context.Context, reference string) error
}

func DeleteUnboundFile(ctx context.Context, svc FileService, reference string) error {
	if cleaner, ok := svc.(UnboundFileService); ok {
		return cleaner.DeleteUnboundFile(ctx, reference)
	}
	if _, ok := types.ParseResourcePath(reference); ok {
		return fmt.Errorf("unbound resource cleanup is unavailable")
	}
	return svc.DeleteFile(ctx, reference)
}
