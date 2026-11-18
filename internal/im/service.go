// Package im orchestrates multi-platform IM bot channels, sessions, and streaming Q&A.
package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/logger"
	mcppkg "github.com/Usir233/GENYEE/internal/mcp"
	"github.com/Usir233/GENYEE/internal/ratelimit"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	// wsLeaderTTL is the TTL for the Redis key used for WebSocket leader election.
	wsLeaderTTL = 15 * time.Second
	// wsLeaderRenewInterval is how often the leader renews its lock.
	wsLeaderRenewInterval = 5 * time.Second
	// wsLeaderRetryInterval is how often non-leader instances try to acquire the lock.
	wsLeaderRetryInterval = 10 * time.Second
	// stopMarkerTTL is the TTL for cross-instance /stop markers in Redis.
	stopMarkerTTL = 30 * time.Second
	// stopPollInterval is how often in-flight workers check for remote /stop signals.
	stopPollInterval = 500 * time.Millisecond

	channelConfigSubBaseBackoff = 200 * time.Millisecond
	channelConfigSubMaxBackoff  = 30 * time.Second
)

// ── Redis key prefixes ──────────────────────────────────────────────────────
// All IM-related Redis keys are defined here for discoverability and to avoid
// scattered string literals across multiple files.
const (
	RedisKeyLeader     = "im:ws:leader:"    // + channelID — WebSocket leader election
	RedisKeyDedup      = "im:dedup:"        // + messageID — message deduplication
	RedisKeyStop       = "im:stop:"         // + userKey   — cross-instance /stop marker (pre-execution)
	RedisKeyInflight   = "im:inflight:"     // + userKey   — maps userKey → sessionID:messageID for cross-instance /stop
	RedisKeyQueueUser  = "im:queue:user:"   // + userKey   — global per-user queue counter
	RedisKeyRateLimit  = "im:ratelimit:"    // + key       — sliding-window rate limiting
	RedisKeyGlobalGate = "im:global:active" // global concurrent worker counter
	// RedisChannelConfig broadcasts durable im_channels mutations so every
	// application replica invalidates its local adapter/config snapshot.
	RedisChannelConfig = "im:channel:config"

	defaultRateLimitWindow      = 60 * time.Second
	defaultRateLimitMaxRequests = 10
)

// ErrChannelDisabled reports that a channel row exists but is disabled, so
// callers can tell it apart from a missing channel or a transient failure.
var ErrChannelDisabled = errors.New("channel is disabled")

// channelState holds runtime state for a running IM channel.
type channelState struct {
	Channel      *IMChannel
	Adapter      Adapter
	Cancel       context.CancelFunc // for stopping websocket goroutines
	leaderCancel context.CancelFunc // stops the leader renewal goroutine (nil if not leader)
}

type leaderRetryState struct {
	cancel context.CancelFunc
}

type channelConfigEvent struct {
	ChannelID      string `json:"channel_id"`
	SourceInstance string `json:"source_instance"`
}

// AdapterFactory creates an Adapter from an IMChannel configuration.
// The second return value is an optional cleanup function (e.g., for stopping websocket connections).
type AdapterFactory func(
	ctx context.Context,
	channel *IMChannel,
	msgHandler func(ctx context.Context, msg *IncomingMessage) error,
) (Adapter, context.CancelFunc, error)

// inflightEntry tracks a running QA request, keyed by userKey in the inflight map.
type inflightEntry struct {
	cancel             context.CancelFunc
	sessionID          string // set after assistant message is created
	assistantMessageID string // set after assistant message is created
}

