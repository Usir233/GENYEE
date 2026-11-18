package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/common"
	werrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func (s *knowledgeService) updateChunkVector(ctx context.Context, kbID string, chunks []*types.Chunk) error {
	// Get embedding model from knowledge base
	sourceKB, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	if !sourceKB.NeedsEmbeddingModel() {
		return nil
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, sourceKB.EmbeddingModelID)
	if err != nil {
		return err
	}

	// Initialize composite retrieve engine from tenant configuration
	indexInfo := make([]*types.IndexInfo, 0, len(chunks))
	ids := make([]string, 0, len(chunks))
	knowledgeCache := make(map[string]*types.Knowledge)
	for _, chunk := range chunks {
		if chunk.KnowledgeBaseID != kbID {
			logger.Warnf(ctx, "Knowledge base ID mismatch: %s != %s", chunk.KnowledgeBaseID, kbID)
			continue
		}
		// Its index row holds the image's own vector, which re-embedding
		// Content here would replace with a text vector of the caption.
		if chunk.ChunkType == types.ChunkTypeImageVector {
			continue
		}
		ids = append(ids, chunk.ID)
		if !chunk.IsEnabled || chunk.ChunkType == types.ChunkTypeParentText {
			continue
		}
		knowledge := knowledgeCache[chunk.KnowledgeID]
		if knowledge == nil {
			knowledge, err = s.repo.GetKnowledgeByID(ctx, chunk.TenantID, chunk.KnowledgeID)
			if err != nil {
				return err
			}
			knowledgeCache[chunk.KnowledgeID] = knowledge
		}
		indexInfo = append(indexInfo, &types.IndexInfo{
			Content:         buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent()),
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeType:   sourceKB.Type,
			IsEnabled:       chunk.IsEnabled,
		})
		meta, metaErr := chunk.DocumentMetadata()
		if metaErr != nil {
			return metaErr
		}
		if meta != nil {
			for _, q := range meta.GeneratedQuestions {
				if strings.TrimSpace(q.Question) != "" {
					indexInfo = append(indexInfo, &types.IndexInfo{
						Content:    buildKnowledgeIndexContent(knowledge, q.Question),
						SourceID:   types.GeneratedQuestionSourceID(chunk.ID, q.ID),
						SourceType: types.ChunkSourceType, ChunkID: chunk.ID,
						KnowledgeID: chunk.KnowledgeID, KnowledgeBaseID: chunk.KnowledgeBaseID,
						KnowledgeType: sourceKB.Type, IsEnabled: true,
					})
				}
			}
		}
	}

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, types.MustTenantIDFromContext(ctx), sourceKB.VectorStoreID)
	if err != nil {
		return err
	}

	// Delete old vector representation of the chunk
	err = retrieveEngine.DeleteByChunkIDList(ctx, ids, embeddingModel.GetDimensions(), sourceKB.Type)
	if err != nil {
		return err
	}

	// Index updated chunk content with new vector representation
	err = retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo)
	if err != nil {
		return err
	}
	return nil
}

