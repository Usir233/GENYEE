package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDocumentReconstructionPreservesRepeatedRows(t *testing.T) {
	row := "| recurring observation | 42 |\n"
	first := "Measurements:\n" + strings.Repeat(row, 5)
	second := strings.Repeat(row, 4) + "End.\n"
	end := utf8.RuneCountInString(first)
	start := end - utf8.RuneCountInString(row)
	chunks := []*types.Chunk{
		{ChunkType: types.ChunkTypeText, IsEnabled: true, Content: first, StartAt: 0, EndAt: end, ChunkIndex: 0},
		{
			ChunkType:  types.ChunkTypeText,
			IsEnabled:  true,
			Content:    second,
			StartAt:    start,
			EndAt:      start + utf8.RuneCountInString(second),
			ChunkIndex: 1,
		},
	}
	for name, reconstruct := range map[string]func([]*types.Chunk) string{
		"wiki source":     reconstructContent,
		"graph relations": (&graphBuilder{}).mergeChunkContents,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, "Measurements:\n"+strings.Repeat(row, 8)+"End.\n", reconstruct(chunks))
		})
	}
}
