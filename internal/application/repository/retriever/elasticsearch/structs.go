package elasticsearch

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/Usir233/GENYEE/internal/types"
)

var urlCredentialsPattern = regexp.MustCompile(`://[^/@\s]+@`)

// VectorEmbedding defines the Elasticsearch document structure for vector embeddings
type VectorEmbedding struct {
	Content         string    `json:"content"           gorm:"column:content;not null"`     // Text content of the chunk
	SourceID        string    `json:"source_id"         gorm:"column:source_id;not null"`   // ID of the source document
	SourceType      int       `json:"source_type"       gorm:"column:source_type;not null"` // Type of the source document
	ChunkID         string    `json:"chunk_id"          gorm:"column:chunk_id"`             // Unique ID of the text chunk
	KnowledgeID     string    `json:"knowledge_id"      gorm:"column:knowledge_id"`         // ID of the knowledge item
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`    // ID of the knowledge base
	TagID           string    `json:"tag_id"            gorm:"column:tag_id"`               // Tag ID for categorization
	Embedding       []float32 `json:"embedding,omitempty" gorm:"column:embedding;not null"` // Vector embedding of the content
	IsEnabled       bool      `json:"is_enabled"`                                           // Whether the chunk is enabled
	IsRecommended   bool      `json:"is_recommended"`                                       // Whether the chunk is recommended
}

// VectorEmbeddingWithScore extends VectorEmbedding with similarity score
type VectorEmbeddingWithScore struct {
	VectorEmbedding
	Score float64 // Similarity score from vector search
}

// ToDBVectorEmbedding converts IndexInfo to Elasticsearch document format
func ToDBVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *VectorEmbedding {
	if embedding == nil {
		return &VectorEmbedding{}
	}
	vector := &VectorEmbedding{
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      int(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		IsEnabled:       embedding.IsEnabled,
		IsRecommended:   embedding.IsRecommended,
	}
	// Add embedding data if available in additionalParams
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), "embedding") {
		if embeddingMap, ok := additionalParams["embedding"].(map[string][]float32); ok {
			vector.Embedding = embeddingMap[embedding.SourceID]
		}
	}
	// Get is_enabled from additionalParams if available
	if additionalParams != nil {
		if chunkEnabledMap, ok := additionalParams["chunk_enabled"].(map[string]bool); ok {
			if enabled, exists := chunkEnabledMap[embedding.ChunkID]; exists {
				vector.IsEnabled = enabled
			}
		}
	}
	return vector
}

// FromDBVectorEmbeddingWithScore converts Elasticsearch document to IndexWithScore domain model
func FromDBVectorEmbeddingWithScore(id string,
	embedding *VectorEmbeddingWithScore,
	matchType types.MatchType,
) *types.IndexWithScore {
	if embedding == nil {
		return &types.IndexWithScore{
			ID:        id,
			MatchType: matchType,
		}
	}
	return &types.IndexWithScore{
		ID:              id,
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		Content:         embedding.Content,
		Score:           embedding.Score,
		MatchType:       matchType,
		IsEnabled:       embedding.IsEnabled,
	}
}

// ValidateEmbedding rejects NaN and +/-Inf vector elements before JSON serialization.
func ValidateEmbedding(vec []float32) error {
	for i, v := range vec {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("elasticsearch: embedding[%d] is not finite", i)
		}
	}
	return nil
}

// RequireKnowledgeBaseIDs trims and validates that at least one non-empty knowledge_base_id is present.
func RequireKnowledgeBaseIDs(kbIDs []string) ([]string, error) {
	cleaned := make([]string, 0, len(kbIDs))
	for _, id := range kbIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("elasticsearch: knowledge_base_id filter is required")
	}
	return cleaned, nil
}

// SanitizeDeleteTargetIDs preserves empty-slice no-op semantics while rejecting blank element IDs.
func SanitizeDeleteTargetIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return nil, fmt.Errorf("elasticsearch: delete target ID must not be empty")
		}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned, nil
}

// ValidateChunkKey rejects empty or whitespace-only chunk IDs in batch updates.
func ValidateChunkKey(chunkID string) error {
	if strings.TrimSpace(chunkID) == "" {
		return fmt.Errorf("elasticsearch: chunk ID must not be empty")
	}
	return nil
}

// EffectiveTopK normalizes TopK into [1, 10000] with default 10 when unset or non-positive.
func EffectiveTopK(topK int) int {
	if topK <= 0 {
		return 10
	}
	if topK > 10000 {
		return 10000
	}
	return topK
}

// RedactURLCredentials strips embedded userinfo from URLs inside error strings.
func RedactURLCredentials(msg string) string {
	return urlCredentialsPattern.ReplaceAllString(msg, "://[REDACTED]@")
}
