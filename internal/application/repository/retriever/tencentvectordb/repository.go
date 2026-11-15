package tencentvectordb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

const (
	copyIndicesQueryPageSize int64 = 500
	copyIndicesChunkBatch          = 200
)

var (
	bearerSecretPattern = regexp.MustCompile(`(?i)(Bearer\s+)[^\s",)]+`)
	apiKeySecretPattern = regexp.MustCompile(`(?i)(api_key=)[^\s&",)]+`)
)

func redactTencentErr(err error) error {
	if err == nil {
		return nil
	}
	orig := err.Error()
	scrubbed := bearerSecretPattern.ReplaceAllString(orig, "${1}[REDACTED]")
	scrubbed = apiKeySecretPattern.ReplaceAllString(scrubbed, "${1}[REDACTED]")
	if scrubbed == orig {
		return err
	}
	return errors.New(scrubbed)
}

func escapeFilterString(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		switch ch := raw[i]; ch {
		case 0:
			continue
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
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

func filterIn(field string, values []string) string {
	cleaned := cleanNonEmptyIDs(values)
	if len(cleaned) == 0 {
		return ""
	}
	quoted := make([]string, len(cleaned))
	for i, v := range cleaned {
		quoted[i] = `"` + escapeFilterString(v) + `"`
	}
	return fmt.Sprintf("%s in (%s)", field, strings.Join(quoted, ","))
}

func filterNotIn(field string, values []string) string {
	cleaned := cleanNonEmptyIDs(values)
	if len(cleaned) == 0 {
		return ""
	}
	quoted := make([]string, len(cleaned))
	for i, v := range cleaned {
		quoted[i] = `"` + escapeFilterString(v) + `"`
	}
	return fmt.Sprintf("%s not in (%s)", field, strings.Join(quoted, ","))
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

func filterGeneratedQuestionHits(results []*types.IndexWithScore, excludedKBIDs []string) []*types.IndexWithScore {
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
		if _, blocked := excluded[hit.KnowledgeBaseID]; blocked && types.IsGeneratedQuestionSource(hit.ChunkID, hit.SourceID, hit.SourceType) {
			continue
		}
		filtered = append(filtered, hit)
	}
	return filtered
}

// NewTencentVectorDBRetrieveEngineRepository creates a Tencent VectorDB-backed retrieve repository.
func NewTencentVectorDBRetrieveEngineRepository(
	client *tcvectordb.RpcClient,
	databaseName string,
	indexCfg *types.IndexConfig,
) interfaces.RetrieveEngineRepository {
	if databaseName == "" {
		databaseName = os.Getenv(envTencentVectorDBDatabase)
	}
	if databaseName == "" {
		databaseName = defaultDatabaseName
	}

	collectionBaseName := types.ResolveCollectionName(indexCfg, envTencentVectorDBCollection, defaultCollectionName)
	return &repository{
		client:             client,
		databaseName:       databaseName,
		collectionBaseName: collectionBaseName,
		useDimensionSuffix: shouldUseDimensionSuffix(indexCfg),
		shardsNum:          defaultIfZero(indexCfg.GetShardsNum(1), 1),
		replicasNum:        resolveReplicaNumber(indexCfg),
	}
}

func resolveReplicaNumber(indexCfg *types.IndexConfig) int {
	if indexCfg != nil && indexCfg.ReplicaNumber > 0 {
		return indexCfg.ReplicaNumber
	}
	if raw := strings.TrimSpace(os.Getenv(envTencentVectorDBReplicaNum)); raw != "" {
		replicas, err := strconv.Atoi(raw)
		if err == nil && replicas >= 0 {
			return replicas
		}
	}
	return defaultReplicaNumber
}

func (r *repository) EngineType() types.RetrieverEngineType {
	return types.TencentVectorDBRetrieverEngineType
}

func (r *repository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

func (r *repository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	if indexInfo == nil {
		return fmt.Errorf("tencent vectordb nil index info")
	}
	return r.BatchSave(ctx, []*types.IndexInfo{indexInfo}, params)
}

func (r *repository) BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error {
	log := logger.GetLogger(ctx)
	if len(indexInfoList) == 0 {
		return nil
	}

	embeddingsByDimension := make(map[int][]*vectorEmbedding)
	for _, indexInfo := range indexInfoList {
		if indexInfo == nil {
			continue
		}
		embedding := toVectorEmbedding(indexInfo, params)
		if len(embedding.Embedding) == 0 {
			log.Warnf("[TencentVectorDB] skip empty embedding for chunk_id=%s", indexInfo.ChunkID)
			continue
		}
		dim := len(embedding.Embedding)
		embeddingsByDimension[dim] = append(embeddingsByDimension[dim], embedding)
	}
	if len(embeddingsByDimension) == 0 {
		return nil
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}

	bm25, err := r.bm25Encoder()
	if err != nil {
		return err
	}

	buildIndex := true
	for dim, embeddings := range embeddingsByDimension {
		docs, err := r.toDocumentsWithSparseVectors(bm25, embeddings)
		if err != nil {
			return err
		}
		if err := r.ensureCollection(ctx, dim); err != nil {
			return err
		}
		collectionName := r.collectionName(dim)
		_, err = r.client.Database(r.databaseName).Collection(collectionName).Upsert(
			ctx,
			docs,
			&tcvectordb.UpsertDocumentParams{BuildIndex: &buildIndex},
		)
		if err != nil {
			return fmt.Errorf("tencent vectordb batch save %s: %w", collectionName, redactTencentErr(err))
		}
	}
	return nil
}

func (r *repository) EstimateStorageSize(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64 {
	var total int64
	for _, indexInfo := range indexInfoList {
		if indexInfo == nil {
			continue
		}
		embedding := toVectorEmbedding(indexInfo, params)
		total += int64(len(embedding.Content))
		total += int64(len(embedding.Embedding) * 4)
		total += int64(len(embedding.Content) * 2)
		total += int64(len(embedding.SourceID) + len(embedding.ChunkID) + len(embedding.KnowledgeID) + len(embedding.KnowledgeBaseID) + 256)
	}
	logger.GetLogger(ctx).Infof("[TencentVectorDB] estimated storage size for %d indices: %d bytes", len(indexInfoList), total)
	return total
}

func (r *repository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, dimension, filterIn(fieldChunkID, chunkIDList))
}

func (r *repository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, dimension, filterIn(fieldSourceID, sourceIDList))
}

func (r *repository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, dimension, filterIn(fieldKnowledgeID, knowledgeIDList))
}

func (r *repository) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	srcKB := strings.TrimSpace(sourceKnowledgeBaseID)
	dstKB := strings.TrimSpace(targetKnowledgeBaseID)
	if srcKB == "" || dstKB == "" {
		return fmt.Errorf("tencent vectordb copy indices requires non-empty source and target knowledge_base_id")
	}
	if r.useDimensionSuffix && dimension <= 0 {
		return fmt.Errorf("tencent vectordb invalid dimension %d for copy indices", dimension)
	}
	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}
	ids := cleanNonEmptyIDs(slices.Sorted(maps.Keys(sourceToTargetChunkIDMap)))
	if len(ids) == 0 {
		return nil
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}
	collectionName := r.collectionName(dimension)

	var embeddings []*vectorEmbedding
	for chunkBatch := range slices.Chunk(ids, copyIndicesChunkBatch) {
		for offset := int64(0); ; offset += copyIndicesQueryPageSize {
			queryParams, err := copySourceQueryParams(srcKB, chunkBatch, offset)
			if err != nil {
				return err
			}
			query, err := r.client.Database(r.databaseName).Collection(collectionName).Query(
				ctx,
				nil,
				queryParams,
			)
			if err != nil {
				return fmt.Errorf("tencent vectordb query source indices: %w", redactTencentErr(err))
			}
			if query == nil {
				break
			}
			embeddings = append(embeddings, remapCopiedEmbeddings(
				query.Documents,
				sourceToTargetKBIDMap,
				sourceToTargetChunkIDMap,
				dstKB,
			)...)
			if len(query.Documents) < int(copyIndicesQueryPageSize) {
				break
			}
		}
	}
	if len(embeddings) == 0 {
		return nil
	}

	bm25, err := r.bm25Encoder()
	if err != nil {
		return err
	}
	docs, err := r.toDocumentsWithSparseVectors(bm25, embeddings)
	if err != nil {
		return err
	}

	buildIndex := true
	_, err = r.client.Database(r.databaseName).Collection(collectionName).Upsert(
		ctx,
		docs,
		&tcvectordb.UpsertDocumentParams{BuildIndex: &buildIndex},
	)
	if err != nil {
		return fmt.Errorf("tencent vectordb copy indices: %w", redactTencentErr(err))
	}
	return nil
}

