package event

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/Usir233/GENYEE/internal/logger"
)

// EventType represents the type of event in the system.
//
//nolint:revive // EventType is the historical name every caller uses.
type EventType string

// System event type constants emitted across query, retrieval, chat, and agent lifecycles.
const (
	// EventQueryReceived indicates a user query arrived.
	EventQueryReceived   EventType = "query.received"   // 用户查询到达
	EventQueryValidated  EventType = "query.validated"  // 查询验证完成
	EventQueryPreprocess EventType = "query.preprocess" // 查询预处理
	EventQueryRewrite    EventType = "query.rewrite"    // 查询改写
	EventQueryRewritten  EventType = "query.rewritten"  // 查询改写完成

	// EventRetrievalStart indicates retrieval started.
	EventRetrievalStart    EventType = "retrieval.start"    // 检索开始
	EventRetrievalVector   EventType = "retrieval.vector"   // 向量检索
	EventRetrievalKeyword  EventType = "retrieval.keyword"  // 关键词检索
	EventRetrievalEntity   EventType = "retrieval.entity"   // 实体检索
	EventRetrievalComplete EventType = "retrieval.complete" // 检索完成

	// EventRerankStart indicates reranking started.
	EventRerankStart    EventType = "rerank.start"    // 排序开始
	EventRerankComplete EventType = "rerank.complete" // 排序完成

	// EventMergeStart indicates chunk merging started.
	EventMergeStart    EventType = "merge.start"    // 合并开始
	EventMergeComplete EventType = "merge.complete" // 合并完成

	// EventChatStart indicates chat generation started.
	EventChatStart    EventType = "chat.start"    // 聊天生成开始
	EventChatComplete EventType = "chat.complete" // 聊天生成完成
	EventChatStream   EventType = "chat.stream"   // 聊天流式输出

	// EventAgentQuery indicates an agent query started.
	EventAgentQuery    EventType = "agent.query"    // Agent 查询开始
	EventAgentPlan     EventType = "agent.plan"     // Agent 计划生成
	EventAgentStep     EventType = "agent.step"     // Agent 步骤执行
	EventAgentTool     EventType = "agent.tool"     // Agent 工具调用
	EventAgentComplete EventType = "agent.complete" // Agent 完成

	// EventAgentProgress delivers real-time user-facing intermediate updates.
	EventAgentProgress      EventType = "progress"       // User-facing intermediate update
	EventAgentThought       EventType = "thought"        // Agent 思考过程
	EventAgentCommandOutput EventType = "command_output" // bounded command output
	EventAgentToolCall      EventType = "tool_call"      // 工具调用通知
	EventAgentToolResult    EventType = "tool_result"    // 工具结果
	EventAgentReflection    EventType = "reflection"     // Agent 反思
	EventAgentReferences    EventType = "references"     // 知识引用
	EventAgentFinalAnswer   EventType = "final_answer"   // 最终答案

	// EventToolApprovalRequired requests human approval for a dangerous MCP tool (issue #1173).
	EventToolApprovalRequired EventType = "tool_approval_required"
	EventToolApprovalResolved EventType = "tool_approval_resolved"

	// EventAgentPromptRequired requests user input (ask_user). The request and answer travel as the
	// prompt kind's JSON, so a new kind needs no new event.
	EventAgentPromptRequired EventType = "agent_prompt_required"
	EventAgentPromptResolved EventType = "agent_prompt_resolved"

	// EventMCPOAuthRequired emits an in-conversation MCP OAuth authorization prompt when an
	// OAuth-enabled MCP service is invoked before the current user has authorized it.
	EventMCPOAuthRequired EventType = "mcp_oauth_required"
	EventMCPOAuthResolved EventType = "mcp_oauth_resolved"

	// EventError represents a pipeline or agent error event.
	EventError EventType = "error" // 错误事件

	// EventMemoryRecalled carries long-term memories recalled for this turn before the answer streams.
	EventMemoryRecalled EventType = "memory_recalled"

	// EventContextCompacted is emitted when older conversation was summarized
	// away to fit the context window.
	EventContextCompacted EventType = "context_compacted"

	// EventUserMessageInjected is emitted when a message the user appended
	// while the run was in flight was accepted into the running turn (see
	// agent drainSteerMessages).
	EventUserMessageInjected EventType = "user_message_injected"

	// EventSessionTitle notifies listeners of an updated session title.
	EventSessionTitle EventType = "session_title" // 会话标题更新

	// EventStop signals cancellation of in-flight generation.
	EventStop EventType = "stop" // 停止对话生成
)

