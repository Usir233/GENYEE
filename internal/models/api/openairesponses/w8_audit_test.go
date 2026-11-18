package openairesponses

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNonStreamIncompleteResponseWithoutDetailsDoesNotReportToolCalls(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "resp_trunc",
			"status": "incomplete",
			"incomplete_details": null,
			"output": [
				{
					"type": "function_call",
					"id": "fc_1",
					"call_id": "call_1",
					"name": "delete_file",
					"arguments": "{\"path\":\"/tmp"
				}
			]
		}`))
	}))
	defer srv.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: srv.URL + "/v1", Model: "gpt-5"},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	got, err := c.Chat(nil, []api.Message{{Role: "user", Content: "go"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "length", got.FinishReason, "truncated response must never report finish_reason=tool_calls")
}

func TestSystemMessageMultiContentPreservedInInstructions(t *testing.T) {
	c := New(Config{Endpoint: api.Endpoint{Model: "gpt-5"}})
	body, err := c.BuildRequestBody([]api.Message{
		{
			Role: "system",
			MultiContent: []api.MessageContentPart{
				{Type: "text", Text: "Follow policy A."},
				{Type: "text", Text: "Follow policy B."},
			},
		},
		{Role: "user", Content: "hello"},
	}, nil, false)
	require.NoError(t, err)
	assert.Equal(t, "Follow policy A.\nFollow policy B.", body["instructions"])
}

func TestChatRedactsSecretsInRequestCreationError(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@api.openai.com/v1\x7f",
			Model:   "gpt-5",
			Query:   map[string]string{"api_key": "sk-secret-openai-999"},
		},
	})
	//nolint:staticcheck // verifying defensive nil-context handling
	_, err := c.Chat(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-secret-openai-999")
}
