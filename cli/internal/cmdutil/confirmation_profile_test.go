package cmdutil

import (
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/prompt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestConfirmationRetryPinsResolvedProfile(t *testing.T) {
	iostreams.SetForTest(t)
	previous := globalProfile
	SetProfile("staging")
	t.Cleanup(func() { SetProfile(previous) })
	for _, batch := range []bool{false, true} {
		original := []string{"genyee", "doc", "delete", "doc-1", "-y"}
		var err error
		if batch {
			err = ConfirmDestructiveBatch(prompt.AgentPrompter{}, false, true, "delete", "document", 1, "doc.delete", original)
		} else {
			err = ConfirmDestructive(prompt.AgentPrompter{}, false, true, "delete", "document", "doc-1", "doc.delete", original)
		}
		var typed *Error
		require.ErrorAs(t, err, &typed)
		require.Equal(t, []string{"genyee", "--profile", "staging", "doc", "delete", "doc-1", "-y"}, typed.RetryArgv)
		require.Equal(t, []string{"genyee", "doc", "delete", "doc-1", "-y"}, original, "must not mutate caller argv")
	}
}
