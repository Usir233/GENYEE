-- Migration: 000056_schema_audit_v3
-- 1. Rename sessions.agent_config -> sessions.last_request_state.
-- 2. Rebuild mcp_oauth_tokens to enforce principal_type / principal_id NOT NULL
--    (closing SQLite UNIQUE NULL-bypass) and widen user_id to VARCHAR(512).
-- 3. Rebuild agent_grants and mcp_metadata to widen principal columns to VARCHAR(512).
-- 4. Add leading-tenant_id composite indexes and PostgreSQL cross-dialect parity indexes.

ALTER TABLE sessions RENAME COLUMN agent_config TO last_request_state;

-- 2. Backfill, deduplicate, and rebuild mcp_oauth_tokens.
DROP INDEX IF EXISTS idx_mcp_oauth_tokens_tenant_principal_svc;

UPDATE mcp_oauth_tokens
SET principal_type = COALESCE(NULLIF(principal_type, ''), 'web_user'),
    principal_id = COALESCE(
        NULLIF(principal_id, ''),
        CASE WHEN user_id IS NOT NULL AND user_id <> '' THEN user_id ELSE id END
    )
WHERE principal_type IS NULL OR principal_type = '' OR principal_id IS NULL OR principal_id = '';

DELETE FROM mcp_oauth_tokens
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY tenant_id, principal_type, principal_id, service_id
                   ORDER BY updated_at DESC, created_at DESC, id ASC
               ) AS rn
        FROM mcp_oauth_tokens
    )
    WHERE rn > 1
);

CREATE TABLE mcp_oauth_tokens_v56 (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(512) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32),
    expires_at DATETIME,
    refresh_lease_id VARCHAR(36),
    refresh_lease_until DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    principal_type VARCHAR(32) NOT NULL,
    principal_id VARCHAR(512) NOT NULL,
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
);

INSERT INTO mcp_oauth_tokens_v56 (
    id, tenant_id, user_id, service_id, access_token, refresh_token, token_type,
    expires_at, refresh_lease_id, refresh_lease_until, created_at, updated_at,
    principal_type, principal_id
)
SELECT
    id, tenant_id, user_id, service_id, access_token, refresh_token, token_type,
    expires_at, refresh_lease_id, refresh_lease_until, created_at, updated_at,
    principal_type, principal_id
FROM mcp_oauth_tokens;

DROP TABLE mcp_oauth_tokens;
ALTER TABLE mcp_oauth_tokens_v56 RENAME TO mcp_oauth_tokens;

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_service_id
    ON mcp_oauth_tokens(service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_user_id
    ON mcp_oauth_tokens(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_principal_svc
    ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_principal
    ON mcp_oauth_tokens(principal_type, principal_id);

-- 3a. Rebuild agent_grants with user_id VARCHAR(512).
CREATE TABLE agent_grants_v56 (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(512) NOT NULL,
    scope VARCHAR(16) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    grant_key VARCHAR(512) NOT NULL,
    label TEXT,
    binding_hash VARCHAR(64) NOT NULL DEFAULT '',
    risk VARCHAR(16) NOT NULL DEFAULT 'write',
    prompt_id VARCHAR(36),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    last_used_at DATETIME,
    expires_at DATETIME,
    revoked_at DATETIME
);

INSERT INTO agent_grants_v56 (
    id, tenant_id, user_id, scope, scope_id, grant_key, label, binding_hash,
    risk, prompt_id, created_at, updated_at, last_used_at, expires_at, revoked_at
)
SELECT
    id, tenant_id, user_id, scope, scope_id, grant_key, label, binding_hash,
    risk, prompt_id, created_at, updated_at, last_used_at, expires_at, revoked_at
FROM agent_grants;

DROP TABLE agent_grants;
ALTER TABLE agent_grants_v56 RENAME TO agent_grants;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_grants_key
    ON agent_grants(tenant_id, user_id, scope, scope_id, grant_key);
CREATE INDEX IF NOT EXISTS idx_agent_grants_scope
    ON agent_grants(tenant_id, scope, scope_id);

-- 3b. Rebuild mcp_metadata with principal VARCHAR(512).
CREATE TABLE mcp_metadata_v56 (
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    principal VARCHAR(512) NOT NULL DEFAULT '',
    config_fingerprint VARCHAR(64) NOT NULL,
    tools TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    server_name TEXT NOT NULL DEFAULT '',
    server_version TEXT NOT NULL DEFAULT '',
    server_description TEXT NOT NULL DEFAULT '',
    synced_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, service_id, principal)
);

INSERT INTO mcp_metadata_v56 (
    tenant_id, service_id, principal, config_fingerprint, tools,
    instructions, server_name, server_version, server_description, synced_at
)
SELECT
    tenant_id, service_id, principal, config_fingerprint, tools,
    instructions, server_name, server_version, server_description, synced_at
FROM mcp_metadata;

DROP TABLE mcp_metadata;
ALTER TABLE mcp_metadata_v56 RENAME TO mcp_metadata;

-- 4. Multi-tenant composite & cross-dialect parity indexes.
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

CREATE INDEX IF NOT EXISTS idx_org_join_requests_type
    ON organization_join_requests (request_type);

CREATE INDEX IF NOT EXISTS idx_knowledges_kb_metadata_external_id
    ON knowledges (
        knowledge_base_id,
        (CASE WHEN json_valid(metadata) THEN json_extract(metadata, '$.external_id') END)
    )
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_sessions_parent_session_id;
CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id
    ON sessions (parent_session_id)
    WHERE parent_session_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_unconsumed_fork
    ON sessions ((CASE WHEN json_valid(fork_bootstrap) THEN json_extract(fork_bootstrap, '$.snapshot_id') END))
    WHERE fork_bootstrap IS NOT NULL
      AND (CASE WHEN json_valid(fork_bootstrap) THEN json_extract(fork_bootstrap, '$.consumed_at') END) IS NULL;
