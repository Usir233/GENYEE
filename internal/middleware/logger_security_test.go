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
		`"credentials":{"app_secret":"sec-1","bot_token":"tok-1"},` +
		`"custom_headers":{"X-Upstream-Auth":"Bearer xyz"},` +
		`"value":"vault-secret","webhook_secret":"whsec_123","custom_key_value":"k1","keyword":"keep-me"}`

	got := sanitizeBody(in)

	for _, forbidden := range []string{
		"alice", "9988", `a\"b\"c`, "sk-123", "secret-env",
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
