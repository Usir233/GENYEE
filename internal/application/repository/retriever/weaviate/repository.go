package weaviate

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/fault"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
)

const (
	envWeaviateCollection = "WEAVIATE_COLLECTION"
	defaultCollectionName = "Genyee_embeddings"
	fieldContent          = "content"
	fieldSourceID         = "source_id"
	fieldSourceType       = "source_type"
	fieldChunkID          = "chunk_id"
	fieldKnowledgeID      = "knowledge_id"
	fieldKnowledgeBaseID  = "knowledge_base_id"
	fieldTagID            = "tag_id"
	fieldEmbedding        = "embedding"
	fieldIsEnabled        = "is_enabled"
	fieldID               = "id"

	// defaultCopyQueryLimit is the row limit CopyIndices reads source vectors
	// with. Weaviate caps a single query at QUERY_MAXIMUM_RESULTS, 10000 by
	// default in v1.37.3 (weaviate/usecases/config.DefaultQueryMaximumResults),
	// and refuses anything above it with "query maximum results exceeded".
	// Without an explicit limit a GraphQL Get would instead fall back to
	// QUERY_DEFAULTS_LIMIT_GRAPHQL (100) and truncate silently. The setting is not
	// exposed to clients - /v1/meta reports only version, hostname and modules
	// - so ask for the default and, if the server refuses, take the value it
	// names in the refusal.
	defaultCopyQueryLimit = 10000
)

var weaviateClassNamePattern = regexp.MustCompile(`^[A-Z][_0-9A-Za-z]*$`)

func isValidWeaviateClassName(name string) bool {
	return weaviateClassNamePattern.MatchString(name)
}

func isPositiveDimensionSuffix(suffix string) bool {
	if suffix == "" || suffix[0] < '1' || suffix[0] > '9' {
		return false
	}
	for i := 1; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	dim, err := strconv.Atoi(suffix)
	return err == nil && dim > 0
}

func sanitizeWeaviateCollectionBase(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultCollectionName
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
			b.WriteByte(ch)
		} else {
			b.WriteByte('_')
		}
	}
	cleaned := strings.Trim(b.String(), "_")
	if cleaned == "" {
		return defaultCollectionName
	}
	if cleaned[0] >= 'a' && cleaned[0] <= 'z' {
		cleaned = string(cleaned[0]-('a'-'A')) + cleaned[1:]
	} else if cleaned[0] < 'A' || cleaned[0] > 'Z' {
		cleaned = "G_" + cleaned
	}
	if !isValidWeaviateClassName(cleaned) {
		return defaultCollectionName
	}
	return cleaned
}

func cleanNonEmptyIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func hasValidRetrieveScope(params types.RetrieveParams) bool {
	kbIDs := cleanNonEmptyIDs(params.KnowledgeBaseIDs)
	kIDs := cleanNonEmptyIDs(params.KnowledgeIDs)
	tagIDs := cleanNonEmptyIDs(params.TagIDs)
	if len(params.KnowledgeBaseIDs) > 0 && len(kbIDs) == 0 {
		return false
	}
	if len(params.KnowledgeIDs) > 0 && len(kIDs) == 0 {
		return false
	}
	if len(params.TagIDs) > 0 && len(tagIDs) == 0 {
		return false
	}
	return len(kbIDs) > 0 || len(kIDs) > 0
}

func weaviateObjectID(sourceID, chunkID string) string {
	return indexObjectID(sourceID, chunkID)
}

func checkBatchResponseErrors(resp []models.ObjectsGetResponse) error {
	for _, r := range resp {
		if r.Result == nil || r.Result.Errors == nil {
			continue
		}
		for _, itemErr := range r.Result.Errors.Error {
			if itemErr != nil && itemErr.Message != "" {
				return fmt.Errorf("weaviate object batch error: %s", itemErr.Message)
			}
		}
		return fmt.Errorf("weaviate object batch failed")
	}
	return nil
}

func filterWeaviateGeneratedQuestions(results []*types.IndexWithScore, excludedKBIDs []string) []*types.IndexWithScore {
	cleaned := cleanNonEmptyIDs(excludedKBIDs)
	if len(results) == 0 || len(cleaned) == 0 {
		return results
	}
	excluded := make(map[string]struct{}, len(cleaned))
	for _, id := range cleaned {
		excluded[id] = struct{}{}
	}
	filtered := make([]*types.IndexWithScore, 0, len(results))
	for _, hit := range results {
		if hit == nil {
			continue
		}
		if _, blocked := excluded[hit.KnowledgeBaseID]; blocked && types.IsGeneratedQuestionSource(
			hit.ChunkID,
			hit.SourceID,
			hit.SourceType,
		) {
			continue
		}
		filtered = append(filtered, hit)
	}
	return filtered
}

// NewWeaviateRetrieveEngineRepository creates and initializes a new Weaviate repository.
// indexCfg is optional — pass nil to use env var / default values (env path).
func NewWeaviateRetrieveEngineRepository(
	client *weaviate.Client,
	indexCfg *types.IndexConfig,
) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Weaviate] Initializing Weaviate retriever engine repository")

	collectionBaseName := sanitizeWeaviateCollectionBase(
		types.ResolveCollectionName(indexCfg, envWeaviateCollection, defaultCollectionName),
	)

	res := &weaviateRepository{
		client:             client,
		collectionBaseName: collectionBaseName,
		replicationFactor:  indexCfg.GetReplicationFactor(0),
		desiredShardCount:  indexCfg.GetDesiredShardCount(0),
	}

	log.Info("[Weaviate] Successfully initialized repository")
	return res
}

func (w *weaviateRepository) normalizedBaseName() string {
	if isValidWeaviateClassName(w.collectionBaseName) {
		return w.collectionBaseName
	}
	return sanitizeWeaviateCollectionBase(w.collectionBaseName)
}

func (w *weaviateRepository) getCollectionName(dimension int) string {
	return fmt.Sprintf("%s_%d", w.normalizedBaseName(), dimension)
}

func (w *weaviateRepository) matchesCollection(collectionName string) bool {
	if !isValidWeaviateClassName(collectionName) {
		return false
	}
	prefix := w.normalizedBaseName() + "_"
	if !strings.HasPrefix(collectionName, prefix) {
		return false
	}
	return isPositiveDimensionSuffix(strings.TrimPrefix(collectionName, prefix))
}

