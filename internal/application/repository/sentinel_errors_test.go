package repository_test

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSentinelErrorsAliasedToTypes(t *testing.T) {
	cases := []struct {
		name    string
		repoErr error
		typeErr error
	}{
		{"ErrRecordNotFound", repository.ErrRecordNotFound, types.ErrRecordNotFound},
		{"ErrKnowledgeBaseNotFound", repository.ErrKnowledgeBaseNotFound, types.ErrKnowledgeBaseNotFound},
		{"ErrKnowledgeNotFound", repository.ErrKnowledgeNotFound, types.ErrKnowledgeNotFound},
		{"ErrChunkNotFound", repository.ErrChunkNotFound, types.ErrChunkNotFound},
		{"ErrCustomAgentNotFound", repository.ErrCustomAgentNotFound, types.ErrCustomAgentNotFound},
		{"ErrSandboxConfigCordoned", repository.ErrSandboxConfigCordoned, types.ErrSandboxConfigCordoned},
		{"ErrCatalogVersionConflict", repository.ErrCatalogVersionConflict, types.ErrCatalogVersionConflict},
		{"ErrUserNotFound", repository.ErrUserNotFound, types.ErrUserNotFound},
		{"ErrUserNotSystemAdmin", repository.ErrUserNotSystemAdmin, types.ErrUserNotSystemAdmin},
		{"ErrCannotRevokeSelf", repository.ErrCannotRevokeSelf, types.ErrCannotRevokeSelf},
		{"ErrLastSystemAdmin", repository.ErrLastSystemAdmin, types.ErrLastSystemAdmin},
		{"ErrWikiFolderNotFound", repository.ErrWikiFolderNotFound, types.ErrWikiFolderNotFound},
		{"ErrWikiPageNotFound", repository.ErrWikiPageNotFound, types.ErrWikiPageNotFound},
		{"ErrWikiFolderConflict", repository.ErrWikiFolderConflict, types.ErrWikiFolderConflict},
		{"ErrWikiFolderNotEmpty", repository.ErrWikiFolderNotEmpty, types.ErrWikiFolderNotEmpty},
		{"ErrWikiPageConflict", repository.ErrWikiPageConflict, types.ErrWikiPageConflict},
		{"ErrWikiIssueNotFound", repository.ErrWikiIssueNotFound, types.ErrWikiIssueNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.repoErr, tc.typeErr)
			require.ErrorIs(t, tc.typeErr, tc.repoErr)
			wrapped := fmt.Errorf("context: %w", tc.repoErr)
			require.True(t, errors.Is(wrapped, tc.typeErr))
		})
	}
}

func TestMiddlewareKBAccessDoesNotImportRepository(t *testing.T) {
	kbAccessPath := filepath.Join("..", "..", "middleware", "kb_access.go")
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, kbAccessPath, nil, parser.ImportsOnly)
	require.NoError(t, err)
	for _, imp := range fileAST.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		require.NotContains(t, importPath, "internal/application/repository",
			"middleware/kb_access.go must not import concrete repository package")
	}
}
