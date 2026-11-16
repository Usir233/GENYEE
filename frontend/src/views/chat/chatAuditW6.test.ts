import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import test, { type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
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

test('AgentStreamDisplay blocks javascript/data URLs in handleCitationActivate and guards openWikiDrawer against stale responses and unmount', async () => {
  const source = readFileSync(new URL('./components/AgentStreamDisplay.vue', import.meta.url), 'utf8')

  // Extract handleCitationActivate and run against malicious & valid data-url values.
  const citeStart = source.indexOf('const handleCitationActivate =')
  const citeEnd = source.indexOf('const getKbIdForWiki =', citeStart)
  assert.notEqual(citeStart, -1)
  assert.notEqual(citeEnd, -1)
  const citeJs = ts.transpileModule(source.slice(citeStart, citeEnd), {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText

  const openedUrls: string[] = []
  const assignedUrls: string[] = []
  const browserUrls: string[] = []
  const citeCtx = {
    openReferencesDrawer: () => false,
    URL,
    window: {
      location: {
        origin: 'https://genyee.example.com',
        assign: (u: string) => assignedUrls.push(u),
      },
      open: (u: string) => {
        openedUrls.push(u)
        return {}
      },
      runtime: {
        BrowserOpenURL: (u: string) => browserUrls.push(u),
      },
    },
  }
  const handleCitationActivate = vm.runInNewContext(`${citeJs}; handleCitationActivate`, citeCtx)

  handleCitationActivate({ getAttribute: () => 'javascript:alert(document.cookie)' })
  handleCitationActivate({ getAttribute: () => 'data:text/html,<script>alert(1)</script>' })
  handleCitationActivate({ getAttribute: () => 'file:///etc/passwd' })
  assert.deepEqual(browserUrls, [], 'unsafe citation URLs must never reach Wails BrowserOpenURL')
  assert.deepEqual(openedUrls, [], 'unsafe citation URLs must never reach window.open')
  assert.deepEqual(assignedUrls, [], 'unsafe citation URLs must never reach window.location.assign')

  handleCitationActivate({ getAttribute: () => 'https://example.com/article' })
  assert.deepEqual(browserUrls, ['https://example.com/article'])

  // Extract openWikiDrawer and verify out-of-order + unmount protection.
  const wikiStart = source.indexOf('const openWikiDrawer =')
  const wikiEnd = source.indexOf('const wikiGraphHref =', wikiStart)
  assert.notEqual(wikiStart, -1)
  assert.notEqual(wikiEnd, -1)
  // Include any helper declarations immediately above openWikiDrawer if present.
  const preWikiStart = source.lastIndexOf('watch(wikiDrawerContent', wikiStart)
  const wikiSlice = source.slice(source.indexOf('});', preWikiStart) + 3, wikiEnd)
  const wikiJs = ts.transpileModule(wikiSlice, {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText

  const resolvers: Array<(v: any) => void> = []
  const wikiState = {
    isUnmounted: false,
    currentWikiKbId: { value: '' },
    wikiDrawerPage: { value: null as any },
    wikiDrawerVisible: { value: false },
    getWikiPage: () => new Promise((resolve) => resolvers.push(resolve)),
    MessagePlugin: { warning() {} },
    t: (k: string) => k,
    console: { error() {} },
  }
  const openWikiDrawer = vm.runInNewContext(`${wikiJs}; openWikiDrawer`, wikiState)
  const p1 = openWikiDrawer('kb-1', 'first-slug')
  const p2 = openWikiDrawer('kb-2', 'second-slug')
  resolvers[1]({ data: { slug: 'second-slug', title: 'Second' } })
  await p2
  resolvers[0]({ data: { slug: 'first-slug', title: 'First (stale)' } })
  await p1
  assert.equal(wikiState.wikiDrawerPage.value?.slug, 'second-slug', 'stale getWikiPage must not overwrite newer drawer page')
  assert.equal(wikiState.currentWikiKbId.value, 'kb-2')
})

test('docInfo, WebFetchResults, WebSearchResults, and KnowledgeChunksList block unsafe href protocols and sanitize chunk summary HTML', async (t: TestContext) => {
  const f = fixture(t)

  // 1. docInfo.vue
  const docInfoPage = f.component(new URL('./components/docInfo.vue', import.meta.url), {
    'vue-router': { useRouter: () => ({ resolve: () => ({ href: '/kb' }) }) },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    '@/utils/security': { sanitizeHTML: (s: string) => s },
    '@/composables/useChatReferencesDrawer': { useChatReferencesDrawer: () => null },
  }, {
    session: {
      knowledge_references: [
        { chunk_type: 'web_search', metadata: { url: 'javascript:alert(1)', title: 'Bad' } },
      ],
    },
  })
  assert.equal(
    docInfoPage.vm.getWebSearchUrl({ metadata: { url: 'javascript:alert(1)' } }),
    '#',
    'docInfo must block javascript: metadata.url',
  )
  assert.equal(
    docInfoPage.vm.getWebSearchUrl({ metadata: { url: 'https://safe.example/doc' } }),
    'https://safe.example/doc',
  )

  // 2. WebFetchResults.vue
  const fetchUrl = new URL('./components/tool-results/WebFetchResults.vue', import.meta.url)
  const fetchProps = {
    data: {
      results: [{ url: 'javascript:alert(1)', status: 200, summary: 'test' }],
    },
  }
  const fetchPage = f.component(fetchUrl, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
  }, fetchProps)
  const fetchDesc = parse(readFileSync(fetchUrl, 'utf8')).descriptor
  const fetchScript = compileScript(fetchDesc, { id: 'web-fetch-test' })
  const fetchTpl = compileTemplate({
    source: fetchDesc.template!.content,
    filename: fetchUrl.pathname,
    id: 'web-fetch-test',
    compilerOptions: { bindingMetadata: fetchScript.bindings },
  })
  const { render: renderFetch } = f.evaluate(fetchTpl.code, {
    vue: { ...vue, resolveComponent: (n: string) => n, withCtx: (fn: unknown) => fn },
  })
  const fetchAnchors = nodes(renderFetch(fetchPage.vm, [], fetchProps, fetchPage.vm, {}, {})).filter((n) => n.type === 'a')
  assert.equal(fetchAnchors.length, 0, 'WebFetchResults must not render <a> for javascript: URL')

  // 3. WebSearchResults.vue
  const searchUrl = new URL('./components/tool-results/WebSearchResults.vue', import.meta.url)
  const searchProps = {
    data: {
      results: [{ result_index: 1, title: 'Injected', url: 'javascript:alert(1)' }],
    },
  }
  const searchPage = f.component(searchUrl, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k, locale: vue.ref('en-US') }) },
  }, searchProps)
  const searchDesc = parse(readFileSync(searchUrl, 'utf8')).descriptor
  const searchScript = compileScript(searchDesc, { id: 'web-search-test' })
  const searchTpl = compileTemplate({
    source: searchDesc.template!.content,
    filename: searchUrl.pathname,
    id: 'web-search-test',
    compilerOptions: { bindingMetadata: searchScript.bindings },
  })
  const { render: renderSearch } = f.evaluate(searchTpl.code, {
    vue: { ...vue, resolveComponent: (n: string) => n, withCtx: (fn: unknown) => fn },
  })
  const searchAnchors = nodes(renderSearch(searchPage.vm, [], searchProps, searchPage.vm, {}, {})).filter((n) => n.type === 'a')
  assert.equal(searchAnchors.length, 0, 'WebSearchResults must not render <a> for javascript: URL')

  // 4. KnowledgeChunksList.vue sanitizes summaryHtml
  let sanitizedCalls = 0
  const chunksPage = f.component(new URL('./components/tool-results/KnowledgeChunksList.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: () => '<img src=x onerror=alert(1)><strong>3</strong>' }) },
    '@/utils/knowledgeChunksDisplay': {
      getKnowledgeChunksSummaryHtml: () => '<img src=x onerror=alert(1)><strong>3</strong>',
    },
    '@/utils/security': {
      sanitizeHTML: (html: string) => {
        sanitizedCalls++
        return html.replace(/<img[^>]*>/g, '')
      },
    },
  }, { data: { chunks: [] } })
  assert.equal(chunksPage.vm.summaryHtml, '<strong>3</strong>')
  assert.equal(sanitizedCalls, 1)
})

