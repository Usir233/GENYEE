package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
)

func TestValidateFileDownloadURL_RejectsNonHTTPSAndUserinfo(t *testing.T) {
	rejected := []string{
		"http://wukong-abc.oss-cn-hangzhou.aliyuncs.com/file?sig=x",
		"file://api.dingtalk.com/etc/passwd",
		"https://user:pass@api.dingtalk.com/temp/file",
		"https://.aliyuncs.com/file",
		"https://.dingtalk.com/file",
	}
	for _, rawURL := range rejected {
		if err := defaultValidateFileDownloadURL(rawURL); err == nil {
			t.Errorf("defaultValidateFileDownloadURL(%q) = nil, want rejection error", rawURL)
		}
	}

	allowed := []string{
		"https://wukong-abc.oss-cn-hangzhou.aliyuncs.com/file?sig=x",
		"https://api.dingtalk.com/temp/file",
	}
	for _, rawURL := range allowed {
		if !isAllowedDingTalkDownloadHost(rawURL) {
			t.Errorf("isAllowedDingTalkDownloadHost(%q) = false, want true", rawURL)
		}
	}
}

func TestReplyViaOpenAPIAndFinalizeStream_HonorBaseURLAndSurfaceFailure(t *testing.T) {
	useTestHTTPClient(t)

	var oToHit, groupHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "tok-1", "expireIn": 7200})
		case "/v1.0/robot/oToMessages/batchSend":
			oToHit = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "/v1.0/robot/groupMessages/send":
			groupHit = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "/v1.0/card/streaming":
			http.Error(w, "card backend down", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origBase := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = origBase }()

	a := &Adapter{clientID: "cid", clientSecret: "sec"}
	if err := a.replyViaOpenAPI(context.Background(), &im.IncomingMessage{
		ChatType: im.ChatTypeDirect,
		UserID:   "user-1",
	}, "hello dm"); err != nil {
		t.Fatalf("replyViaOpenAPI direct error: %v", err)
	}
	if !oToHit {
		t.Fatal("replyViaOpenAPI did not use configured apiBaseURL for direct message")
	}

	if err := a.replyViaOpenAPI(context.Background(), &im.IncomingMessage{
		ChatType: im.ChatTypeGroup,
		ChatID:   "cid-group-1",
	}, "hello group"); err != nil {
		t.Fatalf("replyViaOpenAPI group error: %v", err)
	}
	if !groupHit {
		t.Fatal("replyViaOpenAPI did not use configured apiBaseURL for group message")
	}

	// FinalizeStream must return an error when card streaming update fails so
	// im.Service triggers its SendReply plain-text fallback.
	streamID := "dt:test:finalize-fail"
	streamsMu.Lock()
	dStreams[streamID] = &streamState{
		outTrackID:     "track-1",
		lastActivityAt: time.Now(),
	}
	streamsMu.Unlock()
	t.Cleanup(func() {
		streamsMu.Lock()
		delete(dStreams, streamID)
		streamsMu.Unlock()
	})

	if err := a.FinalizeStream(
		context.Background(), &im.IncomingMessage{UserID: "user-1"}, streamID, "final answer",
	); err == nil {
		t.Fatal("expected FinalizeStream to return error when card update fails, got nil")
	}
}