// Service orchestrates IM message handling:
// 1. Receives a unified IncomingMessage from an Adapter
// 2. Resolves or creates a GENYEE session for the IM channel
// 3. Dispatches slash-commands (/help, /kb, /clear, etc.) without entering QA
// 4. Calls the GENYEE QA pipeline for normal messages
// 5. Collects the streaming answer and sends it back via the Adapter
type Service struct {
	db             *gorm.DB
	sessionService interfaces.SessionService
	messageService interfaces.MessageService
	tenantService  interfaces.TenantService
	agentService   interfaces.CustomAgentService

	// knowledgeService is used for saving IM file messages to knowledge bases.
	knowledgeService interfaces.KnowledgeService

	// kbService is used by slash-commands (/info) to list and inspect knowledge bases.
	kbService      interfaces.KnowledgeBaseService
	kbShareService interfaces.KBShareService

	// oauthManager builds MCP OAuth authorization URLs so IM users can authorize
	// OAuth-enabled MCP services out-of-band (IM cannot resolve the in-conversation
	// prompt). May be nil, in which case a generic console hint is shown instead.
	oauthManager *mcppkg.OAuthManager

	// streamManager writes/reads QA events for distributed stop detection,
	// consistent with the web StopSession mechanism. May be nil in Lite mode
	// (but NewStreamManager always returns at least a memory implementation).
	streamManager interfaces.StreamManager

	// defaultFileSvc is the process-wide storage backend (STORAGE_TYPE / env).
	// Used when tenant StorageEngineConfig cannot build a service for the URL scheme.
	defaultFileSvc  interfaces.FileService
	documentReader  interfaces.DocumentReader
	storageResolver interfaces.StorageBackendResolver
	resourceCatalog interfaces.ResourceCatalog

	// cmdRegistry holds all registered slash-commands.
	cmdRegistry *CommandRegistry

	// channels maps channel ID -> running channel state
	channels      map[string]*channelState
	leaderRetries map[string]*leaderRetryState
	mu            sync.RWMutex

	// adapterFactories maps platform name -> factory function
	adapterFactories map[string]AdapterFactory

	// processedMsgs tracks recently processed message IDs to prevent duplicate handling.
	processedMsgs sync.Map

	// rateLimiter enforces per-user sliding window rate limiting.
	// Uses Redis ZSET when available, falls back to local sliding window.
	rateLimiter  *ratelimit.Limiter
	rateLimitMax int

	// inflight tracks in-progress QA requests, keyed by userKey
	// ("channelID:userID:chatID"). Allows /stop to abort a running request
	// on this instance and look up (sessionID, messageID) for StreamManager.
	inflight sync.Map // userKey -> *inflightEntry

	// qaQueue manages bounded queuing and worker-pool execution of QA requests,
	// providing backpressure to protect downstream LLM resources.
	qaQueue *qaQueue

	// redis is the optional Redis client for distributed state (dedup, rate
	// limiting, leader election, cross-instance /stop). When nil the service
	// falls back to local in-memory state (single-instance / Lite mode).
	redis *redis.Client

	// instanceID uniquely identifies this service instance for leader election.
	instanceID string

	stopCh         chan struct{}
	stopOnce       sync.Once
	subscriberOnce sync.Once
	subCancel      context.CancelFunc
	subWg          sync.WaitGroup
	stopped        atomic.Bool
}

// makeUserKey builds the canonical key used to identify a user's request
// across the queue, inflight map, and /stop command.
// threadID should only be non-empty when channel.SessionMode == "thread";
// callers must guard this to avoid leaking thread scope into user-mode keys.
func makeUserKey(channelID, userID, chatID, threadID string) string {
	if threadID != "" {
		return fmt.Sprintf("%s:%s:%s:%s", channelID, userID, chatID, threadID)
	}
	return fmt.Sprintf("%s:%s:%s", channelID, userID, chatID)
}

// resolveIMConfig extracts IM tuning parameters from the application config,
// falling back to built-in defaults for any zero/nil values.
func resolveIMConfig(
	appCfg *config.Config,
) (workers, maxQueue, maxPerUser, globalMaxWorkers int, rlWindow time.Duration, rlMax int) {
	workers = defaultWorkers
	maxQueue = defaultMaxQueueSize
	maxPerUser = defaultMaxPerUser
	rlWindow = defaultRateLimitWindow
	rlMax = defaultRateLimitMaxRequests

	if appCfg == nil || appCfg.IM == nil {
		return
	}
	im := appCfg.IM
	if im.Workers > 0 {
		workers = im.Workers
	}
	if im.MaxQueueSize > 0 {
		maxQueue = im.MaxQueueSize
	}
	if im.MaxPerUser > 0 {
		maxPerUser = im.MaxPerUser
	}
	if im.GlobalMaxWorkers > 0 {
		globalMaxWorkers = im.GlobalMaxWorkers
	}
	if im.RateLimitWindow > 0 {
		rlWindow = im.RateLimitWindow
	}
	if im.RateLimitMax > 0 {
		rlMax = im.RateLimitMax
	}
	return
}

