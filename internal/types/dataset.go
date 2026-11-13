package types

// QAPair represents a complete QA example with question, related passages and answer
type QAPair struct {
	QID      int      // Question ID
	Question string   // Question text
	PIDs     []int    // Related passage IDs
	Passages []string // Passage texts
	AID      int      // Answer ID
	Answer   string   // Answer text
}

// EvaluationDataset keeps the indexed corpus separate from each query's
// relevant passages, so unreferenced documents remain retrieval candidates.
type EvaluationDataset struct {
	QAPairs        []*QAPair
	CorpusPassages []string
}