func (r *repository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}
	grouped := make(map[bool][]string)
	for chunkID, enabled := range chunkStatusMap {
		grouped[enabled] = append(grouped[enabled], chunkID)
	}
	for enabled, chunkIDs := range grouped {
		if err := r.updateChunkFields(ctx, chunkIDs, map[string]tcvectordb.Field{fieldIsEnabled: {Val: boolToUint64(enabled)}}); err != nil {
			return err
		}
	}
	return nil
}

func (r *repository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		return nil
	}
	grouped := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		grouped[tagID] = append(grouped[tagID], chunkID)
	}
	for tagID, chunkIDs := range grouped {
		if err := r.updateChunkFields(ctx, chunkIDs, map[string]tcvectordb.Field{fieldTagID: {Val: tagID}}); err != nil {
			return err
		}
	}
	return nil
}

func (r *repository) BatchUpdateChunkContent(ctx context.Context, chunkContentMap map[string]string) error {
	if len(chunkContentMap) == 0 {
		return nil
	}
	grouped := make(map[string][]string)
	for chunkID, content := range chunkContentMap {
		trimmedID := strings.TrimSpace(chunkID)
		if trimmedID == "" {
			continue
		}
		cleaned := cleanInvalidUTF8(content)
		grouped[cleaned] = append(grouped[cleaned], trimmedID)
	}
	if len(grouped) == 0 {
		return nil
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}
	bm25, err := r.bm25Encoder()
	if err != nil {
		return err
	}
	collections, err := r.client.Database(r.databaseName).ListCollection(ctx)
	if err != nil {
		return fmt.Errorf("tencent vectordb list collections: %w", redactTencentErr(err))
	}
	if collections == nil {
		return nil
	}

	for content, chunkIDs := range grouped {
		sparseVecs, err := bm25.EncodeTexts([]string{content})
		if err != nil {
			return fmt.Errorf("tencent vectordb encode BM25 sparse vectors: %w", err)
		}
		if len(sparseVecs) != 1 {
			return fmt.Errorf("tencent vectordb encoded sparse vector count mismatch: got %d, want 1", len(sparseVecs))
		}
		filter := tcvectordb.NewFilter(
			filterIn(fieldChunkID, chunkIDs) + " and " + filterIn(fieldSourceID, chunkIDs),
		)
		for _, collection := range collections.Collections {
			if !r.matchesCollection(collection.CollectionName) {
				continue
			}
			_, err := r.client.Database(r.databaseName).Collection(collection.CollectionName).Update(ctx, tcvectordb.UpdateDocumentParams{
				QueryFilter:     filter,
				UpdateFields:    map[string]tcvectordb.Field{fieldContent: {Val: content}},
				UpdateSparseVec: sparseVecs[0],
			})
			if err != nil {
				return fmt.Errorf("tencent vectordb update chunk content in %s: %w", collection.CollectionName, redactTencentErr(err))
			}
		}
	}
	return nil
}

