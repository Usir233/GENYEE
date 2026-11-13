import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'
import * as pinia from 'pinia'

const BUILTIN_QUICK_ANSWER_ID = 'builtin-quick-answer'

function setupTest(legacyTokenMarker = false) {
  const memoryStorage = new Map<string, string>()
  if (legacyTokenMarker) memoryStorage.set('__genyee_token_sync_installed', 'true')
  const mockLocalStorage = {
    getItem: (key: string) => memoryStorage.get(key) ?? null,
    setItem: (key: string, value: string) => {
      memoryStorage.set(key, String(value))
    },
    removeItem: (key: string) => {
      memoryStorage.delete(key)
    },
    clear: () => {
      memoryStorage.clear()
    },
  }

  let chatInvalidated = 0
  let editorInvalidated = 0
  let orgCleared = 0
  let protectedCacheCleared = 0
  let activeProtectedScope = ''

  const chatStoreMock = {
    invalidate: () => { chatInvalidated++ },
  }
  const editorStoreMock = {
    invalidate: () => { editorInvalidated++ },
  }
  const orgStoreMock = {
    clearState: () => { orgCleared++ },
  }
  const securityMock = {
    clearProtectedFileCache: () => { protectedCacheCleared++ },
    setProtectedFileCacheScope: (uid?: string | number | null, tid?: string | number | null) => {
      activeProtectedScope = uid || tid ? `${uid || 'anon'}:${tid || 'default'}` : ''
    },
  }

  const compile = (source: string) => ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const authSource = readFileSync(new URL('./auth.ts', import.meta.url), 'utf8')
  const compiled = compile(authSource)
  const tokenExports: any = {}
  runInNewContext(compile(readFileSync(new URL('../utils/authTokens.ts', import.meta.url), 'utf8')), {
    exports: tokenExports,
    localStorage: mockLocalStorage,
  })
  const storageListeners = new Set<(event: { key: string | null }) => void>()

  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    console,
    globalThis: { localStorage: mockLocalStorage },
    localStorage: mockLocalStorage,
    window: {
      addEventListener(type: string, fn: (event: { key: string | null }) => void) {
        if (type === 'storage') storageListeners.add(fn)
      },
      removeEventListener(type: string, fn: (event: { key: string | null }) => void) {
        if (type === 'storage') storageListeners.delete(fn)
      },
    },
    require(name: string) {
      if (name === 'vue') return vue
      if (name === 'pinia') return pinia
      if (name === '@/i18n') return { default: { global: { t: (k: string) => k } } }
      if (name === '@/api/auth') return { userInfoFromApi: (u: any) => u }
      if (name === '@/api/agent') return { BUILTIN_QUICK_ANSWER_ID }
      if (name === '@/utils/authRefresh') return { invalidateAuthRefresh() {} }
      if (name === '@/utils/authTokens') return tokenExports
      if (name === '@/utils/security') return securityMock
      if (name === '@/composables/useFont') return { reloadFontFromStorage() {} }
      if (name === '@/composables/useTheme') return { reloadThemeFromStorage() {} }
      if (name === '@/composables/preferenceStorage') {
        return {
          resetMigrationLatch() {},
          safeRemoveItem: (key: string) => mockLocalStorage.removeItem(key),
          userKey: (suffix: string) => `GENYEE_user_${suffix}`,
        }
      }
      if (name === '@/stores/chatResources') return { useChatResourcesStore: () => chatStoreMock }
      if (name === '@/stores/editorResources') return { useEditorResourcesStore: () => editorStoreMock }
      if (name === '@/stores/organization') return { useOrganizationStore: () => orgStoreMock }
      if (name === '@/stores/sequencedRefresh') {
        return {
          createSequencedRefresh: () => ({
            refresh: async () => false,
            share: async () => false,
            invalidate() {},
          }),
        }
      }
      throw new Error(`Unexpected require: ${name}`)
    },
  })

  const rootPinia = pinia.createPinia()
  pinia.setActivePinia(rootPinia)

  const authStore = exports.useAuthStore()

  return {
    authStore,
    mockLocalStorage,
    writeAuthTokens: tokenExports.writeAuthTokens as (tokens: { token: string; refreshToken: string }) => void,
    dispatchStorage: (key: string | null) => storageListeners.forEach(fn => fn({ key })),
    storageListeners,
    getInvalidations: () => ({
      chatInvalidated,
      editorInvalidated,
      orgCleared,
      protectedCacheCleared,
      activeProtectedScope,
    }),
  }
}

