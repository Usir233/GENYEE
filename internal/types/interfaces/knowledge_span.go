package interfaces

import (
	"context"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
)

// KnowledgeSpanRepository persists the per-attempt span tree used by the
// knowledge processing pipeline.
type KnowledgeSpanRepository interface {
	Upsert(ctx context.Context, row *types.KnowledgeProcessingSpan) error
	NextAttempt(ctx context.Context, knowledgeID string) (int, error)
	LatestAttempt(ctx context.Context, knowledgeID string) (int, error)
	ListByAttempt(ctx context.Context, knowledgeID string, attempt int) ([]types.KnowledgeProcessingSpan, error)
	GetSpan(ctx context.Context, knowledgeID string, attempt int, spanID string) (*types.KnowledgeProcessingSpan, error)
	CancelDescendants(ctx context.Context, knowledgeID string, attempt int, parentSpanID, reason string) (int64, error)
	CancelAllOpenSpans(ctx context.Context, knowledgeID string, attempt int, errorCode, reason string) (int64, error)
	CancelOpenSpansBeforeAttempt(
		ctx context.Context, knowledgeID string, attempt int, errorCode, reason string,
	) (int64, error)
	CancelOpenSpansByName(
		ctx context.Context,
		knowledgeID string,
		attempt int,
		name,
		errorCode,
		reason string,
	) (int64, error)
	LastActivity(ctx context.Context, knowledgeIDs []string) (map[string]time.Time, error)
}
