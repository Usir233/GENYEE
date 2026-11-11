package utils

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const oidcStateMaxAge = 10 * time.Minute

// OIDCStatePayload is the signed OIDC authorization state carried in the
// redirect URL and validated on callback.
type OIDCStatePayload struct {
	Nonce       string `json:"nonce"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	IssuedAt    int64  `json:"iat"`
	PKCEEnabled bool   `json:"pkce,omitempty"`
}

var (
	oidcStateSecretOnce sync.Once
	oidcStateSecret     string
	oidcStateSecretErr  error
)

func loadOIDCStateSigningKey() (string, error) {
	envSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	isPlaceholder := envSecret == "" || envSecret == "weknora-jwt-secret" || envSecret == "CHANGE-ME-jwt-secret"
	if IsProductionEnv() {
		if err := ValidateProductionJWTSecret(envSecret); err != nil {
			return "", err
		}
		return envSecret, nil
	}
	if !isPlaceholder {
		return envSecret, nil
	}
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate OIDC state signing key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(randomBytes), nil
}

func oidcStateSigningKey() (string, error) {
	oidcStateSecretOnce.Do(func() {
		oidcStateSecret, oidcStateSecretErr = loadOIDCStateSigningKey()
	})
	return oidcStateSecret, oidcStateSecretErr
}

// SignOIDCState returns a tamper-evident state token: base64url(payload).base64url(hmac).
func SignOIDCState(payload *OIDCStatePayload) (string, error) {
	if payload == nil {
		return "", errors.New("oidc state payload is required")
	}
	if strings.TrimSpace(payload.Nonce) == "" {
		return "", errors.New("oidc state nonce is required")
	}
	if strings.TrimSpace(payload.RedirectURI) == "" {
		return "", errors.New("oidc state redirect_uri is required")
	}
	if payload.IssuedAt == 0 {
		payload.IssuedAt = time.Now().Unix()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal oidc state: %w", err)
	}
	signingKey, err := oidcStateSigningKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write(raw)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyOIDCState validates the HMAC and freshness of a state token.
func VerifyOIDCState(raw string) (*OIDCStatePayload, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid oidc state format")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode oidc state payload: %w", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode oidc state signature: %w", err)
	}
	signingKey, err := oidcStateSigningKey()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write(payloadBytes)
	if !hmac.Equal(mac.Sum(nil), sigBytes) {
		return nil, errors.New("oidc state signature mismatch")
	}
	var payload OIDCStatePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal oidc state: %w", err)
	}
	if strings.TrimSpace(payload.RedirectURI) == "" {
		return nil, errors.New("state.redirect_uri is required")
	}
	if payload.IssuedAt == 0 {
		return nil, errors.New("state.iat is required")
	}
	issuedAt := time.Unix(payload.IssuedAt, 0)
	if time.Since(issuedAt) > oidcStateMaxAge || time.Until(issuedAt) > time.Minute {
		return nil, errors.New("oidc state expired or invalid timestamp")
	}
	return &payload, nil
}
