package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	werrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// UpsertFAQEntries imports or appends FAQ entries asynchronously.
// Returns task ID (UUID) for tracking import progress.
func (s *knowledgeService) UpsertFAQEntries(ctx context.Context,
	kbID string, payload *types.FAQBatchUpsertPayload,
) (string, error) {
	if payload == nil || len(payload.Entries) == 0 {
		return "", werrors.NewBadRequestError("FAQ 条目不能为空")
	}
	if payload.Mode == "" {
		payload.Mode = types.FAQBatchModeAppend
	}
	if payload.Mode != types.FAQBatchModeAppend && payload.Mode != types.FAQBatchModeReplace {
		return "", werrors.NewBadRequestError("模式仅支持 append 或 replace")
	}

	// 验证知识库是否存在且有效
	kb, ctx, err := s.writableFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return "", err
	}
	if err := s.validateFAQImportTags(ctx, kb, payload.Entries); err != nil {
		return "", err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 使用传入的TaskID，如果没传则生成增强的TaskID。
	// 客户端传入的 task_id 会进文件名和 Redis key，必须是无路径分隔符的标识符。
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID == "" {
		taskID = secutils.GenerateTaskID("faq_import", tenantID, kbID)
	} else {
		if err := secutils.ValidateTaskID(taskID); err != nil {
			return "", werrors.NewBadRequestError("task_id 格式不合法")
		}
		caller := types.CallerFromContext(ctx)
		if parsedTenant, tErr := secutils.TaskTenantID(taskID); tErr == nil && parsedTenant != 0 {
			if parsedTenant != tenantID && (caller.TenantID == 0 || parsedTenant != caller.TenantID) {
				return "", werrors.NewBadRequestError("task_id 格式不合法")
			}
		}
		if existingProg, pErr := s.GetFAQImportProgress(context.Background(), taskID); pErr == nil &&
			existingProg != nil {
			if existingProg.KBID != "" && existingProg.KBID != kbID {
				return "", werrors.NewBadRequestError("task_id 已被其他任务占用")
			}
		}
	}

	var knowledgeID string

	// 检查是否有正在进行的导入任务（通过Redis）
	runningTaskID, err := s.getRunningFAQImportTaskID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to check running import task: %v", err)
		// 检查失败不影响导入，继续执行
	} else if runningTaskID != "" {
		logger.Warnf(ctx, "Import task already running for KB %s: %s", kbID, runningTaskID)
		return "", werrors.NewBadRequestError(fmt.Sprintf("该知识库已有导入任务正在进行中（任务ID: %s），请等待完成后再试", runningTaskID))
	}

	// 确保 FAQ knowledge 存在
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return "", fmt.Errorf("failed to ensure FAQ knowledge: %w", err)
	}
	knowledgeID = faqKnowledge.ID

	// 记录任务入队时间
	enqueuedAt := time.Now().Unix()
	instanceID := uuid.NewString()
	runningInfoSet := false
	enqueueSucceeded := false

	// 设置 KB 的运行中任务信息
	if err := s.setRunningFAQImportInfo(ctx, kbID, &runningFAQImportInfo{
		TaskID:     taskID,
		EnqueuedAt: enqueuedAt,
		InstanceID: instanceID,
	}); err != nil {
		logger.Errorf(ctx, "Failed to set running FAQ import task info: %v", err)
		// 不影响任务执行，继续
	} else {
		runningInfoSet = true
	}
	defer func() {
		if !runningInfoSet || enqueueSucceeded {
			return
		}
		if clearErr := s.clearRunningFAQImportInfoIfMatches(ctx, kbID, taskID, instanceID,
			enqueuedAt); clearErr != nil {
			logger.Warnf(ctx, "Failed to clear FAQ import running info after setup failure: %v", clearErr)
		}
	}()

	// 初始化导入任务状态到Redis
	progress := &types.FAQImportProgress{
		TaskID:        taskID,
		KBID:          kbID,
		KnowledgeID:   knowledgeID,
		Status:        types.FAQImportStatusPending,
		Progress:      0,
		Total:         len(payload.Entries),
		Processed:     0,
		SuccessCount:  0,
		FailedCount:   0,
		FailedEntries: make([]types.FAQFailedEntry, 0),
		Message:       "任务已创建，等待处理",
		CreatedAt:     time.Now().Unix(),
		UpdatedAt:     time.Now().Unix(),
		DryRun:        payload.DryRun,
	}
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to initialize FAQ import task status: %v", err)
		return "", fmt.Errorf("failed to initialize task: %w", err)
	}

	logger.Infof(ctx, "FAQ import task initialized: %s, kb_id: %s, total entries: %d, dry_run: %v",
		taskID, kbID, len(payload.Entries), payload.DryRun)

	// Enqueue FAQ import task to Asynq
	logger.Info(ctx, "Enqueuing FAQ import task to Asynq")

	// 构建任务 payload
	taskPayload := types.FAQImportPayload{
		TenantID:    tenantID,
		TaskID:      taskID,
		KBID:        kbID,
		KnowledgeID: knowledgeID,
		Mode:        payload.Mode,
		DryRun:      payload.DryRun,
		EnqueuedAt:  enqueuedAt,
		InstanceID:  instanceID,
		Initiator:   types.TaskInitiatorFromContext(ctx),
	}

	// 阈值：超过 200 条或序列化后超过 50KB 时使用对象存储
	const (
		entryCountThreshold  = 200
		payloadSizeThreshold = 50 * 1024 // 50KB
	)

	entryCount := len(payload.Entries)
	if entryCount > entryCountThreshold {
		// 数据量较大，上传到对象存储
		entriesData, err := json.Marshal(payload.Entries)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal FAQ entries: %v", err)
			return "", fmt.Errorf("failed to marshal entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries size: %d bytes, uploading to object storage", len(entriesData))

		// 上传到私有桶（主桶），任务处理完成后清理
		fileName, err := faqImportEntriesFileName(taskID, enqueuedAt)
		if err != nil {
			return "", fmt.Errorf("invalid task id for object name: %w", err)
		}
		entriesURL, err := s.fileSvc.SaveBytes(ctx, entriesData, tenantID, fileName, false)
		if err != nil {
			logger.Errorf(ctx, "Failed to upload FAQ entries to object storage: %v", err)
			return "", fmt.Errorf("failed to upload entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries uploaded to: %s", entriesURL)
		taskPayload.EntriesURL = entriesURL
		taskPayload.EntryCount = entryCount
	} else {
		// 数据量较小，直接存储在 payload 中
		taskPayload.Entries = payload.Entries
	}

	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal FAQ import task payload: %v", err)
		return "", fmt.Errorf("failed to marshal task payload: %w", err)
	}

	// 再次检查 payload 大小
	if len(payloadBytes) > payloadSizeThreshold && taskPayload.EntriesURL == "" {
		// payload 太大但还没上传，现在上传
		entriesData, _ := json.Marshal(payload.Entries)
		fileName, nameErr := faqImportEntriesFileName(taskID, enqueuedAt)
		if nameErr != nil {
			return "", fmt.Errorf("invalid task id for object name: %w", nameErr)
		}
		entriesURL, err := s.fileSvc.SaveBytes(ctx, entriesData, tenantID, fileName, false)
		if err != nil {
			logger.Errorf(ctx, "Failed to upload FAQ entries to object storage: %v", err)
			return "", fmt.Errorf("failed to upload entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries uploaded to (size exceeded): %s", entriesURL)
		taskPayload.Entries = nil
		taskPayload.EntriesURL = entriesURL
		taskPayload.EntryCount = entryCount

		payloadBytes, _ = json.Marshal(taskPayload)
	}

	logger.Infof(ctx, "FAQ import task payload size: %d bytes", len(payloadBytes))

	maxRetry := 5
	if payload.DryRun {
		maxRetry = 3 // dry run 重试次数少一些
	}

	// 使用 taskID:instanceID 作为 asynq 的唯一任务标识
	asynqTaskID := fmt.Sprintf("%s:%s", taskID, instanceID)

	task := asynq.NewTask(
		types.TypeFAQImport,
		payloadBytes,
		asynq.TaskID(asynqTaskID),
		asynq.Queue(types.QueueMaintenance),
		asynq.MaxRetry(maxRetry),
		asynq.Timeout(2*time.Hour),
	)
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue FAQ import task: %v", err)
		if taskPayload.EntriesURL != "" && s.fileSvc != nil {
			if delErr := s.fileSvc.DeleteFile(context.WithoutCancel(ctx), taskPayload.EntriesURL); delErr != nil {
				logger.Warnf(ctx, "Failed to clean up uploaded FAQ entries %s after enqueue error: %v",
					taskPayload.EntriesURL, delErr)
			}
		}
		return "", fmt.Errorf("failed to enqueue task: %w", err)
	}
	logger.Infof(ctx, "Enqueued FAQ import task: id=%s queue=%s task_id=%s dry_run=%v", info.ID, info.Queue, taskID,
		payload.DryRun)
	enqueueSucceeded = true

	if !payload.DryRun {
		recordKBActivity(ctx, s.audit, tenantID, kbID, types.AuditActionFAQImportStarted,
			"faq_entry", knowledgeID, types.AuditOutcomeAccepted,
			map[string]any{
				"task_id": taskID, "mode": payload.Mode, "total": len(payload.Entries),
				"trigger": kbActivityTrigger(ctx), "processing_status": "pending",
			})
	}

	return taskID, nil
}

