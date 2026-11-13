package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLSecurityRejectsRelationColumnRenaming(t *testing.T) {
	for _, query := range []string{
		// cos_config (the tenth physical column) becomes an allowed label.
		`SELECT kb.description FROM knowledge_bases AS kb (id,name,original_description,tenant_id,chunking_config,image_processing_config,embedding_model_id,summary_model_id,rerank_model_id,description,vlm_config,extract_config)`,
		`SELECT title FROM knowledges AS k (id,original_owner,knowledge_base_id,type,title,description,source,parse_status,enable_status,embedding_model_id,file_name,file_type,tenant_id)`,
		`SELECT c.content FROM chunks AS c (id,tenant_id,knowledge_base_id,knowledge_id,original_content,chunk_index,is_enabled,start_at,end_at,pre_chunk_id,next_chunk_id,chunk_type,parent_chunk_id,content)`,
	} {
		t.Run(query, func(t *testing.T) {
			secured, validation, err := ValidateAndSecureSQL(query, WithSecurityDefaults(1))
			require.Error(t, err)
			require.False(t, validation.Valid)
			require.Empty(t, secured)
		})
	}
}

func TestSQLColumnRenamingCannotChangeInjectedPredicateMeaning(t *testing.T) {
	for _, option := range []SQLValidationOption{
		WithTenantIsolation(1), WithSoftDeleteFilter(), WithHiddenKBFilter(),
		WithChunkEnabledFilter(), WithSearchScopeFilter([]string{"kb"}, nil),
		WithSearchScopes([]SearchScope{{TenantID: 1, KnowledgeBaseID: "kb"}}),
	} {
		_, validation := ValidateSQL("SELECT id FROM chunks AS c (id,other_owner)", option)
		require.False(t, validation.Valid)
	}
}

func TestSQLOrdinaryAliasesRemainSupported(t *testing.T) {
	secured, validation, err := ValidateAndSecureSQL(
		`SELECT "My KB".description AS public_description FROM knowledge_bases AS "My KB"`,
		WithSecurityDefaults(1), WithSearchScopes([]SearchScope{{TenantID: 1, KnowledgeBaseID: "kb"}}),
	)
	require.NoError(t, err)
	require.True(t, validation.Valid)
	require.Contains(t, secured, `"My KB".tenant_id = 1`)
	require.Contains(t, secured, "AS public_description")
}
