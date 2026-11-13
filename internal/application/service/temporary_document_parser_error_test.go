package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type temporaryParserResultReader struct {
	interfaces.DocumentReader
	result *types.ReadResult
	err    error
}

func (r temporaryParserResultReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return r.result, r.err
}

type temporaryParserResultRepo struct {
	interfaces.TemporaryDocumentRepository
	document *types.TemporaryDocument
	failed   string
	ready    bool
	content  string
}

func (r *temporaryParserResultRepo) GetByID(context.Context, uint64, string) (*types.TemporaryDocument, error) {
	return r.document, nil
}

func (r *temporaryParserResultRepo) MarkProcessing(context.Context, uint64, string, time.Time) error {
	return nil
}

func (r *temporaryParserResultRepo) MarkFailed(_ context.Context, _ uint64, _ string, message string) error {
	r.failed = message
	return nil
}

func (r *temporaryParserResultRepo) MarkReady(_ context.Context, _ uint64, _ string, content string, _ types.JSON, _ types.JSON, _ types.JSON, _, _ int, _ time.Time) error {
	r.ready, r.content = true, content
	return nil
}

func TestTemporaryDocumentParserResultFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  *types.ReadResult
		err     error
		message string
	}{
		{name: "error envelope", result: &types.ReadResult{Error: "document is encrypted"}, message: "document is encrypted"},
		{name: "partial result with error", result: &types.ReadResult{Error: "page extraction failed", MarkdownContent: "incomplete text"}, message: "page extraction failed"},
		{name: "missing result", message: "empty result"},
		{name: "transport error", err: errors.New("parser unavailable"), message: "parser unavailable"},
		{name: "success", result: &types.ReadResult{MarkdownContent: "complete extracted text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := &types.TemporaryDocument{ID: "document", TenantID: 1, ResourceRef: "local://fixture.pdf", FileName: "fixture.pdf", FileType: ".pdf"}
			repo := &temporaryParserResultRepo{document: doc}
			svc := &temporaryDocumentService{
				repo: repo, fileService: &stagingFileService{files: map[string][]byte{doc.ResourceRef: []byte("isolated fixture")}},
				documentReader: temporaryParserResultReader{result: tc.result, err: tc.err},
				tenantService:  &stubTenantServiceForModelDelete{},
			}
			payload, err := json.Marshal(types.TemporaryDocumentTaskPayload{TenantID: 1, DocumentID: doc.ID})
			require.NoError(t, err)
			// Lite mode has no retry metadata: a parse failure must persist its
			// terminal failed state rather than publishing an empty ready document.
			require.NotPanics(t, func() {
				require.NoError(t, svc.Process(context.Background(), asynq.NewTask("temporary-document", payload)))
			})
			if tc.message != "" {
				require.False(t, repo.ready)
				require.Contains(t, repo.failed, tc.message)
			} else {
				require.True(t, repo.ready)
				require.Empty(t, repo.failed)
				require.Equal(t, tc.result.MarkdownContent, repo.content)
			}
		})
	}
}
