package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/common"
	werrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/infrastructure/chunker"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/google/uuid"
)

func (s *knowledgeService) cloneKnowledge(
	ctx context.Context,
	src *types.Knowledge,
	targetKB *types.KnowledgeBase,
) (err error) {
	sourceKB, err := knowledgeWriteKB(ctx, s.kbService, src)
	if err != nil {
		return err
	}
	if err := access.RequireKBTransfer(ctx, sourceKB, targetKB, access.KBTransferClone); err != nil {
		return err
	}
	if src.ParseStatus != types.ParseStatusCompleted {
		return fmt.Errorf("source knowledge %s is not completed", src.ID)
	}
	if _, err := s.transferChunks(ctx, src, sourceKB.ID); err != nil {
		return err
	}
	dst := &types.Knowledge{
		ID:               uuid.NewString(),
		TenantID:         targetKB.TenantID,
		KnowledgeBaseID:  targetKB.ID,
		Type:             src.Type,
		Channel:          src.Channel,
		Title:            src.Title,
		Description:      src.Description,
		Profile:          src.Profile.Clone(),
		Source:           src.Source,
		ParseStatus:      "processing",
		EnableStatus:     "disabled",
		EmbeddingModelID: targetKB.EmbeddingModelID,
		FileName:         src.FileName,
		FolderPath:       src.FolderPath,
		FileType:         src.FileType,
		FileSize:         src.FileSize,
		FileHash:         src.FileHash,
		FilePath:         src.FilePath,
		StorageSize:      0,
		Metadata:         src.Metadata,
		CustomMetadata:   src.CustomMetadata,
	}

	state := knowledgeTransferState{
		TaskID:    access.TransferTaskID(ctx),
		Operation: access.KBTransferClone,
		SourceKB:  sourceKB.ID,
		TargetKB:  targetKB.ID,
		SourceID:  src.ID,
		Phase:     "cloning",
	}
	if err := setTransferState(dst, state); err != nil {
		return err
	}

	// Deep-copy the source document file into an object owned by the destination
	// knowledge. Without this the clone only shares the source's storage path, so
	// deleting the source knowledge would destroy the clone's file too. The new
	// object is tracked for cleanup if the clone fails downstream.
	var copiedFilePaths []string
	if src.FilePath != "" {
		srcKB, kbErr := s.kbService.GetKnowledgeBaseByID(ctx, src.KnowledgeBaseID)
		if kbErr != nil {
			return fmt.Errorf("clone knowledge: failed to load source knowledge base: %w", kbErr)
		}
		srcSvc := s.resolveFileServiceForPath(ctx, srcKB, src.FilePath)
		dstSvc := s.resolveFileService(ctx, targetKB)
		newPath, copyErr := copyOwnedObject(
			ctx, srcSvc, dstSvc, src.FilePath, targetKB.TenantID, dst.ID,
			s.resourceCatalog, types.ResourceRelationSourceFile)
		if copyErr != nil {
			return fmt.Errorf("clone knowledge file copy failed: %w", copyErr)
		}
		dst.FilePath = newPath
		copiedFilePaths = append(copiedFilePaths, newPath)
	}

	defer func() {
		if err == nil {
			return
		}
		stored, loadErr := s.repo.GetKnowledgeByID(ctx, dst.TenantID, dst.ID)
		if errors.Is(loadErr, repository.ErrKnowledgeNotFound) {
			cleanupCopiedObjects(
				ctx, s.resolveFileService(ctx, targetKB),
				knowledgeResourceOwners(s.resourceCatalog, dst.ID), copiedFilePaths,
			)
			return
		}
		// An uncertain save/read must not delete objects that may be referenced.
		if loadErr != nil || stored == nil || validateTransferKnowledge(stored, targetKB) != nil {
			return
		}
		marker, markerErr := transferState(stored)
		if markerErr != nil || !matchesTransfer(ctx, marker, sourceKB, targetKB, access.KBTransferClone, src.ID, "") ||
			stored.ParseStatus == types.ParseStatusCompleted {
			return
		}
		before, after := *stored, *stored
		after.ParseStatus = types.ParseStatusFailed
		after.ErrorMessage = err.Error()
		if updErr := s.repo.UpdateKnowledgeForTransfer(ctx, &before, &after); updErr != nil {
			logger.Warnf(ctx, "Failed to mark cloned knowledge %s failed: %v", dst.ID, updErr)
		}
	}()
	if err = s.repo.CreateKnowledge(ctx, dst); err != nil {
		return err
	}
	// Create timestamps can be rounded by the database before the first CAS.
	persisted, err := s.repo.GetKnowledgeByID(ctx, dst.TenantID, dst.ID)
	if err != nil {
		return err
	}
	if err := validateTransferKnowledge(persisted, targetKB); err != nil {
		return err
	}
	dst.UpdatedAt = persisted.UpdatedAt
	if err = s.CloneChunk(ctx, src, dst); err != nil {
		return err
	}
	before := *dst
	state.Phase = "done"
	if err = setTransferState(dst, state); err != nil {
		return err
	}
	dst.ParseStatus = types.ParseStatusCompleted
	dst.EnableStatus = "enabled"
	dst.StorageSize = src.StorageSize
	return s.repo.UpdateKnowledgeForTransfer(ctx, &before, dst)
}

