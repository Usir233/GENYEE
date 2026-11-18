// Historical PoC: can the Docker Engine API back the
// semantics GENYEE's RemoteSandboxClient contract requires (session-persistent
// sandbox, exec, filesystem, metadata, lifecycle), plus the snapshot workflow
// planned for E2B?
//
// It drives a real daemon and prints PASS/FAIL plus the observed evidence for
// every operation, including the ones Docker cannot honour ("GAP" steps, which
// assert the gap rather than a capability).
//
// Run with a reachable daemon (DOCKER_HOST honoured):
//
//	cd docs/poc/docker-sandbox && go run .
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	baseImage            = "python:3.11-slim"
	execUser             = "user"
	maxArchiveReadBytes  = 16 << 20
	maxBuildStreamBytes  = 16 << 20
	maxExecOutputBytes   = 16 << 20
	maxTarEntries        = 4096
	defaultClientTimeout = 2 * time.Minute
	pingTimeout          = 15 * time.Second
)

var failures int

func step(name string, err error, detail string) {
	if err != nil {
		failures++
		fmt.Printf("FAIL  %-48s %v\n", name, err)
		return
	}
	fmt.Printf("PASS  %-48s %s\n", name, detail)
}

func main() {
	os.Exit(run())
}

func runWithSteps(fn func() (int, func())) int {
	failed, cleanup := fn()
	if cleanup != nil {
		cleanup()
	}
	fmt.Println()
	if failed == 0 {
		fmt.Println("RESULT: all checks passed")
		return 0
	}
	fmt.Printf("RESULT: %d check(s) failed\n", failed)
	return 1
}

