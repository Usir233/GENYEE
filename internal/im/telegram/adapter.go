// Package telegram implements the Telegram Bot API IM adapter for GENYEE.
package telegram

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/logger"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// Compile-time checks.
var (
	_ im.Adapter        = (*Adapter)(nil)
	_ im.StreamSender   = (*Adapter)(nil)
	_ im.FileDownloader = (*Adapter)(nil)
)

// Adapter implements im.Adapter for Telegram Bot API.
type Adapter struct {
	botToken    string
	secretToken string // optional X-Telegram-Bot-Api-Secret-Token for webhook verification
	client      *LongConnClient
}

// NewWebhookAdapter creates a Telegram adapter for webhook mode.
func NewWebhookAdapter(botToken, secretToken string) *Adapter {
	startStreamReaper()
	return &Adapter{
		botToken:    botToken,
		secretToken: secretToken,
	}
}

// NewAdapter creates a Telegram adapter backed by a long-polling client.
func NewAdapter(client *LongConnClient, botToken string) *Adapter {
	startStreamReaper()
	return &Adapter{
		botToken: botToken,
		client:   client,
	}
}

// Platform returns the platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformTelegram
}

// HandleURLVerification reports that Telegram does not require URL verification challenges.
func (a *Adapter) HandleURLVerification(_ *gin.Context) bool {
	return false // Telegram does not require URL verification challenges.
}

// VerifyCallback validates the X-Telegram-Bot-Api-Secret-Token header on webhook callbacks.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.secretToken == "" {
		return fmt.Errorf("webhook verification secret is required")
	}
	token := c.GetHeader("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.secretToken)) != 1 {
		return fmt.Errorf("invalid secret token")
	}
	return nil
}

// telegramUpdate represents an incoming Telegram update (subset of fields).
type telegramUpdate struct {
	UpdateID int          `json:"update_id"`
	Message  *telegramMsg `json:"message"`
}

type telegramMsg struct {
	MessageID       int             `json:"message_id"`
	MessageThreadID int             `json:"message_thread_id"`
	From            *telegramUser   `json:"from"`
	Chat            telegramChat    `json:"chat"`
	Text            string          `json:"text"`
	Document        *telegramDoc    `json:"document"`
	Photo           []telegramPhoto `json:"photo"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // "private", "group", "supergroup", "channel"
}

type telegramDoc struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
}

type telegramPhoto struct {
	FileID   string `json:"file_id"`
	FileSize int    `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// ParseCallback parses a Telegram Bot API webhook update into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var update telegramUpdate
	if err := json.Unmarshal(bodyBytes, &update); err != nil {
		return nil, fmt.Errorf("parse update: %w", err)
	}

	return parseUpdate(&update), nil
}

func parseUpdate(update *telegramUpdate) *im.IncomingMessage {
	if update.Message == nil {
		return nil
	}
	return parseTelegramMessage(update.Message)
}

func parseTelegramMessage(msg *telegramMsg) *im.IncomingMessage {
	if msg == nil {
		return nil
	}

	chatType := im.ChatTypeDirect
	chatID := ""
	if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
		chatType = im.ChatTypeGroup
		chatID = fmt.Sprintf("%d", msg.Chat.ID)
	}

	userID := ""
	userName := ""
	if msg.From != nil {
		userID = fmt.Sprintf("%d", msg.From.ID)
		userName = strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
		if userName == "" {
			userName = msg.From.Username
		}
	}

	threadID := ""
	if msg.MessageThreadID != 0 {
		threadID = fmt.Sprintf("%d", msg.MessageThreadID)
	}

	incoming := &im.IncomingMessage{
		Platform:    im.PlatformTelegram,
		UserID:      userID,
		UserName:    userName,
		ChatID:      chatID,
		ChatType:    chatType,
		MessageID:   fmt.Sprintf("%d", msg.MessageID),
		ThreadID:    threadID,
		MessageType: im.MessageTypeText,
		Content:     msg.Text,
	}

	// For group messages, strip bot mention prefix (e.g., "/command@botname text" -> "text")
	if chatType == im.ChatTypeGroup {
		content := strings.TrimSpace(msg.Text)
		// Remove @bot mentions
		if idx := strings.Index(content, " "); idx > 0 && strings.Contains(content[:idx], "@") {
			content = strings.TrimSpace(content[idx+1:])
		}
		incoming.Content = content
	}

	// Handle document
	if msg.Document != nil {
		incoming.MessageType = im.MessageTypeFile
		incoming.FileKey = msg.Document.FileID
		incoming.FileName = msg.Document.FileName
		incoming.FileSize = msg.Document.FileSize
	}

	// Handle photo (use the largest photo)
	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		incoming.MessageType = im.MessageTypeImage
		incoming.FileKey = largest.FileID
		incoming.FileName = "photo.jpg"
		incoming.FileSize = int64(largest.FileSize)
	}

	return incoming
}

