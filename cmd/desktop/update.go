package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/handler"
	"github.com/Usir233/GENYEE/internal/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/mod/semver"
)

const (
	maxUpdateChecksumBytes = 1 << 20 // 1 MiB checksum manifest limit
	maxUpdateBinaryBytes   = 2 << 30 // 2 GiB installer binary limit
	windowsUpdateScript    = "@echo off\r\n" +
		"setlocal DisableDelayedExpansion\r\n" +
		"timeout /t 2 /nobreak >nul\r\n" +
		"start /wait \"\" \"%GENYEE_UPDATER_INSTALLER%\" /S\r\n" +
		"start \"\" \"%GENYEE_UPDATER_TARGET_EXE%\"\r\n" +
		"del \"%~f0\"\r\n"
	darwinUpdateScript = `#!/bin/bash
set -euo pipefail
APP_BUNDLE="$1"
NEW_APP="$2"
APP_DIR="$3"
MOUNT_POINT="$4"
STAGE_DIR="$5"

sleep 2
if ! (rm -rf -- "$APP_BUNDLE" && cp -a -- "$NEW_APP" "$APP_DIR"); then
    osascript - "$APP_BUNDLE" "$NEW_APP" "$APP_DIR" <<'APPLESCRIPT'
on run argv
    set targetBundle to quoted form of (item 1 of argv)
    set sourceBundle to quoted form of (item 2 of argv)
    set destDir to quoted form of (item 3 of argv)
    set copyCmd to "rm -rf -- " & targetBundle & " && cp -a -- " & sourceBundle & " " & destDir
    do shell script copyCmd with administrator privileges
end run
APPLESCRIPT
fi
hdiutil detach "$MOUNT_POINT" -force >/dev/null 2>&1 || true
rmdir "$MOUNT_POINT" >/dev/null 2>&1 || true
rm -rf -- "$STAGE_DIR" >/dev/null 2>&1 || true
open -- "$APP_BUNDLE"
rm -f -- "$0"
`
)

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

// sanitizeUpdateAssetFilename strips directory components via filepath.Base and
// rejects empty, dot, or flag-like names to prevent path traversal writes.
func sanitizeUpdateAssetFilename(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("update asset filename is empty")
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	base := filepath.Base(normalized)
	if base == "" || base == "." || base == ".." || base == "/" || strings.HasPrefix(base, "-") {
		return "", fmt.Errorf("invalid update asset filename %q", raw)
	}
	if strings.ContainsAny(base, "/\\\x00\r\n") {
		return "", fmt.Errorf("unsafe characters in update asset filename %q", raw)
	}
	return base, nil
}

// parseSHA256Digest validates and normalizes a "sha256:<hex>" or "<64-hex>" digest.
func parseSHA256Digest(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(s), "sha256:") {
		s = strings.TrimSpace(s[len("sha256:"):])
	}
	if len(s) != sha256.Size*2 {
		return "", fmt.Errorf("invalid sha256 digest length: %d", len(s))
	}
	decoded, err := hex.DecodeString(s)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid sha256 hex digest")
	}
	return strings.ToLower(s), nil
}

// extractChecksumForAsset finds the SHA-256 hex digest for safeFilename inside a
// standard sha256sum manifest or a single-file .sha256 companion file.
func extractChecksumForAsset(manifest string, safeFilename string) (string, error) {
	lines := strings.Split(manifest, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 1 {
			// Companion <asset>.sha256 containing only the digest when unambiguous.
			if len(lines) <= 2 {
				if digest, err := parseSHA256Digest(fields[0]); err == nil {
					return digest, nil
				}
			}
			continue
		}
		candidateHash := fields[0]
		targetPart := strings.TrimSpace(strings.TrimPrefix(line[len(fields[0]):], " "))
		targetPart = strings.TrimSpace(targetPart)
		targetPart = strings.TrimPrefix(targetPart, "*")
		targetBase, err := sanitizeUpdateAssetFilename(targetPart)
		if err != nil || targetBase != safeFilename {
			continue
		}
		return parseSHA256Digest(candidateHash)
	}
	return "", fmt.Errorf("no sha256 entry found for %s", safeFilename)
}

