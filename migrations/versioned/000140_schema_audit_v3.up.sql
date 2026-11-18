-- Migration: 000140_schema_audit_v3
-- 1. Rename sessions.agent_config -> sessions.last_request_state to match Session.LastRequestState.
-- 2. Align messages.attachments and messages.is_fallback NOT NULL + DEFAULT with SQLite.
-- 3. Convert all remaining TIMESTAMP (without TZ) columns to TIMESTAMPTZ (interpreting stored values as UTC).
-- 4. Add leading-tenant_id composite indexes across multi-tenant tables for dual-DB parity.

ALTER TABLE sessions RENAME COLUMN agent_config TO last_request_state;
COMMENT ON COLUMN sessions.last_request_state IS 'Last QA/Agent request input-bar state snapshot in JSON format';

UPDATE messages SET attachments = '[]'::jsonb WHERE attachments IS NULL;
ALTER TABLE messages
    ALTER COLUMN attachments SET DEFAULT '[]'::jsonb,
    ALTER COLUMN attachments SET NOT NULL;

UPDATE messages SET is_fallback = FALSE WHERE is_fallback IS NULL;
ALTER TABLE messages
    ALTER COLUMN is_fallback SET DEFAULT FALSE,
    ALTER COLUMN is_fallback SET NOT NULL;

-- Promote remaining naive TIMESTAMP columns to TIMESTAMP WITH TIME ZONE.
ALTER TABLE data_sources
    ALTER COLUMN last_sync_at TYPE TIMESTAMP WITH TIME ZONE USING last_sync_at AT TIME ZONE 'UTC',
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE sync_logs
    ALTER COLUMN started_at TYPE TIMESTAMP WITH TIME ZONE USING started_at AT TIME ZONE 'UTC',
    ALTER COLUMN finished_at TYPE TIMESTAMP WITH TIME ZONE USING finished_at AT TIME ZONE 'UTC',
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_services
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE mcp_tool_approvals
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC';

ALTER TABLE resources
    ALTER COLUMN expires_at TYPE TIMESTAMP WITH TIME ZONE USING expires_at AT TIME ZONE 'UTC',
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE resource_bindings
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC';

ALTER TABLE storage_backends
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE temporary_documents
    ALTER COLUMN expires_at TYPE TIMESTAMP WITH TIME ZONE USING expires_at AT TIME ZONE 'UTC',
    ALTER COLUMN started_at TYPE TIMESTAMP WITH TIME ZONE USING started_at AT TIME ZONE 'UTC',
    ALTER COLUMN ready_at TYPE TIMESTAMP WITH TIME ZONE USING ready_at AT TIME ZONE 'UTC',
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE vector_stores
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE web_search_providers
    ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
    ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';

-- Multi-tenant composite indexes with leading tenant_id.
CREATE INDEX IF NOT EXISTS idx_models_tenant_type_status
    ON models (tenant_id, type, status)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_chunks_tenant_type_enabled
    ON chunks (tenant_id, chunk_type, is_enabled)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_prompts_tenant_user_status
    ON agent_prompts (tenant_id, user_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_run_suspensions_tenant_session
    ON agent_run_suspensions (tenant_id, session_id);

CREATE INDEX IF NOT EXISTS idx_automation_runs_tenant_automation_created
    ON automation_runs (tenant_id, automation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_tenant_skills_tenant_config
    ON tenant_skills (tenant_id, sandbox_config_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tenant_skill_snapshots_tenant_config_state
    ON tenant_skill_snapshots (tenant_id, sandbox_config_id, state);

CREATE INDEX IF NOT EXISTS idx_wiki_folders_tenant_kb
    ON wiki_folders (tenant_id, knowledge_base_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_wiki_page_revisions_tenant_kb_page
    ON wiki_page_revisions (tenant_id, knowledge_base_id, page_id);

CREATE INDEX IF NOT EXISTS idx_fork_snapshot_leases_tenant
    ON fork_snapshot_leases (tenant_id);

CREATE INDEX IF NOT EXISTS idx_chunk_images_tenant_kb
    ON chunk_images (tenant_id, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_org_join_requests_tenant_status
    ON organization_join_requests (tenant_id, status);
