package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

// EncPrefix marks a string as AES-256-GCM encrypted
const EncPrefix = "enc:v1:"

func isPlaceholderAESKey(raw string) bool {
	return raw == "genyee-system-aes-key-32bytes!!" || raw == "your-32-byte-long-encryption-key!"
}

// IsReleaseMode reports whether GIN_MODE is explicitly set to release.
func IsReleaseMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("GIN_MODE")), "release")
}

// IsProductionEnv reports whether GIN_MODE, APP_ENV, or ENV indicates production/release.
func IsProductionEnv() bool {
	for _, envKey := range []string{"GIN_MODE", "APP_ENV", "ENV"} {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(envKey)))
		if v == "release" || v == "production" || v == "prod" {
			return true
		}
	}
	return false
}

// GetAESKey reads the 32-byte AES key from SYSTEM_AES_KEY env.
// Returns nil if not set, not exactly 32 bytes, or set to a known example placeholder.
func GetAESKey() []byte {
	raw := os.Getenv("SYSTEM_AES_KEY")
	if len(raw) == 32 && !isPlaceholderAESKey(raw) {
		return []byte(raw)
	}
	return nil
}

// ValidateProductionAESKey verifies that SYSTEM_AES_KEY is explicitly configured
// to a valid 32-byte non-placeholder secret for release deployments.
func ValidateProductionAESKey() error {
	raw := os.Getenv("SYSTEM_AES_KEY")
	if len(raw) != 32 || isPlaceholderAESKey(raw) {
		return errors.New("SYSTEM_AES_KEY must be explicitly configured as a 32-byte non-placeholder secret in release mode")
	}
	return nil
}

// ValidateProductionJWTSecret verifies that JWT_SECRET is explicitly configured
// with at least 32 bytes and is not a known default placeholder.
func ValidateProductionJWTSecret(secret string) error {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" || trimmed == "genyee-jwt-secret" || trimmed == "CHANGE-ME-jwt-secret" {
		return errors.New("JWT_SECRET must be explicitly configured in production environment and cannot be empty or default placeholder")
	}
	if len(trimmed) < 32 {
		return errors.New("JWT_SECRET must be at least 32 bytes in production environment")
	}
	return nil
}

// EncryptAESGCM encrypts plaintext with AES-256-GCM.
// Returns the original string if empty, already encrypted, or key is nil.
func EncryptAESGCM(plaintext string, key []byte) (string, error) {
	if plaintext == "" || key == nil {
		return plaintext, nil
	}
	if strings.HasPrefix(plaintext, EncPrefix) {
		return plaintext, nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesgcm.Seal(nil, nonce, []byte(plaintext), nil)
	combined := append(nonce, ciphertext...)
	return EncPrefix + base64.RawURLEncoding.EncodeToString(combined), nil
}

// ErrEncryptedDataMissingKey is returned by DecryptStoredSecret when the value
// carries the enc:v1: prefix but no AES key is available to decrypt it. This
// signals an operator misconfiguration (typically a rotated or unset
// SYSTEM_AES_KEY) and must propagate so the system fails loudly instead of
// silently using ciphertext as a credential.
var ErrEncryptedDataMissingKey = errors.New("encrypted data found but SYSTEM_AES_KEY is not set or has wrong length")

// DecryptAESGCM decrypts an AES-256-GCM encrypted string.
// If the string lacks the enc:v1: prefix, it's treated as legacy plaintext and returned as-is.
func DecryptAESGCM(encrypted string, key []byte) (string, error) {
	if encrypted == "" || key == nil {
		return encrypted, nil
	}
	if !strings.HasPrefix(encrypted, EncPrefix) {
		return encrypted, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, EncPrefix))
	if err != nil {
		return "", err
	}
	if len(data) < 12 {
		return "", errors.New("invalid encrypted data: too short")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce, ciphertext := data[:aesgcm.NonceSize()], data[aesgcm.NonceSize():]
	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// DecryptStoredSecret decrypts a value loaded from the database with strict
// error propagation. Use this in hot-path code that is about to actually
// USE the credential (e.g. building an upstream HTTP request) — a rotated
// or missing key must surface as a loud error rather than silently
// degrading to "no credential", because the user expects the configured
// upstream call to either work or fail with a clear "auth misconfigured"
// message.
//
// Behaviour:
//   - empty input -> empty output, no error.
//   - no enc:v1: prefix -> returned as-is (legacy plaintext column), no error.
//   - has enc:v1: prefix and SYSTEM_AES_KEY is missing or wrong length ->
//     returns ErrEncryptedDataMissingKey.
//   - has enc:v1: prefix and key is set -> decrypts, returns any decryption
//     error verbatim (e.g. base64 decode failure, GCM auth tag mismatch from a
//     rotated key).
//
// For GORM Scan paths that load a whole row (where erroring out hides the
// rest of the resource from the user) use DecryptStoredSecretLenient
// instead — it lets the row load with the secret blanked out so the UI
// can render "credential not configured" and the user can re-enter it,
// without crashing list endpoints when the operator rotates or removes
// SYSTEM_AES_KEY.
func DecryptStoredSecret(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}
	if !strings.HasPrefix(encrypted, EncPrefix) {
		return encrypted, nil
	}
	key := GetAESKey()
	if key == nil {
		return "", ErrEncryptedDataMissingKey
	}
	return DecryptAESGCM(encrypted, key)
}

// DecryptStoredSecretLenient is the load-path counterpart for use inside
// GORM Scan / AfterFind hooks. It prefers graceful degradation over
// failing the whole row load.
//
// Returns:
//   - (plaintext, true)  for empty input, legacy plaintext, or successful decrypt
//   - ("", false)        when the value has the enc:v1: prefix but cannot be
//     decrypted (SYSTEM_AES_KEY missing, rotated, or ciphertext corrupted).
//     The bool=false case is the operator's signal: log a warning and treat
//     the field as unconfigured. The row still loads so the rest of the
//     resource displays normally; the UI shows "credential not configured",
//     and any upstream call attempted with this empty value fails with a
//     clear "missing api key" rather than sending ciphertext as the key.
//
// Rationale: a single line of broken ciphertext used to crash entire list
// endpoints (e.g. ListModels returning "" because one row failed Scan),
// hiding all other models from the user. Lenient load + loud per-row log
// is strictly more recoverable than fail-fast Scan.
func DecryptStoredSecretLenient(encrypted string) (plaintext string, ok bool) {
	plain, err := DecryptStoredSecret(encrypted)
	if err != nil {
		return "", false
	}
	return plain, true
}
