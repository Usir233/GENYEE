package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/agent/tools"
	chatpipeline "github.com/Usir233/GENYEE/internal/application/service/chat_pipeline"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/common"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/searchutil"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// defaultMaxInputChars is the default maximum characters used as input for
// summary generation. The document profile (short summary, gist, topics,
// type, one question) is decided by the head of a document; 8k characters
// keeps the per-document cost a quarter of the previous 24k without changing
// what the profile can say.
const defaultMaxInputChars = 1024 * 8

// defaultSummaryMaxTokens bounds the JSON profile reply; every field is short.
const defaultSummaryMaxTokens = 1024

// documentSummaryResult is what one profiling call yields: the short summary
// stored in knowledge.Description plus the structured profile, when the model
// returned parseable JSON.
type documentSummaryResult struct {
	Summary string
	Profile *types.KnowledgeProfile
}

// documentProfileOutput mirrors the JSON contract of generate_summary.yaml.
type documentProfileOutput struct {
	Summary         string   `json:"summary"`
	Gist            string   `json:"gist"`
	Topics          []string `json:"topics"`
	DocType         string   `json:"doc_type"`
	TypicalQuestion string   `json:"typical_question"`
}

// normalizeSummaryReplyContent strips a UTF-8 BOM, the surrounding whitespace
// and one markdown code fence from a summary reply, so shape detection and
// parsing look at the same text. A fence without a newline carries no payload
// we can classify, so it normalizes to the empty string.
func normalizeSummaryReplyContent(content string) string {
	content = strings.TrimSpace(strings.TrimPrefix(content, "\uFEFF"))
	if rest, ok := strings.CutPrefix(content, "```"); ok {
		// Drop the fence's info string (```json) and the newline after it.
		idx := strings.IndexByte(rest, '\n')
		if idx < 0 {
			return ""
		}
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest[idx+1:]), "```"))
	}
	return content
}

// summaryReplyIsJSONShaped reports whether the reply really is a structured
// JSON payload, either directly or inside a markdown code fence. Only such
// replies are held to the structured contract below; anything else is a
// legacy plain-text summary.
//
// An object opening ("{") always counts, parseable or not: half an object is
// exactly the reply that must never be stored as a summary. A bracket opening
// is ambiguous — "[1] …" and "[文档摘要] …" are prose with a leading citation,
// not broken JSON — so it only counts when the reply is a JSON array or opens
// an array of objects/strings.
func summaryReplyIsJSONShaped(content string) bool {
	content = normalizeSummaryReplyContent(content)
	if content == "" {
		return false
	}
	switch content[0] {
	case '{':
		return true
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal([]byte(content), &arr) == nil {
			return true
		}
		first := strings.TrimLeft(content[1:], " \t\r\n")
		return strings.HasPrefix(first, "{") ||
			strings.HasPrefix(first, "[") ||
			strings.HasPrefix(first, `"`)
	default:
		return false
	}
}

// parseDocumentSummaryOutput accepts both the structured JSON reply and a
// legacy plain-text summary (custom templates, older models). Plain text is
// stored as the summary with no profile, so nothing that worked before
// regresses; only the knowledge-base aggregation loses that document's topics.
//
// A reply that opens like JSON is held to the JSON contract instead: a broken
// object (output cut off at MaxTokens, an unescaped newline inside a string)
// or an object with no usable text returns errSummaryOutputNotParsable. The
// raw reply used to be stored as the summary, which put half a JSON object in
// knowledges.description, marked the summary completed so Asynq never retried
// it, and embedded the same fragment into the RAG index.
func parseDocumentSummaryOutput(content string) (*documentSummaryResult, error) {
	// Some providers prefix the reply with a UTF-8 BOM; TrimSpace alone would
	// leave it in place, hide the "{" and let half an object through as prose.
	content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "\uFEFF"))
	if content == "" {
		return nil, errEmptySummaryOutput
	}
	jsonShaped := summaryReplyIsJSONShaped(content)
	var out documentProfileOutput
	if err := common.ParseLLMJsonResponse(content, &out); err != nil {
		if jsonShaped {
			return nil, fmt.Errorf("%w: %w", errSummaryOutputNotParsable, err)
		}
		// Legacy plain text: keep it as the summary with no profile.
		return &documentSummaryResult{Summary: content}, nil
	}
	summary := strings.TrimSpace(out.Summary)
	profile := (&types.KnowledgeProfile{
		Gist:            out.Gist,
		Topics:          out.Topics,
		DocType:         out.DocType,
		TypicalQuestion: out.TypicalQuestion,
	}).Normalize()
	if summary == "" && profile != nil {
		summary = profile.Gist
	}
	if summary == "" {
		if jsonShaped {
			// JSON without usable text is as unusable as broken JSON: the
			// caller must fail the attempt instead of storing the object.
			return nil, errSummaryOutputNotParsable
		}
		// Plain text that happened to contain balanced JSON punctuation.
		return &documentSummaryResult{Summary: content}, nil
	}
	return &documentSummaryResult{Summary: summary, Profile: profile}, nil
}

