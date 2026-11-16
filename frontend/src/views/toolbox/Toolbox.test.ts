import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

test('Toolbox refreshes summary badges on workspace switch and ignores late responses after unmount', async (t: TestContext) => {
  const f = fixture(t)
  const authState = vue.reactive({
    user: { id: 'user-1' },
    effectiveTenantId: 1,
    currentTenantRole: 'admin',
    canAccessAllTenants: true,
    hasRole: () => true,
  })
  const skillResolvers: Array<(val: any) => void> = []
  const mcpResolvers: Array<(val: any) => void> = []

  const url = new URL('./Toolbox.vue', import.meta.url)
  const page = f.component(url, {
    'vue-router': {
      useRoute: () => ({ params: { section: 'skills' }, query: {} }),
      useRouter: () => ({ replace: async () => {} }),
    },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    '@/stores/auth': { useAuthStore: () => authState },
    '@/stores/browserConnection': {
      useBrowserConnectionStore: () => ({
        loaded: true,
        enabled: false,
        connected: false,
        device: null,
        refresh: async () => {},
      }),
    },
    '@/stores/deploymentCapabilities': {
      useDeploymentCapabilitiesStore: () => ({ isSupported: () => true }),
    },
    '@/api/skill': {
      listSkillCatalog: () => new Promise((resolve) => skillResolvers.push(resolve)),
    },
    '@/api/mcp-service': {
      listMCPServices: () => new Promise((resolve) => mcpResolvers.push(resolve)),
    },
    '@/config/toolbox': {
      TOOLBOX_ITEMS: [
        { key: 'skills', title: 'toolbox.skills', icon: 'code' },
        { key: 'mcp', title: 'toolbox.mcp', icon: 'extension' },
      ],
      canAccessToolboxSection: () => true,
      toolboxLocation: (section: string) => `/platform/toolbox/${section}`,
    },
    '@/components/icons/ResourceIcon.vue': { default: {} },
    '@/components/icons/BrowserIcon.vue': { default: {} },
    '@/components/EmptyState.vue': { default: {} },
    '@/views/settings/SkillSettings.vue': { default: {} },
    '@/views/settings/McpSettings.vue': { default: {} },
    '@/views/settings/BrowserConnectionSettings.vue': { default: {} },
  }, {})

  await flush()
  assert.equal(skillResolvers.length, 1)
  assert.equal(mcpResolvers.length, 1)

  // Switch active workspace while tenant-1 requests are still in flight.
  authState.effectiveTenantId = 2
  await flush()
  assert.equal(skillResolvers.length, 2, 'workspace switch must refetch skills count')
  assert.equal(mcpResolvers.length, 2, 'workspace switch must refetch mcp count')

  // Tenant-2 responds first, then stale Tenant-1 responds late.
  skillResolvers[1]({ data: [{ id: 's1' }, { id: 's2' }] })
  mcpResolvers[1]([{ id: 'm1' }])
  await flush()
  skillResolvers[0]({ data: [{ id: 'stale-1' }, { id: 'stale-2' }, { id: 'stale-3' }, { id: 'stale-4' }] })
  mcpResolvers[0]([{ id: 'stale-m1' }, { id: 'stale-m2' }, { id: 'stale-m3' }])
  await flush()
  assert.equal(page.vm.counts.skills, 2, 'stale workspace skills count must not overwrite active workspace')
  assert.equal(page.vm.counts.mcp, 1, 'stale workspace mcp count must not overwrite active workspace')

  // Switch again, then unmount before response arrives.
  authState.effectiveTenantId = 3
  await flush()
  assert.equal(skillResolvers.length, 3)
  page.close()
  skillResolvers[2]({ data: [{ id: 'after-unmount' }] })
  await flush()
  assert.equal(page.vm.counts.skills, undefined, 'unmounted Toolbox must drop late summary responses')
})