func (r *repository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return r.KeywordsRetrieve(ctx, params)
	default:
		return nil, fmt.Errorf("invalid retriever type: %s", params.RetrieverType)
	}
}

func (r *repository) VectorRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	if !hasValidRetrieveScope(params) {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}
	dimension := len(params.Embedding)
	if dimension == 0 {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}
	if r.client == nil {
		return nil, fmt.Errorf("tencent vectordb client is nil")
	}

	collectionName := r.collectionName(dimension)
	exists, err := r.client.Database(r.databaseName).ExistsCollection(ctx, collectionName)
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb check collection %s: %w", collectionName, redactTencentErr(err))
	}
	if !exists {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}

	topK := params.TopK
	if topK <= 0 {
		topK = 10
	}
	limit := int64(topK)
	if len(cleanNonEmptyIDs(params.ExcludeGeneratedQuestionKBIDs)) > 0 {
		limit = max(limit*4, 32)
	}
	searchParams := &tcvectordb.SearchDocumentParams{
		Filter: r.baseFilter(params),
		// HNSW returns at most ef candidates; a fixed 100 capped recall
		// below TopK, which reaches 500.
		Params:         &tcvectordb.SearchDocParams{Ef: uint32(max(limit, 100))},
		RetrieveVector: false,
		OutputFields:   outputFields(),
		Limit:          limit,
	}
	if params.Threshold > 0 {
		radius := float32(params.Threshold)
		searchParams.Radius = &radius
	}

	search, err := r.client.Database(r.databaseName).Collection(collectionName).Search(ctx, [][]float32{params.Embedding}, searchParams)
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb vector search %s: %w", collectionName, redactTencentErr(err))
	}
	if search == nil || len(search.Documents) == 0 {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}

	results := make([]*types.IndexWithScore, 0, len(search.Documents[0]))
	for _, doc := range search.Documents[0] {
		embedding := fromDocument(doc)
		results = append(results, toIndexWithScore(embedding, types.MatchTypeEmbedding))
	}
	results = filterGeneratedQuestionHits(results, params.ExcludeGeneratedQuestionKBIDs)
	if len(results) > topK {
		results = results[:topK]
	}
	return r.retrieveResult(results, types.VectorRetrieverType), nil
}

