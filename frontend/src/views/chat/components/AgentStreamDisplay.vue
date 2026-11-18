<template>
  <div
    ref="rootElement"
    class="agent-stream-display"
    :class="{ 'is-embedded': embeddedMode, 'is-rag-mode': ragMode, 'is-steer-prefix': session?.steerForked }"
  >
    <t-drawer
      v-if="selectedChild && !childAgentTabs"
      attach="body"
      :close-btn="true"
      :z-index="2000"
      :visible="true"
      :header="selectedChild.displayName"
      size="min(680px, 95vw)"
      :footer="false"
      @close="selectedChildId = ''"
    >
      <ChildAgentStream :key="selectedChild.id + ':' + selectedChild.runId" :task="selectedChild" />
    </t-drawer>

    <!-- Collapsed intermediate steps (tree root) -->
    <div v-if="shouldShowCollapsedSteps" class="tree-container">
      <div class="tool-event">
        <div
          class="action-card tree-root"
          role="button"
          tabindex="0"
          :aria-expanded="showIntermediateSteps"
          @click="toggleIntermediateSteps"
          @keydown.enter.prevent="toggleIntermediateSteps"
          @keydown.space.prevent="toggleIntermediateSteps"
        >
          <div class="action-header">
            <div class="action-title">
              <ChatTimelineIcon class="action-title-icon" name="agent" />
              <span class="action-name tree-root-summary" v-html="intermediateStepsSummaryHtml"></span>
              <div class="action-show-icon">
                <ChatTimelineIcon width="14" height="14" :name="showIntermediateSteps ? 'chevron-down' : 'chevron-right'" />
              </div>
            </div>
          </div>
        </div>
      </div>
      <!-- Tree children (intermediate steps) -->
      <div v-if="showIntermediateSteps" class="tree-children">
        <ChatMemoryStep
          v-if="hasMemory"
          :memories="memoryItems"
          :expanded="memoryExpanded"
          :forgetting-id="memoryForgettingId"
          @toggle="toggleMemory"
          @forget="forgetMemory"
        />
        <template v-for="(event, index) in visibleIntermediateEvents" :key="getEventKey(event, index)">
          <div
            v-if="event && event.type"
            class="tree-child"
            :class="{
              'tree-child-last': !isSegmentDone && index === visibleIntermediateEvents.length - 1,
              'tree-child-progress': event.type === 'child_task',
            }"
          >
            <div class="tree-branch"></div>
            <div class="tree-child-content">
              <CollaborationTasks
                v-if="event.type === 'child_task' && event.task"
                :task="event.task"
                :motion-paused="isSegmentDone"
                :disabled="!!embedToken"
                :tool-label="getLocalizedToolName"
                @open="openChildTask"
              />

              <div v-if="event.type === 'plan_task_change'" class="plan-task-change-event">
                <div class="plan-task-change-card">
                  <div class="plan-task-change-content">
                    <strong>{{ $t('agent.taskLabel') }}</strong> {{ event.task }}
                  </div>
                </div>
              </div>

              <div v-if="event.type === 'context_compacted'" class="tool-event">
                <div class="action-card">
                  <div
                    class="action-header"
                    role="button"
                    tabindex="0"
                    :aria-expanded="isEventExpanded(event.event_id)"
                    @click="toggleEvent(event.event_id)"
                    @keydown.enter.prevent="toggleEvent(event.event_id)"
                    @keydown.space.prevent="toggleEvent(event.event_id)"
                  >
                    <div class="action-title">
                      <ChatTimelineIcon class="action-title-icon" name="compaction" />
                      <span class="action-name">{{ $t('agent.contextCompacted') }}</span>
                      <span class="action-summary">{{ compactionSummaryText(event) }}</span>
                    </div>
                  </div>
                  <div v-if="event.summary && isEventExpanded(event.event_id)" class="action-details">
                    <div class="thinking-detail-content markdown-content">
                      <div v-html="renderMarkdownContent(event.summary)"></div>
                    </div>
                  </div>
                </div>
              </div>

              <ProgressUpdates
                v-if="event.type === 'progress' && event.content"
                :content="event.content"
                :history="event.progress_history"
              />

              <div v-if="event.type === 'thinking'" class="tool-event">
                <div class="action-card thinking-event-card" :class="{ 'action-pending': isThinkingActive(event.event_id) }">
                  <div
                    class="action-header"
                    role="button"
                    tabindex="0"
                    :aria-expanded="isEventExpanded(event.event_id)"
                    @click="toggleThinking(event.event_id)"
                    @keydown.enter.prevent="toggleThinking(event.event_id)"
                    @keydown.space.prevent="toggleThinking(event.event_id)"
                  >
                    <div class="action-title" :class="{ 'thinking-inline-title': !event.title && isEventExpanded(event.event_id) }">
                      <ChatTimelineIcon class="action-title-icon" name="lightbulb" :active="!isSegmentDone && isThinkingActive(event.event_id)" />
                      <span v-if="event.title" class="action-name action-preamble-title">{{ event.title }}</span>
                      <div
                        v-else-if="event.content && isEventExpanded(event.event_id)"
                        class="thinking-inline-content markdown-content"
                        :class="{ 'thinking-live-window': isLiveThinking(event.event_id) }"
                      >
                        <div class="thinking-inline-markdown" v-html="renderMarkdownContent(event.content)"></div>
                      </div>
                      <span v-else-if="getThinkingSummary(event)" class="action-summary">{{ getThinkingSummary(event) }}</span>
                    </div>
                  </div>
                  <div v-if="event.title && event.content && isEventExpanded(event.event_id)" class="action-details">
                    <div
                      class="thinking-detail-content markdown-content"
                      :class="{ 'thinking-live-window': isLiveThinking(event.event_id) }"
                    >
                      <div v-html="renderMarkdownContent(event.content)"></div>
                    </div>
                  </div>
                </div>
              </div>

              <div v-else-if="event.type === 'tool_call' && event.tool_name === 'thinking'" class="tool-event">
                <div class="action-card" :class="{ 'action-pending': event.pending || isThinkingActive(event.tool_call_id) }">
                  <div
                    class="action-header"
                    role="button"
                    tabindex="0"
                    :aria-expanded="isEventExpanded(event.tool_call_id)"
                    @click="toggleEvent(event.tool_call_id)"
                    @keydown.enter.prevent="toggleEvent(event.tool_call_id)"
                    @keydown.space.prevent="toggleEvent(event.tool_call_id)"
                  >
                    <div class="action-title">
                      <ChatTimelineIcon class="action-title-icon" name="lightbulb" :active="!isSegmentDone && (event.pending || isThinkingActive(event.tool_call_id))" />
                      <span class="action-name">{{ $t('agent.think') }}</span>
                      <span v-if="event.tool_data?.thought_number" class="action-badge">
                        {{ event.tool_data.thought_number }}/{{ event.tool_data.total_thoughts }}
                      </span>
                      <span v-if="getThinkingSummary(event) && !isEventExpanded(event.tool_call_id)" class="action-summary">
                        {{ getThinkingSummary(event) }}
                      </span>
                    </div>
                  </div>
                  <div v-if="event.tool_data?.thought && isEventExpanded(event.tool_call_id)" class="action-details">
                    <div class="thinking-detail-content markdown-content">
                      <div v-html="renderMarkdownContent(event.tool_data.thought)"></div>
                    </div>
                  </div>
                </div>
              </div>

              <AgentPromptEventCards
                v-else-if="isInlinePromptEvent(event)"
                :event="event"
                :embed-auth-props="embedAuthProps"
                :show-inline-host-approval="showInlineHostApproval(event)"
                :show-inline-tool-approval="showInlineToolApproval(event)"
              />

              <div v-else-if="event.type === 'tool_call'" class="tool-event">
                <div
                  class="action-card"
                  :class="{
                    'action-pending': event.pending,
                    'action-error': event.success === false,
                    'reference-trigger': canOpenToolReferences(event),
                  }"
                  :role="canOpenToolReferences(event) ? 'button' : undefined"
                  :tabindex="canOpenToolReferences(event) ? 0 : undefined"
                  @click="handleActionCardClick(event)"
                  @keydown.enter="handleActionCardClick(event)"
                  @keydown.space.prevent="handleActionCardClick(event)"
                >
                  <div
                    class="action-header"
                    :class="{ 'no-results': !hasActionResult(event) }"
                    :role="!canOpenToolReferences(event) && hasExpandableResults(event) ? 'button' : undefined"
                    :tabindex="!canOpenToolReferences(event) && hasExpandableResults(event) ? 0 : undefined"
                    :aria-expanded="hasExpandableResults(event) ? isEventExpanded(event.tool_call_id) : undefined"
                    @click.stop="handleActionHeaderClick(event)"
                    @keydown.enter.stop.prevent="handleActionHeaderClick(event)"
                    @keydown.space.stop.prevent="handleActionHeaderClick(event)"
                  >
                    <div class="action-title">
                      <ChatTimelineIcon v-if="event.tool_name" class="action-title-icon" :name="getToolIconName(event.tool_name)" :active="!isSegmentDone && event.pending" />
                      <t-tooltip v-if="event.tool_name === 'todo_write' && event.tool_data?.steps" :content="t('agent.updatePlan')" placement="top">
                        <span class="action-name">{{ $t('agent.updatePlan') }}</span>
                      </t-tooltip>
                      <t-tooltip v-else :content="getToolTitle(event)" placement="top">
                        <span class="action-name">{{ getToolTitle(event) }}</span>
                      </t-tooltip>
                      <span v-if="getSandboxDiffStat(event)" class="sandbox-diff-stat">
                        <span v-if="getSandboxDiffStat(event)?.added" class="diff-add">+{{ getSandboxDiffStat(event)?.added }}</span>
                        <span v-if="getSandboxDiffStat(event)?.removed" class="diff-del">-{{ getSandboxDiffStat(event)?.removed }}</span>
                      </span>
                      <button
                        v-if="event.reasoning"
                        type="button"
                        class="action-reasoning-toggle"
                        :class="{ 'is-open': isEventExpanded(event.reasoning.id) }"
                        :title="t('agent.viewReasoning')"
                        :aria-label="t('agent.viewReasoning')"
                        :aria-expanded="isEventExpanded(event.reasoning.id)"
                        @click.stop="toggleEvent(event.reasoning.id)"
                        @keydown.enter.stop
                        @keydown.space.stop
                      >
                        <ChatTimelineIcon name="lightbulb" width="14" height="14" />
                      </button>
                    </div>
                  </div>
                  <AgentToolCallDetails
                    :event="event"
                    :is-event-expanded="isEventExpanded"
                    :render-markdown-content="renderMarkdownContent"
                    :show-page-link-in-step="showPageLinkInStep(event)"
                    :show-page-edit-in-step="showPageEditInStep(event)"
                    :is-page-read-event="isPageReadEvent(event)"
                  />
                </div>
              </div>
            </div>
          </div>
        </template>
        <div v-if="isConversationDone" class="tree-child tree-child-last agent-step-done">
          <div class="tree-branch"></div>
          <div class="tree-child-content">
            <div class="action-card">
              <div class="action-header no-results">
                <div class="action-title">
                  <ChatTimelineIcon class="action-title-icon" name="check-circle" />
                  <span class="action-name">{{ t(wasUserStopped ? 'input.messages.stopSuccess' : 'common.finish') }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Event Stream (non-tree mode: before answer starts, or answer events) -->
    <div
      v-if="displayEvents.length > 0 || showMemoryRow || showAgentActivityIndicator || childActivity"
      class="streaming-steps-container"
      :class="{ 'is-streaming-timeline': showStreamingTimeline }"
    >
      <ChatMemoryStep
        v-if="showMemoryRow"
        class="event-item"
        :memories="memoryItems"
        :expanded="memoryExpanded"
        :is-last="memoryIsLast"
        :forgetting-id="memoryForgettingId"
        @toggle="toggleMemory"
        @forget="forgetMemory"
      />
      <div v-if="earlierStepsCount > 0" class="event-item tree-child earlier-steps-node">
        <div class="tree-branch"></div>
        <div class="tree-child-content">
          <div class="action-card">
            <div
              class="action-header"
              role="button"
              tabindex="0"
              :aria-expanded="earlierStepsExpanded"
              @click="earlierStepsExpanded = !earlierStepsExpanded"
              @keydown.enter.prevent="earlierStepsExpanded = !earlierStepsExpanded"
              @keydown.space.prevent="earlierStepsExpanded = !earlierStepsExpanded"
            >
              <div class="action-title">
                <ChatTimelineIcon class="action-title-icon" name="ellipsis" />
                <span class="action-name">{{ t('agent.earlierSteps', { count: earlierStepsCount }) }}</span>
                <div class="action-show-icon">
                  <ChatTimelineIcon width="14" height="14" :name="earlierStepsExpanded ? 'chevron-down' : 'chevron-right'" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <template v-for="(event, index) in timelineEvents" :key="getEventKey(event, index)">
        <div
          v-if="event && event.type && !foldedStepKeys.has(getEventKey(event, index))"
          class="event-item"
          :class="{
            'event-answer': event.type === 'answer',
            'tree-child': isStreamingTimelineEvent(event),
            'tree-child-progress': event.type === 'child_task',
            'tree-child-last': isStreamingTimelineEvent(event) && !showAgentActivityIndicator && !childActivity && index === lastStreamingTimelineEventIndex,
          }"
        >
          <div v-if="isStreamingTimelineEvent(event)" class="tree-branch"></div>
          <div :class="{ 'tree-child-content': isStreamingTimelineEvent(event) }">
            <CollaborationTasks
              v-if="event.type === 'child_task' && event.task"
              :task="event.task"
              :motion-paused="isSegmentDone"
              :disabled="!!embedToken"
              :tool-label="getLocalizedToolName"
              @open="openChildTask"
            />

            <div v-if="event.type === 'plan_task_change'" class="plan-task-change-event">
              <div class="plan-task-change-card">
                <div class="plan-task-change-content">
                  <strong>{{ $t('agent.taskLabel') }}</strong> {{ event.task }}
                </div>
              </div>
            </div>

            <div v-if="event.type === 'context_compacted'" class="tool-event">
              <div class="action-card">
                <div
                  class="action-header"
                  role="button"
                  tabindex="0"
                  :aria-expanded="isEventExpanded(event.event_id)"
                  @click="toggleEvent(event.event_id)"
                  @keydown.enter.prevent="toggleEvent(event.event_id)"
                  @keydown.space.prevent="toggleEvent(event.event_id)"
                >
                  <div class="action-title">
                    <ChatTimelineIcon class="action-title-icon" name="compaction" />
                    <span class="action-name">{{ $t('agent.contextCompacted') }}</span>
                    <span class="action-summary">{{ compactionSummaryText(event) }}</span>
                  </div>
                </div>
                <div v-if="event.summary && isEventExpanded(event.event_id)" class="action-details">
                  <div class="thinking-detail-content markdown-content">
                    <div v-html="renderMarkdownContent(event.summary)"></div>
                  </div>
                </div>
              </div>
            </div>

            <ProgressUpdates
              v-if="event.type === 'progress' && event.content"
              :content="event.content"
              :history="event.progress_history"
            />

            <div v-if="event.type === 'thinking'" class="tool-event">
              <div class="action-card thinking-event-card" :class="{ 'action-pending': isThinkingActive(event.event_id) }">
                <div
                  class="action-header"
                  role="button"
                  tabindex="0"
                  :aria-expanded="isEventExpanded(event.event_id)"
                  @click="toggleThinking(event.event_id)"
                  @keydown.enter.prevent="toggleThinking(event.event_id)"
                  @keydown.space.prevent="toggleThinking(event.event_id)"
                >
                  <div class="action-title" :class="{ 'thinking-inline-title': !event.title && isEventExpanded(event.event_id) }">
                    <ChatTimelineIcon class="action-title-icon" name="lightbulb" :active="!isSegmentDone && isThinkingActive(event.event_id)" />
                    <span v-if="event.title" class="action-name action-preamble-title">{{ event.title }}</span>
                    <div
                      v-else-if="event.content && isEventExpanded(event.event_id)"
                      class="thinking-inline-content markdown-content"
                      :class="{ 'thinking-live-window': isLiveThinking(event.event_id) }"
                    >
                      <div class="thinking-inline-markdown" v-html="renderMarkdownContent(event.content)"></div>
                    </div>
                    <span v-else-if="getThinkingSummary(event) && !isEventExpanded(event.event_id)" class="action-summary">
                      {{ getThinkingSummary(event) }}
                    </span>
                  </div>
                </div>
                <div v-if="event.title && event.content && isEventExpanded(event.event_id)" class="action-details">
                  <div
                    class="thinking-detail-content markdown-content"
                    :class="{ 'thinking-live-window': isLiveThinking(event.event_id) }"
                  >
                    <div v-html="renderMarkdownContent(event.content)"></div>
                  </div>
                </div>
              </div>
            </div>

            <AgentPromptEventCards
              v-else-if="isInlinePromptEvent(event)"
              :event="event"
              :embed-auth-props="embedAuthProps"
              :show-inline-host-approval="showInlineHostApproval(event)"
              :show-inline-tool-approval="showInlineToolApproval(event)"
            />

            <div v-else-if="event.type === 'tool_call' && event.tool_name === 'thinking'" class="tool-event">
              <div class="action-card" :class="{ 'action-pending': event.pending || isThinkingActive(event.tool_call_id) }">
                <div
                  class="action-header"
                  role="button"
                  tabindex="0"
                  :aria-expanded="isEventExpanded(event.tool_call_id)"
                  @click="toggleEvent(event.tool_call_id)"
                  @keydown.enter.prevent="toggleEvent(event.tool_call_id)"
                  @keydown.space.prevent="toggleEvent(event.tool_call_id)"
                >
                  <div class="action-title">
                    <ChatTimelineIcon class="action-title-icon" name="lightbulb" :active="!isSegmentDone && (event.pending || isThinkingActive(event.tool_call_id))" />
                    <span class="action-name">{{ $t('agent.think') }}</span>
                    <span v-if="event.tool_data?.thought_number" class="action-badge">
                      {{ event.tool_data.thought_number }}/{{ event.tool_data.total_thoughts }}
                    </span>
                    <span v-if="getThinkingSummary(event) && !isEventExpanded(event.tool_call_id)" class="action-summary">
                      {{ getThinkingSummary(event) }}
                    </span>
                  </div>
                </div>
                <div v-if="event.tool_data?.thought && isEventExpanded(event.tool_call_id)" class="action-details">
                  <div class="thinking-detail-content markdown-content">
                    <div v-html="renderMarkdownContent(event.tool_data.thought)"></div>
                  </div>
                </div>
              </div>
            </div>

            <AgentAnswerSection
              v-else-if="event.type === 'answer' && (event.done || (event.content && event.content.trim()))"
              :event="event"
              :rendered-answer-html="renderAnswerContent(event === activeAnswerEventRef ? typedAnswer : event.content)"
              :is-pinned-answer="event === pinnedAnswerEvent"
              :pinned-page-links="pinnedPageLinks"
              :pinned-page-edits="pinnedPageEdits"
              :answer-fully-rendered="answerFullyRendered"
              :has-artifacts="hasArtifacts"
              :artifacts-collecting="artifactsCollecting"
              :artifact-button-collecting="artifactButtonCollecting"
              :artifact-arrived="artifactArrived"
              :artifact-count="artifactCount"
              :can-fork="canFork"
              :can-rewind="canRewind"
              :embedded-mode="embeddedMode"
              :show-request-info="showRequestInfo"
              :is-conversation-done="isConversationDone"
              :session="session"
              :session-id="sessionId"
              :follow-up-loading="followUpLoading"
              @fork="emitFork"
              @rewind="emitRewind"
              @copy="handleCopyAnswer"
              @add-to-knowledge="handleAddToKnowledge"
              @open-artifacts="openArtifactDrawer()"
              @artifact-arrive-end="onArtifactArriveEnd"
            />

            <div v-else-if="event.type === 'tool_call'" class="tool-event">
              <div
                class="action-card"
                :class="{
                  'action-pending': event.pending,
                  'action-error': event.success === false,
                  'reference-trigger': canOpenToolReferences(event),
                }"
                :role="canOpenToolReferences(event) ? 'button' : undefined"
                :tabindex="canOpenToolReferences(event) ? 0 : undefined"
                @click="handleActionCardClick(event)"
                @keydown.enter="handleActionCardClick(event)"
                @keydown.space.prevent="handleActionCardClick(event)"
              >
                <div
                  class="action-header"
                  :class="{ 'no-results': !hasActionResult(event) }"
                  :role="!canOpenToolReferences(event) && hasExpandableResults(event) ? 'button' : undefined"
                  :tabindex="!canOpenToolReferences(event) && hasExpandableResults(event) ? 0 : undefined"
                  :aria-expanded="hasExpandableResults(event) ? isEventExpanded(event.tool_call_id) : undefined"
                  @click.stop="handleActionHeaderClick(event)"
                  @keydown.enter.stop.prevent="handleActionHeaderClick(event)"
                  @keydown.space.stop.prevent="handleActionHeaderClick(event)"
                >
                  <div class="action-title">
                    <ChatTimelineIcon v-if="event.tool_name" class="action-title-icon" :name="getToolIconName(event.tool_name)" :active="!isSegmentDone && event.pending" />
                    <t-tooltip v-if="event.tool_name === 'todo_write' && event.tool_data?.steps" :content="t('agent.updatePlan')" placement="top">
                      <span class="action-name">{{ $t('agent.updatePlan') }}</span>
                    </t-tooltip>
                    <t-tooltip v-else :content="getToolTitle(event)" placement="top">
                      <span class="action-name">{{ getToolTitle(event) }}</span>
                    </t-tooltip>
                    <span v-if="getSandboxDiffStat(event)" class="sandbox-diff-stat">
                      <span v-if="getSandboxDiffStat(event)?.added" class="diff-add">+{{ getSandboxDiffStat(event)?.added }}</span>
                      <span v-if="getSandboxDiffStat(event)?.removed" class="diff-del">-{{ getSandboxDiffStat(event)?.removed }}</span>
                    </span>
                    <button
                      v-if="event.reasoning"
                      type="button"
                      class="action-reasoning-toggle"
                      :class="{ 'is-open': isEventExpanded(event.reasoning.id) }"
                      :title="t('agent.viewReasoning')"
                      :aria-label="t('agent.viewReasoning')"
                      :aria-expanded="isEventExpanded(event.reasoning.id)"
                      @click.stop="toggleEvent(event.reasoning.id)"
                      @keydown.enter.stop
                      @keydown.space.stop
                    >
                      <ChatTimelineIcon name="lightbulb" width="14" height="14" />
                    </button>
                  </div>
                </div>
                <AgentToolCallDetails
                  :event="event"
                  :is-event-expanded="isEventExpanded"
                  :render-markdown-content="renderMarkdownContent"
                  :show-page-link-in-step="showPageLinkInStep(event)"
                  :show-page-edit-in-step="showPageEditInStep(event)"
                  :is-page-read-event="isPageReadEvent(event)"
                />
              </div>
            </div>
          </div>
        </div>
      </template>
      <div v-if="showRequestInfo && isConversationDone && !hasDoneAnswerContent" class="answer-toolbar">
        <ChatRequestInfoButton :session="session" :session-id="sessionId" />
      </div>
      <!-- One live tail node shares the timeline trunk, icon alignment and shimmer. -->
      <div
        v-if="showAgentActivityIndicator || childActivity"
        class="tree-child tree-child-last streaming-loading-node"
        role="status"
        aria-live="polite"
      >
        <div class="tree-branch"></div>
        <div class="tree-child-content">
          <div class="action-card action-pending">
            <div class="action-header no-results">
              <div class="action-title">
                <ChatTimelineIcon class="action-title-icon" name="lightbulb" :active="!isSegmentDone" />
                <span class="action-name">{{ t(childActivity === 'approval' ? 'agentStream.collaboration.status.waiting' : childActivity ? 'agentStream.collaboration.waiting' : 'chat.thinkingAlt') }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
  <!-- 引用 hover 浮层（与历史消息共用同一组件） -->
  <ChatCitationFloat :float="citationFloat" :on-enter="cancelCitationClose" :on-leave="scheduleCitationClose" />

  <!-- Image Preview -->
  <PicturePreview v-if="imagePreviewVisible" :reviewImg="imagePreviewVisible" :reviewUrl="imagePreviewUrl" @closePreImg="closeImagePreview" />

  <!-- Wiki Page Detail Drawer -->
  <AgentWikiPageDrawer
    v-model:visible="wikiDrawerVisible"
    :page="wikiDrawerPage"
    :content-html="wikiDrawerContent"
    :wiki-graph-href="wikiGraphHref"
    :get-type-theme="getTypeTheme"
    :get-type-label="getTypeLabel"
    @body-el="(el) => (wikiDrawerBodyRef = el)"
    @body-click="handleWikiDrawerClick"
  />
  <ChatArtifactsDrawer
    v-if="hasArtifacts && useArtifactDrawer && sessionIdForArtifacts && messageIdForArtifacts"
    v-model:visible="showArtifactDrawer"
    :session-id="sessionIdForArtifacts"
    :message-id="messageIdForArtifacts"
    :artifacts="liveArtifacts"
    :preview-index="artifactPreviewIndex"
  />
</template>

<script setup lang="ts">
import 'katex/dist/katex.min.css';
import { computed, defineAsyncComponent, inject, nextTick, onBeforeUnmount, onMounted, onUpdated, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import ChatCitationFloat from '@/components/ChatCitationFloat.vue';
import ChatRequestInfoButton from '@/components/ChatRequestInfoButton.vue';
import ChatTimelineIcon from '@/components/ChatTimelineIcon.vue';
import PicturePreview from '@/components/PicturePreview.vue';
import { useArtifactArriveMotion } from '@/composables/useArtifactArriveMotion';
import { useChatCitationPopover } from '@/composables/useChatCitationPopover';
import { useChatMemoryRow, type UsedMemory } from '@/composables/useChatMemoryRow';
import { useChatReferencesDrawer } from '@/composables/useChatReferencesDrawer';
import { useChatSandboxPanel } from '@/composables/useChatSandboxPanel';
import { chatReadingHistoryKey } from '@/composables/useChatScrollFollow';
import { childAgentTabsKey, childAgentTaskLookupKey } from '@/composables/useChildAgentTabs';
import { useChildAgentViewer } from '@/composables/useChildAgentViewer';
import i18n from '@/i18n';
import { configureMarkedForChatMarkdown } from '@/utils/chatMarkdownRenderer';
import { collaborationTasks } from '@/utils/collaborationDisplay';
import { unwrapFinalAnswerWrappers } from '@/utils/finalAnswer';
import { enhanceMarkdownContainer, ensureMermaidInitialized } from '@/utils/mermaidShared';
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess';
import type { KnowledgeReferenceLike, ReferenceHighlightTarget } from '@/utils/referenceSources';
import { hydrateArtifactImages } from '@/utils/sandboxArtifactRefs';
import { hydrateProtectedFileImages, safeMarkdownToHTML } from '@/utils/security';
import { isCollectingSkillArtifacts } from '@/utils/skillArtifacts';
import { getSandboxDiffStat } from '@/utils/skillToolDisplay';
import { persistedAssistantId } from '@/utils/steerStreamFork';
import { useAgentAnswerMarkdown } from '../composables/useAgentAnswerMarkdown';
import { useAgentTimelineEvents } from '../composables/useAgentTimelineEvents';
import { TOOL_NAME_KEYS, useAgentToolPresentation } from '../composables/useAgentToolPresentation';
import { useAgentWikiCitations } from '../composables/useAgentWikiCitations';
import type { AgentSessionData, AgentTimelineEvent, IndexedSessionArtifact } from '../types/agentStream';
import ChatArtifactsDrawer from './ChatArtifactsDrawer.vue';
import ChatMemoryStep from './ChatMemoryStep.vue';
import CollaborationTasks from './CollaborationTasks.vue';
import ProgressUpdates from './ProgressUpdates.vue';
import AgentAnswerSection from './stream/AgentAnswerSection.vue';
import AgentPromptEventCards from './stream/AgentPromptEventCards.vue';
import AgentToolCallDetails from './stream/AgentToolCallDetails.vue';
import AgentWikiPageDrawer from './stream/AgentWikiPageDrawer.vue';

const ChildAgentStream = defineAsyncComponent(() => import('./ChildAgentStream.vue'));

const props = defineProps<{
  session: AgentSessionData;
  sessionId?: string;
  userQuery?: string;
  embeddedMode?: boolean;
  artifactsInDrawer?: boolean;
  embedChannelId?: string;
  embedToken?: string;
  embedSessionSig?: string;
  embedVisitorId?: string;
  ragMode?: boolean;
  followUpLoading?: boolean;
  canFork?: boolean;
  canRewind?: boolean;
  deferToolApprovals?: boolean;
}>();

const emit = defineEmits<{
  (event: 'render-complete-change', ready: boolean): void;
  (event: 'fork', messageId: string): void;
  (event: 'rewind', messageId: string): void;
}>();

const readingHistory = inject(chatReadingHistoryKey, ref(false));
const router = useRouter();
const route = useRoute();
const { t } = useI18n();

ensureMermaidInitialized();
configureMarkedForChatMarkdown();

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
const ID_LABEL_RE = /\b(knowledge_base_id|knowledge_id|chunk_id|knowledge_base_ids)\s*[:=]\s*/gi;

const sanitizeForDisplay = (text: string): string => {
  if (!text) return text;
  let result = text;
  for (const [name, i18nKey] of Object.entries(TOOL_NAME_KEYS)) {
    result = result.replaceAll(name, i18n.global.t(i18nKey));
  }
  result = result.replace(/\bmcp_([a-z0-9_]+)/g, (_match, rest: string) => {
    const parts = rest.split('_');
    return parts.map((p: string) => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
  });
  result = result.replace(ID_LABEL_RE, '');
  result = result.replace(UUID_RE, '');
  result = result.replace(/(?<!`)`[ \t]*`(?!`)/g, '');
  result = result.replace(/\(\s*\)/g, '');
  return result;
};
void sanitizeForDisplay;

// 根元素引用
const rootElement = ref<HTMLElement | null>(null);
const citationKeydownListeners = new WeakMap<HTMLElement, EventListener>();

const imagePreviewVisible = ref(false);
const imagePreviewUrl = ref('');
const openImagePreview = (url: string) => {
  imagePreviewUrl.value = url;
  imagePreviewVisible.value = true;
};
const closeImagePreview = () => {
  imagePreviewVisible.value = false;
};

const {
  getToolIconName,
  getLocalizedToolName,
  getToolReferenceItems,
  hasExpandableResults,
  compactionSummaryText,
  getToolTitle,
} = useAgentToolPresentation(t);

const canFork = computed(() => props.canFork === true && !props.embeddedMode);
const canRewind = computed(() => props.canRewind === true && !props.embeddedMode);
const emitFork = () => {
  const messageId = persistedAssistantId(props.session) || String(props.session?.id || '');
  if (messageId) emit('fork', messageId);
};
const emitRewind = () => {
  const messageId = persistedAssistantId(props.session) || String(props.session?.id || '');
  if (messageId) emit('rewind', messageId);
};

const embedAuthProps = computed(() => ({
  embeddedMode: props.embeddedMode,
  embedChannelId: props.embedChannelId,
  embedToken: props.embedToken,
  embedSessionId: props.sessionId,
  embedSessionSig: props.embedSessionSig,
  embedVisitorId: props.embedVisitorId,
}));

const showRequestInfo = computed(
  () => !props.embeddedMode && !!(props.session?.request_id || props.session?.id),
);

const {
  memoryItems,
  hasMemory,
  expanded: memoryExpanded,
  forgettingId: memoryForgettingId,
  toggle: toggleMemory,
  forget: forgetMemory,
} = useChatMemoryRow(() => props.session?.used_memories as UsedMemory[] | undefined);

const resolveAssistantMessageId = (session?: AgentSessionData) =>
  String(session?.assistant_message_id || session?.id || '').trim();

const protectedFileAccess = computed<ProtectedFileAccessContext | undefined>(() => {
  if (props.embeddedMode && props.embedChannelId && props.embedToken) {
    return { mode: 'embed', channelId: props.embedChannelId, token: props.embedToken };
  }
  const messageId = resolveAssistantMessageId(props.session);
  if (props.sessionId && messageId) {
    return { mode: 'message', sessionId: props.sessionId, messageId };
  }
  return undefined;
});

const showArtifactDrawer = ref(false);
const sandboxPanel = useChatSandboxPanel();
const useArtifactDrawer = computed(() => props.artifactsInDrawer || props.embeddedMode || !sandboxPanel);
const artifactList = computed<IndexedSessionArtifact[]>(() => {
  const list = props.session?.artifacts || [];
  return list.map((a, i) => ({ index: i, ...a }));
});
const liveArtifacts = computed(() => artifactList.value.filter((a) => !a.deleted_at));
const hasArtifacts = computed(() => liveArtifacts.value.length > 0);
const artifactCount = computed(() => liveArtifacts.value.length);
const { artifactArrived, onArtifactArriveEnd } = useArtifactArriveMotion(artifactCount);
const artifactsCollecting = computed(() => isCollectingSkillArtifacts(props.session));
const artifactButtonCollecting = computed(() => artifactsCollecting.value && !hasArtifacts.value);
const sessionIdForArtifacts = computed(() => props.sessionId ?? '');
const messageIdForArtifacts = computed(() =>
  persistedAssistantId(props.session) || String(props.session?.request_id || ''),
);
// Set when the drawer is opened from an inline artifact card in the answer, so
// it lands on that file's preview instead of the list.
const artifactPreviewIndex = ref<number | null>(null);
function openArtifactDrawer(previewIndex: number | null = null) {
  if (!hasArtifacts.value) return;
  if (sandboxPanel && !useArtifactDrawer.value) {
    if (previewIndex == null) {
      sandboxPanel.toggleArtifacts(messageIdForArtifacts.value);
    } else {
      sandboxPanel.open('artifacts', {
        messageId: messageIdForArtifacts.value,
        previewIndex,
      });
    }
    return;
  }
  artifactPreviewIndex.value = previewIndex;
  showArtifactDrawer.value = true;
}

const artifactRefContext = computed(() => {
  const sessionId = sessionIdForArtifacts.value;
  const messageId = messageIdForArtifacts.value;
  if (!sessionId || !messageId) return null;
  return { sessionId, messageId };
});

const artifactRefLabels = computed(() => ({
  previewHint: t('agent.artifactDrawer.inlinePreviewHint'),
  missingHint: t('agent.artifactDrawer.inlineMissing'),
  deletedHint: t('agent.artifactDrawer.inlineDeleted'),
}));

const {
  float: citationFloat,
  rebind: rebindCitations,
  cancelClose: cancelCitationClose,
  scheduleClose: scheduleCitationClose,
} = useChatCitationPopover(rootElement, {
  getKnowledgeReferences: () => props.session?.knowledge_references,
  embedChannelId: () => (props.embeddedMode ? props.embedChannelId : undefined),
  embedToken: () => (props.embeddedMode ? props.embedToken : undefined),
  sessionId: () => props.sessionId,
});

const referencesDrawer = useChatReferencesDrawer();

const getReferencesForDrawer = (
  refsOverride?: KnowledgeReferenceLike[] | null,
): KnowledgeReferenceLike[] => {
  const messageReferences = refsOverride?.length
    ? refsOverride
    : props.session?.knowledge_references;
  if (messageReferences?.length) return messageReferences;

  return (props.session?.agentEventStream || []).flatMap((event) =>
    getToolReferenceItems(event),
  );
};

const openReferencesDrawer = (
  highlight?: ReferenceHighlightTarget,
  refsOverride?: KnowledgeReferenceLike[] | null,
) => {
  const refs = getReferencesForDrawer(refsOverride);
  if (!referencesDrawer || !refs?.length) return false;
  referencesDrawer.open({
    references: refs,
    highlight: highlight || null,
    messageId: props.session?.id,
  });
  return true;
};

const canOpenToolReferences = (event: AgentTimelineEvent): boolean => getToolReferenceItems(event).length > 0;

const openToolReferences = (event: AgentTimelineEvent): boolean => {
  const refs = getToolReferenceItems(event);
  if (!referencesDrawer || !refs.length) return false;
  referencesDrawer.toggle({
    references: refs,
    highlight: null,
    messageId: props.session?.id,
    sourceKey: `tool:${props.session?.id || 'session'}:${event.tool_call_id || event.event_id || event.tool_name || 'references'}`,
  });
  return true;
};

const {
  wikiDrawerVisible,
  wikiDrawerPage,
  wikiDrawerBodyRef,
  wikiDrawerContent,
  wikiGraphHref,
  getTypeTheme,
  getTypeLabel,
  handleWikiDrawerClick,
  onRootClick,
  onRootKeydown,
  disposeWikiCitations,
} = useAgentWikiCitations({
  getSession: () => props.session,
  isEmbeddedMode: () => Boolean(props.embeddedMode),
  hasEmbedToken: () => Boolean(props.embedToken),
  protectedFileAccess,
  getReferencesForDrawer,
  openReferencesDrawer,
  openArtifactDrawer,
  openImagePreview,
  router,
  route,
  t,
});

const eventStream = computed(() => props.session?.agentEventStream || []);
const childAgentTabs = inject(childAgentTabsKey, null);
const lookupChildTask = inject(childAgentTaskLookupKey, null);
const selectedChildId = useChildAgentViewer();
const openChildTask = (task: ReturnType<typeof collaborationTasks>[number]) => {
  if (childAgentTabs) {
    childAgentTabs.open(task, () =>
      lookupChildTask
        ? lookupChildTask(task)
        : collaborationTasks(props.session.collaborationStatus).find((item) => item.id === task.id),
    );
  } else {
    selectedChildId.value = task.id;
  }
};
const selectedChild = computed(() =>
  collaborationTasks(props.session.collaborationStatus).find((task) => task.id === selectedChildId.value),
);
const hasCollaborationTasks = computed(
  () => !props.ragMode && collaborationTasks(props.session.collaborationStatus).length > 0,
);

const markOverflowingLiveThinking = () => {
  rootElement.value?.querySelectorAll<HTMLElement>('.thinking-live-window').forEach((el) => {
    el.classList.toggle('is-overflowing', el.scrollHeight > el.clientHeight + 1);
  });
};

const {
  showInlineHostApproval,
  showInlineToolApproval,
  isEventExpanded,
  toggleEvent,
  isThinkingActive,
  isLiveThinking,
  toggleThinking,
  showIntermediateSteps,
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
} = useAgentTimelineEvents({
  props,
  eventStream,
  readingHistory,
  hasMemory,
  hasCollaborationTasks,
  t,
  onStreamSettled: async () => {
    markOverflowingLiveThinking();
    await hydrateProtectedFileImages(rootElement.value, protectedFileAccess.value);
    await enhanceMarkdownContainer(rootElement.value);
  },
});

const isInlinePromptEvent = (event: AgentTimelineEvent): boolean =>
  (event.type === 'agent_prompt_required' && (event.kind === 'ask_user' || event.kind === 'secure_form')) ||
  event.type === 'mcp_oauth_required' ||
  showInlineHostApproval(event) ||
  showInlineToolApproval(event);

const toggleIntermediateSteps = () => {
  showIntermediateSteps.value = !showIntermediateSteps.value;
  nextTick(async () => {
    if (rootElement.value) {
      await hydrateProtectedFileImages(rootElement.value, protectedFileAccess.value);
    }
  });
};

const handleActionHeaderClick = (event: AgentTimelineEvent) => {
  if (canOpenToolReferences(event)) {
    openToolReferences(event);
    return;
  }
  if (hasExpandableResults(event) && event.tool_call_id) {
    toggleEvent(event.tool_call_id);
  }
};

const handleActionCardClick = (event: AgentTimelineEvent) => {
  if (!canOpenToolReferences(event)) return;
  openToolReferences(event);
};

const hasActionResult = (event: AgentTimelineEvent): boolean =>
  canOpenToolReferences(event) || hasExpandableResults(event);

const {
  activeAnswerEventRef,
  typedAnswer,
  answerFullyRendered,
  renderAgentMarkdown,
  handleCopyAnswer,
  handleAddToKnowledge,
} = useAgentAnswerMarkdown({
  rootElement,
  eventStream,
  isSegmentDone,
  artifactList,
  artifactRefLabels,
  artifactRefContext,
  protectedFileAccess,
  getSession: () => props.session,
  getUserQuery: () => props.userQuery,
  getReferencesForDrawer,
  onRenderCompleteChange: (ready) => emit('render-complete-change', ready),
  t,
});

onMounted(() => {
  nextTick(async () => {
    const root = rootElement.value;
    if (!root) return;
    root.addEventListener('click', onRootClick, true);
    const keydownListener: EventListener = (evt: Event) => onRootKeydown(evt as KeyboardEvent);
    citationKeydownListeners.set(root, keydownListener);
    root.addEventListener('keydown', keydownListener, true);
    rebindCitations();
    await hydrateProtectedFileImages(rootElement.value, protectedFileAccess.value);
    await hydrateArtifactImages(rootElement.value, artifactRefContext.value);
  });
});

onBeforeUnmount(() => {
  disposeWikiCitations();
  const root = rootElement.value;
  if (!root) return;
  root.removeEventListener('click', onRootClick, true);
  const keydownListener = citationKeydownListeners.get(root);
  if (keydownListener) {
    root.removeEventListener('keydown', keydownListener, true);
    citationKeydownListeners.delete(root);
  }
});

onUpdated(() => {
  nextTick(async () => {
    rebindCitations();
    await hydrateProtectedFileImages(rootElement.value, protectedFileAccess.value);
    await hydrateArtifactImages(rootElement.value, artifactRefContext.value);
  });
});

const renderMarkdownContent = (content: unknown): string => {
  return renderAgentMarkdown(content, safeMarkdownToHTML);
};

const renderAnswerContent = (content: unknown): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  return renderMarkdownContent(unwrapFinalAnswerWrappers(contentStr));
};
</script>

<style lang="less" scoped>
@import './agent-stream-display.less';
</style>

<!-- Inlined @import instead of <style src>: plugin-vue 6.0.6 keys unscoped
     src-style descriptors by the imported file path, so two SFCs sharing the
     same src style (this file and WikiBrowser.vue) overwrite each other's
     descriptor during the build and crash with "reading 'scoped'". -->
<style lang="less">
@import "@/components/css/wiki-graph-drawer.less";
</style>
