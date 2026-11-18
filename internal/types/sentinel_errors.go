package types

import "errors"

// Canonical domain sentinel errors shared across service, handler, middleware,
// and repository layers so callers never need to import concrete repository
// packages solely for errors.Is classification.
var (
	// ErrRecordNotFound indicates that a requested domain record does not exist.
	ErrRecordNotFound = errors.New("record not found")

	// ErrKnowledgeBaseNotFound indicates that a knowledge base does not exist.
	ErrKnowledgeBaseNotFound = errors.New("knowledge base not found")
	// ErrKnowledgeNotFound indicates that a knowledge document does not exist.
	ErrKnowledgeNotFound = errors.New("knowledge not found")
	// ErrChunkNotFound indicates that a document/FAQ chunk does not exist.
	ErrChunkNotFound = errors.New("chunk not found")

	// ErrCustomAgentNotFound indicates that a custom agent does not exist.
	ErrCustomAgentNotFound = errors.New("custom agent not found")
	// ErrSandboxConfigCordoned indicates that a sandbox configuration is currently locked for update.
	ErrSandboxConfigCordoned = errors.New("sandbox config is being modified by another request")
	// ErrCatalogVersionConflict indicates an optimistic concurrency conflict on model catalog updates.
	ErrCatalogVersionConflict = errors.New("model catalog changed; reload and preview again")

	// ErrUserNotFound indicates that a user record does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrUserNotSystemAdmin indicates that the target user lacks system administrator privileges.
	ErrUserNotSystemAdmin = errors.New("user is not a system administrator")
	// ErrCannotRevokeSelf indicates an attempt to revoke the caller's own system administrator role.
	ErrCannotRevokeSelf = errors.New("cannot revoke your own system admin privileges")
	// ErrLastSystemAdmin indicates an attempt to revoke the last active system administrator.
	ErrLastSystemAdmin = errors.New("cannot revoke the last remaining system administrator")

	// ErrWikiFolderNotFound indicates that a wiki folder does not exist.
	ErrWikiFolderNotFound = errors.New("wiki folder not found")
	// ErrWikiPageNotFound indicates that a wiki page does not exist.
	ErrWikiPageNotFound = errors.New("wiki page not found")
	// ErrWikiFolderConflict indicates a duplicate wiki folder name within the same parent scope.
	ErrWikiFolderConflict = errors.New("wiki folder name conflict")
	// ErrWikiFolderNotEmpty indicates an attempt to delete a non-empty wiki folder without cascade.
	ErrWikiFolderNotEmpty = errors.New("wiki folder is not empty")
	// ErrWikiPageConflict indicates an optimistic version conflict when updating a wiki page.
	ErrWikiPageConflict = errors.New("wiki page version conflict")
	// ErrWikiIssueNotFound indicates that a wiki lint/health issue does not exist.
	ErrWikiIssueNotFound = errors.New("wiki issue not found")
)
