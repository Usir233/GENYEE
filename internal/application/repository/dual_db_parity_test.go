package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openParitySQLiteDB(t *testing.T, fileBacked bool) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	if fileBacked {
		dsn = filepath.Join(t.TempDir(), "parity.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	}
	db, err := gorm.Open(gormsqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestSQLiteEmptyAndMalformedJSONColumnsDoNotCrash(t *testing.T) {
	ctx := context.Background()
	db := openParitySQLiteDB(t, false)

	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			tag_id TEXT DEFAULT '',
			type TEXT DEFAULT 'file',
			title TEXT DEFAULT '',
			description TEXT DEFAULT '',
			source TEXT DEFAULT '',
			parse_status TEXT DEFAULT 'completed',
			summary_status TEXT DEFAULT '',
			enable_status TEXT DEFAULT 'enabled',
			embedding_model_id TEXT DEFAULT '',
			file_name TEXT DEFAULT '',
			file_type TEXT DEFAULT '',
			file_size INTEGER DEFAULT 0,
			file_hash TEXT DEFAULT '',
			file_path TEXT DEFAULT '',
			folder_path TEXT DEFAULT '',
			storage_size INTEGER DEFAULT 0,
			metadata TEXT DEFAULT '',
			pending_subtasks_count INTEGER DEFAULT 0,
			created_at DATETIME,
			updated_at DATETIME,
			processed_at DATETIME,
			error_message TEXT DEFAULT '',
			deleted_at DATETIME
		);
		CREATE TABLE knowledge_bases (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			name TEXT DEFAULT '',
			embedding_model_id TEXT DEFAULT '',
			summary_model_id TEXT DEFAULT '',
			image_processing_config TEXT DEFAULT '',
			vlm_config TEXT DEFAULT '',
			asr_config TEXT DEFAULT '',
			wiki_config TEXT DEFAULT '',
			auto_tag_config TEXT DEFAULT '',
			question_generation_config TEXT DEFAULT '',
			deleted_at DATETIME
		);
		CREATE TABLE custom_agents (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			name TEXT DEFAULT '',
			config TEXT DEFAULT '',
			deleted_at DATETIME
		);
		CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			seq_id INTEGER DEFAULT 0,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			knowledge_id TEXT NOT NULL,
			tag_id TEXT DEFAULT '',
			content TEXT DEFAULT '',
			chunk_index INTEGER DEFAULT 0,
			is_enabled BOOLEAN DEFAULT 1,
			flags INTEGER DEFAULT 1,
			status INTEGER DEFAULT 0,
			start_at INTEGER DEFAULT 0,
			end_at INTEGER DEFAULT 0,
			pre_chunk_id TEXT DEFAULT '',
			next_chunk_id TEXT DEFAULT '',
			chunk_type TEXT DEFAULT 'text',
			parent_chunk_id TEXT DEFAULT '',
			relation_chunks TEXT DEFAULT '',
			indirect_relation_chunks TEXT DEFAULT '',
			metadata TEXT DEFAULT '',
			source_locators TEXT DEFAULT '',
			content_hash TEXT DEFAULT '',
			image_info TEXT DEFAULT '',
			video_info TEXT DEFAULT '',
			content_revision INTEGER DEFAULT 1,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE mcp_metadata (
			tenant_id INTEGER NOT NULL,
			service_id TEXT NOT NULL,
			principal TEXT NOT NULL,
			config_fingerprint TEXT DEFAULT '',
			tools TEXT DEFAULT '',
			instructions TEXT DEFAULT '',
			server_name TEXT DEFAULT '',
			server_version TEXT DEFAULT '',
			server_description TEXT DEFAULT '',
			synced_at DATETIME
		);
		CREATE TABLE wiki_pages (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			slug TEXT NOT NULL,
			title TEXT DEFAULT '',
			page_type TEXT DEFAULT 'concept',
			status TEXT DEFAULT 'published',
			content TEXT DEFAULT '',
			summary TEXT DEFAULT '',
			aliases TEXT DEFAULT '',
			source_refs TEXT DEFAULT '',
			out_links TEXT DEFAULT '',
			in_links TEXT DEFAULT '',
			folder_id TEXT,
			parent_slug TEXT DEFAULT '',
			category_path TEXT DEFAULT '',
			wiki_path TEXT DEFAULT '',
			depth INTEGER DEFAULT 0,
			sort_order INTEGER DEFAULT 0,
			version INTEGER DEFAULT 1,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			attachments TEXT DEFAULT '',
			deleted_at DATETIME
		);
		CREATE TABLE temporary_documents (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			session_id TEXT NOT NULL,
			file_name TEXT DEFAULT '',
			file_type TEXT DEFAULT '',
			file_size INTEGER DEFAULT 0,
			storage_path TEXT DEFAULT '',
			status TEXT DEFAULT 'ready',
			content TEXT DEFAULT '',
			chunks TEXT DEFAULT '',
			image_refs TEXT DEFAULT '',
			metadata TEXT DEFAULT '',
			token_count INTEGER DEFAULT 0,
			chunk_count INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '',
			started_at DATETIME,
			ready_at DATETIME,
			expires_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE chunk_images (
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			knowledge_id TEXT NOT NULL,
			chunk_id TEXT NOT NULL,
			image_index INTEGER NOT NULL,
			image_key TEXT NOT NULL,
			chunk_type TEXT NOT NULL,
			attrs TEXT DEFAULT ''
		);
	`).Error)

	// Seed rows with empty string "" and malformed JSON alongside valid rows.
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, file_hash, metadata)
		VALUES
			('k-empty', 1, 'kb-1', 'file', 'h1', ''),
			('k-bad', 1, 'kb-1', 'file', 'h2', '{not-json'),
			('k-good', 1, 'kb-1', 'file', 'h3', '{"datasource_id":"ds-1","external_id":"ext/1"}');
	`).Error)

	kgRepo := NewKnowledgeRepository(db)
	exists, gotKg, err := kgRepo.CheckKnowledgeExists(ctx, 1, "kb-1", &types.KnowledgeCheckParams{
		Type:         "file",
		DataSourceID: "ds-1",
		ExternalID:   "ext/1",
		FileHash:     "h3",
	})
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "k-good", gotKg.ID)

	byKey, err := kgRepo.FindByMetadataKey(ctx, 1, "kb-1", "external_id", "ext/1")
	require.NoError(t, err)
	require.NotNil(t, byKey)
	require.Equal(t, "k-good", byKey.ID)

	byPrefix, err := kgRepo.FindByMetadataKeyPrefix(ctx, 1, "kb-1", "external_id", "ext/")
	require.NoError(t, err)
	require.Len(t, byPrefix, 1)

	byDSExt, err := kgRepo.FindByDataSourceExternalID(ctx, 1, "kb-1", "ds-1", "ext/1")
	require.NoError(t, err)
	require.NotNil(t, byDSExt)
	require.Equal(t, "k-good", byDSExt.ID)

	// model_usage.go with empty-string JSON columns
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_bases (id, tenant_id, vlm_config)
		VALUES ('kb-empty', 1, ''), ('kb-match', 1, '{"model_id":"m-1"}');
		INSERT INTO custom_agents (id, tenant_id, config)
		VALUES ('ag-empty', 1, ''), ('ag-match', 1, '{"model_id":"m-1","sandbox_config_id":"sb-1"}');
	`).Error)
	var kbIDs []string
	require.NoError(t, scopeKnowledgeBasesByModelID(db.Table("knowledge_bases"), "m-1").Pluck("id", &kbIDs).Error)
	require.Equal(t, []string{"kb-match"}, kbIDs)

	var agentIDs []string
	require.NoError(t, scopeCustomAgentsByModelID(db.Table("custom_agents"), "m-1").Pluck("id", &agentIDs).Error)
	require.Equal(t, []string{"ag-match"}, agentIDs)

	var sbAgentIDs []string
	require.NoError(
		t,
		scopeCustomAgentsBySandboxConfigID(db.Table("custom_agents"), "sb-1").Pluck("id", &sbAgentIDs).Error,
	)
	require.Equal(t, []string{"ag-match"}, sbAgentIDs)

	// chunk.go with empty-string metadata
	require.NoError(t, db.Exec(`
		INSERT INTO chunks (
			id, tenant_id, knowledge_base_id, knowledge_id, chunk_type, status, is_enabled, content, metadata
		)
		VALUES
			('c-empty', 1, 'kb-1', 'k-good', 'faq', 0, 1, 'empty', ''),
			('c-faq', 1, 'kb-1', 'k-good', 'faq', 0, 1, 'billing answer',
				'{"standard_question":"How to pay?","similar_questions":["Payment method"],"answers":["Use card"]}'),
			('c-doc-empty', 1, 'kb-1', 'k-good', 'text', 0, 1, 'doc', ''),
			('c-doc-q', 1, 'kb-1', 'k-good', 'text', 0, 1, 'doc q', '{"generated_questions":["What is GENYEE?"]}');
	`).Error)
	chunkRepo := NewChunkRepository(db)
	for _, field := range []string{"standard_question", "similar_questions", "answers", ""} {
		_, total, listErr := chunkRepo.ListPagedChunksByKnowledgeID(
			ctx, 1, "k-good", &types.Pagination{Page: 1, PageSize: 10},
			[]types.ChunkType{types.ChunkTypeFAQ}, nil, "pay", field, "", types.KnowledgeTypeFAQ, nil,
		)
		require.NoError(t, listErr, "field=%s", field)
		require.GreaterOrEqual(t, total, int64(0))
	}
	faqHit, err := chunkRepo.FindFAQChunkWithDuplicateQuestion(ctx, 1, "kb-1", "", []string{"Payment method"})
	require.NoError(t, err)
	require.NotNil(t, faqHit)
	require.Equal(t, "c-faq", faqHit.ID)

	docWithQ, err := chunkRepo.ListRecentDocumentChunksWithQuestions(ctx, 1, []string{"kb-1"}, nil, 10)
	require.NoError(t, err)
	require.Len(t, docWithQ, 1)
	require.Equal(t, "c-doc-q", docWithQ[0].ID)

	// mcp_metadata.go with empty tools
	require.NoError(t, db.Exec(`
		INSERT INTO mcp_metadata (tenant_id, service_id, principal, tools)
		VALUES (1, 'svc-empty', 'u1', ''), (1, 'svc-ok', 'u1', '[{"name":"t1"}]');
	`).Error)
	mcpRepo := &mcpServiceRepository{db: db}
	summaries, err := mcpRepo.ListMetadataSummaries(ctx, 1, []string{"u1"})
	require.NoError(t, err)
	require.Len(t, summaries, 2)

	// wiki_page.go with empty category_path and in_links
	require.NoError(t, db.Exec(`
		INSERT INTO wiki_pages (id, tenant_id, knowledge_base_id, slug, title, category_path, in_links)
		VALUES ('wp-1', 1, 'kb-1', 'intro', 'Intro', '', '');
	`).Error)
	wikiRepo := NewWikiPageRepository(db).(*wikiPageRepository)
	pages, _, err := wikiRepo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: "kb-1",
		SortBy:          "wiki_path",
		Page:            1,
		PageSize:        10,
	})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	var orphanCount int64
	require.NoError(t, db.Table("wiki_pages").Where(wikiRepo.wikiEmptyInLinksPredicate()).Count(&orphanCount).Error)
	require.Equal(t, int64(1), orphanCount)

	// temporary_document.go with empty attachments on messages
	past := time.Now().Add(-time.Hour)
	require.NoError(t, db.Exec(`
		INSERT INTO sessions (id, tenant_id) VALUES ('sess-1', 1);
		INSERT INTO messages (id, session_id, role, attachments)
		VALUES ('m-empty', 'sess-1', 'user', ''), ('m-ref', 'sess-1', 'user', '[{"id":"td-keep"}]');
		INSERT INTO temporary_documents (id, tenant_id, session_id, expires_at)
		VALUES ('td-keep', 1, 'sess-1', ?), ('td-expire', 1, 'sess-1', ?);
	`, past, past).Error)
	tdRepo := NewTemporaryDocumentRepository(db)
	expired, err := tdRepo.ListExpired(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, "td-expire", expired[0].ID)

	// chunk_image_assets.go & image_source_order.go with empty attrs
	require.NoError(t, db.Exec(`
		INSERT INTO chunk_images (
			tenant_id, knowledge_base_id, knowledge_id, chunk_id, image_index, image_key, chunk_type, attrs
		)
		VALUES (1, 'kb-1', 'k-good', 'c-doc-q', 0, 'img-1', 'image_caption', '');
	`).Error)
	d := imageAssetDialect{postgres: false}
	valExpr, ok := d.value(types.ImageAssetField{Attr: "chart_type"})
	require.True(t, ok)
	ordExpr := d.sourceAttrOrdinal("page", 1)
	var rowCount int64
	require.NoError(t, db.Table("chunk_images i").
		Where(valExpr+" IS NULL AND "+ordExpr+" IS NULL").
		Count(&rowCount).Error)
	require.Equal(t, int64(1), rowCount)
}

func TestSQLiteWriteLockPreventsConcurrentLastOwnerDemotion(t *testing.T) {
	db := openParitySQLiteDB(t, true)
	require.NoError(t, db.AutoMigrate(&types.TenantMember{}))

	repo := NewTenantMemberRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, &types.TenantMember{
		UserID: "owner-a", TenantID: 1, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive,
	}))
	require.NoError(t, repo.Create(ctx, &types.TenantMember{
		UserID: "owner-b", TenantID: 1, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive,
	}))
	require.NoError(t, db.Model(&types.TenantMember{}).
		Where("user_id = ? AND tenant_id = ?", "owner-b", 1).
		Update("role", types.TenantRoleOwner).Error)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	users := []string{"owner-a", "owner-b"}
	for idx, u := range users {
		wg.Add(1)
		go func(i int, userID string) {
			defer wg.Done()
			<-start
			errs[i] = repo.DemoteOwnerAtomically(ctx, userID, 1, types.TenantRoleAdmin)
		}(idx, u)
	}
	close(start)
	wg.Wait()

	remaining, err := repo.CountActiveOwners(ctx, 1)
	require.NoError(t, err)
	require.GreaterOrEqual(t, remaining, int64(1), "at least one active Owner must remain")
	require.True(t, errors.Is(errs[0], ErrLastOwner) || errors.Is(errs[1], ErrLastOwner),
		"concurrent demotions must reject the second with ErrLastOwner, got errs=%v", errs)
}

func TestDeleteChunksMultiBatchRollsBackAtomicallyOnFailure(t *testing.T) {
	db := openParitySQLiteDB(t, false)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))

	const totalChunks = 5010
	ids := make([]string, 0, totalChunks)
	batch := make([]*types.Chunk, 0, 500)
	for i := 0; i < totalChunks; i++ {
		id := fmt.Sprintf("chunk-%04d", i)
		ids = append(ids, id)
		batch = append(batch, &types.Chunk{
			ID:              id,
			TenantID:        1,
			KnowledgeBaseID: "kb-1",
			KnowledgeID:     "kg-1",
			ChunkType:       types.ChunkTypeText,
			Content:         "body",
		})
		if len(batch) == 500 || i == totalChunks-1 {
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&batch).Error)
			batch = batch[:0]
		}
	}

	var deleteCalls int
	cbName := "test:fail_second_batch"
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(cbName, func(tx *gorm.DB) {
		deleteCalls++
		if deleteCalls == 2 {
			_ = tx.AddError(errors.New("simulated second batch failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(cbName) })

	repo := NewChunkRepository(db)
	err := repo.DeleteChunks(context.Background(), 1, ids)
	require.Error(t, err)

	var remaining int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("tenant_id = 1").Count(&remaining).Error)
	require.Equal(
		t,
		int64(totalChunks),
		remaining,
		"multi-batch DeleteChunks must roll back batch 1 when batch 2 fails",
	)
}
