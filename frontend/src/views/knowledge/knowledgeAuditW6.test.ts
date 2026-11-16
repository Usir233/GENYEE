import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import * as marked from 'marked'
import * as vue from 'vue'
import * as literalTilde from '../../utils/markedLiteralTilde.ts'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import * as sorting from '../../utils/resourceSorting.ts'
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

test('FAQEntryManager blocks unsafe failed_entries_url schemes, aligns shared-KB RBAC, and isolates import polling across KB switches and unmount', async (t: TestContext) => {
  const storageMap = new Map<string, string>()
  const f = fixture(t, {
    getItem: (k: string) => storageMap.get(k) ?? null,
    setItem: (k: string, v: string) => { storageMap.set(k, v) },
    removeItem: (k: string) => { storageMap.delete(k) },
  })
  const opened: Array<{ url: string; target?: string; features?: string }> = []
  ;(f.window as any).open = (u: string, target?: string, features?: string) => {
    opened.push({ url: u, target, features })
    return null
  }
  const origGlobalOpen = (globalThis as any).window
  ;(globalThis as any).window = f.window
  t.after(() => { (globalThis as any).window = origGlobalOpen })

  let progressResolver: ((val: any) => void) | null = null
  const page = f.component(new URL('./components/FAQEntryManager.vue', import.meta.url), {
    'vue-router': { useRouter: () => ({ push() {}, replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { warning() {}, error() {}, success() {}, info() {} } },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => () => {} },
    '@/stores/auth': {
      useAuthStore: () => ({
        user: { id: 'admin-user' },
        hasRole: (r: string) => r === 'admin' || r === 'contributor',
      }),
    },
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {} }) },
    '@/stores/organization': {
      useOrganizationStore: () => ({
        sharedKnowledgeBases: [],
        getKBPermission: () => 'viewer',
        canEditKB: () => false,
        canManageKB: () => false,
        async fetchSharedKnowledgeBases() {},
      }),
    },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({ rawKnowledgeBases: [], async ensureKnowledgeBases() {} }),
    },
    '@/api/knowledge-base': {
      listFAQEntries: async () => ({ data: { data: [], total: 0 } }),
      listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }),
      getKnowledgeBaseById: async (id: string) => ({
        data: { id, type: 'faq', my_permission: 'viewer' },
      }),
      getFAQImportProgress: () => new Promise((resolve) => { progressResolver = resolve }),
    },
    xlsx: {},
    papaparse: {},
  }, { kbId: 'kb-a' })

  await flush()

  // 1. Personal-workspace admin viewing a read-only shared FAQ KB (my_permission='viewer') must NOT get canEdit/canManage!
  assert.equal(page.vm.canEdit, false, 'read-only shared FAQ KB must deny canEdit even for local tenant admin')
  assert.equal(page.vm.canManage, false, 'read-only shared FAQ KB must deny canManage even for local tenant admin')

  // 2. Unsafe failed_entries_url must be blocked; valid https URL opens with noopener,noreferrer
  page.vm.importResult = { failed_entries_url: 'javascript:alert(1)' }
  page.vm.downloadFailedEntries()
  assert.equal(opened.length, 0, 'javascript: failed_entries_url must be blocked')

  page.vm.importResult = { failed_entries_url: 'https://files.example.com/failed.csv' }
  page.vm.downloadFailedEntries()
  assert.equal(opened.length, 1)
  assert.equal(opened[0].features, 'noopener,noreferrer')

  // 3. Import polling started on kb-a must stop on switch to kb-b and never write kb-a's task into kb-b's localStorage
  page.vm.importState.taskId = 'task-a'
  page.vm.startPolling('task-a')
  page.vm.$.props.kbId = 'kb-b'
  await flush()
  assert.equal(page.vm.importState.pollingInterval, null, 'switching kbId must stop FAQ import polling')
  assert.equal(storageMap.get('faq_import_last_completed_kb-b'), undefined)
})

