<template>
  <SettingsModalShell
    :visible="visible"
    :title="editorMode === 'create' ? $t('knowledgeEditor.titleCreate') : $t('knowledgeEditor.titleEdit')"
    v-model="currentSection"
    :nav-groups="navGroups"
    :loading="loading"
    :z-index="1000"
    nav-guide="kb-editor-sidebar"
    nav-item-guide-prefix="kb-editor-nav"
    @close="modalShell.requestClose"
  >
    <div class="content-wrapper">
      <KBEditorBasicProfileSection
        :current-section="currentSection"
        :form-data="formData"
        :editor-mode="editorMode"
        :active-kb-id="activeKbId"
        :is-post-create-session="isPostCreateSession"
        :is-f-a-q="isFAQ"
        :is-indexing-locked="isIndexingLocked"
        :saving="saving"
        :loading="loading"
        :generated-profile="generatedProfile"
        :generating-profile="generatingProfile"
        @copy-kb-id="copyKbId"
        @update:type="(val) => { if (formData) formData.type = val }"
        @update:name="(val) => { if (formData) formData.name = val }"
        @update:description="(val) => { if (formData) formData.description = val }"
        @update:wiki-config="(val) => { if (formData) formData.wikiConfig = val }"
        @toggle-vector-indexing="toggleVectorIndexing"
        @toggle-wiki-indexing="toggleWikiIndexing"
        @adopt-profile-gist="handleAdoptProfileGist"
        @generate-profile="handleGenerateProfile"
      />

      <KBEditorSecondarySections
        :current-section="currentSection"
        :form-data="formData"
        :editor-mode="editorMode"
        :active-kb-id="activeKbId"
        :has-files="hasFiles"
        :is-f-a-q="isFAQ"
        :all-models="allModels"
        :is-kb-in-active-tenant="isKbInActiveTenant"
        :can-share-k-b="canShareKB"
        :can-view-activity="canViewActivity"
        @update:model-config="handleModelConfigUpdate"
        @update:vector-store-id="handleVectorStoreIdUpdate"
        @update:faq-config="(val) => { if (formData) formData.faqConfig = val }"
        @update:parser-engine-rules="handleParserEngineRulesUpdate"
        @update:storage-backend-id="handleStorageBackendUpdate"
        @update:storage-provider="handleStorageProviderUpdate"
        @update:chunking-config="handleChunkingConfigUpdate"
        @update:node-extract="handleNodeExtractUpdate"
        @update:question-generation="handleQuestionGenerationUpdate"
        @update:auto-tag="(val) => { if (formData) formData.autoTagConfig = val }"
        @update:profile-config="(val) => { if (formData) formData.profileConfig = val }"
        @update:table-metadata-instructions="(val) => { if (formData) formData.chunkingConfig.tableMetadataInstructions = val }"
        @update:ds-count="dsCount = $event"
      />

      <div v-if="!isFAQ" v-show="currentSection === 'multimodal'" class="section">
        <div v-if="formData" class="kb-multimodal-settings">
          <div class="section-header">
            <h2>{{ $t('knowledgeEditor.multimodal.title') }}</h2>
            <p class="section-description">{{ $t('knowledgeEditor.multimodal.description') }}</p>
          </div>

          <div class="settings-group">
            <div class="setting-row" data-guide="kb-create-multimodal-toggle">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.advanced.multimodal.label') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.description') }}</p>
              </div>
              <div class="setting-control">
                <t-switch
                  v-model="formData.multimodalConfig.enabled"
                  size="medium"
                  @change="handleMultimodalToggle"
                />
              </div>
            </div>

            <div
              v-if="formData.multimodalConfig.enabled"
              class="setting-row"
              data-guide="kb-create-multimodal-vllm"
            >
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.advanced.multimodal.vllmLabel') }} <span class="required">*</span></label>
                <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.vllmDescription') }}</p>
              </div>
              <div class="setting-control">
                <ModelSelector
                  model-type="VLLM"
                  :selected-model-id="formData.multimodalConfig.vllmModelId"
                  :all-models="allModels"
                  :placeholder="$t('knowledgeEditor.advanced.multimodal.vllmPlaceholder')"
                  @update:selected-model-id="handleMultimodalVLLMChange"
                  @add-model="handleAddVLLMModel"
                />
              </div>
            </div>

            <div v-if="formData.multimodalConfig.enabled" class="setting-row">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.advanced.multimodal.descriptionLanguageLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.descriptionLanguageDescription') }}</p>
              </div>
              <div class="setting-control">
                <t-select
                  v-model="formData.multimodalConfig.descriptionLanguage"
                  clearable
                  :placeholder="$t('knowledgeEditor.advanced.multimodal.descriptionLanguageAuto')"
                >
                  <t-option value="Chinese" :label="$t('language.zhCN')" />
                  <t-option value="English" :label="$t('language.enUS')" />
                  <t-option value="Korean" :label="$t('language.koKR')" />
                  <t-option value="Russian" :label="$t('language.ruRU')" />
                </t-select>
              </div>
            </div>

            <div v-if="formData.multimodalConfig.enabled" class="setting-row setting-row-vertical">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.advanced.multimodal.customInstructionsLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.customInstructionsDescription') }}</p>
              </div>
              <div class="setting-control setting-control-full">
                <t-textarea
                  v-model="formData.multimodalConfig.customInstructions"
                  :placeholder="$t('knowledgeEditor.advanced.multimodal.customInstructionsPlaceholder')"
                  :maxlength="4000"
                  :autosize="{ minRows: 3, maxRows: 8 }"
                />
              </div>
            </div>

            <div v-if="formData.multimodalConfig.enabled" class="setting-row">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.advanced.multimodal.imageVectorLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.imageVectorDescription') }}</p>
                <p v-if="formData.imageVectorEnabled && !embeddingTakesImages" class="desc desc-warning">
                  {{ $t('knowledgeEditor.advanced.multimodal.imageVectorModelUnsupported') }}
                </p>
              </div>
              <div class="setting-control">
                <t-switch v-model="formData.imageVectorEnabled" size="medium" />
              </div>
            </div>

            <div v-if="formData.multimodalConfig.enabled" class="setting-row setting-row-vertical image-panel-flush">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.advanced.multimodal.imagePipelineSectionLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.imagePipelineSectionDescription') }}</p>
              </div>
              <ImagePipelineSettings
                v-model:params="formData.imagePipelineParams"
                :pipeline-id="formData.imagePipeline"
                @update:pipeline-id="onPipelineChange"
                @update:invalid="onPipelineInvalidChange"
              >
                <template #notes>
                  <div v-if="formData.multimodalConfig.enabled && formData.imageAttrsEnabled" class="image-attr-block">
                    <div class="image-attr-heading">
                      <label>{{ $t('knowledgeEditor.advanced.multimodal.imageAttrsSchemaLabel') }}</label>
                      <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.imageAttrsSchemaDescription') }}</p>
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
                        <label>{{ $t('knowledgeEditor.advanced.multimodal.imageAttrsOcrConditions') }}</label>
                        <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.imageAttrsOcrConditionsDesc') }}</p>
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
          </div>
        </div>
      </div>

      <div v-if="!isFAQ" v-show="currentSection === 'asr'" class="section">
        <div v-if="formData" class="kb-multimodal-settings">
          <div class="section-header">
            <h2>{{ $t('knowledgeEditor.asr.title') }}</h2>
            <p class="section-description">{{ $t('knowledgeEditor.asr.description') }}</p>
          </div>

          <div class="settings-group">
            <div class="setting-row">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.asr.label') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.asr.desc') }}</p>
              </div>
              <div class="setting-control">
                <t-switch v-model="formData.asrConfig.enabled" size="medium" />
              </div>
            </div>

            <div v-if="formData.asrConfig.enabled" class="setting-row">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.asr.modelLabel') }} <span class="required">*</span></label>
                <p class="desc">{{ $t('knowledgeEditor.asr.modelDescription') }}</p>
              </div>
              <div class="setting-control">
                <ModelSelector
                  model-type="ASR"
                  :selected-model-id="formData.asrConfig.modelId"
                  :all-models="allModels"
                  :placeholder="$t('knowledgeEditor.asr.modelPlaceholder')"
                  @update:selected-model-id="(val: string) => { if (formData) formData.asrConfig.modelId = val }"
                  @add-model="handleAddASRModel"
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer-note>
      <p v-if="isPostCreateSession" class="settings-footer-note">
        <t-icon name="check-circle-filled" class="settings-footer-note__icon" />
        <span>
          <strong>{{ $t('knowledgeEditor.postCreateHint.title') }}</strong>
          {{ $t('knowledgeEditor.postCreateHint.footer') }}
        </span>
      </p>
      <p v-if="isInstantSection" class="settings-footer-note">
        <t-icon name="info-circle-filled" class="settings-footer-note__icon" />
        <span>{{ $t('knowledgeEditor.footer.instantEffect') }}</span>
      </p>
    </template>
    <template #footer>
      <t-button v-if="isInstantSection" theme="default" variant="outline" @click="modalShell.requestClose">
        {{ $t('common.close') }}
      </t-button>
      <template v-else>
        <t-button theme="default" variant="outline" @click="modalShell.requestClose">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button
          theme="primary"
          data-guide="kb-create-submit"
          :loading="saving"
          :disabled="loading"
          @click="handleSubmit"
        >
          {{ saveButtonLabel }}
        </t-button>
      </template>
    </template>
  </SettingsModalShell>

  <KbCreateContextualGuide
    :when="visible && editorMode === 'create'"
    :is-faq="isFAQ"
    :needs-embedding="kbCreateNeedsEmbedding"
  />
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import KbCreateContextualGuide from '@/components/KbCreateContextualGuide.vue'
import { KB_EDITOR_FOCUS_SECTION_EVENT, markContextualGuideDone } from '@/config/contextualGuides'
import { useModalShell } from '@/composables/useModalShell'
import SettingsModalShell from '@/components/SettingsModalShell.vue'
import ModelSelector from '@/components/ModelSelector.vue'
import ImagePipelineSettings from './ImagePipelineSettings.vue'
import KBEditorBasicProfileSection from './components/KBEditorBasicProfileSection.vue'
import KBEditorSecondarySections from './components/KBEditorSecondarySections.vue'
import {
  createKnowledgeBase,
  getKnowledgeBaseById,
  listKnowledgeFiles,
  updateKnowledgeBase,
  rebuildKBIndex,
  generateKnowledgeBaseProfile,
  mergeImageActions,
  fetchImageAttrSchema,
  FALLBACK_IMAGE_ATTR_SCHEMA,
  type ImageAttrSchema,
  type ImageProcessingConfig,
  type KnowledgeBaseProfile,
} from '@/api/knowledge-base'
import { updateKBConfig } from '@/api/initialization'
import type { ModelConfig } from '@/api/model'
import type { ParserEngineRule } from '@/types/knowledgeProcess'
import {
  buildImageProcessingConfig,
  resolveImagePipelineFromKb as resolveKbImagePipeline,
  IMAGE_PIPELINE_DEFAULT,
  IMAGE_PIPELINE_SMARTOCR,
} from '@/utils/imageProcessingConfig'
import { imageAttrDisplay, imageAttrConditionDisplay } from '@/utils/imageAttrDisplay'
import { useChatResourcesStore } from '@/stores/chatResources'
import { selectInitialModelId } from '@/utils/modelDefaults'
import { copyWithToast } from '@/utils/clipboard'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import {
  createInitialKBFormData,
  mapLoadedKBToFormData,
  buildKBModelConfigRequest,
  WIKI_ONLY_CHUNKING_PRESET,
  DEFAULT_CHUNKING_PRESET,
  type KBEditorChunkingConfig,
  type KBEditorFormData,
  type KBEditorIndexingStrategy,
  type KBEditorModelConfig,
  type KBEditorNodeExtractConfig,
  type KBEditorQuestionGenerationConfig,
  type KBSubmitPayload,
  type LoadedKnowledgeBaseRecord,
} from './composables/useKBEditorForm'