// buildSummaryChunkContent is the text embedded for the document-level
// summary chunk. The gist leads so the vector reflects the headline first.
func buildSummaryChunkContent(summary string, profile *types.KnowledgeProfile) string {
	summary = strings.TrimSpace(summary)
	if profile != nil {
		gist := strings.TrimSpace(profile.Gist)
		if gist != "" && !strings.EqualFold(gist, summary) {
			return "# Summary\n" + gist + "\n\n" + summary
		}
	}
	return "# Summary\n" + summary
}

// imageDominatedTextThreshold is the rune count below which a document is
// considered "image-dominated" — i.e. the body text is so sparse that we
// should fall back to full image enrichment (caption + OCR) for the summary
// LLM call. Above this threshold the document has enough native text that
// caption-only enrichment is preferable (OCR text from incidental figures
// would otherwise add noise without contributing to the main topic).
const imageDominatedTextThreshold = 200

// errInsufficientSummaryContent signals that getSummary refused to call the
// LLM because the document had no usable text after image markup was stripped
// (typical for scanned PDFs where VLM OCR yielded nothing). Callers should
// mark the knowledge's summary as failed instead of falling back to the first
// chunk's raw content (which would just be a bare image reference).
var (
	errInsufficientSummaryContent = errors.New("insufficient text content for summary generation")
	errEmptySummaryOutput         = errors.New("summary model returned empty output")
	// errSummaryOutputNotParsable marks a reply that is JSON-shaped but yields
	// no usable summary text — broken JSON (cut off at the completion budget,
	// unescaped newline) or an object without summary/gist. It is deliberately
	// not stored: the raw reply is not a summary.
	errSummaryOutputNotParsable = errors.New("summary model returned JSON without a usable summary")
	// errSummaryOutputTruncated marks a reply the provider stopped at the
	// completion budget, so its JSON can never be trusted as complete.
	errSummaryOutputTruncated = errors.New("summary model output was truncated at the completion budget")
)

const summaryFallbackMaxRunes = 500

// validateSummaryOutput rejects successful model responses that contain no
// user-visible text. It only looks at the content shape: a provider that
// stopped at the completion budget can still return usable text, and the older
// plain-text consumers (custom summary templates, table/column descriptions)
// deliberately keep whatever the model produced.
func validateSummaryOutput(response *types.ChatResponse) (string, error) {
	if response == nil {
		return "", errEmptySummaryOutput
	}
	content := strings.TrimSpace(tools.StripThinkBlocks(response.Content))
	if content == "" {
		return "", errEmptySummaryOutput
	}
	return content, nil
}

// validateStructuredSummaryDocument is the validator for the document-summary
// entry point (getSummary). On top of validateSummaryOutput it rejects a reply
// the provider cut off at the completion budget — but only when the reply was
// producing a structured summary, because that is the case where the truncated
// text is half a JSON object. A plain-text summary that hit the budget is
// still usable text and keeps the legacy fallback.
//
// The shape is judged before the finish reason, so "finish_reason=length" can
// never turn a plain-text reply into a failure on its own.
func validateStructuredSummaryDocument(response *types.ChatResponse) (string, error) {
	content, err := validateSummaryOutput(response)
	if err != nil {
		return "", err
	}
	structured := summaryReplyIsJSONShaped(content)
	if structured && chatpipeline.IsLengthFinishReason(response.FinishReason) {
		return "", errSummaryOutputTruncated
	}
	return content, nil
}

// firstTextChunkSummaryFallback preserves the existing deterministic fallback:
// use the first already-ordered text chunk and cap it by runes so Chinese and
// emoji are never cut in the middle of a UTF-8 sequence.
func firstTextChunkSummaryFallback(textChunks []*types.Chunk) string {
	if len(textChunks) == 0 || textChunks[0] == nil {
		return ""
	}
	fallback := strings.TrimSpace(textChunks[0].Content)
	runes := []rune(fallback)
	if len(runes) > summaryFallbackMaxRunes {
		fallback = string(runes[:summaryFallbackMaxRunes])
	}
	return fallback
}

// applyRetryableSummaryFailureState keeps an existing description visible
// while another attempt is queued, then publishes the deterministic fallback
// and marks only the summary subtask failed after the retry budget is exhausted.
func applyRetryableSummaryFailureState(
	knowledge *types.Knowledge, textChunks []*types.Chunk, willRetry bool,
) string {
	knowledge.UpdatedAt = time.Now()
	if willRetry {
		knowledge.SummaryStatus = types.SummaryStatusPending
		return ""
	}
	fallback := firstTextChunkSummaryFallback(textChunks)
	knowledge.Description = fallback
	knowledge.Profile = nil
	knowledge.SummaryStatus = types.SummaryStatusFailed
	return fallback
}

