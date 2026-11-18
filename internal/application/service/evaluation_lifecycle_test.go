package service

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type evaluationTestDataset struct {
	interfaces.DatasetService
	pairs []*types.QAPair
}

func (d *evaluationTestDataset) GetDatasetByID(context.Context, string) ([]*types.QAPair, error) {
	return d.pairs, nil
}

func (d *evaluationTestDataset) LoadDataset(context.Context, string) (*types.EvaluationDataset, error) {
	corpus := dataset{corpus: make(map[int64]string)}
	for _, pair := range d.pairs {
		if pair != nil {
			for i, pid := range pair.PIDs {
				if i < len(pair.Passages) {
					corpus.corpus[int64(pid)] = pair.Passages[i]
				}
			}
		}
	}
	return &types.EvaluationDataset{QAPairs: d.pairs, CorpusPassages: corpus.Passages()}, nil
}

type evaluationTestKBs struct {
	interfaces.KnowledgeBaseService
	deleted    []string
	cleanupErr error
	contextErr error
}

func (k *evaluationTestKBs) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "source", EmbeddingModelID: "embedding"}, nil
}

func (k *evaluationTestKBs) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "temporary"}, nil
}

func (k *evaluationTestKBs) DeleteKnowledgeBase(ctx context.Context, id string) error {
	k.deleted = append(k.deleted, id)
	k.contextErr = ctx.Err()
	return k.cleanupErr
}

type evaluationTestKnowledgeRepo struct{ interfaces.KnowledgeRepository }

func (*evaluationTestKnowledgeRepo) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return []*types.Knowledge{{ID: "knowledge", TenantID: 1, KnowledgeBaseID: "temporary"}}, nil
}

type evaluationTestKnowledge struct {
	interfaces.KnowledgeService
	created  bool
	deleted  bool
	passages []string
}

func (k *evaluationTestKnowledge) CreateKnowledgeFromPassageSync(
	_ context.Context,
	_ string,
	passages []string,
	_ string,
) (*types.Knowledge, error) {
	k.created = true
	k.passages = append([]string(nil), passages...)
	return &types.Knowledge{ID: "knowledge"}, nil
}

func TestEvaluationIndexesUnreferencedCorpusPassages(t *testing.T) {
	fixture := validEvaluationDatasetFixture()
	fixture.corpus = append(fixture.corpus, TextInfo{ID: 1 << 40, Text: "unrelated distractor"})
	writeEvaluationDatasetFixture(t, fixture)
	e, detail, _, knowledge := newEvaluationLifecycleTest(t, "question")
	e.dataset = NewDatasetService()
	e.sessionService = &evaluationTestSessions{qa: func(_ context.Context, chat *types.ChatManage) error {
		chat.SearchResult = []*types.SearchResult{{Content: "unrelated distractor"}, {Content: "passage"}}
		return nil
	}}
	detail.Task.DatasetID = "default"
	e.runEvaluation(evaluationTestContext(), detail, "temporary")
	result, err := e.evaluationMemoryStorage.get(detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueSuccess, result.Task.Status)
	require.Equal(t, []string{"passage", "unrelated distractor"}, knowledge.passages,
		"indexing only qrels passages silently removes distractors from evaluation")
	require.InDelta(t, .5, result.Metric.RetrievalMetrics.Precision, 1e-9)
	require.InDelta(t, .5, result.Metric.RetrievalMetrics.MRR, 1e-9)
}

func TestEvaluationInvalidDatasetFailsBeforeIndexing(t *testing.T) {
	fixture := validEvaluationDatasetFixture()
	fixture.qrels[0].PID = 9
	writeEvaluationDatasetFixture(t, fixture)
	e, detail, kbs, knowledge := newEvaluationLifecycleTest(t, "question")
	e.dataset = NewDatasetService()
	detail.Task.DatasetID = "default"
	e.runEvaluation(evaluationTestContext(), detail, "temporary")
	result, err := e.evaluationMemoryStorage.get(detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueFailed, result.Task.Status)
	require.Contains(t, result.Task.ErrMsg, "passage 9")
	require.False(t, knowledge.created)
	require.Equal(t, []string{"temporary"}, kbs.deleted)
}

