import { computed, nextTick, onUnmounted, ref, shallowRef, watch, type ComputedRef } from 'vue'
import type { WikiGraphData, WikiGraphQueryOptions, WikiPage } from '@/api/wiki'
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess'
import {
  GRAPH_DETAIL_MAX,
  graphDetailLevelForSlug,
  graphDetailSlugs,
  type GraphCluster,
  type LayoutPlacement,
} from '../wikiGraphLayout'
import { unwrapWikiPayload } from './useWikiPageEditor'
import { createWikiGraphRenderer, WIKI_NODE_COLOR_MAP } from './wikiGraphRenderer'

export { WIKI_NODE_COLOR_MAP }

const GRAPH_OVERVIEW_LIMIT = 500
const GRAPH_EGO_LIMIT = 500
const GRAPH_EGO_DEFAULT_DEPTH = 1
const BLOOM_MAX_NODES = 1500
const GROW_FRONTIER_CONCURRENCY = 6
const GRAPH_SYSTEM_PAGE_TYPES = new Set(['index'])

export interface GraphStatusCard {
  icon: string
  title: string
  primary: string
  secondary: string
}

export interface WikiGraphApi {
  getWikiGraph: (kbId: string, opts?: WikiGraphQueryOptions) => Promise<unknown>
  getWikiPage: (kbId: string, slug: string) => Promise<unknown>
  searchWikiPages: (kbId: string, query: string, limit?: number) => Promise<unknown>
}

export interface UseWikiGraphInteractionOptions {
  getKbId: () => string
  getRouteSlug: () => string | undefined
  kbFileAccess: ComputedRef<ProtectedFileAccessContext>
  t: (key: string, params?: Record<string, unknown>) => string
  getTypeLabel: (type: string) => string
  renderMarkdown: (content: string) => string
  hydrateImages: (el: HTMLElement, ctx: ProtectedFileAccessContext) => Promise<void>
  openImagePreview: (url: string) => void
  api: WikiGraphApi
  timers?: {
    setTimeout: typeof setTimeout
    clearTimeout: typeof clearTimeout
  }
}

function isFrontierCandidate(
  node: { slug: string; page_type: string; link_count: number },
  centerSlug: string,
  visibleDegree: number,
): boolean {
  if (node.slug === centerSlug) return false
  if (GRAPH_SYSTEM_PAGE_TYPES.has(node.page_type)) return false
  return (node.link_count || 0) > visibleDegree
}