// saveSummaryState persists only the columns the summary task owns. The task
// holds a row snapshot across its LLM call; a full-row save of it wrote the
// parse status of that moment back over a cancel, a housekeeping failure or
// a reparse that landed meanwhile. Over a reparse it put "finalizing" on the
// new run's "processing" row, whose post-process then found the row no
// longer processing, skipped its fan-out and left it stuck.
func (s *knowledgeService) saveSummaryState(ctx context.Context, knowledge *types.Knowledge) error {
	if knowledge.UpdatedAt.IsZero() {
		knowledge.UpdatedAt = time.Now()
	}
	return s.repo.UpdateKnowledgeColumns(ctx, knowledge.ID, map[string]interface{}{
		"description":    knowledge.Description,
		"profile":        knowledge.Profile,
		"summary_status": knowledge.SummaryStatus,
		"updated_at":     knowledge.UpdatedAt,
	})
}

// summaryTaskWillRetry reports whether the current Asynq delivery has another
// configured attempt remaining. Calls outside an Asynq worker are terminal.
// Despite the name it is not summary-specific; processChunks uses it too.
func summaryTaskWillRetry(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
	if retryOK && maxRetryOK {
		return retried < maxRetry
	}
	retried, maxRetry, ok := types.TaskRetryMetadataFromContext(ctx)
	return ok && retried < maxRetry
}

// checkSufficientSummaryContent returns errInsufficientSummaryContent if the
// given content does not carry enough real text (after stripping image markup)
// for an LLM summary call, and logs a warning at the call site. Returns nil
// when the content passes the threshold.
//
// Extracted so the threshold gate can be unit-tested without standing up the
// full ProcessSummaryGeneration dependency graph.
func checkSufficientSummaryContent(ctx context.Context, knowledgeID, content string) error {
	realTextLen := realTextRuneCount(content)
	if realTextLen < minTextContentRunes {
		logger.GetLogger(ctx).Warnf(
			"summary content check: knowledge %s has insufficient text after stripping image markup "+
				"(real_text_runes=%d, min=%d); skipping LLM call",
			knowledgeID, realTextLen, minTextContentRunes,
		)
		return errInsufficientSummaryContent
	}
	return nil
}

// sortChunksForSummary orders chunks for document reconstruction. Parser
// StartAt offsets stay authoritative until any chunk has been manually edited;
// after that ChunkIndex is the safer reading order.
func sortChunksForSummary(chunks []*types.Chunk) []*types.Chunk {
	sorted := make([]*types.Chunk, len(chunks))
	copy(sorted, chunks)
	edited := false
	for _, chunk := range sorted {
		if chunk.ContentRevision > 0 {
			edited = true
			break
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if edited {
			if sorted[i].ChunkIndex != sorted[j].ChunkIndex {
				return sorted[i].ChunkIndex < sorted[j].ChunkIndex
			}
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].StartAt < sorted[j].StartAt
	})
	return sorted
}

