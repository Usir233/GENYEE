package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/access"
	sqliteretriever "github.com/Usir233/GENYEE/internal/application/repository/retriever/sqlite"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/stretchr/testify/require"
)

var registerCloneTagSQLiteVec sync.Once

func cloneTagFixture(t *testing.T) (*documentWriteFixture, interfaces.RetrieveEngineRepository) {
	t.Helper()
	registerCloneTagSQLiteVec.Do(sqlitevec.Auto)
	t.Setenv("RETRIEVE_DRIVER", "sqlite")
	f := transferFixture(t, access.KBTransferClone)
	index := sqliteretriever.NewSQLiteRetrieveEngineRepository(f.db)
	f.svc.retrieveEngine = parentChildRetrieveRegistry{
		engine: retriever.NewKVHybridRetrieveEngine(index, types.SQLiteRetrieverEngineType),
	}
	f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
	for _, kb := range f.kbs.values {
		kb.EmbeddingModelID = "model"
	}
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").
		Updates(map[string]any{"tag_id": "tag", "is_enabled": true}).Error)
	// Both the body and its generated question must inherit the target tag.
	for _, sourceID := range []string{"chunk", "chunk-question"} {
		require.NoError(t, index.Save(f.ctx, &types.IndexInfo{
			SourceID: sourceID, ChunkID: "chunk", KnowledgeID: "doc", KnowledgeBaseID: "kb",
			TagID: "tag", Content: "cloned searchable content", IsEnabled: true,
		}, map[string]any{"embedding": map[string][]float32{sourceID: {1}}}))
	}
	return f, index
}

func cloneTagHits(t *testing.T, f *documentWriteFixture, index interfaces.RetrieveEngineRepository,
	kbID string, tags ...string,
) []*types.IndexWithScore {
	t.Helper()
	results, err := index.Retrieve(f.ctx, types.RetrieveParams{
		Embedding: []float32{1}, TopK: 10, RetrieverType: types.VectorRetrieverType,
		KnowledgeBaseIDs: []string{kbID}, TagIDs: tags,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0].Results
}

func clonedTagKnowledge(t *testing.T, f *documentWriteFixture) *types.Knowledge {
	t.Helper()
	rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	var cloned []*types.Knowledge
	for _, row := range rows {
		if row.ID != "other-doc" {
			cloned = append(cloned, row)
		}
	}
	require.Len(t, cloned, 1)
	return cloned[0]
}

type cloneTagCreateFailure struct {
	interfaces.KnowledgeTagRepository
}

func (cloneTagCreateFailure) Create(context.Context, *types.KnowledgeTag) error {
	return errors.New("target tag creation unavailable")
}

func TestCloneIndicesUseDestinationTagForRetrieval(t *testing.T) {
	for _, mode := range []string{"create target tag", "reuse target tag", "tag mapping unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f, index := cloneTagFixture(t)
			if mode == "reuse target tag" {
				require.NoError(t, f.db.Create(&types.KnowledgeTag{
					ID: "target-tag", TenantID: 7, KnowledgeBaseID: "other", Name: "tag",
				}).Error)
			}
			if mode == "tag mapping unavailable" {
				f.svc.tagRepo = cloneTagCreateFailure{f.svc.tagRepo}
			}
			source, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.NoError(t, f.svc.cloneKnowledge(f.ctx, source, f.kbs.values["other"]))
			destination := clonedTagKnowledge(t, f)
			require.Equal(t, types.ParseStatusCompleted, destination.ParseStatus)
			chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, destination.ID)
			require.NoError(t, err)
			require.Len(t, chunks, 1)
			targetTag := chunks[0].TagID
			if mode == "tag mapping unavailable" {
				require.Empty(t, targetTag, "best-effort tag creation leaves the cloned chunk untagged")
			} else {
				require.NotEmpty(t, targetTag)
				require.NotEqual(t, "tag", targetTag)
				if mode == "reuse target tag" {
					require.Equal(t, "target-tag", targetTag)
				}
				require.Len(t, cloneTagHits(t, f, index, "other", targetTag), 2,
					"actual vector retrieval by the cloned tag must find both index sources")
			}
			hits := cloneTagHits(t, f, index, "other")
			require.Len(t, hits, 2)
			for _, hit := range hits {
				require.Equal(t, targetTag, hit.TagID, "an empty destination mapping must clear the source tag too")
			}
			require.Empty(t, cloneTagHits(t, f, index, "other", "tag"))
			require.Len(t, cloneTagHits(t, f, index, "kb", "tag"), 2, "source tags remain intact")
		})
	}
}

func TestCloneIndexTagFailureCleansDestinationAndCanRetry(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		name := "cleanup succeeds"
		if cleanupFails {
			name = "cleanup fails visibly"
		}
		t.Run(name, func(t *testing.T) {
			f, index := cloneTagFixture(t)
			require.NoError(t, f.db.Exec(`CREATE TRIGGER reject_clone_tag BEFORE UPDATE OF tag_id ON lite_embeddings
				WHEN NEW.knowledge_base_id = 'other' BEGIN SELECT RAISE(ABORT, 'tag update rejected'); END`).Error)
			if cleanupFails {
				require.NoError(t, f.db.Exec(`CREATE TRIGGER reject_clone_cleanup BEFORE DELETE ON lite_embeddings
					WHEN OLD.knowledge_base_id = 'other' BEGIN SELECT RAISE(ABORT, 'cleanup rejected'); END`).Error)
			}
			source, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			err = f.svc.cloneKnowledge(f.ctx, source, f.kbs.values["other"])
			require.ErrorContains(t, err, "tag update rejected")
			destination := clonedTagKnowledge(t, f)
			require.Equal(t, types.ParseStatusFailed, destination.ParseStatus)
			chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, destination.ID)
			require.NoError(t, err)
			if cleanupFails {
				require.Contains(t, destination.ErrorMessage, "cleanup rejected")
				require.Len(t, chunks, 1, "uncertain cleanup retains referenced chunks")
				require.Len(t, cloneTagHits(t, f, index, "other"), 2)
				require.NoError(t, f.db.Exec("DROP TRIGGER reject_clone_cleanup").Error)
			} else {
				require.Empty(t, chunks)
				require.Empty(t, cloneTagHits(t, f, index, "other"))
			}
			require.Len(t, cloneTagHits(t, f, index, "kb", "tag"), 2)
			require.NoError(t, f.db.Exec("DROP TRIGGER reject_clone_tag").Error)
			// The normal task retry removes the failed clone and copies it again.
			// A second delivery of the completed task must not duplicate its rows.
			for range 2 {
				require.NoError(t, f.svc.executeKnowledgeClone(f.ctx, f.kbs.values["kb"], f.kbs.values["other"], nil))
				destination = clonedTagKnowledge(t, f)
				require.Equal(t, types.ParseStatusCompleted, destination.ParseStatus)
				hits := cloneTagHits(t, f, index, "other")
				require.Len(t, hits, 2)
				require.NotEmpty(t, hits[0].TagID)
				require.Len(t, cloneTagHits(t, f, index, "other", hits[0].TagID), 2)
			}
			require.Len(t, cloneTagHits(t, f, index, "kb", "tag"), 2)
		})
	}
}
