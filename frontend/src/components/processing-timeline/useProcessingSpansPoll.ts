import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  cancelKnowledgeParse,
  getKnowledgeDetails,
  getKnowledgeSpans,
  reparseKnowledge,
} from '@/api/knowledge-base/index'
import { knowledgeSpansPayloadHasTrace } from '@/utils/knowledgeTrace'
import { resolveTimelineHeaderStatus } from '@/utils/knowledgeProcessingStatus'
import { stalledMinutes, STALLED_POLL_INTERVAL_MS } from '@/utils/knowledgeProcessingStall'
import {
  extractErrorMessage,
  STAGES,
  type SpanNode,
  type SpansResponse,
  type TimelineProcessOverrides,
} from './types'

const POLL_INTERVAL_MS = 2000
const QUIESCE_GRACE_MS = 2 * 60 * 1000

interface SpansApiEnvelope {
  success?: boolean
  data?: SpansResponse
}

interface KnowledgeDetailsEnvelope {
  success?: boolean
  data?: {
    file_type?: string
    file_name?: string
    title?: string
    metadata?: {
      process_overrides?: TimelineProcessOverrides | null
    }
  }
}

export function parseTime(s?: string | null): number | null {
  if (!s) return null
  const ts = Date.parse(s)
  return Number.isNaN(ts) ? null : ts
}

export function isPolling(status?: string): boolean {
  return status === 'pending' || status === 'processing' || status === 'finalizing'
}

export function isHardTerminal(status?: string): boolean {
  return status === 'cancelled' || status === 'failed' || status === 'completed'
}

export function spanTreeActive(node?: SpanNode): boolean {
  if (!node) return false
  const s = node.status
  if (s === 'running' || s === 'pending') return true
  const kids = node.children || []
  for (let i = 0; i < kids.length; i++) {
    if (spanTreeActive(kids[i])) return true
  }
  return false
}

export function spanTreeLastActivity(node?: SpanNode): number {
  if (!node) return 0
  let max = 0
  const stamps: Array<string | null | undefined> = [
    node.updated_at,
    node.finished_at,
    node.started_at,
    node.created_at,
  ]
  for (const s of stamps) {
    const ts = parseTime(s || undefined)
    if (ts !== null && ts > max) max = ts
  }
  const kids = node.children || []
  for (let i = 0; i < kids.length; i++) {
    const ts = spanTreeLastActivity(kids[i])
    if (ts > max) max = ts
  }
  return max
}

function normalizeFileType(value: string): string {
  return String(value || '').trim().replace(/^\./, '').toLowerCase()
}

