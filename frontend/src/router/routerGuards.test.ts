import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as vueRouter from 'vue-router'
import * as settingsAccess from '../config/settingsAccess'
import * as settingsRoute from '../config/settingsRoute'
import * as toolbox from '../config/toolbox'

function createTestRouter(opts: {
  isLoggedIn?: boolean
  hasValidTenant?: boolean
  isSystemAdmin?: boolean
  isLiteMode?: boolean
  locationHash?: string
  savedLitePath?: string | null
}) {
  const rawSource = readFileSync(new URL('./index.ts', import.meta.url), 'utf8')
    .replace(/import\.meta\.env\.BASE_URL/g, "'/'")
    .replace(/import\.meta\.env\.DEV/g, 'false')
    .replace(/\(\)\s*=>\s*import\([^)]+\)/g, '(() => Promise.resolve(DummyView))')

  const compiled = ts.transpileModule(`const DummyView = { name: 'DummyView', render: () => null };\n${rawSource}`, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const authStore = {
    isLoggedIn: opts.isLoggedIn ?? false,
    hasValidTenant: opts.hasValidTenant ?? false,
    isSystemAdmin: opts.isSystemAdmin ?? false,
    isLiteMode: opts.isLiteMode ?? false,
    token: opts.isLoggedIn ? 'tok' : '',
    refreshToken: '',
    setUser() {},
    setToken() {},
    setRefreshToken() {},
    setTenant() {},
    setMemberships() {},
    setLiteMode() {},
    refreshFromAuthMe: async () => false,
  }

  const windowObj = {
    location: {
      hash: opts.locationHash ?? '',
    },
  }

  const exports: Record<string, any> = {}
  vm.runInNewContext(compiled, {
    exports,
    URLSearchParams,
    window: windowObj,
    localStorage: {
      getItem: (k: string) => (k === 'genyee_lite_mode' && opts.isLiteMode ? 'true' : null),
      setItem() {},
      removeItem() {},
    },
    sessionStorage: {
      getItem: (k: string) => (k === 'genyee_lite_last_path' ? (opts.savedLitePath ?? null) : null),
      setItem() {},
      removeItem() {},
    },
    require(id: string) {
      if (id === 'vue') return vue
      if (id === 'vue-router') {
        return {
          ...vueRouter,
          createWebHistory: vueRouter.createMemoryHistory,
        }
      }
      if (id === '@/stores/auth') return { useAuthStore: () => authStore }
      if (id === '@/stores/deploymentCapabilities') {
        return {
          useDeploymentCapabilitiesStore: () => ({
            ensureLoaded: async () => {},
            isSupported: () => true,
          }),
        }
      }
      if (id === '@/stores/settings') {
        return {
          useSettingsStore: () => ({
            consumeNewChatReferences: () => null,
            setNewChatReferences() {},
            clearChatReferences() {},
          }),
        }
      }
      if (id === '@/api/auth') {
        return {
          autoSetup: async () => ({ success: false }),
          userInfoFromApi: (u: any) => u,
        }
      }
      if (id === 'tdesign-vue-next') {
        return { MessagePlugin: { warning() {} } }
      }
      if (id === '@/i18n') {
        return { default: { global: { t: (k: string) => k } } }
      }
      if (id === '@/config/settingsRoute') return settingsRoute
      if (id === '@/config/settingsAccess') return settingsAccess
      if (id === '@/config/toolbox') return toolbox
      throw new Error(`Unexpected require in routerGuards.test.ts: ${id}`)
    },
  })

  return { router: exports.default as vueRouter.Router, exports, windowObj }
}

test('W6-RT-01: OIDC callback detection rejects substring/empty hash bypasses and sends unauthenticated OIDC callbacks to /login preserving hash', async () => {
  // 1. Substring false-positive `#foo_oidc_error=1` must NOT bypass auth guard
  const sub = createTestRouter({ isLoggedIn: false, locationHash: '#foo_oidc_error=1' })
  await sub.router.push('/platform/knowledge-bases#foo_oidc_error=1')
  assert.equal(
    sub.router.currentRoute.value.path,
    '/login',
    'Substring #foo_oidc_error=1 must not bypass requiresAuth guard',
  )

  // 2. Empty parameter `#oidc_error=` must NOT bypass auth guard
  const empty = createTestRouter({ isLoggedIn: false, locationHash: '#oidc_error=' })
  await empty.router.push('/platform/knowledge-bases#oidc_error=')
  assert.equal(
    empty.router.currentRoute.value.path,
    '/login',
    'Empty #oidc_error= must not bypass requiresAuth guard',
  )

  // 3. Genuine unauthenticated OIDC callback on `/` routes to `/login#oidc_result=...` instead of mounting `/platform/...`
  const realHash = '#oidc_result=eyJzdWNjZXNzIjp0cnVlfQ'
  const real = createTestRouter({ isLoggedIn: false, locationHash: realHash })
  await real.router.push(`/${realHash}`)
  assert.equal(real.router.currentRoute.value.path, '/login')
  assert.equal(real.router.currentRoute.value.hash, realHash)
})

test('W6-RT-02: router blocks non-system-admins from /platform/system/* redirects and SYSTEM_ADMIN_SETTINGS_SECTIONS', async () => {
  const nonAdmin = createTestRouter({
    isLoggedIn: true,
    hasValidTenant: true,
    isSystemAdmin: false,
  })

  for (const restricted of [
    '/platform/system',
    '/platform/system/settings',
    '/platform/system/admins',
    '/platform/system/queues',
    '/platform/settings?section=system-global',
    '/platform/settings?section=model-catalog',
    '/platform/settings?section=runtime-queues',
    '/platform/settings?section=platform-api-keys',
    '/platform/settings?section=system-audit-log',
  ]) {
    await nonAdmin.router.push(restricted)
    assert.equal(
      nonAdmin.router.currentRoute.value.path,
      '/platform/knowledge-bases',
      `Non-system-admin must be bounced from ${restricted}`,
    )
  }

  // Regular workspace settings remain accessible to non-system-admins
  await nonAdmin.router.push('/platform/settings?section=models')
  assert.equal(nonAdmin.router.currentRoute.value.path, '/platform/settings')
  assert.equal(nonAdmin.router.currentRoute.value.query.section, 'models')

  // System admins CAN open system-global
  const sysAdmin = createTestRouter({
    isLoggedIn: true,
    hasValidTenant: true,
    isSystemAdmin: true,
  })
  await sysAdmin.router.push('/platform/system/settings')
  assert.equal(sysAdmin.router.currentRoute.value.path, '/platform/settings')
  assert.equal(sysAdmin.router.currentRoute.value.query.section, 'system-global')
})
