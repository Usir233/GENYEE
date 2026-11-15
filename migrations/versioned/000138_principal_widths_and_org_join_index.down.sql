-- Rollback: 000138_principal_widths_and_org_join_index

DELETE FROM mcp_metadata m
USING (
    SELECT tenant_id, service_id, principal,
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, service_id, LEFT(principal, 255)
               ORDER BY synced_at DESC, principal ASC
           ) AS rn
    FROM mcp_metadata
) dup
WHERE m.tenant_id = dup.tenant_id
  AND m.service_id = dup.service_id
  AND m.principal = dup.principal
  AND dup.rn > 1;

ALTER TABLE mcp_metadata
    ALTER COLUMN principal TYPE VARCHAR(255) USING LEFT(principal, 255);

DELETE FROM agent_grants g
USING (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, LEFT(user_id, 128), scope, scope_id, grant_key
               ORDER BY updated_at DESC, id ASC
           ) AS rn
    FROM agent_grants
) dup
WHERE g.id = dup.id
  AND dup.rn > 1;

ALTER TABLE agent_grants
    ALTER COLUMN user_id TYPE VARCHAR(128) USING LEFT(user_id, 128);

ALTER TABLE agent_prompts
    ALTER COLUMN user_id TYPE VARCHAR(64) USING LEFT(user_id, 64),
    ALTER COLUMN resolved_by TYPE VARCHAR(64) USING LEFT(resolved_by, 64);

DROP INDEX IF EXISTS idx_organizations_invite_code;
UPDATE organizations
SET invite_code = NULL
WHERE invite_code = '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_organizations_invite_code
    ON organizations (invite_code)
    WHERE invite_code IS NOT NULL AND deleted_at IS NULL;

UPDATE organization_join_requests r
SET status = 'rejected',
    review_message = COALESCE(review_message, 'deduplicated during 000138 rollback'),
    updated_at = CURRENT_TIMESTAMP
FROM (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY organization_id, user_id
               ORDER BY created_at ASC, id ASC
           ) AS rn
    FROM organization_join_requests
    WHERE status = 'pending'
) dup
WHERE r.id = dup.id
  AND dup.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_join_requests_org_user_pending
    ON organization_join_requests (organization_id, user_id)
    WHERE status = 'pending';
