package repository

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRepositoryAudit_TagUpdateEnforcesTenantScopeAndCaseInsensitiveSearch(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewKnowledgeTagRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	tagID := uuid.New().String()
	require.NoError(t, repo.Create(ctx, &types.KnowledgeTag{
		ID:              tagID,
		TenantID:        1,
		KnowledgeBaseID: kbID,
		Name:            "OriginalTag",
		Color:           "#112233",
		SortOrder:       1,
	}))

	// nil guard
	require.Error(t, repo.Update(ctx, nil))

	// Cross-tenant Update attempt with TenantID=2 targeting Tenant 1's tag ID must NOT overwrite Tenant 1's row.
	_ = repo.Update(ctx, &types.KnowledgeTag{
		ID:              tagID,
		TenantID:        2,
		KnowledgeBaseID: kbID,
		Name:            "HijackedByTenant2",
		Color:           "#ff0000",
		SortOrder:       99,
	})

	gotT1, err := repo.GetByID(ctx, 1, tagID)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), gotT1.TenantID)
	assert.Equal(t, "OriginalTag", gotT1.Name)
	assert.Equal(t, "#112233", gotT1.Color)
	assert.Equal(t, 1, gotT1.SortOrder)

	_, err = repo.GetByID(ctx, 2, tagID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// Same-tenant Update succeeds.
	gotT1.Name = "ArchitectureSpec"
	gotT1.Color = "#445566"
	gotT1.SortOrder = 5
	require.NoError(t, repo.Update(ctx, gotT1))

	updated, err := repo.GetByID(ctx, 1, tagID)
	require.NoError(t, err)
	assert.Equal(t, "ArchitectureSpec", updated.Name)
	assert.Equal(t, "#445566", updated.Color)
	assert.Equal(t, 5, updated.SortOrder)

	// Case-insensitive keyword search in ListByKB.
	tags, total, err := repo.ListByKB(ctx, 1, kbID, &types.Pagination{Page: 1, PageSize: 10}, "architecturespec")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, tags, 1)
	assert.Equal(t, tagID, tags[0].ID)
}

func TestRepositoryAudit_ChunkUpdateAndDeleteTenantIsolationAndEmptySlices(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()
	chunkID := uuid.New().String()
	require.NoError(t, db.Create(&types.Chunk{
		ID:              chunkID,
		TenantID:        1,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		Content:         "tenant-1-original",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		Status:          int(types.ChunkStatusIndexed),
		TagID:           "tag-1",
	}).Error)

	// Cross-tenant UpdateChunks attempt with TenantID=2 must NOT mutate Tenant 1's chunk.
	require.NoError(t, repo.UpdateChunks(ctx, []*types.Chunk{
		nil,
		{
			ID:        chunkID,
			TenantID:  2,
			Content:   "hijacked-by-tenant-2",
			IsEnabled: false,
			TagID:     "tag-evil",
			Status:    0,
		},
	}))

	got, err := repo.GetChunkByID(ctx, 1, chunkID)
	require.NoError(t, err)
	assert.Equal(t, "tenant-1-original", got.Content)
	assert.True(t, got.IsEnabled)
	assert.Equal(t, "tag-1", got.TagID)

	// Same-tenant UpdateChunks mutates as expected (including boolean false).
	require.NoError(t, repo.UpdateChunks(ctx, []*types.Chunk{
		{
			ID:        chunkID,
			TenantID:  1,
			Content:   "tenant-1-updated",
			IsEnabled: false,
			TagID:     "tag-2",
			Status:    int(types.ChunkStatusIndexed),
		},
	}))
	got, err = repo.GetChunkByID(ctx, 1, chunkID)
	require.NoError(t, err)
	assert.Equal(t, "tenant-1-updated", got.Content)
	assert.False(t, got.IsEnabled)
	assert.Equal(t, "tag-2", got.TagID)

	// Empty-slice guards return cleanly without SQL errors or panics.
	byID, err := repo.ListChunksByID(ctx, 1, nil)
	require.NoError(t, err)
	assert.Empty(t, byID)

	paged, total, err := repo.ListPagedChunksByKnowledgeID(
		ctx, 1, knowledgeID, nil, nil, nil, "", "", "", types.KnowledgeBaseTypeDocument, nil,
	)
	require.NoError(t, err)
	assert.Empty(t, paged)
	assert.Zero(t, total)

	neighbors, err := repo.ListChunkNeighbors(ctx, 1, knowledgeID, 1, 2, 2, nil)
	require.NoError(t, err)
	assert.Empty(t, neighbors)

	imgs, err := repo.ListImageInfoByKnowledgeIDs(ctx, 1, nil)
	require.NoError(t, err)
	assert.Empty(t, imgs)

	require.NoError(t, repo.DeleteByKnowledgeList(ctx, 1, nil))
}