// NewService creates a new IM service.
// redisClient may be nil — in that case the service falls back to local
// in-memory state (Lite / single-instance mode).
// cfg may be nil — in that case built-in defaults are used.
func NewService(
	db *gorm.DB,
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	tenantService interfaces.TenantService,
	agentService interfaces.CustomAgentService,
	knowledgeService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	streamManager interfaces.StreamManager,
	defaultFileSvc interfaces.FileService,
	documentReader interfaces.DocumentReader,
	oauthManager *mcppkg.OAuthManager,
	redisClient *redis.Client,
	appCfg *config.Config,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
	kbShareService interfaces.KBShareService,
) *Service {
	// Resolve IM configuration with defaults.
	workers, maxQueue, maxPerUser, globalMaxWorkers, rlWindow, rlMax := resolveIMConfig(appCfg)

	// Build command registry.
	registry := NewCommandRegistry()
	registry.Register(newHelpCommand(registry))
	registry.Register(newInfoCommand(kbService))
	registry.Register(newSearchCommand(sessionService, kbService))
	registry.Register(newStopCommand())
	registry.Register(newClearCommand())

	instanceID := uuid.New().String()
	s := &Service{
		db:               db,
		sessionService:   sessionService,
		messageService:   messageService,
		tenantService:    tenantService,
		agentService:     agentService,
		knowledgeService: knowledgeService,
		kbService:        kbService,
		streamManager:    streamManager,
		defaultFileSvc:   defaultFileSvc,
		documentReader:   documentReader,
		storageResolver:  storageResolver,
		resourceCatalog:  resourceCatalog,
		kbShareService:   kbShareService,
		oauthManager:     oauthManager,
		cmdRegistry:      registry,
		channels:         make(map[string]*channelState),
		leaderRetries:    make(map[string]*leaderRetryState),
		adapterFactories: make(map[string]AdapterFactory),
		rateLimiter:      ratelimit.New(redisClient, RedisKeyRateLimit, rlWindow, instanceID),
		rateLimitMax:     rlMax,
		redis:            redisClient,
		instanceID:       instanceID,
		stopCh:           make(chan struct{}),
	}

	// Initialize the QA worker pool and bounded queue.
	s.qaQueue = newQAQueue(workers, maxQueue, maxPerUser, globalMaxWorkers, s.executeQARequest, redisClient)
	s.qaQueue.Start(s.stopCh)

	// Start periodic cleanup loops.
	// Dedup cleanup is only needed in single-instance mode (local sync.Map);
	// when Redis handles dedup, the TTL on Redis keys handles expiry automatically.
	if redisClient == nil {
		go s.dedupCleanupLoop()
	}
	go s.rateLimiter.StartCleanup(s.stopCh)

	if redisClient != nil {
		globalInfo := "unlimited"
		if globalMaxWorkers > 0 {
			globalInfo = fmt.Sprintf("%d", globalMaxWorkers)
		}
		logger.Infof(
			context.Background(),
			"[IM] Multi-instance mode enabled (instance=%s, workers=%d, queue=%d, global_max=%s)",
			s.instanceID[:8], workers, maxQueue, globalInfo,
		)
	} else {
		logger.Infof(context.Background(), "[IM] Single-instance mode (no Redis, workers=%d, queue=%d)",
			workers, maxQueue)
	}

	return s
}

// RegisterAdapterFactory registers a factory for creating adapters for a given platform.
func (s *Service) RegisterAdapterFactory(platform string, factory AdapterFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapterFactories[platform] = factory
}

// Stop gracefully shuts down the service, stopping all channels and background goroutines.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		if s.stopCh != nil {
			close(s.stopCh)
		}
		if s.qaQueue != nil {
			s.qaQueue.Stop()
		}
		s.mu.Lock()
		subCancel := s.subCancel
		s.subCancel = nil
		for id, cs := range s.channels {
			s.stopChannelLocked(id, cs)
		}
		for id, retry := range s.leaderRetries {
			retry.cancel()
			delete(s.leaderRetries, id)
		}
		s.mu.Unlock()

		if subCancel != nil {
			subCancel()
		}
		s.subWg.Wait()
	})
}

// dedupCleanupLoop periodically cleans up expired entries from the dedup map.
func (s *Service) dedupCleanupLoop() {
	ticker := time.NewTicker(dedupCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-dedupTTL)
			s.processedMsgs.Range(func(key, value interface{}) bool {
				if t, ok := value.(time.Time); ok && t.Before(cutoff) {
					s.processedMsgs.Delete(key)
				}
				return true
			})
		case <-s.stopCh:
			return
		}
	}
}

// imImageConfigWarning returns an operator warning when IM channels are active
// but APP_EXTERNAL_URL is unset. resource:// images then render only if the
// storage backend is itself publicly reachable (e.g. a cloud bucket with a
// public endpoint); on the default MinIO (internal minio:9000) or local
// deployment it is not, so images silently break. Advisory only; returns ""
// when there is nothing to warn about. Pure (no receiver/closures) so it is
// unit-testable.
func imImageConfigWarning(activeChannels int, externalURL string) string {
	if activeChannels == 0 || strings.TrimSpace(externalURL) != "" {
		return ""
	}
	return fmt.Sprintf("[IM] %d IM channel(s) active but APP_EXTERNAL_URL is unset; "+
		"resource:// images render only if the storage backend is publicly reachable — "+
		"otherwise set APP_EXTERNAL_URL so they route through nginx /r/",
		activeChannels)
}

