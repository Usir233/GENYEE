<template>
  <SettingsModalShell
    :visible="visible"
    :title="editorMode === 'create' ? $t('agent.editor.createTitle') : $t('agent.editor.editTitle')"
    v-model="currentSection"
    :nav-groups="navGroups"
    :loading="editorInitializing"
    overlay-class="agent-editor-shell"
    :z-index="1000"
    nav-guide="agent-editor-sidebar"
    nav-item-guide-prefix="agent-editor-nav"
    @close="modalShell.requestClose"
  >
    <div
      ref="contentWrapperRef"
      class="content-wrapper"
      :class="{ 'content-wrapper--prompts': currentSection === 'prompts' }"
    >
      <AgentBasicSection
        :current-section="currentSection"
        :editor-mode="editorMode"
        :editor-agent-id="editorAgent?.id"
        :is-post-create-session="isPostCreateSession"
        :is-builtin-agent="isBuiltinAgent"
        :agent-mode="agentMode"
        :is-agent-mode="isAgentMode"
        :agent-type="agentType"
        :agent-type-select-options="agentTypeSelectOptions"
        :active-preset-description="activeAgentTypePreset ? agentTypePresetDescription(activeAgentTypePreset) : ''"
        :agent-i-m-channel-count="agentIMChannelCount"
        :agent-embed-channel-count="agentEmbedChannelCount"
        :form-data="formData"
        @copy-agent-id="copyAgentId"
        @goto-integrations="gotoIntegrations"
        @update:agent-mode="(val) => { agentMode = val }"
        @change-agent-type="onAgentTypeChange"
        @update-form="(patch) => Object.assign(formData, patch)"
        @update-config="applyConfigPatch"
      />

      <AgentPromptsSection
        :current-section="currentSection"
        :read-only="props.readOnly"
        :is-builtin-agent="isBuiltinAgent"
        :is-agent-mode="isAgentMode"
        :has-knowledge-base="hasKnowledgeBase"
        :form-data="formData"
        :prompt-nav-items="promptNavItems"
        v-model:active-prompt-anchor="activePromptAnchor"
        :placeholder-data="placeholderData"
        :available-placeholders="availablePlaceholders"
        :context-template-placeholders="contextTemplatePlaceholders"
        :rewrite-system-placeholders="rewriteSystemPlaceholders"
        :rewrite-placeholders="rewritePlaceholders"
        :fallback-placeholders="fallbackPlaceholders"
        :intent-prompt-templates="intentPromptTemplates"
        v-model:selected-intent="selectedIntent"
        v-model:intent-editor-value="intentEditorValue"
        :current-intent-placeholder="currentIntentTemplate?.content"
        :current-intent-template-desc="currentIntentTemplateDesc"
        :is-intent-customized="isIntentCustomized"
        :default-rewrite-prompt-system="defaultRewritePromptSystem"
        :default-rewrite-prompt-user="defaultRewritePromptUser"
        :default-fallback-response="defaultFallbackResponse"
        :default-fallback-prompt="defaultFallbackPrompt"
        :prompt-popups="promptPopups"
        :filtered-placeholders-by-type="filteredPlaceholdersByType"
        :set-prompt-textarea-ref="setPromptTextareaRef"
        @update-config="applyConfigPatch"
        @placeholder-click="handlePlaceholderClick"
        @prompt-field-input="handlePromptFieldInput"
        @insert-prompt-placeholder="insertPromptPlaceholder"
        @hover-popup-item="(type, index) => { promptPopups[type].selectedIndex = index }"
        @reset-current-intent-prompt="resetCurrentIntentPrompt"
        @system-prompt-template-select="handleSystemPromptTemplateSelect"
        @agent-system-prompt-reset-default="handleAgentSystemPromptResetDefault"
        @context-template-select="handleContextTemplateSelect"
        @rewrite-template-select="handleRewriteTemplateSelect"
        @fallback-response-template-select="handleFallbackResponseTemplateSelect"
        @fallback-prompt-template-select="handleFallbackPromptTemplateSelect"
      />

      <AgentModelSection
        :current-section="currentSection"
        :is-agent-mode="isAgentMode"
        :highlighted-field="highlightedField"
        :form-data="formData"
        v-model:max-completion-tokens-mode="maxCompletionTokensMode"
        :effective-default-max-completion-tokens="effectiveDefaultMaxCompletionTokens"
        :has-selected-chat-model="Boolean(selectedChatModel)"
        :selected-chat-model-can-think="selectedChatModelCanThink"
        :selected-chat-model-always-thinks="selectedChatModelAlwaysThinks"
        :reasoning-effort-options="reasoningEffortOptions"
        v-model:reasoning-effort-level="reasoningEffortLevel"
        :show-rerank-model-field="showRerankModelField"
        :needs-rerank-model="needsRerankModel"
        v-model:max-iterations-mode="maxIterationsMode"
        @update-config="applyConfigPatch"
      >
        <template #llm-selector>
          <ModelSelector model-type="KnowledgeQA" :selected-model-id="formData.config.model_id"
            :all-models="allModels"
            @update:selected-model-id="(val: string) => formData.config.model_id = val"
            @add-model="handleAddModel('llm')" :placeholder="$t('agent.editor.modelPlaceholder')" />
        </template>
        <template #rerank-selector>
          <ModelSelector model-type="Rerank" :selected-model-id="formData.config.rerank_model_id"
            :all-models="allModels"
            :clearable="!needsRerankModel"
            @update:selected-model-id="(val: string) => formData.config.rerank_model_id = val"
            @add-model="handleAddModel('rerank')"
            :placeholder="$t('agent.editor.rerankModelPlaceholder')" />
        </template>
        <template #rewrite-model-selector>
          <ModelSelector model-type="KnowledgeQA"
            :selected-model-id="formData.config.query_understand_model_id" :all-models="allModels"
            clearable
            @update:selected-model-id="(val: string) => formData.config.query_understand_model_id = val"
            @add-model="handleAddModel('llm')"
            :placeholder="$t('agent.editor.queryUnderstandModelPlaceholder')" />
        </template>
      </AgentModelSection>

      <AgentAttachmentsConversationSection
        :current-section="currentSection"
        :is-agent-mode="isAgentMode"
        :has-knowledge-base="hasKnowledgeBase"
        :form-data="formData"
        :storage-engine-status="storageEngineStatus"
        @update-config="applyConfigPatch"
        @update-starters="(patch) => Object.assign(formData.config.question_suggestions.starters, patch)"
        @update-starter-item="(idx, val) => { formData.config.question_suggestions.starters.items[idx] = val }"
        @add-starter-suggestion="addStarterSuggestion"
        @remove-starter-suggestion="removeStarterSuggestion"
        @update-follow-ups="(patch) => Object.assign(formData.config.question_suggestions.follow_ups, patch)"
        @open-storage-settings="uiStore.openSettings('storage')"
      >
        <template #vlm-selector>
          <ModelSelector model-type="VLLM" :selected-model-id="formData.config.vlm_model_id"
            :all-models="allModels"
            @update:selected-model-id="(val: string) => formData.config.vlm_model_id = val"
            @add-model="handleAddModel('vllm')"
            :placeholder="$t('agentEditor.imageUpload.vlmModelPlaceholder')" />
        </template>
        <template #asr-selector>
          <ModelSelector model-type="ASR" :selected-model-id="formData.config.asr_model_id"
            :all-models="allModels"
            clearable
            @update:selected-model-id="(val: string) => formData.config.asr_model_id = val"
            @add-model="handleAddModel('asr')"
            :placeholder="$t('agentEditor.audioUpload.asrModelPlaceholder')" />
        </template>
        <template #followup-model-selector>
          <ModelSelector model-type="KnowledgeQA"
            :selected-model-id="formData.config.question_suggestions.follow_ups.model_id"
            :all-models="allModels"
            clearable
            @update:selected-model-id="(val: string) => formData.config.question_suggestions.follow_ups.model_id = val"
            @add-model="handleAddModel('summary')" />
        </template>
      </AgentAttachmentsConversationSection>

      <AgentToolsMcpSkillsSection
        :current-section="currentSection"
        :is-agent-mode="isAgentMode"
        :highlighted-field="highlightedField"
        :form-data="formData"
        :kb-selection-mode="kbSelectionMode"
        :has-knowledge-base="hasKnowledgeBase"
        :rag-kb-count="ragKbCount"
        :wiki-kb-count="wikiKbCount"
        :scope-capabilities="scopeCapabilities"
        :agent-tool-catalog="agentToolCatalog"
        :stored-tool-names="storedToolNames"
        :allowed-tools-to-save="allowedToolsToSave"
        v-model:mcp-selection-mode="mcpSelectionMode"
        :mcp-options="mcpOptions"
        :show-mcp-service-select="showMcpServiceSelect"
        :host-only="hostOnly"
        :has-sandbox-selected="hasSandboxSelected"
        :can-enable-skills="canEnableSkills"
        :can-install-skills="canInstallSkills"
        v-model:skills-selection-mode="skillsSelectionMode"
        :skills-selection-hint="skillsSelectionHint"
        :skill-catalog-count="skillCatalog.length"
        :show-catalog-skill-list="showCatalogSkillList"
        :catalog-skill-groups="catalogSkillGroups"
        :installing-catalog-id="installingCatalogId"
        :sandbox-config-options="sandboxConfigOptions"
        :selected-sandbox-summary="selectedSandboxSummary"
        :backend-label="backendLabel"
        :sandbox-target-line="sandboxTargetLine"
        :skill-status-hint="skillStatusHint"
        :skill-status-icon="skillStatusIcon"
        :is-skill-busy="isSkillBusy"
        :can-install-skill-row="canInstallSkillRow"
        :can-upgrade-skill-row="canUpgradeSkillRow"
        :installs-an-upgrade="installsAnUpgrade"
        :skill-upgrade-hint="skillUpgradeHint"
        @update-config="applyConfigPatch"
        @open-sandbox-settings="uiStore.openSettings('sandbox')"
        @open-skill-settings="openSkillSettings"
        @install-catalog-skill="installCatalogToCurrent"
        @open-skill-install-progress="openSkillInstallProgress"
      />

      <AgentRetrievalWebSection
        :current-section="currentSection"
        :is-agent-mode="isAgentMode"
        :has-knowledge-base="hasKnowledgeBase"
        :has-faq-knowledge-base="hasFaqKnowledgeBase"
        v-model:kb-selection-mode="kbSelectionMode"
        :filtered-my-kb-options="filteredMyKbOptions"
        :filtered-shared-kb-options="filteredSharedKbOptions"
        :web-search-provider-list="webSearchProviderList"
        :form-data="formData"
        @update-config="applyConfigPatch"
      />

      <!-- Share Management (Edit mode & non-builtin & non-readOnly) -->
      <div v-if="editorMode === 'edit' && !props.readOnly && editorAgent?.id && !editorAgent?.is_builtin"
        v-show="currentSection === 'share'" class="section">
        <AgentShareSettings :agent-id="editorAgent.id" :agent="editorAgent" />
      </div>
    </div>

    <template #footer-note>
      <p v-if="isPostCreateSession" class="settings-footer-note">
        <t-icon name="check-circle-filled" class="settings-footer-note__icon" />
        <span>
          <strong>{{ $t('agent.editor.postCreateHint.title') }}</strong>
          {{ $t('agent.editor.postCreateHint.footer') }}
        </span>
      </p>
    </template>
    <template #footer>
      <t-button variant="outline" @click="modalShell.requestClose">
        {{ props.readOnly ? $t('common.close') : $t('common.cancel') }}
      </t-button>
      <t-button v-if="!props.readOnly" theme="primary" data-guide="agent-create-submit" :loading="saving"
        :disabled="editorInitializing" @click="handleSave">
        {{ saveButtonLabel }}
      </t-button>
    </template>
  </SettingsModalShell>

  <SettingDrawer
    v-model:visible="showSkillProgress"
    :title="skillProgressTitle"
    :description="skillProgressDesc"
    :icon="SKILL_ICON"
    width="680px"
    :min-width="560"
    :max-width="920"
    storage-key="setting-drawer:width:skill-catalog-manage"
    :hide-footer="true"
  >
    <SandboxSkillsPanel
      v-if="showSkillProgress && skillProgressRecord && skillProgressId"
      :record="skillProgressRecord"
      mode="list"
      hide-add
      :focus-skill-id="skillProgressId"
      @updated="onSkillProgressUpdated"
      @skills-changed="onSkillProgressChanged"
    />
  </SettingDrawer>

  <AgentCreateContextualGuide :when="visible && editorMode === 'create'" :is-agent-mode="isAgentMode" />
