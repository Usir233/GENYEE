package runtime

import (
	"sync/atomic"
	"time"
)

// serverStartedAt is set at process boot (see MarkServerStarted).
var serverStartedAt atomic.Pointer[time.Time]

// MarkServerStarted records the instant the GENYEE server process started.
// Call once from main() before heavy init so uptime reflects the full process
// lifetime, not merely when the HTTP listener binds.
func MarkServerStarted() {
	now := time.Now().UTC()
	serverStartedAt.Store(&now)
}

// ServerStartedAt returns the boot instant in UTC. Zero when MarkServerStarted
// was not called (e.g. some tests); callers should treat zero as unknown.
func ServerStartedAt() time.Time {
	t := serverStartedAt.Load()
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ServerUptime returns elapsed time since MarkServerStarted. Zero when unset.
func ServerUptime() time.Duration {
	t := serverStartedAt.Load()
	if t == nil || t.IsZero() {
		return 0
	}
	return time.Since(*t)
}
