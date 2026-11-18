<template>
  <SettingDrawer
    :visible="dialogVisible"
    :title="isEdit ? $t('model.editor.editTitle') : $t('model.editor.addTitle')"
    :z-index="2300"
    :description="getModalDescription()"
    :icon="modelTypeIcon"
    :confirm-loading="saving"
    :cancel-disabled="saving"
    :confirm-text="$t('model.editor.saveAndClose')"
    :close-on-overlay-click="!saving"
    :close-on-esc-keydown="!saving"
    @update:visible="(v: boolean) => dialogVisible = v"
    @confirm="handleConfirm"
    @cancel="handleCancel"
  >
    <template v-if="formData.source === 'remote'" #footer-left>
      <t-button
        variant="outline"
        :loading="checking"
        :disabled="saving || !formData.modelName || !formData.baseUrl"
        @click="checkRemoteAPI"
      >
        <template #icon>
          <t-icon
            v-if="!checking && remoteChecked && remoteAvailable"
            name="check-circle-filled"
            class="status-icon available"
          />
          <t-icon
            v-else-if="!checking && remoteChecked && !remoteAvailable"
            name="close-circle-filled"
            class="status-icon unavailable"
          />
        </template>
        {{ checking ? $t('model.editor.testing') : $t('model.editor.testConnection') }}
      </t-button>
      <span v-if="remoteChecked" :class="['connection-status', remoteAvailable ? 'success' : 'error']">
        {{ remoteAvailable ? $t('model.editor.connectionSuccess') : $t('model.editor.connectionFailed') }}
      </span>
    </template>

    <template #footer-extra>
      <div v-if="saveError" class="connection-result error" role="alert">
        <strong>{{ $t('modelSettings.toasts.saveFailed') }}</strong>
        <div class="connection-result__details" tabindex="0">{{ saveError }}</div>
      </div>
      <div v-if="formData.source === 'remote'" class="connection-feedback" aria-live="polite">
        <p class="connection-hint">{{ $t(isEdit ? 'model.editor.testDraftEditHint' : 'model.editor.testDraftHint') }}</p>
        <p v-if="remoteStale" class="connection-hint">{{ $t('model.editor.testStale') }}</p>
        <div v-if="remoteChecked && !remoteAvailable" class="connection-result error">
          <div class="connection-result__header">
            <strong>{{ $t('model.editor.connectionFailed') }}</strong>
            <div class="connection-result__actions">
              <AssistantActionButton
                :label="$t('assistant.actions.diagnose')"
                :query="$t('assistant.prompts.modelFailure')"
                :disabled="remoteStale || checking || saving"
                :selection="{ field: $t('model.editor.connectionFailed'), text: remoteMessage }"
              />
              <t-button size="small" variant="text" @click="copyWithToast(remoteMessage, 'common.copied')">
                {{ $t('common.copy') }}
              </t-button>
            </div>
          </div>
          <div class="connection-result__details" tabindex="0">{{ remoteMessage }}</div>
        </div>
      </div>
    </template>

    <t-form ref="formRef" :inert="saving || undefined" :data="formData" :rules="rules" layout="vertical">
      <section v-if="!isEdit" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionType') }}</h4>
        <div class="model-type-options" role="radiogroup" :aria-label="$t('model.editor.typeLabel')">
          <button
            v-for="opt in modelTypeChoices"
            :key="opt.value"
            type="button"
            class="model-type-option"
            :class="{ 'is-active': activeModelType === opt.value }"
            role="radio"
            :aria-checked="activeModelType === opt.value"
            @click="selectModelType(opt.value)"
          >
            <t-icon :name="opt.icon" class="model-type-option__icon" />
            <span class="model-type-option__label">{{ opt.label }}</span>
          </button>
        </div>
      </section>

      <ModelOllamaDownloadSection
        v-model:source="formData.source"
        v-model:model-name="formData.modelName"
        :active-model-type="activeModelType"
        :ollama-service-status="ollamaServiceStatus"
        :loading-ollama-models="loadingOllamaModels"
        :downloading="downloading"
        :download-progress="downloadProgress"
        :search-keyword="searchKeyword"
        :filtered-ollama-models="filteredOllamaModels"
        :show-download-option="showDownloadOption"
        :handle-model-filter="handleModelFilter"
        :load-ollama-models="loadOllamaModels"
        :refresh-ollama-models="refreshOllamaModels"
        :handle-dropdown-visible-change="handleDropdownVisibleChange"
        @open-ollama-settings="goToOllamaSettings"
      />

      <template v-if="formData.source === 'remote'">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionProvider') }}</h4>

          <ModelProviderCatalogSection
            v-model:provider="formData.provider"
            v-model:model-name="formData.modelName"
            v-model:display-name="formData.displayName"
            :loading-providers="loadingProviders"
            :provider-options="providerOptions"
            :selected-provider-icon="selectedProviderIcon"
            :selected-provider-display-label="selectedProviderDisplayLabel"
            :provider-display-label="providerDisplayLabel"
            :provider-display-description="providerDisplayDescription"
            :provider-display-icon="providerDisplayIcon"
            :vendor-doc-link="vendorDocLink"
            :catalog-model-options="catalogModelOptions"
            :model-name-placeholder="getModelNamePlaceholder()"
            :handle-provider-change="handleProviderChange"
            :handle-catalog-model-create="handleCatalogModelCreate"
            :handle-catalog-model-change="handleCatalogModelChange"
          />

          <ModelConnectionAuthSection
            v-model:base-url="formData.baseUrl"
            v-model:api-key="formData.apiKey"
            v-model:app-secret="formData.appSecret"
            :is-edit="isEdit"
            :model-id="props.modelData?.id"
            :model-name="formData.modelName"
            :base-url-placeholder="getBaseUrlPlaceholder()"
            :api-key-label="apiKeyLabel"
            :api-key-required="apiKeyRequired"
            :api-key-placeholder="apiKeyPlaceholder"
            :api-key-hint="apiKeyHint"
            :credential-api="credentialApi"
            :credential-fields="credentialFields"
            :credential-meta="credentialMeta"
            :secret-extra-field="secretExtraField"
            :plain-extra-fields="plainExtraFields"
            :extra-config="formData.extraConfig"
            :extra-config-bool="extraConfigBool"
            :set-extra-config="setExtraConfig"
            :extra-field-display-label="extraFieldDisplayLabel"
            :extra-field-display-placeholder="extraFieldDisplayPlaceholder"
            :extra-field-display-option-label="extraFieldDisplayOptionLabel"
            :custom-headers="formData.customHeaders"
            :add-custom-header="addCustomHeader"
            :remove-custom-header="removeCustomHeader"
            :show-resolved-panel="showResolvedPanel"
            :resolving="resolving"
            :resolved="resolved"
            :resolve-failed="resolveFailed"
            :resolve-error="resolveError"
            :is-chat-like="isChatLike"
            :resolved-thinking-levels="resolvedThinkingLevels"
            @invalidate-connection-test="invalidateConnectionTest()"
          />
        </section>
      </template>

      <ModelExtraConfigFields
        v-model:dimension="formData.dimension"
        v-model:supports-dimension-override="formData.supportsDimensionOverride"
        v-model:embedding-accepts-images="embeddingAcceptsImages"
        v-model:context-window="formData.contextWindow"
        v-model:supports-vision="formData.supportsVision"
        v-model:max-output-tokens="formData.maxOutputTokens"
        v-model:max-concurrency="formData.maxConcurrency"
        v-model:advanced-open="advancedOpen"
        v-model:thinking-control="formData.thinkingControl"
        v-model:spec-compat="formData.specCompat"
        :active-model-type="activeModelType"
        :source="formData.source"
        :model-name="formData.modelName"
        :checking="checking"
        :dimension-checked="dimensionChecked"
        :dimension-success="dimensionSuccess"
        :dimension-message="dimensionMessage"
        :check-ollama-dimension="checkOllamaDimension"
        :is-chat-like="isChatLike"
        :extra-config="formData.extraConfig"
        :set-extra-config="setExtraConfig"
        :show-legacy-thinking-control="showLegacyThinkingControl"
        :spec-compat-error="specCompatError"
      />
    </t-form>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  checkASRModel,
  checkRemoteModel,
  checkRerankModel,
  testEmbeddingModel,
} from '@/api/initialization'
import {
  deleteModelCredentialField,
  putModelCredentials,
  type CredentialFieldMeta,
  type ModelCredentialField,
} from '@/api/model'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'
import { useModelProvidersStore } from '@/stores/modelProviders'
import { copyWithToast } from '@/utils/clipboard'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import AssistantActionButton from '@/components/assistant/AssistantActionButton.vue'
import type { CredentialFieldDef, CredentialResourceApi } from '@/components/credentials/CredentialResource.vue'
import ModelConnectionAuthSection from './model-editor/ModelConnectionAuthSection.vue'
import ModelExtraConfigFields from './model-editor/ModelExtraConfigFields.vue'
import ModelOllamaDownloadSection from './model-editor/ModelOllamaDownloadSection.vue'
import ModelProviderCatalogSection from './model-editor/ModelProviderCatalogSection.vue'
import {
  RESERVED_EXTRA_CONFIG_KEYS,
  type EditorModelType,
  type ModelFormData,
} from './model-editor/types'
import { useModelCatalogResolver } from './model-editor/useModelCatalogResolver'
import { useOllamaModelManager } from './model-editor/useOllamaModelManager'

