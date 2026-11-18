package utils

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	imageDataURLPatternForLog = regexp.MustCompile(`data:image\/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+`)

	reDSNUserInfo = regexp.MustCompile(
		`(?i)\b((?:postgres(?:ql)?|mysql|rediss?|mongodb(?:\+srv)?|` +
			`amqps?|clickhouse|https?|s?ftp|socks5)://)([^/\s@'"<>]+)@`,
	)
	reQuerySecretParam = regexp.MustCompile(
		`(?i)([?&](?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|token|` +
			`client[_-]?secret|secret[_-]?key|app[_-]?secret|secret|password|passwd|pwd|authorization|` +
			`sig|signature|x-amz-signature|x-amz-security-token|x-goog-signature)=)[^&\s"'<>]+`,
	)
	reAuthScheme = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	reHeaderAuth = regexp.MustCompile(
		`(?i)\b(x-api-key|api-key|x-goog-api-key|authorization|proxy-authorization)` +
			`\s*:\s*(?:bearer\s+|basic\s+)?[^\s"',;]+`,
	)
	reCookieHeader = regexp.MustCompile(
		`(?i)\b((?:set-)?cookie\s*:\s*)((?:[A-Za-z0-9_.-]+=[^\s;,"']+(?:;\s*)?)+)`,
	)
	reCookiePair   = regexp.MustCompile(`([A-Za-z0-9_.-]+=)[^\s;,"']+`)
	reJSONSecretKV = regexp.MustCompile(
		`(?i)("(?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|` +
			`client[_-]?secret|secret[_-]?key|app[_-]?secret|private[_-]?key|signing[_-]?key|jwt[_-]?secret|` +
			`aes[_-]?key|webhook[_-]?secret|hmac[_-]?secret|secret|password|passwd|pwd|` +
			`db[_-]?pass(?:word)?|redis[_-]?password)"\s*:\s*)"((?:\\.|[^"\\])+)"`,
	)
	reInlineSecretKV = regexp.MustCompile(
		`(?i)\b((?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|` +
			`client[_-]?secret|secret[_-]?key|app[_-]?secret|private[_-]?key|signing[_-]?key|jwt[_-]?secret|` +
			`system[_-]?aes[_-]?key|system[_-]?signing[_-]?key|webhook[_-]?secret|hmac[_-]?secret|` +
			`password|passwd|pwd|db[_-]?pass(?:word)?|redis[_-]?password)\s*=\s*)([^\s"',;<>]+)`,
	)
	reProviderToken = regexp.MustCompile(
		`\b(?:sk-[A-Za-z0-9_-]{10,}|pk-lf-[A-Za-z0-9_-]{6,}|sk-lf-[A-Za-z0-9_-]{6,}|` +
			`lf_[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{16,}|gh[pousr]_[A-Za-z0-9_]{16,}|` +
			`glpat-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{10,})\b`,
	)
)

const (
	defaultMaxLogChars       = 12000
	defaultMaxDataURLPreview = 96
	redactedLogMask          = "[REDACTED]"
)

