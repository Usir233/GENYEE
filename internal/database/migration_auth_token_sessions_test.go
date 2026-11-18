package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func testAuthTokenSessionMigration(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	// A temporary table shadows any existing table; rollback leaves even the
	// explicitly configured disposable PostgreSQL database unchanged.
	tx, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(`CREATE TEMP TABLE auth_tokens (id VARCHAR(36) PRIMARY KEY,
		user_id VARCHAR(36) NOT NULL, token TEXT NOT NULL, token_type VARCHAR(50) NOT NULL,
		expires_at TIMESTAMP, is_revoked BOOLEAN DEFAULT FALSE, created_at TIMESTAMP, updated_at TIMESTAMP)`)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO auth_tokens (id, user_id, token, token_type, is_revoked) VALUES
		('old-access', 'user', 'access', 'access_token', FALSE),
		('old-refresh', 'user', 'refresh', 'refresh_token', FALSE),
		('revoked-refresh', 'user', 'revoked', 'refresh_token', TRUE)`)
	require.NoError(t, err)
	migration := "sqlite/000053_auth_token_sessions"
	if dialect == "postgres" {
		migration = "versioned/000137_auth_token_sessions"
	}
	readMigration := func(direction string) string {
		script, err := os.ReadFile(filepath.Join(sqliteRepoRoot(t), "migrations", migration+"."+direction+".sql"))
		require.NoError(t, err)
		return string(script)
	}
	_, err = tx.Exec(readMigration("up"))
	require.NoError(t, err)
	var live, total int
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM auth_tokens WHERE is_revoked = FALSE").Scan(&live))
	require.Zero(t, live, "unlinked historical tokens must require a new login")
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM auth_tokens").Scan(&total))
	require.Equal(t, 3, total, "migration retains existing token records")

	_, err = tx.Exec(`INSERT INTO auth_tokens (id, user_id, token, token_type, session_id)
		VALUES ('new-access', 'user', 'new', 'access_token', 'new-session')`)
	require.NoError(t, err)
	var session string
	var revoked bool
	require.NoError(t, tx.QueryRow(
		"SELECT session_id, is_revoked FROM auth_tokens WHERE id = 'new-access'",
	).Scan(&session, &revoked))
	require.Equal(t, "new-session", session)
	require.False(t, revoked)

	_, err = tx.Exec(readMigration("down"))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM auth_tokens WHERE is_revoked = TRUE").Scan(&total))
	require.Equal(t, 3, total, "rollback must not resurrect already revoked credentials")
	_, err = tx.Exec(readMigration("up"))
	require.NoError(t, err, "down/up cycle must recreate the column and index")
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM auth_tokens WHERE is_revoked = FALSE").Scan(&live))
	require.Zero(t, live)
}

func TestSQLiteAuthTokenSessionMigration(t *testing.T) {
	db := openSQLiteDB(t, filepath.Join(t.TempDir(), "auth-sessions.db"))
	testAuthTokenSessionMigration(t, db, "sqlite")
}

func TestPostgresAuthTokenSessionMigration(t *testing.T) {
	dsn := os.Getenv("GENYEE_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GENYEE_MIGRATION_TEST_POSTGRES_DSN for PostgreSQL auth session migration tests")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	testAuthTokenSessionMigration(t, db, "postgres")
}
