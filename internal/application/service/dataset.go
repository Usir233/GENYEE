package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/parquet-go/parquet-go"
)

// DatasetService provides operations for working with datasets
type DatasetService struct{}

// NewDatasetService creates a new DatasetService instance
func NewDatasetService() interfaces.DatasetService {
	return &DatasetService{}
}

// TextInfo represents text data with ID in parquet format
type TextInfo struct {
	ID   int64  `parquet:"id"`   // Unique identifier
	Text string `parquet:"text"` // Text content
}

// RelsInfo represents question-passage relations in parquet format
type RelsInfo struct {
	QID int64 `parquet:"qid"` // Question ID
	PID int64 `parquet:"pid"` // Passage ID
}

// QaInfo represents question-answer relations in parquet format
type QaInfo struct {
	QID int64 `parquet:"qid"` // Question ID
	AID int64 `parquet:"aid"` // Answer ID
}

// GetDatasetByID retrieves QA pairs from dataset by ID
func (d *DatasetService) GetDatasetByID(ctx context.Context, datasetID string) ([]*types.QAPair, error) {
	ds, err := d.LoadDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	return ds.QAPairs, nil
}

// LoadDataset loads each file once and preserves the entire retrieval corpus.
func (d *DatasetService) LoadDataset(ctx context.Context, datasetID string) (*types.EvaluationDataset, error) {
	logger.Info(ctx, "Start getting dataset by ID")
	logger.Infof(ctx, "Getting dataset with ID: %s", datasetID)

	ds, err := loadDatasetByID(datasetID)
	if err != nil {
		return nil, err
	}
	ds.PrintStats(ctx)
	qaPairs := ds.Iterate()

	logger.Infof(ctx, "Retrieved %d QA pairs from dataset", len(qaPairs))
	return &types.EvaluationDataset{QAPairs: qaPairs, CorpusPassages: ds.Passages()}, nil
}

// DefaultDataset loads and initializes the default dataset from parquet files
func DefaultDataset() dataset {
	ds, err := loadDatasetByID("default")
	if err != nil {
		panic(err)
	}
	return ds
}

