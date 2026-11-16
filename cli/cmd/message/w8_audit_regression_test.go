package messagecmd

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

type w8NilItemSearchSvc struct{}

func (w8NilItemSearchSvc) SearchMessages(context.Context, *sdk.SearchMessagesRequest) (*sdk.MessageSearchResult, error) {
	return &sdk.MessageSearchResult{
		Items: []*sdk.MessageSearchGroupItem{nil, {SessionID: "sess_1", QueryContent: "q", AnswerContent: "a", Score: 0.9}},
		Total: 1,
	}, nil
}

func TestW8_Message_ValidatesIDsConfirmOrderBeforeFlagAndNilSearchItems(t *testing.T) {
	iostreams.SetForTest(t)

	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("Client() must not be called before input validation or confirmation")
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

	// 1. message delete rejects traversal IDs before dry-run/confirm, and confirms BEFORE f.Client().
	err := exec(NewCmdDelete(f), "../bad", "--session", "sess_1", "--dry-run")
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	err = exec(NewCmdDelete(f), "msg_1", "--session", "sess_1")
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)

	// 2. message list validates --session and --before BEFORE f.Client().
	err = exec(NewCmdList(f), "--session", "../bad")
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	err = exec(NewCmdList(f), "--session", "sess_1", "--before", "not-a-timestamp")
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	// 3. message search validates --session slice and tolerates nil element in res.Items.
	err = exec(NewCmdSearch(f), "deploy", "--session", "../bad")
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	require.NoError(t, runSearch(context.Background(), &SearchOptions{Query: "deploy", Limit: 10}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, w8NilItemSearchSvc{}))
}
