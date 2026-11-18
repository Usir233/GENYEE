package neo4j

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive_AdversarialCypherInjectionEngine executes adversarial label queries
// directly against the live Neo4j database engine to verify that Cypher parser
// treats all escaped labels as literal identifiers without clause breakout.
func TestLive_AdversarialCypherInjectionEngine(t *testing.T) {
	uri := strings.TrimSpace(os.Getenv("GENYEE_TEST_NEO4J_URI"))
	if uri == "" {
		t.Skip(
			"optional integration test: set GENYEE_TEST_NEO4J_URI, GENYEE_TEST_NEO4J_USER and " +
				"GENYEE_TEST_NEO4J_PASSWORD for an isolated test database",
		)
	}
	user := strings.TrimSpace(os.Getenv("GENYEE_TEST_NEO4J_USER"))
	password := os.Getenv("GENYEE_TEST_NEO4J_PASSWORD")
	require.NotEmpty(t, user, "GENYEE_TEST_NEO4J_USER is required for the integration test")
	require.NotEmpty(t, password, "GENYEE_TEST_NEO4J_PASSWORD is required for the integration test")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver, err := neo4j.NewDriver(uri, neo4j.BasicAuth(user, password, ""))
	require.NoError(t, err)
	defer func() { _ = driver.Close(ctx) }()

	err = driver.VerifyConnectivity(ctx)
	require.NoError(t, err)

	repo := &Repository{nodePrefix: "ENTITY", driver: driver}

	adversarialCases := []struct {
		name        string
		kb          string
		knowledge   string
		description string
	}{
		{
			name:        "Backtick breakout attempt with DETACH DELETE",
			kb:          "kb`) MATCH (m) DETACH DELETE m //",
			knowledge:   "doc1",
			description: "Classic backtick clause breakout payload",
		},
		{
			name:        "Consecutive backticks",
			kb:          "kb```inject```test",
			knowledge:   "sub```label",
			description: "Odd & even consecutive backticks",
		},
		{
			name:        "Cypher keywords and clauses",
			kb:          "MATCH_RETURN_DELETE_WHERE_DETACH_DROP",
			knowledge:   "UNION ALL MATCH (u:User) RETURN u",
			description: "Reserved keywords and UNION statements",
		},
		{
			name:        "Newlines and Carriage Returns",
			kb:          "kb\r\nWHERE 1=1\r\nRETURN n//",
			knowledge:   "doc\nWITH 1 AS x\nDELETE x",
			description: "Multiline injection payload",
		},
		{
			name:        "Semicolon multi-statement injection",
			kb:          "kb; DROP DATABASE neo4j; //",
			knowledge:   "part; CREATE (evil:Admin);",
			description: "Statement separation attempt",
		},
		{
			name:        "APOC and Procedure call injection",
			kb:          "kb` CALL apoc.periodic.iterate(`",
			knowledge:   "doc` CALL apoc.export.csv.all(`",
			description: "APOC procedure invocation attempt",
		},
		{
			name:        "Colons and delimiter injection",
			kb:          "Label1:Label2:Label3",
			knowledge:   "admin:root:wheel",
			description: "Multiple label delimiters inside payload",
		},
		{
			name:        "Special characters, quotes, and brackets",
			kb:          `kb"'{}[],.!@#$%^&*()-+=<>?/\`,
			knowledge:   `doc{}[]()`,
			description: "Broad punctuation and special characters",
		},
		{
			name:        "Unicode homoglyphs",
			kb:          "kb｀acute´single‘quote’double“quote”",
			knowledge:   "unicode_kb_тест_中文",
			description: "Unicode lookalikes and non-ASCII",
		},
	}

	session := driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer func() { _ = session.Close(ctx) }()

	for _, tc := range adversarialCases {
		t.Run(tc.name, func(t *testing.T) {
			ns := types.NameSpace{
				KnowledgeBase: tc.kb,
				Knowledge:     tc.knowledge,
			}
			labelExpr := repo.Label(ns)
			require.NotEmpty(t, labelExpr)

			// 1. Verify Node Creation query with adversarial label succeeds on live Neo4j
			testNodeName := fmt.Sprintf("adv_test_node_%d", time.Now().UnixNano())
			testKGID := fmt.Sprintf("adv_kg_%d", time.Now().UnixNano())

			createQuery := fmt.Sprintf("CREATE (n:%s {name: $name, kg: $kg}) RETURN n.name as name", labelExpr)
			createRes, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
				res, err := tx.Run(ctx, createQuery, map[string]interface{}{
					"name": testNodeName,
					"kg":   testKGID,
				})
				if err != nil {
					return nil, err
				}
				if res.Next(ctx) {
					record := res.Record()
					val, _ := record.Get("name")
					return val, nil
				}
				return nil, res.Err()
			})
			require.NoError(t, err, "Live Neo4j query failed for payload: %s (Query: %s)", tc.description, createQuery)
			assert.Equal(t, testNodeName, createRes)

			// 2. Verify graphSearchCypher syntax on live Neo4j engine
			searchQuery, searchParams := graphSearchCypher(labelExpr, []string{testNodeName})
			_, err = session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
				res, err := tx.Run(ctx, searchQuery, searchParams)
				if err != nil {
					return nil, err
				}
				count := 0
				for res.Next(ctx) {
					count++
				}
				return count, res.Err()
			})
			assert.NoError(t, err, "graphSearchCypher query failed on live Neo4j for: %s", tc.description)

			// 3. Clean up the created test node using safely escaped label
			delQuery := fmt.Sprintf("MATCH (n:%s {kg: $kg}) DETACH DELETE n", labelExpr)
			_, err = session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
				_, err := tx.Run(ctx, delQuery, map[string]interface{}{"kg": testKGID})
				return nil, err
			})
			assert.NoError(t, err, "Del query failed for: %s", tc.description)
		})
	}
}
