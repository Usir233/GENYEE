package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Usir233/GENYEE/internal/agent"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
)

// docIngestResult captures per-document info for batch post-processing.
type wikiIngestPageRef struct {
	Slug  string
	Title string
}

type docIngestResult struct {
	KnowledgeID string
	DocTitle    string
	Summary     string // one-line summary of the document (from summary page)
	// Pages records the wiki pages this document touched, carrying both
	// the slug used for link/retract bookkeeping and its human-readable title.
	Pages []wikiIngestPageRef
	// MapStats are the per-doc map-phase metrics captured at the moment
	// mapOneDocument finishes. Surfaced into the postprocess.wiki span's
	// output so the trace viewer can show "what the map phase produced"
	// even though the span itself stays open until the batch's reduce +
	// cleanup phases complete (so the user-visible duration covers the
	// whole pipeline for this doc, not just LLM extraction).
	MapStats types.JSONMap
	// WikiSpan is the postprocess.wiki subspan opened at the start of
	// mapOneDocument. ProcessWikiIngest holds it open across the reduce
	// + cleanup phases and closes it once this doc's pages have all
	// been materialised — see the EndSpan call near the end of
	// ProcessWikiIngest. nil when no parent attempt was found, in which
	// case the tracker helpers are all no-ops anyway.
	WikiSpan *Span
}

// WikiBatchContext holds shared data across Map and Reduce phases.
//
// Historically this carried a fully materialized `AllPages` slice plus
// pre-built SlugTitleMap / SummaryContentByKnowledgeID lookup tables.
// At 4w-document scale that meant the very first thing every batch
// did was load 100K+ wiki_pages rows (content TEXT included) into Go
// memory — and then walk them several more times for cleanDeadLinks /
// injectCrossLinks / getExistingPageSlugsForKnowledge.
//
// We now lazy-load via fetchers backed by lightweight projections
// (ListBySlugs / ListSummariesByKnowledgeIDs). Each fetcher caches
// results keyed by its input so repeat lookups within a batch are
// free; the cache is per-batch and goroutine-local-via-mutex (sync.Map
// would also work but mutex keeps the surface small).
type WikiBatchContext struct {
	// SlugTitle resolves a slug to its current title (or "" if missing).
	// Backed by ListBySlugs; cache is populated as callers ask, so we
	// only pay for the slugs we actually look at.
	SlugTitle func(ctx context.Context, slug string) string

	// SlugTitleMany batches a slug-set into a single ListBySlugs query
	// and returns the resolved titles map. Convenient when a caller
	// already has the full slug list; results are still cached.
	SlugTitleMany func(ctx context.Context, slugs []string) map[string]string

	// SummaryContentByKnowledgeID returns the surviving summary page's
	// content for the given knowledge id (or "" if no summary page
	// exists / was archived). Backed by ListSummariesByKnowledgeIDs;
	// cache is populated lazily as well.
	SummaryContentByKnowledgeID func(ctx context.Context, kid string) string

	// ExtractionGranularity drives Pass 0 (candidate slug extraction)
	// aggressiveness. Resolved once per batch from the KnowledgeBase's
	// WikiConfig so every doc in the batch sees the same scope rules.
	// Already Normalize()'d — consumers can assume it is one of the
	// three valid values.
	ExtractionGranularity types.WikiExtractionGranularity

	// ContentInstructions and ExtractionInstructions are KB-scoped business
	// guidance. Stable citation, merge, taxonomy and JSON rules remain in the
	// system templates and cannot be replaced by these fields.
	ContentInstructions    string
	ExtractionInstructions string

	// PlannedFolderID holds the per-slug wiki_folders.id assigned by the batch
	// taxonomy planning pass (planBatchTaxonomy + folder resolution), keyed by
	// page slug. Reduce applies it only to pages that aren't already filed
	// (FolderID == ""), so the whole batch lands on one coherent tree without
	// churning user-curated placements. The folders themselves are created
	// sequentially before reduce, so the parallel reduce phase only assigns
	// pre-resolved ids and never races on folder creation. Read-only during
	// reduce.
	PlannedFolderID map[string]string

	// identityClaims is the Lite-mode and Redis-error fallback for identity
	// reservations. Map workers in one batch run concurrently even though Lite
	// serializes batches, so they still need to converge before Reduce groups
	// updates by slug. The map is batch-scoped and disappears with the batch.
	identityClaims sync.Map

	// identityPages memoizes exact title lookups for this batch so concurrent
	// map workers probing the same (page type, normalized title) share one DB
	// round-trip. Values are []*types.WikiPageLite, including empty slices
	// for confirmed misses.
	identityPages sync.Map
}

// SlugUpdate represents a single update operation for a specific slug
type SlugUpdate struct {
	Slug        string
	Type        string        // "entity", "concept", "summary", "retract", "retractStale"
	Item        extractedItem // For entity/concept
	DocTitle    string
	KnowledgeID string
	SourceRef   string
	// Language is the already-resolved, human-readable language name the
	// Reduce phase interpolates into the editor prompt (e.g. "Chinese
	// (Simplified)"), NOT a locale code. Map resolves it once per document
	// so every page derived from that document shares one language.
	Language          string
	SummaryBody       string // For summary
	SummaryLine       string // For summary
	RetractDocContent string // For retract / retractStale
	// SourceChunks lists the chunk IDs (within KnowledgeID) that substantively
	// support this update. Mirrors Item.SourceChunks for convenience — the
	// Reduce phase reads from here to avoid an extra field hop.
	SourceChunks []string
	// DocSummary is the document-level summary body produced by
	// WikiSummaryPrompt (everything after the SUMMARY: ... headline, falling
	// back to the raw output if no headline could be parsed out). Carried
	// here so the Reduce phase can frame cited chunks with a rich
	// <source_context> block that tells the editor model what the document
	// is about AND what kind of document it is (resume vs announcement vs
	// product page). The one-line headline alone was too terse to keep the
	// editor grounded on longer / multi-topic source documents.
	DocSummary string
}

