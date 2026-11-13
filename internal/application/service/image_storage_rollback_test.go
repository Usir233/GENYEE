package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository"
	files "github.com/Usir233/GENYEE/internal/application/service/file"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/sandbox"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type rollbackFailImageStorage struct {
	interfaces.FileService
	fail   bool
	calls  int
	failAt int
	cancel context.CancelFunc
}

func (f *rollbackFailImageStorage) SaveBytes(ctx context.Context, data []byte, tenantID uint64, name string, temp bool) (string, error) {
	f.calls++
	if f.fail || (f.failAt > 0 && f.calls == f.failAt) {
		if f.cancel != nil {
			f.cancel()
		}
		return "", errors.New("injected upload failure")
	}
	return f.FileService.SaveBytes(ctx, data, tenantID, name, temp)
}

func TestSharedImageSurvivesResolverRollback(t *testing.T) {
	ctx := context.Background()
	catalog, db := newResourceCatalogForTest(t)
	baseDir := t.TempDir()
	raw := &rollbackFailImageStorage{FileService: files.NewLocalFileService(baseDir, "")}
	svc := files.NewResourceCatalogFileService(raw, catalog)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 200, 150))))
	data := buf.Bytes()
	ref, err := svc.SaveBytes(ctx, data, 7, "existing-document-image.png", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "existing-document", types.ResourceRelationExtractedImage))
	physical, res, err := catalog.ResolvePath(ctx, ref)
	require.NoError(t, err)
	r, err := svc.GetFile(ctx, ref)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, data, got)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	other := image.NewRGBA(image.Rect(0, 0, 200, 150))
	other.Set(50, 50, color.RGBA{R: 255, A: 255})
	var otherBuf bytes.Buffer
	require.NoError(t, png.Encode(&otherBuf, other))
	secondURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(otherBuf.Bytes())
	content := fmt.Sprintf("![old](%s)\n![new](%s)", uri, secondURI)
	raw.fail = true
	_, images, err := docparser.NewImageResolver().ResolveAndStore(ctx, &types.ReadResult{MarkdownContent: content}, svc, 7)
	require.ErrorContains(t, err, "injected upload failure")
	require.Len(t, images, 1)
	require.Equal(t, ref, images[0].ServingURL, "first save reused the existing bound image")
	var bindings int64
	require.NoError(t, db.Model(&types.ResourceBinding{}).Where("resource_id = ?", res.ID).Count(&bindings).Error)
	require.EqualValues(t, 1, bindings, "old document binding still exists")
	physicalFile := filepath.Join(baseDir, physical[len("local://"):])
	_, statErr := os.Stat(physicalFile)
	_, resolveErr := catalog.Resolve(ctx, ref)
	t.Logf("After second-document rollback: old binding count=%d, physical file error=%v, catalog resolve error=%v", bindings, statErr, resolveErr)
	require.NoError(t, statErr, "a failed second document must not erase the first document's image")
}

func TestInlineGuardCleanupPreservesBoundImage(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	raw := files.NewLocalFileService(t.TempDir(), "")
	svc := files.NewResourceCatalogFileService(raw, catalog)
	ctx := context.Background()
	ref, err := svc.SaveBytes(ctx, []byte("guard-image"), 7, "guard.png", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "existing", types.ResourceRelationExtractedImage))
	cleanupUnboundInlineImages(ctx, svc, []docparser.StoredImage{{ServingURL: ref, Inline: true, NewlyStored: true}})
	reader, err := svc.GetFile(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
}

type rollbackDeleteStorage struct {
	interfaces.FileService
	failure      error
	failPath     string
	beforeDelete func()
}

func (s *rollbackDeleteStorage) DeleteFile(ctx context.Context, path string) error {
	if s.beforeDelete != nil {
		s.beforeDelete()
	}
	if s.failure != nil && (s.failPath == "" || s.failPath == path) {
		return s.failure
	}
	return s.FileService.DeleteFile(ctx, path)
}

