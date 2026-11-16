package profilecmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
)

func TestW7ProfileUseDryRunValidatesAndCorruptConfigMapsTypedCodeAndSortedHints(t *testing.T) {
	t.Run("profile use --dry-run rejects nonexistent profile with local.profile_not_found", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		xdgDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		require.NoError(t, config.Save(&config.Config{
			CurrentProfile: "prod",
			Profiles: map[string]config.Profile{
				"prod":    {Host: "https://prod.example.com"},
				"staging": {Host: "https://staging.example.com"},
			},
		}))

		cmd := NewCmdUse(cmdutil.New())
		cmd.SetArgs([]string{"does-not-exist", "--dry-run"})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		err := cmd.Execute()
		require.Error(t, err)
		var ce *cmdutil.Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeLocalProfileNotFound, ce.Code)
	})

	t.Run("profile list/use/add/remove map corrupt config.yaml to local.config_corrupt", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		xdgDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		cfgPath := filepath.Join(xdgDir, "genyee", "config.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o700))
		require.NoError(t, os.WriteFile(cfgPath, []byte(":::not-valid-yaml\n\t["), 0o600))

		for _, c := range []*struct {
			name string
			err  error
		}{
			{"list", func() error {
				cmd := NewCmdList(cmdutil.New())
				cmd.SilenceErrors, cmd.SilenceUsage = true, true
				return cmd.Execute()
			}()},
			{"use", func() error {
				cmd := NewCmdUse(cmdutil.New())
				cmd.SetArgs([]string{"prod"})
				cmd.SilenceErrors, cmd.SilenceUsage = true, true
				return cmd.Execute()
			}()},
			{"add", func() error {
				cmd := NewCmdAdd(cmdutil.New())
				cmd.SetArgs([]string{"prod", "--host", "https://kb.example.com"})
				cmd.SilenceErrors, cmd.SilenceUsage = true, true
				return cmd.Execute()
			}()},
			{"remove", func() error {
				cmd := NewCmdRemove(cmdutil.New())
				cmd.SetArgs([]string{"prod"})
				cmd.SilenceErrors, cmd.SilenceUsage = true, true
				return cmd.Execute()
			}()},
		} {
			require.Error(t, c.err, "profile %s must fail on corrupt config", c.name)
			var ce *cmdutil.Error
			require.ErrorAs(t, c.err, &ce, "profile %s must return typed *cmdutil.Error", c.name)
			assert.Equal(t, cmdutil.CodeLocalConfigCorrupt, ce.Code)
		}
	})

	t.Run("profileKeys sorts keys deterministically", func(t *testing.T) {
		keys := profileKeys(map[string]config.Profile{
			"zeta":  {},
			"alpha": {},
			"mid":   {},
		})
		assert.Equal(t, []string{"alpha", "mid", "zeta"}, keys)
	})
}