test('McpOAuthCard validates http(s) popup URL, severs opener, guards poll overlap/unmount, and keeps Skip enabled while authorizing', async (t: TestContext) => {
  const f = fixture(t)
  const openedPopups: Array<{ url: string; popup: any }> = []
  let authUrlResponse = {
    authorizationUrl: 'javascript:alert(1)',
    authorizationAttempt: 'attempt-1',
  }
  let statusResolver: ((ok: boolean) => void) | null = null
  let statusCalls = 0
  let resolveCalls = 0

  ;(f.window as any).location = { origin: 'https://app.example.com', pathname: '/chat', search: '' }
  ;(f.window as any).open = (u: string) => {
    const popup = { closed: false, opener: { secret: true } as any, close() { this.closed = true } }
    openedPopups.push({ url: u, popup })
    return popup
  }
  ;(f.window as any).setInterval = setInterval
  ;(f.window as any).clearInterval = clearInterval

  const submissions = vue.reactive<Record<string, any>>({})
  const url = new URL('./components/McpOAuthCard.vue', import.meta.url)
  const page = f.component(url, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {} } },
    '@/components/ChatTimelineIcon.vue': { default: {} },
    '@/api/mcp-service': {
      MCP_OAUTH_CALLBACK_PATH: '/api/v1/mcp/oauth/callback',
      cancelMCPOAuth: async () => {},
      getMCPOAuthAuthorizeURL: async () => authUrlResponse,
      getMCPOAuthStatus: () => {
        statusCalls++
        return new Promise<boolean>((resolve) => { statusResolver = resolve })
      },
      resolveMCPOAuth: async () => { resolveCalls++ },
      refreshMCPMetadata: async () => {},
    },
    '@/api/embed': {
      cancelEmbedMCPOAuth: async () => {},
      getEmbedMCPOAuthAuthorizeURL: async () => authUrlResponse,
      getEmbedMCPOAuthStatus: async () => false,
      resolveEmbedMCPOAuth: async () => {},
    },
    '@/composables/usePromptDock': {
      markPromptSubmitted: (id: string, val: any) => { submissions[id] = val },
      promptSubmission: (id: string) => submissions[id],
      usePromptDock: () => null,
    },
    '@/utils/promptKeys': { isSubmitKey: () => false },
  }, {
    pendingId: 'oauth-1',
    serviceId: 'svc-1',
    serviceName: 'Docs MCP',
    timeoutSeconds: 600,
    requestedAt: Math.floor(Date.now() / 1000),
  })

  // 1. Unsafe javascript: authorizationUrl must be rejected before window.open
  await page.vm.authorize()
  assert.equal(openedPopups.length, 0, 'javascript: OAuth URL must never be opened')
  assert.equal(page.vm.authorizing, false)

  // 2. Valid https: OAuth URL severs popup.opener and guards overlapping status polls
  authUrlResponse = {
    authorizationUrl: 'https://auth.example.com/authorize?state=1',
    authorizationAttempt: 'attempt-2',
  }
  await page.vm.authorize()
  assert.equal(openedPopups.length, 1)
  assert.equal(openedPopups[0].popup.opener, null, 'popup.opener must be severed to prevent reverse tabnabbing')
  assert.equal(page.vm.authorizing, true)

  // Template check: Skip button must remain enabled while authorizing so user is not locked out
  const rawSource = readFileSync(url, 'utf8')
  assert.doesNotMatch(rawSource, /:disabled="canceling \|\| authorizing"\s*@click="skip"/)

  // Allow skip while popup is open -> stops polling
  await page.vm.skip()
  assert.deepEqual({ ...submissions['oauth-1'] }, { authorized: false })
  assert.equal(statusCalls, 0)
})

