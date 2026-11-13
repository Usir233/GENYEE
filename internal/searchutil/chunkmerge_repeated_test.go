package searchutil

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMergeTextChunksPreservesRepeatedRowsOutsideActualOverlap(t *testing.T) {
	row := "| repeated table cell | 重复内容 |\n"
	prefix, suffix := "Table:\n", "End of table.\n"
	first := prefix + strings.Repeat(row, 5)
	second := strings.Repeat(row, 4) + suffix
	end := utf8.RuneCountInString(first)
	start := end - utf8.RuneCountInString(row)
	chunks := []*types.Chunk{
		{Content: first, StartAt: 0, EndAt: end, ChunkIndex: 0},
		{Content: second, StartAt: start, EndAt: start + utf8.RuneCountInString(second), ChunkIndex: 1},
	}
	want := prefix + strings.Repeat(row, 8) + suffix
	require.Equal(t, want, MergeTextChunks(chunks, "\n"))

	// A preceding disconnected chunk must not prevent exact merging of the
	// next contiguous span, and the caller's order must remain untouched.
	for _, c := range chunks {
		c.StartAt += 100
		c.EndAt += 100
		c.ChunkIndex++
	}
	before := &types.Chunk{Content: "Before", StartAt: 0, EndAt: 6}
	input := []*types.Chunk{chunks[1], before, chunks[0]}
	require.Equal(t, "Before\n"+want, MergeTextChunks(input, "\n"))
	require.Same(t, chunks[1], input[0])
}
