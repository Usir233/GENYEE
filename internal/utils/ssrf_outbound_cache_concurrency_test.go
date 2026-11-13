package utils

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutboundSSRFCacheConcurrentWhitelistReload(t *testing.T) {
	SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(ResetSSRFWhitelistForTest)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		workers.Go(func() {
			<-start
			for range 100 {
				if err := validateURLForSSRFForOutbound("http://127.0.0.1/a"); err != nil {
					t.Errorf("unchanged whitelist rejected local fixture: %v", err)
				}
			}
		})
	}
	workers.Go(func() {
		<-start
		for range 100 {
			SetSSRFWhitelistFromRaw("127.0.0.1")
		}
	})
	close(start)
	workers.Wait()
}

func TestOutboundSSRFCacheRevokesAndRestoresAllowance(t *testing.T) {
	t.Cleanup(ResetSSRFWhitelistForTest)
	SetSSRFWhitelistFromRaw("127.0.0.1")
	require.NoError(t, validateURLForSSRFForOutbound("http://127.0.0.1/a"))
	SetSSRFWhitelistFromRaw("")
	require.Error(t, validateURLForSSRFForOutbound("http://127.0.0.1/b"), "a cached allow result cannot outlive removal")
	SetSSRFWhitelistFromRaw("127.0.0.1")
	require.NoError(t, validateURLForSSRFForOutbound("http://127.0.0.1/c"), "a cached deny result cannot outlive restoration")
}