var validGitHubRepoSegment = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func isLoopbackUpdateHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateSafeUpdateURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("update URL is empty")
	}
	if strings.ContainsAny(trimmed, " \t\r\n\x00") {
		return nil, errors.New("update URL contains unsafe whitespace or control characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid update URL %q", raw)
	}
	if u.User != nil {
		return nil, errors.New("update URL must not embed user credentials")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u, nil
	case "http":
		if isLoopbackUpdateHost(u.Hostname()) {
			return u, nil
		}
		return nil, fmt.Errorf("cleartext HTTP update URL is only permitted on loopback: %s", u.Hostname())
	default:
		return nil, fmt.Errorf("unsupported update URL scheme %q", u.Scheme)
	}
}

func isValidGitHubOwnerRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return false
	}
	for _, seg := range parts {
		if seg == "." || seg == ".." || !validGitHubRepoSegment.MatchString(seg) {
			return false
		}
	}
	return true
}

func checkSafeUpdateRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 update redirects")
	}
	if _, err := validateSafeUpdateURL(req.URL.String()); err != nil {
		return fmt.Errorf("blocked unsafe update redirect: %w", err)
	}
	if len(via) > 0 &&
		(!strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) ||
			!strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
		req.Header.Del("Authorization")
	}
	return nil
}

func newUpdateHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: checkSafeUpdateRedirect,
	}
}

func withSafeUpdateRedirects(client *http.Client, fallbackTimeout time.Duration) *http.Client {
	if client == nil {
		return newUpdateHTTPClient(fallbackTimeout)
	}
	copied := *client
	copied.CheckRedirect = checkSafeUpdateRedirect
	return &copied
}

func drainBoundedBody(body io.Reader) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
}

func showDesktopDialog(ctx context.Context, opts wailsruntime.MessageDialogOptions) string {
	choice, err := wailsruntime.MessageDialog(ctx, opts)
	if err != nil {
		logger.Warnf(context.Background(), "Desktop dialog %q failed: %v", opts.Title, err)
		return ""
	}
	return choice
}

func decodeFixedSizeKeyOrSig(raw string, expectedLen int) ([]byte, bool) {
	if decodedHex, hexErr := hex.DecodeString(raw); hexErr == nil && len(decodedHex) == expectedLen {
		return decodedHex, true
	}
	if decodedB64, b64Err := base64.StdEncoding.DecodeString(raw); b64Err == nil && len(decodedB64) == expectedLen {
		return decodedB64, true
	}
	return nil, false
}

func verifyManifestEd25519(manifest []byte, sigBytes []byte, pubKeyRaw string) error {
	pubBytes, ok := decodeFixedSizeKeyOrSig(strings.TrimSpace(pubKeyRaw), ed25519.PublicKeySize)
	if !ok {
		return errors.New(
			"invalid GENYEE_UPDATE_PUBLIC_KEY: must be a 32-byte Ed25519 public key in hex or standard base64",
		)
	}

	sig := sigBytes
	if decodedSig, sigOK := decodeFixedSizeKeyOrSig(strings.TrimSpace(string(sigBytes)), ed25519.SignatureSize); sigOK {
		sig = decodedSig
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(pubBytes), manifest, sig) {
		return errors.New("update manifest Ed25519 signature verification failed")
	}
	return nil
}

func fetchSmallAssetBytes(client *http.Client, downloadURL string) ([]byte, error) {
	return fetchSmallAssetBytesWithContext(context.Background(), client, downloadURL)
}