func run() int {
	return runWithSteps(func() (int, func()) {
		failures = 0
		var cleanups []func()
		pushCleanup := func(fn func()) {
			cleanups = append(cleanups, fn)
		}
		runCleanups := func() {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		pushCleanup(cancel)

		cli, err := client.New(client.WithHostFromEnv(), client.WithTimeout(defaultClientTimeout))
		if err != nil {
			fmt.Println("cannot reach docker:", err)
			return 1, runCleanups
		}
		pushCleanup(func() { closeClient(cli) })

		pingCtx, pingCancel := context.WithTimeout(ctx, pingTimeout)
		ping, err := cli.Ping(pingCtx, client.PingOptions{})
		pingCancel()
		step("Health: ping control plane", err, fmt.Sprintf("api=%s", ping.APIVersion))
		if err != nil {
			return failures, runCleanups
		}

		tmplImage := "genyee-poc/template:v2"
		err = buildTemplateImage(ctx, cli, tmplImage)
		step("Template: base image with uid-1000 user", err, tmplImage)
		if err != nil {
			return failures, runCleanups
		}

		sessionID := fmt.Sprintf("poc-session-%d", time.Now().Unix())
		labels := map[string]string{
			"genyee.managed":   "true",
			"genyee.tenant":    "1",
			"genyee.session":   sessionID,
			"genyee.config":    "cfg-poc",
			"genyee.createdAt": time.Now().UTC().Format(time.RFC3339),
		}

		// --- Create -------------------------------------------------------------
		id, err := createSandbox(ctx, cli, tmplImage, labels)
		step("Create: container as session sandbox", err, short(id))
		if err != nil {
			return failures, runCleanups
		}
		pushCleanup(func() { remove(cli, id) })

		// --- Connect / Get ------------------------------------------------------
		insp, err := inspectFromFreshClient(ctx, id, sessionID)
		step("Connect+Metadata: re-attach from a new client", err,
			fmt.Sprintf("state=%s session=%s", inspectStatus(insp), inspectLabel(insp, "genyee.session")))

		// --- List by metadata ---------------------------------------------------
		listed, err := cli.ContainerList(ctx, client.ContainerListOptions{
			All:     true,
			Filters: client.Filters{}.Add("label", "genyee.session="+sessionID),
		})
		if err == nil && len(listed.Items) != 1 {
			err = fmt.Errorf("expected 1 container, got %d", len(listed.Items))
		}
		step("List: server-side metadata filter", err, fmt.Sprintf("matched=%d", len(listed.Items)))

		// --- Exec: user / workdir / env / stdin / demux / exit code -------------
		res, err := execIn(ctx, cli, id, execRequest{
			cmd:     []string{"bash", "-lc", `cat; echo "user=$(id -un) cwd=$(pwd) env=$GREET"; echo oops >&2; exit 7`},
			user:    execUser,
			workDir: "/workspace",
			env:     []string{"GREET=hello"},
			stdin:   "stdin-payload\n",
		})
		if err == nil {
			switch {
			case res.exitCode != 7:
				err = fmt.Errorf("exit code %d, want 7", res.exitCode)
			case !strings.Contains(res.stdout, "user=user cwd=/workspace env=hello"):
				err = fmt.Errorf("stdout mismatch: %q", res.stdout)
			case !strings.Contains(res.stdout, "stdin-payload"):
				err = fmt.Errorf("stdin not delivered: %q", res.stdout)
			case !strings.Contains(res.stderr, "oops"):
				err = fmt.Errorf("stderr not separated: %q", res.stderr)
			}
		}
		step("Exec: user/workdir/env/stdin/demux/exit", err,
			fmt.Sprintf("exit=%d stdout=%q stderr=%q", res.exitCode, oneline(res.stdout), oneline(res.stderr)))

		// --- Exec cancellation: does a client-side cancel kill the process? -----
		cancelCtx, cancelExec := context.WithTimeout(ctx, 2*time.Second)
		_, execErr := execIn(cancelCtx, cli, id, execRequest{
			cmd: []string{"bash", "-lc", "sleep 30"},
		})
		cancelExec()
		time.Sleep(1 * time.Second)
		probe, probeErr := execIn(ctx, cli, id, execRequest{
			cmd: []string{"bash", "-lc", "ps -eo args= | grep -c '^sleep 30$' || true"},
		})
		step("Exec cancel: client cancel does NOT kill process (GAP)", probeErr,
			fmt.Sprintf("clientErr=%v surviving 'sleep' procs=%s", execErr != nil, oneline(probe.stdout)))

		// --- Timeout enforced inside the container ------------------------------
		start := time.Now()
		killRes, err := execIn(ctx, cli, id, execRequest{
			cmd: []string{"timeout", "-s", "KILL", "2", "bash", "-lc", "sleep 30"},
		})
		elapsed := time.Since(start)
		if err == nil && killRes.exitCode != 137 {
			err = fmt.Errorf("exit code %d, want 137 (SIGKILL)", killRes.exitCode)
		}
		step("Timeout: enforce via in-container timeout(1)", err,
			fmt.Sprintf("exit=%d after=%s", killRes.exitCode, elapsed.Round(100*time.Millisecond)))

		// --- Filesystem: write / read / stat / mkdir / list / remove ------------
		err = writeFile(ctx, cli, id, "/workspace/input/attachment.txt", []byte("attachment payload\n"))
		step("WriteFile: PUT /containers/{id}/archive", err, "/workspace/input/attachment.txt")

		content, err := readFile(ctx, cli, id, "/workspace/input/attachment.txt")
		if err == nil && string(content) != "attachment payload\n" {
			err = fmt.Errorf("content mismatch: %q", content)
		}
		step("ReadFile: GET /containers/{id}/archive", err, fmt.Sprintf("%d bytes", len(content)))

		stat, err := cli.ContainerStatPath(ctx, id, client.ContainerStatPathOptions{
			Path: "/workspace/input/attachment.txt",
		})
		step("Stat: HEAD /containers/{id}/archive", err,
			fmt.Sprintf("size=%d mode=%v mtime=%s", stat.Stat.Size, stat.Stat.Mode.Perm(),
				stat.Stat.Mtime.Format(time.RFC3339)))

		rootMk, rootErr := execIn(ctx, cli, id, execRequest{
			cmd: []string{"mkdir", "-p", "/workspace/output/rootdir"},
		})
		step("CapDrop ALL: root cannot bypass mode bits (GAP)", rootErr,
			fmt.Sprintf("mkdir as root exit=%d stderr=%q", rootMk.exitCode, oneline(rootMk.stderr)))

		_, err = mustExec(ctx, cli, id, execRequest{
			cmd:  []string{"mkdir", "-p", "/workspace/output/nested"},
			user: execUser,
		})
		step("MakeDir: exec mkdir -p as sandbox user", err, "/workspace/output/nested")

		if _, err = mustExec(ctx, cli, id, execRequest{
			cmd:  []string{"bash", "-lc", "echo body > /workspace/output/nested/report.txt"},
			user: execUser,
		}); err != nil {
			step("ListDir: seed a file to list", err, "")
		}
		lsRes, err := mustExec(ctx, cli, id, execRequest{
			cmd:  []string{"find", "/workspace/output", "-mindepth", "1", "-printf", `%y\t%s\t%T@\t%p\n`},
			user: execUser,
		})
		if err == nil && !strings.Contains(lsRes.stdout, "report.txt") {
			err = fmt.Errorf("listing missing report.txt: %q", lsRes.stdout)
		}
		step("ListDir: exec find (no native API)", err,
			fmt.Sprintf("exit=%d out=%q err=%q", lsRes.exitCode, oneline(lsRes.stdout), oneline(lsRes.stderr)))

		_, err = mustExec(ctx, cli, id, execRequest{
			cmd:  []string{"rm", "-rf", "/workspace/input/attachment.txt"},
			user: execUser,
		})
		step("Remove: exec rm (no native API)", err, "removed")

		// --- Session-scoped state persists across executions --------------------
		installCmd := "pip install --quiet --no-cache-dir --user cowsay==6.1 && " +
			"echo 1 > /workspace/output/counter.txt"
		installRes, err := execIn(ctx, cli, id, execRequest{
			cmd:  []string{"bash", "-lc", installCmd},
			user: execUser,
		})
		if err == nil && installRes.exitCode != 0 {
			err = fmt.Errorf("install failed: %s %s", oneline(installRes.stdout), oneline(installRes.stderr))
		}
		if err == nil {
			var second execResult
			verifyCmd := `python -c 'import cowsay; print("cowsay ok")' && ` +
				`cat /workspace/output/counter.txt`
			second, err = execIn(ctx, cli, id, execRequest{
				cmd:  []string{"bash", "-lc", verifyCmd},
				user: execUser,
			})
			if err == nil && (!strings.Contains(second.stdout, "cowsay ok") ||
				!strings.Contains(second.stdout, "1")) {
				err = fmt.Errorf("state lost between executions: %q %q", second.stdout, second.stderr)
			}
		}
		step("Session state: pip install survives next exec", err, "package + file both visible")

		// --- Pause / Unpause (memory kept resident) -----------------------------
		_, err = cli.ContainerPause(ctx, id, client.ContainerPauseOptions{})
		if err == nil {
			var pausedInsp client.ContainerInspectResult
			pausedInsp, err = cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
			if err == nil && inspectStatus(pausedInsp) != "paused" {
				err = fmt.Errorf("state=%s", inspectStatus(pausedInsp))
			}
		}
		if err == nil {
			_, err = cli.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
		}
		if err == nil {
			var after execResult
			after, err = execIn(ctx, cli, id, execRequest{
				cmd: []string{"cat", "/workspace/output/counter.txt"},
			})
			if err == nil && !strings.Contains(after.stdout, "1") {
				err = fmt.Errorf("state lost after unpause: %q", after.stdout)
			}
		}
		step("Pause/Unpause: cgroup freezer round-trip", err,
			"RAM + processes retained, host memory still occupied")

		// --- Stop / Start (filesystem kept, processes lost) ---------------------
		_, err = execIn(ctx, cli, id, execRequest{
			cmd: []string{"bash", "-lc", "nohup sleep 900 >/dev/null 2>&1 & echo started"},
		})
		timeoutSec := 5
		if err == nil {
			_, err = cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeoutSec})
		}
		if err == nil {
			_, err = cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
		}
		var afterRestart execResult
		if err == nil {
			checkRestartCmd := "cat /workspace/output/counter.txt; " +
				"ps -eo args= | grep -c '^sleep 900$' || true"
			afterRestart, err = execIn(ctx, cli, id, execRequest{
				cmd: []string{"bash", "-lc", checkRestartCmd},
			})
			if err == nil && !strings.Contains(afterRestart.stdout, "1") {
				err = fmt.Errorf("filesystem lost after restart: %q", afterRestart.stdout)
			}
		}
		step("Stop/Start: filesystem survives, processes do not", err,
			fmt.Sprintf("counter+sleep-count=%s", oneline(afterRestart.stdout)))

		// --- Snapshot workflow --------------------------------------------------
		mgmtID, err := createSandbox(ctx, cli, tmplImage,
			map[string]string{"genyee.managed": "true", "genyee.role": "snapshot-builder"})
		step("Snapshot: create workspace management sandbox", err, short(mgmtID))
		if err != nil {
			return failures, runCleanups
		}
		pushCleanup(func() { remove(cli, mgmtID) })

		skillInstallCmd := "pip install --quiet --no-cache-dir requests==2.32.3 && " +
			"mkdir -p /opt/skills/pdf && echo 'skill v1' > /opt/skills/pdf/SKILL.md"
		skillRes, err := execIn(ctx, cli, mgmtID, execRequest{
			cmd: []string{"bash", "-lc", skillInstallCmd},
		})
		if err == nil && skillRes.exitCode != 0 {
			err = fmt.Errorf("skill install failed: %s", oneline(skillRes.stderr))
		}
		step("Snapshot: install a skill into that sandbox", err, "requests==2.32.3 + /opt/skills/pdf")

		snapV1 := "genyee-poc/snapshot:v1"
		commitRes, err := cli.ContainerCommit(ctx, mgmtID, client.ContainerCommitOptions{
			Reference: snapV1,
			Comment:   "genyee snapshot v1",
			Changes:   []string{`LABEL genyee.snapshot.version=1`, `LABEL genyee.snapshot.tenant=1`},
		})
		if err == nil {
			pushCleanup(func() { removeImage(cli, snapV1) })
		}
		step("Snapshot: commit container -> image", err, short(commitRes.ID))

		snapSessionID, err := createSandbox(ctx, cli, snapV1,
			map[string]string{"genyee.managed": "true", "genyee.session": sessionID + "-from-snapshot"})
		step("Snapshot: session sandbox boots from snapshot", err, short(snapSessionID))
		if err == nil {
			pushCleanup(func() { remove(cli, snapSessionID) })
			var check execResult
			checkSkillCmd := `python -c 'import requests; print(requests.__version__)' && ` +
				`cat /opt/skills/pdf/SKILL.md`
			check, err = execIn(ctx, cli, snapSessionID, execRequest{
				cmd: []string{"bash", "-lc", checkSkillCmd},
			})
			if err == nil && (!strings.Contains(check.stdout, "2.32.3") ||
				!strings.Contains(check.stdout, "skill v1")) {
				err = fmt.Errorf("snapshot content missing: %q %q", check.stdout, check.stderr)
			}
			step("Snapshot: installed skill present in new sandbox", err, oneline(check.stdout))
		}

		// Incremental snapshot update: install a second skill on top of v1.
		upgradeID, err := createSandbox(ctx, cli, snapV1,
			map[string]string{"genyee.role": "snapshot-builder"})
		if err == nil {
			pushCleanup(func() { remove(cli, upgradeID) })
			_, err = execIn(ctx, cli, upgradeID, execRequest{
				cmd: []string{
					"bash", "-lc",
					"mkdir -p /opt/skills/chart && echo 'skill v2' > /opt/skills/chart/SKILL.md",
				},
			})
		}
		snapV2 := "genyee-poc/snapshot:v2"
		if err == nil {
			_, err = cli.ContainerCommit(ctx, upgradeID, client.ContainerCommitOptions{
				Reference: snapV2,
				Changes:   []string{`LABEL genyee.snapshot.version=2`},
			})
			if err == nil {
				pushCleanup(func() { removeImage(cli, snapV2) })
			}
		}
		step("Snapshot: incremental update v1 -> v2", err, snapV2)

		imgs, err := cli.ImageList(ctx, client.ImageListOptions{
			All:     true,
			Filters: client.Filters{}.Add("label", "genyee.snapshot.version"),
		})
		step("Snapshot: list snapshots by label", err, fmt.Sprintf("count=%d", len(imgs.Items)))

		imgInsp, err := cli.ImageInspect(ctx, snapV2)
		if err == nil {
			step("Snapshot: layer/size accounting", nil,
				fmt.Sprintf("layers=%d size=%.1fMB (image layer hard limit: 127)",
					len(imgInsp.RootFS.Layers), float64(imgInsp.Size)/1e6))
		} else {
			step("Snapshot: layer/size accounting", err, "")
		}

		// Does a snapshot preserve running processes / RAM? (E2B pause does.)
		var procOut execResult
		_, err = execIn(ctx, cli, mgmtID, execRequest{
			cmd: []string{"bash", "-lc", "nohup sleep 600 >/dev/null 2>&1 & echo started"},
		})
		procSnap := "genyee-poc/snapshot:proc"
		if err == nil {
			_, err = cli.ContainerCommit(ctx, mgmtID, client.ContainerCommitOptions{Reference: procSnap})
		}
		if err == nil {
			pushCleanup(func() { removeImage(cli, procSnap) })
			var procID string
			procID, err = createSandbox(ctx, cli, procSnap, map[string]string{"genyee.role": "proc-check"})
			if err == nil {
				pushCleanup(func() { remove(cli, procID) })
				procOut, err = execIn(ctx, cli, procID, execRequest{
					cmd: []string{"bash", "-lc", "ps -eo args= | grep -c '^sleep 600$' || true"},
				})
			}
		}
		step("Snapshot: running processes NOT captured (GAP)", err,
			fmt.Sprintf("'sleep' procs in restored sandbox=%s", oneline(procOut.stdout)))

		insp2, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		step("Idle TTL: daemon has none, only timestamps (GAP)", err,
			fmt.Sprintf("startedAt=%s -> GENYEE must sweep", inspectStartedAt(insp2)))

		checkCLIOrphan(ctx)

		return failures, runCleanups
	})
}