// getSummary generates a summary for knowledge content using an AI model
func (s *knowledgeService) getSummary(ctx context.Context,
	summaryModel chat.Chat, knowledge *types.Knowledge, chunks []*types.Chunk,
) (*documentSummaryResult, error) {
	// Get knowledge info from the first chunk
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks provided for summary generation")
	}

	// Determine max input chars from config
	maxInputChars := defaultMaxInputChars
	if s.config.Conversation.Summary != nil && s.config.Conversation.Summary.MaxInputChars > 0 {
		maxInputChars = s.config.Conversation.Summary.MaxInputChars
	}

	// Sort chunks for reconstruction. Parser offsets remain authoritative until
	// any chunk has been manually edited; after that ChunkIndex is safer.
	sortedChunks := sortChunksForSummary(chunks)

	// Reconstruct the document before enriching it with image info. Enrichment
	// must happen AFTER reconstruction because StartAt is based on original
	// document offsets; enriched (longer) content would break the positioning.
	chunkContents := ""
	hasEditedChunk := false
	for _, chunk := range sortedChunks {
		if chunk.ContentRevision > 0 {
			hasEditedChunk = true
			break
		}
	}
	if hasEditedChunk {
		// Parser offsets describe the immutable source. Once a replacement has
		// changed length they can no longer be applied to the effective content;
		// concatenate current chunks instead of truncating at stale offsets.
		parts := make([]string, 0, len(sortedChunks))
		for _, chunk := range sortedChunks {
			if chunk.IsEnabled && strings.TrimSpace(chunk.Content) != "" {
				parts = append(parts, chunk.Content)
			}
		}
		chunkContents = strings.Join(parts, "\n\n")
	} else {
		// Synthetic table headers are not represented in StartAt/EndAt. Match
		// real text overlap instead of slicing by source offsets.
		chunkContents = searchutil.MergeTextChunks(sortedChunks, "")
	}

	// Collect image_info from image_ocr/image_caption children and enrich
	chunkIDs := make([]string, len(sortedChunks))
	for i, c := range sortedChunks {
		chunkIDs[i] = c.ID
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, knowledge.TenantID, chunkIDs)
	mergedImageInfo := searchutil.MergeImageInfoJSON(imageInfoMap)
	if mergedImageInfo != "" {
		// For image-dominated documents (e.g. a docx whose only payload is a
		// single embedded picture, or a screenshot-only file), captions alone
		// often carry too little signal — the real content lives in OCR text.
		// Detect that case by measuring the document's real (non-image-markup)
		// text BEFORE enrichment, and switch to full enrichment (caption + OCR)
		// when the body is essentially empty. Text-heavy documents stay on the
		// caption-only path to avoid OCR noise (page headers/footers/watermarks
		// from many figures diluting the main topic).
		if realTextRuneCount(chunkContents) < imageDominatedTextThreshold {
			// Caption + OCR (no URL/original wrappers — those are pure noise
			// for the summary LLM and have been observed to trigger the
			// "image reference with no extracted text" refusal heuristic).
			chunkContents = searchutil.EnrichContentCaptionAndOCR(chunkContents, mergedImageInfo)
		} else {
			chunkContents = searchutil.EnrichContentCaptionOnly(chunkContents, mergedImageInfo)
		}
	}

	// Apply length limit: sample long content to fit within maxInputChars
	chunkContents = sampleLongContent(chunkContents, maxInputChars)

	logger.GetLogger(ctx).Infof("getSummary: content length=%d chars (max=%d) for knowledge %s",
		len([]rune(chunkContents)), maxInputChars, knowledge.ID)

	// Bail out before the LLM call when there is not enough actual text to
	// summarise. We deliberately do not pass filename/file-type metadata to the
	// LLM: scanned PDFs frequently carry filenames like "MX5280.pdf" (the
	// scanner model), and feeding that to the model would invite it to
	// hallucinate a scanner manual instead of admitting the document had no
	// extractable text.
	if err := checkSufficientSummaryContent(ctx, knowledge.ID, chunkContents); err != nil {
		return nil, err
	}

	// User-authored metadata is trusted document context. Internal ingestion
	// metadata remains excluded because it contains IDs and pipeline controls.
	contentWithMetadata := chunkContents
	if custom := knowledge.CustomMetadataText(); custom != "" {
		contentWithMetadata = "Document metadata:\n" + custom + "\n\nDocument content:\n" + chunkContents
	}
	contentWithMetadata = sampleLongContent(contentWithMetadata, maxInputChars)

	// Determine max output tokens from config
	maxTokens := defaultSummaryMaxTokens
	if s.config.Conversation.Summary != nil && s.config.Conversation.Summary.MaxCompletionTokens > 0 {
		maxTokens = s.config.Conversation.Summary.MaxCompletionTokens
	}

	// Generate summary using AI model
	summaryPrompt := types.RenderPromptPlaceholders(s.config.Conversation.GenerateSummaryPrompt,
		types.PlaceholderValues{
			"language": types.LanguageNameFromContext(ctx),
		})
	thinking := false
	modelCtx := types.WithLLMCallMetadata(ctx, "document_summary", "")
	summary, err := summaryModel.Chat(modelCtx, []chat.Message{
		{
			Role:    "system",
			Content: summaryPrompt,
		},
		{
			Role:    "user",
			Content: contentWithMetadata,
		},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   maxTokens,
		Thinking:    &thinking,
	})
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("GetSummary failed")
		return nil, err
	}
	content, err := validateStructuredSummaryDocument(summary)
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Warnf("GetSummary returned no usable content")
		return nil, err
	}
	result, err := parseDocumentSummaryOutput(content)
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Warnf("GetSummary returned unusable content")
		return nil, err
	}
	logger.GetLogger(ctx).WithField("summary", result.Summary).
		WithField("has_profile", result.Profile != nil).Infof("GetSummary success")
	return result, nil
}

// sampleLongContent returns content that fits within maxChars.
// For short content (≤ maxChars), it is returned as-is.
// For long content, it samples: head (60%), tail (20%), and evenly-spaced middle (20%),
// joined by "[...content omitted...]" markers so the LLM knows content was skipped.
func sampleLongContent(content string, maxChars int) string {
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}

	const omitMarker = "\n\n[...content omitted...]\n\n"
	omitRunes := len([]rune(omitMarker))

	// Reserve space for two omit markers (head→middle, middle→tail)
	usable := maxChars - 2*omitRunes
	if usable < 100 {
		// Fallback: just truncate
		return string(runes[:maxChars])
	}

	headLen := usable * 60 / 100
	tailLen := usable * 20 / 100
	midLen := usable - headLen - tailLen

	head := string(runes[:headLen])
	tail := string(runes[len(runes)-tailLen:])

	// Sample middle portion: take a contiguous block from the center of the document
	midStart := len(runes)/2 - midLen/2
	if midStart < headLen {
		midStart = headLen
	}
	midEnd := midStart + midLen
	if midEnd > len(runes)-tailLen {
		midEnd = len(runes) - tailLen
		midStart = midEnd - midLen
		if midStart < headLen {
			midStart = headLen
		}
	}
	middle := string(runes[midStart:midEnd])

	return head + omitMarker + middle + omitMarker + tail
}