// processDocumentFromPassage processes text passages and reports processing failures.
func (s *knowledgeService) processDocumentFromPassage(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, passage []string,
) error {
	// Update status to processing
	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return fmt.Errorf("update passage processing status: %w", err)
	}

	// Convert passages to chunks
	chunks := make([]types.ParsedChunk, 0, len(passage))
	start, end := 0, 0
	for i, p := range passage {
		if p == "" {
			continue
		}
		end += len([]rune(p))
		chunks = append(chunks, types.ParsedChunk{
			Content: p,
			Seq:     i,
			Start:   start,
			End:     end,
		})
		start = end
	}
	// Process and store chunks
	var opts ProcessChunksOptions
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
		opts.EnableQuestionGeneration = true
		opts.QuestionCount = kb.QuestionGenerationConfig.QuestionCount
		if opts.QuestionCount <= 0 {
			opts.QuestionCount = 3
		}
	}
	if err := s.processChunks(ctx, kb, knowledge, chunks, opts); err != nil {
		return fmt.Errorf("process passages for knowledge %s: %w", knowledge.ID, err)
	}
	// Terminal pipeline failures are acknowledged for asynchronous tasks after
	// their failed state is saved. A synchronous caller still needs that error.
	if knowledge.ParseStatus == types.ParseStatusFailed {
		if knowledge.ErrorMessage != "" {
			return errors.New(knowledge.ErrorMessage)
		}
		return errors.New("passage processing failed")
	}
	return nil
}

// ProcessChunksOptions contains options for processing chunks
type ProcessChunksOptions struct {
	EnableQuestionGeneration bool
	QuestionCount            int
	EnableMultimodel         bool
	StoredImages             []docparser.StoredImage
	// ParentChunks holds parent chunk data when parent-child chunking is enabled.
	// When set, the chunks passed to processChunks are child chunks, and each
	// child's ParentIndex references an entry in this slice.
	ParentChunks []types.ParsedParentChunk
	Metadata     map[string]string
}

func imagesForMultimodal(images []docparser.StoredImage) []docparser.StoredImage {
	selected := make([]docparser.StoredImage, 0, len(images))
	for _, image := range images {
		if !image.SkipMultimodal {
			selected = append(selected, image)
		}
	}
	return selected
}

func cleanupUnboundInlineImages(
	ctx context.Context, fileSvc interfaces.FileService, images []docparser.StoredImage,
) {
	if fileSvc == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	deleted := make(map[string]struct{})
	for _, image := range images {
		if !image.NewlyStored || image.ServingURL == "" {
			continue
		}
		if _, exists := deleted[image.ServingURL]; exists {
			continue
		}
		deleted[image.ServingURL] = struct{}{}
		if err := interfaces.DeleteUnboundFile(cleanupCtx, fileSvc, image.ServingURL); err != nil {
			logger.Warnf(ctx, "Failed to clean up unbound inline image %s: %v", image.ServingURL, err)
		}
	}
}

// A failed binding batch may already own some of its newly uploaded images.
// Drop only those claims before cleanup; a reused image can have a preexisting
// claim from this document that must survive the failed attempt.
func cleanupFailedImageBindings(
	ctx context.Context, fileSvc interfaces.FileService, catalog interfaces.ResourceCatalog,
	ownerType, ownerID string, images []docparser.StoredImage,
) {
	if fileSvc == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	cleaned := make(map[string]bool)
	for _, image := range images {
		if !image.NewlyStored || image.ServingURL == "" || cleaned[image.ServingURL] {
			continue
		}
		cleaned[image.ServingURL] = true
		if _, managed := types.ParseResourcePath(image.ServingURL); managed && catalog != nil {
			if _, err := catalog.Release(cleanupCtx, image.ServingURL, ownerType, ownerID); err != nil {
				logger.Warnf(ctx, "Failed to release image from failed binding batch %s: %v", image.ServingURL, err)
				continue
			}
		}
		if err := interfaces.DeleteUnboundFile(cleanupCtx, fileSvc, image.ServingURL); err != nil {
			logger.Warnf(ctx, "Failed to clean image from failed binding batch %s: %v", image.ServingURL, err)
		}
	}
}

