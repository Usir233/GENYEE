package im

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// isDuplicate checks if a message has already been processed.
//
// Multi-instance mode (Redis available): uses Redis SetNX for cross-instance
// deduplication. If Redis fails, returns true (fail-closed) to prevent
// duplicate processing across instances — a dropped message can be retried
// by the user, but a duplicate LLM response wastes resources and confuses.
//
// Single-instance mode (no Redis): uses a local sync.Map, which is sufficient
// when only one instance receives messages.
func (s *Service) isDuplicate(ctx context.Context, messageID string) bool {
	if s.redis != nil {
		key := RedisKeyDedup + messageID
		ok, err := s.redis.SetNX(ctx, key, "1", dedupTTL).Result()
		if err == nil {
			return !ok // SetNX returns true when key was newly set (not a duplicate)
		}
		// Redis is configured but failed — fail-closed to avoid cross-instance
		// duplicate processing. The user can simply resend the message.
		logger.Errorf(ctx, "[IM] Redis dedup failed (fail-closed, message dropped): %v", err)
		return true
	}
	// Single-instance mode: local dedup is sufficient.
	_, loaded := s.processedMsgs.LoadOrStore(messageID, time.Now())
	return loaded
}

// HandleMessage processes an incoming IM message end-to-end using channel config.
func (s *Service) HandleMessage(ctx context.Context, msg *IncomingMessage, channelID string) error {
	// Dedup: skip if this message was already processed (IM platforms may retry).
	// Scope by channelID so sequential/predictable platform-local IDs cannot collide
	// across tenants or channels.
	if msg.MessageID != "" {
		dedupKey := msg.MessageID
		if channelID != "" {
			dedupKey = channelID + ":" + msg.MessageID
		}
		if s.isDuplicate(ctx, dedupKey) {
			logger.Infof(ctx, "[IM] Skipping duplicate message: channel=%s msgid=%s", channelID, msg.MessageID)
			return nil
		}
	}

	// Reject overly long messages to protect the QA pipeline
	contentRunes := []rune(msg.Content)
	if len(contentRunes) > maxContentLength {
		logger.Warnf(ctx, "[IM] Message too long (%d runes), truncating to %d", len(contentRunes), maxContentLength)
		msg.Content = string(contentRunes[:maxContentLength])
	}

	// Get channel config (moved before rate limit so we can reply to the user)
	adapter, channel, ok := s.GetChannelAdapter(channelID)
	if !ok {
		// Try loading from DB (channel might have been created after service start)
		ch, err := s.GetChannelByID(channelID)
		if err != nil {
			return fmt.Errorf("channel not found: %s", channelID)
		}
		if !ch.Enabled {
			return fmt.Errorf("channel is disabled: %s", channelID)
		}
		// Start it dynamically
		if err := s.StartChannel(ch); err != nil {
			return fmt.Errorf("start channel %s: %w", channelID, err)
		}
		adapter, channel, ok = s.GetChannelAdapter(channelID)
		if !ok {
			return fmt.Errorf("channel adapter not available after start: %s", channelID)
		}
	}

	// Resolve threadID for key building — only include in thread mode to avoid
	// leaking thread scope into user-mode rate limit / inflight keys.
	threadID := ""
	if channel.SessionMode == string(SessionModeThread) {
		threadID = msg.ThreadID
	}

	// Rate limit: enforce per-user sliding window to prevent abuse.
	// Slash-commands (/stop, /clear, etc.) bypass rate limiting so the user
	// always retains control over the bot even under heavy messaging.
	isCommand := s.cmdRegistry.IsRegistered(msg.Content)
	if !isCommand {
		rateLimitKey := makeUserKey(channelID, msg.UserID, msg.ChatID, threadID)
		if !s.rateLimiter.Allow(ctx, rateLimitKey, s.rateLimitMax) {
			logger.Warnf(ctx, "[IM] Rate limited: channel=%s user=%s chat=%s", channelID, msg.UserID, msg.ChatID)
			_ = adapter.SendReply(ctx, msg, &ReplyMessage{
				Content: "您的消息发送过于频繁，请稍后再试。",
				IsFinal: true,
			})
			return nil
		}
	}

	tenantID := channel.TenantID
	agentID := channel.AgentID

	logger.Infof(ctx, "[IM] HandleMessage: channel=%s platform=%s user=%s chat=%s msgtype=%s content_len=%d",
		channelID, msg.Platform, msg.UserID, msg.ChatID, msg.MessageType, len(msg.Content))
	logger.Debugf(ctx, "[IM] HandleMessage detail: msgid=%s raw_msgtype=%s filekey=%s filename=%s",
		msg.MessageID, msg.Extra["raw_msgtype"], msg.FileKey, msg.FileName)

	// ── File/Image message handling ──
	// File messages use the normal QA path as well.  A configured knowledge base
	// only adds a best-effort, asynchronous save; it must never replace or block
	// the reply to this message.  With no configured knowledge base, simply skip
	// the save rather than rejecting the message.
	if msg.MessageType == MessageTypeFile || msg.MessageType == MessageTypeImage {
		msg.Content = fileMessageQAContent(msg)
	}

	// Never send an empty query into rewrite/intent classification. Some IM
	// platforms deliver unsupported rich message shapes with no normalized text;
	// allowing those through makes the model infer an unrelated intent from an
	// empty query (for example, rewriting it as a greeting).
	if hint, empty := emptyIncomingMessageReply(msg); empty {
		logger.Infof(ctx, "[IM] Skipping QA for message without content: type=%s raw_type=%s",
			msg.MessageType, msg.Extra["raw_msgtype"])
		if err := adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: hint,
			IsFinal: true,
		}); err != nil {
			logger.Warnf(ctx, "[IM] Failed to send empty-message hint reply: %v", err)
		}
		return nil
	}

	// 1. Get tenant
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	sessionCtx := context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	sessionCtx = withIMIdentity(sessionCtx, channel, msg)

	// 2. Resolve or create a GENYEE session
	channelSession, err := s.resolveSession(sessionCtx, msg, tenantID, agentID, channelID, channel.SessionMode)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}

	// 3. Resolve custom agent (optional)
	var customAgent *types.CustomAgent
	if agentID != "" {
		agent, err := s.agentService.GetAgentByID(sessionCtx, agentID)
		if err != nil {
			logger.Warnf(ctx, "[IM] Failed to get agent %s: %v, using default", agentID, err)
		} else {
			customAgent = agent
		}
	}

	// ── Slash-command dispatch ──
	// Commands are handled before the QA pipeline so they respond instantly.
	if cmd, args, ok := s.cmdRegistry.Parse(msg.Content); ok {
		return s.handleCommand(sessionCtx, cmd, args, msg, adapter, channel, channelSession, customAgent)
	}
	// Unrecognised slash-word: show help hint instead of sending to QA.
	if LooksLikeCommand(msg.Content) {
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "未知指令，发送 `/help` 查看所有可用指令。",
			IsFinal: true,
		})
		return nil
	}

	// 4. Get the GENYEE session
	session, err := s.sessionService.GetSession(sessionCtx, channelSession.SessionID)
	if err != nil {
		// The underlying session may have been deleted from the UI while the
		// ChannelSession mapping still exists (GORM soft-delete does not trigger
		// SQL ON DELETE CASCADE). Recover by soft-deleting the stale mapping and
		// re-creating a fresh session so the IM bot doesn't become permanently
		// unresponsive. (fixes #1046, #1499)
		if isSessionNotFound(err) {
			logger.Warnf(ctx, "[IM] Session %s not found (deleted?), recycling stale channel session %s",
				channelSession.SessionID, channelSession.ID)
			if delErr := s.db.WithContext(sessionCtx).
				Delete(&ChannelSession{}, "id = ?", channelSession.ID).Error; delErr != nil {
				logger.Warnf(ctx, "[IM] Failed to delete stale channel session %s: %v", channelSession.ID, delErr)
			}
			channelSession, err = s.resolveSession(sessionCtx, msg, tenantID, agentID, channelID, channel.SessionMode)
			if err != nil {
				return fmt.Errorf("resolve session (retry): %w", err)
			}
			session, err = s.sessionService.GetSession(sessionCtx, channelSession.SessionID)
			if err != nil {
				return fmt.Errorf("get session (retry): %w", err)
			}
		} else {
			return fmt.Errorf("get session: %w", err)
		}
	}

	// Title an untitled IM session from its first text message, like web chats.
	// GenerateTitleAsync self-guards on a non-empty title and persists to the DB;
	// nil eventBus is fine (IM has no live stream — the sidebar reloads it).
	if session.Title == "" && strings.TrimSpace(msg.Content) != "" {
		// Copy the session: the async title goroutine writes Title while the QA
		// worker below shares the same *session.
		sessionForTitle := *session
		titleModelID := ""
		if customAgent != nil && customAgent.Config.ModelID != "" {
			titleModelID = customAgent.Config.ModelID
		}
		s.sessionService.GenerateTitleAsync(sessionCtx, &sessionForTitle, msg.Content, titleModelID, nil)
	}

	s.persistIMLastRequestState(sessionCtx, session.ID, agentID, customAgent, nil)

	// 5. Enqueue the QA request into the bounded worker pool.
	// The worker pool controls LLM concurrency and provides backpressure.
	qaCtx, qaCancel := context.WithCancel(sessionCtx)
	userKey := makeUserKey(channelID, msg.UserID, msg.ChatID, threadID)

	req := &qaRequest{
		ctx:       qaCtx,
		cancel:    qaCancel,
		msg:       msg,
		session:   session,
		agent:     customAgent,
		adapter:   adapter,
		channel:   channel,
		channelID: channelID,
		tenant:    tenant,
		userKey:   userKey,
	}

	pos, enqueueErr := s.qaQueue.Enqueue(req)
	if enqueueErr != nil {
		qaCancel()
		logger.Warnf(ctx, "[IM] Queue rejected: user=%s reason=%v", msg.UserID, enqueueErr)
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "当前排队人数较多，请稍后再试。",
			IsFinal: true,
		})
		return nil
	}

	if pos > 0 {
		logger.Infof(ctx, "[IM] Enqueued: user=%s pos=%d depth=%d", msg.UserID, pos, s.qaQueue.Metrics().Depth)
		// In multi-instance mode the local queue position does not reflect global
		// depth, so use a generic "queued" hint instead of an exact number.
		queueMsg := fmt.Sprintf("收到，前面还有 %d 条消息在处理，请稍候 ⏳", pos)
		if s.redis != nil {
			queueMsg = "收到，当前排队中，请稍候 ⏳"
		}
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: queueMsg,
			IsFinal: true,
		})
	} else {
		logger.Infof(ctx, "[IM] Enqueued: user=%s pos=0 (immediate)", msg.UserID)
	}

	return nil
}

