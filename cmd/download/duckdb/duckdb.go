// Package main pre-downloads signed DuckDB extensions during container image builds.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

const defaultDownloadTimeout = 5 * time.Minute

var validDuckDBExtensionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// duckdbExtensions is the list of DuckDB extensions required by GENYEE's
// data analysis tool. `spatial` is used for layer metadata (st_read_meta)
// so we can enumerate sheet names from Excel files, while `excel` provides
// the dedicated read_xlsx reader with proper type inference.
var duckdbExtensions = []string{"spatial", "excel"}

func validateDuckDBExtensionName(ext string) error {
	if !validDuckDBExtensionPattern.MatchString(ext) {
		return fmt.Errorf("invalid duckdb extension name %q", ext)
	}
	return nil
}

func validateHTTPSExtensionRepo(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsAny(trimmed, " \t\r\n\x00'") {
		return "", errors.New("custom duckdb extension repository must be a clean HTTPS URL")
	}
	u, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("custom duckdb extension repository must use HTTPS without embedded credentials: %q", raw)
	}
	return u.String(), nil
}

func installAndLoadExtensions(ctx context.Context, sqlDB *sql.DB, exts []string, customRepo string) error {
	var allowUnsigned string
	if err := sqlDB.QueryRowContext(
		ctx,
		"SELECT value FROM duckdb_settings() WHERE name = 'allow_unsigned_extensions';",
	).Scan(&allowUnsigned); err != nil {
		return fmt.Errorf("failed to verify duckdb signature enforcement setting: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(allowUnsigned), "false") {
		return errors.New("refusing to download duckdb extensions with allow_unsigned_extensions enabled")
	}

	validatedRepo, err := validateHTTPSExtensionRepo(customRepo)
	if err != nil {
		return err
	}
	for _, ext := range exts {
		if err := validateDuckDBExtensionName(ext); err != nil {
			return err
		}
	}

	if validatedRepo != "" {
		escaped := strings.ReplaceAll(validatedRepo, "'", "''")
		stmt := fmt.Sprintf("SET custom_extension_repository = '%s';", escaped)
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to configure HTTPS duckdb extension repository: %w", err)
		}
	}

	for _, ext := range exts {
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
			return fmt.Errorf("failed to install %s extension: %w", ext, err)
		}
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			return fmt.Errorf("failed to load %s extension: %w", ext, err)
		}
	}
	return nil
}

func runDownloadExtensions(parent context.Context) (err error) {
	ctx, cancel := context.WithTimeout(parent, defaultDownloadTimeout)
	defer cancel()

	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return fmt.Errorf("open duckdb: %w", err)
	}
	defer func() {
		if closeErr := sqlDB.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close duckdb: %w", closeErr)
		}
	}()

	customRepo := os.Getenv("DUCKDB_CUSTOM_EXTENSION_REPOSITORY")
	return installAndLoadExtensions(ctx, sqlDB, duckdbExtensions, customRepo)
}

func main() {
	if err := runDownloadExtensions(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "duckdb extension download failed: %v\n", err)
		os.Exit(1)
	}
}
