package neo4j

import (
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseCypherLabels parses a colon-separated sequence of backtick-quoted Cypher labels.
// It verifies that:
// 1. Every label starts with ` and ends with `
// 2. Colons separating labels only occur OUTSIDE backticks
// 3. No unquoted characters exist outside backticks except the separating colons
// 4. Any backtick inside a quoted label is properly escaped as ``
func parseCypherLabels(t *testing.T, labelExpr string) []string {
	var tokens []string
	var current strings.Builder
	inBackticks := false

	chars := []rune(labelExpr)
	i := 0
	for i < len(chars) {
		ch := chars[i]
		if inBackticks {
			if ch == '`' {
				if i+1 < len(chars) && chars[i+1] == '`' {
					// Escaped backtick: ``
					current.WriteRune('`')
					current.WriteRune('`')
					i += 2
					continue
				}
				// Closing backtick
				current.WriteRune('`')
				inBackticks = false
				tokens = append(tokens, current.String())
				current.Reset()
				i++
				continue
			}
			current.WriteRune(ch)
			i++
		} else {
			if ch == '`' {
				inBackticks = true
				current.WriteRune('`')
				i++
			} else if ch == ':' {
				// Colon outside backtick separates labels
				i++
			} else {
				t.Fatalf("Syntax error: unquoted character %q outside backticks in labelExpr: %s", ch, labelExpr)
			}
		}
	}

	require.False(t, inBackticks, "Unclosed backtick in label expression: %s", labelExpr)
	return tokens
}

// TestBE01_AdversarialCypherLabelInjection stress-tests Neo4j label construction
// against a wide array of adversarial injection payloads.
func TestBE01_AdversarialCypherLabelInjection(t *testing.T) {
	repo := &Neo4jRepository{nodePrefix: "ENTITY"}

	adversarialPayloads := []struct {
		name        string
		kb          string
		knowledge   string
		description string
	}{
		{
			name:        "Basic backtick escape",
			kb:          "test`label",
			knowledge:   "",
			description: "Single embedded backtick",
		},
		{
			name:        "Cypher clause breakout via backtick",
			kb:          "kb`) MATCH (m) DETACH DELETE m //",
			knowledge:   "",
			description: "Classic Cypher breakout attempt",
		},
		{
			name:        "Multiple consecutive backticks",
			kb:          "kb``` injection ```--",
			knowledge:   "",
			description: "Odd number of backticks to break escaping parity",
		},
		{
			name:        "Semicolon query chaining attempt",
			kb:          "kb; DROP DATABASE neo4j; //",
			knowledge:   "",
			description: "Multi-statement injection payload",
		},
		{
			name:        "Union All injection",
			kb:          "x` UNION ALL MATCH (admin:User) RETURN admin //",
			knowledge:   "",
			description: "UNION injection payload",
		},
		{
			name:        "Backslash and escape tricks",
			kb:          `kb\` + "`" + `) DELETE (n) //`,
			knowledge:   "",
			description: "Backslash preceding backtick",
		},
		{
			name:        "Punctuation and sub-labels via colons",
			kb:          "admin:root:superuser",
			knowledge:   "read:write:exec",
			description: "Colons inside label payload",
		},
		{
			name:        "Newlines and whitespace carriage return",
			kb:          "kb\r\nWHERE 1=1\r\nRETURN n//",
			knowledge:   "doc\nDELETE",
			description: "Newline and CR line break injection",
		},
		{
			name:        "Null byte and control chars",
			kb:          "kb\x00`DELETE",
			knowledge:   "doc\x1b[31m",
			description: "Null bytes and terminal escape codes",
		},
		{
			name:        "Unicode homoglyph backticks and quotes",
			kb:          "kb｀acute´single‘quote’double“quote”",
			knowledge:   "",
			description: "Unicode lookalike quotes",
		},
		{
			name:        "Extreme 10KB payload with alternating backticks",
			kb:          strings.Repeat("`kb-payload-inject`", 500),
			knowledge:   strings.Repeat("`doc-inject`", 500),
			description: "Buffer stress with thousands of backticks",
		},
	}

	for _, tc := range adversarialPayloads {
		t.Run(tc.name, func(t *testing.T) {
			ns := types.NameSpace{
				KnowledgeBase: tc.kb,
				Knowledge:     tc.knowledge,
			}

			label := repo.Label(ns)
			require.NotEmpty(t, label, "Label must not be empty for non-empty namespace")

			// 1. Verify every sub-label is properly quoted and parsed
			tokens := parseCypherLabels(t, label)
			expectedCount := 0
			if tc.kb != "" {
				expectedCount++
			}
			if tc.knowledge != "" {
				expectedCount++
			}
			assert.Equal(t, expectedCount, len(tokens), "Each namespace component must yield exactly one quoted label")

			for idx, part := range tokens {
				assert.True(t, strings.HasPrefix(part, "`"), "Label part %d must start with backtick: %s", idx, part)
				assert.True(t, strings.HasSuffix(part, "`"), "Label part %d must end with backtick: %s", idx, part)

				// Strip outer backticks and verify all inner backticks are escaped as double-backticks
				inner := part[1 : len(part)-1]
				remainingBackticks := strings.ReplaceAll(inner, "``", "")
				assert.False(t, strings.Contains(remainingBackticks, "`"),
					"Unescaped single backtick found in label part %d: %s (from payload: %s)", idx, part, tc.description)
			}

			// 2. Test query generation in graphSearchCypher
			query, _ := graphSearchCypher(label, []string{"seed"})
			assert.True(t, strings.Contains(query, "MATCH (n:"+label+")"),
				"graphSearchCypher must contain properly escaped label")
			assert.True(t, strings.Contains(query, "MATCH (n)-[r]-(m:"+label+")"),
				"graphSearchCypher must contain properly escaped relationship label")

			// 3. Verify Cypher syntax integrity:
			totalBackticks := strings.Count(query, "`")
			assert.Equal(t, 0, totalBackticks%2, "Query backtick count must be even (no unclosed backticks)")
		})
	}
}

// TestBE01_ApocPeriodicIterateDelGraphStringEscaping tests DelGraph query string interpolation
func TestBE01_ApocPeriodicIterateDelGraphStringEscaping(t *testing.T) {
	repo := &Neo4jRepository{nodePrefix: "ENTITY"}

	maliciousNS := types.NameSpace{
		KnowledgeBase: `test" {kg: ""}) DETACH DELETE n; //`,
		Knowledge:     `doc' UNION ALL MATCH (n) RETURN n; //`,
	}

	label := repo.Label(maliciousNS)

	// DelGraph constructs:
	deleteRelsInner := `MATCH (n:` + label + ` {kg: $knowledge_id})-[r]-(m:` + label + ` {kg: $knowledge_id}) RETURN r`
	deleteNodesInner := `MATCH (n:` + label + ` {kg: $knowledge_id}) RETURN n`

	for _, inner := range []string{deleteRelsInner, deleteNodesInner} {
		assert.Equal(t, 0, strings.Count(inner, "`")%2, "Inner APOC query must maintain backtick parity")
		assert.Contains(t, inner, `{kg: $knowledge_id})`, "Node pattern closing paren must come after properties")
	}
}