</template>

<script setup lang="ts">
import { useAssistantDataChange } from '@/utils/assistantDataChange';
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue';
import { useRouter } from 'vue-router';
import AgentCreateContextualGuide from '@/components/AgentCreateContextualGuide.vue';
import {
  AGENT_EDITOR_FOCUS_SECTION_EVENT,
  markContextualGuideDone,
} from '@/config/contextualGuides';
import { useI18n } from 'vue-i18n';
import { serializeAgentPrompts } from '@/utils/agentPromptTemplates';
import { copyWithToast } from '@/utils/clipboard';
import { MessagePlugin } from 'tdesign-vue-next';
import { useModalShell } from '@/composables/useModalShell';
import { useAssistantPageContext } from '@/composables/useAssistantPageContext';
import SettingsModalShell from '@/components/SettingsModalShell.vue';
import {
  createAgent,
  updateAgent,
  listIMChannels,
  type CustomAgent,
  type AgentTypePreset,
  type AgentToolCatalog,
  type AllowedToolsWarnings,
  type AgentType,
} from '@/api/agent';
import { type AgentNotReadyReasonKey, agentRequiresRerankModel } from '@/utils/agent-readiness';
import { normalizeLegacyToolNames } from '@/utils/legacy-tool-names';
import { installSkillCatalog } from '@/api/skill';
import { type WebSearchProviderEntity } from '@/api/web-search-provider';
import { isNamedSandboxBackend } from '@/api/system';
import { useUIStore } from '@/stores/ui';
import { useAuthStore } from '@/stores/auth';
import { useOrganizationStore } from '@/stores/organization';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities';
import { useEditorResourcesStore } from '@/stores/editorResources';
import ModelSelector from '@/components/ModelSelector.vue';
import SandboxSkillsPanel from '@/components/SandboxSkillsPanel.vue';
import SettingDrawer from '@/components/settings/SettingDrawer.vue';
import AgentShareSettings from '@/components/AgentShareSettings.vue';
import { SKILL_ICON } from '@/types/mention';
import { listEmbedChannels } from '@/api/embed';
import { integrationSectionKey } from '@/config/settingsRoute';
import AgentBasicSection from '@/views/agent/components/editor/AgentBasicSection.vue';
import AgentPromptsSection from '@/views/agent/components/editor/AgentPromptsSection.vue';
import AgentModelSection from '@/views/agent/components/editor/AgentModelSection.vue';
import AgentAttachmentsConversationSection from '@/views/agent/components/editor/AgentAttachmentsConversationSection.vue';
import AgentToolsMcpSkillsSection from '@/views/agent/components/editor/AgentToolsMcpSkillsSection.vue';
import AgentRetrievalWebSection from '@/views/agent/components/editor/AgentRetrievalWebSection.vue';
import {
  useAgentEditorForm,
  mapKbToOption,
  type AgentEditorFormConfig,
  type RawKnowledgeBaseItem,
  type SharedKnowledgeBaseEntry,
} from '@/views/agent/composables/useAgentEditorForm';
import { useAgentTypePresets } from '@/views/agent/composables/useAgentTypePresets';
import { useAgentPromptPlaceholders } from '@/views/agent/composables/useAgentPromptPlaceholders';
import { useAgentSkillsSandbox } from '@/views/agent/composables/useAgentSkillsSandbox';