interface FormInstanceLike {
  validate?: () => Promise<boolean | Record<string, unknown>>
  clearValidate?: () => void
}

interface RemoteCheckResult {
  available?: boolean
  message?: string
  dimension?: number
}

interface Props {
  visible: boolean
  modelType: EditorModelType
  modelData?: ModelFormData | null
  draft?: Partial<ModelFormData> | null
  saveModel: (data: ModelFormData & { modelType?: EditorModelType }) => Promise<void>
}

function extractErrorMessage(error: unknown): string {
  if (typeof error === 'string') return error
  if (error && typeof error === 'object' && 'message' in error) {
    const msg = (error as { message?: unknown }).message
    if (typeof msg === 'string') return msg
  }
  return ''
}

const { t, locale } = useI18n()
const uiStore = useUIStore()
const providersStore = useModelProvidersStore()

const props = withDefaults(defineProps<Props>(), {
  visible: false,
  modelData: null,
  draft: null,
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const draftModelType = ref<EditorModelType>(props.modelType)
const isEdit = computed(() => !!props.modelData)
const activeModelType = computed(() => (isEdit.value ? props.modelType : draftModelType.value))

const modelTypeChoices = computed(() => ([
  { value: 'chat' as const, label: t('modelSettings.typeShort.chat'), icon: 'chat' },
  { value: 'embedding' as const, label: t('modelSettings.typeShort.embedding'), icon: 'chart-bubble' },
  { value: 'rerank' as const, label: t('modelSettings.typeShort.rerank'), icon: 'filter-sort' },
  { value: 'vllm' as const, label: t('modelSettings.typeShort.vllm'), icon: 'image' },
  { value: 'asr' as const, label: t('modelSettings.typeShort.asr'), icon: 'sound' },
]))

const currentLocale = computed(() => String(locale.value || ''))

const formRef = ref<FormInstanceLike | null>(null)
const saving = ref(false)
const saveError = ref('')
const advancedOpen = ref(false)
const legacyThinkingControlLoaded = ref(false)
const hydratingForm = ref(false)

const modelChecked = ref(false)
const modelAvailable = ref(false)
const checking = ref(false)
const remoteChecked = ref(false)
const remoteAvailable = ref(false)
const remoteMessage = ref('')
const remoteStale = ref(false)
const dimensionChecked = ref(false)
const dimensionSuccess = ref(false)
const dimensionMessage = ref('')
let connectionRevision = 0
let applyingDetectedDimension = false

const formData = ref<ModelFormData>({
  id: '',
  name: '',
  source: 'remote',
  provider: 'generic',
  modelName: '',
  displayName: '',
  baseUrl: '',
  apiKey: '',
  dimension: undefined,
  supportsDimensionOverride: false,
  interfaceType: 'ollama',
  isDefault: false,
  supportsVision: false,
  contextWindow: undefined,
  maxConcurrency: undefined,
  maxOutputTokens: undefined,
  thinkingControl: '',
  extraConfig: {},
  specCompat: '',
  spec: null,
  customHeaders: [],
  appSecret: '',
})

const {
  loadingProviders,
  loadProviders,
  providerOptions,
  selectedProvider,
  providerDisplayLabel,
  providerDisplayDescription,
  providerDisplayIcon,
  selectedProviderIcon,
  selectedProviderDisplayLabel,
  extraFieldDisplayLabel,
  extraFieldDisplayPlaceholder,
  extraFieldDisplayOptionLabel,
  apiKeyLabel,
  apiKeyRequired,
  apiKeyHint,
  apiKeyPlaceholder,
  secretExtraField,
  plainExtraFields,
  keepVendorNeutralExtraConfig,
  setExtraConfig,
  extraConfigBool,
  applyExtraFieldDefaults,
  catalogEntries,
  catalogModelOptions,
  vendorDocLink,
  catalogFilled,
  embeddingAcceptsImages,
  handleCatalogModelCreate,
  handleCatalogModelChange,
  resetModelSelectionForVendor,
  resolved,
  resolving,
  resolveFailed,
  resolveError,
  isChatLike,
  showResolvedPanel,
  resolvedThinkingLevels,
  specCompatError,
  buildSpec,
  runResolve,
  scheduleResolve,
  cancelResolve,
} = useModelCatalogResolver({
  t: (key, params) => (params ? t(key, params) : t(key)),
  currentLocale,
  visible: () => props.visible,
  isEdit,
  activeModelType,
  formData,
  providersStore,
  onVendorResetChecks: () => {
    modelChecked.value = false
    modelAvailable.value = false
    dimensionChecked.value = false
    dimensionSuccess.value = false
    dimensionMessage.value = ''
  },
})

const {
  loadingOllamaModels,
  searchKeyword,
  downloading,
  downloadProgress,
  ollamaServiceStatus,
  filteredOllamaModels,
  showDownloadOption,
  handleModelFilter,
  loadOllamaModels,
  refreshOllamaModels,
  handleDropdownVisibleChange,
  checkOllamaServiceStatus,
  stopDownloadPolling,
  startDownload,
} = useOllamaModelManager({
  t: (key, params) => (params ? t(key, params) : t(key)),
  visible: () => props.visible,
  isEdit,
  formData,
})

const showLegacyThinkingControl = computed(
  () => activeModelType.value === 'chat' && !!(formData.value.thinkingControl || legacyThinkingControlLoaded.value),
)

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => {
    if (!saving.value) emit('update:visible', val)
  },
})

