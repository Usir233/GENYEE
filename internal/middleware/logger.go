package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	maxBodySize = 1024 * 10 // 最大记录10KB的body内容
	// Truncated JSON can cut through a credential string or container, making
	// redaction unsafe. Log only this marker when the body exceeds the budget.
	bodyTruncatedMarker = "[内容过长，已省略]"
)

// replayedBody 把日志窥探消费掉的字节还给 handler，让它读到的 body 与没有这层
// 中间件时完全一致；Close 仍然关闭原始 body，连接得以释放。
type replayedBody struct {
	io.Reader
	io.Closer
}

// loggerResponseBodyWriter 自定义ResponseWriter用于捕获响应内容（用于logger中间件）
type loggerResponseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// Write 重写Write方法，同时写入buffer和原始writer
// 限制buffer大小，避免SSE等流式响应导致内存无限增长
func (r loggerResponseBodyWriter) Write(b []byte) (int, error) {
	if r.body.Len() < maxBodySize {
		remaining := maxBodySize - r.body.Len()
		if len(b) <= remaining {
			r.body.Write(b)
		} else {
			r.body.Write(b[:remaining])
		}
	}
	return r.ResponseWriter.Write(b)
}

// sensitiveFieldRegex is the fallback redactor for truncated or non-JSON text bodies.
// Complete JSON payloads are redacted by redactJSONBytes first so nested maps
// (values/envs/env_vars/credentials/headers/custom_headers), escaped quotes, and non-string secrets
// are recursively masked while preserving key order.
var sensitiveFieldRegex = regexp.MustCompile(
	`(?i)("(?P<k>value|values|envs|env[_-]?vars|credentials|headers|custom[_-]?headers|` +
		`passwd|ticket|assertion|cookie|set[_-]?cookie|proxy[_-]?authorization|` +
		`authorization|authorization[_-]?url|authorization[_-]?attempt|pairing[_-]?link|` +
		`[a-z0-9_-]*secret[a-z0-9_-]*|[a-z0-9_-]*token[a-z0-9_-]*|` +
		`[a-z0-9_-]*password[a-z0-9_-]*|[a-z0-9_-]*key[a-z0-9_-]*)")\s*:\s*"(?:\\.|[^"\\])*"`,
)

// sensitiveContainerFallbackRegex redacts truncated/non-parsed object maps on container keys.
var sensitiveContainerFallbackRegex = regexp.MustCompile(
	`(?i)("(?:values|envs|env[_-]?vars|credentials|headers|custom[_-]?headers)")\s*:\s*\{[^{}]*\}`,
)

// dataURIRegex matches an inline data URI whose payload is base64. Request logs
// are capped at 10KB, and an image upload still fills that cap with file bytes.
var dataURIRegex = regexp.MustCompile(`(?i)\bdata:[^\s"'<>]{0,128};base64,[A-Za-z0-9+/=\r\n]*`)

func isSensitiveContainerKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "values", "envs", "env_vars", "envvars", "env-vars",
		"credentials", "headers", "custom_headers", "customheaders", "custom-headers":
		return true
	default:
		return false
	}
}

func isSensitiveFieldKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" || lower == "keyword" || lower == "keywords" {
		return false
	}
	switch lower {
	case "value", "passwd", "ticket", "assertion",
		"cookie", "set-cookie", "set_cookie", "setcookie",
		"proxy-authorization", "proxy_authorization", "proxyauthorization",
		"authorization",
		"authorization_url", "authorizationurl", "authorization-url",
		"authorization_attempt", "authorizationattempt", "authorization-attempt",
		"pairing_link", "pairinglink", "pairing-link":
		return true
	}
	return strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "key")
}

func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func readJSONStringToken(src []byte, pos *int) (raw []byte, decoded string, ok bool) {
	i := *pos
	if i >= len(src) || src[i] != '"' {
		return nil, "", false
	}
	i++
	for i < len(src) {
		c := src[i]
		if c == '\\' {
			i += 2
			if i > len(src) {
				return nil, "", false
			}
			continue
		}
		if c == '"' {
			i++
			raw = src[*pos:i]
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, "", false
			}
			*pos = i
			return raw, s, true
		}
		if c < 0x20 {
			return nil, "", false
		}
		i++
	}
	return nil, "", false
}

