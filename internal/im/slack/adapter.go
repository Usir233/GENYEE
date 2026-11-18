// Package slack implements the Slack Events API and Socket Mode IM adapter for GENYEE.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/logger"
)

// Compile-time checks.
var (
	_ im.Adapter        = (*Adapter)(nil)
	_ im.StreamSender   = (*Adapter)(nil)
	_ im.FileDownloader = (*Adapter)(nil)
)

// Adapter implements im.Adapter and im.StreamSender for Slack.
// It delegates to the Slack LongConnClient for Socket Mode.
type Adapter struct {
	client        *LongConnClient
	api           *slack.Client
	signingSecret string
}

// NewAdapter creates an adapter backed by a Slack long connection client.
func NewAdapter(client *LongConnClient, api *slack.Client) *Adapter {
	startStreamReaper()
	return &Adapter{
		client: client,
		api:    api,
	}
}

// NewWebhookAdapter creates an adapter for Slack Events API via Webhook.
func NewWebhookAdapter(api *slack.Client, signingSecret string) *Adapter {
	startStreamReaper()
	return &Adapter{
		api:           api,
		signingSecret: signingSecret,
	}
}

func parseIncomingMessage(
	user, channel, text, ts string,
	chatType im.ChatType,
	files []slack.File,
) *im.IncomingMessage {
	content := text
	if chatType == im.ChatTypeGroup {
		// Slack mentions are in the format <@U12345678>
		for strings.HasPrefix(content, "<@") {
			idx := strings.Index(content, ">")
			if idx >= 0 {
				content = strings.TrimSpace(content[idx+1:])
			} else {
				break
			}
		}
	}

	msg := &im.IncomingMessage{
		Platform:  im.PlatformSlack,
		UserID:    user,
		ChatID:    channel,
		ChatType:  chatType,
		Content:   strings.TrimSpace(content),
		MessageID: ts,
		ThreadID:  ts,
	}

	if len(files) > 0 {
		file := files[0]
		msg.FileKey = file.ID
		msg.FileName = file.Name
		msg.FileSize = int64(file.Size)
		msg.Extra = map[string]string{
			"url_private_download": file.URLPrivateDownload,
		}
		if strings.HasPrefix(file.Mimetype, "image/") {
			msg.MessageType = im.MessageTypeImage
		} else {
			msg.MessageType = im.MessageTypeFile
		}
	} else {
		msg.MessageType = im.MessageTypeText
	}

	return msg
}

// Platform returns the platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformSlack
}

// VerifyCallback validates the Slack signing secret signature on Events API webhooks.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.signingSecret == "" {
		return fmt.Errorf("webhook verification secret is required")
	}

	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	sv, err := slack.NewSecretsVerifier(c.Request.Header, a.signingSecret)
	if err != nil {
		return fmt.Errorf("new secrets verifier: %w", err)
	}
	if _, err := sv.Write(bodyBytes); err != nil {
		return fmt.Errorf("write body to verifier: %w", err)
	}
	if err := sv.Ensure(); err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}

	return nil
}

// ParseCallback parses a Slack Events API webhook body into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(bodyBytes), slackevents.OptionNoVerifyToken())
	if err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}

	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		var rawEvent struct {
			Event struct {
				Files []slack.File `json:"files"`
			} `json:"event"`
		}
		_ = json.Unmarshal(bodyBytes, &rawEvent)
		files := rawEvent.Event.Files

		innerEvent := eventsAPIEvent.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			threadTs := ev.ThreadTimeStamp
			if threadTs == "" {
				threadTs = ev.TimeStamp
			}
			return parseIncomingMessage(ev.User, ev.Channel, ev.Text, threadTs, im.ChatTypeGroup, files), nil
		case *slackevents.MessageEvent:
			if ev.BotID != "" || (ev.SubType != "" && ev.SubType != "file_share") {
				return nil, nil
			}
			chatType := im.ChatTypeDirect
			if ev.ChannelType == "channel" || ev.ChannelType == "group" {
				chatType = im.ChatTypeGroup
			}
			threadTs := ev.ThreadTimeStamp
			if threadTs == "" {
				threadTs = ev.TimeStamp
			}
			return parseIncomingMessage(ev.User, ev.Channel, ev.Text, threadTs, chatType, files), nil
		}
	}

	return nil, nil
}