func previewText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	r := []rune(s)
	if maxRunes <= 0 || len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "...(truncated)"
}

func previewStringSlice(items []string, limit int) string {
	if len(items) == 0 {
		return "[]"
	}
	if limit <= 0 {
		limit = 1
	}
	n := len(items)
	if n > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, previewText(it, 48))
	}
	if n > limit {
		return fmt.Sprintf("[%s ...(+%d)]", strings.Join(out, ", "), n-limit)
	}
	return fmt.Sprintf("[%s]", strings.Join(out, ", "))
}

// previewExtractedItems returns a JSON-friendly preview of the first
// `limit` extracted entities or concepts so the trace viewer's
// postprocess.wiki.extract span shows actual names/slugs/descriptions
// instead of bare counts. Each item is trimmed to a small fixed
// budget — these end up serialised into the spans table's JSONB
// output column, so the cumulative size matters more than per-item
// fidelity.
func previewExtractedItems(items []extractedItem, limit int) []map[string]string {
	if limit <= 0 {
		limit = 1
	}
	n := len(items)
	if n > limit {
		items = items[:limit]
	}
	out := make([]map[string]string, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]string{
			"name":        previewText(it.Name, 60),
			"slug":        it.Slug,
			"description": previewText(it.Description, 120),
		})
	}
	return out
}

// topCitedSlugs returns the top `limit` slugs by chunk-citation count.
// Used by postprocess.wiki.classify so the trace surfaces which
// candidate slugs the citation pass attached the most chunks to —
// useful when triaging "this LLM run extracted weird things" without
// having to open and diff full chunk lists.
func topCitedSlugs(citations map[string][]string, limit int) []map[string]any {
	if len(citations) == 0 {
		return nil
	}
	type entry struct {
		slug  string
		count int
	}
	entries := make([]entry, 0, len(citations))
	for slug, ids := range citations {
		entries = append(entries, entry{slug: slug, count: len(ids)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].slug < entries[j].slug
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"slug":   e.slug,
			"chunks": e.count,
		})
	}
	return out
}

// previewNewSlugs returns a JSON-friendly preview of the first
// `limit` slugs that the citation pass discovered (i.e. did not appear
// in pass-0's candidate list). Surfacing these makes "the citation
// LLM kept inventing entries" trivially diagnosable from the trace
// viewer.
func previewNewSlugs(items []newSlugFromCitation, limit int) []map[string]string {
	if limit <= 0 {
		limit = 1
	}
	n := len(items)
	if n > limit {
		items = items[:limit]
	}
	out := make([]map[string]string, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]string{
			"name":   previewText(it.Name, 60),
			"slug":   it.Slug,
			"type":   it.Type,
			"chunks": fmt.Sprintf("%d", len(it.SourceChunks)),
		})
	}
	return out
}

// wikiLinkRE matches `[[slug]]` and `[[slug|display text]]` references
// inside wiki page content. The slug capture group rejects whitespace and
// the closing-bracket / pipe characters so we don't accidentally swallow
// adjacent text. Display text (group 2) is optional.
var wikiLinkRE = regexp.MustCompile(`\[\[([^\[\]\|\s]+)(?:\|([^\]]+))?\]\]`)

// sanitizeDeadSummaryLinks rewrites the summary pages produced by THIS
// batch to fix `[[slug]]` / `[[slug|display]]` references that point
// at slugs whose entity/concept page generation failed in reduce.
//
// Background: WikiSummaryPrompt instructs the LLM to embed wiki links
// for every extracted slug it knows about, but slug extraction happens
// during map (parallel with summary generation) and the actual page
// creation happens later in reduce. When reduce's WikiPageModifyUserPrompt
// fails on an entity/concept slug the page never gets written — and
// the already-persisted summary is left holding a `[[entity/foo|name]]`
// link that 404s.
//
// We pass the batch's affected-slug set + the SlugTitleMany fetcher
// to the resolver so that LLM-mangled slugs (e.g. extra pinyin hyphens
// in "shang-hai-tower" vs "shanghai-tower") are healed in place rather
// than stripped to plain text — preserving cross-link information
// whenever the display text or surface form unambiguously identifies a
// live page.
//
// Pure text replacement, no LLM call. Scoped to the doc-summary slugs
// in this batch (`summary/<slugify(knowledgeID)>`), keeping the work
// proportional to batch size.
func (s *wikiIngestService) sanitizeDeadSummaryLinks(
	ctx context.Context,
	kbID string,
	docResults []*docIngestResult,
	failedSlugs map[string]struct{},
	batchCtx *WikiBatchContext,
) {
	if len(failedSlugs) == 0 || len(docResults) == 0 {
		return
	}
	// Build a (live-slug-set, title->slug) pair the resolver can consult.
	// We seed liveSlugs from batchCtx (the slugs that DID make it into
	// pages this batch) and expand it lazily as needed via SlugTitleMany.
	// titleToSlug is filled with the same successful pages' titles so the
	// display-text reverse lookup works on first try.
	for _, r := range docResults {
		if r == nil || r.KnowledgeID == "" {
			continue
		}
		summarySlug := "summary/" + slugify(r.KnowledgeID)
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, summarySlug)
		if err != nil || page == nil {
			continue
		}

		// Collect the slugs this summary actually links to (so the
		// resolver has a non-empty pool of candidates), plus all the
		// successfully-written sibling pages from the same doc. These
		// two sets together cover the LLM-vs-actual mismatch cases
		// without paying for a full ListAll scan.
		candidateSlugs := make(map[string]struct{}, len(page.OutLinks)+len(r.Pages))
		for _, slug := range page.OutLinks {
			candidateSlugs[slug] = struct{}{}
		}
		for _, ref := range r.Pages {
			if _, bad := failedSlugs[ref.Slug]; bad {
				continue
			}
			candidateSlugs[ref.Slug] = struct{}{}
		}
		liveSlugs, titleToSlug := s.resolveLiveSlugs(ctx, batchCtx, candidateSlugs)

		newContent, changed := stripDeadWikiLinks(page.Content, failedSlugs, liveSlugs, titleToSlug)
		if !changed {
			continue
		}
		page.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to sanitize dead links in summary %s: %v", summarySlug, err)
			continue
		}
		logger.Infof(ctx, "wiki ingest: sanitized dead [[slug]] refs in summary %s", summarySlug)
	}
}

