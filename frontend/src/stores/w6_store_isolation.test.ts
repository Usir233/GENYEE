import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as pinia from 'pinia'
import * as reasoningEffort from '../utils/reasoningEffort'
import * as sidebarWidth from '../utils/sidebarWidth'
import * as organizationState from './organizationState'
import * as localizedResourceCache from './localizedResourceCache'
import * as versionedRequest from './versionedRequest'
import * as sequencedRefresh from './sequencedRefresh'
import * as modelProvidersState from './modelProvidersState'
import * as deploymentCapabilitiesCfg from '../config/deploymentCapabilities'

type DynamicExports = ReturnType<typeof JSON.parse>

function compileFile(relPath: string): string {
  const source = readFileSync(new URL(relPath, import.meta.url), 'utf8')
  return ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
}

function createMemoryStorage() {
  const map = new Map<string, string>()
  return {
    map,
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => { map.set(k, String(v)) },
    removeItem: (k: string) => { map.delete(k) },
    clear: () => { map.clear() },
  }
}

test('W6-ST-01: logout and user switch scrub tenant-scoped localStorage and reset in-memory settingsStore, modelProviders, browserConnection, menu, ui, uploadConfirm, and assistant transient state', async () => {
  const local = createMemoryStorage()
  const session = createMemoryStorage()

  // Seed stale tenant-10001 settings in localStorage before stores initialize
  local.setItem('genyee_last_chat_model_id', 'model-10001')
  local.setItem('GENYEE_user_last_chat_model', 'model-10001')
  local.setItem(
    'GENYEE_settings',
    JSON.stringify({
      endpoint: '',
      apiKey: 'sk-tenant-a-secret',
      knowledgeBaseId: 'kb-a',
      isAgentEnabled: true,
      selectedAgentId: 'agent-custom-a',
      selectedAgentSourceTenantId: '10001',
      selectedKnowledgeBases: ['kb-a'],
      selectedFiles: ['file-a'],
      selectedFileKbMap: { 'file-a': 'kb-a' },
      selectedTags: [{ id: 'tag-a', name: 'SecretTag', kbId: 'kb-a' }],
      selectedMCPServices: ['mcp-a'],
      selectedSkills: ['skill-a'],
      modelConfig: {
        chatModels: [{ id: 'm1', name: 'M1', source: 'remote', modelName: 'gpt', apiKey: 'sk-inline-a' }],
        embeddingModels: [],
        rerankModels: [],
        vllmModels: [],
      },
      conversationModels: {
        summaryModelId: 'sum-a',
        rerankModelId: 'rerank-a',
        selectedChatModelId: 'chat-a',
      },
    }),
  )

  const tokenExports: DynamicExports = {}
  vm.runInNewContext(compileFile('../utils/authTokens.ts'), {
    exports: tokenExports,
    localStorage: local,
  })

  const agentModeExports: DynamicExports = {}
  const settingsStorageExports: DynamicExports = {}
  const settingsExports: DynamicExports = {}
  const modelProvidersExports: DynamicExports = {}
  const browserConnExports: DynamicExports = {}
  const menuExports: DynamicExports = {}
  const uiExports: DynamicExports = {}
  const uploadConfirmExports: DynamicExports = {}
  const knowledgeExports: DynamicExports = {}
  const assistantExports: DynamicExports = {}
  const authExports: DynamicExports = {}
  const deployCapExports: DynamicExports = {}

  let chatInvalidated = 0
  let editorInvalidated = 0
  let orgCleared = 0
  let protectedCleared = 0

  const sharedRequire = (id: string): unknown => {
    if (id === 'vue') return vue
    if (id === 'pinia') return pinia
    if (id === '@/i18n') {
      const global = { t: (k: string) => k, locale: { value: 'en-US' } }
      return { __esModule: true, default: { global }, global }
    }
    if (id === '@/api/agent') {
      return {
        BUILTIN_QUICK_ANSWER_ID: 'builtin-quick-answer',
        BUILTIN_SMART_REASONING_ID: 'builtin-smart-reasoning',
      }
    }
    if (id === '@/api/auth') return { userInfoFromApi: (u: unknown) => u }
    if (id === '@/api/initialization') {
      return {
        listModelProviders: async () => [{ value: 'openai', label: 'OpenAI', description: '' }],
      }
    }
    if (id === '@/api/system') return { getDeploymentCapabilities: async () => ({ data: { capabilities: {} } }) }
    if (id === '@/api/assistant') {
      return {
        DEFAULT_ASSISTANT_ACCESS_LEVEL: 'read',
        capAssistantAccessLevel: (a: string) => a,
        getTenantAssistantConfig: async () => ({ data: null }),
        isAssistantAccessLevel: (v: unknown) => v === 'read' || v === 'write',
      }
    }
    if (id === '@/utils/api-base') return { getApiBaseUrl: () => '' }
    if (id === '@/utils/agent-mode') return agentModeExports
    if (id === '@/utils/reasoningEffort') return reasoningEffort
    if (id === '@/utils/request') return { get: async () => ({ data: { enabled: true, connected: true, extension_available: true } }) }
    if (id === '../utils/sidebarWidth') return sidebarWidth
    if (id === '@/utils/authRefresh') return { invalidateAuthRefresh() {} }
    if (id === '@/utils/authTokens') return tokenExports
    if (id === '@/utils/security') {
      return {
        clearProtectedFileCache: () => { protectedCleared++ },
        setProtectedFileCacheScope() {},
      }
    }
    if (id === '@/composables/useFont') return { reloadFontFromStorage() {} }
    if (id === '@/composables/useTheme') return { reloadThemeFromStorage() {} }
    if (id === '@/composables/preferenceStorage') {
      return {
        resetMigrationLatch() {},
        safeRemoveItem: (k: string) => local.removeItem(k),
        safeSetItem: (k: string, v: string) => local.setItem(k, v),
        userKey: (s: string) => `GENYEE_user_${s}`,
      }
    }
    if (id === '@/config/deploymentCapabilities') return deploymentCapabilitiesCfg
    if (id === '@/stores/settingsStorage') return settingsStorageExports
    if (id === '@/stores/modelProvidersState') return modelProvidersState
    if (id === '@/stores/sequencedRefresh') return sequencedRefresh
    if (id === '@/stores/chatResources') return { useChatResourcesStore: () => ({ invalidate: () => { chatInvalidated++ } }) }
    if (id === '@/stores/editorResources') return { useEditorResourcesStore: () => ({ invalidate: () => { editorInvalidated++ } }) }
    if (id === '@/stores/organization') return { useOrganizationStore: () => ({ clearState: () => { orgCleared++ } }) }
    if (id === '@/stores/settings') return settingsExports
    if (id === '@/stores/modelProviders') return modelProvidersExports
    if (id === '@/stores/browserConnection') return browserConnExports
    if (id === '@/stores/menu') return menuExports
    if (id === '@/stores/ui') return uiExports
    if (id === '@/stores/uploadConfirm') return uploadConfirmExports
    if (id === '@/stores/knowledge') return knowledgeExports
    if (id === '@/stores/deploymentCapabilities') return deployCapExports
    if (id === '@/stores/auth' || id === './auth') return authExports
    throw new Error(`Unexpected require in W6-ST-01: ${id}`)
  }

  const baseCtx = {
    console,
    AbortController,
    AbortSignal,
    structuredClone,
    WeakMap,
    Map,
    Set,
    Symbol,
    Promise,
    Number,
    String,
    Boolean,
    Array,
    Object,
    Math,
    JSON,
    Date,
    Error,
    setTimeout,
    clearTimeout,
    localStorage: local,
    sessionStorage: session,
    globalThis: { localStorage: local },
    document: {
      hidden: false,
      addEventListener() {},
      removeEventListener() {},
    },
    window: {
      addEventListener() {},
      removeEventListener() {},
    },
    require: sharedRequire,
  }

  vm.runInNewContext(compileFile('../utils/agent-mode.ts'), { ...baseCtx, exports: agentModeExports })
  vm.runInNewContext(compileFile('./settingsStorage.ts'), { ...baseCtx, exports: settingsStorageExports })
  vm.runInNewContext(compileFile('./settings.ts'), { ...baseCtx, exports: settingsExports })
  vm.runInNewContext(compileFile('./modelProviders.ts'), { ...baseCtx, exports: modelProvidersExports })
  vm.runInNewContext(compileFile('./browserConnection.ts'), { ...baseCtx, exports: browserConnExports })
  vm.runInNewContext(compileFile('./deploymentCapabilities.ts'), { ...baseCtx, exports: deployCapExports })
  vm.runInNewContext(compileFile('./menu.ts'), { ...baseCtx, exports: menuExports })
  vm.runInNewContext(compileFile('./ui.ts'), { ...baseCtx, exports: uiExports })
  vm.runInNewContext(compileFile('./uploadConfirm.ts'), { ...baseCtx, exports: uploadConfirmExports })
  vm.runInNewContext(compileFile('./knowledge.ts'), { ...baseCtx, exports: knowledgeExports })
  vm.runInNewContext(compileFile('./auth.ts'), { ...baseCtx, exports: authExports })
  vm.runInNewContext(compileFile('./assistant.ts'), { ...baseCtx, exports: assistantExports })

  const rootPinia = pinia.createPinia()
  pinia.setActivePinia(rootPinia)

  const auth = authExports.useAuthStore()
  const settings = settingsExports.useSettingsStore()
  const providers = modelProvidersExports.useModelProvidersStore()
  const browserConn = browserConnExports.useBrowserConnectionStore()
  const menu = menuExports.useMenuStore()
  const ui = uiExports.useUIStore()
  const uploadConfirm = uploadConfirmExports.useUploadConfirmStore()
  const assistant = assistantExports.useAssistantStore()

  // Populate User A session state
  auth.setUser({ id: 'user-a', username: 'alice', email: 'a@example.com' })
  auth.setToken('tok-a')
  auth.setTenant({ id: '10001', name: 'Workspace A', owner_id: 'user-a', created_at: '', updated_at: '' })
  await providers.ensureLoaded('chat')
  assert.equal(providers.providersFor('chat').length, 1)

  browserConn.apply({
    enabled: true,
    connected: true,
    extension_available: true,
    device: { id: 'dev-a', label: "Alice's MacBook", last_seen_at: '2026-01-01T00:00:00Z' },
  })
  menu.updataMenuChildren({ id: 'chat-secret-a', title: 'Confidential Q4 Plan' })
  menu.changeFirstQuery('Secret prompt', [{ id: 'kb-a' }])
  menu.setPrefillQuery('Prefill secret')
  ui.openManualEditor({ kbId: 'kb-a', title: 'Secret Draft', content: 'Confidential body' })
  ui.toggleSelectedTagId('tag-a')
  const uploadOutcome = uploadConfirm.open({ mode: 'manual', kbInfo: { id: 'kb-a' } }).then(
    () => 'resolved',
    () => 'rejected',
  )
  assistant.setPageDraft({ kind: 'model', draft: { type: 'chat', base_url: 'https://internal-a.local' } })
  assistant.setPageSelection({ text: 'Selected secret paragraph' })

  // User A logs out in SPA (without page reload; User B will log into their own home workspace with selectedTenantId === null)
  auth.logout()
  await vue.nextTick()
  await Promise.resolve()

  // 1. Persisted GENYEE_settings & last_chat_model must be scrubbed on logout!
  assert.equal(local.getItem('genyee_last_chat_model_id'), null)
  const storedAfterLogout = JSON.parse(local.getItem('GENYEE_settings') || '{}')
  assert.equal(storedAfterLogout.apiKey, '')
  assert.equal(storedAfterLogout.selectedAgentId, 'builtin-quick-answer')
  assert.deepEqual(storedAfterLogout.selectedTags, [])
  assert.deepEqual(storedAfterLogout.selectedMCPServices, [])
  assert.deepEqual(storedAfterLogout.selectedSkills, [])
  assert.deepEqual(storedAfterLogout.modelConfig.chatModels, [])

  // 2. In-memory settingsStore must ALSO be scrubbed so a subsequent toggleWebSearch does NOT write User A's selections back!
  settings.toggleWebSearch(true)
  const storedAfterToggle = JSON.parse(local.getItem('GENYEE_settings') || '{}')
  assert.equal(storedAfterToggle.selectedAgentId, 'builtin-quick-answer')
  assert.deepEqual(storedAfterToggle.selectedKnowledgeBases, [])
  assert.deepEqual(storedAfterToggle.selectedTags, [])
  assert.equal(storedAfterToggle.apiKey, '')

  // 3. Account/workspace-scoped stores must be reset on logout
  assert.ok(chatInvalidated > 0)
  assert.ok(editorInvalidated > 0)
  assert.ok(orgCleared > 0)
  assert.ok(protectedCleared > 0)
  assert.equal(providers.providersFor('chat').length, 0)
  assert.equal(browserConn.loaded, false)
  assert.equal(browserConn.connected, false)
  assert.equal(browserConn.device, undefined)
  const chatMenuItem = menu.menuArr.find((m: { path?: string }) => m.path === 'creatChat')
  assert.equal(chatMenuItem.children.length, 0, 'Sidebar chat history must be cleared on logout')
  assert.equal(menu.firstQuery, '')
  assert.equal(menu.prefillQuery, '')
  assert.equal(ui.manualEditorVisible, false)
  assert.equal(ui.manualEditorInitialContent, '')
  assert.equal(ui.selectedTagIds.length, 0)
  assert.equal(uploadConfirm.visible, false)
  assert.equal(
    await Promise.race([uploadOutcome, new Promise((r) => setTimeout(() => r('pending'), 25))]),
    'rejected',
  )
  assert.equal(assistant.takePageDraft('model'), null)
  assert.equal(assistant.pageSelection, null)
})

