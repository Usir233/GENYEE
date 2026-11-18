import { ref, watch, type ComputedRef, type Ref } from 'vue'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'

export interface KBDocumentTag {
  id: string
  name: string
  color?: string
}

export interface KnowledgeCard {
  id: string
  knowledge_base_id?: string
  parse_status: string
  summary_status?: string
  description?: string
  file_name?: string
  original_file_name?: string
  display_name?: string
  title?: string
  type?: string
  updated_at?: string
  created_at?: string
  file_type?: string
  file_size?: number
  folder_path?: string
  channel?: string
  source?: string
  isMore?: boolean
  metadata?: {
    process_overrides?: KnowledgeProcessOverrides | null
    [key: string]: unknown
  }
  error_message?: string
  tags?: KBDocumentTag[]
  last_activity_at?: string
  stalled_minutes?: number
  stall_state?: string
}

export interface KBDetailInfo {
  id: string
  name: string
  description?: string
  type?: 'document' | 'faq' | string
  creator_id?: string
  my_permission?: string
  summary_model_id?: string
  embedding_model_id?: string
  storage_backend_id?: string
  storage_provider_config?: { provider?: string }
  chunking_config?: {
    parser_engine_rules?: Array<{ file_types: string[]; engine: string }>
    [key: string]: unknown
  }
  indexing_strategy?: {
    vector_enabled?: boolean
    keyword_enabled?: boolean
    wiki_enabled?: boolean
    graph_enabled?: boolean
  }
  [key: string]: unknown
}

export type DocumentRowAction =
  | 'download'
  | 'edit'
  | 'reparse'
  | 'cancel-parse'
  | 'move'
  | 'move-folder'
  | 'delete'
  | 'view-trace'
  | 'batch-manage'

export interface MoveTargetKB {
  id: string
  name: string
  description?: string
  knowledge_count?: number
}

const STATUS_POLL_INTERVAL_MS = 1500
const STATUS_QUERY_BATCH_SIZE = 100
const BATCH_REPARSE_ACK_TIMEOUT_MS = 12000

interface BatchQueryApiResponse {
  success?: boolean
  data?: KnowledgeCard[]
}

interface BatchReparseApiResponse {
  success?: boolean
  message?: string
}

