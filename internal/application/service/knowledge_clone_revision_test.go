package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func cloneRevisionChunks(t *testing.T, f *documentWriteFixture) (*types.Knowledge, []*types.Chunk) {
	t.Helper()
	source, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(t, f.svc.cloneKnowledge(f.ctx, source, f.kbs.values["other"]))
	destination := clonedTagKnowledge(t, f)
	chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, destination.ID)
	require.NoError(t, err)
	return destination, chunks
}

func TestClonePreservesEditedBodiesForReconstruction(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	first := "The first rewritten section contains current operating instructions and remains independently meaningful."
	second := "The second edited section contains independent evidence that must not disappear behind old " +
		"parser offsets."
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content": first, "source_content": "original parser first body", "content_revision": 3,
		"start_at": 0, "end_at": 100, "chunk_index": 0, "is_enabled": true,
		"metadata": `{"generated_questions_revision":3,"generated_questions":[
			{"id":"current","question":"current question","content_revision":3},
			{"id":"stale","question":"stale question","content_revision":0}]}`,
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "second", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeText,
		Content: second, SourceContent: "original parser second body", ContentRevision: 4,
		StartAt: 50, EndAt: 90, ChunkIndex: 1, IsEnabled: true,
	}).Error)
	original, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, "doc")
	require.NoError(t, err)
	want := first + "\n\n" + second
	require.Equal(t, want, reconstructContent(original))
	destination, cloned := cloneRevisionChunks(t, f)
	require.Len(t, cloned, 2)

	t.Run("wiki", func(t *testing.T) {
		require.Equal(t, want, reconstructContent(cloned))
	})
	t.Run("summary", func(t *testing.T) {
		f.svc.config = &config.Config{Conversation: &config.ConversationConfig{
			GenerateSummaryPrompt: "Summarize the document.",
		}}
		model := &summaryContentCaptureChat{}
		_, err := f.svc.getSummary(f.ctx, model, destination, cloned)
		require.NoError(t, err)
		require.Len(t, model.messages, 2)
		require.Equal(t, want, model.messages[1].Content)
	})
	t.Run("question freshness", func(t *testing.T) {
		for _, chunk := range cloned {
			if chunk.ChunkIndex != 0 {
				continue
			}
			meta, err := chunk.DocumentMetadata()
			require.NoError(t, err)
			require.NotNil(t, meta)
			require.Len(t, meta.GeneratedQuestions, 2)
			require.True(t, meta.IsQuestionCurrent(meta.GeneratedQuestions[0], chunk.ContentRevision))
			require.False(t, meta.IsQuestionCurrent(meta.GeneratedQuestions[1], chunk.ContentRevision))
		}
	})
}

func TestClonePreservesParserBaseForLaterChildEdits(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	// The first child already shortened its original four-character span.
	// Later editing the second child must still use the original coordinates.
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "parent", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeParentText, Content: "AA456789", SourceContent: "0123456789",
		StartAt: 0, EndAt: 10, IsEnabled: true,
	}).Error)
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content": "AA", "source_content": "0123", "content_revision": 1,
		"start_at": 0, "end_at": 4, "parent_chunk_id": "parent", "chunk_index": 1,
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "second", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeText, ParentChunkID: "parent", Content: "456789", SourceContent: "456789",
		StartAt: 4, EndAt: 10, ChunkIndex: 2, IsEnabled: true,
	}).Error)
	_, cloned := cloneRevisionChunks(t, f)
	var edited *types.Chunk
	for _, chunk := range cloned {
		if chunk.ChunkType == types.ChunkTypeText && chunk.ChunkIndex == 2 {
			edited = chunk
		}
	}
	require.NotNil(t, edited)
	// A clone has its own history: the current snapshot does not manufacture
	// earlier destination edits, and its next edit records only that snapshot.
	history, err := f.chunks.ListChunkRevisions(f.ctx, edited.ID)
	require.NoError(t, err)
	require.Empty(t, history)
	content := "BBB"
	revision := edited.ContentRevision
	updated, err := f.chunks.UpdateDocumentChunk(f.ctx, edited.ID, &content, nil, &revision)
	require.NoError(t, err)
	require.Equal(t, "ready", updated.IndexStatus)
	parent, err := f.chunkRepo.GetChunkByID(f.ctx, 7, edited.ParentChunkID)
	require.NoError(t, err)
	require.Equal(t, "AABBB", parent.Content)
	require.Equal(t, "0123456789", parent.SourceContent)
	history, err = f.chunks.ListChunkRevisions(f.ctx, edited.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, "456789", history[0].Content)
	sourceParent, err := f.chunkRepo.GetChunkByID(f.ctx, 7, "parent")
	require.NoError(t, err)
	require.Equal(t, "AA456789", sourceParent.Content, "editing the clone leaves the source unchanged")
}

