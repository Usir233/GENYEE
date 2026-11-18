package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/common"
	werrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/infrastructure/chunker"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser/anydoc"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/asr"
	"github.com/Usir233/GENYEE/internal/sourceloc"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/hibiken/asynq"
)

// settleKnowledgeBaseReadFailure decides what a parse task does when its
// knowledge base cannot be loaded. A missing KB is permanent: settle the row
// as failed and acknowledge the task. Any other read error is transient:
// return it so the queue retries, instead of turning a database blip into a
// permanently failed document. The datasource sync path makes the same
// distinction for its metadata reads.
func (s *knowledgeService) settleKnowledgeBaseReadFailure(
	ctx context.Context,
	knowledge *types.Knowledge,
	readErr error,
	logPrefix string,
) error {
	if !errors.Is(readErr, repository.ErrKnowledgeBaseNotFound) {
		logger.Errorf(ctx, "%s: failed to read knowledge base: %v", logPrefix, readErr)
		return fmt.Errorf("read knowledge base %s: %w", knowledge.KnowledgeBaseID, readErr)
	}
	logger.Warnf(ctx, "%s: knowledge base %s no longer exists, settling knowledge %s as failed",
		logPrefix, knowledge.KnowledgeBaseID, knowledge.ID)
	knowledge.ParseStatus = types.ParseStatusFailed
	knowledge.ErrorMessage = fmt.Sprintf("failed to get knowledge base: %v", readErr)
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		// Retry: acknowledging now would leave the terminal state unwritten.
		return fmt.Errorf("mark knowledge %s failed: %w", knowledge.ID, err)
	}
	return nil
}

// ProcessManualUpdate handles Asynq manual knowledge update tasks.
// It performs cleanup of old indexes/chunks (when NeedCleanup is true) and re-indexes the content.
func (s *knowledgeService) ProcessManualUpdate(ctx context.Context, t *asynq.Task) error {
	var payload types.ManualProcessPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal manual process task payload: %v", err)
		return nil
	}

	ctx = logger.WithRequestID(ctx, payload.RequestId)
	ctx = logger.WithField(ctx, "manual_process", payload.KnowledgeID)
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		// Retry: acking leaves the row pending with no task behind it.
		logger.Errorf(ctx, "ProcessManualUpdate: failed to get tenant: %v", err)
		return fmt.Errorf("get tenant %d: %w", payload.TenantID, err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		logger.Warnf(ctx, "ProcessManualUpdate: knowledge not found: %s", payload.KnowledgeID)
		return nil
	}
	if err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to get knowledge: %v", err)
		return fmt.Errorf("get knowledge %s: %w", payload.KnowledgeID, err)
	}
	if knowledge == nil {
		logger.Warnf(ctx, "ProcessManualUpdate: knowledge not found: %s", payload.KnowledgeID)
		return nil
	}

	if err := validateProcessingKnowledge(knowledge,
		payload.TenantID,
		payload.KnowledgeBaseID,
		payload.KnowledgeID); err != nil {
		return err
	}

	// Skip if already completed or being deleted
	if knowledge.ParseStatus == types.ParseStatusCompleted {
		logger.Infof(ctx, "ProcessManualUpdate: already completed, skipping: %s", payload.KnowledgeID)
		return nil
	}
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		logger.Infof(ctx, "ProcessManualUpdate: being deleted, skipping: %s", payload.KnowledgeID)
		return nil
	}
	if knowledge.ParseStatus == types.ParseStatusCancelled {
		logger.Infof(ctx, "ProcessManualUpdate: cancelled by user, skipping: %s", payload.KnowledgeID)
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return s.settleKnowledgeBaseReadFailure(ctx, knowledge, err, "ProcessManualUpdate")
	}
	if kb == nil || kb.ID != payload.KnowledgeBaseID || kb.TenantID != payload.TenantID {
		return fmt.Errorf("processing task KB owner changed: %w", asynq.SkipRetry)
	}
	ctx, err = access.WithKBTaskWrite(ctx, kb, payload.TenantID)
	if err != nil {
		return fmt.Errorf("invalid processing scope: %v: %w", err, asynq.SkipRetry)
	}

	// Re-check abort status right before marking processing — see the same
	// note in ProcessDocument for the cancel race this guards.
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "ProcessManualUpdate: knowledge aborted (%s), skipping: %s", status, knowledge.ID)
		return abortRetryErr(ctx, knowledge.ID, status)
	}
	// Update status to processing
	markKnowledgeProcessing(knowledge, time.Now())
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to update status to processing: %v", err)
		return fmt.Errorf("mark knowledge %s processing: %w", knowledge.ID, err)
	}

	// Allocate a fresh span-tracking attempt for this manual (re)index.
	// Without it attemptFromCtx stays 0, so processChunks drops all stage
	// spans and KnowledgePostProcess falls back to LatestAttempt — piling
	// this run's summary/wiki subspans onto the previous attempt's trace.
	attempt := 0
	if root, n, err := s.tracker().OpenAttempt(ctx, knowledge.ID, payload.LangfuseTraceID); err == nil && root != nil {
		attempt = n
	} else if err != nil {
		logger.Warnf(ctx, "ProcessManualUpdate: OpenAttempt failed for %s: %v", knowledge.ID, err)
	}
	ctx = withAttempt(ctx, attempt)

	// Cleanup old resources (indexes, chunks, graph) for update operations
	if payload.NeedCleanup {
		if err := s.cleanupKnowledgeResources(ctx, knowledge); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_id": payload.KnowledgeID,
			})
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("failed to cleanup old resources: %v", err)
			knowledge.UpdatedAt = time.Now()
			s.updateKnowledgeBestEffort(ctx, knowledge)
			return nil
		}
	}

	// Run manual processing (image resolution + chunking + embedding) synchronously within the worker
	return s.triggerManualProcessing(ctx, kb, knowledge, payload.Content, true)
}