func faqImportEntriesFileName(taskID string, enqueuedAt int64) (string, error) {
	return secutils.SafeFileName(fmt.Sprintf("faq_import_entries_%s_%d.json", taskID, enqueuedAt))
}

// generateFailedEntriesCSV 生成失败条目的 CSV 文件并上传
func (s *knowledgeService) generateFailedEntriesCSV(ctx context.Context,
	tenantID uint64, taskID string, failedEntries []types.FAQFailedEntry,
) (string, error) {
	// 生成 CSV 内容
	var buf strings.Builder

	// 写入 BOM 以支持 Excel 正确识别 UTF-8
	buf.WriteString("\xEF\xBB\xBF")

	// 写入表头
	buf.WriteString(
		"错误原因,分类(必填),问题(必填),相似问题(选填-多个用##分隔),反例问题(选填-多个用##分隔),机器人回答(必填-多个用##分隔),是否全部回复(选填-默认FALSE),是否停用(选填-默认FALSE)\n")

	// 写入数据行
	for _, entry := range failedEntries {
		// CSV 转义：如果内容包含逗号、引号或换行，需要用引号包裹并转义内部引号
		reason := csvEscape(entry.Reason)
		tagName := csvEscape(entry.TagName)
		standardQ := csvEscape(entry.StandardQuestion)
		similarQs := ""
		if len(entry.SimilarQuestions) > 0 {
			similarQs = csvEscape(strings.Join(entry.SimilarQuestions, "##"))
		}
		negativeQs := ""
		if len(entry.NegativeQuestions) > 0 {
			negativeQs = csvEscape(strings.Join(entry.NegativeQuestions, "##"))
		}
		answers := ""
		if len(entry.Answers) > 0 {
			answers = csvEscape(strings.Join(entry.Answers, "##"))
		}
		answerAll := "false"
		if entry.AnswerAll {
			answerAll = "true"
		}
		isDisabled := "false"
		if entry.IsDisabled {
			isDisabled = "true"
		}

		fmt.Fprintf(&buf, "%s,%s,%s,%s,%s,%s,%s,%s\n",
			reason, tagName, standardQ, similarQs, negativeQs, answers, answerAll, isDisabled)
	}

	// 上传 CSV 文件到临时存储（会自动过期）
	fileName, err := secutils.SafeFileName(fmt.Sprintf("faq_dryrun_failed_%s.csv", taskID))
	if err != nil {
		return "", fmt.Errorf("invalid task id for object name: %w", err)
	}
	filePath, err := s.fileSvc.SaveBytes(ctx, []byte(buf.String()), tenantID, fileName, true)
	if err != nil {
		return "", fmt.Errorf("failed to save CSV file: %w", err)
	}

	// 获取下载 URL
	fileURL, err := s.fileSvc.GetFileURL(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get file URL: %w", err)
	}

	logger.Infof(ctx, "Generated failed entries CSV: %s, entries: %d", fileURL, len(failedEntries))
	return fileURL, nil
}

