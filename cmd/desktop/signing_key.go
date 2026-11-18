package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Usir233/GENYEE/internal/utils"
)

const maxDesktopKeyFileBytes = 4096

func readOrCreateSecretFile(path string) ([]byte, error) {
	key, err := readBoundedSecretFile(path)
	if os.IsNotExist(err) {
		raw := make([]byte, 32)
		if _, randErr := rand.Read(raw); randErr != nil {
			return nil, randErr
		}
		key = []byte(base64.RawURLEncoding.EncodeToString(raw))
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		switch {
		case os.IsExist(createErr):
			key, err = readBoundedSecretFile(path)
		case createErr != nil:
			return nil, createErr
		default:
			_, writeErr := f.Write(key)
			closeErr := f.Close()
			if writeErr != nil {
				_ = os.Remove(path)
				return nil, writeErr
			}
			if closeErr != nil {
				_ = os.Remove(path)
				return nil, closeErr
			}
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	return key, nil
}

func readBoundedSecretFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxDesktopKeyFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > maxDesktopKeyFileBytes {
		return nil, errors.New("desktop secret file exceeds maximum length")
	}
	return data, nil
}

// ensureDesktopSigningKey persists the desktop signing key and, when no
// explicit secret is configured, a JWT secret. It never replaces an AES
// encryption key, so upgrading cannot make stored credentials unreadable.
// The JWT secret is never that AES key: a leaked session secret must not
// decrypt stored credentials.
func ensureDesktopSigningKey() error {
	dir, err := desktopConfigDir()
	if err != nil {
		return err
	}
	return ensureDesktopSigningKeyInDir(resolveDesktopAppSupportDir(dir))
}

func ensureDesktopSigningKeyInDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// An existing HMAC (an explicit signing key, or the AES key of an older
	// install) stays the signer exactly as configured: replacing it would
	// invalidate presigned URLs, and copying it into signing.key would leave
	// the AES key in a second file. Sessions then get their own secret.
	if len(utils.SystemHMACKey()) != 0 {
		return ensureDesktopJWTSecret(dir, nil)
	}
	path := filepath.Join(dir, "signing.key")
	key, err := readOrCreateSecretFile(path)
	if err != nil {
		return err
	}
	if len(key) < 32 {
		return fmt.Errorf("invalid desktop signing key in %s", path)
	}
	if err := os.Setenv("SYSTEM_SIGNING_KEY", string(key)); err != nil {
		return err
	}
	return ensureDesktopJWTSecret(dir, key)
}

// ensureDesktopJWTSecret fills JWT_SECRET when it is unset or still a
// placeholder. A signing key this function generated can also sign sessions.
// A nil signingKey means the signer came from the environment (possibly the
// AES key), so sessions get their own persisted secret.
func ensureDesktopJWTSecret(dir string, signingKey []byte) error {
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret != "" && jwtSecret != "genyee-jwt-secret" && jwtSecret != "CHANGE-ME-jwt-secret" {
		return nil
	}
	secret := string(signingKey)
	if signingKey == nil {
		var err error
		secret, err = desktopJWTSecret(dir)
		if err != nil {
			return err
		}
	}
	return os.Setenv("JWT_SECRET", secret)
}

func desktopJWTSecret(dir string) (string, error) {
	path := filepath.Join(dir, "jwt.key")
	key, err := readOrCreateSecretFile(path)
	if err != nil {
		return "", err
	}
	if len(key) < 32 {
		return "", fmt.Errorf("invalid desktop jwt secret in %s", path)
	}
	return string(key), nil
}