func emptyIncomingMessageReply(msg *IncomingMessage) (string, bool) {
	if msg == nil || strings.TrimSpace(msg.Content) != "" {
		return "", false
	}
	// Image/file events are allowed to arrive without a caption. Do not depend
	// on fileMessageQAContent having already filled Content — a later reorder
	// of HandleMessage must not reject attachments.
	hasAttachment := msg.MessageType == MessageTypeFile ||
		msg.MessageType == MessageTypeImage ||
		strings.TrimSpace(msg.FileKey) != ""
	if hasAttachment {
		return "", false
	}
	rawType := ""
	if msg.Extra != nil {
		rawType = strings.ToLower(strings.TrimSpace(msg.Extra["raw_msgtype"]))
	}
	switch rawType {
	case "audio":
		return "未能识别这条语音中的文字内容。请改用纯文本发送，或再说一遍。", true
	case "video":
		return "暂不支持视频消息。请改用纯文本发送；图片或文件请单独发送。", true
	default:
		return "未能识别这条消息中的文字内容。请改用纯文本发送；图片或文件请单独发送。", true
	}
}

func (s *Service) persistIMLastRequestState(
	ctx context.Context,
	sessionID, agentID string,
	customAgent *types.CustomAgent,
	kbIDs []string,
) {
	state := buildIMLastRequestState(agentID, customAgent, kbIDs)
	persistCtx := logger.CloneContext(context.WithoutCancel(ctx))
	if err := s.sessionService.UpdateSessionLastRequestState(persistCtx, sessionID, state); err != nil {
		logger.Warnf(ctx, "[IM] persist last_request_state failed for session %s: %v", sessionID, err)
	}
}