// ProcessSummaryGeneration handles async summary generation task
func (s *knowledgeService) ProcessSummaryGeneration(ctx context.Context, t *asynq.Task) (retErr error) {
	var payload types.SummaryGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal summary generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}
	logger.Infof(ctx, "Processing summary generation for knowledge: %s", payload.KnowledgeID)

	// Set tenant and language context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// A newer attempt (re-upload / edit / reparse) has superseded this one:
	// skip before opening the span or registering the FinalizeSubtask defer
	// so we neither read stale chunks nor decrement the new attempt's counter.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		logger.Infof(ctx, "summary: attempt %d superseded for %s, skipping stale enrichment",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}
	if payload.Refresh {
		var err error
		ctx, err = restoreSummaryRefreshTenantInfo(ctx, s.tenantRepo, payload.TenantID)
		if err == nil {
			_, err = s.RegenerateKnowledgeSummary(ctx, payload.KnowledgeID)
		}
		if err != nil {
			if errors.Is(err, ErrSummaryRefreshStale) {
				logger.Infof(ctx, "Discarding stale summary refresh for knowledge %s", payload.KnowledgeID)
				return nil
			}
			logger.Warnf(ctx, "Summary refresh failed for knowledge %s: %v", payload.KnowledgeID, err)
			if errors.Is(err, errInsufficientSummaryContent) {
				return nil
			}
			return err
		}
		return nil
	}

	// Open a subspan under the parent attempt's postprocess stage so the
	// trace surface shows the real summary-generation duration (LLM call
	// + chunk write + index) instead of just the upstream's enqueue time.
	// Closes via the deferred handler below — every return path lands in
	// the defer, including the early returns ahead.
	span := s.beginPostprocessSubspan(ctx, payload.KnowledgeID, payload.Attempt, "postprocess.summary",
		types.JSONMap{
			"language": payload.Language,
		})
	var summaryErr error
	summaryOut := types.JSONMap{}
	// profileKB is the knowledge base once loaded; the deferred handler uses
	// it to schedule the knowledge-base description refresh on terminal exit.
	var profileKB *types.KnowledgeBase
	defer func() {
		// Decrement the parent's enrichment counter on terminal exit.
		// "Terminal" is keyed on the value RETURNED to asynq, not on
		// summaryErr: several branches record a failure on the span
		// (summaryErr != nil) yet deliberately `return nil` so asynq does
		// NOT retry (e.g. insufficient text content, KB/knowledge fetch
		// failures). Those are terminal and must drain — keying on
		// summaryErr would skip them and leave the row stuck in
		// "finalizing". When we DO return an error asynq will retry, so
		// we only drain on the final attempt.
		finalizeSubtaskDetached(ctx, s.repo, payload.KnowledgeID, "summary",
			retErr, false, isFinalAsynqAttempt(ctx), payload.Attempt)
		// Every terminal exit changes what the knowledge-base aggregation
		// sees (a new profile, a cleared one, or a document that will never
		// get one), so the debounced refresh is requested regardless of
		// outcome. It is a no-op unless the KB opted in.
		if retErr == nil && profileKB != nil {
			_ = requestKnowledgeBaseProfileRefresh(ctx, s.task, profileKB, false)
		}
		if span == nil {
			return
		}
		if summaryErr != nil {
			s.failPostprocessSubspan(ctx, span, "SUMMARY_FAILED", summaryErr.Error(), summaryErr)
		} else {
			s.endPostprocessSubspan(ctx, span, summaryOut)
		}
	}()

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		summaryErr = err
		return nil
	}
	// Capture the resolved model id on the span output the moment we
	// know it — debugging "summary stage took 60s" benefits hugely from
	// seeing WHICH chat model was actually used (kb config drift, fall-
	// throughs to a slow upstream, etc.).
	summaryOut["model_id"] = kb.SummaryModelID
	profileKB = kb

	if kb.SummaryModelID == "" {
		logger.Warn(ctx, "Knowledge base summary model ID is empty, skipping summary generation")
		if colErr := s.repo.UpdateKnowledgeColumn(
			ctx, payload.KnowledgeID, "summary_status", types.SummaryStatusFailed,
		); colErr != nil {
			logger.Warnf(ctx, "Failed to mark summary status failed for %s: %v", payload.KnowledgeID, colErr)
		}
		summaryOut["skipped"] = "no_summary_model"
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		summaryErr = err
		return nil
	}
	// Short-circuit when the user cancelled parsing or the row is being deleted.
	if knowledge != nil {
		switch knowledge.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			logger.Infof(ctx, "Summary generation: knowledge aborted (%s), skipping: %s",
				knowledge.ParseStatus, payload.KnowledgeID)
			summaryOut["skipped"] = "knowledge_" + knowledge.ParseStatus
			return nil
		}
	}

	// Update summary status to processing
	knowledge.SummaryStatus = types.SummaryStatusProcessing
	knowledge.UpdatedAt = time.Now()
	if err := s.saveSummaryState(ctx, knowledge); err != nil {
		logger.Warnf(ctx, "Failed to update summary status to processing: %v", err)
	}

	// Helper function to mark summary as failed
	markSummaryFailed := func() {
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		if err := s.saveSummaryState(ctx, knowledge); err != nil {
			logger.Warnf(ctx, "Failed to update summary status to failed: %v", err)
		}
	}

	// Get text chunks for this knowledge
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunks: %v", err)
		markSummaryFailed()
		summaryErr = err
		return nil
	}

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}
	summaryOut["text_chunks"] = len(textChunks)

	if len(textChunks) == 0 {
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		knowledge.Description = ""
		knowledge.Profile = nil
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		if err := s.saveSummaryState(ctx, knowledge); err != nil {
			logger.Warnf(ctx, "Failed to mark summary failed for knowledge without text chunks: %v", err)
		}
		summaryOut["skipped"] = "no_text_chunks"
		return nil
	}

	// Sort chunks by ChunkIndex for proper ordering
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
	})

	summaryMetadataVersion := string(knowledge.CustomMetadata)
	handleRetryableSummaryFailure := func(generationErr error) error {
		summaryErr = generationErr
		summaryOut["error"] = previewText(generationErr.Error(), 500)
		summaryOut["error_type"] = fmt.Sprintf("%T", generationErr)

		if summaryTaskWillRetry(ctx) {
			applyRetryableSummaryFailureState(knowledge, textChunks, true)
			if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
				logger.Warnf(ctx, "Failed to mark summary pending for retry: %v", updateErr)
			}
			summaryOut["retrying"] = true
			return fmt.Errorf("summary generation attempt failed: %w", generationErr)
		}

		// Before publishing the terminal fallback, make sure its source still
		// matches the chunks and metadata captured for this attempt.
		stale, staleErr := summarySourceChanged(
			ctx, s.repo, s.chunkRepo, payload.TenantID, payload.KnowledgeID,
			summaryMetadataVersion, textChunks,
		)
		if staleErr != nil {
			logger.Errorf(ctx, "Failed to verify summary fallback freshness for knowledge %s: %v",
				payload.KnowledgeID, staleErr)
			markSummaryFailed()
			summaryErr = staleErr
			return fmt.Errorf("verify summary fallback freshness: %w", staleErr)
		}
		if stale {
			logger.Infof(ctx, "Discarding stale summary fallback for knowledge %s", payload.KnowledgeID)
			summaryOut["skipped"] = "content_revision_changed"
			return nil
		}

		fallback := applyRetryableSummaryFailureState(knowledge, textChunks, false)
		if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
			logger.Errorf(ctx, "Failed to save terminal summary fallback: %v", updateErr)
			summaryErr = updateErr
			return fmt.Errorf("save terminal summary fallback: %w", updateErr)
		}
		if fallback == "" {
			summaryOut["fallback"] = "empty"
		} else {
			summaryOut["fallback"] = "first_chunk"
		}
		summaryOut["fallback_chars"] = len([]rune(fallback))
		return fmt.Errorf("summary generation exhausted retries: %w", generationErr)
	}

	// Initialize chat model for summary. Model resolution failures use the same
	// retry budget and terminal first-chunk fallback as LLM request failures.
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return handleRetryableSummaryFailure(fmt.Errorf("get chat model: %w", err))
	}

	// Generate summary
	summaryResult, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate summary for knowledge %s: %v", payload.KnowledgeID, err)
		// Surface the underlying LLM/IO error on the span so the trace UI
		// can explain "why did this stage take 60s and then fall back?"
		// without forcing the operator to grep worker logs. We also capture
		// the error type to disambiguate timeouts from upstream HTTP errors
		// (deadline exceeded vs unexpected EOF vs 5xx, etc.).
		summaryOut["error"] = previewText(err.Error(), 500)
		summaryOut["error_type"] = fmt.Sprintf("%T", err)
		// For the insufficient-content case (scanned PDF without OCR, etc.)
		// we deliberately do NOT fall back to the first chunk's raw content,
		// since that chunk is typically just a bare markdown image reference
		// and surfacing it in the description is misleading.
		if errors.Is(err, errInsufficientSummaryContent) {
			knowledge.Description = ""
			knowledge.Profile = nil
			knowledge.SummaryStatus = types.SummaryStatusFailed
			knowledge.UpdatedAt = time.Now()
			if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
				logger.Errorf(ctx, "Failed to mark summary as failed: %v", updateErr)
				summaryErr = updateErr
				return fmt.Errorf("failed to update knowledge: %w", updateErr)
			}
			summaryOut["fallback"] = "insufficient_content"
			summaryErr = err
			return nil
		}
		return handleRetryableSummaryFailure(err)
	}
	// Do not publish an answer derived from a superseded chunk or metadata
	// version. A user can explicitly refresh again from the latest revision.
	staleSummary, staleErr := summarySourceChanged(
		ctx, s.repo, s.chunkRepo, payload.TenantID, payload.KnowledgeID, summaryMetadataVersion, textChunks,
	)
	if staleErr != nil {
		logger.Errorf(ctx, "Failed to verify summary freshness for knowledge %s: %v", payload.KnowledgeID, staleErr)
		markSummaryFailed()
		summaryErr = staleErr
		return fmt.Errorf("verify summary freshness: %w", staleErr)
	}
	if staleSummary {
		logger.Infof(ctx, "Discarding stale summary for knowledge %s", payload.KnowledgeID)
		summaryOut["skipped"] = "content_revision_changed"
		return nil
	}

	// Update knowledge description and structured profile
	summary := summaryResult.Summary
	knowledge.Description = summary
	knowledge.Profile = summaryResult.Profile
	knowledge.SummaryStatus = types.SummaryStatusCompleted
	knowledge.UpdatedAt = time.Now()
	summaryOut["summary_chars"] = len([]rune(summary))
	summaryOut["has_profile"] = summaryResult.Profile != nil
	// Preview the generated summary on the span output so the trace
	// viewer can show "this is what the LLM produced" at a glance,
	// without hopping to the knowledge-detail page. Capped to keep
	// span rows compact.
	summaryOut["summary_preview"] = previewText(summary, 240)
	if err := s.saveSummaryState(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge description: %v", err)
		summaryErr = err
		return fmt.Errorf("failed to update knowledge: %w", err)
	}

	// Create summary chunk and index it — only when RAG indexing is enabled.
	// Wiki-only KBs don't need summary chunks in the vector index.
	if strings.TrimSpace(summary) != "" && kb.NeedsEmbeddingModel() {
		// Get max chunk index
		maxChunkIndex := 0
		for _, chunk := range chunks {
			if chunk.ChunkIndex > maxChunkIndex {
				maxChunkIndex = chunk.ChunkIndex
			}
		}

		// Embed only the LLM-generated summary in the indexed chunk.
		// We deliberately omit knowledge.FileName here: filenames are an
		// unreliable signal (e.g. "MX5280.pdf" for a scanned legal letter)
		// and surfacing them in retrieved RAG context can re-introduce the
		// hallucination vector this branch is meant to close.
		summaryChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         buildSummaryChunkContent(summary, summaryResult.Profile),
			ChunkIndex:      maxChunkIndex + 1,
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         0,
			EndAt:           0,
			ChunkType:       types.ChunkTypeSummary,
			ParentChunkID:   textChunks[0].ID,
		}

		// Save summary chunk
		if err := s.chunkRepo.CreateChunks(ctx, []*types.Chunk{summaryChunk}); err != nil {
			logger.Errorf(ctx, "Failed to create summary chunk: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to create summary chunk: %w", err)
		}

		// Index summary chunk
		tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get tenant info: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to get tenant info: %w", err)
		}
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

		retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
		if err != nil {
			logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to init retrieve engine: %w", err)
		}

		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get embedding model: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to get embedding model: %w", err)
		}

		indexInfo := []*types.IndexInfo{{
			Content:         summaryChunk.Content,
			SourceID:        summaryChunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         summaryChunk.ID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			IsEnabled:       true,
		}}

		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo); err != nil {
			logger.Errorf(ctx, "Failed to index summary chunk: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to index summary chunk: %w", err)
		}

		logger.Infof(ctx, "Successfully created and indexed summary chunk for knowledge: %s", payload.KnowledgeID)
		summaryOut["summary_chunk_indexed"] = true
	}

	logger.Infof(ctx, "Successfully generated summary for knowledge: %s", payload.KnowledgeID)
	summaryOut["status"] = "completed"
	return nil
}