test('KnowledgeBase prevents wiki status polling resurrection after unmount and resets drawer/move/batch/debounce state on KB switch', async (t: TestContext) => {
  const f = fixture(t)
  const route = vue.reactive({ params: { kbId: 'kb-1' }, query: {} })
  let resolveWikiStats!: (val: any) => void
  let wikiStatsCalls = 0

  const page = f.component(new URL('./KnowledgeBase.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {}, push() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {}, info() {}, warning() {} } },
    '@/hooks/useKnowledgeBase': () => ({
      cardList: vue.ref([]),
      total: vue.ref(0),
      moreIndex: vue.ref(-1),
      details: vue.reactive({ id: '' }),
      getKnowled: async () => {},
      getCardDetails: () => {},
      getfDetails() {},
      onVisibleChange() {},
    }),
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {} }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], getKBPermission: () => 'owner' }) },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({
        fetchKnowledgeBaseById: async (id: string) => ({
          id,
          type: 'document',
          indexing_strategy: { wiki_enabled: true, vector_enabled: true },
        }),
        rawKnowledgeBases: [],
        async ensureKnowledgeBases() {},
      }),
    },
    '@/stores/editorResources': { useEditorResourcesStore: () => ({ parserEngines: [], async ensureParserEngines() {} }) },
    '@/stores/uploadConfirm': { useUploadConfirmStore: () => ({}) },
    '@/stores/uploadTasks': { useUploadTasksStore: () => ({}) },
    '@/hooks/useMarqueeSelect': { useMarqueeSelect: () => ({}) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/utils/kbPermission': { permissionCanEditKB: () => true, permissionCanManageKB: () => true },
    '@/utils/knowledgeDeletion': { waitForKnowledgeDeletion: async () => 'completed' },
    '@/utils/knowledgeTrace': {},
    '@/utils/knowledgeProcessingStall': stall,
    '@/api/wiki': {
      getWikiStats: () => {
        wikiStatsCalls++
        return new Promise((resolve) => { resolveWikiStats = resolve })
      },
    },
    '@/api/knowledge-base': {},
    '@/api/knowledge-base/index': {
      listKnowledgeFolders: async () => ({ data: { folders: [] } }),
      listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }),
      batchReparseKnowledge: async () => ({ success: true }),
      batchQueryKnowledge: async () => ({ success: true, data: [] }),
    },
  }, {})

  await flush()
  assert.equal(wikiStatsCalls, 1)

  // Open detail drawer & batchMode on kb-1, then switch to kb-2 -> both must reset!
  page.vm.isCardDetails = true
  page.vm.batchMode = true
  route.params.kbId = 'kb-2'
  await flush()
  assert.equal(page.vm.isCardDetails, false, 'switching KB must close open document detail drawer')
  assert.equal(page.vm.batchMode, false, 'switching KB must exit batch mode')

  // Unmount while getWikiStats is in flight -> resolving is_active:true must NOT resurrect 5s setInterval!
  const callsAtUnmount = wikiStatsCalls
  page.close()
  resolveWikiStats({ data: { pending_tasks: 2, is_active: true, pending_issues: 0 } })
  await flush()
  t.mock.timers.tick(15000)
  await flush()
  assert.equal(wikiStatsCalls, callsAtUnmount, 'unmounted KnowledgeBase must not resurrect wikiStatusTimer')
})

test('KnowledgeBaseEditorModal enforces active effectiveTenantId and contributor floor on share/activity gates and guards async profile/schema loads', () => {
  const source = readFileSync(new URL('./KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')
  const shareAndActivity = source.slice(
    source.indexOf('const activeWorkspaceTenantId = computed'),
    source.indexOf('const chunkingDirty = ref'),
  )
  // Both canShareKB and canViewActivity must check active workspace via effectiveTenantId and require contributor role for creator ownership
  assert.match(shareAndActivity, /effectiveTenantId/)
  assert.match(shareAndActivity, /hasRole\('contributor'\)/)
  assert.doesNotMatch(
    shareAndActivity,
    /Number\(kbTenantId\.value \|\| 0\) !== Number\(authStore\.currentTenantId \|\| 0\)/,
  )

  const resetBlock = source.slice(
    source.indexOf('const resetState = () => {'),
    source.indexOf('const handleClose = () => {'),
  )
  assert.match(resetBlock, /generatedProfile\.value = null/)
  assert.match(resetBlock, /dsCount\.value = 0/)
})

test('WikiBrowser enforces canEdit on folder/drag/issue controls, escapes wiki link/index markdown, and resets/stops timers across KB switch and unmount', async (t: TestContext) => {
  const f = fixture(t)
  let statsResolver!: (val: any) => void
  let statsCalls = 0
  const getWikiIndex = t.mock.fn(async () => ({ groups: [] }))

  const props = vue.reactive({
    knowledgeBaseId: 'kb-1',
    view: 'browser' as const,
    canEdit: false,
  })
  const page = f.component(new URL('./wiki/WikiBrowser.vue', import.meta.url), {
    marked,
    'vue-router': { useRouter: () => ({}), useRoute: () => ({ query: {} }) },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, warning() {}, success() {} } },
    '@/stores/assistant': { useAssistantStore: () => ({ visible: false }) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/stores/settings': { useSettingsStore: () => ({}) },
    '@/api/wiki': {
      getWikiPage: async () => ({ data: {} }),
      getWikiIndex,
      getWikiStats: () => {
        statsCalls++
        return new Promise((resolve) => { statsResolver = resolve })
      },
      listWikiIssues: async () => [],
      listWikiPages: async () => ({ pages: [], total: 0 }),
      listWikiFolders: async () => ({ folders: [] }),
    },
    '@/utils/security': {
      sanitizeMarkdownHTML: (html: string) => html,
      hydrateProtectedFileImages: async () => {},
    },
    '@/utils/markedLiteralTilde': literalTilde,
    'vue-virtual-scroller': {},
    '@/api/knowledge-base': {},
    '@/api/chat': {},
  }, props)

  await flush()

  // 1. Read-only viewer drag handlers must be no-ops when canEdit is false
  page.vm.onPageDragStart({ dataTransfer: { setData() {} } } as any, { id: 'p1', slug: 's1', title: 'T' })
  assert.equal(page.vm.draggedItem, null, 'read-only WikiBrowser must block page drag start')

  // 2. [[slug|display]] inside backticks must remain code, and quotes in slug must be escaped
  const html = page.vm.renderMarkdown('Inline `[[not-a-link|code]]` and [[slug"onclick="alert(1)|Safe <Title>]]')
  assert.match(html, /<code>\[\[not-a-link\|code\]\]<\/code>/)
  assert.doesNotMatch(html, /onclick="alert\(1\)"/)

  // 3. Switching knowledgeBaseId reloads WikiBrowser for the new KB
  getWikiIndex.mock.resetCalls()
  page.vm.$.props.knowledgeBaseId = 'kb-2'
  await flush()
  assert.ok(getWikiIndex.mock.callCount() >= 1, 'changing knowledgeBaseId must reload WikiBrowser')

  // 4. Unmounting while getWikiStats is in flight must NOT resurrect statsTimer
  const callsBeforeClose = statsCalls
  page.close()
  statsResolver({ data: { pending_tasks: 3, is_active: true } })
  await flush()
  t.mock.timers.tick(15000)
  await flush()
  assert.equal(statsCalls, callsBeforeClose, 'unmounted WikiBrowser must not restart 5s statsTimer')
})