const props = defineProps<{
  visible: boolean
  mode: 'create' | 'edit'
  kbId?: string
  initialType?: 'document' | 'faq'
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'success', kbId: string): void
}>()

const uiStore = useUIStore()
const authStore = useAuthStore()
const chatResources = useChatResourcesStore()
const editorResources = useEditorResourcesStore()
const { t, te } = useI18n()

const imageAttrSchema = ref<ImageAttrSchema | null>(null)
const displaySchema = computed<ImageAttrSchema>(
  () => imageAttrSchema.value ?? FALLBACK_IMAGE_ATTR_SCHEMA,
)

const savedKbId = ref<string | null>(null)
const editorMode = computed(() => (savedKbId.value ? 'edit' : props.mode))
const activeKbId = computed(() => savedKbId.value ?? props.kbId)
const isPostCreateSession = computed(() => !!savedKbId.value)
const saveButtonLabel = computed(() =>
  editorMode.value === 'create'
    ? t('knowledgeEditor.buttons.create')
    : t('knowledgeEditor.buttons.saveAndClose')
)

const copyKbId = async () => {
  await copyWithToast(activeKbId.value, 'common.copied')
}

const currentSection = ref<string>('basic')
const onKbEditorFocusSection = (event: Event) => {
  const section = (event as CustomEvent<{ section?: string }>).detail?.section
  if (section) currentSection.value = section
}
onMounted(() => window.addEventListener(KB_EDITOR_FOCUS_SECTION_EVENT, onKbEditorFocusSection))
onBeforeUnmount(() => window.removeEventListener(KB_EDITOR_FOCUS_SECTION_EVENT, onKbEditorFocusSection))

