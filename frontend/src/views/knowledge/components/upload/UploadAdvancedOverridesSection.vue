<template>
  <div v-show="activeSection === 'tags'" class="section">
    <div class="section-content">
      <div class="section-header">
        <h2 class="section-title">{{ t('uploadConfirm.tabTags') }}</h2>
        <p class="section-desc">{{ t('uploadConfirm.tagsDescription') }}</p>
      </div>
      <div class="settings-group">
        <div class="setting-row setting-row-vertical">
          <div class="setting-info">
            <label>{{ t('uploadConfirm.tagsPlaceholder') }}</label>
          </div>
          <div class="setting-control setting-control-full">
            <t-select
              v-model="selectedTagIds"
              :options="tagOptions"
              :loading="tagsLoading"
              multiple
              filterable
              clearable
              :placeholder="t('uploadConfirm.tagsPlaceholder')"
            />
            <p v-if="tagsLoadFailed" class="field-hint field-hint--error">
              {{ t('uploadConfirm.tagsLoadFailed') }}
            </p>
            <p v-else-if="!tagsLoading && tagOptions.length === 0" class="field-hint">
              {{ t('uploadConfirm.tagsEmpty') }}
            </p>
          </div>
        </div>
      </div>
    </div>
  </div>

  <div v-show="activeSection === 'parser'" class="section">
    <KBParserSettings
      :relevant-extensions="batchFileExts"
      :parser-engine-rules="uiState.chunkingConfig.parserEngineRules"
      @update:parser-engine-rules="emit('update:parser-engine-rules', $event)"
    />
    <div v-if="hasPdf" class="kb-settings-block">
      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ t('uploadConfirm.pdfForceScanned.label') }}</label>
            <p class="desc">{{ t('uploadConfirm.pdfForceScanned.description') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :value="uiState.pdfForceScanned"
              size="medium"
              @change="emit('update:pdf-force-scanned', Boolean($event))"
            />
          </div>
        </div>
      </div>
    </div>
  </div>

  <div v-show="activeSection === 'summary'" class="section" data-section="summary">
    <div class="kb-settings-block">
      <div class="section-header">
        <h2 class="section-title">{{ t('uploadConfirm.documentSummary') }}</h2>
        <p class="section-desc">{{ t('uploadConfirm.documentSummaryDescription') }}</p>
      </div>
      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ t('uploadConfirm.generateSummary') }}</label>
            <p class="desc">{{ t('uploadConfirm.generateSummaryHint') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :value="uiState.summaryEnabled"
              size="medium"
              @change="emit('update:summary-enabled', Boolean($event))"
            />
          </div>
        </div>
      </div>
    </div>
  </div>

  <div v-show="activeSection === 'question'" class="section">
    <div class="kb-settings-block">
      <div class="section-header">
        <h2 class="section-title">{{ t('knowledgeEditor.advanced.questionGeneration.label') }}</h2>
        <p class="section-desc">{{ t('knowledgeEditor.advanced.questionGeneration.description') }}</p>
      </div>
      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ t('knowledgeEditor.advanced.questionGeneration.label') }}</label>
            <p class="desc">{{ t('knowledgeEditor.advanced.questionGeneration.countDescription') }}</p>
          </div>
          <div class="setting-control setting-control-inline">
            <t-input-number
              v-if="uiState.questionGenerationConfig.enabled"
              :value="uiState.questionGenerationConfig.questionCount"
              :min="1"
              :max="10"
              :step="1"
              theme="normal"
              :style="{ width: '88px' }"
              @change="(val: number | string) => emit('update:question-generation', { ...uiState.questionGenerationConfig, questionCount: Number(val) || 1 })"
            />
            <t-switch
              :value="uiState.questionGenerationConfig.enabled"
              size="medium"
              @change="emit('update:question-generation', { ...uiState.questionGenerationConfig, enabled: Boolean($event) })"
            />
          </div>
        </div>
        <div v-if="uiState.questionGenerationConfig.enabled" class="setting-row setting-row-vertical">
          <div class="setting-info">
            <label>{{ t('knowledgeEditor.advanced.questionGeneration.instructionsLabel') }}</label>
            <p class="desc">{{ t('knowledgeEditor.advanced.questionGeneration.instructionsDescription') }}</p>
          </div>
          <div class="setting-control setting-control-full">
            <t-textarea
              :value="uiState.questionGenerationConfig.customInstructions"
              :placeholder="t('knowledgeEditor.advanced.questionGeneration.instructionsPlaceholder')"
              :maxlength="4000"
              :autosize="{ minRows: 3, maxRows: 8 }"
              @change="(val: string) => emit('update:question-generation', { ...uiState.questionGenerationConfig, customInstructions: String(val ?? '') })"
            />
          </div>
        </div>
      </div>
    </div>
  </div>

  <div v-if="isGraphSectionAvailable" v-show="activeSection === 'graph'" class="section">
    <GraphSettings
      :graph-extract="uiState.nodeExtractConfig"
      :model-id="llmModelId"
      :all-models="allModels"
      @update:graphExtract="emit('update:node-extract', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelConfig } from '@/api/model'
import type { ParserEngineRule } from '@/types/knowledgeProcess'
import KBParserSettings from '../../settings/KBParserSettings.vue'
import GraphSettings from '../../settings/GraphSettings.vue'
import type {
  ConfigSectionKey,
  UploadNodeExtractConfig,
  UploadUIState,
} from '../../composables/useUploadProcessOverrides'

const props = defineProps<{
  activeSection: ConfigSectionKey
  tagIds: string[]
  tagOptions: Array<{ label: string; value: string }>
  tagsLoading: boolean
  tagsLoadFailed: boolean
  batchFileExts: string[]
  hasPdf: boolean
  uiState: UploadUIState
  isGraphSectionAvailable: boolean
  llmModelId: string
  allModels: ModelConfig[]
}>()

const emit = defineEmits<{
  'update:tagIds': [value: string[]]
  'update:parser-engine-rules': [rules: ParserEngineRule[]]
  'update:pdf-force-scanned': [value: boolean]
  'update:summary-enabled': [value: boolean]
  'update:question-generation': [value: UploadUIState['questionGenerationConfig']]
  'update:node-extract': [config: UploadNodeExtractConfig]
}>()

const { t } = useI18n()

const selectedTagIds = computed({
  get: () => props.tagIds,
  set: (val: string[]) => emit('update:tagIds', val),
})
</script>

<style lang="less" scoped>
@import '../upload-confirm-dialog.less';
</style>
