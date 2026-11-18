package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/agent"
	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/searchutil"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// generateWithTemplate executes a prompt template and calls the LLM with
// bounded exponential-backoff retries for transient infrastructure errors.
//
// Retry policy:
//   - Up to wikiLLMMaxAttempts total attempts (initial + retries).
//   - Only retry errors classified as transient by isTransientLLMError:
//     HTTP 408/429/5xx, context deadline exceeded (when the parent ctx is
//     still alive), or generic "timeout"/"connection reset" wording.
//     4xx (except 408/429) is a caller-side fault and fails fast.
//   - Backoff is exponential base 2s: 2s, 4s, 8s — roughly wikiLLMBackoffBase
//   - 2^(attempt-1). Honors ctx cancellation so the task can abort.
//
// This exists because wiki ingest makes several independent LLM calls per
// document (extraction, summary, dedup, citations, intro) and a single
// transient 504 from the upstream gateway used to drop the document's
// summary page permanently. Retries plus failedOps requeuing (see
// mapOneDocument) turn those events into at-most-a-few-minute hiccups.
//
// Callers that need the provider stop reason — or that write a page body and
// therefore care whether the answer was cut off — use
// generateWithTemplateResult instead.
func (s *wikiIngestService) generateWithTemplate(ctx context.Context, chatModel chat.Chat, promptTpl string,
	data map[string]string,
) (string, error) {
	result, err := s.generateWithTemplateResult(ctx, chatModel, promptTpl, data)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// wikiTemplateResult is one wiki LLM answer: the text plus the provider's stop
// reason. The stop reason used to be dropped on the floor, which is why a page
// rewrite cut off at the completion budget was stored verbatim with no trace.
type wikiTemplateResult struct {
	Content      string
	FinishReason string
}

// errWikiPageRewriteTruncated is returned when the editor model stopped at the
// completion budget and every continuation round did too. The caller treats it
// like any other reduce failure: it logs, keeps the existing page, and flags the
// addition as failed — a truncated page is never written.
var errWikiPageRewriteTruncated = errors.New("wiki page rewrite truncated at the completion budget")

// isLengthStopFinishReason reports whether a provider finish reason means the
// answer was cut off by the completion budget rather than finished. Same
// vocabulary as the agent loop (internal/agent/observe.go, compaction/overflow.go).
func isLengthStopFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	default:
		return false
	}
}