const saving = ref(false)
const loading = ref(false)
const allModels = ref<ModelConfig[]>([])
const hasFiles = ref(false)
const generatedProfile = ref<KnowledgeBaseProfile | null>(null)
const generatingProfile = ref(false)
const initialStorageProvider = ref<string>('')
const tenantDefaultStorageProvider = ref('local')
const initialIndexingStrategy = ref<KBEditorIndexingStrategy | null>(null)
const dsCount = ref(0)
const kbCreatorId = ref<string>('')
const kbTenantId = ref<number>(0)

const activeWorkspaceTenantId = computed(() =>
  Number(authStore.effectiveTenantId ?? authStore.currentTenantId ?? 0),
)

const isKbInActiveTenant = computed(() => {
  const kbTid = Number(kbTenantId.value || 0)
  const activeTid = activeWorkspaceTenantId.value
  if (!kbTid || !activeTid) return true
  return kbTid === activeTid
})

const isKbOwner = computed(() => {
  const userId = authStore.user?.id || ''
  return Boolean(kbCreatorId.value && userId && kbCreatorId.value === userId && authStore.hasRole('contributor'))
})

const canShareKB = computed(() => {
  if (!activeKbId.value || !isKbInActiveTenant.value) return false
  if (isKbOwner.value) return true
  return authStore.hasRole('admin')
})

const canViewActivity = computed(() => {
  if (editorMode.value !== 'edit' || !activeKbId.value) return false
  if (Number(kbTenantId.value || 0) !== activeWorkspaceTenantId.value) return false
  return isKbOwner.value || authStore.hasRole('admin')
})
const chunkingDirty = ref(false)

const INSTANT_SECTIONS = new Set(['datasource', 'share', 'activity'])
const isInstantSection = computed(() => INSTANT_SECTIONS.has(currentSection.value))

const formData = ref<KBEditorFormData | null>(null)

const imageAttrDisplays = computed(() =>
  displaySchema.value.attributes.map((attr) => imageAttrDisplay(attr, t, te)),
)
const imageAttrConditionDisplays = computed(() =>
  (formData.value?.imageActions.ocr.on ?? []).map((cond) =>
    imageAttrConditionDisplay(cond, displaySchema.value, t, te),
  ),
)

const navItems = computed(() => {
  const items: Array<{ key: string; icon: string; label: string; badge?: number }> = [
    { key: 'basic', icon: 'info-circle', label: t('knowledgeEditor.sidebar.basic') },
    { key: 'models', icon: 'control-platform', label: t('knowledgeEditor.sidebar.models') },
    { key: 'vectorStore', icon: 'data-base', label: t('knowledgeEditor.sidebar.vectorStore') },
  ]
  if (formData.value?.type === 'faq') {
    items.push({ key: 'faq', icon: 'help-circle', label: t('knowledgeEditor.sidebar.faq') })
  } else {
    items.push(
      { key: 'parser', icon: 'file-search', label: t('settings.parserEngine') },
      { key: 'multimodal', icon: 'image', label: t('knowledgeEditor.sidebar.multimodal') },
      { key: 'asr', icon: 'sound', label: t('knowledgeEditor.sidebar.asr') },
      { key: 'storage', icon: 'cloud', label: t('knowledgeEditor.sidebar.storage') },
      { key: 'chunking', icon: 'file-copy', label: t('knowledgeEditor.sidebar.chunking') },
      { key: 'graph', icon: 'chart-bubble', label: t('knowledgeEditor.sidebar.graph') },
      { key: 'advanced', icon: 'setting', label: t('knowledgeEditor.sidebar.advanced') },
    )
    if (editorMode.value === 'edit' && activeKbId.value) {
      items.push({ key: 'datasource', icon: 'cloud-download', label: t('knowledgeEditor.sidebar.datasource'), badge: dsCount.value || undefined })
    }
  }
  if (editorMode.value === 'edit' && activeKbId.value && isKbInActiveTenant.value && !authStore.isLiteMode) {
    items.push({ key: 'share', icon: 'share', label: t('knowledgeEditor.sidebar.share') })
  }
  if (canViewActivity.value) {
    items.push({ key: 'activity', icon: 'history', label: t('knowledgeEditor.sidebar.activity') })
  }
  return items
})

