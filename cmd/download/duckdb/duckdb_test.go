package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestDuckDBDownloadGuards_RejectUnsafeExtensionAndCleartextRepoAndUnsignedMode(t *testing.T) {
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	// 1. SQL injection / traversal extension names must be rejected before INSTALL executes.
	for _, badExt := range []string{"", "../spatial", "spatial; DROP TABLE x;--", "https://evil.example/ext.duckdb_extension"} {
		err := installAndLoadExtensions(context.Background(), db, []string{badExt}, "")
		if err == nil || !strings.Contains(err.Error(), "invalid duckdb extension name") {
			t.Fatalf("expected invalid duckdb extension name error for %q, got %v", badExt, err)
		}
	}

	// 2. Cleartext HTTP or credential-bearing custom extension repository URLs must fail closed.
	for _, badRepo := range []string{"http://mirror.example.com/extensions", "file:///tmp/exts", "https://user:pass@mirror.example.com/exts"} {
		err := installAndLoadExtensions(context.Background(), db, nil, badRepo)
		if err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("expected HTTPS validation error for customRepo=%q, got %v", badRepo, err)
		}
	}

	// 3. Valid HTTPS custom repository with zero extensions succeeds and enforces signed-only extensions.
	if err := installAndLoadExtensions(context.Background(), db, nil, "https://extensions.duckdb.org"); err != nil {
		t.Fatalf("expected valid HTTPS custom repository with empty ext list to pass, got %v", err)
	}

	// 4. Unsigned extension mode must be rejected (fail closed).
	unsignedDB, err := sql.Open("duckdb", ":memory:?allow_unsigned_extensions=true")
	if err == nil {
		defer unsignedDB.Close()
		err = installAndLoadExtensions(context.Background(), unsignedDB, nil, "")
		if err == nil || !strings.Contains(err.Error(), "unsigned") {
			t.Fatalf("expected allow_unsigned_extensions=true to fail closed, got %v", err)
		}
	}
}
