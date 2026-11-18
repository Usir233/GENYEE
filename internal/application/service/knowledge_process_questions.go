package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	werrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/searchutil"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// ProcessQuestionGeneration handles async question generation task. It
// dispatches between the batched fan-out path (current: one task per window of
// text chunks, payload.ChunkIDs set) and the legacy whole-knowledge path (kept
// for tasks enqueued before fan-out shipped, no chunk ids). A lone ChunkID
// (from an interim per-chunk build) is treated as a one-element batch.
func (s *knowledgeService) ProcessQuestionGeneration(ctx context.Context, t *asynq.Task) (retErr error) {
	var payload types.QuestionGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal question generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}
	if len(payload.ChunkIDs) > 0 || payload.ChunkID != "" {
		return s.processQuestionGenerationForChunks(ctx, t, payload)
	}
	return s.processQuestionGenerationForKnowledge(ctx, t, payload)
}

// processQuestionGenerationForKnowledge is the legacy whole-knowledge handler:
// it iterates every text chunk of the knowledge in one task. Retained for
// in-flight tasks queued before per-chunk fan-out; new enqueues always set
// payload.ChunkID and take the per-chunk path instead.
func (s *knowledgeService) processQuestionGenerationForKnowledge(
	ctx context.Context,
	_ *asynq.Task,
	payload types.QuestionGenerationPayload,
) (retErr error) {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	exitStatus := "success"
	totalChunks := 0
	totalTextChunks := 0
	emptyContentChunks := 0
	llmCallAttempts := 0
	llmCallSuccess := 0
	llmCallFailed := 0
	llmCallEmpty := 0
	generatedQuestionsTotal := 0
	chunkMetadataSetFailed := 0
	chunkUpdateFailed := 0
	indexEntriesPrepared := 0
	indexBatchAttempted := false
	indexBatchSucceeded := false
	// Sample question + model id surfaced on the span output so the
	// trace viewer can answer "what did the LLM actually produce?" and
	// "which model did it run on?" without joining back to the chunk
	// store. Captured the first time we see a non-empty question batch.
	var sampleQuestion string
	var resolvedModelID string
	// Postprocess subspan for the trace viewer. Opened lazily after we
	// unmarshal the payload (so we have payload.Attempt) and closed in
	// the defer below alongside the stats log so the span output mirrors
	// what we already log to stdout.
	var qSpan *Span
	var qErr error
	// Set when a newer attempt supersedes this run; suppresses the
	// FinalizeSubtask decrement so a stale task can't drain the new
	// attempt's counter.
	superseded := false
	// Decrement enrichment counter on terminal exit. Keyed on the value
	// RETURNED to asynq (retErr), not qErr: some branches record a span
	// failure (qErr != nil) yet `return nil` so asynq won't retry (KB /
	// knowledge fetch failures); those are terminal and must drain.
	// Keying on qErr would skip them and strand the row in "finalizing".
	// When we return an error asynq retries, so we only drain on the
	// final attempt. Runs AFTER the stats-log defer below — defers
	// unwind LIFO, so this one declared first executes last.
	defer func() {
		finalizeSubtaskDetached(ctx, s.repo, payload.KnowledgeID, "question_legacy",
			retErr, superseded, isFinalAsynqAttempt(ctx), payload.Attempt)
	}()
	defer func() {
		logger.Infof(
			ctx,
			"Question generation stats: knowledge=%s kb=%s retry=%d/%d status=%s elapsed=%s chunks(total=%d,"+
				"text=%d,empty_text=%d) llm(attempt=%d,success=%d,empty=%d,failed=%d) "+
				"generated_questions=%d chunk_update_failed=%d metadata_set_failed=%d index(prepared=%d,"+
				"attempted=%v,succeeded=%v)",
			payload.KnowledgeID,
			payload.KnowledgeBaseID,
			retryCount,
			maxRetry,
			exitStatus,
			time.Since(taskStartedAt).Round(time.Millisecond),
			totalChunks,
			totalTextChunks,
			emptyContentChunks,
			llmCallAttempts,
			llmCallSuccess,
			llmCallEmpty,
			llmCallFailed,
			generatedQuestionsTotal,
			chunkUpdateFailed,
			chunkMetadataSetFailed,
			indexEntriesPrepared,
			indexBatchAttempted,
			indexBatchSucceeded,
		)
		if llmCallEmpty > 0 {
			// Empty completions are counted, not logged, at the call site; a
			// run where every chunk came back empty is indistinguishable from
			// a healthy run in the application log. Reasoning-style models
			// burning the whole budget on thinking are the known cause —
			// point operators at the knob instead of leaving them to guess.
			logger.Warnf(ctx,
				"Question generation: %d of %d model calls returned empty completions "+
					"(reasoning-style models can spend the completion budget on thinking even when disabled; "+
					"raise %s to give the reply room)",
				llmCallEmpty, llmCallAttempts, generateQuestionsMaxTokensEnv)
		}
		if qSpan != nil {
			out := types.JSONMap{
				"status":                 exitStatus,
				"total_chunks":           totalChunks,
				"text_chunks":            totalTextChunks,
				"empty_content_chunks":   emptyContentChunks,
				"llm_attempts":           llmCallAttempts,
				"llm_success":            llmCallSuccess,
				"llm_empty":              llmCallEmpty,
				"llm_failed":             llmCallFailed,
				"questions_generated":    generatedQuestionsTotal,
				"chunk_update_failed":    chunkUpdateFailed,
				"metadata_set_failed":    chunkMetadataSetFailed,
				"index_entries_prepared": indexEntriesPrepared,
				"index_batch_attempted":  indexBatchAttempted,
				"index_batch_succeeded":  indexBatchSucceeded,
				"retry":                  retryCount,
				"max_retry":              maxRetry,
			}
			// Surface the resolved model id and a sample question on the
			// span output. These help debugging "why is question generation
			// slow" — both questions ("which model was hit?") and ("what
			// did it produce?") are hard to answer from logs alone.
			if resolvedModelID != "" {
				out["model_id"] = resolvedModelID
			}
			if sampleQuestion != "" {
				out["sample_question"] = sampleQuestion
			}
			// Treat any non-success exitStatus as a failed run; the
			// existing stats-string already enumerates them. qErr stays
			// optional for callers that want to surface a Go error.
			if exitStatus != "success" || qErr != nil {
				msg := exitStatus
				detailErr := qErr
				if qErr != nil {
					msg = qErr.Error()
				}
				s.failPostprocessSubspan(ctx, qSpan, "QUESTION_FAILED", msg, detailErr)
			} else {
				s.endPostprocessSubspan(ctx, qSpan, out)
			}
		}
	}()

	logger.Infof(ctx, "Processing question generation for knowledge: %s", payload.KnowledgeID)

	// A newer attempt has superseded this one: skip before opening the span
	// so we don't read stale chunks. superseded suppresses the counter
	// decrement in the defer above; qSpan stays nil so the stats defer no-ops.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		superseded = true
		exitStatus = "superseded"
		logger.Infof(ctx, "question: attempt %d superseded for %s, skipping stale enrichment",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}

	// Open the postprocess.question subspan now that we have payload.Attempt.
	// Closes via the defer above.
	qSpan = s.beginPostprocessSubspan(ctx, payload.KnowledgeID, payload.Attempt, "postprocess.question",
		types.JSONMap{
			"question_count": payload.QuestionCount,
			"language":       payload.Language,
		})

	// Set tenant context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	if strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt) == "" {
		exitStatus = "prompt_not_configured"
		logger.Errorf(ctx, "GenerateQuestionsPrompt is empty: configure conversation.generate_questions_prompt_id")
		qErr = fmt.Errorf("generate questions prompt not configured")
		return qErr
	}

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		exitStatus = "kb_not_found"
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		qErr = err
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		exitStatus = "knowledge_not_found"
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		qErr = err
		return nil
	}
	// Short-circuit when the user cancelled parsing or the row is being deleted.
	if knowledge != nil {
		switch knowledge.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			exitStatus = "knowledge_" + knowledge.ParseStatus
			logger.Infof(ctx, "Question generation: knowledge aborted (%s), skipping: %s",
				knowledge.ParseStatus, payload.KnowledgeID)
			return nil
		}
	}

	// Get text chunks for this knowledge
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		exitStatus = "list_chunks_failed"
		logger.Errorf(ctx, "Failed to get chunks: %v", err)
		return nil
	}
	totalChunks = len(chunks)

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}
	totalTextChunks = len(textChunks)

	if len(textChunks) == 0 {
		exitStatus = "no_text_chunks"
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		return nil
	}

	// Sort chunks by StartAt for context building
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].StartAt < textChunks[j].StartAt
	})

	// Initialize chat model
	processOverrides, _ := knowledge.ProcessOverrides()
	questionGenCfg := ResolveProcessConfig(kb, processOverrides).QuestionGenerationConfig
	questionModelID, err := resolveQuestionGenerationModel(ctx, s.modelService, kb, processOverrides)
	if err != nil {
		exitStatus = "question_model_invalid"
		qErr = err
		return err
	}
	chatModel, err := s.modelService.GetChatModel(ctx, questionModelID)
	if err != nil {
		exitStatus = "get_chat_model_failed"
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}
	resolvedModelID = questionModelID

	// Initialize embedding model and retrieval engine
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		exitStatus = "get_embedding_model_failed"
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		return fmt.Errorf("failed to get embedding model: %w", err)
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		exitStatus = "get_tenant_failed"
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil {
		exitStatus = "init_retrieve_engine_failed"
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		return fmt.Errorf("failed to init retrieve engine: %w", err)
	}

	questionCount := payload.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	customInstructions := questionGenCfg.CustomInstructions

	// Collect image info for all text chunks so question generation can
	// see caption / OCR text instead of bare image links.
	textChunkIDs := make([]string, len(textChunks))
	for i, c := range textChunks {
		textChunkIDs[i] = c.ID
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, payload.TenantID, textChunkIDs)

	enrichContent := func(chunk *types.Chunk) string {
		if info, ok := imageInfoMap[chunk.ID]; ok && info != "" {
			return searchutil.EnrichContentWithImageInfo(chunk.Content, info)
		}
		return chunk.Content
	}

	// Generate questions for each chunk with context
	var indexInfoList []*types.IndexInfo
	for i, chunk := range textChunks {
		if strings.TrimSpace(chunk.Content) == "" {
			emptyContentChunks++
			continue
		}

		// Build context from adjacent chunks
		var prevContent, nextContent string
		if i > 0 {
			prevContent = enrichContent(textChunks[i-1])
		}
		if i < len(textChunks)-1 {
			nextContent = enrichContent(textChunks[i+1])
		}

		generationRevision := chunk.ContentRevision
		llmCallAttempts++
		questions, err := s.generateQuestionsWithContext(ctx, chatModel, enrichContent(chunk), prevContent, nextContent,
			knowledge.Title, questionCount, customInstructions)
		if err != nil {
			llmCallFailed++
			logger.Warnf(ctx, "Failed to generate questions for chunk %s: %v", chunk.ID, err)
			continue
		}

		if len(questions) == 0 {
			llmCallEmpty++
			continue
		}
		latestChunk, latestErr := s.chunkRepo.GetChunkByID(ctx, payload.TenantID, chunk.ID)
		if latestErr != nil || latestChunk.ContentRevision != generationRevision {
			logger.Infof(ctx, "Skipping stale generated questions for chunk %s (revision changed)", chunk.ID)
			continue
		}
		chunk = latestChunk
		llmCallSuccess++
		generatedQuestionsTotal += len(questions)
		if sampleQuestion == "" && len(questions) > 0 {
			sampleQuestion = previewText(questions[0], 200)
		}

		// Update chunk metadata with unique IDs for each question
		generatedQuestions := make([]types.GeneratedQuestion, len(questions))
		questionRevision := chunk.ContentRevision
		for j, question := range questions {
			questionID := fmt.Sprintf("q%d", time.Now().UnixNano()+int64(j))
			generatedQuestions[j] = types.GeneratedQuestion{
				ID:              questionID,
				Question:        question,
				ContentRevision: &questionRevision,
			}
		}
		meta := &types.DocumentChunkMetadata{
			GeneratedQuestions: generatedQuestions, GeneratedQuestionsRevision: chunk.ContentRevision,
		}
		if err := chunk.SetDocumentMetadata(meta); err != nil {
			chunkMetadataSetFailed++
			logger.Warnf(ctx, "Failed to set document metadata for chunk %s: %v", chunk.ID, err)
			continue
		}

		// Update chunk in database
		if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
			chunkUpdateFailed++
			logger.Warnf(ctx, "Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		// Create index entries for generated questions
		for _, gq := range generatedQuestions {
			sourceID := types.GeneratedQuestionSourceID(chunk.ID, gq.ID)
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         buildKnowledgeIndexContent(knowledge, gq.Question),
				SourceID:        sourceID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				IsEnabled:       true,
			})
		}
		logger.Debugf(ctx, "Generated %d questions for chunk %s", len(questions), chunk.ID)
	}
	indexEntriesPrepared = len(indexInfoList)

	// Index generated questions
	if len(indexInfoList) > 0 {
		indexBatchAttempted = true
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
			exitStatus = "index_questions_failed"
			logger.Errorf(ctx, "Failed to index generated questions: %v", err)
			return fmt.Errorf("failed to index questions: %w", err)
		}
		indexBatchSucceeded = true
		logger.Infof(ctx, "Successfully indexed %d generated questions for knowledge: %s", len(indexInfoList),
			payload.KnowledgeID)
	}

	return nil
}

