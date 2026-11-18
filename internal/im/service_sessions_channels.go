package im

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"gorm.io/gorm"
)

// handleCommand executes a slash-command and sends the result back to the user.
// It also handles side effects (ActionClear, ActionStop).
func (s *Service) handleCommand(
	ctx context.Context,
	cmd Command,
	args []string,
	msg *IncomingMessage,
	adapter Adapter,
	channel *IMChannel,
	channelSession *ChannelSession,
	customAgent *types.CustomAgent,
) error {
	agentName := ""
	if customAgent != nil {
		agentName = customAgent.Name
	}

	cmdCtx := &CommandContext{
		Incoming:          msg,
		Session:           channelSession,
		TenantID:          channel.TenantID,
		AgentName:         agentName,
		CustomAgent:       customAgent,
		ChannelOutputMode: channel.OutputMode,
	}

	result, err := cmd.Execute(ctx, cmdCtx, args)
	if err != nil {
		logger.Errorf(ctx, "[IM] Command /%s error: %v", cmd.Name(), err)
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: "抱歉，执行指令时出现了异常，请稍后再试。",
			IsFinal: true,
		})
		return err
	}

	// Handle service-level side effects.
	switch result.Action {
	case ActionClear:
		// Soft-delete the current ChannelSession so the next IM message
		// starts a completely fresh GENYEE session. Conversation history
		// is keyed by session ID and rebuilt from DB on demand, so no
		// separate cache invalidation step is needed.
		if err := s.db.WithContext(ctx).Model(&ChannelSession{}).
			Where("id = ?", channelSession.ID).
			Update("deleted_at", time.Now()).Error; err != nil {
			logger.Warnf(ctx, "[IM] Failed to soft-delete channel session: %v", err)
		}
	case ActionStop:
		stopThreadID := ""
		if channel.SessionMode == string(SessionModeThread) {
			stopThreadID = msg.ThreadID
		}
		inflightKey := makeUserKey(channel.ID, msg.UserID, msg.ChatID, stopThreadID)

		// 1. Try local cancel: remove from queue or cancel in-flight.
		var localSessionID, localMessageID string
		localStopped := s.qaQueue.Remove(inflightKey)
		if localStopped {
			logger.Infof(ctx, "[IM] Cancelled queued QA: key=%s", inflightKey)
		} else if raw, loaded := s.inflight.LoadAndDelete(inflightKey); loaded {
			e := raw.(*inflightEntry)
			e.cancel()
			localStopped = true
			localSessionID = e.sessionID
			localMessageID = e.assistantMessageID
			logger.Infof(ctx, "[IM] Cancelled in-flight QA: key=%s", inflightKey)
		}

		// 2. Write stop event to StreamManager (same as web StopSession).
		//    For local stop with known IDs, write directly.
		//    For cross-instance, look up Redis inflight mapping to get IDs.
		sessionID, messageID := localSessionID, localMessageID
		if sessionID == "" || messageID == "" {
			// Try cross-instance lookup.
			sessionID, messageID, _ = s.loadInflightMapping(ctx, inflightKey)
		}
		if sessionID != "" && messageID != "" {
			s.writeStopEvent(ctx, sessionID, messageID)
			logger.Infof(ctx, "[IM] Wrote stop event to StreamManager: session=%s message=%s", sessionID, messageID)
		}

		// 3. Set Redis marker as fallback for requests not yet executing
		//    (no assistant message yet → no StreamManager entry to poll).
		if s.redis != nil {
			s.redis.Set(ctx, RedisKeyStop+inflightKey, "1", stopMarkerTTL)
		}

		if !localStopped && sessionID == "" {
			logger.Infof(ctx, "[IM] Set cross-instance stop marker (no inflight found): key=%s", inflightKey)
		}
	}

	// Send the command reply, respecting the configured output mode.
	sent := false
	if channel.OutputMode != "full" {
		if streamer, ok := adapter.(StreamSender); ok {
			if err := s.sendStreamReply(ctx, msg, streamer, result.Content); err != nil {
				logger.Warnf(ctx, "[IM] Stream reply for command /%s failed, falling back: %v", cmd.Name(), err)
			} else {
				sent = true
			}
		}
	}
	if !sent {
		_ = adapter.SendReply(ctx, msg, &ReplyMessage{
			Content: result.Content,
			IsFinal: true,
		})
	}

	logger.Infof(ctx, "[IM] Command /%s executed: channel=%s user=%s action=%d",
		cmd.Name(), channel.ID, msg.UserID, result.Action)
	return nil
}

