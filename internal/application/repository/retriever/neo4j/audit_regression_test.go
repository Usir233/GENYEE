package neo4j

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedCypherCall struct {
	query  string
	params map[string]any
}

type captureTx struct {
	driver.ManagedTransaction
	calls       []recordedCypherCall
	consumed    int
	records     []*driver.Record
	runErr      error
	consumeErr  error
	failOnCall  int
}

func (tx *captureTx) Run(_ context.Context, cypher string, params map[string]any) (driver.Result, error) {
	tx.calls = append(tx.calls, recordedCypherCall{query: cypher, params: params})
	if tx.runErr != nil && (tx.failOnCall == 0 || len(tx.calls) == tx.failOnCall) {
		return nil, tx.runErr
	}
	return &captureResult{
		tx:      tx,
		records: tx.records,
		err:     tx.consumeErr,
	}, nil
}

type captureResult struct {
	driver.Result
	tx      *captureTx
	records []*driver.Record
	idx     int
	err     error
}

func (r *captureResult) Next(context.Context) bool {
	if r.idx >= len(r.records) {
		return false
	}
	r.idx++
	return true
}

func (r *captureResult) Record() *driver.Record {
	return r.records[r.idx-1]
}

func (r *captureResult) Err() error {
	return nil
}

func (r *captureResult) Consume(context.Context) (driver.ResultSummary, error) {
	r.tx.consumed++
	return nil, r.err
}

type captureSession struct {
	driver.Session
	tx          *captureTx
	callbackErr error
	closed      bool
}

func (s *captureSession) ExecuteWrite(_ context.Context, work driver.ManagedTransactionWork, _ ...func(*driver.TransactionConfig)) (any, error) {
	v, err := work(s.tx)
	s.callbackErr = err
	return v, err
}

func (s *captureSession) ExecuteRead(_ context.Context, work driver.ManagedTransactionWork, _ ...func(*driver.TransactionConfig)) (any, error) {
	v, err := work(s.tx)
	s.callbackErr = err
	return v, err
}

func (s *captureSession) Close(context.Context) error {
	s.closed = true
	return nil
}

type captureDriver struct {
	driver.Driver
	session *captureSession
}

func (d *captureDriver) NewSession(context.Context, driver.SessionConfig) driver.Session {
	return d.session
}

func TestDelGraphParameterizesOuterCypherAndChecksBatchFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("double-quote and backslash namespace payload cannot break out of outer Cypher", func(t *testing.T) {
		tx := &captureTx{
			records: []*driver.Record{
				{
					Keys:   []string{"total", "failedBatches", "errorMessages"},
					Values: []any{int64(3), int64(0), map[string]any{}},
				},
			},
		}
		sess := &captureSession{tx: tx}
		repo := NewNeo4jRepository(&captureDriver{session: sess})

		maliciousKB := `kb") YIELD batches CALL db.labels() YIELD label //`
		maliciousDoc := `doc\"` + "`" + ` DETACH DELETE n`
		err := repo.DelGraph(ctx, []types.NameSpace{{
			KnowledgeBase: maliciousKB,
			Knowledge:     maliciousDoc,
		}})
		require.NoError(t, err)
		require.NotEmpty(t, tx.calls)
		assert.Equal(t, len(tx.calls), tx.consumed, "DelGraph must Consume every result stream")

		for _, call := range tx.calls {
			assert.NotContains(t, call.query, maliciousKB,
				"outer Cypher must not interpolate raw namespace strings into double-quoted Cypher literals")
			assert.NotContains(t, call.query, "parallel: true",
				"relationship/node deletion must not run with parallel: true lock contention")
			iterQuery, ok := call.params["iterateQuery"].(string)
			require.True(t, ok, "inner APOC query must be passed via $iterateQuery parameter")
			assert.Equal(t, 0, countRune(iterQuery, '`')%2, "inner iterateQuery must keep balanced backticks")
			assert.Contains(t, iterQuery, "{kg: $knowledge_id}")
		}
	})

	t.Run("whole-KB namespace omits empty kg filter while keeping KB label", func(t *testing.T) {
		tx := &captureTx{
			records: []*driver.Record{
				{
					Keys:   []string{"total", "failedBatches", "errorMessages"},
					Values: []any{int64(2), int64(0), map[string]any{}},
				},
			},
		}
		sess := &captureSession{tx: tx}
		repo := NewNeo4jRepository(&captureDriver{session: sess})

		require.NoError(t, repo.DelGraph(ctx, []types.NameSpace{{KnowledgeBase: "kb-only"}}))
		require.Len(t, tx.calls, 1)
		iterQuery, _ := tx.calls[0].params["iterateQuery"].(string)
		assert.Contains(t, iterQuery, "`ENTITYkb_only`")
		assert.NotContains(t, iterQuery, "{kg: $knowledge_id}")
	})

	t.Run("APOC failedBatches surfaces an error and transient driver error stays retryable", func(t *testing.T) {
		tx := &captureTx{
			records: []*driver.Record{
				{
					Keys:   []string{"total", "failedBatches", "errorMessages"},
					Values: []any{int64(1), int64(1), map[string]any{"Cannot delete": int64(1)}},
				},
			},
		}
		sess := &captureSession{tx: tx}
		repo := NewNeo4jRepository(&captureDriver{session: sess})
		err := repo.DelGraph(ctx, []types.NameSpace{{KnowledgeBase: "kb", Knowledge: "doc"}})
		require.ErrorContains(t, err, "failed")

		deadlock := &driver.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
		txRetry := &captureTx{runErr: deadlock}
		sessRetry := &captureSession{tx: txRetry}
		repoRetry := NewNeo4jRepository(&captureDriver{session: sessRetry})
		err = repoRetry.DelGraph(ctx, []types.NameSpace{{KnowledgeBase: "kb", Knowledge: "doc"}})
		require.ErrorIs(t, err, deadlock)
		assert.True(t, driver.IsRetryable(sessRetry.callbackErr))
	})
}

