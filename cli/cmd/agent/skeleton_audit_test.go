package agentcmd

import (
	"errors"
	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCreateSkeletonCommandWorksOffline(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	calls := 0
	cmd := NewCmdCreate(&cmdutil.Factory{Client: func() (*sdk.Client, error) { calls++; return nil, errors.New("no credentials") }})
	cmd.SetArgs([]string{"--generate-skeleton"})
	require.NoError(t, cmd.Execute())
	require.Zero(t, calls)
	require.Contains(t, out.String(), "model_id:")
}