// checkCLIOrphan reproduces what today's CLI-based DockerSandbox does on
// timeout: exec.CommandContext kills the `docker run --rm` client process. The
// workload lives in the daemon, so killing the client does not stop it — the
// timeout is reported to the caller while the container keeps burning CPU.
func checkCLIOrphan(ctx context.Context) {
	if _, err := exec.LookPath("docker"); err != nil {
		step("CLI orphan: skipped (docker CLI not on PATH)", nil, "")
		return
	}
	name := fmt.Sprintf("genyee-poc-orphan-%d", time.Now().UnixNano())
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "docker", "run", "--rm", "--name", name,
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--network=none",
		"--pids-limit=64", "--memory=128m",
		baseImage, "sleep", "120")
	cliErr := cmd.Run()
	time.Sleep(2 * time.Second)

	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cleanCancel()
	out, err := exec.CommandContext(cleanCtx, "docker", "ps", "--filter", "name="+name,
		"--format", "{{.State}} {{.Status}}").Output()
	state := oneline(string(out))
	if rmErr := exec.CommandContext(cleanCtx, "docker", "rm", "-f", name).Run(); rmErr != nil && state != "" {
		err = errors.Join(err, fmt.Errorf("docker rm -f %s: %w", name, rmErr))
	}
	if state == "" {
		state = "gone"
	}
	step("CLI orphan: killing `docker run` leaves it running (GAP)", err,
		fmt.Sprintf("cliErr=%v container=%s", cliErr != nil, state))
}

