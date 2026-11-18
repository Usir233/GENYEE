package kb

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/prompt"
	sdk "github.com/Usir233/GENYEE/client"
)

type w8NilCheckSvc struct{}

func (w8NilCheckSvc) GetKnowledgeBase(context.Context, string) (*sdk.KnowledgeBase, error) {
	return nil, nil
}

func (w8NilCheckSvc) ListKnowledgeWithFilter(
	context.Context,
	string,
	int,
	int,
	sdk.KnowledgeListFilter,
) ([]sdk.Knowledge, int64, error) {
	return nil, 0, nil
}

func TestW8_KB_ValidatesResourceIDsConfirmOrderAndNilResponses(t *testing.T) {
	iostreams.SetForTest(t)

	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("Client() must not be called before kb-id validation or confirmation gate")
			return nil, nil
		},
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	exec := func(sub *cobra.Command, args ...string) error {
		root := &cobra.Command{Use: "genyee"}
		pf := root.PersistentFlags()
		pf.BoolP("yes", "y", false, "")
		pf.String("format", "", "")
		pf.StringP("jq", "q", "", "")
		root.AddCommand(sub)
		root.SetArgs(append([]string{sub.Name()}, args...))
		root.SilenceErrors = true
		root.SilenceUsage = true
		return root.Execute()
	}

	// 1. Traversal kb-id must fail with input.invalid_argument across kb subcommands.
	for _, tc := range []struct {
		cmd  *cobra.Command
		args []string
	}{
		{NewCmdView(f), []string{"../secret"}},
		{NewCmdStatus(f), []string{"../secret"}},
		{NewCmdCheck(f), []string{"../secret"}},
		{NewCmdConfig(f), []string{"../secret"}},
		{NewCmdDelete(f), []string{"../secret", "--dry-run"}},
		{NewCmdEdit(f), []string{"../secret", "--name", "n", "--dry-run"}},
		{NewCmdPin(f), []string{"../secret", "--dry-run"}},
		{NewCmdConfig(f), []string{"set", "../secret", "--chat-model", "m1", "--embedding-model", "m2", "--dry-run"}},
	} {
		err := exec(tc.cmd, tc.args...)
		require.Error(t, err)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce), "expected *cmdutil.Error for %v, got %v", tc.args, err)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	}

	// 2. Empty --name "" on kb update must be rejected before dry-run/confirm.
	err := exec(NewCmdEdit(f), "kb_1", "--name", "   ", "--dry-run")
	require.Error(t, err)

	// 3. Valid kb delete / kb update / kb config set without -y must exit 10 BEFORE f.Client().
	for _, tc := range []struct {
		cmd  *cobra.Command
		args []string
	}{
		{NewCmdDelete(f), []string{"kb_1"}},
		{NewCmdEdit(f), []string{"kb_1", "--name", "Renamed"}},
		{NewCmdConfig(f), []string{"set", "kb_1", "--chat-model", "m1", "--embedding-model", "m2"}},
	} {
		err := exec(tc.cmd, tc.args...)
		require.Error(t, err)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce))
		assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	}

	// 4. runCheck must not panic if GetKnowledgeBase returns (nil, nil).
	res, err := runCheck(context.Background(), w8NilCheckSvc{}, "kb_1")
	require.NoError(t, err)
	assert.False(t, res.Reachable)
}