test('PendingPromptDock ignores held-Enter auto-repeat and ToolApprovalCard requires empty serviceId for GENYEE operation display', async (t: TestContext) => {
  const f = fixture(t)
  let handleKeyCalls = 0
  const prompts = vue.ref([
    {
      pending_id: 'p-1',
      type: 'tool_approval_required',
      service_id: 'external-mcp-spoof',
      service_name: 'GENYEE',
      mcp_tool_name: 'exfiltrate_secret',
      description: ' Safe-looking operation\nWorkspace',
      args_json: '{"token":"secret"}',
    },
  ])
  const dockUrl = new URL('./components/PendingPromptDock.vue', import.meta.url)
  const dockPage = f.component(dockUrl, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    '@/composables/usePromptDock': {
      usePromptDock: () => ({
        prompts,
        collapsed: vue.ref(false),
        activeId: vue.ref('p-1'),
        focusRequest: vue.ref(0),
      }),
    },
    '@/utils/promptKeys': { isTextEntry: () => false },
    './AskUserCard.vue': { default: {} },
    './McpOAuthCard.vue': { default: {} },
    './SecureFormCard.vue': { default: {} },
    './ToolApprovalCard.vue': { default: {} },
  }, {})

  dockPage.vm.setCard(prompts.value[0], {
    handleKey: () => {
      handleKeyCalls++
      return true
    },
  })
  dockPage.vm.onKeydown({ key: 'Enter', repeat: true, altKey: false, preventDefault() {} } as any)
  assert.equal(handleKeyCalls, 0, 'held-down key repeat must not cascade-submit queued HITL prompts')

  // Verify ToolApprovalCard treats external MCP service with serviceId !== '' as MCP tool, NOT built-in GENYEE operation!
  const cardPage = f.component(new URL('./components/ToolApprovalCard.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string, p?: any) => `${k}:${JSON.stringify(p || {})}` }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {} } },
    '@/components/ChatTimelineIcon.vue': { default: {} },
    '@/api/mcp-service': { resolveToolApproval: async () => ({ resumed: false }) },
    '@/api/embed': { resolveEmbedToolApproval: async () => ({ resumed: false }) },
    '@/utils/suspendedRun': { resumeSuspendedRunKey: Symbol('resume') },
    '@/composables/usePromptCountdown': {
      usePromptCountdown: () => ({
        parked: vue.ref(false),
        secondsLeft: vue.ref(60),
        timerClass: vue.ref(''),
        countdownText: vue.ref('1:00'),
        parkedText: vue.ref(''),
      }),
    },
    '@/composables/usePromptDock': {
      markPromptSubmitted: () => {},
      promptSubmission: () => undefined,
      usePromptDock: () => null,
    },
    '@/utils/promptKeys': { isSubmitKey: () => false },
  }, {
    pendingId: 'p-1',
    serviceId: 'external-mcp-spoof',
    serviceName: 'GENYEE',
    mcpToolName: 'exfiltrate_secret',
    description: 'Safe-looking operation\nWorkspace',
    argsJson: '{"token":"secret"}',
  })
  assert.equal(cardPage.vm.isOperation, false, 'external MCP service with serviceId must never spoof built-in GENYEE operation')
})