// HandleURLVerification responds to Slack's Events API url_verification challenge.
func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var body struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return false
	}

	if body.Type == "url_verification" {
		c.JSON(http.StatusOK, gin.H{"challenge": body.Challenge})
		return true
	}

	return false
}

// SendReply posts a text reply to the Slack channel or thread.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.UserID
	}

	options := []slack.MsgOption{slack.MsgOptionText(reply.Content, false)}
	if incoming.MessageID != "" {
		options = append(options, slack.MsgOptionTS(incoming.MessageID))
	}

	_, _, err := a.api.PostMessageContext(ctx, channelID, options...)
	if err != nil {
		return fmt.Errorf("slack post message: %w", err)
	}
	return nil
}

// slackStreamState tracks per-stream accumulated content.
type slackStreamState struct {
	mu             sync.Mutex
	content        strings.Builder
	ts             string // The timestamp of the message being updated
	channel        string // The channel ID
	lastActivityAt time.Time
	ownerDone      <-chan struct{}
}

const (
	streamOrphanTTL      = 5 * time.Minute
	streamReaperInterval = 1 * time.Minute
)

var (
	slackStreamsMu sync.Mutex
	slackStreams   = map[string]*slackStreamState{}
	reaperMu       sync.Mutex
	reaperRunning  bool
	reaperStopCh   chan struct{}
	reaperDoneCh   chan struct{}
)

func startStreamReaper() {
	reaperMu.Lock()
	if reaperRunning {
		reaperMu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	reaperStopCh = stopCh
	reaperDoneCh = doneCh
	reaperRunning = true
	reaperMu.Unlock()

	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(streamReaperInterval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				reapOrphanStreams(now)
			case <-stopCh:
				return
			}
		}
	}()
}

func reapOrphanStreams(now time.Time) {
	slackStreamsMu.Lock()
	defer slackStreamsMu.Unlock()
	for id, state := range slackStreams {
		if state.ownerDone != nil {
			select {
			case <-state.ownerDone:
			default:
				state.lastActivityAt = now
				continue
			}
		}
		if state.lastActivityAt.Before(now.Add(-streamOrphanTTL)) {
			delete(slackStreams, id)
		}
	}
}

func lookupStream(streamID string) (*slackStreamState, bool) {
	slackStreamsMu.Lock()
	defer slackStreamsMu.Unlock()
	state, ok := slackStreams[streamID]
	if ok {
		state.lastActivityAt = time.Now()
	}
	return state, ok
}

// StopStreamReaper stops the background stream reaper goroutine and waits for it to exit.
func StopStreamReaper() {
	reaperMu.Lock()
	stopCh := reaperStopCh
	doneCh := reaperDoneCh
	if reaperRunning {
		reaperRunning = false
		reaperStopCh = nil
		close(stopCh)
	}
	reaperMu.Unlock()

	if doneCh != nil {
		<-doneCh
	}
}

func isStreamReaperRunning() bool {
	reaperMu.Lock()
	defer reaperMu.Unlock()
	return reaperRunning
}