export function useKBDocumentStatusPoll(deps: {
  kbId: ComputedRef<string>
  isFAQ: ComputedRef<boolean>
  cardList: Ref<KnowledgeCard[]>
  total: Ref<number>
  docListLoading: Ref<boolean>
  selectedIds: Ref<Set<string>>
  batchMode: Ref<boolean>
  batchDeleting: Ref<boolean>
  batchDownloading: Ref<boolean>
  traceAvailableById: Record<string, boolean>
  pendingKnowledgeId: Ref<string | null>
  tryAutoOpenDocument: () => Promise<void>
  clearSelection: () => void
  scheduleWikiStatusProbes: () => void
  fetchWikiStatusOnce: () => Promise<void>
  batchQueryKnowledge: (query: string, kbId?: string) => Promise<unknown>
  batchReparseKnowledge: (kbId: string, ids: string[]) => Promise<unknown>
  stalledMinutes: (row: { parse_status?: string; last_activity_at?: string }) => number
  stalledPollIntervalMs: number
  isKnowledgeParseInFlight: (status?: string) => boolean
  knowledgeNeedsStatusPolling: (item: KnowledgeCard) => boolean
  shouldRefreshWikiStatusAfterKnowledgePoll: (
    before: KnowledgeCard,
    after: KnowledgeCard,
  ) => boolean
  message: {
    info: (msg: string) => void
    warning: (msg: string) => void
    success: (msg: string) => void
    error: (msg: string) => void
  }
  t: (key: string, params?: Record<string, unknown>) => string
}) {
  const batchReparsing = ref(false)
  const pendingReparseAck = new Map<string, ReturnType<typeof setTimeout>>()
  const reparseVersionById = new Map<string, number>()

  let timeout: ReturnType<typeof setTimeout> | null = null
  let statusPollGeneration = 0
  let statusPollRequest: object | null = null
  let statusPollingDisposed = false

  const needsStatusPolling = (item: KnowledgeCard) => deps.knowledgeNeedsStatusPolling(item)

  const pollDelayFor = (items: KnowledgeCard[]) =>
    items.length > 0 && items.every((item) => (item.stalled_minutes ?? 0) > 0)
      ? deps.stalledPollIntervalMs
      : STATUS_POLL_INTERVAL_MS

  const clearReparseAck = (id: string) => {
    const timer = pendingReparseAck.get(id)
    if (timer !== undefined) clearTimeout(timer)
    pendingReparseAck.delete(id)
  }

  const resetStatusPolling = () => {
    statusPollGeneration++
    if (timeout !== null) clearTimeout(timeout)
    timeout = null
    statusPollRequest = null
    for (const id of pendingReparseAck.keys()) clearReparseAck(id)
    reparseVersionById.clear()
    batchReparsing.value = false
  }

  const updateStatus = (analyzeList: KnowledgeCard[], delay = STATUS_POLL_INTERVAL_MS) => {
    if (timeout !== null) {
      clearTimeout(timeout)
      timeout = null
    }
    if (statusPollingDisposed || statusPollRequest || !analyzeList.length) return

    const generation = statusPollGeneration
    const targetKbId = deps.kbId.value
    timeout = setTimeout(async () => {
      timeout = null
      if (statusPollingDisposed || generation !== statusPollGeneration) return
      const requestedIds = deps.cardList.value.filter(needsStatusPolling).map((item) => item.id)
      if (!requestedIds.length) return
      const request = {}
      statusPollRequest = request
      const isCurrentRequest = () =>
        !statusPollingDisposed &&
        generation === statusPollGeneration &&
        statusPollRequest === request

      try {
        let shouldRefreshWikiStatus = false
        for (let offset = 0; offset < requestedIds.length; offset += STATUS_QUERY_BATCH_SIZE) {
          if (!isCurrentRequest()) return
          const batchIds = requestedIds.slice(offset, offset + STATUS_QUERY_BATCH_SIZE)
          const batchIdSet = new Set(batchIds)
          const query = batchIds.map((id) => `ids=${encodeURIComponent(id)}`).join('&')
          const reparseVersions = new Map(
            batchIds.map((id) => [id, reparseVersionById.get(id)]),
          )
          const hasSameReparse = (id: string) =>
            reparseVersionById.get(id) === reparseVersions.get(id)

          let result: BatchQueryApiResponse | null | undefined
          try {
            result = (await deps.batchQueryKnowledge(query, targetKbId)) as
              | BatchQueryApiResponse
              | null
              | undefined
          } catch {
            if (!isCurrentRequest()) return
            continue
          }
          if (!isCurrentRequest()) return
          if (!result?.success || !Array.isArray(result.data)) continue

          result.data.forEach((item) => {
            const index = deps.cardList.value.findIndex((card) => card.id === item.id)
            if (!batchIdSet.has(item.id) || index === -1 || !hasSameReparse(item.id)) return

            let parseStatus = item.parse_status
            if (pendingReparseAck.has(item.id)) {
              if (deps.isKnowledgeParseInFlight(item.parse_status)) {
                clearReparseAck(item.id)
              } else {
                parseStatus = 'pending'
              }
            }

            const card = deps.cardList.value[index]
            card.last_activity_at = item.last_activity_at
            card.stall_state = item.stall_state
            card.stalled_minutes = deps.stalledMinutes({
              parse_status: parseStatus,
              last_activity_at: item.last_activity_at,
            })

            if (
              card.parse_status !== parseStatus ||
              card.summary_status !== item.summary_status ||
              card.description !== item.description
            ) {
              shouldRefreshWikiStatus ||= deps.shouldRefreshWikiStatusAfterKnowledgePoll(card, {
                ...item,
                parse_status: parseStatus,
              })
              card.parse_status = parseStatus
              card.summary_status = item.summary_status
              card.description = item.description
              delete deps.traceAvailableById[item.id]
            }
          })

          const returnedIds = new Set(result.data.map((item) => item.id))
          const goneIds = new Set(
            batchIds.filter((id) => !returnedIds.has(id) && hasSameReparse(id)),
          )
          if (goneIds.size > 0) {
            const before = deps.cardList.value.length
            deps.cardList.value = deps.cardList.value.filter((card) => !goneIds.has(card.id))
            deps.total.value = Math.max(
              0,
              deps.total.value - (before - deps.cardList.value.length),
            )
            goneIds.forEach(clearReparseAck)
          }
        }
        if (shouldRefreshWikiStatus) void deps.fetchWikiStatusOnce()
      } finally {
        if (isCurrentRequest()) {
          statusPollRequest = null
          const stillPending = deps.cardList.value.filter(needsStatusPolling)
          if (stillPending.length > 0) {
            updateStatus(stillPending, pollDelayFor(stillPending))
          }
        }
      }
    }, delay)
  }

  const applyOptimisticBatchReparse = (ids: string[]) => {
    const idSet = new Set(ids)
    for (const card of deps.cardList.value) {
      if (!idSet.has(card.id)) continue
      clearReparseAck(card.id)
      reparseVersionById.set(card.id, (reparseVersionById.get(card.id) ?? 0) + 1)
      const timer = setTimeout(() => {
        if (pendingReparseAck.get(card.id) !== timer) return
        pendingReparseAck.delete(card.id)
        updateStatus(deps.cardList.value.filter(needsStatusPolling), 0)
      }, BATCH_REPARSE_ACK_TIMEOUT_MS)
      pendingReparseAck.set(card.id, timer)
      card.parse_status = 'pending'
      card.summary_status = undefined
      card.description = ''
      delete deps.traceAvailableById[card.id]
      deps.traceAvailableById[card.id] = true
    }
  }

  const confirmBatchReparse = async () => {
    if (
      batchReparsing.value ||
      deps.batchDeleting.value ||
      deps.batchDownloading.value ||
      deps.selectedIds.value.size === 0
    ) {
      return
    }
    const allIds = Array.from(deps.selectedIds.value)
    const ids = allIds.filter((id) => {
      const item = deps.cardList.value.find((c) => c.id === id)
      return !item || !deps.isKnowledgeParseInFlight(item.parse_status)
    })
    const skipped = allIds.length - ids.length
    if (ids.length === 0) {
      deps.message.info(deps.t('knowledgeBase.rebuildInProgress'))
      return
    }
    if (skipped > 0) {
      deps.message.warning(
        deps.t('knowledgeBase.batchReparseSkippedInFlight', { count: skipped }),
      )
    }
    const submissionGeneration = statusPollGeneration
    const targetKbId = deps.kbId.value
    batchReparsing.value = true
    try {
      const res = (await deps.batchReparseKnowledge(targetKbId, ids)) as
        | BatchReparseApiResponse
        | null
        | undefined
      if (statusPollingDisposed || submissionGeneration !== statusPollGeneration) return
      if (res?.success) {
        deps.message.success(deps.t('knowledgeBase.batchReparseSuccess', { count: ids.length }))
        applyOptimisticBatchReparse(ids)
        deps.clearSelection()
        deps.batchMode.value = false
        deps.scheduleWikiStatusProbes()
      } else {
        deps.message.error(res?.message || deps.t('knowledgeBase.batchReparseFailed'))
      }
    } catch (e: unknown) {
      if (!statusPollingDisposed && submissionGeneration === statusPollGeneration) {
        deps.message.error(
          (e as { message?: string } | null)?.message || deps.t('knowledgeBase.batchReparseFailed'),
        )
      }
    } finally {
      if (submissionGeneration === statusPollGeneration) batchReparsing.value = false
    }
  }

  watch(
    () => deps.cardList.value,
    (newValue) => {
      if (deps.isFAQ.value) return
      for (const card of newValue) {
        if (pendingReparseAck.has(card.id) && !deps.isKnowledgeParseInFlight(card.parse_status)) {
          card.parse_status = 'pending'
          card.summary_status = undefined
          card.description = ''
        }
      }
      deps.docListLoading.value = false

      if (deps.pendingKnowledgeId.value) {
        void deps.tryAutoOpenDocument()
      }

      const analyzeList = newValue.filter(needsStatusPolling)
      if (timeout !== null) {
        clearTimeout(timeout)
        timeout = null
      }
      if (analyzeList.length) {
        updateStatus(analyzeList, pollDelayFor(analyzeList))
      }
    },
    { deep: true },
  )

  const disposeStatusPolling = () => {
    statusPollingDisposed = true
    resetStatusPolling()
  }

  return {
    batchReparsing,
    pendingReparseAck,
    resetStatusPolling,
    confirmBatchReparse,
    disposeStatusPolling,
  }
}
