package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

var expectedTenantCompositeIndexes = []string{
	"idx_models_tenant_type_status",
	"idx_chunks_tenant_type_enabled",
	"idx_agent_prompts_tenant_user_status",
	"idx_agent_run_suspensions_tenant_session",
	"idx_automation_runs_tenant_automation_created",
	"idx_tenant_skills_tenant_config",
	"idx_tenant_skill_snapshots_tenant_config_state",
	"idx_wiki_folders_tenant_kb",
	"idx_wiki_page_revisions_tenant_kb_page",
	"idx_fork_snapshot_leases_tenant",
	"idx_chunk_images_tenant_kb",
	"idx_org_join_requests_tenant_status",
}

func TestSchemaAuditV3SQLiteUpDownAndParity(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	v55Root := copySQLiteMigrationsThrough(t, repoRoot, 55)
	chdirAndRestore(t, v55Root)

	dbPath := filepath.Join(t.TempDir(), "audit-v3.db")
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	seedDB := openSQLiteDB(t, dbPath)
	_, err := seedDB.Exec("INSERT INTO tenants (id, name, business) VALUES (10000, 't-audit', 'biz')")
	require.NoError(t, err)
	_, err = seedDB.Exec(
		`INSERT INTO sessions (id, tenant_id, agent_config) VALUES ('sess-state-1', 10000, '{"agent_id":"ag-1"}')`,
	)
	require.NoError(t, err)
	_, err = seedDB.Exec(
		"INSERT INTO mcp_services (id, tenant_id, name, transport_type) VALUES ('svc-v55', 10000, 'svc', 'http')",
	)
	require.NoError(t, err)
	// Legacy v55 allowed NULL principal_type / principal_id rows to bypass UNIQUE.
	_, err = seedDB.Exec(
		`INSERT INTO mcp_oauth_tokens (id, tenant_id, user_id, service_id, principal_type, principal_id, access_token)
		 VALUES ('tok-null-1', 10000, 'u-legacy', 'svc-v55', NULL, NULL, 'old-tok'),
		        ('tok-null-2', 10000, 'u-legacy', 'svc-v55', NULL, NULL, 'new-tok')`,
	)
	require.NoError(t, err)
	require.NoError(t, seedDB.Close())

	// Upgrade to v56.
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ver, dirty := sqliteMigrationState(t, sqlDB)
	require.Equal(t, 56, ver)
	require.False(t, dirty)

	// 1. sessions.agent_config -> sessions.last_request_state preserving row data.
	require.True(t, sqliteColumnExists(t, sqlDB, "sessions", "last_request_state"))
	require.False(t, sqliteColumnExists(t, sqlDB, "sessions", "agent_config"))
	var savedState string
	require.NoError(t, sqlDB.QueryRow(
		"SELECT last_request_state FROM sessions WHERE id = 'sess-state-1'",
	).Scan(&savedState))
	require.JSONEq(t, `{"agent_id":"ag-1"}`, savedState)

	// 2. mcp_oauth_tokens deduplicated, NOT NULL enforced, and principal widths 512.
	var tokCount int
	require.NoError(t, sqlDB.QueryRow(
		"SELECT COUNT(*) FROM mcp_oauth_tokens WHERE tenant_id = 10000 AND service_id = 'svc-v55'",
	).Scan(&tokCount))
	require.Equal(t, 1, tokCount)

	_, err = sqlDB.Exec(
		`INSERT INTO mcp_oauth_tokens (id, tenant_id, user_id, service_id, principal_type, principal_id)
		 VALUES ('tok-reject-null', 10000, 'u1', 'svc-v55', NULL, 'u1')`,
	)
	require.Error(t, err, "principal_type must be NOT NULL on SQLite")
	_, err = sqlDB.Exec(
		`INSERT INTO mcp_oauth_tokens (id, tenant_id, user_id, service_id, principal_type, principal_id)
		 VALUES ('tok-reject-null2', 10000, 'u1', 'svc-v55', 'web_user', NULL)`,
	)
	require.Error(t, err, "principal_id must be NOT NULL on SQLite")

	assertSQLiteColumnType(t, sqlDB, "mcp_oauth_tokens", "user_id", "VARCHAR(512)", true)
	assertSQLiteColumnType(t, sqlDB, "mcp_oauth_tokens", "principal_type", "VARCHAR(32)", true)
	assertSQLiteColumnType(t, sqlDB, "mcp_oauth_tokens", "principal_id", "VARCHAR(512)", true)
	assertSQLiteColumnType(t, sqlDB, "agent_grants", "user_id", "VARCHAR(512)", true)
	assertSQLiteColumnType(t, sqlDB, "mcp_metadata", "principal", "VARCHAR(512)", true)

	// 3. Composite tenant & parity indexes exist.
	for _, idx := range expectedTenantCompositeIndexes {
		require.Truef(t, sqliteIndexExists(t, sqlDB, idx), "missing SQLite index %s", idx)
	}
	for _, idx := range []string{
		"idx_org_join_requests_type",
		"idx_knowledges_kb_metadata_external_id",
		"idx_sessions_unconsumed_fork",
	} {
		require.Truef(t, sqliteIndexExists(t, sqlDB, idx), "missing SQLite parity index %s", idx)
	}

	// 4. Step 56 -> 55 -> 56 rollback/reapply and full Down -> Up reversibility.
	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })

	require.NoError(t, m.Migrate(55))
	require.True(t, sqliteColumnExists(t, sqlDB, "sessions", "agent_config"))
	require.False(t, sqliteColumnExists(t, sqlDB, "sessions", "last_request_state"))

	require.NoError(t, m.Migrate(56))
	require.True(t, sqliteColumnExists(t, sqlDB, "sessions", "last_request_state"))
	require.False(t, sqliteColumnExists(t, sqlDB, "sessions", "agent_config"))

	require.NoError(t, m.Down())
	var remainingTables int
	require.NoError(t, sqlDB.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master "+
			"WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'",
	).Scan(&remainingTables))
	require.Zero(t, remainingTables, "full SQLite Down() must drop every application table")

	require.NoError(t, m.Up())
	ver, dirty = sqliteMigrationState(t, sqlDB)
	require.Equal(t, 56, ver)
	require.False(t, dirty)
}