// sendStreamReply sends a complete content string via the streaming interface.
func (s *Service) sendStreamReply(
	ctx context.Context,
	msg *IncomingMessage,
	streamer StreamSender,
	content string,
) error {
	streamID, err := streamer.StartStream(ctx, msg)
	if err != nil {
		return fmt.Errorf("start stream: %w", err)
	}
	if err := streamer.UpdateStreamContent(ctx, msg, streamID, content); err != nil {
		return fmt.Errorf("update stream content: %w", err)
	}
	if err := streamer.FinalizeStream(ctx, msg, streamID, content); err != nil {
		return fmt.Errorf("finalize stream: %w", err)
	}
	if err := streamer.EndStream(ctx, msg, streamID); err != nil {
		return fmt.Errorf("end stream: %w", err)
	}
	return nil
}

// resolveSession dispatches to the appropriate session resolution strategy
// based on the channel's session mode.
func (s *Service) resolveSession(
	ctx context.Context,
	msg *IncomingMessage,
	tenantID uint64,
	agentID string,
	imChannelID string,
	sessionMode string,
) (*ChannelSession, error) {
	switch SessionMode(sessionMode) {
	case SessionModeThread:
		return s.resolveThreadSession(ctx, msg, tenantID, agentID, imChannelID)
	default: // SessionModeUser
		return s.resolveUserSession(ctx, msg, tenantID, agentID, imChannelID)
	}
}

// buildUserSessionTitle produces a human-distinguishable title for a user-mode
// IM session. Platform adapters only surface ChatID, not a readable chat name,
// so we fall back to short ID suffixes to keep group/DM sessions visually distinct.
// Platform prefix is intentionally omitted — the UI renders a platform icon badge
// alongside the title, so the `[feishu]` prefix would be redundant clutter.
func buildUserSessionTitle(msg *IncomingMessage) string {
	var b strings.Builder
	switch {
	case msg.UserName != "":
		b.WriteString(msg.UserName)
	case msg.UserID != "":
		b.WriteString("user ")
		b.WriteString(shortID(msg.UserID))
	default:
		b.WriteString("user")
	}
	if msg.ChatType == ChatTypeGroup && msg.ChatID != "" {
		fmt.Fprintf(&b, " · group %s", shortID(msg.ChatID))
	} else if msg.ChatType == ChatTypeDirect {
		b.WriteString(" · dm")
	}
	return b.String()
}

// buildThreadSessionTitle produces a title for a thread-mode IM session.
// In thread mode different users can share one session, so the user name is
// omitted and chat/thread IDs carry the distinguishing information.
// Platform prefix is omitted for the same reason as buildUserSessionTitle.
func buildThreadSessionTitle(msg *IncomingMessage) string {
	var b strings.Builder
	if msg.ChatID != "" {
		fmt.Fprintf(&b, "chat %s · ", shortID(msg.ChatID))
	}
	b.WriteString("thread ")
	b.WriteString(shortID(msg.ThreadID))
	return b.String()
}

// shortID returns the last 8 characters of id, or id itself when shorter.
// Used to keep long platform IDs readable inside titles without losing uniqueness.
func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// imInitialSessionTitle picks a new IM session's starting title: "" when the
// message has text to summarise (so it gets a content-based title later, like
// web chats), otherwise the IM identity title so the row is never blank.
func imInitialSessionTitle(msg *IncomingMessage, identityTitle func(*IncomingMessage) string) string {
	if strings.TrimSpace(msg.Content) != "" {
		return ""
	}
	return identityTitle(msg)
}

