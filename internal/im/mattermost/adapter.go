// Package mattermost implements the Mattermost outgoing-webhook and REST v4 IM adapter for GENYEE.
package mattermost

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/logger"
)

// Compile-time checks.
var (
	_ im.Adapter        = (*Adapter)(nil)
	_ im.StreamSender   = (*Adapter)(nil)
	_ im.FileDownloader = (*Adapter)(nil)
)

const (
	extraKeyThreadRoot = "thread_root_id"
	extraKeyChannelID  = "channel_id"
)

// Adapter implements im integration for Mattermost (outgoing webhook inbound + REST outbound).
type Adapter struct {
	client        *Client
	outgoingToken string
	botUserID     string
	// postReplyToMain: when true, bot replies are new top-level channel posts (visible in main timeline).
	// When false (default), replies use root_id tied to the trigger post so they appear in Mattermost threads.
	postReplyToMain bool
}

// NewAdapter creates a Mattermost adapter.
func NewAdapter(client *Client, outgoingToken, botUserID string, postReplyToMain bool) *Adapter {
	startStreamReaper()
	return &Adapter{
		client:          client,
		outgoingToken:   strings.TrimSpace(outgoingToken),
		botUserID:       strings.TrimSpace(botUserID),
		postReplyToMain: postReplyToMain,
	}
}

// outgoingPayload matches Mattermost outgoing webhook parameters (JSON or form).
type outgoingPayload struct {
	Token      string          `json:"token"`
	UserID     string          `json:"user_id"`
	UserName   string          `json:"user_name"`
	ChannelID  string          `json:"channel_id"`
	PostID     string          `json:"post_id"`
	Text       string          `json:"text"`
	RootID     string          `json:"root_id"`
	FileIDsRaw json.RawMessage `json:"file_ids"`
}

// Platform returns the platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformMattermost
}

// HandleURLVerification reports that Mattermost outgoing webhooks do not use challenge handshakes.
func (a *Adapter) HandleURLVerification(_ *gin.Context) bool {
	return false
}

// VerifyCallback validates the Mattermost outgoing webhook token in constant time.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.outgoingToken == "" {
		return fmt.Errorf("invalid outgoing webhook token")
	}
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	payload, err := parseOutgoingBody(c.Request.Header.Get("Content-Type"), bodyBytes)
	if err != nil {
		return fmt.Errorf("parse outgoing payload: %w", err)
	}

	if a.outgoingToken == "" || subtle.ConstantTimeCompare([]byte(payload.Token), []byte(a.outgoingToken)) != 1 {
		return fmt.Errorf("invalid outgoing webhook token")
	}

	return nil
}

// ParseCallback parses a Mattermost outgoing webhook payload into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	payload, err := parseOutgoingBody(c.Request.Header.Get("Content-Type"), bodyBytes)
	if err != nil {
		return nil, err
	}

	if a.botUserID != "" && payload.UserID == a.botUserID {
		logger.Infof(
			c.Request.Context(),
			"[Mattermost] Skip callback: user_id matches bot_user_id (avoid self-reply loop)",
		)
		return nil, nil
	}

	if strings.TrimSpace(payload.Text) == "" && len(parseFileIDs(payload.FileIDsRaw)) == 0 {
		logger.Infof(c.Request.Context(), "[Mattermost] Skip callback: empty text and no file_ids")
		return nil, nil
	}

	var threadRoot string
	if a.postReplyToMain {
		threadRoot = ""
	} else {
		threadRoot = payload.RootID
		if threadRoot == "" {
			// Outgoing webhooks may omit root_id for thread replies.
			// Fetch the post to resolve the actual thread root.
			actualRootID, err := a.client.GetPost(c.Request.Context(), payload.PostID)
			if err == nil && actualRootID != "" {
				threadRoot = actualRootID
			} else {
				// Top-level message: use its own PostID as thread root.
				threadRoot = payload.PostID
			}
		}
	}

	msg := &im.IncomingMessage{
		Platform:  im.PlatformMattermost,
		UserID:    payload.UserID,
		UserName:  payload.UserName,
		ChatID:    payload.ChannelID,
		ChatType:  im.ChatTypeGroup,
		Content:   strings.TrimSpace(payload.Text),
		MessageID: payload.PostID,
		ThreadID:  threadRoot,
		Extra: map[string]string{
			extraKeyThreadRoot: threadRoot,
			extraKeyChannelID:  payload.ChannelID,
		},
	}

	fileIDs := parseFileIDs(payload.FileIDsRaw)
	if len(fileIDs) > 0 {
		msg.MessageType = im.MessageTypeFile
		msg.FileKey = fileIDs[0]
		if len(fileIDs) > 1 {
			msg.Extra["file_ids"] = strings.Join(fileIDs, ",")
		}
	} else {
		msg.MessageType = im.MessageTypeText
	}

	return msg, nil
}

