<template>
  <div>
    <!-- Multimodal / Attachments -->
    <div v-show="currentSection === 'multimodal'" class="section">
      <div class="section-header">
        <h2>{{ $t('agentEditor.imageUpload.sectionTitle') }}</h2>
        <p class="section-description">{{ $t('agentEditor.imageUpload.sectionDesc') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row" data-guide="agent-create-multimodal">
          <div class="setting-info">
            <label>{{ $t('agentEditor.imageUpload.label') }}</label>
            <p class="desc">{{ $t('agentEditor.imageUpload.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.image_upload_enabled"
              :aria-label="$t('agentEditor.imageUpload.label')"
              @update:model-value="(val: unknown) => emit('updateConfig', { image_upload_enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="formData.config.image_upload_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.imageUpload.vlmModel') }} <span class="required">*</span></label>
            <p class="desc">{{ $t('agentEditor.imageUpload.vlmModelDesc') }}</p>
          </div>
          <div class="setting-control">
            <slot name="vlm-selector" />
          </div>
        </div>

        <div v-if="formData.config.image_upload_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.imageUpload.imageUnderstandingLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.imageUpload.imageUnderstandingDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.attachment_image_understanding"
              :aria-label="$t('agentEditor.imageUpload.imageUnderstandingLabel')"
              @update:model-value="(val: unknown) => emit('updateConfig', { attachment_image_understanding: Boolean(val) })"
            />
          </div>
        </div>

        <div
          v-if="formData.config.image_upload_enabled && formData.config.attachment_image_understanding"
          class="setting-row"
        >
          <div class="setting-info">
            <label>{{ $t('agentEditor.imageUpload.ocrMaxPagesLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.imageUpload.ocrMaxPagesDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              :model-value="formData.config.attachment_ocr_max_pages"
              :min="0"
              :max="64"
              :step="1"
              theme="normal"
              style="width: 160px;"
              :placeholder="$t('agentEditor.imageUpload.useGlobalDefault')"
              @update:model-value="(val: unknown) => emit('updateConfig', { attachment_ocr_max_pages: typeof val === 'number' ? val : 0 })"
            />
          </div>
        </div>

        <div v-if="formData.config.image_upload_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.imageUpload.storageProvider') }}</label>
            <p class="desc">{{ $t('agentEditor.imageUpload.storageProviderDesc') }}</p>
          </div>
          <div class="setting-control" style="flex-direction: column; align-items: flex-end;">
            <t-select
              :model-value="formData.config.image_storage_provider"
              style="width: 280px;"
              :placeholder="$t('agentEditor.imageUpload.storageProviderPlaceholder')"
              clearable
              @update:model-value="(val: unknown) => emit('updateConfig', { image_storage_provider: String(val ?? '') })"
            >
              <t-option value="" :label="$t('agentEditor.imageUpload.storageDefault')" />
              <t-option
                v-for="opt in imageStorageOptions"
                :key="opt.value"
                :value="opt.value"
                :label="opt.label"
                :disabled="opt.disabled"
              >
                <span class="select-option-with-tag">
                  <span>{{ opt.label }}</span>
                  <t-tag v-if="opt.disabled" theme="warning" variant="light" size="small">
                    {{ $t('agentEditor.imageUpload.notConfigured') }}
                  </t-tag>
                </span>
              </t-option>
            </t-select>
            <button
              type="button"
              class="go-settings-link"
              @click="emit('openStorageSettings')"
            >
              {{ $t('agentEditor.imageUpload.goStorageSettings') }}
            </button>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.audioUpload.label') }}</label>
            <p class="desc">{{ $t('agentEditor.audioUpload.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="Boolean(formData.config.audio_upload_enabled)"
              :aria-label="$t('agentEditor.audioUpload.label')"
              @update:model-value="(val: unknown) => emit('updateConfig', { audio_upload_enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="formData.config.audio_upload_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.audioUpload.asrModel') }}</label>
            <p class="desc">{{ $t('agentEditor.audioUpload.asrModelDesc') }}</p>
          </div>
          <div class="setting-control">
            <slot name="asr-selector" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.chatParser.waitTimeoutLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.chatParser.waitTimeoutDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              :model-value="formData.config.attachment_parse_wait_timeout_sec"
              :min="0"
              :max="600"
              :step="10"
              theme="normal"
              style="width: 160px;"
              :placeholder="$t('agentEditor.imageUpload.useGlobalDefault')"
              @update:model-value="(val: unknown) => emit('updateConfig', { attachment_parse_wait_timeout_sec: typeof val === 'number' ? val : 0 })"
            />
          </div>
        </div>

        <div class="parser-policy-block">
          <div class="parser-policy-block__header">
            <label>{{ $t('agentEditor.chatParser.label') }}</label>
            <p class="desc">{{ $t('agentEditor.chatParser.desc') }}</p>
          </div>
          <KBParserSettings
            embedded
            :parser-engine-rules="formData.config.chat_parser_engine_rules"
            :relevant-extensions="CHAT_PARSER_EXTENSIONS"
            @update:parser-engine-rules="(val: ParserEngineRule[]) => emit('updateConfig', { chat_parser_engine_rules: val })"
          />
        </div>
      </div>
    </div>

    <!-- Conversation Settings -->
    <div v-show="currentSection === 'conversation'" class="section">
      <div class="section-header">
        <h2>{{ $t('agent.editor.conversationSettings') }}</h2>
        <p class="section-description">{{ conversationSectionDesc }}</p>
      </div>

      <div class="settings-group">
        <div v-if="!isAgentMode" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.multiTurn') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.multiTurn') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.multi_turn_enabled"
              :aria-label="$t('agent.editor.multiTurn')"
              @update:model-value="(val: unknown) => emit('updateConfig', { multi_turn_enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="!isAgentMode && formData.config.multi_turn_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.historyTurns') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.historyRounds') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number :model-value="formData.config.history_turns" :min="1" :max="100" theme="column" @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { history_turns: val })" />
          </div>
        </div>

        <div v-if="isAgentMode && hasKnowledgeBase" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.retainRetrievalHistory') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.retainRetrievalHistory') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.retain_retrieval_history"
              :aria-label="$t('agent.editor.retainRetrievalHistory')"
              @update:model-value="(val: unknown) => emit('updateConfig', { retain_retrieval_history: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="formData.config.multi_turn_enabled && !isAgentMode" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.enableRewrite') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.rewrite') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.enable_rewrite"
              :aria-label="$t('agent.editor.enableRewrite')"
              @update:model-value="(val: unknown) => emit('updateConfig', { enable_rewrite: Boolean(val) })"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- Question Suggestions -->
    <div v-show="currentSection === 'suggestions'" class="section">
      <div class="section-header">
        <h2>{{ $t('agentEditor.questionSuggestions.title') }}</h2>
        <p class="section-description">{{ $t('agentEditor.questionSuggestions.description') }}</p>
      </div>

      <t-tabs v-model="suggestionTab" class="suggestion-tabs">
        <t-tab-panel value="starters" :label="$t('agentEditor.questionSuggestions.startersTitle')" />
        <t-tab-panel value="followUps" :label="$t('agentEditor.questionSuggestions.followUpsTitle')" />
      </t-tabs>

      <div v-show="suggestionTab === 'starters'" class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.questionSuggestions.enableStarters') }}</label>
            <p class="desc">{{ $t('agentEditor.questionSuggestions.enableStartersDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.question_suggestions.starters.enabled"
              :aria-label="$t('agentEditor.questionSuggestions.enableStarters')"
              @update:model-value="(val: unknown) => emit('updateStarters', { enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="formData.config.question_suggestions.starters.enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.questionSuggestions.sourceMode') }}</label>
          </div>
          <div class="setting-control">
            <t-select
              :model-value="formData.config.question_suggestions.starters.mode"
              :options="starterSuggestionModeOptions"
              @update:model-value="onStarterModeChange"
            />
          </div>
        </div>

        <div v-if="formData.config.question_suggestions.starters.enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.questionSuggestions.count') }}</label>
          </div>
          <div class="setting-control">
            <t-input-number
              :model-value="formData.config.question_suggestions.starters.count"
              :min="1"
              :max="8"
              theme="column"
              @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateStarters', { count: val })"
            />
          </div>
        </div>

        <div
          v-if="formData.config.question_suggestions.starters.enabled && ['curated', 'hybrid'].includes(formData.config.question_suggestions.starters.mode)"
          class="setting-row setting-row-vertical"
        >
          <div class="setting-info">
            <div class="setting-info-header setting-info-header--inline">
              <label>{{ $t('agentEditor.questionSuggestions.curatedItems') }}</label>
              <span class="curated-items-count">
                {{ formData.config.question_suggestions.starters.items.length }}/8
              </span>
            </div>
            <p class="desc">{{ $t('agentEditor.questionSuggestions.curatedItemsDesc') }}</p>
          </div>
          <div class="setting-control setting-control-full">
            <div class="suggested-prompts-list">
              <div
                v-for="(itemText, index) in formData.config.question_suggestions.starters.items"
                :key="index"
                class="prompt-item"
              >
                <t-input
                  :model-value="itemText"
                  :maxlength="200"
                  @update:model-value="(val: unknown) => emit('updateStarterItem', Number(index), String(val ?? ''))"
                />
                <t-button
                  variant="text"
                  theme="danger"
                  shape="square"
                  :aria-label="$t('common.delete')"
                  @click="emit('removeStarterSuggestion', Number(index))"
                >
                  <t-icon name="delete" />
                </t-button>
              </div>
              <t-button
                variant="dashed"
                :disabled="formData.config.question_suggestions.starters.items.length >= 8"
                @click="emit('addStarterSuggestion')"
              >
                <template #icon><t-icon name="add" /></template>
                {{ $t('agentEditor.questionSuggestions.addItem') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>

      <div v-show="suggestionTab === 'followUps'" class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.questionSuggestions.enableFollowUps') }}</label>
            <p class="desc">{{ $t('agentEditor.questionSuggestions.enableFollowUpsDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.question_suggestions.follow_ups.enabled"
              :aria-label="$t('agentEditor.questionSuggestions.enableFollowUps')"
              @update:model-value="(val: unknown) => emit('updateFollowUps', { enabled: Boolean(val) })"
            />
          </div>
        </div>

        <template v-if="formData.config.question_suggestions.follow_ups.enabled">
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.sourceMode') }}</label>
            </div>
            <div class="setting-control">
              <t-select
                :model-value="formData.config.question_suggestions.follow_ups.mode"
                :options="followUpSuggestionModeOptions"
                @update:model-value="onFollowUpModeChange"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.count') }}</label>
            </div>
            <div class="setting-control">
              <t-input-number
                :model-value="formData.config.question_suggestions.follow_ups.count"
                :min="1"
                :max="5"
                theme="column"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateFollowUps', { count: val })"
              />
            </div>
          </div>

          <div v-if="formData.config.question_suggestions.follow_ups.mode !== 'knowledge'" class="setting-row">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.model') }}</label>
              <p class="desc">{{ $t('agentEditor.questionSuggestions.modelDesc') }}</p>
            </div>
            <div class="setting-control">
              <slot name="followup-model-selector" />
            </div>
          </div>

          <div class="suggestion-advanced-divider">
            <span>{{ $t('agentEditor.questionSuggestions.advancedSettings') }}</span>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.contextTurns') }}</label>
            </div>
            <div class="setting-control">
              <t-input-number
                :model-value="formData.config.question_suggestions.follow_ups.max_context_turns"
                :min="1"
                :max="5"
                theme="column"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateFollowUps', { max_context_turns: val })"
              />
            </div>
          </div>

          <div class="setting-row setting-row-vertical">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.categories') }}</label>
            </div>
            <div class="setting-control setting-control-full">
              <t-checkbox-group
                :model-value="formData.config.question_suggestions.follow_ups.categories"
                :options="followUpCategoryOptions"
                @update:model-value="onCategoriesChange"
              />
            </div>
          </div>

          <div class="setting-row setting-row-vertical">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.instruction') }}</label>
            </div>
            <div class="setting-control setting-control-full">
              <t-textarea
                :model-value="formData.config.question_suggestions.follow_ups.additional_instruction || ''"
                :placeholder="$t('agentEditor.questionSuggestions.instructionPlaceholder')"
                :maxlength="2000"
                :autosize="{ minRows: 3, maxRows: 8 }"
                @update:model-value="(val: unknown) => emit('updateFollowUps', { additional_instruction: String(val ?? '') })"
              />
            </div>
          </div>

          <div class="setting-row setting-row-vertical">
            <div class="setting-info">
              <label>{{ $t('agentEditor.questionSuggestions.displayRules') }}</label>
            </div>
            <div class="setting-control setting-control-full">
              <div class="suggestion-checkboxes">
                <t-checkbox
                  :checked="formData.config.question_suggestions.follow_ups.suppress_on_fallback"
                  @change="(val: boolean) => emit('updateFollowUps', { suppress_on_fallback: Boolean(val) })"
                >
                  {{ $t('agentEditor.questionSuggestions.suppressFallback') }}
                </t-checkbox>
                <t-checkbox
                  :checked="formData.config.question_suggestions.follow_ups.suppress_when_answer_asks_question"
                  @change="(val: boolean) => emit('updateFollowUps', { suppress_when_answer_asks_question: Boolean(val) })"
                >
                  {{ $t('agentEditor.questionSuggestions.suppressQuestion') }}
                </t-checkbox>
                <t-checkbox
                  :checked="formData.config.question_suggestions.follow_ups.knowledge_fallback"
                  @change="(val: boolean) => emit('updateFollowUps', { knowledge_fallback: Boolean(val) })"
                >
                  {{ $t('agentEditor.questionSuggestions.knowledgeFallback') }}
                </t-checkbox>
                <t-checkbox
                  :checked="formData.config.question_suggestions.follow_ups.allow_regenerate"
                  @change="(val: boolean) => emit('updateFollowUps', { allow_regenerate: Boolean(val) })"
                >
                  {{ $t('agentEditor.questionSuggestions.allowRegenerate') }}
                </t-checkbox>
              </div>
            </div>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, toRefs } from 'vue'
import { useI18n } from 'vue-i18n'
import KBParserSettings, { type ParserEngineRule } from '@/views/knowledge/settings/KBParserSettings.vue'
import type { QuestionSuggestionConfig } from '@/api/agent'
import type { StorageEngineStatusItem } from '@/api/system'
import type { AgentEditorFormConfig, AgentEditorFormData } from '@/views/agent/composables/useAgentEditorForm'

const CHAT_PARSER_EXTENSIONS = [
  'pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'epub', 'mhtml',
  'txt', 'md', 'markdown', 'csv', 'json', 'xml', 'html', 'yaml', 'yml', 'log',
  'jpg', 'jpeg', 'png', 'gif', 'bmp', 'tiff', 'webp',
]

const props = defineProps<{
  currentSection: string
  isAgentMode: boolean
  hasKnowledgeBase: boolean
  formData: AgentEditorFormData
  storageEngineStatus: StorageEngineStatusItem[]
}>()

const emit = defineEmits<{
  (e: 'updateConfig', patch: Partial<AgentEditorFormConfig>): void
  (e: 'updateStarters', patch: Partial<QuestionSuggestionConfig['starters']>): void
  (e: 'updateStarterItem', index: number, value: string): void
  (e: 'addStarterSuggestion'): void
  (e: 'removeStarterSuggestion', index: number): void
  (e: 'updateFollowUps', patch: Partial<QuestionSuggestionConfig['follow_ups']>): void
  (e: 'openStorageSettings'): void
}>()

const { t } = useI18n()
const { isAgentMode, storageEngineStatus } = toRefs(props)
const suggestionTab = ref<'starters' | 'followUps'>('starters')

const conversationSectionDesc = computed(() =>
  isAgentMode.value
    ? t('agentEditor.desc.conversationSectionAgent')
    : t('agentEditor.desc.conversationSection'),
)

const imageStorageOptions = computed(() => {
  const statusMap: Record<string, boolean> = {}
  for (const e of storageEngineStatus.value) {
    statusMap[e.name] = e.available
  }
  return [
    { value: 'local', label: t('settings.storage.engineLocal'), disabled: false },
    { value: 'minio', label: 'MinIO', disabled: statusMap.minio === false },
    { value: 'cos', label: t('settings.storage.engineCos'), disabled: statusMap.cos === false },
    { value: 'tos', label: t('settings.storage.engineTos'), disabled: statusMap.tos === false },
    { value: 's3', label: 'Amazon S3', disabled: statusMap.s3 === false },
    { value: 'oss', label: t('settings.storage.engineOss'), disabled: statusMap.oss === false },
  ]
})

const starterSuggestionModeOptions = computed(() => [
  { value: 'curated', label: t('agentEditor.questionSuggestions.modeCurated') },
  { value: 'knowledge', label: t('agentEditor.questionSuggestions.modeKnowledge') },
  { value: 'hybrid', label: t('agentEditor.questionSuggestions.modeHybrid') },
])

const followUpSuggestionModeOptions = computed(() => [
  { value: 'generated', label: t('agentEditor.questionSuggestions.modeGenerated') },
  { value: 'knowledge', label: t('agentEditor.questionSuggestions.modeKnowledge') },
  { value: 'hybrid', label: t('agentEditor.questionSuggestions.modeHybrid') },
])

const followUpCategoryOptions = computed(() => [
  { value: 'clarify', label: t('agentEditor.questionSuggestions.categoryClarify') },
  { value: 'deepen', label: t('agentEditor.questionSuggestions.categoryDeepen') },
  { value: 'action', label: t('agentEditor.questionSuggestions.categoryAction') },
])

function onStarterModeChange(val: unknown) {
  if (val === 'curated' || val === 'knowledge' || val === 'hybrid') {
    emit('updateStarters', { mode: val })
  }
}

function onFollowUpModeChange(val: unknown) {
  if (val === 'generated' || val === 'knowledge' || val === 'hybrid') {
    emit('updateFollowUps', { mode: val })
  }
}

function onCategoriesChange(val: unknown) {
  if (Array.isArray(val)) {
    const allowed = val.filter(
      (item): item is 'clarify' | 'deepen' | 'action' =>
        item === 'clarify' || item === 'deepen' || item === 'action',
    )
    emit('updateFollowUps', { categories: allowed })
  }
}
</script>
