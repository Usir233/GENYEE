package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/sandbox"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// ─── 10. TenantSkill: Cross-Replica StopSkill + Retry Ownership & SubscribeProgress

func TestW3BAudit_Skill_CrossReplicaStopInvalidatesStaleRunOwnership(t *testing.T) {
	mr := miniredis.RunT(t)
	r1 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r2 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = r1.Close()
		_ = r2.Close()
	})

	fxA := newInstallFixture(t)
	fxA.svc.redis = r1
	fxA.agentErr = errors.New("replica A stale run hijacked row")

	// Replica B shares the same DB repo, config repo, storage, and Redis, with its own process-local runCancels map.
	fxB := newInstallFixture(t)
	fxB.svc.redis = r2
	fxB.svc.skills = fxA.skillRepo
	fxB.svc.configs = fxA.configRepo
	fxB.svc.resolver = fxA.svc.resolver
	fxB.agentErr = errors.New("replica B active retry")

	archive, err := zipSkillFiles(map[string][]byte{
		"SKILL.md":           []byte(validSkillMD),
		"scripts/extract.py": []byte("print('hi')\n"),
	})
	require.NoError(t, err)

	// Make Replica A's install block on config lock while Replica B stops & retries.
	holdKey := "genyee-skill-image-lock:7:cfg-1"
	require.NoError(t, r1.Set(context.Background(), holdKey, "other-holder", time.Minute).Err())

	ctx := context.Background()
	skillID, err := fxA.svc.InstallSkill(ctx, 7, "cfg-1", archive)
	require.NoError(t, err)

	// Wait until Replica A's background goroutine is registered and waiting on the lock.
	require.Eventually(t, func() bool {
		return fxA.svc.lookupSkillRun(7, "cfg-1", skillID) != nil
	}, 2*time.Second, 5*time.Millisecond)

	// Operator calls StopSkill on Replica B, then ReinstallSkill on Replica B with new guidance.
	stopped, err := fxB.svc.StopSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	assert.Equal(t, types.SkillStatusFailed, stopped.Status)

	// Replica B starts a retry; holdKey is still held so DB row is now Status == installing with identical SHA256!
	retryID, err := fxB.svc.ReinstallSkill(ctx, 7, "cfg-1", skillID, "use mirror index")
	require.NoError(t, err)
	require.Equal(t, skillID, retryID)

	// Release holdKey so Replica A's superseded goroutine acquires the lock first (or races).
	// Replica A's superseded attempt must detect it no longer owns the row and exit WITHOUT running its agent or
	// stamping its error!
	require.NoError(t, r1.Del(ctx, holdKey).Err())

	require.Eventually(t, func() bool {
		return fxA.svc.lookupSkillRun(7, "cfg-1", skillID) == nil &&
			fxB.svc.lookupSkillRun(7, "cfg-1", skillID) == nil
	}, 5*time.Second, 10*time.Millisecond)

	assert.Empty(
		t,
		fxA.agentPrompts,
		"superseded Replica A install run must not execute after Replica B stopped and retried",
	)
	row, err := fxA.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.NotContains(
		t,
		row.Error,
		"replica A stale run",
		"superseded cross-replica run must not hijack or fail the retried row",
	)
}

func TestW3BAudit_SubscribeProgress_ReleaseUnblocksParkedPublisherGoroutine(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	fx := newInstallFixture(t)
	fx.svc.redis = rc

	// Long-lived parent context that does NOT cancel when caller invokes release().
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, release, err := fx.svc.SubscribeProgress(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.NotNil(t, out)

	// Flood > skillProgressBuffer (16) messages without reading from `out` so the forwarder parks on `out <- p`.
	for i := 0; i < skillProgressBuffer+8; i++ {
		fx.svc.publishProgress(ctx, 7, "cfg-1", "sk-1", SkillProgress{
			Percent: i, Stage: "seeding", Status: types.SkillStatusInstalling,
		})
	}
	time.Sleep(30 * time.Millisecond)

	// Calling release() while parent ctx is still live must terminate the goroutine and close `out`.
	release()

	drained := make(chan struct{})
	go func() {
		for range out {
		}
		close(drained)
	}()

	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("SubscribeProgress goroutine leaked and never closed channel after release()")
	}
}