// ProcessDocument handles Asynq document processing tasks
func (s *knowledgeService) ProcessDocument(ctx context.Context, t *asynq.Task) error {
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal document process task payload: %v", err)
		return nil
	}

	ctx = logger.WithRequestID(ctx, payload.RequestId)
	ctx = logger.WithField(ctx, "document_process", payload.KnowledgeID)
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// 获取任务重试信息，用于判断是否是最后一次重试。Lite 模式的执行器不提供
	// asynq 的重试计数，改用它注入的 TaskRetryMetadata；否则每次都算最后一次。
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	if retried, limit, ok := types.TaskRetryMetadataFromContext(ctx); ok {
		retryCount, maxRetry = retried, limit
	}
	isLastRetry := retryCount >= maxRetry

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		// Retry: acking leaves the row pending with no task behind it.
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return fmt.Errorf("get tenant %d: %w", payload.TenantID, err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	logger.Infof(ctx, "Processing document task: knowledge_id=%s, file_path=%s, retry=%d/%d",
		payload.KnowledgeID, payload.FilePath, retryCount, maxRetry)

	// 幂等性检查：获取knowledge记录
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		return nil
	}
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge: %v", err)
		return fmt.Errorf("get knowledge %s: %w", payload.KnowledgeID, err)
	}

	if knowledge == nil {
		return nil
	}

	if err := validateProcessingKnowledge(knowledge,
		payload.TenantID,
		payload.KnowledgeBaseID,
		payload.KnowledgeID); err != nil {
		return err
	}

	// A reparse opened a newer attempt after this task was queued (or while
	// an earlier delivery of it was running). Its row, chunks and counter
	// belong to the new run; this task must not touch them.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		logger.Infof(ctx, "Parse attempt %d superseded, skipping stale process task: %s",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}

	// 检查是否正在删除 / 已被用户取消 - 如果是则直接退出
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		logger.Infof(ctx, "Knowledge is being deleted, aborting processing: %s", payload.KnowledgeID)
		return nil
	}
	if knowledge.ParseStatus == types.ParseStatusCancelled {
		logger.Infof(ctx, "Knowledge cancelled by user, aborting processing: %s", payload.KnowledgeID)
		return nil
	}

	// 检查任务状态 - 幂等性处理
	if knowledge.ParseStatus == types.ParseStatusCompleted {
		logger.Infof(ctx, "Document already completed, skipping: %s", payload.KnowledgeID)
		return nil // 幂等：已完成的任务直接返回
	}

	if knowledge.ParseStatus == types.ParseStatusFailed {
		// 检查是否可恢复（例如：超时、临时错误等）
		// 对于不可恢复的错误，直接返回
		logger.Warnf(
			ctx,
			"Document processing previously failed: %s, error: %s",
			payload.KnowledgeID,
			knowledge.ErrorMessage,
		)
		// 这里可以根据错误类型判断是否可恢复，暂时允许重试
	}

	// 检查是否有部分处理（有chunks但状态不是completed）
	if knowledge.ParseStatus != "completed" && knowledge.ParseStatus != "pending" &&
		knowledge.ParseStatus != "processing" {
		// 状态异常，记录日志但继续处理
		logger.Warnf(ctx, "Unexpected parse status: %s for knowledge: %s", knowledge.ParseStatus, payload.KnowledgeID)
	}

	// 获取知识库信息
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return s.settleKnowledgeBaseReadFailure(ctx, knowledge, err, "ProcessDocument")
	}
	if kb == nil || kb.ID != payload.KnowledgeBaseID || kb.TenantID != payload.TenantID {
		return fmt.Errorf("processing task KB owner changed: %w", asynq.SkipRetry)
	}
	ctx, err = access.WithKBTaskWrite(ctx, kb, payload.TenantID)
	if err != nil {
		return fmt.Errorf("invalid processing scope: %v: %w", err, asynq.SkipRetry)
	}

	processOverrides, _ := knowledge.ProcessOverrides()
	eff := ResolveProcessConfig(kb, processOverrides)

	// Re-check abort status right before flipping to "processing" — closes
	// the race where the user cancels between the entry guard above and
	// this write (otherwise the worker would overwrite cancelled→processing
	// and downstream checkpoints would treat the run as live).
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "Knowledge aborted (%s) before marking processing: %s", status, knowledge.ID)
		return abortRetryErr(ctx, knowledge.ID, status)
	}
	if payload.FilePath != "" && knowledge.FilePath != "" && payload.FilePath != knowledge.FilePath {
		logger.Infof(ctx, "Document source replaced, skipping stale process task: %s", payload.KnowledgeID)
		return nil
	}
	if s.isKnowledgeSourceReplaced(ctx, knowledge) {
		logger.Infof(ctx, "Document source replaced, skipping stale process task: %s", payload.KnowledgeID)
		return nil
	}
	markKnowledgeProcessing(knowledge, time.Now())
	if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "failed to update knowledge status to processing: %v", err)
		return fmt.Errorf("mark knowledge %s processing: %w", knowledge.ID, err)
	}
	if s.isKnowledgeSourceReplaced(ctx, knowledge) {
		logger.Infof(ctx, "Document source replaced, aborting after status update: %s", payload.KnowledgeID)
		return nil
	}

	// Resolve the attempt for span tracking. The enqueue site sets
	// payload.Attempt to a fresh number for the initial parse and to
	// max+1 for each user-initiated reparse. Asynq retries within a
	// single user action keep the same payload (so retries record
	// onto the same attempt). For payloads predating this code we
	// fall back to OpenAttempt.
	attempt := payload.Attempt
	if attempt <= 0 {
		if root, n, err := s.tracker().OpenAttempt(ctx, knowledge.ID, payload.LangfuseTraceID); err == nil &&
			root != nil {
			attempt = n
		}
	}
	ctx = withAttempt(ctx, attempt)

	// 检查多模态配置（仅对文件导入）
	if payload.FilePath != "" && !payload.EnableMultimodal && IsImageType(payload.FileType) {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", ErrImageNotParse).Errorf("processDocument image without enable multimodal")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = ErrImageNotParse.Error()
		knowledge.UpdatedAt = time.Now()
		s.updateKnowledgeBestEffort(ctx, knowledge)
		return nil
	}

	// 检查音频ASR配置（仅对文件导入）
	if payload.FilePath != "" && IsAudioType(payload.FileType) && !eff.ASRConfig.IsASREnabled() {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			Errorf("processDocument audio without ASR model configured")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "上传音频文件需要设置ASR语音识别模型"
		knowledge.UpdatedAt = time.Now()
		s.updateKnowledgeBestEffort(ctx, knowledge)
		return nil
	}

	// 视频文件不再支持入库解析
	if payload.FilePath != "" && IsVideoType(payload.FileType) {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			Errorf("processDocument video not supported")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "暂不支持视频文件"
		knowledge.UpdatedAt = time.Now()
		s.updateKnowledgeBestEffort(ctx, knowledge)
		return nil
	}

	// New pipeline: convert -> store images -> chunk -> vectorize -> multimodal tasks
	var convertResult *types.ReadResult
	var chunks []types.ParsedChunk

	switch {
	case payload.FileURL != "":
		// file_url import: SSRF re-check (防 DNS 重绑定), download, persist, then delegate to convert()
		if err := secutils.ValidateURLForSSRF(payload.FileURL); err != nil {
			logger.Errorf(ctx, "File URL rejected for SSRF protection in ProcessDocument: %s, err: %v", payload.FileURL,
				err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "File URL is not allowed for security reasons"
			knowledge.UpdatedAt = time.Now()
			s.updateKnowledgeBestEffort(ctx, knowledge)
			return nil
		}

		resolvedFileName := payload.FileName
		resolvedFileType := payload.FileType
		contentBytes, err := downloadFileFromURL(ctx, payload.FileURL, &resolvedFileName, &resolvedFileType)
		if err != nil {
			logger.Errorf(ctx, "Failed to download file from URL: %s, error: %v", payload.FileURL, err)
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.updateKnowledgeBestEffort(ctx, knowledge)
			}
			return fmt.Errorf("failed to download file from URL: %w", err)
		}

		if resolvedFileType != "" && !isSupportedImportExtension(resolvedFileType) {
			logger.Errorf(ctx, "Unsupported file type resolved from file URL: %s", resolvedFileType)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("unsupported file type: %s", resolvedFileType)
			knowledge.UpdatedAt = time.Now()
			s.updateKnowledgeBestEffort(ctx, knowledge)
			return nil
		}

		if resolvedFileName != "" && knowledge.FileName == "" {
			knowledge.FileName = resolvedFileName
		}
		if resolvedFileType != "" && knowledge.FileType == "" {
			knowledge.FileType = resolvedFileType
			s.updateKnowledgeBestEffort(ctx, knowledge)
		}

		fileSvc := s.resolveFileService(ctx, kb)
		filePath, err := fileSvc.SaveBytes(ctx, contentBytes, payload.TenantID, resolvedFileName, true)
		if err != nil {
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.updateKnowledgeBestEffort(ctx, knowledge)
			}
			return fmt.Errorf("failed to save downloaded file: %w", err)
		}

		payload.FilePath = filePath
		payload.FileName = resolvedFileName
		payload.FileType = resolvedFileType
		convertResult, err = s.convert(ctx, payload, kb, knowledge, eff, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
	case payload.URL != "":
		// URL import
		convertResult, err = s.convert(ctx, payload, kb, knowledge, eff, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
		// Update knowledge title from extracted page title if not already set
		if knowledge.Title == "" || knowledge.Title == payload.URL {
			if extractedTitle := convertResult.Metadata["title"]; extractedTitle != "" {
				knowledge.Title = extractedTitle
				knowledge.UpdatedAt = time.Now()
				if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
					logger.Warnf(ctx, "Failed to update knowledge title from extracted page title: %v", err)
				} else {
					logger.Infof(ctx, "Updated knowledge title to extracted page title: %s", extractedTitle)
				}
			}
		}
	case len(payload.Passages) > 0:
		// Text passage import - direct chunking, no conversion needed
		passageChunks := make([]types.ParsedChunk, 0, len(payload.Passages))
		start, end := 0, 0
		for i, p := range payload.Passages {
			if p == "" {
				continue
			}
			end += len([]rune(p))
			passageChunks = append(passageChunks, types.ParsedChunk{
				Content: p,
				Seq:     i,
				Start:   start,
				End:     end,
			})
			start = end
		}
		passageOpts := ProcessChunksOptions{
			EnableQuestionGeneration: payload.EnableQuestionGeneration,
			QuestionCount:            payload.QuestionCount,
		}
		return s.processChunks(ctx, kb, knowledge, passageChunks, passageOpts)
	default:
		// File import
		convertResult, err = s.convert(ctx, payload, kb, knowledge, eff, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
	}

	// Step 1.5: ASR transcription for audio files
	if convertResult != nil && convertResult.IsAudio && len(convertResult.AudioData) > 0 {
		if !eff.ASRConfig.IsASREnabled() {
			logger.Error(ctx, "Audio file detected but ASR is not configured")
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "ASR model is not configured for audio transcription"
			knowledge.UpdatedAt = time.Now()
			s.updateKnowledgeBestEffort(ctx, knowledge)
			return nil
		}

		logger.Infof(ctx, "[ASR] Starting audio transcription for knowledge %s, audio size=%d bytes",
			knowledge.ID, len(convertResult.AudioData))

		asrModel, err := s.modelService.GetASRModel(ctx, eff.ASRConfig.ModelID)
		if err != nil {
			logger.Errorf(ctx, "[ASR] Failed to get ASR model: %v", err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("failed to get ASR model: %v", err)
			knowledge.UpdatedAt = time.Now()
			s.updateKnowledgeBestEffort(ctx, knowledge)
			return nil
		}

		// The knowledge base's language hint; the vendor declares where it
		// goes, or that it takes none.
		asrCtx := asr.WithLanguage(ctx, eff.ASRConfig.Language)
		transcriptionResult, err := asrModel.Transcribe(asrCtx, convertResult.AudioData, knowledge.FileName)
		if err != nil {
			logger.Errorf(ctx, "[ASR] Transcription failed: %v", err)
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = fmt.Sprintf("audio transcription failed: %v", err)
				knowledge.UpdatedAt = time.Now()
				s.updateKnowledgeBestEffort(ctx, knowledge)
			}
			return fmt.Errorf("audio transcription failed: %w", err)
		}

		transcribedText, timeBlocks := transcriptWithSegments(transcriptionResult)

		if transcribedText == "" {
			logger.Warn(ctx, "[ASR] Transcription returned empty text")
			transcribedText = "[No speech detected in audio file]"
			timeBlocks = nil
		}

		logger.Infof(ctx, "[ASR] Transcription completed, text length=%d", len(transcribedText))
		// Replace the audio placeholder with the transcribed text
		convertResult.MarkdownContent = transcribedText
		convertResult.SourceBlocks = timeBlocks
		convertResult.IsAudio = false
		convertResult.AudioData = nil
	}

	// Normalize inline HTML tables from any parser engine (MinerU, builtin,
	// markitdown, paddleocr-vl, ...) into GFM, or row-newline-separated HTML
	// for merged / non-GFM tables, so the chunker can split them.
	// Runs before image resolution so <img> inside HTML tables becomes
	// markdown the resolver can persist, instead of rewriting data-URI
	// <img> tags to `![...](...)` and then escaping them in a later
	// HTML→GFM pass. Line endings are normalized first so injected row
	// breaks are plain LF.
	// Source blocks were recorded against the parser's markdown; keep it so
	// they can be re-aimed after the rewrites below.
	var parsedMarkdown string
	var sourceBlocks []types.SourceBlock
	if convertResult != nil {
		parsedMarkdown = convertResult.MarkdownContent
		sourceBlocks = convertResult.SourceBlocks
		for i := range sourceBlocks {
			sourceBlocks[i].Locator.SourceHash = knowledge.FileHash
		}
	}
	sanitizeReadResult(convertResult)
	normalizeStarted := time.Now()
	if convertResult != nil {
		convertResult.MarkdownContent = chunker.NormalizeLineEndings(convertResult.MarkdownContent)
		if convertResult.MarkdownContent != "" {
			convertResult.MarkdownContent = docparser.NormalizeHTMLTables(convertResult.MarkdownContent)
		}
	}
	normalizedBytes := 0
	if convertResult != nil {
		normalizedBytes = len(convertResult.MarkdownContent)
	}
	logger.Infof(ctx, "Document text normalization completed in %s (knowledge=%s input_bytes=%d output_bytes=%d)",
		time.Since(normalizeStarted), knowledge.ID, len(parsedMarkdown), normalizedBytes)

	// Step 2: Store images and update markdown references
	var storedImages []docparser.StoredImage
	var imageFileSvc interfaces.FileService
	var inlineParseErr error

	if s.imageResolver != nil && convertResult != nil {
		imageFileSvc = s.resolveFileService(ctx, kb)
		tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
		updatedMarkdown, images, resolveErr := s.imageResolver.ResolveAndStore(
			ctx, convertResult, imageFileSvc, tenantID)
		if errors.Is(resolveErr, docparser.ErrAmbiguousInlineImage) {
			inlineParseErr = resolveErr
			images = nil // ResolveAndStore already rolled back this attempt's uploads.
		} else if resolveErr != nil {
			_, err := s.failKnowledge(ctx, knowledge, isLastRetry, "store inline image: %v", resolveErr)
			return err
		}
		convertResult.MarkdownContent = updatedMarkdown
		storedImages = images

	}
	if inlineParseErr != nil ||
		(convertResult != nil && docparser.HasUnresolvedInlineImagePayload(convertResult.MarkdownContent)) {
		message := "unresolved inline image base64 payload remains before chunking"
		if inlineParseErr != nil {
			message = inlineParseErr.Error()
		}
		logger.Errorf(ctx, "Knowledge %s: %s", knowledge.ID, message)
		cleanupUnboundInlineImages(ctx, imageFileSvc, storedImages)
		knowledge.ParseStatus = types.ParseStatusFailed
		knowledge.ErrorMessage = message
		knowledge.UpdatedAt = time.Now()
		s.beginStage(ctx, knowledge.ID, types.StageChunking, nil)
		s.failStage(ctx, knowledge.ID, types.StageChunking, werrors.ErrCodeChunkingFailed, message, nil)
		if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
			return fmt.Errorf("%w: persist inline image processing failure: %v", asynq.SkipRetry, err)
		}
		return fmt.Errorf("%s: %w", message, asynq.SkipRetry)
	}

	if s.imageResolver != nil && convertResult != nil {
		tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
		// Resolve remote http(s) images (e.g. markdown external URLs) → download + upload to storage.
		// ResolveAndStore handles inline bytes and base64; ResolveRemoteImages handles http/https URLs.
		updatedContent, remoteImages, remoteErr := s.imageResolver.ResolveRemoteImages(
			ctx, convertResult.MarkdownContent, imageFileSvc, tenantID)
		if remoteErr != nil {
			logger.Warnf(ctx, "Remote image resolution partially failed: %v", remoteErr)
		}
		if len(remoteImages) > 0 {
			logger.Infof(ctx, "Resolved %d remote images for knowledge %s", len(remoteImages), knowledge.ID)
			convertResult.MarkdownContent = updatedContent
			storedImages = append(storedImages, remoteImages...)
		}

		// 本地和远程图片分批收集，合并后恢复文档顺序供图片任务编号。
		storedImages = docparser.SortImagesByPosition(convertResult.MarkdownContent, storedImages)

		logger.Infof(ctx, "Resolved %d total images for knowledge %s", len(storedImages), knowledge.ID)
	}

	// Claim the stored images for this document before chunking proceeds:
	// the file proxies authorize images through resource bindings, and an
	// unbound extracted image renders broken for org-shared KB viewers (#3342).
	if bindErr := s.bindStoredImages(ctx, knowledge, storedImages); bindErr != nil {
		cleanupFailedImageBindings(ctx, imageFileSvc, s.resourceCatalog, types.ResourceOwnerKnowledge, knowledge.ID,
			storedImages)
		_, err := s.failKnowledge(ctx, knowledge, isLastRetry, "bind stored images: %v", bindErr)
		return err
	}

	var sourceIndex *sourceloc.Index
	if convertResult != nil {
		sourceIndex = buildSourceIndex(parsedMarkdown, sourceBlocks, convertResult.MarkdownContent, storedImages)
	}

	// Step 3: Split into chunks using Go chunker. Line endings and inline
	// HTML tables were normalized before image resolution above.
	chunkCfg := buildSplitterConfigFromChunking(eff.ChunkingConfig)
	chunkStarted := time.Now()

	multimodalImages := imagesForMultimodal(storedImages)
	processOpts := ProcessChunksOptions{
		EnableQuestionGeneration: payload.EnableQuestionGeneration,
		QuestionCount:            payload.QuestionCount,
		EnableMultimodal:         payload.EnableMultimodal,
		StoredImages:             multimodalImages,
	}

	if convertResult != nil {
		processOpts.Metadata = convertResult.Metadata
	}

	if eff.ChunkingConfig.EnableParentChild {
		parentCfg, childCfg := buildParentChildConfigs(eff.ChunkingConfig, chunkCfg)
		pcResult := chunker.SplitParentChild(convertResult.MarkdownContent, parentCfg, childCfg)
		chunks = make([]types.ParsedChunk, len(pcResult.Children))
		for i, c := range pcResult.Children {
			chunks[i] = types.ParsedChunk{
				Content:        c.Content,
				ContextHeader:  c.ContextHeader,
				Seq:            c.Seq,
				Start:          c.Start,
				End:            c.End,
				ParentIndex:    c.ParentIndex,
				SourceLocators: sourceIndex.Locators(c.Start, c.End),
			}
		}
		parentChunks := make([]types.ParsedParentChunk, len(pcResult.Parents))
		for i, p := range pcResult.Parents {
			parentChunks[i] = types.ParsedParentChunk{
				Content: p.Content, Seq: p.Seq, Start: p.Start, End: p.End,
				SourceLocators: sourceIndex.Locators(p.Start, p.End),
			}
		}
		processOpts.ParentChunks = parentChunks
		logger.Infof(ctx, "Split document into %d parent + %d child chunks for knowledge %s",
			len(pcResult.Parents), len(pcResult.Children), knowledge.ID)
	} else {
		splitChunks := chunker.Split(convertResult.MarkdownContent, chunkCfg)
		chunks = make([]types.ParsedChunk, len(splitChunks))
		for i, c := range splitChunks {
			chunks[i] = types.ParsedChunk{
				Content:        c.Content,
				ContextHeader:  c.ContextHeader,
				Seq:            c.Seq,
				Start:          c.Start,
				End:            c.End,
				SourceLocators: sourceIndex.Locators(c.Start, c.End),
			}
		}
		logger.Infof(ctx, "Split document into %d chunks for knowledge %s", len(chunks), knowledge.ID)
	}
	logger.Infof(ctx, "Document chunking completed in %s (knowledge=%s input_bytes=%d chunks=%d)",
		time.Since(chunkStarted), knowledge.ID, len(convertResult.MarkdownContent), len(chunks))

	// Step 4: Process chunks (vectorize + index + enqueue async tasks)
	return s.processChunks(ctx, kb, knowledge, chunks, processOpts)
}