// resolveChatID returns ChatID if set, otherwise falls back to UserID (for direct messages).
func resolveChatID(incoming *im.IncomingMessage) string {
	if incoming.ChatID != "" {
		return incoming.ChatID
	}
	return incoming.UserID
}

// ── Send reply ──

// SendReply sends a Markdown text reply to the originating Telegram chat or thread.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	chatID := resolveChatID(incoming)
	text := im.FormatIMDisplayContent(reply.Content, im.StreamDisplayFinal)

	body := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if incoming.ThreadID != "" {
		if tid, err := strconv.Atoi(incoming.ThreadID); err == nil {
			body["message_thread_id"] = tid
		}
	}
	return a.callAPI(ctx, "sendMessage", body)
}

func (a *Adapter) editMessage(ctx context.Context, chatID, messageID, text, parseMode string) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": json.Number(messageID),
		"text":       text,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	return a.callAPI(ctx, "editMessageText", body)
}

// httpClient is a shared HTTP client with a reasonable timeout for Telegram API calls.
var httpClient = secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
	Timeout:      15 * time.Second,
	MaxRedirects: 5,
})

type redactedTelegramError struct {
	msg string
	err error
}

func (e *redactedTelegramError) Error() string { return e.msg }
func (e *redactedTelegramError) Unwrap() error { return e.err }

func redactTelegramErr(err error, botToken string) error {
	if err == nil || botToken == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, botToken) {
		return err
	}
	return &redactedTelegramError{
		msg: strings.ReplaceAll(msg, botToken, "[REDACTED]"),
		err: err,
	}
}

func validateTelegramFilePath(filePath string) error {
	if filePath == "" || strings.HasPrefix(filePath, "/") || strings.ContainsAny(filePath, "\\?#\r\n\x00") {
		return fmt.Errorf("invalid telegram file_path")
	}
	for _, seg := range strings.Split(filePath, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid telegram file_path")
		}
	}
	return nil
}

// callAPI calls the Telegram Bot API, discarding the result.
func (a *Adapter) callAPI(ctx context.Context, method string, body interface{}) error {
	return a.callAPIWithResult(ctx, method, body, nil)
}

