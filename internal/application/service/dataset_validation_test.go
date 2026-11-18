package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/require"
)

type evaluationDatasetFixture struct {
	queries, corpus, answers []TextInfo
	qrels                    []RelsInfo
	qas                      []QaInfo
}

func validEvaluationDatasetFixture() evaluationDatasetFixture {
	return evaluationDatasetFixture{
		queries: []TextInfo{{ID: 0, Text: "question"}},
		corpus:  []TextInfo{{ID: -1, Text: "passage"}},
		answers: []TextInfo{{ID: 0, Text: "answer"}},
		qrels:   []RelsInfo{{QID: 0, PID: -1}},
		qas:     []QaInfo{{QID: 0, AID: 0}},
	}
}

func writeEvaluationDatasetFixture(t *testing.T, fixture evaluationDatasetFixture) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "queries.parquet"), fixture.queries))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "corpus.parquet"), fixture.corpus))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "answers.parquet"), fixture.answers))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "qrels.parquet"), fixture.qrels))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "qas.parquet"), fixture.qas))
	t.Setenv("EVALUATION_DATASET_DIR", dir)
	return dir
}

func TestDatasetRejectsInvalidRelationsAndText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*evaluationDatasetFixture)
		want   string
	}{
		{"unknown passage", func(d *evaluationDatasetFixture) { d.qrels[0].PID = 9 }, "passage 9"},
		{"unknown answer", func(d *evaluationDatasetFixture) { d.qas[0].AID = 9 }, "answer 9"},
		{"unknown query in qrels", func(d *evaluationDatasetFixture) { d.qrels[0].QID = 9 }, "query 9"},
		{"unknown query in qas", func(d *evaluationDatasetFixture) { d.qas[0].QID = 9 }, "query 9"},
		{"duplicate query ID", func(
			d *evaluationDatasetFixture,
		) {
			d.queries = append(d.queries, TextInfo{ID: 0, Text: "overwritten"})
		}, "duplicate queries ID 0"},
		{"duplicate passage ID", func(
			d *evaluationDatasetFixture,
		) {
			d.corpus = append(d.corpus, TextInfo{ID: -1, Text: "overwritten"})
		}, "duplicate corpus ID -1"},
		{"duplicate answer ID", func(
			d *evaluationDatasetFixture,
		) {
			d.answers = append(d.answers, TextInfo{ID: 0, Text: "overwritten"})
		}, "duplicate answers ID 0"},
		{"multiple answers for query", func(d *evaluationDatasetFixture) {
			d.answers = append(d.answers, TextInfo{ID: 1, Text: "other"})
			d.qas = append(d.qas, QaInfo{QID: 0, AID: 1})
		}, "multiple answers for query 0"},
		{"empty query", func(d *evaluationDatasetFixture) { d.queries[0].Text = " \n" }, "empty queries text"},
		{"empty passage", func(d *evaluationDatasetFixture) { d.corpus[0].Text = "" }, "empty corpus text"},
		{"empty answer", func(d *evaluationDatasetFixture) { d.answers[0].Text = "\t" }, "empty answers text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := validEvaluationDatasetFixture()
			tc.mutate(&fixture)
			writeEvaluationDatasetFixture(t, fixture)
			pairs, err := NewDatasetService().GetDatasetByID(context.Background(), "default")
			require.ErrorContains(t, err, tc.want, "invalid data must not silently become QA pairs: %+v", pairs)
		})
	}
}

func TestDatasetRejectsMissingIDColumn(t *testing.T) {
	dir := writeEvaluationDatasetFixture(t, validEvaluationDatasetFixture())
	// Without an explicit schema check parquet-go fills the absent ID with
	// zero, accidentally joining this row to the valid zero-ID relationships.
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "queries.parquet"), []struct {
		Text string `parquet:"text"`
	}{{Text: "question"}}))
	_, err := NewDatasetService().GetDatasetByID(context.Background(), "default")
	require.ErrorContains(t, err, "missing column id")
}

func TestDatasetRejectsNullID(t *testing.T) {
	dir := writeEvaluationDatasetFixture(t, validEvaluationDatasetFixture())
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "queries.parquet"), []struct {
		ID   *int64 `parquet:"id,optional"`
		Text string `parquet:"text"`
	}{{Text: "question"}}))
	_, err := NewDatasetService().GetDatasetByID(context.Background(), "default")
	require.ErrorContains(t, err, "null column id")
}

func TestDatasetAcceptsArbitraryIDsAndDuplicateRelations(t *testing.T) {
	fixture := validEvaluationDatasetFixture()
	fixture.corpus = append(fixture.corpus, TextInfo{ID: int64(^uint64(0) >> 1), Text: "large passage"})
	fixture.qrels = append(fixture.qrels, fixture.qrels[0], RelsInfo{QID: 0, PID: fixture.corpus[1].ID})
	fixture.qas = append(fixture.qas, fixture.qas[0])
	writeEvaluationDatasetFixture(t, fixture)
	pairs, err := NewDatasetService().GetDatasetByID(context.Background(), "default")
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	require.Equal(t, 0, pairs[0].QID)
	require.Equal(t, []int{-1, -1, int(^uint(0) >> 1)}, pairs[0].PIDs)
	require.Equal(t, []string{"passage", "passage", "large passage"}, pairs[0].Passages)
	require.Equal(t, "answer", pairs[0].Answer)
}

func TestDatasetAllowsUnanswerableQueries(t *testing.T) {
	fixture := validEvaluationDatasetFixture()
	fixture.qas, fixture.qrels = nil, nil
	writeEvaluationDatasetFixture(t, fixture)
	pairs, err := NewDatasetService().GetDatasetByID(context.Background(), "default")
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	require.Empty(t, pairs[0].Answer)
	require.Empty(t, pairs[0].Passages)
	require.Empty(t, pairs[0].PIDs)
}
