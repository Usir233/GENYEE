package chatpipeline

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Usir233/GENYEE/internal/searchutil"
	"github.com/Usir233/GENYEE/internal/types"
)

func isHistoryWebReference(r *types.SearchResult) bool {
	if r == nil {
		return false
	}
	return r.MatchType == types.MatchTypeWebSearch ||
		strings.EqualFold(strings.TrimSpace(r.KnowledgeSource), "web_search")
}

func historyReferenceInScope(r *types.SearchResult, chatManage *types.ChatManage) bool {
	if r == nil || chatManage == nil {
		return false
	}
	hasKBScope := types.HasKnowledgeRetrievalScope(
		chatManage.SearchTargets,
		chatManage.KnowledgeBaseIDs,
		chatManage.KnowledgeIDs,
	)
	if !hasKBScope && !chatManage.WebSearchEnabled {
		return true
	}
	if isHistoryWebReference(r) {
		return chatManage.WebSearchEnabled
	}
	if r.KnowledgeBaseID == "" {
		return false
	}
	if len(chatManage.SearchTargets) > 0 {
		for _, target := range chatManage.SearchTargets {
			if target == nil || target.KnowledgeBaseID != r.KnowledgeBaseID {
				continue
			}
			if len(target.TagIDs) > 0 || len(target.ScopeTagIDs) > 0 {
				if target.Type == types.SearchTargetTypeKnowledge &&
					r.KnowledgeID != "" &&
					slices.Contains(target.KnowledgeIDs, r.KnowledgeID) {
					return true
				}
				continue
			}
			if target.Type == types.SearchTargetTypeKnowledgeBase {
				return true
			}
			if target.Type == types.SearchTargetTypeKnowledge &&
				r.KnowledgeID != "" &&
				slices.Contains(target.KnowledgeIDs, r.KnowledgeID) {
				return true
			}
		}
		return false
	}
	if slices.Contains(chatManage.KnowledgeBaseIDs, r.KnowledgeBaseID) {
		return true
	}
	return r.KnowledgeID != "" && slices.Contains(chatManage.KnowledgeIDs, r.KnowledgeID)
}

// filterHistoryResults retrieves history references and filters them by
// how much of the current query they cover. Only references whose token
// overlap with the query reaches a threshold are kept, and their scores are
// discounted to reflect that they were not directly retrieved for the
// current query. Results already present in currentResults (by chunk ID)
// are excluded.
//
// Overlap is measured against the smaller token set (in practice the query).
// Jaccard divided by the union, so a query of about 8 terms against a chunk of
// a few hundred never passed 0.05 and the 0.15 threshold kept nothing.
func filterHistoryResults(
	ctx context.Context,
	chatManage *types.ChatManage,
	currentResults []*types.SearchResult,
) []*types.SearchResult {
	const (
		// minSimilarity is the minimum share of query terms (overlap
		// coefficient) a history chunk must contain to be injected.
		minSimilarity = 0.3
		// historyScoreDiscount reduces the original score of history results
		// to rank them below freshly-retrieved results of similar relevance.
		historyScoreDiscount = 0.6
		// maxHistoryResults caps the number of history results injected to
		// avoid overwhelming the context with stale references.
		maxHistoryResults = 3
	)

	if chatManage == nil {
		return nil
	}
	raw := getSearchResultFromHistory(chatManage)
	if len(raw) == 0 {
		return nil
	}

	// Build a set of chunk IDs already in current results for fast dedup
	existingIDs := make(map[string]struct{}, len(currentResults))
	for _, r := range currentResults {
		if r == nil {
			continue
		}
		existingIDs[r.ID] = struct{}{}
	}

	// Use RewriteQuery if available (it's the cleaned-up retrieval query),
	// otherwise fall back to the original query.
	query := chatManage.RewriteQuery
	if query == "" {
		query = chatManage.Query
	}
	queryTokens := searchutil.TokenizeSimple(query)

	var filtered []*types.SearchResult
	for _, r := range raw {
		if r == nil {
			continue
		}
		if _, exists := existingIDs[r.ID]; exists {
			continue
		}
		if !historyReferenceInScope(r, chatManage) {
			pipelineInfo(ctx, "Merge", "history_filter_out_of_scope", map[string]interface{}{
				"chunk_id": r.ID,
				"kb_id":    r.KnowledgeBaseID,
			})
			continue
		}
		contentTokens := searchutil.TokenizeSimple(r.Content)
		sim := searchutil.TokenOverlapRatio(queryTokens, contentTokens)
		if sim < minSimilarity {
			pipelineInfo(ctx, "Merge", "history_filter_drop", map[string]interface{}{
				"chunk_id":   r.ID,
				"similarity": sim,
			})
			continue
		}
		r.MatchType = types.MatchTypeHistory
		r.Score *= historyScoreDiscount
		if r.Metadata == nil {
			r.Metadata = make(map[string]string)
		}
		r.Metadata["history_similarity"] = strings.TrimRight(strings.TrimRight(
			fmt.Sprintf("%.4f", sim), "0"), ".")
		filtered = append(filtered, r)

		pipelineInfo(ctx, "Merge", "history_filter_keep", map[string]interface{}{
			"chunk_id":   r.ID,
			"similarity": sim,
			"new_score":  r.Score,
		})

		if len(filtered) >= maxHistoryResults {
			break
		}
	}
	return filtered
}
