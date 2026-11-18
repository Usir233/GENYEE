package doc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
)

func TestW7DownloadBlocksDanglingSymlinkOverwriteAndServerFilenameTricks(t *testing.T) {
	t.Run(
		"refuses dangling symlink destination without --clobber",
		func(t *testing.T) {
			_, _ = iostreams.SetForTest(t)
			dir := t.TempDir()
			target := filepath.Join(dir, "created-via-dangling.txt")
			link := filepath.Join(dir, "dangling.txt")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}

			svc := &fakeDownloadSvc{content: "overwrite-attempt", filename: "dangling.txt"}
			err := runDownload(
				context.Background(),
				&DownloadOptions{Output: link, Clobber: false},
				textFopts(),
				svc,
				"doc_1",
			)
			require.Error(t, err, "dangling symlink must not be clobbered when --clobber is false")
			_, statErr := os.Stat(target)
			assert.True(t, os.IsNotExist(statErr), "target of dangling symlink must not be created")
		},
	)

	t.Run("rejects server-suggested '-' filename so server cannot hijack stdout", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		svc := &fakeDownloadSvc{content: "hijacked", filename: "-"}
		err := runDownload(context.Background(), &DownloadOptions{}, textFopts(), svc, "doc_1")
		require.Error(t, err)
		assert.Empty(t, out.String())
	})

	t.Run("sanitizes Windows backslash traversal in server-suggested filename", func(t *testing.T) {
		dest, err := resolveDownloadDest(&DownloadOptions{}, `..\..\evil.pdf`)
		require.NoError(t, err)
		assert.Equal(t, "evil.pdf", dest)

		_, err = resolveDownloadDest(&DownloadOptions{}, `..\`)
		require.Error(t, err)
	})

	t.Run("refuses writing server-suggested filename through existing CWD symlink even with --clobber", func(
		t *testing.T,
	) {
		_, _ = iostreams.SetForTest(t)
		cwd := t.TempDir()
		outside := filepath.Join(t.TempDir(), "victim.txt")
		require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o600))
		if err := os.Symlink(outside, filepath.Join(cwd, "report.pdf")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		prevWD, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(cwd))
		defer func() { _ = os.Chdir(prevWD) }()

		svc := &fakeDownloadSvc{content: "pwned", filename: "report.pdf"}
		err = runDownload(context.Background(), &DownloadOptions{Clobber: true}, textFopts(), svc, "doc_1")
		require.Error(t, err)
		got, rerr := os.ReadFile(outside)
		require.NoError(t, rerr)
		assert.Equal(t, "keep", string(got))
	})
}

type fakeDupUploadSvc struct{}

func (f *fakeDupUploadSvc) CreateKnowledgeFromFile(
	_ context.Context, _, _ string, _ map[string]string, _ *bool, _, _ string, _ *sdk.KnowledgeProcessOverrides,
) (*sdk.Knowledge, error) {
	return nil, sdk.ErrDuplicateFile
}

func TestW7UploadRecursiveClassifiesDuplicateAndDryRunValidatesFlags(t *testing.T) {
	t.Run("recursive upload classifies ErrDuplicateFile as resource.already_exists", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o600))

		err := runUploadRecursive(
			context.Background(),
			&UploadOptions{Recursive: true, Glob: "*.md"},
			&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
			&fakeDupUploadSvc{},
			"kb_1",
			dir,
		)
		require.Error(t, err)

		var env struct {
			OK   bool `json:"ok"`
			Data []struct {
				OK    bool `json:"ok"`
				Error struct {
					Type string `json:"type"`
				} `json:"error"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &env))
		require.Len(t, env.Data, 1)
		assert.Equal(t, string(cmdutil.CodeResourceAlreadyExists), env.Data[0].Error.Type)
	})

	t.Run("dry-run rejects --recursive with --name or invalid --glob", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		t.Setenv("GENYEE_KB_ID", "11111111-1111-1111-1111-111111111111")
		f := cmdutil.New()

		c1 := NewCmdUpload(f)
		c1.SetArgs([]string{"./docs", "--recursive", "--name", "custom.md", "--dry-run"})
		c1.SilenceErrors, c1.SilenceUsage = true, true
		err := c1.Execute()
		require.Error(t, err)

		c2 := NewCmdUpload(f)
		c2.SetArgs([]string{"./docs", "--recursive", "--glob", "[", "--dry-run"})
		c2.SilenceErrors, c2.SilenceUsage = true, true
		err = c2.Execute()
		require.Error(t, err)
	})
}

func TestW7DocWaitCancelledContextAndInvalidIntervals(t *testing.T) {
	t.Run("cancelled context returns CodeOperationCancelled without ok:true stdout", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		f := &cmdutil.Factory{
			Client: func() (*sdk.Client, error) { return sdk.NewClient("http://127.0.0.1:1"), nil },
		}
		cmd := NewCmdWait(f)
		cmd.SetArgs([]string{"doc_1"})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cmd.SetContext(ctx)

		err := cmd.Execute()
		require.Error(t, err)
		var ce *cmdutil.Error
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, cmdutil.CodeOperationCancelled, ce.Code)
		assert.Empty(t, out.String(), "cancelled doc wait must not emit ok:true JSON envelope")
	})

	t.Run("rejects non-positive --interval and --timeout", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		f := &cmdutil.Factory{
			Client: func() (*sdk.Client, error) {
				t.Fatal("Client() must not be called when --interval/--timeout is invalid")
				return nil, nil
			},
		}
		for _, args := range [][]string{
			{"doc_1", "--interval", "0s"},
			{"doc_1", "--timeout", "0s"},
			{"doc_1", "--timeout", (-1 * time.Second).String()},
		} {
			cmd := NewCmdWait(f)
			cmd.SetArgs(args)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			err := cmd.Execute()
			require.Error(t, err)
			var ce *cmdutil.Error
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)
		}
	})
}

func TestW7DocListStatusEnumAndTimeValidationBeforeAuth(t *testing.T) {
	for _, st := range []string{"finalizing", "cancelled", "deleting"} {
		opts := &ListOptions{PageSize: 50, Limit: 30, Status: st}
		require.NoError(t, validateListOpts(opts), "status %q must be accepted", st)
	}

	_, _ = iostreams.SetForTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := cmdutil.New()
	f.IgnoreProjectLink = true
	cmd := NewCmdList(f)
	cmd.SetArgs([]string{"--start-time", "not-rfc3339"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.Execute()
	require.Error(t, err)
	var ce *cmdutil.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code, "bad --start-time must fail before KB/auth resolution")
}