const uiStore = useUIStore();
const authStore = useAuthStore();
const deploymentCapabilities = useDeploymentCapabilitiesStore();
const router = useRouter();
const orgStore = useOrganizationStore();
const chatResources = useChatResourcesStore();
const editorResources = useEditorResourcesStore();
const { t, locale: i18nLocale } = useI18n();

const props = defineProps<{
  visible: boolean;
  mode: 'create' | 'edit';
  agent?: CustomAgent | null;
  initialSection?: string;
  initialHighlightField?: string;
  readOnly?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:visible', visible: boolean): void;
  (e: 'success', agent?: CustomAgent): void;
}>();

function getErrorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const msg = (err as { message?: unknown }).message;
    if (typeof msg === 'string' && msg) return msg;
  }
  return fallback;
}

const savedAgent = ref<CustomAgent | null>(null);
const editorMode = computed(() => (savedAgent.value ? 'edit' : props.mode));
const editorAgent = computed(() => savedAgent.value ?? props.agent ?? null);
const isPostCreateSession = computed(() => !!savedAgent.value);

useAssistantDataChange((changes) => {
  const agentId = editorAgent.value?.id;
  if (!props.visible || !agentId) return;
  const relevant = changes.filter(change => change.ids.agentIds.includes(agentId)
    && change.operation !== 'agent.copy');
  if (!relevant.length) return;
  const deleted = relevant.some(change => change.operation === 'agent.delete');
  MessagePlugin.warning(t(deleted ? 'agent.editor.assistantDeleted' : 'agent.editor.assistantChanged'));
}, { areas: ['agent'] });

