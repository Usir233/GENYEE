import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import { canRegister } from './registrationPolicy'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./Login.vue', import.meta.url))
const rawSource = readFileSync(filename, 'utf8')
const { descriptor } = parse(rawSource, { filename })
const script = compileScript(descriptor, { id: 'login-security-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

interface MountLoginOptions {
  runtimeConfig?: Record<string, unknown>
  routeQuery?: Record<string, string>
  isLoggedIn?: boolean
  refreshFromAuthMeResult?: boolean
  oidcUrlResult?: { success: boolean; authorization_url?: string; message?: string }
  autoSetupResult?: { success: boolean; user?: Record<string, unknown>; token?: string }
  inviteByTokenResult?: { success: boolean; data?: Record<string, unknown>; message?: string }
}

function mountLogin(options: MountLoginOptions = {}) {
  const errors: string[] = []
  const warnings: string[] = []
  const successes: string[] = []
  const replacedRoutes: string[] = []
  const storage = new Map<string, string>([['GENYEE_settings', '{"staleAccount":true}']])
  const sessionStore = new Map<string, string>()
  let logoutCalls = 0

  const authStore = reactive({
    isLoggedIn: options.isLoggedIn ?? false,
    isLiteMode: true,
    hasValidTenant: true,
    user: { id: 'old-user' } as Record<string, unknown> | null,
    token: 'old-token',
    refreshToken: 'old-refresh-token',
    memberships: [{ id: 99, name: 'Old Workspace' }] as Array<Record<string, unknown>>,
    tenant: null as Record<string, unknown> | null,
    selectedTenantId: null as number | null,
    logout() {
      logoutCalls += 1
      this.isLoggedIn = false
      this.isLiteMode = false
      this.user = null
      this.token = ''
      this.refreshToken = ''
      this.memberships = []
      this.tenant = null
      this.selectedTenantId = null
    },
    setLiteMode(val: boolean) {
      this.isLiteMode = val
    },
    setUser(user: Record<string, unknown>) {
      this.user = user
    },
    setToken(tok: string) {
      this.token = tok
      this.isLoggedIn = Boolean(tok)
    },
    setRefreshToken(rt: string) {
      this.refreshToken = rt
    },
    setTenant(tenant: Record<string, unknown> | null) {
      this.tenant = tenant
    },
    setMemberships(list: Array<Record<string, unknown>>) {
      this.memberships = list
    },
    setSelectedTenant(id: number | null) {
      this.selectedTenantId = id
    },
    async refreshFromAuthMe() {
      return options.refreshFromAuthMeResult ?? true
    },
    async acceptInvitationByTokenAndRefresh() {
      return { ok: true }
    },
  })

  const winLocation = {
    origin: 'https://genyee.example.com',
    href: 'https://genyee.example.com/login',
  }

  const localStorageStub = {
    getItem: (k: string) => storage.get(k) ?? null,
    setItem: (k: string, v: string) => { storage.set(k, String(v)) },
    removeItem: (k: string) => { storage.delete(k) },
  }
  const sessionStorageStub = {
    getItem: (k: string) => sessionStore.get(k) ?? null,
    setItem: (k: string, v: string) => { sessionStore.set(k, String(v)) },
    removeItem: (k: string) => { sessionStore.delete(k) },
  }

  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    URL,
    console: { ...console, error() {} },
    localStorage: localStorageStub,
    sessionStorage: sessionStorageStub,
    document: {
      addEventListener() {},
      removeEventListener() {},
    },
    window: {
      __RUNTIME_CONFIG__: options.runtimeConfig,
      location: winLocation,
    },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-router') {
        return {
          useRouter: () => ({ replace: (p: string) => replacedRoutes.push(p) }),
          useRoute: () => ({ query: options.routeQuery ?? {} }),
        }
      }
      if (name === 'vue-i18n') {
        return {
          useI18n: () => ({
            t: (key: string) => key,
            tm: () => ({}),
            locale: ref('zh-CN'),
          }),
        }
      }
      if (name === 'tdesign-vue-next') {
        return {
          MessagePlugin: {
            error: (msg: string) => errors.push(msg),
            warning: (msg: string) => warnings.push(msg),
            success: (msg: string) => successes.push(msg),
          },
        }
      }
      if (name === './registrationPolicy') return { canRegister }
      if (name === '@/utils/docsUrl') return { docsUrl: () => 'https://docs.genyee.example.com' }
      if (name === '@/utils/loginNotify') return { notifyLoginSuccess() {} }
      if (name === '@/utils/passwordPolicy') return { newPasswordRules: () => [] }
      if (name === '@/composables/useRoleLabel') {
        return { useRoleLabel: () => ({ formatRole: (r: string) => r, roleIcon: () => '' }) }
      }
      if (name === '@/utils/api-base') return { getApiBaseUrl: () => '' }
      if (name === '@/stores/auth') return { useAuthStore: () => authStore }
      if (name === '@/api/auth') {
        return {
          login: async () => ({ success: false }),
          register: async () => ({ success: false }),
          getOIDCAuthorizationURL: async () =>
            options.oidcUrlResult ?? { success: true, authorization_url: 'https://idp.example.com/auth' },
          getOIDCConfig: async () => ({ success: true, enabled: true, provider_display_name: 'SSO' }),
          autoSetup: async () => options.autoSetupResult ?? { success: false },
          getAuthConfig: async () => ({ success: true, registration_mode: 'public', complex_password_enabled: false }),
          userInfoFromApi: (u: Record<string, unknown>, tenantId: unknown) => ({ ...u, tenant_id: tenantId }),
          getInvitationByToken: async () =>
            options.inviteByTokenResult ?? { success: true, data: { tenant_name: 'Invited Org' } },
          registerByInvite: async () => ({ success: false }),
        }
      }
      if (name === 'swiper/vue') return { Swiper: {}, SwiperSlide: {} }
      if (name === 'swiper/modules') return { Autoplay: {}, EffectFade: {}, Pagination: {} }
      return { default: '' }
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
  const app = renderer.createApp({ render: () => h(component, { ref: instance }) })
  app.mount({})
  return {
    vm: instance.value,
    authStore,
    storage,
    sessionStore,
    winLocation,
    errors,
    replacedRoutes,
    getLogoutCalls: () => logoutCalls,
    unmount: () => app.unmount(),
  }
}

async function flushPromises() {
  await new Promise<void>((resolve) => setImmediate(resolve))
  await nextTick()
}

test('handleOIDCLogin rejects non-http(s) authorization_url schemes and accepts https URLs', async () => {
  const unsafe = mountLogin({
    routeQuery: { token: 'inv-tok' },
    oidcUrlResult: { success: true, authorization_url: 'javascript:alert(document.cookie)' },
  })
  try {
    await flushPromises()
    await unsafe.vm.handleOIDCLogin()
    assert.equal(unsafe.winLocation.href, 'https://genyee.example.com/login')
    assert.equal(unsafe.sessionStore.has('genyee_pending_invite_token'), false)
    assert.ok(unsafe.errors.length > 0)
  } finally {
    unsafe.unmount()
  }

  const safe = mountLogin({
    oidcUrlResult: { success: true, authorization_url: 'https://idp.example.com/authorize?client_id=abc' },
  })
  try {
    await flushPromises()
    await safe.vm.handleOIDCLogin()
    assert.equal(safe.winLocation.href, 'https://idp.example.com/authorize?client_id=abc')
  } finally {
    safe.unmount()
  }
})

test('Login runtime GITHUB_URL/WEBSITE_URL reject unsafe schemes and external anchors use noopener noreferrer', () => {
  const ctx = mountLogin({
    runtimeConfig: {
      GITHUB_URL: 'javascript:alert(1)',
      WEBSITE_URL: 'data:text/html,<script>alert(1)</script>',
    },
  })
  try {
    assert.equal(ctx.vm.githubUrl, '')
    assert.equal(ctx.vm.websiteUrl, 'https://docs.genyee.example.com')
  } finally {
    ctx.unmount()
  }

  const template = descriptor.template?.content ?? ''
  const blankAnchors = template.match(/<a\b[^>]*target="_blank"[^>]*>/g) ?? []
  assert.ok(blankAnchors.length >= 3, 'Expected external target="_blank" anchors in Login.vue')
  for (const tag of blankAnchors) {
    assert.match(tag, /\brel="noopener noreferrer"/)
  }
})

test('persistLoginResponse clears previous account state before storing new user and autoSetup retains lite mode', async () => {
  const ctx = mountLogin()
  try {
    await flushPromises()
    await ctx.vm.persistLoginResponse({
      user: { id: 'new-user', email: 'new@example.com', tenant_id: 12 },
      token: 'new-jwt-token',
      active_tenant: { id: 12, name: 'New Workspace' },
    }, true)

    assert.ok(ctx.getLogoutCalls() >= 1, 'Expected authStore.logout() to purge stale session state')
    assert.equal(ctx.authStore.refreshToken, '')
    assert.deepEqual(ctx.authStore.memberships, [])
    assert.equal(ctx.authStore.isLiteMode, false)
    assert.equal(ctx.storage.has('GENYEE_settings'), false)
    assert.equal(ctx.authStore.token, 'new-jwt-token')
  } finally {
    ctx.unmount()
  }

  const lite = mountLogin({
    autoSetupResult: {
      success: true,
      user: { id: 'lite-user', email: 'lite@local', tenant_id: 1 },
      token: 'lite-jwt',
    },
  })
  try {
    await flushPromises()
    await flushPromises()
    assert.equal(lite.authStore.token, 'lite-jwt')
    assert.equal(lite.authStore.isLiteMode, true)
  } finally {
    lite.unmount()
  }
})

test('expired session on invite ?token= landing calls authStore.logout before showing login/register form', async () => {
  const ctx = mountLogin({
    routeQuery: { token: 'invite-123' },
    isLoggedIn: true,
    refreshFromAuthMeResult: false,
  })
  try {
    await flushPromises()
    await flushPromises()
    assert.equal(ctx.getLogoutCalls(), 1)
    assert.equal(ctx.authStore.isLoggedIn, false)
  } finally {
    ctx.unmount()
  }
})