// --- helpers -----------------------------------------------------------------

func closeClient(cli *client.Client) {
	if cli == nil {
		return
	}
	if err := cli.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "WARN  close docker client: %v\n", err)
	}
}

func inspectFromFreshClient(
	ctx context.Context, id, expectedSession string,
) (insp client.ContainerInspectResult, err error) {
	fresh, err := client.New(client.WithHostFromEnv(), client.WithTimeout(defaultClientTimeout))
	if err != nil {
		return client.ContainerInspectResult{}, err
	}
	defer func() {
		if closeErr := fresh.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	insp, err = fresh.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return client.ContainerInspectResult{}, err
	}
	if inspectLabel(insp, "genyee.session") != expectedSession {
		return insp, errors.New("metadata labels not preserved")
	}
	return insp, nil
}

func inspectStatus(insp client.ContainerInspectResult) string {
	if insp.Container.State == nil {
		return ""
	}
	return string(insp.Container.State.Status)
}

func inspectStartedAt(insp client.ContainerInspectResult) string {
	if insp.Container.State == nil {
		return ""
	}
	return insp.Container.State.StartedAt
}

func inspectLabel(insp client.ContainerInspectResult, key string) string {
	if insp.Container.Config == nil {
		return ""
	}
	return insp.Container.Config.Labels[key]
}