// csvEscape 转义 CSV 字段
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		// 将内部引号替换为两个引号，并用引号包裹整个字段
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

// saveFAQImportResultToDatabase 保存FAQ导入结果统计到数据库
func (s *knowledgeService) saveFAQImportResultToDatabase(ctx context.Context,
	payload *types.FAQImportPayload, progress *types.FAQImportProgress, originalTotalEntries int,
) error {
	// 获取FAQ知识库实例
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("failed to get FAQ knowledge: %w", err)
	}

	// 计算跳过的条目数（总数 - 完全成功 - 部分失败 - 完全失败）
	skippedCount := originalTotalEntries - progress.SuccessCount - progress.PartialFailedCount - progress.FailedCount
	if skippedCount < 0 {
		skippedCount = 0
	}

	// 创建导入结果统计
	importResult := &types.FAQImportResult{
		TotalEntries:       originalTotalEntries,
		SuccessCount:       progress.SuccessCount,
		FailedCount:        progress.FailedCount,
		PartialFailedCount: progress.PartialFailedCount,
		SkippedCount:       skippedCount,
		MergedCount:        progress.MergedCount,
		AddedCount:         progress.AddedCount,
		ImportMode:         payload.Mode,
		ImportedAt:         time.Now(),
		TaskID:             payload.TaskID,
		ProcessingTime:     time.Now().Unix() - progress.CreatedAt, // 处理耗时（秒）
		DisplayStatus:      "open",                                 // 新导入的结果默认显示
	}

	// 如果有失败/部分失败条目 CSV 下载 URL，则写入结果（FailedEntries 包含完全失败与部分失败）
	if progress.FailedEntriesURL != "" {
		importResult.FailedEntriesURL = progress.FailedEntriesURL
	}

	// 设置导入结果到Knowledge的metadata中
	if err := knowledge.SetLastFAQImportResult(importResult); err != nil {
		return fmt.Errorf("failed to set FAQ import result: %w", err)
	}

	// 更新数据库
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return fmt.Errorf("failed to update knowledge with import result: %w", err)
	}

	logger.Infof(ctx,
		"Saved FAQ import result to database: knowledge_id=%s, task_id=%s, total=%d, success=%d, added=%d, "+
			"merged=%d, failed=%d, partial_failed=%d, skipped=%d",
		payload.KnowledgeID, payload.TaskID, originalTotalEntries, progress.SuccessCount, progress.AddedCount,
		progress.MergedCount, progress.FailedCount, progress.PartialFailedCount, skippedCount)

	return nil
}

