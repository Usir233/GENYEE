// Package v8 implements retrieval against its configured vector backend.
package v8

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	elasticsearchRetriever "github.com/Usir233/GENYEE/internal/application/repository/retriever/elasticsearch"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
)

func (e *elasticsearchRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	_ int,
	_ string,
) error {
	sourceKB = strings.TrimSpace(sourceKB)
	targetKB = strings.TrimSpace(targetKB)
	knowledgeID = strings.TrimSpace(knowledgeID)
	if sourceKB == "" || targetKB == "" || knowledgeID == "" {
		return fmt.Errorf("move indices requires non-empty sourceKB, targetKB, and knowledgeID")
	}

	query := &types.Query{Bool: &types.BoolQuery{Filter: []types.Query{
		{
			Terms: &types.TermsQuery{
				TermsQuery: map[string]types.TermsQueryField{e.idField("knowledge_base_id"): []string{sourceKB}},
			},
		},
		{
			Terms: &types.TermsQuery{
				TermsQuery: map[string]types.TermsQueryField{e.idField("knowledge_id"): []string{knowledgeID}},
			},
		},
	}}}
	source := "ctx._source.knowledge_base_id = params.target; ctx._source.tag_id = ''"
	target, err := json.Marshal(targetKB)
	if err != nil {
		return err
	}
	script := &types.Script{Source: &source, Params: map[string]json.RawMessage{"target": target}}
	result, err := e.client.UpdateByQuery(e.index).Query(query).Script(script).Refresh(true).Do(ctx)
	if err != nil {
		return errorsWrapRedacted(err)
	}
	if result == nil || result.Total == nil || result.Updated == nil ||
		*result.Total < 0 || *result.Total != *result.Updated || (result.TimedOut != nil && *result.TimedOut) ||
		(result.VersionConflicts != nil && *result.VersionConflicts != 0) ||
		len(result.Failures) != 0 {
		return fmt.Errorf("move indices was incomplete")
	}
	return nil
}

func errorsWrapRedacted(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", elasticsearchRetriever.RedactURLCredentials(err.Error()))
}