// resolveLiveSlugs builds the (liveSlugs, titleToSlug) pair that
// stripDeadWikiLinks / cleanDeadLinks pass into resolveDeadSlug.
//
// We start from a caller-supplied candidate set (typically the page's
// own out-links + this batch's freshly-written slugs) and ask the
// batch's SlugTitleMany fetcher to resolve them in one batched query.
// The fetcher already filters out archived / system pages, so missing
// entries naturally translate to "not live" without an extra check.
//
// titleToSlug is keyed by the page's exact title only — we don't have
// aliases in the lite projection. That's an acceptable trade-off: the
// reported breakage pattern is "slug munged, display = title", not
// "slug munged, display = alias", so display-by-title carries the
// majority of the rescue value at a fraction of the storage cost.
func (s *wikiIngestService) resolveLiveSlugs(
	ctx context.Context,
	batchCtx *WikiBatchContext,
	candidates map[string]struct{},
) (map[string]struct{}, map[string]string) {
	if len(candidates) == 0 || batchCtx == nil || batchCtx.SlugTitleMany == nil {
		return nil, nil
	}
	slugList := make([]string, 0, len(candidates))
	for s := range candidates {
		slugList = append(slugList, s)
	}
	titles := batchCtx.SlugTitleMany(ctx, slugList)
	live := make(map[string]struct{}, len(titles))
	titleToSlug := make(map[string]string, len(titles))
	for slug, title := range titles {
		live[slug] = struct{}{}
		if title != "" {
			titleToSlug[title] = slug
		}
	}
	return live, titleToSlug
}

// stripDeadWikiLinks rewrites `[[slug]]` / `[[slug|display]]` references
// whose `slug` falls into the dead set. The handling depends on whether
// the dead slug can be repaired:
//
//   - If the resolver maps the dead slug to a live one (typically via
//     display-text reverse lookup or hyphen-normalized equality —
//     see resolveDeadSlug), the link is REWRITTEN with the corrected
//     slug. Display text is preserved.
//   - If no live candidate is close enough, the link is STRIPPED to
//     plain text (display text when present; otherwise a humanized
//     last-segment of the slug). This is the original behaviour.
//
// The resolver is optional: when liveSlugs / titleToSlug are nil or
// empty, every dead slug falls through to the strip path. This keeps
// backward compatibility for tests / call sites that don't yet wire
// the resolution data.
func stripDeadWikiLinks(
	content string,
	deadSlugs map[string]struct{},
	liveSlugs map[string]struct{},
	titleToSlug map[string]string,
) (string, bool) {
	if len(deadSlugs) == 0 || content == "" {
		return content, false
	}
	changed := false
	out := wikiLinkRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := wikiLinkRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		slug := sub[1]
		if _, dead := deadSlugs[slug]; !dead {
			return match
		}
		display := ""
		if len(sub) >= 3 {
			display = strings.TrimSpace(sub[2])
		}

		// (1) Try fuzzy resolve before falling back to strip. The
		// resolver consults display-text reverse lookup, hyphen-
		// normalized equality, and bigram similarity in that order;
		// returns "" only when no candidate is safe.
		if resolved, ok := resolveDeadSlug(slug, display, liveSlugs, titleToSlug); ok && resolved != slug {
			changed = true
			if display != "" {
				return "[[" + resolved + "|" + display + "]]"
			}
			return "[[" + resolved + "]]"
		}

		// (2) Strip — best-effort plain text. Prefer the LLM-supplied
		// display text; otherwise humanize the slug's last path segment
		// so the prose stays readable.
		changed = true
		if display != "" {
			return display
		}
		parts := strings.Split(slug, "/")
		label := parts[len(parts)-1]
		label = strings.ReplaceAll(label, "-", " ")
		return label
	})
	return out, changed
}

