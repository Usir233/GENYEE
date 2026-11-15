package database

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/stretchr/testify/require"
)

func captureAppLoggerOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger.GetLogger(context.Background()).Logger.SetOutput(&buf)
	t.Cleanup(func() {
		logger.ConfigureFromEnv()
	})
	return &buf
}

func TestRunMigrationsRedactsDSNCredentials(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	cases := []struct {
		name    string
		dsn     string
		secrets []string
	}{
		{
			name:    "percent character in URL userinfo embeds raw password on parse error",
			dsn:     "postgres://dbadmin:PercentSecret%zz@127.0.0.1:5432/genyee?sslmode=disable",
			secrets: []string{"PercentSecret"},
		},
		{
			name:    "trailing newline from secret file embeds full DSN in control char error",
			dsn:     "postgres://dbadmin:NewlineS3cret88@127.0.0.1:5432/genyee?sslmode=disable\n",
			secrets: []string{"NewlineS3cret88"},
		},
		{
			name:    "query parameter password in DSN with non-numeric port",
			dsn:     "postgres://dbadmin@127.0.0.1:badport/genyee?password=QueryParamSecret77&sslmode=disable",
			secrets: []string{"QueryParamSecret77"},
		},
		{
			name:    "at-sign in raw password leaks password suffix as parsed hostname",
			dsn:     "postgres://dbadmin:Prefix@LeakedHostSecret99.invalid@127.0.0.1:5432/genyee?sslmode=disable",
			secrets: []string{"LeakedHostSecret99"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := captureAppLoggerOutput(t)
			err := RunMigrationsWithOptions(tc.dsn, MigrationOptions{})
			require.Error(t, err)

			cachedErr := CachedMigrationError()
			require.NotEmpty(t, cachedErr)
			logged := logBuf.String()

			for _, secret := range tc.secrets {
				require.NotContains(t, err.Error(), secret, "returned error must not leak DSN credential")
				require.NotContains(t, cachedErr, secret, "CachedMigrationError must not leak DSN credential")
				require.NotContains(t, logged, secret, "logger output must not leak DSN credential")
			}
		})
	}
}

func TestGetMigrationVersionEncodesAndRedactsCredentials(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	// Pick a guaranteed-closed loopback TCP port so properly encoded URLs dial 127.0.0.1:<port>
	// instead of misparsing DB_PASSWORD characters as host/port/escape sequences.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	rawPass := "P@ssHostSecret42.invalid/db%zz?k=1#frag"
	t.Setenv("DB_USER", "svc@admin")
	t.Setenv("DB_PASSWORD", rawPass)
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("DB_PORT", port)
	t.Setenv("DB_NAME", "genyee\n")

	_, _, err = GetMigrationVersion()
	require.Error(t, err)
	for _, secret := range []string{
		rawPass,
		"ssHostSecret42",
		"%40ssHostSecret42",
	} {
		require.NotContains(t, err.Error(), secret, "GetMigrationVersion error must not leak DB_PASSWORD")
	}
	require.NotContains(t, err.Error(), "invalid URL escape")
	require.NotContains(t, err.Error(), "invalid port")
}

func TestRunMigrationsSQLiteDBPathWithoutSQLiteDSNPrefixAndStateReset(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "path-only.db")
	require.NoError(t, RunMigrationsWithOptions("", MigrationOptions{SQLiteDBPath: dbPath}))

	ver, dirty, ok := CachedMigrationVersion()
	require.True(t, ok)
	require.False(t, dirty)
	require.Equal(t, uint(expectedSQLiteMigrationVersion), ver)
	require.Empty(t, CachedMigrationError())

	logBuf := captureAppLoggerOutput(t)
	err := RunMigrationsWithOptions("unknownscheme://admin:ResetSecret55@localhost/genyee", MigrationOptions{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "ResetSecret55")
	require.NotContains(t, CachedMigrationError(), "ResetSecret55")
	require.NotContains(t, logBuf.String(), "ResetSecret55")

	verAfter, dirtyAfter, okAfter := CachedMigrationVersion()
	require.False(t, okAfter, "early open failure before m.Version() must clear stale cached migration version")
	require.Zero(t, verAfter)
	require.False(t, dirtyAfter)
}
