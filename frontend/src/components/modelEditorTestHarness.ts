import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, type ComponentOptions } from 'vue'
import type { ModelProviderExtraField, ModelProviderExtraFieldOption, ResolvedModelCatalog } from '@/api/initialization'
import type { CredentialFieldDef } from '@/components/credentials/CredentialResource.vue'
import type { CanonicalModelProviderOption, CatalogModelOption, EditorModelType, ModelFormData } from './model-editor/types'

const require = createRequire(import.meta.url)

function readLocalFile(relPath: string): string {
  return readFileSync(fileURLToPath(new URL(relPath, import.meta.url)), 'utf8')
}

function transpileTs(source: string): string {
  return ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
}

const dialogFilename = fileURLToPath(new URL('./ModelEditorDialog.vue', import.meta.url))
const { descriptor: dialogDescriptor } = parse(readFileSync(dialogFilename, 'utf8'), { filename: dialogFilename })
const dialogScript = compileScript(dialogDescriptor, { id: 'model-editor-flow-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiledDialog = transpileTs(dialogScript)
const compiledCatalogResolver = transpileTs(readLocalFile('./model-editor/useModelCatalogResolver.ts'))
const compiledOllamaManager = transpileTs(readLocalFile('./model-editor/useOllamaModelManager.ts'))
const compiledEditorTypes = transpileTs(readLocalFile('./model-editor/types.ts'))

export const combinedEditorTemplates = [
  './ModelEditorDialog.vue',
  './model-editor/ModelOllamaDownloadSection.vue',
  './model-editor/ModelProviderCatalogSection.vue',
  './model-editor/ModelConnectionAuthSection.vue',
  './model-editor/ModelExtraConfigFields.vue',
]
  .map((relPath) => {
    const fname = fileURLToPath(new URL(relPath, import.meta.url))
    return parse(readFileSync(fname, 'utf8'), { filename: fname }).descriptor.template?.content ?? ''
  })
  .join('\n')

export function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}

export type CheckResult = { available: boolean; message?: string; dimension?: number }

export interface ModelEditorVm {
  activeModelType: EditorModelType
  advancedOpen: boolean
  apiKeyHint: string
  apiKeyLabel: string
  apiKeyPlaceholder: string
  apiKeyRequired: boolean
  buildExtraConfig: () => Record<string, string>
  catalogFilled: Partial<ModelFormData>
  catalogModelOptions: CatalogModelOption[]
  checkOllamaDimension: () => Promise<void>
  checkRemoteAPI: () => Promise<void>
  checking: boolean
  credentialFields: CredentialFieldDef[]
  dialogVisible: boolean
  dimensionChecked: boolean
  dimensionMessage: string
  dimensionSuccess: boolean
  embeddingAcceptsImages: boolean
  extraFieldDisplayOptionLabel: (opt: ModelProviderExtraFieldOption) => string
  extraFieldDisplayPlaceholder: (field: ModelProviderExtraField) => string
  formData: ModelFormData & Record<string, unknown>
  formRef: { validate?: () => Promise<boolean | Record<string, unknown>>; clearValidate?: () => void } | null
  getModelNamePlaceholder: () => string
  handleCancel: () => void
  handleCatalogModelChange: (value: unknown) => void
  handleCatalogModelCreate: (value: string | number | boolean | bigint) => void
  handleConfirm: () => Promise<void>
  handleProviderChange: (value: string) => void
  handleSaveEscape: (event: Partial<KeyboardEvent> & { key: string; preventDefault: () => void; stopImmediatePropagation: () => void }) => void
  invalidateConnectionTest: (showStale?: boolean) => void
  modelAvailable: boolean
  modelChecked: boolean
  plainExtraFields: ModelProviderExtraField[]
  providerOptions: CanonicalModelProviderOption[]
  remoteAvailable: boolean
  remoteChecked: boolean
  remoteMessage: string
  remoteStale: boolean
  resolveError: string
  resolveFailed: boolean
  resolved: ResolvedModelCatalog | null
  resolving: boolean
  runResolve: () => Promise<void>
  saveError: string
  saving: boolean
  secretExtraField: ModelProviderExtraField | undefined
  selectModelType: (type: string) => Promise<void>
  selectedProvider: CanonicalModelProviderOption | undefined
  selectedProviderDisplayLabel: string
  setExtraConfig: (key: string, value: string | null | undefined) => void
  showLegacyThinkingControl: boolean
  showResolvedPanel: boolean
  specCompatError: string
  [key: string]: unknown
}

export interface FixtureOptions {
  type?: string
  initialType?: string
  initiallyVisible?: boolean
  draft?: Record<string, unknown>
  edit?: boolean
  check?: (payload: Record<string, unknown>) => Promise<CheckResult>
  save?: (payload: Record<string, unknown>) => Promise<void>
  providers?: Array<Partial<CanonicalModelProviderOption> & { value: string }>
  providersByType?: Record<string, Array<Partial<CanonicalModelProviderOption> & { value: string }>>
  loadProviders?: (type?: string) => Promise<void>
  resolve?: (params: Record<string, unknown>) => Promise<Partial<ResolvedModelCatalog>>
}

export async function fixture(options: FixtureOptions = {}) {
  const requests: Array<Record<string, unknown>> = []
  const saves: Array<Record<string, unknown>> = []
  const resolves: Array<Record<string, unknown>> = []
  const providers = options.providers || []
  const catalogEvents: string[] = []
  const catalogRequests: Array<{ type?: string; force?: boolean }> = []
  const loadedProviders = reactive<Record<string, Array<Partial<CanonicalModelProviderOption> & { value: string }>>>({})

  const providersStore = {
    ensureLoaded: async (type?: string, force?: boolean) => {
      catalogEvents.push(force ? 'refresh' : 'load')
      catalogRequests.push({ type, force })
      const entries = options.providersByType?.[type || ''] ?? providers
      await options.loadProviders?.(type)
      loadedProviders[type || ''] = entries
      return entries
    },
    providersFor: (type?: string) => (options.providersByType ? loadedProviders[type || ''] || [] : providers),
    isLoading: () => false,
    providerById: (id: string) => providers.find((p) => p.value === id),
    iconFor: () => '',
    labelFor: (id: string) => id,
    descriptionFor: () => '',
  }

  const toasts: string[] = []
  const visibility: boolean[] = []
  const props = reactive<{
    visible: boolean
    modelType: string
    modelData: Record<string, unknown> | null
    draft?: Record<string, unknown>
  }>({
    visible: options.initiallyVisible ?? false,
    draft: options.draft,
    modelType: options.initialType || options.type || 'chat',
    modelData: options.edit
      ? {
          id: 'saved-model',
          modelName: 'old-model',
          name: '',
          source: 'remote',
          baseUrl: 'https://example.com/v1',
          provider: 'generic',
          isDefault: false,
        }
      : null,
  })

  const check = async (payload: Record<string, unknown>) => {
    requests.push(payload)
    return options.check ? options.check(payload) : { available: true }
  }

  const moduleCache = new Map<string, Record<string, unknown>>()
  const evaluateCjs = (code: string): Record<string, unknown> => {
    const modExports: Record<string, unknown> = {}
    runInNewContext(code, {
      exports: modExports,
      URL,
      setInterval,
      clearInterval,
      setTimeout,
      clearTimeout,
      JSON,
      window: { addEventListener() {}, removeEventListener() {} },
      console: { error() {}, debug() {}, log() {} },
      require: customRequire,
    })
    return modExports
  }

  function customRequire(name: string): unknown {
    if (moduleCache.has(name)) return moduleCache.get(name)
    if (name === 'vue') return require('vue')
    if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, te: () => false, locale: { value: 'en-US' } }) }
    if (name === '@/stores/ui') return { useUIStore: () => ({}) }
    if (name === 'tdesign-vue-next') {
      return {
        MessagePlugin: Object.fromEntries(
          ['success', 'error', 'warning', 'info'].map((key) => [key, (text: string) => toasts.push(text)]),
        ),
      }
    }
    if (name === '@/api/initialization') {
      return {
        checkRemoteModel: check,
        testEmbeddingModel: check,
        checkRerankModel: check,
        checkASRModel: check,
        listModelProviders: async () => [],
        checkOllamaStatus: async () => ({ available: false }),
        resolveModelCatalog: async (params: Record<string, unknown>) => {
          resolves.push(params)
          if (options.resolve) return options.resolve(params)
          return {
            provider: String(params.provider ?? ''),
            api: 'openai-completions',
            base_url: String(params.base_url ?? ''),
            remote_model: String(params.model ?? ''),
            cataloged: false,
            model: {},
            capabilities: {
              provider: String(params.provider ?? ''),
              api: 'openai-completions',
              cataloged: false,
              reasoning: false,
              thinking_levels: [],
              thinking_format: 'none',
            },
          }
        },
      }
    }
    if (name === '@/stores/modelProviders') return { useModelProvidersStore: () => providersStore }
    if (name === '@/utils/docsUrl') return require('../utils/docsUrl.ts')
    if (name === '@/stores/modelProvidersState') return require('../stores/modelProvidersState.ts')
    if (name === '@/utils/reasoningEffort') return require('../utils/reasoningEffort.ts')
    if (name === '@/utils/contextWindow') return require('../utils/contextWindow.ts')
    if (name === '@/components/modelEditorSourceState') return require('./modelEditorSourceState.ts')
    if (name === './types' || name === './model-editor/types') {
      const evaluated = evaluateCjs(compiledEditorTypes)
      moduleCache.set(name, evaluated)
      return evaluated
    }
    if (name === './model-editor/useModelCatalogResolver') {
      const evaluated = evaluateCjs(compiledCatalogResolver)
      moduleCache.set(name, evaluated)
      return evaluated
    }
    if (name === './model-editor/useOllamaModelManager') {
      const evaluated = evaluateCjs(compiledOllamaManager)
      moduleCache.set(name, evaluated)
      return evaluated
    }
    return { default: {} }
  }

  const dialogExports = evaluateCjs(compiledDialog) as { default: ComponentOptions }
  dialogExports.default.render = () => null

  type HostNode = Record<string, unknown>
  const renderer = createRenderer<HostNode, HostNode>({
    createElement: () => ({}),
    createText: () => ({}),
    createComment: () => ({}),
    insert() {},
    remove() {},
    setElementText() {},
    setText() {},
    patchProp() {},
    parentNode: () => null,
    nextSibling: () => null,
  })

  let vm!: ModelEditorVm
  const app = renderer.createApp({
    setup: () => () => h(dialogExports.default, {
      ...props,
      ref: (value: unknown) => {
        if (value) vm = value as ModelEditorVm
      },
      saveModel: async (payload: Record<string, unknown>) => {
        saves.push(payload)
        await options.save?.(payload)
      },
      'onUpdate:visible': (value: boolean) => {
        visibility.push(value)
        props.visible = value
      },
    }),
  })
  app.mount({})
  props.modelType = options.type || 'chat'
  props.visible = true
  await nextTick()
  await nextTick()
  if (!options.draft) {
    Object.assign(vm.formData, {
      modelName: 'draft-model',
      baseUrl: 'https://example.com/v1',
      apiKey: options.edit ? '' : 'draft-key',
    })
  }
  vm.formRef = { validate: async () => true }
  await nextTick()
  return {
    vm,
    props,
    requests,
    saves,
    toasts,
    visibility,
    resolves,
    catalogEvents,
    catalogRequests,
    close: () => app.unmount(),
  }
}

export const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
