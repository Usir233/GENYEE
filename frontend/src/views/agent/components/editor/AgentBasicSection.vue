<template>
  <div v-show="currentSection === 'basic'" class="section">
    <div class="section-header">
      <div class="section-header-title">
        <h2>{{ $t('agent.editor.basicInfo') }}</h2>
        <t-tooltip v-if="isBuiltinAgent" :content="$t('agentEditor.builtinHint')" placement="top">
          <span
            class="builtin-agent-hint"
            tabindex="0"
            role="img"
            :aria-label="$t('agentEditor.builtinHint')"
          >
            <t-icon name="info-circle" />
          </span>
        </t-tooltip>
      </div>
      <p class="section-description">{{ $t('agent.editor.basicInfoDesc') }}</p>
    </div>

    <div class="settings-group">
      <div v-if="editorMode === 'edit' && editorAgentId" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.agentId') }}</label>
          <p class="desc">{{ $t('agent.editor.agentIdDesc') }}</p>
        </div>
        <div class="setting-control">
          <div class="agent-id-field">
            <code class="agent-id-value" :title="editorAgentId">{{ editorAgentId }}</code>
            <t-tooltip :content="$t('common.copy')" placement="top">
              <t-button
                theme="default"
                size="small"
                variant="text"
                class="agent-id-copy"
                :aria-label="$t('common.copy')"
                @click="emit('copyAgentId')"
              >
                <t-icon name="file-copy" />
              </t-button>
            </t-tooltip>
          </div>
        </div>
      </div>

      <div v-if="editorMode === 'edit' && editorAgentId" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('integrations.agentEditor.label') }}</label>
          <p class="desc">
            {{
              isPostCreateSession
                ? $t('agent.editor.postCreateHint.integrationDesc')
                : $t('integrations.agentEditor.desc')
            }}
          </p>
        </div>
        <div class="setting-control">
          <div class="integration-inline">
            <button
              type="button"
              class="integration-inline__stat integration-inline__link"
              @click="emit('gotoIntegrations', 'im')"
            >
              <span>{{ $t('integrations.tabs.im') }} · {{ agentIMChannelCount }}</span>
              <t-icon name="chevron-right" size="14px" />
            </button>
            <span class="integration-inline__sep" aria-hidden="true">|</span>
            <button
              type="button"
              class="integration-inline__stat integration-inline__link"
              @click="emit('gotoIntegrations', 'embed')"
            >
              <span>{{ $t('integrations.tabs.embed') }} · {{ agentEmbedChannelCount }}</span>
              <t-icon name="chevron-right" size="14px" />
            </button>
          </div>
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.mode') }} <span class="required">*</span></label>
          <p class="desc">
            {{
              agentMode === 'smart-reasoning'
                ? $t('agent.editor.agentDesc')
                : $t('agent.editor.normalDesc')
            }}
          </p>
        </div>
        <div class="setting-control">
          <t-radio-group
            :model-value="agentMode"
            :disabled="isBuiltinAgent"
            data-guide="agent-create-mode"
            @update:model-value="onModeChange"
          >
            <t-radio-button value="quick-answer">
              {{ $t('agent.type.normal') }}
            </t-radio-button>
            <t-radio-button value="smart-reasoning">
              {{ $t('agent.type.agent') }}
            </t-radio-button>
          </t-radio-group>
        </div>
      </div>

      <div
        v-if="isAgentMode && agentTypeSelectOptions.length > 0"
        class="setting-row setting-row--emphasize"
        data-guide="agent-create-agent-type"
      >
        <div class="setting-info">
          <label>{{ $t('agentEditor.agentType.label') }}</label>
          <p class="desc">{{ $t('agentEditor.agentType.desc') }}</p>
          <p v-if="activePresetDescription" class="desc agent-type-preset-desc">
            {{ activePresetDescription }}
          </p>
        </div>
        <div class="setting-control">
          <t-select
            :value="agentType"
            :disabled="isBuiltinAgent"
            :placeholder="$t('agentEditor.agentType.label')"
            :options="agentTypeSelectOptions"
            :popup-props="{ overlayClassName: 'agent-type-popup' }"
            class="agent-type-select"
            @change="onPresetSelectChange"
          >
            <template #option="{ option }">
              <div class="agent-type-option">
                <span class="agent-type-option-label">{{ option.label }}</span>
                <span v-if="option.desc" class="agent-type-option-desc">{{ option.desc }}</span>
              </div>
            </template>
          </t-select>
        </div>
      </div>

      <div class="setting-row" data-guide="agent-create-name">
        <div class="setting-info">
          <label>{{ $t('agent.editor.name') }} <span v-if="!isBuiltinAgent" class="required">*</span></label>
          <p class="desc">{{ $t('agentEditor.desc.name') }}</p>
        </div>
        <div class="setting-control">
          <div class="name-input-wrapper">
            <div v-if="isBuiltinAgent" class="builtin-avatar" :class="isAgentMode ? 'agent' : 'normal'">
              <t-icon :name="isAgentMode ? 'control-platform' : 'chat'" size="24px" />
            </div>
            <AgentAvatar v-else :name="formData.name || '?'" size="medium" />
            <t-input
              :model-value="formData.name"
              :placeholder="$t('agent.editor.namePlaceholder')"
              class="name-input"
              :disabled="isBuiltinAgent"
              @update:model-value="(val: unknown) => emit('updateForm', { name: String(val ?? '') })"
            />
          </div>
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.description') }}</label>
          <p class="desc">{{ $t('agentEditor.desc.description') }}</p>
        </div>
        <div class="setting-control">
          <t-textarea
            :model-value="formData.description"
            :placeholder="$t('agent.editor.descriptionPlaceholder')"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :disabled="isBuiltinAgent"
            @update:model-value="(val: unknown) => emit('updateForm', { description: String(val ?? '') })"
          />
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('agent.editor.memoryEnabled') }}</label>
          <p class="desc">{{ $t('agentEditor.desc.memoryEnabled') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            :model-value="formData.config.memory_enabled"
            :aria-label="$t('agent.editor.memoryEnabled')"
            @update:model-value="(val: unknown) => emit('updateConfig', { memory_enabled: Boolean(val) })"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import AgentAvatar from '@/components/AgentAvatar.vue'
import type { AgentType } from '@/api/agent'
import type { AgentEditorFormConfig, AgentEditorFormData } from '@/views/agent/composables/useAgentEditorForm'

defineProps<{
  currentSection: string
  editorMode: 'create' | 'edit'
  editorAgentId?: string
  isPostCreateSession: boolean
  isBuiltinAgent: boolean
  agentMode: 'quick-answer' | 'smart-reasoning'
  isAgentMode: boolean
  agentType: AgentType
  agentTypeSelectOptions: Array<{ value: AgentType; label: string; desc: string }>
  activePresetDescription: string
  agentIMChannelCount: number
  agentEmbedChannelCount: number
  formData: AgentEditorFormData
}>()

const emit = defineEmits<{
  (e: 'copyAgentId'): void
  (e: 'gotoIntegrations', tab: 'im' | 'embed'): void
  (e: 'update:agentMode', mode: 'quick-answer' | 'smart-reasoning'): void
  (e: 'changeAgentType', val: AgentType): void
  (e: 'updateForm', patch: Partial<Pick<AgentEditorFormData, 'name' | 'description'>>): void
  (e: 'updateConfig', patch: Partial<AgentEditorFormConfig>): void
}>()

function onModeChange(val: unknown) {
  if (val === 'quick-answer' || val === 'smart-reasoning') {
    emit('update:agentMode', val)
  }
}

function onPresetSelectChange(val: unknown) {
  if (typeof val === 'string') {
    emit('changeAgentType', val as AgentType)
  }
}
</script>
