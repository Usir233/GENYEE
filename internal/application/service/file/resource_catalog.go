package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

type crossTenantAuthKey struct{}

// WithCrossTenantResourceAccess marks specific references (or resolved physical
// paths) as explicitly authorized for cross-tenant URL signing in ctx (for
// example after verifying a live shared-KB viewer grant).
func WithCrossTenantResourceAccess(ctx context.Context, refs ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	allowed := make(map[string]struct{}, len(refs))
	if prev, ok := ctx.Value(crossTenantAuthKey{}).(map[string]struct{}); ok {
		for k, v := range prev {
			allowed[k] = v
		}
	}
	for _, r := range refs {
		if r != "" {
			allowed[r] = struct{}{}
		}
	}
	return context.WithValue(ctx, crossTenantAuthKey{}, allowed)
}

func isCrossTenantResourceAuthorized(ctx context.Context, refs ...string) bool {
	if ctx == nil {
		return false
	}
	allowed, ok := ctx.Value(crossTenantAuthKey{}).(map[string]struct{})
	if !ok || len(allowed) == 0 {
		return false
	}
	for _, r := range refs {
		if _, exists := allowed[r]; exists {
			return true
		}
	}
	return false
}

func currentTenantIDFromContext(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	if tenant, ok := types.TenantInfoFromContext(ctx); ok && tenant != nil && tenant.ID != 0 {
		return tenant.ID
	}
	if caller := types.CallerFromContext(ctx); caller.TenantID != 0 {
		return caller.TenantID
	}
	return 0
}

// resourceCatalogFileService keeps provider drivers physical-path-only while
// exposing stable resource:// references to the application layer.
type resourceCatalogFileService struct {
	inner       interfaces.FileService
	catalog     interfaces.ResourceCatalog
	externalURL string
	global      bool
}

// NewGlobalResourceCatalogFileService pins writes to the process-wide driver's
// concrete instance, independently of workspace defaults and legacy aliases.
func NewGlobalResourceCatalogFileService(
	inner interfaces.FileService,
	catalog interfaces.ResourceCatalog,
) interfaces.FileService {
	svc := NewResourceCatalogFileService(inner, catalog)
	if wrapped, ok := svc.(*resourceCatalogFileService); ok {
		wrapped.global = true
	}
	return svc
}

// NewResourceCatalogFileService decorates a physical FileService with stable
// resource registration and resolution.
func NewResourceCatalogFileService(
	inner interfaces.FileService,
	catalog interfaces.ResourceCatalog,
) interfaces.FileService {
	if inner == nil || catalog == nil {
		return inner
	}
	return &resourceCatalogFileService{
		inner:       inner,
		catalog:     catalog,
		externalURL: strings.TrimRight(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")), "/"),
	}
}

func (s *resourceCatalogFileService) CheckConnectivity(ctx context.Context) error {
	return s.inner.CheckConnectivity(ctx)
}

// ResourceCatalog exposes the underlying catalog so request-scoped URL
// rewriters can enforce resource ownership before signing public links.
func (s *resourceCatalogFileService) ResourceCatalog() interfaces.ResourceCatalog {
	return s.catalog
}

func resourceKind(name string) (string, string) {
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	kind := "file"
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		kind = "image"
	case strings.HasPrefix(mimeType, "audio/"):
		kind = "audio"
	case strings.HasPrefix(mimeType, "video/"):
		kind = "video"
	}
	return kind, mimeType
}

func (s *resourceCatalogFileService) register(
	ctx context.Context,
	physical string,
	tenantID uint64,
	name string,
	size int64,
	temporary bool,
	contentHash string,
) (string, error) {
	kind, mimeType := resourceKind(name)
	register := s.catalog.Register
	if s.global {
		registrar, ok := s.catalog.(interfaces.GlobalResourceRegistrar)
		if !ok {
			s.cleanupFailedUpload(ctx, s.inner.DeleteFile, physical)
			return "", fmt.Errorf("global storage registration is unavailable")
		}
		register = registrar.RegisterGlobal
	}
	ref, err := register(ctx, tenantID, physical, interfaces.ResourceRegistration{
		Kind:         kind,
		MimeType:     mimeType,
		OriginalName: filepath.Base(name),
		Size:         size,
		ContentHash:  contentHash,
		Temporary:    temporary,
	})
	if err != nil {
		s.cleanupFailedUpload(ctx, s.inner.DeleteFile, physical)
		return "", fmt.Errorf("register stored resource: %w", err)
	}
	return ref, nil
}

