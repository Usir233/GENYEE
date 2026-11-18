package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeUpdateAssetFilenameBlocksTraversal(t *testing.T) {
	got, err := sanitizeUpdateAssetFilename("../../etc/GENYEE-Lite-mac-arm64.dmg")
	if err != nil || got != "GENYEE-Lite-mac-arm64.dmg" {
		t.Fatalf("expected traversal prefix to strip to base name, got=%q err=%v", got, err)
	}
	gotWin, err := sanitizeUpdateAssetFilename(`..\..\AppData\GENYEE-Setup.exe`)
	if err != nil || gotWin != "GENYEE-Setup.exe" {
		t.Fatalf("expected backslash traversal prefix to strip to base name, got=%q err=%v", gotWin, err)
	}
	for _, invalid := range []string{"", ".", "..", "/", "\\", "-rf"} {
		if name, err := sanitizeUpdateAssetFilename(invalid); err == nil {
			t.Fatalf("expected %q to be rejected, got %q", invalid, name)
		}
	}
}

func TestVerifyFileSHA256FailsClosedOnMissingOrMismatchedDigest(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "GENYEE-Update.dmg")
	content := []byte("verified-release-binary-payload")
	if err := os.WriteFile(binPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	validHex := hex.EncodeToString(sum[:])

	if err := verifyFileSHA256(binPath, ""); err == nil {
		t.Fatal("expected empty digest to fail closed")
	}
	if err := verifyFileSHA256(binPath, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected mismatched digest to fail closed")
	}
	if err := verifyFileSHA256(binPath, "sha256:"+validHex); err != nil {
		t.Fatalf("expected valid sha256 digest to pass, got %v", err)
	}
}

func TestResolveExpectedAssetSHA256AndEd25519Manifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("installer-bytes"))
	sumHex := hex.EncodeToString(sum[:])
	manifest := sumHex + "  GENYEE-Lite-linux-amd64.AppImage\n"
	sig := ed25519.Sign(priv, []byte(manifest))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(manifest))
		case "/SHA256SUMS.sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(sig)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	selected := githubAsset{
		Name:               "GENYEE-Lite-linux-amd64.AppImage",
		BrowserDownloadURL: srv.URL + "/GENYEE-Lite-linux-amd64.AppImage",
	}

	// Missing both digest and checksum manifest must fail closed.
	t.Setenv("GENYEE_UPDATE_PUBLIC_KEY", "")
	_, err = resolveExpectedAssetSHA256(
		srv.Client(), []githubAsset{selected}, selected, selected.Name,
	)
	if err == nil {
		t.Fatal("expected asset without digest or SHA256SUMS to be rejected")
	}

	// Signed manifest with configured GENYEE_UPDATE_PUBLIC_KEY passes.
	t.Setenv("GENYEE_UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	assets := []githubAsset{
		selected,
		{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/SHA256SUMS"},
		{Name: "SHA256SUMS.sig", BrowserDownloadURL: srv.URL + "/SHA256SUMS.sig"},
	}
	resolved, err := resolveExpectedAssetSHA256(srv.Client(), assets, selected, selected.Name)
	if err != nil || resolved != sumHex {
		t.Fatalf("resolveExpectedAssetSHA256() = (%q, %v), want (%q, nil)", resolved, err, sumHex)
	}
}

func TestUpdateScriptsHaveNoFormatStringInterpolation(t *testing.T) {
	if strings.Contains(darwinUpdateScript, "%"+"s") {
		t.Fatal("darwinUpdateScript must pass paths via positional arguments, not format interpolation")
	}
	if !strings.Contains(darwinUpdateScript, "quoted form of (item 1 of argv)") {
		t.Fatal("darwinUpdateScript must quote argv items inside AppleScript")
	}
	if strings.Contains(windowsUpdateScript, "%"+"s") {
		t.Fatal("windowsUpdateScript must read paths from environment variables, not format interpolation")
	}
	if err := validateSafeWindowsUpdatePath(`C:\Temp\evil"&calc.exe&.exe`); err == nil {
		t.Fatal("validateSafeWindowsUpdatePath must reject shell metacharacters")
	}
}

