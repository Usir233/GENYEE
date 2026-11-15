-- Migration: 000138_principal_widths_and_org_join_index
-- 1. Drop legacy per-user pending join-request unique index left behind when
--    000045_org_tenant_members switched pending uniqueness to per-(org, tenant, type)
--    via uq_org_join_requests_pending_per_tenant.
-- 2. Exclude empty-string invite_code ('') from idx_organizations_invite_code
--    because types.Organization.InviteCode is a non-pointer string.
-- 3. Widen Principal.StorageID() columns in agent_prompts, agent_grants, and
--    mcp_metadata to VARCHAR(512) so synthetic embed_session / embed_visitor /
--    api_external_user / im_user principals never fail with SQLSTATE 22001.

DROP INDEX IF EXISTS idx_org_join_requests_org_user_pending;

DROP INDEX IF EXISTS idx_organizations_invite_code;
CREATE UNIQUE INDEX IF NOT EXISTS idx_organizations_invite_code
    ON organizations (invite_code)
    WHERE invite_code IS NOT NULL AND invite_code <> '' AND deleted_at IS NULL;

ALTER TABLE agent_prompts
    ALTER COLUMN user_id TYPE VARCHAR(512),
    ALTER COLUMN resolved_by TYPE VARCHAR(512);

ALTER TABLE agent_grants
    ALTER COLUMN user_id TYPE VARCHAR(512);

ALTER TABLE mcp_metadata
    ALTER COLUMN principal TYPE VARCHAR(512);
