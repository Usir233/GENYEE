//go:build !bindings

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// desktopConfigRoot finds the directory holding config for this executable:
// Contents/Resources in a packaged .app, or the repo above a wails dev bundle.
// It goes by the executable and never the working directory, so starting the
// app from an untrusted folder cannot supply its config, keys or database.
func desktopConfigRoot(execPath string) (string, bool) {
	if i := strings.Index(execPath, ".app/Contents/MacOS"); i >= 0 {
		resources := filepath.Join(execPath[:i+len(".app/Contents")], "Resources")
		if hasDesktopConfig(resources) {
			return resources, true
		}
	}
	dir := filepath.Dir(execPath)
	for {
		if hasDesktopConfig(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func hasDesktopConfig(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "config", "config.yaml"))
	return err == nil
}

func loadOptionalDotenv(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat dotenv %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("dotenv path %q is a directory", path)
	}
	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("load dotenv %q: %w", path, err)
	}
	return nil
}

// loadDesktopEnv reads ENV_FILE when set, then root's .env.lite and .env.
// .env.lite is Lite's own overlay and wins on shared keys: godotenv does not
// overwrite a variable that is already set, so the repo's Docker/remote .env
// only fills in what Lite leaves unset. The packaged app has no .env.lite. The
// calls stay separate because Load returns early at a missing file.
func loadDesktopEnv(root string, found bool) error {
	if path := strings.TrimSpace(os.Getenv("ENV_FILE")); path != "" {
		if err := godotenv.Load(path); err != nil {
			return fmt.Errorf("load ENV_FILE %q: %w", path, err)
		}
	}
	if !found {
		return nil
	}
	if err := loadOptionalDotenv(filepath.Join(root, ".env.lite")); err != nil {
		return err
	}
	if err := loadOptionalDotenv(filepath.Join(root, ".env")); err != nil {
		return err
	}
	return nil
}