// finalizeIndexedKnowledgeState makes a document retrievable as soon as chunks
// and indexes are persisted (enable_status=enabled), but it deliberately does
// NOT mark the row completed when enrichment is still expected. Whenever the
// document still has work to fan out — pending multimodal image tasks, or text
// chunks that feed summary/question/graph generation — parse_status stays
// "processing" so KnowledgePostProcess remains the single authority that drives
// processing → finalizing → completed. Marking the row completed here would make
// post-process hit its "non-processing status" guard and skip the summary
// fan-out, stranding summary_status on "pending" forever.
func finalizeIndexedKnowledgeState(
	knowledge *types.Knowledge,
	totalStorageSize int64,
	textChunkCount int,
	hasPendingMultimodal bool,
	now time.Time,
) {
	if hasPendingMultimodal || textChunkCount > 0 {
		knowledge.ParseStatus = types.ParseStatusProcessing
		knowledge.SummaryStatus = types.SummaryStatusNone
	} else {
		// No text chunks and no pending multimodal work: there is nothing for
		// post-process to enrich, so complete immediately. This is the only
		// route to 'completed' that bypasses FinalizeSubtask, so it has to
		// clear error_message itself — otherwise a successfully indexed row
		// keeps reporting a failure from an earlier attempt.
		knowledge.ParseStatus = types.ParseStatusCompleted
		knowledge.SummaryStatus = types.SummaryStatusNone
		knowledge.ErrorMessage = ""
	}

	knowledge.EnableStatus = "enabled"
	knowledge.StorageSize = totalStorageSize
	knowledge.ProcessedAt = &now
	knowledge.UpdatedAt = now
}

// markKnowledgeProcessing makes the top-level knowledge state describe the
// attempt that is starting now. Clearing error_message is part of that: the
// column is only meaningful for the attempt that produced it, so a row
// re-entering processing must not keep surfacing the previous attempt's
// failure while it is visibly running again.
func markKnowledgeProcessing(knowledge *types.Knowledge, now time.Time) {
	knowledge.ParseStatus = types.ParseStatusProcessing
	knowledge.ErrorMessage = ""
	knowledge.UpdatedAt = now
}

// failKnowledgeOnEmbeddingModel records an unresolvable embedding model as this
// attempt's terminal state.
//
// It re-reads the row instead of trusting the entry guard, because resolving a
// model reads the database: a cancel or a ReplaceKnowledgeFile can land in the
// window between that guard and this write. Both would be clobbered otherwise —
// UpdateKnowledge is a full-row Save and file_path is not among the omitted
// columns, so a stale in-memory row would roll file_path back over the
// replacement and mark the replacement's live attempt failed.
func (s *knowledgeService) failKnowledgeOnEmbeddingModel(
	ctx context.Context, kb *types.KnowledgeBase, knowledge *types.Knowledge, cause error,
) error {
	return s.failKnowledgeAtEmbedding(ctx, kb, knowledge, werrors.ErrCodeEmbeddingProviderFail,
		"failed to get embedding model", cause)
}

// failKnowledgeAtEmbedding records cause as this attempt's terminal state at
// the embedding stage; see failKnowledgeOnEmbeddingModel for the guards. It
// returns nil once the attempt is settled (failure recorded, or the row was
// cancelled / deleted / replaced) and an error when nothing could be
// recorded, so the task is retried instead of acked with the row in flight.
func (s *knowledgeService) failKnowledgeAtEmbedding(
	ctx context.Context, kb *types.KnowledgeBase, knowledge *types.Knowledge, code, what string, cause error,
) error {
	// A cancelled or expired context means the run was interrupted — the user
	// cancelled (asynq CancelProcessing cancels the handler context), the
	// worker was preempted, the process is shutting down. The model itself is
	// not implicated, so recording "failed to get embedding model" would both
	// mislabel the cause and overwrite the cancelled status the abort path
	// just wrote. Leave the row to the abort path, or to the housekeeping
	// sweep when nothing else claims it.
	if ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		logger.Infof(ctx,
			"%s interrupted for %s (%v); leaving parse status untouched",
			what, knowledge.ID, cause)
		return fmt.Errorf("%s: %w", what, cause)
	}
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx,
			"Knowledge aborted (%s), not recording %q: %s", status, what, knowledge.ID)
		return abortRetryErr(ctx, knowledge.ID, status)
	}

	knowledge.ParseStatus = types.ParseStatusFailed
	knowledge.ErrorMessage = fmt.Sprintf("%s: %v", what, cause)
	knowledge.UpdatedAt = time.Now()
	if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "failed to persist %q for %s: %v", what, knowledge.ID, err)
		return fmt.Errorf("persist %s: %w", what, err)
	}
	s.beginStage(ctx, knowledge.ID, types.StageEmbedding, types.JSONMap{
		"model_id": kb.EmbeddingModelID,
	})
	// The span's error_message is what the timeline renders, and its
	// error_detail is withheld from non-admin responses — so carry the same
	// text the document list shows rather than a fixed generic string.
	s.failStage(ctx, knowledge.ID, types.StageEmbedding, code, knowledge.ErrorMessage, cause)
	return nil
}

