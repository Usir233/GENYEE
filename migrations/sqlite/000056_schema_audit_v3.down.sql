-- Rollback: 000056_schema_audit_v3

DROP INDEX IF EXISTS idx_sessions_unconsumed_fork;
DROP INDEX IF EXISTS idx_sessions_parent_session_id;
CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id
    ON sessions (parent_session_id);

DROP INDEX IF EXISTS idx_knowledges_kb_metadata_external_id;
DROP INDEX IF EXISTS idx_org_join_requests_type;
DROP INDEX IF EXISTS idx_org_join_requests_tenant_status;
DROP INDEX IF EXISTS idx_chunk_images_tenant_kb;
DROP INDEX IF EXISTS idx_fork_snapshot_leases_tenant;
DROP INDEX IF EXISTS idx_wiki_page_revisions_tenant_kb_page;
DROP INDEX IF EXISTS idx_wiki_folders_tenant_kb;
DROP INDEX IF EXISTS idx_tenant_skill_snapshots_tenant_config_state;
DROP INDEX IF EXISTS idx_tenant_skills_tenant_config;
DROP INDEX IF EXISTS idx_automation_runs_tenant_automation_created;
DROP INDEX IF EXISTS idx_agent_run_suspensions_tenant_session;
DROP INDEX IF EXISTS idx_agent_prompts_tenant_user_status;
DROP INDEX IF EXISTS idx_chunks_tenant_type_enabled;
DROP INDEX IF EXISTS idx_models_tenant_type_status;

-- Revert mcp_metadata.principal to VARCHAR(255).
DELETE FROM mcp_metadata
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY tenant_id, service_id, SUBSTR(principal, 1, 255)
                   ORDER BY synced_at DESC, principal ASC
               ) AS rn
        FROM mcp_metadata
    )
    WHERE rn > 1
);

CREATE TABLE mcp_metadata_v55 (
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    principal VARCHAR(255) NOT NULL DEFAULT '',
    config_fingerprint VARCHAR(64) NOT NULL,
    tools TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    server_name TEXT NOT NULL DEFAULT '',
    server_version TEXT NOT NULL DEFAULT '',
    server_description TEXT NOT NULL DEFAULT '',
    synced_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, service_id, principal)
);

INSERT INTO mcp_metadata_v55 (
    tenant_id, service_id, principal, config_fingerprint, tools,
    instructions, server_name, server_version, server_description, synced_at
)
SELECT
    tenant_id, service_id, SUBSTR(principal, 1, 255), config_fingerprint, tools,
    instructions, server_name, server_version, server_description, synced_at
FROM mcp_metadata;

DROP TABLE mcp_metadata;
ALTER TABLE mcp_metadata_v55 RENAME TO mcp_metadata;

-- Revert agent_grants.user_id to VARCHAR(128).
DELETE FROM agent_grants
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY tenant_id, SUBSTR(user_id, 1, 128), scope, scope_id, grant_key
                   ORDER BY updated_at DESC, id ASC
               ) AS rn
        FROM agent_grants
    )
    WHERE rn > 1
);

CREATE TABLE agent_grants_v55 (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
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

INSERT INTO agent_grants_v55 (
    id, tenant_id, user_id, scope, scope_id, grant_key, label, binding_hash,
    risk, prompt_id, created_at, updated_at, last_used_at, expires_at, revoked_at
)
SELECT
    id, tenant_id, SUBSTR(user_id, 1, 128), scope, scope_id, grant_key, label, binding_hash,
    risk, prompt_id, created_at, updated_at, last_used_at, expires_at, revoked_at
FROM agent_grants;

DROP TABLE agent_grants;
ALTER TABLE agent_grants_v55 RENAME TO agent_grants;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_grants_key
    ON agent_grants(tenant_id, user_id, scope, scope_id, grant_key);
CREATE INDEX IF NOT EXISTS idx_agent_grants_scope
    ON agent_grants(tenant_id, scope, scope_id);

-- Revert mcp_oauth_tokens to v55 column definitions.
CREATE TABLE mcp_oauth_tokens_v55 (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    token_type VARCHAR(32),
    expires_at DATETIME,
    refresh_lease_id VARCHAR(36),
    refresh_lease_until DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    principal_type VARCHAR(32),
    principal_id VARCHAR(512),
    FOREIGN KEY (service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
);

INSERT INTO mcp_oauth_tokens_v55 (
    id, tenant_id, user_id, service_id, access_token, refresh_token, token_type,
    expires_at, refresh_lease_id, refresh_lease_until, created_at, updated_at,
    principal_type, principal_id
)
SELECT
    id, tenant_id, SUBSTR(user_id, 1, 64), service_id, access_token, refresh_token, token_type,
    expires_at, refresh_lease_id, refresh_lease_until, created_at, updated_at,
    principal_type, principal_id
FROM mcp_oauth_tokens;

DROP TABLE mcp_oauth_tokens;
ALTER TABLE mcp_oauth_tokens_v55 RENAME TO mcp_oauth_tokens;

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_service_id
    ON mcp_oauth_tokens(service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_user_id
    ON mcp_oauth_tokens(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_principal_svc
    ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_principal
    ON mcp_oauth_tokens(principal_type, principal_id);

ALTER TABLE sessions RENAME COLUMN last_request_state TO agent_config;