// cleanDeadLinks rewrites `[[slug]]` references in the batch's affected
// pages whose targets no longer exist (or were archived). Pure text
// cleanup — no LLM call.
//
// Scope is intentionally limited to the slugs touched by this batch:
// at 4w-document scale the legacy "scan every page in the KB" path was
// the dominant tail in the post-batch phase, and the long-tail
// historical dead links are better handled by the lint AutoFix pipeline
// (which runs out-of-band and can afford a full table walk).
//
// For each affected page:
//
//  1. Pull its lite projection (out_links + status) via the batch's
//     SlugTitle fetcher (one IN query for the whole affected set,
//     amortized via the batchCtx cache).
//  2. Probe the union of out-link targets through ExistsSlugs to
//     classify them as live vs dead.
//  3. For each dead link, try resolveDeadSlug first; rewrite if a
//     safe candidate exists, otherwise strip to plain text.
//  4. Persist the rewritten content via UpdateAutoLinkedContent so
//     the version counter stays unchanged (this is a maintenance
//     pass, not a user-visible edit).
func (s *wikiIngestService) cleanDeadLinks(ctx context.Context, kbID string, affectedSlugs []string,
	batchCtx *WikiBatchContext,
) {
	if len(affectedSlugs) == 0 {
		return
	}

	// (1) Load the affected pages' content + out-links in one go.
	// We need the full WikiPage rows here (not just lite projections)
	// because we're going to rewrite content; the lite path saves
	// nothing once we're touching content anyway.
	cleaned := 0
	for _, slug := range affectedSlugs {
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.Status == types.WikiPageStatusArchived {
			continue
		}
		if page.PageType == types.WikiPageTypeIndex {
			continue
		}
		if len(page.OutLinks) == 0 {
			continue
		}

		// (2) Classify out-links as live vs dead via one batched
		// ExistsSlugs query. Empty slug list → no-op.
		liveMap, err := s.wikiService.ExistsSlugs(ctx, kbID, []string(page.OutLinks))
		if err != nil {
			logger.Warnf(ctx, "wiki: ExistsSlugs failed during dead-link cleanup for %s: %v", slug, err)
			continue
		}
		deadSlugs := make(map[string]struct{})
		liveSlugs := make(map[string]struct{}, len(liveMap))
		for outSlug, alive := range liveMap {
			if alive {
				liveSlugs[outSlug] = struct{}{}
			} else {
				deadSlugs[outSlug] = struct{}{}
			}
		}
		if len(deadSlugs) == 0 {
			continue
		}

		// (3) Build the title->slug reverse-lookup map for fuzzy
		// resolve. We pull titles for the live slugs only — those
		// are the candidates a dead reference could be remapped to.
		titles := batchCtx.SlugTitleMany(ctx, []string(page.OutLinks))
		titleToSlug := make(map[string]string, len(titles))
		for s, t := range titles {
			if t != "" {
				titleToSlug[t] = s
			}
		}

		newContent, changed := stripDeadWikiLinks(page.Content, deadSlugs, liveSlugs, titleToSlug)
		if !changed {
			continue
		}

		// (4) Persist. UpdateAutoLinkedContent skips the version bump
		// because dead-link cleanup is a machine-only edit.
		page.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err != nil {
			logger.Warnf(ctx, "wiki: failed to clean dead links in page %s: %v", page.Slug, err)
			continue
		}
		cleaned++
	}

	if cleaned > 0 {
		logger.Infof(ctx, "wiki: cleaned dead links in %d pages", cleaned)
	}
}

// injectCrossLinks scans the batch's affected pages and injects
// `[[wiki-links]]` for mentions of other wiki page titles / aliases
// in the content. Pure text replacement, no LLM call.
//
// Scope is intentionally limited to two slug sets:
//
//  1. The affected pages themselves — we only rewrite their content.
//  2. The candidate refs come from (a) the affected pages' existing
//     out-links (already known to be relevant via prior linkification
//     or manual edits) plus (b) the batch's freshly-written sibling
//     slugs supplied via `linkRefs` from the caller.
//
// At 4w-document scale this is the difference between loading 100K+
// pages just to find link candidates vs O(batch-size) lookups. We
// trade off some long-tail recall (a brand new entity in this batch
// won't be linkified into pages from previous batches until they get
// re-edited), but lint AutoFix is the right place for that.
//
// linkifyContent does the actual matching work, including code-block /
// existing-link / word-boundary exclusions.
func (s *wikiIngestService) injectCrossLinks(
	ctx context.Context,
	kbID string,
	affectedSlugs []string,
	freshRefs []linkRef,
	batchCtx *WikiBatchContext,
) {
	if len(affectedSlugs) == 0 {
		return
	}

	updated := 0
	for _, slug := range affectedSlugs {
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.PageType == types.WikiPageTypeIndex {
			continue
		}

		// Build the per-page candidate ref set: the existing out-links
		// (resolved via the batch's title fetcher to skip archived /
		// system pages) plus the freshly-written sibling slugs from
		// this batch.
		var refs []linkRef
		if len(page.OutLinks) > 0 {
			titles := batchCtx.SlugTitleMany(ctx, []string(page.OutLinks))
			for outSlug, title := range titles {
				if title == "" || outSlug == slug {
					continue
				}
				refs = append(refs, linkRef{slug: outSlug, matchText: title})
			}
		}
		for _, fr := range freshRefs {
			if fr.slug == slug {
				continue
			}
			refs = append(refs, fr)
		}
		if len(refs) == 0 {
			continue
		}

		newContent, changed := linkifyContent(page.Content, refs, page.Slug)
		if !changed {
			continue
		}
		page.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err != nil {
			logger.Warnf(ctx, "wiki ingest: cross-link injection failed for %s: %v", page.Slug, err)
			continue
		}
		updated++
	}

	if updated > 0 {
		logger.Infof(ctx, "wiki ingest: injected cross-links in %d pages", updated)
	}
}

// collectLinkRefs flattens (title + aliases) of all non-system pages into a
// single linkRef slice suitable for linkifyContent.
func collectLinkRefs(pages []*types.WikiPage) []linkRef {
	refs := make([]linkRef, 0, len(pages)*2)
	for _, p := range pages {
		if p.PageType == types.WikiPageTypeIndex {
			continue
		}
		if p.Title != "" {
			refs = append(refs, linkRef{slug: p.Slug, matchText: p.Title})
		}
		for _, alias := range p.Aliases {
			if alias != "" {
				refs = append(refs, linkRef{slug: p.Slug, matchText: alias})
			}
		}
	}
	return refs
}

// wikiTaxonomyPromptMaxPaths caps how many existing folders are rendered into a
// planning prompt as the set to reuse. Reached only for pathologically large
// taxonomies; the similarity preprocessing keeps the fed set well under it.
const wikiTaxonomyPromptMaxPaths = 150

// wikiTaxonomyFolderPoolMax bounds the existing folders pulled from the DB as the
// candidate pool for similarity selection. Distinct folders are few even for
// large KBs, so this only guards against a degenerate taxonomy.
const wikiTaxonomyFolderPoolMax = 400