// sanitizeReadResult protects every text field that can cross from a parser
// into the embedding, storage, or tracing layers. A parser may return a Go
// string containing arbitrary bytes even though the string type itself does
// not enforce UTF-8 validity.
func sanitizeReadResult(result *types.ReadResult) {
	if result == nil {
		return
	}
	result.MarkdownContent = common.CleanInvalidUTF8(result.MarkdownContent)
	result.ImageDirPath = common.CleanInvalidUTF8(result.ImageDirPath)
	result.Error = common.CleanInvalidUTF8(result.Error)
	for key, value := range result.Metadata {
		result.Metadata[key] = common.CleanInvalidUTF8(value)
	}
	for i := range result.ImageRefs {
		result.ImageRefs[i].Filename = common.CleanInvalidUTF8(result.ImageRefs[i].Filename)
		result.ImageRefs[i].OriginalRef = common.CleanInvalidUTF8(result.ImageRefs[i].OriginalRef)
		result.ImageRefs[i].MimeType = common.CleanInvalidUTF8(result.ImageRefs[i].MimeType)
		result.ImageRefs[i].StorageKey = common.CleanInvalidUTF8(result.ImageRefs[i].StorageKey)
	}
}

// convert handles both file and URL reading using a unified ReadRequest.
func (s *knowledgeService) convert(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	eff types.EffectiveProcessConfig,
	isLastRetry bool,
) (*types.ReadResult, error) {
	// Stage tracking: docreader. Mark the stage as running here so the
	// timeline reflects "DocReader" the moment a worker picks the task
	// up — before that, the stage stays "pending" from the initial
	// upload. Failure/skip transitions are emitted at the specific
	// failure points below; success is emitted at the bottom.
	docInput := types.JSONMap{
		"file_name": payload.FileName,
		"file_type": payload.FileType,
		"is_url":    payload.URL != "",
	}
	if payload.URL != "" {
		docInput["url"] = payload.URL
	}
	s.beginStage(ctx, knowledge.ID, types.StageDocReader, docInput)
	isURL := payload.URL != ""
	fileType := payload.FileType
	tenantOverrides := s.getParserEngineOverridesFromContext(ctx)
	var uploadOverrides map[string]string
	if processOverrides, err := knowledge.ProcessOverrides(); err == nil && processOverrides != nil {
		uploadOverrides = processOverrides.ParserEngineOverrides
	}
	mergedOverrides := MergeParserEngineOverrides(tenantOverrides, uploadOverrides)
	applyParserRuleOverrides(mergedOverrides, eff.ChunkingConfig, fileType)
	if err := validateParserEngineOverrideURLs(mergedOverrides); err != nil {
		logger.Errorf(ctx, "Parser endpoint rejected for SSRF protection: %v", err)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "Parser endpoint is not allowed for security reasons"
		knowledge.UpdatedAt = time.Now()
		s.updateKnowledgeBestEffort(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderParseFailed, knowledge.ErrorMessage, err)
		return nil, nil
	}

	if isURL {
		if err := secutils.ValidateURLForSSRF(payload.URL); err != nil {
			logger.Errorf(ctx, "URL rejected for SSRF protection: %s, err: %v", payload.URL, err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "URL is not allowed for security reasons"
			knowledge.UpdatedAt = time.Now()
			s.updateKnowledgeBestEffort(ctx, knowledge)
			s.failStage(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, "URL rejected for security reasons", err)
			return nil, nil
		}
	}

	parserEngine := eff.ChunkingConfig.ResolveParserEngine(fileType)
	if isURL {
		parserEngine = eff.ChunkingConfig.ResolveParserEngine("url")
	}

	logger.Infof(ctx, "[convert] kb=%s fileType=%s isURL=%v engine=%q rules=%+v",
		kb.ID, fileType, isURL, parserEngine, eff.ChunkingConfig.ParserEngineRules)

	reader := s.resolveDocReader(ctx, parserEngine, fileType, isURL, mergedOverrides)
	if reader == nil {
		logger.Errorf(ctx, "[convert] no doc reader for kb=%s knowledge=%s fileType=%s engine=%q isURL=%v",
			kb.ID, knowledge.ID, fileType, parserEngine, isURL)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "Document parsing service is not configured. " +
			"Please use text/paragraph import or set DOCREADER_ADDR."
		knowledge.UpdatedAt = time.Now()
		s.updateKnowledgeBestEffort(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderUnavailable, knowledge.ErrorMessage, nil)
		return nil, nil
	}

	req := &types.ReadRequest{
		URL:                   payload.URL,
		Title:                 knowledge.Title,
		ParserEngine:          parserEngine,
		RequestID:             payload.RequestId,
		ParserEngineOverrides: mergedOverrides,
	}

	if !isURL {
		fileReader, err := s.resolveFileServiceForPath(ctx, kb, payload.FilePath).GetFile(ctx, payload.FilePath)
		if err != nil {
			result, taskErr := s.failKnowledge(ctx, knowledge, isLastRetry, "failed to get file: %v", err)
			s.failStageWithRetry(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, "failed to get file", err, taskErr != nil)
			return result, taskErr
		}
		defer func() { _ = fileReader.Close() }()
		maxDocBytes := maxKnowledgeUploadBytes()
		contentBytes, err := io.ReadAll(io.LimitReader(fileReader, maxDocBytes+1))
		if err != nil {
			result, taskErr := s.failKnowledge(ctx, knowledge, isLastRetry, "failed to read file: %v", err)
			s.failStageWithRetry(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, "failed to read file", err, taskErr != nil)
			return result, taskErr
		}
		if int64(len(contentBytes)) > maxDocBytes {
			limitErr := fmt.Errorf("file exceeds maximum allowed size of %d bytes", maxDocBytes)
			knowledge.ParseStatus = types.ParseStatusFailed
			knowledge.ErrorMessage = limitErr.Error()
			knowledge.UpdatedAt = time.Now()
			if updErr := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); updErr != nil {
				logger.Errorf(ctx, "failed to persist oversize error for %s: %v", knowledge.ID, updErr)
			}
			s.failStage(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, limitErr.Error(), limitErr)
			return nil, nil
		}
		req.FileContent = contentBytes
		req.FileName = payload.FileName
		req.FileType = fileType
	}

	result, err := s.callDocReaderWithTimeout(ctx, reader, req)
	if err == nil && result == nil {
		err = fmt.Errorf("document parser returned no result")
	}
	// Parser resource limits are permanent document rejections, not transient reader failures.
	if errors.Is(err, docparser.ErrCSVMarkdownTooLarge) || errors.Is(err, anydoc.ErrResourceLimit) {
		result, err = &types.ReadResult{Error: err.Error()}, nil
	}
	if err != nil {
		code, message := docReaderFailure(err)
		if engineMessage := parseFailureMessageForEngine(parserEngine, err); engineMessage != "" {
			message = engineMessage
		}
		logger.Errorf(ctx, "[convert] DocReader failed knowledge=%s engine=%s code=%s: %v",
			knowledge.ID, parserEngine, code, err)
		result, taskErr := s.failKnowledge(ctx, knowledge, isLastRetry, "%s", message)
		s.failStageWithRetry(ctx, knowledge.ID, types.StageDocReader,
			code, message, err, taskErr != nil)
		return result, taskErr
	}
	sanitizeReadResult(result)
	if result.Error != "" {
		logger.Errorf(ctx, "[convert] parser returned error kb=%s knowledge=%s file=%q type=%s engine=%q: %s",
			kb.ID, knowledge.ID, req.FileName, fileType, parserEngine, result.Error)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = result.Error
		knowledge.UpdatedAt = time.Now()
		if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
			logger.Errorf(ctx, "failed to persist parser error for %s: %v", knowledge.ID, err)
		}
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderParseFailed, result.Error, nil)
		return nil, nil
	}
	if !isURL {
		attachStructureBlocks(ctx, fileType, req.FileContent, result)
	}
	docOutput := types.JSONMap{
		"text_length":  len(result.MarkdownContent),
		"images_found": len(result.ImageRefs),
		"is_audio":     result.IsAudio,
	}
	if pages := result.Metadata["pages"]; pages != "" {
		docOutput["pages"] = pages
	}
	s.endStage(ctx, knowledge.ID, types.StageDocReader, docOutput)
	return result, nil
}

