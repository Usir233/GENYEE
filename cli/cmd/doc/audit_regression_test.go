package doc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitStopsForCancelledDocument(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{"cancelled-doc": {"cancelled"}})
	res, err := waitForDocs(context.Background(), []string{"cancelled-doc"}, svc, WaitOptions{Timeout: 30 * time.Millisecond, Interval: time.Second})
	require.NoError(t, err)
	require.Empty(t, res.Timeout)
	require.Len(t, res.Failed, 1)
	require.Equal(t, "cancelled-doc", res.Failed[0].ID)
	require.Contains(t, res.Failed[0].Message, "cancelled")
	require.Equal(t, 1, res.ExitCode())
	require.Equal(t, 1, svc.calls["cancelled-doc"])
}
