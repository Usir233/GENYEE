package datasource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/hibiken/asynq"
)

type stubConnector struct {
	typ string
}

func (s stubConnector) Type() string                                            { return s.typ }
func (s stubConnector) Validate(context.Context, *types.DataSourceConfig) error { return nil }
func (s stubConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}

func (s stubConnector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
}

func (s stubConnector) FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error) {
	return nil, nil
}

func (s stubConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return nil, nil, nil
}

func TestW5DatasourceAudit_ValidateConnectorBaseURLBlocksUserinfoQueryFragmentAndRedactsSecret(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	for _, tc := range []struct {
		name   string
		rawURL string
		secret string
	}{
		{name: "userinfo", rawURL: "https://admin:top-secret-pw@open.feishu.cn", secret: "top-secret-pw"},
		{
			name:   "malformed_escape_with_userinfo",
			rawURL: "https://admin:top-secret-pw%zz@open.feishu.cn",
			secret: "top-secret-pw",
		},
		{name: "query_string", rawURL: "https://open.feishu.cn?override=1"},
		{name: "fragment", rawURL: "https://open.feishu.cn#frag"},
		{name: "control_char", rawURL: "https://open.feishu.cn/\r\nx-injected:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConnectorBaseURL(tc.rawURL)
			if err == nil {
				t.Fatalf("expected %q to be rejected", tc.rawURL)
			}
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("error leaked embedded secret: %v", err)
			}
		})
	}
}

func TestW5DatasourceAudit_RegistryConcurrencyAndMetadataImmutability(t *testing.T) {
	reg := NewConnectorRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("conn-%02d", i)
			if err := reg.Register(stubConnector{typ: name}); err != nil {
				t.Errorf("Register(%s): %v", name, err)
			}
			if _, err := reg.Get(name); err != nil {
				t.Errorf("Get(%s): %v", name, err)
			}
			_ = reg.List()
		}()
	}
	wg.Wait()

	got := reg.List()
	if !slices.IsSorted(got) {
		t.Fatalf("expected ConnectorRegistry.List() to be sorted, got %v", got)
	}

	first := ListAvailableConnectors()
	if len(first) == 0 || len(first[0].Capabilities) == 0 {
		t.Fatal("expected non-empty connector metadata with capabilities")
	}
	origCap := first[0].Capabilities[0]
	first[0].Capabilities[0] = "corrupted-capability"

	second := ListAvailableConnectors()
	if second[0].Capabilities[0] != origCap {
		t.Fatalf(
			"mutating ListAvailableConnectors result corrupted global registry: got %q, want %q",
			second[0].Capabilities[0], origCap,
		)
	}
	for iter := 0; iter < 10; iter++ {
		next := ListAvailableConnectors()
		for idx := range second {
			if next[idx].Type != second[idx].Type {
				t.Fatalf(
					"non-deterministic ListAvailableConnectors order at index %d: %q vs %q",
					idx, next[idx].Type, second[idx].Type,
				)
			}
		}
	}
}

type failingRunningSyncRepo struct {
	*fakeSyncLogRepo
	runningErr error
}

func (r *failingRunningSyncRepo) HasRunningSync(ctx context.Context, dsID string) (bool, error) {
	if r.runningErr != nil {
		return false, r.runningErr
	}
	return r.fakeSyncLogRepo.HasRunningSync(ctx, dsID)
}

type errTaskEnqueuer struct {
	err   error
	count int
}

func (e *errTaskEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	e.count++
	return nil, e.err
}

func TestW5DatasourceAudit_SchedulerNilGuardErrorFailClosedAndWrappedConflict(t *testing.T) {
	s := NewScheduler(newFakeDataSourceRepo(), newFakeSyncLogRepo(), &fakeTaskEnqueuer{})
	if err := s.AddOrUpdate(nil); err == nil {
		t.Fatal("expected AddOrUpdate(nil) to return an error")
	}
	if err := s.AddOrUpdate(&types.DataSource{
		ID:           "   ",
		Status:       types.DataSourceStatusActive,
		SyncSchedule: "0 0 * * * *",
	}); err == nil {
		t.Fatal("expected AddOrUpdate with blank ID to return an error")
	}

	// 1. Fail closed when HasRunningSync returns a DB error (do not enqueue overlapping sync).
	dsRepo := newFakeDataSourceRepo()
	_ = dsRepo.Create(context.Background(), &types.DataSource{
		ID:       "ds-db-err",
		TenantID: 1,
		Status:   types.DataSourceStatusActive,
	})
	enq := &fakeTaskEnqueuer{}
	failScheduler := NewScheduler(dsRepo, &failingRunningSyncRepo{
		fakeSyncLogRepo: newFakeSyncLogRepo(),
		runningErr:      errors.New("db connection lost"),
	}, enq)
	failScheduler.triggerSync("ds-db-err", 1)
	if enq.count.Load() != 0 {
		t.Fatalf("expected triggerSync to fail closed on HasRunningSync error, enqueued=%d", enq.count.Load())
	}

	// 2. Wrapped asynq.ErrTaskIDConflict should mark sync log Canceled (deduplicated), not Failed.
	syncLogs := newFakeSyncLogRepo()
	conflictEnq := &errTaskEnqueuer{err: fmt.Errorf("redis enqueue: %w", asynq.ErrTaskIDConflict)}
	conflictScheduler := NewScheduler(dsRepo, syncLogs, conflictEnq)
	conflictScheduler.triggerSync("ds-db-err", 1)

	syncLogs.mu.Lock()
	defer syncLogs.mu.Unlock()
	if len(syncLogs.logs) != 1 {
		t.Fatalf("expected 1 sync log, got %d", len(syncLogs.logs))
	}
	for _, log := range syncLogs.logs {
		if log.Status != types.SyncLogStatusCanceled {
			t.Fatalf(
				"expected SyncLogStatusCanceled on wrapped ErrTaskIDConflict, got %q (msg=%q)",
				log.Status, log.ErrorMessage,
			)
		}
	}
}