// callDocReaderWithTimeout wraps the DocReader RPC in a child context whose
// deadline is min(parent_deadline, DocReaderCallTimeout). Without this cap,
// a hung docreader (network partition, GC pause, OCR runaway) silently
// burns the whole DocumentProcessTimeout budget and pins a worker for hours
// — the #1 cause of "knowledge stuck in processing" reports.
//
// On timeout we annotate the error so retries / dead-letter consumers can
// distinguish "docreader was slow" from "docreader returned an error".
func (s *knowledgeService) callDocReaderWithTimeout(
	ctx context.Context, reader interfaces.DocReader, req *types.ReadRequest,
) (*types.ReadResult, error) {
	timeout := 30 * time.Minute
	if s.config != nil && s.config.KnowledgeBase != nil && s.config.KnowledgeBase.DocReaderCallTimeout > 0 {
		timeout = s.config.KnowledgeBase.DocReaderCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	result, err := reader.Read(callCtx, req)
	elapsed := time.Since(start)
	if err != nil {
		// Promote DeadlineExceeded into a clearer message; retain underlying
		// error via %w so errors.Is(callCtx.Err(), context.DeadlineExceeded)
		// still works for upstream classification.
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			logger.Errorf(ctx, "[convert] docreader call timed out after %s (limit %s) for %q",
				elapsed, timeout, req.FileName)
			return nil, fmt.Errorf("docreader call timeout after %s: %w", timeout, err)
		}
		return nil, err
	}
	logger.Infof(ctx, "[convert] docreader call ok in %s for %q", elapsed, req.FileName)
	return result, nil
}

