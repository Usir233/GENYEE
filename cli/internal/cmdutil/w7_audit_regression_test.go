package cmdutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
	sdk "github.com/Usir233/GENYEE/client"
)

func TestW7ValidateProfileNameRejectsLeadingDash(t *testing.T) {
	for _, bad := range []string{"-y", "--evil", "-profile", ".hidden"} {
		err := ValidateProfileName(bad)
		require.Error(t, err, "profile %q must be rejected", bad)
		var ce *Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, CodeInputInvalidArgument, ce.Code)
	}
	for _, good := range []string{"prod", "prod-1", "team_a.prod", "99bottles"} {
		require.NoError(t, ValidateProfileName(good))
	}
}

func TestW7NormalizeHostAndValidateHTTPURLHardening(t *testing.T) {
	err := ValidateHTTPURL("--host", "https://admin:supersecret999@kb.example.com")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecret999", "credentials in URL must never be echoed")

	err = ValidateHTTPURL("--host", "http://:8080")
	require.Error(t, err)

	for _, badHost := range []string{
		"https://kb.example.com?foo=bar",
		"https://kb.example.com#frag",
		"https://user:pass@kb.example.com",
		"http://:8080",
	} {
		_, err := NormalizeHost(badHost)
		require.Error(t, err, "NormalizeHost(%q) must fail", badHost)
		var ce *Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, CodeInputInvalidArgument, ce.Code)
	}
}

type countingZeroReader struct {
	readBytes int64
	limit     int64
}

func (r *countingZeroReader) Read(p []byte) (int, error) {
	if r.readBytes >= r.limit {
		return 0, io.EOF
	}
	n := len(p)
	if rem := int(r.limit - r.readBytes); n > rem {
		n = rem
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.readBytes += int64(n)
	return n, nil
}

func TestW7AuthRetryTransportBoomerangAndPrefixedAuthAndBoundedDrain(t *testing.T) {
	t.Run("strips credentials across boomerang OriginA -> EvilB -> OriginA redirect", func(t *testing.T) {
		var originAuthAfterRedirect atomic.Value
		var evilSrv *httptest.Server
		originSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/start" {
				http.Redirect(w, r, evilSrv.URL+"/hop", http.StatusFound)
				return
			}
			originAuthAfterRedirect.Store(r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
		}))
		defer originSrv.Close()

		evilSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.Header.Get("Authorization"))
			http.Redirect(w, r, originSrv.URL+"/landed", http.StatusFound)
		}))
		defer evilSrv.Close()

		rt := NewAuthRetryTransportWithOrigin(http.DefaultTransport, originSrv.URL, "secret-jwt", func(
			context.Context,
		) (string, error) {
			return "refreshed", nil
		})
		hc := &http.Client{Transport: rt}
		resp, err := hc.Get(originSrv.URL + "/start")
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, "", originAuthAfterRedirect.Load(), "boomerang redirect must not re-attach bearer token")
	})

	t.Run("does not recurse on 401 from path-prefixed /auth/refresh", func(t *testing.T) {
		var refreshCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		rt := NewAuthRetryTransportWithOrigin(http.DefaultTransport, srv.URL+"/subpath", "stale-jwt", func(
			context.Context,
		) (string, error) {
			refreshCalls.Add(1)
			return "new-jwt", nil
		})
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/subpath/api/v1/auth/refresh", strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, int32(0), refreshCalls.Load(), "401 on prefixed /auth/refresh must not trigger refreshFn")
	})

	t.Run("drainAndClose bounds read bytes on oversized 401 response", func(t *testing.T) {
		cr := &countingZeroReader{limit: 32 << 20} // 32 MiB
		drainAndClose(&http.Response{Body: io.NopCloser(cr)})
		assert.LessOrEqual(t, cr.readBytes, int64(64<<10), "drainAndClose must bound discarded bytes")
	})
}

func TestW7BuildClientSupportsEnvPrefixedProfileSecretRefs(t *testing.T) {
	var gotAPIKey atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey.Store(r.Header.Get("X-API-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	defer srv.Close()

	t.Setenv("W7_TEST_CUSTOM_KEY", "  gy_custom_env_123  ")
	f := &Factory{
		IgnoreProjectLink: true,
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentProfile: "prod",
				Profiles: map[string]config.Profile{
					"prod": {Host: srv.URL, APIKeyRef: "env:W7_TEST_CUSTOM_KEY"},
				},
			}, nil
		},
		Secrets: func() (secrets.Store, error) { return secrets.NewMemStore(), nil },
	}
	f.Client = func() (*sdk.Client, error) { return buildClient(f) }
	cli, err := f.Client()
	require.NoError(t, err)
	_, err = cli.ListKnowledgeBases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "gy_custom_env_123", gotAPIKey.Load())
}
