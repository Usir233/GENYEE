package openaicompletions

import (
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDSMLFilterBoundsUnclosedHeldBuffer(t *testing.T) {
	opts := &api.Options{
		Tools: []api.Tool{{
			Type:     "function",
			Function: api.FunctionDef{Name: "search_knowledge"},
		}},
	}
	f := dsmlFilter{options: opts}

	// Start a DSML marker that never closes, followed by > 1 MiB of plain output.
	assert.Equal(t, "before ", f.Feed("before <｜DSML｜function_calls>\n"))
	chunk := strings.Repeat("x", 64<<10)
	var released int
	for range 20 { // 1.25 MiB > 1 MiB cap
		released += len(f.Feed(chunk))
	}
	rest, calls := f.Flush(true)
	assert.Empty(t, calls)
	assert.Greater(t, released, 0, "oversized unclosed DSML buffer must stop holding unboundedly and stream through")
	assert.Equal(t, len("<｜DSML｜function_calls>\n")+20*len(chunk), released+len(rest))
}

func TestChatNilContextAndSecretRedactionInRequestCreationError(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@api.openai.com/v1\x7f",
			Model:   "gpt-4o",
			Query:   map[string]string{"api_key": "sk-secret-completions-999"},
		},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	_, err := c.Chat(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-secret-completions-999")

	//nolint:staticcheck // verifying defensive nil-context handling
	_, err = c.ChatStream(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-secret-completions-999")
}