// executeQARequest is the worker handler that runs the QA pipeline for a queued request.
// It is called by qaQueue workers and must not block indefinitely.
func (s *Service) executeQARequest(req *qaRequest) {
	ctx := req.ctx
	defer req.cancel()

	// Track in-flight request so /stop can cancel it.
	entry := &inflightEntry{cancel: req.cancel}
	s.inflight.Store(req.userKey, entry)
	defer s.inflight.Delete(req.userKey)

	// Check if a pre-execution /stop was issued while this request was queued.
	if s.checkAndClearStopMarker(ctx, req.userKey) {
		logger.Infof(ctx, "[IM] Request cancelled by remote /stop before execution: %s", req.userKey)
		return
	}

	// NOTE: StreamManager-based stop detection is started inside handleMessageStream /
	// runQA after the assistant message is created (that's when we have the
	// sessionID + messageID needed to poll StreamManager).

	// kbIDs is left empty so the QA pipeline resolves them from the agent config.
	var kbIDs []string
	attachments, imageURLs, downloaded, err := s.prepareIMAttachments(ctx, req.msg, req.adapter)
	if err != nil {
		logger.Warnf(ctx, "[IM] attachment preparation failed: %v", err)
		errReply := &ReplyMessage{Content: "❌ 无法读取此附件，请重试或改用文字描述。", IsFinal: true}
		if sendErr := req.adapter.SendReply(ctx, req.msg, errReply); sendErr != nil {
			logger.Warnf(ctx, "[IM] Failed to send attachment error reply: %v", sendErr)
		}
		return
	}
	if req.channel.KnowledgeBaseID != "" && downloaded != nil {
		go s.processDownloadedFileToKnowledgeBase(
			context.WithoutCancel(ctx), req.channel, downloaded,
		)
	}

	// Determine output mode from channel config.
	streamDisabled := req.channel.OutputMode == "full"

	if streamDisabled {
		if progressSender, ok := req.adapter.(FullOutputProgressSender); ok &&
			progressSender.SupportsFullOutputProgress() {
			// Full output still starts the platform stream so users immediately see
			// its thinking placeholder. No intermediate reasoning/tool content is
			// sent; the placeholder is replaced only after QA completes.
			if err := s.handleMessageFullOutput(
				ctx, req.msg, req.session, req.agent, kbIDs, attachments, imageURLs,
				progressSender, req.adapter, req.userKey, req.tenant,
			); err != nil {
				logger.Errorf(ctx, "[IM] Full-output QA failed: %v", err)
			}
			return
		}
	} else if streamer, ok := req.adapter.(StreamSender); ok {
		// Stream mode sends intermediate reasoning and answer updates.
		if err := s.handleMessageStream(
			ctx, req.msg, req.session, req.agent, kbIDs, attachments, imageURLs,
			streamer, req.adapter, req.userKey, req.tenant,
		); err != nil {
			logger.Errorf(ctx, "[IM] Stream QA failed: %v", err)
		}
		return
	}

	// Non-streaming fallback: collect full answer then send.
	answer, err := s.runQA(
		ctx, req.session, req.msg.Content, req.agent, kbIDs, attachments, imageURLs, req.userKey, req.msg.Quote,
	)
	if err != nil {
		logger.Errorf(ctx, "[IM] QA failed: %v, sending fallback reply", err)
		answer = imQAFailureReply(err)
	}

	outCtx := imOutboundContext(ctx)
	reply := &ReplyMessage{
		Content: formatIMOutboundAnswerOrFallback(outCtx, answer,
			s.newOutboundFileServiceResolver(req.tenant, req.agent, kbIDs)),
		IsFinal: true,
	}
	if err := req.adapter.SendReply(outCtx, req.msg, reply); err != nil {
		logger.Errorf(ctx, "[IM] Send reply failed: %v", err)
		return
	}

	logger.Infof(ctx, "[IM] Reply sent: channel=%s platform=%s user=%s answer_len=%d",
		req.channelID, req.msg.Platform, req.msg.UserID, len(answer))
}

