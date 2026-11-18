package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type attachmentParserResultReader struct {
	interfaces.DocumentReader
	result *types.ReadResult
	err    error
}

func (r attachmentParserResultReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return r.result, r.err
}

type attachmentParserResultFiles struct {
	interfaces.FileService
	saves int
}

func (s *attachmentParserResultFiles) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	s.saves++
	return "local://attachment.pdf", nil
}

func TestAttachmentParserResultFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  *types.ReadResult
		err     error
		message string
	}{
		{
			name: "error envelope", result: &types.ReadResult{Error: "document is encrypted"},
			message: "document is encrypted",
		},
		{
			name:    "partial result with error",
			result:  &types.ReadResult{Error: "page extraction failed", MarkdownContent: "incomplete text"},
			message: "page extraction failed",
		},
		{name: "missing result", message: "empty result"},
		{name: "transport error", err: errors.New("parser unavailable"), message: "parser unavailable"},
		{name: "success", result: &types.ReadResult{MarkdownContent: "complete extracted text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := &attachmentParserResultFiles{}
			processor := NewAttachmentProcessor(
				files, attachmentParserResultReader{result: tc.result, err: tc.err}, nil, nil,
			)
			var attachment *types.MessageAttachment
			require.NotPanics(t, func() {
				var err error
				attachment, err = processor.ProcessAttachment(
					context.Background(), []byte("isolated fixture"), "fixture.pdf", 16, 1, "",
				)
				require.NoError(t, err, "extraction failures retain the uploaded attachment with an error placeholder")
			})
			require.NotNil(t, attachment)
			require.Equal(t, "local://attachment.pdf", attachment.URL)
			require.Equal(t, 1, files.saves)
			if tc.message != "" {
				require.Contains(t, attachment.Content, "<error>")
				require.Contains(t, attachment.Content, tc.message)
				require.NotContains(t, attachment.Content, "incomplete text")
			} else {
				require.Equal(t, tc.result.MarkdownContent, attachment.Content)
			}
		})
	}
}