func (w *weaviateRepository) ensureCollection(ctx context.Context, dimension int) error {
	collectionName := w.getCollectionName(dimension)

	// Check cache first
	if _, ok := w.initializedCollections.Load(dimension); ok {
		return nil
	}

	log := logger.GetLogger(ctx)

	// Check if collection exists
	exists, err := w.client.Schema().ClassExistenceChecker().WithClassName(collectionName).Do(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to check collection existence: %v", err)
		return fmt.Errorf("failed to check collection existence: %w", err)
	}
	enabled := true
	if !exists {
		log.Infof("[Weaviate] Creating collection %s with dimension %d", collectionName, dimension)

		// 定义class结构
		classObj := models.Class{
			Class:       collectionName,
			Description: fmt.Sprintf("GENYEE embeddings collection with dimension %d", dimension),
			VectorConfig: map[string]models.VectorConfig{
				fieldEmbedding: {
					VectorIndexType: "hnsw",
					VectorIndexConfig: map[string]interface{}{
						"distance":       "cosine",
						"efConstruction": 128,
						"maxConnections": 32,
						"ef":             64,
					},
					Vectorizer: map[string]interface{}{
						"none": map[string]interface{}{},
					},
				},
			},
			Properties: []*models.Property{
				{
					Name:         fieldContent,
					DataType:     []string{"text"},
					Tokenization: "gse",
				},
				{
					Name:     fieldSourceID,
					DataType: []string{"text"},
				},
				{
					Name:     fieldSourceType,
					DataType: []string{"int"},
				},
				{
					Name:            fieldChunkID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldKnowledgeID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldKnowledgeBaseID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldTagID,
					DataType:        []string{"text"},
					IndexFilterable: &enabled,
				},
				{
					Name:            fieldIsEnabled,
					DataType:        []string{"boolean"},
					IndexFilterable: &enabled,
				},
			},
		}
		// Set replication factor if explicitly configured (> 0)
		if w.replicationFactor > 0 {
			classObj.ReplicationConfig = &models.ReplicationConfig{
				Factor: int64(w.replicationFactor),
			}
		}
		// Set shard count if explicitly configured (> 0)
		if w.desiredShardCount > 0 {
			classObj.ShardingConfig = map[string]interface{}{
				"desiredCount": w.desiredShardCount,
			}
		}
		// 创建collection
		if err = w.client.Schema().ClassCreator().WithClass(&classObj).Do(ctx); err != nil {
			// The first batches written at a new dimension are saved by several
			// workers at once, so another one (or another replica) may have
			// created the class since our check. Weaviate refuses the losing
			// create with 422, worded differently depending on where it lost,
			// so look again rather than parse the message. The class is
			// created whole, so if it exists there is nothing left to do.
			if isUnprocessableEntity(err) {
				exists, _ = w.client.Schema().ClassExistenceChecker().WithClassName(collectionName).Do(ctx)
			}
			if !exists {
				log.Errorf("[Weaviate] Failed to create collection: %v", err)
				return fmt.Errorf("failed to create collection: %w", err)
			}
			log.Infof("[Weaviate] Collection %s was created concurrently", collectionName)
		} else {
			log.Infof("[Weaviate] Successfully created collection %s", collectionName)
		}
	}
	w.initializedCollections.Store(dimension, true)
	return nil
}

// isUnprocessableEntity reports whether err is Weaviate answering 422, which is
// how it refuses a schema change it cannot apply.
func isUnprocessableEntity(err error) bool {
	var clientErr *fault.WeaviateClientError
	return errors.As(err, &clientErr) && clientErr.StatusCode == http.StatusUnprocessableEntity
}

func (w *weaviateRepository) EngineType() types.RetrieverEngineType {
	return types.WeaviateRetrieverEngineType
}

