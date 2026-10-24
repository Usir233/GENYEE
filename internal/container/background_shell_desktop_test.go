//go:build desktop

package container

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/background"
)

func TestDesktopWithoutHostManagerStaysRemoteOnly(t *testing.T) {
	bg := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	jobs, err := newShellJobService(nil, nil, bg, nil, NewResourceCleaner(),
		service.HostSandboxManager{Desktop: true})
	require.NoError(t, err)
	require.False(t, jobs.LocalAvailable())
}