// StartStream posts a placeholder message in Slack and registers the stream state.
func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.UserID
	}

	options := []slack.MsgOption{slack.MsgOptionText("正在思考...", false)}
	if incoming.MessageID != "" {
		options = append(options, slack.MsgOptionTS(incoming.MessageID))
	}

	// Send initial "Thinking..." message
	_, ts, err := a.api.PostMessageContext(ctx, channelID, options...)
	if err != nil {
		return "", fmt.Errorf("slack start stream: %w", err)
	}

	streamID := fmt.Sprintf("%s:%s", channelID, ts)

	slackStreamsMu.Lock()
	slackStreams[streamID] = &slackStreamState{
		ts:             ts,
		channel:        channelID,
		lastActivityAt: time.Now(),
		ownerDone:      ctx.Done(),
	}
	slackStreamsMu.Unlock()

	logger.Infof(ctx, "[Slack] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

// UpdateStreamContent updates the Slack message with the latest accumulated stream text.
func (a *Adapter) UpdateStreamContent(
	ctx context.Context, _ *im.IncomingMessage, streamID string, fullContent string,
) error {
	return a.updateStreamContent(ctx, streamID, fullContent, false)
}

// FinalizeStream updates the Slack message with the final answer and surfaces API errors.
func (a *Adapter) FinalizeStream(
	ctx context.Context, _ *im.IncomingMessage, streamID string, finalContent string,
) error {
	return a.updateStreamContent(ctx, streamID, finalContent, true)
}

func (a *Adapter) updateStreamContent(ctx context.Context, streamID string, fullContent string, surfaceErr bool) error {
	if fullContent == "" {
		return nil
	}

	state, ok := lookupStream(streamID)
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	state.content.Reset()
	state.content.WriteString(fullContent)
	channel := state.channel
	ts := state.ts
	state.mu.Unlock()

	_, _, _, err := a.api.UpdateMessageContext(ctx, channel, ts, slack.MsgOptionText(fullContent, false))
	if err != nil {
		logger.Warnf(ctx, "[Slack] Failed to update stream content: %v", err)
		if surfaceErr {
			return err
		}
	}
	return nil
}

// SendStreamChunk forwards incremental updates to UpdateStreamContent.
func (a *Adapter) SendStreamChunk(
	ctx context.Context,
	incoming *im.IncomingMessage,
	streamID string,
	content string,
) error {
	return a.UpdateStreamContent(ctx, incoming, streamID, content)
}

// EndStream performs a final message update and removes the stream state.
func (a *Adapter) EndStream(ctx context.Context, _ *im.IncomingMessage, streamID string) error {
	slackStreamsMu.Lock()
	state, ok := slackStreams[streamID]
	delete(slackStreams, streamID)
	slackStreamsMu.Unlock()

	if !ok {
		return nil
	}

	state.mu.Lock()
	fullContent := state.content.String()
	state.mu.Unlock()

	_, _, _, err := a.api.UpdateMessageContext(ctx, state.channel, state.ts, slack.MsgOptionText(fullContent, false))
	if err != nil {
		logger.Warnf(ctx, "[Slack] Failed to end stream: %v", err)
	}

	logger.Infof(ctx, "[Slack] Streaming ended: stream_id=%s", streamID)
	return nil
}

// DownloadFile downloads a private Slack file attachment with bounded size.
func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("file_key is required")
	}

	downloadURL := ""
	if msg.Extra != nil {
		downloadURL = msg.Extra["url_private_download"]
	}

	if downloadURL == "" {
		file, _, _, err := a.api.GetFileInfoContext(ctx, msg.FileKey, 0, 0)
		if err != nil {
			return nil, "", fmt.Errorf("get file info: %w", err)
		}
		downloadURL = file.URLPrivateDownload
	}

	if downloadURL == "" {
		return nil, "", fmt.Errorf("no download URL available for file %s", msg.FileKey)
	}

	u, err := url.Parse(downloadURL)
	if err != nil || !isTrustedFileDownloadURL(u) {
		return nil, "", fmt.Errorf("untrusted Slack file download URL")
	}

	pr, pw := io.Pipe()
	go func() {
		err := a.api.GetFileContext(ctx, downloadURL, pw)
		pw.CloseWithError(err)
	}()

	return im.LimitAttachmentReadCloser(pr), msg.FileName, nil
}

// isTrustedFileDownloadURL accepts private download endpoints, not public
// permalinks or workspace pages. The SDK attaches the bot token to this URL.
func isTrustedFileDownloadURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "files.slack.com":
		return true
	case "slack.com":
		// Legacy private download URLs remain documented in files.sharedPublicURL.
		return strings.HasPrefix(u.Path, "/files-pri/")
	default:
		return false
	}
}