// buildSplitterConfigFromChunking normalizes effective chunking settings with
// the shared chunker defaults.
func buildSplitterConfigFromChunking(cc types.ChunkingConfig) chunker.SplitterConfig {
	return chunker.NormalizeSplitterConfig(chunker.SplitterConfig{
		ChunkSize:    cc.ChunkSize,
		ChunkOverlap: cc.ChunkOverlap,
		Separators:   cc.Separators,
		Strategy:     cc.Strategy,
		TokenLimit:   cc.TokenLimit,
		Languages:    cc.Languages,
	})
}

// buildParentChildConfigs derives parent and child SplitterConfig from ChunkingConfig.
// The base config (already validated with defaults) is used for separators and
// the splitting strategy. Strategy must be propagated: an empty Strategy resolves
// to the legacy tier (see resolveChainWithProfile), which never runs the heading
// splitter, so parent-child chunks would silently lose heading alignment and
// ContextHeader breadcrumbs regardless of the configured strategy.
func buildParentChildConfigs(cc types.ChunkingConfig, base chunker.SplitterConfig) (parent,
	child chunker.SplitterConfig,
) {
	return chunker.DeriveParentChildConfigs(base, cc.ParentChunkSize, cc.ChildChunkSize)
}

// deleteUnindexedChunks drops the chunks processChunks wrote for a knowledge
// that failed before BatchIndex ran. Nothing reached the vector store yet, so
// only the chunk rows need removing; left alone they would stay active under a
// failed knowledge, the same leftovers the BatchIndex failure path cleans up.
func (s *knowledgeService) deleteUnindexedChunks(ctx context.Context, knowledge *types.Knowledge) {
	if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, knowledge.TenantID, knowledge.ID); err != nil {
		logger.Errorf(ctx, "Delete chunks failed: %v", err)
	}
}

// supersededChunkPurger is intentionally narrower than ChunkRepository: only
// persistent repositories that can prove a newer active replacement should
// physically reclaim soft-deleted chunk history.
type supersededChunkPurger interface {
	PurgeSupersededChunksByKnowledgeID(context.Context, uint64, string) (int64, error)
}