func TestUnboundImageCleanupFailureIsRetryable(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	raw := &rollbackDeleteStorage{FileService: files.NewLocalFileService(t.TempDir(), "")}
	svc := files.NewResourceCatalogFileService(raw, catalog)
	ctx := context.Background()
	ref, err := svc.SaveBytes(ctx, []byte("orphan"), 7, "orphan.png", false)
	require.NoError(t, err)
	physical, _, err := catalog.ResolvePath(ctx, ref)
	require.NoError(t, err)
	raw.failure = errors.New("provider temporarily unavailable")
	require.ErrorIs(t, interfaces.DeleteUnboundFile(ctx, svc, ref), raw.failure)
	_, err = catalog.Resolve(ctx, ref)
	require.Error(t, err, "a tombstoned image cannot be reused during deletion")
	require.Error(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "late", types.ResourceRelationExtractedImage))
	reader, err := raw.GetFile(ctx, physical)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	raw.failure = nil
	require.NoError(t, interfaces.DeleteUnboundFile(ctx, svc, ref))
	_, err = raw.GetFile(ctx, physical)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, interfaces.DeleteUnboundFile(ctx, svc, ref), "cleanup is idempotent")
}

func TestUnboundImageCleanupBlocksReuseAndBindDuringPhysicalDelete(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	raw := &rollbackDeleteStorage{FileService: files.NewLocalFileService(t.TempDir(), "")}
	svc := files.NewResourceCatalogFileService(raw, catalog)
	ctx := context.Background()
	data := []byte("concurrent image")
	ref, err := svc.SaveBytes(ctx, data, 7, "image.png", false)
	require.NoError(t, err)
	deleting, resume := make(chan struct{}), make(chan struct{})
	raw.beforeDelete = func() { close(deleting); <-resume }
	done := make(chan error, 1)
	go func() { done <- interfaces.DeleteUnboundFile(ctx, svc, ref) }()
	<-deleting
	require.Error(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "racing", types.ResourceRelationExtractedImage))
	reused, err := svc.SaveBytes(ctx, data, 7, "other.png", false)
	require.NoError(t, err)
	require.NotEqual(t, ref, reused, "a save during physical deletion must return a new live resource")
	require.NoError(t, catalog.Bind(ctx, reused, types.ResourceOwnerKnowledge, "racing", types.ResourceRelationExtractedImage))
	close(resume)
	require.NoError(t, <-done)
	var bindings int64
	require.NoError(t, db.Model(&types.ResourceBinding{}).Count(&bindings).Error)
	require.EqualValues(t, 1, bindings)
	reader, err := svc.GetFile(ctx, reused)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
}

func TestImageDeduplicationDoesNotCrossStorageBackends(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	ctx := context.Background()
	first := files.NewResourceCatalogFileService(files.NewBackendScopedFileService("first", files.NewLocalFileService(t.TempDir(), "")), catalog)
	second := files.NewResourceCatalogFileService(files.NewBackendScopedFileService("second", files.NewLocalFileService(t.TempDir(), "")), catalog)
	data := []byte("image contents")
	ref, err := first.SaveBytes(ctx, data, 7, "image.png", false)
	require.NoError(t, err)
	other, err := second.SaveBytes(ctx, data, 7, "image.png", false)
	require.NoError(t, err)
	reader, err := second.GetFile(ctx, other)
	require.NoError(t, err, "the returned resource must be accessible through the storage service that returned it")
	require.NoError(t, reader.Close())
	require.NotEqual(t, ref, other)
}

func TestResolverRollbackCleansNewUploadsAfterCancellation(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(fmt.Sprint(managed), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			base := t.TempDir()
			raw := &rollbackFailImageStorage{FileService: files.NewLocalFileService(base, ""), failAt: 2, cancel: cancel}
			var svc interfaces.FileService = raw
			if managed {
				catalog, _ := newResourceCatalogForTest(t)
				svc = files.NewResourceCatalogFileService(raw, catalog)
			}
			first := inlineImageTestPNG(t)
			var second bytes.Buffer
			pixels := image.NewRGBA(image.Rect(0, 0, 200, 150))
			pixels.Set(50, 50, color.RGBA{R: 255, A: 255})
			require.NoError(t, png.Encode(&second, pixels))
			content := fmt.Sprintf("![first](data:image/png;base64,%s)\n![second](data:image/png;base64,%s)", base64.StdEncoding.EncodeToString(first), base64.StdEncoding.EncodeToString(second.Bytes()))
			_, images, err := docparser.NewImageResolver().ResolveAndStore(ctx, &types.ReadResult{MarkdownContent: content}, svc, 7)
			require.ErrorContains(t, err, "injected upload failure")
			require.Len(t, images, 1)
			require.True(t, images[0].NewlyStored)
			var filesOnDisk []string
			require.NoError(t, filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					filesOnDisk = append(filesOnDisk, path)
				}
				return nil
			}))
			require.Empty(t, filesOnDisk, "rollback must remove the newly created orphan even after cancellation")
		})
	}
}

