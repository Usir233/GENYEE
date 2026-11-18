<template>
  <div v-show="currentSection === 'model'" class="section">
    <div class="section-header">
      <h2>{{ $t('agent.editor.modelConfig') }}</h2>
      <p class="section-description">{{ $t('agent.editor.modelConfigDesc') }}</p>
    </div>

    <div class="settings-group">
      <!-- Primary LLM -->
      <div
        class="setting-row"
        data-guide="agent-create-model"
        data-agent-field="summary_model"
        :class="{ 'setting-row--field-highlight': highlightedField === 'summary_model' }"
      >
        <div class="setting-info">
          <label>{{ $t('agent.editor.model') }} <span class="required">*</span></label>
          <p class="desc">{{ $t('agentEditor.desc.model') }}</p>
        </div>
        <div class="setting-control">
          <slot name="llm-selector" />
        </div>
      </div>

      <!-- Temperature -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.temperature') }}</label>
          <p class="desc">{{ $t('agentEditor.desc.temperature') }}</p>
        </div>
        <div class="setting-control">
          <div class="slider-wrapper">
            <t-slider
              :model-value="formData.config.temperature"
              :min="0"
              :max="1"
              :step="0.1"
              @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { temperature: val })"
            />
            <span class="slider-value">{{ formData.config.temperature }}</span>
          </div>
        </div>
      </div>

      <!-- Max Completion Tokens -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.maxCompletionTokens') }}</label>
          <p class="desc">
            {{ isAgentMode ? $t('agentEditor.desc.maxTokensAgent') : $t('agentEditor.desc.maxTokens') }}
          </p>
        </div>
        <div class="setting-control max-tokens-control">
          <t-radio-group
            :model-value="maxCompletionTokensMode"
            @update:model-value="(val: unknown) => (val === 'default' || val === 'custom') && emit('update:maxCompletionTokensMode', val)"
          >
            <t-radio-button value="default">{{ $t('agent.editor.maxCompletionTokensDefault') }}</t-radio-button>
            <t-radio-button value="custom">{{ $t('agent.editor.maxCompletionTokensCustom') }}</t-radio-button>
          </t-radio-group>
          <span v-if="maxCompletionTokensMode === 'default'" class="max-tokens-value">
            {{ effectiveDefaultMaxCompletionTokens }}
          </span>
          <t-input-number
            v-else
            :model-value="formData.config.max_completion_tokens"
            :min="100"
            :max="100000"
            :step="100"
            theme="column"
            @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { max_completion_tokens: val })"
          />
        </div>
      </div>

      <!-- Reasoning Effort -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.thinking') }}</label>
          <p class="desc">{{ $t('agentEditor.desc.thinking') }}</p>
          <p v-if="hasSelectedChatModel && !selectedChatModelCanThink" class="desc">
            {{ $t('agent.editor.reasoningEffortUnsupported') }}
          </p>
          <p v-else-if="selectedChatModelAlwaysThinks" class="desc">
            {{ $t('agent.editor.reasoningEffortAlwaysOn') }}
          </p>
        </div>
        <div class="setting-control">
          <t-select
            :model-value="reasoningEffortLevel"
            class="reasoning-effort-select"
            :popup-props="{ overlayClassName: 'reasoning-level-select-popup' }"
            @update:model-value="(val: unknown) => typeof val === 'string' && emit('update:reasoningEffortLevel', val as ReasoningLevel)"
          >
            <t-option
              v-for="level in reasoningEffortOptions"
              :key="level"
              :value="level"
              :label="$t(levelLabelKey(level))"
              :show-overflow-tooltip="false"
            >
              <div class="reasoning-level-option">
                <span class="reasoning-level-option__title">{{ $t(levelLabelKey(level)) }}</span>
                <span class="reasoning-level-option__hint">{{ $t(levelDescriptionKey(level)) }}</span>
              </div>
            </t-option>
          </t-select>
        </div>
      </div>

      <!-- Citation Enabled -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.citationEnabled') }}</label>
          <p class="desc">{{ $t('agent.editor.citationEnabledDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            :model-value="formData.config.citation_enabled"
            :aria-label="$t('agent.editor.citationEnabled')"
            @update:model-value="(val: unknown) => emit('updateConfig', { citation_enabled: Boolean(val) })"
          />
        </div>
      </div>

      <!-- Rerank Model -->
      <div
        v-if="showRerankModelField"
        class="setting-row"
        data-agent-field="rerank_model"
        :class="{ 'setting-row--field-highlight': highlightedField === 'rerank_model' }"
      >
        <div class="setting-info">
          <label>
            {{ $t('agent.editor.rerankModel') }}
            <span v-if="needsRerankModel" class="required">*</span>
          </label>
          <p class="desc">
            {{ $t('agent.editor.rerankModelDesc') }}
            <template v-if="!isAgentMode && !needsRerankModel">
              <br />
              <span class="hint">{{ $t('agent.editor.rerankModelOptionalHint') }}</span>
            </template>
          </p>
        </div>
        <div class="setting-control">
          <slot name="rerank-selector" />
        </div>
      </div>

      <!-- Query Understand Model -->
      <div
        v-if="!isAgentMode && formData.config.multi_turn_enabled && formData.config.enable_rewrite"
        class="setting-row"
      >
        <div class="setting-info">
          <label>{{ $t('agent.editor.queryUnderstandModel') }}</label>
          <p class="desc">{{ $t('agentEditor.desc.queryUnderstandModel') }}</p>
        </div>
        <div class="setting-control">
          <slot name="rewrite-model-selector" />
        </div>
      </div>

      <!-- Max Iterations -->
      <div v-if="isAgentMode" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.maxIterations') }}</label>
          <p class="desc">{{ $t('agentEditor.desc.maxIterations') }}</p>
        </div>
        <div class="setting-control max-tokens-control">
          <t-radio-group
            :model-value="maxIterationsMode"
            @update:model-value="(val: unknown) => (val === 'limit' || val === 'unlimited') && emit('update:maxIterationsMode', val)"
          >
            <t-radio-button value="limit">{{ $t('agent.editor.maxIterationsLimit') }}</t-radio-button>
            <t-radio-button value="unlimited">{{ $t('agent.editor.maxIterationsUnlimited') }}</t-radio-button>
          </t-radio-group>
          <t-input-number
            v-if="maxIterationsMode === 'limit'"
            :model-value="formData.config.max_iterations"
            :min="2"
            :max="50"
            theme="column"
            @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { max_iterations: val })"
          />
        </div>
      </div>

      <!-- LLM Call Timeout -->
      <div v-if="isAgentMode" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agentEditor.llmCallTimeout.label') }}</label>
          <p class="desc">{{ $t('agentEditor.llmCallTimeout.desc') }}</p>
          <p class="desc-hint">{{ $t('agentEditor.llmCallTimeout.hint') }}</p>
        </div>
        <div class="setting-control">
          <t-input-number
            :model-value="formData.config.llm_call_timeout"
            :min="0"
            :max="3600"
            theme="column"
            :placeholder="$t('agentEditor.llmCallTimeout.placeholder')"
            clearable
            @update:model-value="(val: unknown) => emit('updateConfig', { llm_call_timeout: typeof val === 'number' ? val : 0 })"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { AgentNotReadyReasonKey } from '@/utils/agent-readiness'
import {
  levelDescriptionKey,
  levelLabelKey,
  type ReasoningLevel,
} from '@/utils/reasoningEffort'
import type { AgentEditorFormConfig, AgentEditorFormData } from '@/views/agent/composables/useAgentEditorForm'

defineProps<{
  currentSection: string
  isAgentMode: boolean
  highlightedField: AgentNotReadyReasonKey | null
  formData: AgentEditorFormData
  maxCompletionTokensMode: 'default' | 'custom'
  effectiveDefaultMaxCompletionTokens: number
  hasSelectedChatModel: boolean
  selectedChatModelCanThink: boolean
  selectedChatModelAlwaysThinks: boolean
  reasoningEffortOptions: ReasoningLevel[]
  reasoningEffortLevel: ReasoningLevel
  showRerankModelField: boolean
  needsRerankModel: boolean
  maxIterationsMode: 'limit' | 'unlimited'
}>()

const emit = defineEmits<{
  (e: 'updateConfig', patch: Partial<AgentEditorFormConfig>): void
  (e: 'update:maxCompletionTokensMode', mode: 'default' | 'custom'): void
  (e: 'update:reasoningEffortLevel', level: ReasoningLevel): void
  (e: 'update:maxIterationsMode', mode: 'limit' | 'unlimited'): void
}>()
</script>