// handleMessageFullOutput keeps the channel's full-output semantics while
// providing immediate progress feedback on adapters that support replaceable
// stream messages. StartStream creates the platform placeholder; no intermediate
// updates are sent, and the final answer replaces it exactly once.
func (s *Service) handleMessageFullOutput(
	ctx context.Context,
	msg *IncomingMessage,
	session *types.Session,
	customAgent *types.CustomAgent,
	kbIDs []string,
	attachments types.MessageAttachments,
	imageURLs []string,
	streamer FullOutputProgressSender,
	adapter Adapter,
	userKey string,
	tenant *types.Tenant,
) error {
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		logger.Warnf(ctx, "[IM] StartStream failed for full output, falling back to plain reply: %v", err)
		return s.fallbackNonStream(
			ctx, msg, session, customAgent, kbIDs, attachments, imageURLs, adapter, userKey, tenant,
		)
	}

	answer, qaErr := s.runQA(
		ctx, session, msg.Content, customAgent, kbIDs, attachments, imageURLs, userKey, msg.Quote,
	)
	if qaErr != nil {
		logger.Errorf(ctx, "[IM] Full-output QA failed: %v, sending fallback reply", qaErr)
		answer = imQAFailureReply(qaErr)
	}

	// QA (and /stop) may have cancelled ctx. Platform updates must still run so
	// the thinking card is replaced instead of hanging forever.
	outCtx := imOutboundContext(ctx)
	finalContent := formatIMOutboundAnswerOrFallback(outCtx, answer,
		s.newOutboundFileServiceResolver(tenant, customAgent, kbIDs))

	finalizeErr := streamer.FinalizeStream(outCtx, msg, streamID, finalContent)
	if finalizeErr != nil {
		logger.Warnf(ctx, "[IM] FinalizeStream failed for full output: %v", finalizeErr)
	}
	endErr := streamer.EndStream(outCtx, msg, streamID)
	if endErr != nil {
		logger.Warnf(ctx, "[IM] EndStream failed for full output: %v", endErr)
	}

	// If the placeholder could not be replaced, send a plain final reply so the
	// user still receives the answer instead of being left on "thinking".
	var fallbackErr error
	if finalizeErr != nil {
		fallbackErr = adapter.SendReply(outCtx, msg, &ReplyMessage{Content: finalContent, IsFinal: true})
		if fallbackErr != nil {
			logger.Errorf(ctx, "[IM] Plain reply fallback after full-output finalize failure failed: %v", fallbackErr)
		}
	}

	if finalizeErr == nil || fallbackErr == nil {
		logger.Infof(
			ctx, "[IM] Full-output reply sent: platform=%s user=%s answer_len=%d",
			msg.Platform, msg.UserID, len(answer),
		)
		return nil
	}
	return errors.Join(finalizeErr, endErr, fallbackErr)
}