// generateWithTemplateResult is generateWithTemplate plus the provider stop
// reason, and it is where a truncated page rewrite is continued instead of
// accepted.
//
// Why continuation lives here: reduce hands the editor the WHOLE page and takes
// back a full rewrite (see reduceSlugUpdates), so the model's output is not a
// summary of the page, it IS the page. Long enumerations — a certificate ledger
// with a hundred-plus holder rows — are emitted row by row and the provider
// stops the model mid-table. Before this, the fragment was persisted verbatim:
// a 146-row source table became a 119-row page, the page history showed an
// ordinary edit, and nothing in the logs said "truncated".
func (s *wikiIngestService) generateWithTemplateResult(
	ctx context.Context,
	chatModel chat.Chat,
	promptTpl string,
	data map[string]string,
) (wikiTemplateResult, error) {
	tmpl, err := template.New("wiki").Parse(promptTpl)
	if err != nil {
		return wikiTemplateResult{}, fmt.Errorf("parse template: %w", err)
	}

	maskedData, urlMap := maskTemplateDataImageURLs(data)

	var buf strings.Builder
	if err := tmpl.Execute(&buf, maskedData); err != nil {
		return wikiTemplateResult{}, fmt.Errorf("execute template: %w", err)
	}

	prompt := buf.String()
	purpose := wikiPromptPurpose(promptTpl)
	messages := []chat.Message{{Role: "user", Content: prompt}}
	if promptTpl == agent.WikiPageModifyUserPrompt {
		systemPrompt := types.AppendCustomPromptInstructions(
			agent.WikiPageModifySystemPrompt,
			maskedData["CustomInstructions"],
			maskedData["InstructionScope"],
		)
		messages = []chat.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		}
	} else {
		messages[0].Content = types.AppendCustomPromptInstructions(
			prompt, maskedData["CustomInstructions"], maskedData["InstructionScope"],
		)
	}
	thinking := false
	opts := &chat.ChatOptions{Temperature: 0.3, Thinking: &thinking, MaxTokens: wikiLLMMaxTokens}
	prefixFingerprint := chat.PromptPrefixFingerprint(messages, opts)
	warmupKey := ""
	if promptTpl == agent.WikiPageModifyUserPrompt {
		prefixFingerprint = chat.FingerprintPromptPrefix(
			messages[0].Content, maskedData["SharedSourceContexts"],
		)
		if tenantID, ok := types.TenantIDFromContext(ctx); ok {
			warmupKey = chat.BuildPromptCacheKey(
				tenantID, chatModel.GetModelID(), purpose, prefixFingerprint,
			)
		}
	}
	ctx = types.WithLLMCallMetadata(ctx, purpose, prefixFingerprint)

	tenantID, tenantScoped := types.TenantIDFromContext(ctx)
	requestJSON, _ := json.Marshal(struct {
		Messages []chat.Message    `json:"messages"`
		Options  *chat.ChatOptions `json:"options"`
	}{Messages: messages, Options: opts})
	requestKey := chat.BuildPromptCacheKey(
		tenantID, chatModel.GetModelID(), "wiki_exact_request",
		chat.FingerprintPromptPrefix(string(requestJSON)),
	)

	execute := func() (interface{}, error) {
		releaseWarmup := func() {}
		if tenantScoped && promptTpl == agent.WikiPageModifyUserPrompt &&
			strings.TrimSpace(maskedData["SharedSourceContexts"]) != "" {
			var warmupErr error
			releaseWarmup, warmupErr = s.awaitWikiPromptWarmup(ctx, warmupKey)
			if warmupErr != nil {
				return wikiTemplateResult{}, warmupErr
			}
		}
		defer releaseWarmup()

		// call runs one LLM request under the bounded transient-error retry
		// policy and hands back the full response (the caller needs
		// FinishReason, which the old signature threw away).
		call := func(msgs []chat.Message) (*types.ChatResponse, error) {
			var lastErr error
			for attempt := 1; attempt <= wikiLLMMaxAttempts; attempt++ {
				response, callErr := chatModel.Chat(ctx, msgs, opts)
				if callErr == nil && response != nil {
					return response, nil
				}
				if callErr == nil {
					callErr = errors.New("LLM returned nil response")
				}
				lastErr = callErr

				if !isTransientLLMError(ctx, callErr) {
					return nil, fmt.Errorf("LLM call failed: %w", callErr)
				}
				if attempt == wikiLLMMaxAttempts {
					break
				}

				backoff := wikiLLMBackoffBase << (attempt - 1)
				logger.Warnf(ctx, "wiki ingest: LLM call failed (attempt %d/%d), retrying in %s: %v",
					attempt, wikiLLMMaxAttempts, backoff, callErr)
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, fmt.Errorf("LLM call aborted during backoff: %w", ctx.Err())
				case <-timer.C:
				}
			}
			return nil, fmt.Errorf("LLM call failed after %d attempts: %w", wikiLLMMaxAttempts, lastErr)
		}

		// A page rewrite is the one answer whose value is the whole text: a
		// fragment is a broken page, not a shorter answer. So when the provider
		// stops at the completion budget, replay the fragment as an assistant
		// turn and ask for the tail. Everything else keeps the single-call
		// behaviour (a truncated JSON extraction already fails its parse).
		canContinue := promptTpl == agent.WikiPageModifyUserPrompt

		var (
			rewrite      strings.Builder
			finishReason string
			conversation = messages
		)
		for round := 0; ; round++ {
			response, callErr := call(conversation)
			if callErr != nil {
				return wikiTemplateResult{}, callErr
			}
			finishReason = response.FinishReason

			// A continuation round that has nothing left to add ends the loop
			// without appending its sentinel to the page.
			if round > 0 && strings.EqualFold(strings.TrimSpace(response.Content), wikiPageModifyContinuationDone) {
				finishReason = "stop"
				break
			}

			fragment := response.Content
			if canContinue && round > 0 {
				fragment = trimWikiContinuationOverlap(rewrite.String(), fragment)
			}
			rewrite.WriteString(fragment)
			if canContinue {
				if err := validateWikiRewrite(rewrite.String(), maskedData); err != nil {
					return wikiTemplateResult{}, err
				}
			}

			if !canContinue || !isLengthStopFinishReason(finishReason) || round >= wikiPageModifyMaxContinuations {
				break
			}

			logger.Warnf(ctx,
				"wiki ingest: page rewrite %s hit the completion budget (finish_reason=%s, "+
					"%d chars so far); requesting continuation %d/%d",
				maskedData["PageSlug"], finishReason, rewrite.Len(), round+1, wikiPageModifyMaxContinuations)

			conversation = append(
				append([]chat.Message(nil), conversation...),
				chat.Message{Role: "assistant", Content: response.Content},
				chat.Message{Role: "user", Content: agent.WikiPageModifyContinuationPrompt},
			)
		}

		content := rewrite.String()
		if canContinue && isLengthStopFinishReason(finishReason) {
			// Out of continuation rounds and still cut off. Refuse the fragment:
			// the caller keeps the existing page and flags the addition, which is
			// recoverable; storing a half page is not.
			logger.Warnf(ctx,
				"wiki ingest: page rewrite %s still truncated after %d continuation rounds "+
					"(finish_reason=%s, %d chars); refusing the partial page",
				maskedData["PageSlug"], wikiPageModifyMaxContinuations, finishReason, len(content))
			return wikiTemplateResult{}, fmt.Errorf(
				"%w (finish_reason=%s after %d continuation rounds, %d chars)",
				errWikiPageRewriteTruncated, finishReason, wikiPageModifyMaxContinuations, len(content))
		}

		return wikiTemplateResult{Content: content, FinishReason: finishReason}, nil
	}

	// Missing tenant context is unexpected for production Wiki work. Fail safe
	// by skipping cross-call coalescing instead of putting unrelated requests
	// into a synthetic tenant-0 bucket.
	if !tenantScoped {
		value, executeErr := execute()
		if executeErr != nil {
			return wikiTemplateResult{}, executeErr
		}
		result, _ := value.(wikiTemplateResult)
		result.Content = unmaskImageURLs(result.Content, urlMap)
		return result, nil
	}
	resultCh := s.llmRequests.DoChan(requestKey, execute)

	select {
	case <-ctx.Done():
		return wikiTemplateResult{}, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return wikiTemplateResult{}, result.Err
		}
		rewritten, _ := result.Val.(wikiTemplateResult)
		rewritten.Content = unmaskImageURLs(rewritten.Content, urlMap)
		return rewritten, nil
	}
}