const modelTypeIcon = computed(() => {
  const map: Record<string, string> = {
    chat: 'chat',
    embedding: 'chart-bubble',
    rerank: 'filter-sort',
    vllm: 'image',
    asr: 'sound',
  }
  return map[activeModelType.value] || 'setting'
})

const credentialFields = computed<CredentialFieldDef<ModelCredentialField>[]>(() => {
  const fields: CredentialFieldDef<ModelCredentialField>[] = [
    { key: 'api_key', label: String(t('model.editor.apiKeyOptional')) },
  ]
  if (secretExtraField.value) {
    fields.push({ key: 'app_secret', label: extraFieldDisplayLabel(secretExtraField.value) })
  }
  return fields
})

const credentialApi = computed<CredentialResourceApi<ModelCredentialField>>(() => {
  const id = props.modelData?.id ?? ''
  return {
    save: async (patch) => {
      const meta = await putModelCredentials(id, patch)
      return meta.fields
    },
    remove: async (field) => {
      await deleteModelCredentialField(id, field)
    },
  }
})

const credentialMeta = computed<Partial<Record<ModelCredentialField, CredentialFieldMeta>>>(
  () => props.modelData?.credentials ?? {
    api_key: { configured: false },
    app_secret: { configured: false },
  },
)

const handleSaveEscape = (event: KeyboardEvent) => {
  if (props.visible && saving.value && (event.key === 'Escape' || event.code === 'Escape')) {
    event.preventDefault()
    event.stopImmediatePropagation()
  }
}
watch(
  () => props.visible && saving.value,
  (locked) => {
    if (locked) window.addEventListener('keydown', handleSaveEscape, true)
    else window.removeEventListener('keydown', handleSaveEscape, true)
  },
  { flush: 'sync' },
)