func remove(cli *client.Client, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true}); err != nil &&
		!errdefs.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "WARN  remove container %s: %v\n", short(id), err)
	}
}

func removeImage(cli *client.Client, ref string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true, PruneChildren: true}); err != nil &&
		!errdefs.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "WARN  remove image %s: %v\n", ref, err)
	}
}

type buildStreamMessage struct {
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func drainImageBuildResponse(body io.ReadCloser, maxBytes int64) (err error) {
	defer func() {
		if closeErr := body.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	dec := json.NewDecoder(limited)
	for {
		var msg buildStreamMessage
		decodeErr := dec.Decode(&msg)
		if limited.N <= 0 {
			return fmt.Errorf("image build stream exceeds %d bytes", maxBytes)
		}
		if decodeErr == io.EOF {
			return nil
		}
		if decodeErr != nil {
			return decodeErr
		}
		if msg.ErrorDetail.Message != "" {
			return fmt.Errorf("image build failed: %s", msg.ErrorDetail.Message)
		}
		if msg.Error != "" {
			return fmt.Errorf("image build failed: %s", msg.Error)
		}
	}
}

func buildTemplateImage(ctx context.Context, cli *client.Client, tag string) error {
	if _, err := cli.ImageInspect(ctx, tag); err == nil {
		return nil
	}
	dockerfile := `FROM ` + baseImage + "\n" +
		`RUN apt-get update && apt-get install -y --no-install-recommends procps findutils && ` +
		`rm -rf /var/lib/apt/lists/*` + "\n" +
		`RUN useradd -m -u 1000 user && mkdir -p /workspace/input /workspace/output && ` +
		`chown -R user:user /workspace` + "\n" +
		"WORKDIR /workspace\n"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile))}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(dockerfile)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	resp, err := cli.ImageBuild(ctx, &buf, client.ImageBuildOptions{Tags: []string{tag}, Remove: true})
	if err != nil {
		return err
	}
	return drainImageBuildResponse(resp.Body, maxBuildStreamBytes)
}