// isSessionNotFound reports whether err indicates the underlying GENYEE
// session no longer exists. The session repository translates GORM's
// ErrRecordNotFound into apperrors.ErrSessionNotFound, so the application
// sentinel is what GetSession returns today; the GORM check is kept as a
// safety net in case a future repository revert bypasses the translation.
func isSessionNotFound(err error) bool {
	return errors.Is(err, apperrors.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// ── Agent tool call progress formatting ──────────────────────────────
// These helpers format tool-call / tool-result events as Markdown text
// that is injected into the streaming reply so IM users can see the
// agent's reasoning process in real-time.
// ─────────────────────────────────────────────────────────────────────

// internalToolNames lists tools whose execution should NOT be displayed in IM
// messages because they are internal reasoning aids (thinking, planning) rather
// than user-facing actions.
var internalToolNames = map[string]bool{
	"thinking":   true,
	"todo_write": true,
}

// isToolVisibleToUser returns true if the tool's execution progress should be
// displayed to the IM user. Internal reasoning tools (thinking, planning) are
// hidden.
func isToolVisibleToUser(toolName string) bool {
	return !internalToolNames[toolName]
}

// briefToolSummary extracts a short human-readable summary from tool output.
// Returns empty string if no suitable summary can be extracted.
func briefToolSummary(output string) string {
	const maxRunes = 40
	if output == "" {
		return ""
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	// Skip structured data (JSON, XML, etc.)
	if output[0] == '{' || output[0] == '[' || output[0] == '<' {
		return ""
	}
	// Take first non-empty line
	if idx := strings.IndexByte(output, '\n'); idx >= 0 {
		output = strings.TrimSpace(output[:idx])
	}
	if output == "" {
		return ""
	}
	runes := []rune(output)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return output
}

// handleMessageStream runs the QA pipeline and streams answer chunks to the IM platform
// in real-time via the StreamSender interface. Chunks are batched at streamFlushInterval
// to avoid API rate-limiting.
func (s *Service) handleMessageStream(
	ctx context.Context,
	msg *IncomingMessage,
	session *types.Session,
	customAgent *types.CustomAgent,
	kbIDs []string,
	attachments types.MessageAttachments,
	imageURLs []string,
	streamer StreamSender,
	adapter Adapter,
	userKey string,
	tenant *types.Tenant,
) error {
	// Start the stream on the IM platform (e.g., create Feishu streaming card)
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		logger.Warnf(ctx, "[IM] StartStream failed, falling back to non-streaming: %v", err)
		return s.fallbackNonStream(
			ctx, msg, session, customAgent, kbIDs, attachments, imageURLs, adapter, userKey, tenant,
		)
	}

	// Prepare the QA pipeline
	// No total deadline: each agent round has its own LLMCallTimeout (default 120s).
	// A hard pipeline deadline would kill multi-round agent reasoning prematurely.
	qaCtx, qaCancel := context.WithCancel(ctx)
	defer qaCancel()

	useAgent := customAgent != nil && customAgent.IsAgentMode()
	eventBus := event.NewEventBus()

	var (
		bufMu           sync.Mutex
		reasoningInner  streamSection   // quick QA: model reasoning_content
		agentInner      streamSection   // agent: retracted text + thoughts
		agentLiveAnswer strings.Builder // agent: optimistic answer before tool retract
		answerOuter     strings.Builder // final answer for display persistence
		answerBuilder   strings.Builder // answer persisted to DB
		qaErr           error
		done            = make(chan struct{})
		completeDone    = make(chan struct{})
		closeOnce       sync.Once
		completeOnce    sync.Once
		agentDone       bool
		assistantMsg    *types.Message

		seenToolCalls = make(map[string]bool)
		agentToolIdx  = make(map[string]int)
		pipelineIdx   = make(map[string]int)

		agentToolSteps    []IMToolStep
		pipelineToolSteps []IMToolStep

		agentCompleteFinalAnswer string
		streamedAny              bool

		// mcpAuthServices collects OAuth services that need out-of-band
		// authorization (IM cannot resolve the in-conversation prompt).
		mcpAuthServices []imMCPAuthService
		mcpAuthSeen     = make(map[string]bool)
	)
	closeDone := func() { closeOnce.Do(func() { close(done) }) }
	closeComplete := func() { completeOnce.Do(func() { close(completeDone) }) }

	agentWrite := func(s string) {
		if s == "" {
			return
		}
		agentInner.write(s)
		streamedAny = true
	}
	reasoningWrite := func(s string) {
		if s == "" {
			return
		}
		reasoningInner.write(s)
		streamedAny = true
	}

	// retractAgentLiveAnswer moves the optimistic answer into the think block (Web: superseded preamble).
	retractAgentLiveAnswer := func() {
		if agentLiveAnswer.Len() == 0 {
			return
		}
		if agentInner.text.Len() > 0 {
			agentInner.ensureNewlineBefore()
		}
		agentInner.write(agentLiveAnswer.String())
		agentLiveAnswer.Reset()
	}

	getStreamParts := func() IMStreamParts {
		mode := IMStreamModeQuickQA
		if useAgent {
			mode = IMStreamModeAgent
		}
		return IMStreamParts{
			Mode:              mode,
			PipelineToolSteps: pipelineToolSteps,
			ReasoningInner:    reasoningInner.text.String(),
			AgentInner:        agentInner.text.String(),
			AgentToolSteps:    agentToolSteps,
			LiveAnswer:        agentLiveAnswer.String(),
			Answer:            answerOuter.String(),
		}
	}

	// Subscribe to answer chunks.
	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}

		bufMu.Lock()
		if useAgent && !agentDone {
			if data.Content != "" {
				agentLiveAnswer.WriteString(data.Content)
				streamedAny = true
			}
		} else {
			answerOuter.WriteString(data.Content)
			answerBuilder.WriteString(data.Content)
			streamedAny = true
		}
		bufMu.Unlock()

		if data.Done {
			closeDone()
		}
		return nil
	})

	eventBus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		logger.Errorf(ctx, "[IM] QA stream error: %s", data.Error)
		bufMu.Lock()
		qaErr = fmt.Errorf("QA pipeline error: %s", data.Error)
		bufMu.Unlock()
		closeDone()
		closeComplete()
		return nil
	})

	eventBus.On(event.EventAgentReferences, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentReferencesData)
		if !ok {
			return nil
		}
		bufMu.Lock()
		if assistantMsg != nil {
			refs := []*types.SearchResult(assistantMsg.KnowledgeReferences)
			collectIMKnowledgeReferences(&refs, data.References)
			assistantMsg.KnowledgeReferences = types.References(refs)
		}
		bufMu.Unlock()
		return nil
	})

	eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		bufMu.Lock()
		agentDone = true
		agentCompleteFinalAnswer = data.FinalAnswer
		applyIMCompleteDataToMessage(assistantMsg, data)
		mergeIMAgentAnswerBuffers(&answerBuilder, &answerOuter, &agentLiveAnswer, data.FinalAnswer)
		bufMu.Unlock()
		closeComplete()
		// Execute can emit EventError after Complete. The AgentQA return path
		// closes done after those errors have been collected for finalization.
		return nil
	})

	// Subscribe to agent thought events — stream thinking content into <think> block
	eventBus.On(event.EventAgentThought, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentThoughtData)
		if !ok {
			return nil
		}
		bufMu.Lock()
		if useAgent {
			agentWrite(data.Content)
		} else {
			reasoningWrite(data.Content)
		}
		bufMu.Unlock()
		return nil
	})

	// Subscribe to agent tool call events — write status line into the think block.
	eventBus.On(event.EventAgentToolCall, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolCallData)
		if !ok {
			return nil
		}
		if !isToolVisibleToUser(data.ToolName) {
			return nil
		}
		bufMu.Lock()
		if seenToolCalls[data.ToolCallID] {
			if useAgent {
				upsertIMToolStep(&agentToolSteps, agentToolIdx, data.ToolCallID, func(step *IMToolStep) {
					if data.Arguments != nil {
						step.Arguments = data.Arguments
					}
				})
				streamedAny = true
			}
			bufMu.Unlock()
			return nil
		}
		seenToolCalls[data.ToolCallID] = true
		if !useAgent && IsRAGPipelineToolName(data.ToolName) {
			upsertIMToolStep(&pipelineToolSteps, pipelineIdx, data.ToolCallID, func(step *IMToolStep) {
				step.ToolName = data.ToolName
				step.Pending = true
				step.Arguments = data.Arguments
			})
			streamedAny = true
		} else if useAgent {
			retractAgentLiveAnswer()
			upsertIMToolStep(&agentToolSteps, agentToolIdx, data.ToolCallID, func(step *IMToolStep) {
				step.ToolName = data.ToolName
				step.Pending = true
				step.Arguments = data.Arguments
			})
			streamedAny = true
		}
		bufMu.Unlock()
		logger.Debugf(ctx, "[IM] Tool call streamed to IM: tool=%s id=%s", data.ToolName, data.ToolCallID)
		return nil
	})

	// Subscribe to agent tool result events
	eventBus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolResultData)
		if !ok {
			return nil
		}
		if !isToolVisibleToUser(data.ToolName) {
			return nil
		}
		bufMu.Lock()
		if !useAgent && IsRAGPipelineToolName(data.ToolName) {
			upsertIMToolStep(&pipelineToolSteps, pipelineIdx, data.ToolCallID, func(step *IMToolStep) {
				step.ToolName = data.ToolName
				step.Pending = false
				step.Success = data.Success
				step.Data = data.Data
				step.Output = data.Output
			})
			streamedAny = true
		} else if useAgent {
			upsertIMToolStep(&agentToolSteps, agentToolIdx, data.ToolCallID, func(step *IMToolStep) {
				step.ToolName = data.ToolName
				step.Pending = false
				step.Success = data.Success
				step.Data = data.Data
				step.Output = data.Output
			})
			streamedAny = true
		}
		bufMu.Unlock()
		logger.Debugf(ctx, "[IM] Tool result streamed to IM: tool=%s success=%v duration=%dms",
			data.ToolName, data.Success, data.Duration)
		return nil
	})

	// An OAuth-enabled MCP service the IM user has not authorized yet. IM cannot
	// resolve the in-conversation prompt, so collect the service name and append
	// an authorization notice to the final reply (deduped per service).
	eventBus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.MCPOAuthRequiredData)
		if !ok {
			return nil
		}
		bufMu.Lock()
		if !mcpAuthSeen[data.ServiceID] {
			mcpAuthSeen[data.ServiceID] = true
			mcpAuthServices = append(mcpAuthServices, imMCPAuthService{ID: data.ServiceID, Name: data.ServiceName})
		}
		bufMu.Unlock()
		return nil
	})

	// Determine whether to use agent mode (already set above for event handlers).
	requestID := uuid.New().String()

	// Create user message
	userPayload := createIMUserMessagePayload(session.ID, msg.Content, requestID, attachments)
	userMsg, err := s.messageService.CreateMessage(qaCtx, userPayload)
	if err != nil {
		return fmt.Errorf("create user message: %w", err)
	}

	// Create placeholder assistant message
	assistantMsg, err = s.messageService.CreateMessage(qaCtx, createIMAssistantMessagePayload(session.ID, requestID))
	if err != nil {
		return fmt.Errorf("create assistant message: %w", err)
	}

	// Register inflight mapping so cross-instance /stop can find this request
	// and write a stop event to StreamManager.
	if raw, ok := s.inflight.Load(userKey); ok {
		e := raw.(*inflightEntry)
		e.sessionID = session.ID
		e.assistantMessageID = assistantMsg.ID
	}
	s.storeInflightMapping(qaCtx, userKey, session.ID, assistantMsg.ID)
	defer s.clearInflightMapping(ctx, userKey)

	// Start StreamManager stop watcher — mirrors web's handleAgentEventsForSSE
	// stop detection. Cancels qaCtx if a stop event is written by any instance.
	go s.watchStreamManagerStop(qaCtx, session.ID, assistantMsg.ID, qaCancel)

	// Run QA async
	go func() {
		// AgentQA returns after all synchronous events, including errors emitted
		// after EventAgentComplete. KnowledgeQA starts an asynchronous stream,
		// so its return must not end the reply.
		if useAgent {
			defer closeDone()
			defer closeComplete()
		}
		var err error
		req := buildIMQARequest(
			session, msg.Content, assistantMsg.ID, userMsg.ID, customAgent, kbIDs, msg.Quote, attachments,
		)
		req.ImageURLs = imageURLs
		if req.QuotedContext != "" {
			logger.Debugf(qaCtx, "[IM] QuotedContext set: length=%d", len(req.QuotedContext))
		}
		if useAgent {
			err = s.sessionService.AgentQA(qaCtx, req, eventBus)
		} else {
			err = s.sessionService.KnowledgeQA(qaCtx, req, eventBus)
		}
		if err != nil {
			logger.Errorf(ctx, "[IM] QA stream execution error: %v", err)
			bufMu.Lock()
			qaErr = fmt.Errorf("QA execution error: %w", err)
			bufMu.Unlock()
			closeDone()
			closeComplete()
		}
	}()

	// Flush loop: periodically send buffered content to the IM platform.
	// A holdback mechanism prevents flushing incomplete provider:// URLs or
	// XML tags that straddle a chunk boundary (see holdbackCutoff).
	ticker := time.NewTicker(streamFlushInterval)
	defer ticker.Stop()
	fileResolver := s.newOutboundFileServiceResolver(tenant, customAgent, kbIDs)

	flush := func() {
		bufMu.Lock()
		parts := getStreamParts()
		agentRunning := useAgent && !agentDone
		bufMu.Unlock()

		displaySource := FormatIMIntermediateFromParts(parts, agentRunning)
		if displaySource == "" {
			return
		}

		if cut := holdbackCutoff(displaySource); cut < len(displaySource) {
			displaySource = displaySource[:cut]
		}

		display := cleanIMContent(ctx, displaySource, fileResolver)
		if err := streamer.UpdateStreamContent(ctx, msg, streamID, display); err != nil {
			logger.Warnf(ctx, "[IM] UpdateStreamContent failed: %v", err)
		}
	}