const navGroups = computed(() => {
  const itemMap = new Map(navItems.value.map((item) => [item.key, item]))
  const pickItems = (keys: string[]) =>
    keys.map((key) => itemMap.get(key)).filter((item): item is NonNullable<typeof item> => Boolean(item))
  return [
    { key: 'basic', label: t('knowledgeEditor.navGroups.basic'), items: pickItems(['basic', 'models', 'vectorStore', 'faq']) },
    { key: 'processing', label: t('knowledgeEditor.navGroups.processing'), items: pickItems(['parser', 'chunking', 'multimodal', 'asr', 'graph', 'advanced']) },
    { key: 'data', label: t('knowledgeEditor.navGroups.data'), items: pickItems(['storage', 'datasource']) },
    { key: 'integration', label: t('knowledgeEditor.navGroups.integration'), items: pickItems(['share']) },
    { key: 'management', label: t('knowledgeEditor.navGroups.management'), items: pickItems(['activity']) },
  ].filter((group) => group.items.length > 0)
})

const embeddingTakesImages = computed(() => {
  const id = formData.value?.modelConfig.embeddingModelId
  const model = id ? allModels.value.find((m) => m.id === id) : undefined
  return !model || !!model.capabilities?.input?.includes('image')
})
const isFAQ = computed(() => formData.value?.type === 'faq')

const kbCreateNeedsEmbedding = computed(() => {
  if (!formData.value || formData.value.type === 'faq') return false
  const s = formData.value.indexingStrategy
  return Boolean(s?.vectorEnabled || s?.keywordEnabled)
})

const applyDefaultModelsIfEmpty = () => {
  if (!formData.value || editorMode.value !== 'create') return
  const chatModelId = selectInitialModelId(allModels.value, 'KnowledgeQA')
  const embeddingModelId = selectInitialModelId(allModels.value, 'Embedding')
  const vlmModelId = selectInitialModelId(allModels.value, 'VLLM')
  const asrModelId = selectInitialModelId(allModels.value, 'ASR')
  if (!formData.value.modelConfig.llmModelId && chatModelId) formData.value.modelConfig.llmModelId = chatModelId
  if (!formData.value.modelConfig.embeddingModelId && embeddingModelId) formData.value.modelConfig.embeddingModelId = embeddingModelId
  if (!formData.value.multimodalConfig.vllmModelId && vlmModelId) formData.value.multimodalConfig.vllmModelId = vlmModelId
  if (!formData.value.asrConfig.modelId && asrModelId) formData.value.asrConfig.modelId = asrModelId
}

watch(
  () => formData.value?.type,
  (newType, oldType) => {
    if (!formData.value) return
    if (newType === 'faq') {
      if (!formData.value.faqConfig) {
        formData.value.faqConfig = { indexMode: 'question_only', questionIndexMode: 'separate' }
      }
      if (!['basic', 'models', 'faq'].includes(currentSection.value)) currentSection.value = 'faq'
    } else if (oldType === 'faq' && currentSection.value === 'faq') {
      currentSection.value = 'basic'
    }
  },
)

const initFormData = (type: 'document' | 'faq' = 'document') =>
  createInitialKBFormData(type, { mergeImageActions, imagePipelineDefault: IMAGE_PIPELINE_DEFAULT })

const loadAllModels = async (force = false) => {
  try {
    await chatResources.ensureModels(force)
    allModels.value = chatResources.allModels || []
  } catch (error) {
    console.error('Failed to load model list:', error)
    MessagePlugin.error(t('knowledgeEditor.messages.loadModelsFailed'))
    allModels.value = []
  }
}

let kbEditorLoadGeneration = 0

const isCurrentKBLoad = (generation: number, kbId: string) => (
  generation === kbEditorLoadGeneration
  && props.visible
  && activeKbId.value === kbId
)

async function loadImageAttrSchema(kbId: string, generation?: number) {
  try {
    const schema = await fetchImageAttrSchema(kbId)
    if (generation !== undefined && !isCurrentKBLoad(generation, kbId)) return
    imageAttrSchema.value = schema
  } catch {
    if (generation !== undefined && !isCurrentKBLoad(generation, kbId)) return
    imageAttrSchema.value = null
  }
}

const loadKBData = async (kbIdOverride?: string, generation = kbEditorLoadGeneration) => {
  const kbId = kbIdOverride ?? activeKbId.value
  if (editorMode.value !== 'edit' || !kbId) return

  loading.value = true
  try {
    const [kbInfo, filesResult] = await Promise.all([
      getKnowledgeBaseById(kbId),
      listKnowledgeFiles(kbId, { page: 1, page_size: 1 }),
    ])

    if (!isCurrentKBLoad(generation, kbId)) return
    if (!kbInfo || !kbInfo.data) throw new Error(t('knowledgeEditor.messages.notFound'))

    const kb = kbInfo.data as unknown as LoadedKnowledgeBaseRecord
    hasFiles.value = ((filesResult as unknown as { total?: number } | null)?.total ?? 0) > 0
    generatedProfile.value = (kb.generated_profile as KnowledgeBaseProfile | null) || null
    kbCreatorId.value = kb.creator_id || ''
    kbTenantId.value = Number(kb.tenant_id || 0)

    formData.value = mapLoadedKBToFormData(kb, {
      mergeImageActions,
      resolveKbImagePipeline,
      imagePipelineSmartOcr: IMAGE_PIPELINE_SMARTOCR,
    })
    void loadImageAttrSchema(kbId, generation)
    initialStorageProvider.value = formData.value.storageProvider
    initialIndexingStrategy.value = { ...formData.value.indexingStrategy }
  } catch (error) {
    if (!isCurrentKBLoad(generation, kbId)) return
    console.error('Failed to load knowledge base data:', error)
    MessagePlugin.error(t('knowledgeEditor.messages.loadDataFailed'))
    handleClose()
  } finally {
    if (isCurrentKBLoad(generation, kbId)) {
      loading.value = false
      modalShell.markClean()
    }
  }
}