const saveButtonLabel = computed(() =>
  editorMode.value === 'create'
    ? t('agent.editor.buttons.create')
    : t('agent.editor.buttons.saveAndClose')
);

const copyAgentId = async () => {
  await copyWithToast(editorAgent.value?.id, 'common.copied');
};

const AGENT_EDITOR_SECTION_ALIASES: Record<string, string> = {
  sandbox: 'skills',
};

function resolveEditorSection(section?: string | null): string {
  const key = section || 'basic';
  return AGENT_EDITOR_SECTION_ALIASES[key] || key;
}

const currentSection = ref(resolveEditorSection(props.initialSection));
useAssistantPageContext(() => props.visible && props.mode === 'edit' ? { section: currentSection.value } : null);

const saving = ref(false);
const editorInitializing = ref(false);
const contentWrapperRef = ref<HTMLElement | null>(null);
const highlightedField = ref<AgentNotReadyReasonKey | null>(null);
let highlightClearTimer: ReturnType<typeof setTimeout> | null = null;

const VALID_HIGHLIGHT_FIELDS: AgentNotReadyReasonKey[] = ['summary_model', 'rerank_model', 'allowed_tools'];
const FIELD_FLASH_DURATION_MS = 2400;

const sectionForHighlightField = (field: AgentNotReadyReasonKey): string =>
  field === 'allowed_tools' ? 'tools' : 'model';

const clearFieldHighlight = () => {
  if (highlightClearTimer) {
    clearTimeout(highlightClearTimer);
    highlightClearTimer = null;
  }
  highlightedField.value = null;
};

const applyInitialFieldHighlight = async (field: string) => {
  if (!VALID_HIGHLIGHT_FIELDS.includes(field as AgentNotReadyReasonKey)) return;
  const targetField = field as AgentNotReadyReasonKey;
  currentSection.value = sectionForHighlightField(targetField);

  await nextTick();
  await new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  });

  clearFieldHighlight();
  highlightedField.value = null;

  const wrapper = contentWrapperRef.value;
  const row = wrapper?.querySelector(`[data-agent-field="${targetField}"]`) as HTMLElement | null;
  if (row && wrapper) {
    const rowTop = row.offsetTop;
    const scrollTarget = rowTop - wrapper.clientHeight / 2 + row.clientHeight / 2;
    wrapper.scrollTo({ top: Math.max(0, scrollTarget), behavior: 'auto' });

    await nextTick();
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
    });
  }

  highlightedField.value = targetField;
  if (row) {
    const focusTarget = row.querySelector('.t-input, .t-select-input, input, .t-checkbox') as HTMLElement | null;
    focusTarget?.focus({ preventScroll: true });
  }

  highlightClearTimer = setTimeout(() => {
    if (highlightedField.value === targetField) {
      highlightedField.value = null;
    }
    highlightClearTimer = null;
  }, FIELD_FLASH_DURATION_MS);
};

let syncInstalledSkillsFn: (force?: boolean) => Promise<void> = async () => {};