func wikiPromptPurpose(promptTpl string) string {
	switch promptTpl {
	case agent.WikiPageModifyUserPrompt:
		return "wiki_page_modify"
	case agent.WikiChunkCitationPrompt:
		return "wiki_chunk_citation"
	case agent.WikiCandidateSlugPrompt:
		return "wiki_candidate_slug"
	case agent.WikiSummaryPrompt:
		return "wiki_summary"
	case agent.WikiKnowledgeExtractPrompt:
		return "wiki_knowledge_extract"
	case agent.WikiTaxonomyPlanPrompt:
		return "wiki_taxonomy_plan"
	case agent.WikiDeduplicationPrompt:
		return "wiki_deduplication"
	case agent.WikiIndexIntroPrompt, agent.WikiIndexIntroUpdatePrompt:
		return "wiki_index_intro"
	default:
		return "wiki_generation"
	}
}

func (s *wikiIngestService) awaitWikiPromptWarmup(ctx context.Context, key string) (func(), error) {
	if key == "" {
		return func() {}, nil
	}
	candidate := &wikiPromptWarmup{done: make(chan struct{})}
	actual, loaded := s.promptWarmups.LoadOrStore(key, candidate)
	entry := actual.(*wikiPromptWarmup)
	if !loaded {
		return func() {
			entry.once.Do(func() { close(entry.done) })
			// Keep the local warmed marker long enough to cover the parallel
			// reduce burst without turning it into a persistent application cache.
			time.AfterFunc(4*time.Minute, func() {
				s.promptWarmups.CompareAndDelete(key, entry)
			})
		}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-entry.done:
		return func() {}, nil
	}
}

// isTransientLLMError reports whether an error from the chat provider
// looks like an infrastructure hiccup worth retrying. Classification is
// intentionally conservative: the truthful "could not tell, assume
// permanent" choice keeps retries cheap and avoids masking real bugs.
//
// We treat the following as transient:
//   - HTTP 408 (client request timeout — upstream usually didn't process),
//     429 (rate-limited — retry after backoff may succeed), 5xx (any
//     server-side fault, including the 504 "Remote error, timeout with
//     60" we see from the gateway in front of several LLM providers).
//   - Wrapped context.DeadlineExceeded when the parent ctx is still alive
//     (nested per-call timeouts).
//   - Substring matches on the error text for common transport failures
//     ("timeout", "connection reset", "EOF") that providers surface
//     without a structured status code.
//
// rateLimitErrorIndicators are substrings that mark an HTTP 403 response
// body as rate limiting rather than authorization failure. Providers embed
// the response body in their errors ("API request failed with status 403:
// {...}"), and some gateways throttle with 403 (e.g. code 0x04030020,
// "调用频率（qpm）超限") instead of the standard 429, so the status alone
// is not enough to classify the failure.
var rateLimitErrorIndicators = []string{
	"qpm",        // 网关 qpm 配额（0x04030020）
	"qps",        // 网关 qps 配额
	"rate limit", // OpenAI-style "rate limit reached"
	"rate_limit",
	"too many requests", // RFC 6585 language
	"throttl",           // "throttled"
	"调用频率",              // 中文网关常见措辞
	"频率超限",
	"请求过于频繁",
	"繁忙", // "服务繁忙，请稍后重试"
	"try again later",
	"retry later",
	"slow down",
}

