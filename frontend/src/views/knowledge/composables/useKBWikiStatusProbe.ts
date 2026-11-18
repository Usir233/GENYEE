import { computed, ref, watch, type ComputedRef, type Ref } from 'vue'
import type { KbTab } from '../components/KBDocumentHeader.vue'

export interface WikiStatusSnapshot {
  pendingTasks: number
  isActive: boolean
  pendingIssues: number
}

interface WikiStatsApiResponse {
  data?: {
    pending_tasks?: number
    is_active?: boolean
    pending_issues?: number
  }
  pending_tasks?: number
  is_active?: boolean
  pending_issues?: number
}

const VALID_KB_TABS: readonly KbTab[] = ['documents', 'wiki', 'graph', 'gallery']

export function useKBWikiStatusProbe(deps: {
  kbId: ComputedRef<string>
  isWiki: ComputedRef<boolean>
  routeQuery: () => Record<string, unknown>
  replaceRouteQuery: (query: Record<string, unknown>) => unknown
  t: (key: string, params?: Record<string, unknown>) => string
  getWikiStats: (kbId: string) => Promise<unknown>
}) {
  const initialQueryTab = String(deps.routeQuery().tab || '')
  const activeKbTab = ref<KbTab>(
    VALID_KB_TABS.includes(initialQueryTab as KbTab) ? (initialQueryTab as KbTab) : 'documents',
  )

  const wikiStatus = ref<WikiStatusSnapshot>({
    pendingTasks: 0,
    isActive: false,
    pendingIssues: 0,
  })
  const wikiIsIndexing = computed(
    () => wikiStatus.value.isActive || wikiStatus.value.pendingTasks > 0,
  )
  const wikiIndexingTip = computed(() => {
    if (!wikiIsIndexing.value) return ''
    return deps.t('knowledgeEditor.wikiBrowser.queueStatus', {
      count: wikiStatus.value.pendingTasks || 0,
    })
  })

  const kbViewTabs = computed(() => {
    const w = 'knowledgeEditor.wikiBrowser'
    const tabs: Array<{ key: KbTab; icon: string; label: string; tip: string; indexing?: boolean }> = [
      { key: 'documents', icon: 'file', label: deps.t(`${w}.tabDocuments`), tip: deps.t(`${w}.tabDocumentsTip`) },
    ]
    if (deps.isWiki.value) {
      const indexing = wikiIsIndexing.value
      tabs.push(
        {
          key: 'wiki',
          icon: 'book-open',
          label: 'Wiki',
          tip: indexing ? wikiIndexingTip.value : deps.t(`${w}.tabWikiTip`),
          indexing,
        },
        {
          key: 'graph',
          icon: 'relation',
          label: deps.t(`${w}.tabGraph`),
          tip: indexing ? wikiIndexingTip.value : deps.t(`${w}.tabGraphTip`),
          indexing,
        },
      )
    }
    tabs.push({ key: 'gallery', icon: 'image', label: deps.t(`${w}.tabGallery`), tip: deps.t(`${w}.tabGalleryTip`) })
    return tabs
  })

  const shownKbTab = computed<KbTab>(() =>
    kbViewTabs.value.some((tab) => tab.key === activeKbTab.value) ? activeKbTab.value : 'documents',
  )

  const onViewWikiInGraph = async (slug: string) => {
    await deps.replaceRouteQuery({ ...deps.routeQuery(), tab: 'graph', slug })
    activeKbTab.value = 'graph'
  }

  watch(activeKbTab, (tab) => {
    const query = { ...deps.routeQuery() }
    if (tab === 'documents') delete query.tab
    else query.tab = tab
    void deps.replaceRouteQuery(query)
  })

  let wikiStatusTimer: ReturnType<typeof setInterval> | null = null
  let wikiStatusProbeTimers: Array<ReturnType<typeof setTimeout>> = []
  let wikiStatusDisposed = false

  const stopWikiStatusPolling = () => {
    if (wikiStatusTimer) {
      clearInterval(wikiStatusTimer)
      wikiStatusTimer = null
    }
  }

  const clearWikiStatusProbes = () => {
    wikiStatusProbeTimers.forEach((timer) => clearTimeout(timer))
    wikiStatusProbeTimers = []
  }

  const onWikiStatusChange = (payload: WikiStatusSnapshot) => {
    wikiStatus.value = payload
  }

  const fetchWikiStatusOnce = async () => {
    const reqKbId = deps.kbId.value
    if (wikiStatusDisposed || !reqKbId || !deps.isWiki.value) return
    try {
      const res = (await deps.getWikiStats(reqKbId)) as WikiStatsApiResponse | null | undefined
      if (wikiStatusDisposed || deps.kbId.value !== reqKbId || !deps.isWiki.value) return
      const data = res?.data || res
      if (!data) return
      wikiStatus.value = {
        pendingTasks: data.pending_tasks || 0,
        isActive: Boolean(data.is_active),
        pendingIssues: data.pending_issues || 0,
      }
      if (wikiIsIndexing.value) {
        if (!wikiStatusTimer) {
          wikiStatusTimer = setInterval(() => {
            void fetchWikiStatusOnce()
          }, 5000)
        }
      } else {
        stopWikiStatusPolling()
      }
    } catch {
      // Ignore transient wiki status polling errors
    }
  }

  const scheduleWikiStatusProbes = () => {
    if (!deps.kbId.value || !deps.isWiki.value) return
    clearWikiStatusProbes()
    const delays = [500, 2000, 5000, 10000]
    delays.forEach((delay) => {
      const timer = setTimeout(() => {
        void fetchWikiStatusOnce()
      }, delay)
      wikiStatusProbeTimers.push(timer)
    })
  }

  watch(
    [deps.kbId, deps.isWiki],
    ([newKbId, newIsWiki]) => {
      stopWikiStatusPolling()
      clearWikiStatusProbes()
      wikiStatus.value = { pendingTasks: 0, isActive: false, pendingIssues: 0 }
      if (newKbId && newIsWiki) {
        void fetchWikiStatusOnce()
      }
    },
    { immediate: true },
  )

  const disposeWikiStatus = () => {
    wikiStatusDisposed = true
    stopWikiStatusPolling()
    clearWikiStatusProbes()
  }

  return {
    activeKbTab,
    kbViewTabs,
    shownKbTab,
    onViewWikiInGraph,
    wikiStatus: wikiStatus as Ref<WikiStatusSnapshot>,
    wikiIsIndexing,
    wikiIndexingTip,
    isDisposed: () => wikiStatusDisposed,
    onWikiStatusChange,
    fetchWikiStatusOnce,
    scheduleWikiStatusProbes,
    disposeWikiStatus,
  }
}
