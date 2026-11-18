package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
)

// executeFAQDryRunValidation 执行 FAQ dry run 验证，返回通过验证的条目索引
func (s *knowledgeService) executeFAQDryRunValidation(ctx context.Context,
	payload *types.FAQImportPayload, progress *types.FAQImportProgress,
) []int {
	entries := payload.Entries

	// 用于记录已通过基本验证和重复检查的条目索引，后续进行安全检查
	var validEntryIndices []int

	// 根据模式选择不同的验证逻辑
	if payload.Mode == types.FAQBatchModeAppend {
		validEntryIndices = s.validateEntriesForAppendModeWithProgress(ctx, payload.TenantID, payload.KBID, entries,
			progress)
	} else {
		validEntryIndices = s.validateEntriesForReplaceModeWithProgress(ctx, entries, progress)
	}

	return validEntryIndices
}

// validateEntriesForAppendModeWithProgress 验证 Append 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
// validateEntriesForAppendModeWithProgress 验证 Append 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
//
// 分四阶段校验：
// 阶段一（预校验）：
//  1. 标准问 - 仅文件内部去重；如已有KB标准问则标记为合并候选
//  2. 相似问 - 对比文件+知识库（合并候选排除自身已有问题） → 单条移除冲突相似问
//  3. 反例   - 仅对比当前QA自身标准问+相似问 → 单条移除冲突反例
//
// 阶段二（后校验，仅合并候选）：
//  4. 对合并后的完整数据重跑反例校验 → 冲突则整条回退到合并前状态
func (s *knowledgeService) validateEntriesForAppendModeWithProgress(ctx context.Context,
	tenantID uint64, kbID string, entries []types.FAQEntryPayload, progress *types.FAQImportProgress,
) []int {
	totalEntries := len(entries)

	// 查询知识库中已有的所有 FAQ chunks 的 metadata
	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list existing FAQ chunks for dry run: %v", err)
	}

	// 构建已存在的标准问 → chunk 映射（用于合并候选识别）
	existingStdQToChunk := make(map[string]*types.Chunk)
	// 构建已存在的所有问题 → 所属 chunkID 映射（用于相似问冲突检测）
	existingQuestionToChunkID := make(map[string]string)
	// 构建每个 chunk 拥有的问题集合（用于合并时排除自身）
	existingChunkQuestions := make(map[string]map[string]bool)
	// 构建 chunkID → 标准问 映射（用于冲突失败原因展示）
	existingChunkIDToStdQ := make(map[string]string)

	for _, chunk := range existingChunks {
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			continue
		}
		qs := make(map[string]bool)
		if meta.StandardQuestion != "" {
			existingStdQToChunk[meta.StandardQuestion] = chunk
			existingQuestionToChunkID[meta.StandardQuestion] = chunk.ID
			qs[meta.StandardQuestion] = true
		}
		for _, q := range meta.SimilarQuestions {
			if q != "" {
				existingQuestionToChunkID[q] = chunk.ID
				qs[q] = true
			}
		}
		existingChunkQuestions[chunk.ID] = qs
		existingChunkIDToStdQ[chunk.ID] = meta.StandardQuestion
	}

	// 合并候选跟踪：entry index → 目标已有 chunk
	mergeChunkMap := make(map[int]*types.Chunk)

	// ==================== 第一次迭代：基本格式验证 + 文件内标准问去重 + 合并候选识别 ====================
	batchStandardQuestions := make(map[string]int) // value 为首次出现的索引
	validIndicesAfterStdQ := make([]int, 0, totalEntries)

	for i, entry := range entries {
		if err := validateFAQEntryPayloadBasic(&entry); err != nil {
			progress.FailedCount++
			fe := buildFAQFailedEntry(i, err.Error(), &entry)
			fe.FailureType = "pre_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			continue
		}

		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 文件内标准问去重
		if firstIdx, exists := batchStandardQuestions[standardQ]; exists {
			progress.FailedCount++
			fe := buildFAQFailedEntry(i, fmt.Sprintf("标准问冲突：与批次内第 %d 条标准问重复", firstIdx+1), &entry)
			fe.FailureType = "pre_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			continue
		}

		// 判断是否为合并候选
		if chunk, exists := existingStdQToChunk[standardQ]; exists {
			// 标准问在 KB 中已存在 → 标记为合并候选
			mergeChunkMap[i] = chunk
			logger.Infof(ctx,
				"FAQ entry %d: standard question '%s' exists in KB, marking as merge candidate (chunk_id=%s)", i,
				standardQ, chunk.ID)
		} else if conflictChunkID, hit := existingQuestionToChunkID[standardQ]; hit {
			// 标准问未与任何已有标准问重复，但撞上了 KB 中其他条目的相似问
			// → 前置校验失败，避免下游 calculateAppendOperations 静默丢弃导致统计失真
			conflictStdQ := existingChunkIDToStdQ[conflictChunkID]
			progress.FailedCount++
			fe := buildFAQFailedEntry(i,
				fmt.Sprintf(`标准问冲突：与知识库中标准问"%s"的相似问"%s"重复`, conflictStdQ, standardQ),
				&entry)
			fe.FailureType = "pre_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			logger.Infof(ctx,
				"FAQ entry %d: standard question '%s' conflicts with existing similar question of chunk_id=%s "+
					"(std='%s')",
				i, standardQ, conflictChunkID, conflictStdQ)
			continue
		}

		batchStandardQuestions[standardQ] = i
		validIndicesAfterStdQ = append(validIndicesAfterStdQ, i)

		if (i+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证标准问 %d/%d...", i+1, totalEntries)
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第二次迭代：相似问冲突检测 ====================
	// 构建批次内所有标准问 + 相似问的集合
	batchAllQuestions := make(map[string]int)
	for _, i := range validIndicesAfterStdQ {
		standardQ := strings.TrimSpace(entries[i].StandardQuestion)
		batchAllQuestions[standardQ] = i
		for _, q := range entries[i].SimilarQuestions {
			q = strings.TrimSpace(q)
			if q != "" {
				if _, exists := batchAllQuestions[q]; !exists {
					batchAllQuestions[q] = i
				}
			}
		}
	}

	removedSimilarQuestionsMap := make(map[int][]string)
	removedNegativeQuestionsMap := make(map[int][]string)

	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 合并候选：获取目标 chunk 自身的问题集合，用于排除
		var ownChunkQuestions map[string]bool
		if mergeChunk, isMerge := mergeChunkMap[i]; isMerge {
			ownChunkQuestions = existingChunkQuestions[mergeChunk.ID]
		}

		validSimilarQuestions := make([]string, 0, len(entry.SimilarQuestions))
		var removedSimilarQuestions []string
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 相似问不能与自己的标准问相同
			if q == standardQ {
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与本条"标准问"冲突`, q))
				continue
			}
			// 相似问校验：对比知识库已有标准问 + 相似问
			if _, exists := existingQuestionToChunkID[q]; exists {
				// 合并候选：如果问题属于合并目标自身的 chunk，允许（去重合并时会处理）
				if ownChunkQuestions != nil && ownChunkQuestions[q] {
					validSimilarQuestions = append(validSimilarQuestions, q)
					continue
				}
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与知识库已有"标准问/相似问"冲突`,
					q))
				continue
			}
			// 相似问校验：对比批次内标准问 + 相似问
			if firstIdx, exists := batchAllQuestions[q]; exists && firstIdx != i {
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与第 %d 行"标准问/相似问"冲突`,
					q, firstIdx+1))
				continue
			}
			validSimilarQuestions = append(validSimilarQuestions, q)
		}
		entries[i].SimilarQuestions = validSimilarQuestions

		if len(removedSimilarQuestions) > 0 {
			removedSimilarQuestionsMap[i] = removedSimilarQuestions
		}

		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证相似问 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第三次迭代：反例冲突检测（预校验，仅检查新条目自身数据） ====================
	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		currentQAQuestions := make(map[string]bool)
		currentQAQuestions[standardQ] = true
		for _, q := range entry.SimilarQuestions {
			currentQAQuestions[q] = true
		}

		validNegativeQuestions := make([]string, 0, len(entry.NegativeQuestions))
		var removedNegativeQuestions []string
		for _, q := range entry.NegativeQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if currentQAQuestions[q] {
				removedNegativeQuestions = append(removedNegativeQuestions, fmt.Sprintf(`「反例冲突」："%s"与本条"标准问/相似问"冲突`, q))
				continue
			}
			validNegativeQuestions = append(validNegativeQuestions, q)
		}
		entries[i].NegativeQuestions = validNegativeQuestions

		if len(removedNegativeQuestions) > 0 {
			removedNegativeQuestionsMap[i] = removedNegativeQuestions
		}

		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证反例 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第四次迭代：后校验（仅合并候选） ====================
	// 对合并后的完整数据重跑反例校验，冲突则整条回退到合并前状态
	postValidationFailed := make(map[int]bool)
	mergeCount := 0
	for _, i := range validIndicesAfterStdQ {
		mergeChunk, isMerge := mergeChunkMap[i]
		if !isMerge {
			continue
		}

		existingMeta, err := mergeChunk.FAQMetadata()
		if err != nil || existingMeta == nil {
			logger.Warnf(ctx, "FAQ entry %d: failed to get merge target metadata, skipping post-validation", i)
			continue
		}

		entry := &entries[i]

		// 计算合并后的完整数据
		mergedSimilar := unionStrings(existingMeta.SimilarQuestions, entry.SimilarQuestions)
		mergedNegative := unionStrings(existingMeta.NegativeQuestions, entry.NegativeQuestions)

		// 构建合并后的冲突集合（标准问 + 所有合并后的相似问）
		mergedPositiveSet := make(map[string]bool)
		mergedPositiveSet[existingMeta.StandardQuestion] = true
		for _, q := range mergedSimilar {
			mergedPositiveSet[q] = true
		}

		// 检查每个合并后的反例是否与合并后的标准问 / 相似问冲突
		var conflictingNegatives []string
		for _, q := range mergedNegative {
			if mergedPositiveSet[q] {
				conflictingNegatives = append(conflictingNegatives, q)
			}
		}

		if len(conflictingNegatives) > 0 {
			// 后校验失败 → 整条回退到合并前状态
			postValidationFailed[i] = true
			delete(mergeChunkMap, i)
			progress.FailedCount++
			reason := fmt.Sprintf("后校验失败：合并后反例「%s」与相似问冲突", strings.Join(conflictingNegatives, "、"))
			fe := buildFAQFailedEntry(i, reason, entry)
			fe.FailureType = "post_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			logger.Infof(ctx, "FAQ entry %d: post-validation failed, conflicting negatives after merge: %v", i,
				conflictingNegatives)
		} else {
			mergeCount++
		}
	}

	// 从有效索引中移除后校验失败的条目
	if len(postValidationFailed) > 0 {
		filtered := make([]int, 0, len(validIndicesAfterStdQ))
		for _, i := range validIndicesAfterStdQ {
			if !postValidationFailed[i] {
				filtered = append(filtered, i)
			}
		}
		validIndicesAfterStdQ = filtered
	}

	// 将部分失败信息添加到 FailedEntries
	for _, i := range validIndicesAfterStdQ {
		removedSimilar := removedSimilarQuestionsMap[i]
		removedNegative := removedNegativeQuestionsMap[i]
		if len(removedSimilar) > 0 || len(removedNegative) > 0 {
			pf := buildFAQPartialFailedEntry(i, &entries[i], removedSimilar, removedNegative)
			progress.FailedEntries = append(progress.FailedEntries, pf)
			progress.PartialFailedCount++
		}
	}

	// 记录合并条目索引到 progress（用于执行阶段和重试）
	mergeIndices := make([]int, 0, mergeCount)
	for _, i := range validIndicesAfterStdQ {
		if _, isMerge := mergeChunkMap[i]; isMerge {
			mergeIndices = append(mergeIndices, i)
		}
	}
	progress.MergeEntryIndices = mergeIndices

	logger.Infof(ctx,
		"Append mode validation completed: total=%d, valid=%d, merge_candidates=%d, failed=%d, partial_failed=%d",
		totalEntries, len(validIndicesAfterStdQ), mergeCount, progress.FailedCount, progress.PartialFailedCount)

	return validIndicesAfterStdQ
}

// validateEntriesForReplaceModeWithProgress 验证 Replace 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
// 分三次迭代校验，确保前面过滤掉的数据不会被后面考虑：
// 1. 标准问 - 对比所有标准问 → 整条QA失败「标准问冲突」
// 2. 相似问 - 对比所有标准问+相似问 → 单条问法失败「相似问冲突」（仅移除冲突的相似问）
// 3. 反例 - 对比当前QA下所有标准问+相似问 → 单条问法失败「反例冲突」（仅移除冲突的反例）
func (s *knowledgeService) validateEntriesForReplaceModeWithProgress(ctx context.Context,
	entries []types.FAQEntryPayload, progress *types.FAQImportProgress,
) []int {
	totalEntries := len(entries)

	// ==================== 第一次迭代：基本格式验证 + 标准问冲突检测 ====================
	// 标准问冲突会导致整条QA失败
	batchStandardQuestions := make(map[string]int) // value 为首次出现的索引
	validIndicesAfterStdQ := make([]int, 0, totalEntries)

	for i, entry := range entries {
		// 验证条目基本格式
		if err := validateFAQEntryPayloadBasic(&entry); err != nil {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, err.Error(), &entry))
			continue
		}

		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 标准问校验：对比所有标准问 → 整条QA失败「标准问冲突」
		if firstIdx, exists := batchStandardQuestions[standardQ]; exists {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i,
				fmt.Sprintf("标准问冲突：与批次内第 %d 条标准问重复", firstIdx+1), &entry))
			continue
		}

		// 记录标准问
		batchStandardQuestions[standardQ] = i
		validIndicesAfterStdQ = append(validIndicesAfterStdQ, i)

		// 定期更新进度消息
		if (i+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证标准问 %d/%d...", i+1, totalEntries)
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第二次迭代：相似问冲突检测 ====================
	// 只处理通过第一次校验的条目，相似问冲突只移除冲突的相似问
	// 构建所有标准问+相似问的集合（只包含通过第一次校验的条目）
	batchAllQuestions := make(map[string]int) // value 为首次出现的索引
	for _, i := range validIndicesAfterStdQ {
		standardQ := strings.TrimSpace(entries[i].StandardQuestion)
		batchAllQuestions[standardQ] = i
		for _, q := range entries[i].SimilarQuestions {
			q = strings.TrimSpace(q)
			if q != "" {
				// 只记录第一次出现的位置
				if _, exists := batchAllQuestions[q]; !exists {
					batchAllQuestions[q] = i
				}
			}
		}
	}

	// 用于收集每个条目被移除的相似问和反例
	removedSimilarQuestionsMap := make(map[int][]string)  // key 为条目索引
	removedNegativeQuestionsMap := make(map[int][]string) // key 为条目索引

	// 对每个通过第一次校验的条目，过滤冲突的相似问
	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		validSimilarQuestions := make([]string, 0, len(entry.SimilarQuestions))
		var removedSimilarQuestions []string
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 相似问校验：对比所有标准问+相似问 → 单条问法失败「相似问冲突」
			// 如果该相似问与其他条目的标准问或相似问冲突（且不是自己的标准问），则移除
			if firstIdx, exists := batchAllQuestions[q]; exists && firstIdx != i {
				logger.Infof(ctx, "FAQ entry %d: similar question '%s' conflicts with entry %d, removing", i, q,
					firstIdx+1)
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与第 %d 行"标准问/相似问"冲突`,
					q, firstIdx+1))
				continue
			}
			// 相似问不能与自己的标准问相同
			if q == standardQ {
				logger.Infof(ctx, "FAQ entry %d: similar question '%s' same as standard question, removing", i, q)
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与本条"标准问"冲突`, q))
				continue
			}
			validSimilarQuestions = append(validSimilarQuestions, q)
		}
		entries[i].SimilarQuestions = validSimilarQuestions

		// 记录被移除的相似问
		if len(removedSimilarQuestions) > 0 {
			removedSimilarQuestionsMap[i] = removedSimilarQuestions
		}

		// 定期更新进度消息
		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证相似问 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第三次迭代：反例冲突检测 ====================
	// 只处理通过前两次校验的条目，反例冲突只移除冲突的反例
	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 构建当前QA的所有问题集合（标准问+通过校验的相似问）
		currentQAQuestions := make(map[string]bool)
		currentQAQuestions[standardQ] = true
		for _, q := range entry.SimilarQuestions {
			currentQAQuestions[q] = true
		}

		validNegativeQuestions := make([]string, 0, len(entry.NegativeQuestions))
		var removedNegativeQuestions []string
		for _, q := range entry.NegativeQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 反例校验：对比当前QA下所有标准问+相似问 → 单条问法失败「反例冲突」
			if currentQAQuestions[q] {
				logger.Infof(ctx,
					"FAQ entry %d: negative question '%s' conflicts with current QA's questions, removing", i, q)
				removedNegativeQuestions = append(removedNegativeQuestions, fmt.Sprintf(`「反例冲突」："%s"与本条"标准问/相似问"冲突`, q))
				continue
			}
			validNegativeQuestions = append(validNegativeQuestions, q)
		}
		entries[i].NegativeQuestions = validNegativeQuestions

		// 记录被移除的反例
		if len(removedNegativeQuestions) > 0 {
			removedNegativeQuestionsMap[i] = removedNegativeQuestions
		}

		// 定期更新进度消息
		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证反例 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// 将部分失败信息添加到 FailedEntries
	for _, i := range validIndicesAfterStdQ {
		removedSimilar := removedSimilarQuestionsMap[i]
		removedNegative := removedNegativeQuestionsMap[i]
		if len(removedSimilar) > 0 || len(removedNegative) > 0 {
			pf := buildFAQPartialFailedEntry(i, &entries[i], removedSimilar, removedNegative)
			progress.FailedEntries = append(progress.FailedEntries, pf)
			progress.PartialFailedCount++
		}
	}

	return validIndicesAfterStdQ
}

// unionStrings 合并两个字符串切片并去重（完全匹配）
func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	result := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// validateFAQEntryPayloadBasic 验证 FAQ 条目的基本格式
func validateFAQEntryPayloadBasic(entry *types.FAQEntryPayload) error {
	if entry == nil {
		return fmt.Errorf("条目不能为空")
	}
	standardQ := strings.TrimSpace(entry.StandardQuestion)
	if standardQ == "" {
		return fmt.Errorf("标准问不能为空")
	}
	if len(entry.Answers) == 0 {
		return fmt.Errorf("答案不能为空")
	}
	hasValidAnswer := false
	for _, a := range entry.Answers {
		if strings.TrimSpace(a) != "" {
			hasValidAnswer = true
			break
		}
	}
	if !hasValidAnswer {
		return fmt.Errorf("答案不能全为空")
	}
	return nil
}

type faqMergeOperation struct {
	Entry         types.FAQEntryPayload
	ExistingChunk *types.Chunk
	OldMeta       *types.FAQChunkMetadata
	MergedMeta    *types.FAQChunkMetadata
	Detail        types.FAQMergeDetail
}

// calculateAppendOperations 计算 Append 模式下的操作（支持智能合并）。
// 如果 entry 的标准问在 KB 中已存在，则视为合并操作（相似问 / 反例并集，
// 答案 / 策略以新条目为准）；否则视为新建。无变化（hash 一致且操作位
// 也未变）的合并目标会被 skip 掉，避免无效写库 / 重建索引。
//
// 内部 master 行为，对应开源版仅 "重复 = 失败" 的简化逻辑。FAQ 导入的
// 常见使用方式是"导出修改后重新 append"，需要这种合并语义才能正确叠加
// 新的相似问而不丢历史数据。
func (s *knowledgeService) calculateAppendOperations(ctx context.Context,
	tenantID uint64, kbID string, entries []types.FAQEntryPayload,
) (newEntries []types.FAQEntryPayload, mergeOps []faqMergeOperation, skippedCount int, err error) {
	if len(entries) == 0 {
		return nil, nil, 0, nil
	}

	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to list existing FAQ chunks: %w", err)
	}

	existingStdQToChunk := make(map[string]*types.Chunk)
	existingQuestions := make(map[string]bool)
	for _, chunk := range existingChunks {
		meta, cErr := chunk.FAQMetadata()
		if cErr != nil || meta == nil {
			continue
		}
		if meta.StandardQuestion != "" {
			existingStdQToChunk[meta.StandardQuestion] = chunk
			existingQuestions[meta.StandardQuestion] = true
		}
		for _, q := range meta.SimilarQuestions {
			if q != "" {
				existingQuestions[q] = true
			}
		}
	}

	batchQuestions := make(map[string]bool)
	newEntries = make([]types.FAQEntryPayload, 0, len(entries))
	mergeOps = make([]faqMergeOperation, 0)

	for entryIdx, entry := range entries {
		meta, sErr := sanitizeFAQEntryPayload(&entry)
		if sErr != nil {
			skippedCount++
			logger.Warnf(ctx, "Skipping invalid FAQ entry: %v", sErr)
			continue
		}

		// 检查标准问是否在 KB 中作为标准问存在 → 合并候选
		existingChunk, isMergeCandidate := existingStdQToChunk[meta.StandardQuestion]
		if isMergeCandidate {
			existingMeta, mErr := existingChunk.FAQMetadata()
			if mErr == nil && existingMeta != nil {
				mergedSimilar := unionStrings(existingMeta.SimilarQuestions, meta.SimilarQuestions)
				mergedNegative := unionStrings(existingMeta.NegativeQuestions, meta.NegativeQuestions)

				mergedMeta := &types.FAQChunkMetadata{
					StandardQuestion:  existingMeta.StandardQuestion,
					SimilarQuestions:  mergedSimilar,
					NegativeQuestions: mergedNegative,
					Answers:           meta.Answers,
					AnswerStrategy:    meta.AnswerStrategy,
					Version:           existingMeta.Version + 1,
					Source:            existingMeta.Source,
				}

				newHash := types.CalculateFAQContentHash(mergedMeta)
				enabledChanged := entry.IsEnabled != nil && *entry.IsEnabled != existingChunk.IsEnabled
				recommendedChanged := entry.IsRecommended != nil &&
					*entry.IsRecommended != existingChunk.Flags.HasFlag(types.ChunkFlagRecommended)
				answerStrategyChanged := meta.AnswerStrategy != existingMeta.AnswerStrategy
				if existingChunk.ContentHash == newHash && !enabledChanged && !recommendedChanged &&
					!answerStrategyChanged {
					skippedCount++
					logger.Infof(ctx, "Skipping merge for unchanged FAQ entry: %s", meta.StandardQuestion)
					continue
				}

				oldAnswerStr := strings.Join(existingMeta.Answers, "##")
				newAnswerStr := strings.Join(meta.Answers, "##")
				oldSimilarSet := make(map[string]bool, len(existingMeta.SimilarQuestions))
				for _, q := range existingMeta.SimilarQuestions {
					oldSimilarSet[q] = true
				}
				oldNegativeSet := make(map[string]bool, len(existingMeta.NegativeQuestions))
				for _, q := range existingMeta.NegativeQuestions {
					oldNegativeSet[q] = true
				}
				newSimilarCount := 0
				for _, q := range mergedSimilar {
					if !oldSimilarSet[q] {
						newSimilarCount++
					}
				}
				newNegativeCount := 0
				for _, q := range mergedNegative {
					if !oldNegativeSet[q] {
						newNegativeCount++
					}
				}

				mergeOps = append(mergeOps, faqMergeOperation{
					Entry:         entry,
					ExistingChunk: existingChunk,
					OldMeta:       existingMeta,
					MergedMeta:    mergedMeta,
					Detail: types.FAQMergeDetail{
						Index:            entryIdx,
						StandardQuestion: meta.StandardQuestion,
						AnswerChanged:    oldAnswerStr != newAnswerStr,
						NewSimilarCount:  newSimilarCount,
						NewNegativeCount: newNegativeCount,
					},
				})
				continue
			}
		}

		// 走到这里说明既不是合并候选，又跟现有 KB / 当前批次的标准问 / 相似问冲突。
		// 正常情况下这些冲突应在 executeFAQDryRunValidation 阶段就被拦截，这里
		// 仅作为防御性兜底；用 Warn 级别打点便于排查验证层漏检。
		if existingQuestions[meta.StandardQuestion] || batchQuestions[meta.StandardQuestion] {
			skippedCount++
			logger.Warnf(ctx,
				"calculateAppendOperations: dropping FAQ entry with duplicate standard question %q "+
					"(should have been filtered at validation); entry_idx=%d",
				meta.StandardQuestion, entryIdx)
			continue
		}

		batchQuestions[meta.StandardQuestion] = true
		for _, q := range meta.SimilarQuestions {
			batchQuestions[q] = true
		}
		newEntries = append(newEntries, entry)
	}

	return newEntries, mergeOps, skippedCount, nil
}

// calculateReplaceOperations 计算Replace模式下需要删除、创建、更新的条目
// 同时过滤掉同批次内标准问或相似问重复的条目
func (s *knowledgeService) calculateReplaceOperations(ctx context.Context,
	tenantID uint64, knowledgeID string, newEntries []types.FAQEntryPayload,
) ([]types.FAQEntryPayload, []*types.Chunk, int, error) {
	// 获取 kbID 用于解析 tag
	var kbID string
	if len(newEntries) > 0 {
		// 从 knowledgeID 获取 kbID
		knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to get knowledge: %w", err)
		}
		if knowledge != nil {
			kbID = knowledge.KnowledgeBaseID
		}
	}

	// 计算所有新条目的 content hash，并同时构建 hash 到 entry 的映射
	type entryWithHash struct {
		entry types.FAQEntryPayload
		hash  string
		meta  *types.FAQChunkMetadata
	}
	entriesWithHash := make([]entryWithHash, 0, len(newEntries))
	newHashSet := make(map[string]bool)
	// 用于批次内标准问和相似问去重
	batchQuestions := make(map[string]bool)
	batchSkippedCount := 0

	for _, entry := range newEntries {
		meta, err := sanitizeFAQEntryPayload(&entry)
		if err != nil {
			batchSkippedCount++
			logger.Warnf(ctx, "Skipping invalid FAQ entry in replace mode: %v", err)
			continue
		}

		// 检查标准问是否在同批次中重复
		if batchQuestions[meta.StandardQuestion] {
			batchSkippedCount++
			logger.Infof(ctx, "Skipping FAQ entry with duplicate standard question in batch: %s", meta.StandardQuestion)
			continue
		}

		// 检查相似问是否在同批次中重复
		hasDuplicateSimilar := false
		for _, q := range meta.SimilarQuestions {
			if batchQuestions[q] {
				hasDuplicateSimilar = true
				logger.Infof(ctx, "Skipping FAQ entry with duplicate similar question in batch: %s (standard: %s)", q,
					meta.StandardQuestion)
				break
			}
		}
		if hasDuplicateSimilar {
			batchSkippedCount++
			continue
		}

		// 将当前条目的标准问和相似问加入批次集合
		batchQuestions[meta.StandardQuestion] = true
		for _, q := range meta.SimilarQuestions {
			batchQuestions[q] = true
		}

		hash := types.CalculateFAQContentHash(meta)
		if hash != "" {
			entriesWithHash = append(entriesWithHash, entryWithHash{entry: entry, hash: hash, meta: meta})
			newHashSet[hash] = true
		}
	}

	// 查询所有已存在的chunks
	allExistingChunks, err := s.chunkRepo.ListAllFAQChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to list existing chunks: %w", err)
	}

	// 在内存中过滤出匹配新条目hash的chunks，并构建map
	existingHashMap := make(map[string]*types.Chunk)
	for _, chunk := range allExistingChunks {
		if chunk.ContentHash != "" && newHashSet[chunk.ContentHash] {
			existingHashMap[chunk.ContentHash] = chunk
		}
	}

	// 计算需要删除的chunks（数据库中有但新批次中没有的，或hash不匹配的）
	chunksToDelete := make([]*types.Chunk, 0)
	for _, chunk := range allExistingChunks {
		if chunk.ContentHash == "" {
			// 如果没有hash，需要删除（可能是旧数据）
			chunksToDelete = append(chunksToDelete, chunk)
		} else if !newHashSet[chunk.ContentHash] {
			// hash不在新条目中，需要删除
			chunksToDelete = append(chunksToDelete, chunk)
		}
	}

	// 批量预加载 tag 信息，避免循环内逐条查询数据库
	// 收集所有需要查询的 tag_id 和 tag_name
	tagSeqIDSet := make(map[int64]bool)
	tagNameSet := make(map[string]bool)
	for _, ewh := range entriesWithHash {
		switch {
		case ewh.entry.TagID != 0:
			tagSeqIDSet[ewh.entry.TagID] = true
		case ewh.entry.TagName != "":
			tagNameSet[ewh.entry.TagName] = true
		default:
			tagNameSet[types.UntaggedTagName] = true
		}
	}

	// 批量查询 tag by seq_id
	tagSeqIDToUUID := make(map[int64]string)
	if len(tagSeqIDSet) > 0 {
		seqIDs := make([]int64, 0, len(tagSeqIDSet))
		for seqID := range tagSeqIDSet {
			seqIDs = append(seqIDs, seqID)
		}
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, seqIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to batch load tags by seq_ids: %v", err)
		} else {
			for _, tag := range tags {
				tagSeqIDToUUID[tag.SeqID] = tag.ID
			}
		}
	}

	// 批量查询 tag by name
	tagNameToUUID := make(map[string]string)
	if len(tagNameSet) > 0 && kbID != "" {
		for name := range tagNameSet {
			if tag, err := s.tagRepo.GetByName(ctx, tenantID, kbID, name); err == nil && tag != nil {
				tagNameToUUID[name] = tag.ID
			}
		}
	}

	logger.Infof(ctx, "Preloaded %d tags by seq_id, %d tags by name for %d entries",
		len(tagSeqIDToUUID), len(tagNameToUUID), len(entriesWithHash))

	// resolveTagIDFromCache 从缓存中解析 tag ID，缓存未命中时回退到数据库查询
	resolveTagIDFromCache := func(entry *types.FAQEntryPayload) (string, error) {
		if entry.TagID != 0 {
			if uuid, ok := tagSeqIDToUUID[entry.TagID]; ok {
				return uuid, nil
			}
			// 缓存未命中，回退到数据库查询（可能需要创建）
			return s.resolveTagID(ctx, kbID, entry)
		}
		tagName := entry.TagName
		if tagName == "" {
			tagName = types.UntaggedTagName
		}
		if uuid, ok := tagNameToUUID[tagName]; ok {
			return uuid, nil
		}
		// 缓存未命中，回退到数据库查询（可能需要创建）
		return s.resolveTagID(ctx, kbID, entry)
	}

	// 计算需要创建的条目（利用已经计算好的hash，避免重复计算）
	entriesToProcess := make([]types.FAQEntryPayload, 0, len(entriesWithHash))
	skippedCount := batchSkippedCount

	for idx, ewh := range entriesWithHash {
		// 每处理 1000 条打印一次进度日志
		if idx > 0 && idx%1000 == 0 {
			logger.Infof(ctx, "calculateReplaceOperations progress: %d/%d entries processed", idx, len(entriesWithHash))
		}

		existingChunk := existingHashMap[ewh.hash]
		if existingChunk != nil {
			// hash 匹配，检查 tag 是否变化
			newTagID, err := resolveTagIDFromCache(&ewh.entry)
			if err != nil {
				logger.Warnf(ctx, "Failed to resolve tag for entry, treating as new: %v", err)
				entriesToProcess = append(entriesToProcess, ewh.entry)
				continue
			}

			enabledChanged := ewh.entry.IsEnabled != nil && *ewh.entry.IsEnabled != existingChunk.IsEnabled
			recommendedChanged := ewh.entry.IsRecommended != nil &&
				*ewh.entry.IsRecommended != existingChunk.Flags.HasFlag(types.ChunkFlagRecommended)
			answerStrategyChanged := false
			if existingMeta, metaErr := existingChunk.FAQMetadata(); metaErr == nil && existingMeta != nil {
				answerStrategyChanged = ewh.meta.AnswerStrategy != existingMeta.AnswerStrategy
			}

			if existingChunk.TagID != newTagID || enabledChanged || recommendedChanged || answerStrategyChanged {
				if existingChunk.TagID != newTagID {
					logger.Infof(ctx, "FAQ entry tag changed from %s to %s, will update", existingChunk.TagID, newTagID)
				}
				if enabledChanged || recommendedChanged || answerStrategyChanged {
					logger.Infof(ctx,
						"FAQ entry operational fields changed (enabled: %v->%v, recommended: %v->%v, "+
							"answerStrategy changed: %v), will update",
						existingChunk.IsEnabled, ewh.entry.IsEnabled,
						existingChunk.Flags.HasFlag(types.ChunkFlagRecommended), ewh.entry.IsRecommended,
						answerStrategyChanged)
				}
				chunksToDelete = append(chunksToDelete, existingChunk)
				entriesToProcess = append(entriesToProcess, ewh.entry)
			} else {
				// hash、tag、运营状态都相同，跳过
				skippedCount++
			}
			continue
		}

		// hash 不匹配或不存在，需要创建
		entriesToProcess = append(entriesToProcess, ewh.entry)
	}

	return entriesToProcess, chunksToDelete, skippedCount, nil
}

// finalizeFAQValidation 完成 FAQ 验证/导入任务，生成失败条目 CSV（如果有）
func (s *knowledgeService) finalizeFAQValidation(ctx context.Context, payload *types.FAQImportPayload,
	progress *types.FAQImportProgress, originalTotalEntries int,
) error {
	// 清理对象存储中的 entries 文件（如果有）
	if payload.EntriesURL != "" {
		if err := s.fileSvc.DeleteFile(ctx, payload.EntriesURL); err != nil {
			logger.Warnf(ctx, "Failed to delete FAQ entries file from object storage: %v", err)
		} else {
			logger.Infof(ctx, "Deleted FAQ entries file from object storage: %s", payload.EntriesURL)
		}
	}
	progress.UpdatedAt = time.Now().Unix()

	// 如果有失败条目，生成 CSV 文件
	if len(progress.FailedEntries) > 0 {
		csvURL, err := s.generateFailedEntriesCSV(ctx, payload.TenantID, payload.TaskID, progress.FailedEntries)
		if err != nil {
			logger.Warnf(ctx, "Failed to generate failed entries CSV: %v", err)
		} else {
			progress.FailedEntriesURL = csvURL
			progress.FailedEntries = nil // 清空内联数据，使用 URL
			progress.Message += " (失败记录已导出为CSV)"
		}
	}

	// 必须在 saveFAQImportResultToDatabase 之前计算最终统计。
	progress.Status = types.FAQImportStatusCompleted
	progress.Progress = 100
	progress.Processed = originalTotalEntries

	switch {
	case len(progress.ValidEntryIndices) > 0:
		progress.SuccessCount = len(progress.ValidEntryIndices) - progress.PartialFailedCount
	case len(progress.SuccessEntries) > 0:
		progress.SuccessCount = len(progress.SuccessEntries) - progress.PartialFailedCount
	default:
		progress.SuccessCount = originalTotalEntries - progress.FailedCount - progress.PartialFailedCount
	}
	if progress.SuccessCount < 0 {
		progress.SuccessCount = 0
	}

	if progress.AddedCount == 0 && progress.MergedCount > 0 {
		progress.AddedCount = progress.SuccessCount - progress.MergedCount
		if progress.AddedCount < 0 {
			progress.AddedCount = 0
		}
	} else if progress.AddedCount == 0 {
		progress.AddedCount = progress.SuccessCount
	}

	skippedCount := originalTotalEntries - progress.SuccessCount - progress.PartialFailedCount - progress.FailedCount
	if skippedCount < 0 {
		skippedCount = 0
	}
	progress.SkippedCount = skippedCount

	if payload.DryRun {
		progress.Message = s.buildFAQImportResultMessage("验证完成", progress)
	} else {
		progress.Message = s.buildFAQImportResultMessage("导入完成", progress)
	}

	// 如果不是 dry run 模式，保存导入结果统计到数据库
	if !payload.DryRun {
		if err := s.saveFAQImportResultToDatabase(ctx, payload, progress, originalTotalEntries); err != nil {
			logger.Warnf(ctx, "Failed to save FAQ import result to database: %v", err)
		}

		// 只有 replace 模式才清理未使用的 Tag
		// append 模式不应删除用户预先创建的空标签
		if payload.Mode == types.FAQBatchModeReplace {
			deletedTags, err := s.tagRepo.DeleteUnusedTags(ctx, payload.TenantID, payload.KBID)
			if err != nil {
				logger.Warnf(ctx, "FAQ import task %s: failed to cleanup unused tags: %v", payload.TaskID, err)
			} else if deletedTags > 0 {
				logger.Infof(ctx, "FAQ import task %s: cleaned up %d unused tags after replace import", payload.TaskID,
					deletedTags)
			}
		}
	}

	// 使用 updateFAQImportProgressStatus 来确保正确清理 running key
	// 但是需要先保存其他字段，因为 updateFAQImportProgressStatus 不会保存所有字段
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to save final FAQ import progress: %v", err)
	}

	// 然后调用状态更新来清理 running key
	if err := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt,
		types.FAQImportStatusCompleted,
		100, originalTotalEntries, originalTotalEntries, progress.Message, ""); err != nil {
		logger.Warnf(ctx, "Failed to update final FAQ import status: %v", err)
	}

	logger.Infof(ctx,
		"FAQ task completed: %s, dry_run=%v, success: %d, added: %d, merged: %d, failed: %d, partial_failed: %d",
		payload.TaskID, payload.DryRun, progress.SuccessCount, progress.AddedCount, progress.MergedCount,
		progress.FailedCount, progress.PartialFailedCount)

	if !payload.DryRun {
		outcome := faqImportCompletedOutcome(progress.SuccessCount, progress.FailedCount, progress.SkippedCount)
		s.recordFAQImportKBActivity(ctx, payload, progress, originalTotalEntries,
			types.AuditActionFAQImportCompleted, outcome)
	}

	return nil
}
