package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatasetService_GetDatasetByID(t *testing.T) {
	ctx := context.Background()
	svc := NewDatasetService()

	qaPairs, err := svc.GetDatasetByID(ctx, "default")
	require.NoError(t, err, "GetDatasetByID should not return error")
	require.NotEmpty(t, qaPairs, "QA pairs should not be empty")

	for i, pair := range qaPairs {
		t.Logf("Checking QA pair %d: QID=%d, Question=%s", i, pair.QID, pair.Question)
		assert.Greater(t, pair.QID, 0, "QID must be positive")
		assert.NotEmpty(t, pair.Question, "Question must not be empty")
		assert.Greater(t, pair.AID, 0, "AID must be positive")
		assert.NotEmpty(t, pair.Answer, "Answer must not be empty")
		assert.NotEmpty(t, pair.PIDs, "PIDs must not be empty")
		assert.NotEmpty(t, pair.Passages, "Passages must not be empty")
		assert.Equal(t, len(pair.PIDs), len(pair.Passages), "PIDs length should match Passages length")
		for j, p := range pair.Passages {
			assert.NotEmpty(t, p, "Passage %d must not be empty", j)
		}
	}
}

func TestDatasetService_CustomAndInvalidDatasetID(t *testing.T) {
	ctx := context.Background()
	svc := NewDatasetService()

	_, err := svc.GetDatasetByID(ctx, "../secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid dataset_id")

	_, err = svc.GetDatasetByID(ctx, "nonexistent_dataset_id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	tmpRoot := t.TempDir()
	customDir := filepath.Join(tmpRoot, "custom_eval")
	require.NoError(t, os.MkdirAll(customDir, 0o755))
	require.NoError(t, parquet.WriteFile(filepath.Join(customDir, "queries.parquet"), []TextInfo{{ID: 1, Text: "Custom question?"}}))
	require.NoError(t, parquet.WriteFile(filepath.Join(customDir, "corpus.parquet"), []TextInfo{{ID: 0, Text: "Custom passage."}}))
	require.NoError(t, parquet.WriteFile(filepath.Join(customDir, "answers.parquet"), []TextInfo{{ID: 10, Text: "Custom answer."}}))
	require.NoError(t, parquet.WriteFile(filepath.Join(customDir, "qrels.parquet"), []RelsInfo{{QID: 1, PID: 0}}))
	require.NoError(t, parquet.WriteFile(filepath.Join(customDir, "qas.parquet"), []QaInfo{{QID: 1, AID: 10}}))

	t.Setenv("EVALUATION_DATASET_DIR", tmpRoot)
	pairs, err := svc.GetDatasetByID(ctx, "custom_eval")
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, "Custom question?", pairs[0].Question)
	assert.Equal(t, "Custom answer.", pairs[0].Answer)
}

func TestEvaluationMemoryStorage_PersistenceAcrossRestart(t *testing.T) {
	snapshotPath := filepath.Join(t.TempDir(), "evaluation_tasks.json")
	store1 := newEvaluationMemoryStorageWithPath(snapshotPath)

	store1.register(&types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "eval_done_1",
			TenantID:  1,
			DatasetID: "default",
			Status:    types.EvaluationStatueSuccess,
			Total:     10,
			Finished:  10,
			StartTime: time.Now(),
		},
	})
	store1.register(&types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "eval_running_2",
			TenantID:  1,
			DatasetID: "default",
			Status:    types.EvaluationStatueRunning,
			Total:     10,
			Finished:  3,
			StartTime: time.Now(),
		},
	})

	store2 := newEvaluationMemoryStorageWithPath(snapshotPath)
	doneTask, err := store2.get("eval_done_1")
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueSuccess, doneTask.Task.Status)
	assert.Equal(t, 10, doneTask.Task.Finished)

	interruptedTask, err := store2.get("eval_running_2")
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueFailed, interruptedTask.Task.Status)
	assert.Contains(t, interruptedTask.Task.ErrMsg, "interrupted")
}

func TestEvaluationMemoryStorage_ConcurrentUpdateAndGet(t *testing.T) {
	snapshotPath := filepath.Join(t.TempDir(), "evaluation_tasks.json")
	store := newEvaluationMemoryStorageWithPath(snapshotPath)

	initial := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID:        "eval_race_1",
			TenantID:  1,
			DatasetID: "default",
			Status:    types.EvaluationStatueRunning,
			Total:     50,
			Finished:  0,
			StartTime: time.Now(),
		},
		Metric: &types.MetricResult{},
	}
	store.register(initial)

	// Mutate caller's original struct to verify register cloned it
	initial.Task.Status = types.EvaluationStatueFailed

	const workers = 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		var finished int
		for i := 0; i < workers; i++ {
			err := store.update("eval_race_1", func(params *types.EvaluationDetail) {
				finished++
				params.Task.Finished = finished
				params.Metric = &types.MetricResult{
					RetrievalMetrics: types.RetrievalMetrics{
						Recall: float64(finished) / float64(workers),
					},
				}
			})
			require.NoError(t, err)
		}
	}()

	for {
		snap, err := store.get("eval_race_1")
		require.NoError(t, err)
		assert.Equal(t, types.EvaluationStatueRunning, snap.Task.Status)
		// Mutate returned snapshot to verify get cloned it
		snap.Task.Finished = -1
		select {
		case <-done:
			finalSnap, err := store.get("eval_race_1")
			require.NoError(t, err)
			assert.Equal(t, workers, finalSnap.Task.Finished)
			assert.InDelta(t, 1.0, finalSnap.Metric.RetrievalMetrics.Recall, 1e-6)
			return
		default:
		}
	}
}

