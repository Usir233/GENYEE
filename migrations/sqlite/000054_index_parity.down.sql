-- Rollback: 000054_index_parity

DROP INDEX IF EXISTS idx_resources_tenant_content_hash;
DROP INDEX IF EXISTS idx_agent_shares_agent_org;
DROP INDEX IF EXISTS idx_kb_shares_kb_org;
DROP INDEX IF EXISTS idx_organizations_invite_code;

DROP INDEX IF EXISTS idx_tenant_members_user_tenant_unique;
DELETE FROM tenant_members
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY user_id, tenant_id
                   ORDER BY CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END ASC, id DESC
               ) AS rn
        FROM tenant_members
    )
    WHERE rn > 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_members_user_tenant_unique
    ON tenant_members (user_id, tenant_id);

DROP INDEX IF EXISTS idx_channel_thread_lookup;
UPDATE im_channel_sessions
SET deleted_at = CURRENT_TIMESTAMP
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY platform, chat_id, thread_id, tenant_id
                   ORDER BY updated_at DESC, created_at DESC, id ASC
               ) AS rn
        FROM im_channel_sessions
        WHERE deleted_at IS NULL AND thread_id != ''
    )
    WHERE rn > 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id)
    WHERE deleted_at IS NULL AND thread_id != '';

DROP INDEX IF EXISTS idx_channel_lookup;
UPDATE im_channel_sessions
SET deleted_at = CURRENT_TIMESTAMP
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY platform, user_id, chat_id, tenant_id
                   ORDER BY updated_at DESC, created_at DESC, id ASC
               ) AS rn
        FROM im_channel_sessions
        WHERE deleted_at IS NULL
    )
    WHERE rn > 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id)
    WHERE deleted_at IS NULL;
