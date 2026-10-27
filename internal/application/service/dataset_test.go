package service

import (
	"context"
	"testing"

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
