import { nextTick, reactive, ref, type ComputedRef, type Ref } from 'vue'
import type { AssistantDataChange } from '@/utils/assistantDataChange'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import type { OpenUploadConfirmOptions, UploadConfirmResult } from '@/stores/uploadConfirm'
import type { EnqueueUploadsInput } from '@/stores/uploadTasks'
import { buildUploadFileName, ROOT_FOLDER_PATH } from '../folderTree'
import { resolveKnowledgeDownloadFileName } from '../knowledgeDownloadFileName'
import { isKnowledgeParseInFlight } from '../wikiStatusRefresh'
import type { DocumentRowAction, KBDetailInfo, KnowledgeCard } from './useKBDocumentStatusPoll'

const LEAVING_OPERATIONS = ['knowledge.delete', 'knowledge.batch_delete', 'knowledge.move']

export function useKBDocumentOperations(deps: {
  kbId: ComputedRef<string>
  kbInfo: Ref<KBDetailInfo | null>
  isFAQ: ComputedRef<boolean>
  canEdit: ComputedRef<boolean>
  missingStorageEngine: ComputedRef<boolean>
  cardList: Ref<KnowledgeCard[]>
  moreIndex: Ref<number>
  details: { id: string; title?: string; parse_status?: string; [key: string]: unknown }
  isCardDetails: Ref<boolean>
  selectedTagIds: Ref<string[]>
  selectedFolderPath: Ref<string>
  folderOptions: ComputedRef<Array<{ path: string; name: string; depth: number }>>
  acceptFileTypes: ComputedRef<string>
  supportedFileTypes: ComputedRef<Set<string>>
  docContentRef: Ref<{ openTimeline?: () => void } | null>
  isCurrentKb: (targetKbId: string) => boolean
  resetPage: () => void
  loadKnowledgeFiles: (kbIdValue: string) => Promise<void>
  loadFolderTree: (kbIdValue: string) => Promise<void>
  loadTags: (kbIdValue: string, reset?: boolean) => Promise<void>
  loadKnowledgeBaseInfo: (targetKbId: string, force?: boolean) => Promise<void>
  scheduleWikiStatusProbes: () => void
  openCardDetails: (item: KnowledgeCard) => void
  getCardDetails: (item: KnowledgeCard) => void
  closeDoc: () => void
  handleNavigateToKbList: () => void
  shouldSuppressDocClick: () => boolean
  deleteKnowledgeDocuments: (
    ids: string[],
    request: () => Promise<unknown>,
    clearBatchSelection: boolean,
  ) => Promise<void>
  handleMoveKnowledge: (item: KnowledgeCard) => Promise<void>
  handleEnterBatchFromCard: (item: KnowledgeCard) => void
  batchQueryKnowledge: (query: string, kbId?: string) => Promise<unknown>
  waitForKnowledgeDeletion: (
    ids: string[],
    fetchRows: (ids: string[]) => Promise<{ data?: KnowledgeCard[] }>,
    opts: { isActive: () => boolean },
  ) => Promise<string>
  createKnowledgeFromURL: (
    kbId: string,
    payload: { url: string; tag_ids?: string[]; process_config?: KnowledgeProcessOverrides },
  ) => Promise<unknown>
  reparseKnowledge: (
    id: string,
    payload?: { process_config?: KnowledgeProcessOverrides },
  ) => Promise<unknown>
  getKnowledgeDetails: (id: string) => Promise<unknown>
  getKnowledgeSpans: (id: string) => Promise<unknown>
  hasKnowledgeTrace: (data: unknown) => boolean
  cancelKnowledgeParse: (id: string) => Promise<unknown>
  delKnowledgeDetails: (id: string) => Promise<unknown>
  downKnowledgeDetails: (id: string) => Promise<Blob>
  ensureKnowledgeBases: () => Promise<unknown>
  getRawKnowledgeBases: () => Array<{ id: string | number; name: string; type?: string }>
  getSharedKnowledgeBases: () => Array<{
    knowledge_base?: { id: string | number; name: string; type?: string } | null
  }>
  openUploadConfirm: (options: OpenUploadConfirmOptions) => Promise<UploadConfirmResult>
  enqueueUploadBatch: (input: EnqueueUploadsInput) => void
  openManualEditor: (options: {
    mode: 'create' | 'edit'
    kbId: string
    knowledgeId?: string
    status?: 'draft' | 'publish'
    onSuccess: (payload: { kbId: string; knowledgeId: string; status: 'draft' | 'publish' }) => void
  }) => void
  dispatchKnowledgeUploaded: (kbId: string) => void
  message: {
    info: (msg: string) => void
    warning: (msg: string) => void
    success: (msg: string) => void
    error: (msg: string) => void
  }
  t: (key: string, params?: Record<string, unknown>) => string
}) {
  const {
    kbId,
    kbInfo,
    isFAQ,
    canEdit,
    missingStorageEngine,
    cardList,
    moreIndex,
    details,
    isCardDetails,
    selectedTagIds,
    selectedFolderPath,
    folderOptions,
    acceptFileTypes,
    supportedFileTypes,
    docContentRef,
    message,
    t,
  } = deps

  const knowledgeList = ref<Array<{ id: string; name: string; type?: string }>>([])
  const traceAvailableById = reactive<Record<string, boolean>>({})
  const traceProbeInflight = new Set<string>()

  const clearTraceAvailabilityCache = () => {
    for (const key of Object.keys(traceAvailableById)) {
      delete traceAvailableById[key]
    }
    traceProbeInflight.clear()
  }

  const probeTraceAvailable = async (item: KnowledgeCard) => {
    const id = item.id
    if (!id || traceProbeInflight.has(id)) return
    if (isKnowledgeParseInFlight(item.parse_status)) {
      traceAvailableById[id] = true
      return
    }
    if (Object.prototype.hasOwnProperty.call(traceAvailableById, id)) return
    traceProbeInflight.add(id)
    try {
      const res = (await deps.getKnowledgeSpans(id)) as { success?: boolean; data?: unknown } | null
      traceAvailableById[id] = Boolean(res?.success && deps.hasKnowledgeTrace(res.data))
    } catch {
      traceAvailableById[id] = false
    } finally {
      traceProbeInflight.delete(id)
    }
  }

  const loadKnowledgeList = async () => {
    try {
      await deps.ensureKnowledgeBases()
      const myKbs = deps.getRawKnowledgeBases().map((item) => ({
        id: String(item.id),
        name: item.name,
        type: item.type || 'document',
      }))
      const sharedKbs = (deps.getSharedKnowledgeBases() || [])
        .filter((s) => s.knowledge_base != null)
        .map((s) => ({
          id: String(s.knowledge_base!.id),
          name: s.knowledge_base!.name,
          type: s.knowledge_base!.type || 'document',
        }))
      const myKbIds = new Set(myKbs.map((kb) => kb.id))
      knowledgeList.value = [...myKbs, ...sharedKbs.filter((kb) => !myKbIds.has(kb.id))]
    } catch (error) {
      console.error('Failed to load knowledge list:', error)
    }
  }

  const syncDocumentSummaryState = (state: {
    id?: string
    summary_status?: string
    description?: string
  }) => {
    if (!state?.id) return
    const card = cardList.value.find((item) => item.id === state.id)
    if (!card) return
    if (typeof state.summary_status === 'string' && state.summary_status) {
      card.summary_status = state.summary_status
    }
    if (typeof state.description === 'string') {
      card.description = state.description
    }
  }

  const settleThenReload = async (
    targetKbId: string,
    leaving: string[],
    arriving: string[],
    reload: () => void,
  ) => {
    const isActive = () => deps.isCurrentKb(targetKbId)
    const fetchRows = async (ids: string[]) => {
      const query = new URLSearchParams()
      ids.forEach((id) => query.append('ids', id))
      return (await deps.batchQueryKnowledge(query.toString(), targetKbId)) as { data?: KnowledgeCard[] }
    }
    try {
      if (leaving.length) await deps.waitForKnowledgeDeletion(leaving, fetchRows, { isActive })
      for (let i = 0; arriving.length && i < 30 && isActive(); i++) {
        const res = await fetchRows(arriving)
        const present = new Set((res?.data ?? []).map((row) => row.id))
        if (arriving.every((id) => present.has(id))) break
        await new Promise((resolve) => setTimeout(resolve, 1000))
      }
    } catch {
      // Reload whatever the server shows now.
    }
    reload()
  }

  const handleAssistantDataChanges = (changes: AssistantDataChange[]) => {
    const targetKbId = kbId.value
    if (!targetKbId) return
    const relevant = changes.filter(
      (change) =>
        change.ids.knowledgeBaseIds.includes(targetKbId) ||
        (['knowledge', 'chunk'].includes(change.area) &&
          change.ids.knowledgeIds.some(
            (id) =>
              cardList.value.some((item) => item.id === id) ||
              (isCardDetails.value && details.id === id),
          )) ||
        (change.area === 'knowledge' && change.ids.knowledgeBaseIds.length === 0),
    )
    if (!relevant.length) return
    if (relevant.some((change) => change.operation === 'knowledge_base.delete')) {
      message.warning(t('knowledgeBase.assistantDeleted'))
      deps.handleNavigateToKbList()
      return
    }
    if (isCardDetails.value && relevant.some((change) => change.ids.knowledgeIds.includes(details.id))) {
      if (
        relevant.some(
          (change) =>
            change.ids.knowledgeIds.includes(details.id) &&
            LEAVING_OPERATIONS.includes(change.operation),
        )
      ) {
        deps.closeDoc()
      } else {
        deps.getCardDetails({ id: details.id })
      }
    }
    const reload = () => {
      if (!deps.isCurrentKb(targetKbId)) return
      deps.resetPage()
      void deps.loadKnowledgeFiles(targetKbId)
      void deps.loadFolderTree(targetKbId)
      void deps.loadTags(targetKbId, true)
    }
    if (relevant.some((change) => change.area === 'knowledge_base')) {
      deps.resetPage()
      void deps.loadKnowledgeBaseInfo(targetKbId, true)
    } else {
      reload()
    }
    const loaded = new Set(cardList.value.map((item) => item.id))
    const leaving = new Set<string>()
    const arriving = new Set<string>()
    for (const change of relevant) {
      if (!LEAVING_OPERATIONS.includes(change.operation) && change.operation !== 'knowledge.move') continue
      for (const id of change.ids.knowledgeIds) {
        if (change.operation === 'knowledge.move' && !loaded.has(id)) arriving.add(id)
        else leaving.add(id)
      }
    }
    if (leaving.size || arriving.size) {
      void settleThenReload(targetKbId, [...leaving], [...arriving], reload)
    }
  }

  const closeCardMoreMenu = (index: number) => {
    if (cardList.value?.[index]) cardList.value[index].isMore = false
    moreIndex.value = -1
  }

  const confirmDeleteKnowledge = (index: number, item: KnowledgeCard) => {
    closeCardMoreMenu(index)
    void deps.deleteKnowledgeDocuments([item.id], () => deps.delKnowledgeDetails(item.id), false)
  }

  const onReparseMenuClick = (_index: number, item: KnowledgeCard) => {
    if (isKnowledgeParseInFlight(item.parse_status)) {
      message.info(t('knowledgeBase.rebuildInProgress'))
    }
  }

  const manualEditorSuccess = ({
    kbId: savedKbId,
  }: {
    kbId: string
    knowledgeId: string
    status: 'draft' | 'publish'
  }) => {
    if (savedKbId === kbId.value && !isFAQ.value) {
      deps.resetPage()
      void deps.loadKnowledgeFiles(savedKbId)
      void deps.loadFolderTree(savedKbId)
    }
  }

  const ensureDocumentKbReady = () => {
    if (isFAQ.value) {
      message.warning(t('knowledgeBase.operationNotSupportedForType'))
      return false
    }
    if (!kbId.value) {
      message.warning(t('knowledgeEditor.messages.missingId'))
      return false
    }
    if (!kbInfo.value || !kbInfo.value.summary_model_id) {
      message.warning(t('knowledgeBase.notInitialized'))
      return false
    }
    const strategy = kbInfo.value.indexing_strategy
    const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled
    if (needsEmbedding && !kbInfo.value.embedding_model_id) {
      message.warning(t('knowledgeBase.notInitialized'))
      return false
    }
    if (missingStorageEngine.value) {
      message.warning(t('knowledgeBase.missingStorageEngineUpload'))
      return false
    }
    return true
  }

  const enqueueUploads = (
    files: File[],
    options: {
      processConfig?: KnowledgeProcessOverrides
      tagIds?: string[]
      targetFolder?: string
    } = {},
  ) => {
    const targetKbId = kbId.value
    if (!targetKbId || files.length === 0) return
    const targetFolder = options.targetFolder || ROOT_FOLDER_PATH
    deps.enqueueUploadBatch({
      kbId: targetKbId,
      kbName: kbInfo.value?.name || '',
      targetFolder,
      tagIds: options.tagIds,
      processConfig: options.processConfig,
      uploads: files.map((file) => ({ file, fileName: buildUploadFileName(file, targetFolder) })),
    })
  }

  const executeUrlImport = async (
    url: string,
    processConfig?: KnowledgeProcessOverrides,
    tagIds?: string[],
  ) => {
    const targetKbId = kbId.value
    if (!targetKbId) {
      message.error(t('error.missingKbId'))
      return
    }

    const tagIdsToUpload = tagIds && tagIds.length > 0 ? [...tagIds] : undefined
    try {
      const responseData = (await deps.createKnowledgeFromURL(targetKbId, {
        url,
        tag_ids: tagIdsToUpload,
        process_config: processConfig,
      })) as {
        success?: boolean
        code?: number | string
        status?: string
        message?: string
        error?: { code?: string; message?: string }
      } | null
      deps.dispatchKnowledgeUploaded(targetKbId)
      const isSuccess =
        responseData?.success ||
        responseData?.code === 200 ||
        responseData?.status === 'success' ||
        (!responseData?.error && responseData)
      if (isSuccess) {
        message.success(t('knowledgeBase.urlImportSuccess'))
      } else {
        let errorMessage =
          responseData?.error?.message || responseData?.message || t('knowledgeBase.urlImportFailed')
        if (responseData?.code === 'duplicate_url' || responseData?.error?.code === 'duplicate_url') {
          errorMessage = t('knowledgeBase.urlExists')
        }
        message.error(errorMessage)
      }
    } catch (error: unknown) {
      const errObj = error as { code?: string; message?: string; error?: { message?: string } } | null
      let errorMessage =
        errObj?.error?.message || errObj?.message || t('knowledgeBase.urlImportFailed')
      if (errObj?.code === 'duplicate_url') errorMessage = t('knowledgeBase.urlExists')
      message.error(errorMessage)
    }
  }

  const handleUploadConfirmResult = async (result: UploadConfirmResult) => {
    if (result.mode === 'manual') return
    const files = result.files || []
    const urls = result.urls || []
    const processConfig = result.processConfig
    const tagIds = result.tagIds || []

    if (files.length > 0) {
      enqueueUploads(files, {
        processConfig,
        tagIds,
        targetFolder: result.targetFolder || ROOT_FOLDER_PATH,
      })
    }

    for (const url of urls) {
      await executeUrlImport(url, processConfig, tagIds)
    }
  }

  const openUploadConfirmDialog = async (files: File[], urls: string[] = []) => {
    if (!kbInfo.value || (files.length === 0 && urls.length === 0)) return
    try {
      const result = await deps.openUploadConfirm({
        mode: 'file',
        kbInfo: kbInfo.value,
        tagIds: [...selectedTagIds.value],
        files,
        urls,
        acceptFileTypes: acceptFileTypes.value,
        supportedFileTypes: [...supportedFileTypes.value],
        targetFolder: selectedFolderPath.value,
        folderOptions: folderOptions.value,
      })
      await handleUploadConfirmResult(result)
    } catch {
      // User cancelled upload dialog
    }
  }

  const handleUploadSourceFiles = (files: File[]) => {
    if (!ensureDocumentKbReady() || files.length === 0) return
    void openUploadConfirmDialog(files)
  }

  const handleUploadSourceUrl = (url: string) => {
    if (!ensureDocumentKbReady()) return
    void openUploadConfirmDialog([], [url])
  }

  const handleManualCreate = () => {
    if (!ensureDocumentKbReady()) return
    deps.openManualEditor({
      mode: 'create',
      kbId: kbId.value,
      status: 'draft',
      onSuccess: manualEditorSuccess,
    })
  }

  const handleManualEdit = (index: number, item: KnowledgeCard) => {
    if (isFAQ.value) return
    if (cardList.value[index]) cardList.value[index].isMore = false
    deps.openManualEditor({
      mode: 'edit',
      kbId: item.knowledge_base_id || kbId.value,
      knowledgeId: item.id,
      onSuccess: manualEditorSuccess,
    })
  }

  const handleViewTrace = (index: number, item: KnowledgeCard) => {
    if (cardList.value[index]) cardList.value[index].isMore = false
    moreIndex.value = -1
    deps.getCardDetails(item)
    details.id = item.id
    details.parse_status = item.parse_status
    void nextTick(() => {
      docContentRef.value?.openTimeline?.()
    })
  }

  const submitReparse = async (id: string, processConfig?: KnowledgeProcessOverrides) => {
    try {
      await deps.reparseKnowledge(id, processConfig ? { process_config: processConfig } : undefined)
      delete traceAvailableById[id]
      traceAvailableById[id] = true
      message.success(t('knowledgeBase.rebuildSubmitted'))
      deps.resetPage()
      void deps.loadKnowledgeFiles(kbId.value)
      deps.scheduleWikiStatusProbes()
    } catch (error: unknown) {
      message.error(
        (error as { message?: string } | null)?.message || t('knowledgeBase.rebuildFailed'),
      )
    }
  }

  const confirmRebuildKnowledge = async (index: number, item: KnowledgeCard) => {
    if (isFAQ.value || !canEdit.value) return
    if (!item?.id) {
      message.warning(t('knowledgeEditor.messages.missingId'))
      return
    }
    if (isKnowledgeParseInFlight(item.parse_status)) {
      message.info(t('knowledgeBase.rebuildInProgress'))
      return
    }
    closeCardMoreMenu(index)

    if (!kbInfo.value) {
      await submitReparse(item.id)
      return
    }

    let processOverrides: KnowledgeProcessOverrides | null = item.metadata?.process_overrides ?? null
    let fileName = item.file_name || item.title || ''
    let fileType = item.file_type || ''
    try {
      const detail = (await deps.getKnowledgeDetails(item.id)) as {
        success?: boolean
        data?: KnowledgeCard
      } | null
      if (detail?.success && detail.data) {
        processOverrides = detail.data.metadata?.process_overrides ?? processOverrides
        fileName = detail.data.file_name || detail.data.title || fileName
        fileType = detail.data.file_type || fileType
      }
    } catch {
      // Fall back to the list item's fields
    }

    try {
      const result = await deps.openUploadConfirm({
        mode: 'reparse',
        kbInfo: kbInfo.value,
        reparse: { knowledgeId: item.id, fileName, fileType, processOverrides },
      })
      if (result.mode === 'reparse' && result.reparse) {
        await submitReparse(result.reparse.knowledgeId, result.processConfig)
      }
    } catch {
      // User cancelled reparse dialog
    }
  }

  const confirmCancelParseKnowledge = async (item: KnowledgeCard) => {
    if (!item?.id) return
    try {
      await deps.cancelKnowledgeParse(item.id)
      message.success(t('knowledgeBase.cancelParseSubmitted'))
      void deps.loadKnowledgeFiles(kbId.value)
    } catch (error: unknown) {
      message.error(
        (error as { message?: string } | null)?.message || t('knowledgeBase.cancelParseFailed'),
      )
    }
  }

  const downloadKnowledge = async (item: KnowledgeCard) => {
    if (!item?.id) return
    try {
      const file = await deps.downKnowledgeDetails(item.id)
      const objectUrl = URL.createObjectURL(file)
      const link = document.createElement('a')
      link.style.display = 'none'
      link.href = objectUrl
      link.download = resolveKnowledgeDownloadFileName(item)
      document.body.appendChild(link)
      link.click()
      void nextTick(() => {
        link.remove()
        URL.revokeObjectURL(objectUrl)
      })
    } catch {
      message.error(t('file.downloadFailed'))
    }
  }

  const handleCardAction = (action: DocumentRowAction, item: KnowledgeCard) => {
    const idx = (cardList.value || []).findIndex((i) => i.id === item.id)
    if (action === 'download') return downloadKnowledge(item)
    if (action === 'edit') return handleManualEdit(idx, item)
    if (action === 'reparse') {
      if (isKnowledgeParseInFlight(item.parse_status)) return onReparseMenuClick(idx, item)
      return confirmRebuildKnowledge(idx, item)
    }
    if (action === 'cancel-parse') return confirmCancelParseKnowledge(item)
    if (action === 'move') return deps.handleMoveKnowledge(item)
    if (action === 'delete') return confirmDeleteKnowledge(idx, item)
    if (action === 'view-trace') return handleViewTrace(idx, item)
    if (action === 'batch-manage') return deps.handleEnterBatchFromCard(item)
  }

  const handleListAction = (action: DocumentRowAction, item: KnowledgeCard) => {
    const idx = (cardList.value || []).findIndex((i) => i.id === item.id)
    if (action === 'download') return downloadKnowledge(item)
    if (action === 'edit') return handleManualEdit(idx, item)
    if (action === 'reparse') return confirmRebuildKnowledge(idx, item)
    if (action === 'cancel-parse') return confirmCancelParseKnowledge(item)
    if (action === 'move') return deps.handleMoveKnowledge(item)
    if (action === 'delete') return confirmDeleteKnowledge(idx, item)
    if (action === 'view-trace') return handleViewTrace(idx, item)
    if (action === 'batch-manage') return deps.handleEnterBatchFromCard(item)
  }

  const isManualDraftKnowledge = (item: KnowledgeCard) =>
    item.type === 'manual' && item.parse_status === 'draft'

  const openKnowledgeItem = (item: KnowledgeCard) => {
    if (deps.shouldSuppressDocClick()) return
    if (canEdit.value && isManualDraftKnowledge(item)) {
      const index = cardList.value.findIndex((c) => c.id === item.id)
      if (index >= 0) {
        handleManualEdit(index, item)
        return
      }
    }
    deps.openCardDetails(item)
  }

  return {
    knowledgeList,
    traceAvailableById,
    clearTraceAvailabilityCache,
    probeTraceAvailable,
    loadKnowledgeList,
    syncDocumentSummaryState,
    handleAssistantDataChanges,
    ensureDocumentKbReady,
    handleUploadSourceFiles,
    handleUploadSourceUrl,
    handleManualCreate,
    handleCardAction,
    handleListAction,
    openKnowledgeItem,
  }
}
