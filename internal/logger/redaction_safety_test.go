package logger

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatterRedactsSecretsTokensDSNsAndCookies(t *testing.T) {
	secrets := []string{
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.supersecretpayload",
		"sk-proj-1234567890abcdefghijklmnop",
		"SuperSecretPass123",
		"RedisTopSecret99",
		"MySQLSecret88",
		"my_secret_query_key",
		"s3cr3t_cookie_val",
		"field_api_secret_val",
		"field_db_pass_val",
		"dsn_inline_secret_val",
	}

	rawMsg := strings.Join([]string{
		"upstream auth failed:",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.supersecretpayload",
		"key=sk-proj-1234567890abcdefghijklmnop",
		"pg=postgres://admin:SuperSecretPass123@db.internal:5432/genyee?sslmode=disable",
		"redis=redis://:RedisTopSecret99@redis:6379/0",
		"mysql=mysql://root:MySQLSecret88@127.0.0.1:3306/app",
		"url=https://api.example.com/v1/data?api_key=my_secret_query_key&page=1",
		"Cookie: session_token=s3cr3t_cookie_val; theme=dark",
		"injected\r\nERROR forged-line",
	}, " ")

	fields := logrus.Fields{
		"request_id": "req-redact-1",
		"api_key":    "field_api_secret_val",
		"db_pass":    "field_db_pass_val",
		"dsn":        "postgres://u:dsn_inline_secret_val@localhost:5432/db",
		"safe_field": "keep-me-intact",
		"user_note":  "line1\r\nWARN forged-field",
	}

	formatters := []*CustomFormatter{
		{},
		{ForceColor: true},
		{Template: "[%d] %level %traceId | %msg"},
		{ForceColor: true, Template: "[%d] %level %traceId | %msg"},
	}

	for i, f := range formatters {
		entry := newEntry(logrus.WarnLevel, rawMsg, fields)
		outBytes, err := f.Format(entry)
		require.NoError(t, err, "formatter %d", i)
		out := string(outBytes)

		for _, s := range secrets {
			assert.NotContains(t, out, s, "formatter %d leaked secret %q in output: %s", i, s, out)
		}
		assert.Contains(t, out, "keep-me-intact", "formatter %d dropped benign field", i)
		trimmed := strings.TrimSuffix(out, "\n")
		assert.NotContains(t, trimmed, "\n", "formatter %d allowed newline injection: %q", i, out)
		assert.NotContains(t, trimmed, "\r", "formatter %d allowed CR injection: %q", i, out)
	}
}

func TestNilAndMalformedContextSafety(t *testing.T) {
	var nilCtx context.Context //nolint:staticcheck // intentionally testing nil context defense
	require.NotPanics(t, func() {
		entry := GetLogger(nilCtx)
		require.NotNil(t, entry)
	})
	require.NotPanics(t, func() {
		cloned := CloneContext(nilCtx)
		require.NotNil(t, cloned)
	})
	require.NotPanics(t, func() {
		cloned := CloneContextWithoutTrace(nilCtx)
		require.NotNil(t, cloned)
	})

	wrongTypeCtx := context.WithValue(context.Background(), types.LoggerContextKey, "not-a-logrus-entry")
	require.NotPanics(t, func() {
		entry := GetLogger(wrongTypeCtx)
		require.NotNil(t, entry)
	})
}

func TestSetLogLevelRaceWithConfigureFromEnv(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	t.Cleanup(ConfigureFromEnv)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			SetLogLevel(LevelInfo)
			SetLogLevel(LevelDebug)
		}
	}()
	for i := 0; i < 10; i++ {
		SetOutput(&buf)
	}
	<-done
}

func TestLLMDebugLogRedactsSecretsAndWaitsCleanly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "llm_debug_redact")
	t.Setenv("LLM_DEBUG_LOG", dir)
	llmDebug.mu.Lock()
	savedDir := llmDebug.dir
	savedEnabled := llmDebug.enabled
	llmDebug.mu.Unlock()
	t.Cleanup(func() {
		llmDebug.mu.Lock()
		llmDebug.dir = savedDir
		llmDebug.enabled = savedEnabled
		llmDebug.mu.Unlock()
	})
	configureLLMDebugLog()

	ctx := WithRequestID(context.Background(), "redact-llm-1")
	LLMDebugLog(ctx, &LLMCallRecord{
		CallType: "Chat",
		Model:    "gpt-test",
		Sections: []RecordSection{
			{
				Title:   "Prompt",
				Content: "Authorization: Bearer secret_bearer_llm_token_12345 and sk-llmsecretkey987654321",
			},
		},
		Error: "connect postgres://user:pg_llm_secret_pw@db:5432/app failed",
	})

	t.Setenv("LLM_DEBUG_LOG", "false")
	configureLLMDebugLog()

	raw, err := os.ReadFile(filepath.Join(dir, "redact-llm-1.log"))
	require.NoError(t, err)
	content := string(raw)
	assert.NotContains(t, content, "secret_bearer_llm_token_12345")
	assert.NotContains(t, content, "sk-llmsecretkey987654321")
	assert.NotContains(t, content, "pg_llm_secret_pw")
}