func (s *resourceCatalogFileService) cleanupFailedUpload(
	ctx context.Context,
	remove func(context.Context, string) error,
	ref string,
) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = remove(cleanupCtx, ref)
}

func (s *resourceCatalogFileService) SaveFile(
	ctx context.Context,
	file *multipart.FileHeader,
	tenantID uint64,
	knowledgeID string,
) (string, error) {
	physical, err := s.inner.SaveFile(ctx, file, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	ref, err := s.register(ctx, physical, tenantID, file.Filename, file.Size, false, "")
	if err != nil {
		return "", err
	}
	if knowledgeID != "" {
		if err := s.catalog.Bind(
			ctx,
			ref,
			types.ResourceOwnerKnowledge,
			knowledgeID,
			types.ResourceRelationSourceFile,
		); err != nil {
			s.cleanupFailedUpload(ctx, s.DeleteUnboundFile, ref)
			return "", fmt.Errorf("bind stored resource: %w", err)
		}
	}
	return ref, nil
}

// isImagePayload reports whether data should be treated as image content.
//
// The rule is deliberately byte-first: two payloads can only be shared when
// their bytes are equal, so the decision must not hinge on a name the caller
// chose freely. The extension is still consulted as a fallback because some
// extracted image formats — SVG above all — have no signature
// http.DetectContentType recognises, and those are exactly the vector assets a
// document repeats across pages. A payload is therefore an image when its name
// maps to an image media type (the same rule register uses to derive Kind) or
// when its bytes sniff as image/*.
func isImagePayload(name string, data []byte) bool {
	if kind, _ := resourceKind(name); kind == "image" {
		return true
	}
	return strings.HasPrefix(http.DetectContentType(data), "image/")
}

func (s *resourceCatalogFileService) SaveBytes(
	ctx context.Context,
	data []byte,
	tenantID uint64,
	fileName string,
	temp bool,
) (string, error) {
	ref, _, err := s.SaveBytesWithOwnership(ctx, data, tenantID, fileName, temp)
	return ref, err
}

func (s *resourceCatalogFileService) SaveBytesWithOwnership(
	ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool,
) (string, bool, error) {
	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])
	independent := interfaces.IndependentFileWrites(ctx)
	if independent {
		contentHash = ""
	}

	// Identical bytes pulled out of one document — the same logo or mascot on
	// several pages — used to be written once per occurrence and registered as
	// a separate handle each time: duplicated storage, the same picture
	// captioned repeatedly, and one identical gallery card per copy. Look the
	// payload up before writing so every occurrence shares one handle.
	//
	// Reuse is restricted to images outside temporary scratch space. For a
	// user upload or a generated artifact the original name is part of what
	// the caller asked for, and sharing would surface the first name for the
	// second file; extracted document images are named from a generated UUID,
	// so sharing one is invisible. Temporary objects are excluded because
	// their lifecycle is per-use.
	if !temp && !independent && isImagePayload(fileName, data) {
		var ref string
		var ok bool
		var err error
		if scoped, supportsScope := s.catalog.(interfaces.ScopedResourceContentCatalog); supportsScope {
			backendID := ""
			if inner, isScoped := s.inner.(*backendScopedFileService); isScoped {
				backendID = inner.backendID
			}
			ref, ok, err = scoped.ReuseByContentHashInStorage(ctx, tenantID, contentHash, backendID, s.global)
		} else {
			ref, ok, err = s.catalog.ReuseByContentHash(ctx, tenantID, contentHash)
		}
		if err == nil && ok {
			return ref, false, nil
		}
		// Fail open: a lookup failure or a miss must never stop bytes from
		// being stored, so fall through to the normal write-and-register path.
	}

	physical, err := s.inner.SaveBytes(ctx, data, tenantID, fileName, temp)
	if err != nil {
		return "", false, err
	}
	ref, err := s.register(ctx, physical, tenantID, fileName, int64(len(data)), temp, contentHash)
	return ref, err == nil, err
}

func (s *resourceCatalogFileService) DeleteUnboundFile(ctx context.Context, reference string) error {
	if _, ok := types.ParseResourcePath(reference); !ok {
		return s.inner.DeleteFile(ctx, reference)
	}
	cleaner, ok := s.catalog.(interfaces.UnboundResourceCatalog)
	if !ok {
		return fmt.Errorf("atomic resource cleanup is unavailable")
	}
	physical, err := cleaner.ClaimUnboundDeletion(ctx, reference)
	if err != nil || physical == "" {
		return err
	}
	if err := s.inner.DeleteFile(ctx, physical); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.deleteDerivedResources(ctx, reference)
}

