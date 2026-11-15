package milvus

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"sync"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type w4MilvusServer struct {
	milvuspb.UnimplementedMilvusServiceServer

	mu            sync.Mutex
	collections   []string
	hasCollection func(name string) (bool, error)
	deleteReqs    []*milvuspb.DeleteRequest
	searchReqs    []*milvuspb.SearchRequest
	queryErr      error
	upsertErr     error
	searchFn      func(req *milvuspb.SearchRequest) (*milvuspb.SearchResults, error)
}

func (s *w4MilvusServer) ShowCollections(
	_ context.Context, _ *milvuspb.ShowCollectionsRequest,
) (*milvuspb.ShowCollectionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &milvuspb.ShowCollectionsResponse{
		Status:          &commonpb.Status{},
		CollectionNames: append([]string(nil), s.collections...),
	}, nil
}

func (s *w4MilvusServer) HasCollection(
	_ context.Context, req *milvuspb.HasCollectionRequest,
) (*milvuspb.BoolResponse, error) {
	if s.hasCollection != nil {
		ok, err := s.hasCollection(req.GetCollectionName())
		if err != nil {
			return nil, err
		}
		return &milvuspb.BoolResponse{Status: &commonpb.Status{}, Value: ok}, nil
	}
	return &milvuspb.BoolResponse{Status: &commonpb.Status{}, Value: true}, nil
}

func (s *w4MilvusServer) DescribeCollection(
	_ context.Context, req *milvuspb.DescribeCollectionRequest,
) (*milvuspb.DescribeCollectionResponse, error) {
	if s.hasCollection != nil {
		ok, err := s.hasCollection(req.GetCollectionName())
		if err != nil {
			return nil, err
		}
		if !ok {
			return &milvuspb.DescribeCollectionResponse{
				Status: &commonpb.Status{
					Code:      100,
					ErrorCode: commonpb.ErrorCode_CollectionNotExists,
					Reason:    "collection not found",
				},
			}, nil
		}
	}
	return &milvuspb.DescribeCollectionResponse{
		Status: &commonpb.Status{},
		Schema: &schemapb.CollectionSchema{
			Name: req.GetCollectionName(),
			Fields: []*schemapb.FieldSchema{
				{FieldID: 100, Name: fieldID, DataType: schemapb.DataType_VarChar, IsPrimaryKey: true},
				{FieldID: 101, Name: fieldContent, DataType: schemapb.DataType_VarChar, TypeParams: []*commonpb.KeyValuePair{{Key: "multi_analyzer_params", Value: `{}`}}},
				{FieldID: 103, Name: fieldLanguage, DataType: schemapb.DataType_VarChar},
				{FieldID: 102, Name: fieldContentSparse, DataType: schemapb.DataType_SparseFloatVector},
				{FieldID: 104, Name: fieldSourceID, DataType: schemapb.DataType_VarChar},
				{FieldID: 105, Name: fieldSourceType, DataType: schemapb.DataType_Int64},
				{FieldID: 106, Name: fieldChunkID, DataType: schemapb.DataType_VarChar},
				{FieldID: 107, Name: fieldKnowledgeID, DataType: schemapb.DataType_VarChar},
				{FieldID: 108, Name: fieldKnowledgeBaseID, DataType: schemapb.DataType_VarChar},
				{FieldID: 109, Name: fieldTagID, DataType: schemapb.DataType_VarChar},
				{FieldID: 110, Name: fieldIsEnabled, DataType: schemapb.DataType_Bool},
				{FieldID: 111, Name: fieldEmbedding, DataType: schemapb.DataType_FloatVector, TypeParams: []*commonpb.KeyValuePair{{Key: "dim", Value: "2"}}},
			},
		},
	}, nil
}

func (s *w4MilvusServer) Delete(
	_ context.Context, req *milvuspb.DeleteRequest,
) (*milvuspb.MutationResult, error) {
	s.mu.Lock()
	s.deleteReqs = append(s.deleteReqs, req)
	s.mu.Unlock()
	return &milvuspb.MutationResult{Status: &commonpb.Status{}}, nil
}

