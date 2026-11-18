package service

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/agent"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
)

// mapOneDocumentRecovered runs mapOneDocument and turns a panic into a map
// failure. errgroup does not recover panics, so one escaping a map worker
// crashed the process before the batch settled its claims; the op was
// claimed again later, hit the same panic, and its document never left
// "finalizing" (in Lite mode the startup recovery crash-looped the server).
// As a failure it goes through the fail_count budget and, at worst, the
// dead-letter path, which releases the document's slot.
func (s *wikiIngestService) mapOneDocumentRecovered(
	ctx context.Context,
	chatModel chat.Chat,
	payload WikiIngestPayload,
	op WikiPendingOp,
	batchCtx *WikiBatchContext,
) (result *docIngestResult, updates []SlugUpdate, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf(ctx, "wiki ingest: map panicked for knowledge %s: %v\n%s", op.KnowledgeID, r, debug.Stack())
			result, updates, err = nil, nil, fmt.Errorf("wiki map panicked: %v", r)
		}
	}()
	return s.mapOneDocument(ctx, chatModel, payload, op, batchCtx)
}

func (s *wikiIngestService) mapOneDocument(
	ctx context.Context,
	chatModel chat.Chat,
	payload WikiIngestPayload,
	op WikiPendingOp,
	batchCtx *WikiBatchContext,
) (*docIngestResult, []SlugUpdate, error) {
	docStartedAt := time.Now()
	knowledgeID := op.KnowledgeID
	lang := types.ResolveLanguageName(ctx, op.Language)

	// Open a postprocess.wiki subspan under the parent attempt's
	// postprocess stage so the actual per-doc work (LLM extraction +
	// summary + classification) shows up in the trace tree. Returns
	// nil when the parent attempt is gone (no panic on missing
	// lookups — span tracker is best-effort).
	wikiSpan := s.beginWikiSubspan(ctx, knowledgeID, types.JSONMap{
		"language":          lang,
		"knowledge_base_id": payload.KnowledgeBaseID,
	})

	// Guard against the ingest/delete race: if the user deleted the doc while
	// this task was queued (wikiIngestDelay = 30s) or while an earlier stage
	// was in flight, we must NOT proceed to LLM extraction — doing so would
	// create wiki pages whose source_refs point at a ghost knowledge ID,
	// permanently unreachable via wiki_read_source_doc.
	if _, ok := types.TenantIDFromContext(ctx); !ok && payload.TenantID != 0 {
		ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	}
	gone, err := s.isKnowledgeGone(ctx, payload.KnowledgeBaseID, knowledgeID)
	if err != nil {
		failCtx, cancel := wikiIngestCleanupContext(ctx)
		defer cancel()
		s.tracker().FailSpan(failCtx, wikiSpan, "KNOWLEDGE_LOOKUP_FAILED", err.Error(), err)
		return nil, nil, err
	}
	if gone {
		logger.Infof(ctx, "wiki ingest: knowledge %s has been deleted, skip map", knowledgeID)
		s.tracker().SkipSpan(ctx, wikiSpan, "knowledge_deleted")
		return nil, nil, nil
	}

	// Text chunks of a scanned PDF are image links. The readable prose lives on
	// image_ocr chunks and is not always copied into image_info, so load those
	// chunks and append the pages that are still missing.
	chunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, payload.TenantID, knowledgeID, []types.ChunkType{
		types.ChunkTypeText, types.ChunkTypeImageOCR,
	})
	if err != nil {
		s.tracker().FailSpan(ctx, wikiSpan, "LIST_CHUNKS_FAILED", err.Error(), err)
		return nil, nil, fmt.Errorf("get chunks: %w", err)
	}
	if len(chunks) == 0 {
		logger.Infof(ctx, "wiki ingest: document %s has no chunks, skip", knowledgeID)
		s.tracker().SkipSpan(ctx, wikiSpan, "no_chunks")
		return nil, nil, nil
	}

	content := appendScannedPageText(
		reconstructEnrichedContent(ctx, s.chunkRepo, payload.TenantID, chunks),
		chunks,
	)
	rawRuneCount := len([]rune(content))
	if len([]rune(content)) > maxContentForWiki {
		content = string([]rune(content)[:maxContentForWiki])
	}
	logger.Infof(ctx, "wiki ingest: doc %s chunks=%d content_len(raw=%d,truncated=%d)", knowledgeID, len(chunks),
		rawRuneCount, len([]rune(content)))

	// Refuse to run LLM-based extraction when the document carries no real
	// text — e.g. a scanned PDF whose pages were converted to images but where
	// VLM OCR produced nothing usable. Without this guard the LLM would have
	// only image markup left and would happily fabricate entities/concepts.
	if !hasSufficientTextContent(content) {
		logger.Warnf(ctx,
			"wiki ingest: doc %s has insufficient text content after stripping image markup (raw_len=%d), "+
				"skipping LLM extraction",
			knowledgeID, rawRuneCount,
		)
		s.tracker().SkipSpan(ctx, wikiSpan, "insufficient_text_content")
		return nil, nil, nil
	}

	docTitle := knowledgeID
	if kn, err := s.knowledgeSvc.GetKnowledgeByIDOnly(ctx, knowledgeID); err == nil && kn != nil && kn.Title != "" &&
		(payload.TenantID == 0 || kn.TenantID == 0 || kn.TenantID == payload.TenantID) &&
		(payload.KnowledgeBaseID == "" || kn.KnowledgeBaseID == "" || kn.KnowledgeBaseID == payload.KnowledgeBaseID) {
		docTitle = kn.Title
	} else {
		for _, ch := range chunks {
			if ch.Content != "" {
				lines := strings.SplitN(ch.Content, "\n", 2)
				if len(lines) > 0 && len(lines[0]) > 0 && len(lines[0]) < 200 {
					docTitle = strings.TrimPrefix(strings.TrimSpace(lines[0]), "# ")
					break
				}
			}
		}
	}

	// Citation source reference. We deliberately use only the knowledge ID
	// (not docTitle, which is typically the upload filename) so the filename
	// does not leak into citation strings that downstream LLM prompts may
	// surface during wiki page editing.
	sourceRef := knowledgeID
	oldPageSlugs := s.getExistingPageSlugsForKnowledge(ctx, payload.KnowledgeBaseID, knowledgeID)

	// Pass 0: lightweight candidate slug extraction (skeleton only).
	// On failure we fall back to the legacy single-shot extractor so the doc
	// still gets ingested, just without chunk-level citations.
	var (
		extractedEntities []extractedItem
		extractedConcepts []extractedItem
		slugItems         map[string]extractedItem
		pass0Failed       bool
	)
	logger.Infof(ctx, "wiki ingest: pass 0 — extracting candidate slugs for %s", knowledgeID)
	extractSpan := s.tracker().BeginSubSpan(ctx, wikiSpan, "postprocess.wiki.extract", types.SpanKindSubSpan,
		types.JSONMap{
			"content_chars": utf8.RuneCountInString(content),
			"old_pages":     len(oldPageSlugs),
		})
	extractedEntities, extractedConcepts, slugItems, err = s.extractCandidateSlugs(ctx, chatModel,
		payload.KnowledgeBaseID, content, lang, oldPageSlugs, batchCtx)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: pass 0 failed for %s (%v) — falling back to legacy extractor", knowledgeID, err)
		pass0Failed = true
		extractedEntities, extractedConcepts, slugItems, err = s.extractEntitiesAndConceptsNoUpsert(ctx, chatModel,
			payload.KnowledgeBaseID, content, lang, oldPageSlugs, batchCtx)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: legacy fallback also failed for %s: %v", knowledgeID, err)
			s.tracker().FailSpan(ctx, extractSpan, "EXTRACT_FAILED", err.Error(), err)
			s.tracker().FailSpan(ctx, wikiSpan, "EXTRACT_FAILED", err.Error(), err)
			return nil, nil, err
		}
	}
	s.tracker().EndSpan(ctx, extractSpan, types.JSONMap{
		"entities":         len(extractedEntities),
		"concepts":         len(extractedConcepts),
		"pass0_fallback":   pass0Failed,
		"entities_preview": previewExtractedItems(extractedEntities, 8),
		"concepts_preview": previewExtractedItems(extractedConcepts, 8),
	})

	// Build slug listing for Summary's wiki-link input.
	var summaryExtractedPages []string
	for slug := range slugItems {
		summaryExtractedPages = append(summaryExtractedPages, slug)
	}
	// Wiki summary slug is derived from the knowledge ID rather than the
	// docTitle (which is typically the upload filename). Filename-based slugs
	// like "summary/mx5280-pdf" expose the filename in cross-link contexts
	// that downstream LLM prompts read; a UUID-based slug is uglier but
	// hallucination-safe.
	summarySlug := fmt.Sprintf("summary/%s", slugify(knowledgeID))
	var slugListing string
	for _, slug := range summaryExtractedPages {
		if item, ok := slugItems[slug]; ok {
			aliases := ""
			if len(item.Aliases) > 0 {
				aliases = fmt.Sprintf(" (Aliases: %s)", strings.Join(item.Aliases, ", "))
			}
			slugListing += fmt.Sprintf("- [[%s]] = %s%s\n", slug, item.Name, aliases)
		} else {
			slugListing += fmt.Sprintf("- [[%s]]\n", slug)
		}
	}

	// Summary and chunk classification are independent given Pass 0 output —
	// run them in parallel. Summary handles wiki-link injection; classification
	// attaches concrete chunk IDs to each candidate slug.
	var (
		summaryContent string
		summaryErr     error
		citations      map[string][]string
		newSlugs       []newSlugFromCitation
		batchCount     int
	)

	// Both calls run in parallel goroutines under the same wikiSpan
	// parent — their subspans will visually overlap in the trace view,
	// which correctly reflects their wall-clock concurrency.
	summarySpan := s.tracker().BeginSubSpan(ctx, wikiSpan, "postprocess.wiki.summary", types.SpanKindSubSpan,
		types.JSONMap{
			"content_chars":   utf8.RuneCountInString(content),
			"extracted_slugs": len(summaryExtractedPages),
		})
	var classifySpan *Span
	if !pass0Failed {
		classifySpan = s.tracker().BeginSubSpan(ctx, wikiSpan, "postprocess.wiki.classify", types.SpanKindSubSpan,
			types.JSONMap{
				"chunks":     len(chunks),
				"candidates": len(extractedEntities) + len(extractedConcepts),
			})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		summaryContent, summaryErr = s.generateWithTemplate(ctx, chatModel, agent.WikiSummaryPrompt, map[string]string{
			"Content":            content,
			"Language":           lang,
			"ExtractedSlugs":     slugListing,
			"CustomInstructions": batchCtx.ContentInstructions,
			"InstructionScope":   "wiki_content",
		})
		if summaryErr != nil {
			s.tracker().FailSpan(ctx, summarySpan, "SUMMARY_FAILED", summaryErr.Error(), summaryErr)
		} else {
			sumLine, sumBody := splitSummaryLine(summaryContent)
			s.tracker().EndSpan(ctx, summarySpan, types.JSONMap{
				"chars":        utf8.RuneCountInString(summaryContent),
				"summary_line": previewText(sumLine, 160),
				"body_preview": previewText(sumBody, 320),
			})
		}
	}()
	go func() {
		defer wg.Done()
		// Skip citation pass when Pass 0 has fallen back to the legacy path —
		// the legacy output already contains paraphrased Details, so chunk
		// citations would be redundant and we'd spend LLM calls for nothing.
		if pass0Failed {
			citations = map[string][]string{}
			return
		}
		candidatesXML := renderCandidateSlugsXML(extractedEntities, extractedConcepts)
		citations, newSlugs, batchCount = s.classifyChunkCitations(
			ctx, chatModel, candidatesXML, wikiCitationChunks(chunks), lang, batchCtx,
		)
		s.tracker().EndSpan(ctx, classifySpan, types.JSONMap{
			"cited_slugs":      len(citations),
			"new_slugs":        len(newSlugs),
			"batches":          batchCount,
			"top_cited":        topCitedSlugs(citations, 8),
			"new_slugs_sample": previewNewSlugs(newSlugs, 8),
		})
	}()
	wg.Wait()

	// Merge citations back into the item structs (non-failing; items without
	// citations simply keep their Description+Details fallback).
	var uncited int
	extractedEntities, extractedConcepts, uncited = mergeCitationsIntoItems(extractedEntities, extractedConcepts,
		citations, newSlugs)
	extractedEntities, extractedConcepts = s.reclaimExtractedIdentities(
		ctx, payload.KnowledgeBaseID, extractedEntities, extractedConcepts, batchCtx,
	)

	// Rebuild slugItems so stale entries (for slugs that did not survive the
	// merge) and brand-new slugs discovered by the citation pass are both
	// reflected in summaryExtractedPages tracking.
	slugItems = make(map[string]extractedItem, len(extractedEntities)+len(extractedConcepts))
	for _, item := range extractedEntities {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	for _, item := range extractedConcepts {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}

	// extractedPages records every wiki page this document materialized
	// (entities, concepts, plus the summary page appended below). The
	// slug is used for link/retract bookkeeping; the title is retained for
	// trace output and finalize processing.
	extractedPages := make([]wikiIngestPageRef, 0, len(slugItems)+1)
	for slug, item := range slugItems {
		title := item.Name
		if title == "" {
			title = slug
		}
		extractedPages = append(extractedPages, wikiIngestPageRef{Slug: slug, Title: title})
	}

	// Count total distinct chunks cited across all slugs for logging.
	citedChunkSet := make(map[string]bool)
	for _, ids := range citations {
		for _, id := range ids {
			citedChunkSet[id] = true
		}
	}

	var updates []SlugUpdate
	// docSummaryLine is the one-sentence headline used for terse log/audit
	// previews and for <document_added> blocks in retract prompts.
	// docSummary is the full summary body attached to each entity/concept
	// update so the editor model gets rich framing in <source_context>.
	var docSummaryLine string
	var docSummary string

	if summaryErr != nil {
		// Summary is the headline artifact of an ingested document — a
		// document with no summary page is half-ingested and leaves the
		// entity/concept updates hanging without a root to link back to
		// from the index. Historically we just logged and moved on,
		// which meant a single transient 504 permanently dropped the
		// summary page for that document.
		//
		// Returning an error here sends the op to failedOps (see the
		// map-phase loop in ProcessWikiIngest), which requeueFailedOps
		// appends back onto the pending list so the next batch retries.
		// The internal retries in generateWithTemplate already exhaust
		// the LLM's own transient-error budget before we give up here.
		logger.Errorf(ctx, "wiki ingest: generate summary failed for %s, will requeue: %v", knowledgeID, summaryErr)
		s.tracker().FailSpan(ctx, wikiSpan, "SUMMARY_FAILED", summaryErr.Error(), summaryErr)
		return nil, nil, fmt.Errorf("generate summary: %w", summaryErr)
	}
	sumLine, sumBody := splitSummaryLine(summaryContent)
	if sumBody == "" {
		sumBody = summaryContent
	}
	if sumLine == "" {
		sumLine = docTitle
	}
	docSummaryLine = sumLine
	docSummary = sumBody
	if strings.TrimSpace(docSummary) == "" {
		docSummary = sumLine
	}
	updates = append(updates, SlugUpdate{
		Slug:        summarySlug,
		Type:        types.WikiPageTypeSummary,
		DocTitle:    docTitle,
		KnowledgeID: knowledgeID,
		SourceRef:   sourceRef,
		Language:    lang,
		SummaryLine: sumLine,
		SummaryBody: sumBody,
	})
	extractedPages = append(extractedPages, wikiIngestPageRef{Slug: summarySlug, Title: docTitle})

	// Entities
	for _, item := range extractedEntities {
		if item.Slug != "" {
			updates = append(updates, SlugUpdate{
				Slug:         item.Slug,
				Type:         types.WikiPageTypeEntity,
				Item:         item,
				DocTitle:     docTitle,
				KnowledgeID:  knowledgeID,
				SourceRef:    sourceRef,
				Language:     lang,
				SourceChunks: item.SourceChunks,
				DocSummary:   docSummary,
			})
		}
	}

	// Concepts
	for _, item := range extractedConcepts {
		if item.Slug != "" {
			updates = append(updates, SlugUpdate{
				Slug:         item.Slug,
				Type:         types.WikiPageTypeConcept,
				Item:         item,
				DocTitle:     docTitle,
				KnowledgeID:  knowledgeID,
				SourceRef:    sourceRef,
				Language:     lang,
				SourceChunks: item.SourceChunks,
				DocSummary:   docSummary,
			})
		}
	}

	// Reconcile old page set against new extraction.
	//
	// Three cases:
	//
	//  (a) oldSlug ∉ new → preserve the page and its source refs. Extraction
	//      can omit previously produced slugs even when the document still
	//      supports them. Only actual document deletion should remove their
	//      source refs; cleanupWikiOnKnowledgeDelete handles that path.
	//
	//  (b) oldSlug ∈ new AND slug is an entity/concept page  → reparse
	//      swap: emit BOTH a "retract" (carrying the doc's PRIOR summary
	//      body as the old-version signal) AND the normal addition. The
	//      reduce stage sees HasAdditions=1 + HasRetractions=1 and the
	//      WikiPageModifyUserPrompt correctly tells the editor model to
	//      remove the old K section and add the new K section in one
	//      pass — giving us replace-not-append semantics that "append
	//      new K on top of old K" would otherwise violate.
	//
	//  (c) oldSlug ∈ new AND slug is a summary page (summary/...) →
	//      nothing to do here. reduceSlugUpdates' summary branch
	//      unconditionally overwrites the whole page from the new
	//      SummaryBody, so emitting an extra retract would just be
	//      dead weight that the summary branch discards anyway.
	//
	// priorContribution is the doc's LAST summary body, fetched lazily
	// at this point (rather than pre-loaded into the batch context).
	// Empty on first-ever ingest — in that case oldPageSlugs is also
	// empty, so we never consult it.
	priorContribution := batchCtx.SummaryContentByKnowledgeID(ctx, knowledgeID)

	newSlugSet := make(map[string]bool, len(extractedPages))
	for _, ns := range extractedPages {
		newSlugSet[ns.Slug] = true
	}

	var reparseOverlap, preservedCount int
	for oldSlug := range oldPageSlugs {
		if newSlugSet[oldSlug] {
			// Skip summary slugs — they're overwritten wholesale by the
			// summary update, retract would be ignored downstream.
			if strings.HasPrefix(oldSlug, "summary/") {
				continue
			}
			reparseOverlap++
			updates = append(updates, SlugUpdate{
				Slug:              oldSlug,
				Type:              "retract",
				RetractDocContent: priorContribution,
				DocTitle:          docTitle,
				KnowledgeID:       knowledgeID,
				Language:          lang,
			})
			continue
		}
		preservedCount++
	}

	logger.Infof(ctx,
		"wiki ingest: mapped knowledge %s title=%q candidates=%d chunks=%d batches=%d "+
			"cited_chunks=%d uncited_slugs=%d new_slugs=%d updates=%d reparse_slugs=%d "+
			"preserved_slugs=%d pass0_fallback=%v elapsed=%s",
		knowledgeID, previewText(docTitle, 80),
		len(slugItems), len(chunks), batchCount, len(citedChunkSet), uncited, len(newSlugs),
		len(updates), reparseOverlap, preservedCount, pass0Failed,
		time.Since(docStartedAt).Round(time.Millisecond),
	)

	// Map-phase metrics get attached to the postprocess.wiki span's
	// output, but we do NOT EndSpan here — the batch driver keeps the
	// span open through reduce + index rebuild + cross-link injection
	// + page publish, then closes it once this doc's pages have all
	// been written. That way the span's duration reflects the full
	// "wiki processing for this knowledge" time the user sees in the
	// trace viewer, not just the LLM extraction slice.
	mapStats := types.JSONMap{
		"doc_title":        previewText(docTitle, 120),
		"chunks":           len(chunks),
		"candidate_slugs":  len(slugItems),
		"cited_chunks":     len(citedChunkSet),
		"uncited_slugs":    uncited,
		"new_slugs":        len(newSlugs),
		"updates":          len(updates),
		"reparse_slugs":    reparseOverlap,
		"preserved_slugs":  preservedCount,
		"extracted_pages":  len(extractedPages),
		"summary_chars":    utf8.RuneCountInString(docSummary),
		"pass0_fallback":   pass0Failed,
		"classify_batches": batchCount,
		"summary_preview":  previewText(docSummaryLine, 160),
	}

	return &docIngestResult{
		KnowledgeID: knowledgeID,
		DocTitle:    docTitle,
		Summary:     docSummaryLine,
		Pages:       extractedPages,
		MapStats:    mapStats,
		WikiSpan:    wikiSpan,
	}, updates, nil
}