func isTransientLLMError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// Never retry after the parent ctx itself expired — the task is
	// being cancelled and the next attempt would just fail again.
	if ctx.Err() != nil {
		return false
	}

	msg := err.Error()
	// Providers that bubble HTTP status up formatted as
	// "API request failed with status NNN: ..." — match that first.
	for _, s := range []string{
		"status 408", "status 429",
		"status 500", "status 501", "status 502", "status 503", "status 504",
		"status 520", "status 521", "status 522", "status 523", "status 524",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}

	lower := strings.ToLower(msg)
	// Some gateways report QPM/QPS throttling as HTTP 403 instead of 429
	// (e.g. a MaaS gateway returning code 0x04030020, message
	// "调用频率（qpm）超限"). A plain 403 is usually an authorization
	// failure and must NOT be retried, so this stays gated on rate-limit
	// indicators in the response body, which provider errors embed:
	// "API request failed with status 403: {"code":0x04030020,...}".
	if strings.Contains(msg, "status 403") {
		for _, s := range rateLimitErrorIndicators {
			if strings.Contains(lower, s) {
				return true
			}
		}
	}

	for _, s := range []string{
		"timeout",
		"timed out",
		"connection reset",
		"connection refused",
		"broken pipe",
		"no such host", // DNS hiccup
		"i/o timeout",
		"unexpected eof",
		"tls handshake",
		"context deadline exceeded", // nested per-call deadline
	} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// --- Helpers ---

// isKnowledgeGone returns true if the given knowledge has been deleted or is
// in the middle of being deleted. It first consults the Redis tombstone
// (written by cleanupWikiOnKnowledgeDelete) as a fast path, then falls back
// to the DB. A nil result from GetKnowledgeByIDOnly also counts as gone: the
// repo layer uses GORM First() which filters soft-deleted rows, so a
// soft-deleted knowledge surfaces as "not found" here — exactly what we want.
// Other lookup errors leave liveness unknown and must reach the caller so it
// can retry instead of discarding the document's pending work.
func (s *wikiIngestService) isKnowledgeGone(ctx context.Context, kbID, knowledgeID string) (bool, error) {
	if knowledgeID == "" {
		return true, nil
	}
	if s.redisClient != nil {
		if exists, err := s.redisClient.Exists(ctx, WikiDeletedTombstoneKey(kbID, knowledgeID)).Result(); err == nil &&
			exists > 0 {
			return true, nil
		}
	}
	kn, err := s.knowledgeSvc.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if errors.Is(err, apprepo.ErrKnowledgeNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("wiki ingest: check knowledge %s: %w", knowledgeID, err)
	}
	if kn == nil {
		return true, nil
	}
	if tenantID, ok := types.TenantIDFromContext(ctx); ok && tenantID != 0 && kn.TenantID != 0 &&
		kn.TenantID != tenantID {
		return true, nil
	}
	if kbID != "" && kn.KnowledgeBaseID != "" && kn.KnowledgeBaseID != kbID {
		return true, nil
	}
	switch kn.ParseStatus {
	case types.ParseStatusDeleting, types.ParseStatusCancelled:
		return true, nil
	}
	return false, nil
}

// filterLiveUpdates drops additions/summaries whose source knowledge has been
// deleted since the Map phase finished. Retract updates are preserved so
// pages still get cleaned up. Caches per-knowledge results to avoid DB
// hammering when a single reduce slug carries many updates for the same doc.
func (s *wikiIngestService) filterLiveUpdates(
	ctx context.Context, kbID string, updates []SlugUpdate,
) ([]SlugUpdate, error) {
	if len(updates) == 0 {
		return updates, nil
	}
	goneCache := make(map[string]bool)
	isGone := func(kid string) (bool, error) {
		if kid == "" {
			return false, nil
		}
		if v, ok := goneCache[kid]; ok {
			return v, nil
		}
		v, err := s.isKnowledgeGone(ctx, kbID, kid)
		if err != nil {
			return false, err
		}
		goneCache[kid] = v
		return v, nil
	}
	filtered := make([]SlugUpdate, 0, len(updates))
	dropped := 0
	for _, u := range updates {
		switch u.Type {
		case "retract", "retractStale":
			filtered = append(filtered, u)
		default:
			gone, err := isGone(u.KnowledgeID)
			if err != nil {
				return nil, err
			}
			if gone {
				dropped++
				continue
			}
			filtered = append(filtered, u)
		}
	}
	if dropped > 0 {
		logger.Infof(ctx, "wiki ingest: reduce dropped %d updates for deleted knowledge(s)", dropped)
	}
	return filtered, nil
}

