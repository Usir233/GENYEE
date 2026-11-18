<script setup lang="ts">
import { ref } from 'vue';
import type { CustomAgent } from '@/api/agent';
import type { ModelConfig } from '@/api/model';
import { useSettingsStore } from '@/stores/settings';
import { useBrowserConnectionStore } from '@/stores/browserConnection';
import type { AgentNotReadyReasonKey } from '@/utils/agent-readiness';
import { levelLabelKey, type ReasoningLevel } from '@/utils/reasoningEffort';
import type { VoiceInputState } from '@/composables/useVoiceInput';
import BrowserIcon from '@/components/icons/BrowserIcon.vue';
import AgentSelector from '../AgentSelector.vue';
import HostApprovalModeSelect from '../HostApprovalModeSelect.vue';

defineProps<{
  embeddedMode: boolean;
  sessionId?: string;
  query: string;
  isReplying?: boolean;
  canSteer: boolean;
  composerLocked: boolean;
  isCustomAgent: boolean;
  isAgentEnabled: boolean;
  selectedAgent: CustomAgent;
  selectedAgentId: string;
  enabledAgents: CustomAgent[];
  allModels: ModelConfig[];
  showAgentModeSelector: boolean;
  hostApprovalModeVisible: boolean;
  browserSourceUnavailableHint: string;
  showWebSearchButton: boolean;
  isWebSearchConfigured: boolean;
  isWebSearchEnabled: boolean;
  showImageUploadButton: boolean;
  uploadedImagesCount: number;
  uploadedAttachmentsCount: number;
  showVoiceButton: boolean;
  voiceState: VoiceInputState;
  voiceHotkeyText: string;
  voiceElapsedText: string;
  isMentionDisabled: boolean;
  isKnowledgeBaseDisabledByAgent: boolean;
  allSelectedItemsCount: number;
  isModelLockedByAgent: boolean;
  modelTriggerTooltip: string;
  selectedModelDisplayName: string;
  followsDefaultModel: boolean;
  composerModel?: ModelConfig;
  selectedModelContextLabel: string;
  selectedModelContextIsDefault: boolean;
  selectedModelContextTitle: string;
  showModelSelector: boolean;
  reasoningLevels: ReasoningLevel[];
  displayedReasoningLevel: ReasoningLevel;
  showReasoningSelector: boolean;
}>();

const emit = defineEmits<{
  toggleAgentModeSelector: [];
  closeAgentModeSelector: [];
  selectAgent: [agent: CustomAgent, sourceTenantId?: string];
  agentNotReady: [agent: CustomAgent, labels: string[], keys: AgentNotReadyReasonKey[], sourceTenantId?: string];
  openBrowserConnectionSettings: [];
  toggleBrowserSource: [];
  goToWebSearchConfig: [];
  toggleWebSearch: [];
  triggerImageUpload: [];
  triggerAttachmentSelect: [];
  voicePointerDown: [e: PointerEvent];
  voicePointerUp: [];
  goToAgentKnowledgeSettings: [];
  triggerMention: [];
  toggleModelSelector: [];
  'update:showReasoningSelector': [visible: boolean];
  reasoningVisibleChange: [visible: boolean];
  selectReasoningLevel: [level: ReasoningLevel];
  stop: [];
  send: [];
}>();

const settingsStore = useSettingsStore();
const browserConnection = useBrowserConnectionStore();

const agentModeButtonEl = ref<HTMLElement>();
const atButtonEl = ref<HTMLElement>();
const modelButtonEl = ref<HTMLElement>();

defineExpose({
  getAgentModeButtonEl: () => agentModeButtonEl.value,
  getAtButtonEl: () => atButtonEl.value,
  getModelButtonEl: () => modelButtonEl.value,
});
</script>