// ─── 11. Skill Bundle Backslashes, ZIP64 Overflow, UTF8 Truncation & Singleflight

func w3bZipWithRawUint64Size(t *testing.T, entryName string, content []byte, spoofedUncompressed uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte(validSkillMD))
	require.NoError(t, err)

	hdr := &zip.FileHeader{
		Name:   entryName,
		Method: zip.Store,
	}
	ew, err := zw.CreateHeader(hdr)
	require.NoError(t, err)
	_, err = ew.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	// Craft a ZIP64 archive or patch ZIP64 extra field so UncompressedSize64 >= 1<<63.
	// Using Go's zip writer: if we attach a ZIP64 extra field (tag 0x0001) and 0xFFFFFFFF size in central directory,
	// archive/zip parses UncompressedSize64 from it!
	raw := buf.Bytes()
	var patched bytes.Buffer
	zw2 := zip.NewWriter(&patched)
	f1, err := zw2.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f1.Write([]byte(validSkillMD))
	require.NoError(t, err)

	var extra [20]byte
	binary.LittleEndian.PutUint16(extra[0:2], 0x0001) // zip64ExtendedInfoTag
	binary.LittleEndian.PutUint16(extra[2:4], 16)
	binary.LittleEndian.PutUint64(extra[4:12], spoofedUncompressed)
	binary.LittleEndian.PutUint64(extra[12:20], uint64(len(content)))

	fh := &zip.FileHeader{
		Name:               entryName,
		Method:             zip.Store,
		UncompressedSize64: uint64(len(content)),
		Extra:              extra[:],
	}
	f2, err := zw2.CreateHeader(fh)
	require.NoError(t, err)
	_, err = f2.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw2.Close())

	// Patch the Central Directory header for entryName to 0xFFFFFFFF uncompressed & compressed size so Go's
	// zip.NewReader reads ZIP64 Extra!
	b := patched.Bytes()
	cdSig := []byte{0x50, 0x4b, 0x01, 0x02}
	idx := bytes.LastIndex(b, cdSig)
	require.GreaterOrEqual(t, idx, 0)
	binary.LittleEndian.PutUint32(b[idx+20:idx+24], 0xffffffff)
	binary.LittleEndian.PutUint32(b[idx+24:idx+28], 0xffffffff)
	_ = raw
	return b
}

