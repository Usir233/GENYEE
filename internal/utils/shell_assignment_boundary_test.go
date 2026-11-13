package utils

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellAssignmentLiteralMatchesShellQuoting(t *testing.T) {
	for _, word := range []string{
		`'first'"tail"`, `"first\"tail"`, `first\ tail`, `'first'\''tail'`,
		`"first\\tail"`, `"first\qtail"`, `first\;tail`, `first\|tail`,
		`'literal${NAME}$(never-run)'`, `"literal\$NAME"`, "first\\\ntail", "'first\ntail'",
		"first\rcanary_tail",
	} {
		t.Run(word, func(t *testing.T) {
			// Fixed fixtures only. printf is a shell builtin; these literal
			// words cannot execute a command or access user data.
			cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", "TOKEN="+word+`; printf '%s' "$TOKEN"`)
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			want, err := cmd.Output()
			require.NoError(t, err)
			command := "export TOKEN=" + word + "; ./run --model safe"
			require.Equal(t, string(want), ExtractShellAssignments(command)["TOKEN"])
			require.Equal(t, "export TOKEN=***; ./run --model safe", MaskCommandAssignments(command))
		})
	}
}

func TestShellAssignmentExpressionsCannotPersistPartialCredentials(t *testing.T) {
	for _, word := range []string{
		`$EXISTING`, `"$EXISTING"`, `$(printf 'secret tail')`, "`printf 'secret tail'`",
		`${EXISTING:-'secret tail'}`, `$'secret\ntail'`, `('secret tail')`, `<(printf 'secret tail')`,
		`prefix<(printf 'secret tail')`, `prefix:~/secret`,
		`$[1 + 654321]`,
		`'unterminated secret tail`, `"unterminated secret tail`, ``,
	} {
		t.Run(word, func(t *testing.T) {
			command := "TOKEN=old; TOKEN=" + word
			require.NotContains(t, ExtractShellAssignments(command), "TOKEN")
			require.NotContains(t, MaskCommandAssignments(command), "secret")
			require.Contains(t, MaskCommandAssignments(command), "TOKEN=***")
		})
	}
}

func TestShellAssignmentCaptureIgnoresArgumentAndScriptText(t *testing.T) {
	for _, command := range []string{
		`TOKEN=real ./run --note "memo TOKEN=fake rest"`,
		`TOKEN=real ./run TOKEN=fake`,
		`TOKEN=real ./run 'TOKEN=fake'`,
		"TOKEN=real ./run <<'END'\nTOKEN=fake\nEND",
		"TOKEN=real; # comment; TOKEN=fake\nprintf ok",
	} {
		t.Run(command, func(t *testing.T) {
			require.Equal(t, map[string]string{"TOKEN": "real"}, ExtractShellAssignments(command))
		})
	}
}

func TestShellAssignmentOpaqueExpressionsOmitNestedSecrets(t *testing.T) {
	for _, command := range []string{
		`TOKEN=prefix<(printf 'canary_tail') ./run`,
		`echo $(TOKEN=canary_tail ./run)`,
		"TOKEN=first\rcanary_tail ./run",
		`bash -c 'export TOKEN=canary_tail; printf ok'`,
		`bash -c 'export TO'"KEN=canary_tail; printf ok"`,
	} {
		require.NotContains(t, MaskCommandAssignments(command), "canary_tail")
	}
	require.NotContains(t, MaskCommandAssignments(`TOKEN=$[1 + 654321] ./run`), "654321")
}

func TestShellAssignmentNonAssignmentTextUnchanged(t *testing.T) {
	const command = "./run --model safe --url https://example.invalid/?a=b"
	const empty = ""
	for _, value := range []string{command, empty} {
		require.Equal(t, value, MaskCommandAssignments(value))
		require.Empty(t, ExtractShellAssignments(value))
	}
}