func (s *knowledgeService) UpdateImageInfo(
	ctx context.Context,
	knowledgeID string,
	chunkID string,
	imageInfo string,
) error {
	knowledge, _, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, knowledgeID)
	if err != nil {
		return err
	}
	imageInfo = common.CleanInvalidUTF8(imageInfo)
	var images []*types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfo), &images); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal image info: %v", err)
		return err
	}
	if len(images) != 1 {
		logger.Warnf(ctx, "Expected exactly one image info, got %d", len(images))
		return nil
	}
	image := images[0]

	// Retrieve all chunks with the given parent chunk ID
	chunk, err := s.chunkService.GetChunkByID(ctx, chunkID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunk: %v", err)
		return err
	}
	if chunk == nil || chunk.ID != chunkID || chunk.KnowledgeID != knowledge.ID ||
		chunk.TenantID != knowledge.TenantID ||
		chunk.KnowledgeBaseID != knowledge.KnowledgeBaseID {
		return werrors.NewForbiddenError("chunk does not belong to its knowledge document")
	}
	chunk.ImageInfo = imageInfo
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunkChildren, err := s.chunkService.ListChunkByParentID(ctx, tenantID, chunkID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"parent_chunk_id": chunkID,
			"tenant_id":       tenantID,
		})
		return err
	}
	for _, child := range chunkChildren {
		if !sameChunkDocument(chunk, child) {
			return werrors.NewForbiddenError("image child does not belong to its document")
		}
	}
	logger.Infof(ctx, "Found %d chunks with parent chunk ID: %s", len(chunkChildren), chunkID)

	// Iterate through each chunk and update its content based on the image information
	updateChunk := []*types.Chunk{chunk}
	var addChunk []*types.Chunk

	// Track whether we've found OCR and caption child chunks for this image
	hasOCRChunk := false
	hasCaptionChunk := false

	for i, child := range chunkChildren {
		// Skip chunks that are not image types
		var cImageInfo []*types.ImageInfo
		err = json.Unmarshal([]byte(child.ImageInfo), &cImageInfo)
		if err != nil {
			logger.Warnf(ctx, "Failed to unmarshal image %s info: %v", child.ID, err)
			continue
		}
		if len(cImageInfo) == 0 {
			continue
		}
		if cImageInfo[0].OriginalURL != image.OriginalURL {
			logger.Warnf(ctx, "Skipping chunk ID: %s, image URL mismatch: %s != %s",
				child.ID, cImageInfo[0].OriginalURL, image.OriginalURL)
			continue
		}

		// Mark that we've found chunks for this image
		switch child.ChunkType {
		case types.ChunkTypeImageCaption:
			hasCaptionChunk = true
			// Update caption if it has changed
			if image.Caption != cImageInfo[0].Caption {
				child.Content = image.Caption
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		case types.ChunkTypeImageOCR:
			hasOCRChunk = true
			// Update OCR if it has changed
			if image.OCRText != cImageInfo[0].OCRText {
				child.Content = image.OCRText
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		case types.ChunkTypeImageVector:
			// The image's vector does not change with its caption, only the
			// text shown and reranked for it; updateChunkVector leaves the
			// vector alone.
			if image.Caption != "" && image.Caption != child.Content {
				child.Content = image.Caption
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		}
	}

	// Create a new caption chunk if it doesn't exist and we have caption data
	if !hasCaptionChunk && image.Caption != "" {
		captionChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Content:         image.Caption,
			ChunkType:       types.ChunkTypeImageCaption,
			ParentChunkID:   chunk.ID,
			ImageInfo:       imageInfo,
			// CreateChunks inserts with Select("*"), so the gorm default:true never
			// applies -- an unset IsEnabled lands in the database as false and the
			// chunk is silently excluded from retrieval and model context.
			IsEnabled: true,
		}
		addChunk = append(addChunk, captionChunk)
		logger.Infof(ctx, "Created new caption chunk ID: %s for image URL: %s", captionChunk.ID, image.OriginalURL)
	}

	// Create a new OCR chunk if it doesn't exist and we have OCR data
	if !hasOCRChunk && image.OCRText != "" {
		ocrChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Content:         image.OCRText,
			ChunkType:       types.ChunkTypeImageOCR,
			ParentChunkID:   chunk.ID,
			ImageInfo:       imageInfo,
			IsEnabled:       true,
		}
		addChunk = append(addChunk, ocrChunk)
		logger.Infof(ctx, "Created new OCR chunk ID: %s for image URL: %s", ocrChunk.ID, image.OriginalURL)
	}
	logger.Infof(ctx, "Updated %d chunks out of %d total chunks", len(updateChunk), len(chunkChildren)+1)

	if len(addChunk) > 0 {
		err := s.chunkRepo.CreateChunks(ctx, addChunk)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"add_chunk_size": len(addChunk),
			})
			return err
		}
	}

	// Update the chunks
	for _, c := range updateChunk {
		err := s.chunkRepo.UpdateChunk(ctx, c)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"chunk_id":     c.ID,
				"knowledge_id": c.KnowledgeID,
			})
			return err
		}
	}

	// Update the chunk vector
	err = s.updateChunkVector(ctx, chunk.KnowledgeBaseID, append(updateChunk, addChunk...))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id":     chunk.ID,
			"knowledge_id": chunk.KnowledgeID,
		})
		return err
	}

	// Update the knowledge file hash
	knowledge, err = s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return err
	}
	fileHash := calculateStr(knowledgeID, knowledge.FileHash, imageInfo)
	knowledge.FileHash = fileHash
	err = s.repo.UpdateKnowledge(ctx, knowledge)
	if err != nil {
		logger.Warnf(ctx, "Failed to update knowledge file hash: %v", err)
	}

	logger.Infof(ctx, "Updated chunk successfully, chunk ID: %s, knowledge ID: %s", chunk.ID, chunk.KnowledgeID)
	return nil
}