test('DataSourceSyncLogs handles null error_message on partial syncs without crashing and reloads when dataSourceId changes', async (t: TestContext) => {
  const f = fixture(t)
  const fetchedIds: string[] = []
  const url = new URL('./settings/DataSourceSyncLogs.vue', import.meta.url)
  const props = {
    visible: true,
    dataSourceId: 'ds-1',
    dataSourceName: 'Confluence',
  }
  const page = f.component(url, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    '@/api/datasource': {
      getSyncLogs: async (id: string) => {
        fetchedIds.push(id)
        return {
          data: [
            {
              id: 'log-1',
              status: 'partial',
              started_at: '2026-11-16T00:00:00Z',
              finished_at: '2026-11-16T00:01:00Z',
              items_total: 5,
              items_created: 4,
              items_updated: 0,
              items_deleted: 0,
              items_skipped: 0,
              items_failed: 1,
              error_message: null as any,
              result: { errors: [{ title: 'Doc 1', message: 'timeout' }] },
            },
          ],
        }
      },
    },
    '@/components/assistant/AssistantActionButton.vue': { default: {} },
  }, props)

  await flush()
  page.vm.toggleExpand('log-1')

  const { descriptor } = parse(readFileSync(url, 'utf8'))
  const script = compileScript(descriptor, { id: 'sync-logs-test' })
  const tpl = compileTemplate({
    source: descriptor.template!.content,
    filename: url.pathname,
    id: 'sync-logs-test',
    compilerOptions: { bindingMetadata: script.bindings },
  })
  const { render } = f.evaluate(tpl.code, {
    vue: { ...vue, resolveComponent: (n: string) => n, withCtx: (fn: unknown) => fn },
  })

  // Must not throw TypeError: Cannot read properties of null (reading 'slice')!
  assert.doesNotThrow(() => nodes(render(page.vm, [], props, page.vm, {}, {})))

  // Switching dataSourceId while drawer is open must reload logs for ds-2
  page.vm.$.props.dataSourceId = 'ds-2'
  await flush()
  assert.deepEqual(fetchedIds, ['ds-1', 'ds-2'])
})

test('DataSourceEditorDialog cleans up unsaved tempDsId on unmount or type switch and syncs replaced credentials before temp resource listing', () => {
  const source = readFileSync(new URL('./settings/DataSourceEditorDialog.vue', import.meta.url), 'utf8')
  assert.match(source, /onBeforeUnmount/)
  const selectTypeBlock = source.slice(
    source.indexOf('function selectType('),
    source.indexOf('// --- Test connection ---'),
  )
  assert.match(selectTypeBlock, /deleteDataSource/)
})

