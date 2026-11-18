import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test, { type TestContext } from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

const require = createRequire(import.meta.url)

async function flush() {
  await new Promise<void>((resolve) => setImmediate(resolve))
  await nextTick()
}

function compileVueScript(relativePath: string) {
  const filename = fileURLToPath(new URL(relativePath, import.meta.url))
  const raw = readFileSync(filename, 'utf8')
  const { descriptor } = parse(raw, { filename })
  const script = compileScript(descriptor, { id: `settings-test-${relativePath}` }).content
    .replace('__expose();', '')
    .replace('return __returned__', '__expose(__returned__); return __returned__')
  const outputText = ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  return { outputText, template: descriptor.template?.content ?? '' }
}

function mountCompiled(outputText: string, globals: Record<string, unknown>, stubs: Record<string, unknown>, props: Record<string, unknown> = {}) {
  const exports: any = {}
  runInNewContext(outputText, {
    exports,
    URL,
    URLSearchParams,
    Date,
    Math,
    JSON,
    Set,
    Map,
    Promise,
    CustomEvent,
    console: { ...console, error() {} },
    ...globals,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name in stubs) return stubs[name]
      return { default: {} }
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
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
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  return { vm: instance.value, unmount: () => app.unmount() }
}

test('McpServiceDialog rejects non-http(s) OAuth authorizationUrl and severs window.opener on valid popups', async () => {
  const { outputText } = compileVueScript('./components/McpServiceDialog.vue')
  const errors: string[] = []
  const popups: Array<{ closed: boolean; opener: unknown; close: () => void }> = []
  let nextAuthUrl = 'javascript:alert(1)'
  const intervals = new Map<number, () => Promise<void>>()
  let timerSeq = 0

  const props = reactive({
    visible: true,
    service: {
      id: 'svc-1',
      name: 'Docs MCP',
      description: 'Desc',
      transport_type: 'sse',
      url: 'https://mcp.example.com/sse',
      auth_config: { auth_type: 'oauth' },
    },
    mode: 'edit',
    initialStep: 0,
  })

  const { vm, unmount } = mountCompiled(
    outputText,
    {
      window: {
        location: { origin: 'https://genyee.example.com' },
        setInterval: (cb: () => Promise<void>) => {
          intervals.set(++timerSeq, cb)
          return timerSeq
        },
        clearInterval: (id: number) => intervals.delete(id),
        open: () => {
          const popup = {
            closed: false,
            opener: { sensitive: true } as unknown,
            close() {
              this.closed = true
            },
          }
          popups.push(popup)
          return popup
        },
      },
    },
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k, locale: ref('zh-CN') }) },
      'tdesign-vue-next': {
        MessagePlugin: {
          error: (m: string) => errors.push(m),
          warning() {},
          success() {},
        },
      },
      '@/api/mcp-service': {
        MCP_OAUTH_CALLBACK_PATH: '/callback',
        getMCPOAuthAuthorizationStatus: async () => ({ authorized: false, state: 'reauth_required' }),
        getMCPOAuthAuthorizeURL: async () => ({ authorizationUrl: nextAuthUrl, authorizationAttempt: 'att-1' }),
        getMCPOAuthStatus: async () => false,
        revokeMCPOAuthToken: async () => {},
        updateMCPService: async (_id: string, data: Record<string, unknown>) => ({ ...props.service, ...data }),
      },
    },
    props,
  )

  try {
    await flush()
    vm.formRef = { validate: async () => true }
    await vm.handleAuthorize()
    assert.equal(popups.length, 0, 'Must not open popup for javascript: authorizationUrl')
    assert.equal(vm.oauthAuthorizing, false)
    assert.ok(errors.includes('mcpServiceDialog.toasts.authorizeFailed'))

    nextAuthUrl = 'https://auth.example.com/oauth/authorize?client_id=1'
    await vm.handleAuthorize()
    assert.equal(popups.length, 1)
    assert.equal(popups[0]?.opener, null, 'Must sever popup.opener to prevent reverse tabnabbing')
  } finally {
    unmount()
  }
})

