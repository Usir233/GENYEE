package file

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTestFileHeader(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	h.Set("Content-Type", "text/plain")
	part, err := mw.CreatePart(h)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	mr := multipart.NewReader(&buf, mw.Boundary())
	form, err := mr.ReadForm(int64(len(content)) + 4096)
	require.NoError(t, err)
	t.Cleanup(func() { _ = form.RemoveAll() })
	require.NotEmpty(t, form.File["file"])
	return form.File["file"][0]
}

func TestLocalFileService_RejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("outside-secret"), 0o600))

	docDir := filepath.Join(base, "7", "doc")
	require.NoError(t, os.MkdirAll(docDir, 0o755))
	require.NoError(t, os.Symlink(secretPath, filepath.Join(docDir, "escape.txt")))
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "7", "escape_dir")))

	svc := NewLocalFileService(base, "")
	ctx := context.Background()

	_, err := svc.GetFile(ctx, "local://7/doc/escape.txt")
	require.Error(t, err, "GetFile must reject leaf symlink escaping baseDir")

	_, err = svc.GetFile(ctx, "local://7/escape_dir/secret.txt")
	require.Error(t, err, "GetFile must reject ancestor symlink escaping baseDir")

	err = svc.DeleteFile(ctx, "local://7/doc/escape.txt")
	require.Error(t, err, "DeleteFile must reject leaf symlink")
	err = svc.DeleteFile(ctx, "local://7/escape_dir/secret.txt")
	require.Error(t, err, "DeleteFile must reject ancestor symlink escaping baseDir")
	_, statErr := os.Stat(secretPath)
	require.NoError(t, statErr, "outside target file must remain untouched")

	_, err = svc.CopyFile(ctx, "local://7/doc/escape.txt", 7, "safe-dst")
	require.Error(t, err, "CopyFile must reject symlink source")

	validSrc := filepath.Join(docDir, "legit.txt")
	require.NoError(t, os.WriteFile(validSrc, []byte("ok"), 0o644))
	_, err = svc.CopyFile(ctx, "local://7/doc/legit.txt", 7, "escape_dir")
	require.Error(t, err, "CopyFile must reject destination directory symlink escaping baseDir")

	fh := makeTestFileHeader(t, "upload.txt", []byte("payload"))
	_, err = svc.SaveFile(ctx, fh, 7, "escape_dir")
	require.Error(t, err, "SaveFile must reject destination directory symlink escaping baseDir")

	require.NoError(t, os.Symlink(outside, filepath.Join(base, "7", "exports")))
	_, err = svc.SaveBytes(ctx, []byte("payload"), 7, "out.txt", false)
	require.Error(t, err, "SaveBytes must reject exports directory symlink escaping baseDir")
}

func TestNewFileServiceFromStorageConfig_RejectsTraversalLocalPathPrefix(t *testing.T) {
	sec := &types.StorageEngineConfig{
		DefaultProvider: "local",
		Local: &types.LocalEngineConfig{
			PathPrefix: "../../outside",
		},
	}
	svc, _, err := NewFileServiceFromStorageConfig("local", sec, t.TempDir())
	require.Error(t, err, "traversal PathPrefix must fail closed rather than falling back to root baseDir")
	assert.Nil(t, svc)
}

func TestResolveTenantFileServiceWithFallback_NilTenantNoPanic(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	base := t.TempDir()
	globalSvc := NewLocalFileService(base, "")

	require.NotPanics(t, func() {
		svc, provider, ok := ResolveTenantFileServiceWithFallback(
			context.Background(), "audit-test", nil, "", "local", base, nil, globalSvc,
		)
		assert.True(t, ok)
		assert.Equal(t, "local", provider)
		assert.Equal(t, globalSvc, svc)
	})

	require.NotPanics(t, func() {
		svc, _, ok := ResolveTenantFileServiceWithFallback(
			context.Background(), "audit-test", nil, "", "s3", base, nil, globalSvc,
		)
		assert.False(t, ok)
		assert.Nil(t, svc)
	})
}

func TestFileProviders_RejectInvalidTenantOrTraversalKnowledgeIDAndCOSHostInjection(t *testing.T) {
	ctx := context.Background()
	localSvc := NewLocalFileService(t.TempDir(), "")
	fh := makeTestFileHeader(t, "doc.txt", []byte("hello"))

	_, err := localSvc.SaveFile(ctx, nil, 7, "k1")
	require.Error(t, err, "nil multipart header must fail without panic")

	_, err = localSvc.SaveFile(ctx, fh, 0, "k1")
	require.Error(t, err, "zero tenantID must be rejected in SaveFile")

	_, err = localSvc.SaveFile(ctx, fh, 7, "../99/hijack")
	require.Error(t, err, "traversal knowledgeID within baseDir must still be rejected")

	_, err = localSvc.SaveBytes(ctx, []byte("x"), 0, "a.txt", false)
	require.Error(t, err, "zero tenantID must be rejected in SaveBytes")

	cosRaw, err := NewCosFileService("main-1250000000", "ap-shanghai", "ak", "sk", "genyee")
	require.NoError(t, err)
	_, err = cosRaw.SaveFile(ctx, fh, 7, "../99/hijack")
	require.Error(t, err, "COS SaveFile must reject traversal knowledgeID")
	_, err = cosRaw.SaveFile(ctx, fh, 0, "k1")
	require.Error(t, err, "COS SaveFile must reject zero tenantID")
	_, err = cosRaw.CopyFile(ctx, "cos://main-1250000000/ap-shanghai/genyee/7/k1/src.txt", 7, "../99/hijack")
	require.Error(t, err, "COS CopyFile must reject traversal knowledgeID")
	_, err = cosRaw.SaveBytes(ctx, []byte("x"), 0, "a.txt", false)
	require.Error(t, err, "COS SaveBytes must reject zero tenantID")

	for _, bad := range [][2]string{
		{"169.254.169.254/#", "ap-shanghai"},
		{"evil.com@x", "ap-shanghai"},
		{"bucket.with.dots", "ap-shanghai"},
		{"main-1250000000", "ap-shanghai/../../x"},
		{"main-1250000000", "169.254.169.254#"},
	} {
		_, err := newCosClient(bad[0], bad[1], "ak", "sk")
		require.Error(t, err, "newCosClient must reject host/URL breakout %v", bad)
	}
	_, err = NewCosFileServiceWithTempBucket(
		"main-1250000000", "ap-shanghai", "ak", "sk", "genyee",
		"169.254.169.254/#", "ap-beijing",
	)
	require.Error(t, err, "NewCosFileServiceWithTempBucket must reject unsafe tempBucketName")
}