// reconstructContent rebuilds document text from chunks.
//
// This only concatenates text-type chunks — image OCR / caption information is
// stored on image_ocr / image_caption child chunks (see image_multimodal.go),
// not on the parent text chunk's ImageInfo field. Callers that need the full
// enriched content (with OCR / captions inlined) should call
// reconstructEnrichedContent instead so image info is fetched from child
// chunks and embedded alongside Markdown image links.
func reconstructContent(chunks []*types.Chunk) string {
	var textChunks []*types.Chunk
	edited := false
	for _, c := range chunks {
		if c == nil || !c.IsEnabled {
			continue
		}
		if c.ChunkType == types.ChunkTypeText || c.ChunkType == "" {
			textChunks = append(textChunks, c)
			edited = edited || c.ContentRevision > 0
		}
	}
	if edited {
		// Source offsets describe the original file. Preserve every current
		// body in reading order, as summary reconstruction does after edits.
		ordered := sortChunksForSummary(textChunks)
		parts := make([]string, 0, len(ordered))
		for _, c := range ordered {
			if strings.TrimSpace(c.Content) != "" {
				parts = append(parts, c.Content)
			}
		}
		return strings.Join(parts, "\n\n")
	}

	// 重叠去重与排序统一交给公共逻辑（按文本匹配，兼容补写表头 / HTML 实体）。
	return searchutil.MergeTextChunks(textChunks, "\n")
}

// reconstructEnrichedContent rebuilds document text and inlines image_info
// (OCR text + caption) pulled from image_ocr / image_caption child chunks.
//
// Without this enrichment, image-heavy documents (e.g. a scanned PDF or a
// standalone .jpg) reach the LLM as bare Markdown image links, causing
// extraction / summarization to produce empty or "no textual content" output.
func reconstructEnrichedContent(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	chunks []*types.Chunk,
) string {
	content := reconstructContent(chunks)

	var textChunkIDs []string
	for _, c := range chunks {
		if c == nil || !c.IsEnabled {
			continue
		}
		if c.ChunkType == types.ChunkTypeText || c.ChunkType == "" {
			if c.ID != "" {
				textChunkIDs = append(textChunkIDs, c.ID)
			}
		}
	}
	if len(textChunkIDs) == 0 || chunkRepo == nil {
		return content
	}

	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, chunkRepo, tenantID, textChunkIDs)
	mergedImageInfo := searchutil.MergeImageInfoJSON(imageInfoMap)
	if mergedImageInfo == "" {
		return content
	}
	return searchutil.EnrichContentWithImageInfo(content, mergedImageInfo)
}

// appendScannedPageText adds OCR from pages whose parent text is only an
// image link. reconstructEnrichedContent inlines image_info when that JSON
// exists; scanned pages often keep the recognized text only on the image_ocr
// chunk. A document that already has some prose still receives those pages.
// Disabled chunks, OCR beside real prose, and text already present are left out.
func appendScannedPageText(content string, chunks []*types.Chunk) string {
	var b strings.Builder
	b.WriteString(content)
	written := content
	for _, c := range scannedOCRForWiki(chunks) {
		text := strings.TrimSpace(c.Content)
		if text == "" || strings.Contains(written, text) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
		written = b.String()
	}
	return b.String()
}

// wikiCitationChunks gives the citation pass the scanned-page OCR on the
// parent text chunk. splitChunksIntoCitationBatches only reads text chunks,
// so an image-link parent would otherwise be cited without the page text.
// The returned chunks are copies; stored rows are not changed.
func wikiCitationChunks(chunks []*types.Chunk) []*types.Chunk {
	ocrByParent := map[string][]*types.Chunk{}
	var orphans []*types.Chunk
	for _, c := range scannedOCRForWiki(chunks) {
		if c.ParentChunkID == "" {
			orphans = append(orphans, c)
			continue
		}
		ocrByParent[c.ParentChunkID] = append(ocrByParent[c.ParentChunkID], c)
	}
	out := make([]*types.Chunk, 0, len(chunks)+len(orphans))
	for _, c := range chunks {
		if c == nil || !c.IsEnabled {
			continue
		}
		if c.ChunkType != types.ChunkTypeText && c.ChunkType != "" {
			continue
		}
		ocrs := ocrByParent[c.ID]
		if len(ocrs) == 0 || chunkHasExtractableText(c.Content) {
			out = append(out, c)
			continue
		}
		clone := *c
		var b strings.Builder
		b.WriteString(c.Content)
		for _, o := range ocrs {
			text := strings.TrimSpace(o.Content)
			if text == "" || strings.Contains(b.String(), text) {
				continue
			}
			b.WriteByte('\n')
			b.WriteString(text)
		}
		clone.Content = b.String()
		out = append(out, &clone)
	}
	for _, o := range orphans {
		clone := *o
		clone.ChunkType = types.ChunkTypeText
		out = append(out, &clone)
	}
	return out
}