func skipJSONScalar(src []byte, pos *int) bool {
	start := *pos
	i := start
	for i < len(src) {
		c := src[i]
		if isJSONSpace(c) || c == ',' || c == '}' || c == ']' {
			break
		}
		i++
	}
	if i == start || !json.Valid(src[start:i]) {
		return false
	}
	*pos = i
	return true
}

func skipJSONValue(src []byte, pos *int, depth int) bool {
	if depth > 64 {
		return false
	}
	for *pos < len(src) && isJSONSpace(src[*pos]) {
		*pos++
	}
	if *pos >= len(src) {
		return false
	}
	switch src[*pos] {
	case '"':
		_, _, ok := readJSONStringToken(src, pos)
		return ok
	case '{':
		*pos++
		for *pos < len(src) && isJSONSpace(src[*pos]) {
			*pos++
		}
		if *pos < len(src) && src[*pos] == '}' {
			*pos++
			return true
		}
		for {
			for *pos < len(src) && isJSONSpace(src[*pos]) {
				*pos++
			}
			if _, _, ok := readJSONStringToken(src, pos); !ok {
				return false
			}
			for *pos < len(src) && isJSONSpace(src[*pos]) {
				*pos++
			}
			if *pos >= len(src) || src[*pos] != ':' {
				return false
			}
			*pos++
			if !skipJSONValue(src, pos, depth+1) {
				return false
			}
			for *pos < len(src) && isJSONSpace(src[*pos]) {
				*pos++
			}
			if *pos >= len(src) {
				return false
			}
			if src[*pos] == ',' {
				*pos++
				continue
			}
			if src[*pos] == '}' {
				*pos++
				return true
			}
			return false
		}
	case '[':
		*pos++
		for *pos < len(src) && isJSONSpace(src[*pos]) {
			*pos++
		}
		if *pos < len(src) && src[*pos] == ']' {
			*pos++
			return true
		}
		for {
			if !skipJSONValue(src, pos, depth+1) {
				return false
			}
			for *pos < len(src) && isJSONSpace(src[*pos]) {
				*pos++
			}
			if *pos >= len(src) {
				return false
			}
			if src[*pos] == ',' {
				*pos++
				continue
			}
			if src[*pos] == ']' {
				*pos++
				return true
			}
			return false
		}
	default:
		return skipJSONScalar(src, pos)
	}
}

