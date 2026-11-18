package doctor

import (
	"context"
	"testing"

	sdk "github.com/Usir233/GENYEE/client"
	"github.com/stretchr/testify/require"
)

type hostedAuditService struct {
	*fakeServices
	host string
}

func (s hostedAuditService) CacheKey() string { return s.host }

func TestDoctorCacheDoesNotCrossServers(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first := hostedAuditService{&fakeServices{systemInfo: &sdk.SystemInfo{Version: "1.0.0"}}, "https://first.example"}
	second := hostedAuditService{&fakeServices{systemInfo: &sdk.SystemInfo{Version: "2.0.0"}}, "https://second.example"}
	// Switch the actual service rather than substituting a cached response.
	_, cached, err := loadOrProbeServerInfo(context.Background(), &Options{}, first)
	require.NoError(t, err)
	require.False(t, cached)
	got, cached, err := loadOrProbeServerInfo(context.Background(), &Options{}, second)
	require.NoError(t, err)
	require.False(t, cached)
	require.Equal(t, "2.0.0", got.ServerVersion)
}