const invalidateConnectionTest = (showStale = true) => {
  connectionRevision++
  remoteStale.value = showStale && (remoteStale.value || checking.value || remoteChecked.value)
  checking.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
}

const applyDetectedDimension = (dimension: number) => {
  applyingDetectedDimension = true
  try {
    formData.value.dimension = dimension
  } finally {
    applyingDetectedDimension = false
  }
}

const rules = computed(() => ({
  modelName: [
    { required: true, message: t('model.editor.validation.modelNameRequired') },
    {
      validator: (val: string) => {
        if (!val || !val.trim()) {
          return { result: false, message: t('model.editor.validation.modelNameEmpty') }
        }
        if (val.trim().length > 100) {
          return { result: false, message: t('model.editor.validation.modelNameMax') }
        }
        return { result: true }
      },
      trigger: 'blur',
    },
  ],
  baseUrl: [
    {
      required: true,
      message: t('model.editor.validation.baseUrlRequired'),
      trigger: 'blur',
    },
    {
      validator: (val: string) => {
        if (!val || !val.trim()) {
          return { result: false, message: t('model.editor.validation.baseUrlEmpty') }
        }
        try {
          new URL(val.trim())
          return { result: true }
        } catch {
          return { result: false, message: t('model.editor.validation.baseUrlInvalid') }
        }
      },
      trigger: 'blur',
    },
  ],
}))