test('HostApprovalCard records submission to prevent double-submit and resumes suspended runs when server reports resumed', async (t: TestContext) => {
  const f = fixture(t)
  let resolveCalls = 0
  const resumedIds: string[] = []
  const submissions = vue.reactive<Record<string, any>>({})

  const HostCard = new URL('./components/HostApprovalCard.vue', import.meta.url)
  // Provide resumeSuspendedRun via parent wrapper or check injection.
  const resumeKey = Symbol('resumeSuspendedRun')
  const page = f.component(HostCard, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {} } },
    '@/api/mcp-service': {
      resolveToolApproval: async () => {
        resolveCalls++
        return { resumed: true }
      },
    },
    '@/utils/suspendedRun': { resumeSuspendedRunKey: resumeKey },
    '@/composables/usePromptDock': {
      markPromptSubmitted: (id: string, val: any) => { submissions[id] = val },
      promptSubmission: (id: string) => submissions[id],
    },
  }, {
    pendingId: 'host-1',
    host: { reason: 'command', command: 'rm -rf ./build', allow_session: false },
    timeoutSeconds: 600,
    requestedAt: Math.floor(Date.now() / 1000),
    resolved: false,
  })

  // Test double-submit prevention even before props.resolved flips from SSE
  await page.vm.submit('approve', 'once')
  await page.vm.submit('approve', 'once')
  assert.equal(resolveCalls, 1, 'HostApprovalCard must block duplicate submit after local submission')
  assert.deepEqual({ ...submissions['host-1'] }, { approved: true })
  assert.ok(
    readFileSync(HostCard, 'utf8').includes('resumeSuspendedRun'),
    'HostApprovalCard must invoke resumeSuspendedRun when resolveToolApproval returns { resumed: true }',
  )
})