// processQuestionGenerationForChunks generates questions for a batch (window)
// of text chunks. This is the batched fan-out path (one asynq task per
// questionGenChunkBatchSize chunks), aligned with the graph-extract
// TypeChunkExtract pattern: independent retry, per-batch cancellation, and a
// postprocess.question.batch[i] subspan. The payload carries only chunk ids
// (never content); content is read fresh here, and all questions for the batch
// are indexed in a single embedding BatchIndex call.
func (s *knowledgeService) processQuestionGenerationForChunks(
	ctx context.Context,
	_ *asynq.Task,
	payload types.QuestionGenerationPayload,
) (retErr error) {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	// Normalize the batch: prefer ChunkIDs, fall back to a lone ChunkID
	// (interim per-chunk build) so those in-flight tasks still run.
	batchIDs := payload.ChunkIDs
	if len(batchIDs) == 0 && payload.ChunkID != "" {
		batchIDs = []string{payload.ChunkID}
	}

	exitStatus := "success"
	chunksInBatch := len(batchIDs)
	chunksProcessed := 0
	emptyChunks := 0
	llmCallEmpty := 0
	llmCallFailed := 0
	generatedQuestionsTotal := 0
	indexEntriesPrepared := 0
	indexBatchSucceeded := false
	var sampleQuestion string
	var resolvedModelID string
	var qSpan *Span
	var qErr error
	// Suppresses the FinalizeSubtask drain when a newer attempt superseded
	// this run, so a stale task can't decrement the new attempt's counter.
	superseded := false

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// Drain the parent's enrichment counter on terminal exit. Keyed on the
	// value RETURNED to asynq (retErr), not qErr: some branches record a
	// span failure yet `return nil` (terminal, must drain). Declared first
	// so it runs LAST (after the stats/span defer below).
	defer func() {
		finalizeSubtaskDetached(ctx, s.repo, payload.KnowledgeID,
			fmt.Sprintf("question_batch[%d]", payload.BatchIndex),
			retErr, superseded, isFinalAsynqAttempt(ctx), payload.Attempt)
	}()
	defer func() {
		logger.Infof(ctx,
			"Question generation (batch) stats: knowledge=%s batch=%d "+
				"chunks(in_batch=%d,processed=%d,empty=%d,llm_empty=%d) llm_failed=%d retry=%d/%d "+
				"status=%s elapsed=%s generated_questions=%d index(entries=%d,succeeded=%v)",
			payload.KnowledgeID, payload.BatchIndex, chunksInBatch, chunksProcessed, emptyChunks,
			llmCallEmpty, llmCallFailed, retryCount, maxRetry, exitStatus,
			time.Since(taskStartedAt).Round(time.Millisecond),
			generatedQuestionsTotal, indexEntriesPrepared, indexBatchSucceeded,
		)
		if llmCallEmpty > 0 {
			// See the matching warning in processQuestionGenerationForKnowledge:
			// empty completions point at a completion budget spent on thinking.
			logger.Warnf(ctx,
				"Question generation (batch): %d model calls returned empty completions "+
					"(reasoning-style models can spend the completion budget on thinking even when disabled; "+
					"raise %s to give the reply room)",
				llmCallEmpty, generateQuestionsMaxTokensEnv)
		}
		if qSpan != nil {
			out := types.JSONMap{
				"status":                 exitStatus,
				"batch_index":            payload.BatchIndex,
				"chunks_in_batch":        chunksInBatch,
				"chunks_processed":       chunksProcessed,
				"empty_chunks":           emptyChunks,
				"llm_failed":             llmCallFailed,
				"questions_generated":    generatedQuestionsTotal,
				"index_entries_prepared": indexEntriesPrepared,
				"index_batch_succeeded":  indexBatchSucceeded,
				"retry":                  retryCount,
				"max_retry":              maxRetry,
			}
			if resolvedModelID != "" {
				out["model_id"] = resolvedModelID
			}
			if sampleQuestion != "" {
				out["sample_question"] = sampleQuestion
			}
			if exitStatus != "success" || qErr != nil {
				msg := exitStatus
				if qErr != nil {
					msg = qErr.Error()
				}
				s.failPostprocessSubspan(ctx, qSpan, "QUESTION_FAILED", msg, qErr)
			} else {
				s.endPostprocessSubspan(ctx, qSpan, out)
			}
		}
	}()

	logger.Infof(ctx, "Processing question generation for knowledge=%s batch=%d chunks=%d",
		payload.KnowledgeID, payload.BatchIndex, chunksInBatch)

	if chunksInBatch == 0 {
		exitStatus = "empty_batch"
		return nil
	}

	// A newer attempt has superseded this one: skip before opening the span
	// so we don't read stale chunks and don't drain the new attempt.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		superseded = true
		exitStatus = "superseded"
		logger.Infof(ctx, "question: attempt %d superseded for %s, skipping stale enrichment",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}

	qSpan = s.beginQuestionBatchSubspan(ctx, payload.KnowledgeID, payload.Attempt,
		fmt.Sprintf("postprocess.question.batch[%d]", payload.BatchIndex),
		types.JSONMap{
			"batch_index":    payload.BatchIndex,
			"chunks":         chunksInBatch,
			"question_count": payload.QuestionCount,
			"language":       payload.Language,
		})

	if strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt) == "" {
		exitStatus = "prompt_not_configured"
		logger.Errorf(ctx, "GenerateQuestionsPrompt is empty: configure conversation.generate_questions_prompt_id")
		qErr = fmt.Errorf("generate questions prompt not configured")
		return qErr
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		exitStatus = "kb_not_found"
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		qErr = err
		return nil
	}

	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		exitStatus = "knowledge_not_found"
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		qErr = err
		return nil
	}
	// Short-circuit when the user cancelled parsing or the row is being
	// deleted — batched fan-out means we get this check for free on every
	// batch, so a cancel stops burning LLM quota on the remaining batches.
	if knowledge != nil {
		switch knowledge.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			exitStatus = "knowledge_" + knowledge.ParseStatus
			logger.Infof(ctx, "Question generation: knowledge aborted (%s), skipping batch %d",
				knowledge.ParseStatus, payload.BatchIndex)
			return nil
		}
	}

	processOverrides, _ := knowledge.ProcessOverrides()
	questionGenCfg := ResolveProcessConfig(kb, processOverrides).QuestionGenerationConfig
	questionModelID, err := resolveQuestionGenerationModel(ctx, s.modelService, kb, processOverrides)
	if err != nil {
		exitStatus = "question_model_invalid"
		qErr = err
		return err
	}
	chatModel, err := s.modelService.GetChatModel(ctx, questionModelID)
	if err != nil {
		exitStatus = "get_chat_model_failed"
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}
	resolvedModelID = questionModelID

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		exitStatus = "get_embedding_model_failed"
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		return fmt.Errorf("failed to get embedding model: %w", err)
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		exitStatus = "get_tenant_failed"
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil {
		exitStatus = "init_retrieve_engine_failed"
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		return fmt.Errorf("failed to init retrieve engine: %w", err)
	}

	questionCount := payload.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	customInstructions := questionGenCfg.CustomInstructions

	// Fetch the batch chunks (in payload order) plus the two boundary
	// neighbors so we can rebuild the same surrounding context the legacy
	// loop used, all enriched with image OCR / caption info. A vanished
	// chunk degrades gracefully (skipped / empty context).
	getChunk := func(id string) *types.Chunk {
		if id == "" {
			return nil
		}
		c, gerr := s.chunkRepo.GetChunkByID(ctx, payload.TenantID, id)
		if gerr != nil {
			return nil
		}
		return c
	}
	batchChunks := make([]*types.Chunk, len(batchIDs))
	for i, id := range batchIDs {
		batchChunks[i] = getChunk(id)
	}
	prevChunk := getChunk(payload.PrevChunkID)
	nextChunk := getChunk(payload.NextChunkID)

	infoIDs := make([]string, 0, len(batchIDs)+2)
	infoIDs = append(infoIDs, batchIDs...)
	if payload.PrevChunkID != "" {
		infoIDs = append(infoIDs, payload.PrevChunkID)
	}
	if payload.NextChunkID != "" {
		infoIDs = append(infoIDs, payload.NextChunkID)
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, payload.TenantID, infoIDs)
	enrich := func(c *types.Chunk) string {
		if c == nil {
			return ""
		}
		if info, ok := imageInfoMap[c.ID]; ok && info != "" {
			return searchutil.EnrichContentWithImageInfo(c.Content, info)
		}
		return c.Content
	}

	// neighborContent returns the context content for position i within the
	// batch: the in-batch neighbor when present, else the boundary chunk.
	prevContentAt := func(i int) string {
		if i > 0 {
			return enrich(batchChunks[i-1])
		}
		return enrich(prevChunk)
	}
	nextContentAt := func(i int) string {
		if i < len(batchChunks)-1 {
			return enrich(batchChunks[i+1])
		}
		return enrich(nextChunk)
	}

	var indexInfoList []*types.IndexInfo
	for i, chunk := range batchChunks {
		if chunk == nil || strings.TrimSpace(chunk.Content) == "" {
			emptyChunks++
			continue
		}

		generationRevision := chunk.ContentRevision
		questions, gerr := s.generateQuestionsWithContext(
			ctx, chatModel, enrich(chunk), prevContentAt(i), nextContentAt(i), knowledge.Title, questionCount,
			customInstructions)
		if gerr != nil {
			llmCallFailed++
			logger.Warnf(ctx, "Failed to generate questions for chunk %s: %v", chunk.ID, gerr)
			continue
		}
		if len(questions) == 0 {
			llmCallEmpty++
			continue
		}
		latestChunk, latestErr := s.chunkRepo.GetChunkByID(ctx, payload.TenantID, chunk.ID)
		if latestErr != nil || latestChunk.ContentRevision != generationRevision {
			logger.Infof(ctx, "Skipping stale generated questions for chunk %s (revision changed)", chunk.ID)
			continue
		}
		chunk = latestChunk
		chunksProcessed++
		generatedQuestionsTotal += len(questions)
		if sampleQuestion == "" {
			sampleQuestion = previewText(questions[0], 200)
		}

		generatedQuestions := make([]types.GeneratedQuestion, len(questions))
		questionRevision := chunk.ContentRevision
		for j, question := range questions {
			generatedQuestions[j] = types.GeneratedQuestion{
				ID:              fmt.Sprintf("q%d", time.Now().UnixNano()+int64(j)),
				Question:        question,
				ContentRevision: &questionRevision,
			}
		}
		meta := &types.DocumentChunkMetadata{
			GeneratedQuestions: generatedQuestions, GeneratedQuestionsRevision: chunk.ContentRevision,
		}
		if err := chunk.SetDocumentMetadata(meta); err != nil {
			logger.Warnf(ctx, "Failed to set document metadata for chunk %s: %v", chunk.ID, err)
			continue
		}
		if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}
		for _, gq := range generatedQuestions {
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         buildKnowledgeIndexContent(knowledge, gq.Question),
				SourceID:        types.GeneratedQuestionSourceID(chunk.ID, gq.ID),
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				IsEnabled:       true,
			})
		}
	}

	indexEntriesPrepared = len(indexInfoList)
	if len(indexInfoList) > 0 {
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
			exitStatus = "index_questions_failed"
			qErr = err
			logger.Errorf(ctx, "Failed to index generated questions for batch %d: %v", payload.BatchIndex, err)
			return fmt.Errorf("failed to index questions: %w", err)
		}
		indexBatchSucceeded = true
		logger.Infof(ctx, "Indexed %d generated questions for knowledge=%s batch=%d",
			len(indexInfoList), payload.KnowledgeID, payload.BatchIndex)
	}
	return nil
}

