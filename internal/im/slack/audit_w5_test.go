package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/types"
	slacklib "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestSlackFactoryAndFinalizeStream_RequireTokensAndSurfaceError(t *testing.T) {
	factory := NewFactory()

	// WebSocket mode must reject missing app_token or bot_token instead of
	// spawning a background Socket Mode loop with empty credentials.
	for _, creds := range []string{
		`{"bot_token":"xoxb-valid"}`,
		`{"app_token":"xapp-1-valid"}`,
		`{}`,
	} {
		ch := &im.IMChannel{
			ID:          "ch-slack-ws",
			Platform:    string(im.PlatformSlack),
			Mode:        "websocket",
			Credentials: types.JSON(creds),
		}
		if _, cancel, err := factory(context.Background(), ch, nil); err == nil {
			if cancel != nil {
				cancel()
			}
			t.Fatalf("expected websocket factory error for credentials %s, got nil", creds)
		}
	}

	// FinalizeStream must return an error when chat.update fails so im.Service
	// triggers its plain-text SendReply fallback.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "slack api unavailable", http.StatusBadGateway)
	}))
	defer srv.Close()

	api := slacklib.New("xoxb-test", slacklib.OptionAPIURL(srv.URL+"/"))
	adapter := NewWebhookAdapter(api, "signing-secret")

	streamID := "sl:C123:123456.789"
	slackStreamsMu.Lock()
	slackStreams[streamID] = &slackStreamState{
		channel: "C123",
		ts:      "123456.789",
	}
	slackStreamsMu.Unlock()
	t.Cleanup(func() {
		slackStreamsMu.Lock()
		delete(slackStreams, streamID)
		slackStreamsMu.Unlock()
	})

	if err := adapter.FinalizeStream(
		context.Background(), &im.IncomingMessage{ChatID: "C123"}, streamID, "final answer",
	); err == nil {
		t.Fatal("expected FinalizeStream error when Slack UpdateMessageContext fails, got nil")
	}
}

func TestLongConnClient_NilEventRequestAndClosedEventsChannelDoNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	api := slacklib.New(
		"xoxb-test",
		slacklib.OptionAppLevelToken("xapp-1-test"),
		slacklib.OptionAPIURL(srv.URL+"/"),
	)
	smClient := socketmode.New(api)
	lc := &LongConnClient{
		appToken: "xapp-1-test",
		botToken: "xoxb-test",
		api:      api,
		client:   smClient,
		handler:  func(context.Context, *im.IncomingMessage) error { return nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- lc.Start(ctx)
	}()

	// Nil Request on EventsAPI event must be ignored safely rather than panicking.
	smClient.Events <- socketmode.Event{
		Type:    socketmode.EventTypeEventsAPI,
		Data:    slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent},
		Request: nil,
	}

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		_ = err
	case <-time.After(time.Second):
		t.Fatal("Slack LongConnClient.Start did not terminate on context cancellation")
	}
}