<template>
  <div class="control-bar" :class="{ 'is-embedded': embeddedMode }">
    <div class="control-left">
      <template v-if="!embeddedMode">
        <button
          ref="agentModeButtonEl"
          type="button"
          class="control-btn agent-mode-btn"
          :class="{
            'is-normal': !isCustomAgent && !isAgentEnabled,
            'is-agent': !isCustomAgent && isAgentEnabled,
            'is-custom': isCustomAgent,
          }"
          :aria-expanded="showAgentModeSelector"
          @click.stop="emit('toggleAgentModeSelector')"
        >
          <span class="agent-mode-text">
            {{ selectedAgent.name || (isAgentEnabled ? $t('input.agentMode') : $t('input.normalMode')) }}
          </span>
          <svg
            width="12"
            height="12"
            viewBox="0 0 12 12"
            fill="currentColor"
            class="dropdown-arrow"
            :class="{ rotate: showAgentModeSelector }"
            aria-hidden="true"
          >
            <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
          </svg>
        </button>

        <AgentSelector
          :visible="showAgentModeSelector"
          :anchor-el="agentModeButtonEl"
          :current-agent-id="selectedAgentId"
          :agents="enabledAgents"
          :all-models="allModels"
          @close="emit('closeAgentModeSelector')"
          @select="(agent: CustomAgent, sourceTenantId?: string) => emit('selectAgent', agent, sourceTenantId)"
          @not-ready="(agent: CustomAgent, labels: string[], keys: AgentNotReadyReasonKey[], sourceTenantId?: string) => emit('agentNotReady', agent, labels, keys, sourceTenantId)"
        />

        <HostApprovalModeSelect v-if="hostApprovalModeVisible && sessionId" :session-id="sessionId" />

        <t-tooltip
          v-if="settingsStore.isAgentStreamMode"
          placement="top"
          theme="light"
          :popup-props="{ overlayClassName: 'input-field-tooltip' }"
        >
          <template #content>
            <div v-if="!browserConnection.knownOffline" class="browser-source-tooltip">
              <strong>{{ $t('localBrowser.local') }}</strong>
              <span>{{ $t('localBrowser.sourceHint') }}</span>
            </div>
            <div v-else class="tooltip-with-link">
              <span>{{ $t(browserSourceUnavailableHint) }}</span>
              <a href="#" @click.prevent="emit('openBrowserConnectionSettings')">{{
                $t('localBrowser.openSettings')
              }}</a>
            </div>
          </template>
          <button
            type="button"
            class="control-btn browser-source-btn"
            :class="{
              active: settingsStore.isLocalBrowserEnabled && browserConnection.online,
              disabled: browserConnection.knownOffline,
            }"
            :aria-pressed="settingsStore.isLocalBrowserEnabled && browserConnection.online"
            :aria-disabled="browserConnection.knownOffline"
            :aria-label="$t('localBrowser.local')"
            @click.stop="emit('toggleBrowserSource')"
          >
            <BrowserIcon class="control-icon" />
          </button>
        </t-tooltip>

        <t-tooltip
          v-if="showWebSearchButton"
          placement="top"
          theme="light"
          :popup-props="{ overlayClassName: 'input-field-tooltip' }"
        >
          <template #content>
            <span v-if="isWebSearchConfigured">{{
              isWebSearchEnabled ? $t('input.webSearch.toggleOff') : $t('input.webSearch.toggleOn')
            }}</span>
            <div v-else class="tooltip-with-link">
              <span>{{ $t('input.webSearch.notConfigured') }}</span>
              <a href="#" @click.prevent="emit('goToWebSearchConfig')">{{ $t('input.goToAgentSettings') }}</a>
            </div>
          </template>
          <button
            type="button"
            class="control-btn websearch-btn"
            :class="{
              active: isWebSearchEnabled && isWebSearchConfigured,
              disabled: !isWebSearchConfigured,
            }"
            :aria-pressed="isWebSearchEnabled && isWebSearchConfigured"
            :aria-label="
              isWebSearchConfigured
                ? isWebSearchEnabled
                  ? $t('input.webSearch.toggleOff')
                  : $t('input.webSearch.toggleOn')
                : $t('input.webSearch.notConfigured')
            "
            @click.stop="emit('toggleWebSearch')"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 18 18"
              fill="none"
              xmlns="http://www.w3.org/2000/svg"
              class="control-icon websearch-icon"
              :class="{ active: isWebSearchEnabled && isWebSearchConfigured }"
              aria-hidden="true"
            >
              <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none" />
              <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
              <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
              <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
              <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
            </svg>
          </button>
        </t-tooltip>

        <t-tooltip
          v-if="showImageUploadButton"
          placement="top"
          theme="light"
          :popup-props="{ overlayClassName: 'input-field-tooltip' }"
        >
          <template #content>
            <span>{{ $t('chat.imageUploadTooltip') }}</span>
          </template>
          <button
            type="button"
            class="control-btn image-upload-btn"
            :class="{ active: uploadedImagesCount > 0 }"
            :aria-label="$t('chat.imageUploadTooltip')"
            @click.stop="emit('triggerImageUpload')"
          >
            <svg width="18" height="18" viewBox="0 0 1024 1024" fill="currentColor" class="control-icon" aria-hidden="true">
              <path d="M896 128H128c-35.3 0-64 28.7-64 64v640c0 35.3 28.7 64 64 64h768c35.3 0 64-28.7 64-64V192c0-35.3-28.7-64-64-64zM128 832V192h768l0.1 640H128z" />
              <path d="M352 448a96 96 0 1 0 0-192 96 96 0 0 0 0 192z" />
              <path d="M128 768l224-288 160 160 192-256L896 640v128H128z" />
            </svg>
            <span v-if="uploadedImagesCount > 0" class="image-count">{{ uploadedImagesCount }}</span>
          </button>
        </t-tooltip>

        <t-tooltip placement="top" theme="light" :popup-props="{ overlayClassName: 'input-field-tooltip' }">
          <template #content>
            <span>{{
              uploadedAttachmentsCount > 0
                ? $t('chat.attachmentWithCount', { count: uploadedAttachmentsCount })
                : $t('chat.attachmentUploadTooltip')
            }}</span>
          </template>
          <button
            type="button"
            class="control-btn attachment-upload-btn"
            :class="{ active: uploadedAttachmentsCount > 0 }"
            :aria-label="$t('chat.attachmentUploadTooltip')"
            @click.stop="emit('triggerAttachmentSelect')"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
              class="control-icon"
              aria-hidden="true"
            >
              <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
            </svg>
            <span v-if="uploadedAttachmentsCount > 0" class="attachment-count">{{
              uploadedAttachmentsCount
            }}</span>
          </button>
        </t-tooltip>

        <t-tooltip
          v-if="showVoiceButton"
          placement="top"
          theme="light"
          :popup-props="{ overlayClassName: 'input-field-tooltip' }"
        >
          <template #content>
            <span v-if="voiceState === 'preparing'">{{ $t('input.voicePreparing') }}</span>
            <span v-else-if="voiceState === 'recording'">{{ $t('input.voiceRecording') }}</span>
            <span v-else-if="voiceState === 'transcribing'">{{ $t('input.voiceTranscribing') }}</span>
            <span v-else>{{ $t('input.voiceTooltip', { shortcut: voiceHotkeyText }) }}</span>
          </template>
          <button
            type="button"
            class="control-btn voice-input-btn"
            :class="{
              'is-preparing': voiceState === 'preparing',
              'is-recording': voiceState === 'recording',
              'is-transcribing': voiceState === 'transcribing',
            }"
            :aria-label="$t('input.voiceTooltip', { shortcut: voiceHotkeyText })"
            :aria-pressed="voiceState === 'preparing' || voiceState === 'recording'"
            :disabled="voiceState === 'transcribing'"
            @click.stop
            @pointerdown="emit('voicePointerDown', $event)"
            @pointerup="emit('voicePointerUp')"
            @pointercancel="emit('voicePointerUp')"
            @lostpointercapture="emit('voicePointerUp')"
          >
            <t-loading v-if="voiceState === 'transcribing'" size="14px" />
            <svg
              v-else
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
              class="control-icon"
              aria-hidden="true"
            >
              <rect x="9" y="3" width="6" height="11" rx="3" />
              <path d="M5 11a7 7 0 0 0 14 0" />
              <path d="M12 18v3" />
            </svg>
            <span v-if="voiceState === 'recording'" class="voice-elapsed">{{ voiceElapsedText }}</span>
          </button>
        </t-tooltip>

        <t-tooltip placement="top" theme="light" :popup-props="{ overlayClassName: 'input-field-tooltip' }">
          <template #content>
            <div v-if="isMentionDisabled && isKnowledgeBaseDisabledByAgent" class="tooltip-with-link">
              <span>{{ $t('input.kbDisabledByAgent') }}</span>
              <a href="#" @click.prevent="emit('goToAgentKnowledgeSettings')">{{
                $t('input.goToAgentSettings')
              }}</a>
            </div>
            <span v-else>{{
              allSelectedItemsCount > 0
                ? $t('input.knowledgeBaseWithCount', { count: allSelectedItemsCount })
                : $t('input.knowledgeBase')
            }}</span>
          </template>
          <button
            ref="atButtonEl"
            type="button"
            class="control-btn kb-btn"
            data-guide="chat-kb-mention"
            :class="{
              active: allSelectedItemsCount > 0,
              disabled: isMentionDisabled,
            }"
            :aria-disabled="isMentionDisabled"
            :aria-label="$t('input.knowledgeBase')"
            @click.stop
            @mousedown.prevent="emit('triggerMention')"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 20 20"
              fill="none"
              xmlns="http://www.w3.org/2000/svg"
              class="control-icon at-icon"
              aria-hidden="true"
            >
              <circle cx="10" cy="10" r="3.5" stroke="currentColor" stroke-width="1.8" />
              <path
                d="M13.5 10V11.5C13.5 12.163 13.7634 12.7989 14.2322 13.2678C14.7011 13.7366 15.337 14 16 14C16.663 14 17.2989 13.7366 17.7678 13.2678C18.2366 12.7989 18.5 12.163 18.5 11.5V10C18.5 7.74566 17.6045 5.58365 16.0104 3.98959C14.4163 2.39553 12.2543 1.5 10 1.5C7.74566 1.5 5.58365 2.39553 3.98959 3.98959C2.39553 5.58365 1.5 7.74566 1.5 10C1.5 12.2543 2.39553 14.4163 3.98959 16.0104C5.58365 17.6045 7.74566 18.5 10 18.5H12"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>
        </t-tooltip>
        <slot name="background-work" />
      </template>

      <slot v-if="embeddedMode" name="background-work" />
      <slot name="controls" />

      <t-tooltip :content="modelTriggerTooltip" :disabled="!modelTriggerTooltip">
        <div class="model-display" :class="{ 'agent-controlled': isModelLockedByAgent }">
          <button
            ref="modelButtonEl"
            type="button"
            class="model-selector-trigger"
            :aria-expanded="showModelSelector"
            @click.stop="emit('toggleModelSelector')"
          >
            <span class="model-selector-name">
              {{ selectedModelDisplayName }}
            </span>
            <span v-if="followsDefaultModel && composerModel" class="model-selector-default">
              {{ $t('input.modelAuto') }}
            </span>
            <span
              v-else-if="selectedModelContextLabel"
              class="model-selector-ctx"
              :class="{ 'is-default': selectedModelContextIsDefault }"
              :title="selectedModelContextTitle"
            >{{ selectedModelContextLabel }}</span>
            <svg
              width="12"
              height="12"
              viewBox="0 0 12 12"
              fill="currentColor"
              class="model-dropdown-arrow"
              :class="{ rotate: showModelSelector }"
              aria-hidden="true"
            >
              <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
            </svg>
          </button>
        </div>
      </t-tooltip>

      <t-popup
        v-if="!embeddedMode && reasoningLevels.length > 0"
        :visible="showReasoningSelector"
        trigger="click"
        placement="top-right"
        :disabled="composerLocked"
        :overlay-inner-style="{ padding: '4px', borderRadius: 'var(--app-radius-lg)' }"
        @visible-change="(val: boolean) => { emit('update:showReasoningSelector', val); emit('reasoningVisibleChange', val); }"
      >
        <button
          type="button"
          class="model-selector-trigger reasoning-effort-trigger"
          :disabled="composerLocked"
          :class="{ disabled: composerLocked }"
          :aria-label="`${$t('modelSettings.debug.reasoningEffort')}: ${$t(levelLabelKey(displayedReasoningLevel))}`"
          :title="$t('modelSettings.debug.reasoningEffort')"
          aria-haspopup="menu"
          :aria-expanded="showReasoningSelector"
          @keydown.esc="emit('update:showReasoningSelector', false)"
        >
          <span class="model-selector-name">{{ $t(levelLabelKey(displayedReasoningLevel)) }}</span>
          <svg
            width="12"
            height="12"
            viewBox="0 0 12 12"
            fill="currentColor"
            class="model-dropdown-arrow"
            :class="{ rotate: showReasoningSelector }"
            aria-hidden="true"
          >
            <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
          </svg>
        </button>
        <template #content>
          <div
            class="reasoning-effort-menu"
            role="menu"
            :aria-label="$t('modelSettings.debug.reasoningEffort')"
            @keydown.esc="emit('update:showReasoningSelector', false)"
          >
            <div class="reasoning-effort-title" role="presentation">
              {{ $t('modelSettings.debug.reasoningEffort') }}
            </div>
            <button
              v-for="level in reasoningLevels"
              :key="level"
              type="button"
              role="menuitemradio"
              class="reasoning-effort-option"
              :class="{ selected: level === displayedReasoningLevel }"
              :aria-checked="level === displayedReasoningLevel"
              @click="emit('selectReasoningLevel', level)"
            >
              <span>{{ $t(levelLabelKey(level)) }}</span>
              <t-icon v-if="level === displayedReasoningLevel" name="check" size="14px" />
            </button>
          </div>
        </template>
      </t-popup>
    </div>

    <div class="control-right">
      <t-tooltip v-if="isReplying && (!canSteer || !query.trim())" :content="$t('input.stopGeneration')" placement="top">
        <button type="button" class="control-btn stop-btn" :aria-label="$t('input.stopGeneration')" @click="emit('stop')">
          <t-icon name="stop" />
        </button>
      </t-tooltip>
      <t-tooltip v-else :content="`${isReplying && canSteer ? $t('input.steerAfter') : $t('input.send')} · Enter`">
        <button
          type="button"
          class="control-btn send-btn"
          data-guide="chat-send"
          :disabled="!query.trim() || composerLocked"
          :class="{ disabled: !query.trim() || composerLocked }"
          :aria-label="isReplying && canSteer ? $t('input.steerAfter') : $t('input.send')"
          @click="emit('send')"
        >
          <t-icon name="arrow-up" />
        </button>
      </t-tooltip>
    </div>
  </div>
</template>

<style scoped lang="less">
@import './input-field.less';
</style>