// buildFAQFailedEntry 构建 FAQFailedEntry
func buildFAQFailedEntry(idx int, reason string, entry *types.FAQEntryPayload) types.FAQFailedEntry {
	answerAll := entry.AnswerStrategy != nil && *entry.AnswerStrategy == types.AnswerStrategyAll
	isDisabled := entry.IsEnabled != nil && !*entry.IsEnabled
	return types.FAQFailedEntry{
		Index:             idx,
		Reason:            reason,
		TagName:           entry.TagName,
		StandardQuestion:  strings.TrimSpace(entry.StandardQuestion),
		SimilarQuestions:  entry.SimilarQuestions,
		NegativeQuestions: entry.NegativeQuestions,
		Answers:           entry.Answers,
		AnswerAll:         answerAll,
		IsDisabled:        isDisabled,
	}
}

func buildFAQPartialFailedEntry(idx int, entry *types.FAQEntryPayload,
	removedSimilarQuestions, removedNegativeQuestions []string,
) types.FAQFailedEntry {
	answerAll := entry.AnswerStrategy != nil && *entry.AnswerStrategy == types.AnswerStrategyAll
	isDisabled := entry.IsEnabled != nil && !*entry.IsEnabled

	// 构建失败原因描述：汇总信息 + 详细信息
	var summary []string
	if len(removedSimilarQuestions) > 0 {
		summary = append(summary, fmt.Sprintf("%d条相似问被移除", len(removedSimilarQuestions)))
	}
	if len(removedNegativeQuestions) > 0 {
		summary = append(summary, fmt.Sprintf("%d条反例被移除", len(removedNegativeQuestions)))
	}

	// 完整的 reason：汇总 | 相似问详情 | 反例详情
	var reasonParts []string
	reasonParts = append(reasonParts, "部分成功："+strings.Join(summary, "，"))
	if len(removedSimilarQuestions) > 0 {
		reasonParts = append(reasonParts, strings.Join(removedSimilarQuestions, "; "))
	}
	if len(removedNegativeQuestions) > 0 {
		reasonParts = append(reasonParts, strings.Join(removedNegativeQuestions, "; "))
	}

	return types.FAQFailedEntry{
		Index:                    idx,
		Reason:                   strings.Join(reasonParts, " | "),
		IsPartialFailure:         true,
		TagName:                  entry.TagName,
		StandardQuestion:         strings.TrimSpace(entry.StandardQuestion),
		SimilarQuestions:         entry.SimilarQuestions,
		NegativeQuestions:        entry.NegativeQuestions,
		Answers:                  entry.Answers,
		AnswerAll:                answerAll,
		IsDisabled:               isDisabled,
		RemovedSimilarQuestions:  removedSimilarQuestions,
		RemovedNegativeQuestions: removedNegativeQuestions,
	}
}