const handleModelConfigUpdate = (config: Partial<KBEditorModelConfig>) => {
  if (formData.value) {
    formData.value.modelConfig = {
      llmModelId: config.llmModelId ?? '',
      embeddingModelId: config.embeddingModelId ?? '',
      wikiSynthesisModelId: config.wikiSynthesisModelId ?? '',
    }
  }
}

const isIndexingLocked = computed(() => editorMode.value === 'edit' && hasFiles.value)

const toggleVectorIndexing = () => {
  if (!formData.value || isIndexingLocked.value) return
  const next = !formData.value.indexingStrategy.vectorEnabled
  formData.value.indexingStrategy.vectorEnabled = next
  formData.value.indexingStrategy.keywordEnabled = next
}

const toggleWikiIndexing = () => {
  if (!formData.value || isIndexingLocked.value) return
  formData.value.indexingStrategy.wikiEnabled = !formData.value.indexingStrategy.wikiEnabled
}

const handleChunkingConfigUpdate = (config: KBEditorChunkingConfig) => {
  if (formData.value) {
    formData.value.chunkingConfig = {
      ...formData.value.chunkingConfig,
      ...config,
    }
    chunkingDirty.value = true
  }
}

const isWikiOnlyStrategy = computed(() => {
  const s = formData.value?.indexingStrategy
  return Boolean(s && s.wikiEnabled && !s.vectorEnabled && !s.keywordEnabled)
})

watch(isWikiOnlyStrategy, (wikiOnly) => {
  if (editorMode.value !== 'create' || !formData.value || chunkingDirty.value) return
  const preset = wikiOnly ? WIKI_ONLY_CHUNKING_PRESET : DEFAULT_CHUNKING_PRESET
  formData.value.chunkingConfig = { ...formData.value.chunkingConfig, ...preset }
})

const handleParserEngineRulesUpdate = (rules: ParserEngineRule[]) => {
  if (formData.value) {
    formData.value.chunkingConfig.parserEngineRules = rules?.length ? rules : undefined
  }
}

const handleMultimodalToggle = () => {
  if (formData.value && !formData.value.multimodalConfig.enabled) {
    formData.value.multimodalConfig.vllmModelId = ''
  }
}

const onPipelineChange = (value: string) => {
  if (!formData.value) return
  formData.value.imagePipeline = value
  formData.value.imageAttrsEnabled = value === IMAGE_PIPELINE_SMARTOCR
}

const imagePipelineInvalid = ref(false)
const onPipelineInvalidChange = (value: boolean) => {
  imagePipelineInvalid.value = value
}

const handleMultimodalVLLMChange = (modelId: string) => {
  if (formData.value) {
    formData.value.multimodalConfig.vllmModelId = modelId
  }
}

const handleAddVLLMModel = () => uiStore.openSettings('models', 'vllm')
const handleAddASRModel = () => uiStore.openSettings('models', 'asr')

const handleStorageProviderUpdate = (value: string) => {
  if (!formData.value) return
  formData.value.storageProvider =
    editorMode.value === 'create'
      ? editorResources.resolveUsableStorageProvider(value || tenantDefaultStorageProvider.value)
      : (value || tenantDefaultStorageProvider.value || 'local')
}

const handleStorageBackendUpdate = (value: string) => {
  if (formData.value) formData.value.storageBackendId = value
}

async function loadTenantDefaultStorageProvider(force = false) {
  try {
    await editorResources.ensureStorageEngine(force)
    tenantDefaultStorageProvider.value = editorResources.resolveUsableStorageProvider(
      editorResources.storageConfig?.default_provider,
    )
  } catch {
    tenantDefaultStorageProvider.value = editorResources.resolveUsableStorageProvider()
  }
}

function resolvedStorageProvider(): string {
  const explicit = formData.value?.storageProvider?.trim()
  if (editorMode.value === 'create') {
    return editorResources.resolveUsableStorageProvider(explicit || tenantDefaultStorageProvider.value)
  }
  return explicit || tenantDefaultStorageProvider.value || 'local'
}

const handleVectorStoreIdUpdate = (id: string) => {
  if (formData.value) formData.value.vectorStoreId = id || ''
}

const handleQuestionGenerationUpdate = (
  config: Partial<KBEditorQuestionGenerationConfig> & Pick<KBEditorQuestionGenerationConfig, 'enabled' | 'questionCount'>,
) => {
  if (formData.value) {
    formData.value.questionGenerationConfig = {
      modelId: config.modelId ?? '',
      enabled: config.enabled,
      questionCount: config.questionCount,
      customInstructions: config.customInstructions ?? '',
    }
  }
}

