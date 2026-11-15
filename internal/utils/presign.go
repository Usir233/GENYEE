package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// presignPath is the URL path for presigned file access.
	presignPath = "/api/v1/files/presigned"
	// presignDefaultTTL is the default validity period for presigned URLs.
	// Kept short because the HMAC key alone authorizes cross-tenant access —
	// a leaked URL should expire before it can be widely abused. IM clients
	// typically fetch and cache images within seconds of receipt.
	presignDefaultTTL = 2 * time.Hour
)

// SystemHMACKey returns the deployment-wide HMAC key derived from
// SYSTEM_SIGNING_KEY (falling back to SYSTEM_AES_KEY for existing deployments),
// or nil when unset, too short, or a known example value.
// Callers must treat nil as "this deployment cannot sign", not as an empty key.
func SystemHMACKey() []byte {
	key := os.Getenv("SYSTEM_SIGNING_KEY")
	if key == "" {
		key = os.Getenv("SYSTEM_AES_KEY")
	}
	if len(key) < 16 || isPlaceholderAESKey(key) {
		return nil
	}
	return []byte(key)
}

// getPresignKey returns the HMAC key derived from SYSTEM_AES_KEY.
// Returns nil if the key is not configured or invalid.
func getPresignKey() []byte {
	return SystemHMACKey()
}

// signPayload computes HMAC-SHA256 over the canonical payload string.
func signPayload(key []byte, filePath string, tenantID uint64, expires int64) string {
	payload := fmt.Sprintf("file_path=%s&tenant_id=%d&expires=%d", filePath, tenantID, expires)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignFileURL generates a presigned HTTP URL for accessing a storage file.
// baseURL is the external URL of the GENYEE instance (e.g. "https://genyee.example.com").
// filePath is the provider:// storage path (e.g. "local://1/abc/img.png").
// tenantID identifies the tenant that owns the file.
// ttl is how long the URL remains valid (0 uses the default presignDefaultTTL).
//
// Returns ("", error) if the signing key is not configured.
func SignFileURL(baseURL, filePath string, tenantID uint64, ttl time.Duration) (string, error) {
	key := getPresignKey()
	if key == nil {
		return "", fmt.Errorf("presign: SYSTEM_AES_KEY not configured")
	}
	if ttl <= 0 {
		ttl = presignDefaultTTL
	}
	expires := time.Now().Add(ttl).Unix()
	sig := signPayload(key, filePath, tenantID, expires)

	u, err := url.Parse(strings.TrimRight(baseURL, "/") + presignPath)
	if err != nil {
		return "", fmt.Errorf("presign: invalid base URL: %w", err)
	}
	q := u.Query()
	q.Set("file_path", filePath)
	q.Set("tenant_id", strconv.FormatUint(tenantID, 10))
	q.Set("expires", strconv.FormatInt(expires, 10))
	q.Set("sig", sig)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// VerifyFileURLSig checks the HMAC signature and expiry of a presigned URL.
// Returns true only if the signature is valid and the URL has not expired.
func VerifyFileURLSig(filePath string, tenantID uint64, expiresStr, sig string) bool {
	key := getPresignKey()
	if key == nil {
		return false
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return false
	}

	// Check expiry.
	if time.Now().Unix() > expires {
		return false
	}

	// Verify signature.
	expected := signPayload(key, filePath, tenantID, expires)
	return hmac.Equal([]byte(expected), []byte(sig))
}

// kbScopedExportsSegment is the only storage prefix served by the KB-scoped
// file proxy. Embedded wiki/chunk images land under exports/; raw knowledge
// uploads use {tenant}/{knowledgeID}/... and are served via
// /knowledge/{id}/download instead.
const kbScopedExportsSegment = "exports"

// ValidateStoragePathTenant ensures the tenant segment embedded in a provider://
// storage path matches the authenticated caller's tenant. Cross-tenant access
// for arbitrary tenant paths uses /api/v1/files/presigned with an HMAC bound to
// the resource owner; KB-scoped shared rendering uses ValidateKBScopedStoragePath.
func ValidateStoragePathTenant(filePath string, tenantID uint64) error {
	pathTenant := ParseTenantIDFromStoragePath(filePath)
	if pathTenant == 0 {
		return fmt.Errorf("storage path has no tenant segment")
	}
	if pathTenant != tenantID {
		return fmt.Errorf("storage path workspace mismatch")
	}
	return nil
}

// ValidateKBScopedStoragePath is used by GET /knowledge-bases/:id/files. It
// requires the path to belong to the KB owner tenant and to live under the
// exports/ namespace used for embedded images (SaveBytes / multimodal output).
// This prevents borrowers with shared-KB read access from using the proxy to
// fetch arbitrary owner-tenant objects such as raw knowledge uploads.
func ValidateKBScopedStoragePath(filePath string, tenantID uint64) error {
	if err := ValidateStoragePathTenant(filePath, tenantID); err != nil {
		return err
	}
	if !storagePathHasExportsScope(filePath, tenantID) {
		return fmt.Errorf("storage path is outside KB-scoped exports namespace")
	}
	return nil
}

// storageBackendScheme wraps a provider:// path with the concrete instance id:
// storage://<backendID>/<provider>://...  It is duplicated here (rather than
// reusing types.ParseStorageBackendPath) because internal/types already imports
// internal/utils, so a reverse import would create a cycle.
const storageBackendScheme = "storage://"

// unwrapStorageBackendPath strips a leading storage://<backendID>/ wrapper and
// returns the inner provider:// path. Non-wrapped paths are returned unchanged.
// This keeps tenant/exports parsing anchored on the provider path instead of
// relying on the backend id happening not to look like a tenant segment.
func unwrapStorageBackendPath(filePath string) string {
	if !strings.HasPrefix(filePath, storageBackendScheme) {
		return filePath
	}
	rest := strings.TrimPrefix(filePath, storageBackendScheme)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return filePath
	}
	return parts[1]
}

// extractStorageObjectKeyParts unwraps storage://<backendID>/, validates that
// the inner path uses a known storage provider scheme without traversal/encoding
// tricks, strips provider bucket/region header segments, and returns (scheme, keyParts, ok).
func extractStorageObjectKeyParts(filePath string) (string, []string, bool) {
	inner := unwrapStorageBackendPath(filePath)
	if strings.HasPrefix(inner, storageBackendScheme) {
		return "", nil, false
	}
	if strings.ContainsAny(inner, "\\\x00?#%") {
		return "", nil, false
	}
	scheme, rest, ok := strings.Cut(inner, "://")
	if !ok || scheme == "" || rest == "" {
		return "", nil, false
	}
	scheme = strings.ToLower(scheme)
	parts := strings.Split(rest, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", nil, false
		}
	}

	switch scheme {
	case "local":
		if len(parts) < 2 {
			return "", nil, false
		}
		return scheme, parts, true
	case "cos":
		// cos://{bucket}/{region}/{objectKey...}
		if len(parts) < 4 {
			return "", nil, false
		}
		return scheme, parts[2:], true
	case "minio", "s3", "tos", "oss", "ks3", "obs":
		// {scheme}://{bucket}/{objectKey...}
		if len(parts) < 3 {
			return "", nil, false
		}
		return scheme, parts[1:], true
	default:
		return "", nil, false
	}
}