// Attachment previews belong to the original uploaded resource before a
// message exists. Deleting that parent also releases its independent images.
func (s *resourceCatalogFileService) deleteDerivedResources(ctx context.Context, parent string) error {
	cleaner, ok := s.catalog.(interfaces.UnboundResourceCatalog)
	if !ok {
		return nil
	}
	refs, err := cleaner.ListDeletionDependents(ctx, parent)
	if err != nil {
		return err
	}
	var failures []error
	for _, ref := range refs {
		if err := s.DeleteUnboundFile(ctx, ref); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := cleaner.ReleaseDeletionDependent(ctx, ref, parent); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *resourceCatalogFileService) resolve(ctx context.Context, value string) (string, bool, error) {
	physical, resource, err := s.catalog.ResolvePath(ctx, value)
	return physical, resource != nil, err
}

func (s *resourceCatalogFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	physical, _, err := s.resolve(ctx, filePath)
	if err != nil {
		return nil, err
	}
	return s.inner.GetFile(ctx, physical)
}

func (s *resourceCatalogFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	physical, resource, err := s.catalog.ResolvePath(ctx, filePath)
	if err != nil {
		return "", err
	}
	callerTenantID := currentTenantIDFromContext(ctx)
	if resource != nil {
		if resource.TenantID == 0 {
			return "", fmt.Errorf("stored resource missing owner tenant")
		}
		if callerTenantID != 0 && callerTenantID != resource.TenantID &&
			!isCrossTenantResourceAuthorized(ctx, filePath, physical) {
			return "", fmt.Errorf("resource tenant mismatch: owner=%d caller=%d", resource.TenantID, callerTenantID)
		}
		if types.ParseProviderScheme(physical) != "" {
			if err := secutils.ValidateStoragePathTenant(physical, resource.TenantID); err != nil {
				return "", fmt.Errorf("invalid resource storage path: %w", err)
			}
		}
		if s.externalURL != "" {
			token, grantErr := s.catalog.CreateAccessGrant(ctx, filePath, 2*time.Hour)
			if grantErr != nil {
				return "", grantErr
			}
			return s.externalURL + "/r/" + token, nil
		}
		if callerTenantID != 0 && callerTenantID != resource.TenantID {
			ctx = WithCrossTenantResourceAccess(ctx, filePath, physical)
		}
	} else if callerTenantID != 0 && types.ParseProviderScheme(physical) != "" &&
		!isCrossTenantResourceAuthorized(ctx, filePath, physical) {
		if err := secutils.ValidateStoragePathTenant(physical, callerTenantID); err != nil {
			return "", fmt.Errorf("storage path tenant mismatch: %w", err)
		}
	}
	return s.inner.GetFileURL(ctx, physical)
}

func (s *resourceCatalogFileService) DeleteFile(ctx context.Context, filePath string) error {
	physical, isResource, err := s.resolve(ctx, filePath)
	if err != nil {
		if _, managed := types.ParseResourcePath(filePath); managed {
			return s.DeleteUnboundFile(ctx, filePath)
		}
		return err
	}
	if err := s.inner.DeleteFile(ctx, physical); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if isResource {
		if err := s.catalog.MarkDeleted(ctx, filePath); err != nil {
			return err
		}
		return s.deleteDerivedResources(ctx, filePath)
	}
	return nil
}

func (s *resourceCatalogFileService) CopyFile(
	ctx context.Context,
	filePath string,
	tenantID uint64,
	knowledgeID string,
) (string, error) {
	physical, _, err := s.resolve(ctx, filePath)
	if err != nil {
		return "", err
	}
	copied, err := s.inner.CopyFile(ctx, physical, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	ref, err := s.register(ctx, copied, tenantID, filepath.Base(physical), 0, false, "")
	if err != nil {
		return "", err
	}
	if knowledgeID != "" {
		if err := s.catalog.Bind(
			ctx,
			ref,
			types.ResourceOwnerKnowledge,
			knowledgeID,
			types.ResourceRelationSourceFile,
		); err != nil {
			s.cleanupFailedUpload(ctx, s.DeleteUnboundFile, ref)
			return "", fmt.Errorf("bind copied resource: %w", err)
		}
	}
	return ref, nil
}

var _ interfaces.FileService = (*resourceCatalogFileService)(nil)
