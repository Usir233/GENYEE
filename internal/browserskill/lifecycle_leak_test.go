package browserskill

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagerCloseCancelsAndWaitsForForgetGoroutines(t *testing.T) {
	m := NewManager(testStore(t))
	scope := Scope{Tenant: 1, User: "alice"}

	d := &device{
		tasks: map[string]*task{
			"sess-stuck": {
				selected: true,
				starting: true, // simulates an in-flight session.start
			},
		},
	}
	m.mu.Lock()
	m.devices[scope.key()] = d
	m.mu.Unlock()

	m.Forget(scope, []string{"sess-stuck"})

	start := time.Now()
	m.Close()
	require.Less(t, time.Since(start), 500*time.Millisecond,
		"Close must cancel in-flight forgetLocal wait instead of blocking for 10s")
	require.Zero(t, m.activeBackgroundWorkers(),
		"all forgetLocal background goroutines must exit before Close returns")
}
