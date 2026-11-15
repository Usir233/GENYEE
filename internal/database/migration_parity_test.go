package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

func TestSQLiteCrossDialectIndexParity(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "parity.db")
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	// Seed minimal parent rows for foreign-key-checked tables.
	_, err = db.Exec("INSERT INTO tenants (id, name, business) VALUES (10000, 't1', 'biz')")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO sessions (id, tenant_id) VALUES ('sess-a', 10000), ('sess-b', 10000), ('sess-ta', 10000), ('sess-tb', 10000)",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) "+
			"VALUES ('kb-1', 'KB 1', 10000, 'emb-1', 'sum-1')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO custom_agents (id, name, tenant_id) VALUES ('agent-1', 'Agent 1', 10000)",
	)
	require.NoError(t, err)

	// 1. im_channel_sessions: same IM user or thread talking to different agents must coexist,
	// while duplicate active rows for the SAME agent must be rejected (matches Postgres 000038).
	_, err = db.Exec(
		"INSERT INTO im_channel_sessions (id, platform, user_id, chat_id, session_id, tenant_id, agent_id) "+
			"VALUES ('ims-1', 'wecom', 'u1', 'c1', 'sess-a', 10000, 'agent-a')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO im_channel_sessions (id, platform, user_id, chat_id, session_id, tenant_id, agent_id) "+
			"VALUES ('ims-2', 'wecom', 'u1', 'c1', 'sess-b', 10000, 'agent-b')",
	)
	require.NoError(t, err, "multi-agent IM user sessions must allow distinct agent_id rows")
	_, err = db.Exec(
		"INSERT INTO im_channel_sessions (id, platform, user_id, chat_id, session_id, tenant_id, agent_id) "+
			"VALUES ('ims-dup', 'wecom', 'u1', 'c1', 'sess-a', 10000, 'agent-a')",
	)
	require.Error(t, err, "duplicate active IM user session for the same agent_id must be rejected")

	_, err = db.Exec(
		"INSERT INTO im_channel_sessions (id, platform, user_id, chat_id, thread_id, session_id, tenant_id, agent_id) "+
			"VALUES ('imst-1', 'feishu', 'u2', 'c2', 'th-1', 'sess-ta', 10000, 'agent-a')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO im_channel_sessions (id, platform, user_id, chat_id, thread_id, session_id, tenant_id, agent_id) "+
			"VALUES ('imst-2', 'feishu', 'u3', 'c2', 'th-1', 'sess-tb', 10000, 'agent-b')",
	)
	require.NoError(t, err, "multi-agent IM thread sessions must allow distinct agent_id rows")
	_, err = db.Exec(
		"INSERT INTO im_channel_sessions (id, platform, user_id, chat_id, thread_id, session_id, tenant_id, agent_id) "+
			"VALUES ('imst-dup', 'feishu', 'u4', 'c2', 'th-1', 'sess-ta', 10000, 'agent-a')",
	)
	require.Error(t, err, "duplicate active IM thread session for the same agent_id must be rejected")

	// 2. tenant_members: soft-deleted member must be re-addable without UNIQUE constraint failure,
	// while duplicate active memberships for the same (user_id, tenant_id) must be rejected (Postgres 000043).
	_, err = db.Exec(
		"INSERT INTO tenant_members (user_id, tenant_id, role, deleted_at) "+
			"VALUES ('user-rejoin', 10000, 'contributor', '2026-01-01 00:00:00')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO tenant_members (user_id, tenant_id, role, deleted_at) "+
			"VALUES ('user-rejoin', 10000, 'admin', NULL)",
	)
	require.NoError(t, err, "re-adding a previously soft-deleted workspace member must succeed")
	_, err = db.Exec(
		"INSERT INTO tenant_members (user_id, tenant_id, role, deleted_at) "+
			"VALUES ('user-rejoin', 10000, 'viewer', NULL)",
	)
	require.Error(t, err, "duplicate active workspace membership must be rejected")

	// 3. organizations(invite_code), kb_shares, agent_shares, and resources content_hash index.
	_, err = db.Exec(
		"INSERT INTO organizations (id, name, owner_id, owner_tenant_id, invite_code) "+
			"VALUES ('org-empty-1', 'Org E1', 'u1', 10000, ''), ('org-empty-2', 'Org E2', 'u1', 10000, '')",
	)
	require.NoError(t, err, "multiple organizations with empty invite_code must coexist")

	_, err = db.Exec(
		"INSERT INTO organizations (id, name, owner_id, owner_tenant_id, invite_code) "+
			"VALUES ('org-1', 'Org 1', 'u1', 10000, 'INVITE123')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO organizations (id, name, owner_id, owner_tenant_id, invite_code) "+
			"VALUES ('org-dup', 'Org Dup', 'u1', 10000, 'INVITE123')",
	)
	require.Error(t, err, "duplicate active organization invite_code must be rejected")
	_, err = db.Exec(
		"INSERT INTO organizations (id, name, owner_id, owner_tenant_id, invite_code, deleted_at) "+
			"VALUES ('org-del', 'Org Del', 'u1', 10000, 'INVITE123', '2026-01-01 00:00:00')",
	)
	require.NoError(t, err, "soft-deleted organization must not collide with active invite_code")

	_, err = db.Exec(
		"INSERT INTO kb_shares (id, knowledge_base_id, organization_id, shared_by_user_id, source_tenant_id) "+
			"VALUES ('kbs-1', 'kb-1', 'org-1', 'u1', 10000)",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO kb_shares (id, knowledge_base_id, organization_id, shared_by_user_id, source_tenant_id) "+
			"VALUES ('kbs-dup', 'kb-1', 'org-1', 'u1', 10000)",
	)
	require.Error(t, err, "duplicate active kb_shares row must be rejected")
	_, err = db.Exec("UPDATE kb_shares SET deleted_at = '2026-01-01 00:00:00' WHERE id = 'kbs-1'")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO kb_shares (id, knowledge_base_id, organization_id, shared_by_user_id, source_tenant_id) "+
			"VALUES ('kbs-2', 'kb-1', 'org-1', 'u1', 10000)",
	)
	require.NoError(t, err, "re-sharing KB after soft-delete must succeed")

	_, err = db.Exec(
		"INSERT INTO agent_shares (id, agent_id, organization_id, shared_by_user_id, source_tenant_id) "+
			"VALUES ('ags-1', 'agent-1', 'org-1', 'u1', 10000)",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO agent_shares (id, agent_id, organization_id, shared_by_user_id, source_tenant_id) "+
			"VALUES ('ags-dup', 'agent-1', 'org-1', 'u1', 10000)",
	)
	require.Error(t, err, "duplicate active agent_shares row must be rejected")
	_, err = db.Exec("UPDATE agent_shares SET deleted_at = '2026-01-01 00:00:00' WHERE id = 'ags-1'")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO agent_shares (id, agent_id, organization_id, shared_by_user_id, source_tenant_id) "+
			"VALUES ('ags-2', 'agent-1', 'org-1', 'u1', 10000)",
	)
	require.NoError(t, err, "re-sharing custom agent after soft-delete must succeed")

	require.True(t, sqliteIndexExists(t, db, "idx_resources_tenant_content_hash"),
		"SQLite must index resources(tenant_id, content_hash) like Postgres 000123")
	rows, err := db.Query(
		"EXPLAIN QUERY PLAN SELECT id FROM resources WHERE tenant_id = 10000 AND content_hash = 'deadbeef' AND content_hash <> '' AND deleted_at IS NULL",
	)
	require.NoError(t, err)
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		plan.WriteString(detail + "\n")
	}
	require.NoError(t, rows.Close())
	require.Contains(t, plan.String(), "idx_resources_tenant_content_hash")
}

