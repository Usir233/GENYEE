import { computed, nextTick, ref, watch, type ComputedRef, type Ref } from 'vue';
import { useDeferredTimelineCompletion } from '@/composables/useChatScrollFollow';
import { isAwaitingInput } from '@/composables/useChatStreamHandler';
import {
  attachReasoningToTools,
  earlierStepKeys,
  foldProgressIntoThinking,
  groupProgressUpdates,
} from '@/utils/agentTimelinePresentation';
import { isAssistantPageLink } from '@/utils/assistantPageLink';
import {
  collaborationActivity,
  collaborationTimeline,
  collaborationWaitState,
  countToolCalls,
  isUnresolvedCollaborationPrompt,
} from '@/utils/collaborationDisplay';
import { thinkingEqualsAnswer } from '@/utils/finalAnswer';
import { isAssistantTurnComplete } from '@/utils/steerStreamFork';
import { isHostApprovalEvent, isModelReview } from '../components/hostApproval';
import type { AgentSessionData, AgentTimelineEvent } from '../types/agentStream';
import { formatDuration, sanitizeForDisplay, type TranslateFn } from './useAgentToolPresentation';

export interface AgentTimelinePropsLike {
  session: AgentSessionData;
  ragMode?: boolean;
  deferToolApprovals?: boolean;
}

export interface UseAgentTimelineEventsOptions {
  props: AgentTimelinePropsLike;
  eventStream: ComputedRef<AgentTimelineEvent[]>;
  readingHistory: Ref<boolean>;
  hasMemory: ComputedRef<boolean>;
  hasCollaborationTasks: ComputedRef<boolean>;
  t: TranslateFn;
  onStreamSettled?: () => Promise<void> | void;
}