export function useWikiGraphInteraction(options: UseWikiGraphInteractionOptions) {
  const timers = options.timers ?? { setTimeout, clearTimeout }

  const graphData = ref<WikiGraphData | null>(null)
  const graphSearchValue = ref('')
  const graphRef = ref<HTMLElement | null>(null)
  const drawerBodyRef = ref<HTMLElement | null>(null)
  const graphLoading = ref(false)
  const graphReady = ref(false)
  const showArrows = ref(true)
  const showUnlinkedNodes = ref(true)
  const graphFilterTypes = ref<Set<string>>(
    new Set(['summary', 'entity', 'concept', 'synthesis', 'comparison', 'index']),
  )
  const graphMode = ref<'overview' | 'ego'>('overview')
  const graphCenter = ref('')
  const graphDetailLevel = ref(GRAPH_DETAIL_MAX)
  const graphDetailClusters = shallowRef<GraphCluster[]>([])
  const graphDetailPlacements = shallowRef(new Map<string, LayoutPlacement>())

  const graphDrawerVisible = ref(false)
  const graphDrawerPage = ref<WikiPage | null>(null)
  const graphHighlightSlug = ref<string | null>(null)
  const graphSelectedSlug = ref<string | null>(null)

  const graphFocusGroup = ref<{ key: string; name: string } | null>(null)
  const graphGroups = ref<{ key: string; name: string; count: number }[]>([])

  const graphSearchOptions = ref<{ label: string; value: string }[]>([])
  const graphSearchDefaultOptions = ref<{ label: string; value: string }[]>([])
  const graphSearchLoading = ref(false)
  let graphSearchDebounce: ReturnType<typeof setTimeout> | null = null
  let graphSearchSeq = 0

  const bloomGenerations = new Map<string, number>()
  let bloomCurrentGeneration = 0

  const graphControlsVisible = computed(() => graphReady.value || (!graphLoading.value && graphData.value !== null))

  const graphUnlinkedSlugs = computed(() => {
    const data = graphData.value
    const endpoints = new Set((data?.edges ?? []).flatMap((e) => [e.source, e.target]))
    return new Set((data?.nodes ?? []).filter((n) => n.link_count === 0 && !endpoints.has(n.slug)).map((n) => n.slug))
  })

  const graphDetailMarks = computed<Record<number, string>>(() => ({
    0: options.t('knowledgeEditor.wikiBrowser.graphDetailOverview'),
    1: options.t('knowledgeEditor.wikiBrowser.graphDetailOneHop'),
    2: options.t('knowledgeEditor.wikiBrowser.graphDetailTwoHops'),
    [GRAPH_DETAIL_MAX]: options.t('knowledgeEditor.wikiBrowser.graphDetailAll'),
  }))

  const graphDetailLabel = computed(() => graphDetailMarks.value[graphDetailLevel.value])

  const graphGroupMenu = computed(() =>
    graphGroups.value
      .filter((group) => group.key !== graphFocusGroup.value?.key)
      .map((group) => ({ value: group.key, content: `${group.name} · ${group.count}` })),
  )

  const graphSearchEffectiveOptions = computed(() =>
    graphSearchOptions.value.length > 0 ? graphSearchOptions.value : graphSearchDefaultOptions.value,
  )

  const graphDrawerContent = computed(() => {
    if (!graphDrawerPage.value) return ''
    return options.renderMarkdown(graphDrawerPage.value.content)
  })

  const graphDrawerNeighborStatus = computed(() => {
    const page = graphDrawerPage.value
    const data = graphData.value
    if (!page || !data) return null
    const node = data.nodes.find((n) => n.slug === page.slug)
    if (!node) return null
    const neighbors = new Set<string>()
    for (const e of data.edges) {
      if (e.source === page.slug) neighbors.add(e.target)
      else if (e.target === page.slug) neighbors.add(e.source)
    }
    const visible = neighbors.size
    const total = node.link_count || 0
    const hidden = Math.max(0, total - visible)
    const isEgoCenter = data.meta?.mode === 'ego' && data.meta.center === page.slug
    const isOverview = data.meta?.mode === 'overview'
    return {
      visible,
      total,
      hidden,
      isEgoCenter,
      isOverview,
      fullyExplored: total === 0 || visible >= total || isEgoCenter,
    }
  })

  const graphDrawerNeighborHint = computed(() => {
    const status = graphDrawerNeighborStatus.value
    if (!status) return ''
    if (status.total === 0) return options.t('knowledgeEditor.wikiBrowser.neighborsNone')
    if (status.visible >= status.total) {
      return options.t('knowledgeEditor.wikiBrowser.neighborsAllShown', { total: status.total })
    }
    if (status.isEgoCenter) {
      return options.t('knowledgeEditor.wikiBrowser.neighborsCenterUnreachable', {
        visible: status.visible,
        total: status.total,
        hidden: status.hidden,
      })
    }
    if (status.isOverview) {
      return options.t('knowledgeEditor.wikiBrowser.neighborsOverviewHidden', {
        visible: status.visible,
        total: status.total,
        hidden: status.hidden,
      })
    }
    return options.t('knowledgeEditor.wikiBrowser.neighborsProgress', {
      visible: status.visible,
      total: status.total,
      hidden: status.hidden,
    })
  })

  const graphDrawerCanBloom = computed(() => {
    const status = graphDrawerNeighborStatus.value
    if (!status) return true
    if (status.isEgoCenter) return false
    return status.hidden > 0
  })

  const graphFrontierCount = computed(() => {
    const data = graphData.value
    if (!data || data.meta?.mode !== 'ego') return 0
    const visibleDegree = new Map<string, number>()
    for (const e of data.edges) {
      visibleDegree.set(e.source, (visibleDegree.get(e.source) ?? 0) + 1)
      visibleDegree.set(e.target, (visibleDegree.get(e.target) ?? 0) + 1)
    }
    let count = 0
    const centerSlug = data.meta?.center || ''
    for (const n of data.nodes) {
      if (isFrontierCandidate(n, centerSlug, visibleDegree.get(n.slug) ?? 0)) count += 1
    }
    return count
  })

  const graphFamiliarCount = computed(() => graphData.value?.meta?.familiar_count || 0)

  const graphHelpRows = computed(() => [
    { action: options.t('knowledgeEditor.wikiBrowser.helpClickAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpClickDesc') },
    { action: options.t('knowledgeEditor.wikiBrowser.helpDblClickAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpDblClickDesc') },
    { action: options.t('knowledgeEditor.wikiBrowser.helpShiftClickAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpShiftClickDesc') },
    { action: options.t('knowledgeEditor.wikiBrowser.helpHoverPlusAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpHoverPlusDesc') },
    { action: options.t('knowledgeEditor.wikiBrowser.helpDragAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpDragDesc') },
    { action: options.t('knowledgeEditor.wikiBrowser.helpPanAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpPanDesc') },
    { action: options.t('knowledgeEditor.wikiBrowser.helpZoomAction'), desc: options.t('knowledgeEditor.wikiBrowser.helpZoomDesc') },
  ])

  const graphStatusCard = computed((): GraphStatusCard | null => {
    const data = graphData.value
    if (!data?.meta) return null
    const meta = data.meta
    if (meta.mode === 'ego' && meta.center) {
      const centerNode = data.nodes.find((n) => n.slug === meta.center)
      const centerTitle = centerNode?.title || meta.center
      const typeLabel = centerNode ? options.getTypeLabel(centerNode.page_type) : ''
      const relatedCount = Math.max(0, meta.returned - 1)
      const secondaryParts: string[] = []
      if (typeLabel) secondaryParts.push(typeLabel)
      secondaryParts.push(options.t('knowledgeEditor.wikiBrowser.cardRelatedNodes', { count: relatedCount }))
      return {
        icon: 'focus',
        title: options.t('knowledgeEditor.wikiBrowser.cardEgoTitle'),
        primary: centerTitle,
        secondary: secondaryParts.join(' · '),
      }
    }
    if (meta.mode === 'overview') {
      if (graphDetailLevel.value < GRAPH_DETAIL_MAX) {
        return {
          icon: 'chart-bubble',
          title: options.t('knowledgeEditor.wikiBrowser.cardOverviewTitle'),
          primary: options.t('knowledgeEditor.wikiBrowser.cardOverviewPrimary', {
            returned: graphDetailSlugs(
              graphDetailClusters.value,
              graphDetailPlacements.value,
              graphDetailLevel.value,
            ).size,
            total: meta.total,
          }),
          secondary: options.t('knowledgeEditor.wikiBrowser.cardOverviewDetailHint'),
        }
      }
      const hiddenCount = showUnlinkedNodes.value ? 0 : graphUnlinkedSlugs.value.size
      const secondary =
        hiddenCount > 0
          ? options.t('knowledgeEditor.wikiBrowser.unlinkedHidden', { count: hiddenCount })
          : meta.truncated
            ? options.t('knowledgeEditor.wikiBrowser.cardOverviewHintTruncated')
            : options.t('knowledgeEditor.wikiBrowser.cardOverviewHintFull')
      return {
        icon: 'chart-bubble',
        title: options.t('knowledgeEditor.wikiBrowser.cardOverviewTitle'),
        primary: options.t('knowledgeEditor.wikiBrowser.cardOverviewPrimary', {
          returned: data.nodes.length - hiddenCount,
          total: meta.total,
        }),
        secondary,
      }
    }
    return null
  })

  watch(graphDrawerContent, async () => {
    await nextTick()
    if (drawerBodyRef.value) {
      await options.hydrateImages(drawerBodyRef.value, options.kbFileAccess.value)
    }
  })

  async function openGraphDrawer(slug: string) {
    try {
      const res = await options.api.getWikiPage(options.getKbId(), slug)
      graphDrawerPage.value = unwrapWikiPayload<WikiPage>(res)
      graphDrawerVisible.value = true
    } catch (e) {
      console.error(`Failed to load page ${slug}:`, e)
    }
  }

  const {
    focusGraphGroup,
    exitGraphFocus,
    fitGraphToView,
    toggleArrows,
    findNodeBySlug,
    focusAndSelectNode,
    renderGraph,
    disposeRenderer,
  } = createWikiGraphRenderer({
    graphRef,
    graphData,
    graphReady,
    showArrows,
    showUnlinkedNodes,
    graphUnlinkedSlugs,
    graphMode,
    graphCenter,
    graphDetailLevel,
    graphDetailClusters,
    graphDetailPlacements,
    graphDrawerVisible,
    graphSelectedSlug,
    graphHighlightSlug,
    graphFocusGroup,
    graphGroups,
    t: options.t,
    timers,
    onOpenDrawer: openGraphDrawer,
    onBloomNeighbors: (slug) => loadBloomNeighbors(slug),
    onLoadEgoGraph: (slug) => loadEgoGraph(slug),
  })

  watch(
    graphDetailLevel,
    () => {
      if (graphMode.value !== 'overview') return
      renderGraph({ preserveLayout: true })
    },
    { flush: 'sync' },
  )

  function toggleUnlinkedNodes() {
    showUnlinkedNodes.value = !showUnlinkedNodes.value
    if (showUnlinkedNodes.value && graphMode.value === 'overview' && graphDetailLevel.value < GRAPH_DETAIL_MAX) {
      graphDetailLevel.value = GRAPH_DETAIL_MAX
      return
    }
    if (!showUnlinkedNodes.value && graphSelectedSlug.value && graphUnlinkedSlugs.value.has(graphSelectedSlug.value)) {
      graphSelectedSlug.value = null
      graphHighlightSlug.value = null
      graphDrawerVisible.value = false
    }
    renderGraph()
  }

  async function toggleGraphFilterType(type: string) {
    const newSet = new Set(graphFilterTypes.value)
    if (newSet.has(type)) newSet.delete(type)
    else newSet.add(type)
    graphFilterTypes.value = newSet

    graphHighlightSlug.value = null
    if (
      graphSelectedSlug.value &&
      !newSet.has(graphData.value?.nodes.find((n) => n.slug === graphSelectedSlug.value)?.page_type || '')
    ) {
      graphSelectedSlug.value = null
      graphDrawerVisible.value = false
    }

    if (graphMode.value === 'ego' && graphCenter.value) {
      await loadEgoGraph(graphCenter.value)
    } else {
      await loadGraph()
    }
  }

  function graphFilterTypesToArray(): string[] | undefined {
    const all = ['summary', 'entity', 'concept', 'synthesis', 'comparison', 'index']
    if (all.every((t) => graphFilterTypes.value.has(t))) return undefined
    return Array.from(graphFilterTypes.value)
  }

  function graphFilterSelectsNothing(): boolean {
    return graphFilterTypes.value.size === 0
  }

  function resetBloomGenerations(nodes: { slug: string }[] | undefined) {
    bloomGenerations.clear()
    bloomCurrentGeneration = 0
    if (!nodes) return
    for (const n of nodes) bloomGenerations.set(n.slug, 0)
  }

  function setGraphSearchDefaultFromNodes(nodes: { slug: string; title: string }[] | undefined) {
    if (!nodes) return
    graphSearchDefaultOptions.value = nodes.map((n) => ({ label: n.title, value: n.slug }))
  }

  function handleGraphDrawerClick(e: MouseEvent) {
    const target = e.target as HTMLElement | null
    if (!target) return
    if (target.classList.contains('wiki-content-link')) {
      e.preventDefault()
      const slug = target.getAttribute('data-slug')
      if (slug) void handleGraphSearchSelect(slug)
    } else if (target.tagName.toLowerCase() === 'img') {
      e.preventDefault()
      const src = target.getAttribute('src') || ''
      if (src) options.openImagePreview(src)
    }
  }

  async function loadGraph() {
    graphLoading.value = true
    graphReady.value = false
    graphMode.value = 'overview'
    graphCenter.value = ''
    if (graphFilterSelectsNothing()) {
      graphData.value = { nodes: [], edges: [], meta: { mode: 'overview', total: 0, returned: 0, truncated: false } }
      await nextTick()
      renderGraph()
      graphLoading.value = false
      return
    }
    try {
      const res = await options.api.getWikiGraph(options.getKbId(), {
        mode: 'overview',
        limit: GRAPH_OVERVIEW_LIMIT,
        types: graphFilterTypesToArray(),
      })
      graphData.value = unwrapWikiPayload<WikiGraphData>(res)
      setGraphSearchDefaultFromNodes(graphData.value?.nodes)
      resetBloomGenerations(graphData.value?.nodes)
      await nextTick()
      renderGraph()
      const routeSlug = options.getRouteSlug()
      if (routeSlug) {
        graphSelectedSlug.value = null
        timers.setTimeout(() => {
          void handleGraphSearchSelect(routeSlug)
        }, 300)
      }
    } catch (e) {
      console.error('Failed to load graph:', e)
    } finally {
      graphLoading.value = false
    }
  }

  async function loadEgoGraph(slug: string, depth = GRAPH_EGO_DEFAULT_DEPTH) {
    if (!slug) return
    showUnlinkedNodes.value = true
    graphLoading.value = true
    graphReady.value = false
    if (graphFilterSelectsNothing()) {
      graphData.value = {
        nodes: [],
        edges: [],
        meta: { mode: 'ego', total: 0, returned: 0, truncated: false, center: slug, depth },
      }
      graphMode.value = 'ego'
      graphCenter.value = slug
      resetBloomGenerations(graphData.value.nodes)
      await nextTick()
      renderGraph()
      graphLoading.value = false
      return
    }
    try {
      const res = await options.api.getWikiGraph(options.getKbId(), {
        mode: 'ego',
        center: slug,
        depth,
        limit: GRAPH_EGO_LIMIT,
        types: graphFilterTypesToArray(),
      })
      graphData.value = unwrapWikiPayload<WikiGraphData>(res)
      graphMode.value = 'ego'
      graphCenter.value = slug
      resetBloomGenerations(graphData.value?.nodes)
      graphSelectedSlug.value = slug
      graphHighlightSlug.value = null
      await nextTick()
      renderGraph()
    } catch (e) {
      console.error(`Failed to load ego graph for ${slug}:`, e)
      await nextTick()
      renderGraph()
    } finally {
      graphLoading.value = false
    }
  }

  function mergeGraphData(base: WikiGraphData, incoming: WikiGraphData, gen: number): WikiGraphData {
    const nodeBySlug = new Map<string, WikiGraphData['nodes'][number]>()
    for (const n of base.nodes) nodeBySlug.set(n.slug, n)
    for (const n of incoming.nodes) {
      const existing = nodeBySlug.get(n.slug)
      if (!existing) {
        nodeBySlug.set(n.slug, n)
        bloomGenerations.set(n.slug, gen)
      } else if (n.familiar) {
        existing.familiar = true
      }
    }
    const edgeKey = (e: { source: string; target: string }) => `${e.source}→${e.target}`
    const edgeSeen = new Set<string>()
    const edges: WikiGraphData['edges'] = []
    for (const e of base.edges) {
      const k = edgeKey(e)
      if (!edgeSeen.has(k)) {
        edgeSeen.add(k)
        edges.push(e)
      }
    }
    for (const e of incoming.edges) {
      const k = edgeKey(e)
      if (!edgeSeen.has(k)) {
        edgeSeen.add(k)
        edges.push(e)
      }
    }
    const nodes = Array.from(nodeBySlug.values())
    const familiarCount = nodes.filter((n) => n.familiar).length
    return {
      nodes,
      edges,
      meta: {
        ...incoming.meta,
        mode: incoming.meta?.mode ?? 'ego',
        total: incoming.meta?.total ?? nodes.length,
        truncated: incoming.meta?.truncated ?? false,
        returned: nodes.length,
        familiar_count: familiarCount || undefined,
      },
    }
  }

  function evictBloomOverflow(data: WikiGraphData, protect: Set<string>) {
    if (data.nodes.length <= BLOOM_MAX_NODES) return
    const byGen = new Map<number, string[]>()
    for (const n of data.nodes) {
      const g = bloomGenerations.get(n.slug) ?? 0
      if (g === 0 || protect.has(n.slug)) continue
      const list = byGen.get(g) ?? []
      list.push(n.slug)
      byGen.set(g, list)
    }
    const gens = Array.from(byGen.keys()).toSorted((a, b) => a - b)
    const toRemove = new Set<string>()
    let remaining = data.nodes.length
    for (const g of gens) {
      if (remaining <= BLOOM_MAX_NODES) break
      for (const slug of byGen.get(g)!) {
        if (remaining <= BLOOM_MAX_NODES) break
        toRemove.add(slug)
        remaining -= 1
      }
    }
    if (toRemove.size === 0) return
    data.nodes = data.nodes.filter((n) => !toRemove.has(n.slug))
    data.edges = data.edges.filter((e) => !toRemove.has(e.source) && !toRemove.has(e.target))
    for (const slug of toRemove) bloomGenerations.delete(slug)
  }

  async function loadBloomNeighbors(anchorSlug: string, depth = GRAPH_EGO_DEFAULT_DEPTH) {
    if (!anchorSlug || !graphData.value) return
    if (graphMode.value !== 'ego') {
      await loadEgoGraph(anchorSlug, depth)
      return
    }
    graphLoading.value = true
    try {
      const res = await options.api.getWikiGraph(options.getKbId(), {
        mode: 'ego',
        center: anchorSlug,
        depth,
        limit: GRAPH_EGO_LIMIT,
        types: graphFilterTypesToArray(),
      })
      const incoming = unwrapWikiPayload<WikiGraphData>(res)
      if (!incoming || !Array.isArray(incoming.nodes)) return

      bloomCurrentGeneration += 1
      const merged = mergeGraphData(graphData.value, incoming, bloomCurrentGeneration)
      const protect = new Set<string>([graphCenter.value, anchorSlug, graphSelectedSlug.value || ''].filter(Boolean))
      evictBloomOverflow(merged, protect)

      graphData.value = merged
      await nextTick()
      renderGraph({ preserveLayout: true })
    } catch (e) {
      console.error(`Failed to bloom neighbors for ${anchorSlug}:`, e)
    } finally {
      graphLoading.value = false
    }
  }

  async function growFrontier() {
    if (!graphData.value || graphMode.value !== 'ego' || graphFilterSelectsNothing()) return

    const visibleDegree = new Map<string, number>()
    for (const e of graphData.value.edges) {
      visibleDegree.set(e.source, (visibleDegree.get(e.source) ?? 0) + 1)
      visibleDegree.set(e.target, (visibleDegree.get(e.target) ?? 0) + 1)
    }
    const frontier: string[] = []
    for (const n of graphData.value.nodes) {
      if (isFrontierCandidate(n, graphCenter.value, visibleDegree.get(n.slug) ?? 0)) frontier.push(n.slug)
    }
    if (frontier.length === 0) return

    graphLoading.value = true
    try {
      const responses: WikiGraphData[] = []
      let cursor = 0
      const worker = async () => {
        while (cursor < frontier.length) {
          const idx = cursor++
          const slug = frontier[idx]
          try {
            const res = await options.api.getWikiGraph(options.getKbId(), {
              mode: 'ego',
              center: slug,
              depth: GRAPH_EGO_DEFAULT_DEPTH,
              limit: GRAPH_EGO_LIMIT,
              types: graphFilterTypesToArray(),
            })
            const data = unwrapWikiPayload<WikiGraphData>(res)
            if (data?.nodes) responses.push(data)
          } catch (e) {
            console.error(`growFrontier: ego fetch failed for ${slug}:`, e)
          }
        }
      }
      const workerCount = Math.min(GROW_FRONTIER_CONCURRENCY, frontier.length)
      await Promise.all(Array.from({ length: workerCount }, () => worker()))
      if (responses.length === 0) return

      bloomCurrentGeneration += 1
      const gen = bloomCurrentGeneration
      let merged = graphData.value
      for (const incoming of responses) merged = mergeGraphData(merged, incoming, gen)
      const protect = new Set<string>([graphCenter.value, graphSelectedSlug.value || ''].filter(Boolean))
      evictBloomOverflow(merged, protect)

      graphData.value = merged
      await nextTick()
      renderGraph({ preserveLayout: true })
    } finally {
      graphLoading.value = false
    }
  }

  async function handleGraphRemoteSearch(keyword: string) {
    const q = (keyword || '').trim()
    if (graphSearchDebounce) {
      timers.clearTimeout(graphSearchDebounce)
      graphSearchDebounce = null
    }
    if (!q) {
      graphSearchOptions.value = []
      graphSearchLoading.value = false
      return
    }
    graphSearchLoading.value = true
    const seq = ++graphSearchSeq
    graphSearchDebounce = timers.setTimeout(async () => {
      try {
        const res = await options.api.searchWikiPages(options.getKbId(), q, 20)
        if (seq !== graphSearchSeq) return
        const body = unwrapWikiPayload<{ pages?: WikiPage[] }>(res)
        const hits: WikiPage[] = body?.pages || []
        graphSearchOptions.value = hits.map((p) => ({ label: p.title, value: p.slug }))
      } catch (e) {
        if (seq !== graphSearchSeq) return
        console.error('Wiki search failed:', e)
        graphSearchOptions.value = []
      } finally {
        if (seq === graphSearchSeq) graphSearchLoading.value = false
      }
    }, 200)
  }

  async function handleGraphSearchSelect(value: string) {
    if (!value) return
    const revealUnlinked = !showUnlinkedNodes.value && graphUnlinkedSlugs.value.has(value)
    if (revealUnlinked) showUnlinkedNodes.value = true

    if (graphMode.value === 'overview') {
      const placements = graphDetailPlacements.value
      const needed = placements.has(value)
        ? graphDetailLevelForSlug(graphDetailClusters.value, placements, value)
        : revealUnlinked
          ? GRAPH_DETAIL_MAX
          : null
      if (needed !== null && needed > graphDetailLevel.value) {
        graphDetailLevel.value = needed
      } else if (revealUnlinked) {
        renderGraph()
      }
    } else if (revealUnlinked) {
      renderGraph()
    }

    let node = findNodeBySlug(value)
    if (!node) {
      await loadEgoGraph(value)
      node = findNodeBySlug(value)
      if (!node) {
        void openGraphDrawer(value)
        timers.setTimeout(() => {
          graphSearchValue.value = ''
        }, 300)
        return
      }
    }

    focusAndSelectNode(value, node)
    void openGraphDrawer(value)
    timers.setTimeout(() => {
      graphSearchValue.value = ''
    }, 300)
  }

  async function handleGraphSearchEnter(context: { inputValue: string }) {
    const value = context.inputValue?.trim()
    if (!value) return
    const match = graphSearchOptions.value.find(
      (opt) =>
        opt.label.toLowerCase().includes(value.toLowerCase()) ||
        opt.value.toLowerCase().includes(value.toLowerCase()),
    )
    if (match) {
      await handleGraphSearchSelect(match.value)
      return
    }
    try {
      const res = await options.api.searchWikiPages(options.getKbId(), value, 1)
      const body = unwrapWikiPayload<{ pages?: WikiPage[] }>(res)
      const hits: WikiPage[] = body?.pages || []
      if (hits.length > 0) await handleGraphSearchSelect(hits[0].slug)
    } catch (e) {
      console.error('Wiki search failed on enter:', e)
    }
  }

  function resetGraphForKbSwitch() {
    graphDrawerVisible.value = false
    graphDrawerPage.value = null
    graphSelectedSlug.value = null
    graphHighlightSlug.value = null
    if (graphSearchDebounce) {
      timers.clearTimeout(graphSearchDebounce)
      graphSearchDebounce = null
    }
  }

  function disposeGraphInteraction() {
    disposeRenderer()
    if (graphSearchDebounce) {
      timers.clearTimeout(graphSearchDebounce)
      graphSearchDebounce = null
    }
  }

  onUnmounted(disposeGraphInteraction)

  return {
    GRAPH_DETAIL_MAX,
    graphData,
    graphSearchValue,
    graphRef,
    drawerBodyRef,
    graphLoading,
    graphReady,
    graphControlsVisible,
    showArrows,
    showUnlinkedNodes,
    graphUnlinkedSlugs,
    graphFilterTypes,
    graphMode,
    graphCenter,
    graphDetailLevel,
    graphDetailMarks,
    graphDetailLabel,
    graphDrawerVisible,
    graphDrawerPage,
    graphDrawerContent,
    graphDrawerNeighborHint,
    graphDrawerCanBloom,
    graphFrontierCount,
    graphFamiliarCount,
    graphHelpRows,
    graphStatusCard,
    graphFocusGroup,
    graphGroupMenu,
    graphSearchEffectiveOptions,
    graphSearchLoading,
    focusGraphGroup,
    exitGraphFocus,
    fitGraphToView,
    toggleUnlinkedNodes,
    toggleGraphFilterType,
    toggleArrows,
    loadGraph,
    loadEgoGraph,
    loadBloomNeighbors,
    growFrontier,
    handleGraphDrawerClick,
    handleGraphRemoteSearch,
    handleGraphSearchSelect,
    handleGraphSearchEnter,
    resetGraphForKbSwitch,
    disposeGraphInteraction,
  }
}
