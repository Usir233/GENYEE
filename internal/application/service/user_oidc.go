package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

type oidcDiscoveryDocument struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
	Issuer                string `json:"issuer"`
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func newOIDCHTTPClient() *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 30 * time.Second
	return secutils.NewSSRFSafeHTTPClient(cfg)
}

func validateOIDCEndpoint(label, endpoint string, required bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		if required {
			return fmt.Errorf("OIDC %s endpoint is required", label)
		}
		return nil
	}
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return fmt.Errorf("OIDC %s endpoint failed SSRF validation: %w", label, err)
	}
	return nil
}

func validateOIDCEndpoints(cfg *config.OIDCAuthConfig) error {
	if err := validateOIDCEndpoint("authorization", cfg.AuthorizationEndpoint, true); err != nil {
		return err
	}
	if err := validateOIDCEndpoint("token", cfg.TokenEndpoint, true); err != nil {
		return err
	}
	if err := validateOIDCEndpoint("userinfo", cfg.UserInfoEndpoint, false); err != nil {
		return err
	}
	if err := validateOIDCEndpoint("jwks", cfg.JwksURI, false); err != nil {
		return err
	}
	return nil
}

func (s *userService) getOIDCConfig(ctx context.Context) (*config.OIDCAuthConfig, error) {
	if s.config == nil || s.config.OIDCAuth == nil || !s.config.OIDCAuth.Enable {
		return nil, errors.New("OIDC login is disabled")
	}
	cfg := *s.config.OIDCAuth
	if cfg.UserInfoMapping == nil {
		cfg.UserInfoMapping = &config.OIDCUserInfoMapping{Username: "name", Email: "email"}
	}
	if err := s.populateOIDCEndpoints(ctx, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func oidcNeedsDiscovery(cfg *config.OIDCAuthConfig) bool {
	return strings.TrimSpace(cfg.AuthorizationEndpoint) == "" ||
		strings.TrimSpace(cfg.TokenEndpoint) == "" ||
		strings.TrimSpace(cfg.JwksURI) == "" ||
		strings.TrimSpace(cfg.IssuerURL) == ""
}

func (s *userService) populateOIDCEndpoints(ctx context.Context, cfg *config.OIDCAuthConfig) error {
	if oidcNeedsDiscovery(cfg) {
		if strings.TrimSpace(cfg.DiscoveryURL) == "" {
			if strings.TrimSpace(cfg.AuthorizationEndpoint) == "" || strings.TrimSpace(cfg.TokenEndpoint) == "" {
				return errors.New("OIDC discovery_url or explicit endpoints are required")
			}
		} else if err := s.applyOIDCDiscoveryDocument(ctx, cfg); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.AuthorizationEndpoint) == "" || strings.TrimSpace(cfg.TokenEndpoint) == "" {
		return errors.New("OIDC discovery document missing required endpoints")
	}
	return validateOIDCEndpoints(cfg)
}

func (s *userService) applyOIDCDiscoveryDocument(ctx context.Context, cfg *config.OIDCAuthConfig) error {
	if err := validateOIDCEndpoint("discovery", cfg.DiscoveryURL, true); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.DiscoveryURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create OIDC discovery request: %w", err)
	}

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to load OIDC discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf(
			"OIDC discovery request failed: status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(string(body)),
		)
	}

	var doc oidcDiscoveryDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("failed to decode OIDC discovery document: %w", err)
	}
	if strings.TrimSpace(cfg.AuthorizationEndpoint) == "" {
		cfg.AuthorizationEndpoint = doc.AuthorizationEndpoint
	}
	if strings.TrimSpace(cfg.TokenEndpoint) == "" {
		cfg.TokenEndpoint = doc.TokenEndpoint
	}
	if strings.TrimSpace(cfg.UserInfoEndpoint) == "" {
		cfg.UserInfoEndpoint = doc.UserInfoEndpoint
	}
	if strings.TrimSpace(cfg.JwksURI) == "" {
		cfg.JwksURI = doc.JwksURI
	}
	if strings.TrimSpace(cfg.IssuerURL) == "" {
		cfg.IssuerURL = doc.Issuer
	}
	return nil
}

func (s *userService) exchangeOIDCCode(
	ctx context.Context, cfg *config.OIDCAuthConfig, code, redirectURI, codeVerifier string,
) (*oidcTokenResponse, error) {
	if err := validateOIDCEndpoint("token", cfg.TokenEndpoint, true); err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	if cfg.PKCEEnabled {
		if codeVerifier == "" {
			return nil, errors.New("OIDC PKCE verifier is required")
		}
		form.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange OIDC code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("OIDC token exchange failed: status=%d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read OIDC token response: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, errors.New("OIDC token response exceeds 1 MiB")
	}
	var tokenResp oidcTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC token response: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" && strings.TrimSpace(tokenResp.IDToken) == "" {
		return nil, errors.New("OIDC token response missing access_token and id_token")
	}
	return &tokenResp, nil
}

