package profilecmd

import (
	"errors"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
	"github.com/Usir233/GENYEE/cli/internal/testutil"
	"github.com/stretchr/testify/require"
)

type removeFailureStore struct{ *secrets.MemStore }

func (s *removeFailureStore) Delete(string, string) error { return errors.New("keychain locked") }

func TestRemoveKeepsProfileWhenCredentialsCannotBeDeleted(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)
	cfg := &config.Config{CurrentProfile: "prod", Profiles: map[string]config.Profile{"prod": {Host: "https://example.invalid", TokenRef: "mem://prod/access"}}}
	require.NoError(t, config.Save(cfg))
	err := runRemove(&RemoveOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, "prod", &removeFailureStore{secrets.NewMemStore()}, &testutil.ConfirmPrompter{})
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, cmdutil.CodeLocalKeychainDenied, typed.Code)
	require.Empty(t, out.String())
	saved, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, cfg, saved)
}
