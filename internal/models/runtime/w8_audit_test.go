package runtime

import (
	"encoding/json"
	"testing"

	"github.com/Usir233/GENYEE/internal/models"
	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/models/providers"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveThinkingLevelsAndCapabilitiesInputAreIsolatedFromRegistry(t *testing.T) {
	rt := New()
	val := "minimal"
	rt.Register(&providers.Definition{
		ID:             "iso",
		Name:           "Iso",
		API:            api.APIOpenAICompletions,
		ModelTypes:     []types.ModelType{types.ModelTypeKnowledgeQA},
		ThinkingLevels: api.ThinkingLevelMap{api.ReasoningLow: &val},
	}, models.ModelSpec{
		ID:        "iso-m1",
		Type:      types.ModelTypeKnowledgeQA,
		Reasoning: true,
		Input:     []string{"text", "image"},
	})

	r1, err := rt.Resolve(Ref{Provider: "iso", Model: "iso-m1", ModelType: types.ModelTypeKnowledgeQA})
	require.NoError(t, err)
	require.NotNil(t, r1.ThinkingLevels[api.ReasoningLow])

	// Mutating resolved ThinkingLevels pointer and Capabilities().Input slice must NOT
	// corrupt subsequent resolutions from the shared runtime registry.
	*r1.ThinkingLevels[api.ReasoningLow] = "corrupted"
	caps := r1.Capabilities()
	caps.Input[0] = "corrupted"

	r2, err := rt.Resolve(Ref{Provider: "iso", Model: "iso-m1", ModelType: types.ModelTypeKnowledgeQA})
	require.NoError(t, err)
	assert.Equal(t, "minimal", *r2.ThinkingLevels[api.ReasoningLow])
	assert.Equal(t, []string{"text", "image"}, r2.Spec.Input)
}

func TestLoadOverlayIconRejectsXXEEntityAndScriptPayloads(t *testing.T) {
	// Standard SVG DOCTYPE must remain accepted by looksLikeSVG (per TestLooksLikeSVG).
	stdSVG := `<?xml version="1.0"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "x.dtd"><svg>y</svg>`
	assert.True(t, looksLikeSVG([]byte(stdSVG)))

	for name, badSVG := range map[string]string{
		"xxe entity": `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><svg>&xxe;</svg>`,
		"script tag": `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"javascript": `<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"/></svg>`,
		"onload":     `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadOverlayIcon("", badSVG)
			require.Error(t, err, "unsafe SVG %s must be rejected", name)
		})
	}
}

func TestDecodeCompatAndOverlayRejectTrailingTokensAndMalformedOverrideCompat(t *testing.T) {
	var compat api.OpenAICompletionsCompat
	err := DecodeCompat(json.RawMessage(`{"supports_store":true}{"extra":1}`), &compat)
	require.Error(t, err)

	rt := New()
	err = rt.ApplyOverlay([]byte(`{
		"providers": {
			"openai": {
				"model_overrides": {
					"gpt-4o": {
						"compat": ["not-an-object"]
					}
				}
			}
		}
	}`), "")
	require.Error(t, err)

	var nilResolved *Resolved
	_, err = nilResolved.Endpoint(types.ModelTypeKnowledgeQA, Connection{})
	require.Error(t, err)

	var nilProvider *Provider
	assert.Nil(t, nilProvider.Models())
}
