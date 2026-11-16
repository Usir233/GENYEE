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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

async function flush() {
  await new Promise<void>((resolve) => setImmediate(resolve))
  await nextTick()
}

test('OrganizationSettingsModal encodes inviteLink, reloads when orgId changes while visible, and drops stale org responses', async () => {
  const filename = fileURLToPath(new URL('./OrganizationSettingsModal.vue', import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: 'org-modal-race-test' }).content
    .replace('__expose();', '')
    .replace('return __returned__', '__expose(__returned__); return __returned__')
  const compiled = ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const orgCalls: string[] = []
  const memberCalls: string[] = []
  const shareCalls: string[] = []
  const joinReqCalls: string[] = []

  const pendingOrgs = new Map<string, ReturnType<typeof deferred<any>>>()
  const pendingShares = new Map<string, ReturnType<typeof deferred<any>>>()
  const pendingJoinReqs = new Map<string, ReturnType<typeof deferred<any>>>()

  const orgStore = reactive({
    currentOrganization: null as Record<string, any> | null,
    currentMembers: [] as Array<Record<string, any>>,
    organizations: [] as Array<Record<string, any>>,
    clearCurrentOrganizationContext() {
      this.currentOrganization = null
      this.currentMembers = []
    },
    setCurrentOrganization(org: Record<string, any>) {
      this.currentOrganization = org
    },
    async fetchMembers(orgId: string) {
      memberCalls.push(orgId)
    },
    async reviewOrganizationJoinRequest() {
      return { success: true }
    },
  })

  const props = reactive({
    visible: true,
    orgId: 'org-1',
    mode: 'edit' as 'view' | 'edit' | 'create',
  })

  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    URL,
    HTMLElement: class {},
    setTimeout,
    clearTimeout,
    console: { ...console, error() {} },
    document: {
      activeElement: null,
      body: { style: { overflow: '' } },
    },
    window: {
      location: { origin: 'https://genyee.example.com' },
    },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-router') return { useRouter: () => ({ push() {}, replace() {} }) }
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {}, warning() {} } }
      if (name === '@/composables/useModalShell') {
        return { useModalShell: () => ({ markClean() {}, requestClose() {} }) }
      }
      if (name === '@/utils/clipboard') return { copyWithToast: async () => {} }
      if (name === '@/stores/auth') {
        return { useAuthStore: () => reactive({ user: { id: 'u1' }, currentTenantId: 1, canManageCurrentTenant: true }) }
      }
      if (name === '@/stores/organization') return { useOrganizationStore: () => orgStore }
      if (name === '@/api/organization') {
        return {
          getOrganization: (id: string) => {
            orgCalls.push(id)
            const d = deferred<any>()
            pendingOrgs.set(id, d)
            return d.promise
          },
          listOrgShares: (id: string) => {
            shareCalls.push(id)
            const d = deferred<any>()
            pendingShares.set(id, d)
            return d.promise
          },
          listOrgAgentShares: async () => ({ success: true, data: { shares: [] } }),
          listJoinRequests: (id: string) => {
            joinReqCalls.push(id)
            const d = deferred<any>()
            pendingJoinReqs.set(id, d)
            return d.promise
          },
          searchTenantsForInvite: async () => ({ success: true, data: [] }),
        }
      }
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

  try {
    await flush()
    // 1. Invite link query encoding
    instance.value.inviteCode = 'code&inject=1#hash'
    assert.equal(
      instance.value.inviteLink,
      'https://genyee.example.com/join?code=code%26inject%3D1%23hash',
    )

    // 2. Switching orgId while modal stays visible must reload org-2 and ignore late org-1 responses
    instance.value.currentSection = 'joinRequests'
    await flush()
    assert.deepEqual(joinReqCalls, ['org-1'])

    props.orgId = 'org-2'
    await flush()

    assert.deepEqual(orgCalls, ['org-1', 'org-2'])
    assert.deepEqual(memberCalls, ['org-1', 'org-2'])
    assert.deepEqual(shareCalls, ['org-1', 'org-2'])
    assert.deepEqual(joinReqCalls, ['org-1', 'org-2'])

    // Resolve org-2 first, then late org-1
    pendingOrgs.get('org-2')!.resolve({
      success: true,
      data: { id: 'org-2', name: 'Second Org', invite_code: 'c2' },
    })
    pendingShares.get('org-2')!.resolve({
      success: true,
      data: { shares: [{ id: 'kb-org-2' }] },
    })
    pendingJoinReqs.get('org-2')!.resolve({
      success: true,
      data: { requests: [{ id: 'req-org-2', requested_role: 'viewer' }] },
    })
    await flush()

    pendingOrgs.get('org-1')!.resolve({
      success: true,
      data: { id: 'org-1', name: 'Stale First Org', invite_code: 'c1' },
    })
    pendingShares.get('org-1')!.resolve({
      success: true,
      data: { shares: [{ id: 'kb-stale-org-1' }] },
    })
    pendingJoinReqs.get('org-1')!.resolve({
      success: true,
      data: { requests: [{ id: 'req-stale-org-1', requested_role: 'admin' }] },
    })
    await flush()

    assert.equal(orgStore.currentOrganization?.id, 'org-2')
    assert.equal(instance.value.formData.name, 'Second Org')
    assert.deepEqual(instance.value.sharedKnowledgeBases, [{ id: 'kb-org-2' }])
    assert.deepEqual(instance.value.joinRequests, [{ id: 'req-org-2', requested_role: 'viewer' }])

    // 3. Closing modal invalidates in-flight requests
    const closeShareTask = instance.value.fetchSharedKBs()
    const closeJoinTask = instance.value.fetchJoinRequests()
    props.visible = false
    await flush()

    instance.value.sharedKnowledgeBases = []
    instance.value.joinRequests = []

    pendingShares.get('org-2')!.resolve({
      success: true,
      data: { shares: [{ id: 'leaked-after-close' }] },
    })
    pendingJoinReqs.get('org-2')!.resolve({
      success: true,
      data: { requests: [{ id: 'leaked-req-after-close' }] },
    })
    await Promise.all([closeShareTask, closeJoinTask])
    await flush()

    assert.deepEqual(instance.value.sharedKnowledgeBases, [])
    assert.deepEqual(instance.value.joinRequests, [])
  } finally {
    app.unmount()
  }
})

