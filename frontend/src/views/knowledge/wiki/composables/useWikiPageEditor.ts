import { computed, nextTick, onUnmounted, reactive, ref, watch, type ComputedRef, type Ref } from 'vue'
import type { WikiIndexEntryDTO, WikiIndexGroup, WikiPage } from '@/api/wiki'
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess'
import { resolveWikiBacklinkTitle } from '../wikiBacklinkTitles'

const INDEX_SECTION_ORDER = ['summary', 'entity', 'concept', 'synthesis', 'comparison'] as const

export interface WikiMessageLike {
  success: (msg: string) => unknown
  warning: (msg: string) => unknown
  error: (msg: string) => unknown
}

export interface WikiPageEditorApi {
  getWikiPage: (kbId: string, slug: string) => Promise<unknown>
  getWikiIndex: (
    kbId: string,
    opts?: { types?: string[]; limit?: number; cursor?: string },
  ) => Promise<unknown>
  createWikiPage: (
    kbId: string,
    payload: { slug: string; title: string; page_type?: string; summary?: string; content: string },
  ) => Promise<unknown>
  updateWikiPage: (
    kbId: string,
    slug: string,
    payload: { title?: string; summary?: string; content?: string; version: number },
  ) => Promise<unknown>
  deleteWikiPage: (kbId: string, slug: string) => Promise<unknown>
  getKnowledgeDetails?: (id: string) => Promise<unknown>
}

export interface WikiCreatePageFormState {
  title: string
  slug: string
  pageType: string
  content: string
}

export interface WikiEditPageFormState {
  title: string
  summary: string
  content: string
}

export interface UseWikiPageEditorOptions {
  getKbId: () => string
  pages: Ref<WikiPage[]>
  selectedPage: Ref<WikiPage | null>
  kbFileAccess: ComputedRef<ProtectedFileAccessContext>
  t: (key: string, params?: Record<string, unknown>) => string
  message: WikiMessageLike
  api: WikiPageEditorApi
  logger?: Pick<Console, 'error'>
  parseMarkdown: (preprocessed: string) => string
  sanitizeHtml: (html: string) => string
  hydrateImages: (el: HTMLElement, ctx: ProtectedFileAccessContext) => Promise<void>
  onLoadPageIssues: (slug: string) => Promise<void>
  onUpdateSidebarPageTitle: (slug: string, title: string) => void
  onReloadPages: () => Promise<void> | void
}

export function unwrapWikiPayload<T>(res: unknown): T {
  if (res && typeof res === 'object' && 'data' in res) {
    const inner = (res as { data?: unknown }).data
    if (inner !== undefined) return inner as T
  }
  return res as T
}

function getWikiErrorStatus(err: unknown): number | undefined {
  if (err && typeof err === 'object' && 'status' in err) {
    const status = (err as { status?: unknown }).status
    return typeof status === 'number' ? status : undefined
  }
  return undefined
}

function getWikiConflictVersion(err: unknown): number {
  if (!err || typeof err !== 'object') return 0
  const rec = err as {
    current_version?: unknown
    details?: { current_version?: unknown } | null
    error?: { details?: { current_version?: unknown } | null } | null
  }
  const direct = rec.current_version
  if (typeof direct === 'number') return direct
  const fromDetails = rec.details?.current_version
  if (typeof fromDetails === 'number') return fromDetails
  const fromNestedError = rec.error?.details?.current_version
  if (typeof fromNestedError === 'number') return fromNestedError
  return 0
}

export function getWikiErrorMessage(err: unknown): string | undefined {
  if (err && typeof err === 'object' && 'message' in err) {
    const msg = (err as { message?: unknown }).message
    return typeof msg === 'string' && msg ? msg : undefined
  }
  return undefined
}