watch(
  () => [
    props.visible, formData.value.source, formData.value.provider, formData.value.modelName,
    formData.value.baseUrl, formData.value.extraConfig?.api, formData.value.extraConfig?.remote_model_name,
    formData.value.thinkingControl, activeModelType.value,
    formData.value.specCompat, JSON.stringify(formData.value.spec),
  ],
  () => {
    if (!props.visible) return
    scheduleResolve()
  },
)

const getModalDescription = () => {
  const key = `model.editor.description.${activeModelType.value}` as const
  return t(key) || t('model.editor.description.default')
}

const getModelNamePlaceholder = () => {
  const catalogModel = catalogEntries.value[0]
  if (formData.value.source === 'remote' && catalogModel) return catalogModel.id
  if (activeModelType.value === 'vllm') {
    return formData.value.source === 'local'
      ? t('model.editor.modelNamePlaceholder.localVllm')
      : t('model.editor.modelNamePlaceholder.remoteVllm')
  }
  if (activeModelType.value === 'asr') {
    return t('model.editor.modelNamePlaceholder.remoteAsr')
  }
  return formData.value.source === 'local'
    ? t('model.editor.modelNamePlaceholder.local')
    : t('model.editor.modelNamePlaceholder.remote')
}

const getBaseUrlPlaceholder = () => {
  if (activeModelType.value === 'vllm') {
    return t('model.editor.baseUrlPlaceholderVllm')
  }
  if (activeModelType.value === 'asr') {
    return t('model.editor.baseUrlPlaceholderAsr')
  }
  return t('model.editor.baseUrlPlaceholder')
}

const goToOllamaSettings = async () => {
  emit('update:visible', false)
  if (uiStore.showSettingsModal) {
    uiStore.closeSettings()
    await nextTick()
  }
  uiStore.openSettings('ollama')
}

const lastOpenedModelId = ref<string | null>(null)

const generateId = () => `model_${Date.now()}_${Math.random().toString(36).slice(2, 11)}`

const resetForm = () => {
  stopDownloadPolling()
  legacyThinkingControlLoaded.value = false
  formData.value = {
    id: generateId(),
    name: '',
    source: 'remote',
    provider: 'generic',
    modelName: '',
    displayName: '',
    baseUrl: '',
    apiKey: '',
    dimension: undefined,
    supportsDimensionOverride: false,
    interfaceType: undefined,
    isDefault: false,
    supportsVision: false,
    contextWindow: undefined,
    maxConcurrency: undefined,
    maxOutputTokens: undefined,
    thinkingControl: '',
    extraConfig: {},
    specCompat: '',
    spec: null,
    customHeaders: [],
    appSecret: '',
  }
  catalogFilled.value = {}
  advancedOpen.value = false
  saveError.value = ''
  searchKeyword.value = ''
  invalidateConnectionTest(false)
  cancelResolve()
  modelChecked.value = false
  modelAvailable.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
  void nextTick(() => formRef.value?.clearValidate?.())
}