test('W6-ST-02: cross-tab token removal clears user/tenant principals and late fetchPendingInvitationCount cannot repopulate logged-out session', async () => {
  const local = createMemoryStorage()
  type StorageListener = (e: { key: string; storageArea?: unknown }) => void
  const storageListeners = new Set<StorageListener>()
  let resolveInviteCount!: (v: unknown) => void

  const tokenExports: DynamicExports = {}
  vm.runInNewContext(compileFile('../utils/authTokens.ts'), {
    exports: tokenExports,
    localStorage: local,
  })

  const authExports: DynamicExports = {}
  const ctx = {
    exports: authExports,
    console,
    Promise,
    Number,
    String,
    Boolean,
    Array,
    Object,
    Math,
    JSON,
    Date,
    Error,
    localStorage: local,
    sessionStorage: createMemoryStorage(),
    globalThis: { localStorage: local },
    window: {
      addEventListener(type: string, fn: StorageListener) { if (type === 'storage') storageListeners.add(fn) },
      removeEventListener(type: string, fn: StorageListener) { if (type === 'storage') storageListeners.delete(fn) },
    },
    require(id: string) {
      if (id === 'vue') return vue
      if (id === 'pinia') return pinia
      if (id === '@/i18n') return { default: { global: { t: (k: string) => k } } }
      if (id === '@/api/auth') return { userInfoFromApi: (u: unknown) => u }
      if (id === '@/api/agent') return { BUILTIN_QUICK_ANSWER_ID: 'builtin-quick-answer' }
      if (id === '@/api/tenant/invitations') {
        return {
          getMyPendingInvitationCount: () => new Promise((r) => { resolveInviteCount = r }),
        }
      }
      if (id === '@/utils/authRefresh') return { invalidateAuthRefresh() {} }
      if (id === '@/utils/authTokens') return tokenExports
      if (id === '@/utils/security') return { clearProtectedFileCache() {}, setProtectedFileCacheScope() {} }
      if (id === '@/composables/useFont') return { reloadFontFromStorage() {} }
      if (id === '@/composables/useTheme') return { reloadThemeFromStorage() {} }
      if (id === '@/composables/preferenceStorage') {
        return { resetMigrationLatch() {}, safeRemoveItem: (k: string) => local.removeItem(k), userKey: (s: string) => s }
      }
      if (id === '@/stores/chatResources') return { useChatResourcesStore: () => ({ invalidate() {} }) }
      if (id === '@/stores/editorResources') return { useEditorResourcesStore: () => ({ invalidate() {} }) }
      if (id === '@/stores/organization') return { useOrganizationStore: () => ({ clearState() {} }) }
      if (id === '@/stores/sequencedRefresh') return sequencedRefresh
      // Optional stores if imported by auth.ts
      if (id === '@/stores/settings') return { useSettingsStore: () => ({ resetForSessionChange() {} }) }
      if (id === '@/stores/modelProviders') return { useModelProvidersStore: () => ({ reset() {} }) }
      if (id === '@/stores/browserConnection') return { useBrowserConnectionStore: () => ({ reset() {} }) }
      if (id === '@/stores/menu') return { useMenuStore: () => ({ resetSessionMenu() {}, clearMenuArr() {} }) }
      if (id === '@/stores/ui') return { useUIStore: () => ({ resetSessionUI() {} }) }
      if (id === '@/stores/uploadConfirm') return { useUploadConfirmStore: () => ({ rejectConfirm() {} }) }
      if (id === '@/stores/knowledge') return { knowledgeStore: () => ({ cardList: [], total: 0 }) }
      if (id === '@/stores/deploymentCapabilities') return { useDeploymentCapabilitiesStore: () => ({ reset() {} }) }
      throw new Error(`Unexpected require in W6-ST-02: ${id}`)
    },
  }
  // Allow dynamic import('@/api/tenant/invitations') transpiled to Promise.resolve().then(() => require(...))
  vm.runInNewContext(compileFile('./auth.ts'), ctx)

  pinia.setActivePinia(pinia.createPinia())
  const auth = authExports.useAuthStore()
  auth.setUser({ id: 'u-1', username: 'u1', email: 'u1@example.com', is_system_admin: true })
  auth.setToken('tok-1')
  auth.setTenant({ id: '10', name: 'T10', owner_id: 'u-1', created_at: '', updated_at: '' })
  auth.setMemberships([{ tenant_id: 10, role: 'owner' }])

  const inflightInvite = auth.fetchPendingInvitationCount()
  await Promise.resolve()
  await Promise.resolve()
  await Promise.resolve()

  // Simulate another tab logging out
  local.removeItem('genyee_token')
  local.removeItem('genyee_refresh_token')
  storageListeners.forEach((fn) => fn({ key: 'genyee_token', storageArea: local }))

  assert.equal(auth.user, null, 'Cross-tab logout must clear in-memory user')
  assert.equal(auth.tenant, null, 'Cross-tab logout must clear in-memory tenant')
  assert.equal(auth.memberships.length, 0, 'Cross-tab logout must clear memberships')
  assert.equal(auth.isSystemAdmin, false, 'Cross-tab logout must clear isSystemAdmin')

  // Late response from pre-logout fetchPendingInvitationCount arrives now
  resolveInviteCount({ success: true, data: { pending_count: 7 } })
  await inflightInvite
  assert.equal(auth.pendingInvitationCount, 0, 'Pre-logout pending invitation response must be ignored')
  auth.$dispose()
})

