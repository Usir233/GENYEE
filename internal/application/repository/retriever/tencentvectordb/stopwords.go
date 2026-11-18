package tencentvectordb

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed default_stopwords.txt
var embeddedDefaultStopwords []byte

const (
	// EnvVectorDBStorageDir overrides the local cache directory for Tencent VectorDB assets.
	EnvVectorDBStorageDir = "GENYEE_VECTORDB_STORAGE_DIR"
	legacyStorageDir      = "/tmp/tencent/vectordatabase/data"
	legacyStopwordsPath   = "/tmp/tencent/vectordatabase/data/default_stopwords.txt"
)

// StorageDir returns the directory used to cache local Tencent VectorDB assets.
func StorageDir() string {
	if dir := os.Getenv(EnvVectorDBStorageDir); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("genyee-vectordb-%d", os.Getuid()))
}

// StopwordsFilePath returns the filesystem path of the seeded BM25 stopwords file.
func StopwordsFilePath() string {
	return filepath.Join(StorageDir(), "default_stopwords.txt")
}

var (
	// DefaultStorageDir is the directory where vectordb data files are cached.
	DefaultStorageDir = StorageDir()
	// DefaultStopwordsFilePath is the target path where stopwords are stored.
	DefaultStopwordsFilePath = StopwordsFilePath()
)

var stopwordsMu sync.Mutex

func init() {
	_ = EnsureDefaultStopwords()
}

// EnsureDefaultStopwords materializes the embedded BM25 stopwords file if missing or empty.
func EnsureDefaultStopwords() error {
	stopwordsMu.Lock()
	defer stopwordsMu.Unlock()

	targetDir := StorageDir()
	targetPath := StopwordsFilePath()

	// 1. Ensure user-scoped / configured directory has stopwords
	fi, err := os.Stat(targetPath)
	if err != nil || fi.Size() == 0 {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("create vectordb storage dir %s: %w", targetDir, err)
		}
		tmpFile := targetPath + ".tmp"
		if err := os.WriteFile(tmpFile, embeddedDefaultStopwords, 0o644); err != nil {
			return fmt.Errorf("write temp stopwords file: %w", err)
		}
		if err := os.Rename(tmpFile, targetPath); err != nil {
			return fmt.Errorf("seed stopwords file %s: %w", targetPath, err)
		}
	}

	// 2. Best-effort seeding to legacy /tmp/tencent/vectordatabase/data path
	// to satisfy upstream SDK NewJiebaTokenizer(nil) check without failing
	// on multi-user systems where /tmp/tencent is owned by another user.
	if legacyFi, err := os.Stat(legacyStopwordsPath); err != nil || legacyFi.Size() == 0 {
		if mkErr := os.MkdirAll(legacyStorageDir, 0o755); mkErr == nil {
			tmpLegacy := legacyStopwordsPath + ".tmp"
			if wErr := os.WriteFile(tmpLegacy, embeddedDefaultStopwords, 0o644); wErr == nil {
				_ = os.Rename(tmpLegacy, legacyStopwordsPath)
			}
		}
	}

	return nil
}