// LoadAndStartChannels loads all enabled channels from the database and starts them.
func (s *Service) LoadAndStartChannels() error {
	s.startChannelConfigSubscriber()

	ctx := context.Background()
	var channels []IMChannel
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND deleted_at IS NULL", true).
		Find(&channels).Error; err != nil {
		return fmt.Errorf("load im channels: %w", err)
	}

	if msg := imImageConfigWarning(len(channels), os.Getenv("APP_EXTERNAL_URL")); msg != "" {
		logger.Warnf(ctx, "%s", msg)
	}

	for i := range channels {
		ch := channels[i]
		if err := s.StartChannel(&ch); err != nil {
			logger.Warnf(ctx, "[IM] Failed to start channel %s (%s/%s): %v", ch.ID, ch.Platform, ch.Name, err)
		} else if _, _, active := s.GetChannelAdapter(ch.ID); active {
			// Adapter initialization can launch an asynchronous connection attempt.
			// Do not claim network readiness here; platform connection logs report it.
			logger.Infof(ctx, "[IM] Initialized channel runtime: id=%s platform=%s name=%s mode=%s agent=%s",
				ch.ID, ch.Platform, ch.Name, ch.Mode, ch.AgentID)
		} else {
			logger.Infof(ctx, "[IM] Channel runtime is on standby: id=%s platform=%s name=%s mode=%s agent=%s",
				ch.ID, ch.Platform, ch.Name, ch.Mode, ch.AgentID)
		}
	}

	logger.Infof(ctx, "[IM] Loaded %d enabled channels", len(channels))
	return nil
}

// startChannelConfigSubscriber listens for durable channel mutations made by
// other application replicas. Redis Pub/Sub provides the fast path; callback
// freshness checks and the leader renewal DB check remain the durable fallback
// if an event is missed while a replica is disconnected.
func (s *Service) startChannelConfigSubscriber() {
	if s.redis == nil {
		return
	}
	s.subscriberOnce.Do(func() {
		s.mu.Lock()
		if s.stopped.Load() {
			s.mu.Unlock()
			return
		}
		subCtx, subCancel := context.WithCancel(context.Background())
		s.subCancel = subCancel
		s.subWg.Add(1)
		s.mu.Unlock()

		go func() {
			defer s.subWg.Done()
			s.channelConfigSubscriberLoop(subCtx)
		}()
	})
}

func (s *Service) channelConfigSubscriberLoop(subCtx context.Context) {
	backoff := channelConfigSubBaseBackoff
	for {
		if subCtx.Err() != nil || s.stopped.Load() {
			return
		}
		disconnected, ok := s.runChannelConfigSubscription(subCtx, &backoff)
		if !ok {
			return
		}
		if disconnected {
			logger.Warnf(
				context.Background(),
				"[IM] Redis channel config subscription closed; reconnecting in %s",
				backoff,
			)
		}
		if !s.waitChannelConfigSubBackoff(subCtx, backoff) {
			return
		}
		backoff *= 2
		if backoff > channelConfigSubMaxBackoff {
			backoff = channelConfigSubMaxBackoff
		}
	}
}

func (s *Service) runChannelConfigSubscription(subCtx context.Context, backoff *time.Duration) (bool, bool) {
	pubsub := s.redis.Subscribe(subCtx, RedisChannelConfig)
	defer func() {
		_ = pubsub.Close()
	}()

	if _, err := pubsub.Receive(subCtx); err != nil {
		if subCtx.Err() != nil || s.stopped.Load() {
			return false, false
		}
		logger.Warnf(
			context.Background(),
			"[IM] Subscribe channel config events failed: %v; retrying in %s",
			err, *backoff,
		)
		return false, true
	}

	*backoff = channelConfigSubBaseBackoff
	messages := pubsub.Channel(redis.WithChannelHealthCheckInterval(time.Second))
	for {
		select {
		case <-subCtx.Done():
			return false, false
		case <-s.stopCh:
			return false, false
		case msg, ok := <-messages:
			if !ok {
				return true, !s.stopped.Load() && subCtx.Err() == nil
			}
			var evt channelConfigEvent
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				logger.Warnf(context.Background(), "[IM] Ignore invalid channel config event: %v", err)
				continue
			}
			if evt.ChannelID == "" || evt.SourceInstance == s.instanceID {
				continue
			}
			s.reloadChannelFromDB(evt.ChannelID, "config event")
		}
	}
}

func (s *Service) waitChannelConfigSubBackoff(subCtx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-subCtx.Done():
		return false
	case <-s.stopCh:
		return false
	case <-timer.C:
		return true
	}
}

func (s *Service) publishChannelConfigChange(channelID string) {
	if s.redis == nil || channelID == "" || s.stopped.Load() {
		return
	}
	payload, err := json.Marshal(channelConfigEvent{
		ChannelID:      channelID,
		SourceInstance: s.instanceID,
	})
	if err != nil {
		return
	}
	if err := s.redis.Publish(context.Background(), RedisChannelConfig, payload).Err(); err != nil {
		// The database is the source of truth. Subscribers also verify freshness
		// on webhook callbacks / leader renewal, so publication is best-effort.
		logger.Warnf(context.Background(), "[IM] Publish channel config event failed for %s: %v", channelID, err)
	}
}

