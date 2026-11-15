// Package postgres implements retrieval against its configured vector backend.
package postgres

import (
	"context"
	"fmt"
	"strings"
)

func (g *pgRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	_ int,
	_ string,
) error {
	if strings.TrimSpace(sourceKB) == "" || strings.TrimSpace(targetKB) == "" || strings.TrimSpace(knowledgeID) == "" {
		return fmt.Errorf("postgres move: sourceKB, targetKB, and knowledgeID are required")
	}
	return g.db.WithContext(ctx).Model(&pgVector{}).
		Where("knowledge_base_id = ? AND knowledge_id = ?", sourceKB, knowledgeID).
		Updates(map[string]any{"knowledge_base_id": targetKB, "tag_id": ""}).Error
}