const {
  formData,
  allModels,
  kbOptions,
  agentTypePresets,
  agentSystemPromptTemplates,
  promptTemplates,
  intentPromptTemplates,
  agentToolCatalog,
  storageEngineStatus,
  webSearchProviderList,
  storedToolNames,
  keepAllowedToolsUnset,
  isInitializing,
  kbSelectionMode,
  mcpSelectionMode,
  defaultRewritePromptSystem,
  defaultRewritePromptUser,
  defaultFallbackPrompt,
  defaultFallbackResponse,
  agentMode,
  isAgentMode,
  isBuiltinAgent,
  hasKnowledgeBase,
  ragKbCount,
  wikiKbCount,
  hasFaqKnowledgeBase,
  scopeCapabilities,
  allowedToolsToSave,
  showRerankModelField,
  applyDefaultModelsIfEmpty,
  effectiveDefaultMaxCompletionTokens,
  maxCompletionTokensMode,
  maxIterationsMode,
  selectedChatModel,
  selectedChatModelCanThink,
  selectedChatModelAlwaysThinks,
  reasoningEffortOptions,
  reasoningEffortLevel,
  clampReasoningEffortToModel,
  addStarterSuggestion,
  removeStarterSuggestion,
  initKbSelectionMode,
  initMcpSelectionMode,
  fillBuiltinAgentDefaults,
  applyPromptTemplateDefaults,
  applyTenantRetrievalDefaults,
  hydrateEditAgentData,
  buildFreshCreateFormData,
  validateAgentFormBeforeSave,
} = useAgentEditorForm({
  props,
  editorInitializing,
  currentSection,
  t: (key, params) => (params ? t(key, params) : t(key)),
  onValidationError: (msg, targetSection) => {
    MessagePlugin.error(msg);
    currentSection.value = targetSection;
  },
  onSandboxConfigChanged: () => syncInstalledSkillsFn(),
});

const applyConfigPatch = (patch: Partial<AgentEditorFormConfig>) => {
  Object.assign(formData.value.config, patch);
};

const {
  agentType,
  activeAgentTypePreset,
  agentTypePresetDescription,
  agentTypeSelectOptions,
  getPresetDefaultName,
  getPresetDefaultDescription,
  filteredMyKbOptions,
  filteredSharedKbOptions,
  applyAgentTypePreset,
  onAgentTypeChange,
} = useAgentTypePresets({
  formData,
  agentMode,
  isAgentMode,
  kbSelectionMode,
  kbOptions,
  agentTypePresets,
  agentSystemPromptTemplates,
  i18nLocale,
  t: (key, params) => (params ? t(key, params) : t(key)),
  onWarn: (content, duration) => {
    MessagePlugin.warning(content, duration);
  },
});

const {
  placeholderData,
  availablePlaceholders,
  contextTemplatePlaceholders,
  rewriteSystemPlaceholders,
  rewritePlaceholders,
  fallbackPlaceholders,
  activePromptAnchor,
  promptNavItems,
  selectedIntent,
  intentEditorValue,
  currentIntentTemplate,
  currentIntentTemplateDesc,
  isIntentCustomized,
  resetCurrentIntentPrompt,
  promptPopups,
  filteredPlaceholdersByType,
  setPromptTextareaRef,
  handlePromptFieldInput,
  insertPromptPlaceholder,
  handlePlaceholderClick,
  closeAllPlaceholderPopups,
  clearAllPlaceholderTimers,
  hasOpenPlaceholderPopup,
  handleSystemPromptTemplateSelect,
  handleAgentSystemPromptResetDefault,
  handleContextTemplateSelect,
  handleRewriteTemplateSelect,
  handleFallbackResponseTemplateSelect,
  handleFallbackPromptTemplateSelect,
} = useAgentPromptPlaceholders({
  props,
  formData,
  currentSection,
  isAgentMode,
  hasKnowledgeBase,
  agentType,
  agentTypePresets,
  agentSystemPromptTemplates,
  intentPromptTemplates,
  t: (key, params) => (params ? t(key, params) : t(key)),
});

let requestModalCloseWithCallback: (cb: () => void) => void = (cb) => cb();

const {
  skillCatalog,
  installingCatalogId,
  skillsSelectionMode,
  hostOnly,
  hasSandboxSelected,
  canEnableSkills,
  canInstallSkills,
  showCatalogSkillList,
  skillsSelectionHint,
  catalogSkillGroups,
  skillStatusHint,
  skillStatusIcon,
  isSkillBusy,
  canInstallSkillRow,
  canUpgradeSkillRow,
  installsAnUpgrade,
  skillUpgradeHint,
  openSkillSettings,
  showSkillProgress,
  skillProgressRecord,
  skillProgressId,
  skillProgressTitle,
  skillProgressDesc,
  openSkillInstallProgress,
  onSkillProgressUpdated,
  onSkillProgressChanged,
  pruneSelectedSkills,
  syncInstalledSkills,
  installCatalogToCurrent,
  sandboxConfigOptions,
  backendLabel,
  sandboxTargetLine,
  selectedSandboxSummary,
  initSkillsSelectionMode,
  stopCatalogPoll,
  resetSkillProgressDrawer,
} = useAgentSkillsSandbox({
  props,
  formData,
  t: (key, params) => (params ? t(key, params) : t(key)),
  message: MessagePlugin,
  hasAdminRole: () => Boolean(authStore.hasRole?.('admin')),
  isRemoteSandboxSupported: () => deploymentCapabilities.isSupported('settings.sandbox.remote'),
  isHostSandboxSupported: () => deploymentCapabilities.isSupported('settings.sandbox.host'),
  getSandboxConfigs: () => chatResources.sandboxConfigs || [],
  isNamedSandboxBackend: (type) => isNamedSandboxBackend(type || ''),
  ensureSkills: (configId, sourceTenantId, force) => editorResources.ensureSkills(configId, sourceTenantId, force),
  ensureSkillCatalog: (force) => editorResources.ensureSkillCatalog(force),
  getSkillCatalog: () => editorResources.skillCatalog || [],
  installSkillCatalog,
  requestCloseAndNavigate: (location) => {
    requestModalCloseWithCallback(() => {
      void router.push(location);
    });
  },
  setIntervalFn: (handler, timeoutMs) => window.setInterval(handler, timeoutMs),
  clearIntervalFn: (handle) => window.clearInterval(handle as number),
});
syncInstalledSkillsFn = syncInstalledSkills;

