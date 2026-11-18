import type { ComputedRef, Ref, ShallowRef } from 'vue'
import type { WikiGraphData } from '@/api/wiki'
import {
  buildGraphLayout,
  GRAPH_DETAIL_MAX,
  graphClusterOutline,
  graphDetailSlugs,
  graphLandmarkTitle,
  graphNodeRadius,
  graphPointBounds,
  initialGraphViewport,
  stepGraphLayout,
  visibleGraphLabels,
  type GraphBounds,
  type GraphCluster,
  type LayoutPlacement,
} from '../wikiGraphLayout'

export const WIKI_NODE_COLOR_MAP: Record<string, string> = {
  summary: 'var(--gy-accent-blue)',
  entity: 'var(--gy-success)',
  concept: 'var(--gy-warning)',
  synthesis: 'var(--gy-accent-teal)',
  comparison: 'var(--gy-danger)',
  index: 'var(--gy-text-tertiary)',
}

const graphEdgeColor = 'var(--gy-border-strong)'
const graphEdgeWidth = '0.8'
const graphEdgeOpacity = '0.42'

export interface GNode { x: number; y: number; vx: number; vy: number; slug: string; title: string; type: string; linkCount: number; pinned: boolean; familiar: boolean; labelWidth: number }
interface GraphNodeEl { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }
interface GraphEdgeEl { line: SVGLineElement; source: string; target: string; bidir: boolean }

export interface GraphPanZoomHandle {
  setScale: (s: number) => void
  setTranslate: (x: number, y: number) => void
  apply: () => void
  flyTo: (x: number, y: number, s?: number, duration?: number) => void
  getScale: () => number
  getTranslate: () => { x: number; y: number }
}

export interface CreateWikiGraphRendererOptions {
  graphRef: Ref<HTMLElement | null>
  graphData: Ref<WikiGraphData | null>
  graphReady: Ref<boolean>
  showArrows: Ref<boolean>
  showUnlinkedNodes: Ref<boolean>
  graphUnlinkedSlugs: ComputedRef<Set<string>>
  graphMode: Ref<'overview' | 'ego'>
  graphCenter: Ref<string>
  graphDetailLevel: Ref<number>
  graphDetailClusters: ShallowRef<GraphCluster[]>
  graphDetailPlacements: ShallowRef<Map<string, LayoutPlacement>>
  graphDrawerVisible: Ref<boolean>
  graphSelectedSlug: Ref<string | null>
  graphHighlightSlug: Ref<string | null>
  graphFocusGroup: Ref<{ key: string; name: string } | null>
  graphGroups: Ref<{ key: string; name: string; count: number }[]>
  t: (key: string, params?: Record<string, unknown>) => string
  timers: { setTimeout: typeof setTimeout; clearTimeout: typeof clearTimeout }
  onOpenDrawer: (slug: string) => Promise<void> | void
  onBloomNeighbors: (slug: string) => Promise<void> | void
  onLoadEgoGraph: (slug: string) => Promise<void> | void
}