func (s *Service) reloadChannelFromDB(channelID, reason string) {
	fresh, err := s.GetChannelByID(channelID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.StopChannel(channelID)
		return
	}
	if err != nil {
		logger.Warnf(context.Background(), "[IM] Reload channel %s after %s failed: %v", channelID, reason, err)
		return
	}
	if !fresh.Enabled {
		s.StopChannel(channelID)
		return
	}
	if _, cached, running := s.GetChannelAdapter(channelID); running && sameChannelRuntimeConfig(cached, fresh) {
		return
	}

	logger.Infof(context.Background(), "[IM] Reloading channel %s after %s", channelID, reason)
	if err := s.StartChannel(fresh); err != nil && !s.stopped.Load() {
		logger.Warnf(context.Background(), "[IM] Reload channel %s after %s failed: %v", channelID, reason, err)
	}
}

// StartChannel creates and registers an adapter for the given channel.
// For WebSocket channels with Redis available, only one instance acquires
// the leader lock and opens the connection; other instances periodically
// retry so they can take over if the leader dies.
func (s *Service) StartChannel(channel *IMChannel) error {
	if err := validateChannelTransport(channel); err != nil {
		return err
	}
	if s.stopped.Load() {
		return fmt.Errorf("im service is stopped")
	}

	s.mu.Lock()
	s.stopLeaderRetryLocked(channel.ID)
	factory, ok := s.adapterFactories[channel.Platform]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("no adapter factory for platform: %s", channel.Platform)
	}
	// Stop existing channel if running
	if existing, ok := s.channels[channel.ID]; ok {
		s.stopChannelLocked(channel.ID, existing)
	}
	s.mu.Unlock()

	// For WebSocket / long-poll channels, try leader election to avoid
	// duplicate connections. Only one instance should actively poll or
	// maintain a persistent connection for each channel.
	if (channel.Mode == "websocket" || channel.Mode == "longpoll") && s.redis != nil {
		acquired := s.tryAcquireWSLeader(channel.ID)
		if !acquired {
			logger.Infof(context.Background(),
				"[IM] Channel %s %s owned by another instance, will retry", channel.ID, channel.Mode)
			s.scheduleWSLeaderRetry(channel)
			return nil
		}
	}

	return s.startChannelInternal(channel, factory)
}

// startChannelInternal does the actual adapter creation and registration.
func (s *Service) startChannelInternal(channel *IMChannel, factory AdapterFactory) error {
	// Build the message handler that delegates to HandleMessage with this channel's config
	msgHandler := func(msgCtx context.Context, msg *IncomingMessage) error {
		return s.HandleMessage(msgCtx, msg, channel.ID)
	}

	ctx := context.Background()
	adapter, cancelFn, err := factory(ctx, channel, msgHandler)
	if err != nil {
		s.releaseWSLeader(channel.ID) // release lock on failure
		return fmt.Errorf("create adapter: %w", err)
	}

	// Start leader renewal goroutine for WebSocket / long-poll channels.
	var leaderCancel context.CancelFunc
	if (channel.Mode == "websocket" || channel.Mode == "longpoll") && s.redis != nil {
		leaderCtx, lCancel := context.WithCancel(context.Background())
		leaderCancel = lCancel
		go s.wsLeaderRenewLoop(leaderCtx, channel.ID)
	}

	s.mu.Lock()
	// Stop() may have drained the channel map while the factory was connecting
	// above, since the factory runs unlocked. Re-check under the lock, otherwise
	// this adapter's long connection would outlive process shutdown.
	if s.stopped.Load() {
		s.mu.Unlock()
		if leaderCancel != nil {
			leaderCancel()
		}
		if cancelFn != nil {
			cancelFn()
		}
		s.releaseWSLeader(channel.ID)
		return fmt.Errorf("im service is stopped")
	}
	// Idempotency: another goroutine may have started this channel while
	// factory was running above (factory is called unlocked). Stop the old
	// state before overwriting so its adapter / long connection doesn't leak.
	if existing, ok := s.channels[channel.ID]; ok {
		s.stopChannelLocked(channel.ID, existing)
	}
	s.channels[channel.ID] = &channelState{
		Channel:      channel,
		Adapter:      adapter,
		Cancel:       cancelFn,
		leaderCancel: leaderCancel,
	}
	s.mu.Unlock()

	return nil
}

// StopChannel stops and removes a running channel.
func (s *Service) StopChannel(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLeaderRetryLocked(channelID)
	if cs, ok := s.channels[channelID]; ok {
		s.stopChannelLocked(channelID, cs)
	}
}

func (s *Service) stopLeaderRetryLocked(channelID string) {
	if retry, ok := s.leaderRetries[channelID]; ok {
		retry.cancel()
		delete(s.leaderRetries, channelID)
	}
}