test('MemoryWorkspaceSettings blocks autosave when loadConfig fails, reloads on tenant switch, and clears timers on unmount', async (t: TestContext) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const { outputText } = compileVueScript('./MemoryWorkspaceSettings.vue')
  const errors: string[] = []
  const reads: number[] = []
  const writes: Array<{ tenantId: number; payload: Record<string, unknown> }> = []
  let shouldFailLoad = true

  const authStore = reactive({
    effectiveTenantId: 1,
    hasRole: () => true,
  })

  const { vm, unmount } = mountCompiled(
    outputText,
    {
      setTimeout,
      clearTimeout,
      window: {
        setTimeout,
        clearTimeout,
        dispatchEvent() {},
      },
    },
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
      'tdesign-vue-next': {
        MessagePlugin: {
          error: (m: string) => errors.push(m),
          success() {},
        },
      },
      '@/stores/auth': { useAuthStore: () => authStore },
      '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
      '@/api/memory': {
        getTenantMemoryConfig: async () => {
          reads.push(authStore.effectiveTenantId)
          if (shouldFailLoad) throw new Error('network down')
          return {
            data: {
              enabled: true,
              write_mode: 'auto',
              extract_model_id: `model-t${authStore.effectiveTenantId}`,
              max_items: 150,
              extract_delay_seconds: 90,
              extract_min_interval_seconds: 300,
              extract_instructions: '',
              interest_threshold: 3,
              retrieval_conditioning: true,
              embedding_model_id: '',
              vector_recall: true,
            },
          }
        },
        updateTenantMemoryConfig: async (payload: Record<string, unknown>) => {
          writes.push({ tenantId: authStore.effectiveTenantId, payload })
        },
      },
    },
  )

  await flush()
  t.mock.timers.tick(200)
  await flush()

  // 1. Failed initial load must surface an error toast and refuse to clobber server config with defaults
  assert.ok(errors.length >= 1, 'Expected error toast when getTenantMemoryConfig fails')
  vm.debouncedSave()
  t.mock.timers.tick(600)
  await flush()
  assert.equal(writes.length, 0, 'Must not autosave local defaults when loadConfig failed')

  // 2. Switching effectiveTenantId cancels pending save for tenant 1 and loads tenant 2
  shouldFailLoad = false
  await vm.loadConfig()
  t.mock.timers.tick(200)
  await flush()

  vm.config.max_items = 999
  vm.debouncedSave()
  authStore.effectiveTenantId = 2
  await flush()
  t.mock.timers.tick(600)
  await flush()

  assert.equal(writes.length, 0, 'Pending save from previous tenant must be cancelled on workspace switch')
  assert.deepEqual(reads, [1, 1, 2])
  assert.equal(vm.config.extract_model_id, 'model-t2')

  // 3. Unmounting cancels pending saveTimer
  vm.config.max_items = 333
  vm.debouncedSave()
  unmount()
  t.mock.timers.tick(600)
  await flush()
  assert.equal(writes.length, 0)
})

