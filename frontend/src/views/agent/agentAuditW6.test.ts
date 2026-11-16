import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import * as sorting from '../../utils/resourceSorting.ts'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

test('AgentList denies canManageAgent on cross-tenant shared agents and routes shared deep-links to read-only detail', async (t: TestContext) => {
  const f = fixture(t)
  const route = vue.reactive<{ path: string; query: Record<string, any> }>({
    path: '/platform/agents',
    query: { edit: 'shared-1', sourceTenantId: '99' },
  })
  const replaced: any[] = []
  const sharedEntry = {
    share_id: 'sh-1',
    organization_id: 'org-1',
    org_name: 'Partner Org',
    source_tenant_id: 99,
    permission: 'viewer',
    agent: {
      id: 'shared-1',
      name: 'Partner Agent',
      tenant_id: 99,
      created_by: 'other-user',
    },
  }
  const ownAgent = {
    id: 'own-1',
    name: 'Workspace Agent',
    tenant_id: 10,
    created_by: 'teammate-user',
  }

  const page = f.component(new URL('./AgentList.vue', import.meta.url), {
    'vue-router': {
      useRoute: () => route,
      useRouter: () => ({ replace: (loc: any) => { replaced.push(loc) }, push: () => {} }),
    },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {}, warning() {} } },
    '@/stores/auth': {
      useAuthStore: () => ({
        user: { id: 'admin-user' },
        effectiveTenantId: 10,
        currentTenantId: 10,
        hasRole: (role: string) => role === 'admin' || role === 'contributor',
      }),
    },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/organization': {
      useOrganizationStore: () => ({
        organizations: [],
        sharedAgents: [sharedEntry],
        async fetchOrganizations() {},
        async fetchSharedAgents() {},
      }),
    },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({
        fetchAgentsForList: async () => ({ data: [ownAgent], disabled_own_agent_ids: [] }),
      }),
    },
    '@/stores/settings': { useSettingsStore: () => ({}) },
    '@/stores/menu': { useMenuStore: () => ({}) },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    '@/composables/useTenantModelReadiness': {
      useTenantModelReadiness: () => ({ loaded: vue.ref(true), isReadyForAgent: vue.ref(true) }),
    },
    '@/composables/useListUrlState': {
      useListUrlState: () => ({ scope: vue.ref('all'), creator: vue.ref('all'), query: vue.ref('') }),
    },
    '@/composables/useResourcePins': {
      useResourcePins: () => ({ favorites: vue.ref([]), recents: vue.ref([]), touchRecent() {}, isFavorite: () => false }),
    },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/stores/assistant': { useAssistantStore: () => ({ pageAccess: true }) },
    '@/api/agent': {},
    '@/api/chat/index': {},
    '@/api/organization': { listOrganizationSharedAgents: async () => ({ success: true, data: [] }) },
    '@/config/contextualGuides': { markContextualGuideDone() {} },
    '@/config/settingsRoute': { integrationSectionKey: (tab: string) => tab },
    '@/utils/resourceListSearch': { matchesResourceQuery: () => true },
    '@/utils/card-list-badge': { isSharedAgentEditable: (p?: string) => p === 'editor' },
    '@/utils/resourceSorting': sorting,
  }, {})

  await flush()
  await flush()

  // Admin in tenant 10 can manage own-workspace agents, but NEVER cross-tenant shared agents!
  assert.equal(page.vm.canManageAgent(ownAgent), true)
  assert.equal(page.vm.canManageAgent(sharedEntry.agent), false, 'admin must not get manage rights on cross-tenant shared agent')
  assert.equal(
    page.vm.canManageAgent({ ...sharedEntry.agent, isMine: false }),
    false,
    'shared DisplayAgent must not pass canManageAgent',
  )

  // Deep-linking to ?edit=shared-1&sourceTenantId=99 must open read-only shared detail drawer, NOT AgentEditorModal!
  assert.equal(page.vm.editorVisible, false, 'cross-tenant shared deep-link must not open AgentEditorModal')
  assert.equal(page.vm.sharedDetailVisible, true, 'cross-tenant shared deep-link should open shared detail drawer')
  assert.equal(page.vm.currentSharedAgent?.agent?.id, 'shared-1')
})