// wikiTaxonomyFeedAllMaxFolders is the folder count at or below which the whole
// folder set is fed to the planner as-is: a healthy navigation directory is
// small, so feeding everything gives perfect reuse recall with no embedding cost
// (similarity preprocessing only earns its keep once folders are numerous).
const wikiTaxonomyFeedAllMaxFolders = 60

// wikiTaxonomyRelevantTopK is how many nearest existing deeper folders each item
// contributes to the reuse set when similarity preprocessing kicks in.
const wikiTaxonomyRelevantTopK = 3

// wikiTaxonomyPlanChunkSize caps how many items go into a single planning call.
// Larger batches are split into chunks; folders assigned by earlier chunks are
// fed forward as "existing folders" so later chunks converge onto the same tree.
const wikiTaxonomyPlanChunkSize = 60

const wikiTaxonomyEmptyTreeHint = "(none yet — this knowledge base has no folders, design a fresh directory)"

type wikiTaxonomyNode struct {
	children map[string]*wikiTaxonomyNode
}

func insertWikiTaxonomyPath(root *wikiTaxonomyNode, path []string) {
	if root == nil || len(path) == 0 {
		return
	}
	cur := root
	for _, part := range path {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if cur.children == nil {
			cur.children = make(map[string]*wikiTaxonomyNode)
		}
		child := cur.children[part]
		if child == nil {
			child = &wikiTaxonomyNode{}
			cur.children[part] = child
		}
		cur = child
	}
}

func appendWikiTaxonomyNode(buf *strings.Builder, label string, node *wikiTaxonomyNode, depth int) {
	if label != "" {
		fmt.Fprintf(buf, "%s%s\n", strings.Repeat("  ", depth), label)
	}
	if node == nil || len(node.children) == 0 {
		return
	}
	keys := make([]string, 0, len(node.children))
	for k := range node.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		appendWikiTaxonomyNode(buf, k, node.children[k], depth+1)
	}
}

// formatExistingTaxonomyForPrompt renders distinct category_path values as an
// indented folder tree for LLM extraction prompts.
func formatExistingTaxonomyForPrompt(paths [][]string) string {
	if len(paths) == 0 {
		return ""
	}
	root := &wikiTaxonomyNode{}
	for _, path := range paths {
		insertWikiTaxonomyPath(root, path)
	}
	if len(root.children) == 0 {
		return ""
	}
	var buf strings.Builder
	keys := make([]string, 0, len(root.children))
	for k := range root.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		appendWikiTaxonomyNode(&buf, k, root.children[k], 0)
	}
	return strings.TrimSpace(buf.String())
}

// getExistingPageSlugsForKnowledge returns all page slugs that currently
// reference a given knowledge ID in their source_refs. Used to snapshot
// state before re-ingest so the reduce phase can reconcile additions vs
// retractions.
//
// Backed by idx_wiki_pages_source_refs (GIN jsonb_path_ops, migration
// 000041) and the legacy text-index fallback for "kid|title" entries.
// We project to slugs only — no need to load full row content for a
// per-doc snapshot.
//
// Index/log slugs (wiki-intrinsic system pages) never carry real
// source_refs in practice, but we filter them out explicitly here as
// a defense-in-depth measure: an old buggy ingest that mistakenly
// stamped a system page with a knowledge ref would otherwise show up
// in the reparse "old set" and confuse the reduce stage.
func (s *wikiIngestService) getExistingPageSlugsForKnowledge(ctx context.Context, kbID,
	knowledgeID string,
) map[string]bool {
	slugs, err := s.wikiService.ListSlugsBySourceRef(ctx, kbID, knowledgeID)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: ListSlugsBySourceRef(%s) failed: %v", knowledgeID, err)
		return nil
	}
	if len(slugs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		// Defense-in-depth: skip wiki-intrinsic slugs that never have
		// real source refs.
		if slug == "index" {
			continue
		}
		out[slug] = true
	}
	return out
}

// retractStalePages handles pages that were previously linked to this document
// but are no longer produced by the updated extraction.
// - Single-source stale pages → deleted
// - Multi-source stale pages → LLM retract to clean content synchronously

// Build set of newly affected slugs (including summary)

// Stale = was in old set but not in new set

// Remove this doc's source ref

// No other sources → delete the page

// Multi-source → remove ref, queue retract

// extractedItem represents a single extracted entity or concept.
//
// SourceChunks holds the stable chunk IDs (from the source document) that
// substantively discuss this item. Populated by the chunk-citation pass; when
// non-empty the Reduce phase uses these chunks verbatim as the item's
// evidence instead of the shorter Description/Details fields.
type extractedItem struct {
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Aliases      []string `json:"aliases"`
	Description  string   `json:"description"`
	Details      string   `json:"details"`
	SourceChunks []string `json:"source_chunks,omitempty"`
}

// combinedExtraction represents the parsed result of the combined entity+concept extraction
type combinedExtraction struct {
	Entities []extractedItem `json:"entities"`
	Concepts []extractedItem `json:"concepts"`
}

// rebuildIndexPage refreshes the LLM-generated intro that sits on the
// index wiki_pages row.
//
// History: the index page used to store "intro + full directory listing" as
// a single multi-MB markdown blob in content. Every ingest batch rewrote
// the whole column, which on KBs with tens of thousands of pages caused
// O(N) TOAST writes per batch. The directory was lifted out into the
// structured GET /wiki/index endpoint (see wikiPageService.GetIndexView),
// and this method now only maintains the intro.
//
// Intro lifecycle:
//   - First time (empty or legacy placeholder): generate from all document
//     summaries via WikiIndexIntroPrompt.
//   - Subsequent calls with a change description: incremental update via
//     WikiIndexIntroUpdatePrompt so the intro reflects what just landed.
//   - No change description: keep the existing intro untouched.
//
// The new intro is written to both Content and Summary so readers that
// still fall back to Summary (older clients, legacy migrations) stay in
// sync with the column the view actually renders.
// indexIntroSummaryCap caps how many summary pages we feed into the
// LLM when generating the wiki index intro from scratch. A 4w-document
// KB would otherwise blow the context window every batch, and the
// intro is a "set the scene" artifact where the most-recently-touched
// documents carry disproportionately more signal anyway. We pick the
// top-N most-recently-updated summaries and add a "showing N of M"
// hint to the prompt so the LLM can be honest about its sample.
const indexIntroSummaryCap = 200