func TestSQLiteUpgradeFromV53DedupesAndRollsBackCleanly(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	v53Root := copySQLiteMigrationsThrough(t, repoRoot, 53)
	chdirAndRestore(t, v53Root)

	dbPath := filepath.Join(t.TempDir(), "upgrade-v53.db")
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec(
		"INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) "+
			"VALUES ('kb-u1', 'KB U1', 10000, 'e', 's')",
	)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO custom_agents (id, name, tenant_id) VALUES ('ag-u1', 'AG U1', 10000)")
	require.NoError(t, err)
	// Legacy v53 allowed duplicate active invite codes, KB shares, and agent shares.
	_, err = db.Exec(
		"INSERT INTO organizations (id, name, owner_id, owner_tenant_id, invite_code, created_at) VALUES "+
			"('org-a', 'Org A', 'u1', 10000, 'DUPCODE', '2026-01-01 00:00:00'), "+
			"('org-b', 'Org B', 'u1', 10000, 'DUPCODE', '2026-01-02 00:00:00')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO kb_shares (id, knowledge_base_id, organization_id, shared_by_user_id, source_tenant_id, created_at) VALUES "+
			"('kbs-a', 'kb-u1', 'org-a', 'u1', 10000, '2026-01-01 00:00:00'), "+
			"('kbs-b', 'kb-u1', 'org-a', 'u1', 10000, '2026-01-02 00:00:00')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO agent_shares (id, agent_id, organization_id, shared_by_user_id, source_tenant_id, created_at) VALUES "+
			"('ags-a', 'ag-u1', 'org-a', 'u1', 10000, '2026-01-01 00:00:00'), "+
			"('ags-b', 'ag-u1', 'org-a', 'u1', 10000, '2026-01-02 00:00:00')",
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var liveKBShares, liveAgentShares, liveInviteOrgs int
	require.NoError(t, sqlDB.QueryRow("SELECT COUNT(*) FROM kb_shares WHERE deleted_at IS NULL").Scan(&liveKBShares))
	require.Equal(t, 1, liveKBShares)
	require.NoError(t, sqlDB.QueryRow("SELECT COUNT(*) FROM agent_shares WHERE deleted_at IS NULL").Scan(&liveAgentShares))
	require.Equal(t, 1, liveAgentShares)
	require.NoError(t, sqlDB.QueryRow(
		"SELECT COUNT(*) FROM organizations WHERE invite_code = 'DUPCODE' AND deleted_at IS NULL",
	).Scan(&liveInviteOrgs))
	require.Equal(t, 1, liveInviteOrgs)

	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })

	require.NoError(t, m.Migrate(53))
	ver, dirty := sqliteMigrationState(t, sqlDB)
	require.Equal(t, 53, ver)
	require.False(t, dirty)

	require.NoError(t, m.Migrate(54))
	ver, dirty = sqliteMigrationState(t, sqlDB)
	require.Equal(t, 54, ver)
	require.False(t, dirty)
}

