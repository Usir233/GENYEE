import { computed, nextTick, ref, watch, type ComputedRef, type Ref } from 'vue';
import { useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import type { AgentConfig } from '@/api/agent';
import type { ModelConfig } from '@/api/model';
import { loadPreference, savePreference, safeRemoveItem, userKey } from '@/composables/preferenceStorage';
import { useSettingsStore } from '@/stores/settings';
import { useChatResourcesStore } from '@/stores/chatResources';
import {
  effectiveContextWindow,
  formatContextWindow,
  isDefaultContextWindow,
} from '@/utils/contextWindow';
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom';
import {
  clampLevel,
  levelFromLegacy,
  levelLabelKey,
  supportedLevels,
  type ReasoningLevel,
} from '@/utils/reasoningEffort';

export const EMBEDDED_AUTO_MODEL = '__auto__';

export const readLastChatModelID = (): string => loadPreference('last_chat_model') || '';

export const writeLastChatModelID = (id: string): void => {
  if (id) {
    savePreference('last_chat_model', id);
  } else {
    safeRemoveItem(userKey('last_chat_model'));
  }
};

export const modelDisplayName = (model: ModelConfig): string => {
  const displayName = model.display_name?.trim();
  return displayName || model.name;
};

export function useChatInputModelReasoning(options: {
  props: {
    embeddedMode: boolean;
    modelId: string;
    defaultModelId: string;
  };
  availableModels: Ref<ModelConfig[]>;
  selectedAgentId: ComputedRef<string>;
  agentModelId: ComputedRef<string | null>;
  currentAgentConfig: ComputedRef<Partial<AgentConfig>>;
  modelButtonRef: Ref<HTMLElement | undefined>;
  t: (key: string, params?: Record<string, unknown>) => string;
  onUpdateModelId: (modelId: string) => void;
  onCloseOtherPopups: () => void;
}) {
  const {
    props,
    availableModels,
    selectedAgentId,
    agentModelId,
    currentAgentConfig,
    modelButtonRef,
    t,
    onUpdateModelId,
    onCloseOtherPopups,
  } = options;

  const router = useRouter();
  const settingsStore = useSettingsStore();
  const chatResources = useChatResourcesStore();

  const selectedModelId = computed({
    get: () => settingsStore.conversationModels.selectedChatModelId || '',
    set: (val: string) => settingsStore.updateConversationModels({ selectedChatModelId: val }),
  });
  const modelsLoading = ref(false);
  const showModelSelector = ref(false);
  const modelDropdownStyle = ref<Record<string, string>>({});
  const isModelLockedByAgent = computed(() => false);

  const ensureModelSelection = (): void => {
    if (selectedModelId.value) {
      return;
    }
    const lastPick = readLastChatModelID();
    if (lastPick) {
      selectedModelId.value = lastPick;
      return;
    }
    if (availableModels.value.length > 0) {
      selectedModelId.value = availableModels.value[0].id || '';
    }
  };

  const initChatModelSelection = (): void => {
    const lastPick = readLastChatModelID();
    const currentSelectedModel = settingsStore.conversationModels.selectedChatModelId;
    const initialSelection = lastPick || currentSelectedModel || '';
    settingsStore.updateConversationModels({
      summaryModelId: initialSelection,
      selectedChatModelId: initialSelection,
      rerankModelId: '',
    });
    if (!selectedModelId.value) {
      selectedModelId.value = initialSelection;
    }
    ensureModelSelection();
  };

  const loadChatModels = async (force = false): Promise<void> => {
    if (modelsLoading.value) return;
    modelsLoading.value = true;
    try {
      await chatResources.ensureChatModels(force);
      if (!props.embeddedMode) ensureModelSelection();
    } catch (error) {
      console.error('Failed to load chat models:', error);
      chatResources.invalidate('models');
    } finally {
      modelsLoading.value = false;
    }
  };

  watch(
    [selectedAgentId, () => settingsStore.selectedAgentSourceTenantId, agentModelId],
    ([, sourceTenantId, newModelId]) => {
      if (props.embeddedMode || !newModelId || newModelId.trim() === '') return;

      const lastPick = readLastChatModelID();
      const isSharedAgent = Boolean(sourceTenantId);
      const agentModelInList = availableModels.value.some((m) => m.id === newModelId);

      if (
        lastPick
        && selectedModelId.value === lastPick
        && lastPick !== newModelId
        && (!isSharedAgent || agentModelInList)
      ) {
        return;
      }

      if (newModelId !== selectedModelId.value) {
        selectedModelId.value = newModelId;
      }
    },
    { immediate: true },
  );

  const handleGoToConversationModels = (): void => {
    showModelSelector.value = false;
    void router.push('/platform/settings');
    setTimeout(() => {
      const event = new CustomEvent('settings-nav', {
        detail: { section: 'models', subsection: 'chat' },
      });
      window.dispatchEvent(event);
    }, 100);
  };

  const handleModelChange = (value: string | number | Array<string | number> | undefined): void => {
    const normalized = Array.isArray(value) ? value[0] : value;
    const val = normalized !== undefined && normalized !== null ? String(normalized) : '';

    if (!val) {
      selectedModelId.value = '';
      return;
    }
    if (val === '__add_model__') {
      if (!props.embeddedMode) selectedModelId.value = readLastChatModelID();
      handleGoToConversationModels();
      return;
    }
    if (props.embeddedMode) {
      onUpdateModelId(val === EMBEDDED_AUTO_MODEL ? '' : val);
      showModelSelector.value = false;
      return;
    }

    writeLastChatModelID(val);
    selectedModelId.value = val;
    showModelSelector.value = false;

    settingsStore.updateConversationModels({
      summaryModelId: val,
      selectedChatModelId: val,
      rerankModelId: '',
    });
  };

  const selectedModel = computed(() => {
    return availableModels.value.find(model => model.id === selectedModelId.value);
  });

  const embeddedPickedModel = computed(() => props.modelId
    ? availableModels.value.find(model => model.id === props.modelId) : undefined);
  const embeddedDefaultModel = computed(() =>
    availableModels.value.find(model => model.id === props.defaultModelId) || availableModels.value[0]);
  const followsDefaultModel = computed(() => props.embeddedMode && !embeddedPickedModel.value);
  const composerModel = computed(() => {
    if (!props.embeddedMode) return selectedModel.value;
    return embeddedPickedModel.value || embeddedDefaultModel.value;
  });
  const composerModelId = computed(() => {
    if (!props.embeddedMode) return selectedModelId.value;
    return composerModel.value?.id || '';
  });

  const reasoningLevels = computed(() => supportedLevels(selectedModel.value?.capabilities));
  const agentReasoningLevel = computed(() => clampLevel(
    levelFromLegacy(currentAgentConfig.value?.thinking, currentAgentConfig.value?.reasoning_effort),
    reasoningLevels.value,
  ));
  const displayedReasoningLevel = computed(() => settingsStore.reasoningEffortOverride || agentReasoningLevel.value);
  const showReasoningSelector = ref(false);
  const selectReasoningLevel = (level: ReasoningLevel) => {
    settingsStore.reasoningEffortOverride = level === agentReasoningLevel.value ? '' : level;
    showReasoningSelector.value = false;
  };
  watch([selectedModel, reasoningLevels, () => settingsStore.reasoningEffortOverride, () => settingsStore._isApplyingSessionState], () => {
    if (!settingsStore._isApplyingSessionState && selectedModel.value && settingsStore.reasoningEffortOverride
      && !reasoningLevels.value.includes(settingsStore.reasoningEffortOverride)) {
      settingsStore.reasoningEffortOverride = '';
    }
  }, { immediate: true, flush: 'sync' });

  // 模型展示名：本空间列表中有则用名称；若为共享智能体且其 model_id 不在本空间列表中则显示“共享智能体配置的模型”
  const selectedModelDisplayName = computed(() => {
    if (composerModel.value) return modelDisplayName(composerModel.value);
    if (!selectedModelId.value || props.embeddedMode) return t('input.notConfigured');
    const isSharedAgent = Boolean(settingsStore.selectedAgentSourceTenantId);
    const modelFromAgent = agentModelId.value && agentModelId.value === selectedModelId.value;
    if (isSharedAgent && modelFromAgent) return t('input.sharedAgentModelLabel');
    return t('input.notConfigured');
  });

  const modelTriggerTooltip = computed(() => {
    if (isModelLockedByAgent.value) return t('input.modelLockedByAgent');
    if (followsDefaultModel.value) return t('input.modelAutoHint');
    return '';
  });

  const contextWindowTitle = (tokens?: number): string => {
    if (isDefaultContextWindow(tokens)) {
      return t('model.editor.contextWindowDefaultHint', { value: formatContextWindow(tokens) });
    }
    return t('model.editor.contextWindowTokens', { count: effectiveContextWindow(tokens) });
  };

  const selectedModelContextLabel = computed(() => {
    if (!composerModel.value) return '';
    return formatContextWindow(composerModel.value.parameters?.context_window);
  });

  const selectedModelContextIsDefault = computed(() =>
    isDefaultContextWindow(composerModel.value?.parameters?.context_window),
  );

  const selectedModelContextTitle = computed(() => {
    if (!composerModel.value) return '';
    return contextWindowTitle(composerModel.value.parameters?.context_window);
  });

  const updateModelDropdownPosition = (): void => {
    const anchor = modelButtonRef.value;
    if (!anchor) {
      modelDropdownStyle.value = {
        position: 'fixed',
        top: '50%',
        left: '50%',
        transform: 'translate(-50%, -50%)',
      };
      return;
    }

    const zoom = getRootZoom();
    const rect = rectToCssPx(anchor.getBoundingClientRect(), zoom);
    const dropdownWidth = 320;
    const offsetY = 8;
    const { width: vw, height: vh } = cssViewportSize(zoom);

    let left = Math.floor(rect.left);
    const minLeft = 16;
    const maxLeft = Math.max(16, vw - dropdownWidth - 16);
    left = Math.max(minLeft, Math.min(maxLeft, left));

    const preferredDropdownHeight = 280;
    const minDropdownHeight = 200;
    const topMargin = 20;
    const spaceBelow = vh - rect.bottom;
    const spaceAbove = rect.top;

    let actualHeight: number;
    let shouldOpenBelow: boolean;

    if (spaceBelow >= minDropdownHeight + offsetY) {
      actualHeight = Math.min(preferredDropdownHeight, spaceBelow - offsetY - 16);
      shouldOpenBelow = true;
    } else {
      const availableHeight = spaceAbove - offsetY - topMargin;
      actualHeight =
        availableHeight >= preferredDropdownHeight
          ? preferredDropdownHeight
          : Math.max(minDropdownHeight, availableHeight);
      shouldOpenBelow = false;
    }

    if (shouldOpenBelow) {
      const top = Math.floor(rect.bottom + offsetY);
      modelDropdownStyle.value = {
        position: 'fixed !important',
        width: `${dropdownWidth}px`,
        left: `${left}px`,
        top: `${top}px`,
        maxHeight: `${actualHeight}px`,
        transform: 'none !important',
        margin: '0 !important',
        padding: '0 !important',
      };
    } else {
      const bottom = vh - rect.top + offsetY;
      modelDropdownStyle.value = {
        position: 'fixed !important',
        width: `${dropdownWidth}px`,
        left: `${left}px`,
        bottom: `${bottom}px`,
        maxHeight: `${actualHeight}px`,
        transform: 'none !important',
        margin: '0 !important',
        padding: '0 !important',
      };
    }
  };

  const closeModelSelector = (): void => {
    showModelSelector.value = false;
  };

  const toggleModelSelector = (): void => {
    showReasoningSelector.value = false;
    if (isModelLockedByAgent.value) {
      MessagePlugin.warning(t('input.modelLockedByAgent'));
      return;
    }

    onCloseOtherPopups();
    showModelSelector.value = !showModelSelector.value;
    if (showModelSelector.value) {
      if (!availableModels.value.length) {
        void loadChatModels();
      }
      void nextTick(() => {
        updateModelDropdownPosition();
        requestAnimationFrame(() => {
          updateModelDropdownPosition();
          setTimeout(() => {
            updateModelDropdownPosition();
            document
              .querySelector('.model-selector-content .model-option.selected')
              ?.scrollIntoView({ block: 'nearest' });
          }, 50);
        });
      });
    }
  };

  const handleReasoningVisibleChange = (visible: boolean): void => {
    if (!visible) return;
    closeModelSelector();
    onCloseOtherPopups();
  };

  void levelLabelKey;

  return {
    selectedModelId,
    modelsLoading,
    showModelSelector,
    modelDropdownStyle,
    isModelLockedByAgent,
    initChatModelSelection,
    loadChatModels,
    handleModelChange,
    selectedModel,
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
  };
}
