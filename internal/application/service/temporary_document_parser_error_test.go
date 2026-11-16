package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

type parserResultRepo struct {
	interfaces.TemporaryDocumentRepository
	document      *types.TemporaryDocument
	readyCalls    int
	failedMessage string
}

func (r *parserResultRepo) GetByID(context.Context, uint64, string) (*types.TemporaryDocument, error) {
	return r.document, nil
}

func (r *parserResultRepo) MarkProcessing(context.Context, uint64, string, time.Time) error {
	return nil
}

func (r *parserResultRepo) MarkReady(
	context.Context, uint64, string, string, types.JSON, types.JSON, types.JSON, int, int, time.Time,
) error {
	r.readyCalls++
	return nil
}

func (r *parserResultRepo) MarkFailed(_ context.Context, _ uint64, _ string, message string) error {
	r.failedMessage = message
	return nil
}

type parserResultFiles struct {
	interfaces.FileService
}

func (parserResultFiles) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("document bytes")), nil
}

type parserResultTenants struct {
	interfaces.TenantService
}

func (parserResultTenants) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return nil, nil
}

type parserResultReader struct {
	interfaces.DocumentReader
}

func (parserResultReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return &types.ReadResult{Error: "parser rejected document"}, nil
}

func TestTemporaryDocumentParserResultErrorMarksDocumentFailed(t *testing.T) {
	options, err := json.Marshal(types.TemporaryDocumentCreateOptions{ParserEngine: "builtin"})
	require.NoError(t, err)
	repo := &parserResultRepo{document: &types.TemporaryDocument{
		ID: "document-1", TenantID: 7, FileName: "document.pdf", FileType: ".pdf",
		ResourceRef: "document.pdf", ProcessingOptions: types.JSON(options),
	}}
	payload, err := json.Marshal(types.TemporaryDocumentTaskPayload{TenantID: 7, DocumentID: "document-1"})
	require.NoError(t, err)
	svc := &temporaryDocumentService{
		repo: repo, fileService: parserResultFiles{}, documentReader: parserResultReader{},
		tenantService: parserResultTenants{},
	}

	// Lite mode has no Asynq retry metadata, so a saved failure is terminal.
	err = svc.Process(t.Context(), asynq.NewTask("parse", payload))

	require.NoError(t, err)
	require.Zero(t, repo.readyCalls)
	require.Contains(t, repo.failedMessage, "parser rejected document")
}