export function useAgentTimelineEvents(options: UseAgentTimelineEventsOptions) {
  const {
    props,
    eventStream,
    readingHistory,
    hasMemory,
    hasCollaborationTasks,
    t,
    onStreamSettled,
  } = options;

  // Host command cards stay above the chat composer. MCP cards render here
  // unless that composer is already showing them.
  const approvalEvent = (event: { type?: string; kind?: string; host?: unknown; resolved?: boolean }) =>
    event?.type === 'tool_approval_required';

  // Pending host cards sit above the composer. A resolved approval, and every
  // model review (pending or not), stays in the timeline.
  const showInlineHostApproval = (event: { type?: string; kind?: string; host?: unknown; resolved?: boolean }) =>
    approvalEvent(event) && isHostApprovalEvent(event) && (!!event.resolved || isModelReview(event));

  const showInlineToolApproval = (event: { type?: string; kind?: string; host?: unknown; resolved?: boolean }) => {
    if (!approvalEvent(event) || isHostApprovalEvent(event)) return false;
    if (event.resolved) return true;
    return !props.deferToolApprovals;
  };

  const hideApprovalFromTimeline = (event: { type?: string; kind?: string; host?: unknown; resolved?: boolean }) => {
    if (event?.type === 'tool_approval_resolved') return true;
    if (!approvalEvent(event)) return false;
    return !showInlineHostApproval(event) && !showInlineToolApproval(event);
  };

  // Expanded events tracking (for tool calls and thinking events)
  const expandedEvents = ref<Set<string>>(new Set());

  const isEventExpanded = (eventId?: string): boolean => {
    if (!eventId) return false;
    return expandedEvents.value.has(eventId);
  };

  const toggleEvent = (eventId?: string) => {
    if (!eventId) return;
    if (expandedEvents.value.has(eventId)) {
      expandedEvents.value.delete(eventId);
    } else {
      expandedEvents.value.add(eventId);
    }
  };

  // Track IDs of thinking events that are currently "active" (latest, not yet followed by non-thinking)
  const activeThinkingIds = ref<Set<string>>(new Set());
  const activeThinkingVersion = ref(0);

  const isThinkingActive = (eventId?: string): boolean => {
    void activeThinkingVersion.value;
    if (!eventId) return false;
    return activeThinkingIds.value.has(eventId);
  };

  const deferredThinkingFolds = new Set<string>();
  const userExpandedThinking = ref<Set<string>>(new Set());
  const isLiveThinking = (eventId?: string): boolean => !eventId || !userExpandedThinking.value.has(eventId);

  const toggleThinking = (eventId?: string) => {
    if (!eventId) return;
    if (expandedEvents.value.has(eventId) && !userExpandedThinking.value.has(eventId)) {
      userExpandedThinking.value.add(eventId);
      return;
    }
    toggleEvent(eventId);
    if (expandedEvents.value.has(eventId)) userExpandedThinking.value.add(eventId);
    else userExpandedThinking.value.delete(eventId);
  };

  watch(
    [eventStream, readingHistory],
    ([stream]) => {
      if (!stream || !Array.isArray(stream)) return;

      const newActiveIds = new Set<string>();
      let inTrailingThinking = true;
      for (let i = stream.length - 1; i >= 0; i--) {
        const event = stream[i];
        if (!event) continue;

        const isThinking =
          event.type === 'thinking' || (event.type === 'tool_call' && event.tool_name === 'thinking');
        const id = event.type === 'thinking' ? event.event_id : event.tool_call_id;

        if (inTrailingThinking && isThinking && id) {
          newActiveIds.add(id);
          expandedEvents.value.add(id);
        } else if (!isThinking) {
          inTrailingThinking = false;
        }
      }

      for (const oldId of activeThinkingIds.value) {
        if (!newActiveIds.has(oldId)) deferredThinkingFolds.add(oldId);
      }
      if (!readingHistory.value) {
        for (const id of deferredThinkingFolds) {
          if (!newActiveIds.has(id) && !userExpandedThinking.value.has(id)) expandedEvents.value.delete(id);
        }
        deferredThinkingFolds.clear();
      }
      activeThinkingIds.value = newActiveIds;
      activeThinkingVersion.value++;

      if (onStreamSettled) {
        nextTick(async () => {
          await onStreamSettled();
        });
      }
    },
    { immediate: true, deep: true },
  );

  const showIntermediateSteps = ref(false);

  const hasAnswerStarted = computed(() => {
    const stream = eventStream.value;
    if (!stream || !Array.isArray(stream)) return false;
    return stream.some((e) => e.type === 'answer' && !e.superseded && Boolean(e.content?.trim()));
  });

  const agentDurationMs = ref<number>(0);
  watch(
    eventStream,
    (stream) => {
      if (!stream || !Array.isArray(stream)) return;
      if (agentDurationMs.value === 0) {
        const completeEvent = stream.find((e) => e.type === 'agent_complete' && e.total_duration_ms);
        if (completeEvent?.total_duration_ms) {
          agentDurationMs.value = completeEvent.total_duration_ms;
        }
      }
    },
    { deep: true, immediate: true },
  );

  // A steer boundary stops local animation without completing or folding the task.
  // A run awaiting input keeps its steps in the timeline: the card it waits on
  // must stay in view rather than fold into the collapsed steps.
  const isConversationDone = computed(
    () => isAssistantTurnComplete(props.session) && !isAwaitingInput(props.session),
  );
  const timelineCompleted = useDeferredTimelineCompletion(isConversationDone, readingHistory);
  const isSegmentDone = computed(() => Boolean(props.session?.steerForked) || isConversationDone.value);
  const collaborationWait = computed(() => collaborationWaitState(eventStream.value, isSegmentDone.value));
  const childActivity = computed(() =>
    props.ragMode
      ? undefined
      : collaborationActivity(eventStream.value, props.session.collaborationStatus, isSegmentDone.value),
  );

  const wasUserStopped = computed(() => eventStream.value.some((e) => e.type === 'stop'));

  const isThinkingLikeEvent = (event: AgentTimelineEvent): boolean => {
    if (event.type === 'thinking') return true;
    if (event.type === 'tool_call' && event.tool_name === 'thinking') return true;
    return false;
  };

  const getThinkingContent = (event: AgentTimelineEvent): string => {
    if (event.type === 'thinking') return event.content || '';
    if (event.type === 'tool_call' && event.tool_name === 'thinking') {
      return event.tool_data?.thought || event.output || '';
    }
    return '';
  };

  const getThinkingSummary = (event: AgentTimelineEvent): string => {
    const content = getThinkingContent(event);
    if (!content) return '';
    const cleaned = sanitizeForDisplay(content)
      .replace(/^#+\s+/gm, '')
      .replace(/\*\*/g, '')
      .replace(/\*/g, '')
      .replace(/`/g, '')
      .replace(/\n+/g, ' ')
      .trim();
    if (cleaned.length <= 50) return cleaned;
    return `${cleaned.slice(0, 50)}...`;
  };

  const buildFullEventList = (stream: AgentTimelineEvent[]): AgentTimelineEvent[] => {
    const askedCalls = new Set(
      stream
        .filter((e) => e?.type === 'agent_prompt_required' && e.tool_call_id)
        .map((e) => e.tool_call_id as string),
    );
    const validStream = stream.filter((e): e is AgentTimelineEvent => Boolean(e) && typeof e === 'object' && Boolean(e.type));
    let lastTask: string | null = null;
    const result: AgentTimelineEvent[] = [];

    for (let i = 0; i < validStream.length; i++) {
      const event = validStream[i];
      if (event.type === 'tool_call' && event.tool_name === 'todo_write' && event.tool_data?.task) {
        const currentTask = event.tool_data.task;
        if (lastTask === null || currentTask !== lastTask) {
          result.push({
            type: 'plan_task_change',
            task: currentTask as AgentTimelineEvent['task'],
            event_id: `plan-task-change-${event.tool_call_id || i}`,
            timestamp: event.timestamp || Date.now(),
          });
        }
        lastTask = currentTask;
      }

      if (isThinkingLikeEvent(event) && result.length > 0) {
        const prev = result[result.length - 1];
        if (isThinkingLikeEvent(prev)) {
          const prevContent = prev._mergedContent || getThinkingContent(prev);
          const curContent = getThinkingContent(event);

          if (curContent && prevContent && prevContent.includes(curContent)) {
            continue;
          }
          if (curContent && prevContent && curContent.includes(prevContent)) {
            result[result.length - 1] = {
              type: 'thinking',
              event_id: prev.event_id,
              content: curContent,
              thinking: prev.thinking || event.thinking,
              timestamp: prev.timestamp,
              _mergedContent: curContent,
            };
            continue;
          }

          const merged = [prevContent, curContent].filter(Boolean).join('\n\n');
          result[result.length - 1] = {
            type: 'thinking',
            event_id: prev.event_id,
            content: merged,
            thinking: prev.thinking || event.thinking,
            timestamp: prev.timestamp,
            _mergedContent: merged,
          };
          continue;
        }
      }

      result.push(event);
    }

    const folded = collaborationTimeline(
      foldProgressIntoThinking(result),
      props.session.collaborationStatus,
      isSegmentDone.value,
    ).filter((e) => !(e.type === 'tool_call' && e.tool_call_id && askedCalls.has(e.tool_call_id))) as AgentTimelineEvent[];

    return groupProgressUpdates(
      folded.filter((e) => {
        if (e.type !== 'thinking') return true;
        const content = typeof e.content === 'string' ? e.content : '';
        const title = typeof e.title === 'string' ? e.title : '';
        return content.trim().length > 0 || title.trim().length > 0;
      }),
    ) as AgentTimelineEvent[];
  };

  // Should show the collapsed steps indicator (tree root).
  const shouldShowCollapsedSteps = computed(() => {
    if (props.ragMode) return false;
    const hasSteps = intermediateStepsCount.value > 0;
    return hasSteps && timelineCompleted.value;
  });

  // Once the steps collapse, the memory row travels with them into the tree.
  const showMemoryRow = computed(
    () => !props.ragMode && hasMemory.value && !shouldShowCollapsedSteps.value,
  );

  const finalContent = computed(() => {
    const stream = eventStream.value;
    if (!stream || stream.length === 0 || !isSegmentDone.value) {
      return null;
    }

    const answerEvents = stream.filter((e) => e.type === 'answer' && !e.superseded);
    const hasAnswerContent = answerEvents.some((e) => Boolean(e.content?.trim()));

    if (hasAnswerContent) {
      return { type: 'answer' as const };
    }

    if (shouldShowCollapsedSteps.value) {
      return null;
    }
    const wasStopped = stream.some((e) => e.type === 'stop');
    if (wasStopped) {
      return null;
    }

    const thinkingEvents = stream.filter((e) => e.type === 'thinking' && Boolean(e.content?.trim()));
    if (thinkingEvents.length > 0) {
      const lastThinking = thinkingEvents[thinkingEvents.length - 1];
      const doneAnswer = answerEvents.find((e) => e.done === true);
      return {
        type: 'thinking' as const,
        event_id: lastThinking.event_id,
        showAnswerToolbar: !!doneAnswer,
      };
    }

    return null;
  });

  const hiddenThinkingEventIds = computed<Set<string>>(() => {
    const hidden = new Set<string>();
    const stream = eventStream.value;
    if (!stream || !Array.isArray(stream)) return hidden;

    const final = finalContent.value;
    if (final && final.type === 'thinking') {
      const hasRealAnswer = stream.some(
        (e) => e.type === 'answer' && !e.superseded && Boolean(e.content?.trim()),
      );
      if (!hasRealAnswer && final.event_id) {
        hidden.add(final.event_id);
      }
    }

    const answerContent = stream
      .filter((e) => e.type === 'answer' && !e.superseded && e.content)
      .map((e) => e.content)
      .join('');
    if (answerContent.trim()) {
      const merged = buildFullEventList(stream);
      for (const e of merged) {
        if (e.type !== 'thinking' || !e.event_id) continue;
        if (hidden.has(e.event_id)) continue;
        const bodyMatches = Boolean(e.content) && thinkingEqualsAnswer(e.content || '', answerContent);
        const titleOnlyMatches =
          Boolean(e.title) && !e.content?.trim() && thinkingEqualsAnswer(e.title || '', answerContent);
        if (bodyMatches || titleOnlyMatches) {
          hidden.add(e.event_id);
        }
      }
    }

    return hidden;
  });

  const intermediateEvents = computed(() => {
    const stream = eventStream.value;
    if (!stream || !Array.isArray(stream)) return [];
    const result = buildFullEventList(stream);
    const hidden = hiddenThinkingEventIds.value;
    return result.filter((e) => {
      if (e.type === 'answer' || e.type === 'agent_complete') return false;
      if (hideApprovalFromTimeline(e)) return false;
      if (e.type === 'user_message_injected') return false;
      if (e.type === 'thinking' && e.event_id && hidden.has(e.event_id)) return false;
      return true;
    });
  });

  const intermediateStepsCount = computed(() => {
    if (!hasAnswerStarted.value && !isSegmentDone.value && !hasCollaborationTasks.value) return 0;
    return intermediateEvents.value.filter(
      (e) =>
        e.type === 'thinking' ||
        e.type === 'progress' ||
        e.type === 'tool_call' ||
        e.type === 'child_task' ||
        e.type === 'agent_prompt_required',
    ).length;
  });

  const reasoningRoundsCount = computed(() => {
    if (!hasAnswerStarted.value && !isSegmentDone.value && !hasCollaborationTasks.value) return 0;
    return intermediateEvents.value.filter((e) => e.type === 'thinking').length;
  });

  const toolCallsCount = computed(() => {
    if (!hasAnswerStarted.value && !isSegmentDone.value && !hasCollaborationTasks.value) return 0;
    return countToolCalls(eventStream.value);
  });

  const intermediateStepsSummary = computed(() => {
    if (!eventStream.value) return '';
    const rounds = reasoningRoundsCount.value;
    const tools = toolCallsCount.value;
    const elapsed = agentDurationMs.value;

    const parts: string[] = [];
    if (rounds > 0) parts.push(t('agent.reasoningRounds', { rounds }));
    if (tools > 0) parts.push(t('agent.toolCalls', { tools }));
    if (parts.length === 0) parts.push(t('agent.stepsCompleted', { steps: intermediateStepsCount.value }));
    if (elapsed > 0) parts.push(t('agent.durationSuffix', { duration: formatDuration(elapsed) }));

    return parts.join(t('agent.stepSummarySeparator'));
  });

  const intermediateStepsSummaryHtml = computed(() => intermediateStepsSummary.value);

  const canAttachReasoning = (event: AgentTimelineEvent): boolean =>
    event.thinking !== true && !isThinkingActive(event.event_id) && !isEventExpanded(event.event_id);

  const visibleIntermediateEvents = computed(() => attachReasoningToTools(intermediateEvents.value, canAttachReasoning));

  const displayEvents = computed(() => {
    const stream = eventStream.value;
    if (!stream || !Array.isArray(stream)) {
      return [];
    }

    const result = buildFullEventList(stream).filter(
      (e) => e.type !== 'user_message_injected' && !hideApprovalFromTimeline(e),
    );

    if (props.ragMode) {
      return result.filter((e) => e.type === 'answer');
    }

    if (!timelineCompleted.value) {
      return result;
    }

    const foregroundEvents = result.filter((e) => e.type === 'answer');
    if (foregroundEvents.length > 0) {
      return foregroundEvents;
    }

    if (shouldShowCollapsedSteps.value) {
      return [];
    }

    const final = finalContent.value;
    if (!final) {
      return result;
    }

    if (final.type === 'thinking') {
      const thinking = result.find((e) => e.type === 'thinking' && e.event_id === final.event_id);
      if (!thinking || !thinking.content) return result;
      return [{
        type: 'answer',
        event_id: thinking.event_id,
        content: thinking.content,
        done: true,
        _promoted_from_thinking: true,
      }];
    }

    return result;
  });

  // Get unique key for event
  const getEventKey = (event: AgentTimelineEvent | undefined, index: number): string => {
    if (!event) return `event-${index}`;
    if (event.event_id) return `event-${event.event_id}`;
    if (event.type === 'tool_approval_required' && event.pending_id) {
      return `approval-${event.pending_id}`;
    }
    if (event.type === 'mcp_oauth_required' && event.pending_id) {
      return `mcp-oauth-${event.pending_id}`;
    }
    if (event.type === 'agent_prompt_required' && event.pending_id) {
      return `prompt-${event.pending_id}`;
    }
    if (event.tool_call_id) return `tool-${event.tool_call_id}`;
    return `event-${index}-${event.type || 'unknown'}`;
  };

  const timelineEvents = computed(() => attachReasoningToTools(displayEvents.value, canAttachReasoning));

  const hasPendingStreamingActivity = computed(() => {
    return displayEvents.value.some((event) => {
      if (!event) return false;
      if (event.pending === true) return true;
      if (
        event.type === 'thinking' &&
        (event.thinking === true || isThinkingActive(event.event_id))
      ) {
        return true;
      }
      return isUnresolvedCollaborationPrompt(event);
    });
  });

  const showAgentActivityIndicator = computed(() => {
    if (isSegmentDone.value) return false;
    if (props.ragMode || hasAnswerStarted.value) return false;
    if (collaborationWait.value === 'waiting') return false;
    return !hasPendingStreamingActivity.value;
  });

  const isStreamingTimelineEvent = (event?: AgentTimelineEvent | null): boolean => {
    return !isConversationDone.value && Boolean(event?.type) && event?.type !== 'answer';
  };

  const showStreamingTimeline = computed(() => {
    return (
      displayEvents.value.some((event) => isStreamingTimelineEvent(event)) ||
      showAgentActivityIndicator.value ||
      Boolean(childActivity.value)
    );
  });

  const lastStreamingTimelineEventIndex = computed(() => {
    if (isConversationDone.value) return -1;
    for (let i = timelineEvents.value.length - 1; i >= 0; i -= 1) {
      if (isStreamingTimelineEvent(timelineEvents.value[i])) return i;
    }
    return -1;
  });

  const memoryIsLast = computed(
    () => lastStreamingTimelineEventIndex.value === -1 && !showAgentActivityIndicator.value && !childActivity.value,
  );

  const hasDoneAnswerContent = computed(() => {
    const stream = eventStream.value;
    if (!stream || stream.length === 0) return false;
    return stream.some((e) => e.type === 'answer' && e.done && Boolean(e.content?.trim()));
  });

  const isPageLinkEvent = (event?: AgentTimelineEvent | null): boolean =>
    event?.type === 'tool_call' &&
    (event.tool_name === 'open_page' || event.tool_name === 'view_page') &&
    !event.pending &&
    event.success !== false &&
    isAssistantPageLink(event.tool_data);

  const pinnedAnswerEvent = computed(() => {
    if (!isConversationDone.value) return null;
    const events = displayEvents.value;
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i]?.type === 'answer') return events[i];
    }
    return null;
  });

  const pinnedPageLinks = computed(() => {
    if (!pinnedAnswerEvent.value) return [];
    return eventStream.value.filter(isPageLinkEvent);
  });

  const showPageLinkInStep = (event: AgentTimelineEvent): boolean =>
    isPageLinkEvent(event) && pinnedPageLinks.value.length === 0;

  const isPageEditEvent = (event?: AgentTimelineEvent | null): boolean =>
    event?.type === 'tool_call' &&
    event.tool_name === 'fill_page' &&
    !event.pending &&
    event.success !== false &&
    Array.isArray(event.tool_data?.applied) &&
    !!event.tool_call_id;

  const pinnedPageEdits = computed(() => {
    if (!pinnedAnswerEvent.value) return [];
    return eventStream.value.filter(isPageEditEvent);
  });

  const showPageEditInStep = (event: AgentTimelineEvent): boolean =>
    isPageEditEvent(event) && pinnedPageEdits.value.length === 0;

  const isPageReadEvent = (event?: AgentTimelineEvent | null): boolean =>
    event?.type === 'tool_call' &&
    (event.tool_name === 'read_page' || event.tool_name === 'select_tab') &&
    !event.pending &&
    event.success !== false &&
    !!event.tool_data?.location;

  const isStickyStep = (event: AgentTimelineEvent): boolean =>
    event.pending === true ||
    event.type === 'child_task' ||
    isUnresolvedCollaborationPrompt(event) ||
    (event.type === 'thinking' && (event.thinking === true || isThinkingActive(event.event_id)));

  const earlierStepTargets = computed(() =>
    earlierStepKeys(
      timelineEvents.value.map((event, index) => ({
        key: getEventKey(event, index),
        step: isStreamingTimelineEvent(event),
        sticky: isStickyStep(event),
      })),
    ),
  );

  const earlierStepsExpanded = ref(false);
  const foldedStepKeys = ref<Set<string>>(new Set());

  watch(
    [earlierStepTargets, readingHistory],
    ([targets, reading]) => {
      if (!reading && !earlierStepsExpanded.value) foldedStepKeys.value = new Set(targets);
    },
    { immediate: true },
  );

  watch(earlierStepsExpanded, (expanded) => {
    foldedStepKeys.value = expanded ? new Set() : new Set(earlierStepTargets.value);
  });

  const earlierStepsCount = computed(() =>
    earlierStepsExpanded.value ? earlierStepTargets.value.length : foldedStepKeys.value.size,
  );

  return {
    showInlineHostApproval,
    showInlineToolApproval,
    isEventExpanded,
    toggleEvent,
    isThinkingActive,
    isLiveThinking,
    toggleThinking,
    showIntermediateSteps,
    hasAnswerStarted,
    isConversationDone,
    isSegmentDone,
    childActivity,
    wasUserStopped,
    showAgentActivityIndicator,
    isStreamingTimelineEvent,
    showStreamingTimeline,
    lastStreamingTimelineEventIndex,
    hasDoneAnswerContent,
    intermediateStepsSummaryHtml,
    shouldShowCollapsedSteps,
    showMemoryRow,
    memoryIsLast,
    getThinkingSummary,
    visibleIntermediateEvents,
    displayEvents,
    pinnedAnswerEvent,
    pinnedPageLinks,
    showPageLinkInStep,
    pinnedPageEdits,
    showPageEditInStep,
    isPageReadEvent,
    getEventKey,
    timelineEvents,
    earlierStepsExpanded,
    foldedStepKeys,
    earlierStepsCount,
  };
}
