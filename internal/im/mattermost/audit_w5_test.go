package mattermost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestClient_RejectsPathTraversalInPostAndFileIDs(t *testing.T) {
	withMattermostSSRFWhitelist(t, "127.0.0.1")

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "bot-token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	maliciousIDs := []string{
		"",
		"../users/me",
		"../../config",
		"abc/def",
		"abc\\def",
		"id?include_deleted=true",
		"id#frag",
		"id with space",
	}

	ctx := context.Background()
	for _, badID := range maliciousIDs {
		if _, err := client.GetPost(ctx, badID); err == nil {
			t.Errorf("GetPost(%q) = nil, want error", badID)
		}
		if err := client.PatchPostMessage(ctx, badID, "hi"); err == nil {
			t.Errorf("PatchPostMessage(%q) = nil, want error", badID)
		}
		if _, err := client.GetFileInfo(ctx, badID); err == nil {
			t.Errorf("GetFileInfo(%q) = nil, want error", badID)
		}
		if rc, err := client.GetFileReader(ctx, badID); err == nil {
			_ = rc.Close()
			t.Errorf("GetFileReader(%q) = nil, want error", badID)
		}
	}

	if got := hits.Load(); got != 0 {
		t.Fatalf("malicious IDs triggered %d HTTP requests, want 0", got)
	}
}

func TestVerifyCallback_RejectsEmptyOutgoingTokenBeforeParsing(t *testing.T) {
	a := &Adapter{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader("not-valid-json"))
	req.Header.Set("Content-Type", "text/plain")
	c.Request = req

	err := a.VerifyCallback(c)
	if err == nil || !strings.Contains(err.Error(), "outgoing webhook token") {
		t.Fatalf("VerifyCallback error = %v, want outgoing webhook token error", err)
	}
}