// stopChannelLocked stops a channel and removes it from the map.
// Caller must hold s.mu.
func (s *Service) stopChannelLocked(channelID string, cs *channelState) {
	if cs.leaderCancel != nil {
		cs.leaderCancel()
	}
	if cs.Cancel != nil {
		cs.Cancel()
	}
	delete(s.channels, channelID)
	// For long-poll channels, do NOT release the leader lock immediately.
	// Let it expire naturally via TTL so the old poll goroutine has time to
	// fully drain before another instance takes over. This prevents a brief
	// dual-writer window where both old and new instances process messages.
	// For websocket channels, the connection closes synchronously, so
	// immediate release is safe.
	if cs.Channel != nil && cs.Channel.Mode == "longpoll" {
		logger.Infof(
			context.Background(),
			"[IM] Stopped longpoll channel: id=%s (leader lock will expire via TTL)",
			channelID,
		)
	} else {
		s.releaseWSLeader(channelID)
		logger.Infof(context.Background(), "[IM] Stopped channel: id=%s", channelID)
	}
}

// ── WebSocket leader election ───────────────────────────────────────────────

// tryAcquireWSLeader attempts to acquire the Redis lock for a WebSocket channel.
// Returns true if this instance is now the leader.
func (s *Service) tryAcquireWSLeader(channelID string) bool {
	if s.redis == nil {
		return true // single-instance mode: always leader
	}
	key := RedisKeyLeader + channelID
	ok, err := s.redis.SetNX(context.Background(), key, s.instanceID, wsLeaderTTL).Result()
	if err != nil {
		logger.Warnf(
			context.Background(),
			"[IM] Redis leader election failed for %s: %v; connection will retry without taking leadership",
			channelID, err,
		)
		return false
	}
	return ok
}

// releaseWSLeader releases the Redis leader lock for a WebSocket channel,
// but only if this instance owns it.
func (s *Service) releaseWSLeader(channelID string) {
	if s.redis == nil {
		return
	}
	key := RedisKeyLeader + channelID
	// Only delete if we own it (compare-and-delete via Lua).
	script := redis.NewScript(`
		if redis.call('GET', KEYS[1]) == ARGV[1] then
			return redis.call('DEL', KEYS[1])
		end
		return 0
	`)
	script.Run(context.Background(), s.redis, []string{key}, s.instanceID)
}