test('W6-ST-03: organizationStore clearState fences in-flight fetchMembers, fetchSearchableOrganizations, and preview against cross-tenant writeback', async () => {
  let resolveMembers!: (v: unknown) => void
  let resolveSearch!: (v: unknown) => void
  let resolvePreview!: (v: unknown) => void

  const orgExports: DynamicExports = {}
  vm.runInNewContext(compileFile('./organization.ts'), {
    exports: orgExports,
    AbortController,
    AbortSignal,
    Map,
    Set,
    Promise,
    Date,
    Math,
    Object,
    Array,
    require(id: string) {
      if (id === 'pinia') return pinia
      if (id === 'vue') return vue
      if (id === '@/utils/request') return { getCurrentLanguage: () => 'en-US' }
      if (id === './versionedRequest') return versionedRequest
      if (id === './localizedResourceCache') return localizedResourceCache
      if (id === './organizationState') return organizationState
      if (id === '@/api/organization') {
        return {
          listMyOrganizations: async () => ({ success: true, data: { organizations: [] } }),
          listMembers: () => new Promise((r) => { resolveMembers = r }),
          searchSearchableOrganizations: () => new Promise((r) => { resolveSearch = r }),
          previewOrganization: () => new Promise((r) => { resolvePreview = r }),
          listSharedKnowledgeBases: async () => ({ success: true, data: [] }),
          listSharedAgents: async () => ({ success: true, data: [] }),
        }
      }
      throw new Error(`Unexpected require in W6-ST-03: ${id}`)
    },
  })

  pinia.setActivePinia(pinia.createPinia())
  const orgStore = orgExports.useOrganizationStore()

  const pMembers = orgStore.fetchMembers('org-tenant-a')
  const pSearch = orgStore.fetchSearchableOrganizations('alpha')
  const pPreview = orgStore.preview('INVITE-A')

  // Tenant switch / logout clears organization store while requests are in flight
  orgStore.clearState()

  resolveMembers({ success: true, data: { members: [{ id: 'm1', tenant_id: 10001, role: 'admin' }] } })
  resolveSearch({ success: true, data: { data: [{ id: 'org-secret', name: 'Alpha Secret', is_already_member: false }] } })
  resolvePreview({ success: true, data: { id: 'org-a', name: 'Preview Secret' } })
  await Promise.all([pMembers, pSearch, pPreview])

  assert.equal(orgStore.currentMembers.length, 0, 'Pre-clearState fetchMembers must not write into cleared store')
  assert.equal(orgStore.searchableOrganizations.length, 0, 'Pre-clearState search must not write into cleared store')
  assert.equal(orgStore.previewData, null, 'Pre-clearState preview must not write into cleared store')
})