func parseOutgoingBody(contentType string, body []byte) (*outgoingPayload, error) {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))

	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		var p outgoingPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		return &p, nil

	case ct == "application/x-www-form-urlencoded" || ct == "":
		// Parse as form (Mattermost default for some configs).
		values, err := parseFormBody(body)
		if err != nil {
			return nil, err
		}
		p := &outgoingPayload{
			Token:     values.Get("token"),
			UserID:    values.Get("user_id"),
			UserName:  values.Get("user_name"),
			ChannelID: values.Get("channel_id"),
			PostID:    values.Get("post_id"),
			Text:      values.Get("text"),
			RootID:    values.Get("root_id"),
		}
		if f := values.Get("file_ids"); f != "" {
			p.FileIDsRaw = json.RawMessage(jsonArrayFromCSV(f))
		}
		return p, nil

	default:
		// Try JSON fallback for unknown types.
		var p outgoingPayload
		if err := json.Unmarshal(body, &p); err == nil && (p.Token != "" || p.ChannelID != "") {
			return &p, nil
		}
		return nil, fmt.Errorf("unsupported content-type: %s", contentType)
	}
}

func parseFileIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return splitFileIDs(s)
	}
	return nil
}

func splitFileIDs(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SendReply posts a reply message to the originating Mattermost channel or thread.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.Extra[extraKeyChannelID]
	}
	if channelID == "" {
		return fmt.Errorf("missing channel_id")
	}

	threadRoot := ""
	if incoming.Extra != nil {
		threadRoot = incoming.Extra[extraKeyThreadRoot]
	}

	_, err := a.client.CreatePost(ctx, channelID, threadRoot, reply.Content)
	return err
}

type mmStreamState struct {
	mu             sync.Mutex
	content        strings.Builder
	postID         string
	channel        string
	lastActivityAt time.Time
	ownerDone      <-chan struct{}
}

const (
	streamOrphanTTL      = 5 * time.Minute
	streamReaperInterval = 1 * time.Minute
)

var (
	mmStreamsMu   sync.Mutex
	mmStreams     = map[string]*mmStreamState{}
	reaperMu      sync.Mutex
	reaperRunning bool
	reaperStopCh  chan struct{}
	reaperDoneCh  chan struct{}
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
	mmStreamsMu.Lock()
	defer mmStreamsMu.Unlock()
	for id, state := range mmStreams {
		if state.ownerDone != nil {
			select {
			case <-state.ownerDone:
			default:
				state.lastActivityAt = now
				continue
			}
		}
		if state.lastActivityAt.Before(now.Add(-streamOrphanTTL)) {
			delete(mmStreams, id)
		}
	}
}

func lookupStream(streamID string) (*mmStreamState, bool) {
	mmStreamsMu.Lock()
	defer mmStreamsMu.Unlock()
	state, ok := mmStreams[streamID]
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

// StartStream creates a placeholder post in Mattermost and registers the stream state.
func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.Extra[extraKeyChannelID]
	}
	threadRoot := ""
	if incoming.Extra != nil {
		threadRoot = incoming.Extra[extraKeyThreadRoot]
	}

	postID, err := a.client.CreatePost(ctx, channelID, threadRoot, "正在思考...")
	if err != nil {
		return "", err
	}

	streamID := channelID + ":" + postID
	mmStreamsMu.Lock()
	mmStreams[streamID] = &mmStreamState{
		postID:         postID,
		channel:        channelID,
		lastActivityAt: time.Now(),
		ownerDone:      ctx.Done(),
	}
	mmStreamsMu.Unlock()

	logger.Infof(ctx, "[Mattermost] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

// UpdateStreamContent patches the Mattermost post with the latest accumulated stream text.
func (a *Adapter) UpdateStreamContent(
	ctx context.Context, _ *im.IncomingMessage, streamID string, fullContent string,
) error {
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
	postID := state.postID
	state.mu.Unlock()

	if err := a.client.PatchPostMessage(ctx, postID, fullContent); err != nil {
		logger.Warnf(ctx, "[Mattermost] Patch post failed: %v", err)
	}
	return nil
}

// FinalizeStream patches the Mattermost post with the final answer content.
func (a *Adapter) FinalizeStream(
	ctx context.Context, incoming *im.IncomingMessage, streamID string, finalContent string,
) error {
	return a.UpdateStreamContent(ctx, incoming, streamID, finalContent)
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

// EndStream applies the final accumulated text and removes the stream state.
func (a *Adapter) EndStream(ctx context.Context, _ *im.IncomingMessage, streamID string) error {
	mmStreamsMu.Lock()
	state, ok := mmStreams[streamID]
	delete(mmStreams, streamID)
	mmStreamsMu.Unlock()

	if !ok {
		return nil
	}

	state.mu.Lock()
	full := state.content.String()
	postID := state.postID
	state.mu.Unlock()

	if err := a.client.PatchPostMessage(ctx, postID, full); err != nil {
		logger.Warnf(ctx, "[Mattermost] EndStream patch failed: %v", err)
	}
	logger.Infof(ctx, "[Mattermost] Streaming ended: post_id=%s", postID)
	return nil
}

// DownloadFile fetches file metadata and opens a bounded download stream from Mattermost.
func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("file_key is required")
	}

	info, err := a.client.GetFileInfo(ctx, msg.FileKey)
	if err != nil {
		return nil, "", fmt.Errorf("file info: %w", err)
	}

	name := info.Name
	if name == "" {
		name = msg.FileName
	}
	if name == "" {
		name = msg.FileKey
	}

	rc, err := a.client.GetFileReader(ctx, msg.FileKey)
	if err != nil {
		return nil, "", err
	}

	return rc, name, nil
}
