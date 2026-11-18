package service

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestWikiReconstructionUsesCurrentEnabledChunks(t *testing.T) {
	chunks := []*types.Chunk{
		{
			ID: "first", ChunkType: types.ChunkTypeText, IsEnabled: true, ChunkIndex: 0, StartAt: 0, EndAt: 100,
			Content: "Current first section.", ContentRevision: 1,
		},
		{
			ID: "second", ChunkType: types.ChunkTypeText, IsEnabled: true, ChunkIndex: 1, StartAt: 50, EndAt: 90,
			Content: "Edited second section must survive.", ContentRevision: 2,
		},
		{
			ID: "disabled", ChunkType: types.ChunkTypeText, IsEnabled: false, ChunkIndex: 2, StartAt: 100, EndAt: 150,
			Content: "Disabled obsolete instruction.", ContentRevision: 1,
		},
	}
	require.Equal(t, "Current first section.\n\nEdited second section must survive.", reconstructContent(chunks))
}

type wikiCurrentImageRepo struct {
	interfaces.ChunkRepository
	requested []string
}

func (r *wikiCurrentImageRepo) ListChunksByParentIDs(_ context.Context, _ uint64, ids []string) ([]*types.Chunk,
	error,
) {
	r.requested = append(r.requested, ids...)
	for _, id := range ids {
		if id == "disabled" {
			return []*types.Chunk{{
				ID: "image", ParentChunkID: id, ChunkType: types.ChunkTypeImageCaption,
				IsEnabled: true,
				ImageInfo: `[{"url":"resource://disabled-image","caption":"Obsolete image caption"}]`,
			}}, nil
		}
	}
	return nil, nil
}

func TestWikiReconstructionDoesNotReintroduceDisabledImageEvidence(t *testing.T) {
	repo := &wikiCurrentImageRepo{}
	chunks := []*types.Chunk{
		{ID: "enabled", ChunkType: types.ChunkTypeText, IsEnabled: true, Content: "Current evidence."},
		{
			ID: "disabled", ChunkType: types.ChunkTypeText, IsEnabled: false,
			Content: "![obsolete](resource://disabled-image)",
		},
	}
	require.Equal(t, "Current evidence.", reconstructEnrichedContent(context.Background(), repo, 1, chunks))
	require.NotContains(t, repo.requested, "disabled")
}