func TestPostgresPrincipalWidthsAndOrgJoinRequestIndexMigration(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	latest := latestVersionedMigration(t, repoRoot)
	require.GreaterOrEqual(t, latest, 138, "migrations/versioned must include 000138 corrective migration")

	upMatches, err := filepath.Glob(filepath.Join(repoRoot, "migrations", "versioned", "000138_*.up.sql"))
	require.NoError(t, err)
	require.Len(t, upMatches, 1)
	downMatches, err := filepath.Glob(filepath.Join(repoRoot, "migrations", "versioned", "000138_*.down.sql"))
	require.NoError(t, err)
	require.Len(t, downMatches, 1)

	upBytes, err := os.ReadFile(upMatches[0])
	require.NoError(t, err)
	upSQL := string(upBytes)
	require.Contains(t, upSQL, "DROP INDEX IF EXISTS idx_org_join_requests_org_user_pending")
	require.Contains(t, upSQL, "idx_organizations_invite_code")
	for _, snippet := range []string{
		"ALTER TABLE agent_prompts",
		"user_id TYPE VARCHAR(512)",
		"resolved_by TYPE VARCHAR(512)",
		"ALTER TABLE agent_grants",
		"ALTER TABLE mcp_metadata",
		"principal TYPE VARCHAR(512)",
	} {
		require.Contains(t, upSQL, snippet)
	}

	downBytes, err := os.ReadFile(downMatches[0])
	require.NoError(t, err)
	downSQL := string(downBytes)
	require.Contains(t, downSQL, "idx_org_join_requests_org_user_pending")
	require.Contains(t, downSQL, "USING LEFT(")

	if dsn := os.Getenv("GENYEE_MIGRATION_TEST_POSTGRES_DSN"); dsn != "" {
		chdirAndRestore(t, repoRoot)
		require.NoError(t, RunMigrationsWithOptions(dsn, MigrationOptions{}))

		pgDB, err := sql.Open("postgres", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = pgDB.Close() })

		embedPrincipal := "embed_session:10000:4f659efc-8e27-4dee-b232-b053c369e44f:9a12bc34-56de-4789-8012-3456789abcde"
		longExternalPrincipal := "api_external_user:10000:" + strings.Repeat("ext-principal-", 12)

		_, err = pgDB.Exec(
			`INSERT INTO agent_prompts (id, tenant_id, user_id, session_id, kind, status, resolved_by)
			 VALUES ('prm-width-test', 10000, $1, 'sess-width-test', 'tool_approval', 'answered', $2)`,
			embedPrincipal, longExternalPrincipal,
		)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = pgDB.Exec(`DELETE FROM agent_prompts WHERE id = 'prm-width-test'`) })

		_, err = pgDB.Exec(
			`INSERT INTO agent_grants (id, tenant_id, user_id, scope, scope_id, grant_key, created_at, updated_at)
			 VALUES ('grt-width-test', 10000, $1, 'session', 'sess-width-test', 'mcp:svc:tool', NOW(), NOW())`,
			longExternalPrincipal,
		)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = pgDB.Exec(`DELETE FROM agent_grants WHERE id = 'grt-width-test'`) })
	}
}

