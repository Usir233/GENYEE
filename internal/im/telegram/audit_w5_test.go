package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/types"
)

type tgRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tgRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestTelegramRedactsBotTokenFromTransportErrors(t *testing.T) {
	secretBotToken := "99887766:AAH-TopSecretBotTokenValue"

	origClient := httpClient
	httpClient = &http.Client{
		Transport: tgRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
	}
	t.Cleanup(func() { httpClient = origClient })

	adapter := NewWebhookAdapter(secretBotToken, "wh-secret")
	err := adapter.SendReply(
		context.Background(),
		&im.IncomingMessage{ChatID: "12345"},
		&im.ReplyMessage{Content: "hello"},
	)
	if err == nil {
		t.Fatal("expected SendReply transport error, got nil")
	}
	if strings.Contains(err.Error(), secretBotToken) {
		t.Fatalf("SendReply leaked bot_token in error: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected unwrapped DeadlineExceeded preserved, got: %v", err)
	}

	lc := NewLongConnClient(secretBotToken, func(context.Context, *im.IncomingMessage) error { return nil })
	lc.httpClient = httpClient
	_, pollErr := lc.getUpdates(context.Background())
	if pollErr == nil {
		t.Fatal("expected getUpdates error, got nil")
	}
	if strings.Contains(pollErr.Error(), secretBotToken) {
		t.Fatalf("getUpdates leaked bot_token in error: %v", pollErr)
	}
	if !errors.Is(pollErr, context.DeadlineExceeded) {
		t.Fatalf("expected unwrapped DeadlineExceeded preserved in getUpdates, got: %v", pollErr)
	}
}

func TestDownloadFileAndFinalizeStream_PathValidationAndErrorPropagation(t *testing.T) {
	secretBotToken := "99887766:AAH-TopSecretBotTokenValue"

	for _, badFilePath := range []string{
		"",
		"../bot111:other/getMe",
		"/leading/slash.pdf",
		"docs\\back.pdf",
		"photos/pic.jpg?foo=bar",
		"photos/pic.jpg#frag",
	} {
		var fileGETCount int
		origClient := httpClient
		httpClient = &http.Client{
			Transport: tgRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getFile") {
					escapedPath := strings.ReplaceAll(badFilePath, `\`, `\\`)
					body := `{"ok":true,"result":{"file_path":"` + escapedPath + `","file_size":10}}`
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(body)),
						Header:     make(http.Header),
					}, nil
				}
				fileGETCount++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte("x"))),
					Header:     make(http.Header),
				}, nil
			}),
		}
		adapter := NewWebhookAdapter(secretBotToken, "wh-secret")
		rc, _, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{FileKey: "file-1"})
		httpClient = origClient
		if err == nil {
			_ = rc.Close()
			t.Fatalf("DownloadFile accepted malicious file_path %q", badFilePath)
		}
		if fileGETCount != 0 {
			t.Fatalf("malicious file_path %q triggered %d download requests, want 0", badFilePath, fileGETCount)
		}
	}

	// FinalizeStream must surface retry failure when both Markdown and plain edits fail.
	origClient := httpClient
	httpClient = &http.Client{
		Transport: tgRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"ok":false,"result":"message not found"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	t.Cleanup(func() { httpClient = origClient })

	adapter := NewWebhookAdapter(secretBotToken, "wh-secret")
	streamID := "tg:100:200"
	streamsMu.Lock()
	streams[streamID] = &streamState{chatID: "100", msgID: "200"}
	streamsMu.Unlock()
	t.Cleanup(func() {
		streamsMu.Lock()
		delete(streams, streamID)
		streamsMu.Unlock()
	})

	if err := adapter.FinalizeStream(
		context.Background(), &im.IncomingMessage{ChatID: "100"}, streamID, "final",
	); err == nil {
		t.Fatal("expected FinalizeStream to return error when both edit attempts fail, got nil")
	}

	// Factory in websocket mode must reject empty bot_token.
	factory := NewFactory()
	if _, cancel, err := factory(context.Background(), &im.IMChannel{
		ID:          "ch-tg-empty",
		Platform:    string(im.PlatformTelegram),
		Mode:        "websocket",
		Credentials: types.JSON(`{}`),
	}, nil); err == nil {
		if cancel != nil {
			cancel()
		}
		t.Fatal("expected telegram websocket factory to reject empty bot_token")
	}
}