func TestW3BAudit_SkillBundle_BackslashNormalization_Zip64Overflow_UTF8Trunc_AndSingleflight(t *testing.T) {
	t.Run("normalizes Windows backslash zip entries and rejects backslash traversal", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, body := range map[string]string{
			`pdf-tools\SKILL.md`:           validSkillMD,
			`pdf-tools\scripts\extract.py`: "print('win')\n",
		} {
			w, err := zw.Create(name)
			require.NoError(t, err)
			_, err = w.Write([]byte(body))
			require.NoError(t, err)
		}
		require.NoError(t, zw.Close())

		bundle, err := ParseSkillBundle(buf.Bytes())
		require.NoError(t, err)
		require.Contains(t, bundle.Files, "scripts/extract.py")

		files, err := listSkillZipFiles(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, []SkillFileEntry{
			{Path: "SKILL.md", Size: int64(len(validSkillMD))},
			{Path: "scripts/extract.py", Size: int64(len("print('win')\n"))},
		}, files)
		readBack, err := readSkillZipFile(buf.Bytes(), "scripts/extract.py")
		require.NoError(t, err)
		assert.Equal(t, "print('win')\n", string(readBack))

		// Backslash parent-directory escape must be rejected!
		var badBuf bytes.Buffer
		zwBad := zip.NewWriter(&badBuf)
		w1, _ := zwBad.Create("SKILL.md")
		_, _ = w1.Write([]byte(validSkillMD))
		w2, _ := zwBad.Create(`..\escaped.py`)
		_, _ = w2.Write([]byte("x"))
		require.NoError(t, zwBad.Close())
		_, err = ParseSkillBundle(badBuf.Bytes())
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
	})

	t.Run("writeHostSkillFiles rejects Windows drive-relative paths on any OS", func(t *testing.T) {
		dir := t.TempDir()
		err := writeHostSkillFiles(dir, &SkillBundle{
			Files: map[string][]byte{
				"C:evil.py": []byte("print(1)"),
			},
		})
		require.Error(t, err)
	})

	t.Run("rejects crafted ZIP64 header with uint64 >= 1<<63 negative int64 wraparound", func(t *testing.T) {
		crafted := w3bZipWithRawUint64Size(t, "scripts/huge.py", []byte("x"), uint64(1)<<63+42)
		_, err := listSkillZipFiles(crafted)
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
	})

	t.Run("projectSkillFileContent truncates at UTF-8 rune boundary instead of splitting multi-byte rune", func(
		t *testing.T,
	) {
		// Place a 3-byte CJK rune ('中' = E4 B8 AD) straddling byte index skillFileTextLimit (1<<20).
		prefix := bytes.Repeat([]byte("a"), skillFileTextLimit-1)
		body := make([]byte, 0, len(prefix)+len("中文"))
		body = append(body, prefix...)
		body = append(body, []byte("中文")...)
		require.True(t, utf8.Valid(body))

		proj := projectSkillFileContent("README.md", body)
		require.True(t, proj.Truncated)
		assert.True(t, utf8.ValidString(proj.Content), "truncated UTF-8 preview must remain valid UTF-8")
		assert.Equal(t, skillFileTextLimit-1, len(proj.Content))
	})

	t.Run("trySkillBundle singleflight does not fail second caller when first caller cancels context", func(
		t *testing.T,
	) {
		fx := newInstallFixture(t)
		archive, err := zipSkillFiles(map[string][]byte{"SKILL.md": []byte(validSkillMD)})
		require.NoError(t, err)

		started := make(chan struct{})
		releaseDownload := make(chan struct{})
		var calls atomic.Int32
		fx.svc.resolver = &w3bSlowFileResolver{
			getFn: func(ctx context.Context, _ string) (io.ReadCloser, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-releaseDownload:
					return io.NopCloser(bytes.NewReader(archive)), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}

		skill := &types.TenantSkillEntity{
			ID: "sk-sf", TenantID: 7, SandboxConfigID: "cfg-1",
			Name: "pdf-tools", BundleRef: "file://shared.zip", BundleSHA256: skillArchiveSHA256(archive),
		}

		ctx1, cancel1 := context.WithCancel(context.Background())
		done1 := make(chan bool, 1)
		go func() {
			_, ok := fx.svc.trySkillBundle(ctx1, 7, skill)
			done1 <- ok
		}()

		<-started
		done2 := make(chan bool, 1)
		go func() {
			_, ok := fx.svc.trySkillBundle(context.Background(), 7, skill)
			done2 <- ok
		}()
		time.Sleep(15 * time.Millisecond)

		// Cancel caller 1 mid-download, then let the storage read finish for caller 2.
		cancel1()
		require.False(t, <-done1)
		close(releaseDownload)
		require.True(t, <-done2, "coalesced caller with live context must succeed even if first caller cancelled")
	})
}

type w3bSlowFileResolver struct {
	getFn func(context.Context, string) (io.ReadCloser, error)
}

func (r *w3bSlowFileResolver) ResolveFileService(
	context.Context, *types.Tenant, string, string, string,
) (interfaces.FileService, string, error) {
	return w3bSlowFileService{getFn: r.getFn}, "", nil
}

func (r *w3bSlowFileResolver) ResolveBackend(
	context.Context, *types.Tenant, string, string,
) (*types.StorageBackend, error) {
	return nil, nil
}

type w3bSlowFileService struct {
	getFn func(context.Context, string) (io.ReadCloser, error)
}