func (r *repository) KeywordsRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	if !hasValidRetrieveScope(params) {
		return r.retrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return r.retrieveResult(nil, types.KeywordsRetrieverType), nil
	}
	if r.client == nil {
		return nil, fmt.Errorf("tencent vectordb client is nil")
	}

	bm25, err := r.bm25Encoder()
	if err != nil {
		return nil, err
	}
	queryVectors, err := bm25.EncodeQueries([]string{query})
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb encode keyword query: %w", err)
	}
	if len(queryVectors) == 0 || len(queryVectors[0]) == 0 {
		return r.retrieveResult(nil, types.KeywordsRetrieverType), nil
	}

	collections, err := r.client.Database(r.databaseName).ListCollection(ctx)
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb list collections: %w", redactTencentErr(err))
	}
	if collections == nil {
		return r.retrieveResult(nil, types.KeywordsRetrieverType), nil
	}

	topK := params.TopK
	if topK <= 0 {
		topK = 10
	}
	queryLimit := topK
	if len(cleanNonEmptyIDs(params.ExcludeGeneratedQuestionKBIDs)) > 0 {
		queryLimit = max(topK*4, 32)
	}
	results := make([]*types.IndexWithScore, 0, topK)
	matchedCollections := 0
	failedCollections := 0
	filter := r.baseFilter(params)
	for _, collection := range collections.Collections {
		if !r.matchesCollection(collection.CollectionName) {
			continue
		}
		matchedCollections++

		search, err := r.client.Database(r.databaseName).Collection(collection.CollectionName).FullTextSearch(
			ctx,
			tcvectordb.FullTextSearchParams{
				Filter:         filter,
				RetrieveVector: false,
				OutputFields:   outputFields(),
				Limit:          &queryLimit,
				Match: &tcvectordb.FullTextSearchMatchOption{
					FieldName: fieldSparseVector,
					Data:      queryVectors,
				},
			},
		)
		if err != nil {
			failedCollections++
			log.Warnf("[TencentVectorDB] keyword search failed in %s: %v", collection.CollectionName, redactTencentErr(err))
			continue
		}
		if search == nil || len(search.Documents) == 0 {
			continue
		}
		for _, doc := range search.Documents[0] {
			embedding := fromDocument(doc)
			results = append(results, toIndexWithScore(embedding, types.MatchTypeKeywords))
		}
	}
	if matchedCollections > 0 && failedCollections == matchedCollections {
		return nil, fmt.Errorf("tencent vectordb keyword search failed in all matched collections; ensure collections have the %q sparse vector index and reimport data if they were created before keyword support", fieldSparseVector)
	}

	results = filterGeneratedQuestionHits(results, params.ExcludeGeneratedQuestionKBIDs)
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return r.retrieveResult(results, types.KeywordsRetrieverType), nil
}