// enqueueImageMultimodalTasks enqueues asynq tasks for multimodal image
// processing. It returns an error only when no path is left to move the
// knowledge out of "processing" (see releaseUnownedMultimodalSlots).
func (s *knowledgeService) enqueueImageMultimodalTasks(
	ctx context.Context,
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
	images []docparser.StoredImage,
	chunks []types.ParsedChunk,
	metadata map[string]string,
) error {
	if s.task == nil || len(images) == 0 {
		return nil
	}

	attempt := attemptFromCtx(ctx)
	lang := types.LanguageFromContextOrDefault(ctx)
	redisKey := multimodalPendingKey(knowledge.ID)
	counterBatch := uuid.NewString()
	// The counter has to be seeded BEFORE the first task is enqueued: an image
	// task that finishes early would otherwise decrement a key we are about to
	// overwrite, and its slot would be counted twice. The cost of seeding
	// first is that a slot whose task never reaches the queue has no owner to
	// drain it — releaseUnownedMultimodalSlots settles that after the fan-out.
	if err := seedMultimodalCounter(ctx, s.redisClient, redisKey, counterBatch, int64(len(images))); err != nil {
		return fmt.Errorf("seed multimodal counter for %s: %w", knowledge.ID, err)
	}

	// Resolve the image pipeline settings once for the whole fan-out: the image
	// action policy and the pipeline switch travel with each task so the worker
	// needs no second config lookup, and so a task keeps the policy it was
	// enqueued with even if the knowledge base is reconfigured meanwhile.
	overrides, _ := knowledge.ProcessOverrides()
	eff := ResolveProcessConfig(kb, overrides)

	// Mixed PDFs contain both page renders and native figures. Older parsers
	// provide only the document-wide source, which remains the fallback.
	var imageSourceTypes map[string]string
	if raw := metadata["image_source_types"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &imageSourceTypes); err != nil {
			logger.Warnf(ctx, "Invalid image source metadata for knowledge %s: %v", knowledge.ID, err)
			imageSourceTypes = nil
		}
	}

	enqueued := 0
	for idx, img := range images {
		// Match image to the ParsedChunk whose content contains the image URL.
		// ChunkID was populated by processChunks with the real DB UUID.
		chunkID := imageChunkOwner(img.ServingURL, chunks)
		if chunkID == "" {
			logger.Warnf(ctx, "Image has no owning text chunk: knowledge=%s image=%s", knowledge.ID, img.ServingURL)
		}
		imageSourceType := metadata["image_source_type"]
		if source := imageSourceTypes[img.OriginalRef]; source != "" {
			imageSourceType = source
		}

		payload := types.ImageMultimodalPayload{
			TenantID:            knowledge.TenantID,
			KnowledgeID:         knowledge.ID,
			KnowledgeBaseID:     kb.ID,
			ChunkID:             chunkID,
			ImageURL:            img.ServingURL,
			ImagePipelineID:     eff.ImagePipelineID,
			ImagePipelineParams: eff.ImagePipelineParams,
			ImageAttrsEnabled:   eff.ImageAttrsEnabled,
			ImageActions:        eff.ImageActions,
			Language:            lang,
			ImageSourceType:     imageSourceType,
			Attempt:             attempt,
			ImageIndex:          idx,
			CounterBatch:        counterBatch,
			SourceLocators:      img.SourceLocators,
			ImageVectorEnabled:  &eff.ImageVectorEnabled,
		}

		langfuse.InjectTracing(ctx, &payload)
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			logger.Warnf(ctx, "Failed to marshal image multimodal payload: %v", err)
			continue
		}

		task := asynq.NewTask(types.TypeImageMultimodal, payloadBytes,
			asynq.Queue(types.QueueMultimodal), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
		if _, err := s.task.Enqueue(task); err != nil {
			logger.Warnf(ctx, "Failed to enqueue image multimodal task for %s: %v", img.ServingURL, err)
			continue
		}
		enqueued++
		logger.Infof(ctx, "Enqueued image:multimodal task for %s", img.ServingURL)
	}

	return s.releaseUnownedMultimodalSlots(
		ctx, knowledge, redisKey, counterBatch, len(images), enqueued,
	)
}