loop:
	for {
		select {
		case <-ticker.C:
			flush()
		case <-done:
			break loop
		case <-qaCtx.Done():
			break loop
		}
	}

	if useAgent {
		waitForIMAgentComplete(qaCtx, completeDone, session.ID)
	}

	bufMu.Lock()
	parts := getStreamParts()
	resolvedAnswer := pickIMStoredAnswer(
		answerBuilder.String(),
		answerOuter.String(),
		agentLiveAnswer.String(),
		agentCompleteFinalAnswer,
	)
	if parts.Answer == "" {
		parts.Answer = resolvedAnswer
	}
	answer := resolvedAnswer
	finalErr := qaErr
	noVisibleContent := !streamedAny && strings.TrimSpace(resolvedAnswer) == ""
	authServices := append([]imMCPAuthService(nil), mcpAuthServices...)
	bufMu.Unlock()

	outCtx := imOutboundContext(ctx)
	finalDisplay := cleanIMContent(outCtx, FormatIMFinalFromParts(parts), fileResolver)
	if noVisibleContent || finalDisplay == "" {
		fallback := imNoAnswerFallback
		if finalErr != nil {
			fallback = imQAFailureReply(finalErr)
		}
		finalDisplay = fallback
		if answer == "" {
			answer = fallback
		}
	}
	if notice := s.buildIMMCPAuthNotice(ctx, authServices); notice != "" {
		finalDisplay = appendIMAuthNotice(finalDisplay, notice)
		answer = appendIMAuthNotice(answer, notice)
	}

	finalizeErr := streamer.FinalizeStream(outCtx, msg, streamID, finalDisplay)
	if finalizeErr != nil {
		logger.Warnf(ctx, "[IM] FinalizeStream failed: %v", finalizeErr)
	}

	// End the stream
	endErr := streamer.EndStream(outCtx, msg, streamID)
	if endErr != nil {
		logger.Warnf(ctx, "[IM] EndStream failed: %v", endErr)
	}

	// Match full-output delivery: a failed card replacement must not strand the
	// answer in the database while the user only sees intermediate progress.
	var fallbackErr error
	if finalizeErr != nil {
		fallbackErr = adapter.SendReply(outCtx, msg, &ReplyMessage{Content: finalDisplay, IsFinal: true})
		if fallbackErr != nil {
			logger.Errorf(ctx, "[IM] Plain reply fallback after stream finalize failure failed: %v", fallbackErr)
		}
	}

	if answer == "" {
		answer = imNoAnswerFallback
	}

	assistantMsg.Content = answer
	assistantMsg.IsCompleted = true
	if err := s.messageService.UpdateMessage(outCtx, assistantMsg); err != nil {
		logger.Warnf(ctx, "[IM] Failed to update assistant message: %v", err)
	}

	if finalizeErr != nil && fallbackErr != nil {
		return errors.Join(finalizeErr, endErr, fallbackErr)
	}
	logger.Infof(
		ctx,
		"[IM] Stream reply sent: platform=%s user=%s answer_len=%d",
		msg.Platform, msg.UserID, len(answer),
	)
	return endErr
}