func (s *w4MilvusServer) Query(
	_ context.Context, _ *milvuspb.QueryRequest,
) (*milvuspb.QueryResults, error) {
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	return &milvuspb.QueryResults{
		Status: &commonpb.Status{},
		FieldsData: []*schemapb.FieldData{
			{
				FieldName: fieldID,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"row-1"}},
						},
					},
				},
			},
			{
				FieldName: fieldChunkID,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"chunk-1"}},
						},
					},
				},
			},
			{
				FieldName: fieldKnowledgeID,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"know-1"}},
						},
					},
				},
			},
			{
				FieldName: fieldKnowledgeBaseID,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"kb-1"}},
						},
					},
				},
			},
			{
				FieldName: fieldTagID,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"old-tag"}},
						},
					},
				},
			},
			{
				FieldName: fieldContent,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"body"}},
						},
					},
				},
			},
			{
				FieldName: fieldLanguage,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{""}},
						},
					},
				},
			},
			{
				FieldName: fieldSourceID,
				Type:      schemapb.DataType_VarChar,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_StringData{
							StringData: &schemapb.StringArray{Data: []string{"chunk-1"}},
						},
					},
				},
			},
			{
				FieldName: fieldSourceType,
				Type:      schemapb.DataType_Int64,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_LongData{
							LongData: &schemapb.LongArray{Data: []int64{0}},
						},
					},
				},
			},
			{
				FieldName: fieldIsEnabled,
				Type:      schemapb.DataType_Bool,
				Field: &schemapb.FieldData_Scalars{
					Scalars: &schemapb.ScalarField{
						Data: &schemapb.ScalarField_BoolData{
							BoolData: &schemapb.BoolArray{Data: []bool{true}},
						},
					},
				},
			},
			{
				FieldName: fieldEmbedding,
				Type:      schemapb.DataType_FloatVector,
				Field: &schemapb.FieldData_Vectors{
					Vectors: &schemapb.VectorField{
						Dim: 2,
						Data: &schemapb.VectorField_FloatVector{
							FloatVector: &schemapb.FloatArray{Data: []float32{0.6, 0.8}},
						},
					},
				},
			},
		},
	}, nil
}

func (s *w4MilvusServer) Upsert(
	_ context.Context, _ *milvuspb.UpsertRequest,
) (*milvuspb.MutationResult, error) {
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	return &milvuspb.MutationResult{Status: &commonpb.Status{}}, nil
}

func (s *w4MilvusServer) Search(
	_ context.Context, req *milvuspb.SearchRequest,
) (*milvuspb.SearchResults, error) {
	s.mu.Lock()
	s.searchReqs = append(s.searchReqs, req)
	s.mu.Unlock()
	if s.searchFn != nil {
		return s.searchFn(req)
	}
	return &milvuspb.SearchResults{
		Status:  &commonpb.Status{},
		Results: &schemapb.SearchResultData{NumQueries: 1, Topks: []int64{0}},
	}, nil
}

func newW4MilvusRepo(t *testing.T, srv *w4MilvusServer) *milvusRepository {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer()
	milvuspb.RegisterMilvusServiceServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	c, err := client.New(context.Background(), &client.ClientConfig{
		Address:     lis.Addr().String(),
		DisableConn: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	return &milvusRepository{
		client:             c,
		collectionBaseName: "genyee_embeddings",
		metricType:         "IP",
	}
}

func TestW4MilvusAudit_DeleteEscapesQuotesBackslashesAndRejectsControlChars(t *testing.T) {
	srv := &w4MilvusServer{}
	repo := newW4MilvusRepo(t, srv)
	ctx := context.Background()

	payload := `id" ] || knowledge_base_id != "" || chunk_id in ["x\y`
	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{payload}, 4, ""))
	require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{`know"1`}, 4, ""))
	require.NoError(t, repo.DeleteBySourceIDList(ctx, []string{`src\1`}, 4, ""))

	require.Len(t, srv.deleteReqs, 3)
	assert.Equal(t, `chunk_id in ["id\" ] || knowledge_base_id != \"\" || chunk_id in [\"x\\y"]`, srv.deleteReqs[0].GetExpr())
	assert.Equal(t, `knowledge_id in ["know\"1"]`, srv.deleteReqs[1].GetExpr())
	assert.Equal(t, `source_id in ["src\\1"]`, srv.deleteReqs[2].GetExpr())

	// Control characters (such as NUL or newline) must be rejected rather than interpolated.
	require.Error(t, repo.DeleteByChunkIDList(ctx, []string{"bad\x00id"}, 4, ""))
	require.Error(t, repo.DeleteByKnowledgeIDList(ctx, []string{"bad\nid"}, 4, ""))
	require.Error(t, repo.DeleteBySourceIDList(ctx, []string{"bad\rid"}, 4, ""))
	require.Error(t, repo.DeleteByChunkIDList(ctx, []string{"valid"}, 0, ""))

	// Non-existent collection on delete must be a clean no-op without issuing Delete RPC.
	missingSrv := &w4MilvusServer{
		hasCollection: func(string) (bool, error) { return false, nil },
	}
	missingRepo := newW4MilvusRepo(t, missingSrv)
	require.NoError(t, missingRepo.DeleteByChunkIDList(ctx, []string{"c1"}, 8, ""))
	assert.Empty(t, missingSrv.deleteReqs)
}