func TestResolveUpdateReleaseURL_RejectsCleartextRemoteAndRepoTraversal(t *testing.T) {
	for _, badURL := range []string{
		"http://updates.example.com/releases/latest",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://user:secret@updates.example.com/releases/latest",
	} {
		t.Setenv("GENYEE_UPDATE_URL", badURL)
		t.Setenv("GENYEE_UPDATE_REPO", "")
		if got, ok := resolveUpdateReleaseURL(); ok {
			t.Fatalf("expected unsafe GENYEE_UPDATE_URL %q to be rejected, got %q", badURL, got)
		}
	}

	for _, badRepo := range []string{
		"evil/../../users/victim",
		"owner/repo/extra",
		"owner/..",
		"../repo",
		"owner/repo?foo=bar",
	} {
		t.Setenv("GENYEE_UPDATE_URL", "")
		t.Setenv("GENYEE_UPDATE_REPO", badRepo)
		if got, ok := resolveUpdateReleaseURL(); ok {
			t.Fatalf("expected malformed GENYEE_UPDATE_REPO %q to be rejected, got %q", badRepo, got)
		}
	}

	t.Setenv("GENYEE_UPDATE_URL", "http://127.0.0.1:18080/releases/latest")
	t.Setenv("GENYEE_UPDATE_REPO", "")
	if got, ok := resolveUpdateReleaseURL(); !ok || got != "http://127.0.0.1:18080/releases/latest" {
		t.Fatalf("expected loopback http update URL for local tests to pass, got (%q, %v)", got, ok)
	}

	t.Setenv("GENYEE_UPDATE_URL", "")
	t.Setenv("GENYEE_UPDATE_REPO", "genyee/genyee-lite")
	const wantGitHubURL = "https://api.github.com/repos/genyee/genyee-lite/releases/latest"
	if got, ok := resolveUpdateReleaseURL(); !ok || got != wantGitHubURL {
		t.Fatalf("expected valid owner/repo to resolve, got (%q, %v)", got, ok)
	}
}

func TestUpdateSecurity_GitHubTokenScopeRedirectValidationAndManifestError(t *testing.T) {
	// 1. GITHUB_TOKEN must ONLY be sent to https://api.github.com, never custom update servers.
	customReq, err := buildUpdateReleaseRequest(
		"https://updates.enterprise.example/releases/latest",
		"ghp_secret_token",
	)
	if err != nil {
		t.Fatalf("buildUpdateReleaseRequest custom: %v", err)
	}
	if auth := customReq.Header.Get("Authorization"); auth != "" {
		t.Fatalf("leaked GITHUB_TOKEN to non-GitHub update host: %q", auth)
	}

	ghReq, err := buildUpdateReleaseRequest(
		"https://api.github.com/repos/genyee/genyee-lite/releases/latest",
		"ghp_secret_token",
	)
	if err != nil {
		t.Fatalf("buildUpdateReleaseRequest github: %v", err)
	}
	if auth := ghReq.Header.Get("Authorization"); auth != "Bearer ghp_secret_token" {
		t.Fatalf("expected Authorization header on api.github.com, got %q", auth)
	}

	// 2. fetchSmallAssetBytes must reject cleartext remote URLs and redirects to cleartext remote URLs.
	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example.com/SHA256SUMS", http.StatusFound)
	}))
	defer redirSrv.Close()

	if _, err := fetchSmallAssetBytes(nil, "http://evil.example.com/SHA256SUMS"); err == nil {
		t.Fatal("expected cleartext remote manifest URL to be rejected")
	}
	if _, err := fetchSmallAssetBytes(nil, redirSrv.URL+"/SHA256SUMS"); err == nil {
		t.Fatal("expected redirect to cleartext remote host to be rejected")
	}

	// 3. verifyManifestEd25519 invalid key error must not interpolate nil error.
	err = verifyManifestEd25519([]byte("manifest"), []byte("sig"), "not-a-valid-key")
	if err == nil || strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("expected descriptive invalid key error without <nil>, got %v", err)
	}

	// 4. Context cancellation aborts manifest fetch deterministically.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchSmallAssetBytesWithContext(canceledCtx, redirSrv.Client(), redirSrv.URL+"/SHA256SUMS")
	if err == nil {
		t.Fatal("expected canceled context to abort fetchSmallAssetBytesWithContext")
	}
}