func (r *repository) ensureCollection(ctx context.Context, dimension int) error {
	if dimension <= 0 {
		return fmt.Errorf("tencent vectordb invalid dimension %d", dimension)
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}
	if _, ok := r.initialized.Load(dimension); ok {
		return nil
	}

	if _, err := r.client.CreateDatabaseIfNotExists(ctx, r.databaseName); err != nil {
		return fmt.Errorf("tencent vectordb ensure database %s: %w", r.databaseName, redactTencentErr(err))
	}

	collectionName := r.collectionName(dimension)
	exists, err := r.client.Database(r.databaseName).ExistsCollection(ctx, collectionName)
	if err != nil {
		return fmt.Errorf("tencent vectordb check collection %s: %w", collectionName, redactTencentErr(err))
	}
	if exists {
		r.initialized.Store(dimension, true)
		return nil
	}

	indexes := tcvectordb.Indexes{
		VectorIndex: []tcvectordb.VectorIndex{
			{
				FilterIndex: tcvectordb.FilterIndex{
					FieldName: fieldVector,
					FieldType: tcvectordb.Vector,
					IndexType: tcvectordb.HNSW,
				},
				Dimension:  uint32(dimension),
				MetricType: tcvectordb.COSINE,
				Params: &tcvectordb.HNSWParam{
					M:              16,
					EfConstruction: 200,
				},
			},
		},
		SparseVectorIndex: []tcvectordb.SparseVectorIndex{
			{
				FieldName:  fieldSparseVector,
				FieldType:  tcvectordb.SparseVector,
				IndexType:  tcvectordb.SPARSE_INVERTED,
				MetricType: tcvectordb.IP,
			},
		},
		FilterIndex: []tcvectordb.FilterIndex{
			{FieldName: fieldID, FieldType: tcvectordb.String, IndexType: tcvectordb.PRIMARY},
			{FieldName: fieldContent, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldSourceID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldSourceType, FieldType: tcvectordb.Uint64, IndexType: tcvectordb.FILTER},
			{FieldName: fieldChunkID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldKnowledgeID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldKnowledgeBaseID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldTagID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldIsEnabled, FieldType: tcvectordb.Uint64, IndexType: tcvectordb.FILTER},
		},
	}
	_, err = r.client.Database(r.databaseName).CreateCollection(
		ctx,
		collectionName,
		uint32(r.shardsNum),
		uint32(r.replicasNum),
		fmt.Sprintf("GENYEE embeddings collection with dimension %d", dimension),
		indexes,
	)
	if err != nil {
		if isCollectionAlreadyExistsErr(err) {
			logger.GetLogger(ctx).Infof("[TencentVectorDB] collection %s already exists, skip create", collectionName)
			r.initialized.Store(dimension, true)
			return nil
		}
		return fmt.Errorf("tencent vectordb create collection %s: %w", collectionName, redactTencentErr(err))
	}

	r.initialized.Store(dimension, true)
	return nil
}

func isCollectionAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "code: 15202") || strings.Contains(msg, "already exist")
}

func (r *repository) deleteByFilter(ctx context.Context, dimension int, cond string) error {
	if cond == "" {
		return nil
	}
	if r.useDimensionSuffix && dimension <= 0 {
		return fmt.Errorf("tencent vectordb invalid dimension %d for delete", dimension)
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}
	collectionName := r.collectionName(dimension)
	_, err := r.client.Database(r.databaseName).Collection(collectionName).Delete(ctx, tcvectordb.DeleteDocumentParams{
		Filter: tcvectordb.NewFilter(cond),
	})
	if err != nil {
		return fmt.Errorf("tencent vectordb delete from %s: %w", collectionName, redactTencentErr(err))
	}
	return nil
}