func redactJSONBytes(src []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(src)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	var out bytes.Buffer
	out.Grow(len(src))
	pos := 0

	var scanValue func(forceRedact bool, depth int) bool
	scanObject := func(forceRedact bool, depth int) bool {
		out.WriteByte('{')
		pos++
		wsStart := pos
		for pos < len(src) && isJSONSpace(src[pos]) {
			pos++
		}
		if pos < len(src) && src[pos] == '}' {
			out.Write(src[wsStart:pos])
			out.WriteByte('}')
			pos++
			return true
		}
		pos = wsStart
		for {
			wsKey := pos
			for pos < len(src) && isJSONSpace(src[pos]) {
				pos++
			}
			out.Write(src[wsKey:pos])
			keyRaw, keyName, ok := readJSONStringToken(src, &pos)
			if !ok {
				return false
			}
			colonStart := pos
			for pos < len(src) && isJSONSpace(src[pos]) {
				pos++
			}
			if pos >= len(src) || src[pos] != ':' {
				return false
			}
			pos++
			for pos < len(src) && isJSONSpace(src[pos]) {
				pos++
			}
			valStart := pos

			out.Write(keyRaw)
			switch {
			case isSensitiveContainerKey(keyName):
				out.WriteByte(':')
				if !scanValue(true, depth+1) {
					return false
				}
			case forceRedact || isSensitiveFieldKey(keyName):
				if !skipJSONValue(src, &pos, depth+1) {
					return false
				}
				out.WriteString(`:"***"`)
			default:
				out.Write(src[colonStart:valStart])
				if !scanValue(false, depth+1) {
					return false
				}
			}

			wsEnd := pos
			for pos < len(src) && isJSONSpace(src[pos]) {
				pos++
			}
			out.Write(src[wsEnd:pos])
			if pos >= len(src) {
				return false
			}
			if src[pos] == ',' {
				out.WriteByte(',')
				pos++
				continue
			}
			if src[pos] == '}' {
				out.WriteByte('}')
				pos++
				return true
			}
			return false
		}
	}

	scanArray := func(forceRedact bool, depth int) bool {
		out.WriteByte('[')
		pos++
		wsStart := pos
		for pos < len(src) && isJSONSpace(src[pos]) {
			pos++
		}
		if pos < len(src) && src[pos] == ']' {
			out.Write(src[wsStart:pos])
			out.WriteByte(']')
			pos++
			return true
		}
		pos = wsStart
		for {
			wsElem := pos
			for pos < len(src) && isJSONSpace(src[pos]) {
				pos++
			}
			out.Write(src[wsElem:pos])
			if !scanValue(forceRedact, depth+1) {
				return false
			}
			wsAfter := pos
			for pos < len(src) && isJSONSpace(src[pos]) {
				pos++
			}
			out.Write(src[wsAfter:pos])
			if pos >= len(src) {
				return false
			}
			if src[pos] == ',' {
				out.WriteByte(',')
				pos++
				continue
			}
			if src[pos] == ']' {
				out.WriteByte(']')
				pos++
				return true
			}
			return false
		}
	}

	scanValue = func(forceRedact bool, depth int) bool {
		if depth > 64 {
			return false
		}
		ws := pos
		for pos < len(src) && isJSONSpace(src[pos]) {
			pos++
		}
		out.Write(src[ws:pos])
		if pos >= len(src) {
			return false
		}
		switch src[pos] {
		case '{':
			return scanObject(forceRedact, depth)
		case '[':
			return scanArray(forceRedact, depth)
		case '"':
			raw, _, ok := readJSONStringToken(src, &pos)
			if !ok {
				return false
			}
			if forceRedact {
				out.WriteString(`"***"`)
			} else {
				out.Write(raw)
			}
			return true
		default:
			start := pos
			if !skipJSONScalar(src, &pos) {
				return false
			}
			token := src[start:pos]
			if forceRedact && !bytes.Equal(token, []byte("null")) {
				out.WriteString(`"***"`)
			} else {
				out.Write(token)
			}
			return true
		}
	}

	if !scanValue(false, 0) {
		return nil, false
	}
	trail := pos
	for pos < len(src) && isJSONSpace(src[pos]) {
		pos++
	}
	if pos != len(src) {
		return nil, false
	}
	out.Write(src[trail:pos])
	return out.Bytes(), true
}

// sanitizeBody 清理敏感信息，并把 data URI 的 base64 载荷换成长度标记。
func sanitizeBody(body string) string {
	if redactedJSON, ok := redactJSONBytes([]byte(body)); ok {
		return dataURIRegex.ReplaceAllStringFunc(string(redactedJSON), redactDataURI)
	}
	redacted := dataURIRegex.ReplaceAllStringFunc(body, redactDataURI)
	redacted = sensitiveFieldRegex.ReplaceAllStringFunc(redacted, func(match string) string {
		sub := sensitiveFieldRegex.FindStringSubmatch(match)
		if len(sub) >= 3 && (strings.EqualFold(sub[2], "keyword") || strings.EqualFold(sub[2], "keywords")) {
			return match
		}
		if len(sub) >= 2 {
			return sub[1] + `:"***"`
		}
		return `"***"`
	})
	return sensitiveContainerFallbackRegex.ReplaceAllString(redacted, `$1:{"***":"***"}`)
}

func redactDataURI(match string) string {
	marker := ";base64,"
	idx := strings.Index(strings.ToLower(match), marker)
	if idx < 0 {
		return "data:[redacted]"
	}
	payloadLen := len(match) - idx - len(marker)
	return fmt.Sprintf("%s;base64,[redacted %d chars]", match[:idx], payloadLen)
}

var sensitiveQueryFields = map[string]struct{}{
	"access_token":          {},
	"api_key":               {},
	"apikey":                {},
	"authorization_attempt": {},
	"client_secret":         {},
	"code":                  {},
	"id_token":              {},
	"password":              {},
	"refresh_token":         {},
	"secret":                {},
	"sig":                   {},
	"signature":             {},
	"state":                 {},
	// ticket is the sandbox terminal's WebSocket handshake credential. A
	// browser cannot set Authorization on an upgrade, so it travels in the
	// query string; anyone holding it for its 2-minute TTL can open a shell
	// in the session's sandbox, which is why it must never reach a log line.
	"ticket": {},
	"token":  {},
}