const handleGenerateProfile = async () => {
  const kbId = activeKbId.value
  if (!kbId || generatingProfile.value) return
  const generation = kbEditorLoadGeneration
  generatingProfile.value = true
  try {
    const result = (await generateKnowledgeBaseProfile(kbId)) as unknown as {
      success?: boolean
      message?: string
      data?: KnowledgeBaseProfile | null
    }
    if (!isCurrentKBLoad(generation, kbId)) return
    if (!result?.success) {
      throw new Error(result?.message || t('knowledgeEditor.basic.profile.generateFailed'))
    }
    generatedProfile.value = result.data || null
    MessagePlugin.success(t('knowledgeEditor.basic.profile.generated'))
  } catch (error: unknown) {
    if (!isCurrentKBLoad(generation, kbId)) return
    console.error('Generate knowledge base profile failed:', error)
    MessagePlugin.error((error as { message?: string } | null)?.message || t('knowledgeEditor.basic.profile.generateFailed'))
  } finally {
    if (isCurrentKBLoad(generation, kbId)) generatingProfile.value = false
  }
}

const handleAdoptProfileGist = () => {
  const gist = generatedProfile.value?.gist
  if (!gist || !formData.value) return
  formData.value.description = gist.slice(0, 200)
  MessagePlugin.success(t('knowledgeEditor.basic.profile.adopted'))
}

const handleNodeExtractUpdate = (
  config: Partial<KBEditorNodeExtractConfig> & Pick<KBEditorNodeExtractConfig, 'enabled' | 'text' | 'tags' | 'nodes' | 'relations'>,
) => {
  if (formData.value) {
    formData.value.nodeExtractConfig = {
      enabled: config.enabled,
      text: config.text,
      tags: config.tags,
      nodes: config.nodes,
      relations: config.relations,
      customInstructions: config.customInstructions ?? '',
    }
  }
}

const validateForm = (): boolean => {
  if (!formData.value) return false
  if (!formData.value.name || !formData.value.name.trim()) {
    MessagePlugin.warning(t('knowledgeEditor.messages.nameRequired'))
    currentSection.value = 'basic'
    return false
  }
  if (formData.value.type !== 'faq') {
    const s = formData.value.indexingStrategy
    if (s && !s.vectorEnabled && !s.keywordEnabled && !s.wikiEnabled && !s.graphEnabled) {
      MessagePlugin.warning(t('knowledgeEditor.indexing.atLeastOne'))
      currentSection.value = 'basic'
      return false
    }
  }
  const needsEmbedding = formData.value.indexingStrategy?.vectorEnabled || formData.value.indexingStrategy?.keywordEnabled
  if (needsEmbedding && !formData.value.modelConfig.embeddingModelId) {
    MessagePlugin.warning(t('knowledgeEditor.indexing.embeddingRequired'))
    currentSection.value = 'models'
    return false
  }
  if (!formData.value.modelConfig.llmModelId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.summaryRequired'))
    currentSection.value = 'models'
    return false
  }
  if (formData.value.multimodalConfig.enabled && !formData.value.multimodalConfig.vllmModelId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.multimodalInvalid'))
    currentSection.value = 'multimodal'
    return false
  }
  if (formData.value.multimodalConfig.enabled && imagePipelineInvalid.value) {
    MessagePlugin.warning(t('imagePipeline.errors.noActionEnabled'))
    currentSection.value = 'multimodal'
    return false
  }
  if (formData.value.type === 'faq' && !formData.value.faqConfig?.indexMode) {
    MessagePlugin.warning(t('knowledgeEditor.messages.indexModeRequired'))
    currentSection.value = 'faq'
    return false
  }
  return true
}