func (r *repository) updateChunkFields(ctx context.Context, chunkIDs []string, fields map[string]tcvectordb.Field) error {
	cleaned := cleanNonEmptyIDs(chunkIDs)
	if len(cleaned) == 0 {
		return nil
	}
	if r.client == nil {
		return fmt.Errorf("tencent vectordb client is nil")
	}
	collections, err := r.client.Database(r.databaseName).ListCollection(ctx)
	if err != nil {
		return fmt.Errorf("tencent vectordb list collections: %w", redactTencentErr(err))
	}
	if collections == nil {
		return nil
	}

	cond := filterIn(fieldChunkID, cleaned)
	for _, collection := range collections.Collections {
		if !r.matchesCollection(collection.CollectionName) {
			continue
		}
		_, err := r.client.Database(r.databaseName).Collection(collection.CollectionName).Update(ctx, tcvectordb.UpdateDocumentParams{
			QueryFilter:  tcvectordb.NewFilter(cond),
			UpdateFields: fields,
		})
		if err != nil {
			return fmt.Errorf("tencent vectordb update chunks in %s: %w", collection.CollectionName, redactTencentErr(err))
		}
	}
	return nil
}

func (r *repository) collectionName(dimension int) string {
	if !r.useDimensionSuffix {
		return r.collectionBaseName
	}
	return fmt.Sprintf("%s_%d", r.collectionBaseName, dimension)
}

func (r *repository) matchesCollection(collectionName string) bool {
	if !r.useDimensionSuffix {
		return collectionName == r.collectionBaseName
	}
	prefix := r.collectionBaseName + "_"
	if !strings.HasPrefix(collectionName, prefix) {
		return false
	}
	return isPositiveDimensionSuffix(strings.TrimPrefix(collectionName, prefix))
}

func shouldUseDimensionSuffix(indexCfg *types.IndexConfig) bool {
	return indexCfg == nil || indexCfg.CollectionName == ""
}

func (r *repository) baseFilter(params types.RetrieveParams) *tcvectordb.Filter {
	if !hasValidRetrieveScope(params) {
		return tcvectordb.NewFilter(fmt.Sprintf("%s=0", fieldIsEnabled))
	}
	conditions := []string{fmt.Sprintf("%s=1", fieldIsEnabled)}
	if cond := filterIn(fieldKnowledgeBaseID, params.KnowledgeBaseIDs); cond != "" {
		conditions = append(conditions, cond)
	}
	if cond := filterIn(fieldKnowledgeID, params.KnowledgeIDs); cond != "" {
		conditions = append(conditions, cond)
	}
	if cond := filterIn(fieldTagID, params.TagIDs); cond != "" {
		conditions = append(conditions, cond)
	}
	if cond := filterNotIn(fieldKnowledgeID, params.ExcludeKnowledgeIDs); cond != "" {
		conditions = append(conditions, cond)
	}
	if cond := filterNotIn(fieldChunkID, params.ExcludeChunkIDs); cond != "" {
		conditions = append(conditions, cond)
	}
	return tcvectordb.NewFilter(strings.Join(conditions, " and "))
}

func (r *repository) retrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.TencentVectorDBRetrieverEngineType,
			RetrieverType:       retrieverType,
		},
	}
}

func (r *repository) bm25Encoder() (encoder.SparseEncoder, error) {
	r.bm25Once.Do(func() {
		if err := EnsureDefaultStopwords(); err != nil {
			r.bm25Err = fmt.Errorf("tencent vectordb pre-seed stopwords: %w", err)
			return
		}
		r.bm25, r.bm25Err = encoder.NewBM25Encoder(&encoder.BM25EncoderParams{
			Bm25Language: encoder.BM25_ZH_CONTENT,
		})
		if r.bm25Err != nil {
			r.bm25Err = fmt.Errorf("tencent vectordb init BM25 encoder: %w", r.bm25Err)
		}
	})
	return r.bm25, r.bm25Err
}

