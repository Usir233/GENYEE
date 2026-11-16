package providers

import (
	"testing"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefinitionNilReceiverSafetyAndCredentialLabelIsolation(t *testing.T) {
	var nilDef *Definition
	assert.Equal(t, "", nilDef.GetDefaultURL(types.ModelTypeKnowledgeQA))
	assert.False(t, nilDef.SupportsType(types.ModelTypeKnowledgeQA))
	assert.Equal(t, "", nilDef.LocalizedName("zh-CN"))
	assert.Equal(t, "", nilDef.LocalizedDescription("zh-CN"))
	assert.Nil(t, nilDef.CredentialLabelFor(types.ModelTypeRerank))
	assert.Equal(t, AuthStyle(""), nilDef.AuthStyleFor(api.APIOpenAICompletions))
	require.NotNil(t, nilDef.AuthFunc(api.APIOpenAICompletions, api.Credentials{APIKey: "k"}))
	require.Error(t, nilDef.ValidateConfig(&Config{ModelName: "m"}))

	def := &Definition{
		ID:   "test",
		Name: "Test",
		CredentialLabels: []CredentialLabel{{
			Label:      "SecretId",
			Labels:     map[string]string{"zh-CN": "密钥 ID"},
			ModelTypes: []types.ModelType{types.ModelTypeRerank},
		}},
	}
	lbl := def.CredentialLabelFor(types.ModelTypeRerank)
	require.NotNil(t, lbl)
	lbl.Label = "Mutated"
	lbl.Labels["zh-CN"] = "Mutated"
	lbl.ModelTypes[0] = types.ModelTypeEmbedding

	fresh := def.CredentialLabelFor(types.ModelTypeRerank)
	require.NotNil(t, fresh)
	assert.Equal(t, "SecretId", fresh.Label)
	assert.Equal(t, "密钥 ID", fresh.Labels["zh-CN"])
}
