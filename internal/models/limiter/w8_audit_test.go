package limiter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNilContextAndZeroDurationSafetyAcrossLimiters(t *testing.T) {
	// Zero/negative ttl and pollInterval must fall back to safe defaults rather than
	// panicking in time.NewTicker(l.ttl / 3) or busy-spinning in timer.Reset(0).
	lim, _, _ := newTestLimiter(t, 0, 0)

	//nolint:staticcheck // verifying defensive nil-context handling
	rel, err := lim.Acquire(nil, "m-redis", 1)
	require.NoError(t, err)
	require.NotNil(t, rel)

	//nolint:staticcheck // verifying defensive nil-context handling
	stats, err := lim.RuntimeStats(nil)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, int64(1), stats[0].Active)
	rel()

	// Local limiter with nil ctx and contention must also not panic.
	local := NewLocalLimiter()
	//nolint:staticcheck // verifying defensive nil-context handling
	r1, err := local.Acquire(nil, "m-local", 1)
	require.NoError(t, err)
	go func() {
		time.Sleep(15 * time.Millisecond)
		r1()
	}()
	//nolint:staticcheck // verifying defensive nil-context handling
	r2, err := local.Acquire(nil, "m-local", 1)
	require.NoError(t, err)
	r2()

	// Package-level Gate / RuntimeStats must tolerate nil ctx.
	SetGovernor(lim, 2)
	t.Cleanup(func() { SetGovernor(nil, 0) })
	//nolint:staticcheck // verifying defensive nil-context handling
	done := GateNamedN(nil, "m-gov", "GovModel", 2)
	done()
	//nolint:staticcheck // verifying defensive nil-context handling
	_, ok, err := RuntimeStats(nil)
	require.NoError(t, err)
	assert.True(t, ok)
}
