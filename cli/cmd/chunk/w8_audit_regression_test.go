package chunkcmd

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

type w8NilChunkViewSvc struct{}

func (w8NilChunkViewSvc) GetChunkByIDOnly(context.Context, string) (*sdk.Chunk, error) {
	return nil, nil
}

func TestW8_Chunk_ValidatesIDsConfirmationOrderNilViewAndSanitizesText(t *testing.T) {
	out, _ := iostreams.SetForTest(t)

	// 1. chunk delete must reject traversal IDs before dry-run/confirm/Client(),
	// and for valid IDs without -y must return confirmation_required (exit 10) BEFORE f.Client().
	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("Client() must not run before ID validation or exit-10 confirmation")
			return nil, nil
		},
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	wrap := func(sub *cobra.Command, args ...string) error {
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

	err := wrap(NewCmdDelete(f), "../bad", "--doc", "doc_1", "--dry-run")
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	err = wrap(NewCmdDelete(f), "chk_1", "--doc", "doc_1")
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)

	// 2. chunk list must reject traversal --doc in validateListOpts.
	err = validateListOpts(&ListOptions{DocID: "../secret", Limit: 10, PageSize: 10})
	require.Error(t, err)

	// 3. chunk view must not panic on (nil, nil) response, and renderChunk must strip OSC 52.
	err = runView(
		context.Background(),
		&ViewOptions{ChunkID: "chk_1"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText},
		w8NilChunkViewSvc{},
	)
	require.Error(t, err)

	out.Reset()
	renderChunk(out, &sdk.Chunk{
		ID:      "chk_1",
		Content: "hello \x1b]52;c;SGVsbG8=\x07world",
	})
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "SGVsbG8=") {
		t.Fatalf("renderChunk leaked OSC 52 sequence: %q", out.String())
	}
}
