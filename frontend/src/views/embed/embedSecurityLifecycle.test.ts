import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

function nodes(node: any): any[] {
  if (!node || typeof node !== 'object') return []
  if (Array.isArray(node)) return node.flatMap(nodes)
  const children = node.children
  return [
    node,
    ...(Array.isArray(children)
      ? nodes(children)
      : children && typeof children === 'object'
        ? Object.values(children).flatMap((slot) => (typeof slot === 'function' ? nodes(slot()) : []))
        : []),
  ]
}

test('EmbedBotMessage blocks unsafe javascript/data schemes in web citation popover href', async (t: TestContext) => {
  const f = fixture(t)
  const floatState = vue.ref({
    visible: true,
    type: 'web' as const,
    title: 'Untrusted source',
    url: 'javascript:alert(1)',
    content: '',
    loading: false,
    error: '',
    top: 10,
    left: 20,
  })
  const url = new URL('./EmbedBotMessage.vue', import.meta.url)
  const page = f.component(url, {
    'katex/dist/katex.min.css': {},
    '@/utils/security': {
      sanitizeMarkdownHTML: (s: string) => s,
      safeMarkdownToHTML: (s: string) => s,
      createSafeImage: () => '',
      isValidImageURL: () => true,
      hydrateProtectedFileImages: async () => {},
    },
    '@/utils/chatMarkdownRenderer': {
      createChatMarkdownRenderer: () => ({}),
      renderChatMarkdown: (s: string) => s,
    },
    '@/utils/mermaidShared': {
      createMermaidCodeRenderer: () => () => '',
      ensureMermaidInitialized: () => {},
      enhanceMarkdownContainer: async () => {},
    },
    '@/composables/useEmbedCitationPopover': {
      useEmbedCitationPopover: () => ({
        float: floatState,
        rebind: () => {},
        cancelClose: () => {},
        scheduleClose: () => {},
      }),
    },
    '@/composables/useTypewriter': {
      useTypewriter: (getText: () => string) => ({ displayed: vue.computed(getText) }),
    },
    '@/directives/stableHtml': { vStableHtml: {} },
    '@/views/chat/components/RagPipelineProgress.vue': { default: {} },
    '@/views/chat/components/AgentStreamDisplay.vue': { default: {} },
    '@/views/chat/components/docInfo.vue': { default: {} },
    '@/views/chat/components/deepThink.vue': { default: {} },
  }, {})

  const { descriptor } = parse(readFileSync(url, 'utf8'))
  const script = compileScript(descriptor, { id: 'embed-bot-msg-test' })
  const compiled = compileTemplate({
    source: descriptor.template!.content,
    filename: url.pathname,
    id: 'embed-bot-msg-test',
    compilerOptions: { bindingMetadata: script.bindings },
  })
  assert.deepEqual(compiled.errors, [])
  const { render } = f.evaluate(compiled.code, {
    vue: {
      ...vue,
      Teleport: 'teleport',
      resolveComponent: (name: string) => name,
      withCtx: (fn: unknown) => fn,
      withDirectives: (node: unknown) => node,
    },
  })

  const findLink = () => nodes(render(page.vm, [], {}, page.vm, {}, {})).find(
    (n) => n.type === 'a' && String(n.props?.class || '').includes('embed-citation-float__link'),
  )
  assert.equal(findLink(), undefined, 'javascript: citation URL must not render as clickable anchor')

  floatState.value.url = 'data:text/html,<script>alert(1)</script>'
  await vue.nextTick()
  assert.equal(findLink(), undefined, 'data: citation URL must not render as clickable anchor')

  floatState.value.url = 'https://docs.example.com/guide'
  await vue.nextTick()
  const safeLink = findLink()
  assert.ok(safeLink)
  assert.equal(safeLink.props.href, 'https://docs.example.com/guide')
})

