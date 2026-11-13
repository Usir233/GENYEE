package cmd

import (
	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSchemaRejectsBlankCommandWithoutPanic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	for _, arg := range []string{"", " \t "} {
		root := NewRootCmd(cmdutil.New())
		root.SetArgs([]string{"schema", arg})
		var err error
		require.NotPanics(t, func() { err = root.Execute() })
		require.Error(t, err)
		require.Equal(t, cmdutil.CodeInputInvalidArgument, cmdutil.AsError(err).Code)
	}
}

func TestConfirmationRetainsWritePayload(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args, payload []string
	}{
		{"delete body", []string{"api", "-X", "DELETE", "/api/v1/items", "-d", `{"ids":["chosen"]}`}, []string{"-d", `{"ids":["chosen"]}`}},
		{"model params", []string{"model", "update", "model-id", "--param", "temperature=0.5", "--param", "top_p=0.9"}, []string{"--param", "temperature=0.5", "--param", "top_p=0.9"}},
		{"model key stdin", []string{"model", "update", "model-id", "--api-key-stdin"}, []string{"--api-key-stdin=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			_, _ = iostreams.SetForTest(t)
			root := NewRootCmd(cmdutil.New())
			root.SetArgs(tc.args)
			err := root.Execute()
			ce := cmdutil.AsError(err)
			require.NotNil(t, ce)
			require.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
			for _, arg := range tc.payload {
				require.Contains(t, ce.RetryArgv, arg)
			}
		})
	}
}