// Event represents an event in the system
type Event struct {
	ID        string                 // 事件ID (自动生成UUID，用于流式更新追踪)
	Type      EventType              // 事件类型
	SessionID string                 // 会话ID
	Data      interface{}            // 事件数据
	Metadata  map[string]interface{} // 事件元数据
	RequestID string                 // 请求ID
}

// EventHandler is a function that handles events.
//
//nolint:revive // EventHandler is the historical name every caller uses.
type EventHandler func(ctx context.Context, event Event) error

// snapshotEventForHandler returns a per-handler copy of the event with an
// isolated metadata map, for delivery paths that run handlers concurrently
// against one Event value.
func snapshotEventForHandler(event Event) Event {
	e := event
	e.Metadata = maps.Clone(e.Metadata)
	return e
}

// EventBus manages event publishing and subscription.
//
//nolint:revive // EventBus is the historical name every caller uses.
type EventBus struct {
	mu        sync.RWMutex
	handlers  map[EventType][]EventHandler
	asyncMode bool
	asyncWG   sync.WaitGroup
}

// NewEventBus creates a new EventBus instance
func NewEventBus() *EventBus {
	return &EventBus{
		handlers:  make(map[EventType][]EventHandler),
		asyncMode: false,
	}
}

// NewAsyncEventBus creates a new EventBus with async mode enabled
func NewAsyncEventBus() *EventBus {
	return &EventBus{
		handlers:  make(map[EventType][]EventHandler),
		asyncMode: true,
	}
}

// On registers an event handler for a specific event type
// Multiple handlers can be registered for the same event type
func (eb *EventBus) On(eventType EventType, handler EventHandler) {
	if handler == nil {
		return
	}
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.handlers[eventType] = append(eb.handlers[eventType], handler)
}

// Off removes all handlers for a specific event type
func (eb *EventBus) Off(eventType EventType) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	delete(eb.handlers, eventType)
}

// Emit publishes an event to all registered handlers
// Returns error if any handler fails (in sync mode)
// Automatically generates an ID for the event if not provided (from source)
func (eb *EventBus) Emit(ctx context.Context, event Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	eb.mu.RLock()
	handlers := slices.Clone(eb.handlers[event.Type])
	eb.mu.RUnlock()

	if len(handlers) == 0 {
		return nil
	}

	if eb.asyncMode {
		for _, handler := range handlers {
			if handler == nil {
				continue
			}
			h := handler
			e := snapshotEventForHandler(event)
			eb.asyncWG.Add(1)
			go func() {
				defer eb.asyncWG.Done()
				defer func() {
					if r := recover(); r != nil {
						logger.Errorf(ctx, "event handler panic recovered (type=%s): %v", event.Type, r)
					}
				}()
				if err := h(ctx, e); err != nil {
					logger.Errorf(ctx, "async event handler failed (type=%s): %v", event.Type, err)
				}
			}()
		}
		return nil
	}

	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		e := snapshotEventForHandler(event)
		if err := callEventHandlerSafe(ctx, event.Type, handler, e); err != nil {
			return fmt.Errorf("event handler failed for %s: %w", event.Type, err)
		}
	}

	return nil
}

func callEventHandlerSafe(ctx context.Context, eventType EventType, handler EventHandler, event Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("event handler panic (type=%s): %v", eventType, r)
		}
	}()
	return handler(ctx, event)
}

// Wait blocks until all in-flight asynchronous event handlers complete.
func (eb *EventBus) Wait() {
	if eb == nil {
		return
	}
	eb.asyncWG.Wait()
}

// EmitAndWait publishes an event and waits for all handlers to complete.
func (eb *EventBus) EmitAndWait(ctx context.Context, event Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	eb.mu.RLock()
	handlers := slices.Clone(eb.handlers[event.Type])
	eb.mu.RUnlock()

	if len(handlers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(handlers))

	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		wg.Add(1)
		h := handler
		e := snapshotEventForHandler(event)

		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("event handler panic (type=%s): %v", event.Type, r)
				}
			}()
			if err := h(ctx, e); err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return fmt.Errorf("event handler failed for %s: %w", event.Type, err)
		}
	}

	return nil
}

// HasHandlers checks if there are any handlers registered for an event type
func (eb *EventBus) HasHandlers(eventType EventType) bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	handlers, exists := eb.handlers[eventType]
	return exists && len(handlers) > 0
}

// GetHandlerCount returns the number of handlers for a specific event type
func (eb *EventBus) GetHandlerCount(eventType EventType) int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if handlers, exists := eb.handlers[eventType]; exists {
		return len(handlers)
	}
	return 0
}

// Clear removes all event handlers
func (eb *EventBus) Clear() {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.handlers = make(map[EventType][]EventHandler)
}
