import { computed, type ComputedRef, type Ref } from 'vue'
import {
  groupPostprocessGraphSpans,
  postprocessElapsedMs,
  summarizePostprocessTasks,
} from '@/utils/knowledgeTrace'
import { resolveTimelineHeaderStatus, shouldShowProcessingError } from '@/utils/knowledgeProcessingStatus'
import { axisGridStepPct, buildAxisTicks, computeTraceAxis } from '@/utils/traceAxis'
import { isHardTerminal, parseTime } from './useProcessingSpansPoll'
import {
  STAGES,
  type AttemptTab,
  type FlatRow,
  type HeadStat,
  type IdentityField,
  type KvEntry,
  type SpanNode,
  type SpansResponse,
  type StageRowSummary,
} from './types'

const KV_INLINE_ARRAY_LIMIT = 8
const KV_INLINE_OBJECT_KEY_LIMIT = 8
const KV_INLINE_JSON_BYTES_LIMIT = 600

export function formatDuration(ms?: number): string {
  if (ms === undefined || ms === null || Number.isNaN(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(2)}s`
  const mins = Math.floor(ms / 60000)
  const rem = ((ms % 60000) / 1000).toFixed(1)
  return `${mins}m${rem}s`
}

export function formatTime(s?: string | null): string {
  if (!s) return '—'
  const ts = Date.parse(s)
  if (Number.isNaN(ts)) return s
  const d = new Date(ts)
  const ms = String(d.getMilliseconds()).padStart(3, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  const now = new Date()
  const yyyy = d.getFullYear()
  const mo = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const datePart = yyyy === now.getFullYear() ? `${mo}-${dd}` : `${yyyy}-${mo}-${dd}`
  return `${datePart} ${hh}:${mm}:${ss}.${ms}`
}

export function isObjectWithKeys(v: unknown): v is Record<string, unknown> {
  return Boolean(v) && typeof v === 'object' && !Array.isArray(v) && Object.keys(v as object).length > 0
}

export function hasContent(v: unknown): boolean {
  if (v === null || v === undefined || v === '') return false
  if (Array.isArray(v)) return v.length > 0
  if (typeof v === 'object') return Object.keys(v as object).length > 0
  return true
}

export function prettyJSON(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

function humanizeKey(k: string): string {
  return k
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b([a-z])/g, (_, c: string) => c.toUpperCase())
}

function shouldInlineExpand(value: unknown): boolean {
  if (Array.isArray(value)) {
    if (value.length > KV_INLINE_ARRAY_LIMIT) return false
    try {
      return JSON.stringify(value).length <= KV_INLINE_JSON_BYTES_LIMIT
    } catch {
      return false
    }
  }
  if (value && typeof value === 'object') {
    const keys = Object.keys(value as object)
    if (keys.length > KV_INLINE_OBJECT_KEY_LIMIT) return false
    try {
      return JSON.stringify(value).length <= KV_INLINE_JSON_BYTES_LIMIT
    } catch {
      return false
    }
  }
  return false
}

function toKvEntry(key: string, value: unknown): KvEntry {
  const label = humanizeKey(key)
  if (value === null || value === undefined) {
    return { key, label, kind: 'scalar', display: '—', raw: value, defaultExpanded: false }
  }
  if (typeof value === 'boolean') {
    return { key, label, kind: 'bool', display: value ? 'true' : 'false', raw: value, defaultExpanded: false }
  }
  if (typeof value === 'number') {
    return { key, label, kind: 'scalar', display: value.toLocaleString(), raw: value, defaultExpanded: false }
  }
  if (typeof value === 'string') {
    return { key, label, kind: 'scalar', display: value, raw: value, defaultExpanded: false }
  }
  if (Array.isArray(value)) {
    return {
      key,
      label,
      kind: 'array',
      display: `Array · ${value.length}`,
      raw: value,
      defaultExpanded: shouldInlineExpand(value),
    }
  }
  if (typeof value === 'object') {
    const n = Object.keys(value as object).length
    return {
      key,
      label,
      kind: 'object',
      display: `Object · ${n} keys`,
      raw: value,
      defaultExpanded: shouldInlineExpand(value),
    }
  }
  return { key, label, kind: 'scalar', display: String(value), raw: value, defaultExpanded: false }
}

export function buildKvEntries(obj: unknown): KvEntry[] {
  if (!isObjectWithKeys(obj)) return []
  return Object.entries(obj).map(([key, value]) => toKvEntry(key, value))
}

export function attemptGlyph(status: string): { ch: string; cls: string } {
  switch (status) {
    case 'done':
      return { ch: '✓', cls: 'kp-glyph-done' }
    case 'failed':
      return { ch: '✗', cls: 'kp-glyph-failed' }
    case 'running':
    case 'pending':
    case 'processing':
      return { ch: '●', cls: 'kp-glyph-running' }
    default:
      return { ch: '–', cls: 'kp-glyph-unknown' }
  }
}

export function isPlaceholder(node: SpanNode): boolean {
  return !node.span_id && !node.started_at
}

function rowKey(node: SpanNode, fallback: string): string {
  return node.span_id || fallback
}

function nodeStart(node: SpanNode): number | null {
  return parseTime(node.started_at || undefined)
}

function nodeEnd(node: SpanNode): number | null {
  const e = parseTime(node.finished_at || undefined)
  if (e !== null) return e
  const s = nodeStart(node)
  if (s !== null && typeof node.duration_ms === 'number' && node.duration_ms > 0) {
    return s + node.duration_ms
  }
  return null
}

function collectStarts(node: SpanNode | undefined, out: number[]): void {
  if (!node) return
  const s = nodeStart(node)
  if (s !== null) out.push(s)
  for (const c of node.children || []) collectStarts(c, out)
}

function collectEnds(node: SpanNode | undefined, out: number[]): void {
  if (!node) return
  const e = nodeEnd(node)
  if (e !== null) out.push(e)
  for (const c of node.children || []) collectEnds(c, out)
}

function descendantMaxEnd(node: SpanNode): number | null {
  const ends: number[] = []
  for (const c of node.children || []) {
    collectEnds(c, ends)
  }
  if (ends.length === 0) return null
  return Math.max(...ends)
}

export function useSpanWaterfallMetrics(options: {
  data: Ref<SpansResponse | null>
  selectedAttempt: Ref<number | undefined>
  expandedRows: Ref<Set<string>>
  selectedSpanId: Ref<string | null>
  nowTick: Ref<number>
  lastFetchedAt: Ref<number>
  lastFetchOk: Ref<boolean>
  attemptStatuses: Map<number, string>
  traceActive: ComputedRef<boolean>
  isLive: ComputedRef<boolean>
  stalledMin: ComputedRef<number>
  shouldPollNow: () => boolean
  t: (key: string, params?: Record<string, unknown>) => string
}) {
  const {
    data,
    selectedAttempt,
    expandedRows,
    selectedSpanId,
    nowTick,
    lastFetchedAt,
    lastFetchOk,
    attemptStatuses,
    traceActive,
    isLive,
    stalledMin,
    shouldPollNow,
    t,
  } = options

  const stages = computed<SpanNode[]>(() => {
    const trace = data.value?.trace
    const children = trace?.children || []
    const byName = new Map<string, SpanNode>()
    for (const c of children) {
      if (c && c.kind === 'stage' && c.name) {
        byName.set(c.name, (c.name === 'postprocess' ? groupPostprocessGraphSpans(c) : c) as SpanNode)
      }
    }
    return STAGES.map((n) => byName.get(n) || ({ name: n, kind: 'stage', status: 'pending' } as SpanNode))
  })

  const currentStageLabel = computed(() => {
    const running = stages.value.find((s) => s.status === 'running')
    const failed = stages.value.find((s) => s.status === 'failed')
    const target =
      failed || running || stages.value.find((s) => s.status === 'pending') || stages.value[stages.value.length - 1]
    return target ? t(`knowledgeStages.stage.${target.name}`) : ''
  })

  const currentStageIndex = computed(() => {
    const idx = stages.value.findIndex((s) => s.status === 'running' || s.status === 'failed')
    if (idx >= 0) return idx + 1
    const traversed = stages.value.filter(
      (s) => s.status === 'done' || s.status === 'skipped',
    ).length
    return Math.min(traversed + 1, stages.value.length)
  })

  function formatRelativeTime(ts: number): string {
    if (!ts) return '—'
    const sec = Math.max(0, Math.floor((nowTick.value - ts) / 1000))
    if (sec < 1) return t('knowledgeStages.justNow')
    if (sec < 60) return t('knowledgeStages.secondsAgo', { n: sec })
    const min = Math.floor(sec / 60)
    return t('knowledgeStages.minutesAgo', { n: min })
  }

  function postprocessDisplayNow(): number {
    return shouldPollNow() ? nowTick.value : (lastFetchedAt.value || nowTick.value)
  }

  function formatSpanDuration(node: SpanNode, includeChildren = false): string {
    if (node.status === 'skipped' || node.status === 'pending') return '—'
    if (includeChildren) {
      const elapsed = postprocessElapsedMs(node, postprocessDisplayNow())
      if (elapsed !== null) return formatDuration(elapsed)
    }
    return formatDuration(node.duration_ms)
  }

  const traceRoot = computed<SpanNode | null>(() => {
    const trace = data.value?.trace
    if (!trace) return null
    return {
      ...trace,
      name: trace.name || 'knowledge_processing',
      kind: trace.kind || 'root',
      children: stages.value.map((stage) => stage),
    }
  })

  const t0 = computed<number | null>(() => {
    const root = traceRoot.value
    if (!root) return null
    const direct = nodeStart(root)
    if (direct !== null) return direct
    const all: number[] = []
    collectStarts(root, all)
    if (all.length === 0) return null
    return Math.min(...all)
  })

  const tEnd = computed<number | null>(() => {
    const root = traceRoot.value
    if (!root) return null
    const direct = parseTime(root.finished_at || undefined)
    const all: number[] = []
    collectEnds(root, all)
    let candidate: number | null = direct
    if (all.length > 0) {
      const max = Math.max(...all)
      candidate = candidate === null ? max : Math.max(candidate, max)
    }
    if (isLive.value) {
      const now = nowTick.value
      candidate = candidate === null ? now : Math.max(candidate, now)
    }
    return candidate
  })

  const totalMs = computed<number>(() => {
    if (t0.value === null || tEnd.value === null) return 0
    const observed = Math.max(0, tEnd.value - t0.value)
    const traceDur = data.value?.trace?.duration_ms
    if (typeof traceDur === 'number' && traceDur > 0) {
      return Math.max(traceDur, observed)
    }
    return observed
  })

  const showRuler = computed(() => totalMs.value >= 50)
  const timeAxis = computed(() => computeTraceAxis(totalMs.value))
  const axisMaxMs = computed(() => timeAxis.value.maxMs)
  const gridStepPct = computed(() => axisGridStepPct(timeAxis.value))
  const rulerTicks = computed(() => (showRuler.value ? buildAxisTicks(timeAxis.value) : []))

  const nowMarkerPct = computed<number | null>(() => {
    if (!isLive.value || !t0.value || !axisMaxMs.value) return null
    const pct = ((nowTick.value - t0.value) / axisMaxMs.value) * 100
    return Math.max(0, Math.min(100, pct))
  })

  const flatRows = computed<FlatRow[]>(() => {
    const root = traceRoot.value
    if (!root) return []
    const rows: FlatRow[] = []

    const rootKey = rowKey(root, '__root__')
    rows.push({
      key: rootKey,
      depth: 0,
      node: root,
      hasChildren: (root.children || []).length > 0,
      isRoot: true,
      isStage: false,
    })

    for (const stage of root.children || []) {
      const stageKey = rowKey(stage, `stage:${stage.name}`)
      const stageChildren = stage.children || []
      rows.push({
        key: stageKey,
        depth: 1,
        node: stage,
        hasChildren: stageChildren.length > 0,
        isRoot: false,
        isStage: true,
        parentKey: rootKey,
      })
      if (!expandedRows.value.has(stageKey)) continue

      const walk = (n: SpanNode, depth: number, idxPath: string, parentKey: string): void => {
        const key = rowKey(n, `${idxPath}:${n.name}`)
        const kids = n.children || []
        rows.push({
          key,
          depth,
          node: n,
          hasChildren: kids.length > 0,
          isRoot: false,
          isStage: false,
          parentKey,
        })
        if (!expandedRows.value.has(key)) return
        kids.forEach((c, i) => walk(c, depth + 1, `${idxPath}/${i}`, key))
      }
      stageChildren.forEach((c, i) => walk(c, 2, `${stageKey}/${i}`, stageKey))
    }

    return rows
  })

  const selectedRow = computed<FlatRow | null>(() => {
    const id = selectedSpanId.value
    if (!id) return null
    return flatRows.value.find((r) => r.key === id) || null
  })

  const detailOpen = computed(() => selectedSpanId.value !== null && selectedRow.value !== null)
  const traceTerminal = computed<boolean>(
    () => isHardTerminal(data.value?.parse_status) && !traceActive.value,
  )

  function isNotRun(node: SpanNode): boolean {
    return traceTerminal.value && node.status === 'pending' && isPlaceholder(node)
  }

  function barStyle(node: SpanNode): Record<string, string> {
    const total = axisMaxMs.value
    if (!total || t0.value === null) return { display: 'none' }
    const start = nodeStart(node)
    if (start === null) return { display: 'none' }
    const liveBar = isLive.value || node.status === 'running' || node.status === 'pending'
    const end = nodeEnd(node) ?? (liveBar ? nowTick.value : start)
    const leftPct = ((start - t0.value) / total) * 100
    const widthPct = Math.max(0.4, ((end - start) / total) * 100)
    return {
      left: `${Math.max(0, Math.min(100, leftPct))}%`,
      width: `${Math.min(100 - Math.max(0, leftPct), widthPct)}%`,
    }
  }

  function wrapStyle(node: SpanNode): Record<string, string> | null {
    const total = axisMaxMs.value
    if (!total || t0.value === null) return null
    const start = nodeStart(node)
    if (start === null) return null
    const selfEnd = nodeEnd(node) ?? start
    const childEnd = descendantMaxEnd(node)
    if (childEnd === null || childEnd - selfEnd < 50) return null
    const leftPct = ((start - t0.value) / total) * 100
    const widthPct = Math.max(0.4, ((childEnd - start) / total) * 100)
    return {
      left: `${Math.max(0, Math.min(100, leftPct))}%`,
      width: `${Math.min(100 - Math.max(0, leftPct), widthPct)}%`,
    }
  }

  function wrapDurationMs(node: SpanNode): number {
    const start = nodeStart(node)
    const childEnd = descendantMaxEnd(node)
    if (start === null || childEnd === null) return 0
    return Math.max(0, childEnd - start)
  }

  function barOffsetPct(node: SpanNode): number | null {
    const total = axisMaxMs.value
    if (!total || t0.value === null) return null
    const start = nodeStart(node)
    if (start === null) return null
    return Math.max(0, Math.min(100, ((start - t0.value) / total) * 100))
  }

  function barOffsetMs(node: SpanNode): number {
    const start = nodeStart(node)
    if (start === null || t0.value === null) return 0
    return Math.max(0, start - t0.value)
  }

  function liveElapsedMs(node: SpanNode): number {
    const s = nodeStart(node)
    if (s === null) return 0
    return Math.max(0, nowTick.value - s)
  }

  function localizedStatus(status: string): string {
    const key = `knowledgeStages.status.${status}`
    const localized = t(key)
    return localized === key ? status : localized
  }

  function localizedErrorTitle(code?: string): string {
    if (!code) return ''
    const key = `knowledgeStages.errorCode.${code}`
    const localized = t(key)
    return localized === key ? code : localized
  }

  function localizedErrorSuggestion(code?: string): string {
    if (!code) return ''
    const key = `knowledgeStages.errorCode.${code}_SUGGESTION`
    const localized = t(key)
    if (localized !== key) return localized
    const fallback = t('knowledgeStages.errorCode.UNKNOWN_SUGGESTION')
    return fallback === 'knowledgeStages.errorCode.UNKNOWN_SUGGESTION' ? '' : fallback
  }

  function rowLabel(row: FlatRow): string {
    if (row.isRoot) return t('knowledgeStages.root')
    if (row.isStage) return t(`knowledgeStages.stage.${row.node.name}`)
    if (row.node.name === 'postprocess.graph') return t('knowledgeStages.processConfig.graph')
    const graphChunk = /^postprocess\.graph\.chunk\[(\d+)\]$/.exec(row.node.name)
    if (graphChunk) return `${t('knowledgeStages.processConfig.graph')} #${Number(graphChunk[1]) + 1}`
    return row.node.name
  }

  function rowKindLabel(row: FlatRow): string {
    if (row.isRoot || row.isStage) return ''
    return row.node.kind === 'generation' ? 'generation' : ''
  }

  const attemptTabs = computed<AttemptTab[]>(() => {
    const latest = data.value?.latest_attempt || 0
    if (latest <= 1) return []
    const active = selectedAttempt.value ?? data.value?.attempt ?? latest
    const out: AttemptTab[] = []
    for (let n = 1; n <= latest; n++) {
      out.push({
        n,
        status: attemptStatuses.get(n) || 'unknown',
        active: n === active,
      })
    }
    return out
  })

  const viewingLatestAttempt = computed<boolean>(() => {
    const latest = data.value?.latest_attempt || 0
    if (latest <= 1) return true
    const active = selectedAttempt.value ?? data.value?.attempt ?? latest
    return active === latest
  })

  const headerStatus = computed(() =>
    resolveTimelineHeaderStatus({
      parseStatus: data.value?.parse_status,
      traceStatus: data.value?.trace?.status,
      isLatestAttempt: viewingLatestAttempt.value,
    }),
  )

  const headerStatusText = computed(() => {
    const s = headerStatus.value
    return s ? localizedStatus(s) : ''
  })

  const headerStatusTheme = computed<'success' | 'danger' | 'warning' | 'default'>(() => {
    switch (headerStatus.value) {
      case 'done':
      case 'completed':
        return 'success'
      case 'failed':
        return 'danger'
      case 'running':
      case 'processing':
      case 'pending':
      case 'finalizing':
        return 'warning'
      default:
        return 'default'
    }
  })

  const showLastError = computed(() =>
    shouldShowProcessingError(
      Boolean(data.value?.last_error),
      data.value?.parse_status,
      data.value?.trace?.status,
    ),
  )

  const failedStage = computed<SpanNode | null>(
    () => stages.value.find((s) => s.status === 'failed') || null,
  )

  const errorHeadline = computed(() => {
    const err = data.value?.last_error
    const localized = localizedErrorTitle(err?.error_code)
    if (localized && localized !== err?.error_code) return localized
    const name =
      failedStage.value?.name || (STAGES as readonly string[]).find((n) => n === err?.name)
    if (name) {
      return t('knowledgeStages.stageFailed', { stage: t(`knowledgeStages.stage.${name}`) })
    }
    return t('knowledgeStages.status.failed')
  })

  const showToolbarRetry = computed(
    () => data.value?.parse_status === 'failed' && !showLastError.value,
  )

  const stagesStatDisplay = computed(() => {
    const total = stages.value.length
    const completedCount = stages.value.filter(
      (s) => s.status === 'done' || s.status === 'skipped',
    ).length
    const inProgress = stages.value.some(
      (s) => s.status === 'running' || s.status === 'failed' || s.status === 'pending',
    )
    if (inProgress) {
      return {
        label: t('knowledgeStages.head.stagesProgress'),
        value: `${currentStageIndex.value}/${total}`,
      }
    }
    return {
      label: t('knowledgeStages.head.stagesDone'),
      value: `${completedCount}/${total}`,
    }
  })

  const postprocessTaskStats = computed(() =>
    summarizePostprocessTasks(data.value?.trace),
  )

  const headStats = computed<HeadStat[]>(() => {
    if (!data.value) return []
    const out: HeadStat[] = []
    if (totalMs.value > 0) {
      out.push({
        key: 'duration',
        label: t('knowledgeStages.stat.duration'),
        value: formatDuration(totalMs.value),
        mono: true,
      })
    }
    const st = stagesStatDisplay.value
    out.push({ key: 'stages', label: st.label, value: st.value, mono: true })

    const postprocess = postprocessTaskStats.value
    if (postprocess.total > 0) {
      out.push({
        key: 'tasks',
        label: t('knowledgeStages.stat.tasks'),
        value: t('knowledgeStages.stat.tasksValue', {
          running: postprocess.running,
          failed: postprocess.failed,
          completed: postprocess.completed,
        }),
        mono: false,
        note: postprocess.running > 0 || postprocess.failed > 0,
      })
    }
    if (attemptTabs.value.length === 0 && data.value.current_attempt) {
      out.push({
        key: 'attempt',
        label: t('knowledgeStages.stat.attempt'),
        value: `#${data.value.current_attempt}`,
        mono: true,
      })
    }
    const lastActivity = parseTime(data.value.last_activity_at)
    if (lastActivity !== null && isLive.value) {
      out.push({
        key: 'progress',
        label: t('knowledgeStages.head.lastProgress'),
        value: formatRelativeTime(lastActivity),
        mono: false,
        note: stalledMin.value > 0,
      })
    }
    if (lastFetchedAt.value && isLive.value) {
      let updated = formatRelativeTime(lastFetchedAt.value)
      if (!lastFetchOk.value) {
        updated += ` (${t('knowledgeStages.fetchFailedShort')})`
      }
      out.push({
        key: 'updated',
        label: t('knowledgeStages.head.updated'),
        value: updated,
        mono: false,
        note: !lastFetchOk.value,
      })
    }
    return out
  })

  const stageBreakdown = computed<StageRowSummary[]>(() => {
    const total = totalMs.value || 1
    return stages.value.map((s) => ({
      name: s.name,
      label: t(`knowledgeStages.stage.${s.name}`),
      status: s.status,
      duration_ms: s.duration_ms,
      pct:
        typeof s.duration_ms === 'number' && s.duration_ms > 0
          ? Math.min(100, (s.duration_ms / total) * 100)
          : 0,
    }))
  })

  function identityFields(row: FlatRow): IdentityField[] {
    const out: IdentityField[] = []
    const node = row.node
    out.push({ key: 'name', label: t('knowledgeStages.detail.name'), value: rowLabel(row), mono: false, copyable: false })
    out.push({ key: 'kind', label: t('knowledgeStages.detail.kind'), value: rowKindLabel(row), mono: true, copyable: false })
    out.push({ key: 'status', label: t('knowledgeStages.detail.status'), value: localizedStatus(row.node.status), mono: false, copyable: false })
    if (row.isStage) {
      const idx = stages.value.findIndex((s) => s.name === row.node.name)
      if (idx >= 0) {
        out.push({
          key: 'stageIndex',
          label: t('knowledgeStages.detail.stageOrder'),
          value: `${idx + 1} / ${stages.value.length}`,
          mono: true,
          copyable: false,
        })
      }
    }
    if (row.hasChildren) {
      out.push({
        key: 'children',
        label: t('knowledgeStages.detail.childCount'),
        value: String((row.node.children || []).length),
        mono: true,
        copyable: false,
      })
    }
    if (node.span_id) out.push({ key: 'span_id', label: 'span_id', value: node.span_id, mono: true, copyable: true })
    if (node.parent_span_id) {
      out.push({ key: 'parent_span_id', label: 'parent_span_id', value: node.parent_span_id, mono: true, copyable: true })
    }
    if (data.value?.knowledge_id) {
      out.push({ key: 'knowledge_id', label: 'knowledge_id', value: data.value.knowledge_id, mono: true, copyable: true })
    }
    if (data.value?.current_attempt) {
      out.push({
        key: 'attempt',
        label: t('knowledgeStages.head.attempt'),
        value: `#${data.value.current_attempt}`,
        mono: true,
        copyable: false,
      })
    }
    return out
  }

  return {
    stages,
    currentStageLabel,
    currentStageIndex,
    stagesStatDisplay,
    postprocessDisplayNow,
    formatSpanDuration,
    totalMs,
    showRuler,
    gridStepPct,
    rulerTicks,
    nowMarkerPct,
    flatRows,
    selectedRow,
    detailOpen,
    isNotRun,
    barStyle,
    wrapStyle,
    wrapDurationMs,
    barOffsetPct,
    barOffsetMs,
    liveElapsedMs,
    localizedStatus,
    localizedErrorTitle,
    localizedErrorSuggestion,
    rowLabel,
    rowKindLabel,
    attemptTabs,
    headerStatus,
    headerStatusText,
    headerStatusTheme,
    showLastError,
    errorHeadline,
    showToolbarRetry,
    headStats,
    stageBreakdown,
    identityFields,
  }
}