// sanitizeQuery prevents OAuth authorization codes, CSRF/attempt state, and
// handshake credentials from being copied into access logs. Parsing the query
// also covers repeated and percent-encoded parameters without relying on
// fragile string replacement.
func sanitizeQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[invalid query omitted]"
	}
	for key := range values {
		if _, sensitive := sensitiveQueryFields[strings.ToLower(key)]; sensitive {
			values[key] = []string{"***"}
		}
	}
	return values.Encode()
}

func sanitizeFormBody(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[invalid form body omitted]"
	}
	for key, list := range values {
		lower := strings.ToLower(strings.TrimSpace(key))
		_, inQuery := sensitiveQueryFields[lower]
		if inQuery || isSensitiveFieldKey(key) || isSensitiveContainerKey(key) {
			values[key] = []string{"***"}
			continue
		}
		for i, item := range list {
			list[i] = dataURIRegex.ReplaceAllStringFunc(item, redactDataURI)
		}
	}
	return values.Encode()
}

// readRequestBody 读取请求体（限制大小用于日志，但把剩余字节原样交还后续handler）
func readRequestBody(c *gin.Context) string {
	if c.Request.Body == nil {
		return ""
	}

	// 检查Content-Type，只记录文本/JSON/表单类型
	contentType := c.GetHeader("Content-Type")
	lowerContentType := strings.ToLower(contentType)
	if !strings.Contains(lowerContentType, "application/json") &&
		!strings.Contains(lowerContentType, "application/x-www-form-urlencoded") &&
		!strings.Contains(lowerContentType, "text/") {
		return "[非文本类型，已跳过]"
	}

	// 重置request body：把已窥探的前缀接回未读取的剩余部分，handler 仍能读到完整
	// 数据，但中间件自身只驻留日志需要的那点字节。
	//
	// 这里原本是无上限的 io.ReadAll(c.Request.Body)。Logger 注册在 Auth 之前
	// （router.go NewRouter），因此对所有请求执行，而任何路由的
	// http.MaxBytesReader（limitJSONBody / limitUploadBody / browserskill 的 4 KiB
	// 授权接口）都是在 handler 里才装上的：body 早已被整体读进内存，那些 cap 只剩
	// 报错的意义，没有限住内存的作用。没有 cap 的路由（对话、检索、IM webhook、
	// MCP 端点）则连上限都不存在。
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize+1))
	originalBody := c.Request.Body
	c.Request.Body = &replayedBody{
		Reader: io.MultiReader(bytes.NewReader(bodyBytes), originalBody),
		Closer: originalBody,
	}
	if err != nil {
		return "[读取请求体失败]"
	}

	if len(bodyBytes) > maxBodySize {
		return bodyTruncatedMarker
	}

	if strings.Contains(lowerContentType, "application/x-www-form-urlencoded") {
		return sanitizeFormBody(string(bodyBytes))
	}

	return sanitizeBody(string(bodyBytes))
}

func isValidRequestIDHeader(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// RequestID middleware adds a unique request ID to the context
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Accept caller X-Request-ID only when bounded and printable visible ASCII;
		// otherwise mint a fresh UUID to prevent header/log injection.
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if !isValidRequestIDHeader(requestID) {
			requestID = uuid.New().String()
		}
		safeRequestID := secutils.SanitizeForLog(requestID)
		// Set request ID in header
		c.Header("X-Request-ID", requestID)

		// Set request ID in context
		c.Set(types.RequestIDContextKey.String(), requestID)

		// Set logger in context
		requestLogger := logger.GetLogger(c)
		requestLogger = requestLogger.WithField("request_id", safeRequestID)
		c.Set(types.LoggerContextKey.String(), requestLogger)

		// Set request ID in the global context for logging
		c.Request = c.Request.WithContext(
			context.WithValue(
				context.WithValue(c.Request.Context(), types.RequestIDContextKey, requestID),
				types.LoggerContextKey, requestLogger,
			),
		)

		c.Next()
	}
}