func TestCloneParserBaseUsesCopiedImageReferences(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	files := &countingFileService{}
	f.svc.fileSvc = files
	const sourceImage = "original.png"
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content":          "Edited ![diagram](" + sourceImage + ")",
		"source_content":   "Original ![diagram](" + sourceImage + ")",
		"content_revision": 2,
		"image_info":       mustImageInfoJSON(t, []types.ImageInfo{{URL: sourceImage}}),
	}).Error)
	_, cloned := cloneRevisionChunks(t, f)
	require.Len(t, cloned, 1)
	require.Equal(t, 1, files.copyCalls)
	require.Equal(t, "Edited ![diagram](local://7/exports/copy-of-original.png)", cloned[0].Content)
	require.Equal(t, "Original ![diagram](local://7/exports/copy-of-original.png)", cloned[0].SourceContent)
	require.Equal(t, 2, cloned[0].ContentRevision)
}

func TestCloneImageURLRewritePreservesChildEditCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name, first string
		revision    int
	}{
		{"unedited", "FIRST", 0}, {"edited snapshot", "CURRENT-FIRST", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := transferFixture(t, access.KBTransferClone)
			f.svc.fileSvc = &countingFileService{}
			const prefix = "序言![diagram](original.png)"
			const base = prefix + "FIRSTSECOND"
			const parentStart = 100
			childStart := parentStart + len([]rune(prefix))
			require.NoError(t, f.db.Create(&types.Chunk{
				ID: "parent", TenantID: 7, KnowledgeID: "doc",
				KnowledgeBaseID: "kb",
				ChunkType:       types.ChunkTypeParentText, Content: prefix + tc.first + "SECOND", SourceContent: base,
				StartAt: parentStart, EndAt: parentStart + len([]rune(base)), IsEnabled: true,
			}).Error)
			require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
				"content": tc.first, "source_content": "FIRST", "content_revision": tc.revision, "start_at": childStart,
				"end_at": childStart + 5, "parent_chunk_id": "parent", "chunk_index": 1,
			}).Error)
			require.NoError(t, f.db.Create(&types.Chunk{
				ID: "second", TenantID: 7, KnowledgeID: "doc",
				KnowledgeBaseID: "kb",
				ChunkType:       types.ChunkTypeText,
				ParentChunkID:   "parent", Content: "SECOND", SourceContent: "SECOND",
				StartAt: childStart + 5, EndAt: childStart + 11, ChunkIndex: 2, IsEnabled: true,
			}).Error)
			require.NoError(t, f.db.Create(&types.Chunk{
				ID: "image", TenantID: 7, KnowledgeID: "doc",
				KnowledgeBaseID: "kb",
				ChunkType:       types.ChunkTypeImageCaption,
				ParentChunkID:   "parent", Content: "diagram", IsEnabled: true,
				ImageInfo: mustImageInfoJSON(t, []types.ImageInfo{{URL: "original.png"}}),
			}).Error)
			_, cloned := cloneRevisionChunks(t, f)
			var edited *types.Chunk
			for _, c := range cloned {
				if c.ChunkType == types.ChunkTypeText && c.ChunkIndex == 2 {
					edited = c
				}
			}
			require.NotNil(t, edited)
			content := "REPLACED"
			revision := edited.ContentRevision
			updated, err := f.chunks.UpdateDocumentChunk(f.ctx, edited.ID, &content, nil, &revision)
			require.NoError(t, err)
			require.Equal(t, "ready", updated.IndexStatus)
			parent, err := f.chunkRepo.GetChunkByID(f.ctx, 7, edited.ParentChunkID)
			require.NoError(t, err)
			require.Equal(t, "序言![diagram](local://7/exports/copy-of-original.png)"+tc.first+"REPLACED", parent.Content)
		})
	}
}