// rebuildIndexPage refreshes the LLM-generated intro on the index
// page. Two paths:
//
//   - First-time generation (no existing intro, or only the legacy
//     placeholder): the LLM gets a CAPPED window of the most recent
//     summary pages (most-recently-updated wins). Compare with the
//     legacy path which loaded ALL summaries — at 4w-document scale
//     that produced multi-MB prompts that simply broke the context
//     window and silently fell back to a hardcoded intro.
//   - Incremental update: the LLM gets only the existing intro plus
//     the change description for THIS batch. Document summaries are
//     intentionally NOT included — at scale the change-description
//     alone is enough signal for "what landed?", and excluding the
//     full summary set keeps the prompt size bounded regardless of
//     KB size.
//
// The intro is written to both Content and Summary so legacy readers
// that fall through to Summary stay in sync.
func (s *wikiIngestService) rebuildIndexPage(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload,
	changeDesc, lang, customInstructions string,
) error {
	indexPage, _ := s.wikiService.GetIndex(ctx, payload.KnowledgeBaseID)
	if indexPage == nil {
		return nil
	}

	// The intro lives on both Content and Summary. Prefer Content since
	// that's what the new index view returns; fall back to Summary for
	// rows written before this refactor so the incremental-update prompt
	// has something to work with.
	existingIntro := strings.TrimSpace(indexPage.Content)
	if existingIntro == "" {
		existingIntro = strings.TrimSpace(indexPage.Summary)
	}
	// Detect the legacy "intro + directory" payload. Such rows embed the
	// fence-separated "## Summary" sections right after the intro, so we
	// clip everything from the first directory heading onward to keep the
	// intro length bounded when we feed it back into the update prompt.
	if idx := strings.Index(existingIntro, "\n## "); idx >= 0 {
		existingIntro = strings.TrimSpace(existingIntro[:idx])
	}

	var intro string
	switch {
	case existingIntro == "" || existingIntro == "Wiki index - table of contents":
		// First-time generation: pull the top-N most-recent summary
		// pages via the lite projection. CountByType lets us tell the
		// LLM "showing N of M" so it can frame the intro honestly when
		// the KB is bigger than what we're sampling.
		recentSummaries, listErr := s.wikiService.ListByTypeRecent(ctx, payload.KnowledgeBaseID,
			types.WikiPageTypeSummary, indexIntroSummaryCap)
		if listErr != nil {
			return listErr
		}
		var docSummaries strings.Builder
		for _, e := range recentSummaries {
			fmt.Fprintf(&docSummaries, "<document>\n<title>%s</title>\n<summary>%s</summary>\n</document>\n\n", e.Title,
				e.Summary)
		}
		// Best-effort total count for the framing hint. CountByType
		// counts every page type; we need just summary, so we read
		// directly. A failure here doesn't block intro generation.
		totalSummaries := int64(len(recentSummaries))
		if counts, cntErr := s.wikiService.CountByType(ctx, payload.KnowledgeBaseID); cntErr == nil {
			if t, ok := counts[types.WikiPageTypeSummary]; ok {
				totalSummaries = t
			}
		}
		framing := ""
		if int(totalSummaries) > len(recentSummaries) && len(recentSummaries) > 0 {
			framing = fmt.Sprintf("(showing %d most recent of %d total documents)\n\n", len(recentSummaries),
				totalSummaries)
		}
		if docSummaries.Len() == 0 {
			docSummaries.WriteString("(no documents yet)")
		}
		generatedIntro, genErr := s.generateWithTemplate(ctx, chatModel, agent.WikiIndexIntroPrompt, map[string]string{
			"DocumentSummaries":  framing + docSummaries.String(),
			"Language":           lang,
			"CustomInstructions": customInstructions,
			"InstructionScope":   "wiki_content",
		})
		if genErr != nil {
			intro = "# Wiki Index\n\nThis wiki contains knowledge extracted from uploaded documents.\n"
		} else {
			intro = strings.TrimSpace(generatedIntro)
		}
	case changeDesc != "":
		// Incremental update: only the existing intro + this batch's
		// change description go into the prompt. We deliberately stop
		// passing the full DocumentSummaries set here — at 4w docs it
		// would re-flood the context every batch, and the
		// change-description block already encodes the "what just
		// changed" signal the prompt is asking for.
		updatedIntro, genErr := s.generateWithTemplate(ctx, chatModel, agent.WikiIndexIntroUpdatePrompt,
			map[string]string{
				"ExistingIntro":      existingIntro,
				"ChangeDescription":  changeDesc,
				"DocumentSummaries":  "",
				"Language":           lang,
				"CustomInstructions": customInstructions,
				"InstructionScope":   "wiki_content",
			})
		if genErr != nil {
			intro = existingIntro // keep existing on error
		} else {
			intro = strings.TrimSpace(updatedIntro)
		}
	default:
		// No change description and an existing intro: leave it as-is so
		// we don't bump the version for a no-op.
		intro = existingIntro
	}

	// Defensive: some LLM outputs occasionally bleed into a directory-
	// like section even when the intro prompt doesn't ask for one. If
	// the freshly-generated intro starts to look like a legacy payload,
	// clip it at the first "\n## " just like we did on the read path
	// above. This keeps indexPage.Content a bounded intro-only blob.
	if idx := strings.Index(intro, "\n## "); idx >= 0 {
		intro = strings.TrimSpace(intro[:idx])
	}

	indexPage.Content = intro
	indexPage.Summary = intro
	_, err := s.wikiService.UpdatePage(ctx, indexPage)
	return err
}