// processChunks processes chunks and creates embeddings for knowledge content
func (s *knowledgeService) processChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []types.ParsedChunk,
	opts ...ProcessChunksOptions,
) error {
	// Get options
	var options ProcessChunksOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	// Parser output and manually supplied passages can contain malformed byte
	// sequences. Clean them before logging, chunk persistence, or embedding;
	// the embedding provider and tracing/database drivers expect valid UTF-8.
	for i := range chunks {
		chunks[i].Content = common.CleanInvalidUTF8(chunks[i].Content)
		chunks[i].ContextHeader = common.CleanInvalidUTF8(chunks[i].ContextHeader)
		for j := range chunks[i].Images {
			chunks[i].Images[j].URL = common.CleanInvalidUTF8(chunks[i].Images[j].URL)
			chunks[i].Images[j].Caption = common.CleanInvalidUTF8(chunks[i].Images[j].Caption)
			chunks[i].Images[j].OCRText = common.CleanInvalidUTF8(chunks[i].Images[j].OCRText)
			chunks[i].Images[j].OriginalURL = common.CleanInvalidUTF8(chunks[i].Images[j].OriginalURL)
		}
	}
	for i := range options.ParentChunks {
		options.ParentChunks[i].Content = common.CleanInvalidUTF8(options.ParentChunks[i].Content)
	}

	// Check if knowledge is being deleted/cancelled before processing.
	// Both statuses short-circuit identically here — there's nothing to clean
	// up yet so the branch is purely "stop early".
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "Knowledge aborted (%s), skipping chunk processing: %s", status, knowledge.ID)
		return abortRetryErr(ctx, knowledge.ID, status)
	}
	if s.isKnowledgeSourceReplaced(ctx, knowledge) {
		logger.Infof(ctx, "Knowledge source replaced, skipping chunk processing: %s", knowledge.ID)
		return nil
	}
	// A reparse started after this run: the cleanup below would wipe the
	// new attempt's chunks and the final save would put its status back.
	if s.currentAttemptSuperseded(ctx, knowledge.ID) {
		logger.Infof(ctx, "Parse attempt superseded, skipping chunk processing: %s", knowledge.ID)
		return nil
	}

	// Get embedding model for vectorization — only needed when vector/keyword indexing is enabled
	var embeddingModel embedding.Embedder
	if kb.NeedsEmbeddingModel() {
		var err error
		embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			// Terminal for this attempt, and it has to be recorded as such.
			// A KB that indexes vectors cannot proceed without an embedder;
			// returning without a status leaves parse_status on "processing",
			// so the row hangs until the housekeeping sweep fails it hours
			// later with a generic "stuck in processing" message. Failing
			// here names the real cause instead: a model row that was
			// deleted or deactivated, or a configuration the embedder factory
			// refuses. (Credential and connectivity problems surface later, on
			// the first BatchIndex round-trip — resolving a model only reads
			// the row and constructs a client.)
			logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks get embedding model failed")
			return s.failKnowledgeOnEmbeddingModel(ctx, kb, knowledge, err)
		}
	} else {
		logger.Infof(ctx, "Vector/keyword indexing disabled for KB %s, skipping embedding model", kb.ID)
	}

	// Resolve the vector store before deleting anything: failing after the
	// cleanup below would leave the document with neither old nor new chunks.
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil && embeddingModel != nil {
		// Indexing below dereferences the engine; a nil one would panic.
		logger.Errorf(ctx, "processChunks resolve vector store for KB %s failed: %v", kb.ID, err)
		if errors.Is(err, retriever.ErrVectorStoreUnavailable) {
			// The lookup failed, not the store: retry rather than fail.
			return fmt.Errorf("resolve vector store: %w", err)
		}
		return s.failKnowledgeAtEmbedding(ctx, kb, knowledge, werrors.ErrCodeVectorStoreWriteFailed,
			"failed to resolve vector store", err)
	}

	// 幂等性处理：清理旧的chunks和索引数据，避免重复数据
	logger.Infof(ctx, "Cleaning up existing chunks and index data for knowledge: %s", knowledge.ID)

	// 删除旧的chunks
	if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, knowledge.TenantID, knowledge.ID); err != nil {
		logger.Warnf(ctx, "Failed to delete existing chunks (may not exist): %v", err)
		// 不返回错误，继续处理（可能没有旧数据）
	}

	// 删除旧的索引数据 — only when vector/keyword indexing is enabled
	if embeddingModel != nil {
		if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(),
			knowledge.Type); err != nil {
			logger.Warnf(ctx, "Failed to delete existing index data (may not exist): %v", err)
			// 不返回错误，继续处理（可能没有旧数据）
		} else {
			logger.Infof(ctx, "Successfully deleted existing index data for knowledge: %s", knowledge.ID)
		}
	}

	// 删除知识图谱数据（如果存在）
	namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
	if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		logger.Warnf(ctx, "Failed to delete existing graph data (may not exist): %v", err)
		// 不返回错误，继续处理
	}

	logger.Infof(ctx, "Cleanup completed, starting to process new chunks")

	// ========== DocReader 解析结果日志 ==========
	logger.Infof(ctx, "[DocReader] ========== 解析结果概览 ==========")
	logger.Infof(ctx, "[DocReader] 知识ID: %s, 知识库ID: %s", knowledge.ID, knowledge.KnowledgeBaseID)
	logger.Infof(ctx, "[DocReader] 总Chunk数量: %d", len(chunks))

	// 统计图片信息
	totalImages := 0
	chunksWithImages := 0
	for _, chunkData := range chunks {
		if len(chunkData.Images) > 0 {
			chunksWithImages++
			totalImages += len(chunkData.Images)
		}
	}
	logger.Infof(ctx, "[DocReader] 包含图片的Chunk数: %d, 总图片数: %d", chunksWithImages, totalImages)

	// 打印每个Chunk的详细信息
	for idx, chunkData := range chunks {
		contentPreview := chunkData.Content
		contentPreview = cutLogPreview(contentPreview, 200)
		logger.Infof(ctx, "[DocReader] Chunk #%d (seq=%d): 内容长度=%d, 图片数=%d, 范围=[%d-%d]",
			idx, chunkData.Seq, len(chunkData.Content), len(chunkData.Images), chunkData.Start, chunkData.End)
		logger.Debugf(ctx, "[DocReader] Chunk #%d 内容预览: %s", idx, contentPreview)

		// 打印图片详细信息
		for imgIdx, img := range chunkData.Images {
			logger.Infof(ctx, "[DocReader]   图片 #%d: URL=%s", imgIdx, img.URL)
			logger.Infof(ctx, "[DocReader]   图片 #%d: OriginalURL=%s", imgIdx, img.OriginalURL)
			if img.Caption != "" {
				captionPreview := img.Caption
				captionPreview = cutLogPreview(captionPreview, 100)
				logger.Infof(ctx, "[DocReader]   图片 #%d: Caption=%s", imgIdx, captionPreview)
			}
			if img.OCRText != "" {
				ocrPreview := img.OCRText
				ocrPreview = cutLogPreview(ocrPreview, 100)
				logger.Infof(ctx, "[DocReader]   图片 #%d: OCRText=%s", imgIdx, ocrPreview)
			}
			logger.Infof(ctx, "[DocReader]   图片 #%d: 位置=[%d-%d]", imgIdx, img.Start, img.End)
		}
	}
	logger.Infof(ctx, "[DocReader] ========== 解析结果概览结束 ==========")

	// Create chunk objects from proto chunks
	maxSeq := 0

	// 统计图片相关的子Chunk数量，用于扩展insertChunks的容量
	imageChunkCount := 0
	for _, chunkData := range chunks {
		if len(chunkData.Images) > 0 {
			// 为每个图片的OCR和Caption分别创建一个Chunk
			imageChunkCount += len(chunkData.Images) * 2
		}
		if int(chunkData.Seq) > maxSeq {
			maxSeq = int(chunkData.Seq)
		}
	}

	// === Parent-Child Chunking: create parent chunks first ===
	hasParentChild := len(options.ParentChunks) > 0
	var parentDBChunks []*types.Chunk // indexed by ParsedParentChunk position
	if hasParentChild {
		parentDBChunks = make([]*types.Chunk, len(options.ParentChunks))
		for i, pc := range options.ParentChunks {
			parentDBChunks[i] = &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        knowledge.TenantID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				Content:         pc.Content,
				ChunkIndex:      pc.Seq,
				IsEnabled:       true,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
				StartAt:         pc.Start,
				EndAt:           pc.End,
				ChunkType:       types.ChunkTypeParentText,
				SourceLocators:  pc.SourceLocators,
			}
		}
		// Set prev/next links for parent chunks
		for i := range parentDBChunks {
			if i > 0 {
				parentDBChunks[i-1].NextChunkID = parentDBChunks[i].ID
				parentDBChunks[i].PreChunkID = parentDBChunks[i-1].ID
			}
		}
		logger.Infof(ctx, "Created %d parent chunks for parent-child strategy", len(parentDBChunks))
	}

	// 重新分配容量，考虑图片相关的Chunk + parent chunks
	parentCount := len(options.ParentChunks)
	insertChunks := make([]*types.Chunk, 0, len(chunks)+imageChunkCount+parentCount)
	// Add parent chunks first (they go into DB but NOT into the vector index)
	if hasParentChild {
		insertChunks = append(insertChunks, parentDBChunks...)
	}

	for idx, chunkData := range chunks {
		if strings.TrimSpace(chunkData.Content) == "" {
			continue
		}

		// 创建主文本Chunk
		textChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         chunkData.Content,
			ContextHeader:   chunkData.ContextHeader,
			ChunkIndex:      int(chunkData.Seq),
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         int(chunkData.Start),
			EndAt:           int(chunkData.End),
			ChunkType:       types.ChunkTypeText,
			SourceLocators:  chunkData.SourceLocators,
		}

		// Wire up ParentChunkID for child chunks
		if hasParentChild && chunkData.ParentIndex >= 0 && chunkData.ParentIndex < len(parentDBChunks) {
			textChunk.ParentChunkID = parentDBChunks[chunkData.ParentIndex].ID
		}

		chunks[idx].ChunkID = textChunk.ID
		insertChunks = append(insertChunks, textChunk)
	}

	// Sort chunks by index for proper ordering
	sort.Slice(insertChunks, func(i, j int) bool {
		return insertChunks[i].ChunkIndex < insertChunks[j].ChunkIndex
	})

	// Collect retrievable text chunks only. ParentChunkID only controls parent expansion after retrieval.
	// When ParentChunkID is empty, retrieval keeps the standalone child content without loading a parent.
	textChunks := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range insertChunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}

	// 设置文本Chunk之间的前后关系 (skip if parent-child, children don't need prev/next links)
	if !hasParentChild {
		for i, chunk := range textChunks {
			if i > 0 {
				textChunks[i-1].NextChunkID = chunk.ID
			}
			if i < len(textChunks)-1 {
				textChunks[i+1].PreChunkID = chunk.ID
			}
		}
	}

	// Check if knowledge is being deleted/cancelled before writing chunks.
	// Nothing has been persisted yet, so both branches just bail.
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "Knowledge aborted (%s), skipping chunk write: %s", status, knowledge.ID)
		return abortRetryErr(ctx, knowledge.ID, status)
	}
	if s.isKnowledgeSourceReplaced(ctx, knowledge) {
		logger.Infof(ctx, "Knowledge source replaced, skipping chunk write: %s", knowledge.ID)
		return nil
	}

	// Save chunks to database — ALWAYS, regardless of indexing strategy.
	// Chunks are needed for wiki generation, graph extraction, and summary generation
	// even when vector/keyword indexing is disabled.
	s.beginStage(ctx, knowledge.ID, types.StageChunking, types.JSONMap{
		"chunks_planned": len(insertChunks),
	})
	if err := s.chunkRepo.CreateChunks(ctx, insertChunks); err != nil {
		knowledge.ParseStatus = types.ParseStatusFailed
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.updateKnowledgeBestEffort(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageChunking,
			werrors.ErrCodeChunkingFailed, "create chunks failed", err)
		return nil
	}
	totalChunkChars := 0
	for _, c := range insertChunks {
		totalChunkChars += len(c.Content)
	}
	s.endStage(ctx, knowledge.ID, types.StageChunking, types.JSONMap{
		"chunks_written":   len(insertChunks),
		"total_text_chars": totalChunkChars,
	})

	// Create index information and perform vector indexing — only when vector/keyword is enabled.
	// Chunks are ALWAYS saved to DB (above) because wiki and graph need them even without vector indexing.
	var totalStorageSize int64
	if kb.NeedsEmbeddingModel() && embeddingModel != nil {
		embedInput := types.JSONMap{
			"chunks_to_embed": len(textChunks),
			"model_id":        kb.EmbeddingModelID,
		}
		if dim := embeddingModel.GetDimensions(); dim > 0 {
			embedInput["dim"] = dim
		}
		s.beginStage(ctx, knowledge.ID, types.StageEmbedding, embedInput)
		// Create index information — only for child/flat chunks, NOT parent chunks.
		// Parent chunks are stored for context retrieval but do not need vector embeddings.
		// Prepend the document title to improve semantic alignment between
		// question-style queries and statement-style chunk content.
		const indexWindowSize = 200
		buildIndexWindow := func(chunks []*types.Chunk) []*types.IndexInfo {
			infos := make([]*types.IndexInfo, 0, len(chunks))
			for _, chunk := range chunks {
				// chunk.EmbeddingContent prepends ContextHeader (heading breadcrumb)
				// when the chunker populated it during Tier-1 splitting; falls back
				// to plain Content otherwise. The document title sits outermost;
				// custom metadata remains document-scoped model context.
				indexContent := buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent())
				infos = append(infos, &types.IndexInfo{
					Content:         indexContent,
					SourceID:        chunk.ID,
					SourceType:      types.ChunkSourceType,
					ChunkID:         chunk.ID,
					KnowledgeID:     knowledge.ID,
					KnowledgeBaseID: knowledge.KnowledgeBaseID,
					IsEnabled:       true,
				})
			}
			return infos
		}

		// Estimate the full quota before any index writes, but bound generated
		// content and the estimator's placeholder vectors to one window at a time.
		for batch := range slices.Chunk(textChunks, indexWindowSize) {
			totalStorageSize += retrieveEngine.EstimateStorageSize(ctx, embeddingModel, buildIndexWindow(batch))
		}
		if tenantInfo.StorageQuota > 0 {
			// Re-fetch tenant storage information
			tenantInfo, err = s.tenantRepo.GetTenantByID(ctx, tenantInfo.ID)
			if err != nil {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.updateKnowledgeBestEffort(ctx, knowledge)
				s.deleteUnindexedChunks(ctx, knowledge)
				return nil
			}
			// Check if there's enough storage quota available
			if tenantInfo.StorageUsed+totalStorageSize > tenantInfo.StorageQuota {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = "存储空间不足"
				knowledge.UpdatedAt = time.Now()
				s.updateKnowledgeBestEffort(ctx, knowledge)
				s.deleteUnindexedChunks(ctx, knowledge)
				return nil
			}
		}

		// Check again before batch indexing (heavy operation).
		// deleting → row is going away anyway, drop the chunks we just wrote.
		// cancelled → user wants to keep what was already persisted, just stop.
		if s.isKnowledgeSourceReplaced(ctx, knowledge) {
			logger.Infof(ctx, "Knowledge source replaced, skipping indexing: %s", knowledge.ID)
			return nil
		}
		if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
			logger.Infof(ctx, "Knowledge aborted (%s) before indexing: %s", status, knowledge.ID)
			if status == types.ParseStatusDeleting {
				if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, knowledge.TenantID, knowledge.ID); err != nil {
					logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
				}
			}
			return abortRetryErr(ctx, knowledge.ID, status)
		}

		for batch := range slices.Chunk(textChunks, indexWindowSize) {
			if err = ctx.Err(); err != nil {
				break
			}
			err = retrieveEngine.BatchIndex(ctx, embeddingModel, buildIndexWindow(batch))
			if err != nil {
				break
			}
		}
		if err != nil {
			// Embedding and vector-store errors are often transient. While the
			// task has attempts left, leave the row processing and hand the
			// error to the queue so the idempotent parse runs again; only the
			// final attempt (or a caller without a retry loop) records failure.
			willRetry := summaryTaskWillRetry(ctx)
			if !willRetry {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
					logger.Errorf(ctx, "Update knowledge status failed after batch index error: %v", updateErr)
				}
			}

			// delete failed chunks
			if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, knowledge.TenantID, knowledge.ID); err != nil {
				logger.Errorf(ctx, "Delete chunks failed: %v", err)
			}

			// delete index
			if err := retrieveEngine.DeleteByKnowledgeIDList(
				ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), kb.Type,
			); err != nil {
				logger.Errorf(ctx, "Delete index failed: %v", err)
			}
			// Map vector store / embedding rate-limit errors to a
			// stable code so the UI can offer "retry later" hints.
			code := werrors.ErrCodeVectorStoreWriteFailed
			if isLikelyRateLimitError(err) {
				code = werrors.ErrCodeEmbeddingRateLimit
			}
			if willRetry {
				s.failStageRetryable(ctx, knowledge.ID, types.StageEmbedding,
					code, "batch index failed", err)
				return fmt.Errorf("batch index: %w", err)
			}
			s.failStage(ctx, knowledge.ID, types.StageEmbedding,
				code, "batch index failed", err)
			return nil
		}
		logger.GetLogger(ctx).Infof("processChunks batch index successfully, with %d index", len(textChunks))
		s.endStage(ctx, knowledge.ID, types.StageEmbedding, types.JSONMap{
			"vectors_written": len(textChunks),
			"storage_bytes":   totalStorageSize,
		})

		// Final check before marking as completed.
		// deleting → drop chunks+index we just wrote.
		// cancelled → keep persisted data; the row stays in cancelled status
		// and downstream stages skip via the entry guards.
		if s.isKnowledgeSourceReplaced(ctx, knowledge) {
			logger.Infof(ctx, "Knowledge source replaced, skipping completion: %s", knowledge.ID)
			return nil
		}
		if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
			logger.Infof(ctx, "Knowledge aborted (%s) after indexing: %s", status, knowledge.ID)
			if status == types.ParseStatusDeleting {
				if err := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, knowledge.TenantID, knowledge.ID); err != nil {
					logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
				}
				if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID},
					embeddingModel.GetDimensions(), kb.Type); err != nil {
					logger.Warnf(ctx, "Failed to cleanup index after deletion detected: %v", err)
				}
			}
			return abortRetryErr(ctx, knowledge.ID, status)
		}
	} else {
		logger.Infof(ctx, "Vector/keyword indexing disabled for KB %s, skipping BatchIndex", kb.ID)
		s.skipStage(ctx, knowledge.ID, types.StageEmbedding, "skipped")
	}

	// Check if this document has extracted images that will be processed asynchronously
	isImage := IsImageType(knowledge.FileType)
	isVideo := IsVideoType(knowledge.FileType)
	pendingMultimodal := isImage && options.EnableMultimodel && len(options.StoredImages) > 0
	pendingPDFMultimodal := !isImage && !isVideo && options.EnableMultimodel && len(options.StoredImages) > 0

	if s.currentAttemptSuperseded(ctx, knowledge.ID) {
		logger.Infof(ctx, "Parse attempt superseded, skipping completion: %s", knowledge.ID)
		return nil
	}

	// Reparse has now written the replacement chunks and, when enabled, their
	// replacement indexes. Only at this successful point is it safe to reclaim
	// old soft-deleted rows. Keep this optional at the interface boundary so
	// existing non-SQL repositories and narrow test doubles retain their
	// lifecycle behavior; the concrete SQL chunk repository implements it.
	if purger, ok := s.chunkRepo.(supersededChunkPurger); ok {
		purged, purgeErr := purger.PurgeSupersededChunksByKnowledgeID(ctx, knowledge.TenantID, knowledge.ID)
		if purgeErr != nil {
			logger.Warnf(ctx, "Failed to purge superseded chunks for knowledge %s: %v", knowledge.ID, purgeErr)
		} else if purged > 0 {
			logger.Infof(ctx, "Purged %d superseded chunks for knowledge %s", purged, knowledge.ID)
		}
	}

	now := time.Now()
	finalizeIndexedKnowledgeState(
		knowledge,
		totalStorageSize,
		len(textChunks),
		pendingMultimodal || pendingPDFMultimodal,
		now,
	)

	if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update knowledge failed")
	}

	// Enqueue multimodal tasks for images (async, non-blocking)
	if options.EnableMultimodel && len(options.StoredImages) > 0 {
		// Counts only, no policy claims: whether a given image is OCR'd is
		// decided per image once its attributes are observed, so a boolean here
		// would assert something the pipeline has not decided yet — and would
		// drift from what actually happened.
		s.beginStage(ctx, knowledge.ID, types.StageMultimodal, types.JSONMap{
			"image_count": len(options.StoredImages),
		})
		if err := s.enqueueImageMultimodalTasks(
			ctx, knowledge, kb, options.StoredImages, chunks, options.Metadata,
		); err != nil {
			// Nothing else moves the row out of "processing"; let asynq
			// run the (idempotent) parse again, or dead-letter it.
			return err
		}
	} else {
		s.skipStage(ctx, knowledge.ID, types.StageMultimodal, "skipped")
		// If there are no multimodal tasks, enqueue the post process task immediately
		if err := s.enqueueKnowledgePostProcessTask(ctx, knowledge); err != nil {
			return err
		}
	}

	// Update tenant's storage usage
	tenantInfo.StorageUsed += totalStorageSize
	if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, totalStorageSize); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update tenant storage used failed")
	}
	logger.GetLogger(ctx).Infof("processChunks successfully")
	return nil
}
