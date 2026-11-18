import { computed, ref, type ComputedRef, type Ref } from 'vue'
import {
  resolveModelCatalog,
  type ModelCatalogEntry,
  type ModelProviderExtraField,
  type ModelProviderExtraFieldOption,
  type ResolvedModelCatalog,
} from '@/api/initialization'
import type { ModelSpecOverride } from '@/api/model'
import {
  credentialLabelForModelType,
  extraFieldLabel,
  extraFieldOptionLabel,
  extraFieldPlaceholder,
  extraFieldsForModelType,
  pickLocalized,
  providerDescription,
  providerIcon,
  providerLabel,
} from '@/stores/modelProvidersState'
import { supportedLevels } from '@/utils/reasoningEffort'
import { formatTokenCount } from '@/utils/contextWindow'
import {
  RESERVED_EXTRA_CONFIG_KEYS,
  VENDOR_NEUTRAL_EXTRA_CONFIG_KEYS,
  type CanonicalModelProviderOption,
  type CatalogModelOption,
  type EditorModelType,
  type ModelFormData,
} from './types'

export interface ModelProvidersStoreLike {
  isLoading: (modelType?: string) => boolean
  ensureLoaded: (modelType?: string, force?: boolean) => Promise<unknown>
  providersFor: (modelType?: string) => unknown[]
  providerById: (id: string) => unknown
}

function extractResolveErrorDetail(error: unknown): string {
  if (typeof error === 'string') return error.trim()
  if (error && typeof error === 'object') {
    const rec = error as { message?: unknown; error?: unknown }
    if (typeof rec.message === 'string') return rec.message.trim()
    if (typeof rec.error === 'string') return rec.error.trim()
    if (rec.error && typeof rec.error === 'object') {
      const nested = (rec.error as { message?: unknown }).message
      if (typeof nested === 'string') return nested.trim()
    }
  }
  return ''
}

