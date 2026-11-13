package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestLoggerTruncationCannotExposePartialCredentials(t *testing.T) {
	for _, response := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "response"}[response], func(t *testing.T) {
			const secret = "fixture-credential-0123456789abcdef"
			prefix := `{"name":"` + strings.Repeat("a", maxBodySize-len(`{"name":"","config":{"secret_access_key":"`)-len("fixture-credential")) + `","config":{"secret_access_key":"`
			payload := prefix + secret + `"}}`
			require.True(t, json.Valid([]byte(payload)))
			require.Greater(t, len(payload), maxBodySize)
			var output bytes.Buffer
			log := logrus.New()
			log.SetFormatter(&logrus.JSONFormatter{})
			log.SetOutput(&output)
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.ContextWithFallback = true
			r.Use(Logger())
			var body string
			r.POST("/api/v1/storage-backends", func(c *gin.Context) {
				data, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				body = string(data)
				if response {
					c.Data(http.StatusOK, "application/json", []byte(payload))
				} else {
					c.JSON(http.StatusOK, gin.H{"success": true})
				}
			})
			requestPayload := payload
			if response {
				requestPayload = `{}`
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/storage-backends", strings.NewReader(requestPayload))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), types.LoggerContextKey, logrus.NewEntry(log)))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, requestPayload, body, "logging must not change the handler's body")
			if response {
				require.Equal(t, payload, w.Body.String(), "logging must not change the response")
			}
			var entry map[string]any
			require.NoError(t, json.Unmarshal(output.Bytes(), &entry))
			field := "request_body"
			if response {
				field = "response_body"
			}
			logged, ok := entry[field].(string)
			require.True(t, ok)
			require.NotContains(t, logged, "fixture-credential", "a truncated JSON string has no closing quote for regex fallback to redact")
			require.Equal(t, "/api/v1/storage-backends", entry["path"])
		})
	}
}
