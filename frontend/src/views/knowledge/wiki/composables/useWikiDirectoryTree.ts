import { computed, nextTick, onUnmounted, ref, watch, type Ref } from 'vue'
import type { WikiFolderNode, WikiPage, WikiStats } from '@/api/wiki'
import { expandedWikiDirectoryPaths, expandWikiDirectoryPath } from '../wikiDirectoryState'
import { getWikiErrorMessage, unwrapWikiPayload, type WikiMessageLike } from './useWikiPageEditor'

export type SidebarViewMode = 'tree' | 'list'

export const WIKI_SIDEBAR_PAGE_SIZE = 100
export const WIKI_PAGE_ITEM_HEIGHT = 100

const KNOWLEDGE_TAB = 'knowledge'
const KNOWLEDGE_TYPES = ['entity', 'concept', 'synthesis', 'comparison']
const CONTENT_TABS = [KNOWLEDGE_TAB, 'summary']
const SIDEBAR_VIEW_MODE_KEY = 'genyee.wiki.sidebar.viewMode'

export interface DirectoryPageState { nextPage: number; total: number; loading: boolean; initialized: boolean }
export interface DirectoryCategoryState { nextPage: number; totalPages: number; loading: boolean; initialized: boolean }

export interface PageTypeBucket {
  items: WikiPage[]
  nextPage: number
  total: number
  loading: boolean
  initialized: boolean
  categoryPaths: Array<{ path: string[]; count: number }>
  folderIdByPath: Record<string, string>
  categoriesLoading: boolean
  categoriesInitialized: boolean
  categoryPages: Record<string, DirectoryCategoryState>
  directoryPages: Record<string, DirectoryPageState>
  flatItems: WikiPage[]
  flatNextPage: number
  flatTotal: number
  flatLoading: boolean
  flatInitialized: boolean
}

export type WikiTreeRow =
  | { kind: 'directory'; rowKey: string; pathKey: string; folderId: string; path: string[]; label: string; depth: number; count: number; hasChildren: boolean; collapsed: boolean }
  | { kind: 'load-more'; rowKey: string; type: string; path: string[]; depth: number; loading: boolean }
  | { kind: 'page'; rowKey: string; page: WikiPage; depth: number }

interface WikiTreeDirectory { label: string; path: string[]; dirs: Map<string, WikiTreeDirectory>; pages: WikiPage[]; count: number }

export type WikiDraggedItem =
  | { kind: 'page'; page: WikiPage }
  | { kind: 'folder'; folderId: string; path: string[] }

export interface WikiPendingMove { item: WikiDraggedItem; folderId: string; targetLabel: string; x: number; y: number }

export interface WikiDirectoryApi {
  listWikiPages: (kbId: string, params: { page_type: string; page: number; page_size: number; sort_by: string; sort_order: 'asc' | 'desc'; category_path?: string; category_depth?: number }) => Promise<unknown>
  listWikiFolders: (kbId: string, parentId: string, pageTypes: string) => Promise<unknown>
  createWikiFolder: (kbId: string, parentId: string, name: string) => Promise<unknown>
  updateWikiFolder: (kbId: string, folderId: string, payload: { name?: string; parent_id?: string; move_parent?: boolean }) => Promise<unknown>
  deleteWikiFolder: (kbId: string, folderId: string) => Promise<unknown>
  moveWikiPage: (kbId: string, slug: string, folderId: string) => Promise<unknown>
  searchWikiPages: (kbId: string, query: string, limit?: number) => Promise<unknown>
}

export interface UseWikiDirectoryTreeOptions {
  getKbId: () => string
  canEdit: () => boolean
  getRouteSlug: () => string | undefined
  pages: Ref<WikiPage[]>
  selectedPage: Ref<WikiPage | null>
  activeSystemView: Ref<'' | 'index'>
  indexAvailable: Ref<boolean>
  stats: Ref<WikiStats | null>
  t: (key: string, params?: Record<string, unknown>) => string
  getTypeLabel: (type: string) => string
  message: WikiMessageLike
  api: WikiDirectoryApi
  storage?: Pick<Storage, 'getItem' | 'setItem'>
  timers?: {
    setTimeout: typeof setTimeout
    clearTimeout: typeof clearTimeout
  }
  onLoadIndex: () => Promise<void>
  onOpenIndexView: () => Promise<void>
  onNavigateToSlug: (slug: string) => Promise<void>
}

function tabPageTypes(tab: string): string {
  return tab === KNOWLEDGE_TAB ? KNOWLEDGE_TYPES.join(',') : tab
}

function preferredDefaultTab(tabs: Array<{ type: string }>): string {
  for (const tab of CONTENT_TABS) {
    if (tabs.some((item) => item.type === tab)) return tab
  }
  return tabs[0]?.type || ''
}

function pageCategoryPath(page: WikiPage): string[] {
  const raw = Array.isArray(page.category_path) ? page.category_path : []
  return raw.map((part) => String(part || '').trim()).filter(Boolean)
}