// resolveUserSession finds or creates a ChannelSession keyed by (platform, user_id, chat_id, tenant_id, agent_id).
// Only mappings with an empty thread_id belong to user mode.
//
// Invariant: a cache miss creates a brand-new session — we never attach a second
// mapping to an existing session. The session-list source filter (repository
// QueryPaged) relies on this one-mapping-per-session property; if this ever
// re-maps an existing session, that JOIN needs a one-row-per-session guard.
func (s *Service) resolveUserSession(
	ctx context.Context,
	msg *IncomingMessage,
	tenantID uint64,
	agentID string,
	imChannelID string,
) (*ChannelSession, error) {
	var cs ChannelSession
	result := s.db.WithContext(ctx).Where(
		"platform = ? AND user_id = ? AND chat_id = ? AND tenant_id = ? "+
			"AND agent_id = ? AND thread_id = '' AND deleted_at IS NULL",
		string(msg.Platform), msg.UserID, msg.ChatID, tenantID, agentID).
		First(&cs)

	if result.Error == nil {
		return &cs, nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query channel session: %w", result.Error)
	}

	// Create a new GENYEE session. Start untitled when there's text to summarise
	// so it gets a content-based title after the first message (see HandleMessage);
	// fall back to the IM identity title otherwise.
	title := imInitialSessionTitle(msg, buildUserSessionTitle)

	newSession := &types.Session{
		TenantID:    tenantID,
		Title:       title,
		Description: fmt.Sprintf("Auto-created from %s IM integration", msg.Platform),
	}

	createdSession, err := s.sessionService.CreateSession(ctx, newSession)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Create the channel-session mapping; use a unique constraint fallback
	// to handle concurrent creation attempts for the same channel.
	cs = ChannelSession{
		Platform:    string(msg.Platform),
		UserID:      msg.UserID,
		ChatID:      msg.ChatID,
		SessionID:   createdSession.ID,
		TenantID:    tenantID,
		AgentID:     agentID,
		IMChannelID: imChannelID,
	}
	if err := s.db.WithContext(ctx).Create(&cs).Error; err != nil {
		if delErr := s.db.WithContext(context.WithoutCancel(ctx)).
			Where("id = ?", createdSession.ID).Delete(createdSession).Error; delErr != nil {
			logger.Warnf(ctx, "[IM] Failed to clean up orphaned session %s: %v", createdSession.ID, delErr)
		}
		var existing ChannelSession
		if findErr := s.db.WithContext(ctx).Where(
			"platform = ? AND user_id = ? AND chat_id = ? AND tenant_id = ? "+
				"AND agent_id = ? AND thread_id = '' AND deleted_at IS NULL",
			string(msg.Platform), msg.UserID, msg.ChatID, tenantID, agentID).
			First(&existing).Error; findErr != nil {
			return nil, fmt.Errorf("create channel session: %w (lookup fallback: %v)", err, findErr)
		}
		return &existing, nil
	}

	logger.Infof(ctx, "[IM] Created new session mapping: channel=%s/%s/%s -> session=%s",
		msg.Platform, msg.UserID, msg.ChatID, createdSession.ID)

	return &cs, nil
}