func TestStaleResourceBindingCannotReviveDeletedResource(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	ctx := context.Background()
	ref, err := catalog.Register(ctx, 7, "local://7/stale.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	stale, err := catalog.Resolve(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, catalog.MarkDeleted(ctx, ref))
	err = repository.NewResourceRepository(db).CreateBinding(ctx, &types.ResourceBinding{
		ResourceID: stale.ID, TenantID: 7, OwnerType: types.ResourceOwnerKnowledge, OwnerID: "racing", Relation: types.ResourceRelationExtractedImage,
	})
	require.Error(t, err, "a Bind that resolved before deletion must recheck state under the cleanup lock")
	var count int64
	require.NoError(t, db.Model(&types.ResourceBinding{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestManualImageBindingFailureStopsChunkPersistence(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 1})
	knowledge := &types.Knowledge{ID: "manual-bind-fails", TenantID: 1, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing}
	catalog := &fakeBindCatalog{resources: map[string]*types.StoredResource{}, bindErr: map[string]error{"local://test/image-001.png": errors.New("resource was removed before bind")}}
	storage := &inlineImageStorageState{catalog: catalog}
	chunks := &inlineImageChunkRepo{}
	svc := &knowledgeService{
		repo: &embedFailureKnowledgeRepo{knowledge: knowledge}, fileSvc: inlineImageFileService{state: storage},
		imageResolver: docparser.NewImageResolver(), resourceCatalog: catalog, chunkRepo: chunks,
		modelService: embedFailureModelService{err: errors.New("embedding should be disabled")},
		graphEngine:  parentChildGraphRepo{}, tenantRepo: parentChildTenantRepo{}, task: &slotReleaseEnqueuer{},
	}
	useLiteCounter(t, knowledge.ID)
	content := fmt.Sprintf("![image](data:image/png;base64,%s)", base64.StdEncoding.EncodeToString(inlineImageTestPNG(t)))
	err := svc.triggerManualProcessing(ctx, &types.KnowledgeBase{ID: "kb", TenantID: 1}, knowledge, content, true)
	require.ErrorContains(t, err, "resource was removed before bind")
	require.False(t, chunks.created, "a successful document must never contain an image whose binding failed")
}

type partialImageBindingCatalog struct {
	interfaces.ResourceCatalog
	calls int
	seen  map[string]*types.StoredResource
}

func (c *partialImageBindingCatalog) Bind(ctx context.Context, ref, ownerType, ownerID, relation string) error {
	resource, err := c.Resolve(ctx, ref)
	if err != nil {
		return err
	}
	c.seen[ref] = resource
	if relation == types.ResourceRelationExtractedImage {
		c.calls++
		if c.calls == 3 {
			return errors.New("third image binding failed")
		}
	}
	return c.ResourceCatalog.Bind(ctx, ref, ownerType, ownerID, relation)
}

func TestPartialImageBindingFailureCleansOnlyNewUploads(t *testing.T) {
	for _, ownerType := range []string{types.ResourceOwnerKnowledge, types.ResourceOwnerTemporaryDocument} {
		t.Run(ownerType, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 7})
			catalog, db := newResourceCatalogForTest(t)
			raw := files.NewLocalFileService(t.TempDir(), "")
			storage := files.NewResourceCatalogFileService(raw, catalog)
			payloads := make([][]byte, 3)
			content := ""
			for i := range payloads {
				img := image.NewRGBA(image.Rect(0, 0, 200, 150))
				img.Set(50, 50, color.RGBA{R: uint8(i + 1), A: 255})
				var buffer bytes.Buffer
				require.NoError(t, png.Encode(&buffer, img))
				payloads[i] = buffer.Bytes()
				content += fmt.Sprintf("![image](data:image/png;base64,%s)\n", base64.StdEncoding.EncodeToString(payloads[i]))
			}
			oldRef, err := storage.SaveBytes(ctx, payloads[0], 7, "old.png", false)
			require.NoError(t, err)
			require.NoError(t, catalog.Bind(ctx, oldRef, ownerType, "document", types.ResourceRelationExtractedImage))
			faults := &partialImageBindingCatalog{ResourceCatalog: catalog, seen: make(map[string]*types.StoredResource)}
			if ownerType == types.ResourceOwnerKnowledge {
				knowledge := &types.Knowledge{ID: "document", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing}
				svc := &knowledgeService{
					repo: &embedFailureKnowledgeRepo{knowledge: knowledge}, fileSvc: storage,
					imageResolver: docparser.NewImageResolver(), resourceCatalog: faults,
					chunkRepo: &inlineImageChunkRepo{}, task: &slotReleaseEnqueuer{},
				}
				useLiteCounter(t, knowledge.ID)
				err = svc.triggerManualProcessing(ctx, &types.KnowledgeBase{ID: "kb", TenantID: 7}, knowledge, content, true)
			} else {
				source, saveErr := storage.SaveBytes(ctx, []byte(content), 7, "document.md", false)
				require.NoError(t, saveErr)
				options, marshalErr := json.Marshal(types.TemporaryDocumentCreateOptions{ParserEngine: "simple"})
				require.NoError(t, marshalErr)
				svc := &temporaryDocumentService{fileService: storage, resourceCatalog: faults, imageResolver: docparser.NewImageResolver()}
				_, images, _, parseErr := svc.parse(ctx, &types.TemporaryDocument{
					ID: "document", TenantID: 7, ResourceRef: source, FileName: "document.md", FileType: ".md", ProcessingOptions: options,
				})
				require.Empty(t, images, "failed attempt has no persisted image list to clean up later")
				err = parseErr
			}
			require.ErrorContains(t, err, "third image binding failed")
			require.Len(t, faults.seen, 3)
			for ref, resource := range faults.seen {
				var bindings int64
				require.NoError(t, db.Model(&types.ResourceBinding{}).Where("resource_id = ?", resource.ID).Count(&bindings).Error)
				reader, readErr := raw.GetFile(ctx, resource.PhysicalPath)
				if ref == oldRef {
					require.Positive(t, bindings, "preexisting owner claims must remain")
					require.NoError(t, readErr)
					require.NoError(t, reader.Close())
				} else {
					if reader != nil {
						_ = reader.Close()
					}
					require.Zero(t, bindings, "partly bound new image must not become an unreachable owner claim")
					require.ErrorIs(t, readErr, os.ErrNotExist, "new failed-attempt bytes must be reclaimed")
				}
			}
		})
	}
}