func resolveDatasetDir(datasetID string) (string, error) {
	trimmed := strings.TrimSpace(datasetID)
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, `/\`) {
		return "", fmt.Errorf("invalid dataset_id %q", datasetID)
	}

	subDir := "samples"
	if trimmed != "" && trimmed != "default" && trimmed != "samples" {
		subDir = trimmed
	}

	var candidates []string
	if envDir := strings.TrimSpace(os.Getenv("EVALUATION_DATASET_DIR")); envDir != "" {
		candidates = append(candidates, filepath.Join(envDir, subDir))
		if subDir == "samples" {
			candidates = append(candidates, envDir)
		}
	}
	candidates = append(candidates,
		filepath.Join("./dataset", subDir),
		filepath.Join("../../dataset", subDir),
		filepath.Join("../../../dataset", subDir),
	)

	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, "queries.parquet")); err == nil && !info.IsDir() {
			return dir, nil
		}
	}
	return candidates[0], fmt.Errorf("dataset %q not found (expected parquet files under %s)", datasetID, candidates[0])
}

func loadDatasetByID(datasetID string) (dataset, error) {
	datasetDir, err := resolveDatasetDir(datasetID)
	if err != nil {
		return dataset{}, err
	}
	queries, err := loadParquet[TextInfo](filepath.Join(datasetDir, "queries.parquet"))
	if err != nil {
		return dataset{}, fmt.Errorf("load queries.parquet: %w", err)
	}
	corpus, err := loadParquet[TextInfo](filepath.Join(datasetDir, "corpus.parquet"))
	if err != nil {
		return dataset{}, fmt.Errorf("load corpus.parquet: %w", err)
	}
	answers, err := loadParquet[TextInfo](filepath.Join(datasetDir, "answers.parquet"))
	if err != nil {
		return dataset{}, fmt.Errorf("load answers.parquet: %w", err)
	}
	qrels, err := loadParquet[RelsInfo](filepath.Join(datasetDir, "qrels.parquet"))
	if err != nil {
		return dataset{}, fmt.Errorf("load qrels.parquet: %w", err)
	}
	qas, err := loadParquet[QaInfo](filepath.Join(datasetDir, "qas.parquet"))
	if err != nil {
		return dataset{}, fmt.Errorf("load qas.parquet: %w", err)
	}

	res := dataset{
		queries: make(map[int64]string),  // qid -> question text
		corpus:  make(map[int64]string),  // pid -> passage text
		answers: make(map[int64]string),  // aid -> answer text
		qrels:   make(map[int64][]int64), // qid -> list of pid
		qas:     make(map[int64]int64),   // qid -> aid
	}
	for _, table := range []struct {
		name  string
		rows  []TextInfo
		index map[int64]string
	}{
		{"queries", queries, res.queries},
		{"corpus", corpus, res.corpus},
		{"answers", answers, res.answers},
	} {
		for _, row := range table.rows {
			if _, exists := table.index[row.ID]; exists {
				return dataset{}, fmt.Errorf("duplicate %s ID %d", table.name, row.ID)
			}
			if strings.TrimSpace(row.Text) == "" {
				return dataset{}, fmt.Errorf("empty %s text for ID %d", table.name, row.ID)
			}
			table.index[row.ID] = row.Text
		}
	}
	for _, ri := range qrels {
		if _, exists := res.queries[ri.QID]; !exists {
			return dataset{}, fmt.Errorf("qrels references unknown query %d", ri.QID)
		}
		if _, exists := res.corpus[ri.PID]; !exists {
			return dataset{}, fmt.Errorf("qrels references unknown passage %d", ri.PID)
		}
		res.qrels[ri.QID] = append(res.qrels[ri.QID], ri.PID)
	}
	for _, qi := range qas {
		if _, exists := res.queries[qi.QID]; !exists {
			return dataset{}, fmt.Errorf("qas references unknown query %d", qi.QID)
		}
		if _, exists := res.answers[qi.AID]; !exists {
			return dataset{}, fmt.Errorf("qas references unknown answer %d", qi.AID)
		}
		if previous, exists := res.qas[qi.QID]; exists && previous != qi.AID {
			return dataset{}, fmt.Errorf("multiple answers for query %d", qi.QID)
		}
		res.qas[qi.QID] = qi.AID
	}
	return res, nil
}

// dataset represents the in-memory dataset structure
type dataset struct {
	queries map[int64]string  // qid -> question text
	corpus  map[int64]string  // pid -> passage text
	answers map[int64]string  // aid -> answer text
	qrels   map[int64][]int64 // qid -> list of related pids
	qas     map[int64]int64   // qid -> aid
}

// Passages returns all corpus rows in stable ID order. IDs are not array
// offsets: negative, sparse and large IDs do not increase allocation size.
func (d *dataset) Passages() []string {
	ids := make([]int64, 0, len(d.corpus))
	for id := range d.corpus {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	passages := make([]string, 0, len(ids))
	for _, id := range ids {
		passages = append(passages, d.corpus[id])
	}
	return passages
}

// Iterate generates QA pairs from the dataset
func (d *dataset) Iterate() []*types.QAPair {
	var pairs []*types.QAPair

	for qid, question := range d.queries {
		// Get answer info
		aid, hasAnswer := d.qas[qid]
		answer := ""
		if hasAnswer {
			answer = d.answers[aid]
		}

		// Get related passages
		pids := d.qrels[qid]
		var pidStr []int
		for _, pid := range pids {
			pidStr = append(pidStr, int(pid))
		}
		var passages []string
		for _, pid := range pids {
			passages = append(passages, d.corpus[pid])
		}

		pairs = append(pairs, &types.QAPair{
			QID:      int(qid),
			Question: question,
			PIDs:     pidStr,
			Passages: passages,
			AID:      int(aid),
			Answer:   answer,
		})
	}

	return pairs
}

// GetContextForQID retrieves context passages for a given question ID
func (d *dataset) GetContextForQID(qid int64) ([]string, error) {
	pids, ok := d.qrels[qid]
	if !ok {
		return nil, errors.New("question ID not found")
	}

	var contextParts []string
	for _, pid := range pids {
		if text, exists := d.corpus[pid]; exists {
			contextParts = append(contextParts, text)
		}
	}

	return contextParts, nil
}

// PrintStats prints dataset statistics to the logger
func (d *dataset) PrintStats(ctx context.Context) {
	logger.Infof(ctx, "QA System Statistics:")
	logger.Infof(ctx, "- Total queries: %d", len(d.queries))
	logger.Infof(ctx, "- Total corpus passages: %d", len(d.corpus))
	logger.Infof(ctx, "- Total answers: %d", len(d.answers))

	// Calculate average passages per query
	totalRelations := 0
	for _, pids := range d.qrels {
		totalRelations += len(pids)
	}
	avgPassages := 0.0
	if len(d.qrels) > 0 {
		avgPassages = float64(totalRelations) / float64(len(d.qrels))
	}
	logger.Infof(ctx, "- Average passages per query: %.2f", avgPassages)

	// Calculate coverage
	coveredQueries := len(d.qas)
	coverage := 0.0
	if len(d.queries) > 0 {
		coverage = float64(coveredQueries) / float64(len(d.queries)) * 100
	}
	logger.Infof(ctx, "- Answer coverage: %.2f%% (%d/%d)", coverage, coveredQueries, len(d.queries))
}

// PrintRandomQA prints a random question with its related passages and answer
func (d *dataset) PrintRandomQA() error {
	// Get a random qid
	var qid int64
	for k := range d.qas {
		qid = k
		break
	}
	if qid == 0 {
		return errors.New("no questions available")
	}

	// Get question text
	question, ok := d.queries[qid]
	if !ok {
		return fmt.Errorf("question %d not found", qid)
	}

	// Get answer info
	aid, ok := d.qas[qid]
	if !ok {
		return fmt.Errorf("answer for question %d not found", qid)
	}
	answer, ok := d.answers[aid]
	if !ok {
		return fmt.Errorf("answer %d not found", aid)
	}

	// Print formatted QA
	fmt.Println("===== Random QA =====")
	fmt.Printf("QID: %d\n", qid)
	fmt.Printf("Question: %s\n", question)

	// Print passages if available
	if pids, exists := d.qrels[qid]; exists && len(pids) > 0 {
		fmt.Println("\nRelated passages:")
		for i, pid := range pids {
			if text, exists := d.corpus[pid]; exists {
				fmt.Printf("\nPassage %d (PID: %d):\n%s\n", i+1, pid, text)
			}
		}
	} else {
		fmt.Println("\nNo related passages found")
	}

	// Print answer
	fmt.Printf("\nAnswer (AID: %d):\n%s\n", aid, answer)

	return nil
}

// loadParquet loads data from parquet file into specified type
func loadParquet[T any](filePath string) ([]T, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	file, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return nil, err
	}
	// Parquet schema projection fills absent columns with zero values. IDs
	// legitimately include zero, so missing columns must be rejected before
	// projection can manufacture relationships to a different record.
	expectedSchema := parquet.SchemaOf(new(T))
	requiredColumns := make(map[int]string)
	for _, column := range expectedSchema.Columns() {
		actual, exists := file.Schema().Lookup(column...)
		if !exists {
			return nil, fmt.Errorf("missing column %s", strings.Join(column, "."))
		}
		expected, _ := expectedSchema.Lookup(column...)
		if actual.Node.Type().Kind() != expected.Node.Type().Kind() || actual.MaxRepetitionLevel != 0 {
			return nil, fmt.Errorf("invalid type for column %s", strings.Join(column, "."))
		}
		requiredColumns[actual.ColumnIndex] = strings.Join(column, ".")
	}
	// Optional Parquet columns (including pandas output) are supported, but
	// null IDs must not be projected to valid zero IDs either. Scan in bounded
	// batches before projecting into non-nullable Go fields.
	reader := parquet.NewReader(file)
	defer func() { _ = reader.Close() }()
	rows := make([]parquet.Row, 64)
	for {
		n, err := reader.ReadRows(rows)
		for _, row := range rows[:n] {
			for _, value := range row {
				if name, required := requiredColumns[value.Column()]; required && value.IsNull() {
					return nil, fmt.Errorf("null column %s", name)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return parquet.Read[T](file, info.Size())
}
