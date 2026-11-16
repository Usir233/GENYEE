package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMarkServerStartedAndUptime(t *testing.T) {
	MarkServerStarted()
	boot := ServerStartedAt()
	if boot.IsZero() {
		t.Fatal("ServerStartedAt should be set after MarkServerStarted")
	}
	time.Sleep(2 * time.Millisecond)
	if got := ServerUptime(); got <= 0 {
		t.Fatalf("ServerUptime() = %v, want > 0", got)
	}
}

func TestServerStartedGinCountConcurrencyAndStartupLogSanitization(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			MarkServerStarted()
			_ = ServerStartedAt()
			_ = ServerUptime()
			LogGinRouteCount(context.Background())
		}()
	}
	wg.Wait()

	sanitized := formatEnvValue(envVarSpec{name: "DB_USER"}, "admin\n[startup-env]   JWT_SECRET=forged\r\n")
	if strings.ContainsAny(sanitized, "\r\n") {
		t.Fatalf("formatEnvValue leaked raw CR/LF into startup log: %q", sanitized)
	}
}