test('ParserEngineSettings checks connected.value, surfaces save toast errors, and clears checkMessage timer on unmount', async (t: TestContext) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const { outputText } = compileVueScript('./ParserEngineSettings.vue')
  const errors: string[] = []
  const successes: string[] = []
  let checkCalls = 0
  let failSave = true

  const { vm, unmount } = mountCompiled(
    outputText,
    {
      setTimeout,
      clearTimeout,
    },
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
      '@/stores/ui': { useUIStore: () => ({}) },
      '@/stores/auth': { useAuthStore: () => ({ isLiteMode: false }) },
      'tdesign-vue-next': {
        MessagePlugin: {
          error: (m: string) => errors.push(m),
          success: (m: string) => successes.push(m),
        },
      },
      '@/api/system': {
        getParserEngines: async () => ({
          data: [
            { name: 'builtin', description: '', file_types: [], available: false },
            { name: 'mineru', description: '', file_types: [], available: false },
          ],
          connected: false,
        }),
        getParserEngineConfig: async () => ({ data: {} }),
        checkParserEngines: async () => {
          checkCalls += 1
          return {
            data: [
              { name: 'builtin', description: '', file_types: [], available: true },
              { name: 'mineru', description: '', file_types: [], available: true },
            ],
            connected: true,
          }
        },
        updateParserEngineConfig: async () => {
          if (failSave) throw new Error('save boom')
        },
      },
      '@/api/model': {},
    },
  )

  await flush()
  assert.equal(vm.connected, false)

  // Non-builtin engine when disconnected must short-circuit with ensureDocreaderConnected
  vm.currentEngine = { name: 'mineru', description: '', file_types: [], available: false }
  await vm.onCheck()
  assert.equal(checkCalls, 0, 'Expected !connected.value guard to short-circuit for non-builtin engine')
  assert.equal(vm.checkMessage, 'settings.parser.ensureDocreaderConnected')

  // Builtin engine can re-probe DocReader connectivity even when currently disconnected
  vm.currentEngine = { name: 'builtin', description: '', file_types: [], available: false }
  await vm.onCheck()
  assert.equal(checkCalls, 1)
  assert.equal(vm.connected, true)

  // Save failure & success must emit MessagePlugin feedback
  await vm.onSave()
  assert.ok(errors.includes('save boom'))
  failSave = false
  await vm.onSave()
  assert.ok(successes.includes('settings.parser.saveSuccess'))

  // Unmounting before 3000ms clears checkMessage timer cleanly
  unmount()
  t.mock.timers.tick(3500)
  await flush()
})

test('StorageBackendSettings catches load, testRaw, testSaved, and makeDefault rejections without unhandled rejections', async () => {
  const { outputText } = compileVueScript('./StorageBackendSettings.vue')
  const errors: string[] = []
  let failLoad = true

  const { vm, unmount } = mountCompiled(
    outputText,
    {},
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
      'tdesign-vue-next': {
        MessagePlugin: {
          error: (m: string) => errors.push(m),
          success() {},
          warning() {},
        },
        DialogPlugin: { confirm: () => ({ destroy() {} }) },
      },
      '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
      '@/utils/providerLogos': { providerLogo: () => null },
      '@/api/storage-backend': {
        listStorageBackends: async () => {
          if (failLoad) throw new Error('load failed')
          return { data: [{ id: 'b1', name: 'S3', provider: 's3', config: {} }], default_storage_backend_id: 'b1' }
        },
        listStorageBackendTypes: async () => ({ data: ['local', 's3'] }),
        testStorageBackend: async () => {
          throw new Error('raw test network error')
        },
        testStorageBackendByID: async () => {
          throw new Error('saved test network error')
        },
        createStorageBackend: async () => ({}),
        updateStorageBackend: async () => ({}),
        deleteStorageBackend: async () => ({}),
        setDefaultStorageBackend: async () => {
          throw new Error('cannot set default')
        },
      },
    },
  )

  try {
    await flush()
    assert.equal(vm.loading, false)
    assert.ok(errors.includes('load failed'))

    failLoad = false
    await vm.load()
    assert.equal(vm.defaultID, 'b1')

    await vm.testRaw()
    assert.equal(vm.rawTestResult, 'error')
    assert.ok(errors.includes('raw test network error'))

    await vm.testSaved({ id: 'b1', name: 'S3', provider: 's3', config: {} })
    assert.ok(errors.includes('saved test network error'))

    await vm.makeDefault({ id: 'b2', name: 'MinIO', provider: 'minio', config: {} })
    assert.equal(vm.defaultID, 'b1', 'defaultID must not mutate when setDefaultStorageBackend rejects')
    assert.ok(errors.includes('cannot set default'))
  } finally {
    unmount()
  }
})

