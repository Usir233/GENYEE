package cmdutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
)

func TestOpenInput_DoesNotCloseStdinPipe(t *testing.T) {
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		_ = pr.Close()
		_ = pw.Close()
	}()

	origIn := iostreams.IO.In
	iostreams.IO.In = pr
	t.Cleanup(func() { iostreams.IO.In = origIn })

	_, err = pw.WriteString("hello-from-stdin")
	require.NoError(t, err)
	require.NoError(t, pw.Close())

	rc, err := OpenInput("-")
	require.NoError(t, err)
	if closer, ok := any(rc).(io.Closer); ok {
		require.NoError(t, closer.Close())
	}

	// Reading after Close() must still work because "-" borrows iostreams.IO.In
	// rather than owning the file descriptor.
	data, err := io.ReadAll(pr)
	require.NoError(t, err)
	assert.Equal(t, "hello-from-stdin", string(data))
}

func TestOpenInput_ClosesRegularFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(p, []byte("system prompt"), 0o600))

	r, err := OpenInput(p)
	require.NoError(t, err)
	closer, ok := any(r).(io.Closer)
	require.True(t, ok, "regular file reader must implement io.Closer")
	require.NoError(t, closer.Close())

	_, err = r.Read(make([]byte, 4))
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestEnvHostAndResolveHost_CanonicalPrecedence(t *testing.T) {
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod": {Host: "https://profile.example.com"},
		},
	}

	t.Run("GENYEE_BASE_URL wins over GENYEE_HOST and profile when env creds active", func(t *testing.T) {
		t.Setenv("GENYEE_API_KEY", "sk-test")
		t.Setenv("GENYEE_TOKEN", "")
		t.Setenv("GENYEE_HOST", "https://host.example.com")
		t.Setenv("GENYEE_BASE_URL", "https://base-url.example.com")

		assert.Equal(t, "https://base-url.example.com", EnvHost())
		assert.Equal(t, "https://base-url.example.com", ResolveHost(cfg, false))
	})

	t.Run("GENYEE_HOST wins over profile when GENYEE_BASE_URL unset and env creds active", func(t *testing.T) {
		t.Setenv("GENYEE_API_KEY", "sk-test")
		t.Setenv("GENYEE_TOKEN", "")
		t.Setenv("GENYEE_HOST", "https://host.example.com")
		t.Setenv("GENYEE_BASE_URL", "")

		assert.Equal(t, "https://host.example.com", EnvHost())
		assert.Equal(t, "https://host.example.com", ResolveHost(cfg, false))
	})

	t.Run("profile host wins when no env creds and allowUnauthBaseURL is false", func(t *testing.T) {
		t.Setenv("GENYEE_API_KEY", "")
		t.Setenv("GENYEE_TOKEN", "")
		t.Setenv("GENYEE_HOST", "https://ignored-host.example.com")
		t.Setenv("GENYEE_BASE_URL", "https://ignored-base.example.com")

		assert.Equal(t, "https://profile.example.com", ResolveHost(cfg, false))
		assert.Equal(t, "https://ignored-base.example.com", ResolveHost(cfg, true))
	})

	t.Run("buildClientFromEnv accepts GENYEE_BASE_URL with stateless credential", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"success":true,"data":{"user":{"id":"u1"}}}`)
		}))
		defer srv.Close()

		t.Setenv("GENYEE_API_KEY", "sk-test")
		t.Setenv("GENYEE_TOKEN", "")
		t.Setenv("GENYEE_HOST", "")
		t.Setenv("GENYEE_BASE_URL", srv.URL)

		cli, handled, err := buildClientFromEnv(&Factory{
			Config: func() (*config.Config, error) { return &config.Config{}, nil },
		})
		require.True(t, handled)
		require.NoError(t, err)
		resp, err := cli.GetCurrentUser(context.Background())
		require.NoError(t, err)
		require.NotNil(t, resp.Data.User)
		assert.Equal(t, "u1", resp.Data.User.ID)
	})
}

func TestClassifyHTTPError_CanonicalNestedAppErrorNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{
  "success": false,
  "error": {
    "code": 1003,
    "message": "knowledge base not found"
  }
}`)
	}))
	defer srv.Close()

	_, err := sdk.NewClient(srv.URL).GetKnowledgeBase(context.Background(), "kb-missing")
	require.Error(t, err)
	assert.Equal(t, CodeResourceNotFound, ClassifyHTTPError(err))
}
