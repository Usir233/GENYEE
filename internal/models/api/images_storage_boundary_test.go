package api

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func storedImageFixture(t *testing.T) (string, []byte) {
	t.Helper()
	parent := t.TempDir()
	base := filepath.Join(parent, "files")
	require.NoError(t, os.Mkdir(base, 0700))
	data := []byte("\x89PNG\r\n\x1a\naudit-only image fixture")
	require.NoError(t, os.WriteFile(filepath.Join(base, "allowed.png"), data, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "outside.png"), data, 0600))
	require.NoError(t, os.Symlink("allowed.png", filepath.Join(base, "inside-link.png")))
	require.NoError(t, os.Symlink(filepath.Join(parent, "outside.png"), filepath.Join(base, "outside-link.png")))
	t.Setenv("LOCAL_STORAGE_BASE_DIR", base)
	original := LocalImageResolver
	LocalImageResolver = nil
	t.Cleanup(func() { LocalImageResolver = original })
	return base, data
}

func assertStoredImageUnreadable(t *testing.T, ref string) {
	t.Helper()
	require.Nil(t, ResolveImageURLForOllama(ref), "denied paths must not expose local file bytes")
	require.Equal(t, ref, ResolveImageURLForLLM(ref), "denied paths must not become provider-bound data URIs")
}

// The application resolver returns false on catalog, tenant, backend and file
// errors. A local copy must never bypass that authoritative failure.
func TestStoredImageResolverFailureDoesNotFallBackToDisk(t *testing.T) {
	storedImageFixture(t)
	var requested []string
	LocalImageResolver = func(ref string) ([]byte, bool) {
		requested = append(requested, ref)
		return nil, false
	}
	for _, ref := range []string{
		"local://allowed.png",
		"local://../outside.png",
		"local://outside-link.png",
	} {
		t.Run(ref, func(t *testing.T) { assertStoredImageUnreadable(t, ref) })
	}
	require.NotEmpty(t, requested)
}

func TestStoredImageFallbackStaysWithinLocalStorage(t *testing.T) {
	base, data := storedImageFixture(t)
	// Object/resource identifiers are not local filenames, even if similarly
	// named files happen to exist under the base directory.
	for _, ref := range []string{"s3://bucket/object.png", "resource://entry/object.png", "storage://backend/local://object.png"} {
		path := filepath.Join(base, filepath.FromSlash(ref))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	for _, ref := range []string{
		"local://../outside.png",
		"local://outside-link.png",
		"s3://bucket/object.png",
		"resource://entry/object.png",
		"storage://backend/local://object.png",
	} {
		t.Run(ref, func(t *testing.T) { assertStoredImageUnreadable(t, ref) })
	}
}

func TestStoredImageFallbackAllowsFilesAndInternalSymlinks(t *testing.T) {
	_, data := storedImageFixture(t)
	for _, ref := range []string{"local://allowed.png", "local://inside-link.png"} {
		t.Run(ref, func(t *testing.T) {
			require.Equal(t, data, ResolveImageURLForOllama(ref))
			want := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
			require.Equal(t, want, ResolveImageURLForLLM(ref))
		})
	}
}

func TestStoredImageResolverSuccessRemainsAuthoritative(t *testing.T) {
	_, data := storedImageFixture(t)
	for _, ref := range []string{
		"local://tenant-specific/image.png",
		"minio://bucket/image.png", "cos://bucket/image.png", "tos://bucket/image.png",
		"s3://bucket/image.png", "oss://bucket/image.png", "ks3://bucket/image.png",
		"obs://bucket/image.png", "dummy://bucket/image.png",
		"resource://entry/image.png", "storage://backend/local://tenant/image.png",
	} {
		t.Run(ref, func(t *testing.T) {
			LocalImageResolver = func(requested string) ([]byte, bool) {
				// No corresponding file exists in the environment fallback.
				require.Equal(t, ref, requested)
				return data, true
			}
			require.Equal(t, data, ResolveImageURLForOllama(ref))
			require.Contains(t, ResolveImageURLForLLM(ref), base64.StdEncoding.EncodeToString(data))
		})
	}
}
