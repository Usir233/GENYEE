package modelcmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/prompt"
	sdk "github.com/Usir233/GENYEE/client"
)

type w8LeakyViewSvc struct{}

func (w8LeakyViewSvc) GetModel(context.Context, string) (*sdk.Model, error) {
	return &sdk.Model{
		ID:     "mdl_1",
		Name:   "gpt-4o",
		Type:   sdk.ModelTypeKnowledgeQA,
		Source: sdk.ModelSourceRemote,
		Parameters: sdk.ModelParameters{
			"provider":   "openai",
			"api_key":    "sk-supersecretkey123456",
			"app_secret": "app_super_secret",
		},
	}, nil
}

func TestW8_Model_CapsStdinValidatesIDsBaseURLAndScrubsSecrets(t *testing.T) {
	out, _ := iostreams.SetForTest(t)

	// 1. readStdinTrimmed must cap oversized stdin and reject embedded newlines/control chars.
	_, err := readStdinTrimmed(strings.NewReader(strings.Repeat("x", (16<<10)+10)))
	require.Error(t, err, "oversized --api-key-stdin must be rejected")
	_, err = readStdinTrimmed(strings.NewReader("sk-line1\nsk-line2"))
	require.Error(t, err, "multi-line --api-key-stdin must be rejected")

	// 2. model delete / update / view must validate <model-id> and model list/create/update must validate flags before
	// Client().
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
		root.SilenceErrors = true
		root.SilenceUsage = true
		return root.Execute()
	}

	for _, tc := range []struct {
		cmd  *cobra.Command
		args []string
	}{
		{NewCmdView(f), []string{"../bad"}},
		{NewCmdDelete(f), []string{"../bad", "--dry-run"}},
		{NewCmdUpdate(f), []string{"../bad", "--display-name", "x", "--dry-run"}},
		{NewCmdCreate(f), []string{"m", "--type", "Embedding", "--source", "local", "--dimension", "-5", "--dry-run"}},
		{
			NewCmdCreate(f),
			[]string{"m", "--type", "Embedding", "--source", "local", "--base-url", "ftp://bad", "--dry-run"},
		},
		{NewCmdUpdate(f), []string{"mdl_1", "--base-url", "javascript:alert(1)", "--dry-run"}},
		{NewCmdList(f), []string{"--type", "NotAType"}},
	} {
		err := exec(tc.cmd, tc.args...)
		require.Error(t, err)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce), "expected *cmdutil.Error for %v, got %v", tc.args, err)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	}

	// model delete without -y must exit 10 BEFORE Client().
	err = exec(NewCmdDelete(f), "mdl_1")
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)

	// 3. model view JSON must scrub api_key / app_secret from parameters if returned by server.
	out.Reset()
	require.NoError(
		t,
		runView(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, w8LeakyViewSvc{}, "mdl_1"),
	)
	if strings.Contains(out.String(), "sk-supersecretkey123456") || strings.Contains(out.String(), "app_super_secret") {
		t.Fatalf("model view leaked provider secret in JSON output: %s", out.String())
	}
}
