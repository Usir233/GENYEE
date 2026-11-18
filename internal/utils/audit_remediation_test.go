package utils

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSQLBlocksSystemColumnAndSubqueryInsideAggregateFilterOrderAndWindow(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		opts []SQLValidationOption
	}{
		{
			name: "system column in FILTER clause",
			sql:  "SELECT COUNT(id) FILTER (WHERE xmin::text != '') FROM chunks",
			opts: []SQLValidationOption{WithNoSystemColumns(), WithAllowedTables("chunks")},
		},
		{
			name: "system column in aggregate ORDER BY",
			sql:  "SELECT array_agg(id ORDER BY ctid) FROM chunks",
			opts: []SQLValidationOption{WithNoSystemColumns(), WithAllowedTables("chunks")},
		},
		{
			name: "system column in OVER window clause",
			sql:  "SELECT COUNT(id) OVER (PARTITION BY xmin) FROM chunks",
			opts: []SQLValidationOption{WithNoSystemColumns(), WithAllowedTables("chunks")},
		},
		{
			name: "subquery in FILTER clause",
			sql:  "SELECT COUNT(id) FILTER (WHERE (SELECT 1) = 1) FROM chunks",
			opts: []SQLValidationOption{WithNoSubqueries(), WithAllowedTables("chunks")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, res := ValidateSQL(tc.sql, tc.opts...)
			assert.False(t, res.Valid, "expected query %q to be rejected", tc.sql)
		})
	}
}

func TestSanitizeHTMLAndStdioEnvVarsPreserveValidUTF8OnTruncation(t *testing.T) {
	// 3335 * 3 bytes = 10005 bytes; byte offset 10000 splits a 3-byte UTF-8 rune.
	longCJK := strings.Repeat("中", 3335)
	got := SanitizeHTML(longCJK)
	assert.LessOrEqual(t, len(got), 10000)
	assert.True(t, utf8.ValidString(got), "SanitizeHTML must not split multi-byte UTF-8 runes")

	longKey := strings.Repeat("键", 100) // 300 bytes; byte offset 50 splits a 3-byte rune
	err := ValidateStdioEnvVars(map[string]string{longKey: "ok"})
	require.Error(t, err)
	assert.True(t, utf8.ValidString(err.Error()), "ValidateStdioEnvVars error must stay valid UTF-8")

	taskID := GenerateTaskID("sync", 1, strings.Repeat("业", 10))
	assert.True(t, utf8.ValidString(taskID))
}

func TestDebugHelpersUseScanAndPropagateErrors(t *testing.T) {
	mini := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	ctx := context.Background()
	// Key without TTL (persistent) and key with huge TTL (> maxAge) must be cleaned via SCAN.
	require.NoError(t, mini.Set("task:run:stale_no_ttl", "t1"))
	require.NoError(t, rc.Set(ctx, "task:run:stale_long", "t2", 5*time.Hour).Err())
	require.NoError(t, rc.Set(ctx, "task:run:fresh", "t3", 5*time.Minute).Err())

	deleted, err := CleanupStaleRunningTasks(ctx, rc, "task:run:", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
	assert.False(t, mini.Exists("task:run:stale_no_ttl"))
	assert.False(t, mini.Exists("task:run:stale_long"))
	assert.True(t, mini.Exists("task:run:fresh"))

	status, err := CheckRunningTaskStatus(ctx, rc, "task:run:fresh", "task:prog:missing")
	require.NoError(t, err)
	assert.Equal(t, true, status["running_task_exists"])
	assert.Equal(t, false, status["progress_exists"])
}

func TestDownloadBytesRedactsURLCredentialsInError(t *testing.T) {
	_, err := DownloadBytes("ftp://admin:UltraSecretURLPass@files.example.com/archive.zip")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "UltraSecretURLPass")
}
