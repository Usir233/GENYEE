package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellJobMasksCompleteAssignmentWords(t *testing.T) {
	for _, value := range []string{
		`'first'"canary_tail"`,
		`"first\"canary_tail"`,
		`first\ canary_tail`,
		`'first'\''canary_tail'`,
		`"unterminated canary_tail`,
	} {
		t.Run(value, func(t *testing.T) {
			command := "export TOKEN=" + value + " ./run --model safe"
			for _, request := range []ShellJobSubmitRequest{
				{Command: command},
				{Command: "execution wrapper", DisplayCommand: command},
			} {
				masked := request.MaskedCommand()
				require.NotContains(t, masked, "canary_tail", "persisted job metadata must not expose a value suffix")
				require.Contains(t, masked, "TOKEN=***")
			}
		})
	}
}

func TestSkillCredentialCaptureUsesCompleteLiteralAssignment(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{`'first'"canary_tail"`, "firstcanary_tail"},
		{`"first\"canary_tail"`, `first"canary_tail`},
		{`first\ canary_tail`, "first canary_tail"},
		{`'first'\''canary_tail'`, "first'canary_tail"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got := collectUsedSkillEnv("export TOKEN="+tc.value+" ./run", nil)
			require.Equal(t, tc.want, got["TOKEN"], "persisted skill credential must equal the literal shell word")
		})
	}
}

func TestShellJobMetadataHidesQuotedScriptsWithoutCapturingTheirVariables(t *testing.T) {
	const command = `TOKEN=real bash -c 'export TOKEN=canary_tail; printf ok'`
	require.NotContains(t, (ShellJobSubmitRequest{Command: command}).MaskedCommand(), "canary_tail")
	require.Equal(t, map[string]string{"TOKEN": "real"}, collectUsedSkillEnv(command, nil))
}