// splitSummaryLine extracts the "SUMMARY: ..." line from LLM output.
// Returns (summary, content). If no SUMMARY line found, summary is empty.
func splitSummaryLine(raw string) (summary string, content string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "SUMMARY:") || strings.HasPrefix(raw, "SUMMARY：") {
		idx := strings.IndexByte(raw, '\n')
		if idx < 0 {
			// Only one line
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "SUMMARY:"), "SUMMARY：")), ""
		}
		summaryLine := raw[:idx]
		summaryLine = strings.TrimPrefix(summaryLine, "SUMMARY:")
		summaryLine = strings.TrimPrefix(summaryLine, "SUMMARY：")
		return strings.TrimSpace(summaryLine), strings.TrimSpace(raw[idx+1:])
	}
	return "", raw
}

// publishDraftPages transitions draft pages to published status after ingest completes.
// This ensures users don't see half-built pages during the ingest process.
func (s *wikiIngestService) publishDraftPages(ctx context.Context, kbID string, slugs []string) {
	for _, slug := range slugs {
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.Status == types.WikiPageStatusDraft {
			page.Status = types.WikiPageStatusPublished
			if err := s.wikiService.UpdatePageMeta(ctx, page); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to publish page %s: %v", slug, err)
			}
		}
	}
}

// writeDedupCandidateGroup renders one new item together with its own
// similarity-candidate existing pages, nested under a <candidates> element.
// This per-item grouping is what constrains the dedup model to local
// decisions (see the grouping rationale in deduplicateExtractedBatch). The
// candidate pages keep their aliases so the model still has the acronym /
// translation signal it needs to accept a legitimate merge.
func writeDedupCandidateGroup(
	buf *strings.Builder, item extractedItem, itemType string, candidates []*types.WikiPageLite,
) {
	fmt.Fprintf(buf, "  <item slug=%q type=%q>\n", item.Slug, itemType)
	fmt.Fprintf(buf, "    <name>%s</name>\n", xmlEscape(item.Name))
	for _, alias := range item.Aliases {
		if alias == "" {
			continue
		}
		fmt.Fprintf(buf, "    <alias>%s</alias>\n", xmlEscape(alias))
	}
	buf.WriteString("    <candidates>\n")
	for _, p := range candidates {
		if p == nil {
			continue
		}
		fmt.Fprintf(buf, "      <page slug=%q type=%q>\n", p.Slug, p.PageType)
		fmt.Fprintf(buf, "        <name>%s</name>\n", xmlEscape(p.Title))
		for _, alias := range []string(p.Aliases) {
			if alias == "" {
				continue
			}
			fmt.Fprintf(buf, "        <alias>%s</alias>\n", xmlEscape(alias))
		}
		buf.WriteString("      </page>\n")
	}
	buf.WriteString("    </candidates>\n")
	buf.WriteString("  </item>\n")
}

