// Package sqlite implements local retrieval with SQLite FTS and vectors.
package sqlite

import (
	"context"
	"fmt"
	"strings"
)

func (r *sqliteRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	_ int,
	_ string,
) error {
	if strings.TrimSpace(sourceKB) == "" || strings.TrimSpace(targetKB) == "" || strings.TrimSpace(knowledgeID) == "" {
		return fmt.Errorf("[SQLite] MoveKnowledgeIndices: sourceKB, targetKB, and knowledgeID are required")
	}
	// FTS and vector rows keep their IDs. Queries join them to this metadata row.
	return r.db.WithContext(ctx).Model(&sqliteEmbedding{}).
		Where("knowledge_base_id = ? AND knowledge_id = ?", sourceKB, knowledgeID).
		Updates(map[string]any{"knowledge_base_id": targetKB, "tag_id": ""}).Error
}
