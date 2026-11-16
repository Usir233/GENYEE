package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/secrets"
	sdk "github.com/Usir233/GENYEE/client"
)

type fakeNilUserDoctorSvc struct{}

func (f *fakeNilUserDoctorSvc) PingBaseURL(_ context.Context) error { return nil }
func (f *fakeNilUserDoctorSvc) GetCurrentUser(_ context.Context) (*sdk.CurrentUserResponse, error) {
	return &sdk.CurrentUserResponse{Success: false}, nil
}
func (f *fakeNilUserDoctorSvc) GetSystemInfo(_ context.Context) (*sdk.SystemInfo, error) {
	return &sdk.SystemInfo{Version: "0.10.0"}, nil
}

func TestW7DoctorRejectsEmptyAuthUserAndAllowsOfflineWhenConfigBroken(t *testing.T) {
	restore := SetCredStoreFactoryForTest(func() (secrets.Store, error) {
		return secrets.NewMemStore(), nil
	})
	defer restore()

	t.Run("auth_credential fails when GetCurrentUser returns Success=false or nil User", func(t *testing.T) {
		res := runChecks(context.Background(), &Options{}, &fakeNilUserDoctorSvc{}, "0.10.0")
		assert.Equal(t, StatusFail, res.Checks[1].Status, "auth_credential must fail when Success=false / User==nil")
		assert.Equal(t, StatusSkip, res.Checks[2].Status, "server_version must cascade-skip when auth_credential fails")
	})

	t.Run("doctor --offline still runs credential_storage even if f.Config() errors", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		f := &cmdutil.Factory{
			Config: func() (*config.Config, error) {
				return nil, cmdutil.Wrapf(cmdutil.CodeLocalConfigCorrupt, errors.New("bad yaml"), "corrupt")
			},
		}
		cmd := NewCmd(f)
		cmd.SetArgs([]string{"--offline"})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		require.NoError(t, cmd.Execute())
		assert.Contains(t, out.String(), `"credential_storage"`)
	})
}