func (w *weaviateRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
func (w *weaviateRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var totalStorageSize int64
	for _, embedding := range indexInfoList {
		embeddingDB := toWeaviateVectorEmbedding(embedding, params)
		totalStorageSize += w.calculateStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Weaviate] Storage size for %d indices: %d bytes", len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single point in Weaviate
func (w *weaviateRepository) Save(ctx context.Context,
	embedding *types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if embedding == nil {
		return fmt.Errorf("weaviate: nil index info")
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}
	log.Debugf("[Weaviate] Saving index for chunk ID: %s", embedding.ChunkID)

	embeddingDB := toWeaviateVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[Weaviate] %v", err)
		return err
	}

	dimension := len(embeddingDB.Embedding)
	if err := w.ensureCollection(ctx, dimension); err != nil {
		return err
	}
	collectionName := w.getCollectionName(dimension)
	dataSchema := createPayload(embeddingDB)

	id := indexObjectID(embeddingDB.SourceID, embeddingDB.ChunkID)
	// Create point in Weaviate
	_, err := w.client.Data().Creator().
		WithClassName(collectionName).
		WithID(id).
		WithProperties(dataSchema).
		WithVector(embeddingDB.Embedding).
		Do(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to save index: %v", err)
		return err
	}
	log.Infof("[Weaviate] Successfully saved index for chunk ID: %s", embedding.ChunkID)
	return nil
}

// BatchSave stores multiple points in Weaviate using batch insert
func (w *weaviateRepository) BatchSave(ctx context.Context,
	embeddingList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Weaviate] Empty list provided to BatchSave, skipping")
		return nil
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}

	log.Infof("[Weaviate] Batch saving %d indices", len(embeddingList))

	// Group points by dimension
	embeddingsByDimension := make(map[int][]*types.IndexInfo)

	for _, embedding := range embeddingList {
		if embedding == nil {
			continue
		}
		embeddingDB := toWeaviateVectorEmbedding(embedding, additionalParams)
		if len(embeddingDB.Embedding) == 0 {
			log.Warnf("[Weaviate] Skipping empty embedding for chunk ID: %s", embedding.ChunkID)
			continue
		}

		dimension := len(embeddingDB.Embedding)
		embeddingsByDimension[dimension] = append(embeddingsByDimension[dimension], embedding)
		log.Debugf("[Weaviate] Added chunk ID %s to batch request (dimension: %d)", embedding.ChunkID, dimension)
	}

	if len(embeddingsByDimension) == 0 {
		log.Warn("[Weaviate] No valid points to save after filtering")
		return nil
	}

	// Save points to each dimension-specific collection
	totalSaved := 0
	for dimension, embeddings := range embeddingsByDimension {
		batcher := w.client.Batch().ObjectsBatcher()
		if err := w.ensureCollection(ctx, dimension); err != nil {
			return err
		}
		collectionName := w.getCollectionName(dimension)
		for _, embedding := range embeddings {
			embeddingDB := toWeaviateVectorEmbedding(embedding, additionalParams)
			dataSchema := createPayload(embeddingDB)

			obj := &models.Object{
				Class:      collectionName,
				ID:         strfmt.UUID(indexObjectID(embeddingDB.SourceID, embeddingDB.ChunkID)),
				Properties: dataSchema,
				Vector:     embeddingDB.Embedding,
			}
			batcher.WithObjects(obj)
		}
		// Flush batch
		resp, err := batcher.Do(ctx)
		if err != nil {
			log.Errorf("[Weaviate] Failed to execute batch operation for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, err)
		}
		if err := checkBatchResponseErrors(resp); err != nil {
			log.Errorf("[Weaviate] Batch save object error for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, err)
		}
		totalSaved += len(embeddings)
		log.Infof("[Weaviate] Saved %d points to collection %s", len(embeddings), collectionName)
	}

	log.Infof("[Weaviate] Successfully batch saved %d indices", totalSaved)
	return nil
}

// DeleteByChunkIDList removes points from the collection based on chunk IDs
func (w *weaviateRepository) DeleteByChunkIDList(
	ctx context.Context, chunkIDList []string, dimension int, _ string,
) error {
	log := logger.GetLogger(ctx)
	cleaned := cleanNonEmptyIDs(chunkIDList)
	if len(cleaned) == 0 {
		log.Warn("[Weaviate] Empty chunk ID list provided for deletion, skipping")
		return nil
	}
	if dimension <= 0 {
		return fmt.Errorf("weaviate: invalid dimension %d for chunk deletion", dimension)
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}

	collectionName := w.getCollectionName(dimension)
	log.Infof("[Weaviate] Deleting indices by chunk IDs from %s, count: %d", collectionName, len(cleaned))

	filter := w.client.Batch().ObjectsBatchDeleter().
		WithClassName(collectionName).
		WithWhere(filters.Where().
			WithPath([]string{fieldChunkID}).
			WithOperator(filters.ContainsAny).
			WithValueText(cleaned...)).
		WithOutput("minimal")

	if _, err := filter.Do(ctx); err != nil {
		log.Errorf("[Weaviate] Failed to delete by chunk IDs: %v", err)
		return fmt.Errorf("failed to delete by chunk IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by chunk IDs")
	return nil
}

// DeleteByKnowledgeIDList removes points from the collection based on knowledge IDs
func (w *weaviateRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, _ string,
) error {
	log := logger.GetLogger(ctx)
	cleaned := cleanNonEmptyIDs(knowledgeIDList)
	if len(cleaned) == 0 {
		log.Warn("[Weaviate] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}
	if dimension <= 0 {
		return fmt.Errorf("weaviate: invalid dimension %d for knowledge deletion", dimension)
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}

	collectionName := w.getCollectionName(dimension)
	log.Infof("[Weaviate] Deleting indices by knowledge IDs from %s, count: %d", collectionName, len(cleaned))

	filter := w.client.Batch().ObjectsBatchDeleter().
		WithClassName(collectionName).
		WithWhere(filters.Where().
			WithPath([]string{fieldKnowledgeID}).
			WithOperator(filters.ContainsAny).
			WithValueText(cleaned...)).
		WithOutput("minimal")

	if _, err := filter.Do(ctx); err != nil {
		log.Errorf("[Weaviate] Failed to delete by knowledge IDs: %v", err)
		return fmt.Errorf("failed to delete by knowledge IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by knowledge IDs")
	return nil
}

// DeleteBySourceIDList removes points from the collection based on source IDs
func (w *weaviateRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, _ string,
) error {
	log := logger.GetLogger(ctx)
	cleaned := cleanNonEmptyIDs(sourceIDList)
	if len(cleaned) == 0 {
		log.Warn("[Weaviate] Empty Source ID list provided for deletion, skipping")
		return nil
	}
	if dimension <= 0 {
		return fmt.Errorf("weaviate: invalid dimension %d for source deletion", dimension)
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}
	collectionName := w.getCollectionName(dimension)
	log.Infof("[Weaviate] Deleting indices by source IDs from %s, count: %d", collectionName, len(cleaned))

	filter := w.client.Batch().ObjectsBatchDeleter().
		WithClassName(collectionName).
		WithWhere(filters.Where().
			WithPath([]string{fieldSourceID}).
			WithOperator(filters.ContainsAny).
			WithValueText(cleaned...)).
		WithOutput("minimal")

	if _, err := filter.Do(ctx); err != nil {
		log.Errorf("[Weaviate] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by source IDs: %w", err)
	}
	log.Infof("[Weaviate] Successfully deleted documents by source IDs")
	return nil
}

// BatchUpdateChunkEnabledStatus updates every retrieval source belonging to the chunks.
func (w *weaviateRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	updates := make(map[string]map[string]interface{}, len(chunkStatusMap))
	for chunkID, enabled := range chunkStatusMap {
		updates[chunkID] = map[string]interface{}{fieldIsEnabled: enabled}
	}
	return w.updateChunkProperties(ctx, updates)
}

// BatchUpdateChunkTagID updates every retrieval source belonging to the chunks.
func (w *weaviateRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	updates := make(map[string]map[string]interface{}, len(chunkTagMap))
	for chunkID, tagID := range chunkTagMap {
		updates[chunkID] = map[string]interface{}{fieldTagID: tagID}
	}
	return w.updateChunkProperties(ctx, updates)
}

// BatchUpdateChunkContent updates the indexed content of primary chunk rows in batch
func (w *weaviateRepository) BatchUpdateChunkContent(ctx context.Context, chunkContentMap map[string]string) error {
	log := logger.GetLogger(ctx)
	if len(chunkContentMap) == 0 {
		log.Warn("[Weaviate] Empty chunk content map provided, skipping")
		return nil
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}

	log.Infof("[Weaviate] Batch updating chunk content, count: %d", len(chunkContentMap))

	collections, err := w.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to list collections: %v", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	sourceIDs := cleanNonEmptyIDs(slices.Sorted(maps.Keys(chunkContentMap)))
	if len(sourceIDs) == 0 {
		return nil
	}
	const batchSize = 64
	for _, collectionName := range collections {
		if !w.matchesCollection(collectionName) {
			continue
		}
		for batch := range slices.Chunk(sourceIDs, batchSize) {
			where := filters.Where().
				WithPath([]string{fieldSourceID}).
				WithOperator(filters.ContainsAny).
				WithValueText(batch...)
			result, err := w.client.GraphQL().Get().
				WithClassName(collectionName).
				WithWhere(where).
				WithLimit(len(batch)*2).
				WithFields(
					graphql.Field{Name: fieldSourceID},
					graphql.Field{Name: fieldChunkID},
					graphql.Field{Name: "_additional", Fields: []graphql.Field{{Name: "id"}}},
				).
				Do(ctx)
			if err != nil {
				return fmt.Errorf("query chunk content targets in %s: %w", collectionName, err)
			}
			if result == nil || len(result.Errors) > 0 {
				return fmt.Errorf("graphql query chunk content targets in %s failed", collectionName)
			}
			if result.Data == nil {
				continue
			}
			get, ok := result.Data["Get"].(map[string]interface{})
			if !ok || get[collectionName] == nil {
				continue
			}
			rows, ok := get[collectionName].([]interface{})
			if !ok {
				continue
			}
			for _, raw := range rows {
				data, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				srcID, _ := data[fieldSourceID].(string)
				chkID, _ := data[fieldChunkID].(string)
				if srcID == "" || srcID != chkID {
					continue
				}
				content, ok := chunkContentMap[srcID]
				if !ok {
					continue
				}
				additional, ok := data["_additional"].(map[string]interface{})
				if !ok {
					continue
				}
				objectID, _ := additional["id"].(string)
				if objectID == "" {
					continue
				}
				if err := w.client.Data().Updater().
					WithClassName(collectionName).
					WithID(objectID).
					WithMerge().
					WithProperties(map[string]interface{}{
						fieldContent: content,
					}).
					Do(ctx); err != nil {
					return fmt.Errorf("update chunk %s content in %s: %w", srcID, collectionName, err)
				}
			}
		}
	}

	log.Infof("[Weaviate] Batch update chunk content completed")
	return nil
}

func (w *weaviateRepository) updateChunkProperties(
	ctx context.Context, updates map[string]map[string]interface{},
) error {
	if len(updates) == 0 {
		return nil
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}
	chunkIDs := cleanNonEmptyIDs(slices.Sorted(maps.Keys(updates)))
	if len(chunkIDs) == 0 {
		return nil
	}
	collections, err := w.ListCollections(ctx)
	if err != nil {
		return err
	}
	for _, collection := range collections {
		if !w.matchesCollection(collection) {
			continue
		}
		for _, chunkID := range chunkIDs {
			properties := updates[chunkID]
			ids, err := w.chunkObjectIDs(ctx, collection, chunkID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := w.client.Data().Updater().WithClassName(collection).WithID(id).WithMerge().
					WithProperties(properties).Do(ctx); err != nil {
					return fmt.Errorf("update chunk %s object %s: %w", chunkID, id, err)
				}
			}
		}
	}
	return nil
}

// Object IDs identify individual retrieval sources, whereas chunk_id groups a
// body and its generated questions. Resolve that group before changing metadata.
func (w *weaviateRepository) chunkObjectIDs(ctx context.Context, collection, chunkID string) ([]string, error) {
	limit := defaultCopyQueryLimit
	for {
		result, err := w.client.GraphQL().Get().WithClassName(collection).
			WithWhere(filters.Where().WithPath([]string{fieldChunkID}).
				WithOperator(filters.Equal).WithValueString(chunkID)).
			WithFields(
				graphql.Field{Name: fieldChunkID},
				graphql.Field{Name: "_additional", Fields: []graphql.Field{{Name: "id"}}},
			).
			WithLimit(limit).Do(ctx)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, fmt.Errorf("invalid chunk object response")
		}
		if len(result.Errors) > 0 {
			message := "graphql error"
			if result.Errors[0] != nil && result.Errors[0].Message != "" {
				message = result.Errors[0].Message
			}
			if serverLimit, ok := queryMaximumResults(message); ok && serverLimit < limit {
				limit = serverLimit
				continue
			}
			return nil, fmt.Errorf("list chunk %s objects: %s", chunkID, message)
		}
		get, ok := result.Data["Get"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid chunk object response")
		}
		rows, ok := get[collection].([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid chunk object collection %s", collection)
		}
		if len(rows) >= limit {
			return nil, fmt.Errorf("chunk %s reaches query limit %d; metadata update stopped", chunkID, limit)
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			object, _ := row.(map[string]interface{})
			if rowChunkID, ok := object[fieldChunkID].(string); ok && rowChunkID != "" && rowChunkID != chunkID {
				continue
			}
			additional, _ := object["_additional"].(map[string]interface{})
			id, _ := additional["id"].(string)
			if id == "" {
				return nil, fmt.Errorf("chunk %s object has no id", chunkID)
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
}

func (w *weaviateRepository) getBaseFilter(params types.RetrieveParams) *filters.WhereBuilder {
	if !hasValidRetrieveScope(params) {
		return filters.Where().
			WithOperator(filters.And).
			WithOperands([]*filters.WhereBuilder{
				filters.Where().
					WithPath([]string{fieldIsEnabled}).
					WithOperator(filters.Equal).
					WithValueBoolean(false),
			})
	}

	var operands []*filters.WhereBuilder
	operands = append(operands, filters.Where().
		WithPath([]string{fieldIsEnabled}).
		WithOperator(filters.Equal).
		WithValueBoolean(true))

	if kbIDs := cleanNonEmptyIDs(params.KnowledgeBaseIDs); len(kbIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldKnowledgeBaseID}).
			WithOperator(filters.ContainsAny).
			WithValueText(kbIDs...))
	}
	if kIDs := cleanNonEmptyIDs(params.KnowledgeIDs); len(kIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldKnowledgeID}).
			WithOperator(filters.ContainsAny).
			WithValueText(kIDs...))
	}
	if tagIDs := cleanNonEmptyIDs(params.TagIDs); len(tagIDs) > 0 {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldTagID}).
			WithOperator(filters.ContainsAny).
			WithValueText(tagIDs...))
	}
	for _, excludedDocID := range cleanNonEmptyIDs(params.ExcludeKnowledgeIDs) {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldKnowledgeID}).
			WithOperator(filters.NotEqual).
			WithValueText(excludedDocID))
	}
	for _, excludedChunkID := range cleanNonEmptyIDs(params.ExcludeChunkIDs) {
		operands = append(operands, filters.Where().
			WithPath([]string{fieldChunkID}).
			WithOperator(filters.NotEqual).
			WithValueText(excludedChunkID))
	}

	return filters.Where().
		WithOperator(filters.And).
		WithOperands(operands)
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
func (w *weaviateRepository) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Weaviate] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return w.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return w.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Weaviate] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search
func (w *weaviateRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	if !hasValidRetrieveScope(params) {
		log.Warn("[Weaviate] Empty or invalid retrieval scope for vector search, returning empty results")
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	dimension := len(params.Embedding)
	if dimension <= 0 {
		log.Warn("[Weaviate] Empty embedding vector provided to VectorRetrieve, returning empty results")
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	if w.client == nil {
		return nil, fmt.Errorf("weaviate: client is nil")
	}
	log.Infof("[Weaviate] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		dimension, params.TopK, params.Threshold)

	collectionName := w.getCollectionName(dimension)

	hasCollection, err := w.client.Schema().ClassExistenceChecker().WithClassName(collectionName).Do(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to check collection existence: %v", err)
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !hasCollection {
		log.Warnf("[Weaviate] Collection %s does not exist, returning empty results", collectionName)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	where := w.getBaseFilter(params)
	topK := params.TopK
	if topK <= 0 {
		topK = 10
	}
	limit := topK
	if len(cleanNonEmptyIDs(params.ExcludeGeneratedQuestionKBIDs)) > 0 {
		limit = topK * 4
		if limit < 32 {
			limit = 32
		}
	}
	// Weaviate's certainty is (1 + cos) / 2; callers pass a cosine
	// similarity threshold, the scale every other engine uses. A zero
	// threshold means no filtering, so it stays 0 rather than becoming 0.5.
	var scoreThreshold float32
	if params.Threshold > 0 {
		scoreThreshold = float32((1 + params.Threshold) / 2)
	}
	fields := getEmbeddingFields()
	result, err := w.client.GraphQL().Get().WithClassName(collectionName).
		WithWhere(where).
		WithLimit(limit).
		WithFields(fields...).
		WithNearVector(w.client.GraphQL().NearVectorArgBuilder().
			WithVector(params.Embedding).
			WithCertainty(scoreThreshold)).
		Do(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Vector search failed: %v", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("graphql search failed: nil response")
	}
	if len(result.Errors) > 0 {
		msg := "graphql error"
		if result.Errors[0] != nil && result.Errors[0].Message != "" {
			msg = result.Errors[0].Message
		}
		log.Errorf("[Weaviate] Vector search failed: %v", result.Errors)
		return nil, fmt.Errorf("graphql search failed: %s", msg)
	}
	if result.Data == nil {
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}

	data, ok := result.Data["Get"].(map[string]interface{})
	if !ok || data[collectionName] == nil {
		log.Warnf("[Weaviate] No vector matches found that meet threshold %.4f", params.Threshold)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	items, ok := data[collectionName].([]interface{})
	if !ok {
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	results := parseGraphQLResponse(items, collectionName, types.MatchTypeEmbedding)
	results = filterWeaviateGeneratedQuestions(results, params.ExcludeGeneratedQuestionKBIDs)
	if len(results) > topK {
		results = results[:topK]
	}

	if len(results) == 0 {
		log.Warnf("[Weaviate] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Weaviate] Vector retrieval found %d results", len(results))
		log.Debugf("[Weaviate] Top result score: %.4f", results[0].Score)
	}

	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// KeywordsRetrieve performs keyword-based search in document content
// This searches across all collections since keyword search doesn't depend on dimension
func (w *weaviateRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	if !hasValidRetrieveScope(params) {
		log.Warn("[Weaviate] Empty or invalid retrieval scope for keywords search, returning empty results")
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	if strings.TrimSpace(params.Query) == "" {
		log.Warn("[Weaviate] Empty query provided to KeywordsRetrieve, returning empty results")
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	if w.client == nil {
		return nil, fmt.Errorf("weaviate: client is nil")
	}
	log.Infof("[Weaviate] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	collections, err := w.ListCollections(ctx)
	if err != nil {
		log.Errorf("[Weaviate] Failed to list collections: %v", err)
		return nil, fmt.Errorf("failed to list collections: %w", err)
	}

	topK := params.TopK
	if topK <= 0 {
		topK = 10
	}
	queryLimit := topK
	if len(cleanNonEmptyIDs(params.ExcludeGeneratedQuestionKBIDs)) > 0 {
		queryLimit = topK * 4
		if queryLimit < 32 {
			queryLimit = 32
		}
	}

	var allResults []*types.IndexWithScore
	filter := w.getBaseFilter(params)
	fields := getKeywordsFields()

	for _, collectionName := range collections {
		log.Debugf("[Weaviate] Checking collection: %s", collectionName)
		if !w.matchesCollection(collectionName) {
			log.Debugf(
				"[Weaviate] Skipping collection %s (doesn't match base name %s)",
				collectionName,
				w.collectionBaseName,
			)
			continue
		}

		bm25 := w.client.GraphQL().Bm25ArgBuilder().
			WithQuery(params.Query).
			WithProperties([]string{fieldContent}...)

		result, err := w.client.GraphQL().Get().WithClassName(collectionName).
			WithWhere(filter).
			WithLimit(queryLimit).
			WithFields(fields...).
			WithBM25(bm25).
			Do(ctx)
		if err != nil {
			log.Errorf("[Weaviate] keywords search failed: %v", err)
			return nil, fmt.Errorf("failed to search: %w", err)
		}
		if result == nil {
			return nil, fmt.Errorf("graphql search failed: nil response")
		}
		if len(result.Errors) > 0 {
			msg := "graphql error"
			if result.Errors[0] != nil && result.Errors[0].Message != "" {
				msg = result.Errors[0].Message
			}
			log.Errorf("[Weaviate] keywords search failed: %v", result.Errors)
			return nil, fmt.Errorf("graphql search failed: %s", msg)
		}
		if result.Data == nil {
			continue
		}
		data, ok := result.Data["Get"].(map[string]interface{})
		if !ok || data[collectionName] == nil {
			log.Warnf("[Weaviate] No keywords matches found in collection %s", collectionName)
			continue
		}
		items, ok := data[collectionName].([]interface{})
		if !ok {
			continue
		}
		results := parseGraphQLResponse(items, collectionName, types.MatchTypeKeywords)
		results = filterWeaviateGeneratedQuestions(results, params.ExcludeGeneratedQuestionKBIDs)
		allResults = append(allResults, results...)
	}

	slices.SortStableFunc(allResults, func(a, b *types.IndexWithScore) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return 0
		}
	})
	if len(allResults) > topK {
		allResults = allResults[:topK]
	}

	if len(allResults) == 0 {
		log.Warnf("[Weaviate] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Weaviate] Keywords retrieval found %d results", len(allResults))
	}
	return buildRetrieveResult(allResults, types.KeywordsRetrieverType), nil
}

// CopyIndices copies index data from source knowledge base to target knowledge base
func (w *weaviateRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	_ string,
) error {
	log := logger.GetLogger(ctx)
	sourceKB := strings.TrimSpace(sourceKnowledgeBaseID)
	targetKB := strings.TrimSpace(targetKnowledgeBaseID)
	if sourceKB == "" || targetKB == "" {
		return fmt.Errorf("weaviate: copy indices requires non-empty source and target knowledge_base_id")
	}
	if dimension <= 0 {
		return fmt.Errorf("weaviate: invalid dimension %d for copy indices", dimension)
	}
	log.Infof("[Weaviate] Copying indices from %s to %s, count: %d",
		sourceKB, targetKB, len(sourceToTargetChunkIDMap))

	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}
	if w.client == nil {
		return fmt.Errorf("weaviate: client is nil")
	}

	collectionName := w.getCollectionName(dimension)
	reader := newCopySourceReader(w, collectionName, getVectorFields())
	totalCopied := 0

	sourceChunkIDs := cleanNonEmptyIDs(slices.Sorted(maps.Keys(sourceToTargetChunkIDMap)))
	const chunkBatchSize = 64
	for chunkBatch := range slices.Chunk(sourceChunkIDs, chunkBatchSize) {
		objects, err := reader.readBatch(ctx, sourceKB, chunkBatch)
		if err != nil {
			return err
		}
		if len(objects) == 0 {
			continue
		}
		log.Infof("[Weaviate] Found %d source points in batch", len(objects))

		targetObjects := make([]*models.Object, 0, len(objects))
		for _, obj := range objects {
			data, ok := obj.(map[string]interface{})
			if !ok {
				continue
			}
			additional, ok := data["_additional"].(map[string]interface{})
			if !ok {
				continue
			}

			if id, ok := additional["id"].(string); !ok || id == "" {
				return fmt.Errorf("weaviate: copy indices source object has no id")
			}

			sourceChunkID, ok := data[fieldChunkID].(string)
			if !ok {
				continue
			}
			sourceKnowledgeID, ok := data[fieldKnowledgeID].(string)
			if !ok {
				continue
			}
			originalSourceID, ok := data[fieldSourceID].(string)
			if !ok {
				continue
			}

			targetChunkID, ok1 := sourceToTargetChunkIDMap[sourceChunkID]
			targetKnowledgeID, ok2 := sourceToTargetKBIDMap[sourceKnowledgeID]
			if !ok1 || !ok2 || strings.TrimSpace(targetChunkID) == "" || strings.TrimSpace(targetKnowledgeID) == "" {
				continue
			}

			var targetSourceID string
			switch {
			case originalSourceID == sourceChunkID:
				targetSourceID = targetChunkID
			case strings.HasPrefix(originalSourceID, sourceChunkID+"-"):
				questionID := strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
				targetSourceID = fmt.Sprintf("%s-%s", targetChunkID, questionID)
			default:
				targetSourceID = uuid.New().String()
			}

			vectorRaw, ok := additional["vector"].([]interface{})
			if !ok {
				continue
			}
			vector := make([]float32, len(vectorRaw))
			validVector := true
			for i, v := range vectorRaw {
				f, ok := v.(float64)
				if !ok {
					validVector = false
					break
				}
				vector[i] = float32(f)
			}
			if !validVector {
				continue
			}

			isEnabled := true
			newObj := &models.Object{
				Class: collectionName,
				ID:    strfmt.UUID(weaviateObjectID(targetSourceID, targetChunkID)),
				Properties: map[string]interface{}{
					fieldContent:         data[fieldContent],
					fieldSourceID:        targetSourceID,
					fieldSourceType:      data[fieldSourceType],
					fieldChunkID:         targetChunkID,
					fieldKnowledgeID:     targetKnowledgeID,
					fieldKnowledgeBaseID: targetKB,
					fieldTagID:           data[fieldTagID],
					fieldIsEnabled:       isEnabled,
				},
				Vector: vector,
			}
			targetObjects = append(targetObjects, newObj)
		}

		if len(targetObjects) > 0 {
			resp, err := w.client.Batch().ObjectsBatcher().WithObjects(targetObjects...).Do(ctx)
			if err != nil {
				return fmt.Errorf("batch upsert failed: %w", err)
			}
			if err := checkBatchResponseErrors(resp); err != nil {
				log.Errorf("[Weaviate] Copy batch object error: %v", err)
				return fmt.Errorf("batch upsert failed: %w", err)
			}
			totalCopied += len(targetObjects)
			log.Infof("[Weaviate] Successfully copied batch, total: %d", totalCopied)
		}
	}
	log.Infof("[Weaviate] Index copy completed, total copied: %d", totalCopied)
	return nil
}

// copySourceReader reads the source vectors a copy needs, one batch of source
// chunk IDs at a time.
//
// A chunk is not one vector: it is stored as a row for its own content plus one
// row per generated question, all sharing the same chunk_id. A batch of n chunk
// IDs therefore says nothing about how many rows come back, and the only reads
// that can be trusted as complete are the ones that returned fewer rows than
// the limit they asked for. A batch that comes back full is split and re-read;
// a single chunk that still fills the limit cannot be read completely, and the
// copy reports that instead of returning success on a partial index.
type copySourceReader struct {
	repo           *weaviateRepository
	collectionName string
	fields         []graphql.Field
	// limit is the row limit every query asks for. It starts at the server's
	// documented default and is lowered to the value the server names if it
	// refuses a query for exceeding QUERY_MAXIMUM_RESULTS.
	limit int
}

func newCopySourceReader(repo *weaviateRepository, collectionName string, fields []graphql.Field) *copySourceReader {
	limit := repo.copyQueryLimit
	if limit <= 0 {
		limit = defaultCopyQueryLimit
	}
	return &copySourceReader{
		repo:           repo,
		collectionName: collectionName,
		fields:         fields,
		limit:          limit,
	}
}

// readBatch returns every source vector whose chunk_id is in chunkIDs.
func (r *copySourceReader) readBatch(ctx context.Context,
	sourceKnowledgeBaseID string, chunkIDs []string,
) ([]interface{}, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}

	objects, err := r.query(ctx, sourceKnowledgeBaseID, chunkIDs)
	if err != nil {
		return nil, err
	}
	if len(objects) < r.limit {
		return objects, nil
	}

	// The batch filled the limit, so it may have been cut off. A batch of one
	// chunk that does so cannot be split any further.
	if len(chunkIDs) == 1 {
		return nil, fmt.Errorf(
			"weaviate: source chunk %q returned a full query page of %d vectors, "+
				"which reaches the server's query limit (QUERY_MAXIMUM_RESULTS); "+
				"the chunk cannot be read completely and the copy was stopped",
			chunkIDs[0], r.limit)
	}

	middle := len(chunkIDs) / 2
	front, err := r.readBatch(ctx, sourceKnowledgeBaseID, chunkIDs[:middle])
	if err != nil {
		return nil, err
	}
	back, err := r.readBatch(ctx, sourceKnowledgeBaseID, chunkIDs[middle:])
	if err != nil {
		return nil, err
	}
	return append(front, back...), nil
}

// query runs one chunk_id-bounded Get. Weaviate answers a query it could not
// run with HTTP 200, an errors array and no data, so a failed query has to be
// read as a failure here - reading it as "the source has no more rows" is how
// a failed copy gets reported as a finished one.
func (r *copySourceReader) query(ctx context.Context,
	sourceKnowledgeBaseID string, chunkIDs []string,
) ([]interface{}, error) {
	log := logger.GetLogger(ctx)
	result, err := r.repo.client.GraphQL().Get().
		WithClassName(r.collectionName).
		WithWhere(filters.Where().
			WithOperator(filters.And).
			WithOperands([]*filters.WhereBuilder{
				filters.Where().
					WithPath([]string{fieldKnowledgeBaseID}).
					WithOperator(filters.Equal).
					WithValueString(sourceKnowledgeBaseID),
				filters.Where().
					WithPath([]string{fieldChunkID}).
					WithOperator(filters.ContainsAny).
					WithValueText(chunkIDs...),
			})).
		WithLimit(r.limit).
		WithFields(r.fields...).
		Do(ctx)

	var queryError string
	switch {
	case err != nil:
		queryError = err.Error()
	case result == nil:
		queryError = "nil graphql response"
	case len(result.Errors) > 0:
		if result.Errors[0] != nil && result.Errors[0].Message != "" {
			queryError = result.Errors[0].Message
		} else {
			queryError = "graphql error"
		}
	}
	if queryError != "" {
		if serverLimit, ok := queryMaximumResults(queryError); ok && serverLimit < r.limit {
			// QUERY_MAXIMUM_RESULTS is a server setting with no client-facing
			// API, so the refusal is the only place it can be read. Retry the
			// same read under the limit the server actually enforces; the
			// completeness check above uses the lowered limit from here on.
			log.Warnf("[Weaviate] Source query limit %d exceeds the server's "+
				"QUERY_MAXIMUM_RESULTS %d; retrying with %d",
				r.limit, serverLimit, serverLimit)
			r.limit = serverLimit
			return r.query(ctx, sourceKnowledgeBaseID, chunkIDs)
		}
		log.Errorf("[Weaviate] Failed to query source points: %s", queryError)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("weaviate: copy indices query failed: %s", queryError)
	}

	if result.Data == nil {
		return nil, fmt.Errorf("weaviate: copy indices invalid response")
	}
	get, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("weaviate: copy indices invalid response")
	}
	objects, ok := get[r.collectionName].([]interface{})
	if !ok {
		return nil, fmt.Errorf("weaviate: copy indices invalid response for collection %s", r.collectionName)
	}
	return objects, nil
}

// queryMaximumResultsPattern matches the refusal Weaviate returns for a query
// that asks for more rows than QUERY_MAXIMUM_RESULTS allows, e.g. "query
// maximum results exceeded: ... exceeds the configured value for
// QUERY_MAXIMUM_RESULTS '10000'" (weaviate/adapters/repos/db/search.go).
var queryMaximumResultsPattern = regexp.MustCompile(`QUERY_MAXIMUM_RESULTS '(\d+)'`)

// queryMaximumResults returns the row cap the server names in a refusal, if
// message is one.
func queryMaximumResults(message string) (int, bool) {
	match := queryMaximumResultsPattern.FindStringSubmatch(message)
	if match == nil {
		return 0, false
	}
	limit, err := strconv.Atoi(match[1])
	if err != nil || limit <= 0 {
		return 0, false
	}
	return limit, true
}

func (w *weaviateRepository) ListCollections(ctx context.Context) ([]string, error) {
	if w.client == nil {
		return nil, fmt.Errorf("weaviate: client is nil")
	}
	schema, err := w.client.Schema().Getter().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate 获取 schema 失败: %w", err)
	}
	if schema == nil {
		return nil, nil
	}
	var collectionNames []string
	for _, class := range schema.Classes {
		if class != nil && class.Class != "" {
			collectionNames = append(collectionNames, class.Class)
		}
	}

	return collectionNames, nil
}

// indexObjectID keeps the existing ID of a chunk's body while assigning each
// generated question its own stable UUID. Source IDs are not necessarily UUIDs.
// Deterministic IDs let a retried batch update the same objects.
func indexObjectID(sourceID, chunkID string) string {
	src := strings.TrimSpace(sourceID)
	chk := strings.TrimSpace(chunkID)
	if src == "" || src == chk {
		if chk == "" {
			return uuid.New().String()
		}
		if parsed, err := uuid.Parse(chk); err == nil {
			return parsed.String()
		}
		return uuid.NewSHA1(uuid.NameSpaceURL, []byte(chk)).String()
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(chk+"\x00"+src)).String()
}

func createPayload(embedding *VectorEmbedding) map[string]interface{} {
	payload := map[string]any{
		fieldContent:         embedding.Content,
		fieldSourceID:        embedding.SourceID,
		fieldSourceType:      int64(embedding.SourceType),
		fieldChunkID:         embedding.ChunkID,
		fieldKnowledgeID:     embedding.KnowledgeID,
		fieldKnowledgeBaseID: embedding.KnowledgeBaseID,
		fieldTagID:           embedding.TagID,
		fieldIsEnabled:       embedding.IsEnabled,
	}
	return payload
}

func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.WeaviateRetrieverEngineType,
			RetrieverType:       retrieverType,
			Error:               nil,
		},
	}
}

func getKeywordsFields() []graphql.Field {
	return []graphql.Field{
		{Name: fieldContent},
		{Name: fieldSourceID},
		{Name: fieldSourceType},
		{Name: fieldChunkID},
		{Name: fieldKnowledgeID},
		{Name: fieldKnowledgeBaseID},
		{Name: fieldTagID},
		{
			Name: "_additional",
			Fields: []graphql.Field{
				{Name: "id"},
				{Name: "score"},
			},
		},
	}
}

func getEmbeddingFields() []graphql.Field {
	return []graphql.Field{
		{Name: fieldContent},
		{Name: fieldSourceID},
		{Name: fieldSourceType},
		{Name: fieldChunkID},
		{Name: fieldKnowledgeID},
		{Name: fieldKnowledgeBaseID},
		{Name: fieldTagID},
		{
			Name: "_additional",
			Fields: []graphql.Field{
				{Name: "id"},
				{Name: "certainty"},
			},
		},
	}
}

func getVectorFields() []graphql.Field {
	return []graphql.Field{
		{Name: fieldContent},
		{Name: fieldSourceID},
		{Name: fieldSourceType},
		{Name: fieldChunkID},
		{Name: fieldKnowledgeID},
		{Name: fieldKnowledgeBaseID},
		{Name: fieldTagID},
		{
			Name: "_additional",
			Fields: []graphql.Field{
				{Name: "id"},
				{Name: "vector"},
			},
		},
	}
}

// parseGraphQLResponse parses the GraphQL response for vector embeddings or keyword search results.
func parseGraphQLResponse(items []interface{}, _ string, matchType types.MatchType) []*types.IndexWithScore {
	var results []*types.IndexWithScore
	var additionalName string
	if matchType == types.MatchTypeEmbedding {
		additionalName = "certainty"
	} else {
		additionalName = "score"
	}
	for _, item := range items {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		additional, ok := obj["_additional"].(map[string]interface{})
		if !ok {
			continue
		}

		pointID, ok := additional["id"].(string)
		if !ok || pointID == "" {
			continue
		}
		score := 0.0
		switch rawScore := additional[additionalName].(type) {
		case float64:
			if matchType == types.MatchTypeKeywords {
				score = rawScore
			} else {
				// certainty = (1 + cos) / 2; report cosine similarity so
				// scores compare with the other engines and thresholds.
				score = 2*rawScore - 1
			}
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(rawScore), 64); err == nil {
				if matchType == types.MatchTypeKeywords {
					score = parsed
				} else {
					score = 2*parsed - 1
				}
			}
		}

		getString := func(key string) string {
			if v, ok := obj[key].(string); ok {
				return v
			}
			return ""
		}
		getInt := func(key string) int {
			switch v := obj[key].(type) {
			case float64:
				return int(v)
			case int:
				return v
			case int64:
				return int(v)
			default:
				return 0
			}
		}
		isEnabled := true
		if rawEnabled, ok := obj[fieldIsEnabled].(bool); ok {
			isEnabled = rawEnabled
		}

		embedding := &VectorEmbeddingWithScore{
			VectorEmbedding{
				Content:         getString(fieldContent),
				SourceID:        getString(fieldSourceID),
				SourceType:      getInt(fieldSourceType),
				ChunkID:         getString(fieldChunkID),
				KnowledgeID:     getString(fieldKnowledgeID),
				KnowledgeBaseID: getString(fieldKnowledgeBaseID),
				TagID:           getString(fieldTagID),
				IsEnabled:       isEnabled,
			},
			float64(score),
		}

		results = append(results, fromWeaviateVectorEmbedding(pointID, embedding, matchType))
	}
	return results
}

// Ref: https://github.com/weaviate/weaviate/blob/b4aec91c6fe4/entities/vectorindex/hnsw/config.go#L27
func (w *weaviateRepository) calculateStorageSize(embedding *VectorEmbedding) int64 {
	// Payload fields
	payloadSizeBytes := int64(0)
	payloadSizeBytes += int64(len(embedding.Content))         // content string
	payloadSizeBytes += int64(len(embedding.SourceID))        // source_id string
	payloadSizeBytes += int64(len(embedding.ChunkID))         // chunk_id string
	payloadSizeBytes += int64(len(embedding.KnowledgeID))     // knowledge_id string
	payloadSizeBytes += int64(len(embedding.KnowledgeBaseID)) // knowledge_base_id string
	payloadSizeBytes += 8                                     // source_type int64

	// Vector storage and index
	var vectorSizeBytes int64
	var hnswIndexBytes int64
	if embedding.Embedding != nil {
		dimensions := int64(len(embedding.Embedding))
		vectorSizeBytes = dimensions * 4

		// HNSW graph links per vector: M×2 neighbors in layer 0, ~8 bytes per link
		// (4 bytes for neighbor ID + multi-layer amortization).
		// Graph link count depends on M, NOT on vector dimensions.
		const hnswM = 32
		hnswIndexBytes = hnswM * 2 * 8
	}

	// ID tracker metadata: 24 bytes per vector
	// (forward refs + backward refs + version tracking = 8 + 8 + 8)
	const idTrackerBytes int64 = 24

	totalSizeBytes := payloadSizeBytes + vectorSizeBytes + hnswIndexBytes + idTrackerBytes
	return totalSizeBytes
}

// toWeaviateVectorEmbedding converts IndexInfo to Weaviate payload format
func toWeaviateVectorEmbedding(
	embedding *types.IndexInfo,
	additionalParams map[string]interface{},
) *VectorEmbedding {
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
	}
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), fieldEmbedding) {
		if embeddingMap, ok := additionalParams[fieldEmbedding].(map[string][]float32); ok {
			vector.Embedding = embeddingMap[embedding.SourceID]
		}
	}
	return vector
}

// fromWeaviateVectorEmbedding converts Weaviate point to IndexWithScore domain model
func fromWeaviateVectorEmbedding(id string,
	embedding *VectorEmbeddingWithScore,
	matchType types.MatchType,
) *types.IndexWithScore {
	var vectorScore float64
	if matchType == types.MatchTypeEmbedding {
		vectorScore = embedding.Score
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
		VectorScore:     vectorScore,
		MatchType:       matchType,
		IsEnabled:       embedding.IsEnabled,
	}
}