func (r *repository) toDocumentsWithSparseVectors(
	bm25 encoder.SparseEncoder,
	embeddings []*vectorEmbedding,
) ([]tcvectordb.Document, error) {
	texts := make([]string, 0, len(embeddings))
	for _, embedding := range embeddings {
		texts = append(texts, embedding.Content)
	}
	sparseVectors, err := bm25.EncodeTexts(texts)
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb encode BM25 sparse vectors: %w", err)
	}
	docs := make([]tcvectordb.Document, 0, len(embeddings))
	for i, embedding := range embeddings {
		if i >= len(sparseVectors) {
			return nil, fmt.Errorf("tencent vectordb encoded sparse vector count mismatch: got %d, want %d", len(sparseVectors), len(embeddings))
		}
		embedding.SparseVector = sparseVectors[i]
		docs = append(docs, toDocument(embedding))
	}
	return docs, nil
}

func toVectorEmbedding(indexInfo *types.IndexInfo, params map[string]any) *vectorEmbedding {
	embedding := &vectorEmbedding{
		ID:              indexInfo.ID,
		Content:         cleanInvalidUTF8(indexInfo.Content),
		SourceID:        indexInfo.SourceID,
		SourceType:      int(indexInfo.SourceType),
		ChunkID:         indexInfo.ChunkID,
		KnowledgeID:     indexInfo.KnowledgeID,
		KnowledgeBaseID: indexInfo.KnowledgeBaseID,
		TagID:           indexInfo.TagID,
		IsEnabled:       indexInfo.IsEnabled,
	}
	if embedding.ID == "" {
		embedding.ID = indexInfo.SourceID
	}
	if embedding.ID == "" {
		embedding.ID = indexInfo.ChunkID
	}
	if params != nil && slices.Contains(slices.Collect(maps.Keys(params)), fieldVector) {
		if embeddingMap, ok := params[fieldVector].(map[string][]float32); ok {
			embedding.Embedding = lookupEmbedding(embeddingMap, indexInfo)
		}
	}
	if params != nil && slices.Contains(slices.Collect(maps.Keys(params)), "embedding") {
		if embeddingMap, ok := params["embedding"].(map[string][]float32); ok {
			embedding.Embedding = lookupEmbedding(embeddingMap, indexInfo)
		}
	}
	return embedding
}

func lookupEmbedding(embeddingMap map[string][]float32, indexInfo *types.IndexInfo) []float32 {
	if embedding, ok := embeddingMap[indexInfo.SourceID]; ok {
		return embedding
	}
	return embeddingMap[indexInfo.ChunkID]
}

func copySourceQueryParams(sourceKnowledgeBaseID string, chunkIDs []string, offset int64) (*tcvectordb.QueryDocumentParams, error) {
	srcKB := strings.TrimSpace(sourceKnowledgeBaseID)
	if srcKB == "" {
		return nil, fmt.Errorf("tencent vectordb copy indices requires non-empty source knowledge_base_id")
	}
	chunkCond := filterIn(fieldChunkID, chunkIDs)
	if chunkCond == "" {
		return nil, fmt.Errorf("tencent vectordb copy indices requires non-empty chunk_ids")
	}
	kbCond := filterIn(fieldKnowledgeBaseID, []string{srcKB})
	return &tcvectordb.QueryDocumentParams{
		Filter:         tcvectordb.NewFilter(kbCond + " and " + chunkCond),
		RetrieveVector: true,
		OutputFields:   outputFields(),
		Offset:         offset,
		Limit:          copyIndicesQueryPageSize,
	}, nil
}