export function useModelCatalogResolver(options: {
  t: (key: string, params?: Record<string, unknown>) => string
  currentLocale: ComputedRef<string>
  visible: () => boolean
  isEdit: ComputedRef<boolean>
  activeModelType: ComputedRef<EditorModelType>
  formData: Ref<ModelFormData>
  providersStore: ModelProvidersStoreLike
  onVendorResetChecks: () => void
}) {
  const {
    t,
    currentLocale,
    visible,
    isEdit,
    activeModelType,
    formData,
    providersStore,
    onVendorResetChecks,
  } = options

  const loadingProviders = computed(() => providersStore.isLoading(activeModelType.value))

  const loadProviders = async (force = false) => {
    try {
      await providersStore.ensureLoaded(activeModelType.value, force)
    } catch (error) {
      console.error('Failed to load providers from API', error)
    }
  }

  const providerOptions = computed<CanonicalModelProviderOption[]>(() =>
    providersStore.providersFor(activeModelType.value) as CanonicalModelProviderOption[],
  )

  const selectedProvider = computed<CanonicalModelProviderOption | undefined>(() => {
    const id = formData.value.provider
    if (!id) return undefined
    return (
      providerOptions.value.find((p) => p.value === id)
      || (providersStore.providerById(id) as CanonicalModelProviderOption | undefined)
    )
  })

  const providerDisplayLabel = (p: CanonicalModelProviderOption) =>
    providerLabel(p as unknown as Parameters<typeof providerLabel>[0], currentLocale.value)
  const providerDisplayDescription = (p: CanonicalModelProviderOption) =>
    providerDescription(p as unknown as Parameters<typeof providerDescription>[0], currentLocale.value)
  const providerDisplayIcon = (p: CanonicalModelProviderOption) =>
    providerIcon(p as unknown as Parameters<typeof providerIcon>[0])
  const selectedProviderIcon = computed(() =>
    selectedProvider.value ? providerDisplayIcon(selectedProvider.value) : '',
  )
  const selectedProviderDisplayLabel = computed(() => (
    selectedProvider.value
      ? providerDisplayLabel(selectedProvider.value)
      : (formData.value.provider || '')
  ))

  const extraFieldDisplayLabel = (field: ModelProviderExtraField) =>
    extraFieldLabel(field, currentLocale.value)
  const extraFieldDisplayPlaceholder = (field: ModelProviderExtraField) =>
    extraFieldPlaceholder(field, currentLocale.value)
  const extraFieldDisplayOptionLabel = (option: ModelProviderExtraFieldOption) =>
    extraFieldOptionLabel(option, currentLocale.value)

  const credentialLabel = computed(() =>
    credentialLabelForModelType(selectedProvider.value?.credential_labels, activeModelType.value),
  )
  const apiKeyLabel = computed(() => {
    const label = credentialLabel.value
    return label ? pickLocalized(label.labels, currentLocale.value, label.label) : t('model.editor.apiKeyOptional')
  })
  const apiKeyRequired = computed(
    () => credentialLabel.value?.required === true || (!!selectedProvider.value?.requires_auth && !isEdit.value),
  )
  const apiKeyHint = computed(() => {
    const label = credentialLabel.value
    if (!label?.hint) return ''
    return pickLocalized(label.hints, currentLocale.value, label.hint)
  })
  const apiKeyPlaceholder = computed(() => {
    const label = credentialLabel.value
    if (label?.placeholder) return pickLocalized(label.placeholders, currentLocale.value, label.placeholder)
    return t('model.editor.apiKeyPlaceholder')
  })

  const visibleExtraFields = computed<ModelProviderExtraField[]>(() =>
    extraFieldsForModelType(selectedProvider.value?.extra_fields, activeModelType.value)
      .filter((field) => !RESERVED_EXTRA_CONFIG_KEYS.has(field.key)),
  )
  const isSecretExtraField = (field: ModelProviderExtraField) =>
    field.secret === true || field.type === 'password'
  const secretExtraField = computed<ModelProviderExtraField | undefined>(() =>
    visibleExtraFields.value.find(isSecretExtraField),
  )
  const plainExtraFields = computed<ModelProviderExtraField[]>(() =>
    visibleExtraFields.value.filter((field) => !isSecretExtraField(field)),
  )

  const keepVendorNeutralExtraConfig = (): Record<string, string> => {
    const keep: Record<string, string> = {}
    for (const key of VENDOR_NEUTRAL_EXTRA_CONFIG_KEYS) {
      const value = formData.value.extraConfig?.[key]
      if (value) keep[key] = value
    }
    return keep
  }

  const setExtraConfig = (key: string, value: string | null | undefined) => {
    const next = { ...(formData.value.extraConfig || {}) }
    const normalized = value == null ? '' : String(value)
    if (normalized === '') delete next[key]
    else next[key] = normalized
    formData.value.extraConfig = next
  }

  const extraConfigBool = (key: string) => {
    const raw = (formData.value.extraConfig?.[key] || '').trim().toLowerCase()
    return raw === 'true' || raw === '1' || raw === 'yes'
  }

  const applyExtraFieldDefaults = () => {
    for (const field of plainExtraFields.value) {
      if (!field.default) continue
      if ((formData.value.extraConfig?.[field.key] ?? '') === '') {
        setExtraConfig(field.key, field.default)
      }
    }
  }

  const catalogEntries = computed<ModelCatalogEntry[]>(() => selectedProvider.value?.models || [])

  const catalogModelOptions = computed<CatalogModelOption[]>(() => {
    const opts: CatalogModelOption[] = catalogEntries.value.map((entry) => ({
      value: entry.id,
      label: entry.name || entry.id,
      contextWindow: entry.context_window ? formatTokenCount(entry.context_window) : '',
      dimension: entry.dimension || undefined,
      reasoning: !!entry.reasoning || (entry.thinking_levels?.length ?? 0) > 0,
      vision: Array.isArray(entry.input) && entry.input.includes('image'),
      entry,
    }))
    const current = (formData.value.modelName || '').trim()
    if (current && !opts.some((o) => o.value === current)) {
      opts.unshift({ value: current, label: current, contextWindow: '', reasoning: false, vision: false })
    }
    return opts
  })

  const findCatalogEntry = (name: string) => catalogEntries.value.find((m) => m.id === name)

  const vendorDocLink = computed(() => {
    const provider = selectedProvider.value
    if (!provider) return ''
    const entry = findCatalogEntry((formData.value.modelName || '').trim())
    const url = entry?.source || provider.website || ''
    return /^https?:\/\//i.test(url) ? url : ''
  })

  const catalogFilled = ref<Partial<ModelFormData>>({})

  const catalogEmbeddingAcceptsImages = computed(() =>
    !!findCatalogEntry((formData.value.modelName || '').trim())?.input?.includes('image'),
  )

  const embeddingAcceptsImages = computed<boolean>({
    get: () => {
      const input = formData.value.spec?.input
      return Array.isArray(input) && input.length > 0
        ? input.includes('image')
        : catalogEmbeddingAcceptsImages.value
    },
    set: (value) => {
      const spec: ModelSpecOverride = { ...(formData.value.spec || {}) }
      if (value === catalogEmbeddingAcceptsImages.value) delete spec.input
      else spec.input = value ? ['text', 'image'] : ['text']
      formData.value.spec = Object.keys(spec).length > 0 ? spec : null
    },
  })

  const dropEmbeddingInputOverride = () => {
    if (!formData.value.spec?.input) return
    const spec: ModelSpecOverride = { ...formData.value.spec }
    delete spec.input
    formData.value.spec = Object.keys(spec).length > 0 ? spec : null
  }

  const applyCatalogEntry = (entry: ModelCatalogEntry) => {
    if (activeModelType.value === 'chat' || activeModelType.value === 'vllm') {
      if (!formData.value.contextWindow && entry.context_window) {
        formData.value.contextWindow = entry.context_window
        catalogFilled.value.contextWindow = entry.context_window
      }
      if (!formData.value.maxOutputTokens && entry.max_output_tokens) {
        formData.value.maxOutputTokens = entry.max_output_tokens
        catalogFilled.value.maxOutputTokens = entry.max_output_tokens
      }
    }
    if (
      activeModelType.value === 'chat'
      && !formData.value.supportsVision
      && Array.isArray(entry.input)
      && entry.input.includes('image')
    ) {
      formData.value.supportsVision = true
      catalogFilled.value.supportsVision = true
    }
    if (activeModelType.value === 'embedding') dropEmbeddingInputOverride()
    if (activeModelType.value === 'embedding' && !formData.value.dimension && entry.dimension) {
      formData.value.dimension = entry.dimension
      catalogFilled.value.dimension = entry.dimension
    }
  }

  const handleCatalogModelCreate = (value: string | number | boolean | bigint) => {
    formData.value.modelName = String(value ?? '').trim()
  }

  const handleCatalogModelChange = (value: unknown) => {
    const name = typeof value === 'string' ? value.trim() : ''
    if (!name) return
    const entry = findCatalogEntry(name)
    if (entry) applyCatalogEntry(entry)
  }

  const resetModelSelectionForVendor = () => {
    const current = (formData.value.modelName || '').trim()
    if (!current || findCatalogEntry(current)) return

    formData.value.modelName = ''
    const filled = catalogFilled.value
    if (filled.contextWindow && formData.value.contextWindow === filled.contextWindow) {
      formData.value.contextWindow = undefined
    }
    if (filled.maxOutputTokens && formData.value.maxOutputTokens === filled.maxOutputTokens) {
      formData.value.maxOutputTokens = undefined
    }
    if (filled.dimension && formData.value.dimension === filled.dimension) {
      formData.value.dimension = undefined
    }
    if (filled.supportsVision && formData.value.supportsVision) {
      formData.value.supportsVision = false
    }
    if (activeModelType.value === 'embedding') dropEmbeddingInputOverride()
    catalogFilled.value = {}
    onVendorResetChecks()
  }

  const resolved = ref<ResolvedModelCatalog | null>(null)
  const resolving = ref(false)
  const resolveFailed = ref(false)
  const resolveError = ref('')
  let resolveTimer: ReturnType<typeof setTimeout> | null = null
  let resolveRevision = 0

  const isChatLike = computed(() => activeModelType.value === 'chat' || activeModelType.value === 'vllm')
  const showResolvedPanel = computed(
    () => isChatLike.value && formData.value.source === 'remote' && !!formData.value.provider,
  )
  const resolvedThinkingLevels = computed(() => supportedLevels(resolved.value?.capabilities))

  const specCompatError = computed(() => {
    const text = (formData.value.specCompat || '').trim()
    if (!text) return ''
    try {
      const parsed: unknown = JSON.parse(text)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return t('model.editor.advanced.compat.mustBeObject')
      }
      return ''
    } catch (error: unknown) {
      if (error && typeof error === 'object' && 'message' in error) {
        const msg = (error as { message?: unknown }).message
        if (typeof msg === 'string' && msg) return msg
      }
      return 'invalid JSON'
    }
  })

  const buildSpec = (): ModelSpecOverride => {
    if (specCompatError.value) throw new Error(specCompatError.value)
    const spec: ModelSpecOverride = { ...(formData.value.spec || {}) }
    const text = (formData.value.specCompat || '').trim()
    if (text) spec.compat = JSON.parse(text) as Record<string, unknown>
    else delete spec.compat
    return spec
  }

  const runResolve = async () => {
    const revision = ++resolveRevision
    const provider = (formData.value.provider || '').trim()
    if (!visible() || !showResolvedPanel.value || !provider) {
      resolved.value = null
      resolving.value = false
      resolveFailed.value = false
      resolveError.value = ''
      return
    }
    resolving.value = true
    try {
      const result = await resolveModelCatalog({
        provider,
        spec: buildSpec(),
        model: formData.value.modelName || '',
        base_url: formData.value.baseUrl || '',
        model_type: activeModelType.value,
        api: formData.value.extraConfig?.api || '',
        thinking_control: formData.value.thinkingControl || '',
        remote_model_name: formData.value.extraConfig?.remote_model_name || '',
        ...Object.fromEntries(
          plainExtraFields.value
            .map((field) => [field.key, formData.value.extraConfig?.[field.key] || ''])
            .filter(([, value]) => !!value),
        ),
      })
      if (revision !== resolveRevision) return
      resolved.value = result
      resolveFailed.value = false
      resolveError.value = ''
    } catch (error: unknown) {
      if (revision !== resolveRevision) return
      resolved.value = null
      resolveFailed.value = true
      resolveError.value = extractResolveErrorDetail(error)
    } finally {
      if (revision === resolveRevision) resolving.value = false
    }
  }

  const scheduleResolve = () => {
    if (resolveTimer) clearTimeout(resolveTimer)
    resolveTimer = setTimeout(() => {
      resolveTimer = null
      void runResolve()
    }, 400)
  }

  const cancelResolve = () => {
    resolveRevision++
    if (resolveTimer) {
      clearTimeout(resolveTimer)
      resolveTimer = null
    }
    resolving.value = false
    resolved.value = null
    resolveFailed.value = false
    resolveError.value = ''
  }

  return {
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
    credentialLabel,
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
    findCatalogEntry,
    vendorDocLink,
    catalogFilled,
    embeddingAcceptsImages,
    dropEmbeddingInputOverride,
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
  }
}
