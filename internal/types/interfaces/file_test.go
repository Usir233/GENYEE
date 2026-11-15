package interfaces

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndependentFileWritesNilContextSafe(t *testing.T) {
	var nilCtx context.Context
	require.NotPanics(t, func() {
		assert.False(t, IndependentFileWrites(nilCtx))
		ctx := WithIndependentFileWrites(nilCtx)
		assert.True(t, IndependentFileWrites(ctx))
	})
}
