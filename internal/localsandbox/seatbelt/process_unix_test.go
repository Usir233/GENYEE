//go:build unix

package seatbelt

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/localsandbox/core"
)

func countOpenFDs(t *testing.T) int {
	t.Helper()
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		// os.ReadDir itself opens and closes the directory fd before returning.
		return len(entries)
	}
	if entries, err := os.ReadDir("/dev/fd"); err == nil {
		return len(entries)
	}
	t.Fatal("neither /proc/self/fd nor /dev/fd is readable on this unix host")
	return 0
}

func TestSpawnExecCmdClosesPipesOnStderrPipeAndStartFailure(t *testing.T) {
	cwd := t.TempDir()
	ctx := context.Background()

	// Warm up logger/poller descriptors before measuring baseline.
	warmCmd := buildSeatbeltExecCmd("/bin/echo", []string{"warm"}, core.Command{
		Argv: []string{"/bin/echo", "warm"},
		Cwd:  cwd,
	})
	warmProc, err := spawnExecCmd(ctx, warmCmd, cwd, "/bin/echo")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, warmProc.Stdout())
	_, _ = io.Copy(io.Discard, warmProc.Stderr())
	_, err = warmProc.Wait(ctx)
	require.NoError(t, err)

	runtime.GC()
	baseline := countOpenFDs(t)

	// 1. Force StderrPipe() to fail after StdoutPipe() has already opened an OS pipe.
	for range 24 {
		execCmd := buildSeatbeltExecCmd("/bin/echo", []string{"x"}, core.Command{
			Argv: []string{"/bin/echo", "x"},
			Cwd:  cwd,
		})
		execCmd.Stderr = io.Discard
		_, spawnErr := spawnExecCmd(ctx, execCmd, cwd, "/bin/echo")
		require.Error(t, spawnErr)
	}

	// 2. Force Start() to fail after both StdoutPipe() and StderrPipe() succeeded.
	missingBin := filepath.Join(cwd, "does-not-exist")
	for range 24 {
		execCmd := buildSeatbeltExecCmd(missingBin, nil, core.Command{
			Argv: []string{missingBin},
			Cwd:  cwd,
		})
		_, spawnErr := spawnExecCmd(ctx, execCmd, cwd, missingBin)
		require.Error(t, spawnErr)
	}

	runtime.GC()
	after := countOpenFDs(t)
	require.LessOrEqual(t, after, baseline+2,
		"failed StderrPipe/Start must close opened stdout/stderr pipes without leaking fds (baseline=%d after=%d)",
		baseline, after)
}

func TestSeatbeltProcessWaitRespectsWaitContextAndReapsProcessGroup(t *testing.T) {
	cwd := t.TempDir()
	// spawnCtx stays alive (simulating StartForSession's long-lived app ctx),
	// while waitCtx carries a short caller deadline.
	spawnCtx, cancelSpawn := context.WithCancel(context.Background())
	defer cancelSpawn()

	script := `sleep 30 & child=$!; printf '%d\n' "$child"; wait "$child"`
	execCmd := buildSeatbeltExecCmd("/bin/sh", []string{"-c", script}, core.Command{
		Argv: []string{"/bin/sh", "-c", script},
		Cwd:  cwd,
	})
	proc, err := spawnExecCmd(spawnCtx, execCmd, cwd, "/bin/sh")
	require.NoError(t, err)

	reader := bufio.NewReader(proc.Stdout())
	lineCh := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		lineCh <- strings.TrimSpace(line)
		_, _ = io.Copy(io.Discard, reader)
	}()
	go func() {
		_, _ = io.Copy(io.Discard, proc.Stderr())
	}()

	var grandChildPID int
	select {
	case line := <-lineCh:
		grandChildPID, err = strconv.Atoi(line)
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		_ = proc.Kill()
		t.Fatal("timed out waiting for child pid")
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancelWait()

	type waitOutcome struct {
		status core.ExitStatus
		err    error
	}
	done := make(chan waitOutcome, 1)
	go func() {
		st, werr := proc.Wait(waitCtx)
		done <- waitOutcome{status: st, err: werr}
	}()

	select {
	case out := <-done:
		require.NoError(t, out.err)
		require.True(t, out.status.Killed, "Wait context cancellation must kill the process group")
	case <-time.After(1500 * time.Millisecond):
		_ = proc.Kill()
		<-done
		t.Fatal("seatbeltProcess.Wait ignored waitCtx cancellation")
	}

	// Both the shell leader and its background descendant must be gone/reaped.
	require.Eventually(t, func() bool {
		errLeader := syscall.Kill(proc.PID(), 0)
		errChild := syscall.Kill(grandChildPID, 0)
		return errLeader == syscall.ESRCH && errChild == syscall.ESRCH
	}, time.Second, 20*time.Millisecond, "entire process group must be terminated and reaped")
}