test('OrganizationList clears pending searchDebounceTimer on closeInvitePreview and unmount', async (t: TestContext) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const filename = fileURLToPath(new URL('./OrganizationList.vue', import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: 'org-list-timer-test' }).content
    .replace('__expose();', '')
    .replace('return __returned__', '__expose(__returned__); return __returned__')
  const compiled = ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  let searchCalls = 0
  const orgStore = reactive({
    organizations: [],
    searchableOrganizations: [],
    loading: false,
    async fetchOrganizations() {},
    async fetchSearchableOrganizations() {
      searchCalls += 1
    },
    clearSearchableOrganizations() {
      this.searchableOrganizations = []
    },
    async joinById() {
      return { success: true }
    },
  })

  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    URL,
    Set,
    setTimeout,
    clearTimeout,
    ResizeObserver: class {
      observe() {}
      disconnect() {}
    },
    console: { ...console, error() {} },
    window: {
      addEventListener() {},
      removeEventListener() {},
    },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-router') {
        return {
          useRoute: () => ({ path: '/platform/organizations', query: {} }),
          useRouter: () => ({ push() {}, replace() {} }),
        }
      }
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {}, warning() {} } }
      if (name === '@/components/settings/useConfirmDelete') {
        return { useConfirmDelete: () => ({ confirmDelete() {} }) }
      }
      if (name === '@/stores/organization') return { useOrganizationStore: () => orgStore }
      if (name === '@/stores/auth') {
        return { useAuthStore: () => reactive({ user: { id: 'u1' }, currentTenantId: 1, canManageCurrentTenant: true }) }
      }
      if (name === '@/api/organization') {
        return {
          previewOrganization: async () => ({ success: false }),
          submitJoinRequest: async () => ({ success: true }),
        }
      }
      if (name === '@/utils/clipboard') return { copyWithToast: async () => {} }
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
  const app = renderer.createApp({ render: () => h(component, { ref: instance }) })
  app.mount({})

  await flush()
  instance.value.searchQuery = 'alpha'
  instance.value.doSearchSearchableDebounced()
  instance.value.closeInvitePreview()
  t.mock.timers.tick(350)
  await flush()
  assert.equal(searchCalls, 0, 'Expected closeInvitePreview to cancel pending debounced search')

  instance.value.searchQuery = 'beta'
  instance.value.doSearchSearchableDebounced()
  app.unmount()
  t.mock.timers.tick(350)
  await flush()
  assert.equal(searchCalls, 0, 'Expected unmount to cancel pending debounced search')
})
