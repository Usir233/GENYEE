<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="dialogVisible" class="upload-confirm-overlay">
        <div class="upload-confirm-modal" role="dialog" :aria-label="dialogTitle">
          <button class="close-btn" type="button" :aria-label="t('general.close')" @click="handleCancel">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </button>

          <div class="upload-confirm-container">
            <UploadSourceQueuePanel
              :dialog-title="dialogTitle"
              :mode="mode"
              :files="localFiles"
              :urls="localUrls"
              :manual-preview="manualPreview"
              :reparse-preview="reparsePreview"
              :accept-file-types="acceptFileTypes"
              :supported-file-types="supportedFileTypes"
              :target-folder="localTargetFolder"
              :folder-options="folderOptions"
              :reset-counter="dialogSessionSeq"
              @append-files="appendFiles"
              @append-url="appendUrl"
              @remove-file="removeFile"
              @remove-url="removeUrl"
              @update:target-folder="localTargetFolder = $event"
            />

            <aside class="settings-sidebar">
              <div class="settings-sidebar-header">
                <h2 class="settings-sidebar-title">{{ t('uploadConfirm.parseConfig') }}</h2>
              </div>
              <nav class="settings-nav" :aria-label="t('uploadConfirm.configNav')">
                <button
                  v-for="item in navItems"
                  :key="item.key"
                  type="button"
                  class="nav-item"
                  :class="{
                    active: activeSection === item.key,
                    'nav-item--issue': item.issue,
                  }"
                  @click="activeSection = item.key"
                >
                  <t-icon :name="item.icon" class="nav-icon" />
                  <span class="nav-label-wrap">
                    <span class="nav-label">{{ item.label }}</span>
                    <span
                      class="nav-status"
                      :class="{
                        'nav-status--warning': item.statusTone === 'warning',
                        'nav-status--error': item.statusTone === 'error',
                        'nav-status--muted': item.statusTone === 'muted',
                      }"
                      :title="item.statusFull"
                    >{{ item.status }}</span>
                  </span>
                  <span v-if="item.issue" class="nav-dot" aria-hidden="true" />
                </button>
              </nav>
            </aside>

            <div class="config-panel">
              <div class="content-wrapper upload-confirm-content">
                <UploadAdvancedOverridesSection
                  :active-section="activeSection"
                  :tag-ids="selectedTagIds"
                  :tag-options="tagOptions"
                  :tags-loading="tagsLoading"
                  :tags-load-failed="tagsLoadFailed"
                  :batch-file-exts="batchFileExts"
                  :has-pdf="hasPdf"
                  :ui-state="uiState"
                  :is-graph-section-available="isGraphSectionAvailable"
                  :llm-model-id="llmModelId"
                  :all-models="allModels"
                  @update:tag-ids="selectedTagIds = $event"
                  @update:parser-engine-rules="handleParserEngineRulesUpdate"
                  @update:pdf-force-scanned="uiState.pdfForceScanned = $event"
                  @update:summary-enabled="uiState.summaryEnabled = $event"
                  @update:question-generation="uiState.questionGenerationConfig = $event"
                  @update:node-extract="handleNodeExtractUpdate"
                />

                <div v-show="activeSection === 'chunking'" class="section">
                  <div class="section-content">
                    <div class="section-header">
                      <h2 class="section-title">{{ t('knowledgeEditor.chunking.title') }}</h2>
                      <p class="section-desc">{{ t('knowledgeEditor.chunking.description') }}</p>
                    </div>
                    <div class="settings-group">
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.strategyLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.chunking.strategyDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-select
                            v-model="uiState.chunkingConfig.strategy"
                            :options="chunkingStrategyOptions"
                            :clearable="false"
                            :style="{ width: '280px' }"
                          />
                        </div>
                      </div>
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.sizeLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.chunking.sizeDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-input-number
                            v-model="uiState.chunkingConfig.chunkSize"
                            :min="100"
                            :max="4000"
                            :step="50"
                            theme="normal"
                            :style="{ width: '200px' }"
                          />
                        </div>
                      </div>
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.overlapLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.chunking.overlapDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-input-number
                            v-model="uiState.chunkingConfig.chunkOverlap"
                            :min="0"
                            :max="500"
                            :step="20"
                            theme="normal"
                            :style="{ width: '200px' }"
                          />
                        </div>
                      </div>
                    </div>

                    <button
                      type="button"
                      class="more-options-toggle"
                      :aria-expanded="chunkingMoreOpen"
                      @click="chunkingMoreOpen = !chunkingMoreOpen"
                    >
                      <t-icon name="chevron-down" :class="{ 'is-open': chunkingMoreOpen }" />
                      <span>{{ t('uploadConfirm.moreOptions') }}</span>
                    </button>

                    <div v-if="chunkingMoreOpen" class="settings-group settings-group--more">
                      <div class="setting-row setting-row--separators">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.separatorsLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.chunking.separatorsDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-select
                            v-model="uiState.chunkingConfig.separators"
                            :options="separatorOptions"
                            multiple
                            creatable
                            filterable
                            :style="{ width: '280px' }"
                          />
                        </div>
                      </div>
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.tokenLimitLabel') }}</label>
                        </div>
                        <div class="setting-control">
                          <t-input-number
                            v-model="uiState.chunkingConfig.tokenLimit"
                            :min="0"
                            :max="8192"
                            :step="64"
                            theme="normal"
                            :style="{ width: '200px' }"
                          />
                        </div>
                      </div>
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.languagesLabel') }}</label>
                        </div>
                        <div class="setting-control">
                          <t-select
                            v-model="uiState.chunkingConfig.languages"
                            :options="languageOptions"
                            multiple
                            :style="{ width: '280px' }"
                          />
                        </div>
                      </div>
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.parentChildLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.chunking.parentChildDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-switch v-model="uiState.chunkingConfig.enableParentChild" />
                        </div>
                      </div>
                      <div v-if="uiState.chunkingConfig.enableParentChild" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.parentChunkSizeLabel') }}</label>
                        </div>
                        <div class="setting-control">
                          <t-input-number
                            v-model="uiState.chunkingConfig.parentChunkSize"
                            :min="512"
                            :max="8192"
                            :step="64"
                            theme="normal"
                            :style="{ width: '200px' }"
                          />
                        </div>
                      </div>
                      <div v-if="uiState.chunkingConfig.enableParentChild" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.chunking.childChunkSizeLabel') }}</label>
                        </div>
                        <div class="setting-control">
                          <t-input-number
                            v-model="uiState.chunkingConfig.childChunkSize"
                            :min="64"
                            :max="2048"
                            :step="32"
                            theme="normal"
                            :style="{ width: '200px' }"
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                <div v-show="activeSection === 'multimodal'" class="section" data-section="multimodal">
                  <div class="kb-settings-block">
                    <div class="section-header">
                      <h2 class="section-title">{{ t('knowledgeEditor.multimodal.title') }}</h2>
                      <p class="section-desc">{{ t('knowledgeEditor.multimodal.description') }}</p>
                    </div>
                    <div v-if="issueSectionKeys.has('multimodal')" class="section-notice">
                      <t-icon name="info-circle-filled" />
                      <span>{{ t('uploadConfirm.multimodalSetupHint') }}</span>
                    </div>
                    <div class="settings-group">
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.advanced.multimodal.label') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.description') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-switch v-model="uiState.multimodalConfig.enabled" size="medium" />
                        </div>
                      </div>
                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.advanced.multimodal.vllmLabel') }} <span class="required">*</span></label>
                          <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.vllmDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <ModelSelector
                            model-type="VLLM"
                            :selected-model-id="uiState.multimodalConfig.vllmModelId"
                            :all-models="allModels"
                            :status="showMultimodalModelError ? 'error' : 'default'"
                            :placeholder="t('knowledgeEditor.advanced.multimodal.vllmPlaceholder')"
                            @update:selected-model-id="handleMultimodalVLLMChange"
                            @add-model="handleAddVLLMModel"
                          />
                        </div>
                      </div>
                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.advanced.multimodal.descriptionLanguageLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.descriptionLanguageDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-select
                            v-model="uiState.multimodalConfig.descriptionLanguage"
                            clearable
                            :placeholder="t('knowledgeEditor.advanced.multimodal.descriptionLanguageAuto')"
                            :style="{ width: '280px' }"
                          >
                            <t-option value="Chinese" :label="t('language.zhCN')" />
                            <t-option value="English" :label="t('language.enUS')" />
                            <t-option value="Korean" :label="t('language.koKR')" />
                            <t-option value="Russian" :label="t('language.ruRU')" />
                          </t-select>
                        </div>
                      </div>
                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row setting-row-vertical">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.advanced.multimodal.customInstructionsLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.customInstructionsDescription') }}</p>
                        </div>
                        <div class="setting-control setting-control-full">
                          <t-textarea
                            v-model="uiState.multimodalConfig.customInstructions"
                            :placeholder="t('knowledgeEditor.advanced.multimodal.customInstructionsPlaceholder')"
                            :maxlength="4000"
                            :autosize="{ minRows: 3, maxRows: 8 }"
                          />
                        </div>
                      </div>
                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row setting-row-vertical">
                        <div class="setting-info">
                          <label>{{ t('uploadConfirm.imageEmbeddingModel') }}</label>
                          <p class="desc">{{ t('uploadConfirm.imageIndexInherited') }}</p>
                          <p v-if="uiState.imageVectorEnabled && !embeddingTakesImages" class="warn">
                            {{ t('knowledgeEditor.advanced.multimodal.imageVectorModelUnsupported') }}
                          </p>
                        </div>
                        <div class="setting-control">
                          <ModelSelector
                            model-type="Embedding"
                            :selected-model-id="kbInfo?.embedding_model_id || ''"
                            :all-models="allModels"
                            disabled
                            :placeholder="t('uploadConfirm.notSet')"
                          />
                        </div>
                      </div>
                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('uploadConfirm.imageVectorIndex') }}</label>
                          <p class="desc">{{ t('uploadConfirm.imageVectorIndexHint') }}</p>
                          <p v-if="!kbInfo?.indexing_strategy?.vector_enabled" class="warn">
                            {{ t('uploadConfirm.imageVectorRequiresIndex') }}
                          </p>
                        </div>
                        <div class="setting-control">
                          <t-switch
                            v-model="uiState.imageVectorEnabled"
                            :disabled="!kbInfo?.indexing_strategy?.vector_enabled"
                            size="medium"
                          />
                        </div>
                      </div>

                      <div v-if="uiState.multimodalConfig.enabled" class="setting-row setting-row-vertical image-panel-flush">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.advanced.multimodal.imagePipelineSectionLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.imagePipelineSectionDescription') }}</p>
                        </div>
                        <ImagePipelineSettings
                          v-model:params="uiState.imagePipelineParams"
                          :pipeline-id="uiState.imagePipeline"
                          @update:pipeline-id="uiState.imagePipeline = $event"
                          @update:invalid="imagePipelineInvalid = $event"
                        >
                          <template #notes>
                            <div
                              v-if="
                                uiState.multimodalConfig.enabled &&
                                uiState.imagePipeline === IMAGE_PIPELINE_SMARTOCR
                              "
                              class="image-attr-block"
                            >
                              <div class="image-attr-heading">
                                <label>{{ t('knowledgeEditor.advanced.multimodal.imageAttrsSchemaLabel') }}</label>
                                <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.imageAttrsSchemaDescription') }}</p>
                              </div>
                              <ul v-if="imageAttrDisplays.length" class="image-attr-list">
                                <li v-for="attr in imageAttrDisplays" :key="attr.name" class="image-attr-row">
                                  <div class="image-attr-head">
                                    <span class="image-attr-label">{{ attr.label }}</span>
                                    <code class="image-attr-name">{{ attr.name }}</code>
                                  </div>
                                  <p v-if="attr.description" class="image-attr-desc">{{ attr.description }}</p>
                                  <ul class="image-attr-value-list">
                                    <li v-for="v in attr.values" :key="v.value" class="image-attr-value">
                                      <code>{{ v.value }}</code>
                                      <span class="image-attr-value-label">{{ v.label }}</span>
                                    </li>
                                  </ul>
                                </li>
                              </ul>
                              <div class="image-attr-section">
                                <div class="setting-info">
                                  <label>{{ t('knowledgeEditor.advanced.multimodal.imageAttrsOcrConditions') }}</label>
                                  <p class="desc">{{ t('knowledgeEditor.advanced.multimodal.imageAttrsOcrConditionsDesc') }}</p>
                                </div>
                                <ul class="image-attr-condition-list">
                                  <li v-for="(cond, i) in imageAttrConditionDisplays" :key="i" class="image-attr-condition">
                                    <span class="image-attr-condition-label">{{ cond.label }}</span>
                                    <code class="image-attr-condition-raw">{{ cond.raw }}</code>
                                  </li>
                                </ul>
                              </div>
                            </div>
                          </template>
                        </ImagePipelineSettings>
                      </div>
                      <div
                        v-if="uiState.multimodalConfig.enabled && uiState.imagePipeline === IMAGE_PIPELINE_SMARTOCR"
                        class="image-pipeline-kb-note"
                      >
                        {{ t('knowledgeEditor.advanced.multimodal.imagePipelineKbNote') }}
                      </div>
                    </div>
                  </div>
                </div>

                <div v-show="activeSection === 'asr'" class="section" data-section="asr">
                  <div class="kb-settings-block">
                    <div class="section-header">
                      <h2 class="section-title">{{ t('knowledgeEditor.asr.title') }}</h2>
                      <p class="section-desc">{{ t('knowledgeEditor.asr.description') }}</p>
                    </div>
                    <div v-if="issueSectionKeys.has('asr')" class="section-notice">
                      <t-icon name="info-circle-filled" />
                      <span>{{ t('uploadConfirm.asrSetupHint') }}</span>
                    </div>
                    <div class="settings-group">
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.asr.label') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.asr.desc') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-switch v-model="uiState.asrConfig.enabled" size="medium" />
                        </div>
                      </div>
                      <div v-if="uiState.asrConfig.enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.asr.modelLabel') }} <span class="required">*</span></label>
                          <p class="desc">{{ t('knowledgeEditor.asr.modelDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <ModelSelector
                            model-type="ASR"
                            :selected-model-id="uiState.asrConfig.modelId"
                            :all-models="allModels"
                            :status="showAsrModelError ? 'error' : 'default'"
                            :placeholder="t('knowledgeEditor.asr.modelPlaceholder')"
                            @update:selected-model-id="(val: string) => { uiState.asrConfig.modelId = val }"
                            @add-model="handleAddASRModel"
                          />
                        </div>
                      </div>
                      <div v-if="uiState.asrConfig.enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ t('knowledgeEditor.asr.languageLabel') }}</label>
                          <p class="desc">{{ t('knowledgeEditor.asr.languageDescription') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-input
                            v-model="uiState.asrConfig.language"
                            clearable
                            :placeholder="t('knowledgeEditor.asr.languagePlaceholder')"
                            :style="{ width: '280px' }"
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <footer class="modal-footer">
                <t-button theme="default" variant="outline" @click="handleCancel">
                  {{ t('uploadConfirm.cancel') }}
                </t-button>
                <t-button theme="primary" :disabled="!canConfirm" @click="handleConfirm">
                  {{ confirmButtonText }}
                </t-button>
              </footer>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ModelSelector from '@/components/ModelSelector.vue'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { useUIStore } from '@/stores/ui'
import { getUploadFileKey } from '../utils/uploadSources'
import {
  listKnowledgeTags,
  mergeImageActions,
  fetchImageAttrSchema,
  FALLBACK_IMAGE_ATTR_SCHEMA,
  type ImageAttrSchema,
} from '@/api/knowledge-base'
import type { ModelConfig } from '@/api/model'
import { imageAttrDisplay, imageAttrConditionDisplay } from '@/utils/imageAttrDisplay'
import ImagePipelineSettings from '../ImagePipelineSettings.vue'
import {
  buildPipelineFields,
  resolveImagePipelineFromKb,
  normalizeImagePipelineId,
  IMAGE_PIPELINE_DEFAULT,
  IMAGE_PIPELINE_SMARTOCR,
} from '@/utils/imageProcessingConfig'
import type { FolderOption } from './FolderPickerMenu.vue'
import type { ParserEngineRule } from '@/types/knowledgeProcess'
import type {
  UploadConfirmManualSource,
  UploadConfirmMode,
  UploadConfirmReparseSource,
  UploadConfirmResult,
} from '@/stores/uploadConfirm'
import UploadSourceQueuePanel from './upload/UploadSourceQueuePanel.vue'
import UploadAdvancedOverridesSection from './upload/UploadAdvancedOverridesSection.vue'
import {
  useUploadProcessOverrides,
  truncateUploadNavText,
  type ConfigSectionKey,
  type IssueSectionKey,
  type UploadKBInfo,
  type UploadNavItem,
  type UploadNodeExtractConfig,
} from '../composables/useUploadProcessOverrides'

const IMAGE_EXTENSIONS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp']
const AUDIO_EXTENSIONS = ['mp3', 'wav', 'm4a', 'flac', 'ogg']

const props = withDefaults(defineProps<{
  visible: boolean
  kbInfo: UploadKBInfo | null | undefined
  mode?: UploadConfirmMode
  files?: File[]
  urls?: string[]
  tagIds?: string[]
  manualPreview?: UploadConfirmManualSource | null
  reparsePreview?: UploadConfirmReparseSource | null
  tagId?: string
  acceptFileTypes?: string
  supportedFileTypes?: string[]
  targetFolder?: string
  folderOptions?: FolderOption[]
}>(), {
  mode: 'file',
  files: () => [],
  urls: () => [],
  tagIds: () => [],
  manualPreview: null,
  reparsePreview: null,
  acceptFileTypes: '',
  supportedFileTypes: () => [],
  targetFolder: '',
  folderOptions: () => [],
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [payload: UploadConfirmResult]
  cancel: []
}>()

const { t, te } = useI18n()
const chatResources = useChatResourcesStore()
const editorResources = useEditorResourcesStore()
const uiStore = useUIStore()

const imageAttrSchema = ref<ImageAttrSchema | null>(null)
const displaySchema = computed<ImageAttrSchema>(
  () => imageAttrSchema.value ?? FALLBACK_IMAGE_ATTR_SCHEMA,
)
let schemaLoadSeq = 0
async function loadImageAttrSchema(kbId: string) {
  const seq = ++schemaLoadSeq
  try {
    const schema = await fetchImageAttrSchema(kbId)
    if (seq !== schemaLoadSeq || props.kbInfo?.id !== kbId) return
    imageAttrSchema.value = schema
  } catch {
    if (seq === schemaLoadSeq) imageAttrSchema.value = null
  }
}

const { uiState, initFromKbInfo, buildProcessOverrides, applyOverridesToState } =
  useUploadProcessOverrides({
    mergeImageActions,
    resolveImagePipelineFromKb,
    buildPipelineFields,
    normalizeImagePipelineId,
    imagePipelineDefault: IMAGE_PIPELINE_DEFAULT,
    imagePipelineSmartOcr: IMAGE_PIPELINE_SMARTOCR,
    onLoadImageAttrSchema: loadImageAttrSchema,
  })

const imageAttrDisplays = computed(() =>
  displaySchema.value.attributes.map((attr) => imageAttrDisplay(attr, t, te)),
)
const imageAttrConditionDisplays = computed(() =>
  uiState.value.imageActions.ocr.on.map((cond) =>
    imageAttrConditionDisplay(cond, displaySchema.value, t, te),
  ),
)

const allModels = ref<ModelConfig[]>([])
const embeddingTakesImages = computed(() => {
  const model = allModels.value.find((m) => m.id === props.kbInfo?.embedding_model_id)
  return !model || !!model.capabilities?.input?.includes('image')
})
const localFiles = ref<File[]>([])
const localUrls = ref<string[]>([])
const availableTags = ref<Array<{ id: string; name: string }>>([])
const selectedTagIds = ref<string[]>([])
const tagsLoading = ref(false)
const tagsLoadFailed = ref(false)
const chunkingMoreOpen = ref(false)
const activeSection = ref<ConfigSectionKey>('tags')
const localTargetFolder = ref('')
const dialogSessionSeq = ref(0)

const dialogVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

function getModelName(modelId: string): string {
  if (!modelId) return t('uploadConfirm.notSet')
  const model = allModels.value.find((m) => m.id === modelId)
  return model?.name || modelId
}

function hasParserCustomization(): boolean {
  const rules = uiState.value.chunkingConfig.parserEngineRules
  if (!rules?.length) return false
  return (
    rules.some((rule) => rule.engine && rule.engine !== 'builtin') ||
    rules.some((rule) => rule.xlsx_first_row_as_header)
  )
}

function getFileExt(file: File): string {
  const dot = file.name.lastIndexOf('.')
  if (dot < 0) return ''
  return file.name.substring(dot + 1).toLowerCase()
}

function getExtFromUrl(url: string): string {
  try {
    const pathname = new URL(url).pathname
    const dot = pathname.lastIndexOf('.')
    if (dot < 0) return ''
    return pathname.substring(dot + 1).toLowerCase()
  } catch {
    return ''
  }
}

function inferMediaExtsFromMarkdown(content: string): string[] {
  const exts = new Set<string>()
  const patterns = [
    /\.(jpg|jpeg|png|gif|bmp|webp)(\?|#|\)|\s|$)/gi,
    /\.(mp3|wav|m4a|flac|ogg)(\?|#|\)|\s|$)/gi,
  ]
  for (const pattern of patterns) {
    let match: RegExpExecArray | null
    while ((match = pattern.exec(content)) !== null) {
      let ext = match[1].toLowerCase()
      if (ext === 'jpeg') ext = 'jpg'
      exts.add(ext)
    }
  }
  return [...exts]
}

const batchItemCount = computed(() => localFiles.value.length + localUrls.value.length)

const dialogTitle = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.titleManual')
  if (props.mode === 'reparse') return t('uploadConfirm.titleReparse')
  return t('uploadConfirm.title')
})

const confirmButtonText = computed(() => {
  if (props.mode === 'manual') return t('uploadConfirm.confirmManual')
  if (props.mode === 'reparse') return t('uploadConfirm.confirmReparse')
  return t('uploadConfirm.confirm')
})

const batchFileExts = computed(() => {
  const set = new Set<string>()
  if (props.mode === 'manual' && props.manualPreview?.content) {
    for (const ext of inferMediaExtsFromMarkdown(props.manualPreview.content)) {
      set.add(ext)
    }
  }
  if (props.mode === 'reparse') {
    const ext = (props.reparsePreview?.fileType || '').toLowerCase()
    if (ext) set.add(ext)
  }
  for (const url of localUrls.value) {
    const ext = getExtFromUrl(url)
    if (ext) set.add(ext)
  }
  for (const file of localFiles.value) {
    const ext = getFileExt(file)
    if (ext) set.add(ext)
  }
  return [...set]
})

const hasPdf = computed(() => batchFileExts.value.includes('pdf'))

const chunkingStrategyOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.strategies.auto.label'), value: 'auto' },
  { label: t('knowledgeEditor.chunking.strategies.heading.label'), value: 'heading' },
  { label: t('knowledgeEditor.chunking.strategies.heuristic.label'), value: 'heuristic' },
  { label: t('knowledgeEditor.chunking.strategies.legacy.label'), value: 'legacy' },
])

const separatorOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.separators.doubleNewline'), value: '\n\n' },
  { label: t('knowledgeEditor.chunking.separators.singleNewline'), value: '\n' },
  { label: t('knowledgeEditor.chunking.separators.periodCn'), value: '。' },
  { label: t('knowledgeEditor.chunking.separators.exclamationCn'), value: '！' },
  { label: t('knowledgeEditor.chunking.separators.questionCn'), value: '？' },
  { label: t('knowledgeEditor.chunking.separators.semicolonCn'), value: '；' },
  { label: t('knowledgeEditor.chunking.separators.semicolonEn'), value: ';' },
  { label: t('knowledgeEditor.chunking.separators.space'), value: ' ' },
])

const languageOptions = computed(() => [
  { label: t('knowledgeEditor.chunking.languageOptions.de'), value: 'de' },
  { label: t('knowledgeEditor.chunking.languageOptions.en'), value: 'en' },
  { label: t('knowledgeEditor.chunking.languageOptions.zh'), value: 'zh' },
])

const tagOptions = computed(() =>
  availableTags.value.map((tag) => ({
    label: tag.name,
    value: tag.id,
  })),
)

const llmModelId = computed(() => props.kbInfo?.summary_model_id || '')

const hasImages = computed(() => {
  if (props.mode === 'manual' && props.manualPreview?.content) {
    if (/data:image\/|!\[[^\]]*\]\([^)]+\)/i.test(props.manualPreview.content)) return true
  }
  return batchFileExts.value.some((ext) => IMAGE_EXTENSIONS.includes(ext))
})