// isLikelyRateLimitError performs a fuzzy classification of an error as a
// rate-limit / quota / backpressure failure. We only need a hint — the
// caller maps to one of two error_codes so the UI can offer "retry later"
// vs. "fix configuration" advice. False positives are harmless (the
// detail is preserved in error_detail anyway).
func isLikelyRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"rate limit", "ratelimit", "429", "too many requests", "quota"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// resolveDocReader picks the reader for one parse request. The engine catalog
// itself lives in the docparser registry; this only supplies the dependencies
// the service owns. Returns nil when the chosen engine cannot run — an
// unconfigured cloud engine, a disconnected docreader — after logging why.
func (s *knowledgeService) resolveDocReader(
	ctx context.Context, engine, fileType string, isURL bool, overrides map[string]string,
) interfaces.DocReader {
	reader, err := docparser.NewReader(ctx, engine, fileType, isURL, docparser.ReaderDeps{
		Overrides: overrides,
		Remote:    s.documentReader,
	})
	if err != nil {
		logger.Warnf(ctx, "[resolveDocReader] engine=%q fileType=%q unusable: %v", engine, fileType, err)
		return nil
	}
	return reader
}

// preflightReparseEngine checks that the parser engine a reparse will use can
// be created, with the same engine and override resolution as the parse
// worker. It does not parse anything. Call it before cleanup so an unavailable
// engine rejects the request while the old content is still in place.
func (s *knowledgeService) preflightReparseEngine(
	ctx context.Context, knowledge *types.Knowledge, eff types.EffectiveProcessConfig,
) error {
	var fileType string
	isURL := false
	switch {
	case knowledge.FilePath != "":
		fileType = getFileType(knowledge.FileName)
	case knowledge.Type == "file_url" && knowledge.Source != "":
		fileType = knowledge.FileType
	case knowledge.Type == "url" && knowledge.Source != "":
		isURL = true
	default:
		return nil // nothing will be parsed; reparse handles this case itself
	}

	var uploadOverrides map[string]string
	if po, err := knowledge.ProcessOverrides(); err == nil && po != nil {
		uploadOverrides = po.ParserEngineOverrides
	}
	overrides := MergeParserEngineOverrides(s.getParserEngineOverridesFromContext(ctx), uploadOverrides)
	applyParserRuleOverrides(overrides, eff.ChunkingConfig, fileType)

	engine := eff.ChunkingConfig.ResolveParserEngine(fileType)
	if isURL {
		engine = eff.ChunkingConfig.ResolveParserEngine("url")
	}
	if s.resolveDocReader(ctx, engine, fileType, isURL, overrides) == nil {
		return werrors.NewBadRequestError(fmt.Sprintf(
			"Parser engine %q is not available, so the document was not reparsed and its existing content is kept. "+
				"Check the parser engine settings or the document parsing service.", engine))
	}
	return nil
}

// failKnowledge marks knowledge as failed (only on last retry) and returns an error.
func (s *knowledgeService) failKnowledge(
	ctx context.Context,
	knowledge *types.Knowledge,
	isLastRetry bool,
	format string,
	args ...interface{},
) (*types.ReadResult, error) {
	errMsg := fmt.Sprintf(format, args...)
	if s.isKnowledgeSourceReplaced(ctx, knowledge) {
		logger.Infof(ctx, "Skip failing knowledge %s: source file was replaced", knowledge.ID)
		return nil, nil
	}
	if isLastRetry {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = errMsg
		knowledge.UpdatedAt = time.Now()
		if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
			logger.Errorf(ctx, "failed to persist knowledge failure for %s: %v", knowledge.ID, err)
		}
	}
	return nil, fmt.Errorf(format, args...)
}

func (s *knowledgeService) updateKnowledgeBestEffort(ctx context.Context, knowledge *types.Knowledge) {
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Warnf(ctx, "failed to update knowledge %s: %v", knowledge.ID, err)
	}
}