func fetchSmallAssetBytesWithContext(ctx context.Context, client *http.Client, downloadURL string) ([]byte, error) {
	parsedURL, err := validateSafeUpdateURL(downloadURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GENYEE-Lite-Desktop-App")
	safeClient := withSafeUpdateRedirects(client, 10*time.Second)
	resp, err := safeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		drainBoundedBody(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Debugf(context.Background(), "Close HTTP response body: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdateChecksumBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxUpdateChecksumBytes {
		return nil, errors.New("checksum manifest exceeds maximum size")
	}
	return data, nil
}

// resolveExpectedAssetSHA256 resolves the expected SHA-256 digest for selected.
// Fails closed if no digest is present or if signature verification fails.
func resolveExpectedAssetSHA256(
	client *http.Client,
	assets []githubAsset,
	selected githubAsset,
	safeFilename string,
) (string, error) {
	return resolveExpectedAssetSHA256WithContext(context.Background(), client, assets, selected, safeFilename)
}

func resolveExpectedAssetSHA256WithContext(
	ctx context.Context,
	client *http.Client,
	assets []githubAsset,
	selected githubAsset,
	safeFilename string,
) (string, error) {
	pubKey := strings.TrimSpace(os.Getenv("GENYEE_UPDATE_PUBLIC_KEY"))
	if pubKey == "" && strings.TrimSpace(selected.Digest) != "" {
		return parseSHA256Digest(selected.Digest)
	}

	checksumNames := map[string]bool{
		strings.ToLower(safeFilename + ".sha256"): true,
		"sha256sums":       true,
		"sha256sums.txt":   true,
		"checksums.txt":    true,
		"checksums.sha256": true,
	}
	byLowerName := make(map[string]githubAsset, len(assets))
	for _, a := range assets {
		byLowerName[strings.ToLower(strings.TrimSpace(a.Name))] = a
	}

	for _, a := range assets {
		lower := strings.ToLower(strings.TrimSpace(a.Name))
		if !checksumNames[lower] || strings.TrimSpace(a.BrowserDownloadURL) == "" {
			continue
		}
		manifestBytes, err := fetchSmallAssetBytesWithContext(ctx, client, a.BrowserDownloadURL)
		if err != nil {
			continue
		}
		if pubKey != "" {
			sigAsset, ok := byLowerName[lower+".sig"]
			if !ok || strings.TrimSpace(sigAsset.BrowserDownloadURL) == "" {
				return "", errors.New("GENYEE_UPDATE_PUBLIC_KEY is set but manifest .sig asset is missing")
			}
			sigBytes, err := fetchSmallAssetBytesWithContext(ctx, client, sigAsset.BrowserDownloadURL)
			if err != nil {
				return "", fmt.Errorf("failed to download manifest signature: %w", err)
			}
			if err := verifyManifestEd25519(manifestBytes, sigBytes, pubKey); err != nil {
				return "", err
			}
		}
		if lower == strings.ToLower(safeFilename+".sha256") {
			fields := strings.Fields(string(manifestBytes))
			if len(fields) >= 1 {
				if digest, err := parseSHA256Digest(fields[0]); err == nil {
					return digest, nil
				}
			}
		}
		if digest, err := extractChecksumForAsset(string(manifestBytes), safeFilename); err == nil {
			return digest, nil
		}
	}

	if pubKey != "" {
		return "", errors.New("signed checksum manifest required when GENYEE_UPDATE_PUBLIC_KEY is configured")
	}
	return "", errors.New("refusing update without SHA-256 digest verification")
}

// verifyFileSHA256 computes the SHA-256 of filePath and compares it in constant time
// against expectedDigest. Fails closed if expectedDigest is empty or invalid.
func verifyFileSHA256(filePath string, expectedDigest string) (err error) {
	normalizedExpected, err := parseSHA256Digest(expectedDigest)
	if err != nil {
		return fmt.Errorf("missing or invalid expected sha256 digest: %w", err)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return err
	}
	actualHex := hex.EncodeToString(hasher.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actualHex), []byte(normalizedExpected)) != 1 {
		return fmt.Errorf("update file sha256 mismatch: got %s, want %s", actualHex, normalizedExpected)
	}
	return nil
}

func validateSafeWindowsUpdatePath(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("empty update path")
	}
	if strings.ContainsAny(p, "\"%&|<>^\x00\r\n") {
		return fmt.Errorf("unsafe characters in windows update path %q", p)
	}
	return nil
}