func TestCloneImageURLRewriteKeepsGlobalOverlappingCoordinates(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	files := &countingFileService{}
	f.svc.fileSvc = files
	const image = "![diagram](original.png)"
	const copied = "![diagram](local://7/exports/copy-of-original.png)"
	text := "序言" + image + strings.Repeat("重复段落", 40) + image + strings.Repeat("后续正文", 30)
	runes := []rune(text)
	firstEnd := utf8.RuneCountInString("序言"+image+image) + 160 + 40
	secondStart := firstEnd - 80
	const origin = 100
	// Parent windows overlap and see the same first/second image in multiple
	// children. Only physical occurrences contribute to the document shift.
	parents := []struct {
		id         string
		start, end int
	}{
		{"parent-one", 0, firstEnd}, {"parent-two", secondStart, len(runes)},
	}
	for _, p := range parents {
		body := string(runes[p.start:p.end])
		require.NoError(t, f.db.Create(&types.Chunk{
			ID: p.id, TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
			ChunkType: types.ChunkTypeParentText, Content: body, StartAt: origin + p.start, EndAt: origin + p.end,
			IsEnabled: true,
		}).Error)
	}
	children := []struct {
		id, parent string
		start, end int
	}{
		{"chunk", "parent-one", 0, firstEnd - 4},
		{"one-tail", "parent-one", firstEnd - 8, firstEnd},
		{"two-head", "parent-two", secondStart, len(runes) - 40},
		{
			"two-tail", "parent-two", len(runes) - 44,
			len(runes),
		},
	}
	require.NoError(t, f.db.Unscoped().Where("id = ?", "chunk").Delete(&types.Chunk{}).Error)
	for i, c := range children {
		require.NoError(t, f.db.Create(&types.Chunk{
			ID: c.id, TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
			ChunkType: types.ChunkTypeText, ParentChunkID: c.parent, Content: string(runes[c.start:c.end]),
			StartAt: origin + c.start, EndAt: origin + c.end, ChunkIndex: i, IsEnabled: true,
		}).Error)
	}
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "image", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeImageCaption, ParentChunkID: "parent-one", Content: "diagram", IsEnabled: true,
		ImageInfo: mustImageInfoJSON(t, []types.ImageInfo{{URL: "original.png"}}),
	}).Error)
	destination, cloned := cloneRevisionChunks(t, f)
	require.Equal(t, 1, files.copyCalls)
	var textChunks []*types.Chunk
	for _, c := range cloned {
		if c.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, c)
		}
	}
	want := strings.ReplaceAll(text, image, copied)
	t.Run("wiki", func(t *testing.T) { require.Equal(t, want, reconstructContent(cloned)) })
	t.Run("summary", func(t *testing.T) {
		f.svc.config = &config.Config{Conversation: &config.ConversationConfig{GenerateSummaryPrompt: "Summarize."}}
		model := &summaryContentCaptureChat{}
		_, err := f.svc.getSummary(f.ctx, model, destination, textChunks)
		require.NoError(t, err)
		require.Len(t, model.messages, 2)
		require.Equal(t, want, model.messages[1].Content)
	})
	t.Run("trusted rune lengths and global offsets", func(t *testing.T) {
		for _, c := range textChunks {
			original := children[c.ChunkIndex]
			require.Zero(t, c.ContentRevision)
			require.Equal(t, utf8.RuneCountInString(c.Content), c.EndAt-c.StartAt)
			require.Equal(t, origin+utf8.RuneCountInString(strings.ReplaceAll(string(runes[:original.start]), image,
				copied)), c.StartAt)
			parent, err := f.chunkRepo.GetChunkByID(f.ctx, 7, c.ParentChunkID)
			require.NoError(t, err)
			require.Equal(t, c.Content, string([]rune(parent.Content)[c.StartAt-parent.StartAt:c.EndAt-parent.StartAt]))
		}
	})
}

func TestCloneImageCoordinatesAcrossURLBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, replacement, first, second string
		split, end                       int
	}{
		{"longer unicode URL", "local://图", "前![图](loc", "al://图)后", 109, 117},
		{"shorter URL", "x", "前![图](", "x)后", 106, 109},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const base = "前![图](图.png)后"
			// A source hard token budget splits the URL after its second rune.
			parent := &types.Chunk{
				ChunkType: types.ChunkTypeParentText, Content: base, SourceContent: base,
				StartAt: 100, EndAt: 113,
			}
			first := &types.Chunk{ChunkType: types.ChunkTypeText, Content: "前![图](图.", StartAt: 100, EndAt: 108}
			second := &types.Chunk{
				ChunkType: types.ChunkTypeText, Content: "png)后", SourceContent: "png)后",
				StartAt: 108, EndAt: 113,
			}
			caption := &types.Chunk{ChunkType: types.ChunkTypeImageCaption, Content: "图.png", StartAt: 100, EndAt: 105}
			require.NoError(t, rewriteClonedChunkImages([]*types.Chunk{parent, first, second, caption},
				map[string]string{"图.png": tc.replacement}))
			require.Equal(t, tc.first, first.Content)
			require.Equal(t, tc.second, second.Content)
			require.Equal(t, first.Content+second.Content, parent.Content)
			require.Equal(t, parent.Content, parent.SourceContent)
			require.Equal(t, second.Content, second.SourceContent)
			require.Equal(t, tc.split, first.EndAt)
			require.Equal(t, tc.split, second.StartAt)
			require.Equal(t, tc.end, parent.EndAt)
			require.Equal(t, tc.end, second.EndAt)
			require.Equal(t, tc.replacement, caption.Content)
		})
	}
}

func TestCloneImageRewriteDoesNotCascade(t *testing.T) {
	require.Equal(t, "a.png x.png", rewriteContentImageURLs("long-source.png a.png", map[string]string{
		"long-source.png": "a.png", "a.png": "x.png",
	}))
	require.Equal(t, "full small", rewriteContentImageURLs("a.png?q=1 a.png", map[string]string{
		"a.png": "small", "a.png?q=1": "full",
	}))
}

func TestCloneImageCoordinateFailureCleansCopiesBeforePersistence(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	files := &countingFileService{}
	f.svc.fileSvc = files
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content": "a.png", "start_at": 100, "end_at": 105, "image_info": mustImageInfoJSON(t,
			[]types.ImageInfo{{URL: "a.png"}}),
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "conflict", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeText, Content: "b.png", StartAt: 100, EndAt: 105, IsEnabled: true,
		ImageInfo: mustImageInfoJSON(t, []types.ImageInfo{{URL: "b.png"}}),
	}).Error)
	source, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	err = f.svc.cloneKnowledge(f.ctx, source, f.kbs.values["other"])
	require.ErrorContains(t, err, "conflicting image coordinates")
	destination := clonedTagKnowledge(t, f)
	require.Equal(t, types.ParseStatusFailed, destination.ParseStatus)
	chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, destination.ID)
	require.NoError(t, err)
	require.Empty(t, chunks)
	require.Equal(t, 2, files.copyCalls)
	require.Equal(t, 2, files.deleteCalls)
	original, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Len(t, original, 2)
}

func TestCloneRejectsPartiallyOverlappingImageRewrites(t *testing.T) {
	f := transferFixture(t, access.KBTransferClone)
	files := &countingFileService{}
	f.svc.fileSvc = files
	// Both are valid URL strings, and both exact source windows agree on
	// their shared text. They nevertheless refer to different copied images.
	// No external request is made: countingFileService serves these fixtures.
	const firstURL = "https://images.example/a?next=https://images.example/b"
	const secondURL = "https://images.example/b?size=1"
	secondStart := strings.Index(firstURL, "https://images.example/b")
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Updates(map[string]any{
		"content": firstURL, "source_content": firstURL, "start_at": 0, "end_at": len(firstURL),
		"image_info": mustImageInfoJSON(t, []types.ImageInfo{{URL: firstURL}}),
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "overlap", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeText, Content: secondURL, SourceContent: secondURL, StartAt: secondStart,
		EndAt: secondStart + len(secondURL), IsEnabled: true,
		ImageInfo: mustImageInfoJSON(t, []types.ImageInfo{{URL: secondURL}}),
	}).Error)
	source, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	err = f.svc.cloneKnowledge(f.ctx, source, f.kbs.values["other"])
	require.ErrorContains(t, err, "conflicting image coordinates")
	destination := clonedTagKnowledge(t, f)
	require.Equal(t, types.ParseStatusFailed, destination.ParseStatus)
	chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, destination.ID)
	require.NoError(t, err)
	require.Empty(t, chunks)
	require.Equal(t, 2, files.copyCalls)
	require.Equal(t, 2, files.deleteCalls)
}
