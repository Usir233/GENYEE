package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, root, path, contents string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalTreeAndDeterministicManifest(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "LICENCE", "Original British-spelling license\r\nCopyright Fixture\r\n")
	fixture(t, root, "native/vendor/licenses/BSD.txt", "Original native vendor license\n")
	fixture(t, root, "native/THIRD_PARTY_NOTICES.txt", "Original bundled third-party notice\n")
	fixture(t, root, "copyright_test.go", "package fixture\n")
	files, err := collectLicenses(root, "modules/example.com/native@v1.0.0")
	if err != nil || len(files) != 3 {
		t.Fatalf("files=%+v error=%v", files, err)
	}
	result := manifest{SchemaVersion: 1, GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1",
		Modules: []moduleNotice{{Path: "example.com/native", Version: "v1.0.0", Sum: "h1:fixture", Licenses: files}}}
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	if err := writeBundle(first, result); err != nil {
		t.Fatal(err)
	}
	if err := writeBundle(second, result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go-dependencies.json", "go-third-party-notices.txt"} {
		a, _ := os.ReadFile(filepath.Join(first, name))
		b, _ := os.ReadFile(filepath.Join(second, name))
		if !bytes.Equal(a, b) {
			t.Fatalf("non-deterministic %s", name)
		}
		if bytes.Contains(a, []byte(root)) {
			t.Fatal("host path leaked into artifact")
		}
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(file.BundlePath)))
		if err != nil || !bytes.Equal(data, file.data) {
			t.Fatalf("original file changed: %s", file.Path)
		}
	}
	metadata, _ := os.ReadFile(filepath.Join(first, "go-dependencies.json"))
	var actual manifest
	if err := json.Unmarshal(metadata, &actual); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(filepath.Join(first, "go-third-party-notices.txt"))
	if actual.NoticesSHA256 != digest(text) {
		t.Fatal("notice content hash mismatch")
	}
}

func TestRuntimeIncludesVendorAndExcludesTestAndCompilerOnlyNotices(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "src/crypto/x509/testdata/UserNotice.crt", "\x00\xff")
	for _, path := range []string{"LICENSE", "src/vendor/example/LICENSE", "src/crypto/active/LICENSE", "src/cmd/vendor/unused/LICENSE", "src/runtime/testdata/fixture/LICENSE", "src/unused/LICENSE"} {
		fixture(t, root, path, path)
	}
	files, err := runtimeLicenses(root, "go1.fixture", map[string]bool{filepath.Join(root, "src/crypto/active"): true})
	if err != nil || len(files) != 3 {
		t.Fatalf("files=%+v error=%v", files, err)
	}
	for _, file := range files {
		if strings.Contains(file.Path, "unused") || strings.Contains(file.Path, "testdata") {
			t.Fatalf("unshipped runtime notice: %s", file.Path)
		}
	}
	if err := os.Remove(filepath.Join(root, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeLicenses(root, "go1.fixture", nil); err == nil {
		t.Fatal("vendor license hid a missing Go runtime LICENSE")
	}
}

func TestMissingLicenseAndChangedFallbackIdentityFail(t *testing.T) {
	root := t.TempDir()
	if _, err := supplementalLicense(root, &module{Path: "example.com/unknown", Version: "v1"}, "modules/x"); err == nil {
		t.Fatal("unknown missing license accepted")
	}
	for _, mod := range []*module{
		{Path: "github.com/asg017/sqlite-vec-go-bindings", Version: "v0.1.7", Sum: "h1:Nx0jAzyS38XpkKznJ9xQjFXz2X9tI7KqjwVxV8RNoww="},
		{Path: "github.com/asg017/sqlite-vec-go-bindings", Version: "v0.1.6", Sum: "h1:changed"},
	} {
		if _, err := supplementalLicense(root, mod, "modules/x"); err == nil {
			t.Fatal("unreviewed fallback accepted")
		}
	}
	fixture(t, root, "licenses/go-supplemental/sqlite-vec-v0.1.6-LICENSE-MIT.txt", "changed")
	_, err := supplementalLicense(root, &module{Path: "github.com/asg017/sqlite-vec-go-bindings", Version: "v0.1.6", Sum: "h1:Nx0jAzyS38XpkKznJ9xQjFXz2X9tI7KqjwVxV8RNoww="}, "modules/x")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected fallback hash failure, got %v", err)
	}
}

func TestEmptyLicenseAndUnsafeOutputFail(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "LICENSE", " \n")
	if _, err := collectLicenses(root, "modules/example"); err == nil {
		t.Fatal("empty license accepted")
	}
	if _, err := safePath(root, "../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := writeBundle(root, manifest{}); err == nil {
		t.Fatal("existing destination replaced")
	}
}

func TestDuckDBNativeSnapshotAndVersionGuards(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := &module{Path: "github.com/duckdb/duckdb-go-bindings", Version: "v0.10502.0", Sum: "h1:Uhg/dfvPLQv4cH35lMD48hqUcdOh2Z7bcuykjr4qnOA="}
	files, err := duckDBNativeLicenses(root, mod, "modules/duckdb")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 33 {
		t.Fatalf("got %d native originals", len(files))
	}
	for _, name := range []string{"third_party/mbedtls/LICENSE", "third_party/re2/LICENSE", "extension/icu/third_party/icu/LICENSE", "extension/tpcds/dsdgen/dsdgen-c/date.cpp.legal-notice.txt"} {
		found := false
		for _, file := range files {
			if strings.HasSuffix(file.Path, name) && len(file.data) > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing native notice %s", name)
		}
	}
	mod.Version = "v0.10503.0"
	if _, err := duckDBNativeLicenses(root, mod, "x"); err == nil {
		t.Fatal("unreviewed DuckDB native version accepted")
	}
}

func TestRustGateValidatesTargetAndOriginalHashes(t *testing.T) {
	root := t.TempDir()
	if err := verifyRustNative(root, "darwin", "arm64"); err == nil {
		t.Fatal("missing anydoc Rust notices accepted")
	}
	license := "Original Rust crate license\n"
	fixture(t, root, "licenses/rust/originals/example/LICENSE", license)
	entry := map[string]any{"name": "example", "version": "1.0.0", "licenses": []licenseFile{{Path: "LICENSE", SHA256: digest([]byte(license)), BundlePath: "originals/example/LICENSE"}}}
	lock := "fixture Cargo.lock\n"
	fixture(t, root, "Cargo.lock", lock)
	metadata := map[string]any{"schemaVersion": 1, "target": "aarch64-apple-darwin", "lockSha256": digest([]byte(lock)), "packages": []any{entry}, "runtime": entry}
	data, _ := json.Marshal(metadata)
	fixture(t, root, "licenses/rust/rust-dependencies.json", string(data))
	if err := verifyRustNative(root, "darwin", "arm64"); err != nil {
		t.Fatal(err)
	}
	if err := verifyRustNative(root, "linux", "amd64"); err == nil {
		t.Fatal("wrong Rust target accepted")
	}
	fixture(t, root, "Cargo.lock", "changed lock")
	if err := verifyRustNative(root, "darwin", "arm64"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale lock rejection, got %v", err)
	}
	fixture(t, root, "Cargo.lock", lock)
	fixture(t, root, "licenses/rust/originals/example/LICENSE", "changed")
	if err := verifyRustNative(root, "darwin", "arm64"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected original integrity error, got %v", err)
	}
}
