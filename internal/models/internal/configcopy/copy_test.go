package configcopy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type sampleWithUnexported struct {
	Exported   map[string][]string
	Ptr        *string
	unexported int
}

func TestCloneDeepCopiesExportedContainersAndSurvivesUnexportedFields(t *testing.T) {
	val := "orig"
	src := sampleWithUnexported{
		Exported:   map[string][]string{"k": {"a", "b"}},
		Ptr:        &val,
		unexported: 42,
	}
	dst := Clone(src)
	assert.Equal(t, 42, dst.unexported)
	assert.NotSame(t, src.Ptr, dst.Ptr)

	*dst.Ptr = "changed"
	dst.Exported["k"][0] = "changed"
	dst.Exported["new"] = []string{"x"}

	assert.Equal(t, "orig", *src.Ptr)
	assert.Equal(t, []string{"a", "b"}, src.Exported["k"])
	assert.NotContains(t, src.Exported, "new")
}