func TestNeo4jOperationsFailClosedOnEmptyNamespaceAndEmptySearchTerms(t *testing.T) {
	ctx := context.Background()

	t.Run("AddGraph rejects missing KB or Knowledge namespace and sanitizes malformed graph items", func(t *testing.T) {
		tx := &captureTx{}
		sess := &captureSession{tx: tx}
		repo := NewNeo4jRepository(&captureDriver{session: sess})

		require.Error(t, repo.AddGraph(ctx, types.NameSpace{}, []*types.GraphData{{}}))
		require.Error(t, repo.AddGraph(ctx, types.NameSpace{KnowledgeBase: "kb"}, []*types.GraphData{{}}))
		require.Error(t, repo.AddGraph(ctx, types.NameSpace{Knowledge: "doc"}, []*types.GraphData{{}}))
		assert.Empty(t, tx.calls, "invalid namespace must fail closed before executing Cypher")

		err := repo.AddGraph(ctx, types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}, []*types.GraphData{
			nil,
			{
				Node: []*types.GraphNode{
					nil,
					{Name: "   "},
					{Name: "Acme"},
				},
				Relation: []*types.GraphRelation{
					nil,
					{Node1: "", Node2: "Shanghai", Type: "LOCATED_IN"},
					{Node1: "Acme", Node2: "Shanghai", Type: "   "},
					{Node1: "Acme", Node2: "Shanghai", Type: "LOCATED_IN`)\x00"},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, tx.calls, 2)
		nodeRows, ok := tx.calls[0].params["data"].([]map[string]any)
		require.True(t, ok)
		require.Len(t, nodeRows, 1)
		assert.Equal(t, "Acme", nodeRows[0]["name"])

		relRows, ok := tx.calls[1].params["data"].([]map[string]any)
		require.True(t, ok)
		require.Len(t, relRows, 1)
		assert.NotContains(t, relRows[0]["type"], "`")
		assert.NotContains(t, relRows[0]["type"], "\x00")
	})

	t.Run("DelGraph rejects any empty KnowledgeBase atomically before mutating", func(t *testing.T) {
		tx := &captureTx{}
		sess := &captureSession{tx: tx}
		repo := NewNeo4jRepository(&captureDriver{session: sess})

		require.NoError(t, repo.DelGraph(ctx, nil))
		assert.Empty(t, tx.calls)

		require.Error(t, repo.DelGraph(ctx, []types.NameSpace{{}}))
		require.Error(t, repo.DelGraph(ctx, []types.NameSpace{
			{KnowledgeBase: "kb-1", Knowledge: "doc-1"},
			{KnowledgeBase: "   ", Knowledge: "doc-2"},
		}))
		assert.Empty(t, tx.calls, "mixed valid/invalid namespaces must fail closed before any write")
	})

	t.Run("SearchNode and SearchGraph reject empty KB and never run CONTAINS empty-string wildcard", func(t *testing.T) {
		tx := &captureTx{}
		sess := &captureSession{tx: tx}
		drv := &captureDriver{session: sess}
		repo := NewNeo4jRepository(drv)
		publicRepo := NewGraphSearchRepository(drv)

		_, err := repo.SearchNode(ctx, types.NameSpace{}, []string{"Acme"})
		require.Error(t, err)
		_, err = repo.SearchNode(ctx, types.NameSpace{Knowledge: "doc-only"}, []string{"Acme"})
		require.Error(t, err)
		assert.Empty(t, tx.calls)

		emptyGraph, err := repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb"}, []string{"", "   "})
		require.NoError(t, err)
		require.NotNil(t, emptyGraph)
		assert.Empty(t, emptyGraph.Node)
		assert.Empty(t, tx.calls, "blank-only node terms must not query Neo4j with CONTAINS ''")

		_, err = repo.SearchNode(ctx, types.NameSpace{KnowledgeBase: "kb"}, []string{"Acme", "", "  ", "Acme"})
		require.NoError(t, err)
		require.Len(t, tx.calls, 1)
		assert.Equal(t, []string{"Acme"}, tx.calls[0].params["nodes"])

		tx.calls = nil
		_, err = publicRepo.SearchGraph(ctx, types.NameSpace{KnowledgeBase: "kb"}, "   ")
		require.Error(t, err)
		assert.Empty(t, tx.calls)
	})
}

func countRune(s string, target rune) int {
	n := 0
	for _, r := range s {
		if r == target {
			n++
		}
	}
	return n
}