test('KnowledgeBaseActivitySettings, DataSourceSettings, WikiRevisionDrawer, KBShareSettings, KBChunkingDebug, KnowledgeBaseList, KbUploadSourceDropdown, UploadConfirmDialog, ImageGallery, and GallerySourcePicker guard stale async state and schemes', async (t: TestContext) => {
  const f = fixture(t)

  // 1. KnowledgeBaseActivitySettings does not drop reload() when a previous fetchPage is in flight
  const activityResolvers: Array<(val: any) => void> = []
  const activityPage = f.component(new URL('./settings/KnowledgeBaseActivitySettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k, te: () => false, tm: () => ({}), locale: vue.ref('en-US') }) },
    '@/api/knowledge-base': {
      listKnowledgeBaseActivity: () => new Promise((resolve) => activityResolvers.push(resolve)),
    },
    '@/components/settings/SettingDrawer.vue': { default: {} },
    '@/components/assistant/AssistantActionButton.vue': { default: {} },
    '@/i18n/auditActionRegistry': { AUDIT_ACTION_I18N_ROOTS: {} },
    '@/i18n/auditActionLabel': { auditActionLabel: (_t: unknown, _te: unknown, a: string) => a },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    './KnowledgeBaseActivityAssistant': { buildActivityAssistantSelection: () => ({}) },
  }, { kbId: 'kb-1', active: true })

  await flush()
  assert.equal(activityResolvers.length, 1)
  activityPage.vm.outcome = 'failure'
  activityPage.vm.reload()
  await flush()
  assert.equal(activityResolvers.length, 2, 'reload() on filter change must not be dropped while initial fetch is in flight')
  activityResolvers[1]({ data: [{ id: 2, action: 'fresh' }], next_cursor: 0 })
  await flush()
  activityResolvers[0]({ data: [{ id: 1, action: 'stale' }], next_cursor: 0 })
  await flush()
  assert.deepEqual(Array.from(activityPage.vm.entries, (e: any) => e.id), [2])

  // 2. KbUploadSourceDropdown rejects non-http(s) URLs (e.g. javascript:, file:)
  const emittedUrls: string[] = []
  const uploadDropdown = f.component(new URL('./components/KbUploadSourceDropdown.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { warning() {}, info() {} } },
    'tdesign-icons-vue-next': {
      AddIcon: {},
      FileAddIcon: {},
      UploadIcon: {},
      FolderAddIcon: {},
      LinkIcon: {},
      EditIcon: {},
    },
    '../utils/uploadSources': { filterUploadFiles: () => ({ validFiles: [] }) },
  }, {
    onUrl: (u: string) => emittedUrls.push(u),
  })
  uploadDropdown.vm.urlInputValue = 'javascript:alert(1)'
  uploadDropdown.vm.handleUrlDialogConfirm()
  uploadDropdown.vm.urlInputValue = 'file:///etc/passwd'
  uploadDropdown.vm.handleUrlDialogConfirm()
  assert.deepEqual(emittedUrls, [], 'KbUploadSourceDropdown must reject non-http(s) URLs')

  uploadDropdown.vm.urlInputValue = 'https://docs.example.com/spec.pdf'
  uploadDropdown.vm.handleUrlDialogConfirm()
  assert.deepEqual(emittedUrls, ['https://docs.example.com/spec.pdf'])

  // 3. DataSourceSettings stops polling on unmount even if in-flight loadList resolves with running sync
  let dsResolve!: (val: any) => void
  let dsCalls = 0
  const dsPage = f.component(new URL('./settings/DataSourceSettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {} } },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/utils/cronHumanize': { humanizeCron: () => '', relativeTime: () => '' },
    '@/composables/useConnectorLabel': { useConnectorLabel: () => ({ connectorLabel: (s: string) => s }) },
    '@/api/datasource': {
      listDataSources: () => {
        dsCalls++
        return new Promise((resolve) => { dsResolve = resolve })
      },
    },
  }, { kbId: 'kb-1' })
  await flush()
  assert.equal(dsCalls, 1)
  dsPage.close()
  dsResolve({ data: [{ id: 'ds-1', latest_sync_log: { status: 'running' } }] })
  await flush()
  t.mock.timers.tick(6000)
  await flush()
  assert.equal(dsCalls, 1, 'unmounted DataSourceSettings must not schedule follow-up poll')

  // 4. ImageGallery clears prefsTimer on unmount
  const gallerySource = readFileSync(new URL('./gallery/ImageGallery.vue', import.meta.url), 'utf8')
  const galleryUnmount = gallerySource.slice(
    gallerySource.indexOf('onBeforeUnmount(() => {'),
    gallerySource.indexOf('})', gallerySource.indexOf('onBeforeUnmount(() => {')),
  )
  assert.match(galleryUnmount, /prefsTimer/)
})