test('FE-03: Multi-tenant cache pollution: setSelectedTenant evicts chat models, KBs, and settings', () => {
  const { authStore, mockLocalStorage, getInvalidations } = setupTest()

  // 1. Initial State for Tenant 10001
  mockLocalStorage.setItem('genyee_last_chat_model_id', 'tenant-10001-custom-llm')
  mockLocalStorage.setItem('genyee_current_kb', JSON.stringify({ id: 'kb-private-10001', name: 'Private KB' }))

  const initialSettings = {
    selectedAgentId: 'agent-10001-custom',
    selectedAgentSourceTenantId: 10001,
    isAgentEnabled: true,
    conversationModels: {
      selectedChatModelId: 'custom-llm-10001',
      summaryModelId: 'summary-10001',
      rerankModelId: 'rerank-10001',
    },
    selectedKnowledgeBases: ['kb-10001-a', 'kb-10001-b'],
    selectedFiles: ['secret-contract.pdf'],
    selectedFileKbMap: { 'secret-contract.pdf': 'kb-10001-a' },
    knowledgeBaseId: 'kb-10001-a',
  }
  mockLocalStorage.setItem('GENYEE_settings', JSON.stringify(initialSettings))

  // Establish active tenant 10001
  authStore.setSelectedTenant(10001, 'Tenant 10001')
  assert.equal(authStore.selectedTenantId, 10001)

  // Re-seed tenant-scoped keys to simulate active session work
  mockLocalStorage.setItem('genyee_last_chat_model_id', 'tenant-10001-custom-llm')
  mockLocalStorage.setItem('genyee_current_kb', JSON.stringify({ id: 'kb-private-10001' }))
  mockLocalStorage.setItem('GENYEE_settings', JSON.stringify(initialSettings))

  // 2. Switch to Tenant 20002
  authStore.setSelectedTenant(20002, 'Tenant 20002')

  // 3. Verify complete cache eviction
  assert.equal(authStore.selectedTenantId, 20002)
  assert.equal(mockLocalStorage.getItem('genyee_selected_tenant_id'), '20002')

  // Tenant-scoped keys must be removed
  assert.equal(mockLocalStorage.getItem('genyee_last_chat_model_id'), null, 'Chat model ID must be evicted')
  assert.equal(mockLocalStorage.getItem('genyee_current_kb'), null, 'Current KB selection must be evicted')

  // GENYEE_settings must be scrubbed of previous tenant references
  const rawSettings = mockLocalStorage.getItem('GENYEE_settings')
  assert.ok(rawSettings)
  const settings = JSON.parse(rawSettings)

  assert.equal(settings.selectedAgentId, BUILTIN_QUICK_ANSWER_ID, 'Agent ID must reset to default')
  assert.equal(settings.selectedAgentSourceTenantId, null, 'Source tenant ID must be null')
  assert.equal(settings.isAgentEnabled, false, 'isAgentEnabled must be false')
  assert.equal(settings.conversationModels.selectedChatModelId, '', 'Selected chat model must be reset')
  assert.equal(settings.conversationModels.summaryModelId, '', 'Summary model must be reset')
  assert.equal(settings.conversationModels.rerankModelId, '', 'Rerank model must be reset')
  assert.deepEqual(settings.selectedKnowledgeBases, [], 'Selected KBs must be emptied')
  assert.deepEqual(settings.selectedFiles, [], 'Selected files must be emptied')
  assert.deepEqual(settings.selectedFileKbMap, {}, 'Selected file KB map must be emptied')
  assert.equal(settings.knowledgeBaseId, '', 'knowledgeBaseId must be reset')

  // Verify Pinia resource stores and protected file blob cache were invalidated
  const inv = getInvalidations()
  assert.ok(inv.chatInvalidated >= 1, 'Chat resources store must be invalidated')
  assert.ok(inv.editorInvalidated >= 1, 'Editor resources store must be invalidated')
  assert.ok(inv.orgCleared >= 1, 'Organization store must be cleared')
  assert.ok(inv.protectedCacheCleared >= 1, 'Protected file blob cache must be cleared on tenant switch')
  assert.equal(inv.activeProtectedScope, 'anon:20002', 'Protected file cache scope must update to new tenant')
})