// scannedOCRForWiki returns enabled OCR chunks whose parent is only an image
// link, ordered by the parent text chunk and then the PDF page.
func scannedOCRForWiki(chunks []*types.Chunk) []*types.Chunk {
	parentIndex := make(map[string]int, len(chunks))
	disabledParent := make(map[string]bool)
	for _, c := range chunks {
		if c == nil || (c.ChunkType != types.ChunkTypeText && c.ChunkType != "") {
			continue
		}
		parentIndex[c.ID] = c.ChunkIndex
		if !c.IsEnabled {
			disabledParent[c.ID] = true
		}
	}
	enabled := make([]*types.Chunk, 0, len(chunks))
	for _, c := range chunks {
		if c == nil || !c.IsEnabled || disabledParent[c.ParentChunkID] {
			continue
		}
		enabled = append(enabled, c)
	}
	var ocr []*types.Chunk
	for _, c := range selectGraphChunks(enabled) {
		if c != nil && c.ChunkType == types.ChunkTypeImageOCR {
			ocr = append(ocr, c)
		}
	}
	sort.SliceStable(ocr, func(i, j int) bool {
		pi, pj := parentIndex[ocr[i].ParentChunkID], parentIndex[ocr[j].ParentChunkID]
		if pi != pj {
			return pi < pj
		}
		pageI, pageJ := pdfPageOf(ocr[i]), pdfPageOf(ocr[j])
		if pageI != pageJ {
			return pageI < pageJ
		}
		if ocr[i].ChunkIndex != ocr[j].ChunkIndex {
			return ocr[i].ChunkIndex < ocr[j].ChunkIndex
		}
		return ocr[i].StartAt < ocr[j].StartAt
	})
	return ocr
}

func pdfPageOf(c *types.Chunk) int {
	if c == nil {
		return 0
	}
	for _, loc := range c.SourceLocators {
		if loc.Type == types.SourceLocatorPDF && loc.Page > 0 {
			return loc.Page
		}
	}
	return 0
}

// slugify creates a URL-friendly slug from a string
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '/' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		// Keep CJK characters
		if r >= 0x4E00 && r <= 0x9FFF {
			return r
		}
		return -1
	}, s)
	// Collapse multiple hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// appendUnique appends a string to a StringArray if not already present
func appendUnique(arr types.StringArray, s string) types.StringArray {
	for _, v := range arr {
		if v == s {
			return arr
		}
	}
	return append(arr, s)
}

// minTextContentRunes is the minimum number of non-whitespace, non-image-reference
// runes required for content to be considered substantive enough for LLM
// summarization or wiki extraction. Documents below this threshold (e.g. a
// scanned PDF where OCR yielded nothing AND no caption either) are routed to
// a deterministic empty-content fallback instead of being passed to the LLM,
// which would otherwise hallucinate based on metadata alone.
//
// The threshold is intentionally low: legitimate short documents (brief
// memos, single-line notes) must still pass. The goal is only to catch
// the empty-image-only case.
//
// Declared as a var (not const) so tests can override it and future config
// plumbing can adjust it at runtime without a rebuild.
var minTextContentRunes = 10