func TestGetMigrationVersionSupportsSQLiteAndSSLMode(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "version-check.db")
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DB_PATH", dbPath)
	ver, dirty, err := GetMigrationVersion()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(56), ver)
}

func TestSchemaAuditV3PostgresUpDownAndParadeDBBootstrap(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	paradeMigrateBytes, err := os.ReadFile(
		filepath.Join(repoRoot, "migrations", "paradedb", "01-migrate-to-paradedb.sql"),
	)
	require.NoError(t, err)
	require.NotContains(t, string(paradeMigrateBytes), "CREATE TABLE chinese_documents",
		"01-migrate-to-paradedb.sql must not create unguarded persistent demo tables")

	dsn := os.Getenv("GENYEE_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		return
	}
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions(dsn, MigrationOptions{}))

	pgDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgDB.Close() })

	var ver int
	var dirty bool
	require.NoError(t, pgDB.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&ver, &dirty))
	require.Equal(t, 140, ver)
	require.False(t, dirty)

	// 1. sessions.last_request_state renamed from agent_config.
	var hasLastState, hasAgentCfg int
	require.NoError(t, pgDB.QueryRow(
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = 'sessions' AND column_name = 'last_request_state'`,
	).Scan(&hasLastState))
	require.Equal(t, 1, hasLastState)
	require.NoError(t, pgDB.QueryRow(
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = 'sessions' AND column_name = 'agent_config'`,
	).Scan(&hasAgentCfg))
	require.Zero(t, hasAgentCfg)

	// 2. messages.attachments and messages.is_fallback are NOT NULL.
	for _, col := range []string{"attachments", "is_fallback"} {
		var nullable string
		require.NoError(t, pgDB.QueryRow(
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_schema = 'public' AND table_name = 'messages' AND column_name = $1`, col,
		).Scan(&nullable))
		require.Equal(t, "NO", nullable, "messages.%s must be NOT NULL", col)
	}

	// 3. Zero remaining TIMESTAMP WITHOUT TIME ZONE columns in public schema.
	var naiveCount int
	require.NoError(t, pgDB.QueryRow(
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = 'public' AND data_type = 'timestamp without time zone'`,
	).Scan(&naiveCount))
	require.Zero(t, naiveCount, "all public timestamp columns must be timestamptz")

	// 4. All 12 composite tenant indexes exist on Postgres.
	for _, idx := range expectedTenantCompositeIndexes {
		var found int
		require.NoError(t, pgDB.QueryRow(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1", idx,
		).Scan(&found))
		require.Equal(t, 1, found, "missing Postgres index %s", idx)
	}

	// 5. Step 140 -> 139 -> 140 rollback and re-apply succeeds cleanly.
	m, err := migrate.New("file://migrations/versioned", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))
	require.NoError(t, pgDB.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&ver, &dirty))
	require.Equal(t, 139, ver)
	require.False(t, dirty)

	require.NoError(t, m.Steps(1))
	require.NoError(t, pgDB.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&ver, &dirty))
	require.Equal(t, 140, ver)
	require.False(t, dirty)

	// 6. Execute 00-init-db.sql + 01-migrate-to-paradedb.sql idempotently on migrated DB.
	initBytes, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "paradedb", "00-init-db.sql"))
	require.NoError(t, err)
	_, err = pgDB.Exec(string(initBytes))
	require.NoError(t, err)
	_, err = pgDB.Exec(string(paradeMigrateBytes))
	require.NoError(t, err)
}

func assertSQLiteColumnType(
	t *testing.T, db *sql.DB, table, col, wantType string, wantNotNull bool,
) {
	t.Helper()
	var gotType string
	var notNull int
	require.NoError(t, db.QueryRow(
		"SELECT type, \"notnull\" FROM pragma_table_info(?) WHERE name = ?", table, col,
	).Scan(&gotType, &notNull))
	require.Truef(t, strings.EqualFold(wantType, gotType),
		"%s.%s type = %q, want %q", table, col, gotType, wantType)
	require.Equal(t, wantNotNull, notNull == 1, "%s.%s notnull mismatch", table, col)
}
