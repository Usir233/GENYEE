-- Migration: 000054_index_parity
-- Align SQLite unique/partial indexes with PostgreSQL versioned migrations:
-- 1. im_channel_sessions: include agent_id in idx_channel_lookup and
--    idx_channel_thread_lookup (matches PostgreSQL 000038_im_session_agent_id).
-- 2. tenant_members: scope idx_tenant_members_user_tenant_unique to live rows
--    (WHERE deleted_at IS NULL, matching PostgreSQL 000043_tenant_rbac) so
--    soft-deleted workspace members can rejoin.
-- 3. organizations, kb_shares, agent_shares, resources: add partial unique /
--    content-hash lookup indexes matching PostgreSQL 000012 and 000123.

UPDATE im_channel_sessions
SET agent_id = ''
WHERE agent_id IS NULL;

UPDATE im_channel_sessions
SET deleted_at = CURRENT_TIMESTAMP
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY platform, user_id, chat_id, tenant_id, agent_id
                   ORDER BY updated_at DESC, created_at DESC, id ASC
               ) AS rn
        FROM im_channel_sessions
        WHERE deleted_at IS NULL
    )
    WHERE rn > 1
);

UPDATE im_channel_sessions
SET deleted_at = CURRENT_TIMESTAMP
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY platform, chat_id, thread_id, tenant_id, agent_id
                   ORDER BY updated_at DESC, created_at DESC, id ASC
               ) AS rn
        FROM im_channel_sessions
        WHERE deleted_at IS NULL AND thread_id != ''
    )
    WHERE rn > 1
);

DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_channel_thread_lookup;
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL AND thread_id != '';

UPDATE tenant_members
SET deleted_at = CURRENT_TIMESTAMP
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY user_id, tenant_id
                   ORDER BY id ASC
               ) AS rn
        FROM tenant_members
        WHERE deleted_at IS NULL
    )
    WHERE rn > 1
);

DROP INDEX IF EXISTS idx_tenant_members_user_tenant_unique;
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_members_user_tenant_unique
    ON tenant_members (user_id, tenant_id)
    WHERE deleted_at IS NULL;

UPDATE organizations
SET invite_code = ''
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY invite_code
                   ORDER BY created_at ASC, id ASC
               ) AS rn
        FROM organizations
        WHERE invite_code IS NOT NULL AND invite_code <> '' AND deleted_at IS NULL
    )
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_organizations_invite_code
    ON organizations (invite_code)
    WHERE invite_code IS NOT NULL AND invite_code <> '' AND deleted_at IS NULL;

UPDATE kb_shares
SET deleted_at = CURRENT_TIMESTAMP
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY knowledge_base_id, organization_id
                   ORDER BY created_at ASC, id ASC
               ) AS rn
        FROM kb_shares
        WHERE deleted_at IS NULL
    )
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_shares_kb_org
    ON kb_shares (knowledge_base_id, organization_id)
    WHERE deleted_at IS NULL;

UPDATE agent_shares
SET deleted_at = CURRENT_TIMESTAMP
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY agent_id, source_tenant_id, organization_id
                   ORDER BY created_at ASC, id ASC
               ) AS rn
        FROM agent_shares
        WHERE deleted_at IS NULL
    )
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_shares_agent_org
    ON agent_shares (agent_id, source_tenant_id, organization_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_resources_tenant_content_hash
    ON resources (tenant_id, content_hash)
    WHERE deleted_at IS NULL AND content_hash <> '';