func TestOwnerlessImageUploadsCannotShareCleanupLifetime(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	svc := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
	ctx := context.Background()
	data := []byte("unbound attachment preview")
	before, err := svc.SaveBytes(ctx, data, 7, "before.png", false)
	require.NoError(t, err)
	preview, err := svc.SaveBytes(interfaces.WithIndependentFileWrites(ctx), data, 7, "preview.png", false)
	require.NoError(t, err)
	require.NotEqual(t, before, preview, "ownerless previews must not reuse an upload another request can clean up")
	require.NoError(t, interfaces.DeleteUnboundFile(ctx, svc, before))
	after, err := svc.SaveBytes(ctx, data, 7, "after.png", false)
	require.NoError(t, err)
	require.NotEqual(t, preview, after, "later owners must not share an unbound preview's cleanup lifetime")
	require.NoError(t, interfaces.DeleteUnboundFile(ctx, svc, after))
	reader, err := svc.GetFile(ctx, preview)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
}

func TestParentResourceCleanupRetainsFailedChildForRetry(t *testing.T) {
	ctx := context.Background()
	catalog, _ := newResourceCatalogForTest(t)
	raw := &rollbackDeleteStorage{FileService: files.NewLocalFileService(t.TempDir(), "")}
	svc := files.NewResourceCatalogFileService(raw, catalog)
	parent, err := svc.SaveBytes(ctx, []byte("parent"), 7, "document.pdf", false)
	require.NoError(t, err)
	child, err := svc.SaveBytes(ctx, []byte("child"), 7, "image.png", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, child, types.ResourceOwnerResource, parent, types.ResourceRelationExtractedImage))
	raw.failPath, _, err = catalog.ResolvePath(ctx, child)
	require.NoError(t, err)
	raw.failure = errors.New("child provider unavailable")
	require.ErrorIs(t, svc.DeleteFile(ctx, parent), raw.failure)
	pending, err := catalog.(interfaces.UnboundResourceCatalog).ListDeletionDependents(ctx, parent)
	require.NoError(t, err)
	require.Equal(t, []string{child}, pending, "failed child deletion must remain discoverable from the parent")
	raw.failure = nil
	require.NoError(t, svc.DeleteFile(ctx, parent), "retrying the parent must complete child cleanup")
	pending, err = catalog.(interfaces.UnboundResourceCatalog).ListDeletionDependents(ctx, parent)
	require.NoError(t, err)
	require.Empty(t, pending)
	_, err = raw.GetFile(ctx, raw.failPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, svc.DeleteFile(ctx, parent), "physical duplicate deletion must be idempotent")
}