func isOIDCEmailVerified(claims map[string]interface{}) bool {
	if len(claims) == 0 {
		return false
	}
	v, ok := claims["email_verified"].(bool)
	return ok && v
}

func (s *userService) resolveRegistrationMode(ctx context.Context) string {
	def := config.AuthRegistrationModeSelfServe
	if s.config != nil && s.config.Auth != nil {
		if m := strings.TrimSpace(s.config.Auth.RegistrationMode); m != "" {
			def = m
		}
	}
	if s.systemSettingSvc == nil {
		return def
	}
	return s.systemSettingSvc.GetString(ctx, "auth.registration_mode", "", def)
}

func (s *userService) resolveOIDCUserInfo(
	ctx context.Context, cfg *config.OIDCAuthConfig, tokenResp *oidcTokenResponse,
) (*types.OIDCUserInfo, error) {
	claims := map[string]interface{}{}
	verifiedFromIDToken := false
	var idTokenSub, idTokenEmail string

	emailClaimKey := "email"
	if cfg.UserInfoMapping != nil && strings.TrimSpace(cfg.UserInfoMapping.Email) != "" {
		emailClaimKey = strings.TrimSpace(cfg.UserInfoMapping.Email)
	}

	if idToken := strings.TrimSpace(tokenResp.IDToken); idToken != "" {
		if strings.TrimSpace(cfg.JwksURI) == "" {
			if strings.TrimSpace(cfg.UserInfoEndpoint) == "" || strings.TrimSpace(tokenResp.AccessToken) == "" {
				return nil, errors.New("cannot verify OIDC id_token: no jwks_uri configured")
			}
			logger.Warnf(ctx, "OIDC id_token ignored: no jwks_uri configured; relying on userinfo endpoint")
		} else {
			idTokenClaims, err := s.verifyOIDCIDToken(ctx, cfg, idToken)
			if err != nil {
				return nil, fmt.Errorf("OIDC id_token verification failed: %w", err)
			}
			for k, v := range idTokenClaims {
				claims[k] = v
			}
			idTokenSub = strings.TrimSpace(extractClaimAsString(idTokenClaims, "sub"))
			idTokenEmail = strings.TrimSpace(extractClaimAsString(idTokenClaims, emailClaimKey))
			verifiedFromIDToken = true
		}
	}

	if strings.TrimSpace(cfg.UserInfoEndpoint) != "" && strings.TrimSpace(tokenResp.AccessToken) != "" {
		userInfoClaims, err := s.fetchOIDCUserInfo(ctx, cfg.UserInfoEndpoint, tokenResp.AccessToken)
		if err != nil {
			if !verifiedFromIDToken {
				return nil, fmt.Errorf("failed to fetch OIDC userinfo: %w", err)
			}
			logger.Warnf(ctx, "Failed to fetch OIDC userinfo, using verified id_token claims: %v", err)
		} else {
			if verifiedFromIDToken {
				uiSub := strings.TrimSpace(extractClaimAsString(userInfoClaims, "sub"))
				if uiSub == "" || uiSub != idTokenSub {
					return nil, errors.New("OIDC userinfo sub claim does not match verified id_token sub")
				}
				uiEmail := strings.TrimSpace(extractClaimAsString(userInfoClaims, emailClaimKey))
				if uiEmail != "" && !strings.EqualFold(uiEmail, idTokenEmail) {
					delete(claims, "email_verified")
				}
			}
			for k, v := range userInfoClaims {
				if verifiedFromIDToken && (k == "iss" || k == "aud" || k == "sub") {
					continue
				}
				claims[k] = v
			}
		}
	}

	if len(claims) == 0 {
		return nil, errors.New("OIDC provider returned no user claims")
	}

	info := &types.OIDCUserInfo{Claims: claims}
	info.Subject = extractClaimAsString(claims, "sub")
	if info.Subject == "" {
		return nil, errors.New("OIDC provider did not return sub claim")
	}
	info.Username = extractClaimAsString(claims, cfg.UserInfoMapping.Username)
	info.Email = extractClaimAsString(claims, cfg.UserInfoMapping.Email)
	if info.Username == "" {
		info.Username = extractClaimAsString(claims, "preferred_username")
	}
	if info.Username == "" {
		info.Username = extractClaimAsString(claims, "name")
	}
	if info.Username == "" && info.Email != "" {
		info.Username = strings.Split(info.Email, "@")[0]
	}
	return info, nil
}

