package automation

import (
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestScheduleNilReceiverSafety(t *testing.T) {
	var s *Schedule
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	require.NotPanics(t, func() {
		require.Equal(t, types.AutomationTrigger{}, s.Trigger())
		require.Nil(t, s.Next(now))
		require.Empty(t, s.Upcoming(now, 5))
		require.NoError(t, s.CheckInterval(now, DefaultMinInterval))
	})
}
