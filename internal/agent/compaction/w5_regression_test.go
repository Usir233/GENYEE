package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/models/chat"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancellingChat struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancellingChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, context.Canceled
}

func (c *cancellingChat) ChatStream(
	ctx context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	c.calls++
	if c.cancel != nil {
		c.cancel()
	}
	return nil, ctx.Err()
}

func (c *cancellingChat) GetModelName() string { return "cancelling" }
func (c *cancellingChat) GetModelID() string   { return "cancelling" }

func TestCompactPropagatesContextCancellation(t *testing.T) {
	est := newEstimator(t)
	msgs := withStoredHistory(reactTurn(12), storedTurn("turn-a", 1, "/workspace/a.txt"))

	t.Run("already cancelled before Compact", func(t *testing.T) {
		llm := &stubChat{response: "## Goal\nsummary"}
		c := New(llm, est, testSettings())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		result, err := c.Compact(ctx, msgs, ReasonThreshold)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, result)
		assert.Zero(t, llm.calls, "cancelled turn must not invoke summarizer")
	})

	t.Run("cancelled during history summarization does not degrade or call split-turn summarizer", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		llm := &cancellingChat{cancel: cancel}
		c := New(llm, est, testSettings())

		result, err := c.Compact(ctx, msgs, ReasonThreshold)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, result, "cancelled compaction must not return a degraded raw-archive checkpoint")
		assert.Equal(t, 1, llm.calls, "split-turn prefix summarization must not start after parent cancellation")
	})
}

func TestSplitTurnFallbackAndToolCallSerializationRespectBudgets(t *testing.T) {
	est := newEstimator(t)
	settings := Settings{
		Enabled:          true,
		MaxContextTokens: 20000,
		ReserveTokens:    8000,
		KeepRecentTokens: 1500,
	}

	// Build a single long turn whose discarded prefix ends on an assistant step
	// with hundreds of tool calls so both serializeConversation and split-turn
	// fallback are exercised against their respective token budgets.
	heavyAssistant := chat.Message{
		Role:    "assistant",
		Content: "Dispatched batch inspection.",
	}
	for i := range 500 {
		heavyAssistant.ToolCalls = append(heavyAssistant.ToolCalls, chat.ToolCall{
			ID:   fmt.Sprintf("heavy-%d", i),
			Type: "function",
			Function: chat.FunctionCall{
				Name:      "read_file",
				Arguments: fmt.Sprintf(`{"path":"/workspace/资料_%03d.csv","offset":%d}`, i, i*10),
			},
		})
	}
	serialized := serializeConversation([]chat.Message{heavyAssistant})
	assert.LessOrEqual(t, len([]rune(serialized)), textMaxChars*3,
		"single assistant step with many tool calls must remain bounded in transcript")

	msgs := []chat.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "audit all CSV files"},
		heavyAssistant,
	}
	for i := range 500 {
		msgs = append(msgs, chat.Message{
			Role:       "tool",
			Name:       "read_file",
			ToolCallID: fmt.Sprintf("heavy-%d", i),
			Content:    "ok",
		})
	}
	// Add a second huge assistant call step right before the kept tail so the
	// newest message in TurnPrefixMessages itself has 500 tool calls.
	newestPrefixAssistant := heavyAssistant
	newestPrefixAssistant.Content = "Latest prefix batch completed."
	newestPrefixAssistant.ToolCalls = nil
	for i := range 500 {
		newestPrefixAssistant.ToolCalls = append(newestPrefixAssistant.ToolCalls, chat.ToolCall{
			ID:   fmt.Sprintf("heavy2-%d", i),
			Type: "function",
			Function: chat.FunctionCall{
				Name:      "read_file",
				Arguments: fmt.Sprintf(`{"path":"/workspace/最新资料_%03d.csv"}`, i),
			},
		})
	}
	msgs = append(msgs, newestPrefixAssistant)
	for i := range 500 {
		msgs = append(msgs, chat.Message{
			Role:       "tool",
			Name:       "read_file",
			ToolCallID: fmt.Sprintf("heavy2-%d", i),
			Content:    "ok",
		})
	}
	// Small tail that fits KeepRecentTokens.
	msgs = append(msgs,
		chat.Message{Role: "assistant", Content: strings.Repeat("tail ", 80)},
	)

	c := New(&stubChat{err: assert.AnError}, est, settings)
	prep := Prepare(msgs, c.Settings(), est)
	require.NotNil(t, prep)
	require.True(t, prep.IsSplitTurn)

	combined, degraded := c.appendTurnPrefix(context.Background(), prep, "Prior history.")
	require.True(t, degraded)
	_, prefix, ok := strings.Cut(combined, splitTurnSeparator)
	require.True(t, ok)
	assert.LessOrEqual(t, est.EstimateString(prefix), c.Settings().turnPrefixBudget(),
		"degraded split-turn prefix archive must stay within turnPrefixBudget")
	assert.True(t, utf8.ValidString(prefix))
}

func TestSettingsNormalizeClampsKeepRecentOnSmallWindows(t *testing.T) {
	for _, window := range []int{8192, 16384} {
		s := Settings{
			Enabled:          true,
			MaxContextTokens: window,
			ReserveTokens:    DefaultReserveTokens,
		}.Normalize()

		assert.Less(t, s.ReserveTokens, window, "window=%d reserve must leave usable context", window)
		assert.Greater(t, s.Threshold(), 1000, "window=%d threshold must not collapse to 1 token", window)
		assert.LessOrEqual(t, s.KeepRecentTokens, s.Threshold()/4,
			"window=%d keep-recent budget must be clamped below threshold", window)
		assert.False(t, s.ShouldCompact(100), "window=%d tiny conversation must not trigger compaction", window)
	}

	tiny := Settings{
		Enabled:          true,
		MaxContextTokens: 100,
		ReserveTokens:    98,
	}.Normalize()
	assert.LessOrEqual(t, tiny.KeepRecentTokens, tiny.Threshold(),
		"keep-recent must not remain at DefaultKeepRecentTokens when usable/4 == 0")
}
