// Package envfile loads optional dotenv configuration before the application
// container reads environment variables.
package envfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// maxEnvFileBytes bounds dotenv files read at startup (1 MiB).
const maxEnvFileBytes = 1 << 20

// Load reads ENV_FILE when it is set, on any build. Otherwise it only acts on
// Lite builds, loading .env.lite — first beside the running executable (the
// tarball layout), then the current working directory (the repo/dev layout).
//
// Process environment variables always take precedence over dotenv values.
// Missing default files (or directories named .env.lite) are ignored; an
// explicitly requested file, an oversized file, or invalid syntax is reported.
func Load(lite bool) error {
	if path := strings.TrimSpace(os.Getenv("ENV_FILE")); path != "" {
		if err := loadBoundedDotenv(path, false); err != nil {
			return fmt.Errorf("load ENV_FILE %q: %w", path, err)
		}
		return nil
	}
	if !lite {
		return nil
	}

	for _, path := range liteDotenvPaths() {
		err := loadBoundedDotenv(path, true)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errSkippedDirectory) {
				continue
			}
			return fmt.Errorf("load %s: %w", path, err)
		}
		return nil
	}
	return nil
}

var errSkippedDirectory = errors.New("dotenv candidate is a directory")

func loadBoundedDotenv(path string, skipDir bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if skipDir {
			return errSkippedDirectory
		}
		return fmt.Errorf("path is a directory, expected regular env file")
	}
	if info.Size() > maxEnvFileBytes {
		return fmt.Errorf("env file exceeds maximum size of %d bytes", maxEnvFileBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxEnvFileBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxEnvFileBytes {
		return fmt.Errorf("env file exceeds maximum size of %d bytes", maxEnvFileBytes)
	}

	parsed, err := godotenv.Parse(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	for k, v := range parsed {
		if _, exists := os.LookupEnv(k); !exists {
			if setErr := os.Setenv(k, v); setErr != nil {
				return setErr
			}
		}
	}
	return nil
}

// liteDotenvPaths returns the .env.lite candidate locations in priority
// order: beside the running executable first, then the working directory.
func liteDotenvPaths() []string {
	paths := []string{".env.lite"}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" && dir != "." {
			candidate := filepath.Join(dir, ".env.lite")
			if cwd, cwdErr := os.Getwd(); cwdErr != nil || filepath.Clean(dir) != filepath.Clean(cwd) {
				paths = append([]string{candidate}, paths...)
			}
		}
	}
	return paths
}