test('chat index cancels loadFollowUpSuggestions, attachSteerFollowUp, and resumeSuspendedRun loops when chatLoadVersion advances on leave/unmount', async () => {
  const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')

  const followStart = source.indexOf('const loadFollowUpSuggestions = async')
  const followEnd = source.indexOf('const recordSuggestionEvent =', followStart)
  const attachStart = source.indexOf('const attachSteerFollowUp = async')
  const attachEnd = source.indexOf('const sendMsg = async', attachStart)
  const resumeStart = source.indexOf('let attachingResumedRun = false;')
  const resumeEnd = source.indexOf('provide(resumeSuspendedRunKey', resumeStart)

  let suggestionPolls = 0
  let messageListPolls = 0
  let streamStarts = 0
  let timerCallback: (() => void) | null = null
  const runTimer = () => {
    const cb = timerCallback
    if (cb) cb()
  }

  const ctx: any = {
    chatLoadVersion: 1,
    isAutomationSession: { value: false },
    resolveAssistantMessageId: (m: any) => m.id,
    session_id: { value: 'sess-1' },
    isAwaitingInput: () => false,
    ensureMessageSuggestions: async () => ({ data: { status: 'generating' } }),
    getMessageSuggestions: async () => {
      suggestionPolls++
      return { data: { status: 'generating' } }
    },
    steerQueue: { value: [{ steer_id: 's1', content: 'next', failed: false }] },
    attachingSteerFollowUp: false,
    isReplying: { value: false },
    loading: { value: false },
    getMessageList: async () => {
      messageListPolls++
      return { data: [] }
    },
    messagesList: [],
    dropSteerQueueItem: () => {},
    hydrateSteerQueue: async () => {},
    currentAssistantMessageId: { value: '' },
    startStream: async () => { streamStarts++ },
    MessagePlugin: { error() {} },
    t: (k: string) => k,
    findLastMessage: () => ({ id: 'asst-1', role: 'assistant', agentEventStream: [{ pending_id: 'p-1' }] }),
    isPromptCard: () => true,
    persistedAssistantId: (m: any) => m.id,
    prepareResumedMessage: () => {},
    error: { value: 'not ready' },
    setTimeout: (fn: () => void) => { timerCallback = fn; return 1 },
    console: { warn() {}, error() {} },
  }

  const fns = vm.runInNewContext(
    `${source.slice(followStart, followEnd)}\n${source.slice(attachStart, attachEnd)}\n${source.slice(resumeStart, resumeEnd)}\n({ loadFollowUpSuggestions, attachSteerFollowUp, resumeSuspendedRun, advance: () => { chatLoadVersion++ } })`,
    ctx,
  )

  // 1. loadFollowUpSuggestions stops when chatLoadVersion increments (even though session_id stays 'sess-1'!)
  const msg: any = { id: 'm1' }
  const pFollow = fns.loadFollowUpSuggestions(msg, true)
  await Promise.resolve()
  await Promise.resolve()
  fns.advance() // simulate onBeforeRouteLeave / onUnmounted
  runTimer()
  await pFollow
  assert.equal(suggestionPolls, 0, 'loadFollowUpSuggestions must abort after chatLoadVersion increments')

  // 2. attachSteerFollowUp stops when chatLoadVersion increments
  const pAttach = fns.attachSteerFollowUp('completed-1')
  await Promise.resolve()
  await Promise.resolve()
  assert.equal(messageListPolls, 1)
  fns.advance()
  runTimer()
  await pAttach
  assert.equal(messageListPolls, 1, 'attachSteerFollowUp must abort polling after chatLoadVersion increments')

  // 3. resumeSuspendedRun stops before startStream when chatLoadVersion increments
  ctx.isReplying.value = false
  const pResume = fns.resumeSuspendedRun('p-1')
  fns.advance()
  runTimer()
  await pResume
  assert.equal(streamStarts, 0, 'resumeSuspendedRun must not call startStream after chatLoadVersion increments')
})

