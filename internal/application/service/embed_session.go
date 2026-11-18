package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
	"github.com/redis/go-redis/v9"
)

const (
	embedSessionTokenPrefix = "ems_"
	embedSessionRedisPrefix = "embed:session:"
	embedSessionTTL         = 30 * time.Minute
)

// ErrEmbedSessionUnavailable indicates that Redis-backed embed session tokens are unavailable.
var ErrEmbedSessionUnavailable = errors.New("embed session tokens unavailable")

func generateEmbedSessionToken() (string, error) {
	buf := make([]byte, embedTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return embedSessionTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func embedPublishTokenDigest(publishToken string) string {
	trimmed := strings.TrimSpace(publishToken)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("embed-publish-v1|" + trimmed))
	return hex.EncodeToString(sum[:16])
}

// IssueSessionToken mints a short-lived session token bound to channelID and
// the current PublishToken digest so RotateToken invalidates old ems_ tokens.
func (s *embedChannelService) IssueSessionToken(ctx context.Context, channelID string) (string, int, error) {
	if s.redis == nil {
		return "", 0, ErrEmbedSessionUnavailable
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return "", 0, ErrEmbedTokenInvalid
	}
	value := channelID
	if s.repo != nil {
		ch, err := s.repo.GetByID(ctx, channelID)
		if err != nil {
			return "", 0, err
		}
		if ch == nil {
			return "", 0, ErrEmbedTokenInvalid
		}
		if !ch.Enabled {
			return "", 0, ErrEmbedChannelDisabled
		}
		digest := embedPublishTokenDigest(ch.PublishToken)
		if digest == "" {
			return "", 0, ErrEmbedTokenInvalid
		}
		value = channelID + "|" + digest
	}
	token, err := generateEmbedSessionToken()
	if err != nil {
		return "", 0, err
	}
	key := embedSessionRedisPrefix + token
	if err := s.redis.Set(ctx, key, value, embedSessionTTL).Err(); err != nil {
		return "", 0, err
	}
	return token, int(embedSessionTTL.Seconds()), nil
}

// ResolveSessionToken returns the channel ID stored for a session token,
// verifying that the channel is still enabled and its PublishToken has not
// been rotated since the ems_ token was minted.
func (s *embedChannelService) ResolveSessionToken(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, embedSessionTokenPrefix) {
		return "", ErrEmbedTokenInvalid
	}
	if s.redis == nil {
		return "", ErrEmbedSessionUnavailable
	}
	redisKey := embedSessionRedisPrefix + token
	stored, err := s.redis.Get(ctx, redisKey).Result()
	if err == redis.Nil {
		return "", ErrEmbedTokenInvalid
	}
	if err != nil {
		return "", err
	}
	channelID, digest, hasDigest := strings.Cut(strings.TrimSpace(stored), "|")
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return "", ErrEmbedTokenInvalid
	}
	if s.repo != nil {
		ch, err := s.repo.GetByID(ctx, channelID)
		if err != nil {
			return "", err
		}
		if ch == nil {
			if delErr := s.redis.Del(ctx, redisKey).Err(); delErr != nil {
				logger.Warnf(ctx, "Failed to delete stale embed session token: %v", delErr)
			}
			return "", ErrEmbedTokenInvalid
		}
		if !ch.Enabled {
			return "", ErrEmbedChannelDisabled
		}
		expectedDigest := embedPublishTokenDigest(ch.PublishToken)
		if !hasDigest || expectedDigest == "" ||
			subtle.ConstantTimeCompare([]byte(expectedDigest), []byte(strings.TrimSpace(digest))) != 1 {
			if delErr := s.redis.Del(ctx, redisKey).Err(); delErr != nil {
				logger.Warnf(ctx, "Failed to delete rotated embed session token: %v", delErr)
			}
			return "", ErrEmbedTokenInvalid
		}
	}
	return channelID, nil
}

// LookupEnabledChannel loads an embed channel and verifies it is enabled.
func (s *embedChannelService) LookupEnabledChannel(ctx context.Context, channelID string) (*types.EmbedChannel, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, ErrEmbedTokenInvalid
	}
	ch, err := s.repo.GetByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, ErrEmbedTokenInvalid
	}
	if !ch.Enabled {
		return nil, ErrEmbedChannelDisabled
	}
	decryptEmbedChannelInPlace(ch)
	return ch, nil
}

// IsEmbedSessionToken reports whether token is a session token (ems_ prefix).
func IsEmbedSessionToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), embedSessionTokenPrefix)
}

// SignEmbedSessionHandle binds a chat session id to its embed channel with an
// HMAC keyed by a server-only deployment secret. The handle is handed
// to the widget at session-creation time and must be presented on every history
// load / chat call. Because the session id travels in the request path (and can
// land in access logs), this signature — sent in a header, never logged — is the
// real authorization secret: a leaked session id is useless without it. Rotating
// the channel token invalidates outstanding handles, which is acceptable.
func SignEmbedSessionHandle(ch *types.EmbedChannel, sessionID string) string {
	if ch == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	key := utils.SystemHMACKey()
	if len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("embed-session:v2|" + ch.PublishToken + "|" + ch.ID + "|" + sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyEmbedSessionHandle reports whether sig is a valid handle for sessionID
// on channel ch, using a constant-time comparison.
func VerifyEmbedSessionHandle(ch *types.EmbedChannel, sessionID, sig string) bool {
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return false
	}
	expected := SignEmbedSessionHandle(ch, sessionID)
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1
}

// IssuePreviewSession mints a short-lived session token for management UI preview.
func (s *embedChannelService) IssuePreviewSession(
	ctx context.Context, tenantID uint64, channelID string,
) (string, int, error) {
	ch, err := s.getOwned(ctx, tenantID, channelID)
	if err != nil {
		return "", 0, err
	}
	if !ch.Enabled {
		return "", 0, ErrEmbedChannelDisabled
	}
	return s.IssueSessionToken(ctx, ch.ID)
}
