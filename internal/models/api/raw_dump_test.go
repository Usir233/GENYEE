package api

import (
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamPacketDumper_ConcurrentWriteAndCloseAndPrivatePerms(t *testing.T) {
	dumpDir := t.TempDir()
	t.Setenv("GENYEE_LLM_STREAM_RAW_DUMP_DIR", dumpDir)

	d := NewStreamPacketDumper("gpt-4o-mini", map[string]any{"prompt": "hello"})
	require.NotNil(t, d)

	info, err := os.Stat(d.Path())
	require.NoError(t, err)
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("expected private dump file permissions (0600), got %04o", perm)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				d.WritePacketRaw([]byte(`{"delta":"token"}`))
				d.WriteError("stream interrupted")
				d.WriteHTTPError(502, []byte(`{"error":"bad gateway"}`))
				d.WritePacket(map[string]any{"seq": j})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.Close()
		d.Close()
	}()
	wg.Wait()
}