// xmlEscape escapes the minimal set of characters that can break XML text
// content. Slugs are ASCII-only so they don't need escaping when used as
// attribute values.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// deduplicateExtractedBatch deduplicates both entities and concepts against
// existing wiki pages in a single LLM call. Pre-filters candidates via the
// pg_trgm trigram index on lower(title) — every new item issues a
// FindSimilarPages probe and the union of top-K hits across all items is
// the candidate set. This replaces the legacy "ListAllPages + Go-side
// surface-form Jaccard" path that scaled O(P × N) on large KBs.
//
// The KB-id-keyed query relies on idx_wiki_pages_title_trgm (added in
// migration 000041); pg_search environments load pg_trgm in the same
// init step (see migrations/paradedb/00-init-db.sql).
func (s *wikiIngestService) deduplicateExtractedBatch(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	entities, concepts []extractedItem,
	batchCtx *WikiBatchContext,
) ([]extractedItem, []extractedItem) {
	if len(entities) == 0 && len(concepts) == 0 {
		return entities, concepts
	}
	if s.wikiService == nil {
		return s.stabilizeExtractedIdentities(ctx, kbID, types.WikiPageTypeEntity, entities, nil, nil, batchCtx),
			s.stabilizeExtractedIdentities(ctx, kbID, types.WikiPageTypeConcept, concepts, nil, nil, batchCtx)
	}

	// Build the candidate set: for each new item, ask the repo for
	// the top-K trigram-similar pages and union the results. Dedup by
	// slug as we go so the prompt only carries each candidate once.
	//
	// itemCandidates additionally records, per new item, the slugs that
	// surfaced for THAT item specifically. The prompt only ever sees the
	// flattened union, so validMerge below uses this per-item scoping to
	// reject a merge whose target was pulled in for a *different* item —
	// the class of hallucination the union otherwise enables (e.g. weak
	// models emitting entity/acme-open → entity/hiring-agent, which
	// share no trigram signal and were never candidates for each other).
	candidatePages := make(map[string]*types.WikiPageLite)
	itemCandidates := make(map[string]map[string]bool)
	probe := func(item extractedItem) {
		queries := make([]string, 0, 1+len(item.Aliases))
		if item.Name != "" {
			queries = append(queries, item.Name)
		}
		for _, alias := range item.Aliases {
			if alias != "" {
				queries = append(queries, alias)
			}
		}
		own := itemCandidates[item.Slug]
		if own == nil {
			own = make(map[string]bool)
			itemCandidates[item.Slug] = own
		}
		for _, q := range queries {
			pages, err := s.wikiService.FindSimilarPages(ctx, kbID, q,
				[]string{types.WikiPageTypeEntity, types.WikiPageTypeConcept},
				dedupCandidateTopK)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: dedup FindSimilarPages(%q) failed: %v", q, err)
				continue
			}
			for _, p := range pages {
				if p == nil || p.Slug == "" {
					continue
				}
				if _, ok := candidatePages[p.Slug]; !ok {
					candidatePages[p.Slug] = p
				}
				own[p.Slug] = true
			}
		}
	}
	for _, e := range entities {
		probe(e)
	}
	for _, c := range concepts {
		probe(c)
	}
	s.attachExactIdentityPages(ctx, kbID, types.WikiPageTypeEntity, entities, candidatePages, itemCandidates, batchCtx)
	s.attachExactIdentityPages(ctx, kbID, types.WikiPageTypeConcept, concepts, candidatePages, itemCandidates, batchCtx)

	// Resolve exact same-type, same-title candidates deterministically before
	// asking the model about semantic/alias variants. Besides avoiding an LLM
	// call for the obvious case, this makes an already-materialized page
	// authoritative for the identity reservation below.
	exactTargets := make(map[string]string)
	mergeTargets := make(map[string]string)
	collectExactIdentityTargets(entities, types.WikiPageTypeEntity, itemCandidates, candidatePages, exactTargets)
	collectExactIdentityTargets(concepts, types.WikiPageTypeConcept, itemCandidates, candidatePages, exactTargets)

	stabilize := func() ([]extractedItem, []extractedItem) {
		return s.stabilizeExtractedIdentities(
				ctx, kbID, types.WikiPageTypeEntity, entities, mergeTargets, exactTargets, batchCtx,
			), s.stabilizeExtractedIdentities(
				ctx, kbID, types.WikiPageTypeConcept, concepts, mergeTargets, exactTargets, batchCtx,
			)
	}

	if len(candidatePages) == 0 {
		// No similar existing pages — identity reservations still make
		// concurrent batches converge before they materialize new pages.
		logger.Infof(ctx, "wiki ingest: no similar existing pages found for %d new items", len(entities)+len(concepts))
		return stabilize()
	}
	logger.Infof(ctx, "wiki ingest: %d similar existing pages selected for %d new items",
		len(candidatePages), len(entities)+len(concepts))

	// Group each new item with ONLY the existing pages that surfaced for
	// its own similarity probe. Presenting the model two flat lists (all
	// new items × all candidates) invites cross-item mispairings — it has
	// no way to tell which candidate is relevant to which item, so a weak
	// model pairs unrelated slugs that merely coexist in the prompt. A
	// per-item shortlist turns dedup into a local yes/no decision against
	// a handful of genuinely-similar pages and makes cross-item pairings
	// structurally unnatural to express. Items with no candidate are
	// omitted entirely (they cannot merge and only add hallucination
	// surface + tokens).
	var candBuf strings.Builder
	groups := 0
	renderGroup := func(item extractedItem, itemType string) {
		if exactTargets[item.Slug] != "" {
			return
		}
		cset := itemCandidates[item.Slug]
		if len(cset) == 0 {
			return
		}
		slugs := make([]string, 0, len(cset))
		for slug := range cset {
			// Skip the item's own slug: an identically-slugged existing
			// page is a re-ingest/update, not a merge target.
			if slug == item.Slug {
				continue
			}
			if _, ok := candidatePages[slug]; ok {
				slugs = append(slugs, slug)
			}
		}
		if len(slugs) == 0 {
			return
		}
		sort.Strings(slugs)
		pages := make([]*types.WikiPageLite, 0, len(slugs))
		for _, slug := range slugs {
			pages = append(pages, candidatePages[slug])
		}
		writeDedupCandidateGroup(&candBuf, item, itemType, pages)
		groups++
	}
	for _, item := range entities {
		renderGroup(item, "entity")
	}
	for _, item := range concepts {
		renderGroup(item, "concept")
	}
	if groups == 0 {
		// Every item was resolved exactly or has no safe semantic candidate.
		return stabilize()
	}

	dedupeJSON, err := s.generateWithTemplate(ctx, chatModel, agent.WikiDeduplicationPrompt, map[string]string{
		"Candidates": candBuf.String(),
	})
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: deduplication LLM call failed: %v", err)
		return stabilize()
	}

	dedupeJSON = cleanLLMJSON(dedupeJSON)

	var dedupeResult struct {
		Merges map[string]string `json:"merges"`
	}
	if err := json.Unmarshal([]byte(dedupeJSON), &dedupeResult); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to parse dedup JSON: %v\nRaw: %s", err, dedupeJSON)
		return stabilize()
	}

	validMerge := func(srcSlug, dstSlug string) bool {
		if reason := dedupMergeRejectReason(srcSlug, dstSlug, itemCandidates[srcSlug]); reason != "" {
			logger.Warnf(ctx, "wiki ingest: dedup rejected %s → %s (%s)", srcSlug, dstSlug, reason)
			return false
		}
		return true
	}

	for _, item := range entities {
		if exactTargets[item.Slug] != "" {
			continue
		}
		if existingSlug, ok := dedupeResult.Merges[item.Slug]; ok && validMerge(item.Slug, existingSlug) {
			logger.Infof(ctx, "wiki ingest: dedup merge %s → %s", item.Slug, existingSlug)
			mergeTargets[item.Slug] = existingSlug
		}
	}
	for _, item := range concepts {
		if exactTargets[item.Slug] != "" {
			continue
		}
		if existingSlug, ok := dedupeResult.Merges[item.Slug]; ok && validMerge(item.Slug, existingSlug) {
			logger.Infof(ctx, "wiki ingest: dedup merge %s → %s", item.Slug, existingSlug)
			mergeTargets[item.Slug] = existingSlug
		}
	}

	return stabilize()
}
