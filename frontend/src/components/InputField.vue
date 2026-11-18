<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, onUnmounted, ref, watch, type PropType } from 'vue';
import { storeToRefs } from 'pinia';
import { onBeforeRouteUpdate, useRoute } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { stopSession } from '@/api/chat';
import type { SteerQueueItem } from '@/api/chat/steer';
import { useSettingsStore } from '@/stores/settings';
import { useBrowserConnectionStore } from '@/stores/browserConnection';
import { useUIStore } from '@/stores/ui';
import { useMenuStore } from '@/stores/menu';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities';
import type { SendMessageOptions } from '@/utils/questionOrigin';
import { chatSubmitShortcut } from '@/utils/chatSubmitShortcut';
import type { MentionRequestItem } from '@/types/mention';

import KnowledgeBaseSelector from './KnowledgeBaseSelector.vue';
import MentionSelector from './MentionSelector.vue';
import AttachmentUpload, { type AttachmentFile } from './AttachmentUpload.vue';
import SteerQueueBar from './chat-input/SteerQueueBar.vue';
import InputSelectedChips from './chat-input/InputSelectedChips.vue';
import InputControlBar from './chat-input/InputControlBar.vue';
import InputModelPickerPopup from './chat-input/InputModelPickerPopup.vue';
import { CHAT_FILE_DROP_EVENT, useChatInputImages, type AttachmentUploadHandle } from './chat-input/useChatInputImages';
import { useChatInputVoice } from './chat-input/useChatInputVoice';
import { useChatInputAgentScope } from './chat-input/useChatInputAgentScope';
import { useChatInputModelReasoning } from './chat-input/useChatInputModelReasoning';
import { useChatInputMentions } from './chat-input/useChatInputMentions';

interface MentionSelectorHandle {
  moveActive: (delta: number) => void;
  confirmActive: () => void;
  enterActiveGroup: () => boolean;
  leaveGroup: () => boolean;
}