// releaseUnownedMultimodalSlots settles the multimodal fan-in counter against
// what actually reached the queue.
//
// Every enqueued image task drains exactly one slot when it exits terminally,
// and the knowledge only leaves "processing" once the counter reaches zero. A
// slot whose task was never enqueued (payload marshal failure, asynq refusing
// the write) has no owner, so without this release the counter stalls above
// zero, post-process is never triggered, and the row sits in "processing"
// until the housekeeping sweep fails it an hour later — which reaches the user
// as an unexplained parse failure. This mirrors the shortfall release
// KnowledgePostProcess already performs for its own enrichment subtasks.
func (s *knowledgeService) releaseUnownedMultimodalSlots(
	ctx context.Context,
	knowledge *types.Knowledge,
	redisKey, counterBatch string,
	planned, enqueued int,
) error {
	if enqueued == 0 {
		// No task exists to finalize this knowledge, whatever the counter
		// says. Drive post-process directly so the row completes on the
		// chunks that were already indexed instead of waiting to be swept.
		logger.Warnf(ctx,
			"[ImageMultimodal] No image task enqueued for %s (planned=%d); enqueueing post-process directly",
			knowledge.ID, planned)
		// Detached like the shortfall path below: a cancelled parent
		// context would otherwise leave the seeded key to expire on its
		// own 24h TTL.
		dctx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), finalizeSubtaskDetachedTimeout)
		deleteMultimodalCounter(dctx, s.redisClient, redisKey, counterBatch)
		cancel()
		return s.enqueueKnowledgePostProcessTask(ctx, knowledge)
	}

	shortfall := planned - enqueued
	if shortfall <= 0 {
		return nil
	}
	logger.Warnf(ctx,
		"[ImageMultimodal] Releasing %d un-enqueued image slot(s) for %s (planned=%d enqueued=%d)",
		shortfall, knowledge.ID, planned, enqueued)
	// Detached ctx, for the same reason KnowledgePostProcess detaches its own
	// releases: these slots have no other path to drain, so a parent context
	// cancelled mid-fan-out (graceful shutdown) must not skip the release.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeSubtaskDetachedTimeout)
	defer cancel()
	pending, current, err := s.decrMultimodalSlots(rctx, redisKey, counterBatch, int64(shortfall))
	if err != nil {
		// Giving up here puts the row back in the state this whole fix exists
		// to remove: a counter no task can drain, recovered only by the
		// housekeeping sweep an hour later. Say so at error level so it is
		// greppable, rather than hiding it in a warning.
		logger.Errorf(ctx,
			"Failed to release %d multimodal slot(s) for %s after %d attempts: %v; "+
				"row will be left to the housekeeping sweep",
			shortfall, knowledge.ID, multimodalSlotReleaseAttempts, err)
		return nil
	}
	if current && pending <= 0 {
		// Every enqueued sibling had already finished, so the release owns the
		// finalize. Ordering is safe either way: the slots we just drained have
		// no task behind them, so exactly one path reaches zero.
		if err := s.enqueueKnowledgePostProcessTask(ctx, knowledge); err != nil {
			return err
		}
		deleteMultimodalCounter(rctx, s.redisClient, redisKey, counterBatch)
	}
	return nil
}