function escapeWikiHtml(raw: string): string {
  return String(raw)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

function escapeMarkdownText(raw: string): string {
  return String(raw).replace(/([\\`*_{}[\]<>|])/g, '\\$1').replace(/\r?\n+/g, ' ')
}

function stripDuplicateLeadingTitle(content: string, title: string): string {
  if (!content || !title) return content
  const lines = content.split('\n')
  let i = 0
  while (i < lines.length && lines[i].trim() === '') i++
  if (i >= lines.length) return content
  const headingMatch = lines[i].match(/^#\s+(.+?)\s*$/)
  if (!headingMatch) return content
  const heading = headingMatch[1].trim()
  const pageTitle = title.trim()
  if (heading !== pageTitle && heading.toLowerCase() !== pageTitle.toLowerCase()) return content
  i++
  while (i < lines.length && lines[i].trim() === '') i++
  return lines.slice(i).join('\n')
}

function stripLegacyIndexDirectory(intro: string): string {
  if (!intro) return ''
  const idx = intro.indexOf('\n## ')
  if (idx < 0) return intro.trim()
  return intro.slice(0, idx).trim()
}

export function formatWikiDate(dateStr: string): string {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export function useWikiPageEditor(options: UseWikiPageEditorOptions) {
  const { pages, selectedPage } = options
  const logger = options.logger ?? console

  const navHistory = ref<WikiPage[]>([])
  const navFromSystemView = ref<'' | 'index'>('')
  const activeSystemView = ref<'' | 'index'>('')

  const indexMarkdown = ref('')
  const indexLoading = ref(false)
  const indexAvailable = ref(false)
  const indexSections = ref<Record<string, { loaded: boolean; cursor: string; total: number }>>({})
  const indexSectionIdx = ref(0)
  const indexBodyRef = ref<HTMLElement | null>(null)
  const indexSentinelRef = ref<HTMLElement | null>(null)
  const readerBodyRef = ref<HTMLElement | null>(null)
  let indexObserver: IntersectionObserver | null = null

  const imagePreviewVisible = ref(false)
  const imagePreviewUrl = ref('')

  const sourceRefTitleCache = reactive<Record<string, string>>({})
  let sourceRefTitleRequestSeq = 0

  const editingPage = ref(false)
  const savingPage = ref(false)
  const editForm = ref<WikiEditPageFormState>({ title: '', summary: '', content: '' })
  const editBaseVersion = ref(0)
  const editConflictVersion = ref<number | null>(null)

  const showRevisionDrawer = ref(false)
  const showCreatePageDialog = ref(false)
  const creatingPage = ref(false)
  const createPageForm = ref<WikiCreatePageFormState>({
    title: '',
    slug: '',
    pageType: 'concept',
    content: '',
  })
  const createPageSlugTouched = ref(false)

  function getTypeTheme(type: string): 'primary' | 'success' | 'warning' | 'danger' | 'default' {
    const map: Record<string, 'primary' | 'success' | 'warning' | 'danger' | 'default'> = {
      summary: 'primary',
      entity: 'success',
      concept: 'warning',
      synthesis: 'primary',
      comparison: 'danger',
      index: 'default',
    }
    return map[type] || 'default'
  }

  function getTypeLabel(type: string): string {
    const map: Record<string, string> = {
      knowledge: options.t('knowledgeEditor.wikiBrowser.filterKnowledge'),
      summary: options.t('knowledgeEditor.wikiBrowser.filterSummary'),
      entity: options.t('knowledgeEditor.wikiBrowser.filterEntity'),
      concept: options.t('knowledgeEditor.wikiBrowser.filterConcept'),
      synthesis: options.t('knowledgeEditor.wikiBrowser.filterSynthesis'),
      comparison: options.t('knowledgeEditor.wikiBrowser.filterComparison'),
      index: 'Index',
    }
    return map[type] || type
  }

  function getPageIcon(page: Pick<WikiPage, 'page_type'>): string {
    const map: Record<string, string> = {
      entity: 'tag',
      concept: 'lightbulb',
      synthesis: 'relativity',
      comparison: 'view-module',
      summary: 'file',
    }
    return map[page.page_type] || 'file'
  }

  function slugDisplayName(slug: string): string {
    return resolveWikiBacklinkTitle(slug, selectedPage.value?.in_link_titles ?? {}, pages.value)
  }

  function renderMarkdown(content: string): string {
    const codeTokens: string[] = []
    const masked = content.replace(/```[\s\S]*?```|`[^`\n]+`/g, (match) => {
      const idx = codeTokens.push(match) - 1
      return `\u0000WIKICODE_${idx}\u0000`
    })
    const linked = masked.replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
      const pipeIdx = inner.indexOf('|')
      const slug = pipeIdx > 0 ? inner.substring(0, pipeIdx).trim() : inner.trim()
      const display = pipeIdx > 0 ? inner.substring(pipeIdx + 1).trim() : slugDisplayName(slug)
      return `<a href="#" class="wiki-content-link" data-slug="${escapeWikiHtml(slug)}">${escapeWikiHtml(display)}</a>`
    })
    const preprocessed = linked.replace(/\u0000WIKICODE_(\d+)\u0000/g, (_, idx) => codeTokens[Number(idx)] ?? '')
    const html = options.parseMarkdown(preprocessed)
    return options.sanitizeHtml(html)
  }

  const renderedContent = computed(() => {
    if (!selectedPage.value) return ''
    const body = stripDuplicateLeadingTitle(selectedPage.value.content || '', selectedPage.value.title)
    return renderMarkdown(body)
  })

  const renderedIndexMarkdown = computed(() => {
    if (!indexMarkdown.value) return ''
    return renderMarkdown(indexMarkdown.value)
  })

  const indexHasMore = computed(() => {
    if (!indexAvailable.value) return false
    return indexSectionIdx.value < INDEX_SECTION_ORDER.length
  })

  const backLabel = computed(() => {
    if (navHistory.value.length > 0) {
      return navHistory.value[navHistory.value.length - 1].title
    }
    if (navFromSystemView.value === 'index') {
      return options.t('knowledgeEditor.wikiBrowser.indexTitle')
    }
    return ''
  })

  const readingAssistantActions = computed(() => {
    const source = JSON.stringify({
      knowledge_base_id: options.getKbId(),
      wiki_slug: selectedPage.value?.slug,
    })
    return [
      {
        key: 'summary',
        label: options.t('assistant.actions.readSummary'),
        query: options.t('assistant.prompts.readSummary', { source }),
      },
      {
        key: 'steps',
        label: options.t('assistant.actions.readSteps'),
        query: options.t('assistant.prompts.readSteps', { source }),
      },
      {
        key: 'faq',
        label: options.t('assistant.actions.extractFaq'),
        query: options.t('assistant.prompts.extractFaq', { source }),
      },
    ]
  })

  function parseSourceRefEntry(refItem: string): { id: string; title: string } {
    const pipeIdx = refItem.indexOf('|')
    if (pipeIdx > 0) {
      return { id: refItem.substring(0, pipeIdx), title: refItem.substring(pipeIdx + 1) }
    }
    const cached = sourceRefTitleCache[refItem]
    if (cached) return { id: refItem, title: cached }
    return {
      id: refItem,
      title: refItem.length > 20 ? `${refItem.substring(0, 8)}...` : refItem,
    }
  }

  const parsedSourceRefs = computed(() => {
    if (!selectedPage.value?.source_refs?.length) return []
    return selectedPage.value.source_refs.map(parseSourceRefEntry)
  })

  async function hydrateSourceRefTitles(refs: string[]) {
    if (typeof options.api.getKnowledgeDetails !== 'function') return
    const ids = refs.filter((item) => item.indexOf('|') < 0 && !sourceRefTitleCache[item])
    if (!ids.length) return
    const seq = ++sourceRefTitleRequestSeq
    for (const id of ids) {
      try {
        const res = await options.api.getKnowledgeDetails(id)
        if (seq !== sourceRefTitleRequestSeq) return
        const data = unwrapWikiPayload<{ title?: string; file_name?: string; fileName?: string }>(res)
        const title = data?.title || data?.file_name || data?.fileName
        if (title) sourceRefTitleCache[id] = title
      } catch {
        // Keep truncated-ID fallback when the source doc was deleted or inaccessible.
      }
    }
  }

  watch(
    () => selectedPage.value?.source_refs,
    (refs) => {
      if (refs?.length) void hydrateSourceRefTitles(refs)
    },
    { immediate: true },
  )

  watch(renderedContent, async () => {
    await nextTick()
    if (readerBodyRef.value) {
      await options.hydrateImages(readerBodyRef.value, options.kbFileAccess.value)
    }
  })

  watch(renderedIndexMarkdown, async () => {
    await nextTick()
    if (indexBodyRef.value) {
      await options.hydrateImages(indexBodyRef.value, options.kbFileAccess.value)
    }
  })

  function openImagePreview(url: string) {
    if (!url) return
    imagePreviewUrl.value = url
    imagePreviewVisible.value = true
  }

  function closeImagePreview() {
    imagePreviewVisible.value = false
    imagePreviewUrl.value = ''
  }

  function handleContentClick(e: MouseEvent) {
    const target = e.target as HTMLElement | null
    if (!target) return
    if (target.classList.contains('wiki-content-link')) {
      e.preventDefault()
      const slug = target.getAttribute('data-slug')
      if (slug) void navigateToSlug(slug)
    } else if (target.tagName.toLowerCase() === 'img') {
      e.preventDefault()
      openImagePreview(target.getAttribute('src') || '')
    }
  }

  async function loadIndex() {
    try {
      const idxRes = await options.api.getWikiIndex(options.getKbId(), {
        types: ['__intro_only__'],
        limit: 1,
      })
      const body = unwrapWikiPayload<{ intro?: string }>(idxRes)
      const cleanIntro = stripLegacyIndexDirectory(body?.intro || '')
      indexMarkdown.value = cleanIntro ? `${cleanIntro}\n` : ''
      indexAvailable.value = true
      indexSections.value = {}
      indexSectionIdx.value = 0
    } catch (e) {
      console.error('Failed to load wiki index:', e)
    }
  }

  async function openIndexView() {
    selectedPage.value = null
    activeSystemView.value = 'index'
    if (!indexMarkdown.value) {
      indexLoading.value = true
      try {
        await loadIndex()
      } finally {
        indexLoading.value = false
      }
    }
  }

  function appendIndexDirectoryLines(items: WikiIndexEntryDTO[]): string {
    let out = ''
    const emittedDirs = new Set<string>()
    for (const entry of items) {
      const path = (Array.isArray(entry.category_path) ? entry.category_path : [])
        .map((part) => String(part || '').trim())
        .filter(Boolean)
      for (let i = 0; i < path.length; i++) {
        const parts = path.slice(0, i + 1)
        const key = parts.join('/')
        if (emittedDirs.has(key)) continue
        emittedDirs.add(key)
        out += `${'  '.repeat(i)}**${escapeMarkdownText(path[i])}**\n`
      }
      const rawDisplay = (entry.title || entry.slug).replace(/[\]|]/g, ' ').replace(/\r?\n+/g, ' ')
      const safeSlug = String(entry.slug || '').replace(/[\]|]/g, '')
      const indent = '  '.repeat(path.length)
      if (entry.summary) {
        out += `${indent}[[${safeSlug}|${rawDisplay}]] — ${escapeMarkdownText(entry.summary)}\n`
      } else {
        out += `${indent}[[${safeSlug}|${rawDisplay}]]\n`
      }
    }
    return out
  }

  function sentinelInView(): boolean {
    const el = indexSentinelRef.value
    if (!el || typeof window === 'undefined') return false
    const rect = el.getBoundingClientRect()
    const vh = window.innerHeight || (typeof document !== 'undefined' ? document.documentElement.clientHeight : 0)
    return rect.top < vh + 200 && rect.bottom > -200
  }

  async function loadMoreIndexSection() {
    if (indexLoading.value || indexSectionIdx.value >= INDEX_SECTION_ORDER.length) return

    const type = INDEX_SECTION_ORDER[indexSectionIdx.value]
    const state = indexSections.value[type] || { loaded: false, cursor: '', total: 0 }
    const isFirstChunkOfSection = !state.loaded

    indexLoading.value = true
    try {
      const res = await options.api.getWikiIndex(options.getKbId(), {
        types: [type],
        limit: 50,
        cursor: isFirstChunkOfSection ? undefined : state.cursor || undefined,
      })
      const body = unwrapWikiPayload<{ groups?: WikiIndexGroup[] }>(res)
      const group = (body?.groups || []).find((g) => g.type === type)
      const items: WikiIndexEntryDTO[] = group?.items || []
      const total = group?.total || 0
      const nextCursor = group?.next_cursor || ''

      let appended = ''
      if (isFirstChunkOfSection && items.length > 0) {
        const label = getTypeLabel(type)
        appended += `\n## ${label} (${total})\n\n`
      }
      appended += appendIndexDirectoryLines(items)
      if (appended) {
        indexMarkdown.value += appended
      }

      indexSections.value[type] = {
        loaded: true,
        cursor: nextCursor,
        total,
      }

      if (!nextCursor) {
        indexSectionIdx.value += 1
      }
    } catch (e) {
      console.error(`Failed to load more index entries for ${INDEX_SECTION_ORDER[indexSectionIdx.value]}:`, e)
    } finally {
      indexLoading.value = false
    }

    await nextTick()
    if (indexHasMore.value && sentinelInView()) {
      void loadMoreIndexSection()
    }
  }

  watch([indexSentinelRef, () => activeSystemView.value], ([el, view]) => {
    if (indexObserver) {
      indexObserver.disconnect()
      indexObserver = null
    }
    if (view !== 'index' || !el || typeof IntersectionObserver === 'undefined') return
    indexObserver = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && indexHasMore.value && !indexLoading.value) {
            void loadMoreIndexSection()
          }
        }
      },
      { rootMargin: '200px' },
    )
    indexObserver.observe(el)
  })

  onUnmounted(() => {
    if (indexObserver) {
      indexObserver.disconnect()
      indexObserver = null
    }
  })

  async function selectPage(page: WikiPage) {
    try {
      const prevPage = selectedPage.value
      const prevSystemView = activeSystemView.value
      const res = await options.api.getWikiPage(options.getKbId(), page.slug)
      const loaded = unwrapWikiPayload<WikiPage>(res)
      if (prevPage && prevPage.id !== page.id) {
        navHistory.value.push(prevPage)
      } else if (!prevPage && prevSystemView) {
        navFromSystemView.value = prevSystemView
      }
      activeSystemView.value = ''
      selectedPage.value = loaded
      await options.onLoadPageIssues(page.slug)
    } catch (e) {
      logger.error('Failed to load wiki page:', e)
    }
  }

  async function navigateToSlug(slug: string) {
    try {
      const prevPage = selectedPage.value
      const prevSystemView = activeSystemView.value
      const res = await options.api.getWikiPage(options.getKbId(), slug)
      const loaded = unwrapWikiPayload<WikiPage>(res)
      if (prevPage && prevPage.slug !== slug) {
        navHistory.value.push(prevPage)
      } else if (!prevPage && prevSystemView) {
        navFromSystemView.value = prevSystemView
      }
      activeSystemView.value = ''
      selectedPage.value = loaded
      await options.onLoadPageIssues(slug)
    } catch (e) {
      logger.error(`Failed to navigate to ${slug}:`, e)
    }
  }

  function goBack() {
    const prev = navHistory.value.pop()
    if (prev) {
      selectedPage.value = prev
      void options.onLoadPageIssues(prev.slug)
      return
    }
    if (navFromSystemView.value) {
      const view = navFromSystemView.value
      navFromSystemView.value = ''
      selectedPage.value = null
      if (view === 'index') void openIndexView()
    }
  }

  async function refreshSelectedPage() {
    if (!selectedPage.value) return
    const slug = selectedPage.value.slug
    try {
      const res = await options.api.getWikiPage(options.getKbId(), slug)
      selectedPage.value = unwrapWikiPayload<WikiPage>(res)
      await options.onLoadPageIssues(slug)
    } catch (e) {
      logger.error(`Failed to refresh wiki page ${slug}:`, e)
    }
  }

  watch(
    () => selectedPage.value?.slug,
    () => {
      editingPage.value = false
      editConflictVersion.value = null
    },
  )

  function startEditPage() {
    if (!selectedPage.value) return
    editForm.value = {
      title: selectedPage.value.title,
      summary: selectedPage.value.summary || '',
      content: selectedPage.value.content || '',
    }
    editBaseVersion.value = selectedPage.value.version
    editConflictVersion.value = null
    editingPage.value = true
  }

  function cancelEditPage() {
    editingPage.value = false
    editConflictVersion.value = null
  }

  async function savePageEdit(versionOverride?: number) {
    if (!selectedPage.value) return
    const slug = selectedPage.value.slug
    savingPage.value = true
    try {
      const res = await options.api.updateWikiPage(options.getKbId(), slug, {
        title: editForm.value.title,
        summary: editForm.value.summary,
        content: editForm.value.content,
        version: versionOverride ?? editBaseVersion.value,
      })
      const updated = unwrapWikiPayload<WikiPage>(res)
      selectedPage.value = { ...selectedPage.value, ...updated }
      editingPage.value = false
      editConflictVersion.value = null
      options.onUpdateSidebarPageTitle(slug, updated.title)
      options.message.success(options.t('knowledgeEditor.wikiBrowser.editSaveSuccess'))
    } catch (e: unknown) {
      if (getWikiErrorStatus(e) === 409) {
        editConflictVersion.value = getWikiConflictVersion(e)
      } else {
        options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.editSaveFailed'))
      }
    } finally {
      savingPage.value = false
    }
  }

  async function overwriteSavePage() {
    if (!selectedPage.value) return
    try {
      const res = await options.api.getWikiPage(options.getKbId(), selectedPage.value.slug)
      const latest = unwrapWikiPayload<WikiPage>(res)
      await savePageEdit(latest.version)
    } catch (e) {
      console.error('Failed to fetch latest version for overwrite save:', e)
      options.message.error(options.t('knowledgeEditor.wikiBrowser.editSaveFailed'))
    }
  }

  async function reloadLatestIntoEditor() {
    await refreshSelectedPage()
    startEditPage()
  }

  async function confirmDeletePage() {
    if (!selectedPage.value) return
    const slug = selectedPage.value.slug
    try {
      await options.api.deleteWikiPage(options.getKbId(), slug)
      options.message.success(options.t('knowledgeEditor.wikiBrowser.deletePageSuccess'))
      selectedPage.value = null
      editingPage.value = false
      await options.onReloadPages()
    } catch (e: unknown) {
      options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.deletePageFailed'))
    }
  }

  function openRevisionDrawer() {
    if (!selectedPage.value) return
    showRevisionDrawer.value = true
  }

  function onPageReverted(page: WikiPage) {
    selectedPage.value = page
    editingPage.value = false
    options.onUpdateSidebarPageTitle(page.slug, page.title)
  }

  function openCreatePageDialog() {
    createPageForm.value = { title: '', slug: '', pageType: 'concept', content: '' }
    createPageSlugTouched.value = false
    showCreatePageDialog.value = true
  }

  function syncCreatePageSlug() {
    if (createPageSlugTouched.value) return
    const base = createPageForm.value.title
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9\s_-]/g, '')
      .replace(/\s+/g, '-')
      .replace(/-{2,}/g, '-')
      .replace(/^-|-$/g, '')
    createPageForm.value.slug = base ? `${createPageForm.value.pageType}/${base}` : ''
  }

  function updateCreatePageTitle(title: string) {
    createPageForm.value.title = title
    syncCreatePageSlug()
  }

  function updateCreatePageSlug(slug: string) {
    createPageForm.value.slug = slug
    createPageSlugTouched.value = true
  }

  function updateCreatePageType(pageType: string) {
    createPageForm.value.pageType = pageType
    syncCreatePageSlug()
  }

  function updateCreatePageContent(content: string) {
    createPageForm.value.content = content
  }

  async function submitCreatePage() {
    const title = createPageForm.value.title.trim()
    const slug = createPageForm.value.slug.trim().replace(/^\/+|\/+$/g, '')
    if (!title || !slug) {
      options.message.warning(options.t('knowledgeEditor.wikiBrowser.newPageMissingFields'))
      return
    }
    if (!/^[\p{L}\p{N}][\p{L}\p{N}\s_\-/]*$/u.test(slug)) {
      options.message.warning(options.t('knowledgeEditor.wikiBrowser.newPageSlugHint'))
      return
    }
    creatingPage.value = true
    try {
      await options.api.createWikiPage(options.getKbId(), {
        slug,
        title,
        page_type: createPageForm.value.pageType,
        content: createPageForm.value.content,
      })
      showCreatePageDialog.value = false
      options.message.success(options.t('knowledgeEditor.wikiBrowser.newPageSuccess'))
      void options.onReloadPages()
      await navigateToSlug(slug)
    } catch (e: unknown) {
      options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.newPageFailed'))
    } finally {
      creatingPage.value = false
    }
  }

  function editSourceVisible(source?: string): boolean {
    return source === 'user' || source === 'agent' || source === 'revert'
  }

  function editSourceLabel(source?: string): string {
    switch (source) {
      case 'user':
        return options.t('knowledgeEditor.wikiBrowser.editSourceUser')
      case 'agent':
        return options.t('knowledgeEditor.wikiBrowser.editSourceAgent')
      case 'revert':
        return options.t('knowledgeEditor.wikiBrowser.editSourceRevert')
      default:
        return options.t('knowledgeEditor.wikiBrowser.editSourcePipeline')
    }
  }

  function editSourceIcon(source?: string): string {
    switch (source) {
      case 'user':
        return 'user'
      case 'agent':
        return 'tools'
      case 'revert':
        return 'rollback'
      default:
        return 'file-code'
    }
  }

  async function hydrateReaderIfVisible() {
    await nextTick()
    if (readerBodyRef.value && renderedContent.value) {
      await options.hydrateImages(readerBodyRef.value, options.kbFileAccess.value)
    }
  }

  function resetReaderForKbSwitch() {
    editingPage.value = false
    editConflictVersion.value = null
    selectedPage.value = null
    navHistory.value = []
    navFromSystemView.value = ''
    activeSystemView.value = ''
    indexMarkdown.value = ''
    indexSectionIdx.value = 0
    indexSections.value = {}
    showRevisionDrawer.value = false
    showCreatePageDialog.value = false
  }

  return {
    navHistory,
    navFromSystemView,
    activeSystemView,
    indexMarkdown,
    indexLoading,
    indexAvailable,
    indexBodyRef,
    indexSentinelRef,
    readerBodyRef,
    imagePreviewVisible,
    imagePreviewUrl,
    openImagePreview,
    closeImagePreview,
    editingPage,
    savingPage,
    editForm,
    editConflictVersion,
    showRevisionDrawer,
    showCreatePageDialog,
    creatingPage,
    createPageForm,
    getTypeTheme,
    getTypeLabel,
    getPageIcon,
    slugDisplayName,
    renderMarkdown,
    renderedContent,
    renderedIndexMarkdown,
    indexHasMore,
    backLabel,
    readingAssistantActions,
    parsedSourceRefs,
    handleContentClick,
    loadIndex,
    openIndexView,
    selectPage,
    navigateToSlug,
    goBack,
    refreshSelectedPage,
    startEditPage,
    cancelEditPage,
    savePageEdit,
    overwriteSavePage,
    reloadLatestIntoEditor,
    confirmDeletePage,
    openRevisionDrawer,
    onPageReverted,
    openCreatePageDialog,
    updateCreatePageTitle,
    updateCreatePageSlug,
    updateCreatePageType,
    updateCreatePageContent,
    submitCreatePage,
    editSourceVisible,
    editSourceLabel,
    editSourceIcon,
    hydrateReaderIfVisible,
    resetReaderForKbSwitch,
  }
}