const needsRerankModel = computed(() => {
  if (isAgentMode.value) {
    return agentRequiresRerankModel({
      kb_selection_mode: kbSelectionMode.value,
      allowed_tools: allowedToolsToSave(),
    });
  }
  if (!hasKnowledgeBase.value) return false;
  const mode = kbSelectionMode.value;
  if (mode === 'all') {
    return kbOptions.value.some(kb => kb.ragEnabled);
  }
  if (mode === 'selected') {
    const selectedIds = formData.value.config.knowledge_bases || [];
    return kbOptions.value.some(kb => selectedIds.includes(kb.value) && kb.ragEnabled);
  }
  return false;
});

type McpSelectOption = { label: string; value: string; disabled?: boolean };
const mcpOptions = computed<McpSelectOption[]>(() => {
  const services = editorResources.mcpServices || [];
  const selectedIds = new Set<string>(formData.value.config.mcp_services || []);
  const serviceById = new Map(services.map((mcp) => [mcp.id, mcp]));
  const options: McpSelectOption[] = [];

  for (const mcp of services) {
    if (mcp.enabled) {
      options.push({ label: mcp.name, value: mcp.id });
    }
  }
  for (const id of selectedIds) {
    const mcp = serviceById.get(id);
    if (mcp && !mcp.enabled) {
      options.push({
        label: `${mcp.name} (${t('mcpSettings.disabled')})`,
        value: mcp.id,
        disabled: true,
      });
    } else if (!mcp) {
      options.push({
        label: t('agentEditor.mcp.unavailableService'),
        value: id,
        disabled: true,
      });
    }
  }
  return options;
});

const showMcpServiceSelect = computed(() =>
  mcpOptions.value.length > 0 || (formData.value.config.mcp_services?.length ?? 0) > 0,
);

const navItems = computed(() => {
  const items: { key: string; icon: string; label: string; badge?: number }[] = [
    { key: 'basic', icon: 'info-circle', label: t('agent.editor.basicInfo') },
    { key: 'prompts', icon: 'file-paste', label: t('agent.editor.promptsConfig'), badge: promptNavItems.value.length > 1 ? promptNavItems.value.length : undefined },
    { key: 'model', icon: 'control-platform', label: t('agent.editor.modelConfig') },
    { key: 'suggestions', icon: 'help-circle', label: t('agentEditor.questionSuggestions.navLabel') },
  ];
  items.push({ key: 'conversation', icon: 'chat', label: t('agent.editor.conversationSettings') });
  items.push({ key: 'knowledge', icon: 'folder', label: t('agent.editor.knowledgeConfig') });
  if (hasKnowledgeBase.value && !isAgentMode.value) {
    items.push({ key: 'retrieval', icon: 'search', label: t('agent.editor.retrievalStrategy') });
  }
  items.push({ key: 'websearch', icon: 'internet', label: t('agent.editor.webSearchConfig') });
  items.push({ key: 'multimodal', icon: 'attach', label: t('agentEditor.imageUpload.navLabel') });
  if (isAgentMode.value) {
    items.push({ key: 'tools', icon: 'tools', label: t('agent.editor.toolsConfig') });
    items.push({ key: 'mcp', icon: 'server', label: t('agentEditor.mcp.label') });
    items.push({ key: 'skills', icon: SKILL_ICON, label: t('agent.editor.skillsConfig') });
  }
  if (editorMode.value === 'edit' && !props.readOnly && editorAgent.value?.id && !editorAgent.value?.is_builtin && !authStore.isLiteMode) {
    items.push({ key: 'share', icon: 'share', label: t('knowledgeEditor.sidebar.share') });
  }
  return items;
});

const navGroups = computed(() => {
  const itemMap = new Map(navItems.value.map((item) => [item.key, item]));
  const pickItems = (keys: string[]) =>
    keys.map((key) => itemMap.get(key)).filter(Boolean) as typeof navItems.value;
  return [
    {
      key: 'basic',
      label: t('agentEditor.navGroups.basic'),
      items: pickItems(['basic', 'prompts', 'model', 'conversation', 'suggestions']),
    },
    {
      key: 'knowledge',
      label: t('agentEditor.navGroups.knowledge'),
      items: pickItems(['knowledge', 'retrieval', 'websearch']),
    },
    {
      key: 'capability',
      label: t('agentEditor.navGroups.capability'),
      items: pickItems(['multimodal', 'tools', 'mcp', 'skills']),
    },
    {
      key: 'integration',
      label: t('agentEditor.navGroups.integration'),
      items: pickItems(['share']),
    },
  ].filter((group) => group.items.length > 0);
});