func TestW4MilvusAudit_FilterRejectsDisallowedFieldInjection(t *testing.T) {
	f := &filter{}

	var injected universalFilterCondition
	require.NoError(t, json.Unmarshal(
		[]byte(`{"field":"is_enabled == true || chunk_id","operator":"=","value":"x"}`),
		&injected,
	))
	_, err := f.Convert(&injected)
	require.Error(t, err, "injected field name must be rejected")

	var injectedIn universalFilterCondition
	require.NoError(t, json.Unmarshal(
		[]byte(`{"field":"chunk_id in [\"a\"] || knowledge_base_id","operator":"in","value":["kb"]}`),
		&injectedIn,
	))
	_, err = f.Convert(&injectedIn)
	require.Error(t, err)

	// Legitimate schema fields continue to convert cleanly using parameterized placeholders.
	res, err := f.Convert(&universalFilterCondition{
		Field:    fieldKnowledgeBaseID,
		Operator: "in",
		Value:    []string{`kb" || 1==1`},
	})
	require.NoError(t, err)
	assert.Equal(t, "knowledge_base_id in {knowledge_base_id_1}", res.exprStr)
	assert.Equal(t, []string{`kb" || 1==1`}, res.params["knowledge_base_id_1"])
}

func TestW4MilvusAudit_BatchUpdateChunkTagIDPropagatesErrors(t *testing.T) {
	ctx := context.Background()

	upsertBoom := errors.New("milvus upsert rejected")
	srv := &w4MilvusServer{
		collections: []string{"genyee_embeddings_2"},
		upsertErr:   upsertBoom,
	}
	repo := newW4MilvusRepo(t, srv)
	err := repo.BatchUpdateChunkTagID(ctx, map[string]string{"chunk-1": "new-tag"})
	require.ErrorContains(t, err, upsertBoom.Error(), "BatchUpdateChunkTagID must not swallow Upsert failures")

	queryBoom := errors.New("milvus query failed")
	srvQuery := &w4MilvusServer{
		collections: []string{"genyee_embeddings_2"},
		queryErr:    queryBoom,
	}
	repoQuery := newW4MilvusRepo(t, srvQuery)
	err = repoQuery.BatchUpdateChunkTagID(ctx, map[string]string{"chunk-1": "new-tag"})
	require.ErrorContains(t, err, queryBoom.Error(), "BatchUpdateChunkTagID must not swallow Query failures")
}

