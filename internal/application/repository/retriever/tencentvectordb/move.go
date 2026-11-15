// Package tencentvectordb implements retrieval against its configured vector backend.
package tencentvectordb

import (
	"context"
	"fmt"
	"strings"

	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

func (r *repository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	src := strings.TrimSpace(sourceKB)
	dst := strings.TrimSpace(targetKB)
	docID := strings.TrimSpace(knowledgeID)
	if src == "" || dst == "" || docID == "" {
		return fmt.Errorf("tencent vectordb move indices requires non-empty sourceKB, targetKB, and knowledgeID")
	}
	if r.useDimensionSuffix && dimension <= 0 {
		return fmt.Errorf("tencent vectordb invalid dimension %d for move indices", dimension)
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}
	filter := tcvectordb.NewFilter(
		filterIn(fieldKnowledgeBaseID, []string{src}) + " and " + filterIn(fieldKnowledgeID, []string{docID}),
	)
	_, err := r.client.Database(r.databaseName).
		Collection(r.collectionName(dimension)).
		Update(ctx, tcvectordb.UpdateDocumentParams{
			QueryFilter:  filter,
			UpdateFields: map[string]tcvectordb.Field{fieldKnowledgeBaseID: {Val: dst}, fieldTagID: {Val: ""}},
		})
	return redactTencentErr(err)
}
