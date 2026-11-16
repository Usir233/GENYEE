package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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

func newDryFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("Client() must not be called on rejected preflight")
			return nil, nil
		},
		Prompter: func() prompt.Prompter {
			return prompt.AgentPrompter{}
		},
	}
}

func wrapAPIRoot(sub *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "genyee"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "")
	pf.StringP("jq", "q", "", "")
	root.AddCommand(sub)
	root.SetArgs(append([]string{"api"}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

func TestW8_API_RejectsTraversalProtocolRelativeBadMethodAndNaNField(t *testing.T) {
	iostreams.SetForTest(t)

	// 1. Protocol-relative //evil.com, dot-segment traversal, fragment, and percent-encoded traversal
	// must be rejected as input.invalid_argument BEFORE dry-run / confirmation / network.
	for _, badPath := range []string{
		"//evil.example.com/api/v1/knowledge-bases",
		"/api/v1/../admin",
		"/api/v1/%2e%2e/admin",
		"/api/v1/items#frag",
		`/api/v1\evil`,
	} {
		err := wrapAPIRoot(NewCmd(newDryFactory(t)), "-X", "DELETE", badPath, "--dry-run").Execute()
		require.Error(t, err, "path %q should be rejected", badPath)
		var ce *cmdutil.Error
		require.True(t, errors.As(err, &ce), "expected *cmdutil.Error for %q, got %v", badPath, err)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
	}

	// 2. Invalid HTTP method token with space/invalid token chars must fail before dry-run/confirm.
	err := wrapAPIRoot(NewCmd(newDryFactory(t)), "-X", "BAD METHOD", "/api/v1/items", "--dry-run").Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	// 3. -F score=NaN / -F score=Inf must not crash json.Marshal as untyped error; treat as string or reject cleanly.
	body, berr := buildFieldBody([]string{"score=NaN", "limit=Inf"})
	require.NoError(t, berr, "-F NaN/Inf should serialize safely without crashing json.Marshal")
	assert.Contains(t, string(body), `"NaN"`)

	// 4. --paginate must enforce a page cap when upstream server loops endlessly.
	calls := 0
	endlessSvc := &fakeAPISvc{
		do: func(_, _ string, _ any) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"id":"1"}],"total":999999999,"page":1,"page_size":1}`)),
			}, nil
		},
	}
	err = runAPI(context.Background(), &Options{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, endlessSvc, http.MethodGet, "/api/v1/items", true)
	require.Error(t, err)
	assert.LessOrEqual(t, calls, maxPaginatePages+1)
	assert.True(t, strings.Contains(err.Error(), "page") || strings.Contains(err.Error(), "limit"))
}
