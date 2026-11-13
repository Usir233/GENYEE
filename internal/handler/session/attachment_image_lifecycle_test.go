package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/application/service"
	files "github.com/Usir233/GENYEE/internal/application/service/file"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAttachmentImagesHaveIndependentParentOwnedLifetimes(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "resources.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{}))
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(db))
	svc := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
	var payload bytes.Buffer
	require.NoError(t, png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 200, 150))))
	documentImage, err := svc.SaveBytes(ctx, payload.Bytes(), 7, "document.png", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, documentImage, types.ResourceOwnerKnowledge, "existing-document", types.ResourceRelationExtractedImage))
	resolver := docparser.NewImageResolver()
	processor := &AttachmentProcessor{fileService: svc, imageResolver: resolver}
	upload := func(name string) (string, string) {
		attachment, err := processor.ProcessAttachment(ctx, payload.Bytes(), name, int64(payload.Len()), 7, "")
		require.NoError(t, err)
		children, err := catalog.(interfaces.UnboundResourceCatalog).ListDeletionDependents(ctx, attachment.URL)
		require.NoError(t, err)
		require.Len(t, children, 1)
		require.Contains(t, attachment.Content, children[0])
		return attachment.URL, children[0]
	}
	firstParent, firstImage := upload("one.png")
	secondParent, secondImage := upload("two.png")
	paths := map[string]bool{}
	for _, ref := range []string{documentImage, firstParent, firstImage, secondParent, secondImage} {
		physical, _, err := catalog.ResolvePath(ctx, ref)
		require.NoError(t, err)
		require.False(t, paths[physical])
		paths[physical] = true
	}
	require.NoError(t, svc.DeleteFile(ctx, firstParent))
	_, err = catalog.Resolve(ctx, firstImage)
	require.Error(t, err, "deleting the attachment releases its derived image")
	for _, ref := range []string{documentImage, secondParent, secondImage} {
		reader, err := svc.GetFile(ctx, ref)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
	}
	require.NoError(t, svc.DeleteFile(ctx, secondParent))
	_, err = catalog.Resolve(ctx, secondImage)
	require.Error(t, err)
}

type attachmentSaveFailure struct {
	interfaces.FileService
	calls  int
	failAt int
}

func (s *attachmentSaveFailure) SaveBytes(ctx context.Context, data []byte, tenant uint64, name string, temp bool) (string, error) {
	s.calls++
	if s.calls == s.failAt {
		return "", errors.New("attachment upload failed")
	}
	return s.FileService.SaveBytes(ctx, data, tenant, name, temp)
}

func TestAttachmentRollbackKeepsAnotherAttachmentAndBoundDocument(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "resources.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{}))
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(db))
	raw := &attachmentSaveFailure{FileService: files.NewLocalFileService(t.TempDir(), "")}
	svc := files.NewResourceCatalogFileService(raw, catalog)
	var payload bytes.Buffer
	require.NoError(t, png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 200, 150))))
	document, err := svc.SaveBytes(ctx, payload.Bytes(), 7, "existing.png", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, document, types.ResourceOwnerKnowledge, "document", types.ResourceRelationExtractedImage))
	parent, err := svc.SaveBytes(ctx, []byte("attachment"), 7, "attachment.pdf", false)
	require.NoError(t, err)
	resolver := docparser.NewImageResolver()
	content := fmt.Sprintf("![image](data:image/png;base64,%s)", base64.StdEncoding.EncodeToString(payload.Bytes()))
	_, first, err := resolver.ResolveAndStore(interfaces.WithIndependentFileWrites(ctx), &types.ReadResult{MarkdownContent: content}, svc, 7)
	require.NoError(t, err)
	processor := &AttachmentProcessor{fileService: svc, imageResolver: resolver}
	require.NoError(t, processor.bindAttachmentImages(ctx, parent, first))
	raw.failAt = raw.calls + 2
	_, failed, err := resolver.ResolveAndStore(interfaces.WithIndependentFileWrites(ctx), &types.ReadResult{MarkdownContent: content + "\n" + content}, svc, 7)
	require.ErrorContains(t, err, "attachment upload failed")
	require.Len(t, failed, 1)
	_, err = catalog.Resolve(ctx, failed[0].ServingURL)
	require.Error(t, err, "failed attachment's new image is reclaimed")
	for _, ref := range []string{document, first[0].ServingURL} {
		reader, err := svc.GetFile(ctx, ref)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
	}
}

type bindAfterArtifactResolve struct {
	interfaces.ResourceCatalog
	bind func()
}

func (c *bindAfterArtifactResolve) ResolvePath(ctx context.Context, ref string) (string, *types.StoredResource, error) {
	path, resource, err := c.ResourceCatalog.ResolvePath(ctx, ref)
	if err == nil && c.bind != nil {
		c.bind()
	}
	return path, resource, err
}

func TestArtifactCleanupRechecksBindingsAfterLocationLookup(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "resources.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{}))
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(db))
	svc := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
	ref, err := svc.SaveBytes(ctx, []byte("artifact"), 7, "artifact.png", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerMessage, "old-message", types.ResourceRelationArtifact))
	racing := &bindAfterArtifactResolve{ResourceCatalog: catalog, bind: func() {
		require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "new-owner", types.ResourceRelationAttachment))
	}}
	handler := &Handler{fileService: svc, resourceCatalog: racing}
	handler.reclaimArtifactBlobs(ctx, 7, []types.ArtifactBlobRef{{URL: ref, MessageIDs: []string{"old-message"}}})
	reader, err := svc.GetFile(ctx, ref)
	require.NoError(t, err, "an owner added after the advisory count must protect the artifact")
	require.NoError(t, reader.Close())
}

type retryArtifactDeleteStorage struct {
	interfaces.FileService
	fail bool
}

func (s *retryArtifactDeleteStorage) DeleteFile(ctx context.Context, ref string) error {
	if s.fail {
		s.fail = false
		return errors.New("provider delete unavailable")
	}
	return s.FileService.DeleteFile(ctx, ref)
}

func TestArtifactCleanupRetriesPhysicalDeletionAfterTombstone(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "resources.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{}))
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(db))
	raw := &retryArtifactDeleteStorage{FileService: files.NewLocalFileService(t.TempDir(), ""), fail: true}
	svc := files.NewResourceCatalogFileService(raw, catalog)
	ref, err := svc.SaveBytes(ctx, []byte("artifact"), 7, "report.txt", false)
	require.NoError(t, err)
	physical, _, err := catalog.ResolvePath(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerMessage, "message", types.ResourceRelationArtifact))
	handler := &Handler{fileService: svc, resourceCatalog: catalog}
	refs := []types.ArtifactBlobRef{{URL: ref, MessageIDs: []string{"message"}}}
	handler.reclaimArtifactBlobs(ctx, 7, refs)
	_, err = catalog.Resolve(ctx, ref)
	require.Error(t, err, "cleanup commits the tombstone before the provider operation")
	reader, err := raw.GetFile(ctx, physical)
	require.NoError(t, err, "failed physical removal must remain retryable")
	require.NoError(t, reader.Close())
	handler.reclaimArtifactBlobs(ctx, 7, refs)
	_, err = raw.GetFile(ctx, physical)
	require.ErrorIs(t, err, os.ErrNotExist)
	handler.reclaimArtifactBlobs(ctx, 7, refs)
}