const (
	// generateQuestionsDefaultMaxTokens is the per-chunk completion budget for
	// question generation. The reply itself is a handful of short lines, but
	// reasoning-style models can spend a small budget on thinking even when
	// thinking is disabled and come back with an empty completion (the same
	// failure mode the description path grew KNOWLEDGE_BASE_PROFILE_MAX_TOKENS
	// for, issue #4221) — so the default keeps headroom.
	generateQuestionsDefaultMaxTokens = 2048
	// generateQuestionsMaxTokensEnv lets operators raise (or lower) the budget
	// without a rebuild; invalid or non-positive values fall back to the
	// default so one bad variable cannot break question generation.
	generateQuestionsMaxTokensEnv = "GENERATE_QUESTIONS_MAX_TOKENS"
)

func generateQuestionsMaxTokens() int {
	if v := strings.TrimSpace(os.Getenv(generateQuestionsMaxTokensEnv)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return generateQuestionsDefaultMaxTokens
}

// generateQuestionsWithContext generates questions for a chunk with surrounding context
func (s *knowledgeService) generateQuestionsWithContext(ctx context.Context,
	chatModel chat.Chat, content, prevContent, nextContent, docName string, questionCount int,
	customInstructions string,
) ([]string, error) {
	if content == "" || questionCount <= 0 {
		return nil, nil
	}

	prompt := strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt)
	if prompt == "" {
		return nil, fmt.Errorf("generate questions prompt not configured")
	}

	// Build context section
	var contextSection string
	if prevContent != "" || nextContent != "" {
		contextSection = "<surrounding_context>\n"
		if prevContent != "" {
			contextSection += fmt.Sprintf("<preceding_content>\n%s\n\n</preceding_content>\n\n", prevContent)
		}
		if nextContent != "" {
			contextSection += fmt.Sprintf("<following_content>\n%s\n\n</following_content>\n\n", nextContent)
		}
		contextSection += "</surrounding_context>\n\n"
	}

	langName := types.LanguageNameFromContext(ctx)
	prompt = types.RenderPromptPlaceholders(prompt, types.PlaceholderValues{
		"question_count": fmt.Sprintf("%d", questionCount),
		"content":        content,
		"context":        contextSection,
		"doc_name":       docName,
		"language":       langName,
	})
	prompt = types.AppendCustomPromptInstructions(prompt, customInstructions, "question_generation")

	thinking := false
	modelCtx := types.WithLLMCallMetadata(ctx, "question_generation", "")
	response, err := chatModel.Chat(modelCtx, []chat.Message{
		{
			Role:    "user",
			Content: prompt,
		},
	}, &chat.ChatOptions{
		Temperature: 0.7,
		MaxTokens:   generateQuestionsMaxTokens(),
		Thinking:    &thinking,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate questions: %w", err)
	}

	return parseGeneratedQuestions(response.Content, questionCount), nil
}

// parseGeneratedQuestions drops inline <think> reasoning first: servers that
// ignore Thinking=false still emit it in content, and each reasoning line
// would otherwise be indexed as a question.
func parseGeneratedQuestions(content string, questionCount int) []string {
	lines := generatedQuestionLines(strings.TrimSpace(tools.StripThinkBlocks(content)))
	questions := make([]string, 0, questionCount)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "0123456789.-*) ")
		line = strings.TrimSpace(line)
		if line != "" && len(line) > 5 {
			questions = append(questions, line)
			if len(questions) >= questionCount {
				break
			}
		}
	}
	return questions
}

