package chatpipeline

import (
	"context"
	"sort"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// wikiBoostFactor is the score multiplier applied to wiki page chunks.
// Wiki pages contain LLM-synthesized, cross-referenced knowledge and should
// be preferred over raw document chunks when both are available.
const wikiBoostFactor = 1.3

// PluginWikiBoost boosts the relevance score of wiki page chunks in search results.
// Wiki pages contain pre-synthesized knowledge that is more coherent and
// cross-referenced than raw document chunks, so they should rank higher.
//
// This plugin runs in the CHUNK_RERANK phase, after initial retrieval and reranking.
// It identifies chunks with ChunkType == "wiki_page" and multiplies their score.
type PluginWikiBoost struct {
	kbService interfaces.KnowledgeBaseService
}

// NewPluginWikiBoost creates and registers the wiki boost plugin
func NewPluginWikiBoost(eventManager *EventManager, kbService interfaces.KnowledgeBaseService) *PluginWikiBoost {
	p := &PluginWikiBoost{
		kbService: kbService,
	}
	eventManager.Register(p)
	return p
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginWikiBoost) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent boosts wiki page chunk scores after reranking
func (p *PluginWikiBoost) OnEvent(
	ctx context.Context,
	_ types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	// Run the normal reranking first
	if err := next(); err != nil {
		return err
	}

	if chatManage == nil || p == nil || p.kbService == nil {
		return nil
	}

	// Fast path: skip all work if there are no wiki_page chunks in the result set.
	// This avoids hitting the KB service on every chat turn for non-wiki queries.
	hasWikiChunk := false
	for i := range chatManage.RerankResult {
		if chatManage.RerankResult[i] != nil && chatManage.RerankResult[i].ChunkType == types.ChunkTypeWikiPage {
			hasWikiChunk = true
			break
		}
	}
	if !hasWikiChunk {
		return nil
	}

	// Collect which search target KBs are wiki-enabled so only chunks from
	// wiki-enabled KBs receive the wiki boost.
	wikiKBIDs := make(map[string]bool, len(chatManage.SearchTargets))
	for _, target := range chatManage.SearchTargets {
		if target == nil || target.KnowledgeBaseID == "" || wikiKBIDs[target.KnowledgeBaseID] {
			continue
		}
		kb, err := p.kbService.GetKnowledgeBaseByIDOnly(ctx, target.KnowledgeBaseID)
		if err == nil && kb != nil && kb.IsWikiEnabled() {
			wikiKBIDs[target.KnowledgeBaseID] = true
		}
	}
	if len(wikiKBIDs) == 0 {
		return nil
	}

	// Boost wiki page chunks in RerankResult only for wiki-enabled KBs.
	boostedCount := 0
	for i := range chatManage.RerankResult {
		r := chatManage.RerankResult[i]
		if r != nil && r.ChunkType == types.ChunkTypeWikiPage && wikiKBIDs[r.KnowledgeBaseID] {
			r.Score *= wikiBoostFactor
			boostedCount++
		}
	}

	if boostedCount > 0 {
		logger.Infof(ctx, "WikiBoost: boosted %d wiki page chunks by %.1fx", boostedCount, wikiBoostFactor)
		// Re-sort by score after boosting; stable sort preserves ordering for ties.
		sort.SliceStable(chatManage.RerankResult, func(i, j int) bool {
			if chatManage.RerankResult[i] == nil {
				return false
			}
			if chatManage.RerankResult[j] == nil {
				return true
			}
			return chatManage.RerankResult[i].Score > chatManage.RerankResult[j].Score
		})
	}

	return nil
}