func (w3bSlowFileService) CheckConnectivity(context.Context) error { return nil }
func (w3bSlowFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (w3bSlowFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}

func (s w3bSlowFileService) GetFile(ctx context.Context, ref string) (io.ReadCloser, error) {
	return s.getFn(ctx, ref)
}
func (w3bSlowFileService) GetFileURL(context.Context, string) (string, error) { return "", nil }
func (w3bSlowFileService) DeleteFile(context.Context, string) error           { return nil }
func (w3bSlowFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

// ─── 12. WorkspaceCheckpointer Shell Injection & workspaceLayoutFor Guards ───

type w3bNonVersionedRunner struct {
	fakeShellRunner
}

func (w3bNonVersionedRunner) VersionsWorkspace(context.Context, string) bool { return false }

type w3bBadLayoutRunner struct {
	fakeShellRunner
	layout sandbox.WorkspaceLayout
}

func (r *w3bBadLayoutRunner) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	return r.layout, nil
}

func TestW3BAudit_Workspace_CheckpointerShellInjectionAndResetLayoutGuards(t *testing.T) {
	t.Run("checkpointScript quotes single quotes in messageID so shell commands cannot inject", func(t *testing.T) {
		requireGit(t)
		workspace, gitDir, env := splitGitDirs(t)
		pwnedFile := filepath.Join(t.TempDir(), "pwned")
		maliciousMsgID := fmt.Sprintf("m1'; touch %s; echo '", pwnedFile)

		script := checkpointScript(workspace, gitDir, maliciousMsgID)
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))

		_, statErr := os.Stat(pwnedFile)
		assert.True(t, os.IsNotExist(statErr), "injected shell command in messageID must NOT execute")
	})

	t.Run("resetWorkspaceToEmpty refuses non-versioned runner, host layout, root '/', and GitDir inside Root", func(
		t *testing.T,
	) {
		nonVer := &w3bNonVersionedRunner{}
		err := resetWorkspaceToEmpty(context.Background(), nonVer, "sess-1", "sbx-1")
		require.Error(t, err)
		assert.Empty(t, nonVer.calls)

		for _, bad := range []sandbox.WorkspaceLayout{
			{Origin: sandbox.WorkspaceOriginHost, Root: "/tmp/host-ws", GitDir: "/tmp/host-git"},
			{Origin: sandbox.WorkspaceOriginRemote, Root: "/", GitDir: "/var/lib/genyee/ws.git"},
			{Origin: sandbox.WorkspaceOriginRemote, Root: "/workspace", GitDir: "/workspace/.git"},
			{Origin: sandbox.WorkspaceOriginRemote, Root: "relative/ws", GitDir: "/var/lib/genyee/ws.git"},
		} {
			r := &w3bBadLayoutRunner{layout: bad}
			err := resetWorkspaceToEmpty(context.Background(), r, "sess-1", "sbx-1")
			require.Error(t, err, "must reject unsafe layout %+v", bad)
			assert.Empty(t, r.calls)
		}
	})
}

// ─── 13. WikiLint: Transient DB Error Protection & Cross-Tenant/Cross-KB Ref Isolation

type w3bLintKnowledgeSvc struct {
	interfaces.KnowledgeService
	byID map[string]*types.Knowledge
	errs map[string]error
}

func (s *w3bLintKnowledgeSvc) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if err, ok := s.errs[id]; ok {
		return nil, err
	}
	if kn, ok := s.byID[id]; ok {
		return kn, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}

