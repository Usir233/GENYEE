package compat

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Usir233/GENYEE/cli/internal/xdg"
)

const (
	ttl           = 24 * time.Hour
	maxCacheBytes = 64 << 10
)

func cachePath() (string, error) {
	return xdg.Path("XDG_CACHE_HOME", ".cache", "server-info.yaml")
}

// LoadCache reads the cached Info. Returns (info, fresh, err).
//
//	info == nil when no cache exists (err == nil)
//	fresh == false when the cache is missing or TTL-expired
func LoadCache() (*Info, bool, error) {
	p, err := cachePath()
	if err != nil {
		return nil, false, err
	}
	data, err := xdg.ReadSecureFile(p, maxCacheBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read cache: %w", err)
	}
	var info Info
	if err := yaml.Unmarshal(data, &info); err != nil {
		return nil, false, fmt.Errorf("parse cache: %w", err)
	}
	age := time.Since(info.ProbedAt)
	fresh := !info.ProbedAt.IsZero() && age >= 0 && age < ttl
	return &info, fresh, nil
}

// SaveCache atomically writes Info to the cache file (mode 0600).
func SaveCache(info *Info) error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	return xdg.WriteAtomicYAML(p, info)
}