func TestEvaluationResultRemainsTenantScoped(t *testing.T) {
	e, detail, _, _ := newEvaluationLifecycleTest(t, "question")
	_, err := e.EvaluationResult(types.WithExecutionTenant(context.Background(), 2), detail.Task.ID)
	require.ErrorContains(t, err, "tenant ID does not match")
	result, err := e.EvaluationResult(evaluationTestContext(), detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, detail.Task.ID, result.Task.ID)
	result.Task.Status = types.EvaluationStatueFailed
	again, err := e.EvaluationResult(evaluationTestContext(), detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatuePending, again.Task.Status)
}

func (*evaluationTestKnowledge) GetRepository() interfaces.KnowledgeRepository {
	return &evaluationTestKnowledgeRepo{}
}

func (k *evaluationTestKnowledge) DeleteKnowledgeList(ctx context.Context, _ []string) error {
	k.deleted = true
	return ctx.Err()
}

type evaluationTestSessions struct {
	interfaces.SessionService
	qa func(context.Context, *types.ChatManage) error
}

func (s *evaluationTestSessions) KnowledgeQAByEvent(
	ctx context.Context,
	chat *types.ChatManage,
	_ []types.EventType,
) error {
	return s.qa(ctx, chat)
}

type evaluationTestModels struct{ interfaces.ModelService }

func (*evaluationTestModels) ListModels(context.Context) ([]*types.Model, error) { return nil, nil }

func newEvaluationLifecycleTest(
	t *testing.T,
	questions ...string,
) (*EvaluationService, *types.EvaluationDetail, *evaluationTestKBs, *evaluationTestKnowledge) {
	t.Helper()
	pairs := make([]*types.QAPair, len(questions))
	for i, question := range questions {
		pairs[i] = &types.QAPair{
			QID:      i,
			Question: question,
			PIDs:     []int{i},
			Passages: []string{question},
			Answer:   "answer",
		}
	}
	kbs := &evaluationTestKBs{}
	knowledge := &evaluationTestKnowledge{}
	e := &EvaluationService{
		config: &config.Config{
			Conversation: &config.ConversationConfig{Summary: &config.SummaryConfig{}},
		},
		dataset:              &evaluationTestDataset{pairs: pairs},
		knowledgeBaseService: kbs,
		knowledgeService:     knowledge,
		modelService:         &evaluationTestModels{},
		sessionService: &evaluationTestSessions{qa: func(
			context.Context,
			*types.ChatManage,
		) error {
			return nil
		}},
		evaluationMemoryStorage: newEvaluationMemoryStorageWithPath(filepath.Join(t.TempDir(), "tasks.json")),
	}
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "evaluation-test",
			TenantID:  1,
			DatasetID: "test",
			Status:    types.EvaluationStatuePending,
		},
		Params: &types.ChatManage{},
	}
	e.evaluationMemoryStorage.register(detail)
	return e, detail, kbs, knowledge
}

func evaluationTestContext() context.Context {
	return types.WithExecutionTenant(context.Background(), 1)
}

func TestEvaluationTaskSuccessIsPersisted(t *testing.T) {
	e, detail, kbs, knowledge := newEvaluationLifecycleTest(t, "one", "two", "three")
	e.runEvaluation(evaluationTestContext(), detail, "temporary")
	// Read from a fresh store, not the mutable in-memory task.
	restarted := newEvaluationMemoryStorageWithPath(e.evaluationMemoryStorage.filePath)
	result, err := restarted.get(detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueSuccess, result.Task.Status)
	require.Equal(t, 3, result.Task.Total)
	require.Equal(t, 3, result.Task.Finished)
	require.Empty(t, result.Task.ErrMsg)
	require.Equal(t, []string{"temporary"}, kbs.deleted)
	require.True(t, knowledge.deleted)
}