const props = defineProps({
  compact: {
    type: Boolean,
    default: false,
  },
  autoFocus: {
    type: Boolean,
    default: false,
  },
  isReplying: {
    type: Boolean,
    required: false,
  },
  composerLocked: {
    type: Boolean,
    default: false,
  },
  sessionId: {
    type: String,
    required: false,
  },
  assistantMessageId: {
    type: String,
    required: false,
  },
  embeddedMode: {
    type: Boolean,
    default: false,
  },
  modelId: {
    type: String,
    default: '',
  },
  defaultModelId: {
    type: String,
    default: '',
  },
  queuedSteers: {
    type: Array as PropType<SteerQueueItem[]>,
    default: () => [],
  },
  canSteer: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits<{
  (
    e: 'send-msg',
    query: string,
    modelId: string,
    mentionedItems: MentionRequestItem[],
    imageFiles: File[],
    attachmentFiles: AttachmentFile[],
    options: SendMessageOptions,
  ): void;
  (e: 'update:modelId', modelId: string): void;
  (e: 'stop-generation'): void;
  (e: 'stop-confirmed'): void;
  (e: 'stop-failed'): void;
  (e: 'steer-msg', query: string, mentionedItems: MentionRequestItem[], delivery: 'inject' | 'after'): void;
  (e: 'promote-steer', steerId: string): void;
  (e: 'remove-steer', steerId: string): void;
  (e: 'retry-steer', steerId: string): void;
}>();

const route = useRoute();
const settingsStore = useSettingsStore();
const browserConnection = useBrowserConnectionStore();
const uiStore = useUIStore();
const menuStore = useMenuStore();
const chatResources = useChatResourcesStore();
const deploymentCapabilities = useDeploymentCapabilitiesStore();
const {
  agents,
  disabledOwnAgentIds,
  allModels,
  chatModels: availableModels,
  webSearchProviders,
} = storeToRefs(chatResources);
const { t, locale } = useI18n();

const query = ref('');
const showKbSelector = ref(false);
const textareaRef = ref<HTMLTextAreaElement | { $el?: HTMLElement } | null>(null);
const mentionSelectorRef = ref<MentionSelectorHandle | null>(null);
const attachmentUploadRef = ref<AttachmentUploadHandle>();
const controlBarRef = ref<InstanceType<typeof InputControlBar>>();

const agentModeButtonRef = computed(() => controlBarRef.value?.getAgentModeButtonEl());
const atButtonRef = computed(() => controlBarRef.value?.getAtButtonEl());
const modelButtonRef = computed(() => controlBarRef.value?.getModelButtonEl());

const getTextareaEl = (): HTMLTextAreaElement | null => {
  const raw = textareaRef.value;
  if (!raw) return null;
  if (typeof HTMLTextAreaElement !== 'undefined' && raw instanceof HTMLTextAreaElement) return raw;
  const el = (raw as { $el?: HTMLElement }).$el ?? (raw as unknown as HTMLElement);
  if (!el) return null;
  if (el.tagName === 'TEXTAREA') return el as HTMLTextAreaElement;
  return el.querySelector?.('textarea') ?? null;
};

const focusInput = async () => {
  await nextTick();
  const textarea = getTextareaEl();
  if (textarea?.isConnected) textarea.focus({ preventScroll: true });
};

const hostApprovalModeVisible = computed(
  () =>
    !props.embeddedMode
    && Boolean(props.sessionId)
    && deploymentCapabilities.isSupported('settings.sandbox.host'),
);

const isAgentEnabled = computed(() => settingsStore.isAgentEnabled);
const isWebSearchEnabled = computed(() => settingsStore.isWebSearchEnabled);

let refreshMentionsOnAgentSwitch = (): void => {};
let clearImagesOnAgentDisallow = (): void => {};
let closeAllPopupsExceptModel = (): void => {};
let closeAllPopupsExceptAgent = (): void => {};
let closeAllPopupsExceptMention = (): void => {};

const selectedModelIdBridge = computed({
  get: () => settingsStore.conversationModels.selectedChatModelId || '',
  set: (val: string) => settingsStore.updateConversationModels({ selectedChatModelId: val }),
});

let loadChatModelsBridge: (force?: boolean) => Promise<void> = async () => {};

const {
  showAgentModeSelector,
  selectedAgentId,
  selectedAgent,
  isCustomAgent,
  hasAgentConfig,
  currentAgentConfig,
  agentKnowledgeBases,
  agentKBSelectionMode,
  sharedAgentKbList,
  sharedAgentOrgName,
  isKnowledgeBaseLockedByAgent,
  isKnowledgeBaseDisabledByAgent,
  agentModelId,
  agentSupportedFileTypes,
  agentAllowedTools,
  agentMCPSelectionMode,
  isMCPAllowedByAgent,
  agentSkillsSelectionMode,
  isSkillAllowedByAgent,
  agentMode,
  isKbCompatibleWithAgent,
  isImageUploadEnabledByAgent,
  isWebSearchConfigured,
  showWebSearchButton,
  showImageUploadButton,
  enabledAgents,
  currentAgentScope,
  mcpServices,
  browserSourceUnavailableHint,
  openBrowserConnectionSettings,
  toggleBrowserSource,
  loadMCPServices,
  loadWebSearchConfig,
  loadAgents,
  updateAgentModeDropdownPosition,
  toggleAgentModeSelector,
  closeAgentModeSelector,
  collectAgentNotReadyReasons,
  showAgentNotReadyMessage,
  handleAgentNotReady,
  handleSelectAgent,
  handleGoToAgentSettings,
  handleGoToWebSearchConfig,
  toggleWebSearch,
  invalidateSharedAgentKbRequests,
} = useChatInputAgentScope({
  props,
  agents,
  disabledOwnAgentIds,
  allModels,
  webSearchProviders,
  selectedModelId: selectedModelIdBridge,
  agentModeButtonRef,
  loadChatModels: (force) => loadChatModelsBridge(force),
  onAgentSwitchedRefreshMention: () => refreshMentionsOnAgentSwitch(),
  onAgentDisallowsImages: () => clearImagesOnAgentDisallow(),
  onCloseOtherPopups: () => closeAllPopupsExceptAgent(),
  getLocale: () => String(locale.value),
  t: (key, params) => t(key, params ?? {}),
});

const {
  selectedModelId,
  showModelSelector,
  modelDropdownStyle,
  isModelLockedByAgent,
  initChatModelSelection,
  loadChatModels,
  handleModelChange,
  embeddedDefaultModel,
  followsDefaultModel,
  composerModel,
  composerModelId,
  reasoningLevels,
  displayedReasoningLevel,
  showReasoningSelector,
  selectReasoningLevel,
  selectedModelDisplayName,
  modelTriggerTooltip,
  contextWindowTitle,
  selectedModelContextLabel,
  selectedModelContextIsDefault,
  selectedModelContextTitle,
  updateModelDropdownPosition,
  toggleModelSelector,
  closeModelSelector,
  handleReasoningVisibleChange,
} = useChatInputModelReasoning({
  props,
  availableModels,
  selectedAgentId,
  agentModelId,
  currentAgentConfig,
  modelButtonRef,
  t: (key, params) => t(key, params ?? {}),
  onUpdateModelId: (id) => emit('update:modelId', id),
  onCloseOtherPopups: () => closeAllPopupsExceptModel(),
});

loadChatModelsBridge = loadChatModels;

const {
  uploadedImages,
  imageInputRef,
  uploadedAttachments,
  removeImage,
  revokeAllUploadedImages,
  clearPendingUploads,
  triggerImageUpload,
  handleImageSelect,
  handleChatFileDrop,
  onPaste,
  onDrop,
  onDragOver,
} = useChatInputImages({
  isImageUploadEnabledByAgent,
  attachmentUploadRef,
  t: (key, params) => t(key, params ?? {}),
});

const {
  showMention,
  mentionQuery,
  mentionItems,
  fileIdToKbId,
  mentionActiveIndex,
  mentionStyle,
  isComposing,
  isMentionTriggeredByButton,
  mentionHasMore,
  mentionGroupCounts,
  mentionBrowserKbs,
  mentionLoading,
  selectedKbIds,
  selectedFileIds,
  selectedTags,
  isMentionDisabled,
  mentionEmptyHint,
  mentionBrowserContext,
  allSelectedItems,
  removeSelectedItem,
  loadKnowledgeBases,
  loadMentionItems,
  loadMoreMentionItems,
  onMentionSearchInput,
  closeMentionFromPopup,
  onInput,
  onCompositionStart,
  onCompositionEnd,
  triggerMention,
  onMentionSelect,
  closeMentionSelector,
  cleanupMentionTimers,
} = useChatInputMentions({
  props,
  query,
  selectedAgentId,
  hasAgentConfig,
  currentAgentConfig,
  agentKnowledgeBases,
  agentKBSelectionMode,
  agentSupportedFileTypes,
  agentAllowedTools,
  agentMode,
  isKnowledgeBaseLockedByAgent,
  isKnowledgeBaseDisabledByAgent,
  agentMCPSelectionMode,
  isMCPAllowedByAgent,
  agentSkillsSelectionMode,
  isSkillAllowedByAgent,
  isKbCompatibleWithAgent,
  sharedAgentKbList,
  sharedAgentOrgName,
  currentAgentScope,
  mcpServices,
  getTextareaEl,
  focusInput,
  onCloseOtherPopups: () => closeAllPopupsExceptMention(),
  t: (key, params) => t(key, params ?? {}),
});

const {
  showVoiceButton,
  voiceHotkeyText,
  voiceState,
  voiceElapsedText,
  onVoiceKeydown,
  onVoiceKeyup,
  onVoiceWindowBlur,
  onVoiceVisibilityChange,
  onVoiceButtonPointerDown,
  onVoiceButtonPointerUp,
} = useChatInputVoice({
  query,
  isComposing,
  isEmbeddedMode: () => props.embeddedMode,
  hasAgentConfig,
  currentAgentConfig,
  selectedAgentId,
  getSelectedAgentSourceTenantId: () => settingsStore.selectedAgentSourceTenantId ?? undefined,
  getLocale: () => String(locale.value),
  getTextareaEl,
  t: (key, params) => t(key, params ?? {}),
});

refreshMentionsOnAgentSwitch = () => {
  if (showMention.value) {
    void loadMentionItems(mentionQuery.value, true);
  }
};
clearImagesOnAgentDisallow = () => {
  if (uploadedImages.value.length > 0) {
    revokeAllUploadedImages();
  }
};
closeAllPopupsExceptModel = () => {
  showMention.value = false;
  showAgentModeSelector.value = false;
};
closeAllPopupsExceptAgent = () => {
  showMention.value = false;
  showModelSelector.value = false;
};
closeAllPopupsExceptMention = () => {
  showAgentModeSelector.value = false;
  showModelSelector.value = false;
};

let viewportResizeObserver: ResizeObserver | null = null;
let resizeHandler: (() => void) | null = null;
let scrollHandler: (() => void) | null = null;

onMounted(() => {
  if (props.autoFocus) void focusInput();
  // 嵌入时 agent/KB 由宿主注入，只需模型列表供宿主选择
  if (props.embeddedMode) {
    void loadChatModels();
    return;
  }

  window.addEventListener('keydown', onVoiceKeydown, true);
  window.addEventListener('keyup', onVoiceKeyup, true);
  window.addEventListener('blur', onVoiceWindowBlur);
  document.addEventListener('visibilitychange', onVoiceVisibilityChange);

  browserConnection.watchStatus();

  initChatModelSelection();
  void Promise.all([
    loadKnowledgeBases(),
    loadWebSearchConfig(),
    loadChatModels(),
    loadAgents(),
    loadMCPServices(),
  ]);
  window.addEventListener(CHAT_FILE_DROP_EVENT, handleChatFileDrop as EventListener);

  const persisted = settingsStore.settings.selectedFileKbMap;
  const ids = settingsStore.settings.selectedFiles || [];
  if (persisted && typeof persisted === 'object' && ids.length > 0) {
    const next: Record<string, string> = {};
    ids.forEach((id: string) => {
      if (persisted[id]) next[id] = persisted[id];
    });
    fileIdToKbId.value = next;
  }

  const kbId = typeof route.params?.kbId === 'string' ? route.params.kbId : '';
  if (kbId && !selectedKbIds.value.includes(kbId)) {
    settingsStore.addKnowledgeBase(kbId);
  }

  watch(
    () => menuStore.prefillQuery,
    (prefill) => {
      if (!prefill || route.path !== '/platform/creatChat') return;
      query.value = menuStore.consumePrefillQuery();
      requestAnimationFrame(() => {
        void focusInput();
      });
    },
    { immediate: true },
  );

  document.addEventListener('click', closeAgentModeSelector);
  document.addEventListener('click', closeModelSelector);
  document.addEventListener('click', closeMentionSelector);

  const syncOpenPopupsPosition = () => {
    if (showModelSelector.value) {
      updateModelDropdownPosition();
    }
    if (showAgentModeSelector.value) {
      updateAgentModeDropdownPosition();
    }
  };

  if (typeof ResizeObserver !== 'undefined' && typeof document !== 'undefined' && document.documentElement) {
    viewportResizeObserver = new ResizeObserver(syncOpenPopupsPosition);
    viewportResizeObserver.observe(document.documentElement);
  } else {
    resizeHandler = syncOpenPopupsPosition;
    window.addEventListener('resize', resizeHandler, { passive: true });
  }
  scrollHandler = syncOpenPopupsPosition;
  window.addEventListener('scroll', scrollHandler, { passive: true, capture: true });
});

onBeforeUnmount(() => {
  const textarea = getTextareaEl();
  if (textarea?.isConnected && document.activeElement === textarea) textarea.blur();
});

onUnmounted(() => {
  invalidateSharedAgentKbRequests();
  uploadedImages.value.forEach((img) => URL.revokeObjectURL(img.preview));
  uploadedImages.value = [];
  window.removeEventListener('keydown', onVoiceKeydown, true);
  window.removeEventListener('keyup', onVoiceKeyup, true);
  window.removeEventListener('blur', onVoiceWindowBlur);
  document.removeEventListener('visibilitychange', onVoiceVisibilityChange);
  if (!props.embeddedMode) browserConnection.unwatchStatus();
  window.removeEventListener(CHAT_FILE_DROP_EVENT, handleChatFileDrop as EventListener);
  document.removeEventListener('click', closeAgentModeSelector);
  document.removeEventListener('click', closeModelSelector);
  document.removeEventListener('click', closeMentionSelector);
  cleanupMentionTimers();
  if (viewportResizeObserver) {
    viewportResizeObserver.disconnect();
    viewportResizeObserver = null;
  }
  if (resizeHandler) {
    window.removeEventListener('resize', resizeHandler);
  }
  if (scrollHandler) {
    window.removeEventListener('scroll', scrollHandler, { capture: true });
  }
});

// 监听路由变化
watch(
  () => route.params.kbId,
  (newKbId) => {
    if (props.embeddedMode) return;
    if (newKbId && typeof newKbId === 'string' && !selectedKbIds.value.includes(newKbId)) {
      settingsStore.addKnowledgeBase(newKbId);
    }
  },
);

watch(
  () => uiStore.showSettingsModal,
  (visible, prevVisible) => {
    if (prevVisible && !visible && !props.embeddedMode) {
      void browserConnection.refresh();
    }
  },
);

watch(
  [selectedKbIds, selectedFileIds],
  ([kbIds, fileIds]) => {
    if (!kbIds.length && !fileIds.length) {
      closeModelSelector();
    }
  },
  { deep: true },
);

const clearvalue = () => {
  if (!getTextareaEl()) return;
  query.value = '';
};

const createSession = async (
  val: string,
  delivery: 'inject' | 'after' = 'after',
  options: SendMessageOptions = {},
) => {
  if (props.composerLocked) {
    return;
  }
  if (!val.trim()) {
    MessagePlugin.info(t('input.messages.enterContent'));
    return;
  }
  if (props.isReplying) {
    if (!props.canSteer) {
      MessagePlugin.info(t('input.messages.replying'));
      return;
    }
    if (uploadedAttachments.value.some((item) => item.status === 'uploading')) {
      MessagePlugin.warning(t('input.messages.steerAttachmentPending'));
      return;
    }
    if (uploadedAttachments.value.length || uploadedImages.value.length) {
      MessagePlugin.warning(t('input.messages.steerHasAttachments'));
      return;
    }
    const steerMentions: MentionRequestItem[] = (props.embeddedMode ? [] : allSelectedItems.value).map((item) => ({
      id: item.id,
      name: item.name,
      type: item.type,
      kb_type: item.type === 'kb' ? item.kbType || 'document' : undefined,
      kb_id: item.kbId,
      kb_name: item.kbName,
      service_id: item.serviceId,
      skill_name: item.skillName,
    }));
    emit('steer-msg', val.trim(), steerMentions, delivery);
    clearvalue();
    void focusInput();
    return;
  }

  const pendingAttachment = uploadedAttachments.value.find((item) => item.status === 'uploading');
  if (pendingAttachment) {
    MessagePlugin.warning(t('chat.attachmentStillProcessing', { name: pendingAttachment.name }));
    return;
  }
  const failedAttachment = uploadedAttachments.value.find((item) => item.status === 'failed');
  if (failedAttachment) {
    MessagePlugin.error(failedAttachment.error || t('chat.attachmentParseFailed'));
    return;
  }

  if (props.embeddedMode) {
    emit('send-msg', val, composerModelId.value, [], [], [], options);
    clearvalue();
    void focusInput();
    return;
  }

  const MAX_TOTAL_ATTACHMENTS = 5;
  const combinedAttachmentCount =
    uploadedImages.value.length
    + uploadedAttachments.value.filter((item) => item.status !== 'failed').length;
  if (combinedAttachmentCount > MAX_TOTAL_ATTACHMENTS) {
    MessagePlugin.warning(t('chat.attachmentTotalTooMany', { max: MAX_TOTAL_ATTACHMENTS }));
    return;
  }

  if (!chatResources.isLoaded('models')) {
    await loadChatModels();
  }

  const agentToCheck = selectedAgent.value;
  let actualAgent = agentToCheck;
  if (agentToCheck.is_builtin && !settingsStore.selectedAgentSourceTenantId) {
    let builtin = agents.value.find((a) => a.id === selectedAgentId.value);
    if (!builtin) {
      await loadAgents();
      builtin = agents.value.find((a) => a.id === selectedAgentId.value);
    }
    actualAgent = builtin || agentToCheck;
  }
  const isAgentMode = actualAgent.config?.agent_mode === 'smart-reasoning';
  const { keys: notReadyKeys, labels: notReadyReasons } = collectAgentNotReadyReasons(
    actualAgent,
    isAgentMode,
    settingsStore.selectedAgentSourceTenantId ?? undefined,
  );
  if (notReadyReasons.length > 0) {
    showAgentNotReadyMessage(
      actualAgent,
      notReadyReasons,
      notReadyKeys,
      settingsStore.selectedAgentSourceTenantId ?? undefined,
    );
    return;
  }

  const mentionedItems: MentionRequestItem[] = allSelectedItems.value.map((item) => ({
    id: item.id,
    name: item.name,
    type: item.type,
    kb_type: item.type === 'kb' ? item.kbType || 'document' : undefined,
    kb_id: item.kbId,
    kb_name: item.kbName,
    service_id: item.serviceId,
    skill_name: item.skillName,
  }));
  const imageFiles = uploadedImages.value.map((img) => img.file);
  const attachmentFiles = uploadedAttachments.value;

  emit('send-msg', val, selectedModelId.value, mentionedItems, imageFiles, attachmentFiles, options);

  uploadedImages.value.forEach((img) => URL.revokeObjectURL(img.preview));
  uploadedImages.value = [];

  attachmentUploadRef.value?.clear();
  uploadedAttachments.value = [];

  clearvalue();
  void focusInput();
};

// End createSession
const steerShortcutLabel = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ Enter' : 'Alt+Enter';
const firstQueuedSteer = computed(() =>
  props.queuedSteers.find(
    (item) => item.delivery === 'after' && !item.pending && !item.promoting && !item.failed,
  ),
);
const injectCurrentInput = () => {
  if (props.composerLocked) return;
  if (!props.isReplying || !props.canSteer) return;
  if (query.value.trim()) void createSession(query.value, 'inject');
  else if (firstQueuedSteer.value) emit('promote-steer', firstQueuedSteer.value.steer_id);
};

const onKeydown = (val: string, event: { e: KeyboardEvent }) => {
  if (isComposing.value || event.e.isComposing || event.e.keyCode === 229) return;
  if (showMention.value) {
    if (event.e.keyCode === 38) {
      event.e.preventDefault();
      mentionSelectorRef.value?.moveActive(-1);
      return;
    }
    if (event.e.keyCode === 40) {
      event.e.preventDefault();
      mentionSelectorRef.value?.moveActive(1);
      return;
    }
    if (event.e.keyCode === 13) {
      event.e.preventDefault();
      mentionSelectorRef.value?.confirmActive();
      return;
    }
    if (event.e.keyCode === 9 && !event.e.shiftKey) {
      if (mentionSelectorRef.value?.enterActiveGroup()) event.e.preventDefault();
      return;
    }
    if (event.e.keyCode === 27) {
      if (mentionSelectorRef.value?.leaveGroup()) {
        return;
      }
      showMention.value = false;
      return;
    }
  }

  if (event.e.keyCode === 8) {
    const textarea = getTextareaEl();
    if (textarea && textarea.selectionStart === 0 && textarea.selectionEnd === 0 && query.value === '') {
      const items = allSelectedItems.value;
      if (items.length > 0) {
        event.e.preventDefault();
        removeSelectedItem(items[items.length - 1]);
        return;
      }
    }
  }

  const delivery = chatSubmitShortcut(event.e, Boolean(props.isReplying && props.canSteer));
  if (delivery) {
    event.e.preventDefault();
    if (props.composerLocked) return;
    if (delivery === 'inject' && props.isReplying && props.canSteer) injectCurrentInput();
    else void createSession(val, delivery);
  }
};

// End onKeydown
const handleStop = async () => {
  if (!props.sessionId) {
    MessagePlugin.warning(t('input.messages.sessionMissing'));
    return;
  }

  if (!props.assistantMessageId) {
    console.error('[Stop] Assistant message ID is empty');
    MessagePlugin.warning(t('input.messages.messageMissing'));
    return;
  }

  emit('stop-generation');

  try {
    await stopSession(props.sessionId, props.assistantMessageId);
    emit('stop-confirmed');
    MessagePlugin.success(t('input.messages.stopSuccess'));
  } catch (error) {
    console.error('Failed to stop session:', error);
    emit('stop-failed');
    MessagePlugin.error(t('input.messages.stopFailed'));
  }
};

onBeforeRouteUpdate((_to, _from, next) => {
  if (!props.embeddedMode) {
    clearvalue();
    clearPendingUploads();
  }
  next();
});

defineExpose({
  focusInput,
  getComposerModelId: () => composerModelId.value,
  triggerSend(text: string, options: SendMessageOptions = {}) {
    if (!text.trim()) return;
    query.value = text;
    nextTick(() => createSession(text, 'after', options));
  },
  prefill(text: string) {
    query.value = text;
  },
});
</script>

<template>
  <div
    class="answers-input"
    :class="{ 'is-embedded': embeddedMode, 'is-compact': compact }"
    @drop="onDrop"
    @dragover="onDragOver"
  >
    <input
      ref="imageInputRef"
      type="file"
      accept="image/jpeg,image/png,image/gif,image/webp"
      multiple
      style="display:none"
      @change="handleImageSelect"
    />

    <SteerQueueBar
      :queued-steers="queuedSteers"
      :first-queued-steer-id="firstQueuedSteer?.steer_id"
      :steer-shortcut-label="steerShortcutLabel"
      @promote-steer="emit('promote-steer', $event)"
      @remove-steer="emit('remove-steer', $event)"
      @retry-steer="emit('retry-steer', $event)"
    />

    <div
      class="rich-input-container"
      :class="{ 'has-selected-tags': Boolean($slots.chips) || (!embeddedMode && allSelectedItems.length > 0) }"
      data-guide="chat-input"
    >
      <InputSelectedChips
        :uploaded-images="uploadedImages"
        :embedded-mode="embeddedMode"
        :all-selected-items="allSelectedItems"
        @remove-image="removeImage"
        @remove-selected-item="removeSelectedItem"
      >
        <template v-if="$slots.chips" #chips>
          <slot name="chips" />
        </template>
      </InputSelectedChips>

      <AttachmentUpload
        ref="attachmentUploadRef"
        :max-files="5"
        :session-id="sessionId"
        :agent-id="selectedAgentId"
        :agent-source-tenant-id="settingsStore.selectedAgentSourceTenantId ?? undefined"
        @update:files="uploadedAttachments = $event"
      />

      <t-textarea
        ref="textareaRef"
        v-model="query"
        :placeholder="t('input.placeholder')"
        name="description"
        :autosize="true"
        @keydown="onKeydown"
        @input="onInput"
        @compositionstart="onCompositionStart"
        @compositionend="onCompositionEnd"
        @paste="onPaste"
      />

      <InputControlBar
        ref="controlBarRef"
        :embedded-mode="embeddedMode"
        :session-id="sessionId"
        :query="query"
        :is-replying="isReplying"
        :can-steer="canSteer"
        :composer-locked="composerLocked"
        :is-custom-agent="isCustomAgent"
        :is-agent-enabled="isAgentEnabled"
        :selected-agent="selectedAgent"
        :selected-agent-id="selectedAgentId"
        :enabled-agents="enabledAgents"
        :all-models="allModels"
        :show-agent-mode-selector="showAgentModeSelector"
        :host-approval-mode-visible="hostApprovalModeVisible"
        :browser-source-unavailable-hint="browserSourceUnavailableHint"
        :show-web-search-button="showWebSearchButton"
        :is-web-search-configured="isWebSearchConfigured"
        :is-web-search-enabled="isWebSearchEnabled"
        :show-image-upload-button="showImageUploadButton"
        :uploaded-images-count="uploadedImages.length"
        :uploaded-attachments-count="uploadedAttachments.length"
        :show-voice-button="showVoiceButton"
        :voice-state="voiceState"
        :voice-hotkey-text="voiceHotkeyText"
        :voice-elapsed-text="voiceElapsedText"
        :is-mention-disabled="isMentionDisabled"
        :is-knowledge-base-disabled-by-agent="isKnowledgeBaseDisabledByAgent"
        :all-selected-items-count="allSelectedItems.length"
        :is-model-locked-by-agent="isModelLockedByAgent"
        :model-trigger-tooltip="modelTriggerTooltip"
        :selected-model-display-name="selectedModelDisplayName"
        :follows-default-model="followsDefaultModel"
        :composer-model="composerModel"
        :selected-model-context-label="selectedModelContextLabel"
        :selected-model-context-is-default="selectedModelContextIsDefault"
        :selected-model-context-title="selectedModelContextTitle"
        :show-model-selector="showModelSelector"
        :reasoning-levels="reasoningLevels"
        :displayed-reasoning-level="displayedReasoningLevel"
        :show-reasoning-selector="showReasoningSelector"
        @toggle-agent-mode-selector="toggleAgentModeSelector"
        @close-agent-mode-selector="closeAgentModeSelector"
        @select-agent="handleSelectAgent"
        @agent-not-ready="handleAgentNotReady"
        @open-browser-connection-settings="openBrowserConnectionSettings"
        @toggle-browser-source="toggleBrowserSource"
        @go-to-web-search-config="handleGoToWebSearchConfig"
        @toggle-web-search="toggleWebSearch"
        @trigger-image-upload="triggerImageUpload"
        @trigger-attachment-select="attachmentUploadRef?.triggerFileSelect()"
        @voice-pointer-down="onVoiceButtonPointerDown"
        @voice-pointer-up="onVoiceButtonPointerUp"
        @go-to-agent-knowledge-settings="handleGoToAgentSettings('knowledge')"
        @trigger-mention="triggerMention"
        @toggle-model-selector="toggleModelSelector"
        @update:show-reasoning-selector="showReasoningSelector = $event"
        @reasoning-visible-change="handleReasoningVisibleChange"
        @select-reasoning-level="selectReasoningLevel"
        @stop="handleStop"
        @send="createSession(query)"
      >
        <template v-if="$slots['background-work']" #background-work>
          <slot name="background-work" />
        </template>
        <template v-if="$slots.controls" #controls>
          <slot name="controls" />
        </template>
      </InputControlBar>

      <InputModelPickerPopup
        :visible="showModelSelector"
        :dropdown-style="modelDropdownStyle"
        :embedded-mode="embeddedMode"
        :embedded-default-model="embeddedDefaultModel"
        :follows-default-model="followsDefaultModel"
        :available-models="availableModels"
        :composer-model-id="composerModelId"
        :context-window-title="contextWindowTitle"
        @close="closeModelSelector"
        @select-model="handleModelChange"
      />
    </div>

    <Teleport to="body">
      <MentionSelector
        ref="mentionSelectorRef"
        v-model:active-index="mentionActiveIndex"
        :visible="showMention"
        :style="mentionStyle"
        :items="mentionItems"
        :has-more="mentionHasMore"
        :loading="mentionLoading"
        :empty-hint="mentionEmptyHint"
        :query="mentionQuery"
        :searchable="isMentionTriggeredByButton"
        :group-counts="mentionGroupCounts"
        :browser-knowledge-bases="mentionBrowserKbs"
        :browser-context="mentionBrowserContext"
        :browser-file-types="agentSupportedFileTypes"
        :browser-selected-ids="selectedFileIds"
        :browser-whole-kb-ids="settingsStore.getSelectedKnowledgeBases()"
        :browser-tag-kb-ids="selectedTags.map((tag) => tag.kbId).filter((id): id is string => Boolean(id))"
        @update:query="onMentionSearchInput"
        @close="closeMentionFromPopup"
        @focus-input="getTextareaEl()?.focus()"
        @select="onMentionSelect"
        @load-more="loadMoreMentionItems"
      />
    </Teleport>

    <Teleport to="body">
      <KnowledgeBaseSelector
        v-model:visible="showKbSelector"
        :anchor-el="atButtonRef"
        @close="showKbSelector = false"
      />
    </Teleport>
  </div>
</template>

<style scoped lang="less">
.answers-input {
  position: absolute;
  z-index: 99;
  bottom: 60px;
  left: 50%;
  transform: translateX(-50%);
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;

  &.is-embedded {
    position: relative;
    bottom: auto;
    left: auto;
    transform: none;
    z-index: auto;

    .rich-input-container,
    :deep(.steer-queue) {
      max-width: 100%;
    }
  }

  &.is-compact {
    --composer-input-min-height: 56px;
    --composer-control-icon-size: 16px;
    --composer-control-bar-margin: 0 12px 8px;
    --composer-control-bar-pt: 4px;

    .rich-input-container :deep(.t-textarea__inner) {
      padding: 12px 14px;
    }
  }
}

.rich-input-container {
  position: relative;
  width: 100%;
  max-width: 960px;
  background: var(--td-bg-color-container);
  border-radius: var(--app-radius-xl);
  border: 1px solid var(--td-component-stroke);
  box-shadow: var(--gy-shadow-sm);

  &:focus-within {
    border-color: var(--td-brand-color);
  }

  &.has-selected-tags :deep(.t-textarea__inner) {
    padding-top: 8px;
  }

  &:not(.has-selected-tags) :deep(.t-textarea__inner) {
    border-radius: var(--app-radius-xl);
    padding-top: 16px;
  }
}

:deep(.t-textarea__inner) {
  width: 100%;
  max-height: 152px !important;
  min-height: var(--composer-input-min-height, 72px) !important;
  resize: none;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-xl);
  font-weight: 400;
  line-height: 24px;
  font-family: var(--app-font-family);
  padding: 12px 16px;
  border-radius: 0 0 12px 12px;
  border: none;
  box-sizing: border-box;
  background: transparent;
  box-shadow: none;

  &:focus {
    border: none;
    box-shadow: none;
  }

  &::placeholder {
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: var(--app-text-xl);
    font-weight: 400;
    line-height: 24px;
  }
}
</style>
