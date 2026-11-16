package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
)

func TestW7RootRejectsInvalidProfileOverrideAndSchemaHandlesEdgeCases(t *testing.T) {
	t.Run("root PersistentPreRunE rejects shell-metacharacter --profile and GENYEE_PROFILE before execution", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		root := NewRootCmd(cmdutil.New())
		root.SetArgs([]string{"--profile", "prod; echo pwned", "version", "--format", "json"})
		err := root.Execute()
		require.Error(t, err)
		var ce *cmdutil.Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

		t.Setenv("GENYEE_PROFILE", "-y")
		root2 := NewRootCmd(cmdutil.New())
		root2.SetArgs([]string{"version", "--format", "json"})
		err = root2.Execute()
		require.Error(t, err)
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	})

	t.Run("schema does not panic on Unicode-whitespace-only arg and rejects trailing tokens on leaf commands", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		_, err := resolveSchemaTarget(NewRootCmd(cmdutil.New()), []string{"\u3000"})
		require.Error(t, err)

		root := NewRootCmd(cmdutil.New())
		root.SetArgs([]string{"schema", "version", "bogus", "--format", "json"})
		err = root.Execute()
		require.Error(t, err)
		var ce *cmdutil.Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeInputUnknownSubcommand, ce.Code)
	})

	t.Run("schema index includes runnable parent command kb config", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		root := NewRootCmd(cmdutil.New())
		root.SetArgs([]string{"schema", "--format", "json"})
		require.NoError(t, root.Execute())

		var env struct {
			Data []schemaIndexEntry `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &env))
		foundKBConfig := false
		for _, e := range env.Data {
			if e.Command == "kb config" {
				foundKBConfig = true
			}
			assert.NotEqual(t, "kb", e.Command, "pure group command must not appear in schema index")
		}
		assert.True(t, foundKBConfig, "runnable `kb config` command must appear in `genyee schema` index")
	})
}
