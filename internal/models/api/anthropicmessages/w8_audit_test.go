package anthropicmessages

import (
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatNilContextAndSecretRedactionInRequestCreationError(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://user:supersecretpw@api.anthropic.com\x7f",
			Model:   "claude-sonnet-4-5",
			Query:   map[string]string{"api_key": "sk-ant-secret-key-999"},
		},
	})
	_, err := c.Chat(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-ant-secret-key-999")

	_, err = c.ChatStream(nil, []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecretpw")
	assert.NotContains(t, err.Error(), "sk-ant-secret-key-999")
}

func TestURLPreservesEndpointQueryParameters(t *testing.T) {
	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://gateway.example.com/anthropic/v1",
			Query:   map[string]string{"api-version": "2024-10-01"},
		},
	})
	assert.Equal(t, "https://gateway.example.com/anthropic/v1/messages?api-version=2024-10-01", c.URL())
}

func TestImageSourceForRejectsOversizedBase64BeforeAllocating(t *testing.T) {
	// > 20 MiB decoded size must be rejected before base64.StdEncoding.DecodeString allocates.
	hugeB64 := strings.Repeat("A", ((20<<20)/3+4)*4)
	assert.Nil(t, imageSourceFor("data:image/png;base64,"+hugeB64))
}