func TestW4MilvusAudit_CollectionNameStrictDigitsAndRetrieveWriteGuards(t *testing.T) {
	ctx := context.Background()

	// Non-canonical collection suffixes (+dim, leading zeros) must be rejected.
	for _, bad := range []string{"genyee_embeddings_+768", "genyee_embeddings_0768", "genyee_embeddings_-4"} {
		_, ok := collectionDimensionForBase(bad, "genyee_embeddings")
		assert.False(t, ok, "non-canonical collection %q must be rejected", bad)
	}
	dim, ok := collectionDimensionForBase("genyee_embeddings_768", "genyee_embeddings")
	require.True(t, ok)
	assert.Equal(t, 768, dim)

	srv := &w4MilvusServer{
		collections: []string{"genyee_embeddings_2"},
		searchFn: func(req *milvuspb.SearchRequest) (*milvuspb.SearchResults, error) {
			return &milvuspb.SearchResults{
				Status: &commonpb.Status{},
				Results: &schemapb.SearchResultData{
					NumQueries: 1,
					TopK:       2,
					Topks:      []int64{2},
					Scores:     []float32{0.95, 0.80},
					FieldsData: []*schemapb.FieldData{
						{
							FieldName: fieldID, Type: schemapb.DataType_VarChar,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_StringData{StringData: &schemapb.StringArray{Data: []string{"r1", "r2"}}}}},
						},
						{
							FieldName: fieldChunkID, Type: schemapb.DataType_VarChar,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_StringData{StringData: &schemapb.StringArray{Data: []string{"chunk-1", "chunk-1"}}}}},
						},
						{
							FieldName: fieldKnowledgeID, Type: schemapb.DataType_VarChar,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_StringData{StringData: &schemapb.StringArray{Data: []string{"doc-1", "doc-1"}}}}},
						},
						{
							FieldName: fieldKnowledgeBaseID, Type: schemapb.DataType_VarChar,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_StringData{StringData: &schemapb.StringArray{Data: []string{"kb-off", "kb-off"}}}}},
						},
						{
							FieldName: fieldSourceID, Type: schemapb.DataType_VarChar,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_StringData{StringData: &schemapb.StringArray{Data: []string{"chunk-1-q1", "chunk-1"}}}}},
						},
						{
							FieldName: fieldSourceType, Type: schemapb.DataType_Int64,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_LongData{LongData: &schemapb.LongArray{Data: []int64{0, 0}}}}},
						},
						{
							FieldName: fieldContent, Type: schemapb.DataType_VarChar,
							Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{Data: &schemapb.ScalarField_StringData{StringData: &schemapb.StringArray{Data: []string{"generated question?", "body text"}}}}},
						},
					},
				},
			}, nil
		},
	}
	repo := newW4MilvusRepo(t, srv)
	repo.initializedCollections.Store(2, true)

	// Explicit empty KnowledgeBaseIDs ([]string{}) MUST fail closed without searching collections.
	res, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{0.5, 0.5},
		TopK:             5,
		KnowledgeBaseIDs: []string{},
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Empty(t, res[0].Results)
	assert.Empty(t, srv.searchReqs)

	res, err = repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "body",
		TopK:             5,
		KnowledgeBaseIDs: []string{},
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Empty(t, res[0].Results)
	assert.Empty(t, srv.searchReqs)

	// TopK <= 0, empty Embedding, and blank Query must return empty without RPCs.
	res, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{0.5, 0.5},
		TopK:             -1,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	assert.Empty(t, res[0].Results)
	assert.Empty(t, srv.searchReqs)

	res, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        nil,
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	assert.Empty(t, res[0].Results)
	assert.Empty(t, srv.searchReqs)

	res, err = repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "   ",
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	assert.Empty(t, res[0].Results)
	assert.Empty(t, srv.searchReqs)

	// NaN / Inf embeddings must be rejected on Save, BatchSave, and VectorRetrieve.
	_, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{float32(math.NaN()), 1.0},
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-off"},
	})
	require.Error(t, err)

	require.Error(t, repo.Save(ctx, nil, nil))
	require.Error(t, repo.Save(ctx, &types.IndexInfo{ChunkID: "c1", SourceID: "c1"}, map[string]any{
		"embedding": map[string][]float32{"c1": {float32(math.Inf(1)), 0}},
	}))
	require.Error(t, repo.BatchSave(ctx, []*types.IndexInfo{nil}, nil))
	assert.Equal(t, int64(0), repo.EstimateStorageSize(ctx, []*types.IndexInfo{nil}, nil))

	// MoveKnowledgeIndices must reject empty KB/knowledge IDs or non-positive dimension.
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "", "kb-dst", "doc-1", nil, 2, ""))
	require.Error(t, repo.MoveKnowledgeIndices(ctx, "kb-src", "kb-dst", "doc-1", nil, 0, ""))

	// ExcludeGeneratedQuestionKBIDs must drop generated-question rows before TopK truncation.
	res, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:                     []float32{0.5, 0.5},
		TopK:                          1,
		KnowledgeBaseIDs:              []string{"kb-off"},
		ExcludeGeneratedQuestionKBIDs: []string{"kb-off"},
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Len(t, res[0].Results, 1)
	assert.Equal(t, "chunk-1", res[0].Results[0].SourceID)
}