func TestBootstrapSQLIdempotencyInvariants(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	mysqlBytes, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "mysql", "00-init-db.sql"))
	require.NoError(t, err)
	mysqlSQL := string(mysqlBytes)

	createTableRE := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)\s*\(`)
	dropTableRE := regexp.MustCompile(`(?i)DROP\s+TABLE\s+IF\s+EXISTS\s+([a-z0-9_]+)\s*;`)

	var createdTables []string
	for _, m := range createTableRE.FindAllStringSubmatch(mysqlSQL, -1) {
		createdTables = append(createdTables, strings.ToLower(m[1]))
	}
	require.NotEmpty(t, createdTables)

	droppedPos := map[string]int{}
	for idx, m := range dropTableRE.FindAllStringSubmatch(mysqlSQL, -1) {
		droppedPos[strings.ToLower(m[1])] = idx
	}
	for _, tbl := range createdTables {
		_, ok := droppedPos[tbl]
		require.Truef(t, ok, "migrations/mysql/00-init-db.sql must DROP TABLE IF EXISTS %s before recreating it", tbl)
	}
	require.Less(
		t,
		droppedPos["message_suggestion_events"],
		droppedPos["message_suggestion_sets"],
		"child FK table message_suggestion_events must be dropped before parent message_suggestion_sets",
	)

	paradeBytes, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "paradedb", "00-init-db.sql"))
	require.NoError(t, err)
	paradeSQL := string(paradeBytes)
	require.Contains(t, paradeSQL, "< 10000", "migrations/paradedb/00-init-db.sql must guard tenants_id_seq restart")
	require.Contains(t, paradeSQL, "CREATE INDEX IF NOT EXISTS embeddings_embedding_idx_3584")
	require.Contains(t, paradeSQL, "CREATE INDEX IF NOT EXISTS embeddings_embedding_idx_798")
	require.Contains(t, paradeSQL, "CREATE INDEX IF NOT EXISTS embeddings_embedding_idx_1024")
}