func TestW3BAudit_WikiLint_AbortsOnTransientDBErrorAndRejectsCrossTenantSourceRef(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.KnowledgeBase{},
		&types.WikiFolder{},
		&types.WikiPage{},
		&types.WikiPageRevision{},
		&types.WikiPageIssue{},
	))

	ctx := context.Background()
	repo := apprepo.NewWikiPageRepository(db)
	wikiSvc := NewWikiPageService(repo, nil, nil, nil, nil, nil)
	const kbID = "kb-wiki-1"
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: kbID, TenantID: 7, Name: "wiki-kb"}).Error)
	body := strings.Repeat("Detailed architectural documentation for GENYEE wiki health verification. ", 2)

	_, err = wikiSvc.CreatePage(ctx, &types.WikiPage{
		TenantID:        7,
		KnowledgeBaseID: kbID,
		Slug:            "entity/transient",
		Title:           "Transient Doc Page",
		PageType:        types.WikiPageTypeEntity,
		Content:         body,
		SourceRefs:      types.StringArray{"kn-transient"},
	})
	require.NoError(t, err)

	kb := &types.KnowledgeBase{ID: kbID, TenantID: 7}
	kb.IndexingStrategy.WikiEnabled = true

	t.Run("AutoFix aborts on transient DB error without deleting live page", func(t *testing.T) {
		knowSvc := &w3bLintKnowledgeSvc{
			errs: map[string]error{"kn-transient": errors.New("driver: bad connection")},
		}
		lint := NewWikiLintService(wikiSvc, markdownLintKBService{kb: kb}, knowSvc)
		_, err := lint.AutoFix(ctx, kbID)
		require.Error(t, err)

		stillAlive, getErr := wikiSvc.GetPageBySlug(ctx, kbID, "entity/transient")
		require.NoError(t, getErr)
		require.NotNil(t, stillAlive)
	})

	t.Run("RunLint treats foreign-tenant or foreign-KB knowledge ID as non-live", func(t *testing.T) {
		_, err := wikiSvc.CreatePage(ctx, &types.WikiPage{
			TenantID:        7,
			KnowledgeBaseID: kbID,
			Slug:            "entity/foreign-ref",
			Title:           "Foreign Ref Page",
			PageType:        types.WikiPageTypeEntity,
			Content:         body,
			SourceRefs:      types.StringArray{"kn-other-tenant"},
		})
		require.NoError(t, err)

		knowSvc := &w3bLintKnowledgeSvc{
			byID: map[string]*types.Knowledge{
				"kn-transient":    {ID: "kn-transient", TenantID: 7, KnowledgeBaseID: kbID},
				"kn-other-tenant": {ID: "kn-other-tenant", TenantID: 99, KnowledgeBaseID: "kb-foreign"},
			},
		}
		lint := NewWikiLintService(wikiSvc, markdownLintKBService{kb: kb}, knowSvc)
		report, err := lint.RunLint(ctx, kbID)
		require.NoError(t, err)

		staleSlugs := map[string]bool{}
		for _, iss := range report.Issues {
			if iss.Type == LintIssueStaleRef {
				staleSlugs[iss.PageSlug] = true
			}
		}
		assert.False(t, staleSlugs["entity/transient"])
		assert.True(t, staleSlugs["entity/foreign-ref"], "foreign-tenant knowledge_id must not be accepted as live")
	})
}

// ─── 14. WikiIngest / Finalize: Zero-Tenant Deleted-Tenant Guard Bypass ──────

type w3bZeroTenantPendingRepo struct {
	wikiTenantGuardPendingRepo
}

func (r *w3bZeroTenantPendingRepo) PeekBatch(
	_ context.Context,
	_,
	_,
	scopeID string,
	_ int,
) ([]*types.TaskPendingOp, error) {
	return []*types.TaskPendingOp{
		{ID: 1, ScopeID: scopeID, Op: wikiFinalizeOpSlug, Payload: []byte(`{"slug":"s"}`)},
	}, nil
}