// IsSensitiveLogKey reports whether a structured log field key carries credentials,
// tokens, passwords, cookies, signing keys, or connection DSNs.
func IsSensitiveLogKey(rawKey string) bool {
	k := strings.ToLower(strings.TrimSpace(rawKey))
	if k == "" {
		return false
	}
	switch k {
	case "api_key", "apikey", "api-key", "x-api-key", "x-goog-api-key",
		"token", "access_token", "access-token", "refresh_token", "refresh-token",
		"id_token", "id-token", "auth_token", "auth-token", "publish_token", "bot_token", "session_token",
		"secret", "secret_key", "secret-key", "app_secret", "app-secret",
		"client_secret", "client-secret", "private_key", "private-key",
		"signing_key", "signing-key", "jwt_secret", "jwt-secret", "aes_key", "aes-key",
		"webhook_secret", "hmac_secret", "secret_access_key",
		"password", "passwd", "pwd", "pass", "db_pass", "db_password", "redis_password",
		"authorization", "proxy-authorization", "cookie", "set-cookie", "set_cookie",
		"dsn", "database_url", "redis_url", "connection_string",
		"sig", "signature", "x-amz-signature", "x-amz-security-token", "x-goog-signature":
		return true
	}
	for _, suffix := range []string{
		"_secret", "-secret", "_token", "-token",
		"_password", "-password", "_passwd", "_pass", "_pwd",
		"_api_key", "-api-key", "_apikey",
		"_signing_key", "_private_key", "_aes_key", "_dsn",
	} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// RedactSensitiveString masks API keys, bearer/basic auth tokens, cookies,
// passwords, signing keys, and URI/DSN credentials in free-form text.
func RedactSensitiveString(s string) string {
	if s == "" {
		return ""
	}
	out := reDSNUserInfo.ReplaceAllStringFunc(s, func(match string) string {
		sub := reDSNUserInfo.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		scheme, userinfo := sub[1], sub[2]
		if idx := strings.IndexByte(userinfo, ':'); idx >= 0 {
			user := userinfo[:idx]
			if user == "" {
				return scheme + ":" + redactedLogMask + "@"
			}
			return scheme + user + ":" + redactedLogMask + "@"
		}
		return scheme + redactedLogMask + "@"
	})
	out = reQuerySecretParam.ReplaceAllString(out, "${1}"+redactedLogMask)
	out = reAuthScheme.ReplaceAllString(out, "${1} "+redactedLogMask)
	out = reHeaderAuth.ReplaceAllString(out, "${1}: "+redactedLogMask)
	out = reCookieHeader.ReplaceAllStringFunc(out, func(match string) string {
		sub := reCookieHeader.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		return sub[1] + reCookiePair.ReplaceAllString(sub[2], "${1}"+redactedLogMask)
	})
	out = reJSONSecretKV.ReplaceAllStringFunc(out, func(match string) string {
		sub := reJSONSecretKV.FindStringSubmatch(match)
		if len(sub) != 3 || isAlreadyRedactedValue(sub[2]) {
			return match
		}
		return sub[1] + `"` + redactedLogMask + `"`
	})
	out = reInlineSecretKV.ReplaceAllStringFunc(out, func(match string) string {
		sub := reInlineSecretKV.FindStringSubmatch(match)
		if len(sub) != 3 || isAlreadyRedactedValue(sub[2]) {
			return match
		}
		return sub[1] + redactedLogMask
	})
	out = reProviderToken.ReplaceAllString(out, redactedLogMask)
	return out
}

func isAlreadyRedactedValue(val string) bool {
	trimmed := strings.Trim(strings.TrimSpace(val), `"'`)
	return trimmed == "" ||
		trimmed == "***" ||
		trimmed == redactedLogMask ||
		trimmed == "<unset>" ||
		trimmed == "set"
}

// CompactImageDataURLForLog shortens large image data URLs for log output.
func CompactImageDataURLForLog(raw string) string {
	masked := imageDataURLPatternForLog.ReplaceAllStringFunc(raw, func(match string) string {
		if len(match) <= defaultMaxDataURLPreview {
			return match
		}
		hidden := len(match) - defaultMaxDataURLPreview
		return match[:defaultMaxDataURLPreview] + "...<omitted " + strconv.Itoa(hidden) + " chars>"
	})

	if len(masked) <= defaultMaxLogChars {
		return masked
	}
	// Back off to a rune boundary: request bodies are often Chinese, and a
	// partial rune leaves invalid UTF-8 in the log line.
	cut := defaultMaxLogChars
	for cut > 0 && !utf8.RuneStart(masked[cut]) {
		cut--
	}
	return masked[:cut] + "... (truncated, total " + strconv.Itoa(len(masked)) + " chars)"
}