// resolveThreadSession finds or creates a ChannelSession keyed by (platform, chat_id, thread_id, tenant_id, agent_id).
// In thread mode, each message thread gets its own session. Multiple users in the
// same thread share the same session. Top-level messages use their own ID as
// ThreadID, creating a new session per top-level message.
func (s *Service) resolveThreadSession(
	ctx context.Context,
	msg *IncomingMessage,
	tenantID uint64,
	agentID string,
	imChannelID string,
) (*ChannelSession, error) {
	threadID := msg.ThreadID
	if threadID == "" {
		// Defense-in-depth: frontend blocks thread mode for unsupported platforms,
		// but if ThreadID is somehow empty, fall back to user-mode resolution
		// to avoid creating a shared session for all empty-thread messages.
		logger.Warnf(
			ctx,
			"[IM] Thread mode but ThreadID is empty (platform=%s chat=%s), falling back to user session",
			msg.Platform, msg.ChatID,
		)
		return s.resolveUserSession(ctx, msg, tenantID, agentID, imChannelID)
	}

	var cs ChannelSession
	result := s.db.WithContext(ctx).Where(
		"platform = ? AND chat_id = ? AND thread_id = ? AND tenant_id = ? AND agent_id = ? AND deleted_at IS NULL",
		string(msg.Platform), msg.ChatID, threadID, tenantID, agentID,
	).First(&cs)

	if result.Error == nil {
		return &cs, nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query thread session: %w", result.Error)
	}

	// Start untitled when there's text to summarise so it gets a content-based
	// title after the first message; fall back to the chat/thread identity title.
	title := imInitialSessionTitle(msg, buildThreadSessionTitle)

	newSession := &types.Session{
		TenantID:    tenantID,
		Title:       title,
		Description: fmt.Sprintf("Thread-based session from %s IM", msg.Platform),
	}

	createdSession, err := s.sessionService.CreateSession(ctx, newSession)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	cs = ChannelSession{
		Platform:    string(msg.Platform),
		UserID:      msg.UserID, // record the first creator
		ChatID:      msg.ChatID,
		ThreadID:    threadID,
		SessionID:   createdSession.ID,
		TenantID:    tenantID,
		AgentID:     agentID,
		IMChannelID: imChannelID,
	}

	if err := s.db.WithContext(ctx).Create(&cs).Error; err != nil {
		// Unique constraint fallback for concurrent creation.
		if delErr := s.db.WithContext(context.WithoutCancel(ctx)).
			Where("id = ?", createdSession.ID).Delete(createdSession).Error; delErr != nil {
			logger.Warnf(ctx, "[IM] Failed to clean up orphaned session %s: %v", createdSession.ID, delErr)
		}
		var existing ChannelSession
		if findErr := s.db.WithContext(ctx).Where(
			"platform = ? AND chat_id = ? AND thread_id = ? AND tenant_id = ? AND agent_id = ? AND deleted_at IS NULL",
			string(msg.Platform), msg.ChatID, threadID, tenantID, agentID,
		).First(&existing).Error; findErr != nil {
			return nil, fmt.Errorf("create thread session: %w (lookup fallback: %v)", err, findErr)
		}
		return &existing, nil
	}

	logger.Infof(ctx, "[IM] Created new thread session: platform=%s thread=%s chat=%s -> session=%s",
		msg.Platform, threadID, msg.ChatID, createdSession.ID)
	return &cs, nil
}

// ── CRUD operations for IM channels ──

// ListChannelsByAgent returns all channels for a given agent within a tenant.
func (s *Service) ListChannelsByAgent(agentID string, tenantID uint64) ([]IMChannel, error) {
	var channels []IMChannel
	if err := s.db.WithContext(context.Background()).
		Where("agent_id = ? AND tenant_id = ? AND deleted_at IS NULL", agentID, tenantID).
		Order("created_at DESC").Find(&channels).Error; err != nil {
		return nil, err
	}
	return channels, nil
}

