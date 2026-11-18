package types

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalDomainSentinelErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"ErrRecordNotFound", ErrRecordNotFound, "record not found"},
		{"ErrKnowledgeBaseNotFound", ErrKnowledgeBaseNotFound, "knowledge base not found"},
		{"ErrKnowledgeNotFound", ErrKnowledgeNotFound, "knowledge not found"},
		{"ErrChunkNotFound", ErrChunkNotFound, "chunk not found"},
		{"ErrCustomAgentNotFound", ErrCustomAgentNotFound, "custom agent not found"},
		{"ErrSandboxConfigCordoned", ErrSandboxConfigCordoned, "sandbox config is being modified by another request"},
		{"ErrUserNotSystemAdmin", ErrUserNotSystemAdmin, "user is not a system administrator"},
		{"ErrCannotRevokeSelf", ErrCannotRevokeSelf, "cannot revoke your own system admin privileges"},
		{"ErrLastSystemAdmin", ErrLastSystemAdmin, "cannot revoke the last remaining system administrator"},
		{"ErrUserNotFound", ErrUserNotFound, "user not found"},
		{"ErrCatalogVersionConflict", ErrCatalogVersionConflict, "model catalog changed; reload and preview again"},
		{"ErrWikiFolderNotFound", ErrWikiFolderNotFound, "wiki folder not found"},
		{"ErrWikiPageNotFound", ErrWikiPageNotFound, "wiki page not found"},
		{"ErrWikiFolderConflict", ErrWikiFolderConflict, "wiki folder name conflict"},
		{"ErrWikiFolderNotEmpty", ErrWikiFolderNotEmpty, "wiki folder is not empty"},
		{"ErrWikiPageConflict", ErrWikiPageConflict, "wiki page version conflict"},
		{"ErrWikiIssueNotFound", ErrWikiIssueNotFound, "wiki issue not found"},
	}

	for i, tc := range cases {
		require.NotNil(t, tc.err, tc.name)
		assert.Equal(t, tc.want, tc.err.Error(), tc.name)
		wrapped := fmt.Errorf("wrap %s: %w", tc.name, tc.err)
		assert.ErrorIs(t, wrapped, tc.err, tc.name)
		for j, other := range cases {
			if i != j {
				assert.False(t, errors.Is(wrapped, other.err), "%s must not match %s", tc.name, other.name)
			}
		}
	}
}