test('W6-ST-04: deploymentCapabilitiesStore retries after transient load failure instead of latching loaded=true', async () => {
  let calls = 0
  const capExports: DynamicExports = {}
  vm.runInNewContext(compileFile('./deploymentCapabilities.ts'), {
    exports: capExports,
    Promise,
    Error,
    String,
    require(id: string) {
      if (id === 'vue') return vue
      if (id === 'pinia') return pinia
      if (id === '@/config/deploymentCapabilities') return deploymentCapabilitiesCfg
      if (id === '@/stores/auth') return { useAuthStore: () => ({ isLiteMode: false }) }
      if (id === '@/api/system') {
        return {
          getDeploymentCapabilities: async () => {
            calls++
            if (calls === 1) throw new Error('temporary 503')
            return { data: { edition: 'standard', capabilities: { 'settings.sandbox': { supported: false } } } }
          },
        }
      }
      throw new Error(`Unexpected require in W6-ST-04: ${id}`)
    },
  })

  pinia.setActivePinia(pinia.createPinia())
  const capStore = capExports.useDeploymentCapabilitiesStore()

  await capStore.ensureLoaded()
  assert.equal(capStore.loaded, false, 'Transient error must not permanently mark capabilities loaded=true')
  assert.equal(capStore.isSupported('settings.sandbox'), true, 'Fail-open while unloaded')

  await capStore.ensureLoaded()
  assert.equal(calls, 2, 'Subsequent ensureLoaded() must retry after failure')
  assert.equal(capStore.loaded, true)
  assert.equal(capStore.isSupported('settings.sandbox'), false)
})