// wsLeaderRenewLoop periodically refreshes the leader lock TTL.
// Stops when ctx is cancelled (channel stopped) or if the lock is lost.
func (s *Service) wsLeaderRenewLoop(ctx context.Context, channelID string) {
	key := RedisKeyLeader + channelID
	ticker := time.NewTicker(wsLeaderRenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Only renew if we still own the lock.
			script := redis.NewScript(`
				if redis.call('GET', KEYS[1]) == ARGV[1] then
					redis.call('PEXPIRE', KEYS[1], ARGV[2])
					return 1
				end
				return 0
			`)
			result, err := script.Run(ctx, s.redis, []string{key}, s.instanceID, wsLeaderTTL.Milliseconds()).Int64()
			if err != nil || result == 0 {
				logger.Warnf(context.Background(),
					"[IM] Lost leadership for channel %s, stopping adapter and scheduling recovery", channelID)
				s.handleWSLeadershipLoss(channelID)
				return
			}
			// Still the leader — verify the channel is still active. A
			// delete/disable is served by whichever instance got the HTTP
			// request; without this check the leader would keep the long
			// connection open until process restart. The renew interval
			// bounds the worst-case lag.
			ch, err := s.GetChannelByID(channelID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Infof(context.Background(),
					"[IM] Channel %s deleted; leader stepping down", channelID)
				s.StopChannel(channelID)
				return
			}
			if err != nil {
				// Transient DB error — don't stop a possibly-healthy channel
				// on a DB hiccup. Skip this round; the next renewal re-checks.
				logger.Warnf(
					context.Background(),
					"[IM] DB check failed for channel %s during leader renewal: %v (skipping this round)",
					channelID, err,
				)
				continue
			}
			if !ch.Enabled {
				logger.Infof(context.Background(),
					"[IM] Channel %s disabled; leader stepping down", channelID)
				s.StopChannel(channelID)
				return
			}
			_, cached, running := s.GetChannelAdapter(channelID)
			if !running {
				return
			}
			if !sameChannelRuntimeConfig(cached, ch) {
				logger.Infof(context.Background(),
					"[IM] Channel %s config changed; rebuilding runtime", channelID)
				// StartChannel synchronously stops the old runtime, releases its
				// lease, and then competes to start the fresh configuration. Return
				// because the old renewal context has been cancelled.
				if err := s.StartChannel(ch); err != nil && !s.stopped.Load() {
					logger.Warnf(context.Background(),
						"[IM] Rebuild changed channel %s failed: %v", channelID, err)
				}
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// handleWSLeadershipLoss tears down the adapter that no longer owns its lease
// and puts the enabled channel back into the existing takeover loop. The retry
// loop re-reads the durable channel row before reconnecting, so a concurrent
// delete, disable, or config update cannot resurrect a stale runtime.
func (s *Service) handleWSLeadershipLoss(channelID string) {
	_, channel, running := s.GetChannelAdapter(channelID)
	if !running || channel == nil {
		return
	}

	s.StopChannel(channelID)
	if s.stopped.Load() {
		return
	}
	s.scheduleWSLeaderRetry(channel)
}

func (s *Service) scheduleWSLeaderRetry(channel *IMChannel) {
	retryCtx, cancel := context.WithCancel(context.Background())
	state := &leaderRetryState{cancel: cancel}
	s.mu.Lock()
	if existing, ok := s.leaderRetries[channel.ID]; ok {
		existing.cancel()
	}
	if s.stopped.Load() {
		s.mu.Unlock()
		cancel()
		return
	}
	s.leaderRetries[channel.ID] = state
	s.mu.Unlock()
	go s.wsLeaderRetryLoop(retryCtx, channel, state)
}

// wsLeaderRetryLoop periodically tries to acquire the WebSocket leader lock.
// When it succeeds, it starts the channel adapter. A per-channel retry state
// ensures repeated config events cannot accumulate duplicate retry goroutines.
func (s *Service) wsLeaderRetryLoop(ctx context.Context, channel *IMChannel, state *leaderRetryState) {
	ticker := time.NewTicker(wsLeaderRetryInterval)
	defer ticker.Stop()
	defer func() {
		s.mu.Lock()
		if s.leaderRetries[channel.ID] == state {
			delete(s.leaderRetries, channel.ID)
		}
		s.mu.Unlock()
	}()

	for {
		select {
		case <-ticker.C:
			// Check if channel is already running (another goroutine may have started it).
			if _, _, ok := s.GetChannelAdapter(channel.ID); ok {
				return
			}
			if s.tryAcquireWSLeader(channel.ID) {
				// Re-check the DB before starting: the channel may have been
				// deleted or disabled on another instance while we waited for
				// leadership. The in-memory `channel` is a startup snapshot and
				// won't reflect that, so without this guard we'd resurrect a
				// stopped channel (and reopen its long connection).
				fresh, err := s.GetChannelByID(channel.ID)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// Channel was actually deleted — give up for good.
					s.releaseWSLeader(channel.ID)
					logger.Infof(context.Background(),
						"[IM] Channel %s deleted while waiting for leadership; aborting leader takeover", channel.ID)
					return
				}
				if err != nil {
					// Transient DB error — don't make a destructive decision.
					// Release the lock for this round and retry on the next tick.
					s.releaseWSLeader(channel.ID)
					logger.Warnf(context.Background(),
						"[IM] DB check failed for channel %s during leader takeover: %v (will retry)", channel.ID, err)
					continue
				}
				if !fresh.Enabled {
					s.releaseWSLeader(channel.ID)
					logger.Infof(context.Background(),
						"[IM] Channel %s disabled while waiting for leadership; aborting leader takeover", channel.ID)
					return
				}
				channel = fresh // use latest config (credentials/mode may have changed)
				logger.Infof(context.Background(),
					"[IM] Acquired leadership for channel %s, starting adapter", channel.ID)
				s.mu.RLock()
				factory, ok := s.adapterFactories[channel.Platform]
				s.mu.RUnlock()
				if !ok {
					return
				}
				if err := s.startChannelInternal(channel, factory); err != nil {
					logger.Warnf(context.Background(),
						"[IM] Failed to start channel %s after acquiring leadership: %v", channel.ID, err)
				}
				return
			}
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		}
	}
}

// ── Cross-instance /stop via StreamManager ───────────────────────────────────
//
// The mechanism mirrors the web StopSession flow:
//   1. /stop writes a stop StreamEvent to StreamManager (keyed by sessionID + messageID)
//   2. A per-request watcher polls StreamManager and cancels the context on detection
//
// A Redis marker (im:stop:{userKey}) is kept as a lightweight pre-execution
// check for requests that haven't created an assistant message yet.

// checkAndClearStopMarker checks if a pre-execution /stop marker exists for
// the given userKey. If found, it deletes the marker and returns true.
func (s *Service) checkAndClearStopMarker(ctx context.Context, userKey string) bool {
	if s.redis == nil {
		return false
	}
	stopKey := RedisKeyStop + userKey
	deleted, err := s.redis.Del(ctx, stopKey).Result()
	if err != nil {
		return false
	}
	return deleted > 0
}

// storeInflightMapping writes the (sessionID, assistantMessageID) to Redis so
// that /stop on any instance can look it up and write to StreamManager.
func (s *Service) storeInflightMapping(ctx context.Context, userKey, sessionID, messageID string) {
	if s.redis == nil {
		return
	}
	val := sessionID + ":" + messageID
	if err := s.redis.Set(ctx, RedisKeyInflight+userKey, val, 10*time.Minute).Err(); err != nil {
		logger.Warnf(ctx, "[IM] Failed to store inflight mapping: %v", err)
	}
}

// clearInflightMapping removes the inflight mapping from Redis.
func (s *Service) clearInflightMapping(ctx context.Context, userKey string) {
	if s.redis == nil {
		return
	}
	s.redis.Del(ctx, RedisKeyInflight+userKey)
}

// loadInflightMapping retrieves (sessionID, messageID) from Redis.
func (s *Service) loadInflightMapping(ctx context.Context, userKey string) (sessionID, messageID string, ok bool) {
	if s.redis == nil {
		return "", "", false
	}
	val, err := s.redis.Get(ctx, RedisKeyInflight+userKey).Result()
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(val, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// writeStopEvent writes a stop event to StreamManager, matching the web
// StopSession pattern. The QA watcher goroutine detects it and cancels.
func (s *Service) writeStopEvent(ctx context.Context, sessionID, messageID string) {
	stopEvt := interfaces.StreamEvent{
		ID:        fmt.Sprintf("stop-%d", time.Now().UnixNano()),
		Type:      types.ResponseType(event.EventStop),
		Content:   "",
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
			"reason":     "user_requested",
			"source":     "im",
		},
	}
	if err := s.streamManager.AppendEvent(ctx, sessionID, messageID, stopEvt); err != nil {
		logger.Warnf(ctx, "[IM] Failed to write stop event to StreamManager: %v", err)
	}
}

// watchStreamManagerStop polls StreamManager for stop events and cancels the
// QA context when one is detected. This is the IM equivalent of the web SSE
// handler's stop detection loop. Exits when ctx is done.
func (s *Service) watchStreamManagerStop(ctx context.Context, sessionID, messageID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()

	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, newOffset, err := s.streamManager.GetEvents(ctx, sessionID, messageID, offset)
			if err != nil {
				continue
			}
			for _, evt := range events {
				if evt.Type == types.ResponseType(event.EventStop) {
					logger.Infof(ctx, "[IM] Stop event from StreamManager, cancelling: session=%s message=%s",
						sessionID, messageID)
					cancel()
					return
				}
			}
			offset = newOffset
		}
	}
}

// GetChannelAdapter returns the adapter and channel config for a given channel ID.
func (s *Service) GetChannelAdapter(channelID string) (Adapter, *IMChannel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cs, ok := s.channels[channelID]
	if !ok {
		return nil, nil, false
	}
	return cs.Adapter, cs.Channel, true
}

// EnsureChannelAdapter loads the durable channel row before serving a webhook
// callback. This prevents a replica that missed an invalidation event from
// verifying or processing the callback with stale credentials/configuration.
func (s *Service) EnsureChannelAdapter(channelID string) (Adapter, *IMChannel, error) {
	fresh, err := s.GetChannelByID(channelID)
	if err != nil {
		// A deleted channel may still exist in this replica's runtime map. Do
		// not tear down a healthy runtime on a transient database failure.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.StopChannel(channelID)
		}
		return nil, nil, err
	}
	if !fresh.Enabled {
		s.StopChannel(channelID)
		return nil, fresh, ErrChannelDisabled
	}

	adapter, cached, ok := s.GetChannelAdapter(channelID)
	if !ok || !sameChannelRuntimeConfig(cached, fresh) {
		if err := s.StartChannel(fresh); err != nil {
			return nil, fresh, err
		}
		adapter, cached, ok = s.GetChannelAdapter(channelID)
		if !ok {
			return nil, fresh, fmt.Errorf("channel adapter is not active on this instance")
		}
	}
	return adapter, cached, nil
}

