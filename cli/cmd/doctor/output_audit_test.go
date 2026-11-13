package doctor

import (
	"errors"
	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDoctorWithEnvironmentHostIgnoresBrokenConfig(t *testing.T) {
	t.Setenv("GENYEE_TOKEN", "fixture-token")
	t.Setenv("GENYEE_HOST", "https://fixture.invalid")
	t.Setenv("GENYEE_BASE_URL", "")
	f := &cmdutil.Factory{Config: func() (*config.Config, error) { return nil, errors.New("broken local config") }}
	svc, err := buildServices(f)
	require.NoError(t, err)
	require.Equal(t, "https://fixture.invalid", svc.(*realServices).host)
}

func TestDoctorReturnsOutputProjectionErrors(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	withCredStoreFactory(t, func() (secrets.Store, error) { return secrets.NewMemStore(), nil })
	f := &cmdutil.Factory{Config: func() (*config.Config, error) { return &config.Config{}, nil }}
	cmd := NewCmd(f)
	cmd.Flags().String("format", "json", "")
	cmd.Flags().String("jq", `error("fixture projection error")`, "")
	cmd.SetArgs([]string{"--offline"})
	require.Error(t, cmd.Execute())
}