const selectModelType = async (type: EditorModelType) => {
  if (saving.value || isEdit.value || draftModelType.value === type) return
  draftModelType.value = type
  resetForm()
  const draft = formData.value
  await loadProviders()
  if (props.visible && !isEdit.value && formData.value === draft && activeModelType.value === type) {
    applyExtraFieldDefaults()
  }
}

const initializeVisibleForm = (val: boolean) => {
  if (!val) return
  const typeChanged = draftModelType.value !== props.modelType
  draftModelType.value = props.modelType

  void checkOllamaServiceStatus()
  advancedOpen.value = false
  modelChecked.value = false
  modelAvailable.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''

  const currentId = props.modelData?.id ?? null

  hydratingForm.value = true
  try {
    if (props.modelData) {
      const loadedExtra: Record<string, string> = {}
      for (const [key, value] of Object.entries(props.modelData.extraConfig || {})) {
        if (RESERVED_EXTRA_CONFIG_KEYS.has(key)) continue
        if (value != null && String(value) !== '') loadedExtra[key] = String(value)
      }
      const loadedSpec = props.modelData.spec || null
      formData.value = {
        ...props.modelData,
        apiKey: '',
        appSecret: '',
        extraConfig: loadedExtra,
        thinkingControl: props.modelData.thinkingControl || '',
        spec: loadedSpec,
        specCompat: props.modelData.specCompat
          ?? (loadedSpec?.compat ? JSON.stringify(loadedSpec.compat, null, 2) : ''),
        customHeaders: Array.isArray(props.modelData.customHeaders)
          ? props.modelData.customHeaders.map((h) => ({ key: h.key, value: h.value }))
          : [],
      }
      legacyThinkingControlLoaded.value = !!props.modelData.thinkingControl
    } else if (props.draft) {
      resetForm()
      Object.assign(formData.value, props.draft)
    } else if (typeChanged || lastOpenedModelId.value !== null || !formData.value.id) {
      resetForm()
    }

    lastOpenedModelId.value = currentId
    if (activeModelType.value === 'rerank') {
      formData.value.source = 'remote'
    }
  } finally {
    void nextTick(() => {
      hydratingForm.value = false
    })
  }

  const draft = formData.value
  const type = activeModelType.value
  void loadProviders(true).then(() => {
    if (props.visible && !isEdit.value && formData.value === draft && activeModelType.value === type) {
      applyExtraFieldDefaults()
    }
  })
}

watch(() => props.visible, initializeVisibleForm)
onMounted(() => initializeVisibleForm(props.visible))

watch(
  () => [
    activeModelType.value, props.modelData?.id, formData.value.source,
    formData.value.provider, formData.value.modelName, formData.value.baseUrl,
    formData.value.apiKey, formData.value.appSecret, formData.value.customHeaders,
    formData.value.dimension, formData.value.supportsDimensionOverride,
    formData.value.extraConfig, formData.value.thinkingControl,
    formData.value.specCompat, formData.value.spec,
  ],
  () => {
    if (!applyingDetectedDimension) invalidateConnectionTest(props.visible && !hydratingForm.value)
  },
  { deep: true, flush: 'sync' },
)

watch(
  () => props.visible,
  (open) => {
    if (!open) stopDownloadPolling()
    invalidateConnectionTest(false)
    saveError.value = ''
  },
  { flush: 'sync' },
)

const handleProviderChange = (value: string) => {
  const provider = providerOptions.value.find((opt) => opt.value === value)
  formData.value.baseUrl = provider?.default_urls?.[activeModelType.value]
    ?? provider?.default_urls?.chat
    ?? ''
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  if (hydratingForm.value) return
  resetModelSelectionForVendor()
  formData.value.extraConfig = keepVendorNeutralExtraConfig()
  formData.value.appSecret = ''
  applyExtraFieldDefaults()
}

const addCustomHeader = () => {
  if (!Array.isArray(formData.value.customHeaders)) {
    formData.value.customHeaders = []
  }
  formData.value.customHeaders.push({ key: '', value: '' })
}

const removeCustomHeader = (idx: number) => {
  if (!Array.isArray(formData.value.customHeaders)) return
  formData.value.customHeaders.splice(idx, 1)
}

