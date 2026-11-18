package localsandbox

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var errFakeSpawn = errors.New("spawn boom")

func TestStartDoesNotTakeRootLock(t *testing.T) {
	backend := &fakeBackend{stdout: "ok", exit: ExitStatus{Code: 0}}
	svc := serviceFixture(t, backend, ModeAuto)
	_, ws, err := svc.GuardForSession(context.Background(), "s1")
	require.NoError(t, err)
	unlock := svc.LockRoot(ws.Root)
	defer unlock()
	ready := make(chan Process, 1)
	failed := make(chan error, 1)
	go func() {
		proc, err := svc.StartForSession(context.Background(), RunRequest{
			SessionID: "s1", Command: "true", Env: map[string]string{"K": "v"},
		})
		if err != nil {
			failed <- err
			return
		}
		ready <- proc
	}()
	select {
	case err := <-failed:
		t.Fatal(err)
	case proc := <-ready:
		_, err := io.Copy(io.Discard, proc.Stdout())
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, proc.Stderr())
		require.NoError(t, err)
		_, err = proc.Wait(context.Background())
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Start blocked behind the synchronous root lock")
	}
	require.Equal(t, "v", backend.spawnCmd.Env["K"], "env 经 BuildCommandEnv 传给 Spawn")
	require.Len(t, backend.preps, 1)
	require.True(t, backend.preps[0].closed.Load(), "Wait closes the prepared handle")
}

func TestStartValidatesWorkDirAndMode(t *testing.T) {
	backend := &fakeBackend{}
	svc := serviceFixture(t, backend, ModeFull)
	_, err := svc.StartForSession(context.Background(), RunRequest{SessionID: "s", Command: "true"})
	require.ErrorIs(t, err, ErrApprovalModeNotShipped)
	require.Empty(t, backend.prepared)
	svc = serviceFixture(t, backend, ModeAuto)
	_, err = svc.StartForSession(context.Background(), RunRequest{
		SessionID: "s", Command: "true", WorkDir: "/etc",
	})
	require.Error(t, err)
	require.Empty(t, backend.prepared)
}

func TestStartSpawnFailureClosesPrepared(t *testing.T) {
	backend := &fakeBackend{spawnErr: errFakeSpawn}
	svc := serviceFixture(t, backend, ModeAuto)
	_, err := svc.StartForSession(context.Background(), RunRequest{SessionID: "s", Command: "true"})
	require.ErrorIs(t, err, errFakeSpawn)
	require.Len(t, backend.prepared, 1, "Prepare 被调用过；Close 由 Start 负责")
	require.Len(t, backend.preps, 1)
	require.True(t, backend.preps[0].closed.Load(), "Start closes the prepared handle when Spawn fails")
}

func TestStartPreparedProcessPropagatesCloseErrorAndHonorsWaitContext(t *testing.T) {
	closeBoom := errors.New("prepared close failed")
	backend := &fakeBackend{stdout: "ok", exit: ExitStatus{Code: 0}, prepClose: closeBoom}
	svc := serviceFixture(t, backend, ModeAuto)
	proc, err := svc.StartForSession(context.Background(), RunRequest{SessionID: "s", Command: "true"})
	require.NoError(t, err)
	_, err = proc.Wait(context.Background())
	require.ErrorIs(t, err, closeBoom, "Wait must not silently drop Prepared.Close errors")

	// Also verify Wait(waitCtx) kills a blocking process when waitCtx expires
	// even if Start's own context has not been cancelled.
	blocking := newRecordingProcess(ExitStatus{Code: 0})
	blocking.blockUntilKill = true
	backend2 := &fakeBackend{proc: blocking}
	svc2 := serviceFixture(t, backend2, ModeAuto)
	proc2, err := svc2.StartForSession(context.Background(), RunRequest{SessionID: "s", Command: "sleep 60"})
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	done := make(chan ExitStatus, 1)
	go func() {
		st, _ := proc2.Wait(waitCtx)
		done <- st
	}()
	select {
	case st := <-done:
		require.True(t, st.Killed)
		require.GreaterOrEqual(t, blocking.killCount(), 1)
	case <-time.After(time.Second):
		_ = proc2.Kill()
		t.Fatal("preparedProcess.Wait ignored waitCtx timeout")
	}
}