// sameChannelRuntimeConfig compares every field that affects adapter creation
// or message routing. It intentionally does not compare UpdatedAt: PostgreSQL
// timestamp precision can differ from the in-memory value assigned by GORM,
// which would otherwise rebuild webhook adapters on every callback.
func sameChannelRuntimeConfig(cached, fresh *IMChannel) bool {
	if cached == nil || fresh == nil {
		return false
	}
	return cached.ID == fresh.ID &&
		cached.TenantID == fresh.TenantID &&
		cached.AgentID == fresh.AgentID &&
		cached.Platform == fresh.Platform &&
		cached.Enabled == fresh.Enabled &&
		cached.Mode == fresh.Mode &&
		cached.OutputMode == fresh.OutputMode &&
		cached.Locale == fresh.Locale &&
		cached.KnowledgeBaseID == fresh.KnowledgeBaseID &&
		cached.SessionMode == fresh.SessionMode &&
		equalChannelCredentials(cached.Credentials, fresh.Credentials)
}

func equalChannelCredentials(left, right types.JSON) bool {
	left = DecryptCredentialsJSON(left)
	right = DecryptCredentialsJSON(right)
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return string(left) == string(right)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// GetChannelByID loads a channel from the database.
func (s *Service) GetChannelByID(channelID string) (*IMChannel, error) {
	var ch IMChannel
	if err := s.db.WithContext(context.Background()).
		Where("id = ? AND deleted_at IS NULL", channelID).
		First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// GetChannelByIDAndTenant loads a channel from the database, scoped to a specific tenant.
func (s *Service) GetChannelByIDAndTenant(channelID string, tenantID uint64) (*IMChannel, error) {
	var ch IMChannel
	if err := s.db.WithContext(context.Background()).
		Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", channelID, tenantID).
		First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}