func remapCopiedEmbeddings(
	docs []tcvectordb.Document,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
) []*vectorEmbedding {
	embeddings := make([]*vectorEmbedding, 0, len(docs))
	for _, doc := range docs {
		embedding := fromDocument(doc)
		targetChunkID := sourceToTargetChunkIDMap[embedding.ChunkID]
		if targetChunkID == "" {
			continue
		}
		originalSourceID := embedding.SourceID
		if originalSourceID == "" {
			originalSourceID = embedding.ID
		}
		targetSourceID := translateSourceID(originalSourceID, embedding.ChunkID, targetChunkID)
		embedding.ID = targetSourceID
		embedding.SourceID = targetSourceID
		embedding.ChunkID = targetChunkID
		embedding.KnowledgeBaseID = targetKnowledgeBaseID
		if targetKBID := sourceToTargetKBIDMap[embedding.KnowledgeID]; targetKBID != "" {
			embedding.KnowledgeID = targetKBID
		}
		embeddings = append(embeddings, embedding)
	}
	return embeddings
}

func translateSourceID(originalSourceID, sourceChunkID, targetChunkID string) string {
	switch {
	case originalSourceID == sourceChunkID:
		return targetChunkID
	case strings.HasPrefix(originalSourceID, sourceChunkID+"-"):
		questionID := strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
		return fmt.Sprintf("%s-%s", targetChunkID, questionID)
	default:
		sum := sha256.Sum256([]byte(targetChunkID + "\x00" + sourceChunkID + "\x00" + originalSourceID))
		return fmt.Sprintf("%s-%s", targetChunkID, hex.EncodeToString(sum[:])[:16])
	}
}

func cleanInvalidUTF8(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r == 0 {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func toDocument(embedding *vectorEmbedding) tcvectordb.Document {
	return tcvectordb.Document{
		Id:           embedding.ID,
		Vector:       embedding.Embedding,
		SparseVector: embedding.SparseVector,
		Fields: map[string]tcvectordb.Field{
			fieldContent:         {Val: embedding.Content},
			fieldSourceID:        {Val: embedding.SourceID},
			fieldSourceType:      {Val: uint64(embedding.SourceType)},
			fieldChunkID:         {Val: embedding.ChunkID},
			fieldKnowledgeID:     {Val: embedding.KnowledgeID},
			fieldKnowledgeBaseID: {Val: embedding.KnowledgeBaseID},
			fieldTagID:           {Val: embedding.TagID},
			fieldIsEnabled:       {Val: boolToUint64(embedding.IsEnabled)},
		},
	}
}

func fromDocument(doc tcvectordb.Document) *vectorEmbedding {
	return &vectorEmbedding{
		ID:              doc.Id,
		Content:         fieldString(doc, fieldContent),
		SourceID:        fieldString(doc, fieldSourceID),
		SourceType:      int(fieldUint64(doc, fieldSourceType)),
		ChunkID:         fieldString(doc, fieldChunkID),
		KnowledgeID:     fieldString(doc, fieldKnowledgeID),
		KnowledgeBaseID: fieldString(doc, fieldKnowledgeBaseID),
		TagID:           fieldString(doc, fieldTagID),
		Embedding:       doc.Vector,
		SparseVector:    doc.SparseVector,
		IsEnabled:       fieldUint64(doc, fieldIsEnabled) == 1,
		Score:           float64(doc.Score),
	}
}

func toIndexWithScore(embedding *vectorEmbedding, matchType types.MatchType) *types.IndexWithScore {
	var vectorScore float64
	if matchType == types.MatchTypeEmbedding {
		vectorScore = embedding.Score
	}
	return &types.IndexWithScore{
		ID:              embedding.ID,
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		Score:           embedding.Score,
		VectorScore:     vectorScore,
		MatchType:       matchType,
		IsEnabled:       embedding.IsEnabled,
	}
}

func outputFields() []string {
	return []string{
		fieldID,
		fieldContent,
		fieldSourceID,
		fieldSourceType,
		fieldChunkID,
		fieldKnowledgeID,
		fieldKnowledgeBaseID,
		fieldTagID,
		fieldIsEnabled,
	}
}

func fieldString(doc tcvectordb.Document, name string) string {
	field, ok := doc.Fields[name]
	if !ok {
		return ""
	}
	return field.String()
}

func fieldUint64(doc tcvectordb.Document, name string) uint64 {
	field, ok := doc.Fields[name]
	if !ok {
		return 0
	}
	return field.Uint64()
}

func boolToUint64(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func defaultIfZero(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