// multimodalSlotReleaseAttempts bounds the retry on the release decrement.
//
// The release is the only thing that can ever drain an un-owned slot, so a
// single transient Redis error must not be the end of it — that would strand
// the row exactly as it did before this fix. Attempts stay small because they
// share the detached context's budget, and the housekeeping sweep is still the
// backstop when Redis is genuinely down.
//
// DECRBY is not idempotent, so a retry after a lost reply can over-decrement.
// That is the safe direction to fail in: an over-decrement finalizes early
// (post-process may miss some OCR / caption chunks, and repeat deliveries are
// already handled by its non-processing-status branch), whereas not retrying
// strands the document in "processing" for an hour.
const multimodalSlotReleaseAttempts = 3

// multimodalSlotReleaseBackoff is the unit of linear backoff between release
// attempts, small enough that all attempts fit the detached 10s budget.
const multimodalSlotReleaseBackoff = 100 * time.Millisecond

// decrMultimodalSlots decrements the fan-in counter by `by`, retrying transient
// Redis failures, and returns the resulting count.
func (s *knowledgeService) decrMultimodalSlots(
	ctx context.Context, redisKey, counterBatch string, by int64,
) (int64, bool, error) {
	var lastErr error
	for attempt := 1; attempt <= multimodalSlotReleaseAttempts; attempt++ {
		pending, current, err := decrementMultimodalCounter(ctx, s.redisClient, redisKey, counterBatch, by)
		if err == nil {
			return pending, current, nil
		}
		lastErr = err
		if attempt == multimodalSlotReleaseAttempts {
			break
		}
		logger.Warnf(ctx, "multimodal slot release attempt %d/%d failed for %s: %v",
			attempt, multimodalSlotReleaseAttempts, redisKey, err)
		if sleepErr := sleepWithContext(ctx, time.Duration(attempt)*multimodalSlotReleaseBackoff); sleepErr != nil {
			return 0, false, sleepErr
		}
	}
	return 0, false, lastErr
}

// enqueueKnowledgePostProcessTask hands a knowledge to the post-process
// orchestrator, which owns the enrichment fan-out and the promotion out of
// "processing". Shared by the no-multimodal path and by the multimodal slot
// release, so both build the same payload (attempt, language, tracing).
//
// The error must reach the caller's task result: once the chunks are indexed
// this enqueue is the only thing that moves the row out of "processing", so
// swallowing it acked the task with the row stranded until housekeeping.
func (s *knowledgeService) enqueueKnowledgePostProcessTask(
	ctx context.Context, knowledge *types.Knowledge,
) error {
	if s.task == nil {
		return nil
	}
	payload := types.KnowledgePostProcessPayload{
		TenantID:        knowledge.TenantID,
		KnowledgeID:     knowledge.ID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		Language:        types.LanguageFromContextOrDefault(ctx),
		Attempt:         attemptFromCtx(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal knowledge post process payload: %v", err)
		return fmt.Errorf("marshal post process payload: %w", err)
	}
	task := asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes,
		knowledgePostProcessTaskOptions()...)
	if err := enqueueWithRetry(ctx, s.task, task); err != nil {
		logger.Errorf(ctx, "Failed to enqueue knowledge post process task for %s: %v", knowledge.ID, err)
		return err
	}
	logger.Infof(ctx, "Enqueued knowledge post process task for %s", knowledge.ID)
	return nil
}
