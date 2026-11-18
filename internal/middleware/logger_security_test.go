package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSanitizeBodyRecursiveFuzzyRedaction(t *testing.T) {
	in := `{"action":"submit","values":{"username":"alice","pin":"9988","quote":"a\"b\"c"},` +
		`"envs":{"OPENAI_KEY":"sk-123","PLAIN_VAR":"secret-env"},` +
		`"env_vars":{"MCP_STDIO_CRED":"stdio-secret","PLAIN_ENV":"plain-env-secret"},` +
		`"headers":{"Authorization":"Bearer mcp-hdr","X-Custom":"hdr-secret"},` +
		`"credentials":{"app_secret":"sec-1","bot_token":"tok-1"},` +
		`"custom_headers":{"X-Upstream-Auth":"Bearer xyz"},` +
		`"cookie":"sid=cookie-secret","set-cookie":"sid=set-cookie-secret","proxy-authorization":"Basic cHJveHk=",` +
		`"value":"vault-secret","webhook_secret":"whsec_123","custom_key_value":"k1","keyword":"keep-me"}`

	got := sanitizeBody(in)

	for _, forbidden := range []string{
		"alice", "9988", `a\"b\"c`, "sk-123", "secret-env",
		"stdio-secret", "plain-env-secret", "Bearer mcp-hdr", "hdr-secret",
		"cookie-secret", "set-cookie-secret", "cHJveHk=",
		"sec-1", "tok-1", "Bearer xyz", "vault-secret", "whsec_123", "k1",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitizeBody leaked sensitive value %q in %s", forbidden, got)
		}
	}
	if !strings.Contains(got, `"keyword":"keep-me"`) {
		t.Fatalf("expected non-secret keyword field to remain intact, got %s", got)
	}
}

func TestReadRequestBodyRedactsFormUrlencodedSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := "client_id=my-app&client_secret=super-secret-123&password=my%2Bpass&code=oauth-code-9&keyword=keep"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/mcp-services/callback", strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	got := readRequestBody(c)
	for _, leaked := range []string{"super-secret-123", "my+pass", "my%2Bpass", "oauth-code-9"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("form-urlencoded body leaked %q in %q", leaked, got)
		}
	}
	if !strings.Contains(got, "client_id=my-app") || !strings.Contains(got, "keyword=keep") {
		t.Fatalf("expected non-sensitive form fields preserved, got %q", got)
	}

	// Malformed percent-encoding in form body fails closed.
	cBad, _ := gin.CreateTestContext(httptest.NewRecorder())
	cBad.Request = httptest.NewRequest(
		http.MethodPost, "/api/v1/mcp-services/callback", strings.NewReader("client_secret=%ZZbad"),
	)
	cBad.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if badGot := readRequestBody(cBad); badGot != "[invalid form body omitted]" {
		t.Fatalf("malformed form body = %q, want [invalid form body omitted]", badGot)
	}
}

func TestLoggerMiddlewareDoesNotBufferSkippedSecretRouteBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Logger())

	var handlerRead string
	r.PUT("/api/v1/me/env-vars/skill", func(c *gin.Context) {
		var body [64]byte
		n, _ := c.Request.Body.Read(body[:])
		handlerRead = string(body[:n])
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	payload := `{"skill_id":"sk1","name":"API_TOKEN","value":"top-secret-val"}`
	counter := &countingBody{Reader: strings.NewReader(payload)}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/env-vars/skill", nil)
	req.Body = counter
	req.ContentLength = int64(len(payload))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if handlerRead != payload {
		t.Fatalf("handler read %q, want %q", handlerRead, payload)
	}
}