// executeFAQImport 执行实际的FAQ导入逻辑
func (s *knowledgeService) executeFAQImport(ctx context.Context, taskID string, kbID string,
	payload *types.FAQBatchUpsertPayload, tenantID uint64, processedCount int,
	progress *types.FAQImportProgress,
) (err error) {
	// 保存知识库和embedding模型信息，用于清理索引
	var kb *types.KnowledgeBase
	var embeddingModel embedding.Embedder
	totalEntries := len(payload.Entries) + processedCount

	// Recovery机制：如果发生任何错误或panic，回滚所有已创建的chunks和索引数据
	defer func() {
		// 捕获panic
		if r := recover(); r != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			stack := string(buf[:n])
			logger.Errorf(ctx, "FAQ import task %s panicked: %v\n%s", taskID, r, stack)
			err = fmt.Errorf("panic during FAQ import: %v", r)
		}
	}()

	kb, ctx, err = s.writableFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}

	kb.EnsureDefaults()

	// 获取embedding模型，用于后续清理索引
	embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return fmt.Errorf("failed to get embedding model: %w", err)
	}
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return err
	}

	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 增量更新逻辑：计算需要处理的条目
	var entriesToProcess []types.FAQEntryPayload
	var chunksToDelete []*types.Chunk
	var skippedCount int

	if payload.Mode == types.FAQBatchModeReplace {
		// Replace模式：计算需要删除、创建、更新的条目
		entriesToProcess, chunksToDelete, skippedCount, err = s.calculateReplaceOperations(
			ctx,
			tenantID,
			faqKnowledge.ID,
			payload.Entries,
		)
		if err != nil {
			return fmt.Errorf("failed to calculate replace operations: %w", err)
		}

		// 删除需要删除的chunks（包括需要更新的旧chunks）
		if len(chunksToDelete) > 0 {
			chunkIDsToDelete := make([]string, 0, len(chunksToDelete))
			for _, chunk := range chunksToDelete {
				chunkIDsToDelete = append(chunkIDsToDelete, chunk.ID)
			}
			if err := s.chunkRepo.DeleteChunks(ctx, tenantID, chunkIDsToDelete); err != nil {
				return fmt.Errorf("failed to delete chunks: %w", err)
			}
			// 删除索引
			if err := s.deleteFAQChunkVectors(ctx, kb, faqKnowledge, chunksToDelete); err != nil {
				return fmt.Errorf("failed to delete chunk vectors: %w", err)
			}
			logger.Infof(ctx, "FAQ import task %s: deleted %d chunks (including updates)", taskID, len(chunksToDelete))
		}
	} else {
		// Append 模式（智能合并）：标准问已存在的条目走 merge ops，其余作为新建。
		var mergeOps []faqMergeOperation
		entriesToProcess, mergeOps, skippedCount, err = s.calculateAppendOperations(ctx, tenantID, kb.ID,
			payload.Entries)
		if err != nil {
			return fmt.Errorf("failed to calculate append operations: %w", err)
		}

		if len(mergeOps) > 0 {
			mergedCount, mergeErr := s.executeFAQMergeOperations(ctx, taskID, kb, faqKnowledge, embeddingModel,
				indexMode, mergeOps, progress)
			if mergeErr != nil {
				return fmt.Errorf("failed to execute merge operations: %w", mergeErr)
			}
			logger.Infof(ctx, "FAQ import task %s: merged %d entries", taskID, mergedCount)
			progress.MergedCount = mergedCount
			for _, op := range mergeOps {
				progress.MergeDetails = append(progress.MergeDetails, op.Detail)
			}
		}
	}
	logger.Infof(
		ctx,
		"FAQ import task %s: total entries: %d, new to create: %d, skipped: %d, merged: %d",
		taskID,
		len(payload.Entries),
		len(entriesToProcess),
		skippedCount,
		progress.MergedCount,
	)

	// 如果没有需要处理的条目，直接返回
	if len(entriesToProcess) == 0 {
		logger.Infof(ctx, "FAQ import task %s: no new entries to create", taskID)
		return nil
	}

	// 分批处理需要创建的条目
	remainingEntries := len(entriesToProcess)
	totalStartTime := time.Now()
	actualProcessed := skippedCount + processedCount + progress.MergedCount

	logger.Infof(
		ctx,
		"FAQ import task %s: starting batch processing, remaining entries: %d, total entries: %d, batch size: %d",
		taskID,
		remainingEntries,
		totalEntries,
		faqImportBatchSize,
	)

	for i := 0; i < remainingEntries; i += faqImportBatchSize {
		batchStartTime := time.Now()
		end := i + faqImportBatchSize
		if end > remainingEntries {
			end = remainingEntries
		}

		batch := entriesToProcess[i:end]
		logger.Infof(ctx, "FAQ import task %s: processing batch %d-%d (%d entries)", taskID, i+1, end, len(batch))

		// 构建chunks
		buildStartTime := time.Now()
		chunks := make([]*types.Chunk, 0, len(batch))
		chunkIDs := make([]string, 0, len(batch))
		for idx, entry := range batch {
			meta, err := sanitizeFAQEntryPayload(&entry)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"entry":   entry,
					"task_id": taskID,
				})
				return fmt.Errorf("failed to sanitize entry at index %d: %w", i+idx, err)
			}

			// 解析 TagID
			tagID, err := s.resolveTagID(ctx, kbID, &entry)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"entry":   entry,
					"task_id": taskID,
				})
				return fmt.Errorf("failed to resolve tag for entry at index %d: %w", i+idx, err)
			}

			isEnabled := true
			if entry.IsEnabled != nil {
				isEnabled = *entry.IsEnabled
			}
			// ChunkIndex计算：startChunkIndex + (i+idx) + initialProcessed
			chunk := &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        tenantID,
				KnowledgeID:     faqKnowledge.ID,
				KnowledgeBaseID: kb.ID,
				Content:         buildFAQChunkContent(meta, indexMode),
				// ChunkIndex:      0,
				IsEnabled: isEnabled,
				ChunkType: types.ChunkTypeFAQ,
				TagID:     tagID,                        // 使用解析后的 TagID
				Status:    int(types.ChunkStatusStored), // store but not indexed
			}
			// 如果指定了 ID（用于数据迁移），设置 SeqID
			if entry.ID != nil && *entry.ID > 0 {
				chunk.SeqID = *entry.ID
			}
			if err := chunk.SetFAQMetadata(meta); err != nil {
				return fmt.Errorf("failed to set FAQ metadata: %w", err)
			}
			chunks = append(chunks, chunk)
			chunkIDs = append(chunkIDs, chunk.ID)
		}
		buildDuration := time.Since(buildStartTime)
		logger.Debugf(ctx, "FAQ import task %s: batch %d-%d built %d chunks in %v, chunk IDs: %v",
			taskID, i+1, end, len(chunks), buildDuration, chunkIDs)
		// 创建chunks
		createStartTime := time.Now()
		if err := s.chunkService.CreateChunks(ctx, chunks); err != nil {
			return fmt.Errorf("failed to create chunks: %w", err)
		}
		createDuration := time.Since(createStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d created %d chunks in %v",
			taskID,
			i+1,
			end,
			len(chunks),
			createDuration,
		)

		// 索引chunks
		indexStartTime := time.Now()
		// 注意：如果索引失败，defer中的recovery机制会自动回滚已创建的chunks和索引数据
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, chunks, embeddingModel, true, false); err != nil {
			return fmt.Errorf("failed to index chunks: %w", err)
		}
		indexDuration := time.Since(indexStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d indexed %d chunks in %v",
			taskID,
			i+1,
			end,
			len(chunks),
			indexDuration,
		)

		// 更新chunks的Status为已索引：所有行写同一个值，一条 UPDATE ... WHERE id IN 即可，
		// 不需要把 content 等字段再回传一遍。
		for _, chunk := range chunks {
			chunk.Status = int(types.ChunkStatusIndexed) // indexed
		}
		if err := s.chunkRepo.UpdateChunkFieldsByIDs(ctx, tenantID, chunkIDs, map[string]interface{}{
			"status": int(types.ChunkStatusIndexed),
		}); err != nil {
			return fmt.Errorf("failed to update chunks status: %w", err)
		}

		// 收集成功条目信息（tag 信息按批一次查出，避免每条一次查询）
		tagsByID := s.loadFAQTagsForChunks(ctx, tenantID, chunks)
		for idx, chunk := range chunks {
			entryIdx := i + idx + processedCount // 原始条目索引
			meta, _ := chunk.FAQMetadata()
			standardQ := ""
			if meta != nil {
				standardQ = meta.StandardQuestion
			}
			tagID, tagName := faqTagInfo(tagsByID, chunk.TagID)
			progress.SuccessEntries = append(progress.SuccessEntries, types.FAQSuccessEntry{
				Index:            entryIdx,
				SeqID:            chunk.SeqID,
				TagID:            tagID,
				TagName:          tagName,
				StandardQuestion: standardQ,
			})
		}

		actualProcessed += len(batch)
		// 更新任务进度
		progress := int(float64(actualProcessed) / float64(totalEntries) * 100)
		if err := s.updateFAQImportProgressStatus(ctx, taskID, "", 0, types.FAQImportStatusProcessing, progress,
			totalEntries, actualProcessed, fmt.Sprintf("正在处理第 %d/%d 条", actualProcessed, totalEntries),
			""); err != nil {
			logger.Errorf(ctx, "Failed to update task progress: %v", err)
		}

		batchDuration := time.Since(batchStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d completed in %v (build: %v, create: %v, index: %v), total "+
				"progress: %d/%d (%d%%)",
			taskID,
			i+1,
			end,
			batchDuration,
			buildDuration,
			createDuration,
			indexDuration,
			actualProcessed,
			totalEntries,
			progress,
		)
	}

	totalDuration := time.Since(totalStartTime)
	var avgPerEntry time.Duration
	if actualProcessed > 0 {
		avgPerEntry = totalDuration / time.Duration(actualProcessed)
	}
	logger.Infof(
		ctx,
		"FAQ import task %s: all batches completed, processed: %d entries (skipped: %d) in %v, avg: %v per entry",
		taskID,
		actualProcessed,
		skippedCount,
		totalDuration,
		avgPerEntry,
	)

	return nil
}

