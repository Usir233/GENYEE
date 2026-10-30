package tencentvectordb

import (
	_ "embed"
	"fmt"
	"os"
	"sync"
)

//go:embed default_stopwords.txt
var embeddedDefaultStopwords []byte

const (
	// DefaultStorageDir is the default directory where tencentvectordb SDK caches data files.
	DefaultStorageDir = "/tmp/tencent/vectordatabase/data"
	// DefaultStopwordsFilePath is the target path the SDK checks for stopwords.
	DefaultStopwordsFilePath = "/tmp/tencent/vectordatabase/data/default_stopwords.txt"
)

var stopwordsMu sync.Mutex

func init() {
	_ = EnsureDefaultStopwords()
}

// EnsureDefaultStopwords guarantees that /tmp/tencent/vectordatabase/data/default_stopwords.txt
// exists on the local filesystem.
//
// In an air-gapped / offline environment (--network none), github.com/tencent/vectordatabase-sdk-go
// tokenizer.NewJiebaTokenizer attempts to download default_stopwords.txt from Tencent Cloud COS
// if the local file does not exist. Pre-seeding this file ensures the SDK operates completely
// offline with zero external network calls.
func EnsureDefaultStopwords() error {
	stopwordsMu.Lock()
	defer stopwordsMu.Unlock()

	fi, err := os.Stat(DefaultStopwordsFilePath)
	if err == nil && fi.Size() > 0 {
		return nil
	}

	if err := os.MkdirAll(DefaultStorageDir, 0755); err != nil {
		return fmt.Errorf("create vectordb storage dir %s: %w", DefaultStorageDir, err)
	}

	tmpFile := DefaultStopwordsFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, embeddedDefaultStopwords, 0644); err != nil {
		return fmt.Errorf("write temp stopwords file: %w", err)
	}

	if err := os.Rename(tmpFile, DefaultStopwordsFilePath); err != nil {
		return fmt.Errorf("seed stopwords file %s: %w", DefaultStopwordsFilePath, err)
	}

	return nil
}
