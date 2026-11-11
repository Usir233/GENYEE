package ratelimit

import "testing"

func TestNextMemberUniqueWithinSameMillisecond(t *testing.T) {
	l := &Limiter{instanceID: "inst1"}
	const nowMs = int64(1700000000123)
	seen := make(map[string]struct{}, 512)
	for i := 0; i < 512; i++ {
		m := l.nextMember(nowMs)
		if _, dup := seen[m]; dup {
			t.Fatalf("duplicate ZSET member generated in same millisecond at iter %d: %q", i, m)
		}
		seen[m] = struct{}{}
	}
}
