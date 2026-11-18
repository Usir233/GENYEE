<template>
  <div
    class="chat"
    :class="{
      'is-embedded': embeddedMode,
      'has-references-panel': referencesDrawerVisible,
      'has-sandbox-panel': sandboxPanel.visible.value,
    }"
    :style="{
      '--sandbox-panel-width': `${sandboxPanel.width.value}px`,
      '--references-panel-width': `${referencesPanelWidth}px`,
    }"
  >
    <div v-if="!embeddedMode" class="chat-topbar">
      <ChatHeader :session="currentSession" />
      <div v-if="!sandboxPanel.visible.value" class="sandbox-header-toggle">
        <t-tooltip placement="bottom">
          <template #content>{{ t('chatHeader.toggleSandboxPanel') }}</template>
          <button
            type="button"
            class="sandbox-header-toggle__btn"
            :aria-label="t('chatHeader.toggleSandboxPanel')"
            @click="sandboxPanel.open()"
          >
            <svg viewBox="0 0 20 20" width="16" height="16" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
              <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor" stroke-width="1.2" />
              <line x1="12.5" y1="1.5" x2="12.5" y2="18.5" stroke="currentColor" stroke-width="1.2" />
              <line x1="16" y1="7.5" x2="16" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
            </svg>
          </button>
        </t-tooltip>
      </div>
    </div>

    <ChildAgentTabBar
      v-if="!inheritedChildTabs && childTabs.tabs.value.length"
      :tabs="childTabs.tabs.value"
      :active-id="childTabs.activeId.value"
      :attention="promptDock.prompts.value.length > 0"
      @select="childTabs.select"
      @close="childTabs.close"
    />

    <div
      v-show="!childTabs.activeId.value"
      class="chat_thread"
      :style="{ '--chat-composer-height': `${composerHeight}px`, '--chat-scrollbar-gutter': `${scrollbarGutter}px` }"
    >
      <ChatMessageViewport
        ref="viewportRef"
        :messages-list="messagesList"
        :session-id="String(session_id || '')"
        :embedded-mode="embeddedMode"
        :has-current-session="Boolean(currentSession)"
        :is-automation-session="isAutomationSession"
        :history-loading="historyLoading"
        :loading="loading"
        :is-first-enter="isFirstEnter"
        :show-global-typing-indicator="showGlobalTypingIndicator"
        :suggested-questions="suggestedQuestions"
        :suggested-questions-loading="suggestedQuestionsLoading"
        :should-render-assistant-message="shouldRenderAssistantMessage"
        :fork-affordance-of="forkAffordanceOf"
        :can-rewind-message="canRewindMessage"
        @scroll="handleScroll" @scroll-bottom="scrollToBottom"
        @refresh-suggested-questions="fetchSuggestedQuestions"
        @select-suggested-question="handleSuggestedQuestionClick"
        @retry-steer="handleRetrySteer" @remove-steer="handleRemoveSteer"
        @fork="handleFork" @rewind="handleRewind" @ask-assistant="handleAskAssistant"
        @render-complete-change="(session, ready) => handleAnswerRenderComplete(session, ready)"
        @follow-up-select="(session, item) => handleFollowUpSelect(session, item)"
        @follow-up-regenerate="(session) => loadFollowUpSuggestions(session, true, true)"
        @follow-up-impression="(session, set) => recordSuggestionEvent(session, set, 'impression')"
        @follow-up-dismiss="(session, set) => dismissSuggestions(session, set)"
      />

      <div ref="composerElement" class="chat_composer">
        <div v-if="composerApprovals.length" class="host-approval-pop">
          <HostApprovalCard
            v-for="approval in composerApprovals"
            :key="String(approval.pending_id || '')"
            :pending-id="String(approval.pending_id || '')"
            :host="approval.host || { reason: approval.kind === 'host_command' ? 'delete' : '' }"
            :timeout-seconds="approval.timeout_seconds" :requested-at="approval.requested_at"
            :resolved="approval.resolved" :approved="approval.approved"
            :resolve-reason="approval.resolve_reason" :resolve-scope="approval.resolve_scope"
          />
        </div>
        <div class="input-container" :class="{ 'is-embedded': embeddedMode }">
          <transition name="scroll-btn-fade">
            <button v-show="userHasScrolledUp" type="button" class="scroll-to-bottom-btn"
              :aria-label="t('common.backToBottom', 'Scroll to bottom')" @click="onClickScrollToBottom">
              <t-icon name="chevron-down" size="18px" />
            </button>
          </transition>
          <PendingPromptDock :embedded-mode="embeddedMode" />
          <div v-if="isAutomationSession" class="automation-session-notice" role="note">
            <t-icon name="time" size="14px" />
            <span>{{ t('automation.conversationNotice') }}</span>
          </div>
          <InputField
            v-else-if="embeddedMode || currentSession"
            ref="inputFieldRef" :auto-focus="focusComposerOnMount" compact
            :isReplying="isReplying" :composer-locked="composerLocked"
            :sessionId="String(session_id || '')" :assistantMessageId="currentAssistantMessageId"
            :embeddedMode="embeddedMode" :model-id="modelId" :default-model-id="defaultModelId"
            :queuedSteers="steerQueue.filter((item) => item.delivery === 'after')"
            :canSteer="isAgentStreamSession()"
            @send-msg="(query, mid, mentionedItems, imageFiles, attachmentFiles, options) => sendMsg(query, mid, mentionedItems, imageFiles, attachmentFiles, options)"
            @steer-msg="(query, mentionedItems, delivery) => handleSteerMsg(query, mentionedItems, delivery)"
            @promote-steer="handlePromoteSteer" @remove-steer="handleRemoveSteer" @retry-steer="handleRetrySteer"
            @stop-generation="handleStopGeneration" @stop-confirmed="handleStopConfirmed" @stop-failed="handleStopFailed"
            @update:model-id="(id: string) => emit('update:modelId', id)"
          >
            <template v-if="$slots['composer-chips']" #chips>
              <slot name="composer-chips" />
            </template>
            <template v-if="backgroundItems.length" #background-work>
              <BackgroundWorkChip :items="backgroundItems" :cancel-work="onCancelWork" @view="openWorkOutput" />
            </template>
            <template v-if="$slots['composer-controls']" #controls>
              <slot name="composer-controls" />
            </template>
          </InputField>
        </div>
      </div>

      <div v-if="!embeddedMode" class="chat_overlays">
        <BrowserTaskPreview v-if="session_id" :key="String(session_id)" :session-id="String(session_id)" />
        <ChatQuestionMinimap :scroll-container="scrollContainer" :messages="messagesList" @jump="jumpToQuestion" />
      </div>
    </div>

    <div v-for="tab in childTabs.tabs.value" :key="tab.id" v-show="childTabs.activeId.value === tab.id"
      class="chat-child-detail" role="tabpanel" :aria-label="tab.task.displayName">
      <ChildAgentStream :key="tab.task.sessionId + ':' + tab.task.runId" :task="tab.task" :active="childTabs.activeId.value === tab.id" />
    </div>
  </div>

  <KnowledgeBaseEditorModal v-if="!embeddedMode" :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
    :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
    @update:visible="(val: boolean) => (val ? null : uiStore.closeKBEditor())" @success="handleKBEditorSuccess" />
  <ChatReferencesDrawer />
  <ChatAttachmentPreviewDrawer />
  <SandboxSidePanel v-if="!embeddedMode" :session-id="String(session_id || '')"
    :agent-id="useSettingsStoreInstance.selectedAgentId"
    :agent-source-tenant-id="useSettingsStoreInstance.selectedAgentSourceTenantId"
    :shifted="referencesDrawerVisible" :shift-width="referencesPanelWidth"
    :artifacts="sessionArtifacts" :artifacts-collecting="sessionArtifactsCollecting"
    @artifact-deleted="handleArtifactDeleted" />
  <t-dialog v-model:visible="workOutputVisible" :header="viewingWorkTitle" :footer="false" width="720px">
    <BackgroundWorkResults v-if="workOutputVisible && viewingWorkId && session_id"
      :session-id="String(session_id)" :work-id="viewingWorkId" />
  </t-dialog>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import { computed, inject, nextTick, onBeforeMount, onMounted, onUnmounted, provide, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router';
import type { SuggestedQuestion } from '@/api/agent/index';
import { getMessageList, getSession } from '@/api/chat/index';
import { useStream } from '@/api/chat/stream';
import ChatQuestionMinimap from '@/components/chat/ChatQuestionMinimap.vue';
import SandboxSidePanel from '@/components/chat/SandboxSidePanel.vue';
import ChatAttachmentPreviewDrawer from '@/components/ChatAttachmentPreviewDrawer.vue';
import ChatHeader from '@/components/ChatHeader.vue';
import ChatReferencesDrawer from '@/components/ChatReferencesDrawer.vue';
import InputField from '@/components/InputField.vue';
import {
  notifySessionMutation,
  SESSION_MUTATION_EVENT,
  type SessionMutationDetail,
} from '@/components/sessionMutations';
import { useAssistantPageContext } from '@/composables/useAssistantPageContext';
import { provideChatAttachmentPreviewDrawer } from '@/composables/useChatAttachmentPreviewDrawer';
import { provideChatReferencesDrawer } from '@/composables/useChatReferencesDrawer';
import { provideChatSandboxPanel } from '@/composables/useChatSandboxPanel';
import { useChatScrollFollow } from '@/composables/useChatScrollFollow';
import { useChatStreamHandler } from '@/composables/useChatStreamHandler';
import {
  childAgentTabsKey,
  childAgentTaskLookupKey,
  createChildAgentTabs,
  findChildAgentTask,
} from '@/composables/useChildAgentTabs';
import { providePromptDock } from '@/composables/usePromptDock';
import { useKnowledgeBaseCreationNavigation } from '@/hooks/useKnowledgeBaseCreationNavigation';
import { useAssistantStore } from '@/stores/assistant';
import { useBrowserConnectionStore } from '@/stores/browserConnection';
import { useMenuStore } from '@/stores/menu';
import { useSessionActivityStore } from '@/stores/sessionActivity';
import { useSettingsStore } from '@/stores/settings';
import { useUIStore } from '@/stores/ui';
import { clearCitationChunkCache } from '@/utils/citationChunkCache';
import { historyHasMoreAfterAttempt } from '@/utils/historyPagination';
import { respondToPageAction } from '@/utils/pageActionResponder';
import { questionOriginFromSuggestion } from '@/utils/questionOrigin';
import { collectSessionArtifacts, markSessionArtifactDeleted } from '@/utils/sessionArtifacts';
import { isCollectingSkillArtifacts } from '@/utils/skillArtifacts';
import { discardSteerPreview, persistedAssistantId } from '@/utils/steerStreamFork';
import { resumeSuspendedRunKey } from '@/utils/suspendedRun';
import KnowledgeBaseEditorModal from '@/views/knowledge/KnowledgeBaseEditorModal.vue';
import BackgroundWorkChip from './components/BackgroundWorkChip.vue';
import BackgroundWorkResults from './components/BackgroundWorkResults.vue';
import BrowserTaskPreview from './components/BrowserTaskPreview.vue';
import ChatMessageViewport from './components/ChatMessageViewport.vue';
import ChildAgentStream from './components/ChildAgentStream.vue';
import ChildAgentTabBar from './components/ChildAgentTabBar.vue';
import {
  clearComposerApproval,
  isHostApprovalEvent,
  pendingComposerApprovals,
} from './components/hostApproval';
import HostApprovalCard from './components/HostApprovalCard.vue';
import PendingPromptDock from './components/PendingPromptDock.vue';
import { useChatBackgroundWork } from './composables/useChatBackgroundWork';
import { useChatFollowUpSuggestions } from './composables/useChatFollowUpSuggestions';
import { useChatForkRewind } from './composables/useChatForkRewind';
import { useChatIncompleteRecovery } from './composables/useChatIncompleteRecovery';
import {
  prepareOutgoingAttachments,
  resolveOutgoingResourceScope,
} from './composables/useChatOutgoingAttachments';
import { useChatSteerQueue } from './composables/useChatSteerQueue';
import type {
  AttachmentUploadItem,
  ChatMessageRow,
  ChatSessionDetail,
  HostScopeSnapshot,
  InputFieldExposed,
  MentionedItemInput,
  SendMessageOptions,
} from './types/chatView';

const inheritedChildTabs = inject(childAgentTabsKey, null);
const childTabs = inheritedChildTabs || createChildAgentTabs();
provide(childAgentTabsKey, childTabs);
const referencesDrawer = provideChatReferencesDrawer();
provideChatAttachmentPreviewDrawer();
const sandboxPanel = provideChatSandboxPanel();
const { visible: referencesDrawerVisible, panelWidth: referencesPanelWidth } = referencesDrawer;

const props = withDefaults(
  defineProps<{
    session_id?: string;
    agentId?: string;
    embeddedMode?: boolean;
    initialQuery?: string;
    hostScope?: (() => HostScopeSnapshot | null) | null;
    modelId?: string;
    defaultModelId?: string;
  }>(),
  {
    session_id: '',
    agentId: '',
    embeddedMode: false,
    initialQuery: '',
    hostScope: null,
    modelId: '',
    defaultModelId: '',
  },
);
const emit = defineEmits<{
  (event: 'update:modelId', modelId: string): void;
}>();

const usemenuStore = useMenuStore();
const useSettingsStoreInstance = useSettingsStore();

const isAgentStreamSession = (): boolean => {
  if (props.embeddedMode) {
    return !!(props.agentId && props.agentId !== 'builtin-quick-answer');
  }
  return useSettingsStoreInstance.isAgentStreamMode;
};

const uiStore = useUIStore();
const { navigateToKnowledgeBaseList } = useKnowledgeBaseCreationNavigation();
const { t } = useI18n();
const {
  firstQuery,
  firstMentionedItems,
  firstModelId,
  firstImageFiles,
  firstAttachmentFiles,
  firstQuestionOrigin,
} = storeToRefs(usemenuStore);
// Capture before the initial send consumes firstQuery; the child focuses after mounting.
const focusComposerOnMount = Boolean(props.embeddedMode ? props.initialQuery : firstQuery.value);
const { onChunk, error, isStreaming, startStream, stopStream, lastStreamRequest } = useStream();

interface StreamDebugPayload {
  requestId?: string;
  url?: string;
  method?: string;
  body?: unknown;
  sentAt?: string | number;
  sessionId?: string;
}

const pendingStreamDebug = ref<StreamDebugPayload | null>(null);

const route = useRoute();
const router = useRouter();
const session_id = ref<string>(String(props.session_id || route.params.chatid || ''));
watch(session_id, () => childTabs.reset(), { flush: 'sync' });
const currentSession = ref<ChatSessionDetail | null>(null);
let chatLoadVersion = 0;
const isAutomationSession = computed(() => currentSession.value?.kind === 'automation');

const buildStreamDebugPayload = (): StreamDebugPayload | null => {
  const meta = lastStreamRequest.value;
  if (!meta) return null;
  return {
    requestId: meta.requestId,
    url: meta.url,
    method: meta.method,
    body: meta.body,
    sentAt: meta.sentAt,
    sessionId: session_id.value,
  };
};

const attachStreamDebugToMessage = (message?: ChatMessageRow | null) => {
  if (!message) return;
  const payload = pendingStreamDebug.value || buildStreamDebugPayload();
  if (!payload) return;
  if (payload.requestId && !message.request_id) {
    message.request_id = payload.requestId;
  }
  message.debugRequest = payload as unknown as Record<string, unknown>;
};

const loadSessionAndHydrate = async (sid: string) => {
  if (!sid || props.embeddedMode) return;
  const preserveDraft = Boolean(firstQuery.value);
  const version = chatLoadVersion;
  try {
    const sessionRes = await getSession(sid);
    if (sessionRes?.data && version === chatLoadVersion && sid === session_id.value) {
      currentSession.value = sessionRes.data as ChatSessionDetail;
      const lastState = (sessionRes.data as ChatSessionDetail).last_request_state;
      useSettingsStoreInstance.hydrateSessionInputState(lastState, preserveDraft);
    }
  } catch (err: unknown) {
    if (version !== chatLoadVersion || sid !== session_id.value) return;
    console.error('Failed to load session data:', err);
  }
};

const inputFieldRef = ref<InputFieldExposed | undefined>();
const viewportRef = ref<InstanceType<typeof ChatMessageViewport> | null>(null);
const scrollContainer = computed<HTMLElement | null>(() => viewportRef.value?.scrollBoxRef ?? null);
const composerElement = ref<HTMLElement | null>(null);
const composerHeight = ref(0);
const scrollbarGutter = ref(0);
const created_at = ref('');
const limit = ref(20);
const messagesList = reactive<ChatMessageRow[]>([]);

const {
  backgroundItems,
  viewingWorkId,
  workOutputVisible,
  viewingWorkTitle,
  backgroundPoller,
  startBackgroundWork,
  openWorkOutput,
  onCancelWork,
} = useChatBackgroundWork(session_id, messagesList);

provide(childAgentTaskLookupKey, (task) => findChildAgentTask(messagesList, task));
const promptDock = providePromptDock(messagesList);
watch(promptDock.focusRequest, () => childTabs.select(''), { flush: 'sync' });
watch(
  promptDock.prompts,
  (prompts) => {
    childTabs.attention.value = prompts.length > 0;
  },
  { immediate: true },
);

const assistant = useAssistantStore();
const askedMessage = ref({ sessionId: '', messageId: '' });
useAssistantPageContext(() => {
  const asked = askedMessage.value;
  if (props.embeddedMode || !asked.messageId || asked.sessionId !== props.session_id) return null;
  return { page: 'chat', chatSessionId: asked.sessionId, messageId: asked.messageId };
});
function handleAskAssistant(messageId: string) {
  askedMessage.value = { sessionId: props.session_id, messageId };
  assistant.restorePageContext();
  assistant.open();
}

const composerApprovals = computed(() =>
  pendingComposerApprovals.value.filter((item) => item && !item.resolved && isHostApprovalEvent(item)),
);

const sessionArtifacts = computed(() => collectSessionArtifacts(messagesList));
function handleArtifactDeleted({ messageId, index }: { messageId: string; index: number }) {
  markSessionArtifactDeleted(messagesList, messageId, index);
}
const sessionArtifactsCollecting = computed(() =>
  messagesList.some((message) => isCollectingSkillArtifacts(message)),
);

const isReplying = ref(false);
const currentAssistantMessageId = ref('');
const scrollLock = ref(false);
const isFirstEnter = ref(true);
const loading = ref(false);
const historyLoading = ref(true);
const historyLoadingMore = ref(false);
const hasMoreHistory = ref(true);
const fullContent = ref('');
const userHasScrolledUp = ref(false);
const SCROLL_BOTTOM_THRESHOLD = 80;

watch(session_id, () => {
  clearComposerApproval();
});
watch(isReplying, (replying) => {
  if (!replying) clearComposerApproval();
});

watch(
  [composerElement, scrollContainer],
  ([element, scroller], _, onCleanup) => {
    if (!element || !scroller) return;
    const measure = () => {
      composerHeight.value = element.offsetHeight;
      scrollbarGutter.value = scroller.offsetWidth - scroller.clientWidth;
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    observer.observe(scroller);
    onCleanup(() => observer.disconnect());
  },
  { flush: 'post' },
);

const scrollFollow = useChatScrollFollow(
  scrollContainer,
  userHasScrolledUp,
  computed(() => !childTabs.activeId.value),
);
const scrollToBottom = scrollFollow.scrollToBottom;
const onClickScrollToBottom = scrollFollow.resume;

let parentTabScrollTop = 0;
watch(childTabs.activeId, (active, previous) => {
  if (!previous && active) parentTabScrollTop = scrollContainer.value?.scrollTop || 0;
  if (!active) {
    nextTick(() => {
      if (scrollContainer.value) {
        scrollContainer.value.scrollTop = parentTabScrollTop;
        scrollFollow.rememberPosition();
      }
    });
  }
});

const fetchMessageList = (data: { session_id: string; created_at: string; limit: number }) =>
  getMessageList(data) as Promise<{ data?: ChatMessageRow[] }>;

const {
  suggestedQuestions,
  suggestedQuestionsLoading,
  cancelSuggestedQuestionsFetch,
  fetchSuggestedQuestions,
  fetchSuggestedQuestionsIfNeeded,
  debouncedFetchSuggestions,
  handleAnswerRenderComplete,
  loadFollowUpSuggestions,
  recordSuggestionEvent,
  handleFollowUpSelect,
  dismissSuggestions,
  consumePendingSuggestionScope,
} = useChatFollowUpSuggestions({
  sessionIdRef: session_id,
  getLoadVersion: () => chatLoadVersion,
  embeddedMode: () => props.embeddedMode,
  isAutomationSession,
  historyLoading,
  messagesList,
  inputFieldRef,
  sendMsg: (text) => sendMsg(text),
});

watch(
  () => ({
    agentId: useSettingsStoreInstance.selectedAgentId,
    kbs: useSettingsStoreInstance.settings.selectedKnowledgeBases,
    files: useSettingsStoreInstance.settings.selectedFiles,
    tags: useSettingsStoreInstance.settings.selectedTags,
    mcps: useSettingsStoreInstance.settings.selectedMCPServices,
    skills: useSettingsStoreInstance.settings.selectedSkills,
  }),
  debouncedFetchSuggestions,
  { deep: true },
);

const handleSuggestedQuestionClick = (item: SuggestedQuestion) => {
  const options: SendMessageOptions = { questionOrigin: questionOriginFromSuggestion(item) };
  if (inputFieldRef.value?.triggerSend) {
    inputFieldRef.value.triggerSend(item.question, options);
  } else {
    void sendMsg(item.question, '', [], [], [], options);
  }
};

const sessionActivity = useSessionActivityStore();
const activitySessionId = ref('');
watch(
  activitySessionId,
  (id) => {
    if (!props.embeddedMode) sessionActivity.setViewing(id);
  },
  { flush: 'sync' },
);

const {
  findLastMessage,
  shouldRenderAssistantMessage,
  shouldShowGlobalTypingIndicator,
  handleMsgList,
  processStreamChunk,
  prepareForNewOutgoingMessage,
  prepareResumedMessage,
  markInFlightAssistantStopped,
} = useChatStreamHandler({
  messagesList,
  loading,
  isReplying,
  currentAssistantMessageId,
  fullContent,
  isAgentStreamSession,
  scrollToBottom,
  onError: (msg) => MessagePlugin.error(msg),
  onPageAction: (prompt) =>
    void respondToPageAction(
      prompt as unknown as Parameters<typeof respondToPageAction>[0],
      !props.embeddedMode ? 'unavailable' : assistant.pageAccess ? 'enabled' : 'disabled',
      { current: () => router.currentRoute.value.fullPath, push: (location) => router.push(location) },
    ),
  preserveIncompleteStreamReactive: true,
  isFirstEnter,
  scrollContainer,
  debug: import.meta.env.DEV,
  onAfterMsgList: async () => {
    const version = chatLoadVersion;
    activitySessionId.value = String(session_id.value);
    for (const message of messagesList) {
      if (message.role === 'assistant' && message.is_completed && message.suggestionSet === undefined) {
        void loadFollowUpSuggestions(message, false);
      }
    }
    if (!steerQueue.value.length) {
      await hydrateSteerQueue();
    }
    if (version !== chatLoadVersion) return;
    const lastMessage = findLastMessage(
      (message) => message.role === 'assistant' && !message.is_completed,
    );
    const locallyRunning = isReplying.value || isImRecovering.value;
    if (!props.embeddedMode && !locallyRunning && !lastMessage) {
      sessionActivity.update(activitySessionId.value, false);
    }
    if (lastMessage) {
      isReplying.value = true;
      const resumeId = persistedAssistantId(lastMessage);
      currentAssistantMessageId.value = resumeId;
      isAttachingImStream.value = lastMessage.channel === 'im';
      await startStream({
        session_id: session_id.value,
        query: resumeId,
        method: 'GET',
        url: '/api/v1/sessions/continue-stream',
      });
      if (version === chatLoadVersion && !error.value) isAttachingImStream.value = false;
    }
  },
  onAgentQuery: (_data, existingMessage) => {
    pendingStreamDebug.value = buildStreamDebugPayload();
    if (existingMessage) attachStreamDebugToMessage(existingMessage);
  },
  onMessageCreated: (message) => attachStreamDebugToMessage(message),
  onMessageUpdated: (message, payload) => {
    attachStreamDebugToMessage(message);
    if (payload?.is_completed) pendingStreamDebug.value = null;
  },
  onAgentAnswerDone: (message) => {
    attachStreamDebugToMessage(message);
    pendingStreamDebug.value = null;
  },
  onAgentChunkBound: (message) => {
    attachStreamDebugToMessage(message);
    pendingStreamDebug.value = null;
  },
  onUserMessageInjected: (steerId) => {
    dropSteerQueueItem(steerId);
  },
  onGenerationStopped: () => {
    for (const item of steerQueue.value) discardSteerPreview(messagesList, item.steer_id);
    steerQueue.value = [];
  },
  onTurnComplete: (message) => {
    void loadFollowUpSuggestions(message, true);
    void flushSteerAfterTurn(persistedAssistantId(message));
  },
});

const {
  isAttachingImStream,
  isImRecovering,
  stopRecoverPoll,
  recoverIncompleteMessage,
  resumeSuspendedRun,
  isAttachingResumedRun,
} = useChatIncompleteRecovery({
  sessionIdRef: session_id,
  getLoadVersion: () => chatLoadVersion,
  limitRef: limit,
  createdAtRef: created_at,
  messagesList,
  isReplying,
  loading,
  currentAssistantMessageId,
  streamError: error,
  findLastMessage,
  prepareResumedMessage,
  startStream: (params) => startStream(params as unknown as Parameters<typeof startStream>[0]),
  reloadThread: (query) => {
    void getmsgList(query);
  },
  t,
});
provide(resumeSuspendedRunKey, resumeSuspendedRun);

watch(
  [activitySessionId, isReplying, isImRecovering, currentAssistantMessageId],
  () => {
    if (props.embeddedMode || !activitySessionId.value) return;
    sessionActivity.update(
      activitySessionId.value,
      isReplying.value || isImRecovering.value,
      currentAssistantMessageId.value,
    );
  },
  { flush: 'sync' },
);

const composerLockedRef = ref(false);
const {
  steerQueue,
  hydrateSteerQueue,
  handleStopConfirmed,
  handleStopFailed,
  dropSteerQueueItem,
  handleSteerMsg,
  handleRetrySteer,
  handlePromoteSteer,
  handleRemoveSteer,
  flushSteerAfterTurn,
} = useChatSteerQueue({
  sessionIdRef: session_id,
  getLoadVersion: () => chatLoadVersion,
  messagesList,
  isReplying,
  isStreaming,
  loading,
  currentAssistantMessageId,
  composerLocked: composerLockedRef,
  scrollToBottom,
  sendMsg: (value, mid, mentions) => sendMsg(value, mid, mentions),
  startStream: (params) => startStream(params as unknown as Parameters<typeof startStream>[0]),
  t,
});

const {
  forkAffordanceOf,
  canRewindMessage,
  composerLocked,
  applyForkLanding,
  handleFork,
  handleRewind,
} = useChatForkRewind({
  sessionIdRef: session_id,
  embeddedMode: () => props.embeddedMode,
  isAutomationSession,
  currentSession,
  messagesList,
  steerQueue,
  inputFieldRef,
  isReplying,
  isStreaming,
  isImRecovering,
  historyLoading,
  hasMoreHistory,
  createdAtRef: created_at,
  limitRef: limit,
  fetchMessageList,
  handleMsgList,
  router,
  t,
});
watch(
  composerLocked,
  (locked) => {
    composerLockedRef.value = locked;
  },
  { immediate: true, flush: 'sync' },
);

watch(
  historyLoading,
  (isLoading) => {
    if (isLoading) return;
    applyForkLanding();
  },
  { flush: 'post' },
);

const showGlobalTypingIndicator = computed(() =>
  shouldShowGlobalTypingIndicator(messagesList, loading.value, isImRecovering.value),
);

const jumpToQuestion = (id: string) => {
  const root = scrollContainer.value;
  if (!root || !id) return;
  const el = root.querySelector(`[data-message-id="${CSS.escape(id)}"]`);
  if (!el) return;

  const offset = el.getBoundingClientRect().top - root.getBoundingClientRect().top + root.scrollTop;
  const nearEnd = root.scrollHeight - offset < root.clientHeight + SCROLL_BOTTOM_THRESHOLD;
  userHasScrolledUp.value = !nearEnd;

  el.scrollIntoView({ block: 'start', behavior: 'smooth' });
};

const handleKBEditorSuccess = (kbId: string) => {
  navigateToKnowledgeBaseList(kbId);
};

const getmsgList = (
  data: { session_id: string; created_at: string; limit: number },
  isScrollType = false,
  scrollHeight?: number,
) => {
  const sid = String(data?.session_id || session_id.value || '');
  const version = chatLoadVersion;
  const isCurrent = () => version === chatLoadVersion && sid === String(session_id.value || '');
  if (!isCurrent()) return;
  if (isScrollType) {
    if (historyLoadingMore.value || !hasMoreHistory.value) return;
    historyLoadingMore.value = true;
  }
  return fetchMessageList(data)
    .then(async (res) => {
      if (!isCurrent()) return;
      const batch = res?.data;
      if (!batch?.length) {
        if (isScrollType) {
          hasMoreHistory.value = historyHasMoreAfterAttempt(hasMoreHistory.value, {
            status: 'loaded',
            itemCount: 0,
            pageSize: limit.value,
            cursorAdvanced: false,
          });
        }
        return;
      }
      if (!isScrollType) {
        cancelSuggestedQuestionsFetch();
      }
      const nextCursor = batch[0].created_at || '';
      const cursorAdvanced = !(isScrollType && created_at.value && nextCursor === created_at.value);
      hasMoreHistory.value = historyHasMoreAfterAttempt(hasMoreHistory.value, {
        status: 'loaded',
        itemCount: batch.length,
        pageSize: limit.value,
        cursorAdvanced,
      });
      if (!cursorAdvanced) return;
      created_at.value = nextCursor;
      await handleMsgList(batch, isScrollType, scrollHeight);
    })
    .catch((err: unknown) => {
      if (!isCurrent()) return;
      console.error('Failed to load messages:', err);
      if (isScrollType) {
        hasMoreHistory.value = historyHasMoreAfterAttempt(hasMoreHistory.value, {
          status: 'failed',
        });
      }
    })
    .finally(() => {
      if (!isCurrent()) return;
      historyLoading.value = false;
      historyLoadingMore.value = false;
      if (!isScrollType && messagesList.length === 0) {
        void fetchSuggestedQuestionsIfNeeded();
      }
    });
};

watch([() => route.params], async (newvalue) => {
  if (props.embeddedMode) return;
  isFirstEnter.value = true;
  const nextChatId = newvalue[0]?.chatid ? String(newvalue[0].chatid) : '';
  if (nextChatId) {
    stopStream();
    if (!firstQuery.value) {
      scrollLock.value = false;
    }
    messagesList.splice(0);
    steerQueue.value = [];
    session_id.value = nextChatId;
    currentSession.value = null;
    const version = ++chatLoadVersion;
    const sid = session_id.value;
    clearCitationChunkCache();

    historyLoading.value = true;
    historyLoadingMore.value = false;
    hasMoreHistory.value = true;
    created_at.value = '';
    loading.value = false;
    isReplying.value = false;
    currentAssistantMessageId.value = '';
    userHasScrolledUp.value = false;

    useSettingsStoreInstance.restoreDefaultsIfSnapshotted();

    await loadSessionAndHydrate(sid);
    if (version !== chatLoadVersion) return;
    const data = {
      session_id: sid,
      created_at: '',
      limit: limit.value,
    };
    void getmsgList(data);
  }
});

let scrollTopDebounceTimer: ReturnType<typeof setTimeout> | null = null;
const onChatScrollTop = () => {
  if (childTabs.activeId.value) return;
  if (scrollLock.value || historyLoadingMore.value || !hasMoreHistory.value) return;
  if (!scrollContainer.value) return;
  const { scrollTop, scrollHeight } = scrollContainer.value;
  isFirstEnter.value = false;
  if (scrollTop <= 0) {
    const data = {
      session_id: session_id.value,
      created_at: created_at.value,
      limit: limit.value,
    };
    void getmsgList(data, true, scrollHeight);
  }
};
const handleScroll = () => {
  if (childTabs.activeId.value) return;
  scrollFollow.onScroll();
  if (scrollTopDebounceTimer) clearTimeout(scrollTopDebounceTimer);
  scrollTopDebounceTimer = setTimeout(onChatScrollTop, 500);
};

const handleStopGeneration = () => {
  stopStream();
  loading.value = false;
  isReplying.value = false;
  stopRecoverPoll();
  markInFlightAssistantStopped(currentAssistantMessageId.value);
};

async function sendMsg(
  value: string,
  modelId = '',
  mentionedItems: MentionedItemInput[] = [],
  imageFiles: File[] = [],
  attachmentFiles: AttachmentUploadItem[] = [],
  options: SendMessageOptions = {},
) {
  if (!props.embeddedMode && !currentSession.value) {
    const version = chatLoadVersion;
    await loadSessionAndHydrate(session_id.value);
    if (version !== chatLoadVersion || !currentSession.value) return;
  }
  if (composerLocked.value) return;
  const reasoningEffort = props.embeddedMode
    ? undefined
    : useSettingsStoreInstance.reasoningEffortOverride || undefined;
  stopStream();
  prepareForNewOutgoingMessage();
  activitySessionId.value = String(session_id.value);
  isReplying.value = true;
  loading.value = true;
  const selectedAgentId = props.embeddedMode
    ? props.agentId
    : useSettingsStoreInstance.selectedAgentId || '';
  const selectedAgentSourceTenantId = props.embeddedMode
    ? undefined
    : useSettingsStoreInstance.selectedAgentSourceTenantId || undefined;

  const prepared = await prepareOutgoingAttachments({
    sessionId: session_id.value,
    embeddedMode: props.embeddedMode,
    selectedAgentId,
    selectedAgentSourceTenantId,
    imageFiles,
    attachmentFiles,
    t,
  });
  if (!prepared) {
    loading.value = false;
    isReplying.value = false;
    return;
  }
  const { imageAttachments, userImages, attachmentIds, attachmentUploads } = prepared;

  messagesList.push({
    content: value,
    role: 'user',
    mentioned_items: mentionedItems,
    images: userImages,
    attachments: attachmentFiles.map((a) => ({
      id: a.documentId,
      file_name: a.name,
      file_size: a.size,
      file_type: '.' + (a.name.split('.').pop()?.toLowerCase() || ''),
    })),
    channel: 'web',
    created_at: new Date().toISOString(),
  });
  userHasScrolledUp.value = false;
  scrollToBottom(true);

  const agentEnabled = props.embeddedMode
    ? Boolean(props.agentId && props.agentId !== 'builtin-quick-answer')
    : useSettingsStoreInstance.isAgentStreamMode;

  const webSearchEnabled = props.embeddedMode ? true : useSettingsStoreInstance.isWebSearchEnabled;
  const hostScope = props.embeddedMode && props.hostScope ? props.hostScope() : null;
  const { attribution: suggestionAttribution, kbIds: pendingSuggestionKbIds } =
    consumePendingSuggestionScope();
  const { kbIds, knowledgeIds, tagIds, mcpServiceIds, skillNames } = resolveOutgoingResourceScope({
    embeddedMode: props.embeddedMode,
    hostScope,
    selectedKnowledgeBases: useSettingsStoreInstance.settings.selectedKnowledgeBases,
    selectedFiles: useSettingsStoreInstance.settings.selectedFiles,
    pendingSuggestionKbIds,
    mentionedItems,
  });

  const endpoint = agentEnabled ? '/api/v1/agent-chat' : '/api/v1/knowledge-chat';
  const requestMcpServiceIds = agentEnabled ? mcpServiceIds : [];
  const requestSkillNames = agentEnabled ? skillNames : [];

  await startStream({
    session_id: session_id.value,
    knowledge_base_ids: kbIds,
    knowledge_ids: knowledgeIds,
    agent_enabled: agentEnabled,
    agent_id: selectedAgentId,
    agent_source_tenant_id: selectedAgentSourceTenantId,
    web_search_enabled: webSearchEnabled,
    local_browser_enabled: !props.embeddedMode && agentEnabled && useSettingsStoreInstance.isLocalBrowserEnabled && !useBrowserConnectionStore().knownOffline,
    summary_model_id: modelId,
    reasoning_effort: reasoningEffort,
    mcp_service_ids: requestMcpServiceIds,
    skill_names: requestSkillNames,
    tag_ids: tagIds,
    mentioned_items: mentionedItems,
    images: imageAttachments.length > 0 ? imageAttachments : undefined,
    attachment_uploads: attachmentUploads.length > 0 ? attachmentUploads : undefined,
    attachment_ids: attachmentIds.length > 0 ? attachmentIds : undefined,
    query: value,
    suggestion_attribution: suggestionAttribution || undefined,
    question_origin: options?.questionOrigin || undefined,
    page_context: agentEnabled ? hostScope?.pageContext || undefined : undefined,
    assistant_access: agentEnabled ? hostScope?.assistantAccess || undefined : undefined,
    method: 'POST',
    url: endpoint,
  });
}

if (props.embeddedMode) {
  watch(
    [() => assistant.queuedMessage, historyLoading],
    ([message, pending]) => {
      if (!message || pending) return;
      assistant.takeQueuedMessage();
      if (isReplying.value || loading.value) {
        inputFieldRef.value?.prefill?.(message.query);
        return;
      }
      void sendMsg(message.query, inputFieldRef.value?.getComposerModelId?.() || props.modelId);
    },
    { immediate: true },
  );
  watch(
    () => assistant.focusRequest,
    () => {
      inputFieldRef.value?.focusInput?.();
    },
  );
}

watch(error, (newError) => {
  if (!newError) return;
  if (isAttachingResumedRun()) return;
  if (isAttachingImStream.value) {
    isAttachingImStream.value = false;
    recoverIncompleteMessage();
    return;
  }
  MessagePlugin.error(newError);
  isReplying.value = false;
  loading.value = false;
  currentAssistantMessageId.value = '';
});

onChunk((data) => {
  const chunkSessionId = data.session_id || data.data?.session_id;
  if (chunkSessionId && chunkSessionId !== session_id.value) {
    return;
  }
  if (data.response_type === 'session_title') {
    const title = data.content || data.data?.title;
    if (title && data.data?.session_id) {
      const titleSessionId = String(data.data.session_id);
      const nextTitle = String(title);
      usemenuStore.updatasessionTitle(titleSessionId, nextTitle);
      usemenuStore.changeIsFirstSession(false);
      notifySessionMutation({
        sessionId: titleSessionId,
        patch: { title: nextTitle },
      });
    }
    return;
  }
  processStreamChunk(data);
});

const handleSessionMutation = (event: Event) => {
  const detail = (event as CustomEvent<SessionMutationDetail>).detail;
  if (detail?.sessionId !== session_id.value) return;

  if (detail.patch) {
    currentSession.value = {
      ...(currentSession.value || { id: session_id.value }),
      ...detail.patch,
    };
  }
  if (detail.messagesCleared) {
    chatLoadVersion++;
    historyLoading.value = false;
    messagesList.splice(0);
    steerQueue.value = [];
    created_at.value = '';
    hasMoreHistory.value = true;
    historyLoadingMore.value = false;
    void fetchSuggestedQuestionsIfNeeded();
  }
};

onBeforeMount(async () => {
  if (props.embeddedMode) return;
  const agentIdFromQuery = route.query.agent_id ? String(route.query.agent_id) : '';
  const sourceTenantIdFromQuery = route.query.source_tenant_id
    ? String(route.query.source_tenant_id)
    : '';
  if (agentIdFromQuery && sourceTenantIdFromQuery) {
    useSettingsStoreInstance.selectAgent(agentIdFromQuery, sourceTenantIdFromQuery);
  } else if (agentIdFromQuery) {
    useSettingsStoreInstance.selectAgent(agentIdFromQuery, null);
  }
  await loadSessionAndHydrate(session_id.value);
});

onMounted(async () => {
  window.addEventListener(SESSION_MUTATION_EVENT, handleSessionMutation);
  messagesList.splice(0);
  steerQueue.value = [];
  loading.value = false;
  isReplying.value = false;
  startBackgroundWork();

  if (props.embeddedMode && props.initialQuery) {
    scrollLock.value = true;
    historyLoading.value = false;
    void sendMsg(props.initialQuery, inputFieldRef.value?.getComposerModelId?.() || props.modelId);
  } else if (!props.embeddedMode && firstQuery.value) {
    scrollLock.value = true;
    historyLoading.value = false;
    if (firstModelId.value) {
      useSettingsStoreInstance.updateConversationModels({
        summaryModelId: firstModelId.value,
        selectedChatModelId: firstModelId.value,
        rerankModelId: '',
      });
    }
    void sendMsg(
      firstQuery.value,
      firstModelId.value || '',
      (firstMentionedItems.value as MentionedItemInput[] | undefined) || [],
      (firstImageFiles.value as File[] | undefined) || [],
      (firstAttachmentFiles.value as unknown as AttachmentUploadItem[] | undefined) || [],
      { questionOrigin: firstQuestionOrigin.value || undefined },
    );
    usemenuStore.changeFirstQuery('', [], '', [], []);
  } else {
    scrollLock.value = false;
    hasMoreHistory.value = true;
    historyLoadingMore.value = false;
    const data = {
      session_id: session_id.value,
      created_at: '',
      limit: limit.value,
    };
    void getmsgList(data);
  }
});

const clearData = () => {
  chatLoadVersion++;
  cancelSuggestedQuestionsFetch();
  if (!props.embeddedMode) sessionActivity.detach(activitySessionId.value);
  activitySessionId.value = '';
  stopStream();
  referencesDrawer.close();
  isReplying.value = false;
  fullContent.value = '';
  stopRecoverPoll();
};

onUnmounted(() => {
  chatLoadVersion++;
  cancelSuggestedQuestionsFetch();
  if (!props.embeddedMode) sessionActivity.detach(activitySessionId.value);
  activitySessionId.value = '';
  window.removeEventListener(SESSION_MUTATION_EVENT, handleSessionMutation);
  if (scrollTopDebounceTimer) {
    clearTimeout(scrollTopDebounceTimer);
    scrollTopDebounceTimer = null;
  }
  stopRecoverPoll();
  backgroundPoller.dispose();
  clearComposerApproval();
});

onBeforeRouteLeave((_to, _from, next) => {
  if (props.embeddedMode) return next();
  clearData();
  useSettingsStoreInstance.restoreDefaultsIfSnapshotted();
  next();
});

onBeforeRouteUpdate((_to, _from, next) => {
  if (props.embeddedMode) return next();
  clearData();
  next();
});
</script>

<style lang="less" scoped>
@import './chat-index.less';
</style>

<style lang="less">
@import './chat-rewind-popconfirm.less';
</style>