test('EmbedChatCore stops follow-up suggestion polling and ignores stale starter suggestions after unmount', async (t: TestContext) => {
  const f = fixture(t)
  let pollCalls = 0
  const starterResolvers: Array<(value: any) => void> = []
  let onTurnCompleteHook!: (msg: Record<string, unknown>) => void

  const url = new URL('./EmbedChatCore.vue', import.meta.url)
  const props = vue.reactive({
    sessionId: 'sess-1',
    sessionSig: 'sig-1',
    visitorId: 'vis-1',
    channelId: 'ch-1',
    token: 'tok-1',
    agentId: 'agent-1',
    kbIds: [] as string[],
    showSuggestedQuestions: true,
  })
  const page = f.component(url, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    '@/api/embed': {
      ensureEmbedMessageSuggestions: async () => ({ data: { status: 'generating' } }),
      getEmbedMessageSuggestions: async () => {
        pollCalls++
        return { data: { status: 'generating' } }
      },
      getEmbedSuggestedQuestions: () => new Promise((resolve) => starterResolvers.push(resolve)),
      onEmbedHostOpenWithQuery: () => () => {},
      recordEmbedMessageSuggestionEvent: async () => {},
    },
    '@/components/EmbedInputField.vue': { default: {} },
    '@/views/embed/EmbedBotMessage.vue': { default: {} },
    '@/views/embed/EmbedUserMessage.vue': { default: {} },
    '@/components/ChatReferencesDrawer.vue': { default: {} },
    '@/views/chat/components/PendingPromptDock.vue': { default: {} },
    '@/composables/usePromptDock': { providePromptDock: () => {} },
    '@/composables/useChatReferencesDrawer': { provideChatReferencesDrawer: () => {} },
    '@/composables/useEmbedChatSession': {
      useEmbedChatSession: (opts: any) => {
        onTurnCompleteHook = opts.onTurnComplete
        return {
          messagesList: vue.reactive([]),
          loading: vue.ref(false),
          isReplying: vue.ref(false),
          historyLoading: vue.ref(false),
          scrollContainer: vue.ref(null),
          userHasScrolledUp: vue.ref(false),
          shouldRenderAssistantMessage: () => true,
          shouldShowGlobalTypingIndicator: () => false,
          getUserQuery: () => '',
          handleScroll: () => {},
          scrollToBottom: () => {},
          onClickScrollToBottom: () => {},
          sendMsg: async () => {},
          handleStopGeneration: async () => {},
          setSuggestionAttribution: () => {},
        }
      },
    },
    '@/composables/useChatStreamHandler': { isAwaitingInput: () => false },
    '@/components/chat/FollowUpSuggestions.vue': { default: {} },
    '@/components/chat/MessageTimestamp.vue': { default: {} },
    '@/utils/messageTimestamp': { shouldShowConversationTimestamp: () => false },
    '@/utils/embedContext': { stripHostContextFromQuery: (q: string) => q },
  }, props)

  await flush()
  assert.equal(starterResolvers.length, 1)

  page.vm.$.props.token = 'tok-2'
  await flush()
  assert.equal(starterResolvers.length, 2)
  starterResolvers[1]({ data: { questions: [{ question: 'Fresh starter' }] } })
  await flush()
  starterResolvers[0]({ data: { questions: [{ question: 'Stale starter' }] } })
  await flush()
  assert.deepEqual(page.vm.suggestedQuestions, [{ question: 'Fresh starter' }])

  const msg: Record<string, unknown> = { id: 'msg-1', role: 'assistant', is_completed: true }
  onTurnCompleteHook(msg)
  await flush()
  t.mock.timers.tick(1000)
  await flush()
  assert.equal(pollCalls, 1)

  // Unmounting EmbedChatCore (as EmbedPage does via :key="sessionId") must stop polling immediately.
  page.close()
  t.mock.timers.tick(10000)
  await flush()
  assert.equal(pollCalls, 1, 'unmounted EmbedChatCore must not continue polling getEmbedMessageSuggestions')
})

test('EmbedPage strips CSS injection tokens from untrusted primary_color', async (t: TestContext) => {
  const f = fixture(t)
  const config = vue.ref<any>({ primary_color: '#2563eb; background: url(https://evil.example/leak)' })
  const url = new URL('./EmbedPage.vue', import.meta.url)
  const page = f.component(url, {
    'vue-router': { useRoute: () => ({ params: { channelId: 'ch-1' } }) },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    '@/views/embed/EmbedChatView.vue': { default: {} },
    '@/composables/useEmbedBridge': {
      useEmbedBridge: () => ({
        token: vue.ref('tok'),
        config,
        sessionId: vue.ref('sess-1'),
        sessionSig: vue.ref('sig-1'),
        visitorId: vue.ref('vis-1'),
        loadError: vue.ref(''),
        awaitingToken: vue.ref(false),
        bootstrapping: vue.ref(false),
        hostContext: vue.ref({}),
        startNewSession: async () => {},
        retryBootstrap: async () => {},
        sessionHistory: vue.ref([]),
        sessionBusy: vue.ref(false),
        historyError: vue.ref(''),
        switchHistorySession: async () => false,
        clearSessionHistory: async () => false,
        rememberCurrentSession: () => {},
        setSessionHasMessages: () => {},
      }),
    },
    '@/utils/protectedFileAccess': { setDefaultProtectedFileAccess: () => {} },
  }, {})

  assert.deepEqual({ ...page.vm.pageStyle }, {}, 'unsafe CSS tokens in primary_color must be rejected')
  assert.deepEqual({ ...page.vm.badgeStyle }, {}, 'unsafe CSS tokens in primary_color must be rejected')

  config.value = { primary_color: '#2563eb' }
  await vue.nextTick()
  assert.equal(page.vm.pageStyle['--embed-primary'], '#2563eb')
})