func TestW3BAudit_WikiTasks_ZeroTenantPayloadStillEnforcesDeletedTenantAfterKBLoad(t *testing.T) {
	rawPayload, err := json.Marshal(WikiIngestPayload{
		TenantID:        0, // legacy / recovery trigger without tenant ID
		KnowledgeBaseID: "kb-soft-deleted-tenant",
	})
	require.NoError(t, err)

	kb := &types.KnowledgeBase{ID: "kb-soft-deleted-tenant", TenantID: 77}
	kb.IndexingStrategy.WikiEnabled = true

	t.Run("ProcessWikiIngest checks kb.TenantID liveness when payload.TenantID is 0", func(t *testing.T) {
		repo := &w3bZeroTenantPendingRepo{wikiTenantGuardPendingRepo: wikiTenantGuardPendingRepo{active: false}}
		svc := &wikiIngestService{
			pendingRepo: repo,
			kbService:   markdownLintKBService{kb: kb},
		}
		err := svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, rawPayload))
		require.NoError(t, err)
		assert.Equal(t, []uint64{77}, repo.lookups)
		assert.Equal(t, []string{"kb-soft-deleted-tenant"}, repo.deletedKBs)
	})

	t.Run("ProcessWikiFinalize checks kb.TenantID liveness when payload.TenantID is 0", func(t *testing.T) {
		repo := &w3bZeroTenantPendingRepo{wikiTenantGuardPendingRepo: wikiTenantGuardPendingRepo{active: false}}
		svc := &wikiIngestService{
			pendingRepo: repo,
			kbService:   markdownLintKBService{kb: kb},
		}
		err := svc.ProcessWikiFinalize(context.Background(), asynq.NewTask(types.TypeWikiFinalize, rawPayload))
		require.NoError(t, err)
		assert.Equal(t, []uint64{77}, repo.lookups)
		assert.Equal(t, []string{"kb-soft-deleted-tenant"}, repo.deletedKBs)
	})
}

// ─── 15. WikiPage CreatePage Mass-Assignment & Wiki Slug Lock Token Ownership

func TestW3BAudit_WikiCreatePageSanitizesUserFields_AndSlugLockUsesTokenOwnership(t *testing.T) {
	t.Run("CreatePage ignores client-supplied ID, DeletedAt, Version, and InLinks for user edits", func(t *testing.T) {
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.AutoMigrate(
			&types.KnowledgeBase{},
			&types.WikiFolder{},
			&types.WikiPage{},
			&types.WikiPageRevision{},
			&types.WikiPageIssue{},
		))
		require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "kb-1"}).Error)

		repo := apprepo.NewWikiPageRepository(db)
		svc := NewWikiPageService(repo, nil, nil, nil, nil, nil)
		userCtx := types.WithWikiEditSource(context.Background(), types.WikiEditSourceUser)

		created, err := svc.CreatePage(userCtx, &types.WikiPage{
			ID:              "attacker-chosen-pk",
			TenantID:        7,
			KnowledgeBaseID: "kb-1",
			Slug:            "concept/safe",
			Title:           "Safe",
			Content:         "Safe content",
			Version:         999,
			InLinks:         types.StringArray{"forged/backlink"},
			DeletedAt:       gorm.DeletedAt{Time: time.Now(), Valid: true},
		})
		require.NoError(t, err)
		assert.NotEqual(t, "attacker-chosen-pk", created.ID)
		assert.Equal(t, 1, created.Version)
		assert.Empty(t, created.InLinks)
		assert.False(t, created.DeletedAt.Valid)

		// Must be visible as a live page!
		loaded, err := svc.GetPageBySlug(context.Background(), "kb-1", "concept/safe")
		require.NoError(t, err)
		assert.Equal(t, created.ID, loaded.ID)
	})

	t.Run("withSlugLock release does not delete successor lock if TTL expired mid-fn", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rc.Close() })

		svc := &wikiIngestService{redisClient: rc}
		key := wikiSlugLockPrefix + "kb-1:entity/shared"

		acquired, err := svc.withSlugLock(context.Background(), "kb-1", "entity/shared", func() error {
			// Simulate lock expiry and Holder B acquiring the lock before Holder A's deferred release runs.
			mr.Del(key)
			require.NoError(t, rc.Set(context.Background(), key, "holder-B-token", time.Minute).Err())
			return nil
		})
		require.NoError(t, err)
		require.True(t, acquired)

		// Holder B's lock MUST still exist in Redis!
		val, err := rc.Get(context.Background(), key).Result()
		require.NoError(t, err)
		assert.Equal(t, "holder-B-token", val, "unowned deferred unlock must not delete Holder B's live lock")
	})
}