test('OllamaSettings clears download progress polling interval on unmount', async (t: TestContext) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const { outputText } = compileVueScript('./OllamaSettings.vue')
  let progressPolls = 0

  const { vm, unmount } = mountCompiled(
    outputText,
    {
      setInterval,
      clearInterval,
    },
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
      'tdesign-vue-next': { MessagePlugin: { success() {}, error() {} } },
      '@/stores/settings': {
        useSettingsStore: () => ({
          settings: { ollamaConfig: { baseUrl: 'http://localhost:11434' } },
          updateOllamaConfig() {},
        }),
      },
      '@/api/initialization': {
        checkOllamaStatus: async () => ({ available: true, baseUrl: 'http://localhost:11434' }),
        listOllamaModels: async () => [],
        downloadOllamaModel: async () => ({ status: 'downloading', taskId: 'task-1' }),
        getDownloadProgress: async () => {
          progressPolls += 1
          return { status: 'downloading', progress: 42 }
        },
      },
    },
  )

  await flush()
  vm.downloadModelName = 'qwen3:0.6b'
  await vm.downloadModel()
  t.mock.timers.tick(1000)
  await flush()
  assert.equal(progressPolls, 1)

  unmount()
  t.mock.timers.tick(3000)
  await flush()
  assert.equal(progressPolls, 1, 'Polling must stop immediately when OllamaSettings unmounts')
})

test('SystemInfo and WebSearchSettings reject non-http(s) external URLs', async () => {
  const sysCompiled = compileVueScript('./SystemInfo.vue')
  const sys = mountCompiled(
    sysCompiled.outputText,
    {
      __FRONTEND_VERSION__: '1.0.0',
      __FRONTEND_COMMIT__: 'abcdef0',
      setInterval: () => 1,
      clearInterval() {},
      window: {
        __RUNTIME_CONFIG__: { GITHUB_URL: 'javascript:alert(1)' },
      },
    },
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k, locale: ref('en-US') }) },
      '@/stores/editorResources': {
        useEditorResourcesStore: () => ({ loadSystemInfo: async () => ({ version: '1.0.0', db_migration_error: 'boom' }) }),
      },
      '@/utils/docsUrl': { docsUrl: () => 'https://docs.genyee.example.com/migrations' },
    },
  )
  try {
    await flush()
    assert.equal(sys.vm.githubUrl, '')
    assert.equal(sys.vm.reportIssueURL, '')
  } finally {
    sys.unmount()
  }

  const wsCompiled = compileVueScript('./WebSearchSettings.vue')
  const ws = mountCompiled(
    wsCompiled.outputText,
    {},
    {
      'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
      'tdesign-vue-next': {
        MessagePlugin: { success() {}, error() {}, warning() {} },
        DialogPlugin: { confirm: () => ({ destroy() {} }) },
      },
      'tdesign-icons-vue-next': { AddIcon: {} },
      '@/utils/assistantDataChange': { useAssistantDataChange: () => {} },
      '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({ confirmDelete() {} }) },
      '@/stores/auth': { useAuthStore: () => ({ isLiteMode: false }) },
      '@/stores/chatResources': { useChatResourcesStore: () => ({ invalidateWebSearch() {} }) },
      './providerLogos': { providerLogo: () => null },
      '@/api/web-search-provider': {
        listWebSearchProviderTypes: async () => [
          { id: 'evil-search', name: 'Evil Search', docs_url: 'javascript:alert(1)', config_fields: [] },
          { id: 'good-search', name: 'Good Search', docs_url: 'https://search.example.com/docs', config_fields: [] },
        ],
        listWebSearchProviders: async () => [],
        createWebSearchProvider: async () => ({}),
        updateWebSearchProvider: async () => ({}),
        deleteWebSearchProvider: async () => ({}),
        testWebSearchProvider: async () => ({ ok: true }),
        putWebSearchProviderCredentials: async () => ({ fields: {} }),
        deleteWebSearchProviderCredentialField: async () => ({}),
      },
    },
  )
  try {
    await flush()
    ws.vm.providerForm.provider = 'evil-search'
    await flush()
    assert.equal(ws.vm.safeSelectedDocsUrl, '')

    ws.vm.providerForm.provider = 'good-search'
    await flush()
    assert.equal(ws.vm.safeSelectedDocsUrl, 'https://search.example.com/docs')
    assert.match(wsCompiled.template, /v-if="safeSelectedDocsUrl"[\s\S]*?:href="safeSelectedDocsUrl"/)
  } finally {
    ws.unmount()
  }
})
