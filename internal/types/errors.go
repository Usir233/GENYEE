package types

import "fmt"

// StorageQuotaExceededError represents the storage quota exceeded error.
type StorageQuotaExceededError struct {
	Message string
}

// Error implements the error interface.
func (e *StorageQuotaExceededError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewStorageQuotaExceededError creates a storage quota exceeded error.
func NewStorageQuotaExceededError() *StorageQuotaExceededError {
	return &StorageQuotaExceededError{
		Message: "Storage quota exceeded",
	}
}

// DuplicateKnowledgeError reports a duplicate knowledge item and carries the existing record.
type DuplicateKnowledgeError struct {
	Message   string
	Knowledge *Knowledge
}

// Error implements the error interface.
func (e *DuplicateKnowledgeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewDuplicateFileError creates a duplicate file error.
func NewDuplicateFileError(knowledge *Knowledge) *DuplicateKnowledgeError {
	fileName := ""
	if knowledge != nil {
		fileName = knowledge.FileName
	}
	return &DuplicateKnowledgeError{
		Message:   fmt.Sprintf("File already exists: %s", fileName),
		Knowledge: knowledge,
	}
}

// NewDuplicateURLError creates a duplicate URL error.
func NewDuplicateURLError(knowledge *Knowledge) *DuplicateKnowledgeError {
	source := ""
	if knowledge != nil {
		source = knowledge.Source
	}
	return &DuplicateKnowledgeError{
		Message:   fmt.Sprintf("URL already exists: %s", source),
		Knowledge: knowledge,
	}
}