// resolveUpdateReleaseURL returns the release metadata endpoint configured via
// GENYEE_UPDATE_URL or GENYEE_UPDATE_REPO (e.g. "owner/repo"). When neither is
// set (or when the configured value fails transport/format validation), it
// returns ("", false) so customer desktops never phone home or downgrade by default.
func resolveUpdateReleaseURL() (string, bool) {
	if u := strings.TrimSpace(os.Getenv("GENYEE_UPDATE_URL")); u != "" {
		parsed, err := validateSafeUpdateURL(u)
		if err != nil {
			logger.Warnf(context.Background(), "Ignoring unsafe GENYEE_UPDATE_URL: %v", err)
			return "", false
		}
		return parsed.String(), true
	}
	if repo := strings.Trim(strings.TrimSpace(os.Getenv("GENYEE_UPDATE_REPO")), "/"); repo != "" {
		if !isValidGitHubOwnerRepo(repo) {
			logger.Warnf(context.Background(), "Ignoring invalid GENYEE_UPDATE_REPO %q (expected owner/repo)", repo)
			return "", false
		}
		return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo), true
	}
	return "", false
}

// isAutoUpdateEnabled reports whether background update checks should run.
// Defaults to false when no custom update endpoint is configured, or when
// GENYEE_AUTO_UPDATE is not explicitly enabled.
func isAutoUpdateEnabled() bool {
	_, configured := resolveUpdateReleaseURL()
	if !configured {
		return false
	}
	if v := strings.TrimSpace(os.Getenv("GENYEE_AUTO_UPDATE")); v != "" {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return false
}

func buildUpdateReleaseRequest(releaseURL string, githubToken string) (*http.Request, error) {
	return buildUpdateReleaseRequestWithContext(context.Background(), releaseURL, githubToken)
}

func buildUpdateReleaseRequestWithContext(
	ctx context.Context,
	releaseURL string,
	githubToken string,
) (*http.Request, error) {
	parsedURL, err := validateSafeUpdateURL(releaseURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GENYEE-Lite-Desktop-App")
	if trimmedToken := strings.TrimSpace(githubToken); trimmedToken != "" &&
		strings.EqualFold(parsedURL.Scheme, "https") &&
		strings.EqualFold(parsedURL.Hostname(), "api.github.com") {
		req.Header.Set("Authorization", "Bearer "+trimmedToken)
	}
	return req, nil
}

func checkUpdate(ctx context.Context, currentVersion string, showUpToDate bool, autoDownload bool) {
	go func() {
		releaseURL, configured := resolveUpdateReleaseURL()
		if !configured {
			if showUpToDate {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.InfoDialog,
					Title:         "Check for Updates",
					Message:       "Online update checks are currently disabled.",
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		if currentVersion == "unknown" || currentVersion == "" {
			if showUpToDate {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.InfoDialog,
					Title:         "Check for Updates",
					Message:       "Unable to determine the current version. Cannot check for updates.",
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		if !strings.HasPrefix(currentVersion, "v") {
			currentVersion = "v" + currentVersion
		}

		client := newUpdateHTTPClient(10 * time.Second)
		req, err := buildUpdateReleaseRequestWithContext(ctx, releaseURL, os.Getenv("GITHUB_TOKEN"))
		if err != nil {
			logger.Warnf(context.Background(), "Check update failed: %v", err)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			logger.Warnf(context.Background(), "Check update failed: %v", err)
			if showUpToDate {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Check Update Failed",
					Message:       fmt.Sprintf("Failed to connect to update server:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}
		defer func() {
			drainBoundedBody(resp.Body)
			if closeErr := resp.Body.Close(); closeErr != nil {
				logger.Debugf(context.Background(), "Close HTTP response body: %v", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			logger.Warnf(context.Background(), "Update server returned status code: %d", resp.StatusCode)
			if showUpToDate {
				msg := fmt.Sprintf("Update server returned an unexpected status code: %d", resp.StatusCode)
				if resp.StatusCode == http.StatusForbidden {
					msg = "Update server rate limit reached. Please try again later."
				}
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Check Update Failed",
					Message:       msg,
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		var release githubRelease
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpdateChecksumBytes)).Decode(&release); err != nil {
			logger.Warnf(context.Background(), "Failed to parse release info: %v", err)
			return
		}

		latestVersion := release.TagName
		if !strings.HasPrefix(latestVersion, "v") {
			latestVersion = "v" + latestVersion
		}

		if semver.IsValid(latestVersion) && semver.IsValid(currentVersion) {
			if semver.Compare(latestVersion, currentVersion) > 0 {
				selectedAsset, foundAsset := findBestReleaseAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
				var safeName, expectedSHA256 string
				if foundAsset && selectedAsset.BrowserDownloadURL != "" {
					var nameErr, hashErr error
					safeName, nameErr = sanitizeUpdateAssetFilename(selectedAsset.Name)
					if nameErr != nil {
						logger.Warnf(
							context.Background(),
							"Rejected unsafe update asset name %q: %v",
							selectedAsset.Name, nameErr,
						)
						foundAsset = false
					} else {
						expectedSHA256, hashErr = resolveExpectedAssetSHA256WithContext(
							ctx, client, release.Assets, selectedAsset, safeName,
						)
						if hashErr != nil {
							logger.Warnf(
								context.Background(),
								"Rejected unverified update asset %q: %v",
								safeName, hashErr,
							)
							foundAsset = false
						}
					}
				}

				if autoDownload && foundAsset && selectedAsset.BrowserDownloadURL != "" {
					downloadAndInstall(
						ctx, selectedAsset.BrowserDownloadURL, safeName, expectedSHA256, latestVersion, true,
					)
					return
				}

				msg := fmt.Sprintf(
					"A new version of GENYEE Lite is available!\n\n"+
						"Current version: %s\nLatest version: %s\n\nWould you like to download it now?",
					currentVersion, latestVersion,
				)
				choice := showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.InfoDialog,
					Title:         "Update Available",
					Message:       msg,
					Buttons:       []string{"Download", "Cancel"},
					DefaultButton: "Download",
				})
				if choice == "Download" {
					if foundAsset && selectedAsset.BrowserDownloadURL != "" {
						downloadAndInstall(
							ctx, selectedAsset.BrowserDownloadURL, safeName, expectedSHA256, latestVersion, false,
						)
					} else if release.HTMLURL != "" {
						if safeHTMLURL, urlErr := validateSafeUpdateURL(release.HTMLURL); urlErr == nil {
							wailsruntime.BrowserOpenURL(ctx, safeHTMLURL.String())
						} else {
							logger.Warnf(
								context.Background(),
								"Blocked unsafe release HTML URL %q: %v",
								release.HTMLURL, urlErr,
							)
						}
					}
				}
				return
			}
		}

		if showUpToDate {
			upToDateMsg := fmt.Sprintf(
				"You are using the latest version of GENYEE Lite.\n\nCurrent version: %s",
				currentVersion,
			)
			showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
				Type:          wailsruntime.InfoDialog,
				Title:         "Up to Date",
				Message:       upToDateMsg,
				Buttons:       []string{"OK"},
				DefaultButton: "OK",
			})
		}
	}()
}

func isChecksumMetadataAsset(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(lower, ".sha256") ||
		strings.HasSuffix(lower, ".sig") ||
		strings.HasSuffix(lower, ".asc") ||
		lower == "sha256sums" ||
		lower == "sha256sums.txt" ||
		lower == "checksums.txt" ||
		lower == "checksums.sha256"
}

func findBestReleaseAsset(assets []githubAsset, goos, goarch string) (githubAsset, bool) {
	osKeyword := ""
	switch goos {
	case "darwin":
		osKeyword = "mac"
	case "windows":
		osKeyword = "win"
	case "linux":
		osKeyword = "linux"
	}

	archKeyword := ""
	switch goarch {
	case "amd64":
		archKeyword = "amd64"
	case "arm64":
		archKeyword = "arm64"
	}

	// 1. Try to match both OS and Arch
	for _, asset := range assets {
		if isChecksumMetadataAsset(asset.Name) {
			continue
		}
		name := strings.ToLower(asset.Name)
		matchesArch := strings.Contains(name, archKeyword) ||
			strings.Contains(name, "universal") ||
			strings.Contains(name, "aarch64")
		if strings.Contains(name, osKeyword) && matchesArch {
			return asset, true
		}
	}

	// 2. Try to match OS only (e.g. universal binaries without arch in name)
	for _, asset := range assets {
		if isChecksumMetadataAsset(asset.Name) {
			continue
		}
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, osKeyword) {
			return asset, true
		}
	}

	// 3. MacOS specific fallback (e.g. .dmg)
	if goos == "darwin" {
		for _, asset := range assets {
			if isChecksumMetadataAsset(asset.Name) {
				continue
			}
			if strings.HasSuffix(strings.ToLower(asset.Name), ".dmg") {
				return asset, true
			}
		}
	}

	// 4. Windows specific fallback (e.g. .exe)
	if goos == "windows" {
		for _, asset := range assets {
			if isChecksumMetadataAsset(asset.Name) {
				continue
			}
			if strings.HasSuffix(strings.ToLower(asset.Name), ".exe") {
				return asset, true
			}
		}
	}

	return githubAsset{}, false
}

func downloadAndInstall(
	ctx context.Context,
	downloadURL string,
	filename string,
	expectedSHA256 string,
	latestVersion string,
	silent bool,
) {
	safeDownloadURL, err := validateSafeUpdateURL(downloadURL)
	if err != nil {
		logger.Warnf(context.Background(), "Refusing download from unsafe URL %q: %v", downloadURL, err)
		return
	}
	safeFilename, err := sanitizeUpdateAssetFilename(filename)
	if err != nil {
		logger.Warnf(context.Background(), "Refusing download with unsafe filename %q: %v", filename, err)
		return
	}
	normalizedSHA256, err := parseSHA256Digest(expectedSHA256)
	if err != nil {
		logger.Warnf(context.Background(), "Refusing download without valid SHA-256 digest: %v", err)
		return
	}

	stageDir, err := os.MkdirTemp("", "genyee-update-*")
	if err != nil {
		logger.Warnf(context.Background(), "Failed to create secure staging dir: %v", err)
		return
	}
	savePath := filepath.Join(stageDir, safeFilename)
	if filepath.Dir(savePath) != stageDir {
		_ = os.RemoveAll(stageDir)
		logger.Warnf(context.Background(), "Update path escaped staging dir: %s", savePath)
		return
	}

	go func() {
		cleanupStage := true
		defer func() {
			if cleanupStage {
				_ = os.RemoveAll(stageDir)
			}
		}()

		logger.Infof(context.Background(), "Starting download from %s to %s", safeDownloadURL.String(), savePath)
		client := newUpdateHTTPClient(30 * time.Minute)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, safeDownloadURL.String(), nil)
		if reqErr != nil {
			logger.Warnf(context.Background(), "Download request build failed: %v", reqErr)
			return
		}
		req.Header.Set("User-Agent", "GENYEE-Lite-Desktop-App")
		resp, err := client.Do(req)
		if err != nil {
			logger.Warnf(context.Background(), "Download failed: %v", err)
			if !silent {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Download Failed",
					Message:       fmt.Sprintf("Failed to download the update:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}
		defer func() {
			drainBoundedBody(resp.Body)
			if closeErr := resp.Body.Close(); closeErr != nil {
				logger.Debugf(context.Background(), "Close HTTP response body: %v", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			logger.Warnf(context.Background(), "Download failed, server returned status: %d", resp.StatusCode)
			if !silent {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Download Failed",
					Message:       fmt.Sprintf("Server returned status: %d", resp.StatusCode),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		out, err := os.OpenFile(savePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			logger.Warnf(context.Background(), "Failed to create file for download: %v", err)
			if !silent {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Save Failed",
					Message:       fmt.Sprintf("Failed to save the update file:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		written, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxUpdateBinaryBytes+1))
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil || written > maxUpdateBinaryBytes {
			logger.Warnf(
				context.Background(),
				"Error occurred during download copying: copyErr=%v closeErr=%v size=%d",
				copyErr, closeErr, written,
			)
			if !silent {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Download Error",
					Message:       "An error occurred while downloading the update.",
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		if err := verifyFileSHA256(savePath, normalizedSHA256); err != nil {
			logger.Warnf(context.Background(), "Update integrity verification failed: %v", err)
			if !silent {
				showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Update Verification Failed",
					Message:       "The downloaded update failed SHA-256 verification and was discarded.",
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		logger.Infof(context.Background(), "Download and SHA-256 verification completed successfully: %s", savePath)

		readyMsg := fmt.Sprintf(
			"GENYEE Lite %s has been downloaded and verified.\n\n"+
				"Would you like to restart and install the new version now?",
			latestVersion,
		)
		choice := showDesktopDialog(ctx, wailsruntime.MessageDialogOptions{
			Type:          wailsruntime.InfoDialog,
			Title:         "Update Ready",
			Message:       readyMsg,
			Buttons:       []string{"Restart Now", "Later"},
			DefaultButton: "Restart Now",
		})

		if choice == "Restart Now" {
			cleanupStage = false
			applyUpdateAndRestart(ctx, savePath, stageDir)
		}
	}()
}

// desktopAboutVersion 优先使用构建脚本注入的 handler.Version，否则尝试读取仓库根目录 VERSION（本地 wails dev 等未带 ldflags 时）。
func desktopAboutVersion() string {
	if v := strings.TrimSpace(handler.Version); v != "" && v != "unknown" {
		return v
	}
	for _, p := range []string{
		"VERSION",
		filepath.Join("..", "..", "VERSION"),
		filepath.Join("..", "..", "..", "VERSION"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	return "unknown"
}

// applyUpdateAndRestart applies the verified update and restarts the application.
func applyUpdateAndRestart(ctx context.Context, savePath string, stageDir string) {
	switch runtime.GOOS {
	case "windows":
		applyWindowsUpdate(ctx, savePath, stageDir)
	case "darwin":
		applyDarwinUpdate(ctx, savePath, stageDir)
	default:
		if err := exec.Command("xdg-open", savePath).Start(); err != nil {
			logger.Warnf(context.Background(), "Failed to launch xdg-open for %s: %v", savePath, err)
			_ = os.RemoveAll(stageDir)
			return
		}
		wailsruntime.Quit(ctx)
	}
}

func applyWindowsUpdate(ctx context.Context, savePath string, stageDir string) {
	execPath, err := os.Executable()
	if err != nil {
		logger.Warnf(context.Background(), "Failed to get executable path: %v", err)
		_ = os.RemoveAll(stageDir)
		return
	}
	if err := validateSafeWindowsUpdatePath(savePath); err != nil {
		logger.Warnf(context.Background(), "Unsafe installer path: %v", err)
		_ = os.RemoveAll(stageDir)
		return
	}
	if err := validateSafeWindowsUpdatePath(execPath); err != nil {
		logger.Warnf(context.Background(), "Unsafe executable path: %v", err)
		_ = os.RemoveAll(stageDir)
		return
	}
	scriptDir, err := os.MkdirTemp("", "genyee-win-upd-*")
	if err != nil {
		logger.Warnf(context.Background(), "Failed to create update script dir: %v", err)
		_ = os.RemoveAll(stageDir)
		return
	}
	scriptPath := filepath.Join(scriptDir, "genyee_update.bat")
	if err := os.WriteFile(scriptPath, []byte(windowsUpdateScript), 0o700); err != nil {
		logger.Warnf(context.Background(), "Failed to write update script: %v", err)
		_ = os.RemoveAll(scriptDir)
		_ = os.RemoveAll(stageDir)
		return
	}

	cmd := exec.Command("cmd.exe", "/d", "/c", scriptPath)
	cmd.Env = append(os.Environ(),
		"GENYEE_UPDATER_INSTALLER="+savePath,
		"GENYEE_UPDATER_TARGET_EXE="+execPath,
	)
	if err := cmd.Start(); err != nil {
		logger.Warnf(context.Background(), "Failed to start Windows update script: %v", err)
		_ = os.RemoveAll(scriptDir)
		_ = os.RemoveAll(stageDir)
		return
	}
	wailsruntime.Quit(ctx)
}

func applyDarwinUpdate(ctx context.Context, savePath string, stageDir string) {
	if !strings.HasSuffix(strings.ToLower(savePath), ".dmg") {
		if err := exec.Command("open", "--", savePath).Start(); err != nil {
			logger.Warnf(context.Background(), "Failed to open macOS update artifact %s: %v", savePath, err)
			_ = os.RemoveAll(stageDir)
			return
		}
		wailsruntime.Quit(ctx)
		return
	}

	go func() {
		execPath, err := os.Executable()
		if err != nil {
			logger.Warnf(context.Background(), "Failed to get executable path: %v", err)
			_ = os.RemoveAll(stageDir)
			return
		}

		appBundlePath := filepath.Dir(filepath.Dir(filepath.Dir(execPath)))
		if !strings.HasSuffix(appBundlePath, ".app") {
			if startErr := exec.Command("open", "--", savePath).Start(); startErr != nil {
				logger.Warnf(context.Background(), "Failed to open dmg %s: %v", savePath, startErr)
				_ = os.RemoveAll(stageDir)
				return
			}
			wailsruntime.Quit(ctx)
			return
		}

		mountPoint, err := os.MkdirTemp("", "genyee-dmg-mount-*")
		if err != nil {
			logger.Warnf(context.Background(), "Failed to create dmg mount point: %v", err)
			_ = os.RemoveAll(stageDir)
			return
		}
		detachAndCleanup := func() {
			_ = exec.Command("hdiutil", "detach", mountPoint, "-force").Run()
			_ = os.Remove(mountPoint)
			_ = os.RemoveAll(stageDir)
		}

		cmdMount := exec.Command(
			"hdiutil", "attach", savePath,
			"-mountpoint", mountPoint, "-nobrowse", "-readonly", "-quiet",
		)
		if err := cmdMount.Run(); err != nil {
			logger.Warnf(context.Background(), "Failed to mount dmg: %v", err)
			_ = os.Remove(mountPoint)
			_ = os.RemoveAll(stageDir)
			return
		}

		entries, err := os.ReadDir(mountPoint)
		if err != nil {
			detachAndCleanup()
			return
		}

		var newAppPath string
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			safeEntry, nameErr := sanitizeUpdateAssetFilename(entry.Name())
			if nameErr == nil && strings.HasSuffix(safeEntry, ".app") {
				newAppPath = filepath.Join(mountPoint, safeEntry)
				break
			}
		}

		if newAppPath == "" {
			detachAndCleanup()
			return
		}

		if err := exec.Command("codesign", "--verify", "--deep", "--strict", newAppPath).Run(); err != nil {
			logger.Warnf(context.Background(), "Rejected unsigned or tampered macOS app bundle: %v", err)
			detachAndCleanup()
			return
		}

		appDir := filepath.Dir(appBundlePath)
		scriptFile, err := os.CreateTemp("", "genyee-macos-upd-*.sh")
		if err != nil {
			detachAndCleanup()
			return
		}
		scriptPath := scriptFile.Name()
		_, writeErr := scriptFile.WriteString(darwinUpdateScript)
		chmodErr := scriptFile.Chmod(0o700)
		closeErr := scriptFile.Close()
		if writeErr != nil || chmodErr != nil || closeErr != nil {
			_ = os.Remove(scriptPath)
			detachAndCleanup()
			return
		}

		startCmd := exec.Command(
			"/bin/bash", scriptPath, appBundlePath, newAppPath, appDir, mountPoint, stageDir,
		)
		if err := startCmd.Start(); err != nil {
			logger.Warnf(context.Background(), "Failed to start macOS update script: %v", err)
			_ = os.Remove(scriptPath)
			detachAndCleanup()
			return
		}
		wailsruntime.Quit(ctx)
	}()
}