func createAndStartContainer(
	ctx context.Context,
	createFn func(context.Context) (string, error),
	startFn func(context.Context, string) error,
	removeFn func(context.Context, string, bool),
) (string, error) {
	id, err := createFn(ctx)
	if err != nil {
		return "", err
	}
	if err := startFn(ctx, id); err != nil {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		removeFn(cleanCtx, id, true)
		return "", err
	}
	return id, nil
}

func createSandbox(ctx context.Context, cli *client.Client, img string, labels map[string]string) (string, error) {
	pids := int64(256)
	return createAndStartContainer(
		ctx,
		func(c context.Context) (string, error) {
			created, err := cli.ContainerCreate(c, client.ContainerCreateOptions{
				Image: img,
				Config: &container.Config{
					Cmd:        []string{"sleep", "infinity"},
					WorkingDir: "/workspace",
					Labels:     labels,
				},
				HostConfig: &container.HostConfig{
					Resources: container.Resources{
						Memory:     1 << 30,
						MemorySwap: 1 << 30,
						NanoCPUs:   1_000_000_000,
						PidsLimit:  &pids,
					},
					CapDrop:     []string{"ALL"},
					SecurityOpt: []string{"no-new-privileges"},
				},
			})
			if err != nil {
				return "", err
			}
			return created.ID, nil
		},
		func(c context.Context, id string) error {
			_, err := cli.ContainerStart(c, id, client.ContainerStartOptions{})
			return err
		},
		func(c context.Context, id string, force bool) {
			if _, rmErr := cli.ContainerRemove(c, id, client.ContainerRemoveOptions{Force: force}); rmErr != nil &&
				!errdefs.IsNotFound(rmErr) {
				fmt.Fprintf(os.Stderr, "WARN  rollback container %s: %v\n", short(id), rmErr)
			}
		},
	)
}

type execRequest struct {
	cmd     []string
	user    string
	workDir string
	env     []string
	stdin   string
}

type execResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func collectExecStream(
	ctx context.Context, r io.Reader, closeConn func() error, maxBytes int64,
) (string, string, error) {
	var stdout, stderr bytes.Buffer
	limited := &io.LimitedReader{R: r, N: maxBytes + 1}
	var copyErr error
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, copyErr = stdcopy.StdCopy(&stdout, &stderr, limited)
	}()
	select {
	case <-copyDone:
		if limited.N <= 0 {
			return "", "", fmt.Errorf("exec stream output exceeds %d bytes", maxBytes)
		}
		if copyErr != nil {
			return "", "", copyErr
		}
		return stdout.String(), stderr.String(), nil
	case <-ctx.Done():
		if closeConn != nil {
			_ = closeConn()
		}
		<-copyDone
		return stdout.String(), stderr.String(), ctx.Err()
	}
}

