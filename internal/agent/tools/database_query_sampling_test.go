package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDatabaseQueryRejectsUnfilteredTableSample(t *testing.T) {
	// SQLite intentionally has no tables: unsupported sampling must fail in
	// validation, before the database sees the query. The accepted SQL was also
	// executed against an isolated PostgreSQL fixture during the audit.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	tool := NewDatabaseQueryTool(db, types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "filled", TenantID: 1,
	}})
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	for _, query := range []string{
		"SELECT count(*) FROM chunks TABLESAMPLE SYSTEM(100)",
		"SELECT count(*) FROM chunks TABLESAMPLE SYSTEM(100), knowledge_bases kb",
		"SELECT count(*) FROM knowledge_bases kb JOIN chunks c TABLESAMPLE BERNOULLI(100) ON true",
	} {
		t.Run(query, func(t *testing.T) {
			input, err := json.Marshal(DatabaseQueryInput{SQL: query})
			require.NoError(t, err)
			result, err := tool.Execute(ctx, input)
			require.Error(t, err)
			require.Contains(t, result.Error, "SQL validation failed")
			require.False(t, result.Success)
			require.Empty(t, result.Data)
		})
	}
}