func TestKnowledgeSourceDeletionRespectsOtherOwners(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			catalog, db := newResourceCatalogForTest(t)
			raw := files.NewLocalFileService(t.TempDir(), "")
			svc := files.NewResourceCatalogFileService(raw, catalog)
			ref, err := svc.SaveBytes(f.ctx, []byte("source file bytes"), 7, "source.txt", false)
			require.NoError(t, err)
			require.NoError(t, catalog.Bind(f.ctx, ref, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationSourceFile))
			if shared {
				require.NoError(t, catalog.Bind(f.ctx, ref, types.ResourceOwnerMessage, "other-message", types.ResourceRelationAttachment))
			}
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			row.FilePath = ref
			require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
			f.svc.fileSvc = svc
			f.svc.resourceCatalog = catalog
			require.NoError(t, f.svc.DeleteKnowledge(f.ctx, "doc"))
			reader, err := svc.GetFile(f.ctx, ref)
			if shared {
				require.NoError(t, err, "removing a source document must preserve another owner's file")
				require.NoError(t, reader.Close())
			} else {
				require.Error(t, err, "the final source owner must not leak its file")
			}
			var ownerBindings int64
			require.NoError(t, db.Model(&types.ResourceBinding{}).Where("owner_type = ? AND owner_id = ?", types.ResourceOwnerKnowledge, "doc").Count(&ownerBindings).Error)
			require.Zero(t, ownerBindings, "source_file bindings must be released with the document")
		})
	}
}

type artifactSaveInterleaving struct {
	interfaces.FileService
	afterSave func(string)
}

func (s *artifactSaveInterleaving) SaveBytesWithOwnership(ctx context.Context, data []byte, tenant uint64, name string, temp bool) (string, bool, error) {
	ref, created, err := interfaces.SaveBytesWithOwnership(ctx, s.FileService, data, tenant, name, temp)
	if err == nil && s.afterSave != nil {
		s.afterSave(ref)
	}
	return ref, created, err
}
func (s *artifactSaveInterleaving) SaveBytes(ctx context.Context, data []byte, tenant uint64, name string, temp bool) (string, error) {
	ref, _, err := s.SaveBytesWithOwnership(ctx, data, tenant, name, temp)
	return ref, err
}

func TestNewArtifactCannotPublishAResourceRemovedBeforeBinding(t *testing.T) {
	ctx := context.Background()
	catalog, _ := newResourceCatalogForTest(t)
	svc := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
	data := []byte("racing artifact image")
	original, err := svc.SaveBytes(ctx, data, 7, "original.png", false)
	require.NoError(t, err)
	racing := &artifactSaveInterleaving{FileService: svc, afterSave: func(ref string) {
		require.Equal(t, original, ref)
		require.NoError(t, interfaces.DeleteUnboundFile(ctx, svc, original))
	}}
	collector := &ArtifactCollector{fileService: racing, catalog: catalog}
	_, ok := collector.persistBytes(ctx, "session", "message", 7, sandbox.RemoteDirEntry{Name: "artifact.png"}, data, "hash")
	require.False(t, ok, "a new artifact must not be returned after its reused resource was cleaned before binding")
}
