import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

test('creatChat clears debounced suggested-question timer on unmount and URL-encodes sessionId on navigation', async (t: TestContext) => {
  const f = fixture(t)
  let fetchCalls = 0
  const pushed: string[] = []
  const settingsState = vue.reactive({
    selectedAgentId: 'agent-1',
    reasoningEffortOverride: '',
    settings: {
      selectedKnowledgeBases: [] as string[],
      selectedFiles: [] as string[],
      selectedTags: [] as string[],
    },
    agentConfig: {
      maxIterations: 5,
      temperature: 0.7,
      allowedTools: [] as string[],
    },
    getSuggestedQuestionsParams: () => ({}),
  })

  const url = new URL('./creatChat.vue', import.meta.url)
  const page = f.component(url, {
    'vue-router': {
      useRoute: () => ({ name: 'globalCreatChat' }),
      useRouter: () => ({ push: (target: string) => { pushed.push(target) } }),
      onBeforeRouteLeave: () => {},
    },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {} } },
    '@/components/ContextualGuide.vue': { default: {} },
    '@/components/Input-field.vue': { default: {} },
    '@/api/chat/index': { createSessions: async () => ({ data: { id: 'sess/1?x=1' } }) },
    '@/utils/desktopProjectDir': { pickHostProjectDir: async () => '' },
    '@/utils/hostWorkspace': {
      projectDirBasename: (p: string) => p,
      shouldRenderHostProjectSettings: () => false,
      withOptionalProjectDir: (d: any) => d,
    },
    '@/api/agent/index': {
      getSuggestedQuestions: async () => {
        fetchCalls++
        return { data: { questions: [] } }
      },
    },
    '@/utils/questionOrigin': { questionOriginFromSuggestion: () => null },
    '@/stores/menu': {
      useMenuStore: () => ({
        isFirstSession: false,
        updataMenuChildren: () => {},
        changeIsFirstSession: () => {},
        changeFirstQuery: () => {},
      }),
    },
    '@/stores/settings': { useSettingsStore: () => settingsState },
    '@/stores/ui': { useUIStore: () => ({ showKBEditorModal: false }) },
    '@/stores/deploymentCapabilities': {
      useDeploymentCapabilitiesStore: () => ({ isSupported: () => false }),
    },
    '@/views/knowledge/KnowledgeBaseEditorModal.vue': { default: {} },
    '@/hooks/useKnowledgeBaseCreationNavigation': {
      useKnowledgeBaseCreationNavigation: () => ({ navigateToKnowledgeBaseList: () => {} }),
    },
  }, {})

  await flush()
  assert.equal(fetchCalls, 1)

  page.vm.sendMsg('hello', '', [])
  await flush()
  assert.equal(pushed[0], `/platform/chat/${encodeURIComponent('sess/1?x=1')}`)

  // Trigger debounced fetch, then unmount before 300ms timer fires.
  settingsState.selectedAgentId = 'agent-2'
  await vue.nextTick()
  page.close()
  t.mock.timers.tick(500)
  await flush()
  assert.equal(fetchCalls, 1, 'unmounted creatChat must cancel pending debounceTimer')
})