const buildSubmitData = (): KBSubmitPayload | null => {
  if (!formData.value) return null

  const data: KBSubmitPayload = {
    name: formData.value.name,
    description: formData.value.description,
    type: formData.value.type,
    chunking_config: {
      chunk_size: formData.value.chunkingConfig.chunkSize,
      chunk_overlap: formData.value.chunkingConfig.chunkOverlap,
      separators: formData.value.chunkingConfig.separators,
      enable_parent_child: formData.value.chunkingConfig.enableParentChild,
      parent_chunk_size: formData.value.chunkingConfig.parentChunkSize,
      child_chunk_size: formData.value.chunkingConfig.childChunkSize,
      strategy: formData.value.chunkingConfig.strategy ?? '',
      token_limit: formData.value.chunkingConfig.tokenLimit ?? 0,
      languages: formData.value.chunkingConfig.languages ?? [],
      table_metadata_instructions: formData.value.chunkingConfig.tableMetadataInstructions || '',
      ...(formData.value.chunkingConfig.parserEngineRules?.length
        ? { parser_engine_rules: formData.value.chunkingConfig.parserEngineRules }
        : {}),
    },
    embedding_model_id: formData.value.modelConfig.embeddingModelId,
    summary_model_id: formData.value.modelConfig.llmModelId,
  }

  if (formData.value.vectorStoreId) {
    data.vector_store_id = formData.value.vectorStoreId
  }

  data.vlm_config = {
    enabled: formData.value.multimodalConfig.enabled,
    model_id: formData.value.multimodalConfig.enabled
      ? (formData.value.multimodalConfig.vllmModelId || '')
      : '',
    description_language: formData.value.multimodalConfig.descriptionLanguage || '',
    custom_instructions: formData.value.multimodalConfig.customInstructions || '',
  }

  const built = buildImageProcessingConfig(formData.value.imageProcessingConfigSnapshot, {
    imageAttrsEnabled: formData.value.imageAttrsEnabled,
    imageVectorEnabled: formData.value.imageVectorEnabled,
    defaultOn: displaySchema.value.default_actions.ocr.on,
    pipelineId: formData.value.multimodalConfig.enabled ? formData.value.imagePipeline : '',
    pipelineParams: formData.value.imagePipelineParams,
  })
  if (built) {
    data.image_processing_config = built
  }

  data.asr_config = {
    enabled: formData.value.asrConfig?.enabled || false,
    model_id: formData.value.asrConfig?.enabled ? (formData.value.asrConfig?.modelId || '') : '',
    language: formData.value.asrConfig?.language || '',
  }

  if (formData.value.storageBackendId) {
    data.storage_backend_id = formData.value.storageBackendId
  }
  const storageProvider = resolvedStorageProvider()
  data.storage_provider_config = { provider: storageProvider }
  data.storage_config = { provider: storageProvider }

  data.question_generation_config = formData.value.questionGenerationConfig?.enabled
    ? {
        model_id: formData.value.questionGenerationConfig?.modelId || '',
        enabled: true,
        question_count: formData.value.questionGenerationConfig.questionCount || 3,
        custom_instructions: formData.value.questionGenerationConfig.customInstructions || '',
      }
    : {
        model_id: formData.value.questionGenerationConfig?.modelId || '',
        enabled: false,
        question_count: 3,
        custom_instructions: formData.value.questionGenerationConfig?.customInstructions || '',
      }

  data.auto_tag_config = {
    enabled: formData.value.autoTagConfig?.enabled || false,
    model_id: formData.value.autoTagConfig?.modelId || '',
    max_tags: formData.value.autoTagConfig?.maxTags || 3,
    skip_if_tagged: formData.value.autoTagConfig?.skipIfTagged ?? true,
  }

  data.profile_config = {
    enabled: formData.value.profileConfig?.enabled || false,
    model_id: formData.value.profileConfig?.modelId || '',
    custom_instructions: formData.value.profileConfig?.customInstructions || '',
  }

  if (formData.value.type === 'faq') {
    data.faq_config = {
      index_mode: formData.value.faqConfig?.indexMode || 'question_only',
      question_index_mode: formData.value.faqConfig?.questionIndexMode || 'separate',
    }
  }

  if (formData.value.type !== 'faq') {
    data.wiki_config = {
      auto_generate: formData.value.wikiConfig?.autoGenerate ?? true,
      synthesis_model_id: formData.value.modelConfig?.wikiSynthesisModelId || '',
      max_pages_per_ingest: formData.value.wikiConfig?.maxPagesPerIngest || 0,
      extraction_granularity: formData.value.wikiConfig?.extractionGranularity || 'standard',
      content_instructions: formData.value.wikiConfig?.contentInstructions || '',
      extraction_instructions: formData.value.wikiConfig?.extractionInstructions || '',
    }
    data.indexing_strategy = {
      vector_enabled: formData.value.indexingStrategy?.vectorEnabled ?? true,
      keyword_enabled: formData.value.indexingStrategy?.keywordEnabled ?? true,
      wiki_enabled: formData.value.indexingStrategy?.wikiEnabled ?? false,
      graph_enabled: formData.value.indexingStrategy?.graphEnabled ?? false,
    }
  }

  if (formData.value.nodeExtractConfig) {
    data.extract_config = {
      enabled: !!formData.value.nodeExtractConfig.enabled,
      text: formData.value.nodeExtractConfig.text || '',
      tags: formData.value.nodeExtractConfig.tags || [],
      nodes: formData.value.nodeExtractConfig.nodes || [],
      relations: formData.value.nodeExtractConfig.relations || [],
      custom_instructions: formData.value.nodeExtractConfig.customInstructions || '',
    }
  }

  return data
}

const handleSubmit = async () => {
  if (!validateForm()) return

  if (
    editorMode.value === 'edit' &&
    hasFiles.value &&
    formData.value &&
    initialStorageProvider.value &&
    formData.value.storageProvider !== initialStorageProvider.value
  ) {
    const dialog = DialogPlugin.confirm({
      header: t('common.confirm'),
      body: t('knowledgeEditor.messages.storageChangeConfirm'),
      confirmBtn: t('common.confirm'),
      cancelBtn: t('common.cancel'),
      onConfirm: () => {
        dialog.destroy()
        void doSubmit()
      },
      onCancel: () => {
        dialog.destroy()
      },
    })
    return
  }

  await doSubmit()
}