func TestEvaluationBackgroundTaskSurvivesRequestCancellation(t *testing.T) {
	e, _, _, _ := newEvaluationLifecycleTest(t, "one")
	started, release := make(chan struct{}), make(chan struct{})
	var sawCancelledContext atomic.Bool
	e.sessionService = &evaluationTestSessions{qa: func(ctx context.Context, _ *types.ChatManage) error {
		close(started)
		<-release
		sawCancelledContext.Store(ctx.Err() != nil)
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(evaluationTestContext())
	defer cancel()
	detail, err := e.Evaluation(ctx, "test", "source", "chat", "rerank")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("background evaluation did not start")
	}
	cancel()
	close(release)
	require.Eventually(t, func() bool {
		result, err := e.evaluationMemoryStorage.get(detail.Task.ID)
		return err == nil && result.Task.Status == types.EvaluationStatueSuccess
	}, 5*time.Second, 10*time.Millisecond)
	require.False(t, sawCancelledContext.Load(), "returning the HTTP response must not cancel an admitted evaluation")
}

func TestEvaluationWorkerFailureCancelsPeersAndPersistsFailure(t *testing.T) {
	previous := runtime.GOMAXPROCS(3)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for _, panicWorker := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicWorker], func(t *testing.T) {
			e, detail, kbs, knowledge := newEvaluationLifecycleTest(t, "blocked", "failure", "unstarted")
			started := make(chan struct{})
			var cancelled atomic.Bool
			e.sessionService = &evaluationTestSessions{qa: func(ctx context.Context, chat *types.ChatManage) error {
				switch chat.Query {
				case "blocked":
					close(started)
					<-ctx.Done()
					cancelled.Store(true)
					return ctx.Err()
				case "failure":
					<-started
					if panicWorker {
						panic("model unavailable")
					}
					return errors.New("model unavailable")
				default:
					return nil
				}
			}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				e.runEvaluation(evaluationTestContext(), detail, "temporary")
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("evaluation did not cancel its blocked worker")
			}
			require.True(t, cancelled.Load())
			result, err := newEvaluationMemoryStorageWithPath(e.evaluationMemoryStorage.filePath).get(detail.Task.ID)
			require.NoError(t, err)
			require.Equal(t, types.EvaluationStatueFailed, result.Task.Status)
			require.Contains(t, result.Task.ErrMsg, "model unavailable")
			require.Equal(t, 0, result.Task.Finished)
			require.Equal(t, []string{"temporary"}, kbs.deleted)
			require.NoError(t, kbs.contextErr)
			require.True(t, knowledge.deleted)
		})
	}
}

func TestEvaluationCancelledTaskStillCleansTemporaryKB(t *testing.T) {
	e, detail, kbs, knowledge := newEvaluationLifecycleTest(t, "one")
	ctx, cancel := context.WithCancel(evaluationTestContext())
	cancel()
	e.runEvaluation(ctx, detail, "temporary")
	result, err := e.evaluationMemoryStorage.get(detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueFailed, result.Task.Status)
	require.Contains(t, result.Task.ErrMsg, "context canceled")
	require.Equal(t, []string{"temporary"}, kbs.deleted)
	require.NoError(t, kbs.contextErr)
	require.False(t, knowledge.created)
}

func TestEvaluationEmptyDatasetAndCleanupFailureAreNotSuccess(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(map[bool]string{true: "empty-dataset", false: "cleanup-error"}[empty], func(t *testing.T) {
			questions := []string{"one"}
			if empty {
				questions = nil
			}
			e, detail, kbs, _ := newEvaluationLifecycleTest(t, questions...)
			if !empty {
				kbs.cleanupErr = errors.New("cleanup database unavailable")
			}
			e.runEvaluation(evaluationTestContext(), detail, "temporary")
			result, err := e.evaluationMemoryStorage.get(detail.Task.ID)
			require.NoError(t, err)
			require.Equal(t, types.EvaluationStatueFailed, result.Task.Status)
			require.NotEmpty(t, result.Task.ErrMsg)
			require.Equal(t, []string{"temporary"}, kbs.deleted)
		})
	}
}

func TestEvaluationSetupFailureCleansTemporaryKB(t *testing.T) {
	e, _, kbs, _ := newEvaluationLifecycleTest(t, "one")
	_, err := e.Evaluation(evaluationTestContext(), "test", "source", "", "rerank")
	require.ErrorContains(t, err, "no default chat model found")
	require.Equal(t, []string{"temporary"}, kbs.deleted)
}

func TestEvaluationPassagesUseSparseIDsWithoutLargeAllocation(t *testing.T) {
	ds := dataset{corpus: map[int64]string{1 << 60: "last", 7: "first"}}
	require.Equal(t, []string{"first", "last"}, ds.Passages())
	for _, pair := range []*types.QAPair{nil, {PIDs: []int{1}}} {
		e, detail, kbs, knowledge := newEvaluationLifecycleTest(t, "one")
		e.dataset = &evaluationTestDataset{pairs: []*types.QAPair{pair}}
		err := e.EvalDataset(evaluationTestContext(), detail, "temporary")
		require.ErrorContains(t, err, "invalid passages")
		require.False(t, knowledge.created)
		require.Equal(t, []string{"temporary"}, kbs.deleted)
	}
}