func execIn(ctx context.Context, cli *client.Client, id string, req execRequest) (execResult, error) {
	created, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          req.cmd,
		User:         req.user,
		WorkingDir:   req.workDir,
		Env:          req.env,
		AttachStdin:  req.stdin != "",
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return execResult{}, err
	}
	attached, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return execResult{}, err
	}
	defer attached.Close()

	if req.stdin != "" {
		if _, err := attached.Conn.Write([]byte(req.stdin)); err != nil {
			return execResult{}, err
		}
		if cw, ok := attached.Conn.(interface{ CloseWrite() error }); ok {
			if err := cw.CloseWrite(); err != nil {
				return execResult{}, fmt.Errorf("close exec stdin: %w", err)
			}
		}
	}

	outStr, errStr, err := collectExecStream(
		ctx,
		attached.Reader,
		func() error { attached.Close(); return nil },
		maxExecOutputBytes,
	)
	if err != nil {
		return execResult{stdout: outStr, stderr: errStr}, err
	}

	inspect, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return execResult{}, err
	}
	return execResult{
		stdout:   outStr,
		stderr:   errStr,
		exitCode: inspect.ExitCode,
	}, nil
}

// mustExec fails when the command itself exits non-zero, so a silently
// failing setup step cannot be mistaken for a passing capability check.
func mustExec(ctx context.Context, cli *client.Client, id string, req execRequest) (execResult, error) {
	res, err := execIn(ctx, cli, id, req)
	if err != nil {
		return res, err
	}
	if res.exitCode != 0 {
		return res, fmt.Errorf("exit=%d stderr=%s", res.exitCode, oneline(res.stderr))
	}
	return res, nil
}

func cleanArchiveDest(dest string) (dir, base string, err error) {
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.HasSuffix(dest, "/") {
		return "", "", fmt.Errorf("invalid archive destination %q", dest)
	}
	for _, seg := range strings.Split(dest, "/") {
		if seg == ".." || seg == "." {
			return "", "", fmt.Errorf("archive destination %q must not contain dot segments", dest)
		}
	}
	cleaned := path.Clean(dest)
	if cleaned == "/" {
		return "", "", fmt.Errorf("invalid archive destination %q", dest)
	}
	dir = path.Dir(cleaned)
	base = path.Base(cleaned)
	if base == "" || base == "/" || base == "." {
		return "", "", fmt.Errorf("invalid archive base name in %q", dest)
	}
	return dir, base, nil
}

func writeFile(ctx context.Context, cli *client.Client, id, dest string, content []byte) error {
	dir, name, err := cleanArchiveDest(dest)
	if err != nil {
		return err
	}
	if _, err := mustExec(ctx, cli, id, execRequest{
		cmd:  []string{"mkdir", "-p", dir},
		user: execUser,
	}); err != nil {
		return err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), Uid: 1000, Gid: 1000,
	}); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	_, err = cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{
		DestinationPath: dir,
		Content:         &buf,
		CopyUIDGID:      true,
	})
	return err
}

func readTarRegBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	const archiveEnvelopeSlack = 4 << 20
	tr := tar.NewReader(io.LimitReader(r, maxBytes+archiveEnvelopeSlack))
	for entries := 0; entries < maxTarEntries; entries++ {
		header, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("file not present in archive")
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg {
			if header.Size < 0 || header.Size > maxBytes {
				return nil, fmt.Errorf("archive entry %q size %d exceeds limit %d", header.Name, header.Size, maxBytes)
			}
			data, err := io.ReadAll(io.LimitReader(tr, maxBytes+1))
			if err != nil {
				return nil, err
			}
			if int64(len(data)) > maxBytes {
				return nil, fmt.Errorf("archive entry %q exceeds limit %d", header.Name, maxBytes)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("archive exceeds entry limit %d without regular file", maxTarEntries)
}

func readFile(ctx context.Context, cli *client.Client, id, src string) (data []byte, err error) {
	res, err := cli.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: src})
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := res.Content.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return readTarRegBounded(res.Content, maxArchiveReadBytes)
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func oneline(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