// generatedQuestionLines accepts plain lists and JSON question completions.
// Structured metadata must not become indexed questions or chat starters.
func generatedQuestionLines(content string) []string {
	if header, body, ok := strings.Cut(content, "\n"); ok &&
		(strings.TrimSpace(header) == "```" || strings.EqualFold(strings.TrimSpace(header), "```json")) {
		content = strings.TrimSpace(strings.TrimSuffix(body, "```"))
	}
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") {
		var value any
		if err := json.Unmarshal([]byte(content), &value); err == nil {
			if object, ok := value.(map[string]any); ok {
				value = object["questions"]
			}
			items, ok := value.([]any)
			if !ok {
				return nil
			}
			lines := make([]string, 0, len(items))
			for _, item := range items {
				if question, ok := item.(string); ok {
					lines = append(lines, question)
				}
			}
			return lines
		}
		// Do not turn incomplete JSON into individual question lines. Preserve
		// ordinary text such as "[RAG] How ..." and "{name} is used for ...".
		rest := strings.TrimSpace(content[1:])
		if rest == "" || strings.ContainsRune(`"{}[]`, rune(rest[0])) {
			return nil
		}
	}
	return strings.Split(content, "\n")
}

// RegenerateChunkQuestions synchronously refreshes Doc2Query-style auxiliary
// questions for the current chunk revision and atomically replaces their
// retrieval entries through updateChunkVector.
func (s *knowledgeService) RegenerateChunkQuestions(
	ctx context.Context, chunkID string,
) ([]types.GeneratedQuestion, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}
	if chunk.ChunkType != types.ChunkTypeText {
		return nil, fmt.Errorf("questions can only be generated for text chunks")
	}
	generationRevision := chunk.ContentRevision
	knowledge, kb, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}
	if knowledge.KnowledgeBaseID != chunk.KnowledgeBaseID || chunk.TenantID != knowledge.TenantID {
		return nil, werrors.NewForbiddenError("chunk does not belong to its knowledge document")
	}
	overrides, _ := knowledge.ProcessOverrides()
	questionModelID, err := resolveQuestionGenerationModel(ctx, s.modelService, kb, overrides)
	if err != nil {
		return nil, err
	}
	if questionModelID == "" {
		return nil, fmt.Errorf("a chat model is required for question generation")
	}
	chatModel, err := s.modelService.GetChatModel(ctx, questionModelID)
	if err != nil {
		return nil, err
	}
	resolveNeighbor := func(id string) string {
		if id == "" {
			return ""
		}
		neighbor, getErr := s.chunkRepo.GetChunkByID(ctx, tenantID, id)
		if getErr != nil || !sameChunkDocument(chunk, neighbor) {
			return ""
		}
		return neighbor.Content
	}
	config := ResolveProcessConfig(kb, overrides).QuestionGenerationConfig
	count := config.QuestionCount
	if count <= 0 {
		count = 3
	}
	if count > 10 {
		count = 10
	}
	questions, err := s.generateQuestionsWithContext(
		ctx, chatModel, chunk.Content, resolveNeighbor(chunk.PreChunkID),
		resolveNeighbor(chunk.NextChunkID), knowledge.Title, count, config.CustomInstructions,
	)
	if err != nil {
		return nil, err
	}
	latestChunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}
	if latestChunk.ContentRevision != generationRevision {
		return nil, ErrChunkRevisionConflict
	}
	chunk = latestChunk
	generated := make([]types.GeneratedQuestion, 0, len(questions))
	questionRevision := chunk.ContentRevision
	for _, question := range questions {
		generated = append(generated, types.GeneratedQuestion{
			ID: uuid.NewString(), Question: question, ContentRevision: &questionRevision,
		})
	}
	meta := &types.DocumentChunkMetadata{
		GeneratedQuestions: generated, GeneratedQuestionsRevision: chunk.ContentRevision,
	}
	if err := chunk.SetDocumentMetadata(meta); err != nil {
		return nil, err
	}
	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}
	if err := s.updateChunkVector(ctx, chunk.KnowledgeBaseID, []*types.Chunk{chunk}); err != nil {
		return nil, err
	}
	return generated, nil
}
