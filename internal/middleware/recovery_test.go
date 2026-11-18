package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecovery_DoesNotLeakPanicDetailsToClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), Recovery())
	r.GET("/boom", func(*gin.Context) {
		panic("postgres://admin:top-secret-pw@10.0.0.5:5432/prod dial failed")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	req.Header.Set("X-Request-ID", "req-panic-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, secret := range []string{"top-secret-pw", "postgres://", "10.0.0.5"} {
		if strings.Contains(body, secret) {
			t.Fatalf("Recovery() leaked internal panic detail %q in response body: %s", secret, body)
		}
	}
}

func TestErrorHandlerAndRecoveryDoNotCorruptAlreadyWrittenStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Recovery(), ErrorHandler())
	r.GET("/sse-err", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write([]byte("data: chunk-1\n\n"))
		c.Writer.Flush()
		_ = c.Error(http.ErrAbortHandler)
	})
	r.GET("/sse-panic", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write([]byte("data: chunk-1\n\n"))
		c.Writer.Flush()
		panic("mid-stream boom")
	})

	for _, path := range []string{"/sse-err", "/sse-panic"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if got := w.Body.String(); got != "data: chunk-1\n\n" {
			t.Fatalf("%s appended trailing JSON onto already-written stream: %q", path, got)
		}
	}
}
