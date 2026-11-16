package sessioncmd

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

type w8SessionStub struct{}

func (w8SessionStub) ContinueStream(context.Context, string, string, func(*sdk.StreamResponse) error, ...sdk.ResourceURLOptions) error {
	return nil
}

func (w8SessionStub) GetSession(context.Context, string) (*sdk.Session, error) {
	return nil, nil
}

func (w8SessionStub) LoadMessages(context.Context, string, int, *interface{}, ...sdk.ResourceURLOptions) ([]sdk.Message, error) {
	return nil, nil
}

func TestW8_Session_ValidatesIDsSinceOverflowConfirmOrderAndResumeCleanStdout(t *testing.T) {
	out, _ := iostreams.SetForTest(t)

	// 1. parseSinceDuration must reject NaNd, +Infd, and int64-overflowing day counts like 200000d.
	for _, bad := range []string{"NaNd", "+Infd", "Infd", "200000d", "-5d"} {
		_, err := parseSinceDuration(bad)
		require.Error(t, err, "parseSinceDuration(%q) must error", bad)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce))
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	}

	// 2. session resume with unsafe IDs must fail BEFORE writing the NDJSON init line to stdout.
	out.Reset()
	err := runResume(context.Background(), &ResumeOptions{SessionID: "../bad", MessageID: "msg_1"}, nil, w8SessionStub{})
	require.Error(t, err)
	assert.Empty(t, out.String(), "stdout must stay empty when session resume rejects invalid IDs")

	// 3. session subcommands must validate IDs and run exit-10 confirmation BEFORE f.Client().
	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("Client() must not run before preflight validation or confirmation")
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
		root.SetContext(context.Background())
		root.SilenceErrors = true
		root.SilenceUsage = true
		return root.Execute()
	}

	for _, tc := range []struct {
		cmd  *cobra.Command
		args []string
	}{
		{NewCmdAsk(f), []string{"--agent", "../bad", "hello"}},
		{NewCmdDelete(f), []string{"../bad", "--dry-run"}},
		{NewCmdStop(f), []string{"../bad", "--message", "msg_1", "--dry-run"}},
		{NewCmdResume(f), []string{"../bad", "--message", "msg_1"}},
		{NewCmdView(f), []string{"../bad"}},
		{NewCmdToolApproval(f), []string{"resolve", "../bad", "--dry-run"}},
		{NewCmdList(f), []string{"--since", "200000d"}},
	} {
		err := exec(tc.cmd, tc.args...)
		require.Error(t, err)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce), "expected *cmdutil.Error for %v, got %v", tc.args, err)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	}

	for _, tc := range []struct {
		cmd  *cobra.Command
		args []string
	}{
		{NewCmdDelete(f), []string{"sess_1"}},
		{NewCmdToolApproval(f), []string{"resolve", "pend_1"}},
	} {
		err := exec(tc.cmd, tc.args...)
		require.Error(t, err)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce))
		assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	}
}
