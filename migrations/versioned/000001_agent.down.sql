BEGIN;

DROP INDEX IF EXISTS idx_knowledges_summary_status;
ALTER TABLE knowledges DROP COLUMN IF EXISTS summary_status;

ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS question_generation_config;

ALTER TABLE users
    DROP COLUMN IF EXISTS can_access_all_tenants;

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS rerank_model_id VARCHAR(64);

UPDATE models
SET tenant_id = 0
FROM knowledges k
WHERE models.id = k.id
  AND models.tenant_id = k.tenant_id;

DROP INDEX IF EXISTS idx_chunks_content_hash;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS content_hash;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS status;

DROP INDEX IF EXISTS idx_chunks_tag;
ALTER TABLE chunks DROP COLUMN IF EXISTS tag_id;

DROP INDEX IF EXISTS idx_knowledges_tag;
ALTER TABLE knowledges DROP COLUMN IF EXISTS tag_id;

DROP INDEX IF EXISTS idx_knowledge_tags_kb_name;
DROP INDEX IF EXISTS idx_knowledge_tags_kb;
DROP TABLE IF EXISTS knowledge_tags;

ALTER TABLE chunks
    DROP COLUMN IF EXISTS metadata;

ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS faq_config,
    DROP COLUMN IF EXISTS type;

DROP INDEX IF EXISTS idx_models_is_builtin;

ALTER TABLE models
    DROP COLUMN IF EXISTS is_builtin;

ALTER TABLE mcp_services
    DROP CONSTRAINT IF EXISTS chk_mcp_transport_config;

ALTER TABLE mcp_services
    DROP COLUMN IF EXISTS env_vars,
    DROP COLUMN IF EXISTS stdio_config;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS web_search_config;

DROP TRIGGER IF EXISTS trigger_mcp_services_updated_at ON mcp_services;
DROP FUNCTION IF EXISTS update_mcp_services_updated_at();

DROP INDEX IF EXISTS idx_mcp_services_tenant_id;
DROP INDEX IF EXISTS idx_mcp_services_enabled;
DROP INDEX IF EXISTS idx_mcp_services_deleted_at;
DROP TABLE IF EXISTS mcp_services;

ALTER TABLE auth_tokens DROP CONSTRAINT IF EXISTS fk_auth_tokens_user;
ALTER TABLE users DROP CONSTRAINT IF EXISTS fk_users_tenant;

DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS users;

ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS is_temporary;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS context_config;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS conversation_config;

DROP INDEX IF EXISTS idx_messages_agent_steps;
DROP INDEX IF EXISTS idx_sessions_context_config;
DROP INDEX IF EXISTS idx_sessions_agent_config;
DROP INDEX IF EXISTS idx_tenants_agent_config;

COMMIT;