function directoryPathKey(type: string, parts: string[]): string {
  return `${type}:${parts.join('/')}`
}

function parseDirectoryPathKey(pathKey: string): { type: string; path: string[] } | null {
  const idx = pathKey.indexOf(':')
  if (idx < 0) return null
  const type = pathKey.slice(0, idx)
  const path = pathKey
    .slice(idx + 1)
    .split('/')
    .map((part) => part.trim())
    .filter(Boolean)
  return { type, path }
}

function emptyBucket(): PageTypeBucket {
  return {
    items: [],
    nextPage: 1,
    total: 0,
    loading: false,
    initialized: false,
    categoryPaths: [],
    folderIdByPath: {},
    categoriesLoading: false,
    categoriesInitialized: false,
    categoryPages: {},
    directoryPages: {},
    flatItems: [],
    flatNextPage: 1,
    flatTotal: 0,
    flatLoading: false,
    flatInitialized: false,
  }
}

export function useWikiDirectoryTree(options: UseWikiDirectoryTreeOptions) {
  const { pages, stats, getTypeLabel } = options
  const timers = options.timers ?? { setTimeout, clearTimeout }

  const loading = ref(false)
  const pagesByType = ref<Record<string, PageTypeBucket>>({})
  const collapsedDirectories = ref<Set<string>>(new Set())
  const touchedDirectories = ref<Set<string>>(new Set())
  const searchQuery = ref('')
  const searchResults = ref<WikiPage[] | null>(null)
  const activeTab = ref('')

  function initialSidebarViewMode(): SidebarViewMode {
    try {
      return options.storage?.getItem(SIDEBAR_VIEW_MODE_KEY) === 'list' ? 'list' : 'tree'
    } catch {
      return 'tree'
    }
  }

  const sidebarViewMode = ref<SidebarViewMode>(initialSidebarViewMode())
  let initialSidebarLoad = true

  const pageListRef = ref<HTMLElement | null>(null)
  const treeListRef = ref<HTMLElement | null>(null)
  const groupSentinelRef = ref<HTMLElement | null>(null)
  const sidebarViewSwitching = ref(false)
  const sidebarTabSwitching = ref(false)

  let groupSentinelObserver: IntersectionObserver | null = null
  let loadMoreObserver: IntersectionObserver | null = null
  let searchTimer: ReturnType<typeof setTimeout> | null = null

  // groupedPages projects the bucketed state into the shape the sidebar
  // template expects: one {type, label, items, total, loading, hasMore}
  // per displayed group. Built-in tab totals come from whole-library stats;
  // groups with no pages remain visible when they still contain directories.
  const groupedPages = computed(() => {
    type Group = {
      type: string
      label: string
      pages: WikiPage[]
      total: number
      loading: boolean
      hasMore: boolean
    }
    const out: Group[] = []
    const seen = new Set<string>()
    const statTotal = (tab: string) => {
      const byType = stats.value?.pages_by_type
      if (!byType) return 0
      if (tab === KNOWLEDGE_TAB) return KNOWLEDGE_TYPES.reduce((sum, t) => sum + (byType[t] || 0), 0)
      return byType[tab] || 0
    }
    const push = (tab: string) => {
      const bucket = pagesByType.value[tab]
      if (!bucket) return
      const total = CONTENT_TABS.includes(tab)
        ? statTotal(tab)
        : bucket.total || statTotal(tab) || bucket.categoryPaths.length
      if (total === 0 && bucket.items.length === 0 && bucket.categoryPaths.length === 0) return
      out.push({
        type: tab,
        label: getTypeLabel(tab),
        pages: bucket.items,
        total,
        loading: bucket.loading,
        hasMore: bucket.total > 0 && bucket.items.length < bucket.total,
      })
      seen.add(tab)
    }
    for (const tab of CONTENT_TABS) push(tab)
    for (const tab of Object.keys(pagesByType.value)) {
      if (seen.has(tab)) continue
      if (tab === 'index') continue
      push(tab)
    }
    return out
  })

  // hasContentPages is the sidebar's empty-state gate.
  const hasContentPages = computed(() => {
    for (const bucket of Object.values(pagesByType.value)) {
      if (bucket.total > 0 || bucket.categoryPaths.length > 0) return true
    }
    return false
  })

  const visibleTabs = computed(() =>
    groupedPages.value.map((g) => ({ type: g.type, label: g.label, total: g.total })),
  )

  const activeGroup = computed(() => {
    if (!activeTab.value) return null
    return groupedPages.value.find((g) => g.type === activeTab.value) || null
  })

  const activeFlatPages = computed(() => {
    if (!activeTab.value) return []
    return pagesByType.value[activeTab.value]?.flatItems || []
  })

  const activeFlatState = computed(() => {
    if (!activeTab.value) return null
    const bucket = pagesByType.value[activeTab.value]
    if (!bucket) return null
    return {
      loading: bucket.flatLoading,
      hasMore: !bucket.flatInitialized || bucket.flatItems.length < bucket.flatTotal,
    }
  })

  function ensureBucket(type: string): PageTypeBucket {
    if (!pagesByType.value[type]) {
      pagesByType.value[type] = emptyBucket()
    }
    return pagesByType.value[type]
  }

  function clearDirectoryStateForType(type: string) {
    const prefix = `${type}:`
    collapsedDirectories.value = new Set([...collapsedDirectories.value].filter((key) => !key.startsWith(prefix)))
    touchedDirectories.value = new Set([...touchedDirectories.value].filter((key) => !key.startsWith(prefix)))
  }

  function initializeDefaultCollapsedDirectories(type: string, pagesToInspect: WikiPage[]) {
    if (pagesToInspect.length === 0) return
    const next = new Set(collapsedDirectories.value)
    const touched = touchedDirectories.value
    let changed = false

    for (const page of pagesToInspect) {
      const path = pageCategoryPath(page)
      for (let i = 0; i < path.length; i++) {
        const key = directoryPathKey(type, path.slice(0, i + 1))
        if (touched.has(key) || next.has(key)) continue
        next.add(key)
        changed = true
      }
    }

    if (changed) {
      collapsedDirectories.value = next
    }
  }

  function toggleDirectory(pathKey: string) {
    const next = new Set(collapsedDirectories.value)
    if (next.has(pathKey)) {
      next.delete(pathKey)
      const parsed = parseDirectoryPathKey(pathKey)
      if (parsed) {
        void loadCategoriesForType(parsed.type, { parentPath: parsed.path })
        void loadPagesForType(parsed.type, { categoryPath: parsed.path })
      }
    } else {
      next.add(pathKey)
    }
    collapsedDirectories.value = next

    const touched = new Set(touchedDirectories.value)
    touched.add(pathKey)
    touchedDirectories.value = touched
  }

  const activeTreeRows = computed<WikiTreeRow[]>(() => {
    const group = activeGroup.value
    if (!group) return []
    const groupType = group.type

    const rows: WikiTreeRow[] = []
    const root: WikiTreeDirectory = {
      label: '',
      path: [],
      dirs: new Map(),
      pages: [],
      count: 0,
    }

    function ensureDirectory(parent: WikiTreeDirectory, label: string): WikiTreeDirectory {
      const existing = parent.dirs.get(label)
      if (existing) return existing
      const dir: WikiTreeDirectory = {
        label,
        path: [...parent.path, label],
        dirs: new Map(),
        pages: [],
        count: 0,
      }
      parent.dirs.set(label, dir)
      return dir
    }

    const bucket = pagesByType.value[groupType]
    const hasCategorySkeleton = (bucket?.categoryPaths.length || 0) > 0
    for (const category of bucket?.categoryPaths || []) {
      const categoryPath = category.path
      let cursor = root
      for (let i = 0; i < categoryPath.length; i++) {
        const part = categoryPath[i]
        cursor = ensureDirectory(cursor, part)
        if (i === categoryPath.length - 1) {
          cursor.count = category.count
        }
      }
    }

    for (const page of group.pages) {
      const path = pageCategoryPath(page)
      let cursor = root
      if (!hasCategorySkeleton) cursor.count += 1
      for (const part of path) {
        cursor = ensureDirectory(cursor, part)
        if (!hasCategorySkeleton) cursor.count += 1
      }
      cursor.pages.push(page)
    }

    function appendDirectory(dir: WikiTreeDirectory, depth: number) {
      const key = directoryPathKey(groupType, dir.path)
      const collapsed = collapsedDirectories.value.has(key)
      rows.push({
        kind: 'directory',
        rowKey: `dir:${key}`,
        pathKey: key,
        folderId: bucket?.folderIdByPath[dir.path.join('/')] || '',
        path: dir.path,
        label: dir.label,
        depth,
        count: dir.count,
        hasChildren: dir.dirs.size > 0,
        collapsed,
      })
      if (collapsed) return

      for (const child of dir.dirs.values()) {
        appendDirectory(child, depth + 1)
      }
      for (const page of dir.pages) {
        rows.push({
          kind: 'page',
          rowKey: `page:${page.id}`,
          page,
          depth: depth + 1,
        })
      }
      const state = bucket?.directoryPages[key]
      const hasMore = state && state.total > 0 && (state.nextPage - 1) * WIKI_SIDEBAR_PAGE_SIZE < state.total
      if (state?.loading || hasMore) {
        rows.push({
          kind: 'load-more',
          rowKey: `load-more:${key}`,
          type: groupType,
          path: dir.path,
          depth: depth + 1,
          loading: !!state?.loading,
        })
      }
    }

    for (const dir of root.dirs.values()) {
      appendDirectory(dir, 0)
    }
    for (const page of root.pages) {
      rows.push({
        kind: 'page',
        rowKey: `page:${page.id}`,
        page,
        depth: 0,
      })
    }
    return rows
  })

  watch(visibleTabs, (tabs) => {
    if (initialSidebarLoad) return
    if (tabs.length === 0) {
      activeTab.value = ''
      return
    }
    if (!tabs.some((tab) => tab.type === activeTab.value)) {
      activeTab.value = preferredDefaultTab(tabs)
    }
  })

  async function loadFlatPagesForType(type: string, reset = false): Promise<boolean> {
    const bucket = ensureBucket(type)
    if (bucket.flatLoading) return bucket.flatItems.length > 0
    if (!reset && bucket.flatInitialized && bucket.flatItems.length >= bucket.flatTotal) return true

    bucket.flatLoading = true
    try {
      const requestPage = reset ? 1 : bucket.flatNextPage
      const res = await options.api.listWikiPages(options.getKbId(), {
        page_type: tabPageTypes(type),
        page: requestPage,
        page_size: WIKI_SIDEBAR_PAGE_SIZE,
        sort_by: 'wiki_path',
        sort_order: 'asc',
      })
      const body = unwrapWikiPayload<{ pages?: WikiPage[]; total?: number }>(res)
      const batch: WikiPage[] = body?.pages || []
      if (reset) {
        bucket.flatItems = batch
        bucket.flatNextPage = 2
      } else {
        const seen = new Set(bucket.flatItems.map((page) => page.id))
        for (const page of batch) {
          if (!seen.has(page.id)) bucket.flatItems.push(page)
        }
        bucket.flatNextPage += 1
      }
      bucket.flatTotal = Number(body?.total) || 0
      bucket.flatInitialized = true

      const seenPages = new Set(pages.value.map((page) => page.id))
      for (const page of batch) {
        if (!seenPages.has(page.id)) pages.value.push(page)
      }
      return true
    } catch (e) {
      console.error(`Failed to load flat wiki pages of type ${type}:`, e)
      return false
    } finally {
      bucket.flatLoading = false
    }
  }

  async function loadCategoriesForType(type: string, opts: { reset?: boolean; parentPath?: string[] } = {}) {
    const bucket = ensureBucket(type)
    const parentPath = opts.parentPath || []
    const parentKey = directoryPathKey(type, parentPath)
    const isRoot = parentPath.length === 0

    const parentId = isRoot ? '' : bucket.folderIdByPath[parentPath.join('/')]
    if (!isRoot && parentId === undefined) return

    let state = bucket.categoryPages[parentKey]
    if (state?.loading) return
    if (!opts.reset && state && state.initialized) return
    if (!state) {
      state = { nextPage: 1, totalPages: 1, loading: false, initialized: false }
    }

    const setState = (next: DirectoryCategoryState) => {
      bucket.categoryPages = { ...bucket.categoryPages, [parentKey]: next }
    }
    setState({ ...state, loading: true })
    if (isRoot) bucket.categoriesLoading = true
    try {
      const res = await options.api.listWikiFolders(options.getKbId(), parentId || '', tabPageTypes(type))
      const body = unwrapWikiPayload<{ folders?: WikiFolderNode[] }>(res)
      const folders: WikiFolderNode[] = Array.isArray(body?.folders) ? body.folders : []
      const incoming = folders
        .map((folder) => ({
          path: String(folder.path || '')
            .split('/')
            .map((part) => part.trim())
            .filter(Boolean),
          count: Number(folder.page_count) || 0,
          id: String(folder.id || ''),
        }))
        .filter((entry) => entry.path.length > 0)
        .filter((entry) => type !== 'summary' || entry.count > 0)

      const existing = new Map(
        (opts.reset ? [] : bucket.categoryPaths).map((entry) => [directoryPathKey(type, entry.path), entry]),
      )
      const folderIds = opts.reset ? {} : { ...bucket.folderIdByPath }
      for (const entry of incoming) {
        existing.set(directoryPathKey(type, entry.path), { path: entry.path, count: entry.count })
        folderIds[entry.path.join('/')] = entry.id
      }
      bucket.categoryPaths = Array.from(existing.values())
      bucket.folderIdByPath = folderIds
      if (opts.reset) bucket.categoryPages = {}
      setState({ nextPage: 2, totalPages: 1, loading: false, initialized: true })
      if (isRoot) bucket.categoriesInitialized = true
      initializeDefaultCollapsedDirectories(
        type,
        incoming.map((entry) => ({ category_path: entry.path }) as WikiPage),
      )
    } catch (e) {
      console.error(`Failed to load wiki folders of type ${type}:`, e)
      setState({ ...state, loading: false })
    } finally {
      if (isRoot) bucket.categoriesLoading = false
    }
  }

  async function loadPagesForType(
    type: string,
    opts: { reset?: boolean; categoryPath?: string[]; preserveDirectoryState?: boolean } = {},
  ) {
    const bucket = ensureBucket(type)
    const categoryPath = opts.categoryPath || []
    const scopedToCategory = categoryPath.length > 0
    const scopedPathKey = scopedToCategory ? directoryPathKey(type, categoryPath) : ''
    if (scopedToCategory && !bucket.directoryPages[scopedPathKey]) {
      bucket.directoryPages = {
        ...bucket.directoryPages,
        [scopedPathKey]: { nextPage: 1, total: 0, loading: false, initialized: false },
      }
    }
    const scopedState = scopedToCategory ? bucket.directoryPages[scopedPathKey] : null
    if (scopedState?.loading || (!scopedToCategory && bucket.loading)) return
    if (scopedToCategory) {
      if (!scopedState) return
      if (scopedState.initialized && scopedState.total > 0 && (scopedState.nextPage - 1) * WIKI_SIDEBAR_PAGE_SIZE >= scopedState.total) {
        return
      }
    } else if (!opts.reset && bucket.initialized && bucket.items.length >= bucket.total) {
      return
    }

    if (scopedState) {
      bucket.directoryPages = {
        ...bucket.directoryPages,
        [scopedPathKey]: { ...scopedState, loading: true },
      }
    } else {
      bucket.loading = true
    }

    try {
      const currentScopedState = scopedToCategory ? bucket.directoryPages[scopedPathKey] : null
      const requestPage = opts.reset ? 1 : currentScopedState ? currentScopedState.nextPage : bucket.nextPage
      const res = await options.api.listWikiPages(options.getKbId(), {
        page_type: tabPageTypes(type),
        page: requestPage,
        page_size: WIKI_SIDEBAR_PAGE_SIZE,
        sort_by: 'wiki_path',
        sort_order: 'asc',
        category_path: categoryPath.join('/'),
        category_depth: categoryPath.length,
      })
      const body = unwrapWikiPayload<{ pages?: WikiPage[]; total?: number }>(res)
      const batch: WikiPage[] = body?.pages || []
      const reportedTotal = Number(body?.total) || 0

      if (!scopedToCategory) {
        if (opts.reset) {
          bucket.items = batch
          bucket.nextPage = 2
          bucket.directoryPages = {}
          if (!opts.preserveDirectoryState) clearDirectoryStateForType(type)
        } else {
          const seenItems = new Set(bucket.items.map((p) => p.id))
          for (const p of batch) {
            if (!seenItems.has(p.id)) bucket.items.push(p)
          }
          bucket.nextPage += 1
        }
        bucket.total = reportedTotal
        bucket.initialized = true
      } else if (currentScopedState) {
        const seenItems = new Set(bucket.items.map((p) => p.id))
        for (const p of batch) {
          if (!seenItems.has(p.id)) bucket.items.push(p)
        }
        bucket.directoryPages = {
          ...bucket.directoryPages,
          [scopedPathKey]: {
            ...currentScopedState,
            total: reportedTotal,
            nextPage: currentScopedState.nextPage + 1,
            initialized: true,
            loading: false,
          },
        }
      }
      initializeDefaultCollapsedDirectories(type, batch)

      if (batch.length > 0) {
        const seen = new Set(pages.value.map((p) => p.id))
        for (const p of batch) {
          if (!seen.has(p.id)) pages.value.push(p)
        }
      }
    } catch (e) {
      console.error(`Failed to load wiki pages of type ${type}:`, e)
    } finally {
      if (scopedState) {
        const latest = bucket.directoryPages[scopedPathKey]
        if (latest?.loading) {
          bucket.directoryPages = {
            ...bucket.directoryPages,
            [scopedPathKey]: { ...latest, loading: false, initialized: true },
          }
        }
      } else {
        bucket.loading = false
      }
    }

    await nextTick()
  }

  async function reloadDirectoryForType(type: string, opts: { preserveDirectoryState?: boolean } = {}) {
    const refreshFlatList = sidebarViewMode.value === 'list' || ensureBucket(type).flatInitialized
    const expandedPaths = opts.preserveDirectoryState
      ? expandedWikiDirectoryPaths(type, collapsedDirectories.value, touchedDirectories.value)
      : []
    if (!opts.preserveDirectoryState) clearDirectoryStateForType(type)
    await loadPagesForType(type, {
      reset: true,
      preserveDirectoryState: opts.preserveDirectoryState,
    })
    await loadCategoriesForType(type, { reset: true })
    for (const path of expandedPaths) {
      await Promise.all([
        loadPagesForType(type, { categoryPath: path }),
        loadCategoriesForType(type, { parentPath: path }),
      ])
    }
    if (refreshFlatList) await loadFlatPagesForType(type, true)
  }

  const draggedItem = ref<WikiDraggedItem | null>(null)
  const dropTargetKey = ref('')
  const pendingMove = ref<WikiPendingMove | null>(null)

  function primeDragData(e: DragEvent) {
    if (!e.dataTransfer) return
    e.dataTransfer.effectAllowed = 'move'
    try {
      e.dataTransfer.setData('text/plain', '')
    } catch {
      // Ignore synthetic DragEvent implementations without full DataTransfer support.
    }
  }

  function onPageDragStart(e: DragEvent, page: WikiPage) {
    if (!options.canEdit()) return
    draggedItem.value = { kind: 'page', page }
    primeDragData(e)
  }

  function onFolderDragStart(e: DragEvent, folderId: string, path: string[]) {
    if (!options.canEdit() || !folderId) return
    draggedItem.value = { kind: 'folder', folderId, path }
    primeDragData(e)
  }

  function onPageDragEnd() {
    draggedItem.value = null
    dropTargetKey.value = ''
  }

  function onDirectoryDragOver(e: DragEvent, pathKey: string) {
    if (!draggedItem.value) return
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    dropTargetKey.value = pathKey
  }

  function onRootDragOver(e: DragEvent) {
    if (!draggedItem.value) return
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    dropTargetKey.value = '__root__'
  }

  function onDirectoryDragLeave(pathKey: string) {
    if (dropTargetKey.value === pathKey) dropTargetKey.value = ''
  }

  function onDropOnDirectory(e: DragEvent, folderId: string, path: string[]) {
    const item = draggedItem.value
    draggedItem.value = null
    dropTargetKey.value = ''
    if (!options.canEdit() || !item) return
    const targetPath = path.join('/')

    if (item.kind === 'page') {
      if ((pageCategoryPath(item.page) || []).join('/') === targetPath) return
    } else {
      const sourcePath = item.path.join('/')
      if (sourcePath === targetPath) return
      if (folderId === item.folderId) return
      if (targetPath === sourcePath || targetPath.startsWith(`${sourcePath}/`)) {
        options.message.warning(options.t('knowledgeEditor.wikiBrowser.moveFolderIntoSelf'))
        return
      }
    }

    const targetLabel = path.length > 0 ? path[path.length - 1] : options.t('knowledgeEditor.wikiBrowser.rootFolderLabel')
    pendingMove.value = { item, folderId, targetLabel, x: e.clientX, y: e.clientY }
  }

  function cancelPendingMove() {
    pendingMove.value = null
  }

  async function confirmPendingMove() {
    const move = pendingMove.value
    pendingMove.value = null
    if (!move) return
    const { item, folderId } = move

    if (item.kind === 'page') {
      try {
        await options.api.moveWikiPage(options.getKbId(), item.page.slug, folderId)
        options.message.success(options.t('knowledgeEditor.wikiBrowser.movePageSuccess'))
        await reloadDirectoryForType(activeTab.value)
      } catch (e) {
        console.error('Failed to move wiki page:', e)
        options.message.error(options.t('knowledgeEditor.wikiBrowser.movePageFailed'))
      }
      return
    }

    try {
      await options.api.updateWikiFolder(options.getKbId(), item.folderId, {
        parent_id: folderId,
        move_parent: true,
      })
      options.message.success(options.t('knowledgeEditor.wikiBrowser.moveFolderSuccess'))
      await reloadDirectoryForType(activeTab.value)
    } catch (e: unknown) {
      console.error('Failed to move wiki folder:', e)
      options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.moveFolderFailed'))
    }
  }

  async function createFolder(parentId: string, parentPath: string[], name: string) {
    const type = activeTab.value
    const scrollTop = pageListRef.value?.scrollTop
    try {
      await options.api.createWikiFolder(options.getKbId(), parentId, name)
      options.message.success(options.t('knowledgeEditor.wikiBrowser.createFolderSuccess'))
      if (parentPath.length > 0) {
        const state = expandWikiDirectoryPath(
          type,
          parentPath,
          collapsedDirectories.value,
          touchedDirectories.value,
        )
        collapsedDirectories.value = state.collapsed
        touchedDirectories.value = state.touched
      }
      await reloadDirectoryForType(type, { preserveDirectoryState: true })
      await nextTick()
      if (activeTab.value === type && scrollTop !== undefined && pageListRef.value) {
        pageListRef.value.scrollTop = scrollTop
      }
    } catch (e: unknown) {
      console.error('Failed to create wiki folder:', e)
      options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.createFolderFailed'))
    }
  }

  // Inline rename: the directory row swaps its label for a text input instead
  // of opening a popup.
  const editingFolderId = ref('')
  const editingName = ref('')
  const creatingRootFolder = ref(false)
  const creatingRootFolderName = ref('')

  function startCreateRootFolder() {
    if (creatingRootFolder.value) return
    creatingRootFolder.value = true
    creatingRootFolderName.value = ''
  }

  function cancelCreateRootFolder() {
    creatingRootFolder.value = false
    creatingRootFolderName.value = ''
  }

  async function submitCreateRootFolder() {
    const name = creatingRootFolderName.value.trim()
    if (!name) return
    cancelCreateRootFolder()
    await createFolder('', [], name)
  }

  function startRenameFolder(folderId: string, currentName: string) {
    if (!folderId) return
    editingFolderId.value = folderId
    editingName.value = currentName
    void nextTick(() => {
      if (typeof document === 'undefined') return
      const el = document.querySelector('.wiki-directory-rename-input') as HTMLInputElement | null
      el?.focus()
      el?.select()
    })
  }

  function cancelRenameFolder() {
    editingFolderId.value = ''
    editingName.value = ''
  }

  async function commitRenameFolder(folderId: string, originalName: string) {
    if (editingFolderId.value !== folderId) return
    const name = editingName.value.trim()
    cancelRenameFolder()
    if (!name || name === originalName) return
    try {
      await options.api.updateWikiFolder(options.getKbId(), folderId, { name })
      options.message.success(options.t('knowledgeEditor.wikiBrowser.renameFolderSuccess'))
      await reloadDirectoryForType(activeTab.value)
    } catch (e: unknown) {
      console.error('Failed to rename wiki folder:', e)
      options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.renameFolderFailed'))
    }
  }

  async function deleteFolder(folderId: string) {
    if (!folderId) return
    try {
      await options.api.deleteWikiFolder(options.getKbId(), folderId)
      options.message.success(options.t('knowledgeEditor.wikiBrowser.deleteFolderSuccess'))
      await reloadDirectoryForType(activeTab.value)
    } catch (e: unknown) {
      console.error('Failed to delete wiki folder:', e)
      options.message.error(getWikiErrorMessage(e) || options.t('knowledgeEditor.wikiBrowser.deleteFolderFailed'))
    }
  }

  watch(sidebarViewMode, (mode) => {
    try {
      options.storage?.setItem(SIDEBAR_VIEW_MODE_KEY, mode)
    } catch {
      // Ignore storage quota errors.
    }
    cancelCreateRootFolder()
    if (pageListRef.value) pageListRef.value.scrollTop = 0
  })

  async function switchSidebarViewMode(mode: SidebarViewMode) {
    if (mode === sidebarViewMode.value || sidebarViewSwitching.value) return
    sidebarViewSwitching.value = true
    try {
      if (mode === 'list' && activeTab.value) {
        const ready = await loadFlatPagesForType(activeTab.value)
        if (!ready && activeFlatPages.value.length === 0) return
      }
      sidebarViewMode.value = mode
    } finally {
      sidebarViewSwitching.value = false
    }
  }

  function waitForTabData(type: string, mode: SidebarViewMode): Promise<void> {
    const isBusy = () => {
      const bucket = pagesByType.value[type]
      if (!bucket) return false
      return mode === 'list' ? bucket.flatLoading : bucket.loading || bucket.categoriesLoading
    }
    if (!isBusy()) return Promise.resolve()

    return new Promise((resolve) => {
      const stop = watch(
        isBusy,
        (busy) => {
          if (!busy) {
            stop()
            resolve()
          }
        },
        { flush: 'post' },
      )
      if (!isBusy()) {
        stop()
        resolve()
      }
    })
  }

  async function setActiveTab(type: string) {
    if (activeTab.value === type || sidebarTabSwitching.value) return
    sidebarTabSwitching.value = true
    ensureBucket(type)
    try {
      if (sidebarViewMode.value === 'list') {
        await loadFlatPagesForType(type)
      } else {
        await Promise.all([loadPagesForType(type), loadCategoriesForType(type)])
      }
      await waitForTabData(type, sidebarViewMode.value)
      cancelCreateRootFolder()
      activeTab.value = type
    } finally {
      sidebarTabSwitching.value = false
    }
    if (pageListRef.value) pageListRef.value.scrollTop = 0
  }

  watch(
    [groupSentinelRef, sidebarViewMode],
    ([el]) => {
      if (groupSentinelObserver) {
        groupSentinelObserver.disconnect()
        groupSentinelObserver = null
      }
      if (!el || typeof IntersectionObserver === 'undefined') return
      groupSentinelObserver = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            if (!entry.isIntersecting) continue
            const type = (entry.target as HTMLElement).dataset.type
            if (!type) continue
            if (sidebarViewMode.value === 'list') void loadFlatPagesForType(type)
            else void loadPagesForType(type)
          }
        },
        { rootMargin: '200px' },
      )
      groupSentinelObserver.observe(el)
    },
    { flush: 'post' },
  )

  function dispatchLoadMore(rowKey: string) {
    const row = activeTreeRows.value.find((r) => r.rowKey === rowKey)
    if (row?.kind === 'load-more') {
      void loadPagesForType(row.type, { categoryPath: row.path })
    }
  }

  watch(
    [activeTreeRows, treeListRef],
    () => {
      if (loadMoreObserver) {
        loadMoreObserver.disconnect()
        loadMoreObserver = null
      }
      const container = treeListRef.value
      if (!container || typeof IntersectionObserver === 'undefined') return
      loadMoreObserver = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            if (!entry.isIntersecting) continue
            const key = (entry.target as HTMLElement).dataset.loadmoreKey
            if (key) dispatchLoadMore(key)
          }
        },
        { rootMargin: '200px' },
      )
      container.querySelectorAll<HTMLElement>('[data-loadmore-key]').forEach((el) => loadMoreObserver!.observe(el))
    },
    { flush: 'post' },
  )

  async function loadPages() {
    loading.value = true
    try {
      searchResults.value = null
      for (const tab of CONTENT_TABS) ensureBucket(tab)
      await options.onLoadIndex()
      await Promise.all(
        CONTENT_TABS.map(async (tab) => {
          await loadPagesForType(tab, { reset: true })
          await loadCategoriesForType(tab, { reset: true })
        }),
      )

      if (initialSidebarLoad) {
        activeTab.value = preferredDefaultTab(visibleTabs.value)
        initialSidebarLoad = false
      } else {
        const tabValid = activeTab.value && visibleTabs.value.some((tab) => tab.type === activeTab.value)
        if (!tabValid) {
          activeTab.value = preferredDefaultTab(visibleTabs.value)
        }
      }
      if (sidebarViewMode.value === 'list' && activeTab.value) {
        await loadFlatPagesForType(activeTab.value, true)
      }

      if (!options.selectedPage.value && options.activeSystemView.value === '') {
        const slug = options.getRouteSlug()
        if (slug) {
          await options.onNavigateToSlug(slug)
        } else if (options.indexAvailable.value) {
          await options.onOpenIndexView()
        }
      }
    } finally {
      loading.value = false
    }
  }

  async function doSearch() {
    if (!searchQuery.value.trim()) {
      searchResults.value = null
      return
    }
    loading.value = true
    try {
      const res = await options.api.searchWikiPages(options.getKbId(), searchQuery.value)
      const body = unwrapWikiPayload<{ pages?: WikiPage[] }>(res)
      const hits: WikiPage[] = body?.pages || []
      searchResults.value = hits
      const seen = new Set(pages.value.map((p) => p.id))
      for (const p of hits) {
        if (!seen.has(p.id)) pages.value.push(p)
      }
    } catch (e) {
      console.error('Wiki search failed:', e)
    } finally {
      loading.value = false
    }
  }

  watch(searchQuery, (val) => {
    if (searchTimer) timers.clearTimeout(searchTimer)
    searchTimer = timers.setTimeout(() => {
      if (!val || !val.trim()) {
        searchResults.value = null
      } else {
        void doSearch()
      }
    }, 300)
  })

  function updateSidebarPageTitle(slug: string, title: string) {
    for (const p of pages.value) {
      if (p.slug === slug) p.title = title
    }
    for (const bucket of Object.values(pagesByType.value)) {
      for (const p of bucket.items) {
        if (p.slug === slug) p.title = title
      }
      for (const p of bucket.flatItems) {
        if (p.slug === slug) p.title = title
      }
    }
  }

  function resetTreeForKbSwitch() {
    if (searchTimer) {
      timers.clearTimeout(searchTimer)
      searchTimer = null
    }
    initialSidebarLoad = true
    pagesByType.value = {}
    collapsedDirectories.value = new Set()
    touchedDirectories.value = new Set()
    pendingMove.value = null
    draggedItem.value = null
    dropTargetKey.value = ''
    cancelCreateRootFolder()
    cancelRenameFolder()
  }

  function disposeDirectoryTree() {
    if (searchTimer) {
      timers.clearTimeout(searchTimer)
      searchTimer = null
    }
    if (groupSentinelObserver) {
      groupSentinelObserver.disconnect()
      groupSentinelObserver = null
    }
    if (loadMoreObserver) {
      loadMoreObserver.disconnect()
      loadMoreObserver = null
    }
  }

  onUnmounted(disposeDirectoryTree)

  return {
    loading,
    pagesByType,
    searchQuery,
    searchResults,
    activeTab,
    sidebarViewMode,
    sidebarViewSwitching,
    sidebarTabSwitching,
    pageListRef,
    treeListRef,
    groupSentinelRef,
    groupedPages,
    hasContentPages,
    visibleTabs,
    activeGroup,
    activeFlatPages,
    activeFlatState,
    activeTreeRows,
    draggedItem,
    dropTargetKey,
    pendingMove,
    editingFolderId,
    editingName,
    creatingRootFolder,
    creatingRootFolderName,
    toggleDirectory,
    loadPagesForType,
    loadPages,
    doSearch,
    switchSidebarViewMode,
    setActiveTab,
    onPageDragStart,
    onFolderDragStart,
    onPageDragEnd,
    onDirectoryDragOver,
    onRootDragOver,
    onDirectoryDragLeave,
    onDropOnDirectory,
    cancelPendingMove,
    confirmPendingMove,
    createFolder,
    startCreateRootFolder,
    cancelCreateRootFolder,
    submitCreateRootFolder,
    startRenameFolder,
    cancelRenameFolder,
    commitRenameFolder,
    deleteFolder,
    updateSidebarPageTitle,
    resetTreeForKbSwitch,
    disposeDirectoryTree,
  }
}