// callAPIWithResult calls the Telegram Bot API and optionally decodes the result field.
func (a *Adapter) callAPIWithResult(ctx context.Context, method string, body interface{}, result interface{}) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", a.botToken, method)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", redactTelegramErr(err, a.botToken))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", redactTelegramErr(err, a.botToken))
	}
	defer func() { _ = resp.Body.Close() }()

	var apiResp struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := im.DecodeIMResponse(resp.Body, &apiResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !apiResp.OK {
		return fmt.Errorf("telegram API %s failed: %s", method, string(apiResp.Result))
	}
	if result != nil {
		if err := json.Unmarshal(apiResp.Result, result); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// ── StreamSender implementation (edit message in-place) ──

// minEditInterval is the minimum time between consecutive editMessageText calls
// to avoid hitting Telegram's rate limit (~30 msg/sec global, ~20 edit/min per chat).
const minEditInterval = 500 * time.Millisecond

type streamState struct {
	mu             sync.Mutex
	content        strings.Builder
	msgID          string // Telegram message ID of the "thinking" message
	chatID         string
	lastEdit       time.Time // last successful editMessageText timestamp
	lastActivityAt time.Time
	ownerDone      <-chan struct{}
}

const (
	streamOrphanTTL      = 5 * time.Minute
	streamReaperInterval = 1 * time.Minute
)

var (
	streamsMu     sync.Mutex
	streams       = map[string]*streamState{}
	reaperMu      sync.Mutex
	reaperRunning bool
	reaperStopCh  chan struct{}
	reaperDoneCh  chan struct{}
)

// startStreamReaper starts a background goroutine that periodically removes
// orphaned stream entries and restarts cleanly after StopStreamReaper.
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
	streamsMu.Lock()
	defer streamsMu.Unlock()
	for id, state := range streams {
		if state.ownerDone != nil {
			select {
			case <-state.ownerDone:
			default:
				state.lastActivityAt = now
				continue
			}
		}
		if state.lastActivityAt.Before(now.Add(-streamOrphanTTL)) {
			delete(streams, id)
		}
	}
}

func lookupStream(streamID string) (*streamState, bool) {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	state, ok := streams[streamID]
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

// StartStream posts a placeholder message and returns the stream identifier.
func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	chatID := resolveChatID(incoming)

	// Send initial "thinking" message
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    "正在思考...",
	}
	if incoming.ThreadID != "" {
		if tid, err := strconv.Atoi(incoming.ThreadID); err == nil {
			body["message_thread_id"] = tid
		}
	}
	var sentMsg struct {
		MessageID int `json:"message_id"`
	}
	if err := a.callAPIWithResult(ctx, "sendMessage", body, &sentMsg); err != nil {
		return "", fmt.Errorf("telegram start stream: %w", err)
	}

	msgID := fmt.Sprintf("%d", sentMsg.MessageID)
	streamID := fmt.Sprintf("%s:%s", chatID, msgID)

	streamsMu.Lock()
	streams[streamID] = &streamState{
		msgID:          msgID,
		chatID:         chatID,
		lastActivityAt: time.Now(),
		ownerDone:      ctx.Done(),
	}
	streamsMu.Unlock()

	logger.Infof(ctx, "[Telegram] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

// UpdateStreamContent edits the in-flight message with the latest accumulated text.
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
	if time.Since(state.lastEdit) < minEditInterval {
		state.content.Reset()
		state.content.WriteString(fullContent)
		state.mu.Unlock()
		return nil
	}
	state.content.Reset()
	state.content.WriteString(fullContent)
	chatID := state.chatID
	msgID := state.msgID
	state.lastEdit = time.Now()
	state.mu.Unlock()

	if err := a.editMessage(ctx, chatID, msgID, fullContent, ""); err != nil {
		logger.Warnf(ctx, "[Telegram] Failed to update stream content: %v", err)
	}
	return nil
}

// FinalizeStream edits the stream message with the final Markdown answer.
func (a *Adapter) FinalizeStream(
	ctx context.Context, _ *im.IncomingMessage, streamID string, finalContent string,
) error {
	state, ok := lookupStream(streamID)
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	state.content.Reset()
	state.content.WriteString(finalContent)
	chatID := state.chatID
	msgID := state.msgID
	state.mu.Unlock()

	if err := a.editMessage(ctx, chatID, msgID, finalContent, "Markdown"); err != nil {
		logger.Warnf(ctx, "[Telegram] Markdown finalize failed, retrying plain: %v", err)
		if retryErr := a.editMessage(ctx, chatID, msgID, finalContent, ""); retryErr != nil {
			logger.Warnf(ctx, "[Telegram] Failed to finalize stream: %v", retryErr)
			return retryErr
		}
	}
	return nil
}

// SendStreamChunk forwards incremental stream updates to UpdateStreamContent.
func (a *Adapter) SendStreamChunk(
	ctx context.Context,
	incoming *im.IncomingMessage,
	streamID string,
	content string,
) error {
	return a.UpdateStreamContent(ctx, incoming, streamID, content)
}

// EndStream removes the completed stream state from the in-memory registry.
func (a *Adapter) EndStream(ctx context.Context, _ *im.IncomingMessage, streamID string) error {
	streamsMu.Lock()
	_, ok := streams[streamID]
	delete(streams, streamID)
	streamsMu.Unlock()

	if !ok {
		return nil
	}

	logger.Infof(ctx, "[Telegram] Streaming ended: stream_id=%s", streamID)
	return nil
}

// ── FileDownloader implementation ──

// DownloadFile downloads a Telegram document or photo attachment with bounded size.
func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("file_key is required")
	}

	// Get file path via getFile API
	var fileInfo struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := a.callAPIWithResult(ctx, "getFile", map[string]string{"file_id": msg.FileKey}, &fileInfo); err != nil {
		return nil, "", fmt.Errorf("get file info: %w", err)
	}
	if err := validateTelegramFilePath(fileInfo.FilePath); err != nil {
		return nil, "", err
	}

	// Download the file
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", a.botToken, fileInfo.FilePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", redactTelegramErr(err, a.botToken))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download file: %w", redactTelegramErr(err, a.botToken))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return im.LimitAttachmentReadCloser(resp.Body), msg.FileName, nil
}
