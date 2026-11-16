-- The 000054 constraint cannot represent user-mode and thread-mode rows for
-- the same user+agent or multiple threads opened by one user. Fail atomically
-- rather than discard mappings if such conflicts exist.
-- golang-migrate's SQLite driver wraps this migration in a transaction.
DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_channel_thread_lookup;
CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id, agent_id)
    WHERE deleted_at IS NULL AND thread_id != '';