func TestRepositoryAudit_TenantAdjustStorageUsedPreservesChatHistoryConfigAndClamps(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewTenantRepository(db)
	ctx := context.Background()

	tenant := &types.Tenant{
		Name:        "StorageSafeWorkspace",
		Description: "Tests isolated storage_used adjustment",
		Status:      "active",
		StorageUsed: 1024,
	}
	require.NoError(t, repo.CreateTenant(ctx, tenant))
	require.NoError(t, repo.SetChatHistoryConfig(ctx, tenant.ID, &types.ChatHistoryConfig{
		Enabled:         true,
		KnowledgeBaseID: "kb-chat-history-1",
	}))

	// Install a trigger detecting any UPDATE that writes chat_history_config during AdjustStorageUsed.
	require.NoError(t, db.Exec(`
		CREATE TABLE chat_history_clobber_log (cnt INTEGER);
		CREATE TRIGGER detect_chat_history_clobber
		AFTER UPDATE OF chat_history_config ON tenants
		BEGIN
			INSERT INTO chat_history_clobber_log(cnt) VALUES (1);
		END;
	`).Error)

	require.NoError(t, repo.AdjustStorageUsed(ctx, tenant.ID, 512))

	var clobberCount int64
	require.NoError(t, db.Table("chat_history_clobber_log").Count(&clobberCount).Error)
	assert.Zero(t, clobberCount, "AdjustStorageUsed must only update storage_used, never overwrite chat_history_config")

	got, err := repo.GetTenantByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1536), got.StorageUsed)
	require.NotNil(t, got.ChatHistoryConfig)
	assert.True(t, got.ChatHistoryConfig.Enabled)
	assert.Equal(t, "kb-chat-history-1", got.ChatHistoryConfig.KnowledgeBaseID)

	// Negative delta clamps StorageUsed at 0.
	require.NoError(t, repo.AdjustStorageUsed(ctx, tenant.ID, -99999))
	got, err = repo.GetTenantByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Zero(t, got.StorageUsed)

	// Missing tenant returns ErrRecordNotFound.
	require.ErrorIs(t, repo.AdjustStorageUsed(ctx, 9999999, 100), gorm.ErrRecordNotFound)
}

