package neo4j

import (
	"context"
	"errors"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/require"
)

func TestDelGraphReadsBatchOutcomes(t *testing.T) {
	readErr := errors.New("result interrupted")
	for _, tc := range []struct {
		name     string
		results  []deleteBatchResult
		wantErr  string
		wantRuns int
	}{
		{"both stages succeed", []deleteBatchResult{{}, {}}, "", 2},
		{"relationship batch fails", []deleteBatchResult{{failedBatches: 1}, {}}, "delete relationships", 1},
		{"node operations fail", []deleteBatchResult{{}, {failedOperations: 2}}, "delete nodes", 2},
		{"terminated", []deleteBatchResult{{terminated: true}, {}}, "delete relationships", 1},
		{"result read fails", []deleteBatchResult{{err: readErr}, {}}, readErr.Error(), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &deleteGraphTx{results: tc.results}
			repo := NewNeo4jRepository(deleteGraphDriver{tx: tx})
			err := repo.DelGraph(context.Background(), []types.NameSpace{{KnowledgeBase: "kb", Knowledge: "doc"}})
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
			require.Equal(t, tc.wantRuns, tx.runs)
		})
	}
}

type deleteGraphDriver struct {
	neo4j.Driver
	tx *deleteGraphTx
}

func (d deleteGraphDriver) NewSession(context.Context, neo4j.SessionConfig) neo4j.Session {
	return deleteGraphSession{tx: d.tx}
}

type deleteGraphSession struct {
	neo4j.Session
	tx *deleteGraphTx
}

func (s deleteGraphSession) ExecuteWrite(
	_ context.Context, work neo4j.ManagedTransactionWork, _ ...func(*neo4j.TransactionConfig),
) (any, error) {
	return work(s.tx)
}

func (s deleteGraphSession) Close(context.Context) error { return nil }

type deleteGraphTx struct {
	neo4j.ManagedTransaction
	results []deleteBatchResult
	runs    int
}

func (tx *deleteGraphTx) Run(context.Context, string, map[string]any) (neo4j.Result, error) {
	result := tx.results[tx.runs]
	tx.runs++
	return &result, nil
}

type deleteBatchResult struct {
	neo4j.Result
	failedBatches    int64
	failedOperations int64
	terminated       bool
	err              error
	read             bool
}

func (r *deleteBatchResult) Next(context.Context) bool {
	if r.read || r.err != nil {
		return false
	}
	r.read = true
	return true
}

func (r *deleteBatchResult) Record() *neo4j.Record {
	return &neo4j.Record{
		Keys:   []string{"failedBatches", "failedOperations", "wasTerminated"},
		Values: []any{r.failedBatches, r.failedOperations, r.terminated},
	}
}

func (r *deleteBatchResult) Err() error { return r.err }

func (r *deleteBatchResult) Consume(context.Context) (neo4j.ResultSummary, error) {
	return nil, r.err
}
