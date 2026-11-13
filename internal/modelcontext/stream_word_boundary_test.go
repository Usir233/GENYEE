package modelcontext

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type textStreamDecoder interface {
	Feed(string) string
	Flush() string
}

// Check every one- and two-cut partition, including empty chunks, plus a
// bytewise stream. Decoder stages must retain lexical context across calls;
// testing only splits inside the handle misses boundaries before its prefix.
func assertStreamPartitions(t *testing.T, input, want string, fresh func() textStreamDecoder) {
	t.Helper()
	for first := 0; first <= len(input); first++ {
		decoder := fresh()
		got := decoder.Feed(input[:first]) + decoder.Feed(input[first:]) + decoder.Flush()
		require.Equal(t, want, got, "one cut at %d", first)
		for second := first; second <= len(input); second++ {
			decoder := fresh()
			got := decoder.Feed(input[:first]) + decoder.Feed(input[first:second]) +
				decoder.Feed(input[second:]) + decoder.Flush()
			require.Equal(t, want, got, "two cuts at %d, %d", first, second)
		}
	}
	decoder := fresh()
	var got string
	for i := 0; i < len(input); i++ {
		got += decoder.Feed(input[i : i+1])
	}
	got += decoder.Flush()
	require.Equal(t, want, got, "bytewise stream")
	require.Empty(t, decoder.Flush(), "repeated flush must not duplicate text")
}

func TestHandleStreamPreservesWordBoundaries(t *testing.T) {
	for _, space := range []struct {
		prefix       string
		width, start int
	}{{"i", 0, 1}, {"ms", 0, 1}, {"mt", 0, 1}, {"c", 3, 0}, {"ref-", 0, 1}} {
		t.Run(space.prefix, func(t *testing.T) {
			table := NewHandleTable(space.prefix, space.width, space.start)
			handle := table.Register("durable-id")
			for _, tc := range []struct{ name, input, want string }{
				{"left-word", "api" + handle, "api" + handle},
				{"left-underscore", "_" + handle, "_" + handle},
				{"left-digit", "7" + handle, "7" + handle},
				{"right-word", handle + "value", handle + "value"},
				{"right-underscore", handle + "_", handle + "_"},
				{"start", handle, "durable-id"},
				{"hyphen-boundary", "x-" + handle + "-y", "x-durable-id-y"},
				{"punctuation", "(" + handle + ")", "(durable-id)"},
				{"unicode", "中文" + handle + "。", "中文durable-id。"},
				{"line-boundary", "x\n" + handle + "\n", "x\ndurable-id\n"},
				{"unknown-extension", handle + "99", handle + "99"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					require.Equal(t, tc.want, table.DecodeKnownText(tc.input), "buffered control")
					assertStreamPartitions(t, tc.input, tc.want, func() textStreamDecoder {
						return NewHandleStreamDecoder(table)
					})
				})
			}
		})
	}
}

func TestRegistryStreamPreservesSourceWordBoundaries(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterKnowledgeBase("kb-id")
	registry.RegisterDocument("doc-id")
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-id", DocumentTitle: "Title"})
	registry.RegisterWeb("https://example.test/page", "Page")
	registry.RegisterIssue("issue-id")
	registry.mcpServers.Register("server-id")
	registry.mcpTools.Register("tool-id")
	for _, handle := range []string{"kb://1", "doc://1", "chunk://1", "url://1", "i1", "ms1", "mt1"} {
		for _, input := range []string{
			"x" + handle, "_" + handle, "7" + handle,
			"x-" + handle + "!", "中文" + handle + "。", "(" + handle + ")",
			handle + "a", handle + "_", handle + "99", handle,
		} {
			t.Run(input, func(t *testing.T) {
				assertStreamPartitions(t, input, registry.DecodeOutputText(input), func() textStreamDecoder {
					return registry.StreamDecoder()
				})
			})
		}
	}
}

func TestHandleStreamKeepsProgressAndResetsAfterFlush(t *testing.T) {
	table := NewHandleTable("i", 0, 1)
	table.Register("issue-id")
	decoder := NewHandleStreamDecoder(table)
	require.Equal(t, "api", decoder.Feed("api"), "ordinary prose must stream without buffering a whole word")
	require.Empty(t, decoder.Feed(""))
	require.Equal(t, "1 ", decoder.Feed("1 "))
	require.Empty(t, decoder.Flush())
	for i := 0; i < 2; i++ {
		require.Equal(t, "x", decoder.Feed("x"))
		require.Empty(t, decoder.Flush())
		require.Empty(t, decoder.Feed("i1"))
		require.Equal(t, "issue-id", decoder.Flush(), fmt.Sprintf("new stream %d", i))
	}
}