func parsePositiveTenantSegment(seg string) uint64 {
	id, err := strconv.ParseUint(seg, 10, 64)
	if err != nil || id == 0 {
		return 0
	}
	return id
}

// storagePathHasExportsScope reports whether tenantID appears next to an
// exports segment in either canonical object-key layout:
//   - [prefix/]{tenant}/exports/...  (local, minio, s3, cos, tos, oss)
//   - exports/{tenant}/...           (temp-bucket layout)
func storagePathHasExportsScope(filePath string, tenantID uint64) bool {
	_, keyParts, ok := extractStorageObjectKeyParts(filePath)
	if !ok {
		return false
	}
	tenantSeg := strconv.FormatUint(tenantID, 10)
	for i, part := range keyParts {
		if part != tenantSeg {
			continue
		}
		if i+1 < len(keyParts) && keyParts[i+1] == kbScopedExportsSegment {
			return true
		}
		if i > 0 && keyParts[i-1] == kbScopedExportsSegment && i+1 < len(keyParts) {
			return true
		}
	}
	return false
}

// ParseTenantIDFromStoragePath extracts the tenant ID from a provider:// storage path,
// strictly anchored at the managed root or configured prefix boundary after stripping
// bucket/region header segments so numeric buckets, regions, or leaf filenames cannot
// spoof tenant ownership.
func ParseTenantIDFromStoragePath(filePath string) uint64 {
	scheme, keyParts, ok := extractStorageObjectKeyParts(filePath)
	if !ok || len(keyParts) < 2 {
		return 0
	}

	// local:// and minio:// never prepend a pathPrefix before {tenantID}.
	if scheme == "local" || scheme == "minio" {
		return parsePositiveTenantSegment(keyParts[0])
	}

	// Root layout without pathPrefix: {tenantID}/{rest...}
	if id := parsePositiveTenantSegment(keyParts[0]); id > 0 {
		return id
	}

	// Temp / exports root layout: exports/{tenantID}/{file} or temp/{tenantID}/{file}
	if keyParts[0] == kbScopedExportsSegment || keyParts[0] == "temp" {
		if len(keyParts) >= 3 {
			return parsePositiveTenantSegment(keyParts[1])
		}
		return 0
	}

	// Single-level configured pathPrefix: {prefix}/{tenantID}/{rest...}
	// or {prefix}/temp/{tenantID}/{file}
	if len(keyParts) >= 3 {
		if id := parsePositiveTenantSegment(keyParts[1]); id > 0 {
			return id
		}
		if (keyParts[1] == "temp" || keyParts[1] == kbScopedExportsSegment) && len(keyParts) >= 4 {
			return parsePositiveTenantSegment(keyParts[2])
		}
	}

	return 0
}