// skipsBodyLogging reports routes whose bodies carry credentials, write-only
// vaults, page content or screenshots. Their access metadata is still logged;
// their bodies are never read or buffered.
func skipsBodyLogging(path string) bool {
	if strings.HasPrefix(path, "/api/v1/local-browser/") ||
		path == "/api/v1/me/browser" || strings.HasSuffix(path, "/local-browser") ||
		path == "/api/v1/agent/secure-forms" || strings.HasPrefix(path, "/api/v1/agent/secure-forms/") ||
		path == "/api/v1/me/credential-vault" || strings.HasPrefix(path, "/api/v1/me/credential-vault/") ||
		path == "/api/v1/me/env-vars" || strings.HasPrefix(path, "/api/v1/me/env-vars/") ||
		path == "/api/v1/im-channels" || strings.HasPrefix(path, "/api/v1/im-channels/") ||
		path == "/api/v1/models" || strings.HasPrefix(path, "/api/v1/models/") ||
		path == "/api/v1/initialization" || strings.HasPrefix(path, "/api/v1/initialization/") ||
		path == "/api/v1/system/admin/api-keys" || strings.HasPrefix(path, "/api/v1/system/admin/api-keys/") ||
		strings.HasPrefix(path, "/api/v1/auth/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/sandbox-configs/") && strings.Contains(path, "/skills") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/tenants/") && strings.Contains(path, "/api-keys") {
		return true
	}
	return false
}

// Logger middleware logs request details with request ID, input and output
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		isWikiStats := strings.HasPrefix(path, "/api/v1/knowledgebase/") && strings.HasSuffix(path, "/wiki/stats")
		if strings.HasPrefix(path, "/assets/") || isWikiStats {
			c.Next()
			return
		}

		browserTraffic := skipsBodyLogging(path)
		// 读取请求体（在Next之前读取，因为Next会消费body）
		var requestBody string
		if !browserTraffic && (c.Request.Method == "POST" || c.Request.Method == "PUT" || c.Request.Method == "PATCH") {
			requestBody = readRequestBody(c)
		}

		// 创建响应体捕获器
		responseBody := &bytes.Buffer{}
		responseWriter := &loggerResponseBodyWriter{
			ResponseWriter: c.Writer,
			body:           responseBody,
		}
		if !browserTraffic {
			c.Writer = responseWriter
		}

		// Process request
		c.Next()

		// Get request ID from context
		requestID, exists := c.Get(types.RequestIDContextKey.String())
		requestIDStr := "unknown"
		if exists {
			if idStr, ok := requestID.(string); ok && idStr != "" {
				requestIDStr = idStr
			}
		}
		safeRequestID := secutils.SanitizeForLog(requestIDStr)

		// Calculate latency
		latency := time.Since(start)

		// Get client IP and status code
		clientIP := c.ClientIP()
		statusCode := c.Writer.Status()
		method := c.Request.Method

		if raw != "" {
			path = path + "?" + sanitizeQuery(raw)
		}

		// 读取响应体
		responseBodyStr := ""
		if responseBody.Len() > 0 {
			contentType := c.Writer.Header().Get("Content-Type")
			if strings.Contains(contentType, "text/event-stream") {
				responseBodyStr = "[SSE流式响应，已跳过]"
			} else if strings.Contains(contentType, "application/json") ||
				strings.Contains(contentType, "text/") {
				bodyBytes := responseBody.Bytes()
				if len(bodyBytes) >= maxBodySize {
					responseBodyStr = bodyTruncatedMarker
				} else {
					responseBodyStr = string(bodyBytes)
				}
				responseBodyStr = sanitizeBody(responseBodyStr)
			} else {
				responseBodyStr = "[非文本类型，已跳过]"
			}
		}

		// 构建日志消息
		logMsg := logger.GetLogger(c)
		logMsg = logMsg.WithFields(map[string]interface{}{
			"request_id":  safeRequestID,
			"method":      method,
			"path":        secutils.SanitizeForLog(path),
			"status_code": statusCode,
			"size":        c.Writer.Size(),
			"latency":     latency.String(),
			"client_ip":   secutils.SanitizeForLog(clientIP),
		})

		// 添加请求体（如果有）
		if requestBody != "" {
			logMsg = logMsg.WithField("request_body", secutils.SanitizeForLog(requestBody))
		}

		// 添加响应体（如果有）
		if responseBodyStr != "" {
			logMsg = logMsg.WithField("response_body", secutils.SanitizeForLog(responseBodyStr))
		}
		if last := c.Errors.Last(); last != nil && last.Err != nil {
			logMsg = logMsg.WithField("error", secutils.SanitizeForLog(last.Err.Error()))
		}
		switch {
		case statusCode >= 500:
			logMsg.Error()
		case statusCode >= 400:
			logMsg.Warn()
		default:
			logMsg.Info()
		}
	}
}