function getFileTypeFromName(name: string): string {
  const clean = String(name || '').split(/[?#]/)[0]
  const dot = clean.lastIndexOf('.')
  return dot >= 0 ? clean.slice(dot + 1) : ''
}

export function useProcessingSpansPoll(options: {
  getProps: () => {
    knowledgeId: string
    parseStatus?: string
    autoPoll: boolean
    compact: boolean
    gracePoll: boolean
    docTitle: string
  }
  t: (key: string, params?: Record<string, unknown>) => string
  onHasSpansChange: (has: boolean) => void
  onAfterSpansLoaded: () => void
}) {
  const { getProps, t, onHasSpansChange, onAfterSpansLoaded } = options

  const data = ref<SpansResponse | null>(null)
  const processOverrides = ref<TimelineProcessOverrides | null>(null)
  const currentKnowledgeFileType = ref('')
  const loading = ref(false)
  const refreshing = ref(false)
  const cancelling = ref(false)
  const selectedAttempt = ref<number | undefined>(undefined)
  const expandedRows = ref<Set<string>>(new Set(['__root__']))
  const selectedSpanId = ref<string | null>(null)
  const autoSelectPending = ref(true)
  const userToggledRows = ref<Set<string>>(new Set())
  const nowTick = ref(Date.now())
  const lastFetchedAt = ref<number>(0)
  const failedAttempts = ref<number>(0)
  const lastFetchOk = ref<boolean>(true)
  const attemptStatuses = reactive<Map<number, string>>(new Map())

  let pollTimer: ReturnType<typeof setInterval> | null = null
  let nowTimer: ReturnType<typeof setInterval> | null = null
  let unmounted = false
  let fetchInFlight = false
  let documentGeneration = 0
  let spansRequest = 0

  function invalidateSpansRequest(): void {
    spansRequest += 1
    fetchInFlight = false
    loading.value = false
    refreshing.value = false
  }

  function isCurrentDocument(id: string, generation: number): boolean {
    return !unmounted && id === getProps().knowledgeId && generation === documentGeneration
  }

  const traceActive = computed<boolean>(() => spanTreeActive(data.value?.trace))

  const isLive = computed<boolean>(() => {
    if (data.value) {
      if (isHardTerminal(data.value.parse_status)) return false
      return isPolling(data.value.parse_status) || traceActive.value
    }
    const hintStatus = getProps().parseStatus
    if (isHardTerminal(hintStatus)) return false
    return isPolling(hintStatus)
  })

  const lastTraceActivityAt = computed<number>(() =>
    spanTreeLastActivity(data.value?.trace),
  )

  const isWithinQuiesceGrace = computed<boolean>(() => {
    if (isLive.value) return false
    const last = lastTraceActivityAt.value
    if (!last) return false
    return nowTick.value - last < QUIESCE_GRACE_MS
  })

  function shouldPollNow(): boolean {
    const currentProps = getProps()
    if (!data.value) return isPolling(currentProps.parseStatus)
    if (currentProps.gracePoll) {
      return isLive.value || isWithinQuiesceGrace.value
    }
    return isPolling(data.value.parse_status)
  }

  const stalledMin = computed<number>(() => {
    const d = data.value
    if (!d || d.attempt !== (d.latest_attempt || d.attempt)) return 0
    return stalledMinutes(d, nowTick.value)
  })

  const stalledStageLabel = computed(() => {
    const stage = data.value?.current_stage
    return stage && (STAGES as readonly string[]).includes(stage)
      ? t(`knowledgeStages.stage.${stage}`)
      : ''
  })

  const canCancelParse = computed<boolean>(() => {
    const status = data.value?.parse_status ?? getProps().parseStatus
    return isPolling(status)
  })

  function ensureAttemptStatuses(): void {
    const id = getProps().knowledgeId
    const generation = documentGeneration
    const latest = data.value?.latest_attempt || 0
    if (latest <= 1) return
    for (let n = 1; n <= latest; n++) {
      if (attemptStatuses.has(n)) continue
      getKnowledgeSpans(id, n)
        .then((rawRes: unknown) => {
          const res = rawRes as SpansApiEnvelope
          if (!isCurrentDocument(id, generation)) return
          if (res?.success && res.data?.trace) {
            attemptStatuses.set(
              n,
              resolveTimelineHeaderStatus({
                parseStatus: res.data.parse_status,
                traceStatus: res.data.trace?.status,
                isLatestAttempt: n === latest,
              }) || 'running',
            )
          }
        })
        .catch(() => {})
    }
  }

  async function fetchSpans(opts: { manual?: boolean } = {}): Promise<void> {
    const currentProps = getProps()
    if (unmounted || !currentProps.knowledgeId) return
    if (fetchInFlight) return
    const id = currentProps.knowledgeId
    const generation = documentGeneration
    const request = ++spansRequest
    const isCurrent = () => isCurrentDocument(id, generation) && request === spansRequest
    fetchInFlight = true
    if (opts.manual) refreshing.value = true
    if (!data.value) loading.value = true
    let attemptOk = false
    try {
      const res = (await getKnowledgeSpans(id, selectedAttempt.value)) as SpansApiEnvelope
      if (!isCurrent()) return
      if (res?.success && res.data) {
        data.value = res.data
        attemptOk = true
        if (selectedAttempt.value === undefined) {
          selectedAttempt.value = data.value.attempt
        }
        const expanded = new Set(expandedRows.value)
        expanded.add('__root__')
        const autoExpand = (n: SpanNode): void => {
          const key = n.span_id || `stage:${n.name}`
          const kids = n.children || []
          if (kids.length > 0 && !userToggledRows.value.has(key)) {
            expanded.add(key)
          }
          for (const c of kids) autoExpand(c)
        }
        for (const stage of data.value.trace?.children || []) autoExpand(stage)
        expandedRows.value = expanded
        const latestAttempt = data.value.latest_attempt || data.value.attempt || 0
        const tabStatus =
          resolveTimelineHeaderStatus({
            parseStatus: data.value.parse_status,
            traceStatus: data.value.trace?.status,
            isLatestAttempt: data.value.attempt === latestAttempt,
          }) || 'running'
        attemptStatuses.set(data.value.attempt, tabStatus)
        ensureAttemptStatuses()
        onHasSpansChange(knowledgeSpansPayloadHasTrace(data.value))
        void nextTick(() => {
          if (isCurrent()) onAfterSpansLoaded()
        })
      } else {
        onHasSpansChange(false)
      }
    } catch (e) {
      if (!isCurrent()) return
      console.warn('[KnowledgeTimeline] fetchSpans failed', e)
      onHasSpansChange(false)
    } finally {
      if (isCurrent()) {
        lastFetchedAt.value = Date.now()
        lastFetchOk.value = attemptOk
        if (attemptOk) {
          failedAttempts.value = 0
        } else {
          failedAttempts.value += 1
        }
        loading.value = false
        refreshing.value = false
        fetchInFlight = false
      }
    }
  }

  async function fetchProcessOverrides(): Promise<void> {
    const currentProps = getProps()
    if (unmounted || currentProps.compact || !currentProps.knowledgeId) return
    const id = currentProps.knowledgeId
    const generation = documentGeneration
    try {
      const res = (await getKnowledgeDetails(id)) as KnowledgeDetailsEnvelope
      if (!isCurrentDocument(id, generation)) return
      if (res?.success && res.data) {
        processOverrides.value = res.data.metadata?.process_overrides ?? null
        currentKnowledgeFileType.value = normalizeFileType(
          res.data.file_type || getFileTypeFromName(res.data.file_name || res.data.title || ''),
        )
      }
    } catch {
      if (!isCurrentDocument(id, generation)) return
      processOverrides.value = null
      currentKnowledgeFileType.value = ''
    }
  }

  async function onRetry(): Promise<void> {
    const id = getProps().knowledgeId
    if (!id) return
    try {
      await reparseKnowledge(id)
      selectedAttempt.value = undefined
      attemptStatuses.clear()
      selectedSpanId.value = null
      autoSelectPending.value = true
      await fetchSpans()
    } catch {
      // ignore
    }
  }

  async function onManualRefresh(): Promise<void> {
    if (refreshing.value || loading.value) return
    await fetchSpans({ manual: true })
  }

  async function onCancelParseConfirm(): Promise<void> {
    if (cancelling.value) return
    const id = getProps().knowledgeId
    if (!id) return
    cancelling.value = true
    try {
      await cancelKnowledgeParse(id)
      MessagePlugin.success(t('knowledgeBase.cancelParseSubmitted'))
      await fetchSpans({ manual: true })
    } catch (e: unknown) {
      MessagePlugin.error(extractErrorMessage(e, t('knowledgeBase.cancelParseFailed')))
    } finally {
      cancelling.value = false
    }
  }

  function onAttemptChange(n: number): void {
    if (Number.isNaN(n)) return
    invalidateSpansRequest()
    selectedAttempt.value = n
    selectedSpanId.value = null
    autoSelectPending.value = true
    userToggledRows.value = new Set()
    expandedRows.value = new Set(['__root__'])
    void fetchSpans()
  }

  function formatParserRulesForCurrentFile(
    rules: Array<{ file_types?: string[]; engine?: string }>,
  ): string {
    const currentType = currentKnowledgeFileType.value
    if (currentType) {
      const matched = rules.find((rule) =>
        (rule.file_types || []).some((ft) => normalizeFileType(ft) === currentType),
      )
      if (matched?.engine) {
        return `${currentType}→${matched.engine}`
      }
    }
    return rules.map((r) => `${(r.file_types || []).join('/')}→${r.engine}`).join(', ')
  }

  const processConfigLines = computed<string[]>(() => {
    const o = processOverrides.value
    if (!o) return [t('knowledgeStages.processConfig.kbDefault')]
    const k = (s: string) => `knowledgeStages.processConfig.${s}`
    const onOff = (v: boolean) => (v ? t(k('on')) : t(k('off')))
    const lines: string[] = []

    const cc = o.chunking_config
    if (cc) {
      const parts: string[] = []
      if (cc.chunk_size != null) parts.push(t(k('chunkSize'), { n: cc.chunk_size }))
      if (cc.enable_parent_child != null) {
        parts.push(cc.enable_parent_child ? t(k('parentChildOn')) : t(k('parentChildOff')))
      }
      if (parts.length) lines.push(`${t(k('chunking'))}: ${parts.join(' · ')}`)
    }

    const rules = o.parser_engine_rules || cc?.parser_engine_rules
    if (rules?.length) {
      lines.push(`${t(k('parser'))}: ${formatParserRulesForCurrentFile(rules)}`)
    }

    const mm = o.vlm_config?.enabled ?? o.enable_multimodal
    if (mm != null) lines.push(`${t(k('multimodal'))}: ${onOff(mm)}`)

    if (o.asr_config?.enabled != null) lines.push(`${t(k('asr'))}: ${onOff(o.asr_config.enabled)}`)

    const qg = o.question_generation_config
    if (o.summary_enabled != null) {
      lines.push(`${t('uploadConfirm.documentSummary')}: ${onOff(o.summary_enabled)}`)
    }
    if (qg?.enabled != null) {
      lines.push(
        `${t(k('question'))}: ${qg.enabled ? t(k('questionOn'), { n: qg.question_count ?? 3 }) : t(k('off'))}`,
      )
    }

    const graph = o.graph_enabled ?? o.extract_config?.enabled
    if (graph != null) lines.push(`${t(k('graph'))}: ${onOff(graph)}`)

    return lines.length ? lines : [t('knowledgeStages.processConfig.kbDefault')]
  })

  watch(
    () => getProps().knowledgeId,
    () => {
      documentGeneration += 1
      invalidateSpansRequest()
      lastFetchedAt.value = 0
      lastFetchOk.value = true
      failedAttempts.value = 0
      selectedAttempt.value = undefined
      data.value = null
      processOverrides.value = null
      currentKnowledgeFileType.value = ''
      expandedRows.value = new Set(['__root__'])
      selectedSpanId.value = null
      autoSelectPending.value = true
      attemptStatuses.clear()
      userToggledRows.value = new Set()
      onHasSpansChange(false)
      void fetchSpans()
      void fetchProcessOverrides()
    },
    { flush: 'sync' },
  )

  onMounted(() => {
    void fetchSpans()
    void fetchProcessOverrides()
    if (getProps().autoPoll) {
      pollTimer = setInterval(() => {
        if (unmounted) return
        if (fetchInFlight) return
        if (!shouldPollNow()) return
        if (stalledMin.value > 0 && Date.now() - lastFetchedAt.value < STALLED_POLL_INTERVAL_MS) return
        void fetchSpans()
      }, POLL_INTERVAL_MS)
    }
    nowTimer = setInterval(() => {
      nowTick.value = Date.now()
    }, 1000)
  })

  onBeforeUnmount(() => {
    unmounted = true
    invalidateSpansRequest()
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
    if (nowTimer) {
      clearInterval(nowTimer)
      nowTimer = null
    }
  })

  return {
    data,
    processOverrides,
    currentKnowledgeFileType,
    loading,
    refreshing,
    cancelling,
    selectedAttempt,
    expandedRows,
    selectedSpanId,
    autoSelectPending,
    userToggledRows,
    nowTick,
    lastFetchedAt,
    failedAttempts,
    lastFetchOk,
    attemptStatuses,
    traceActive,
    isLive,
    shouldPollNow,
    stalledMin,
    stalledStageLabel,
    canCancelParse,
    processConfigLines,
    fetchSpans,
    onRetry,
    onManualRefresh,
    onCancelParseConfirm,
    onAttemptChange,
  }
}