var (
	// Markdown image references like ![alt](path) — pure visual placeholders
	// with no extractable text, so the whole reference is removed.
	mdImageRefRE = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)

	// <image_original>...</image_original> blocks wrap the verbatim Markdown
	// image reference inside an enriched <image> block (see
	// searchutil.EnrichContentWithImageInfo). The content is just a redundant
	// copy of an already-stripped image link, so the whole block (tags +
	// content) is removed.
	imageOriginalBlockRE = regexp.MustCompile(`(?is)<image_original\b[^>]*>.*?</image_original>`)

	// Self-closing or attribute-only HTML <img> tags.
	htmlImgTagRE = regexp.MustCompile(`(?i)<img\b[^>]*/?>`)

	// Wrapper-style <image>, <images>, <image_caption>, <image_ocr> tags
	// (opening or closing). Matches ONLY the tag; the text content between
	// open and close tags is preserved. This is critical: VLM-generated OCR
	// and caption text live inside <image_ocr>...</image_ocr> and
	// <image_caption>...</image_caption> blocks, and stripping the content
	// would silently destroy the very text we want to keep.
	imageWrapperTagRE = regexp.MustCompile(`(?i)</?image[a-z_]*\b[^>]*/?>`)

	// Markdown image references with the URL captured separately so LLM-bound
	// image URLs can be frozen while captions remain editable.
	mdImageURLRE = regexp.MustCompile(`!\[[^\]]*\]\(([^)]*)\)`)

	// Enriched image blocks store the original object URL as an attribute,
	// e.g. <image url="...">. Capture both double- and single-quoted forms.
	imageURLAttrRE = regexp.MustCompile(`(?i)<image\b[^>]*\surl\s*=\s*(?:"([^"]*)"|'([^']*)')`)

	imagePlaceholderTokenRE = regexp.MustCompile(`(?i)wkimg\s*[:：/_\-]?\s*[0-9]{1,6}`)

	// looseImagePlaceholderTokenRE matches a whole token that the synthesis
	// model may have rewritten with a different separator (wkimg/0010,
	// wkimg_0010, wkimg-0010, "wkimg 0010", full-width colon). The numeric
	// suffix still identifies the image, so these are normalised back to the
	// canonical wkimg:0010 form before lookup.
	looseImagePlaceholderTokenRE = regexp.MustCompile(`(?i)^wkimg\s*[:：/_\-]?\s*([0-9]{1,6})$`)
)

func maskTemplateDataImageURLs(data map[string]string) (map[string]string, map[string]string) {
	if len(data) == 0 {
		return data, nil
	}

	masked := make(map[string]string, len(data))
	urlToToken := make(map[string]string)
	tokenToURL := make(map[string]string)

	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		masked[key] = maskImageURLsWithState(data[key], urlToToken, tokenToURL)
	}

	return masked, tokenToURL
}

// maskImageURLs replaces image URLs with low-entropy placeholders. It only
// freezes URLs; alt/caption text remains in place for the LLM to edit.
func maskImageURLs(s string) (string, map[string]string) {
	urlToToken := make(map[string]string)
	tokenToURL := make(map[string]string)
	return maskImageURLsWithState(s, urlToToken, tokenToURL), tokenToURL
}

func maskImageURLsWithState(s string, urlToToken, tokenToURL map[string]string) string {
	urls := collectMaskableImageURLs(s)
	if len(urls) == 0 {
		return s
	}

	for _, url := range urls {
		if _, ok := urlToToken[url]; ok {
			continue
		}
		token := fmt.Sprintf("wkimg:%04d", len(tokenToURL)+1)
		urlToToken[url] = token
		tokenToURL[token] = url
	}

	replaceURLs := append([]string(nil), urls...)
	sort.SliceStable(replaceURLs, func(i, j int) bool {
		return len(replaceURLs[i]) > len(replaceURLs[j])
	})

	masked := s
	for _, url := range replaceURLs {
		masked = strings.ReplaceAll(masked, url, urlToToken[url])
	}
	return masked
}

func collectMaskableImageURLs(s string) []string {
	seen := make(map[string]struct{})
	var urls []string

	addURL := func(url string) {
		url = strings.TrimSpace(url)
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}

	for _, match := range mdImageURLRE.FindAllStringSubmatch(s, -1) {
		addURL(match[1])
	}
	for _, match := range imageURLAttrRE.FindAllStringSubmatch(s, -1) {
		if match[1] != "" {
			addURL(match[1])
			continue
		}
		addURL(match[2])
	}

	return urls
}

// unmaskImageURLs restores known placeholders and drops any corrupted or
// invented image placeholders so broken image links never reach storage.
func unmaskImageURLs(out string, urlMap map[string]string) string {
	out = mdImageURLRE.ReplaceAllStringFunc(out, func(match string) string {
		parts := mdImageURLRE.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		url := strings.TrimSpace(parts[1])
		if realURL, ok := urlMap[url]; ok {
			idx := strings.LastIndex(match, "(")
			if idx < 0 {
				return match
			}
			return match[:idx+1] + realURL + ")"
		}
		if canonical, ok := canonicalImagePlaceholder(url); ok {
			realURL, known := urlMap[canonical]
			if !known {
				return ""
			}
			idx := strings.LastIndex(match, "(")
			if idx < 0 {
				return match
			}
			return match[:idx+1] + realURL + ")"
		}
		if strings.HasPrefix(strings.ToLower(url), "wkimg") {
			return ""
		}
		return match
	})

	return replaceImagePlaceholderTokensOutsideMarkdown(out, urlMap)
}

