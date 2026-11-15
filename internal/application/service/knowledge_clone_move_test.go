package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/application/access"
	sqliteretriever "github.com/Usir233/GENYEE/internal/application/repository/retriever/sqlite"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cloneIndexDim2Embedder struct {
	parentChildEmbedder
}

func (cloneIndexDim2Embedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

func (cloneIndexDim2Embedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func (cloneIndexDim2Embedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func (cloneIndexDim2Embedder) GetDimensions() int { return 2 }

func TestCloneChunkSynchronizesRetrieverIndexContentWithRewrittenResourceHandle(t *testing.T) {
	registerCloneTagSQLiteVec.Do(sqlitevec.Auto)
	t.Setenv("RETRIEVE_DRIVER", "sqlite")

	f := transferFixture(t, access.KBTransferClone)
	catalog, physical := cloneRollbackStorage(t, f)

	index := sqliteretriever.NewSQLiteRetrieveEngineRepository(f.db)
	f.svc.retrieveEngine = parentChildRetrieveRegistry{
		engine: retriever.NewKVHybridRetrieveEngine(index, types.SQLiteRetrieverEngineType),
	}
	f.svc.modelService = parentChildModelService{embedder: cloneIndexDim2Embedder{}}

	for _, kb := range []*types.KnowledgeBase{f.kbs.values["kb"], f.kbs.values["other"]} {
		kb.EmbeddingModelID = "model"
		kb.IndexingStrategy.VectorEnabled = true
		kb.IndexingStrategy.KeywordEnabled = true
		kb.ImageProcessingConfig.ImageVectorEnabled = true
	}

	// Register a source resource:// handle (without content_hash, e.g. migrated/external
	// source) so cloning into the destination mints a distinct resource://<newHandle>.
	srcPhysical, err := physical.SaveBytes(f.ctx, []byte("png-diagram-payload"), 7, "diagram.png", false)
	require.NoError(t, err)
	oldRef, err := catalog.Register(f.ctx, 7, srcPhysical, interfaces.ResourceRegistration{
		Kind:         "image",
		OriginalName: "diagram.png",
	})
	require.NoError(t, err)
	require.Regexp(t, `^resource://[0-9A-Za-z_-]{22}$`, oldRef)
	require.NoError(t, catalog.Bind(
		f.ctx, oldRef, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationExtractedImage,
	))

	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Updates(map[string]any{
		"title":              "Architecture Runbook",
		"embedding_model_id": "model",
	}).Error)

	srcBody := "Topology overview: ![arch](" + oldRef + ")"
	srcMeta := `{"generated_questions_revision":0,"generated_questions":[{"id":"q1","question":"How is topology wired?"}]}`
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content":        srcBody,
		"source_content": srcBody,
		"start_at":       0,
		"end_at":         utf8.RuneCountInString(srcBody),
		"context_header": "## Network",
		"metadata":       srcMeta,
		"is_enabled":     true,
	}).Error)

	imgInfo := mustImageInfoJSON(t, []types.ImageInfo{{
		URL:         oldRef,
		OriginalURL: oldRef,
		Caption:     "topology diagram",
	}})
	srcCaptionBody := "Diagram reference " + oldRef
	require.NoError(t, f.db.Create(&types.Chunk{
		ID:              "img-caption",
		TenantID:        7,
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		ChunkType:       types.ChunkTypeImageCaption,
		ParentChunkID:   "chunk",
		Content:         srcCaptionBody,
		ImageInfo:       imgInfo,
		IsEnabled:       true,
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID:              "img-vector",
		TenantID:        7,
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		ChunkType:       types.ChunkTypeImageVector,
		ParentChunkID:   "chunk",
		Content:         "visual topology diagram",
		ImageInfo:       imgInfo,
		IsEnabled:       true,
	}).Error)

	srcDoc, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	srcTextChunk, err := f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
	require.NoError(t, err)

	// Index primary text chunk, generated question, image caption, and image vector.
	require.NoError(t, index.Save(f.ctx, &types.IndexInfo{
		SourceID:        "chunk",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         buildKnowledgeIndexContent(srcDoc, srcTextChunk.EmbeddingContent()),
		IsEnabled:       true,
	}, map[string]any{"embedding": map[string][]float32{"chunk": {1, 0}}}))

	require.NoError(t, index.Save(f.ctx, &types.IndexInfo{
		SourceID:        "chunk-q1",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         buildKnowledgeIndexContent(srcDoc, "How is topology wired?"),
		IsEnabled:       true,
	}, map[string]any{"embedding": map[string][]float32{"chunk-q1": {0.6, 0.8}}}))

	require.NoError(t, index.Save(f.ctx, &types.IndexInfo{
		SourceID:        "img-caption",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "img-caption",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         srcCaptionBody,
		IsEnabled:       true,
	}, map[string]any{"embedding": map[string][]float32{"img-caption": {0.8, 0.6}}}))

	require.NoError(t, index.Save(f.ctx, &types.IndexInfo{
		SourceID:        "img-vector",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "img-vector",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "visual topology diagram",
		IsEnabled:       true,
	}, map[string]any{"embedding": map[string][]float32{"img-vector": {0, 1}}}))

	// Clone document into target KB "other".
	require.NoError(t, f.svc.cloneKnowledge(f.ctx, srcDoc, f.kbs.values["other"]))

	dstDoc := clonedTagKnowledge(t, f)
	require.Equal(t, types.ParseStatusCompleted, dstDoc.ParseStatus)

	dstChunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, dstDoc.ID)
	require.NoError(t, err)
	require.Len(t, dstChunks, 3)

	var (
		dstTextChunk    *types.Chunk
		dstCaptionChunk *types.Chunk
		dstImageVec     *types.Chunk
	)
	for _, c := range dstChunks {
		switch c.ChunkType {
		case types.ChunkTypeText:
			dstTextChunk = c
		case types.ChunkTypeImageCaption:
			dstCaptionChunk = c
		case types.ChunkTypeImageVector:
			dstImageVec = c
		}
	}
	require.NotNil(t, dstTextChunk)
	require.NotNil(t, dstCaptionChunk)
	require.NotNil(t, dstImageVec)

	var copiedImages []types.ImageInfo
	require.NoError(t, json.Unmarshal([]byte(dstCaptionChunk.ImageInfo), &copiedImages))
	require.Len(t, copiedImages, 1)
	newRef := copiedImages[0].URL
	require.Regexp(t, `^resource://[0-9A-Za-z_-]{22}$`, newRef)
	require.NotEqual(t, oldRef, newRef, "clone must mint an independent destination resource handle")

	assert.Equal(t, "## Network", dstTextChunk.ContextHeader)
	assert.Contains(t, dstTextChunk.Content, newRef)
	assert.NotContains(t, dstTextChunk.Content, oldRef)

	// Hydrate vector retrieval results via processSearchResults and verify MatchedContent.
	vecHits, err := index.Retrieve(f.ctx, types.RetrieveParams{
		Embedding:                     []float32{1, 0},
		TopK:                          10,
		KnowledgeBaseIDs:              []string{"other"},
		ExcludeGeneratedQuestionKBIDs: []string{"other"},
		RetrieverType:                 types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, vecHits, 1)
	require.NotEmpty(t, vecHits[0].Results)

	searchSvc := &knowledgeBaseService{kgRepo: f.repo, chunkRepo: f.chunkRepo}
	searchCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	searchCtx = context.WithValue(searchCtx, types.UserIDContextKey, "user-7")

	hydrated, err := searchSvc.processSearchResults(searchCtx, vecHits[0].Results, 10, true)
	require.NoError(t, err)

	byID := make(map[string]*types.SearchResult, len(hydrated))
	for _, r := range hydrated {
		byID[r.ID] = r
	}
	require.Contains(t, byID, dstTextChunk.ID)
	assert.Contains(t, byID[dstTextChunk.ID].Content, newRef)
	assert.NotContains(t, byID[dstTextChunk.ID].Content, oldRef)
	assert.Contains(t, byID[dstTextChunk.ID].MatchedContent, newRef,
		"MatchedContent on cloned text chunk must use the rewritten resource:// handle")
	assert.NotContains(t, byID[dstTextChunk.ID].MatchedContent, oldRef,
		"MatchedContent on cloned text chunk must not leak the source resource:// handle")
	assert.Contains(t, byID[dstTextChunk.ID].MatchedContent, "Architecture Runbook")
	assert.Contains(t, byID[dstTextChunk.ID].MatchedContent, "## Network")

	require.Contains(t, byID, dstCaptionChunk.ID)
	assert.Contains(t, byID[dstCaptionChunk.ID].MatchedContent, newRef,
		"MatchedContent on cloned image_caption chunk must use the rewritten resource:// handle")
	assert.NotContains(t, byID[dstCaptionChunk.ID].MatchedContent, oldRef)

	// Verify keyword FTS5 index in target KB matches new handle and never matches old handle when FTS5 is built.
	if f.db.Migrator().HasTable("lite_embeddings_fts") {
		oldHandleToken := strings.TrimPrefix(oldRef, "resource://")
		newHandleToken := strings.TrimPrefix(newRef, "resource://")

		oldKW, err := index.Retrieve(f.ctx, types.RetrieveParams{
			Query:            oldHandleToken,
			TopK:             10,
			KnowledgeBaseIDs: []string{"other"},
			RetrieverType:    types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		require.Len(t, oldKW, 1)
		assert.Empty(t, oldKW[0].Results, "old resource:// handle must not remain in target FTS index")

		newKW, err := index.Retrieve(f.ctx, types.RetrieveParams{
			Query:            newHandleToken,
			TopK:             10,
			KnowledgeBaseIDs: []string{"other"},
			RetrieverType:    types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		require.Len(t, newKW, 1)
		require.NotEmpty(t, newKW[0].Results, "new resource:// handle must be searchable in target FTS index")
	}

	// Verify ChunkTypeImageVector kept its [0, 1] image embedding untouched.
	imgVecHits, err := index.Retrieve(f.ctx, types.RetrieveParams{
		Embedding:        []float32{0, 1},
		TopK:             1,
		KnowledgeBaseIDs: []string{"other"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, imgVecHits, 1)
	require.Len(t, imgVecHits[0].Results, 1)
	assert.Equal(t, dstImageVec.ID, imgVecHits[0].Results[0].ChunkID)
	assert.InDelta(t, 1.0, imgVecHits[0].Results[0].Score, 1e-5,
		"image_vector embedding must remain intact without corruption")

	// Verify generated question row <dstTextChunk.ID>-q1 preserved its question text.
	allHits, err := index.Retrieve(f.ctx, types.RetrieveParams{
		Embedding:        []float32{0.6, 0.8},
		TopK:             10,
		KnowledgeBaseIDs: []string{"other"},
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, allHits, 1)
	var questionHit *types.IndexWithScore
	for _, h := range allHits[0].Results {
		if h.SourceID == dstTextChunk.ID+"-q1" {
			questionHit = h
			break
		}
	}
	require.NotNil(t, questionHit, "copied generated question row must exist")
	assert.Equal(t, "Architecture Runbook\nHow is topology wired?", questionHit.Content)
}

func TestCloneChunkIndexContentFailureRollsBackDestinationAndCanRetry(t *testing.T) {
	registerCloneTagSQLiteVec.Do(sqlitevec.Auto)
	t.Setenv("RETRIEVE_DRIVER", "sqlite")

	f := transferFixture(t, access.KBTransferClone)
	catalog, physical := cloneRollbackStorage(t, f)

	index := sqliteretriever.NewSQLiteRetrieveEngineRepository(f.db)
	f.svc.retrieveEngine = parentChildRetrieveRegistry{
		engine: retriever.NewKVHybridRetrieveEngine(index, types.SQLiteRetrieverEngineType),
	}
	f.svc.modelService = parentChildModelService{embedder: cloneIndexDim2Embedder{}}
	for _, kb := range []*types.KnowledgeBase{f.kbs.values["kb"], f.kbs.values["other"]} {
		kb.EmbeddingModelID = "model"
		kb.IndexingStrategy.VectorEnabled = true
	}

	srcPhysical, err := physical.SaveBytes(f.ctx, []byte("rollback-diagram"), 7, "rollback.png", false)
	require.NoError(t, err)
	oldRef, err := catalog.Register(f.ctx, 7, srcPhysical, interfaces.ResourceRegistration{
		Kind: "image", OriginalName: "rollback.png",
	})
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(
		f.ctx, oldRef, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationExtractedImage,
	))

	srcBody := "Rollback diagram: ![](" + oldRef + ")"
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content":        srcBody,
		"source_content": srcBody,
		"start_at":       0,
		"end_at":         utf8.RuneCountInString(srcBody),
		"image_info":     mustImageInfoJSON(t, []types.ImageInfo{{URL: oldRef, OriginalURL: oldRef}}),
		"is_enabled":     true,
	}).Error)
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
		Update("embedding_model_id", "model").Error)

	require.NoError(t, index.Save(f.ctx, &types.IndexInfo{
		SourceID: "chunk", SourceType: types.ChunkSourceType, ChunkID: "chunk",
		KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: srcBody, IsEnabled: true,
	}, map[string]any{"embedding": map[string][]float32{"chunk": {1, 0}}}))

	require.NoError(t, f.db.Exec(`CREATE TRIGGER reject_clone_content_update
		BEFORE UPDATE OF content ON lite_embeddings
		WHEN NEW.knowledge_base_id = 'other'
		BEGIN SELECT RAISE(ABORT, 'content update rejected'); END`).Error)

	srcDoc, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	err = f.svc.cloneKnowledge(f.ctx, srcDoc, f.kbs.values["other"])
	require.ErrorContains(t, err, "content update rejected")

	failedDst := clonedTagKnowledge(t, f)
	require.Equal(t, types.ParseStatusFailed, failedDst.ParseStatus)
	chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, failedDst.ID)
	require.NoError(t, err)
	assert.Empty(t, chunks, "failed content sync must roll back destination chunks")

	targetHits, err := index.Retrieve(f.ctx, types.RetrieveParams{
		Embedding: []float32{1, 0}, TopK: 10, KnowledgeBaseIDs: []string{"other"},
		RetrieverType: types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, targetHits, 1)
	assert.Empty(t, targetHits[0].Results, "failed content sync must roll back destination indices")

	// Drop trigger and verify retry converges cleanly with rewritten MatchedContent.
	require.NoError(t, f.db.Exec("DROP TRIGGER reject_clone_content_update").Error)
	require.NoError(t, f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil))

	recoveredDst := clonedTagKnowledge(t, f)
	require.Equal(t, types.ParseStatusCompleted, recoveredDst.ParseStatus)
	targetHits, err = index.Retrieve(f.ctx, types.RetrieveParams{
		Embedding: []float32{1, 0}, TopK: 10, KnowledgeBaseIDs: []string{"other"},
		RetrieverType: types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, targetHits, 1)
	require.Len(t, targetHits[0].Results, 1)
	assert.NotContains(t, targetHits[0].Results[0].Content, oldRef)
	assert.Contains(t, targetHits[0].Results[0].Content, "resource://")
}