const checkOllamaDimension = async () => {
  if (checking.value || saving.value) return
  if (!formData.value.modelName || formData.value.source !== 'local' || activeModelType.value !== 'embedding') {
    return
  }

  const revision = ++connectionRevision
  checking.value = true
  dimensionChecked.value = false
  dimensionMessage.value = ''

  try {
    const result = await testEmbeddingModel({
      source: 'local',
      modelName: formData.value.modelName,
      dimension: formData.value.dimension,
      supportsDimensionOverride: formData.value.supportsDimensionOverride ?? false,
    })

    if (revision !== connectionRevision) return
    dimensionChecked.value = true
    dimensionSuccess.value = result.available || false

    if (result.available && result.dimension) {
      applyDetectedDimension(result.dimension)
      dimensionMessage.value = t('model.editor.dimensionDetected', { value: result.dimension })
      MessagePlugin.success(dimensionMessage.value)
    } else {
      if (result.message) {
        console.debug('Backend dimension message:', result.message)
      }
      dimensionMessage.value = t('model.editor.dimensionFailed')
      MessagePlugin.warning(dimensionMessage.value)
    }
  } catch (error: unknown) {
    if (revision !== connectionRevision) return
    console.error('Ollama dimension check failed:', error)
    dimensionChecked.value = true
    dimensionSuccess.value = false
    dimensionMessage.value = t('model.editor.dimensionFailed')
    MessagePlugin.error(dimensionMessage.value)
  } finally {
    if (revision === connectionRevision) checking.value = false
  }
}

const buildExtraConfig = (): Record<string, string> => {
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(formData.value.extraConfig || {})) {
    const trimmed = (value ?? '').toString().trim()
    if (key && trimmed) out[key] = trimmed
  }
  const legacy = (formData.value.thinkingControl || '').trim()
  if (legacy && activeModelType.value === 'chat' && formData.value.source === 'remote') {
    out.thinking_control = legacy
  }
  return out
}

const checkRemoteAPI = async () => {
  if (checking.value || saving.value) return
  if (!formData.value.modelName || !formData.value.baseUrl) {
    MessagePlugin.warning(t('model.editor.fillModelAndUrl'))
    return
  }

  const revision = ++connectionRevision
  checking.value = true
  remoteStale.value = false
  remoteChecked.value = false
  remoteMessage.value = ''

  try {
    let result: RemoteCheckResult

    const customHeaders: Record<string, string> = {}
    if (Array.isArray(formData.value.customHeaders)) {
      for (const item of formData.value.customHeaders) {
        const key = (item?.key ?? '').trim()
        const value = (item?.value ?? '').trim()
        if (key && value) customHeaders[key] = value
      }
    }
    const headerPayload = Object.keys(customHeaders).length > 0 ? { customHeaders } : {}
    const idPayload = isEdit.value && props.modelData?.id
      ? { modelId: props.modelData.id }
      : {}

    const extraConfig = buildExtraConfig()
    const extraPayload = {
      spec: buildSpec(),
      ...(Object.keys(extraConfig).length > 0 ? { extraConfig } : {}),
      ...(formData.value.appSecret?.trim() ? { appSecret: formData.value.appSecret.trim() } : {}),
    }

    switch (activeModelType.value) {
      case 'chat':
        result = await checkRemoteModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          ...extraPayload,
        })
        break

      case 'embedding':
        result = await testEmbeddingModel({
          source: 'remote',
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          dimension: formData.value.dimension,
          supportsDimensionOverride: formData.value.supportsDimensionOverride ?? false,
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          ...extraPayload,
        })
        if (revision !== connectionRevision) return
        if (result.available && result.dimension) applyDetectedDimension(result.dimension)
        break

      case 'rerank':
        result = await checkRerankModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          ...extraPayload,
        })
        break

      case 'vllm':
        result = await checkRemoteModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          ...extraPayload,
        })
        break

      case 'asr':
        result = await checkASRModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          ...extraPayload,
        })
        break

      default:
        MessagePlugin.error(t('model.editor.unsupportedModelType'))
        return
    }

    if (revision !== connectionRevision) return
    remoteChecked.value = true
    remoteAvailable.value = result.available || false
    remoteMessage.value = result.available
      ? t('model.editor.connectionSuccess')
      : result.message || t('model.editor.connectionFailed')
  } catch (error: unknown) {
    if (revision !== connectionRevision) return
    remoteChecked.value = true
    remoteAvailable.value = false
    remoteMessage.value = extractErrorMessage(error) || t('model.editor.connectionConfigError')
  } finally {
    if (revision === connectionRevision) checking.value = false
  }
}

