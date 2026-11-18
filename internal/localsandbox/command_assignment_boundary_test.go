package localsandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreviewCommandMasksCompleteAssignmentWords(t *testing.T) {
	values := []string{
		`'first'"canary_tail"`, `"first\"canary_tail"`,
		`first\ canary_tail`, `"unterminated canary_tail`, "first\\\ncanary_tail",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			got := previewCommand("export TOKEN=" + value + " ./run")
			require.NotContains(t, got, "canary_tail")
			require.Contains(t, got, "TOKEN=***")
		})
	}
}