// ChannelWithAgent augments an IMChannel summary with its owning agent's display name.
// Credentials are intentionally omitted so this type is safe to return from a
// tenant-scoped list endpoint; callers that need credentials must use the
// per-agent endpoint which enforces the same tenant scope anyway.
type ChannelWithAgent struct {
	ID          string    `json:"id"`
	TenantID    uint64    `json:"tenant_id"`
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name"`
	Platform    string    `json:"platform"`
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	Mode        string    `json:"mode"`
	OutputMode  string    `json:"output_mode"`
	Locale      string    `json:"locale"`
	SessionMode string    `json:"session_mode"`
	BotIdentity string    `json:"bot_identity"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListChannelsByTenant returns all non-deleted IM channels in the given tenant,
// joined with custom_agents.name. Built-in agent names are re-localized from
// YAML i18n using ctx (the DB column may be empty or frozen in the writer's
// language). Channels whose custom agent was soft-deleted are excluded so
// overview lists stay consistent after agent removal.
func (s *Service) ListChannelsByTenant(ctx context.Context, tenantID uint64) ([]ChannelWithAgent, error) {
	builtinIDs := types.GetBuiltinAgentIDs()
	var rows []ChannelWithAgent
	q := s.db.WithContext(ctx).Table("im_channels AS c").
		Select(`c.id, c.tenant_id, c.agent_id,
                COALESCE(a.name, '') AS agent_name,
                c.platform, c.name, c.enabled, c.mode, c.output_mode, c.locale,
                c.session_mode, c.bot_identity, c.created_at, c.updated_at`).
		Joins(`LEFT JOIN custom_agents AS a
               ON a.id = c.agent_id AND a.tenant_id = c.tenant_id AND a.deleted_at IS NULL`).
		Where("c.tenant_id = ? AND c.deleted_at IS NULL", tenantID)
	if len(builtinIDs) > 0 {
		q = q.Where("a.id IS NOT NULL OR c.agent_id IN ?", builtinIDs)
	} else {
		q = q.Where("a.id IS NOT NULL")
	}
	err := q.Order("c.created_at DESC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	relocalizeBuiltinChannelAgentNames(ctx, rows)
	return rows, nil
}

// relocalizeBuiltinChannelAgentNames overlays YAML i18n names onto overview
// rows for built-in agents. Custom agents are left unchanged.
func relocalizeBuiltinChannelAgentNames(ctx context.Context, rows []ChannelWithAgent) {
	for i := range rows {
		if a := types.GetBuiltinAgentWithContext(ctx, rows[i].AgentID, rows[i].TenantID); a != nil && a.Name != "" {
			rows[i].AgentName = a.Name
		}
	}
}

// CreateChannel creates a new IM channel and optionally starts it.
// Returns a duplicate_bot error if the bot identity is already used by another channel.
func (s *Service) CreateChannel(channel *IMChannel) error {
	if err := validateChannelTransport(channel); err != nil {
		return err
	}
	if err := s.checkDuplicateBot(channel, ""); err != nil {
		return err
	}
	if err := s.db.WithContext(context.Background()).Create(channel).Error; err != nil {
		return err
	}
	if channel.Enabled {
		if err := s.StartChannel(channel); err != nil {
			logger.Warnf(context.Background(), "[IM] Created channel %s but failed to start: %v", channel.ID, err)
		}
	}
	s.publishChannelConfigChange(channel.ID)
	return nil
}

// SetChannelAgentID validates and assigns a new agent for an existing channel.
func (s *Service) SetChannelAgentID(ctx context.Context, channel *IMChannel, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	agent, err := s.agentService.GetAgentByID(ctx, agentID)
	if err != nil {
		return err
	}
	if agent == nil || agent.TenantID != channel.TenantID {
		return fmt.Errorf("agent not found")
	}
	channel.AgentID = agentID
	return nil
}

// SetChannelKnowledgeBaseID binds the KB that IM files are saved into. It must
// belong to the channel's workspace, which writes into it, and to a KB-restricted
// API key's allow-list; a foreign ID would create records in (and read the
// configuration of) another workspace's KB. An empty ID clears the binding.
func (s *Service) SetChannelKnowledgeBaseID(ctx context.Context, channel *IMChannel, kbID string) error {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		channel.KnowledgeBaseID = ""
		return nil
	}
	if err := types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbID); err != nil {
		return fmt.Errorf("knowledge base not found")
	}
	if s.kbService == nil {
		return fmt.Errorf("knowledge base not found")
	}
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil || kb == nil || kb.TenantID != channel.TenantID {
		return fmt.Errorf("knowledge base not found")
	}
	channel.KnowledgeBaseID = kbID
	return nil
}

// UpdateChannel updates a channel and restarts it if needed.
// Returns a duplicate_bot error if the bot identity is already used by another channel.
func (s *Service) UpdateChannel(channel *IMChannel) error {
	if err := validateChannelTransport(channel); err != nil {
		return err
	}
	if err := s.checkDuplicateBot(channel, channel.ID); err != nil {
		return err
	}
	if err := s.db.WithContext(context.Background()).Save(channel).Error; err != nil {
		return err
	}
	// Restart channel: stop old, start new if enabled
	s.StopChannel(channel.ID)
	if channel.Enabled {
		if err := s.StartChannel(channel); err != nil {
			logger.Warnf(context.Background(), "[IM] Updated channel %s but failed to restart: %v", channel.ID, err)
		}
	}
	s.publishChannelConfigChange(channel.ID)
	return nil
}

// DeleteChannelsByAgent stops and soft-deletes every IM channel bound to the
// given agent within the tenant. Used when a custom agent is removed so
// overview lists and running adapters do not outlive the agent.
func (s *Service) DeleteChannelsByAgent(agentID string, tenantID uint64) error {
	ctx := context.Background()
	var channels []IMChannel
	if err := s.db.WithContext(ctx).
		Where("agent_id = ? AND tenant_id = ? AND deleted_at IS NULL", agentID, tenantID).
		Find(&channels).Error; err != nil {
		return err
	}
	if len(channels) == 0 {
		return nil
	}
	if err := s.db.WithContext(ctx).
		Where("agent_id = ? AND tenant_id = ? AND deleted_at IS NULL", agentID, tenantID).
		Delete(&IMChannel{}).Error; err != nil {
		return err
	}
	for i := range channels {
		s.StopChannel(channels[i].ID)
		s.publishChannelConfigChange(channels[i].ID)
	}
	return nil
}

// DeleteChannel soft-deletes a channel and stops it. Only deletes if the channel belongs to the given tenant.
func (s *Service) DeleteChannel(channelID string, tenantID uint64) error {
	result := s.db.WithContext(context.Background()).
		Where("id = ? AND tenant_id = ?", channelID, tenantID).
		Delete(&IMChannel{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("channel not found")
	}
	s.StopChannel(channelID)
	s.publishChannelConfigChange(channelID)
	return nil
}

// ToggleChannel enables or disables a channel. Only toggles if the channel belongs to the given tenant.
func (s *Service) ToggleChannel(channelID string, tenantID uint64) (*IMChannel, error) {
	ctx := context.Background()
	var ch IMChannel
	if err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", channelID, tenantID).
		First(&ch).Error; err != nil {
		return nil, err
	}
	ch.Enabled = !ch.Enabled
	if err := s.db.WithContext(ctx).Save(&ch).Error; err != nil {
		return nil, err
	}
	if ch.Enabled {
		if err := s.StartChannel(&ch); err != nil {
			logger.Warnf(ctx, "[IM] Failed to start channel %s after enable: %v", ch.ID, err)
		}
	} else {
		s.StopChannel(channelID)
	}
	s.publishChannelConfigChange(channelID)
	return &ch, nil
}

// checkDuplicateBot queries the bot_identity index to see if another active channel
// already uses the same bot. This is an O(1) index lookup, not a full table scan.
// The DB unique index on bot_identity serves as an additional safety net.
// excludeID is the channel's own ID (for updates); pass "" for new channels.
func (s *Service) checkDuplicateBot(channel *IMChannel, excludeID string) error {
	// Compute bot_identity the same way the BeforeSave hook will
	botKey := channel.computeBotIdentity()
	if botKey == "" {
		return nil
	}

	var existing IMChannel
	query := s.db.WithContext(context.Background()).Where("bot_identity = ? AND deleted_at IS NULL", botKey)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	if err := query.First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil // no conflict
		}
		return fmt.Errorf("check duplicate bot: %w", err)
	}
	return fmt.Errorf(
		"duplicate_bot: this bot is already bound to channel %q (%s); "+
			"each bot can only be connected to one channel",
		existing.Name, existing.ID,
	)
}