test('FE-03: Switching to null tenant clears selected tenant and purges cache', () => {
  const { authStore, mockLocalStorage, getInvalidations } = setupTest()

  authStore.setSelectedTenant(10001, 'Tenant 10001')
  mockLocalStorage.setItem('genyee_last_chat_model_id', 'some-model')

  authStore.setSelectedTenant(null, null)

  assert.equal(authStore.selectedTenantId, null)
  assert.equal(mockLocalStorage.getItem('genyee_selected_tenant_id'), null)
  assert.equal(mockLocalStorage.getItem('genyee_selected_tenant_name'), null)
  assert.equal(mockLocalStorage.getItem('genyee_last_chat_model_id'), null)
})

test('FE-03: Idempotent setSelectedTenant does NOT clear cache when switching to same tenant', () => {
  const { authStore, mockLocalStorage, getInvalidations } = setupTest()

  authStore.setSelectedTenant(50001, 'Tenant 50001')
  mockLocalStorage.setItem('genyee_last_chat_model_id', 'preserve-this-model')

  const beforeInv = getInvalidations()

  // Select same tenant again
  authStore.setSelectedTenant(50001, 'Tenant 50001')

  assert.equal(mockLocalStorage.getItem('genyee_last_chat_model_id'), 'preserve-this-model')
  const afterInv = getInvalidations()
  assert.equal(afterInv.chatInvalidated, beforeInv.chatInvalidated, 'No invalidation on idempotent tenant select')
  assert.equal(afterInv.protectedCacheCleared, beforeInv.protectedCacheCleared, 'No protected cache wipe on idempotent tenant select')
})

test('MED-16: logout revokes protected file blob cache and resets user:tenant scope', () => {
  const { authStore, getInvalidations } = setupTest()
  authStore.setUser({ id: 'user-99', username: 'alice', email: 'alice@example.com' })
  authStore.setSelectedTenant(30001, 'Tenant 30001')
  assert.equal(getInvalidations().activeProtectedScope, 'user-99:30001')
  const before = getInvalidations().protectedCacheCleared

  authStore.logout()

  const after = getInvalidations()
  assert.ok(after.protectedCacheCleared > before, 'logout must clear protected file cache')
  assert.equal(after.activeProtectedScope, '', 'logout must clear active user:tenant scope')
})

test('FE-03: Resilient error handling when GENYEE_settings contains malformed JSON', () => {
  const { authStore, mockLocalStorage } = setupTest()

  authStore.setSelectedTenant(60001, 'Tenant 60001')
  mockLocalStorage.setItem('GENYEE_settings', 'CORRUPT_JSON{{{>>>')

  assert.doesNotThrow(() => {
    authStore.setSelectedTenant(60002, 'Tenant 60002')
  })
})

test('background token rotation synchronizes the store after reload and unsubscribes on disposal', () => {
  const f = setupTest(true)
  // A legacy persistent marker must never suppress a fresh store's subscription.
  f.writeAuthTokens({ token: 'access-2', refreshToken: 'refresh-2' })
  assert.equal(f.authStore.token, 'access-2')
  assert.equal(f.authStore.refreshToken, 'refresh-2')

  f.mockLocalStorage.setItem('genyee_token', 'other-tab-access')
  f.mockLocalStorage.setItem('genyee_refresh_token', 'other-tab-refresh')
  f.dispatchStorage('genyee_token')
  assert.equal(f.authStore.token, 'other-tab-access')
  assert.equal(f.authStore.refreshToken, 'other-tab-refresh')
  f.mockLocalStorage.clear()
  f.dispatchStorage(null)
  assert.equal(f.authStore.token, '')
  assert.equal(f.authStore.refreshToken, '')

  f.authStore.$dispose()
  f.writeAuthTokens({ token: 'later', refreshToken: 'later-refresh' })
  assert.equal(f.authStore.token, '')
  assert.equal(f.storageListeners.size, 0)
})