func TestRepositoryAudit_KnowledgeSearchExcludesDeletedKBAndEscapesFileTypeWildcards(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()

	const tenantID = uint64(1)
	activeKB := uuid.New().String()
	deletedKB := uuid.New().String()

	seedLikeEscapeKnowledgeBase(t, db, tenantID, activeKB)
	seedLikeEscapeKnowledgeBase(t, db, tenantID, deletedKB)
	require.NoError(
		t,
		db.Exec(`UPDATE knowledge_bases SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, deletedKB).Error,
	)

	literalExtID := uuid.New().String()
	decoyExtID := uuid.New().String()
	deletedKBDocID := uuid.New().String()

	seedLikeEscapeKnowledge(t, db, tenantID, activeKB, literalExtID, "archive.p_f", "Literal Ext")
	seedLikeEscapeKnowledge(t, db, tenantID, activeKB, decoyExtID, "archive.pdf", "Decoy PDF")
	seedLikeEscapeKnowledge(t, db, tenantID, deletedKB, deletedKBDocID, "orphan.p_f", "Orphan In Deleted KB")

	// SearchKnowledge without fileTypes must exclude documents in soft-deleted KBs.
	allDocs, _, err := repo.SearchKnowledge(ctx, tenantID, "", 0, 20, nil)
	require.NoError(t, err)
	require.Len(t, allDocs, 2, "documents inside soft-deleted KBs must not appear in SearchKnowledge")

	// SearchKnowledge with fileTypes containing '_' must match '.p_f' literally, not '.pdf'.
	extDocs, _, err := repo.SearchKnowledge(ctx, tenantID, "", 0, 20, []string{"p_f"})
	require.NoError(t, err)
	require.Len(t, extDocs, 1, "fileTypes underscore must be escaped in LIKE pattern")
	assert.Equal(t, literalExtID, extDocs[0].ID)

	// SearchKnowledgeInScopes must also exclude soft-deleted KBs and escape fileTypes wildcards.
	scopes := []types.KnowledgeSearchScope{
		{TenantID: tenantID, KBID: activeKB},
		{TenantID: tenantID, KBID: deletedKB},
	}
	scopedAll, _, totalAll, err := repo.SearchKnowledgeInScopes(ctx, scopes, "", 0, 20, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalAll)
	require.Len(t, scopedAll, 2)

	scopedExt, _, totalExt, err := repo.SearchKnowledgeInScopes(ctx, scopes, "", 0, 20, []string{".p_f"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalExt)
	require.Len(t, scopedExt, 1)
	assert.Equal(t, literalExtID, scopedExt[0].ID)

	// Nil/empty guards
	exists, existing, err := repo.CheckKnowledgeExists(ctx, tenantID, activeKB, nil)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, existing)

	require.NoError(t, repo.DeleteKnowledgeList(ctx, tenantID, nil))
	batch, err := repo.GetKnowledgeBatch(ctx, tenantID, nil)
	require.NoError(t, err)
	assert.Empty(t, batch)
}

func TestRepositoryAudit_WikiPageSourceRefQueriesWorkOnSQLiteAndEscapeWildcards(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	require.NoError(t, db.Exec(wikiPagesTestDDL).Error)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	otherKB := uuid.New().String()
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: kbID, TenantID: 1, Name: "kb-1"}).Error)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: otherKB, TenantID: 1, Name: "kb-2"}).Error)

	seedPage := func(kb, slug string, refs types.StringArray, content string) {
		t.Helper()
		require.NoError(t, repo.Create(ctx, &types.WikiPage{
			ID:              uuid.New().String(),
			TenantID:        1,
			KnowledgeBaseID: kb,
			Slug:            slug,
			Title:           slug,
			PageType:        types.WikiPageTypeSummary,
			Status:          types.WikiPageStatusPublished,
			Content:         content,
			SourceRefs:      refs,
		}))
	}

	seedPage(kbID, "exact-underscore", types.StringArray{"doc_1"}, "summary exact doc_1")
	seedPage(kbID, "prefix-underscore", types.StringArray{"doc_1|Design Spec"}, "summary prefix doc_1")
	seedPage(kbID, "decoy-wildcard", types.StringArray{"docX1", "docX1|Decoy"}, "decoy docX1")
	seedPage(kbID, "percent-kid", types.StringArray{"100%|Full Coverage"}, "summary 100%")
	seedPage(kbID, "decoy-percent", types.StringArray{"1000|Thousand"}, "decoy 1000")
	seedPage(otherKB, "other-kb-page", types.StringArray{"doc_1"}, "wrong kb")

	pages, err := repo.ListBySourceRef(ctx, kbID, "doc_1")
	require.NoError(t, err)
	gotSlugs := make([]string, 0, len(pages))
	for _, p := range pages {
		gotSlugs = append(gotSlugs, p.Slug)
	}
	sort.Strings(gotSlugs)
	assert.Equal(t, []string{"exact-underscore", "prefix-underscore"}, gotSlugs)

	slugs, err := repo.ListSlugsBySourceRef(ctx, kbID, "doc_1")
	require.NoError(t, err)
	sort.Strings(slugs)
	assert.Equal(t, []string{"exact-underscore", "prefix-underscore"}, slugs)

	pctSlugs, err := repo.ListSlugsBySourceRef(ctx, kbID, "100%")
	require.NoError(t, err)
	assert.Equal(t, []string{"percent-kid"}, pctSlugs)

	summaries, err := repo.ListSummariesByKnowledgeIDs(ctx, kbID, []string{"doc_1", "100%"})
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	assert.Contains(t, []string{"summary exact doc_1", "summary prefix doc_1"}, summaries["doc_1"])
	assert.Equal(t, "summary 100%", summaries["100%"])

	// Nil request guard on List
	listed, total, err := repo.List(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, listed)
	assert.Zero(t, total)
}

func TestRepositoryAudit_MessageArtifactSessionAndAgentPromptGuards(t *testing.T) {
	db := newArtifactTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.AgentPrompt{}))

	msgRepo := NewMessageRepository(db)
	sessRepo := NewSessionRepository(db)
	promptRepo := NewAgentPromptRepository(db).(*AgentPromptRepository)
	ctx := context.Background()

	// Nil query guards
	items, total, err := msgRepo.ListArtifactLibrary(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Zero(t, total)

	sessions, sTotal, err := sessRepo.QueryPaged(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, sessions)
	assert.Zero(t, sTotal)

	// Seed a web session, message, and artifact with lowercase file_type ".pdf".
	sess := &types.Session{
		ID:       uuid.New().String(),
		TenantID: 1,
		UserID:   "web_user:alice",
		Title:    "Artifact Session",
	}
	require.NoError(t, db.Create(sess).Error)

	msg := &types.Message{
		ID:        uuid.New().String(),
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   "Here is your PDF",
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(msg).Error)
	require.NoError(t, db.Create(&types.MessageArtifactRecord{
		ID:        uuid.New().String(),
		SessionID: sess.ID,
		MessageID: msg.ID,
		Position:  0,
		URL:       "local://1/report.pdf",
		FileName:  "report.pdf",
		FileType:  ".pdf",
		FileSize:  128,
		CreatedAt: time.Now(),
	}).Error)

	// ListArtifactLibrary with uppercase FileTypes (".PDF", ".MD") matches lowercase ".pdf".
	items, total, err = msgRepo.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		TenantID:  1,
		UserID:    "web_user:alice",
		FileTypes: []string{".PDF", ".MD"},
		Page:      1,
		PageSize:  10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "report.pdf", items[0].FileName)

	// GetMessagesBySession with page=0, pageSize=0 clamps page=1 and returns all messages without LIMIT 0.
	msgs, err := msgRepo.GetMessagesBySession(ctx, sess.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, msg.ID, msgs[0].ID)

	// AgentPromptRepository empty statuses guards.
	stale, err := promptRepo.ListStaleLeases(ctx, nil, time.Now(), 10)
	require.NoError(t, err)
	assert.Empty(t, stale)

	bySess, err := promptRepo.ListBySession(ctx, sess.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, bySess)

	byMsgs, err := promptRepo.ListByMessages(ctx, sess.ID, []string{msg.ID}, nil)
	require.NoError(t, err)
	assert.Empty(t, byMsgs)
}
