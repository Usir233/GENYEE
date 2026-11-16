import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import * as schedule from '../../utils/automationSchedule.ts'

const baseAutomation = {
  id: 'automation-a',
  name: 'Daily Brief',
  instruction: 'Summarize updates',
  trigger: { type: 'cron' as const, expr: '0 9 * * *', timezone: 'UTC' },
  approval_timeout_minutes: 1440,
  limits: { max_duration_minutes: 30 },
  enabled: true,
  session_mode: 'per_run' as const,
  access_level: 'confirm' as const,
  carry_last_result: false,
  misfire_policy: 'skip' as const,
  overlap_policy: 'skip' as const,
  notify_on_success: false,
}

test('AutomationDetail drops stale loadGrants, runNow, and cancel responses after switching or closing', async (t: TestContext) => {
  const f = fixture(t)
  const grantResolvers: Array<(val: any) => void> = []
  let resolveRunNow!: (val: any) => void
  let resolveCancel!: (val: any) => void

  const props = vue.reactive({
    visible: true,
    automation: { ...baseAutomation } as any,
  })
  const page = f.component(new URL('./AutomationDetail.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k, locale: vue.ref('en-US') }) },
    'vue-router': { useRoute: () => ({ params: {}, query: {} }), useRouter: () => ({ push() {}, replace() {} }) },
    'tdesign-vue-next': {
      MessagePlugin: { success() {}, error() {}, warning() {} },
      DialogPlugin: { confirm: () => ({ hide() {} }) },
    },
    '@/api/automation': {
      listAutomationRuns: async (id: string) => ({
        data: [{ id: `existing-${id}`, status: 'running', scheduled_at: '2026-11-16T00:00:00Z', session_id: 's1' }],
        total: 1,
      }),
      listAutomationGrants: () => new Promise((resolve) => grantResolvers.push(resolve)),
      listAutomationGrantCandidates: async () => ({ data: [] }),
      runAutomationNow: () => new Promise((resolve) => { resolveRunNow = resolve }),
      cancelAutomationRun: () => new Promise((resolve) => { resolveCancel = resolve }),
    },
    '@/api/assistant': { ASSISTANT_ACCESS_LEVELS: ['read_only', 'confirm', 'auto_write'] },
    '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
    '@/utils/automationSchedule': schedule,
    '@/composables/useModalShell': { useModalShell: () => ({ markClean() {}, requestClose() {} }) },
  }, props)

  await flush()
  assert.equal(grantResolvers.length, 1)

  const pendingRunNow = page.vm.runNow()
  const pendingCancel = page.vm.cancel({ id: 'existing-automation-b', status: 'running' })

  // Switch to automation-b while automation-a's loadGrants/runNow/cancel are in flight.
  page.vm.$.props.automation = { ...baseAutomation, id: 'automation-b', name: 'Weekly Report' }
  await flush()
  assert.equal(grantResolvers.length, 2)

  // Automation B grants arrive first, then stale Automation A grants resolve.
  grantResolvers[1]({ data: [{ id: 'grant-b', tool: 'tool-b' }] })
  await flush()
  grantResolvers[0]({ data: [{ id: 'grant-a-stale', tool: 'tool-a' }] })
  await flush()
  assert.deepEqual(Array.from(page.vm.grants, (g: any) => g.id), ['grant-b'], 'stale grants from previous automation must be ignored')

  // Stale runNow and cancel from automation-a resolve now; they must not mutate automation-b's runs!
  resolveRunNow({ data: { id: 'stale-queued-from-a', status: 'queued', scheduled_at: '2026-11-16T01:00:00Z', session_id: 's-stale' } })
  resolveCancel({ data: { id: 'existing-automation-b', status: 'canceled', scheduled_at: '2026-11-16T00:00:00Z', session_id: 's1' } })
  await Promise.all([pendingRunNow, pendingCancel])
  await flush()

  assert.deepEqual(
    Array.from(page.vm.runs, (r: any) => `${r.id}:${r.status}`),
    ['existing-automation-b:running'],
    'stale runNow/cancel from previous automation must not mutate current runs',
  )
})

test('AutomationEditor clears debounced trigger validation timer on unmount', async (t: TestContext) => {
  const f = fixture(t)
  let validateCalls = 0
  const props = vue.reactive({ visible: true, automation: { ...baseAutomation } })
  const page = f.component(new URL('./AutomationEditor.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k, locale: vue.ref('en-US') }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {}, warning() {} } },
    '@/api/automation': {
      createAutomation: async () => ({ data: baseAutomation }),
      updateAutomation: async () => ({ data: baseAutomation }),
      validateAutomationTrigger: async () => {
        validateCalls++
        return { data: { next_runs: ['2026-11-17T09:00:00Z'] } }
      },
    },
    '@/api/assistant': { ASSISTANT_ACCESS_LEVELS: ['read_only', 'confirm', 'auto_write'] },
    '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
    '@/utils/automationSchedule': schedule,
    '@/composables/useModalShell': { useModalShell: () => ({ markClean() {}, requestClose() {} }) },
  }, props)

  await vue.nextTick()
  page.vm.schedule.hour = 10
  await vue.nextTick()
  // Unmount before the 400ms previewTimer fires.
  page.close()
  t.mock.timers.tick(500)
  await flush()
  assert.equal(validateCalls, 0, 'unmounted AutomationEditor must not fire validateAutomationTrigger')
})