func (s *userService) fetchOIDCUserInfo(
	ctx context.Context, endpoint, accessToken string,
) (map[string]interface{}, error) {
	if err := validateOIDCEndpoint("userinfo", endpoint, true); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("userinfo request failed: status=%d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, errors.New("OIDC userinfo response exceeds 1 MiB")
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// provisionOIDCUser auto-creates a local account for a first-time OIDC
// login. The provisioning mode is decided by the caller (the OIDC callback
// handler resolves it from the same auth.default_tenant_mode system-setting
// that governs public password registration) so both entry points share a
// single deployment policy. An empty mode falls back to create_personal via
// Register's own defaulting.
func (s *userService) provisionOIDCUser(
	ctx context.Context,
	info *types.OIDCUserInfo,
	provisioning types.TenantProvisioningMode,
) (*types.User, error) {
	username := s.generateOIDCUsername(ctx, info)
	randomPassword, err := generateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate password for OIDC user: %w", err)
	}

	user, err := s.Register(ctx, &types.RegisterRequest{
		Username:           username,
		Email:              info.Email,
		Password:           randomPassword,
		TenantProvisioning: provisioning,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to auto-provision OIDC user: %w", err)
	}

	oidcOnly := true
	user.Preferences.OidcOnlyLogin = &oidcOnly
	user.UpdatedAt = time.Now()
	if err := s.userRepo.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to mark OIDC-only login preference: %w", err)
	}
	return user, nil
}

func (s *userService) generateOIDCUsername(ctx context.Context, info *types.OIDCUserInfo) string {
	base := sanitizeUsernameCandidate(info.Username)
	if base == "" {
		base = sanitizeUsernameCandidate(strings.Split(info.Email, "@")[0])
	}
	if base == "" {
		base = "oidc-user"
	}

	candidate := base
	for i := 0; i < 20; i++ {
		existing, err := s.userRepo.GetUserByUsername(ctx, candidate)
		if isUserLookupNotFound(err) || (err == nil && existing == nil) {
			return candidate
		}
		if err != nil && !isUserLookupNotFound(err) {
			logger.Warnf(ctx, "Failed to check existing OIDC username %q: %v", candidate, err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i+1)
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}

func generateRandomString(length int) (string, error) {
	buffer := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func getRandomChar(charset string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
	if err != nil {
		return 0, err
	}
	return charset[n.Int64()], nil
}

func generateComplexPassword(length int) (string, error) {
	if length < 4 {
		return "", fmt.Errorf("password length must be at least 4")
	}

	password := make([]byte, 0, length)

	c, err := getRandomChar(upperChars)
	if err != nil {
		return "", err
	}
	password = append(password, c)

	c, err = getRandomChar(lowerChars)
	if err != nil {
		return "", err
	}
	password = append(password, c)

	c, err = getRandomChar(digitChars)
	if err != nil {
		return "", err
	}
	password = append(password, c)

	c, err = getRandomChar(passwordSpecialChars)
	if err != nil {
		return "", err
	}
	password = append(password, c)

	for len(password) < length {
		c, err = getRandomChar(allChars)
		if err != nil {
			return "", err
		}
		password = append(password, c)
	}

	// Fisher-Yates shuffle
	for i := len(password) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}

		j := int(n.Int64())
		password[i], password[j] = password[j], password[i]
	}

	return string(password), nil
}

// generatePolicyCompliantPassword returns a cryptographically random
// password that satisfies ValidatePasswordPolicy.
//
// Simple policies may require regeneration (a single 32-char base64url
// draw misses digits ~0.4% of the time). Complex policies are satisfied
// in a single pass.
func generatePolicyCompliantPassword(complexPasswordEnabled bool) (string, error) {
	var password string
	var err error
	for {
		if complexPasswordEnabled {
			password, err = generateComplexPassword(16)
		} else {
			password, err = generateRandomString(24)
		}

		if err != nil {
			return "", err
		}

		if ValidatePasswordPolicy(password, complexPasswordEnabled) == nil {
			return password, nil
		}
	}
}

// oidcJWK / oidcJWKS model the subset of a JWKS document we need to rebuild an
// RSA signing key. Only RSA keys are supported (the overwhelmingly common OIDC
// id_token signing type); other key types are skipped during lookup.
type oidcJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type oidcJWKS struct {
	Keys []oidcJWK `json:"keys"`
}

// decodeJWKBase64 decodes a base64url-encoded JWK field (RFC 7518 §6.3.1).
func decodeJWKBase64(value string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(value); err == nil && len(b) > 0 {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func (k oidcJWK) rsaPublicKey() (*rsa.PublicKey, error) {
	if !strings.EqualFold(k.Kty, "RSA") {
		return nil, fmt.Errorf("unsupported JWK key type: %s", k.Kty)
	}
	nBytes, err := decodeJWKBase64(k.N)
	if err != nil {
		return nil, fmt.Errorf("invalid JWK modulus: %w", err)
	}
	eBytes, err := decodeJWKBase64(k.E)
	if err != nil {
		return nil, fmt.Errorf("invalid JWK exponent: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, errors.New("empty JWK modulus or exponent")
	}
	eInt := new(big.Int).SetBytes(eBytes)
	if !eInt.IsInt64() {
		return nil, errors.New("invalid JWK exponent value")
	}
	e := int(eInt.Int64())
	if e <= 0 {
		return nil, errors.New("invalid JWK exponent value")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func (jwks *oidcJWKS) rsaKeyForKid(kid string) (*rsa.PublicKey, error) {
	var usable []oidcJWK
	for _, k := range jwks.Keys {
		if k.Use != "" && !strings.EqualFold(k.Use, "sig") {
			continue
		}
		if k.Kty != "" && !strings.EqualFold(k.Kty, "RSA") {
			continue
		}
		if kid != "" && k.Kid != kid {
			continue
		}
		if _, err := k.rsaPublicKey(); err != nil {
			continue
		}
		usable = append(usable, k)
	}
	if kid != "" {
		if len(usable) == 0 {
			return nil, fmt.Errorf("no matching JWKS RSA key for kid %q", kid)
		}
		return usable[0].rsaPublicKey()
	}
	if len(usable) == 0 {
		return nil, errors.New("no matching JWKS key for id_token")
	}
	if len(usable) > 1 {
		return nil, errors.New("id_token missing kid and JWKS contains multiple RSA signing keys")
	}
	return usable[0].rsaPublicKey()
}

// fetchOIDCJWKS loads the provider's JWKS document over the SSRF-safe client.
func (s *userService) fetchOIDCJWKS(ctx context.Context, jwksURI string) (*oidcJWKS, error) {
	if err := validateOIDCEndpoint("jwks", jwksURI, true); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("JWKS request failed: status=%d", resp.StatusCode)
	}

	var jwks oidcJWKS
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to decode JWKS document: %w", err)
	}
	if len(jwks.Keys) == 0 {
		return nil, errors.New("JWKS document contains no keys")
	}
	return &jwks, nil
}

const oidcIDTokenLeeway = 2 * time.Minute

// verifyOIDCIDToken cryptographically verifies an OIDC id_token: it checks the
// RSA signature against the provider's JWKS (matched by kid) and validates the
// issuer, audience (client_id), expiry and subject. It returns the verified claims.
func (s *userService) verifyOIDCIDToken(
	ctx context.Context, cfg *config.OIDCAuthConfig, idToken string,
) (map[string]interface{}, error) {
	if strings.TrimSpace(cfg.JwksURI) == "" {
		return nil, errors.New("cannot verify OIDC id_token: no jwks_uri configured")
	}
	if strings.TrimSpace(cfg.IssuerURL) == "" {
		return nil, errors.New("cannot verify OIDC id_token: issuer is not configured")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, errors.New("cannot verify OIDC id_token: client_id is not configured")
	}

	jwks, err := s.fetchOIDCJWKS(ctx, cfg.JwksURI)
	if err != nil {
		return nil, err
	}

	keyFunc := func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected id_token signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		return jwks.rsaKeyForKid(kid)
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(oidcIDTokenLeeway),
		jwt.WithIssuer(strings.TrimSpace(cfg.IssuerURL)),
		jwt.WithAudience(strings.TrimSpace(cfg.ClientID)),
	).ParseWithClaims(idToken, claims, keyFunc); err != nil {
		return nil, fmt.Errorf("id_token verification failed: %w", err)
	}
	verified := map[string]interface{}(claims)
	if strings.TrimSpace(extractClaimAsString(verified, "sub")) == "" {
		return nil, errors.New("id_token missing sub claim")
	}
	return verified, nil
}

func extractClaimAsString(claims map[string]interface{}, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	value, ok := claims[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func sanitizeUsernameCandidate(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-._")
	if len(result) > 50 {
		result = strings.Trim(result[:50], "-._")
	}
	return result
}

func isUserLookupNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, apprepo.ErrUserNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "user not found")
}