const doSubmit = async () => {
  saving.value = true
  try {
    const data = buildSubmitData()
    if (!data || !formData.value) {
      throw new Error(t('knowledgeEditor.messages.buildDataFailed'))
    }

    if (editorMode.value === 'create') {
      const result = (await createKnowledgeBase(data)) as unknown as {
        success?: boolean
        message?: string
        data?: { id?: string }
      }
      if (!result.success || !result.data?.id) {
        throw new Error(result.message || t('knowledgeEditor.messages.createFailed'))
      }
      const createdKbId = result.data.id
      savedKbId.value = createdKbId
      currentSection.value = 'basic'
      await loadKBData(createdKbId)
      MessagePlugin.success(t('knowledgeEditor.messages.createSuccess'))
      markContextualGuideDone('kbCreate')
      emit('success', createdKbId)
    } else {
      // 编辑模式：分别更新基本信息和配置
      const kbId = activeKbId.value
      if (!kbId) throw new Error(t('knowledgeEditor.messages.missingId'))

      const updateConfig: {
        faq_config?: KBSubmitPayload['faq_config']
        wiki_config?: KBSubmitPayload['wiki_config']
        auto_tag_config?: KBSubmitPayload['auto_tag_config']
        profile_config?: KBSubmitPayload['profile_config']
        indexing_strategy?: KBSubmitPayload['indexing_strategy']
        image_processing_config?: ImageProcessingConfig
      } = {}
      if (formData.value.type === 'faq' && formData.value.faqConfig) {
        updateConfig.faq_config = {
          index_mode: formData.value.faqConfig.indexMode || 'question_only',
          question_index_mode: formData.value.faqConfig.questionIndexMode || 'separate',
        }
      }
      if (formData.value.wikiConfig && formData.value.type !== 'faq') {
        updateConfig.wiki_config = {
          auto_generate: formData.value.wikiConfig.autoGenerate ?? true,
          synthesis_model_id: formData.value.modelConfig?.wikiSynthesisModelId || '',
          max_pages_per_ingest: formData.value.wikiConfig.maxPagesPerIngest || 0,
          extraction_granularity: formData.value.wikiConfig.extractionGranularity || 'standard',
          content_instructions: formData.value.wikiConfig.contentInstructions || '',
          extraction_instructions: formData.value.wikiConfig.extractionInstructions || '',
        }
      }
      if (formData.value.type !== 'faq') {
        updateConfig.auto_tag_config = data.auto_tag_config
        updateConfig.profile_config = data.profile_config
        updateConfig.indexing_strategy = {
          vector_enabled: formData.value.indexingStrategy?.vectorEnabled ?? true,
          keyword_enabled: formData.value.indexingStrategy?.keywordEnabled ?? true,
          wiki_enabled: formData.value.indexingStrategy?.wikiEnabled ?? false,
          graph_enabled: formData.value.indexingStrategy?.graphEnabled ?? false,
        }
      }
      if (data.image_processing_config) {
        updateConfig.image_processing_config = data.image_processing_config
      }
      await updateKnowledgeBase(kbId, {
        name: data.name,
        description: data.description,
        config: updateConfig,
      })

      const config = buildKBModelConfigRequest(data, formData.value)
      await updateKBConfig(kbId, config)
      MessagePlugin.success(t('knowledgeEditor.messages.updateSuccess'))

      if (hasFiles.value && initialIndexingStrategy.value && formData.value) {
        const curr = formData.value.indexingStrategy
        const prev = initialIndexingStrategy.value
        const strategyChanged = (
          curr.vectorEnabled !== prev.vectorEnabled ||
          curr.keywordEnabled !== prev.keywordEnabled ||
          curr.wikiEnabled !== prev.wikiEnabled ||
          curr.graphEnabled !== prev.graphEnabled
        )
        if (strategyChanged) {
          const dialog = DialogPlugin.confirm({
            header: t('knowledgeEditor.indexing.rebuildConfirmTitle'),
            body: t('knowledgeEditor.indexing.rebuildConfirmBody', { count: '...' }),
            confirmBtn: t('common.confirm'),
            cancelBtn: t('common.cancel'),
            onConfirm: async () => {
              dialog.destroy()
              try {
                const result = (await rebuildKBIndex(kbId)) as unknown as { data?: { document_count?: number } }
                const count = result?.data?.document_count ?? 0
                MessagePlugin.success(t('knowledgeEditor.indexing.rebuildSuccess', { count }))
              } catch (e) {
                console.error('Rebuild index failed:', e)
              }
            },
            onCancel: () => {
              dialog.destroy()
              MessagePlugin.info(t('knowledgeEditor.indexing.rebuildSkip'))
            },
          })
        }
      }

      emit('success', kbId)
      handleClose()
    }
  } catch (error: unknown) {
    console.error('Knowledge base operation failed:', error)
    const errObj = error as {
      code?: number
      message?: string
      response?: { data?: { error?: { code?: number } } }
    } | null
    const code = errObj?.response?.data?.error?.code ?? errObj?.code
    if (code === 2200) {
      MessagePlugin.error(t('knowledgeEditor.errors.vectorStoreBindingInvalid'))
      currentSection.value = 'vectorStore'
    } else if (code === 2201) {
      MessagePlugin.error(t('knowledgeEditor.errors.vectorStoreUnavailable'))
      currentSection.value = 'vectorStore'
    } else {
      MessagePlugin.error(errObj?.message || t('common.operationFailed'))
    }
  } finally {
    saving.value = false
  }
}

const resetState = () => {
  savedKbId.value = null
  currentSection.value = 'basic'
  formData.value = null
  hasFiles.value = false
  generatedProfile.value = null
  generatingProfile.value = false
  imageAttrSchema.value = null
  dsCount.value = 0
  initialStorageProvider.value = ''
  tenantDefaultStorageProvider.value = 'local'
  initialIndexingStrategy.value = null
  saving.value = false
  loading.value = false
  chunkingDirty.value = false
  kbCreatorId.value = ''
  kbTenantId.value = 0
}

const handleClose = () => {
  emit('update:visible', false)
  setTimeout(() => {
    if (props.visible) return
    resetState()
  }, 300)
}

const modalShell = useModalShell({
  visible: () => props.visible,
  close: handleClose,
  snapshot: () => formData.value,
})

watch(() => props.visible, async (newVal) => {
  const generation = ++kbEditorLoadGeneration
  if (newVal) {
    resetState()
    modalShell.markClean()
    loading.value = true
    const targetKbId = props.kbId
    if (uiStore.kbEditorInitialSection) {
      currentSection.value = uiStore.kbEditorInitialSection
    }
    await Promise.all([loadAllModels(), loadTenantDefaultStorageProvider()])
    if (generation !== kbEditorLoadGeneration || !props.visible) return

    if (props.mode === 'edit' && targetKbId) {
      await loadKBData(targetKbId, generation)
    } else {
      formData.value = initFormData(props.initialType || 'document')
      formData.value.storageProvider = tenantDefaultStorageProvider.value
      hasFiles.value = false
      applyDefaultModelsIfEmpty()
      loading.value = false
      modalShell.markClean()
    }
  } else {
    setTimeout(() => {
      if (props.visible) return
      resetState()
      currentSection.value = 'basic'
    }, 300)
  }
})

watch(
  () => uiStore.showSettingsModal,
  async (visible, previous) => {
    if (!visible && previous && props.visible) {
      await loadAllModels(true)
    }
  },
)

watch(() => chatResources.allModels, (list) => {
  if (props.visible) {
    allModels.value = list || []
    applyDefaultModelsIfEmpty()
  }
})
</script>

<style scoped lang="less">
@import './knowledge-base-editor-modal.less';
</style>
