package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/prompt"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
)

func TestW7AuthLoginWithTokenBoundsAndControlCharsAndEnvRefsAndDeterministicLogout(t *testing.T) {
	t.Run("auth login --with-token rejects embedded newline/control chars and oversized stdin", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		store := secrets.NewMemStore()
		cfg := &config.Config{
			CurrentProfile: "prod",
			Profiles: map[string]config.Profile{
				"prod": {Host: "http://127.0.0.1:8080/"},
			},
		}
		f := &cmdutil.Factory{
			Config:  func() (*config.Config, error) { return cfg, nil },
			Secrets: func() (secrets.Store, error) { return store, nil },
		}

		err := runLogin(
			context.Background(),
			&LoginOptions{WithToken: true, StdinReader: strings.NewReader("gy_first_line\ngy_second_line\n")},
			&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
			f,
			nil,
		)
		require.Error(t, err)
		var ce *cmdutil.Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code, "multi-line stdin token must fail as input.invalid_argument, not network.error")

		err = runLogin(
			context.Background(),
			&LoginOptions{WithToken: true, StdinReader: strings.NewReader(strings.Repeat("a", 32<<10))},
			&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
			f,
			nil,
		)
		require.Error(t, err)
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	})

	t.Run("auth token honors env:<VAR> in APIKeyRef and TokenRef", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		t.Setenv("W7_CUSTOM_TOKEN", "gy_from_profile_env_ref")
		cfg := &config.Config{
			CurrentProfile: "prod",
			Profiles: map[string]config.Profile{
				"prod": {Host: "https://kb.example.com", APIKeyRef: "env:W7_CUSTOM_TOKEN"},
			},
		}
		f := &cmdutil.Factory{
			Config:  func() (*config.Config, error) { return cfg, nil },
			Secrets: func() (secrets.Store, error) { return secrets.NewMemStore(), nil },
		}
		require.NoError(t, runToken(f, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}))
		assert.Equal(t, "gy_from_profile_env_ref", out.String())
	})

	t.Run("auth refresh validates corrupt profile host before network call", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		cfg := &config.Config{
			CurrentProfile: "prod",
			Profiles: map[string]config.Profile{
				"prod": {Host: "ftp://bad-host", RefreshRef: "mem://prod/refresh"},
			},
		}
		f := &cmdutil.Factory{
			Config:  func() (*config.Config, error) { return cfg, nil },
			Secrets: func() (secrets.Store, error) { return secrets.NewMemStore(), nil },
		}
		cmd := NewCmdRefresh(f)
		cmd.SetArgs([]string{"--dry-run"})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		err := cmd.Execute()
		require.Error(t, err)
		var ce *cmdutil.Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeLocalConfigCorrupt, ce.Code)
	})

	t.Run("auth logout --all returns sorted profile names deterministically", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		cfg := &config.Config{
			CurrentProfile: "zeta",
			Profiles: map[string]config.Profile{
				"zeta":  {Host: "https://z.example.com"},
				"alpha": {Host: "https://a.example.com"},
				"mid":   {Host: "https://m.example.com"},
			},
		}
		targets, err := pickLogoutTargets(&LogoutOptions{All: true}, cfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"alpha", "mid", "zeta"}, targets)

		// Corrupt profile key with traversal in config.yaml must be rejected
		badCfg := &config.Config{
			Profiles: map[string]config.Profile{
				"../escape": {Host: "https://e.example.com"},
			},
		}
		f := &cmdutil.Factory{
			Config:   func() (*config.Config, error) { return badCfg, nil },
			Secrets:  func() (secrets.Store, error) { return secrets.NewMemStore(), nil },
			Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		}
		err = runLogout(&LogoutOptions{All: true, Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, f)
		require.Error(t, err)
	})
}