const onAgentEditorFocusSection = (event: Event) => {
  const section = (event as CustomEvent<{ section?: string }>).detail?.section;
  if (!section) return;
  const resolved = resolveEditorSection(section);
  if (navItems.value.some((item) => item.key === resolved)) {
    currentSection.value = resolved;
  }
};

onMounted(() => {
  window.addEventListener(AGENT_EDITOR_FOCUS_SECTION_EVENT, onAgentEditorFocusSection);
});

onBeforeUnmount(() => {
  window.removeEventListener(AGENT_EDITOR_FOCUS_SECTION_EVENT, onAgentEditorFocusSection);
  stopCatalogPoll();
  clearFieldHighlight();
  clearAllPlaceholderTimers();
});

const agentIMChannelCount = ref(0);
const agentEmbedChannelCount = ref(0);

async function loadAgentIntegrationCounts(agentId: string) {
  try {
    const [imResp, embedResp] = await Promise.all([
      listIMChannels(agentId),
      listEmbedChannels(agentId),
    ]);
    agentIMChannelCount.value = imResp?.data?.length ?? 0;
    agentEmbedChannelCount.value = embedResp?.data?.length ?? 0;
  } catch {
    agentIMChannelCount.value = 0;
    agentEmbedChannelCount.value = 0;
  }
}

function gotoIntegrations(tab: 'im' | 'embed') {
  const agentId = editorAgent.value?.id;
  if (!agentId) return;
  handleClose();
  void router.push({ path: '/platform/settings', query: { section: integrationSectionKey(tab), agentId } });
}

const loadDependencies = async () => {
  try {
    await Promise.all([
      chatResources.ensureModels(),
      chatResources.ensureKnowledgeBases(),
      chatResources.ensureWebSearchProviders(),
      chatResources.ensureSandboxConfigs(),
      editorResources.prefetchAgentEditorDeps(),
    ]);

    if (chatResources.allModels.length > 0) {
      allModels.value = chatResources.allModels;
    }

    const rawKbs = (chatResources.rawKnowledgeBases || []) as RawKnowledgeBaseItem[];
    const myKbs = rawKbs.map((kb) => mapKbToOption(kb, false));
    const myKbIds = new Set(myKbs.map((kb) => kb.value));
    const rawSharedKbs = (orgStore.sharedKnowledgeBases || []) as SharedKnowledgeBaseEntry[];
    const sharedKbs = rawSharedKbs
      .filter((entry): entry is SharedKnowledgeBaseEntry & { knowledge_base: RawKnowledgeBaseItem } =>
        Boolean(entry.knowledge_base && !myKbIds.has(entry.knowledge_base.id)),
      )
      .map((entry) => mapKbToOption(entry.knowledge_base, true, entry.org_name));
    kbOptions.value = [...myKbs, ...sharedKbs];

    agentTypePresets.value = editorResources.agentTypePresets as AgentTypePreset[];
    agentToolCatalog.value = editorResources.agentTools as AgentToolCatalog | null;
    applyPromptTemplateDefaults(editorResources.promptTemplates);
    storageEngineStatus.value = editorResources.storageStatus;
    webSearchProviderList.value = chatResources.webSearchProviders as WebSearchProviderEntity[];

    if (editorResources.placeholders) {
      placeholderData.value = editorResources.placeholders;
    }

    applyTenantRetrievalDefaults(editorResources.tenantRetrievalConfig as Record<string, number> | null);
  } catch (e) {
    console.error('Failed to load dependencies', e);
  }
};

let editorInitializationGeneration = 0;