// ProcessFAQImport handles Asynq FAQ import tasks (including dry run mode)
func (s *knowledgeService) ProcessFAQImport(ctx context.Context, t *asynq.Task) error {
	var payload types.FAQImportPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal FAQ import task payload: %v", err)
		return fmt.Errorf("failed to unmarshal task payload: %w", err)
	}
	ctx = payload.Initiator.Apply(ctx)
	ctx = withKBActivityTask(ctx, payload.TaskID, kbActivityTrigger(ctx))

	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "faq_import", payload.TaskID)
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	kb, err := s.validateFAQKnowledgeBase(ctx, payload.KBID)
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			return fmt.Errorf("%w: FAQ task KB no longer exists", asynq.SkipRetry)
		}
		return err
	}
	ctx, err = access.WithKBTaskWrite(ctx, kb, payload.TenantID)
	if err != nil {
		return fmt.Errorf("%w: FAQ task KB does not belong to its tenant", asynq.SkipRetry)
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeNotFound) {
			return fmt.Errorf("%w: FAQ task document no longer exists", asynq.SkipRetry)
		}
		return err
	}
	if knowledge == nil || knowledge.TenantID != payload.TenantID || knowledge.KnowledgeBaseID != payload.KBID ||
		knowledge.Type != types.KnowledgeTypeFAQ {
		return fmt.Errorf("%w: FAQ task document does not belong to its KB", asynq.SkipRetry)
	}

	// A replay must not start writing progress for another accepted import.
	// Missing markers can expire before a manual retry; retain that recovery
	// path and the existing timestamp fallback for legacy task instances.
	running, err := s.getRunningFAQImportInfo(ctx, payload.KBID)
	if err != nil {
		return fmt.Errorf("failed to check FAQ import instance: %w", err)
	}
	if running != nil {
		if err := secutils.ValidateTaskID(running.TaskID); err != nil {
			return fmt.Errorf("invalid running FAQ import task ID: %w", err)
		}
		if !runningFAQImportInfoMatches(running, payload.TaskID, payload.InstanceID, payload.EnqueuedAt) {
			return fmt.Errorf("%w: another FAQ import instance owns this KB", asynq.SkipRetry)
		}
	}

	// 获取任务重试信息，用于判断是否是最后一次重试
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		if errors.Is(err, repository.ErrTenantNotFound) {
			return fmt.Errorf("%w: FAQ task tenant no longer exists", asynq.SkipRetry)
		}
		return fmt.Errorf("failed to get FAQ import tenant: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// 如果 entries 存储在对象存储中，先下载
	if payload.EntriesURL != "" && len(payload.Entries) == 0 {
		logger.Infof(ctx, "Downloading FAQ entries from object storage: %s", payload.EntriesURL)
		reader, err := s.fileSvc.GetFile(ctx, payload.EntriesURL)
		if err != nil {
			logger.Errorf(ctx, "Failed to download FAQ entries from object storage: %v", err)
			return fmt.Errorf("failed to download entries: %w", err)
		}
		defer func() { _ = reader.Close() }()

		const maxFAQImportEntriesBytes int64 = 64 << 20
		maxBytes := maxKnowledgeUploadBytes()
		if maxBytes < maxFAQImportEntriesBytes {
			maxBytes = maxFAQImportEntriesBytes
		}
		entriesData, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
		if err != nil {
			logger.Errorf(ctx, "Failed to read FAQ entries data: %v", err)
			return fmt.Errorf("failed to read entries data: %w", err)
		}
		if int64(len(entriesData)) > maxBytes {
			limitErr := fmt.Errorf("FAQ entries payload exceeds maximum size of %d bytes", maxBytes)
			logger.Errorf(ctx, "FAQ entries payload too large: %v", limitErr)
			return fmt.Errorf("%w: %v", asynq.SkipRetry, limitErr)
		}

		var entries []types.FAQEntryPayload
		if err := json.Unmarshal(entriesData, &entries); err != nil {
			logger.Errorf(ctx, "Failed to unmarshal FAQ entries: %v", err)
			return fmt.Errorf("failed to unmarshal entries: %w", err)
		}

		payload.Entries = entries
		logger.Infof(ctx, "Downloaded %d FAQ entries from object storage", len(entries))
	}

	logger.Infof(ctx, "Processing FAQ import task: task_id=%s, kb_id=%s, total_entries=%d, dry_run=%v, retry=%d/%d",
		payload.TaskID, payload.KBID, len(payload.Entries), payload.DryRun, retryCount, maxRetry)
	if err := s.validateFAQImportTags(ctx, kb, payload.Entries); err != nil {
		return err
	}

	// 保存原始总数量
	originalTotalEntries := len(payload.Entries)

	// 初始化进度
	// 检查是否已有验证结果（用于重试时跳过验证）
	// 注意：必须在保存新 progress 之前查询，否则会被覆盖
	existingProgress, _ := s.GetFAQImportProgress(ctx, payload.TaskID)
	if existingProgress != nil && existingProgress.Status == types.FAQImportStatusCompleted {
		logger.Infof(ctx, "FAQ import task already completed, skipping duplicate delivery: task_id=%s", payload.TaskID)
		_ = s.clearRunningFAQImportInfoIfMatches(ctx, payload.KBID, payload.TaskID, payload.InstanceID,
			payload.EnqueuedAt)
		return nil
	}

	progress := &types.FAQImportProgress{
		TaskID:         payload.TaskID,
		KBID:           payload.KBID,
		KnowledgeID:    payload.KnowledgeID,
		Status:         types.FAQImportStatusProcessing,
		Progress:       0,
		Total:          originalTotalEntries,
		Processed:      0,
		SuccessCount:   0,
		FailedCount:    0,
		FailedEntries:  make([]types.FAQFailedEntry, 0),
		SuccessEntries: make([]types.FAQSuccessEntry, 0),
		Message:        "正在验证条目...",
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
		DryRun:         payload.DryRun,
	}

	var validEntryIndices []int
	if existingProgress != nil && len(existingProgress.ValidEntryIndices) > 0 {
		// 重试时直接使用之前的验证结果
		validEntryIndices = existingProgress.ValidEntryIndices
		progress.ValidEntryIndices = existingProgress.ValidEntryIndices
		progress.FailedCount = existingProgress.FailedCount
		progress.PartialFailedCount = existingProgress.PartialFailedCount
		progress.FailedEntries = existingProgress.FailedEntries
		if err := s.saveFAQImportProgress(ctx, progress); err != nil {
			logger.Warnf(ctx, "Failed to save initial FAQ import progress: %v", err)
		}
		logger.Infof(ctx, "Reusing previous validation result: valid=%d, failed=%d",
			len(validEntryIndices), progress.FailedCount)
	} else {
		if err := s.saveFAQImportProgress(ctx, progress); err != nil {
			logger.Warnf(ctx, "Failed to save initial FAQ import progress: %v", err)
		}
		// 第一步：执行验证（无论是 dry run 还是 import 模式都需要验证）
		validEntryIndices = s.executeFAQDryRunValidation(ctx, &payload, progress)
		// 保存验证通过的索引，用于重试时跳过验证
		progress.ValidEntryIndices = validEntryIndices
		if err := s.saveFAQImportProgress(ctx, progress); err != nil {
			logger.Warnf(ctx, "Failed to save validation result: %v", err)
		}
		logger.Infof(ctx, "FAQ validation completed: total=%d, valid=%d, failed=%d",
			originalTotalEntries, len(validEntryIndices), progress.FailedCount)
	}

	// Dry run 模式：验证完成后直接返回结果
	if payload.DryRun {
		return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
	}

	// Import 模式：检查是否有有效条目需要导入
	if len(validEntryIndices) == 0 {
		// 没有有效条目，直接完成
		return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
	}

	// 提取有效的条目
	validEntries := make([]types.FAQEntryPayload, 0, len(validEntryIndices))
	for _, idx := range validEntryIndices {
		validEntries = append(validEntries, payload.Entries[idx])
	}

	// 更新进度消息
	progress.Message = fmt.Sprintf("验证完成，开始导入 %d 条有效数据...", len(validEntries))
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to update FAQ import progress: %v", err)
	}

	// 检查任务状态 - 幂等性处理（复用之前获取的 existingProgress）
	var processedCount int
	if existingProgress != nil {
		if existingProgress.Status == types.FAQImportStatusCompleted {
			logger.Infof(ctx, "FAQ import already completed, skipping: %s", payload.TaskID)
			if clearErr := s.clearRunningFAQImportInfoIfMatches(ctx, payload.KBID, payload.TaskID, payload.InstanceID,
				payload.EnqueuedAt); clearErr != nil {
				logger.Warnf(ctx, "Failed to clear running FAQ import info for completed task: %v", clearErr)
			}
			return nil // 幂等：已完成的任务直接返回
		}
		// 获取已处理的数量（注意：这是相对于 validEntries 的索引）
		processedCount = existingProgress.Processed - progress.FailedCount // 已处理数 - 验证失败数 = 已导入的有效条目数
		if processedCount < 0 {
			processedCount = 0
		}
		logger.Infof(ctx, "Resuming FAQ import from progress: %d/%d (valid entries)", processedCount, len(validEntries))
	}

	// 幂等性处理：清理可能已部分处理的chunks和索引数据
	chunksDeleted, err := s.chunkRepo.DeleteUnindexedChunks(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to delete unindexed chunks: %v", err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt,
				types.FAQImportStatusFailed, 0, originalTotalEntries, 0, "清理未索引数据失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
			s.recordFAQImportKBActivity(ctx, &payload, progress, originalTotalEntries, types.AuditActionFAQImportFailed,
				types.AuditOutcomeFailed)
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("failed to delete unindexed chunks: %w", err)
	}
	if len(chunksDeleted) > 0 {
		logger.Infof(ctx, "Deleted unindexed chunks: %d", len(chunksDeleted))

		// 删除索引数据
		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err == nil {
			retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
				ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
			if err == nil {
				chunkIDs := make([]string, 0, len(chunksDeleted))
				for _, chunk := range chunksDeleted {
					chunkIDs = append(chunkIDs, chunk.ID)
				}
				if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(),
					types.KnowledgeTypeFAQ); err != nil {
					logger.Warnf(ctx, "Failed to delete index data for chunks (may not exist): %v", err)
				} else {
					logger.Infof(ctx, "Successfully deleted index data for %d chunks", len(chunksDeleted))
				}
			}
		}
	}

	// 如果已经处理了一部分有效条目，从该位置继续
	entriesToImport := validEntries
	importMode := payload.Mode
	if processedCount > 0 && processedCount < len(validEntries) {
		entriesToImport = validEntries[processedCount:]
		// 重试场景下，如果之前已经处理了一部分数据，需要切换到 Append 模式
		// 因为 Replace 模式的删除操作在第一次运行时已经执行过了
		// 如果继续使用 Replace 模式，calculateReplaceOperations 会将之前成功导入的数据标记为删除
		// 导致数据丢失
		if payload.Mode == types.FAQBatchModeReplace {
			importMode = types.FAQBatchModeAppend
			logger.Infof(ctx, "Switching to Append mode for retry, original mode was Replace")
		}
		logger.Infof(ctx, "Continuing FAQ import from entry %d, remaining: %d entries", processedCount,
			len(entriesToImport))
	}

	// 构建FAQBatchUpsertPayload（使用验证通过的有效条目）
	faqPayload := &types.FAQBatchUpsertPayload{
		Entries: entriesToImport,
		Mode:    importMode,
	}

	// 执行FAQ导入（传入已处理的偏移量，用于进度计算）
	if err := s.executeFAQImport(ctx, payload.TaskID, payload.KBID, faqPayload, payload.TenantID, progress.FailedCount+
		processedCount, progress); err != nil {
		logger.Errorf(ctx, "FAQ import task failed: %s, error: %v", payload.TaskID, err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt,
				types.FAQImportStatusFailed, 0, originalTotalEntries, len(validEntries), "导入失败",
				err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
			s.recordFAQImportKBActivity(ctx, &payload, progress, originalTotalEntries, types.AuditActionFAQImportFailed,
				types.AuditOutcomeFailed)
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("FAQ import failed: %w", err)
	}

	// 任务成功完成
	logger.Infof(ctx, "FAQ import task completed: %s, imported: %d, failed: %d",
		payload.TaskID, len(progress.SuccessEntries), progress.FailedCount)

	// 最终完成处理（生成失败条目 CSV 等）
	return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
}