const handleConfirm = async () => {
  if (saving.value) return
  saving.value = true
  if (checking.value) invalidateConnectionTest()
  saveError.value = ''
  try {
    if (!formData.value.modelName || !formData.value.modelName.trim()) {
      MessagePlugin.warning(t('model.editor.validation.modelNameRequired'))
      return
    }

    if (formData.value.modelName.trim().length > 100) {
      MessagePlugin.warning(t('model.editor.validation.modelNameMax'))
      return
    }

    if (formData.value.source === 'remote') {
      if (!formData.value.baseUrl || !formData.value.baseUrl.trim()) {
        MessagePlugin.warning(t('model.editor.remoteBaseUrlRequired'))
        return
      }

      try {
        new URL(formData.value.baseUrl.trim())
      } catch {
        MessagePlugin.warning(t('model.editor.validation.baseUrlInvalid'))
        return
      }

      for (const field of plainExtraFields.value) {
        if (field.required && !(formData.value.extraConfig?.[field.key] || '').trim()) {
          MessagePlugin.warning(
            t('model.editor.validation.extraFieldRequired', { name: extraFieldDisplayLabel(field) }),
          )
          return
        }
      }
      if (!isEdit.value && secretExtraField.value?.required && !(formData.value.appSecret || '').trim()) {
        MessagePlugin.warning(
          t('model.editor.validation.extraFieldRequired', { name: extraFieldDisplayLabel(secretExtraField.value) }),
        )
        return
      }
      if (specCompatError.value) {
        advancedOpen.value = true
        MessagePlugin.warning(`${t('model.editor.advanced.compat.invalid')}: ${specCompatError.value}`)
        return
      }
    }

    const validation = await formRef.value?.validate?.()
    if (validation !== undefined && validation !== true) return

    if (!formData.value.id) {
      formData.value.id = generateId()
    }

    await props.saveModel({
      ...formData.value,
      extraConfig: buildExtraConfig(),
      ...(isEdit.value ? {} : { modelType: activeModelType.value }),
    })
    emit('update:visible', false)
    invalidateConnectionTest(false)
    resetForm()
    lastOpenedModelId.value = null
  } catch (error: unknown) {
    saveError.value = extractErrorMessage(error) || t('modelSettings.toasts.saveFailed')
  } finally {
    saving.value = false
  }
}

watch(
  () => formData.value.modelName,
  async (newValue, oldValue) => {
    if (!newValue) return
    if (newValue.startsWith('__download__')) {
      const modelName = newValue.replace('__download__', '')
      formData.value.modelName = ''
      await startDownload(modelName)
      return
    }
    if (
      activeModelType.value === 'embedding'
      && formData.value.source === 'local'
      && newValue !== oldValue
      && oldValue !== ''
    ) {
      MessagePlugin.info(t('model.editor.dimensionHint'))
    }
  },
)

onUnmounted(() => {
  window.removeEventListener('keydown', handleSaveEscape, true)
  invalidateConnectionTest(false)
  cancelResolve()
  stopDownloadPolling()
})

watch(
  () => formData.value.source,
  () => {
    modelChecked.value = false
    modelAvailable.value = false
    remoteChecked.value = false
    remoteAvailable.value = false
    remoteMessage.value = ''
    dimensionChecked.value = false
    dimensionSuccess.value = false
    dimensionMessage.value = ''
    searchKeyword.value = ''
    stopDownloadPolling()
  },
)

watch(
  () => formData.value.modelName,
  () => {
    dimensionChecked.value = false
    dimensionSuccess.value = false
    dimensionMessage.value = ''
  },
)

const handleCancel = () => {
  if (saving.value) return
  resetForm()
  lastOpenedModelId.value = null
  dialogVisible.value = false
}

// Keep selectedProvider and runResolve exposed on instance setupState for integration tests
void selectedProvider
void runResolve
</script>

<style lang="less" scoped src="./model-editor/model-editor-dialog.less"></style>
<style lang="less" src="./model-editor/model-editor-popups.less"></style>
