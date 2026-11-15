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

func TestConfirmationRetrySkipsEnvAuthDeduplicatesAndExcludesGlobalOps(t *testing.T) {
	iostreams.SetForTest(t)
	previous := globalProfile
	SetProfile("staging")
	t.Cleanup(func() { SetProfile(previous) })

	// Already contains --profile -> no duplicate injection.
	alreadyPinned := []string{"genyee", "--profile", "staging", "doc", "delete", "doc-1", "-y"}
	require.Equal(t, alreadyPinned, confirmationRetryArgv(alreadyPinned))

	alreadyEq := []string{"genyee", "--profile=staging", "doc", "delete", "doc-1", "-y"}
	require.Equal(t, alreadyEq, confirmationRetryArgv(alreadyEq))

	// profile remove and auth logout --all must not inject --profile.
	profRemove := []string{"genyee", "profile", "remove", "staging", "-y"}
	require.Equal(t, profRemove, confirmationRetryArgv(profRemove))

	logoutAll := []string{"genyee", "auth", "logout", "--all", "-y"}
	require.Equal(t, logoutAll, confirmationRetryArgv(logoutAll))

	// Stateless env auth without GENYEE_PROFILE must not inject local profile.
	t.Setenv("GENYEE_API_KEY", "sk-stateless")
	t.Setenv("GENYEE_PROFILE", "")
	docDel := []string{"genyee", "doc", "delete", "doc-1", "-y"}
	require.Equal(t, docDel, confirmationRetryArgv(docDel))
}