test('AgentList ignores out-of-order spaceSelection and fetchList responses', async (t: TestContext) => {
  const f = fixture(t)
  const orgResolvers: Record<string, (val: any) => void> = {}
  const listResolvers: Array<(val: any) => void> = []
  const scope = vue.ref('org-a')
  const creator = vue.ref('all')

  const page = f.component(new URL('./AgentList.vue', import.meta.url), {
    'vue-router': { useRoute: () => ({ query: {} }), useRouter: () => ({ replace() {}, push() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: {} },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/organization': {
      useOrganizationStore: () => ({
        sharedAgents: [],
        async fetchOrganizations() {},
        async fetchSharedAgents() {},
      }),
    },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({
        fetchAgentsForList: () => new Promise((resolve) => listResolvers.push(resolve)),
      }),
    },
    '@/stores/settings': { useSettingsStore: () => ({}) },
    '@/stores/menu': { useMenuStore: () => ({}) },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    '@/composables/useTenantModelReadiness': {
      useTenantModelReadiness: () => ({ loaded: vue.ref(true), isReadyForAgent: vue.ref(true) }),
    },
    '@/composables/useListUrlState': {
      useListUrlState: () => ({ scope, creator, query: vue.ref('') }),
    },
    '@/composables/useResourcePins': {
      useResourcePins: () => ({ favorites: vue.ref([]), recents: vue.ref([]) }),
    },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/stores/assistant': { useAssistantStore: () => ({ pageAccess: true }) },
    '@/api/agent': {},
    '@/api/chat/index': {},
    '@/api/organization': {
      listOrganizationSharedAgents: (orgId: string) => new Promise((resolve) => { orgResolvers[orgId] = resolve }),
    },
    '@/config/contextualGuides': { markContextualGuideDone() {} },
    '@/config/settingsRoute': {},
    '@/utils/resourceListSearch': {},
    '@/utils/card-list-badge': {},
    '@/utils/resourceSorting': sorting,
  }, {})

  await flush()
  scope.value = 'org-b'
  await flush()

  // org-b resolves first, then stale org-a resolves late.
  orgResolvers['org-b']({ success: true, data: [{ share_id: 'b1', agent: { id: 'agent-b' } }] })
  await flush()
  orgResolvers['org-a']({ success: true, data: [{ share_id: 'a1', agent: { id: 'agent-a-stale' } }] })
  await flush()
  assert.deepEqual(
    Array.from(page.vm.spaceAgentsList, (x: any) => x.agent.id),
    ['agent-b'],
    'stale spaceSelection response must not overwrite newer space',
  )

  // Creator filter triggers second fetchList; newer resolves before older.
  creator.value = 'mine'
  await flush()
  assert.equal(listResolvers.length, 2)
  listResolvers[1]({ data: [{ id: 'fresh-mine', name: 'Mine' }], disabled_own_agent_ids: [] })
  await flush()
  listResolvers[0]({ data: [{ id: 'stale-all', name: 'Stale' }], disabled_own_agent_ids: [] })
  await flush()
  assert.deepEqual(
    Array.from(page.vm.agents, (a: any) => a.id),
    ['fresh-mine'],
    'stale fetchList response must not overwrite newer creator filter list',
  )
})

test('AgentEditorModal hides share management and dirty snapshot in readOnly mode, escapes placeholder tokens without v-html, and clears timers on unmount', () => {
  const url = new URL('./AgentEditorModal.vue', import.meta.url)
  const source = readFileSync(url, 'utf8')
  const { descriptor } = parse(source, { filename: url.pathname })
  const script = compileScript(descriptor, { id: 'agent-editor-audit' })
  const compiled = compileTemplate({
    source: descriptor.template!.content,
    filename: url.pathname,
    id: 'agent-editor-audit',
    compilerOptions: { bindingMetadata: script.bindings },
  })
  assert.deepEqual(compiled.errors, [])

  // Zero raw v-html on placeholder.name tokens.
  assert.doesNotMatch(source, /v-html="`\{\{\$\{placeholder\.name\}\}\}`"/)

  // Share nav item and AgentShareSettings must be gated on !props.readOnly.
  const navBlock = source.slice(source.indexOf('const navItems = computed'), source.indexOf('const navGroups = computed'))
  assert.match(navBlock, /!props\.readOnly[\s\S]*?key:\s*'share'|key:\s*'share'/)
  assert.match(navBlock, /!props\.readOnly/)
  assert.match(source, /<div v-if="editorMode === 'edit' && !props\.readOnly && editorAgent\?\.id && !editorAgent\?\.is_builtin"/)

  // Read-only viewer inspection must not snapshot formData as dirty.
  const shellBlock = source.slice(source.indexOf('const modalShell = useModalShell({'), source.indexOf('});', source.indexOf('const modalShell = useModalShell({')))
  assert.match(shellBlock, /props\.readOnly\s*\?\s*null/)

  // Unmount must stop catalog polling and clear highlight/popup timers.
  const unmountBlock = source.slice(source.indexOf('onBeforeUnmount(() => {'), source.indexOf('})', source.indexOf('onBeforeUnmount(() => {')))
  assert.match(unmountBlock, /stopCatalogPoll\(\)/)
  assert.match(unmountBlock, /clearFieldHighlight\(\)/)
})