watch(() => props.visible, async (val) => {
  const generation = ++editorInitializationGeneration;
  if (val) {
    void deploymentCapabilities.ensureLoaded();
    editorInitializing.value = true;
    try {
      savedAgent.value = null;
      currentSection.value = resolveEditorSection(props.initialSection);
      await loadDependencies();
      if (generation !== editorInitializationGeneration || !props.visible) return;

      if (props.mode === 'edit' && props.agent) {
        isInitializing.value = true;
        const hydrated = hydrateEditAgentData(props.agent);
        formData.value = hydrated;
        initKbSelectionMode();
        initMcpSelectionMode();
        initSkillsSelectionMode();
        void nextTick(() => {
          isInitializing.value = false;
        });
        fillBuiltinAgentDefaults();
        if (hydrated.id) {
          void loadAgentIntegrationCounts(hydrated.id);
        }
      } else {
        const newFormData = buildFreshCreateFormData();
        formData.value = newFormData;
        kbSelectionMode.value = 'all';
        mcpSelectionMode.value = 'none';
        skillsSelectionMode.value = 'none';

        if (newFormData.config.agent_mode === 'smart-reasoning') {
          const defaultTypeId = newFormData.config.agent_type as AgentType;
          const preset = agentTypePresets.value.find(p => p.id === defaultTypeId) || null;
          if (defaultTypeId && defaultTypeId !== 'custom') {
            applyAgentTypePreset(preset);
          }
          if (!formData.value.name) {
            formData.value.name = getPresetDefaultName(preset);
          }
          if (!formData.value.description) {
            formData.value.description = getPresetDefaultDescription(preset);
          }
        }
        applyDefaultModelsIfEmpty();
      }

      await syncInstalledSkills();
      if (generation !== editorInitializationGeneration || !props.visible) return;

      if (props.initialHighlightField) {
        await applyInitialFieldHighlight(props.initialHighlightField);
        if (generation !== editorInitializationGeneration || !props.visible) return;
      }
    } catch (error) {
      console.error('Failed to initialize agent editor', error);
    } finally {
      if (generation === editorInitializationGeneration && props.visible) {
        clampReasoningEffortToModel();
        await nextTick();
        if (generation === editorInitializationGeneration && props.visible) {
          modalShell.markClean();
          editorInitializing.value = false;
        }
      }
    }
  } else {
    editorInitializing.value = false;
    clearFieldHighlight();
    agentIMChannelCount.value = 0;
    agentEmbedChannelCount.value = 0;
    resetSkillProgressDrawer();
  }
});

watch(() => uiStore.showSettingsModal, async (visible, prevVisible) => {
  if (prevVisible && !visible && props.visible) {
    try {
      await Promise.all([
        chatResources.ensureModels(true),
        editorResources.ensureStorageEngine(true),
        chatResources.ensureSandboxConfigs(true),
      ]);
      if (chatResources.allModels.length > 0) {
        allModels.value = chatResources.allModels;
      }
      if (editorResources.storageStatus.length > 0) {
        storageEngineStatus.value = editorResources.storageStatus;
      }
      await syncInstalledSkills(true);
    } catch (e) {
      console.warn('Failed to refresh data after settings closed', e);
    }
  }
});

watch(() => chatResources.allModels, (list) => {
  if (props.visible) {
    allModels.value = list;
  }
});

const handleAddModel = (subSection: string) => {
  uiStore.openSettings('models', subSection);
};

const handleClose = () => {
  closeAllPlaceholderPopups();
  emit('update:visible', false);
};

const modalShell = useModalShell({
  visible: () => props.visible,
  loading: () => editorInitializing.value,
  close: handleClose,
  snapshot: () => (props.readOnly ? null : formData.value),
  ignoreEscape: () => hasOpenPlaceholderPopup(),
});
requestModalCloseWithCallback = (cb) => modalShell.requestClose(cb);

const notifyAllowedToolsWarnings = (warnings?: AllowedToolsWarnings) => {
  if (warnings?.kept_unknown?.length) {
    MessagePlugin.warning(t('agentEditor.tools.keptUnknownWarning', { names: warnings.kept_unknown.join(', ') }));
  }
  if (warnings?.dropped?.length) {
    MessagePlugin.warning(t('agentEditor.tools.droppedWarning', { names: warnings.dropped.join(', ') }));
  }
};

const handleSave = async () => {
  if (!validateAgentFormBeforeSave()) return;

  formData.value.config.question_suggestions.starters.items =
    formData.value.config.question_suggestions.starters.items
      .map((p: string) => p.trim())
      .filter(Boolean);

  if (!formData.value.config.intent_prompts || Object.keys(formData.value.config.intent_prompts).length === 0) {
    delete formData.value.config.intent_prompts;
  }

  pruneSelectedSkills();
  if (hostOnly.value) formData.value.config.sandbox_config_id = '';

  const payload = { ...formData.value, config: serializeAgentPrompts({ ...formData.value.config, allowed_tools: allowedToolsToSave() }, promptTemplates.value) };
  saving.value = true;
  try {
    if (editorMode.value === 'create') {
      const result = await createAgent(payload);
      const created = result?.data;
      if (!created?.id) {
        throw new Error(getErrorMessage(result, t('agent.messages.saveFailed')));
      }
      savedAgent.value = created;
      formData.value.id = created.id;
      if (Array.isArray(created.config?.allowed_tools)) {
        formData.value.config.allowed_tools = normalizeLegacyToolNames(created.config.allowed_tools);
        storedToolNames.value = [...formData.value.config.allowed_tools];
        keepAllowedToolsUnset.value = false;
      }
      modalShell.markClean();
      markContextualGuideDone('agentCreate');
      currentSection.value = 'basic';
      void loadAgentIntegrationCounts(created.id);
      MessagePlugin.success(t('agent.messages.created'));
      notifyAllowedToolsWarnings(result?.allowed_tools_warnings);
      emit('success', created);
    } else {
      if (!formData.value.id) return;
      const updated = await updateAgent(formData.value.id, payload);
      MessagePlugin.success(t('agent.messages.updated'));
      emit('success');
      handleClose();
      notifyAllowedToolsWarnings(updated?.allowed_tools_warnings);
    }
  } catch (e: unknown) {
    MessagePlugin.error(getErrorMessage(e, t('agent.messages.saveFailed')));
  } finally {
    saving.value = false;
  }
};
</script>

<style lang="less" src="./agent-editor-modal.less"></style>
