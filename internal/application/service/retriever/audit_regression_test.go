package retriever

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type batchNoopEngine struct {
	fakeEngine
}

func (b *batchNoopEngine) BatchIndex(_ context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType) error {
	return nil
}

func (b *batchNoopEngine) BatchUpdateChunkEnabledStatus(_ context.Context, _ map[string]bool) error {
	return nil
}

func TestFactoryAndOwnership_FailClosedOnZeroTenantBlankStoreNilDepsAndTypedNilTenantInfo(t *testing.T) {
	ctx := context.Background()
	esEngine := &fakeEngine{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	}
	registry := registryWithStores(t, map[string]*fakeEngine{"store-A": esEngine, "   ": esEngine}, nil)
	ownership := &fakeOwnership{owned: map[string]uint64{"store-A": 0, "   ": 1}}

	err := VerifyBinding(ctx, registry, ownership, 0, "store-A")
	assert.ErrorIs(t, err, ErrVectorStoreForbidden, "zero tenantID must be rejected fail-closed")

	err = VerifyBinding(ctx, registry, ownership, 1, "   ")
	assert.ErrorIs(t, err, ErrVectorStoreForbidden, "blank storeID must be rejected fail-closed")

	storeID := "store-A"
	_, err = CreateRetrieveEngineForKB(ctx, registry, ownership, 0, &storeID)
	assert.ErrorIs(t, err, ErrVectorStoreForbidden, "zero tenantID must be rejected in resolveBoundEngine")

	blankStore := "   "
	_, err = CreateRetrieveEngineForKB(ctx, registry, ownership, 1, &blankStore)
	assert.ErrorIs(t, err, ErrVectorStoreForbidden, "whitespace storeID must not bypass unbound normalization into GetOrLoad")

	ownedBy1 := &fakeOwnership{owned: map[string]uint64{"store-A": 1}}
	require.NotPanics(t, func() {
		assert.ErrorIs(t, VerifyBinding(ctx, nil, ownership, 1, "store-A"), ErrVectorStoreForbidden,
			"unowned store must return ErrVectorStoreForbidden even when registry is nil")
		assert.ErrorIs(t, VerifyBinding(ctx, nil, ownedBy1, 1, "store-A"), ErrVectorStoreUnavailable,
			"owned store with nil registry must return ErrVectorStoreUnavailable")
		assert.ErrorIs(t, VerifyBinding(ctx, registry, nil, 1, "store-A"), ErrVectorStoreUnavailable)
	})

	typedNilTenantCtx := context.WithValue(ctx, types.TenantInfoContextKey, (*types.Tenant)(nil))
	require.NotPanics(t, func() {
		_, err = CreateRetrieveEngineForKB(typedNilTenantCtx, registry, ownership, 1, nil)
		assert.ErrorIs(t, err, ErrTenantInfoMissing)
	})

	nilRepoOwnership := NewVectorStoreRepoOwnership(nil)
	require.NotPanics(t, func() {
		owned, oerr := nilRepoOwnership.StoreOwnedBy(ctx, "store-A", 1)
		assert.False(t, owned)
		assert.ErrorIs(t, oerr, ErrVectorStoreUnavailable)

		owned, oerr = nilRepoOwnership.StoreOwnedBy(ctx, "store-A", 0)
		assert.False(t, owned)
		assert.NoError(t, oerr)
	})
}

func TestCompositeRetrieveEngine_NilSafetyOnNilEntriesAndBatchIndex(t *testing.T) {
	ctx := context.Background()
	good := &batchNoopEngine{
		fakeEngine: fakeEngine{
			engineType: types.PostgresRetrieverEngineType,
			support:    []types.RetrieverType{types.VectorRetrieverType},
		},
	}
	c := &CompositeRetrieveEngine{
		engineInfos: []*engineInfo{
			nil,
			{retrieveEngine: nil, retrieverType: []types.RetrieverType{types.VectorRetrieverType}},
			{retrieveEngine: good, retrieverType: []types.RetrieverType{types.VectorRetrieverType}},
		},
	}

	require.NotPanics(t, func() {
		assert.NoError(t, c.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{"c1": true}))
	})

	require.NotPanics(t, func() {
		assert.Error(t, c.ValidateKnowledgeIndexMove(ctx))
	})

	require.NotPanics(t, func() {
		assert.NoError(t, c.BatchIndex(ctx, nil, []*types.IndexInfo{nil, {SourceID: "s1"}}))
	})

	require.NotPanics(t, func() {
		assert.Error(t, c.BatchIndexVectors(ctx, nil, []*types.IndexInfo{nil}, [][]float32{{1}}))
	})
}