func (s *wikiIngestService) extractEntitiesAndConceptsNoUpsert(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	content, lang string,
	oldPageSlugs map[string]bool,
	batchCtx *WikiBatchContext,
) ([]extractedItem, []extractedItem, map[string]extractedItem, error) {
	// Only entity/* and concept/* slugs are relevant for LLM slug-continuity —
	// summary slugs are code-generated from the knowledge ID and never appear
	// in the extraction output, so including them just wastes tokens and risks
	// confusing the model.
	var prevSlugsText string
	if len(oldPageSlugs) > 0 {
		var sb strings.Builder
		for slug := range oldPageSlugs {
			if !strings.HasPrefix(slug, "entity/") && !strings.HasPrefix(slug, "concept/") {
				continue
			}
			fmt.Fprintf(&sb, "- %s\n", slug)
		}
		prevSlugsText = sb.String()
	}
	if prevSlugsText == "" {
		prevSlugsText = "(none — this is a new document)"
	}

	extractionJSON, err := s.generateWithTemplate(ctx, chatModel, agent.WikiKnowledgeExtractPrompt, map[string]string{
		"Content":            content,
		"Language":           lang,
		"PreviousSlugs":      prevSlugsText,
		"CustomInstructions": batchCtx.ExtractionInstructions,
		"InstructionScope":   "wiki_extraction",
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("combined extraction failed: %w", err)
	}

	extractionJSON = cleanLLMJSON(extractionJSON)

	var result combinedExtraction
	if err := json.Unmarshal([]byte(extractionJSON), &result); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to parse combined extraction JSON: %v\nRaw: %s", err, extractionJSON)
		return nil, nil, nil, fmt.Errorf("parse combined extraction JSON: %w", err)
	}

	// Dedup pre-filter is dispatched against the wiki page repo via
	// pg_trgm (see deduplicateExtractedBatch). Until the trgm path
	// lands the dedup pre-filter degrades to "no dedup" which is the
	// safe default — the LLM merge call simply doesn't get a candidate
	// list and the items pass through unchanged.
	result.Entities, result.Concepts = s.deduplicateExtractedBatch(
		ctx, chatModel, kbID, result.Entities, result.Concepts, batchCtx,
	)

	slugItems := make(map[string]extractedItem)
	for _, item := range result.Entities {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	for _, item := range result.Concepts {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}

	return result.Entities, result.Concepts, slugItems, nil
}

// newWikiBatchContext builds the per-run lazy fetchers used by both the ingest
// batch and the debounced finalize task.
func (s *wikiIngestService) newWikiBatchContext(
	kbID string,
	wikiConfig *types.WikiConfig,
) *WikiBatchContext {
	var (
		fetchMu         sync.Mutex
		slugTitleCache  = make(map[string]string) // slug -> title; "" = known-missing
		summaryKIDCache = make(map[string]string) // kid -> content; "" = known-missing
	)

	resolveSlugs := func(ctx context.Context, slugs []string) map[string]string {
		fetchMu.Lock()
		need := slugs[:0:0]
		for _, slug := range slugs {
			if _, ok := slugTitleCache[slug]; ok {
				continue
			}
			need = append(need, slug)
		}
		fetchMu.Unlock()

		if len(need) > 0 {
			pages, err := s.wikiService.ListBySlugs(ctx, kbID, need)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: ListBySlugs(%d slugs) failed: %v", len(need), err)
			}
			fetchMu.Lock()
			for _, slug := range need {
				if p, ok := pages[slug]; ok && p != nil {
					if p.Status == types.WikiPageStatusArchived || p.PageType == types.WikiPageTypeIndex {
						slugTitleCache[slug] = ""
						continue
					}
					slugTitleCache[slug] = p.Title
				} else {
					slugTitleCache[slug] = ""
				}
			}
			fetchMu.Unlock()
		}

		out := make(map[string]string, len(slugs))
		fetchMu.Lock()
		for _, slug := range slugs {
			if title := slugTitleCache[slug]; title != "" {
				out[slug] = title
			}
		}
		fetchMu.Unlock()
		return out
	}

	resolveSummaries := func(ctx context.Context, kids []string) map[string]string {
		fetchMu.Lock()
		need := kids[:0:0]
		for _, kid := range kids {
			if _, ok := summaryKIDCache[kid]; ok {
				continue
			}
			need = append(need, kid)
		}
		fetchMu.Unlock()

		if len(need) > 0 {
			contents, err := s.wikiService.ListSummariesByKnowledgeIDs(ctx, kbID, need)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: ListSummariesByKnowledgeIDs(%d kids) failed: %v", len(need), err)
			}
			fetchMu.Lock()
			for _, kid := range need {
				if c, ok := contents[kid]; ok && c != "" {
					summaryKIDCache[kid] = c
				} else {
					summaryKIDCache[kid] = ""
				}
			}
			fetchMu.Unlock()
		}

		out := make(map[string]string, len(kids))
		fetchMu.Lock()
		for _, kid := range kids {
			if content := summaryKIDCache[kid]; content != "" {
				out[kid] = content
			}
		}
		fetchMu.Unlock()
		return out
	}

	granularity := types.WikiExtractionStandard
	contentInstructions := ""
	extractionInstructions := ""
	if wikiConfig != nil {
		granularity = wikiConfig.ExtractionGranularity.Normalize()
		contentInstructions = wikiConfig.ContentInstructions
		extractionInstructions = wikiConfig.ExtractionInstructions
	}
	return &WikiBatchContext{
		SlugTitle: func(ctx context.Context, slug string) string {
			m := resolveSlugs(ctx, []string{slug})
			return m[slug]
		},
		SlugTitleMany: resolveSlugs,
		SummaryContentByKnowledgeID: func(ctx context.Context, kid string) string {
			m := resolveSummaries(ctx, []string{kid})
			return m[kid]
		},
		ExtractionGranularity:  granularity,
		ContentInstructions:    contentInstructions,
		ExtractionInstructions: extractionInstructions,
	}
}