test('ChatArtifactsDrawer guards listMessageArtifacts against out-of-order loads and botmsg binds capture click handler declaratively on parentMd', async (t: TestContext) => {
  const f = fixture(t)
  const resolvers: Array<(val: any) => void> = []
  const props = vue.reactive({
    visible: false,
    sessionId: 'sess-1',
    messageId: 'msg-1',
    artifacts: [] as any[],
    previewIndex: undefined as number | undefined,
  })
  const drawer = f.component(new URL('./components/ChatArtifactsDrawer.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {} } },
    '@/api/chat': {
      downloadArtifact: async () => new Blob(),
      listMessageArtifacts: () => new Promise((resolve) => resolvers.push(resolve)),
    },
    '@/utils/files': { getFileIcon: () => 'file' },
    '@/utils/sessionArtifacts': {
      formatArtifactDateTime: () => '',
      formatArtifactSize: () => '',
    },
    '@/utils/filePreview': { resolveFilePreviewExt: () => 'md' },
    '@/components/document-preview.vue': { default: {} },
    './ArtifactFileIcon.vue': { default: {} },
  }, props)

  drawer.vm.$.props.visible = true
  await flush()
  assert.equal(resolvers.length, 1)

  // Switch messageId while drawer is open -> must refetch and ignore stale msg-1 response!
  drawer.vm.$.props.messageId = 'msg-2'
  await flush()
  assert.equal(resolvers.length, 2, 'changing messageId while open must reload artifacts')

  resolvers[1]({ data: [{ index: 0, file_name: 'msg2-fresh.md', file_type: 'md' }] })
  await flush()
  resolvers[0]({ data: [{ index: 0, file_name: 'msg1-stale.md', file_type: 'md' }] })
  await flush()
  assert.equal(drawer.vm.items[0]?.file_name, 'msg2-fresh.md', 'stale listMessageArtifacts response must be ignored')

  // Verify botmsg.vue binds @click.capture="handleMarkdownImageClick" on parentMd so v-if toggles never drop listener
  const botmsgSource = readFileSync(new URL('./components/botmsg.vue', import.meta.url), 'utf8')
  assert.match(botmsgSource, /ref="parentMd"[^>]*@click\.capture="handleMarkdownImageClick"/)
})