export function createWikiGraphRenderer(opts: CreateWikiGraphRendererOptions) {
  const {
    graphRef, graphData, graphReady, showArrows, showUnlinkedNodes, graphUnlinkedSlugs,
    graphMode, graphCenter, graphDetailLevel, graphDetailClusters, graphDetailPlacements,
    graphDrawerVisible, graphSelectedSlug, graphHighlightSlug, graphFocusGroup, graphGroups, timers,
  } = opts

  let graphNodes: GNode[] = []
  let graphLayoutBounds: GraphBounds | null = null
  let graphClusterEls: { group: SVGGElement; cluster: GraphCluster; anchorSlug: string; labelWidth: number; priority: number }[] = []
  let graphContourEls: { path: SVGPathElement; cluster: GraphCluster; scale?: number; positions?: number[] }[] = []
  let graphSectionEls: { text: SVGTextElement; top: number }[] = []
  let graphImportantSlugs = new Set<string>()
  let graphFocusedSlugs = new Set<string>()
  let graphHoverSuppressedUntil = 0
  let graphSvg: SVGSVGElement | null = null
  let graphAnimFrame = 0
  let refreshGraphLabels = () => {}
  let graphEdgeMarkersVisible = true
  let disposeGraphPanZoom = () => {}
  let graphHoverLeaveTimer: ReturnType<typeof setTimeout> | null = null
  let graphPanZoomRef: GraphPanZoomHandle | null = null
  let graphNodeElsRef: GraphNodeEl[] = []
  let graphEdgeElsRef: GraphEdgeEl[] = []
  let graphAdjacencyRef = new Map<string, Set<string>>()
  let graphFocusReturnViewport: { scale: number; x: number; y: number } | null = null
  let graphGroupFocusers = new Map<string, () => void>()

  function focusGraphGroup(key: string) {
    graphGroupFocusers.get(key)?.()
  }

  function exitGraphFocus() {
    if (!graphFocusGroup.value) return
    graphFocusedSlugs.clear()
    graphFocusGroup.value = null
    const back = graphFocusReturnViewport
    graphFocusReturnViewport = null
    clearHighlight(graphNodeElsRef, graphEdgeElsRef)
    if (back && graphPanZoomRef) graphPanZoomRef.flyTo(back.x, back.y, back.scale, 500)
  }

  function fitGraphToView() {
    graphFocusedSlugs.clear()
    graphFocusGroup.value = null
    graphFocusReturnViewport = null
    if (!graphReady.value || !graphPanZoomRef || !graphRef.value || graphNodes.length === 0) return
    clearHighlight(graphNodeElsRef, graphEdgeElsRef)

    const container = graphRef.value
    const width = container.clientWidth
    const height = container.clientHeight
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const node of graphNodes) {
      minX = Math.min(minX, node.x)
      minY = Math.min(minY, node.y)
      maxX = Math.max(maxX, node.x)
      maxY = Math.max(maxY, node.y)
    }
    if (graphLayoutBounds) {
      minX = Math.min(minX, graphLayoutBounds.left)
      minY = Math.min(minY, graphLayoutBounds.top)
      maxX = Math.max(maxX, graphLayoutBounds.right)
      maxY = Math.max(maxY, graphLayoutBounds.bottom)
    }
    const cx = (minX + maxX) / 2
    const cy = (minY + maxY) / 2
    const padding = 100
    const boxWidth = Math.max(maxX - minX, 100)
    const boxHeight = Math.max(maxY - minY, 100)
    const sidebarWidth = graphDrawerVisible.value ? 480 : Math.min(240, width * 0.25)
    const availableWidth = Math.max(160, width - sidebarWidth)
    const scaleX = Math.max(100, availableWidth - padding * 2) / boxWidth
    const scaleY = Math.max(100, height - padding * 2) / boxHeight
    const targetScale = Math.max(0.005, Math.min(2, Math.min(scaleX, scaleY)))
    graphPanZoomRef.flyTo(availableWidth / 2 - cx * targetScale, height / 2 - cy * targetScale, targetScale, 600)
  }

  function toggleArrows() {
    showArrows.value = !showArrows.value
    for (const e of graphEdgeElsRef) {
      if (showArrows.value) {
        e.line.setAttribute('marker-end', 'url(#arrow-end)')
        if (e.bidir) e.line.setAttribute('marker-start', 'url(#arrow-start)')
      } else {
        e.line.removeAttribute('marker-end')
        e.line.removeAttribute('marker-start')
      }
    }
    refreshGraphLabels()
  }

  function findNodeBySlug(slug: string): GNode | undefined {
    return graphNodes.find((n) => n.slug === slug)
  }

  function focusAndSelectNode(slug: string, node: GNode) {
    if (graphPanZoomRef && graphRef.value) {
      const cw = graphRef.value.clientWidth
      const ch = graphRef.value.clientHeight
      const currentScale = graphPanZoomRef.getScale()
      graphPanZoomRef.flyTo(cw / 2 - node.x * currentScale - 240, ch / 2 - node.y * currentScale)
    }
    graphSelectedSlug.value = slug
    graphHighlightSlug.value = slug
    if (graphNodeElsRef.length > 0) {
      applyHighlight(slug, graphAdjacencyRef, graphNodeElsRef, graphEdgeElsRef)
    }
  }

  function renderGraph(renderOpts: { preserveLayout?: boolean } = {}) {
    const container = graphRef.value
    const data = graphData.value
    if (!container) return
    if (graphAnimFrame && typeof cancelAnimationFrame === 'function') {
      cancelAnimationFrame(graphAnimFrame)
      graphAnimFrame = 0
    }
    if (graphHoverLeaveTimer) {
      timers.clearTimeout(graphHoverLeaveTimer)
      graphHoverLeaveTimer = null
    }
    const previousViewport =
      renderOpts.preserveLayout && graphPanZoomRef
        ? { scale: graphPanZoomRef.getScale(), ...graphPanZoomRef.getTranslate() }
        : undefined
    disposeGraphPanZoom()
    refreshGraphLabels = () => {}
    graphPanZoomRef = null
    graphSvg = null
    graphLayoutBounds = null
    graphClusterEls = []
    graphContourEls = []
    graphSectionEls = []
    graphFocusedSlugs.clear()
    graphFocusGroup.value = null
    graphFocusReturnViewport = null
    graphGroups.value = []
    graphGroupFocusers = new Map()
    graphReady.value = false
    if (!data || !data.nodes?.length) {
      container.replaceChildren()
      graphNodes = []
      graphNodeElsRef = []
      graphEdgeElsRef = []
      graphAdjacencyRef = new Map()
      if (graphMode.value === 'overview') {
        graphDetailClusters.value = []
        graphDetailPlacements.value = new Map()
      }
      return
    }
    const graph = showUnlinkedNodes.value
      ? data
      : { ...data, nodes: data.nodes.filter((n) => !graphUnlinkedSlugs.value.has(n.slug)) }

    const width = container.clientWidth || 800
    const height = container.clientHeight || 600
    const priorCoords = new Map<string, { x: number; y: number; pinned: boolean }>()
    if (renderOpts.preserveLayout) {
      for (const n of graphNodes) priorCoords.set(n.slug, { x: n.x, y: n.y, pinned: n.pinned })
    }

    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`)
    svg.style.width = '100%'
    svg.style.height = '100%'
    svg.style.cursor = 'grab'
    svg.style.userSelect = 'none'
    container.replaceChildren(svg)
    graphSvg = svg

    const rootG = document.createElementNS('http://www.w3.org/2000/svg', 'g')
    rootG.setAttribute('class', 'graph-root')
    svg.appendChild(rootG)

    const edgeG = document.createElementNS('http://www.w3.org/2000/svg', 'g')
    edgeG.setAttribute('pointer-events', 'none')
    rootG.appendChild(edgeG)

    const nodeG = document.createElementNS('http://www.w3.org/2000/svg', 'g')
    rootG.appendChild(nodeG)

    const layoutPrior = graphMode.value === 'overview' ? new Map<string, { x: number; y: number }>() : priorCoords
    const layout = buildGraphLayout(graph.nodes, graph.edges, width, height, layoutPrior)
    const visibleSlugs =
      graphMode.value === 'overview'
        ? graphDetailSlugs(layout.clusters, layout.placements, graphDetailLevel.value)
        : new Set(graph.nodes.map((n) => n.slug))
    if (graphMode.value === 'overview') {
      graphDetailClusters.value = layout.clusters
      graphDetailPlacements.value = layout.placements
    }
    const renderNodes = graph.nodes.filter((n) => visibleSlugs.has(n.slug))
    const renderEdges =
      graphMode.value === 'overview'
        ? graph.edges.filter((edge) => visibleSlugs.has(edge.source) && visibleSlugs.has(edge.target))
        : graph.edges
    const showClusterChrome = graphMode.value === 'overview' && graphDetailLevel.value >= GRAPH_DETAIL_MAX

    const adjacency = new Map<string, Set<string>>()
    for (const edge of renderEdges) {
      if (!adjacency.has(edge.source)) adjacency.set(edge.source, new Set())
      if (!adjacency.has(edge.target)) adjacency.set(edge.target, new Set())
      adjacency.get(edge.source)!.add(edge.target)
      adjacency.get(edge.target)!.add(edge.source)
    }

    const regions = showClusterChrome || graphMode.value !== 'overview' ? layout.sections : []
    if (regions.length) {
      graphLayoutBounds = {
        left: Math.min(...regions.map((r) => r.left)),
        top: Math.min(...regions.map((r) => r.top)),
        right: Math.max(...regions.map((r) => r.right)),
        bottom: Math.max(...regions.map((r) => r.bottom)),
      }
    }
    graphImportantSlugs = new Set(
      [...renderNodes]
        .sort((a, b) => b.link_count - a.link_count)
        .slice(0, Math.max(1, Math.min(12, Math.ceil(renderNodes.length / 30))))
        .map((n) => n.slug),
    )

    const overviewClusters = showClusterChrome ? layout.clusters : []
    for (const cluster of overviewClusters) {
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path')
      path.classList.add('graph-cluster-outline')
      path.setAttribute('fill', 'var(--gy-surface-subtle)')
      path.setAttribute('data-cluster', cluster.representative)
      path.setAttribute('stroke', 'var(--gy-border)')
      path.setAttribute('stroke-width', '1')
      path.setAttribute('vector-effect', 'non-scaling-stroke')
      path.setAttribute('pointer-events', 'visiblePainted')
      rootG.insertBefore(path, edgeG)
      graphContourEls.push({ path, cluster })
    }
    if (overviewClusters.length) {
      const clusterBySlug = new Map(
        overviewClusters.flatMap((c) => c.slugs.map((slug) => [slug, c.representative] as const)),
      )
      let hoveredCluster: string | null = null
      const highlightCluster = (cluster: string | null) => {
        if (cluster === hoveredCluster) return
        hoveredCluster = cluster
        for (const { path, cluster: group } of graphContourEls) {
          path.classList.toggle('is-hovered', group.representative === cluster)
        }
      }
      svg.addEventListener('mousemove', (e) => {
        const target = e.target as Element
        const slug =
          target.closest('[data-graph-node]')?.getAttribute('data-graph-node') ??
          target.closest('[data-representative]')?.getAttribute('data-representative')
        highlightCluster(
          slug
            ? (clusterBySlug.get(slug) ?? null)
            : (target.closest('[data-cluster]')?.getAttribute('data-cluster') ?? null),
        )
      })
      svg.addEventListener('mouseleave', () => highlightCluster(null))
    }

    if (showClusterChrome) {
      const pageBySlug = new Map(graph.nodes.map((n) => [n.slug, n]))
      for (const cluster of layout.clusters) {
        if (cluster.slugs.length < 5) continue
        const focus = () => {
          if (!graphPanZoomRef) return
          if (graphFocusGroup.value?.key === cluster.representative) {
            exitGraphFocus()
            return
          }
          if (!graphFocusGroup.value) {
            graphFocusReturnViewport = { scale: graphPanZoomRef.getScale(), ...graphPanZoomRef.getTranslate() }
          }
          graphFocusGroup.value = {
            key: cluster.representative,
            name: graphLandmarkTitle(pageBySlug.get(cluster.anchors[0]!)!.title),
          }
          graphFocusedSlugs = new Set(cluster.slugs)
          graphSelectedSlug.value = null
          graphHighlightSlug.value = null
          graphDrawerVisible.value = false
          graphHoverSuppressedUntil = performance.now() + 500
          clearHighlight(graphNodeElsRef, graphEdgeElsRef)
          const availableWidth = Math.max(160, width - Math.min(240, width * 0.25))
          const members = new Set(cluster.slugs)
          const bounds = graphPointBounds(graphNodes.filter((n) => members.has(n.slug)))
          const scale = Math.min(
            1.75,
            Math.max(
              0.4,
              Math.min(
                (availableWidth - 160) / Math.max(100, bounds.right - bounds.left),
                (height - 180) / Math.max(100, bounds.bottom - bounds.top),
              ),
            ),
          )
          graphPanZoomRef.flyTo(
            availableWidth / 2 - ((bounds.left + bounds.right) / 2) * scale,
            height / 2 - ((bounds.top + bounds.bottom) / 2) * scale,
            scale,
          )
        }
        const addCaption = (anchorSlug: string, rank: number) => {
          const label = graphLandmarkTitle(pageBySlug.get(anchorSlug)!.title)
          const caption = document.createElementNS('http://www.w3.org/2000/svg', 'g')
          caption.classList.add('graph-cluster-caption')
          caption.setAttribute('data-representative', anchorSlug)
          caption.setAttribute('role', 'button')
          caption.setAttribute('tabindex', '0')
          caption.setAttribute('aria-label', `${label} · ${opts.t('knowledgeEditor.wikiBrowser.focusGroup')}`)
          const title = document.createElementNS('http://www.w3.org/2000/svg', 'text')
          title.textContent = label.length > 20 ? `${label.slice(0, 20)}…` : label
          title.setAttribute('text-anchor', 'middle')
          title.setAttribute('font-size', '13')
          title.setAttribute('font-weight', '600')
          title.setAttribute('fill', 'var(--gy-text-primary)')
          const pill = document.createElementNS('http://www.w3.org/2000/svg', 'rect')
          pill.classList.add('graph-caption-pill')
          caption.append(pill, title)
          rootG.appendChild(caption)
          const textWidth = title.getComputedTextLength() || title.textContent.length * 13
          pill.setAttribute('x', String(-textWidth / 2 - 12))
          pill.setAttribute('y', '-17')
          pill.setAttribute('width', String(textWidth + 24))
          pill.setAttribute('height', '24')
          pill.setAttribute('rx', '12')
          graphClusterEls.push({
            group: caption,
            cluster,
            anchorSlug,
            labelWidth: textWidth + 24,
            priority: 100000 + cluster.slugs.length - rank,
          })
          caption.addEventListener('click', (e) => {
            e.stopPropagation()
            focus()
          })
          caption.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              e.stopPropagation()
              focus()
            }
          })
        }
        cluster.anchors.forEach((slug, rank) => addCaption(slug, rank))
        graphGroupFocusers.set(cluster.representative, focus)
        graphGroups.value.push({
          key: cluster.representative,
          count: cluster.slugs.length,
          name: graphLandmarkTitle(pageBySlug.get(cluster.anchors[0]!)!.title),
        })
      }
    }

    for (const section of regions) {
      const caption = document.createElementNS('http://www.w3.org/2000/svg', 'text')
      caption.setAttribute('x', String(section.left + 16))
      caption.setAttribute('y', String(section.top + 20))
      caption.setAttribute('font-size', '13')
      caption.setAttribute('fill', 'var(--gy-text-secondary)')
      caption.setAttribute('pointer-events', 'none')
      caption.textContent = opts.t(
        section.kind === 'unlinked'
          ? 'knowledgeEditor.wikiBrowser.unlinkedSection'
          : 'knowledgeEditor.wikiBrowser.offscreenSection',
        { count: section.count },
      )
      rootG.insertBefore(caption, edgeG)
      graphSectionEls.push({ text: caption, top: section.top })
    }

    const nodeMap = new Map<string, GNode>()
    graphNodes = renderNodes.map((n) => {
      const prior = priorCoords.get(n.slug)
      const placement = layout.placements.get(n.slug)!
      const node: GNode = {
        x: prior?.x ?? placement.x, y: prior?.y ?? placement.y, vx: 0, vy: 0,
        slug: n.slug, title: n.title, type: n.page_type,
        linkCount: n.link_count || 0, pinned: prior?.pinned ?? false,
        familiar: !!n.familiar, labelWidth: Math.min(n.title.length, 15) * 11,
      }
      nodeMap.set(n.slug, node)
      return node
    })
    const nodeRadius = (n: GNode) => graphNodeRadius(n.linkCount)

    const setAttrs = (el: Element, attrs: Record<string, string>) => {
      for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v)
    }
    const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs')
    for (const spec of [
      { id: 'arrow-end', refX: '10', d: 'M0,0 L10,3 L0,6 L2,3 Z', fill: graphEdgeColor },
      { id: 'arrow-start', refX: '0', d: 'M10,0 L0,3 L10,6 L8,3 Z', fill: graphEdgeColor },
      { id: 'arrow-end-hl', refX: '10', d: 'M0,0 L10,3 L0,6 L2,3 Z', fill: 'var(--gy-brand)' },
      { id: 'arrow-start-hl', refX: '0', d: 'M10,0 L0,3 L10,6 L8,3 Z', fill: 'var(--gy-brand)' },
    ]) {
      const m = document.createElementNS('http://www.w3.org/2000/svg', 'marker')
      setAttrs(m, { id: spec.id, viewBox: '0 0 10 6', refX: spec.refX, refY: '3', markerWidth: '8', markerHeight: '6', orient: 'auto' })
      const p = document.createElementNS('http://www.w3.org/2000/svg', 'path')
      setAttrs(p, { d: spec.d, fill: spec.fill })
      m.appendChild(p)
      defs.appendChild(m)
    }

    const filter = document.createElementNS('http://www.w3.org/2000/svg', 'filter')
    setAttrs(filter, { id: 'node-shadow', x: '-20%', y: '-20%', width: '140%', height: '140%' })
    const dropShadow = document.createElementNS('http://www.w3.org/2000/svg', 'feDropShadow')
    setAttrs(dropShadow, { dx: '0', dy: '2', stdDeviation: '3', 'flood-color': 'var(--gy-text-primary)', 'flood-opacity': '0.15' })
    filter.appendChild(dropShadow)
    defs.appendChild(filter)
    svg.appendChild(defs)

    const edgePairSet = new Set<string>()
    for (const edge of renderEdges) edgePairSet.add(`${edge.source}→${edge.target}`)

    const edgeEls: GraphEdgeEl[] = []
    const processedPairs = new Set<string>()
    for (const edge of renderEdges) {
      const pairKey = [edge.source, edge.target].toSorted().join('↔')
      if (processedPairs.has(pairKey)) continue
      processedPairs.add(pairKey)
      const bidir = edgePairSet.has(`${edge.target}→${edge.source}`)
      const line = document.createElementNS('http://www.w3.org/2000/svg', 'line')
      line.setAttribute('stroke', graphEdgeColor)
      line.setAttribute('stroke-width', graphEdgeWidth)
      line.setAttribute('stroke-opacity', graphEdgeOpacity)
      line.setAttribute('stroke-linecap', 'round')
      if (showArrows.value) line.setAttribute('marker-end', 'url(#arrow-end)')
      line.style.transition = 'stroke 0.2s, stroke-width 0.2s, stroke-opacity 0.2s'
      if (bidir && showArrows.value) line.setAttribute('marker-start', 'url(#arrow-start)')
      edgeG.appendChild(line)
      edgeEls.push({ line, source: edge.source, target: edge.target, bidir })
    }

    const nodeEls: GraphNodeEl[] = []
    for (const n of graphNodes) {
      const g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
      g.style.cursor = 'pointer'
      g.setAttribute('data-graph-node', n.slug)

      const r = nodeRadius(n)
      const nodeColor = WIKI_NODE_COLOR_MAP[n.type] || 'var(--gy-text-tertiary)'
      const visibleNeighbors = adjacency.get(n.slug)?.size ?? 0
      const hiddenNeighbors = Math.max(0, n.linkCount - visibleNeighbors)
      const isEgoCenter = graph.meta?.mode === 'ego' && graph.meta.center === n.slug
      const showExpansionRing = graph.meta?.mode === 'ego' && hiddenNeighbors > 0 && !isEgoCenter

      const expansionRing = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      setAttrs(expansionRing, { r: String(r + 3), fill: 'none', stroke: nodeColor, 'stroke-width': '1.5', 'stroke-dasharray': '3 3', 'pointer-events': 'none' })
      expansionRing.style.opacity = showExpansionRing ? '0.55' : '0'
      expansionRing.style.transition = 'opacity 0.2s'
      expansionRing.classList.add('node-expansion-ring')
      g.appendChild(expansionRing)

      if (n.familiar) {
        const familiarRing = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
        setAttrs(familiarRing, { r: String(r + 7), fill: 'none', stroke: 'var(--gy-brand)', 'stroke-width': '2', 'pointer-events': 'none' })
        familiarRing.style.opacity = '0.9'
        familiarRing.classList.add('node-familiar-ring')
        g.appendChild(familiarRing)
      }

      const activeRing = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      setAttrs(activeRing, { r: String(r + 5), fill: 'none', stroke: nodeColor, 'stroke-width': '2' })
      activeRing.style.opacity = '0'
      activeRing.style.transition = 'opacity 0.2s'
      activeRing.classList.add('node-active-ring')
      g.appendChild(activeRing)

      const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      setAttrs(circle, { r: String(r), fill: nodeColor, stroke: 'var(--gy-surface)', 'stroke-width': '1.5' })
      circle.classList.add('node-circle')
      circle.style.transition = 'r 0.2s, stroke-width 0.2s, opacity 0.2s'
      g.appendChild(circle)

      const textBg = document.createElementNS('http://www.w3.org/2000/svg', 'rect')
      g.appendChild(textBg)

      const text = document.createElementNS('http://www.w3.org/2000/svg', 'text')
      text.setAttribute('text-anchor', 'middle')
      text.setAttribute('dy', String(r + 14))
      text.setAttribute('font-size', '11')
      text.setAttribute('fill', 'var(--gy-text-secondary)')
      text.setAttribute('pointer-events', 'none')
      text.style.textShadow =
        '0 1px 3px var(--gy-surface), 0 -1px 3px var(--gy-surface), 1px 0 3px var(--gy-surface), -1px 0 3px var(--gy-surface)'
      text.textContent = n.title.length > 14 ? `${n.title.substring(0, 14)}…` : n.title
      g.appendChild(text)

      let bloomBtn: SVGGElement | null = null
      if (!isEgoCenter && graph.meta?.mode === 'ego' && hiddenNeighbors > 0) {
        bloomBtn = document.createElementNS('http://www.w3.org/2000/svg', 'g')
        bloomBtn.classList.add('node-bloom-btn')
        bloomBtn.style.opacity = '0'
        bloomBtn.style.transition = 'opacity 0.15s'
        bloomBtn.style.pointerEvents = 'none'
        bloomBtn.style.cursor = 'pointer'
        const btnOffset = r + 6
        const btnX = Math.SQRT1_2 * btnOffset
        const btnY = -Math.SQRT1_2 * btnOffset

        const btnBg = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
        btnBg.setAttribute('cx', String(btnX))
        btnBg.setAttribute('cy', String(btnY))
        btnBg.setAttribute('r', '8')
        btnBg.setAttribute('fill', 'var(--gy-surface)')
        btnBg.setAttribute('stroke', 'var(--gy-brand)')
        btnBg.setAttribute('stroke-width', '1.5')
        bloomBtn.appendChild(btnBg)

        for (const [x1, y1, x2, y2] of [
          [btnX, btnY - 4, btnX, btnY + 4],
          [btnX - 4, btnY, btnX + 4, btnY],
        ] as const) {
          const cross = document.createElementNS('http://www.w3.org/2000/svg', 'line')
          cross.setAttribute('x1', String(x1))
          cross.setAttribute('y1', String(y1))
          cross.setAttribute('x2', String(x2))
          cross.setAttribute('y2', String(y2))
          cross.setAttribute('stroke', 'var(--gy-brand)')
          cross.setAttribute('stroke-width', '1.8')
          cross.setAttribute('stroke-linecap', 'round')
          bloomBtn.appendChild(cross)
        }

        bloomBtn.addEventListener('click', (e) => {
          e.stopPropagation()
          void opts.onBloomNeighbors(n.slug)
        })
        g.appendChild(bloomBtn)
      }

      g.addEventListener('mouseenter', () => {
        if (performance.now() < graphHoverSuppressedUntil) return
        if (graphHoverLeaveTimer) {
          timers.clearTimeout(graphHoverLeaveTimer)
          graphHoverLeaveTimer = null
        }
        if (bloomBtn) {
          bloomBtn.style.opacity = '1'
          bloomBtn.style.pointerEvents = 'auto'
        }
        if (!graphSelectedSlug.value) {
          if (graphHighlightSlug.value === n.slug) return
          graphHighlightSlug.value = n.slug
          applyHighlight(n.slug, adjacency, nodeEls, edgeEls)
        } else if (graphSelectedSlug.value !== n.slug) {
          if (graphHighlightSlug.value === n.slug) return
          graphHighlightSlug.value = n.slug
          applyHighlight(graphSelectedSlug.value, adjacency, nodeEls, edgeEls, n.slug)
        }
      })

      g.addEventListener('mouseleave', () => {
        if (graphHoverLeaveTimer) timers.clearTimeout(graphHoverLeaveTimer)
        if (bloomBtn) {
          bloomBtn.style.opacity = '0'
          bloomBtn.style.pointerEvents = 'none'
        }
        graphHoverLeaveTimer = timers.setTimeout(() => {
          graphHoverLeaveTimer = null
          if (!graphSelectedSlug.value) {
            graphHighlightSlug.value = null
            clearHighlight(nodeEls, edgeEls)
          } else {
            graphHighlightSlug.value = null
            applyHighlight(graphSelectedSlug.value, adjacency, nodeEls, edgeEls)
          }
        }, 60)
      })

      let pendingSingleClick: ReturnType<typeof setTimeout> | null = null
      g.addEventListener('click', (e) => {
        e.stopPropagation()
        if (e.shiftKey) {
          if (pendingSingleClick) {
            timers.clearTimeout(pendingSingleClick)
            pendingSingleClick = null
          }
          void opts.onBloomNeighbors(n.slug)
          return
        }
        if (pendingSingleClick) timers.clearTimeout(pendingSingleClick)
        pendingSingleClick = timers.setTimeout(() => {
          pendingSingleClick = null
          graphSelectedSlug.value = n.slug
          applyHighlight(n.slug, adjacency, nodeEls, edgeEls)
          if (graphPanZoomRef && graphRef.value) {
            const cw = graphRef.value.clientWidth
            const ch = graphRef.value.clientHeight
            graphPanZoomRef.flyTo(
              cw / 2 - n.x * graphPanZoomRef.getScale() - 240,
              ch / 2 - n.y * graphPanZoomRef.getScale(),
            )
          }
          void opts.onOpenDrawer(n.slug)
        }, 220)
      })

      g.addEventListener('dblclick', (e) => {
        e.stopPropagation()
        if (pendingSingleClick) {
          timers.clearTimeout(pendingSingleClick)
          pendingSingleClick = null
        }
        void opts.onLoadEgoGraph(n.slug)
      })

      setupDrag(g, n, nodeMap, edgeEls, nodeRadius)
      nodeG.appendChild(g)
      nodeEls.push({ g, circle, text, activeRing, node: n })
    }

    for (const { text, node } of nodeEls) node.labelWidth = text.getComputedTextLength() || node.labelWidth
    for (const { text } of nodeEls) text.style.display = 'none'

    const centerNode = nodeMap.get(graphCenter.value)
    const initialFocus = centerNode ?? layout.clusters[0] ?? graphNodes[0]
    const availableWidth = Math.max(160, width - (graphDrawerVisible.value ? 480 : Math.min(240, width * 0.25)))
    setupPanZoom(svg, rootG, previousViewport ?? initialGraphViewport(graphNodes, availableWidth, height, initialFocus))

    const retained = new Set(priorCoords.keys())
    let alpha = 1.0
    let frame = 0
    const tick = () => {
      alpha *= 0.985
      if (alpha < 0.02) {
        refreshGraphLabels()
        graphAnimFrame = 0
        return
      }
      stepGraphLayout(graphNodes, renderEdges, layout.placements, alpha, retained)
      for (const { g, node } of nodeEls) g.setAttribute('transform', `translate(${node.x},${node.y})`)
      for (const e of edgeEls) {
        const s = nodeMap.get(e.source)
        const t = nodeMap.get(e.target)
        if (s && t) setEdgePositions(e.line, s, t, nodeRadius)
      }
      if (++frame % 6 === 0) refreshGraphLabels()
      graphAnimFrame = requestAnimationFrame(tick)
    }

    for (const { g, node } of nodeEls) g.setAttribute('transform', `translate(${node.x},${node.y})`)
    for (const e of edgeEls) {
      const s = nodeMap.get(e.source)
      const t = nodeMap.get(e.target)
      if (s && t) setEdgePositions(e.line, s, t, nodeRadius)
    }

    graphNodeElsRef = nodeEls
    graphEdgeElsRef = edgeEls.map((e) => ({ line: e.line, source: e.source, target: e.target, bidir: e.bidir }))
    graphAdjacencyRef = adjacency

    if (graphNodes.length && typeof requestAnimationFrame === 'function') {
      graphAnimFrame = requestAnimationFrame(tick)
    }
    graphReady.value = true
    if (graphSelectedSlug.value && nodeMap.has(graphSelectedSlug.value)) {
      applyHighlight(graphSelectedSlug.value, adjacency, nodeEls, edgeEls)
    }
    refreshGraphLabels()
  }

  function setEdgePositions(line: SVGLineElement, s: GNode, t: GNode, nodeRadius: (n: GNode) => number) {
    const dx = t.x - s.x
    const dy = t.y - s.y
    const dist = Math.sqrt(dx * dx + dy * dy) || 1
    const ux = dx / dist
    const uy = dy / dist
    const margin = graphEdgeMarkersVisible ? 4 : 0
    const rS = nodeRadius(s) + margin
    const rT = nodeRadius(t) + margin
    line.setAttribute('x1', String(s.x + ux * rS))
    line.setAttribute('y1', String(s.y + uy * rS))
    line.setAttribute('x2', String(t.x - ux * rT))
    line.setAttribute('y2', String(t.y - uy * rT))
  }

  function setupDrag(
    g: SVGGElement,
    node: GNode,
    nodeMap: Map<string, GNode>,
    edgeEls: GraphEdgeEl[],
    nodeRadius: (n: GNode) => number,
  ) {
    let dragging = false
    let startX = 0
    let startY = 0
    let pointerStartX = 0
    let pointerStartY = 0
    let moved = false

    const getPoint = (e: MouseEvent | Touch) => {
      const svg = graphSvg
      if (!svg) return { x: e.clientX, y: e.clientY }
      const pt = svg.createSVGPoint()
      pt.x = e.clientX
      pt.y = e.clientY
      const rootG = svg.querySelector('.graph-root') as SVGGElement | null
      const ctm = rootG?.getCTM()?.inverse()
      if (ctm) {
        const svgP = pt.matrixTransform(ctm)
        return { x: svgP.x, y: svgP.y }
      }
      return { x: e.clientX, y: e.clientY }
    }

    const onMove = (e: MouseEvent) => {
      if (!dragging) return
      if (Math.hypot(e.clientX - pointerStartX, e.clientY - pointerStartY) > 4) moved = true
      const p = getPoint(e)
      node.x = p.x - startX
      node.y = p.y - startY
      node.vx = 0
      node.vy = 0
      g.setAttribute('transform', `translate(${node.x},${node.y})`)
      refreshGraphLabels()
      for (const edge of edgeEls) {
        if (edge.source === node.slug || edge.target === node.slug) {
          const sn = nodeMap.get(edge.source)
          const tn = nodeMap.get(edge.target)
          if (sn && tn) setEdgePositions(edge.line, sn, tn, nodeRadius)
        }
      }
    }

    const onEnd = () => {
      dragging = false
      g.querySelector('.node-circle')?.setAttribute('stroke', 'var(--gy-surface)')
      g.querySelector('.node-circle')?.setAttribute('stroke-width', '1.5')
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onEnd)
    }

    const onStart = (e: MouseEvent) => {
      if (e.button !== 0) return
      e.stopPropagation()
      dragging = true
      moved = false
      pointerStartX = e.clientX
      pointerStartY = e.clientY
      node.pinned = true
      const p = getPoint(e)
      startX = p.x - node.x
      startY = p.y - node.y
      g.querySelector('.node-circle')?.setAttribute(
        'stroke',
        WIKI_NODE_COLOR_MAP[node.type] || 'var(--gy-text-tertiary)',
      )
      g.querySelector('.node-circle')?.setAttribute('stroke-width', '2.5')
      window.addEventListener('mousemove', onMove)
      window.addEventListener('mouseup', onEnd)
    }

    g.addEventListener('mousedown', onStart)
    g.addEventListener(
      'click',
      (e) => {
        if (moved) {
          e.preventDefault()
          e.stopImmediatePropagation()
          moved = false
        }
      },
      true,
    )
  }

  function setupPanZoom(svg: SVGSVGElement, rootG: SVGGElement, viewport?: { scale: number; x: number; y: number }) {
    let scale = viewport?.scale ?? 1
    let translateX = viewport?.x ?? 0
    let translateY = viewport?.y ?? 0
    let panning = false
    let panStartX = 0
    let panStartY = 0
    let dragStartX = 0
    let dragStartY = 0
    let labelFrame = 0
    let labelScale: number | undefined

    function scheduleLabelsUpdate() {
      if (labelFrame) return
      labelFrame = requestAnimationFrame(() => {
        labelFrame = 0
        updateLabelsVisibility()
      })
    }

    function applyTransform() {
      rootG.setAttribute('transform', `translate(${translateX},${translateY}) scale(${scale})`)
      if (scale !== labelScale) scheduleLabelsUpdate()
    }

    function updateLabelsVisibility() {
      labelScale = scale
      rootG.classList.toggle('hide-edge-markers', scale < 0.8)
      const markersVisible = showArrows.value && scale >= 0.8
      if (markersVisible !== graphEdgeMarkersVisible) {
        graphEdgeMarkersVisible = markersVisible
        const bySlug = new Map(graphNodes.map((n) => [n.slug, n]))
        for (const e of graphEdgeElsRef) {
          const s = bySlug.get(e.source)
          const t = bySlug.get(e.target)
          if (s && t) setEdgePositions(e.line, s, t, (n) => graphNodeRadius(n.linkCount))
        }
      }
      for (const { text, top } of graphSectionEls) {
        const fontSize = String(13 / scale)
        const y = String(top - 12 / scale)
        if (text.getAttribute('font-size') !== fontSize) text.setAttribute('font-size', fontSize)
        if (text.getAttribute('y') !== y) text.setAttribute('y', y)
      }
      const positionsBySlug = new Map(graphNodes.map((n) => [n.slug, n]))
      for (const contour of graphContourEls) {
        const { path, cluster } = contour
        const members = cluster.slugs.flatMap((slug) => (positionsBySlug.has(slug) ? [positionsBySlug.get(slug)!] : []))
        if (
          contour.scale !== scale ||
          contour.positions?.length !== members.length * 2 ||
          members.some((n, i) => contour.positions![i * 2] !== n.x || contour.positions![i * 2 + 1] !== n.y)
        ) {
          path.setAttribute(
            'd',
            graphClusterOutline(
              members.map((n) => ({ ...n, radius: graphNodeRadius(n.linkCount) })),
              scale,
            ),
          )
          contour.scale = scale
          contour.positions = members.flatMap((n) => [n.x, n.y])
        }
        path.classList.toggle('is-dimmed', graphFocusedSlugs.size > 0 && !graphFocusedSlugs.has(cluster.representative))
      }
      const groupLabels = graphClusterEls.flatMap(({ group, cluster, anchorSlug, labelWidth, priority }) => {
        const anchor = positionsBySlug.get(anchorSlug)
        const outside = graphFocusedSlugs.size > 0 && !graphFocusedSlugs.has(cluster.representative)
        group.classList.toggle('is-outside-focus', outside)
        const shown = !!anchor && (outside ? anchorSlug === cluster.anchors[0] : scale < 1.2)
        if (!shown || !anchor) return []
        const labelX = anchor.x
        const labelY = anchor.y + graphNodeRadius(anchor.linkCount) + 18 / scale
        const x = labelX * scale
        const y = labelY * scale
        group.setAttribute('transform', `translate(${labelX},${labelY}) scale(${1 / scale})`)
        return [
          {
            slug: `anchor:${anchorSlug}`,
            priority,
            required: outside || anchor.slug === graphSelectedSlug.value || anchor.slug === graphHighlightSlug.value,
            left: x - labelWidth / 2 - 8,
            right: x + labelWidth / 2 + 8,
            top: y - 16,
            bottom: y + 5,
          },
        ]
      })
      const captionedSlugs = new Set(groupLabels.map((label) => label.slug.slice('anchor:'.length)))
      const candidates = graphNodeElsRef.flatMap(({ node }) => {
        const required = node.slug === graphSelectedSlug.value || node.slug === graphHighlightSlug.value
        if (captionedSlugs.has(node.slug)) return []
        const showDetail = scale >= 0.5 || (graphFocusedSlugs.has(node.slug) && scale >= 0.4)
        const landmark = graphImportantSlugs.has(node.slug) && (groupLabels.length === 0 || scale >= 0.4)
        if (!required && !showDetail && !landmark) return []
        const halfWidth = (node.labelWidth * 12) / 11 / 2 + 5
        const baseline = (node.y + graphNodeRadius(node.linkCount)) * scale + 16
        return [
          {
            slug: node.slug,
            priority: node.linkCount,
            required,
            left: node.x * scale - halfWidth,
            right: node.x * scale + halfWidth,
            top: baseline - 15,
            bottom: baseline + 4,
          },
        ]
      })
      const visible = visibleGraphLabels([...groupLabels, ...candidates])
      for (const { group, anchorSlug } of graphClusterEls) {
        const shown = visible.has(`anchor:${anchorSlug}`)
        const display = shown ? '' : 'none'
        const tabIndex = shown ? '0' : '-1'
        if (group.style.display !== display) group.style.display = display
        if (group.getAttribute('tabindex') !== tabIndex) group.setAttribute('tabindex', tabIndex)
      }
      for (const { text, node } of graphNodeElsRef) {
        const shown = visible.has(node.slug)
        const display = shown ? '' : 'none'
        if (text.style.display !== display) text.style.display = display
        if (!shown) continue
        const fontSize = String(12 / scale)
        const dy = String(graphNodeRadius(node.linkCount) + 16 / scale)
        if (text.getAttribute('font-size') !== fontSize) text.setAttribute('font-size', fontSize)
        if (text.getAttribute('dy') !== dy) text.setAttribute('dy', dy)
      }
    }
    refreshGraphLabels = scheduleLabelsUpdate

    let animId = 0
    graphPanZoomRef = {
      setScale: (s: number) => {
        scale = s
      },
      setTranslate: (x: number, y: number) => {
        translateX = x
        translateY = y
      },
      apply: applyTransform,
      getScale: () => scale,
      getTranslate: () => ({ x: translateX, y: translateY }),
      flyTo: (tx: number, ty: number, s?: number, duration = 400) => {
        cancelAnimationFrame(animId)
        const startX = translateX
        const startY = translateY
        const startScale = scale
        const targetScale = s || scale
        const startTime = performance.now()
        const animate = (time: number) => {
          let progress = (time - startTime) / duration
          if (progress > 1) progress = 1
          const ease = 1 - Math.pow(1 - progress, 3)
          translateX = startX + (tx - startX) * ease
          translateY = startY + (ty - startY) * ease
          scale = startScale + (targetScale - startScale) * ease
          applyTransform()
          if (progress < 1) animId = requestAnimationFrame(animate)
        }
        animId = requestAnimationFrame(animate)
      },
    }

    svg.addEventListener(
      'wheel',
      (e) => {
        e.preventDefault()
        cancelAnimationFrame(animId)
        const zoomFactor = e.deltaY > 0 ? 0.92 : 1.08
        const newScale = Math.max(0.005, Math.min(5, scale * zoomFactor))
        const rect = svg.getBoundingClientRect()
        const cx = e.clientX - rect.left
        const cy = e.clientY - rect.top
        translateX = cx - (cx - translateX) * (newScale / scale)
        translateY = cy - (cy - translateY) * (newScale / scale)
        scale = newScale
        applyTransform()
      },
      { passive: false },
    )

    svg.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return
      if (!(e.target as Element).closest('[data-graph-node], .graph-cluster-caption')) {
        cancelAnimationFrame(animId)
        panning = true
        panStartX = e.clientX - translateX
        panStartY = e.clientY - translateY
        dragStartX = e.clientX
        dragStartY = e.clientY
        svg.style.cursor = 'grabbing'
      }
    })

    const onMouseMove = (e: MouseEvent) => {
      if (!panning) return
      translateX = e.clientX - panStartX
      translateY = e.clientY - panStartY
      applyTransform()
    }

    const onMouseUp = (e: MouseEvent) => {
      if (panning) {
        panning = false
        svg.style.cursor = 'grab'
        const dx = e.clientX - dragStartX
        const dy = e.clientY - dragStartY
        if (Math.abs(dx) < 5 && Math.abs(dy) < 5) {
          if (!(e.target as Element).closest('[data-graph-node], .graph-cluster-caption')) {
            if (graphSelectedSlug.value || graphDrawerVisible.value) {
              graphSelectedSlug.value = null
              graphDrawerVisible.value = false
              clearHighlight(graphNodeElsRef, graphEdgeElsRef)
            } else {
              exitGraphFocus()
            }
          }
        }
      }
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || !graphFocusGroup.value || graphDrawerVisible.value) return
      if (e.target instanceof Element && e.target.closest('input, textarea, [contenteditable="true"]')) return
      exitGraphFocus()
    }

    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
    window.addEventListener('keydown', onKeyDown)
    disposeGraphPanZoom = () => {
      cancelAnimationFrame(animId)
      cancelAnimationFrame(labelFrame)
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
      window.removeEventListener('keydown', onKeyDown)
    }
    applyTransform()
  }

  // ─── Hover Highlight ───
  function applyHighlight(
    slug: string,
    adjacency: Map<string, Set<string>>,
    nodeEls: GraphNodeEl[],
    edgeEls: GraphEdgeEl[],
    hoverSlug?: string,
  ) {
    refreshGraphLabels()
    const neighbors = adjacency.get(slug) || new Set()
    const hoverNeighbors = hoverSlug ? adjacency.get(hoverSlug) || new Set() : new Set()
    const getRadius = (n: GNode) => graphNodeRadius(n.linkCount)

    for (const { g, circle, activeRing, node } of nodeEls) {
      const r = getRadius(node)
      if (node.slug === slug || (hoverSlug && node.slug === hoverSlug)) {
        circle.setAttribute('r', String(r + 3))
        circle.setAttribute('stroke-width', '2.5')
        g.style.opacity = '1'
      } else if (neighbors.has(node.slug) || (hoverSlug && hoverNeighbors.has(node.slug))) {
        circle.setAttribute('r', String(r))
        circle.setAttribute('stroke-width', '1.5')
        g.style.opacity = '1'
      } else {
        circle.setAttribute('r', String(r))
        circle.setAttribute('stroke-width', '1.5')
        g.style.opacity = '0.2'
      }
      activeRing.style.opacity = node.slug === graphSelectedSlug.value ? '1' : '0'
    }

    for (const e of edgeEls) {
      if (e.source === slug || e.target === slug || (hoverSlug && (e.source === hoverSlug || e.target === hoverSlug))) {
        e.line.setAttribute('stroke-opacity', '0.85')
        e.line.setAttribute('stroke-width', '1.5')
        const focusSlug = hoverSlug && (e.source === hoverSlug || e.target === hoverSlug) ? hoverSlug : slug
        const hlColor =
          WIKI_NODE_COLOR_MAP[nodeEls.find((n) => n.node.slug === focusSlug)?.node.type || ''] || 'var(--gy-brand)'
        e.line.setAttribute('stroke', hlColor)
        setEdgeMarkers(e, '-hl')
      } else {
        e.line.setAttribute('stroke', graphEdgeColor)
        e.line.setAttribute('stroke-opacity', '0.08')
        e.line.setAttribute('stroke-width', graphEdgeWidth)
        setEdgeMarkers(e)
      }
    }
  }

  function setEdgeMarkers(e: { line: SVGLineElement; bidir: boolean }, suffix = '') {
    if (!showArrows.value) {
      e.line.removeAttribute('marker-end')
      e.line.removeAttribute('marker-start')
      return
    }
    e.line.setAttribute('marker-end', `url(#arrow-end${suffix})`)
    if (e.bidir) e.line.setAttribute('marker-start', `url(#arrow-start${suffix})`)
    else e.line.removeAttribute('marker-start')
  }

  function clearHighlight(nodeEls: GraphNodeEl[], edgeEls: GraphEdgeEl[]) {
    if (graphSelectedSlug.value) {
      applyHighlight(graphSelectedSlug.value, graphAdjacencyRef, nodeEls, edgeEls)
      return
    }
    refreshGraphLabels()
    const getRadius = (n: GNode) => graphNodeRadius(n.linkCount)
    for (const { g, circle, activeRing, node } of nodeEls) {
      circle.setAttribute('r', String(getRadius(node)))
      circle.setAttribute('stroke-width', '1.5')
      g.style.opacity = graphFocusedSlugs.size && !graphFocusedSlugs.has(node.slug) ? '0.12' : '1'
      activeRing.style.opacity = '0'
    }
    for (const e of edgeEls) {
      e.line.setAttribute('stroke', graphEdgeColor)
      e.line.setAttribute('stroke-width', graphEdgeWidth)
      const outsideFocus =
        graphFocusedSlugs.size && (!graphFocusedSlugs.has(e.source) || !graphFocusedSlugs.has(e.target))
      e.line.setAttribute('stroke-opacity', outsideFocus ? '0.05' : graphEdgeOpacity)
      setEdgeMarkers(e)
    }
  }

  function disposeRenderer() {
    disposeGraphPanZoom()
    if (graphHoverLeaveTimer) {
      timers.clearTimeout(graphHoverLeaveTimer)
      graphHoverLeaveTimer = null
    }
    if (graphAnimFrame && typeof cancelAnimationFrame === 'function') {
      cancelAnimationFrame(graphAnimFrame)
      graphAnimFrame = 0
    }
  }

  return {
    focusGraphGroup,
    exitGraphFocus,
    fitGraphToView,
    toggleArrows,
    findNodeBySlug,
    focusAndSelectNode,
    renderGraph,
    disposeRenderer,
  }
}
