package collaboration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpawnRejectsMismatchedRequestKeyReuse(t *testing.T) {
	s, scope := testStore(t)
	r := New(s, nil)
	ctx := context.Background()

	firstID, err := r.Spawn(ctx, scope, "shared-key", "worker_alpha", "agent-a", "{}", "inspect alpha")
	require.NoError(t, err)
	require.NotEmpty(t, firstID)

	// Exact retry with same task name and agent ID remains idempotent.
	retryID, err := r.Spawn(ctx, scope, "shared-key", "worker_alpha", "agent-a", "{}", "inspect alpha")
	require.NoError(t, err)
	require.Equal(t, firstID, retryID)

	// Reusing the same request key for a different child task must fail closed
	// instead of silently returning worker_alpha's member ID.
	_, err = r.Spawn(ctx, scope, "shared-key", "worker_beta", "agent-a", "{}", "inspect beta")
	require.Error(t, err)
	require.Len(t, read(t, s, scope).Members, 1)
}