const hasAudio = computed(() => batchFileExts.value.some((ext) => AUDIO_EXTENSIONS.includes(ext)))

const isGraphDatabaseEnabled = computed(() => {
  const engine = editorResources.systemInfo?.graph_database_engine
  return !!engine && engine !== 'Not Enabled'
})

const isGraphSectionAvailable = computed(
  () => isGraphDatabaseEnabled.value && uiState.value.graphEnabled,
)

const showMultimodalModelError = computed(
  () => uiState.value.multimodalConfig.enabled && !uiState.value.multimodalConfig.vllmModelId,
)

const showAsrModelError = computed(
  () => uiState.value.asrConfig.enabled && !uiState.value.asrConfig.modelId,
)

const issueSectionKeys = computed(() => {
  const keys = new Set<IssueSectionKey>()
  if (hasImages.value) {
    if (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId) {
      keys.add('multimodal')
    }
  } else if (showMultimodalModelError.value) {
    keys.add('multimodal')
  }
  if (hasAudio.value) {
    if (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId) {
      keys.add('asr')
    }
  } else if (showAsrModelError.value) {
    keys.add('asr')
  }
  return keys
})

function getSectionNavStatus(
  key: ConfigSectionKey,
  issue?: boolean,
): { status: string; statusTone?: 'warning' | 'error' | 'muted' } {
  switch (key) {
    case 'tags':
      return selectedTagIds.value.length === 0
        ? { status: t('uploadConfirm.summaryNoTags'), statusTone: 'muted' }
        : { status: t('uploadConfirm.summaryTagsCount', { count: selectedTagIds.value.length }) }
    case 'parser':
      if (uiState.value.pdfForceScanned && hasPdf.value) {
        return { status: t('uploadConfirm.summaryParserForceScanned') }
      }
      return hasParserCustomization()
        ? { status: t('uploadConfirm.navParserCustomized') }
        : { status: t('uploadConfirm.navParserDefault'), statusTone: 'muted' }
    case 'chunking': {
      const chunking = uiState.value.chunkingConfig
      const parts = [t('uploadConfirm.navChunkingSummary', { size: chunking.chunkSize })]
      if (chunking.enableParentChild) parts.push(t('uploadConfirm.summaryParentChildShort'))
      return { status: parts.join(' · ') }
    }
    case 'multimodal': {
      if (issue) return { status: t('uploadConfirm.statusNeedsSetup'), statusTone: 'error' }
      const mm = uiState.value.multimodalConfig
      if (!mm.enabled) return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      return {
        status: mm.vllmModelId ? getModelName(mm.vllmModelId) : t('uploadConfirm.notSet'),
        statusTone: mm.vllmModelId ? undefined : 'warning',
      }
    }
    case 'asr': {
      if (issue) return { status: t('uploadConfirm.statusNeedsSetup'), statusTone: 'error' }
      const asr = uiState.value.asrConfig
      if (!asr.enabled) return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      return {
        status: asr.modelId ? getModelName(asr.modelId) : t('uploadConfirm.notSet'),
        statusTone: asr.modelId ? undefined : 'warning',
      }
    }
    case 'summary':
      return uiState.value.summaryEnabled
        ? { status: t('uploadConfirm.statusOn') }
        : { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
    case 'question': {
      const question = uiState.value.questionGenerationConfig
      return !question.enabled
        ? { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
        : { status: t('uploadConfirm.summaryQuestionCountValue', { count: question.questionCount }) }
    }
    case 'graph': {
      if (!uiState.value.graphEnabled || !uiState.value.nodeExtractConfig.enabled) {
        return { status: t('uploadConfirm.statusOff'), statusTone: 'muted' }
      }
      const tagCount = uiState.value.nodeExtractConfig.tags?.length ?? 0
      return tagCount > 0
        ? { status: t('uploadConfirm.summaryGraphTagsValue', { count: tagCount }) }
        : { status: t('uploadConfirm.statusOn') }
    }
    default:
      return { status: '' }
  }
}

const navItems = computed<UploadNavItem[]>(() => {
  const items: UploadNavItem[] = []
  const push = (key: ConfigSectionKey, icon: string, label: string, issue?: boolean) => {
    const statusMeta = getSectionNavStatus(key, issue)
    const full = statusMeta.status
    items.push({
      key,
      icon,
      label,
      status: truncateUploadNavText(full),
      statusFull: full,
      statusTone: statusMeta.statusTone,
      issue,
    })
  }

  if (props.mode !== 'reparse') push('tags', 'tag', t('uploadConfirm.tabTags'))
  push('parser', 'file-search', t('settings.parserEngine'))
  push('chunking', 'file-copy', t('knowledgeEditor.sidebar.chunking'))
  push('multimodal', 'image', t('knowledgeEditor.sidebar.multimodal'), issueSectionKeys.value.has('multimodal'))
  push('asr', 'sound', t('knowledgeEditor.sidebar.asr'), issueSectionKeys.value.has('asr'))
  push('summary', 'file', t('uploadConfirm.documentSummary'))
  push('question', 'chat', t('knowledgeEditor.advanced.questionGeneration.label'))
  if (isGraphSectionAvailable.value) push('graph', 'chart-bubble', t('knowledgeEditor.sidebar.graph'))
  return items
})

const canConfirm = computed(() => {
  if (props.mode === 'file' && batchItemCount.value === 0) return false
  if (props.mode === 'manual' && !props.manualPreview?.content?.trim()) return false
  if (hasImages.value && (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId)) {
    return false
  }
  if (hasAudio.value && (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId)) {
    return false
  }
  if (showMultimodalModelError.value || showAsrModelError.value) return false
  return true
})

function getDefaultSection(): ConfigSectionKey {
  if (props.mode === 'reparse') return 'parser'
  if (issueSectionKeys.value.has('multimodal')) return 'multimodal'
  if (issueSectionKeys.value.has('asr')) return 'asr'
  return 'tags'
}

function goToSection(key: ConfigSectionKey) {
  activeSection.value = key
  nextTick(() => {
    document.querySelector(`[data-section="${key}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}

async function loadModels() {
  try {
    await chatResources.ensureModels()
    allModels.value = chatResources.allModels || []
  } catch {
    allModels.value = []
  }
}

async function loadSystemInfo() {
  try {
    await editorResources.ensureSystemInfo()
  } catch {
    // Graph section falls back to hidden when system info is unavailable.
  }
}

interface KnowledgeTagListResponse {
  data?: {
    data?: Array<{ id: string | number; name?: string }>
  }
}

let tagsLoadSeq = 0
async function loadTags() {
  const seq = ++tagsLoadSeq
  const kbId = props.kbInfo?.id
  availableTags.value = []
  tagsLoadFailed.value = false
  if (!kbId || props.mode === 'reparse') {
    tagsLoading.value = false
    return
  }

  tagsLoading.value = true
  try {
    const response = (await listKnowledgeTags(kbId, { page: 1, page_size: 1000 })) as unknown as KnowledgeTagListResponse
    if (seq !== tagsLoadSeq || props.kbInfo?.id !== kbId) return
    const tags = response?.data?.data || []
    availableTags.value = tags.map((tag) => ({
      id: String(tag.id),
      name: String(tag.name || ''),
    }))
  } catch {
    if (seq === tagsLoadSeq) tagsLoadFailed.value = true
  } finally {
    if (seq === tagsLoadSeq) tagsLoading.value = false
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    dialogSessionSeq.value++
    localFiles.value = props.mode === 'file' ? [...(props.files || [])] : []
    localUrls.value = props.mode === 'file' ? [...(props.urls || [])] : []
    selectedTagIds.value = props.mode === 'reparse' ? [] : [...(props.tagIds || [])]
    localTargetFolder.value = props.mode === 'file' ? (props.targetFolder || '') : ''
    initFromKbInfo(props.kbInfo)
    if (props.mode === 'reparse') {
      applyOverridesToState(props.reparsePreview?.processOverrides)
    }
    activeSection.value = getDefaultSection()
    chunkingMoreOpen.value = false
    loadModels()
    loadSystemInfo()
    loadTags()
  },
)

watch(isGraphSectionAvailable, (available) => {
  if (!available && activeSection.value === 'graph') {
    activeSection.value = getDefaultSection()
  }
})

const appendFiles = (incoming: File[]) => {
  const existingKeys = new Set(localFiles.value.map(getUploadFileKey))
  const toAdd: File[] = []
  let duplicateCount = 0

  for (const file of incoming) {
    const key = getUploadFileKey(file)
    if (existingKeys.has(key)) {
      duplicateCount++
      continue
    }
    existingKeys.add(key)
    toAdd.push(file)
  }

  if (toAdd.length > 0) {
    localFiles.value = [...localFiles.value, ...toAdd]
    MessagePlugin.success(t('uploadConfirm.filesAdded', { count: toAdd.length }))
  } else if (duplicateCount > 0) {
    MessagePlugin.warning(t('uploadConfirm.filesAllDuplicate'))
  }
}

const appendUrl = (url: string) => {
  if (localUrls.value.includes(url)) {
    MessagePlugin.warning(t('uploadConfirm.urlDuplicate'))
    return
  }
  localUrls.value = [...localUrls.value, url]
  MessagePlugin.success(t('uploadConfirm.urlAdded'))
}

const removeUrl = (index: number) => {
  localUrls.value = localUrls.value.filter((_, i) => i !== index)
}

const removeFile = (index: number) => {
  localFiles.value = localFiles.value.filter((_, i) => i !== index)
}

const handleParserEngineRulesUpdate = (rules: ParserEngineRule[]) => {
  uiState.value.chunkingConfig.parserEngineRules = rules
}

const handleMultimodalVLLMChange = (modelId: string) => {
  uiState.value.multimodalConfig.vllmModelId = modelId
}

const handleAddVLLMModel = () => {
  uiStore.openSettings('models', 'vllm')
}

const handleAddASRModel = () => {
  uiStore.openSettings('models', 'asr')
}

const handleNodeExtractUpdate = (config: UploadNodeExtractConfig) => {
  uiState.value.nodeExtractConfig = { ...config }
  uiState.value.graphEnabled = config.enabled
}

const validateBeforeConfirm = (): boolean => {
  if (hasImages.value) {
    if (!uiState.value.multimodalConfig.enabled || !uiState.value.multimodalConfig.vllmModelId) {
      MessagePlugin.warning(t('uploadConfirm.vlmModelRequired'))
      uiState.value.multimodalConfig.enabled = true
      goToSection('multimodal')
      return false
    }
  } else if (showMultimodalModelError.value) {
    MessagePlugin.warning(t('uploadConfirm.vlmModelSelectRequired'))
    goToSection('multimodal')
    return false
  }

  if (hasAudio.value) {
    if (!uiState.value.asrConfig.enabled || !uiState.value.asrConfig.modelId) {
      MessagePlugin.warning(t('uploadConfirm.asrModelRequired'))
      uiState.value.asrConfig.enabled = true
      goToSection('asr')
      return false
    }
  } else if (showAsrModelError.value) {
    MessagePlugin.warning(t('uploadConfirm.asrModelSelectRequired'))
    goToSection('asr')
    return false
  }
  return true
}

const handleCancel = () => {
  emit('cancel')
  emit('update:visible', false)
}

const imagePipelineInvalid = ref(false)

const handleConfirm = () => {
  if (props.mode === 'file' && batchItemCount.value === 0) {
    MessagePlugin.warning(t('uploadConfirm.noItems'))
    return
  }
  if (!validateBeforeConfirm()) return
  if (uiState.value.multimodalConfig.enabled && imagePipelineInvalid.value) {
    MessagePlugin.warning(t('imagePipeline.errors.noActionEnabled'))
    return
  }

  const processConfig = buildProcessOverrides()
  if (props.mode === 'manual' && props.manualPreview) {
    emit('confirm', {
      processConfig,
      mode: 'manual',
      tagIds: [...selectedTagIds.value],
      manual: { ...props.manualPreview, tagIds: [...selectedTagIds.value] },
    })
  } else if (props.mode === 'reparse' && props.reparsePreview) {
    emit('confirm', { processConfig, mode: 'reparse', reparse: { ...props.reparsePreview } })
  } else {
    emit('confirm', {
      processConfig,
      mode: 'file',
      tagIds: [...selectedTagIds.value],
      files: [...localFiles.value],
      urls: [...localUrls.value],
      targetFolder: localTargetFolder.value,
    })
  }
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
@import './upload-confirm-dialog.less';
</style>

<style lang="less">
.upload-destination-popup {
  z-index: 3100 !important;

  .t-popup__content {
    padding: 4px !important;
    margin-top: 6px !important;
    min-width: 208px;
    border-radius: var(--app-radius-lg) !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow: var(--gy-shadow-lg) !important;
  }
}
</style>
