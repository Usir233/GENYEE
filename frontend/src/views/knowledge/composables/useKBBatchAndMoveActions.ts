import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { isBatchDownloadableKnowledge } from '../knowledgeDownloadFileName'
import type { KnowledgeCard, MoveTargetKB } from './useKBDocumentStatusPoll'

const MAX_BATCH_DOWNLOAD_FILES = 200
const MAX_BATCH_DOWNLOAD_BYTES = 512 * 1024 * 1024

export function useKBBatchAndMoveActions(deps: {
  kbId: ComputedRef<string>
  canMutateKnowledge: ComputedRef<boolean>
  cardList: Ref<KnowledgeCard[]>
  moreIndex: Ref<number>
  isBatchReparsing: () => boolean
  isCurrentKb: (targetKbId: string) => boolean
  isDisposed: () => boolean
  resetPage: () => void
  loadKnowledgeFiles: (kbIdValue: string) => Promise<void>
  loadFolderTree: (kbIdValue: string) => Promise<void>
  loadTags: (kbIdValue: string, reset?: boolean) => Promise<void>
  batchDownloadKnowledge: (kbId: string, ids: string[], signal?: AbortSignal) => Promise<Blob>
  batchDeleteKnowledge: (kbId: string, ids: string[]) => Promise<unknown>
  batchQueryKnowledge: (query: string, kbId?: string) => Promise<unknown>
  updateKnowledgeTagBatch: (data: { updates: Record<string, string[]> }) => Promise<unknown>
  waitForKnowledgeDeletion: (
    ids: string[],
    fetchRows: (ids: string[]) => Promise<{ data?: KnowledgeCard[] }>,
    opts: { isActive: () => boolean },
  ) => Promise<string>
  listMoveTargets: (kbId: string) => Promise<unknown>
  moveKnowledge: (payload: {
    knowledge_ids: string[]
    source_kb_id: string
    target_kb_id: string
    mode: 'reuse_vectors' | 'reparse'
  }) => Promise<unknown>
  getKnowledgeMoveProgress: (taskId: string) => Promise<unknown>
  message: {
    info: (msg: string) => void
    warning: (msg: string) => void
    success: (msg: string) => void
    error: (msg: string) => void
  }
  t: (key: string, params?: Record<string, unknown>) => string
}) {
  const selectedIds = ref<Set<string>>(new Set())
  const batchMode = ref(false)
  const batchDeleting = ref(false)
  const batchTagging = ref(false)
  const batchDownloading = ref(false)
  const batchTagDialogVisible = ref(false)

  let lastSelectedIndex = -1
  let batchDownloadController: AbortController | undefined
  let deleteGeneration = 0

  const selectedDocumentAssistantActions = computed(() => {
    const scope = { kb: deps.kbId.value, ids: JSON.stringify([...selectedIds.value]) }
    return [
      {
        key: 'summarize',
        label: deps.t('assistant.actions.summarizeSelected'),
        query: deps.t('assistant.prompts.summarizeDocuments', scope),
      },
      {
        key: 'compare',
        label: deps.t('assistant.actions.compareDocuments'),
        query: deps.t('assistant.prompts.compareDocuments', scope),
        disabled: selectedIds.value.size < 2,
      },
      ...(deps.canMutateKnowledge.value
        ? [
            {
              key: 'tags',
              label: deps.t('assistant.actions.suggestTags'),
              query: deps.t('assistant.prompts.tagDocuments', scope),
            },
            {
              key: 'folders',
              label: deps.t('assistant.actions.organizeDocuments'),
              query: deps.t('assistant.prompts.organizeDocuments', scope),
            },
          ]
        : []),
    ]
  })

  const batchTagPreSelectedIds = computed(() => {
    const ids = Array.from(selectedIds.value)
    if (ids.length === 0) return []
    const cards = ids
      .map((id) => deps.cardList.value.find((c) => c.id === id))
      .filter((c): c is KnowledgeCard => Boolean(c))
    if (cards.length === 0) return []
    const firstTagIds = new Set((cards[0].tags || []).map((tag) => tag.id))
    for (let i = 1; i < cards.length; i++) {
      const cur = new Set((cards[i].tags || []).map((tag) => tag.id))
      for (const tid of firstTagIds) {
        if (!cur.has(tid)) firstTagIds.delete(tid)
      }
    }
    return Array.from(firstTagIds)
  })

  const toggleSelectRow = (id: string, checked: boolean, shiftKey?: boolean) => {
    const items = deps.cardList.value || []
    const idx = items.findIndex((i) => i.id === id)
    if (shiftKey && lastSelectedIndex >= 0 && idx >= 0) {
      const [s, e] = idx < lastSelectedIndex ? [idx, lastSelectedIndex] : [lastSelectedIndex, idx]
      for (let i = s; i <= e; i++) {
        if (checked) selectedIds.value.add(items[i].id)
        else selectedIds.value.delete(items[i].id)
      }
    } else if (checked) {
      selectedIds.value.add(id)
    } else {
      selectedIds.value.delete(id)
    }
    lastSelectedIndex = idx
  }

  const onCardGridCheckboxChange = (id: string, checked: boolean, ctx?: { e?: Event }) => {
    const me = ctx?.e as MouseEvent | undefined
    toggleSelectRow(id, checked, Boolean(me?.shiftKey))
  }

  const toggleSelectAll = (checked: boolean) => {
    if (checked) {
      for (const item of deps.cardList.value || []) selectedIds.value.add(item.id)
    } else {
      for (const item of deps.cardList.value || []) selectedIds.value.delete(item.id)
    }
  }

  const clearSelection = () => {
    selectedIds.value.clear()
    lastSelectedIndex = -1
  }

  const clampSelectionIndices = () => {
    const items = deps.cardList.value || []
    const n = items.length
    if (lastSelectedIndex >= n) {
      lastSelectedIndex = n > 0 ? n - 1 : -1
    }
    if (deps.moreIndex.value >= n) {
      deps.moreIndex.value = -1
    }
    if (selectedIds.value.size === 0) return
    const visible = new Set(items.map((i) => i.id))
    for (const id of selectedIds.value) {
      if (!visible.has(id)) selectedIds.value.delete(id)
    }
  }

  const toggleBatchMode = () => {
    batchMode.value = !batchMode.value
    if (!batchMode.value) clearSelection()
  }

  const handleBatchCancel = () => {
    clearSelection()
    batchMode.value = false
  }

  const handleEnterBatchFromCard = (item: KnowledgeCard | null | undefined) => {
    if (item) item.isMore = false
    deps.moreIndex.value = -1
    clearSelection()
    batchMode.value = true
  }

  const abortBatchDownload = () => {
    batchDownloadController?.abort()
  }

  const bumpDeleteGeneration = () => {
    deleteGeneration++
    batchDeleting.value = false
  }

  const handleBatchDownload = async () => {
    if (
      batchDownloading.value ||
      batchDeleting.value ||
      deps.isBatchReparsing() ||
      batchTagging.value ||
      selectedIds.value.size === 0
    ) {
      return
    }
    const selected = Array.from(selectedIds.value)
    const itemsById = new Map((deps.cardList.value || []).map((item) => [item.id, item]))
    const ids = selected.filter((id) => isBatchDownloadableKnowledge(itemsById.get(id)))
    const skipped = selected.length - ids.length
    if (ids.length === 0) {
      deps.message.warning(deps.t('knowledgeBase.batchDownloadNoFiles'))
      return
    }
    if (ids.length > MAX_BATCH_DOWNLOAD_FILES) {
      deps.message.warning(deps.t('knowledgeBase.batchDownloadHint'))
      return
    }
    let knownBytes = 0
    for (const id of ids) {
      const size = Number(itemsById.get(id)?.file_size)
      if (Number.isFinite(size) && size > 0) knownBytes += size
    }
    if (knownBytes > MAX_BATCH_DOWNLOAD_BYTES) {
      deps.message.warning(deps.t('knowledgeBase.batchDownloadTooLarge'))
      return
    }
    if (skipped > 0) {
      deps.message.warning(deps.t('knowledgeBase.batchDownloadSkipped', { count: skipped }))
    }
    const controller = new AbortController()
    batchDownloadController = controller
    batchDownloading.value = true
    try {
      const blob = await deps.batchDownloadKnowledge(deps.kbId.value, ids, controller.signal)
      if (controller.signal.aborted) return
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `knowledge-files-${Date.now()}.zip`
      document.body.appendChild(link)
      link.click()
      link.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 60000)
      deps.message.success(deps.t('knowledgeBase.batchDownloadStarted'))
    } catch (error: unknown) {
      if (!controller.signal.aborted) {
        deps.message.error(
          (error as { message?: string } | null)?.message ||
            deps.t('knowledgeBase.batchDownloadFailed'),
        )
      }
    } finally {
      if (batchDownloadController === controller) {
        batchDownloading.value = false
        batchDownloadController = undefined
      }
    }
  }

  const deleteKnowledgeDocuments = async (
    ids: string[],
    submit: () => Promise<unknown>,
    batch: boolean,
  ) => {
    if (
      batchDeleting.value ||
      deps.isBatchReparsing() ||
      batchDownloading.value ||
      ids.length === 0
    ) {
      return
    }
    const targetKbId = deps.kbId.value
    const generation = ++deleteGeneration
    const isActive = () => generation === deleteGeneration && deps.isCurrentKb(targetKbId)
    batchDeleting.value = true
    let submitted = false
    try {
      const res = (await submit()) as { success?: boolean; message?: string } | null
      if (!isActive()) return
      if (!res?.success) {
        deps.message.error(res?.message || deps.t('knowledgeBase.batchDeleteFailed'))
        return
      }
      submitted = true
      deps.message.info(deps.t('knowledgeBase.deleteSubmitted'))
      if (batch) {
        clearSelection()
        batchMode.value = false
      }
      const result = await deps.waitForKnowledgeDeletion(
        ids,
        async (queryIds) => {
          const query = new URLSearchParams()
          queryIds.forEach((id) => query.append('ids', id))
          return (await deps.batchQueryKnowledge(query.toString(), targetKbId)) as {
            data?: KnowledgeCard[]
          }
        },
        { isActive },
      )
      if (!isActive() || result === 'cancelled') return
      if (result === 'completed') {
        deps.message.success(
          batch
            ? deps.t('knowledgeBase.batchDeleteSuccess', { count: ids.length })
            : deps.t('knowledgeBase.deleteSuccess'),
        )
      } else if (result === 'failed') {
        deps.message.error(deps.t('knowledgeBase.deleteTaskFailed'))
      } else {
        deps.message.info(deps.t('knowledgeBase.deletePending'))
      }
    } catch (e: unknown) {
      if (!isActive()) return
      if (submitted) {
        deps.message.warning(deps.t('knowledgeBase.deleteStatusUnavailable'))
      } else {
        deps.message.error(
          (e as { message?: string } | null)?.message ||
            deps.t('knowledgeBase.batchDeleteFailed'),
        )
      }
    } finally {
      if (isActive()) {
        batchDeleting.value = false
        if (submitted) {
          deps.resetPage()
          await deps.loadKnowledgeFiles(targetKbId)
          if (isActive()) {
            void deps.loadTags(targetKbId, true)
            void deps.loadFolderTree(targetKbId)
          }
        }
      }
    }
  }

  const confirmBatchDelete = () => {
    const targetKbId = deps.kbId.value
    const ids = Array.from(selectedIds.value)
    return deleteKnowledgeDocuments(ids, () => deps.batchDeleteKnowledge(targetKbId, ids), true)
  }

  const handleBatchTag = () => {
    if (
      batchDeleting.value ||
      deps.isBatchReparsing() ||
      batchTagging.value ||
      batchDownloading.value ||
      selectedIds.value.size === 0
    ) {
      return
    }
    batchTagDialogVisible.value = true
  }

  const onBatchTagConfirm = async (tagIds: string[]) => {
    if (batchTagging.value || selectedIds.value.size === 0) return
    const ids = Array.from(selectedIds.value)
    const updateMap: Record<string, string[]> = {}
    for (const id of ids) {
      updateMap[id] = tagIds
    }
    batchTagging.value = true
    try {
      await deps.updateKnowledgeTagBatch({ updates: updateMap })
      deps.message.success(deps.t('knowledgeBase.batchTagSuccess', { count: ids.length }))
      batchTagDialogVisible.value = false
      clearSelection()
      batchMode.value = false
      deps.resetPage()
      void deps.loadKnowledgeFiles(deps.kbId.value)
      void deps.loadTags(deps.kbId.value, true)
    } catch (e: unknown) {
      deps.message.error(
        (e as { message?: string } | null)?.message || deps.t('knowledgeBase.batchTagFailed'),
      )
    } finally {
      batchTagging.value = false
    }
  }

  // Cross-KB Move state
  const moveMenuMode = ref<'normal' | 'targets' | 'confirm'>('normal')
  const moveKnowledgeId = ref('')
  const moveTargetKbs = ref<MoveTargetKB[]>([])
  const moveTargetsLoading = ref(false)
  const moveSelectedTargetId = ref('')
  const moveSelectedTargetName = ref('')
  const moveMode = ref<'reuse_vectors' | 'reparse'>('reuse_vectors')
  const moveSubmitting = ref(false)
  let movePollTimer: ReturnType<typeof setInterval> | null = null

  const stopMovePoll = () => {
    if (movePollTimer) {
      clearInterval(movePollTimer)
      movePollTimer = null
    }
  }

  const startMovePoll = (taskId: string) => {
    const pollKbId = deps.kbId.value
    stopMovePoll()
    movePollTimer = setInterval(async () => {
      if (deps.isDisposed() || deps.kbId.value !== pollKbId) {
        stopMovePoll()
        return
      }
      try {
        const res = (await deps.getKnowledgeMoveProgress(taskId)) as {
          data?: { status?: string; processed?: number; failed?: number }
        } | null
        if (deps.isDisposed() || deps.kbId.value !== pollKbId) {
          stopMovePoll()
          return
        }
        const data = res?.data
        if (!data) return
        if (data.status === 'completed') {
          stopMovePoll()
          moveSubmitting.value = false
          const failed = data.failed || 0
          if (failed > 0) {
            deps.message.warning(
              deps.t('knowledgeBase.moveCompletedWithErrors', {
                success: (data.processed || 0) - failed,
                failed,
              }),
            )
          } else {
            deps.message.success(deps.t('knowledgeBase.moveCompleted'))
          }
          deps.resetPage()
          void deps.loadKnowledgeFiles(deps.kbId.value)
          void deps.loadFolderTree(deps.kbId.value)
        } else if (data.status === 'failed') {
          stopMovePoll()
          moveSubmitting.value = false
          deps.message.error(deps.t('knowledgeBase.moveFailed'))
        }
      } catch {
        // Ignore transient poll errors
      }
    }, 2000)
  }

  const handleMoveKnowledge = async (item: KnowledgeCard) => {
    moveKnowledgeId.value = item.id
    moveMenuMode.value = 'targets'
    moveTargetsLoading.value = true
    moveTargetKbs.value = []
    try {
      const res = (await deps.listMoveTargets(deps.kbId.value)) as { data?: MoveTargetKB[] } | null
      moveTargetKbs.value = res?.data || []
    } catch {
      moveTargetKbs.value = []
    } finally {
      moveTargetsLoading.value = false
    }
  }

  const handleMoveSelectTarget = (kb: MoveTargetKB) => {
    moveSelectedTargetId.value = kb.id
    moveSelectedTargetName.value = kb.name
    moveMode.value = 'reuse_vectors'
    moveMenuMode.value = 'confirm'
  }

  const handleMoveBack = () => {
    moveMenuMode.value = moveMenuMode.value === 'confirm' ? 'targets' : 'normal'
  }

  const handleMoveConfirm = async () => {
    if (!moveSelectedTargetId.value || moveSubmitting.value) return
    moveSubmitting.value = true
    try {
      const res = (await deps.moveKnowledge({
        knowledge_ids: [moveKnowledgeId.value],
        source_kb_id: deps.kbId.value,
        target_kb_id: moveSelectedTargetId.value,
        mode: moveMode.value,
      })) as { data?: { task_id?: string } } | null
      const taskId = res?.data?.task_id
      deps.message.info(deps.t('knowledgeBase.moveStarted'))
      moveMenuMode.value = 'normal'
      deps.cardList.value.forEach((c) => {
        c.isMore = false
      })

      if (taskId) {
        startMovePoll(taskId)
      } else {
        moveSubmitting.value = false
        deps.resetPage()
        void deps.loadKnowledgeFiles(deps.kbId.value)
        void deps.loadFolderTree(deps.kbId.value)
      }
    } catch (e: unknown) {
      deps.message.error(
        (e as { message?: string } | null)?.message || deps.t('knowledgeBase.moveFailed'),
      )
      moveSubmitting.value = false
    }
  }

  return {
    selectedIds,
    batchMode,
    batchDeleting,
    batchTagging,
    batchDownloading,
    batchTagDialogVisible,
    batchTagPreSelectedIds,
    selectedDocumentAssistantActions,
    toggleSelectRow,
    onCardGridCheckboxChange,
    toggleSelectAll,
    clearSelection,
    clampSelectionIndices,
    toggleBatchMode,
    handleBatchCancel,
    handleEnterBatchFromCard,
    abortBatchDownload,
    bumpDeleteGeneration,
    handleBatchDownload,
    deleteKnowledgeDocuments,
    confirmBatchDelete,
    handleBatchTag,
    onBatchTagConfirm,
    moveMenuMode,
    moveTargetKbs,
    moveTargetsLoading,
    moveSelectedTargetName,
    moveMode,
    moveSubmitting,
    stopMovePoll,
    handleMoveKnowledge,
    handleMoveSelectTarget,
    handleMoveBack,
    handleMoveConfirm,
  }
}
