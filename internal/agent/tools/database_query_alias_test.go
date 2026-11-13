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

func TestDatabaseQueryRejectsRenamedConfigColumnBeforeExecution(t *testing.T) {
	// The empty isolated database also ensures rejection never needs a query
	// to inspect real credentials or infer the table's physical column order.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	tool := NewDatabaseQueryTool(db, types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "own-kb", TenantID: 1,
	}})
	input, err := json.Marshal(DatabaseQueryInput{SQL: `SELECT kb.description FROM knowledge_bases AS kb (id,name,original_description,tenant_id,chunking_config,image_processing_config,embedding_model_id,summary_model_id,rerank_model_id,description,vlm_config,extract_config)`})
	require.NoError(t, err)
	result, err := tool.Execute(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)), input)
	require.ErrorContains(t, err, "Statement validation failed")
	require.NotNil(t, result)
	require.Contains(t, result.Error, "SQL validation failed")
	require.False(t, result.Success)
	require.Empty(t, result.Data)
}
