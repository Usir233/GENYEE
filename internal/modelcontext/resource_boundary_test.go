package modelcontext

import (
	"fmt"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResourceHandlesRejectUnregisteredNumericExtensions(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	for _, handle := range []string{"res://00010", "res://00010001"} {
		t.Run(handle, func(t *testing.T) {
			registry := NewRegistry(true)
			require.Equal(t, "res://0001", registry.CompactKnownText(ref))
			calls := []types.LLMToolCall{{Function: types.FunctionCall{
				Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, handle),
			}}}
			registry.DecodeToolCalls(calls)
			require.Equal(t, []string{handle}, calls[0].UnresolvedHandles,
				"an unknown handle must not resolve to a registered shorter prefix")
			require.Equal(t, ArgumentResolutionUnresolved, calls[0].ArgumentResolution)
			require.JSONEq(t, fmt.Sprintf(`{"path":%q}`, handle), calls[0].Function.Arguments)
		})
	}
}

func TestResourceOutputNumericBoundaries(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	for _, tc := range []struct{ name, text, want string }{
		{"unknown-extension", "before res://00010 after", "before  after"},
		{"unknown-at-end", "res://00010", ""},
		{"known-at-end", "res://0001", ref},
		{"markdown-image", "![a](res://0001)", "![a](" + ref + ")"},
		{"wiki-link", "[[res://0001|title]]", "[[" + ref + "|title]]"},
		{"adjacent-handles", "res://0001res://0001", ref + ref},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry(true)
			require.Equal(t, "res://0001", registry.CompactKnownText(ref))
			t.Run("buffered", func(t *testing.T) {
				require.Equal(t, tc.want, registry.DecodeOutputText(tc.text))
			})
			for split := 0; split <= len(tc.text); split++ {
				t.Run(fmt.Sprintf("split-%d", split), func(t *testing.T) {
					decoder := registry.StreamDecoder()
					got := decoder.Feed(tc.text[:split]) + decoder.Feed(tc.text[split:]) + decoder.Flush()
					require.Equal(t, tc.want, got)
				})
			}
			t.Run("bytewise", func(t *testing.T) {
				decoder := registry.StreamDecoder()
				var got string
				for i := range tc.text {
					got += decoder.Feed(tc.text[i : i+1])
				}
				got += decoder.Flush()
				require.Equal(t, tc.want, got)
			})
		})
	}
}

func TestResourceStreamWaitsForCompleteNumericHandle(t *testing.T) {
	registry := NewRegistry(true)
	// Sequential handles grow beyond their minimum four-digit width. Model
	// output may split a registered five-digit handle after its first four.
	registry.resources.table.next = 1000
	require.Equal(t, "res://1000", registry.CompactKnownText("local://short.png"))
	registry.resources.table.next = 10000
	require.Equal(t, "res://10000", registry.CompactKnownText("local://long.png"))
	decoder := registry.StreamDecoder()
	require.Empty(t, decoder.Feed("res://1000"), "a numeric suffix may still grow")
	require.Equal(t, "local://long.png!", decoder.Feed("0!"))
	require.Empty(t, decoder.Flush())
}
