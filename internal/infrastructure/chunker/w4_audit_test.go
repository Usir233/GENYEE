package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeuristicSplitterOverlapProgressAndSizeBounds(t *testing.T) {
	sec1 := "1. Introduction\n" + strings.Repeat("a", 83) + "\n"
	sec2 := "2. Methods\n" + strings.Repeat("b", 78) + "\n"
	sec3 := "3. Results\n" + strings.Repeat("c", 68) + "\n"
	doc := sec1 + sec2 + sec3

	cfg := SplitterConfig{
		ChunkSize:    120,
		ChunkOverlap: 60,
		Strategy:     StrategyHeuristic,
	}
	chunks := Split(doc, cfg)
	require.GreaterOrEqual(t, len(chunks), 3)

	for i, c := range chunks {
		runeCount := utf8.RuneCountInString(c.Content)
		assert.LessOrEqual(t, runeCount, cfg.ChunkSize,
			"chunk %d exceeded ChunkSize (%d > %d)", i, runeCount, cfg.ChunkSize)
		if i > 0 {
			assert.Greater(t, c.Start, chunks[i-1].Start,
				"chunk %d Start (%d) must strictly advance past chunk %d Start (%d)",
				i, c.Start, i-1, chunks[i-1].Start)
		}
	}

	// Direct invocation with unnormalized ChunkSize <= 0 or ChunkOverlap >= ChunkSize
	// must also terminate with strictly advancing starts and positive default budget.
	direct := splitByHeuristicsImpl(doc, SplitterConfig{
		ChunkSize:    0,
		ChunkOverlap: 999,
	}, nil)
	require.NotEmpty(t, direct)
	for i, c := range direct {
		assert.LessOrEqual(t, utf8.RuneCountInString(c.Content), DefaultChunkSize)
		if i > 0 {
			assert.Greater(t, c.Start, direct[i-1].Start)
		}
	}
}

func TestHeuristicBoundariesCommonMarkCodeFences(t *testing.T) {
	doc := strings.Join([]string{
		"Preamble paragraph.",
		"~~~",
		"1. Fake Section Inside Tilde Fence",
		"CHAPTER 1: FAKE CHAPTER",
		"~~~",
		"Inline fence line: ```echo hello```",
		"    ```",
		"1. Real Section One",
		"````markdown",
		"```",
		"2. Fake Section Still Inside Quad Fence",
		"````",
		"3. Real Section Three",
	}, "\n")

	bounds := findHeuristicBoundaries(doc, []string{LangEnglish})
	var matchedLines []string
	runes := []rune(doc)
	for _, b := range bounds {
		rem := string(runes[b.runeStart:])
		line, _, _ := strings.Cut(rem, "\n")
		matchedLines = append(matchedLines, line)
	}

	assert.Contains(t, matchedLines, "1. Real Section One")
	assert.Contains(t, matchedLines, "3. Real Section Three")
	assert.NotContains(t, matchedLines, "1. Fake Section Inside Tilde Fence")
	assert.NotContains(t, matchedLines, "CHAPTER 1: FAKE CHAPTER")
	assert.NotContains(t, matchedLines, "2. Fake Section Still Inside Quad Fence")
}

func TestChunkerUTF8BoundaryAndInvalidByteSanitization(t *testing.T) {
	// Malformed UTF-8 bytes from upstream parsers must never leak into Chunk.Content
	// or Chunk.ContextHeader (which would fail PostgreSQL UTF8 TEXT inserts).
	rawProse := "Hello \xff\xfe world with invalid UTF-8 bytes."
	for _, c := range SplitText(rawProse, DefaultConfig()) {
		assert.True(t, utf8.ValidString(c.Content), "SplitText emitted invalid UTF-8 Content: %q", c.Content)
		assert.True(t, utf8.ValidString(c.ContextHeader), "SplitText emitted invalid UTF-8 ContextHeader: %q", c.ContextHeader)
	}

	rawHeadings := strings.Join([]string{
		"# Chapter One \xff Invalid Header Byte",
		strings.Repeat("First chapter prose sentence. ", 20),
		"# Chapter Two \xfe Another Header",
		strings.Repeat("Second chapter prose \x80 sentence. ", 20),
	}, "\n\n")

	for _, strat := range []string{StrategyAuto, StrategyHeading, StrategyHeuristic, StrategyLegacy} {
		chunks := Split(rawHeadings, SplitterConfig{
			ChunkSize:    180,
			ChunkOverlap: 20,
			Strategy:     strat,
		})
		require.NotEmpty(t, chunks, "strategy %s produced no chunks", strat)
		for i, c := range chunks {
			assert.True(t, utf8.ValidString(c.Content),
				"strategy %s chunk %d Content has invalid UTF-8", strat, i)
			assert.True(t, utf8.ValidString(c.ContextHeader),
				"strategy %s chunk %d ContextHeader has invalid UTF-8", strat, i)
		}
	}

	res := SplitParentChild(rawHeadings, SplitterConfig{
		ChunkSize: 300,
		Strategy:  StrategyHeading,
	}, SplitterConfig{
		ChunkSize: 120,
		Strategy:  StrategyHeading,
	})
	for i, p := range res.Parents {
		assert.True(t, utf8.ValidString(p.Content), "parent %d invalid UTF-8", i)
	}
	for i, ch := range res.Children {
		assert.True(t, utf8.ValidString(ch.Content), "child %d invalid UTF-8", i)
		assert.True(t, utf8.ValidString(ch.ContextHeader), "child %d header invalid UTF-8", i)
	}

	// Verify multi-byte UTF-8 input crossing 4096-byte and 512-byte sample cutoffs
	// profiles and detects language cleanly.
	cjkCrossing4096 := strings.Repeat("中", 1366) // 4098 bytes; byte 4096 falls mid-rune
	prof := ProfileDocument(cjkCrossing4096)
	assert.Contains(t, prof.DetectedLangs, LangChinese)

	umlautCrossing512 := strings.Repeat("a", 511) + "ä" + " der Mann und die Frau"
	assert.Equal(t, LangGerman, DetectLanguage(umlautCrossing512))
}
