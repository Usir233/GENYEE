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
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close duckdb: %v", closeErr)
		}
	}()

	// 1. SQL injection / traversal extension names must be rejected before INSTALL executes.
	badExts := []string{
		"",
		"../spatial",
		"spatial; DROP TABLE x;--",
		"https://evil.example/ext.duckdb_extension",
	}
	for _, badExt := range badExts {
		err := installAndLoadExtensions(context.Background(), db, []string{badExt}, "")
		if err == nil || !strings.Contains(err.Error(), "invalid duckdb extension name") {
			t.Fatalf("expected invalid duckdb extension name error for %q, got %v", badExt, err)
		}
	}

	// 2. Cleartext HTTP or credential-bearing custom extension repository URLs must fail closed.
	badRepos := []string{
		"http://mirror.example.com/extensions",
		"file:///tmp/exts",
		"https://user:pass@mirror.example.com/exts",
	}
	for _, badRepo := range badRepos {
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
		defer func() { _ = unsignedDB.Close() }()
		err = installAndLoadExtensions(context.Background(), unsignedDB, nil, "")
		if err == nil || !strings.Contains(err.Error(), "unsigned") {
			t.Fatalf("expected allow_unsigned_extensions=true to fail closed, got %v", err)
		}
	}

	// 5. runDownloadExtensions must return a clean error instead of panicking on invalid repo env.
	t.Setenv("DUCKDB_CUSTOM_EXTENSION_REPOSITORY", "http://insecure.example.com/exts")
	if err := runDownloadExtensions(context.Background()); err == nil {
		t.Fatal("expected runDownloadExtensions to return error on cleartext custom repo")
	}
}