// canonicalImagePlaceholder normalises the separator variants a model may
// introduce and reports whether token is a whole image placeholder. Only the
// separator is normalised: the digits are preserved verbatim so an invented
// index such as wkimg:9999 does not accidentally resolve to a real image.
func canonicalImagePlaceholder(token string) (string, bool) {
	m := looseImagePlaceholderTokenRE.FindStringSubmatch(strings.TrimSpace(token))
	if m == nil {
		return "", false
	}
	return "wkimg:" + m[1], true
}

func replaceImagePlaceholderTokensOutsideMarkdown(s string, urlMap map[string]string) string {
	matches := mdImageURLRE.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return replaceImagePlaceholderTokens(s, urlMap)
	}

	var b strings.Builder
	last := 0
	for _, match := range matches {
		if match[0] > last {
			b.WriteString(replaceImagePlaceholderTokens(s[last:match[0]], urlMap))
		}
		b.WriteString(s[match[0]:match[1]])
		last = match[1]
	}
	if last < len(s) {
		b.WriteString(replaceImagePlaceholderTokens(s[last:], urlMap))
	}
	return b.String()
}

func replaceImagePlaceholderTokens(s string, urlMap map[string]string) string {
	return imagePlaceholderTokenRE.ReplaceAllStringFunc(s, func(token string) string {
		canonical, ok := canonicalImagePlaceholder(token)
		if !ok {
			return ""
		}
		if realURL, ok := urlMap[canonical]; ok {
			return realURL
		}
		return ""
	})
}

// stripImageMarkup removes image-only placeholders (Markdown image refs,
// <img> tags, <image_original> redundancy blocks) and unwraps the
// <image>/<image_caption>/<image_ocr> XML wrappers produced by the search
// enrichment layer, leaving any OCR or caption text as plain inline text.
//
// This shape matters: when VLM OCR succeeds on a scanned PDF page, the
// extracted text reaches downstream code wrapped in <image_ocr> tags inside
// an <image> block. A naive "strip the whole <image>...</image> block"
// approach would discard the OCR text — the exact opposite of what we want.
func stripImageMarkup(s string) string {
	s = imageOriginalBlockRE.ReplaceAllString(s, "")
	s = mdImageRefRE.ReplaceAllString(s, "")
	s = htmlImgTagRE.ReplaceAllString(s, "")
	s = imageWrapperTagRE.ReplaceAllString(s, "")
	return s
}

// extractRealText returns the trimmed content with image markup stripped.
// Cached at the call site for use both in the threshold check and in any
// subsequent log message, avoiding redundant regex passes over large docs.
func extractRealText(content string) string {
	return strings.TrimSpace(stripImageMarkup(content))
}

// hasSufficientTextContent reports whether the given content carries enough
// real text (after image markup is stripped, with OCR/caption text retained)
// to warrant an LLM call. It is the primary defence against filename-driven
// hallucinations on scanned PDFs that have NO usable text at all.
func hasSufficientTextContent(content string) bool {
	return realTextRuneCount(content) >= minTextContentRunes
}

// realTextRuneCount returns the rune length of the content after image
// markup is stripped. Uses utf8.RuneCountInString to avoid allocating a
// rune slice for the count.
func realTextRuneCount(content string) int {
	return utf8.RuneCountInString(extractRealText(content))
}

// cleanLLMJSON strips markdown code-fence wrappers and sanitizes control characters
// from LLM-generated JSON output so it can be safely unmarshalled.
func cleanLLMJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	return sanitizeJSONString(s)
}

// sanitizeJSONString sanitizes a string that is intended to be parsed as JSON,
// by properly escaping unescaped control characters (like newlines) inside string literals.
func sanitizeJSONString(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	inString := false
	escape := false
	for _, r := range s {
		if escape {
			switch r {
			case '\n':
				buf.WriteString(`n`)
			case '\r':
				buf.WriteString(`r`)
			case '\t':
				buf.WriteString(`t`)
			default:
				buf.WriteRune(r)
			}
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			buf.WriteRune(r)
			continue
		}
		if r == '"' {
			inString = !inString
			buf.WriteRune(r)
			continue
		}
		if inString {
			if r == '\n' {
				buf.WriteString(`\n`)
				continue
			}
			if r == '\r' {
				buf.WriteString(`\r`)
				continue
			}
			if r == '\t' {
				buf.WriteString(`\t`)
				continue
			}
		}
		buf.WriteRune(r)
	}
	return buf.String()
}