// fallbackNonStream is used when streaming initialization fails.
func (s *Service) fallbackNonStream(
	ctx context.Context,
	msg *IncomingMessage,
	session *types.Session,
	customAgent *types.CustomAgent,
	kbIDs []string,
	attachments types.MessageAttachments,
	imageURLs []string,
	adapter Adapter,
	userKey string,
	tenant *types.Tenant,
) error {
	answer, err := s.runQA(ctx, session, msg.Content, customAgent, kbIDs, attachments, imageURLs, userKey, msg.Quote)
	if err != nil {
		logger.Errorf(ctx, "[IM] QA fallback failed: %v", err)
		answer = imQAFailureReply(err)
	}

	outCtx := imOutboundContext(ctx)
	return adapter.SendReply(outCtx, msg, &ReplyMessage{
		Content: formatIMOutboundAnswerOrFallback(outCtx, answer,
			s.newOutboundFileServiceResolver(tenant, customAgent, kbIDs)),
		IsFinal: true,
	})
}

// runQA executes the GENYEE QA pipeline and returns the full answer text.
func (s *Service) runQA(
	ctx context.Context,
	session *types.Session,
	query string,
	customAgent *types.CustomAgent,
	kbIDs []string,
	attachments types.MessageAttachments,
	imageURLs []string,
	userKey string,
	quote *QuotedMessage,
) (string, error) {
	// Cancellable context (no hard deadline): each agent round has its own
	// LLMCallTimeout. The context can still be cancelled by /stop.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eventBus := event.NewEventBus()

	// Thread-safe answer collection
	var answerMu sync.Mutex
	var answerBuilder strings.Builder
	var qaErr error
	var mcpAuthServices []imMCPAuthService
	mcpAuthSeen := make(map[string]bool)
	done := make(chan struct{})
	completeDone := make(chan struct{})
	var closeOnce sync.Once
	var completeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }
	closeComplete := func() { completeOnce.Do(func() { close(completeDone) }) }

	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		answerMu.Lock()
		answerBuilder.WriteString(data.Content)
		answerMu.Unlock()
		if data.Done {
			closeDone()
		}
		return nil
	})

	eventBus.On(event.EventError, func(ctx context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		logger.Errorf(ctx, "[IM] QA error: %s", data.Error)
		answerMu.Lock()
		qaErr = fmt.Errorf("QA pipeline error: %s", data.Error)
		answerMu.Unlock()
		closeDone()
		closeComplete()
		return nil
	})

	// Collect OAuth services that need out-of-band authorization (IM cannot
	// resolve the in-conversation prompt); appended to the answer below.
	eventBus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.MCPOAuthRequiredData)
		if !ok {
			return nil
		}
		answerMu.Lock()
		if !mcpAuthSeen[data.ServiceID] {
			mcpAuthSeen[data.ServiceID] = true
			mcpAuthServices = append(mcpAuthServices, imMCPAuthService{ID: data.ServiceID, Name: data.ServiceName})
		}
		answerMu.Unlock()
		return nil
	})

	// Determine whether to use agent mode
	useAgent := customAgent != nil && customAgent.IsAgentMode()

	// Generate a shared RequestID to pair user and assistant messages for history
	requestID := uuid.New().String()

	// Create user message so it appears in conversation history
	userPayload := createIMUserMessagePayload(session.ID, query, requestID, attachments)
	userMsg, err := s.messageService.CreateMessage(ctx, userPayload)
	if err != nil {
		return "", fmt.Errorf("create user message: %w", err)
	}

	// Create a placeholder assistant message
	assistantMsg, err := s.messageService.CreateMessage(ctx, createIMAssistantMessagePayload(session.ID, requestID))
	if err != nil {
		return "", fmt.Errorf("create assistant message: %w", err)
	}

	eventBus.On(event.EventAgentReferences, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentReferencesData)
		if !ok {
			return nil
		}
		answerMu.Lock()
		refs := []*types.SearchResult(assistantMsg.KnowledgeReferences)
		collectIMKnowledgeReferences(&refs, data.References)
		assistantMsg.KnowledgeReferences = types.References(refs)
		answerMu.Unlock()
		return nil
	})

	eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		answerMu.Lock()
		applyIMCompleteDataToMessage(assistantMsg, data)
		if answerBuilder.Len() == 0 && strings.TrimSpace(data.FinalAnswer) != "" {
			answerBuilder.WriteString(data.FinalAnswer)
		}
		answerMu.Unlock()
		closeComplete()
		return nil
	})

	// Register inflight mapping for cross-instance /stop via StreamManager.
	if raw, ok := s.inflight.Load(userKey); ok {
		e := raw.(*inflightEntry)
		e.sessionID = session.ID
		e.assistantMessageID = assistantMsg.ID
	}
	s.storeInflightMapping(ctx, userKey, session.ID, assistantMsg.ID)
	defer s.clearInflightMapping(ctx, userKey)

	// Start StreamManager stop watcher.
	go s.watchStreamManagerStop(ctx, session.ID, assistantMsg.ID, cancel)

	// Run QA async
	go func() {
		// Match the streaming path: a returned AgentQA cannot produce more
		// events, while KnowledgeQA may still be consuming its answer stream.
		if useAgent {
			defer closeDone()
			defer closeComplete()
		}
		var err error
		req := buildIMQARequest(session, query, assistantMsg.ID, userMsg.ID, customAgent, kbIDs, quote, attachments)
		req.ImageURLs = imageURLs
		if req.QuotedContext != "" {
			logger.Debugf(ctx, "[IM] QuotedContext set: length=%d", len(req.QuotedContext))
		}
		if useAgent {
			err = s.sessionService.AgentQA(ctx, req, eventBus)
		} else {
			err = s.sessionService.KnowledgeQA(ctx, req, eventBus)
		}
		if err != nil {
			logger.Errorf(ctx, "[IM] QA execution error: %v", err)
			answerMu.Lock()
			qaErr = fmt.Errorf("QA execution error: %w", err)
			answerMu.Unlock()
			closeDone()
			closeComplete()
		}
	}()

	// Wait for completion or cancellation (e.g., /stop)
	select {
	case <-done:
		if useAgent {
			waitForIMAgentComplete(ctx, completeDone, session.ID)
		}
	case <-ctx.Done():
		// Mark assistant message as completed to avoid dangling incomplete records
		assistantMsg.Content = imCancelledFallback
		assistantMsg.IsCompleted = true
		// Use a fresh context since the original is cancelled
		if updateErr := s.messageService.UpdateMessage(context.WithoutCancel(ctx), assistantMsg); updateErr != nil {
			logger.Warnf(ctx, "[IM] Failed to update cancelled assistant message: %v", updateErr)
		}
		return "", fmt.Errorf("QA cancelled: %w", ctx.Err())
	}

	answerMu.Lock()
	answer := answerBuilder.String()
	qaError := qaErr
	authServices := append([]imMCPAuthService(nil), mcpAuthServices...)
	answerMu.Unlock()

	if answer == "" && qaError != nil {
		return "", qaError
	}
	if answer == "" {
		answer = imNoAnswerFallback
	}
	if notice := s.buildIMMCPAuthNotice(ctx, authServices); notice != "" {
		answer = appendIMAuthNotice(answer, notice)
	}

	// Update assistant message with the full answer (including citation tags for web rendering).
	assistantMsg.Content = answer
	assistantMsg.IsCompleted = true
	if err := s.messageService.UpdateMessage(ctx, assistantMsg); err != nil {
		logger.Warnf(ctx, "[IM] Failed to update assistant message: %v", err)
	}

	// Return raw answer — callers apply cleanIMContent with the appropriate FileService.
	return answer, nil
}