// RegenerateKnowledgeSummary refreshes both the knowledge description and the
// summary chunk(s), then reindexes only those summary chunks. It is
// intentionally idempotent.
func (s *knowledgeService) RegenerateKnowledgeSummary(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if kb.SummaryModelID == "" {
		return nil, fmt.Errorf("summary model is not configured")
	}
	allChunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range allChunks {
		if chunk.ChunkType == types.ChunkTypeText && chunk.IsEnabled {
			textChunks = append(textChunks, chunk)
		}
	}
	if len(textChunks) == 0 {
		knowledge.Description = ""
		knowledge.Profile = nil
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
			return knowledge, updateErr
		}
		_ = requestKnowledgeBaseProfileRefresh(ctx, s.task, kb, false)
		return knowledge, errInsufficientSummaryContent
	}
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
	})
	metadataVersion := string(knowledge.CustomMetadata)
	knowledge.SummaryStatus = types.SummaryStatusProcessing
	if err := s.saveSummaryState(ctx, knowledge); err != nil {
		return nil, err
	}
	handleGenerationFailure := func(generationErr error) (*types.Knowledge, error) {
		if errors.Is(generationErr, errInsufficientSummaryContent) {
			knowledge.Description = ""
			knowledge.Profile = nil
			knowledge.SummaryStatus = types.SummaryStatusFailed
			knowledge.UpdatedAt = time.Now()
			if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
				return knowledge, updateErr
			}
			return knowledge, generationErr
		}
		if summaryTaskWillRetry(ctx) {
			applyRetryableSummaryFailureState(knowledge, textChunks, true)
			if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
				logger.Warnf(ctx, "Failed to mark summary refresh pending for retry: %v", updateErr)
			}
			return knowledge, generationErr
		}

		stale, staleErr := summarySourceChanged(
			ctx, s.repo, s.chunkRepo, tenantID, knowledgeID, metadataVersion, textChunks,
		)
		if staleErr != nil {
			knowledge.SummaryStatus = types.SummaryStatusFailed
			if saveErr := s.saveSummaryState(ctx, knowledge); saveErr != nil {
				logger.Warnf(ctx, "Failed to save failed summary state for %s: %v", knowledgeID, saveErr)
			}
			return knowledge, fmt.Errorf("verify summary fallback freshness: %w", staleErr)
		}
		if stale {
			return knowledge, ErrSummaryRefreshStale
		}

		applyRetryableSummaryFailureState(knowledge, textChunks, false)
		if updateErr := s.saveSummaryState(ctx, knowledge); updateErr != nil {
			return knowledge, updateErr
		}
		return knowledge, generationErr
	}

	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		return handleGenerationFailure(fmt.Errorf("get chat model: %w", err))
	}
	summaryResult, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		return handleGenerationFailure(err)
	}
	stale, err := summarySourceChanged(
		ctx, s.repo, s.chunkRepo, tenantID, knowledgeID, metadataVersion, textChunks,
	)
	if err != nil {
		return nil, fmt.Errorf("verify summary freshness: %w", err)
	}
	if stale {
		logger.Infof(ctx, "Discarding stale summary refresh for knowledge %s", knowledgeID)
		return nil, ErrSummaryRefreshStale
	}
	summary := summaryResult.Summary
	knowledge.Description = summary
	knowledge.Profile = summaryResult.Profile
	knowledge.SummaryStatus = types.SummaryStatusCompleted
	knowledge.UpdatedAt = time.Now()
	if err := s.saveSummaryState(ctx, knowledge); err != nil {
		return nil, err
	}
	summaryChunkContent := buildSummaryChunkContent(summary, summaryResult.Profile)
	defer func() { _ = requestKnowledgeBaseProfileRefresh(ctx, s.task, kb, false) }()
	if kb.NeedsEmbeddingModel() {
		maxIndex := 0
		for _, chunk := range allChunks {
			if chunk.ChunkIndex > maxIndex {
				maxIndex = chunk.ChunkIndex
			}
		}
		// allChunks holds text chunks only, so it can never carry the existing
		// summary chunk. Scanning it for one always came up empty, which left
		// every refresh appending a new summary chunk beside the stale one --
		// and a stale summary stays enabled and indexed, so content the user
		// edited out of the document kept being retrievable through it.
		existingSummaries, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(
			ctx, tenantID, knowledgeID, []types.ChunkType{types.ChunkTypeSummary},
		)
		if err != nil {
			return nil, err
		}
		summaryChunks := make([]*types.Chunk, 0, len(existingSummaries))
		for _, chunk := range existingSummaries {
			chunk.Content = summaryChunkContent
			chunk.SourceContent = chunk.Content
			chunk.IsEnabled = true
			chunk.UpdatedAt = time.Now()
			if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
				return nil, err
			}
			summaryChunks = append(summaryChunks, chunk)
		}
		if len(summaryChunks) == 0 {
			summaryChunk := &types.Chunk{
				ID: uuid.NewString(), TenantID: tenantID, KnowledgeID: knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID, Content: summaryChunkContent,
				ChunkIndex: maxIndex + 1, IsEnabled: true, ChunkType: types.ChunkTypeSummary,
				ParentChunkID: textChunks[0].ID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}
			if err := s.chunkRepo.CreateChunks(ctx, []*types.Chunk{summaryChunk}); err != nil {
				return nil, err
			}
			summaryChunks = append(summaryChunks, summaryChunk)
		}
		if err := s.updateChunkVector(ctx, knowledge.KnowledgeBaseID, summaryChunks); err != nil {
			return nil, err
		}
	}
	return knowledge, nil
}
