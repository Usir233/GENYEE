<template>
  <div v-show="currentSection === 'models'" class="section">
    <KBModelConfig
      v-if="formData"
      :config="formData.modelConfig"
      :has-files="hasFiles"
      :wiki-enabled="formData.indexingStrategy?.wikiEnabled"
      :rag-enabled="formData.indexingStrategy?.vectorEnabled || formData.indexingStrategy?.keywordEnabled"
      :all-models="allModels"
      @update:config="emit('update:model-config', $event)"
    />
  </div>

  <div v-show="currentSection === 'vectorStore'" class="section">
    <KBVectorStoreSettings
      v-if="formData"
      :mode="editorMode"
      :vector-store-id="formData.vectorStoreId"
      :bound-source="(formData.vectorStoreInfo?.source as VectorStoreSource | undefined)"
      :bound-name="formData.vectorStoreInfo?.name"
      :bound-engine-type="formData.vectorStoreInfo?.engineType"
      :bound-status="(formData.vectorStoreInfo?.status as VectorStoreStatus | undefined)"
      @update:vector-store-id="emit('update:vector-store-id', $event)"
    />
  </div>

  <div v-if="isFAQ && formData" v-show="currentSection === 'faq'" class="section">
    <div class="section-content">
      <div class="section-header">
        <h3 class="section-title">{{ $t('knowledgeEditor.faq.title') }}</h3>
        <p class="section-desc">{{ $t('knowledgeEditor.faq.description') }}</p>
      </div>
      <div class="section-body">
        <div class="form-item">
          <label class="form-label required">{{ $t('knowledgeEditor.faq.indexModeLabel') }}</label>
          <t-radio-group
            :value="formData.faqConfig.indexMode"
            @change="(val: string | number | boolean) => emit('update:faq-config', { ...formData!.faqConfig, indexMode: String(val) })"
          >
            <t-radio-button value="question_only">{{ $t('knowledgeEditor.faq.modes.questionOnly') }}</t-radio-button>
            <t-radio-button value="question_answer">{{ $t('knowledgeEditor.faq.modes.questionAnswer') }}</t-radio-button>
          </t-radio-group>
          <p class="form-tip">{{ $t('knowledgeEditor.faq.indexModeDescription') }}</p>
        </div>
        <div class="form-item">
          <label class="form-label required">{{ $t('knowledgeEditor.faq.questionIndexModeLabel') }}</label>
          <t-radio-group
            :value="formData.faqConfig.questionIndexMode"
            @change="(val: string | number | boolean) => emit('update:faq-config', { ...formData!.faqConfig, questionIndexMode: String(val) })"
          >
            <t-radio-button value="combined">{{ $t('knowledgeEditor.faq.modes.combined') }}</t-radio-button>
            <t-radio-button value="separate">{{ $t('knowledgeEditor.faq.modes.separate') }}</t-radio-button>
          </t-radio-group>
          <p class="form-tip">{{ $t('knowledgeEditor.faq.questionIndexModeDescription') }}</p>
        </div>
        <div class="faq-guide">
          <p>{{ $t('knowledgeEditor.faq.entryGuide') }}</p>
        </div>
      </div>
    </div>
  </div>

  <div v-if="!isFAQ && formData && currentSection === 'parser'" class="section">
    <KBParserSettings
      :parser-engine-rules="formData.chunkingConfig.parserEngineRules"
      @update:parser-engine-rules="emit('update:parser-engine-rules', $event)"
    />
  </div>

  <div v-if="!isFAQ && formData && currentSection === 'storage'" class="section">
    <KBStorageSettings
      :storage-backend-id="formData.storageBackendId"
      :storage-provider="formData.storageProvider"
      :has-files="editorMode === 'edit' && hasFiles"
      @update:storage-backend-id="emit('update:storage-backend-id', $event)"
      @update:storage-provider="emit('update:storage-provider', $event)"
    />
  </div>

  <div v-if="!isFAQ" v-show="currentSection === 'chunking'" class="section">
    <KBChunkingSettings
      v-if="formData"
      :config="formData.chunkingConfig"
      @update:config="emit('update:chunking-config', $event)"
    />
  </div>

  <div v-if="!isFAQ && currentSection === 'graph'" class="section">
    <GraphSettings
      v-if="formData"
      :graph-extract="formData.nodeExtractConfig"
      :model-id="formData.modelConfig.llmModelId"
      :all-models="allModels"
      @update:graphExtract="emit('update:node-extract', $event)"
    />
  </div>

  <div v-if="!isFAQ" v-show="currentSection === 'advanced'" class="section">
    <KBAdvancedSettings
      v-if="formData"
      :question-generation="formData.questionGenerationConfig"
      :auto-tag="formData.autoTagConfig"
      :profile-config="formData.profileConfig"
      :rag-enabled="formData.indexingStrategy?.vectorEnabled || formData.indexingStrategy?.keywordEnabled"
      :all-models="allModels"
      :table-metadata-instructions="formData.chunkingConfig.tableMetadataInstructions"
      @update:question-generation="emit('update:question-generation', $event)"
      @update:auto-tag="emit('update:auto-tag', $event)"
      @update:profile-config="emit('update:profile-config', $event)"
      @update:table-metadata-instructions="emit('update:table-metadata-instructions', $event)"
    />
  </div>

  <div v-if="editorMode === 'edit' && activeKbId && currentSection === 'datasource'" class="section">
    <DataSourceSettings :kb-id="activeKbId" @count="emit('update:ds-count', $event)" />
  </div>

  <div v-if="editorMode === 'edit' && activeKbId && isKbInActiveTenant && currentSection === 'share'" class="section">
    <KBShareSettings :kb-id="activeKbId" :can-share="canShareKB" />
  </div>

  <div v-if="editorMode === 'edit' && activeKbId && canViewActivity && currentSection === 'activity'" class="section">
    <KnowledgeBaseActivitySettings :kb-id="activeKbId" :active="currentSection === 'activity'" />
  </div>
