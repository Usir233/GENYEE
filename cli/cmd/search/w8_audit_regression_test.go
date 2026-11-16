package search

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
)

func TestW8_Search_ValidatesThresholdsPageSizeBeforeClientAndSanitizesChunks(t *testing.T) {
	out, _ := iostreams.SetForTest(t)

	// 1. NaN / Inf / out-of-[0,1] thresholds must fail with input.invalid_argument.
	for _, bad := range []ChunksOptions{
		{Query: "q", VectorThreshold: math.NaN()},
		{Query: "q", VectorThreshold: math.Inf(1)},
		{Query: "q", VectorThreshold: -0.1},
		{Query: "q", VectorThreshold: 1.2},
		{Query: "q", KeywordThreshold: -0.01},
		{Query: "q", KeywordThreshold: 2.0},
	} {
		err := bad.validate()
		require.Error(t, err)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce))
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	}

	// 2. search docs and search sessions must validate --page-size BEFORE f.Client().
	f := &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("Client() must not be called when --page-size is invalid")
			return nil, nil
		},
	}
	exec := func(sub *cobra.Command, args ...string) error {
		root := &cobra.Command{Use: "genyee"}
		pf := root.PersistentFlags()
		pf.String("format", "", "")
		pf.StringP("jq", "q", "", "")
		root.AddCommand(sub)
		root.SetArgs(append([]string{sub.Name()}, args...))
		root.SetContext(context.Background())
		root.SilenceErrors = true
		root.SilenceUsage = true
		return root.Execute()
	}

	err := exec(NewCmdSessions(f), "onboarding", "--page-size", "0")
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	err = exec(NewCmdDocs(f), "spec", "--kb", "11111111-1111-4111-8111-111111111111", "--page-size", "9999")
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	// 3. renderChunkResults must tolerate nil elements and strip OSC 52 escape payloads.
	out.Reset()
	require.NoError(t, renderChunkResults([]*sdk.SearchResult{
		nil,
		{KnowledgeID: "doc\x1b[31m_1", Score: 0.8, Content: "hello \x1b]52;c;SGVsbG8=\x07world"},
	}, "kb\x1b]0;spoof\x07_1"))
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "SGVsbG8=") {
		t.Fatalf("renderChunkResults leaked escape sequence: %q", out.String())
	}
}