</template>

<script setup lang="ts">
import type { VectorStoreSource, VectorStoreStatus } from '@/api/knowledge-base'
import type { ModelConfig } from '@/api/model'
import type { ParserEngineRule } from '@/types/knowledgeProcess'
import KBModelConfig from '../settings/KBModelConfig.vue'
import KBParserSettings from '../settings/KBParserSettings.vue'
import KBStorageSettings from '../settings/KBStorageSettings.vue'
import KBChunkingSettings from '../settings/KBChunkingSettings.vue'
import KBVectorStoreSettings from '../settings/KBVectorStoreSettings.vue'
import KBAdvancedSettings from '../settings/KBAdvancedSettings.vue'
import GraphSettings from '../settings/GraphSettings.vue'
import KBShareSettings from '../settings/KBShareSettings.vue'
import DataSourceSettings from '../settings/DataSourceSettings.vue'
import KnowledgeBaseActivitySettings from '../settings/KnowledgeBaseActivitySettings.vue'
import type {
  KBEditorAutoTagConfig,
  KBEditorChunkingConfig,
  KBEditorFaqConfig,
  KBEditorFormData,
  KBEditorModelConfig,
  KBEditorNodeExtractConfig,
  KBEditorProfileConfig,
  KBEditorQuestionGenerationConfig,
} from '../composables/useKBEditorForm'

defineProps<{
  currentSection: string
  formData: KBEditorFormData | null
  editorMode: 'create' | 'edit'
  activeKbId?: string
  hasFiles: boolean
  isFAQ: boolean
  allModels: ModelConfig[]
  isKbInActiveTenant: boolean
  canShareKB: boolean
  canViewActivity: boolean
}>()

const emit = defineEmits<{
  'update:model-config': [config: Partial<KBEditorModelConfig>]
  'update:vector-store-id': [id: string]
  'update:faq-config': [config: KBEditorFaqConfig]
  'update:parser-engine-rules': [rules: ParserEngineRule[]]
  'update:storage-backend-id': [id: string]
  'update:storage-provider': [provider: string]
  'update:chunking-config': [config: KBEditorChunkingConfig]
  'update:node-extract': [config: Partial<KBEditorNodeExtractConfig> & Pick<KBEditorNodeExtractConfig, 'enabled' | 'text' | 'tags' | 'nodes' | 'relations'>]
  'update:question-generation': [config: Partial<KBEditorQuestionGenerationConfig> & Pick<KBEditorQuestionGenerationConfig, 'enabled' | 'questionCount'>]
  'update:auto-tag': [config: KBEditorAutoTagConfig]
  'update:profile-config': [config: KBEditorProfileConfig]
  'update:table-metadata-instructions': [instructions: string]
  'update:ds-count': [count: number]
}>()
</script>

<style scoped lang="less">
@import '../knowledge-base-editor-modal.less';
</style>
