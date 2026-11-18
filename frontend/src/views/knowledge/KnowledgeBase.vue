<script setup lang="ts">
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import { ref, onMounted, onUnmounted, watch, computed, nextTick, type Ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import DocContent from '@/components/DocContent.vue'
import useKnowledgeBase from '@/hooks/useKnowledgeBase'
import { useRoute, useRouter } from 'vue-router'
import ContextualGuide from '@/components/ContextualGuide.vue'
import { useUIStore } from '@/stores/ui'
import { useOrganizationStore } from '@/stores/organization'
import { useAuthStore } from '@/stores/auth'
import { permissionCanEditKB, permissionCanManageKB } from '@/utils/kbPermission'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useEditorResourcesStore } from '@/stores/editorResources'
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue'
import {
  batchQueryKnowledge, listKnowledgeTags, updateKnowledgeTagBatch, createKnowledgeFromURL,
  reparseKnowledge, cancelKnowledgeParse, batchDeleteKnowledge, batchDownloadKnowledge,
  delKnowledgeDetails, batchReparseKnowledge, getKnowledgeSpans, getKnowledgeDetails,
  listKnowledgeFolders, moveKnowledgeToFolder, renameKnowledgeFolder, downKnowledgeDetails,
} from '@/api/knowledge-base/index'
import { listMoveTargets, moveKnowledge, getKnowledgeMoveProgress } from '@/api/knowledge-base'
import { waitForKnowledgeDeletion } from '@/utils/knowledgeDeletion'
import { knowledgeSpansPayloadHasTrace } from '@/utils/knowledgeTrace'
import FAQEntryManager from './components/FAQEntryManager.vue'
import KbFolderTree from './components/KbFolderTree.vue'
import BatchTagDialog from './components/BatchTagDialog.vue'
import KBDocumentHeader from './components/KBDocumentHeader.vue'
import KBDocumentToolbar from './components/KBDocumentToolbar.vue'
import KBDocumentListPane from './components/KBDocumentListPane.vue'
import { useUploadConfirmStore } from '@/stores/uploadConfirm'
import { useUploadTasksStore } from '@/stores/uploadTasks'
import WikiBrowser from './wiki/WikiBrowser.vue'
import ImageGallery from './gallery/ImageGallery.vue'
import { getWikiStats } from '@/api/wiki'
import {
  isKnowledgeParseInFlight,
  knowledgeNeedsStatusPolling,
  shouldRefreshWikiStatusAfterKnowledgePoll,
} from './wikiStatusRefresh'
import { stalledMinutes, STALLED_POLL_INTERVAL_MS } from '@/utils/knowledgeProcessingStall'
import { ROOT_FOLDER_PATH } from './folderTree'
import { DEFAULT_DOCUMENT_SORT, getDocumentSortParams, type DocumentSortValue } from './documentSorting'
import { useI18n } from 'vue-i18n'
import { useMarqueeSelect } from '@/hooks/useMarqueeSelect'
import { useAssistantPageContext } from '@/composables/useAssistantPageContext'
import type { ParserEngineInfo } from '@/api/system'
import { useKBWikiStatusProbe } from './composables/useKBWikiStatusProbe'
import { useKBDocumentStatusPoll, type KBDetailInfo, type KnowledgeCard } from './composables/useKBDocumentStatusPoll'
import { useKBFolderAndTags } from './composables/useKBFolderAndTags'
import { useKBBatchAndMoveActions } from './composables/useKBBatchAndMoveActions'
import { useKBDocumentOperations } from './composables/useKBDocumentOperations'

type SnakeCaseParserEngine = ParserEngineInfo & {
  name?: string
  description?: string
  file_types?: string[]
  available?: boolean
  unavailable_reason?: string
}

const uiStore = useUIStore()
const orgStore = useOrganizationStore()
const authStore = useAuthStore()
const chatResources = useChatResourcesStore()
const editorResources = useEditorResourcesStore()
const uploadConfirmStore = useUploadConfirmStore()
const uploadTasksStore = useUploadTasksStore()
const router = useRouter()
const route = useRoute()
const { t } = useI18n()

const kbId = computed(() => String((route.params as Record<string, unknown>).kbId || ''))
const kbInfo = ref<KBDetailInfo | null>(null)
const toolbarRef = ref<InstanceType<typeof KBDocumentToolbar> | null>(null)
const docContentRef = ref<{ openTimeline?: () => void } | null>(null)
const kbLoading = ref(false)
const docListLoading = ref(true)
const isFAQ = computed(() => (kbInfo.value?.type || '') === 'faq')
const isWiki = computed(() => Boolean(kbInfo.value?.indexing_strategy?.wiki_enabled))

const {
  activeKbTab, kbViewTabs, shownKbTab, onViewWikiInGraph, isDisposed: isWikiDisposed,
  onWikiStatusChange, fetchWikiStatusOnce, scheduleWikiStatusProbes, disposeWikiStatus,
} = useKBWikiStatusProbe({
  kbId,
  isWiki,
  routeQuery: () => (route.query || {}) as Record<string, unknown>,
  replaceRouteQuery: (query) => router.replace({ query: query as Record<string, string> }),
  t,
  getWikiStats,
})

onUnmounted(() => {
  disposeWikiStatus()
})

const missingStorageEngine = computed(() => {
  if (!kbInfo.value || isFAQ.value) return false
  if (kbInfo.value.storage_backend_id) return false
  const spc = kbInfo.value.storage_provider_config
  return !spc || !spc.provider
})

const parserEngines = computed<SnakeCaseParserEngine[]>(
  () => editorResources.parserEngines as SnakeCaseParserEngine[],
)

const supportedFileTypes = computed<Set<string>>(() => {
  const engines = parserEngines.value
  if (!engines.length) return new Set<string>()
  const rules = kbInfo.value?.chunking_config?.parser_engine_rules || []
  const ruleMap = new Map<string, string>()
  for (const r of rules) {
    for (const ft of r.file_types) ruleMap.set(ft, r.engine)
  }
  const available = new Set<string>()
  const availableEngineNames = new Set(
    engines.filter((e) => e.available !== false).map((e) => e.name || ''),
  )
  for (const engine of engines) {
    for (const ft of engine.file_types || []) {
      if (available.has(ft)) continue
      const explicitEngine = ruleMap.get(ft)
      if (explicitEngine) {
        if (availableEngineNames.has(explicitEngine)) available.add(ft)
      } else if (engine.available !== false) {
        available.add(ft)
      }
    }
  }
  return available
})

const acceptFileTypes = computed(() => [...supportedFileTypes.value].map((ext) => '.' + ext).join(','))

const unsupportedFileTypes = computed<string[]>(() => {
  const engines = parserEngines.value
  if (!engines.length) return []
  const allTypes = new Set<string>()
  for (const engine of engines) {
    for (const ft of engine.file_types || []) allTypes.add(ft)
  }
  const supported = supportedFileTypes.value
  return [...allTypes].filter((ft) => !supported.has(ft)).sort()
})

const goToParserSettings = () => {
  if (kbId.value) uiStore.openKBSettings(kbId.value, 'parser')
}

const isOwner = computed(() => {
  const creatorId = kbInfo.value?.creator_id || ''
  const userId = authStore.user?.id || ''
  return Boolean(creatorId && creatorId === userId && authStore.hasRole('contributor'))
})
const isViaShare = computed(() =>
  Boolean(orgStore.sharedKnowledgeBases.find((s) => s.knowledge_base?.id === kbId.value)),
)
const effectiveKBPermission = computed(
  () => orgStore.getKBPermission(kbId.value) || kbInfo.value?.my_permission || '',
)

const canEdit = computed(() => {
  const permission = effectiveKBPermission.value
  if (permission) return permissionCanEditKB(permission)
  if (isViaShare.value) return orgStore.canEditKB(kbId.value, false)
  return isOwner.value || authStore.hasRole('admin') || orgStore.canEditKB(kbId.value, false)
})

const canManage = computed(() => {
  const permission = effectiveKBPermission.value
  if (permission) return permissionCanManageKB(permission)
  if (isViaShare.value) return orgStore.canManageKB(kbId.value, false)
  return isOwner.value || authStore.hasRole('admin') || orgStore.canManageKB(kbId.value, false)
})

const canMutateKnowledge = computed(
  () =>
    canEdit.value &&
    (isViaShare.value || isOwner.value || authStore.hasRole('admin') || authStore.hasRole('contributor')),
)

const canDownloadKnowledge = computed(() => {
  if (!authStore.hasRole('contributor')) return false
  const permission = effectiveKBPermission.value
  return !permission || permission === 'owner' || permission === 'admin' || permission === 'editor'
})

const kbHook = useKnowledgeBase(kbId.value)
const cardList = kbHook.cardList as Ref<KnowledgeCard[]>
const total = kbHook.total as Ref<number>
const moreIndex = kbHook.moreIndex as Ref<number>
const details = kbHook.details as { id: string; title?: string; parse_status?: string; [key: string]: unknown }
const { getKnowled, onVisibleChange: _onVisibleChange, getCardDetails, getfDetails } = kbHook

const showKbDetailContextualGuide = computed(
  () => Boolean(kbId.value) && !isFAQ.value && canEdit.value && !docListLoading.value && cardList.value.length === 0,
)

const isCardDetails = ref(false)
useAssistantPageContext(() => {
  if (!kbId.value) return null
  const docOpen = isCardDetails.value && Boolean(details.id)
  return {
    page: 'knowledge_base',
    section: kbInfo.value?.id === kbId.value && isFAQ.value ? 'faq' : undefined,
    knowledgeBaseId: kbId.value,
    knowledgeBaseName: kbInfo.value?.id === kbId.value ? kbInfo.value?.name : undefined,
    chatModelId: kbInfo.value?.id === kbId.value ? kbInfo.value?.summary_model_id || undefined : undefined,
    knowledgeId: docOpen ? details.id : undefined,
    knowledgeTitle: docOpen ? details.title : undefined,
  }
})

const knowledgeScroll = ref<HTMLElement | null>(null)
const page = ref(1)
const viewportHeight = window.innerHeight || document.documentElement.clientHeight
const pageSize = Math.max(35, Math.floor(viewportHeight / 148) * 5)
let scrollLoading = false
let documentPageGeneration = 0
const resetPage = () => {
  page.value = 1
  scrollLoading = false
  documentPageGeneration++
}

const isCurrentKb = (targetKbId: string) => targetKbId === kbId.value

type DocViewMode = 'grid' | 'list'
const VIEW_MODE_KEY = 'genyee.kb.docs.viewMode'
const initViewMode = (): DocViewMode => {
  try {
    return localStorage.getItem(VIEW_MODE_KEY) === 'grid' ? 'grid' : 'list'
  } catch {
    return 'list'
  }
}
const viewMode = ref<DocViewMode>(initViewMode())
watch(viewMode, (v) => {
  try {
    localStorage.setItem(VIEW_MODE_KEY, v)
  } catch {
    // Ignore storage quota / private mode errors
  }
})

const docSearchKeyword = ref('')
const selectedFileType = ref('')
const selectedParseStatus = ref('')
const selectedSource = ref('')
const updatedTimeRange = ref<string[]>([])
const selectedDocumentSort = ref<DocumentSortValue>(DEFAULT_DOCUMENT_SORT)

const messageAdapter = {
  info: (msg: string) => MessagePlugin.info(msg),
  warning: (msg: string) => MessagePlugin.warning(msg),
  success: (msg: string) => MessagePlugin.success(msg),
  error: (msg: string) => MessagePlugin.error(msg),
}

const {
  selectedIds, batchMode, batchDeleting, batchTagging, batchDownloading,
  batchTagDialogVisible, batchTagPreSelectedIds, selectedDocumentAssistantActions,
  toggleSelectRow, onCardGridCheckboxChange, toggleSelectAll, clearSelection,
  clampSelectionIndices, toggleBatchMode, handleBatchCancel, handleEnterBatchFromCard,
  abortBatchDownload, bumpDeleteGeneration, handleBatchDownload, deleteKnowledgeDocuments,
  confirmBatchDelete, handleBatchTag, onBatchTagConfirm, moveMenuMode, moveTargetKbs,
  moveTargetsLoading, moveSelectedTargetName, moveMode, moveSubmitting, stopMovePoll,
  handleMoveKnowledge, handleMoveSelectTarget, handleMoveBack, handleMoveConfirm,
} = useKBBatchAndMoveActions({
  kbId, canMutateKnowledge, cardList, moreIndex,
  isBatchReparsing: () => batchReparsing.value,
  isCurrentKb, isDisposed: isWikiDisposed, resetPage,
  loadKnowledgeFiles: (id) => loadKnowledgeFiles(id),
  loadFolderTree: (id) => loadFolderTree(id),
  loadTags: (id, reset) => loadTags(id, reset),
  batchDownloadKnowledge, batchDeleteKnowledge, batchQueryKnowledge, updateKnowledgeTagBatch,
  waitForKnowledgeDeletion, listMoveTargets, moveKnowledge, getKnowledgeMoveProgress,
  message: messageAdapter, t,
})

const {
  folderTree, folderTreeLoading, selectedFolderPath, folderTreeCollapsed, showFolderTree,
  isFiltering, showDocumentFolderPath, visibleSubFolders, folderBreadcrumbs,
  folderContentsCount, showFolderContentsCount, folderContentsMessage,
  folderSubfolderDocumentCount, folderEmptyState, folderOptions, loadFolderTree,
  handleFolderSelect, moveKnowledgeIntoFolder, handleFolderRename,
  handleFolderTreeCollapsedChange, selectedTagIds, selectedTagNames, tagList,
  filterTagOptions, tagLoading, tagSearchQuery, tagPage, tagHasMore, tagLoadingMore,
  loadTags, handleTagFilterChange, onTagCatalogChanged,
} = useKBFolderAndTags({
  kbId, isFAQ, total, docSearchKeyword, selectedFileType, selectedParseStatus,
  selectedSource, updatedTimeRange, isCurrentKb, resetPage, clearSelection, batchMode,
  loadKnowledgeFiles: (id) => loadKnowledgeFiles(id),
  listKnowledgeFolders, moveKnowledgeToFolder, renameKnowledgeFolder, listKnowledgeTags,
  uiStore, message: messageAdapter, t,
})

const pendingKnowledgeId = ref<string | null>((route.query.knowledge_id as string) || null)
let autoOpenRequest = 0

const openCardDetails = (item: KnowledgeCard) => {
  isCardDetails.value = true
  getCardDetails(item)
}

const closeDoc = () => {
  isCardDetails.value = false
}

const tryAutoOpenDocument = async () => {
  if (!pendingKnowledgeId.value) return
  const targetId = pendingKnowledgeId.value
  pendingKnowledgeId.value = null
  const request = ++autoOpenRequest
  const card = cardList.value.find((c) => c.id === targetId)

  let target: KnowledgeCard = card || { id: targetId, parse_status: '' }
  try {
    const response = (await getKnowledgeDetails(targetId)) as { data?: Partial<KnowledgeCard> } & Partial<KnowledgeCard>
    if (request !== autoOpenRequest) return
    const detail = response?.data || response
    if (detail && typeof detail === 'object') {
      target = { ...target, ...detail, id: targetId }
      selectedFolderPath.value = detail.folder_path || ROOT_FOLDER_PATH
    }
  } catch (error) {
    console.error('Failed to resolve referenced document folder', error)
  }

  if (request !== autoOpenRequest) return
  await nextTick()
  openCardDetails(target)
}

const handleOpenKBSettings = () => {
  if (!kbId.value) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  uiStore.openKBSettings(kbId.value)
}

const handleNavigateToKbList = () => {
  void router.push('/platform/knowledge-bases')
}

const handleNavigateToCurrentKB = () => {
  if (kbId.value) void router.push(`/platform/knowledge-bases/${kbId.value}`)
}

const handleKnowledgeDropdownSelect = (id: string) => {
  if (id && id !== kbId.value) void router.push(`/platform/knowledge-bases/${id}`)
}

const {
  onContainerMouseDown: onDocMarqueeMouseDown,
  marqueeVisible: docMarqueeVisible,
  marqueeMode: docMarqueeMode,
  boxStyle: docMarqueeBoxStyle,
  shouldSuppressClick: shouldSuppressDocClick,
} = useMarqueeSelect({
  containerRef: knowledgeScroll,
  itemSelector: '.knowledge-card[data-select-id], .doc-list-row[data-select-id]',
  selectedIds,
  getItemId: (el) => el.dataset.selectId || null,
  enabled: computed(() => (canEdit.value || canDownloadKnowledge.value) && !isFAQ.value && cardList.value.length > 0),
  onSelectionStart: () => {
    batchMode.value = true
  },
})

const {
  knowledgeList, traceAvailableById, clearTraceAvailabilityCache, probeTraceAvailable,
  loadKnowledgeList, syncDocumentSummaryState, handleAssistantDataChanges,
  ensureDocumentKbReady, handleUploadSourceFiles, handleUploadSourceUrl,
  handleManualCreate, handleCardAction, handleListAction, openKnowledgeItem,
} = useKBDocumentOperations({
  kbId, kbInfo, isFAQ, canEdit, missingStorageEngine, cardList, moreIndex, details,
  isCardDetails, selectedTagIds, selectedFolderPath, folderOptions, acceptFileTypes,
  supportedFileTypes, docContentRef, isCurrentKb, resetPage,
  loadKnowledgeFiles: (id) => loadKnowledgeFiles(id),
  loadFolderTree: (id) => loadFolderTree(id),
  loadTags: (id, reset) => loadTags(id, reset),
  loadKnowledgeBaseInfo: (id, force) => loadKnowledgeBaseInfo(id, force),
  scheduleWikiStatusProbes, openCardDetails, getCardDetails, closeDoc,
  handleNavigateToKbList,
  shouldSuppressDocClick: () => Boolean(shouldSuppressDocClick?.()),
  deleteKnowledgeDocuments, handleMoveKnowledge, handleEnterBatchFromCard,
  batchQueryKnowledge, waitForKnowledgeDeletion, createKnowledgeFromURL,
  reparseKnowledge, getKnowledgeDetails, getKnowledgeSpans,
  hasKnowledgeTrace: knowledgeSpansPayloadHasTrace,
  cancelKnowledgeParse, delKnowledgeDetails, downKnowledgeDetails,
  ensureKnowledgeBases: () => chatResources.ensureKnowledgeBases(),
  getRawKnowledgeBases: () => chatResources.rawKnowledgeBases,
  getSharedKnowledgeBases: () => orgStore.sharedKnowledgeBases || [],
  openUploadConfirm: (options) => uploadConfirmStore.open(options),
  enqueueUploadBatch: (input) => uploadTasksStore.enqueue(input),
  openManualEditor: (options) => uiStore.openManualEditor(options),
  dispatchKnowledgeUploaded: (targetKbId) => {
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', { detail: { kbId: targetKbId } }))
  },
  message: messageAdapter, t,
})

const {
  batchReparsing, pendingReparseAck, resetStatusPolling, confirmBatchReparse, disposeStatusPolling,
} = useKBDocumentStatusPoll({
  kbId, isFAQ, cardList, total, docListLoading, selectedIds, batchMode,
  batchDeleting, batchDownloading, traceAvailableById, pendingKnowledgeId,
  tryAutoOpenDocument, clearSelection, scheduleWikiStatusProbes, fetchWikiStatusOnce,
  batchQueryKnowledge, batchReparseKnowledge, stalledMinutes,
  stalledPollIntervalMs: STALLED_POLL_INTERVAL_MS,
  isKnowledgeParseInFlight, knowledgeNeedsStatusPolling,
  shouldRefreshWikiStatusAfterKnowledgePoll,
  message: messageAdapter, t,
})

watch([kbId, selectedFolderPath], resetStatusPolling, { flush: 'sync' })
watch(kbId, () => {
  abortBatchDownload()
  bumpDeleteGeneration()
})

useAssistantDataChange(handleAssistantDataChanges, {
  areas: ['knowledge_base', 'knowledge', 'chunk'],
})

const onVisibleChange = (visible: boolean) => {
  _onVisibleChange(visible)
  if (!visible) moveMenuMode.value = 'normal'
}

const onCardMoreVisibleChange = (visible: boolean, item: KnowledgeCard) => {
  onVisibleChange(visible)
  if (visible) void probeTraceAvailable(item)
}

const clearDocumentFilters = () => {
  selectedFileType.value = ''
  selectedParseStatus.value = ''
  selectedSource.value = ''
  updatedTimeRange.value = []
  handleTagFilterChange([])
}

const filterParams = computed(() => {
  const [start, end] = updatedTimeRange.value || []
  return {
    tag_ids: selectedTagIds.value.length > 0 ? selectedTagIds.value.join(',') : undefined,
    keyword: docSearchKeyword.value ? docSearchKeyword.value.trim() : undefined,
    file_type: selectedFileType.value || undefined,
    parse_status: selectedParseStatus.value || undefined,
    source: selectedSource.value || undefined,
    start_time: start ? `${start} 00:00:00` : undefined,
    end_time: end ? `${end} 23:59:59` : undefined,
    ...getDocumentSortParams(selectedDocumentSort.value),
    folder_path: selectedFolderPath.value,
    folder_recursive: isFiltering.value,
  }
})

function loadKnowledgeFiles(kbIdValue: string): Promise<void> {
  if (!kbIdValue) return Promise.resolve()
  if (!isFAQ.value) docListLoading.value = true
  return getKnowled(
    { page: 1, page_size: pageSize, ...filterParams.value },
    kbIdValue,
  ).finally(() => {
    if (isCurrentKb(kbIdValue) && !isFAQ.value) docListLoading.value = false
  })
}

const handleDocumentSortSelect = (value: DocumentSortValue) => {
  if (selectedDocumentSort.value === value) return
  selectedDocumentSort.value = value
  clearSelection()
  resetPage()
  if (knowledgeScroll.value) knowledgeScroll.value.scrollTop = 0
  if (kbId.value && !isFAQ.value) void loadKnowledgeFiles(kbId.value)
}

const loadKnowledgeBaseInfo = async (targetKbId: string, force = false) => {
  if (!targetKbId) {
    kbInfo.value = null
    cardList.value = []
    total.value = 0
    return
  }
  kbLoading.value = true
  try {
    const data = (await chatResources.fetchKnowledgeBaseById(targetKbId, force)) as KBDetailInfo | null
    if (!isCurrentKb(targetKbId)) return

    kbInfo.value = data
    selectedTagIds.value = []
    selectedTagNames.value = {}
    uiStore.clearSelectedTagIds()
    if (!isFAQ.value) {
      void loadKnowledgeFiles(targetKbId)
      void loadFolderTree(targetKbId)
    } else {
      cardList.value = []
      total.value = 0
      folderTree.value = null
    }
    void loadTags(targetKbId, true)
  } catch (error) {
    if (!isCurrentKb(targetKbId)) return
    console.error('Failed to load knowledge base info:', error)
    kbInfo.value = null
    cardList.value = []
    total.value = 0
  } finally {
    if (isCurrentKb(targetKbId)) kbLoading.value = false
  }
}

let tagSearchDebounce: number | null = null
let docSearchDebounce: number | null = null

watch(
  () => kbId.value,
  (newKbId, oldKbId) => {
    if (oldKbId !== undefined && newKbId !== oldKbId) {
      autoOpenRequest++
      stopMovePoll()
      moveSubmitting.value = false
      moveMenuMode.value = 'normal'
      isCardDetails.value = false
      batchMode.value = false
      batchTagDialogVisible.value = false
      if (tagSearchDebounce) {
        clearTimeout(tagSearchDebounce)
        tagSearchDebounce = null
      }
      if (docSearchDebounce) {
        clearTimeout(docSearchDebounce)
        docSearchDebounce = null
      }
    }
    if (!newKbId) {
      kbInfo.value = null
      cardList.value = []
      total.value = 0
      return
    }
    if (newKbId === oldKbId && kbInfo.value) return

    if (newKbId !== oldKbId) {
      clearTraceAvailabilityCache()
      cardList.value = []
      total.value = 0
      docListLoading.value = true
      resetPage()
      tagSearchQuery.value = ''
      tagPage.value = 1
      uiStore.clearSelectedTagIds()
      folderTree.value = null
      selectedFolderPath.value = ROOT_FOLDER_PATH
    }
    void loadKnowledgeBaseInfo(newKbId)
  },
  { immediate: true },
)

watch(
  selectedTagIds,
  (_newVal, oldVal) => {
    if (oldVal !== undefined && kbId.value) void loadKnowledgeFiles(kbId.value)
  },
  { deep: true },
)

watch(tagSearchQuery, (newVal, oldVal) => {
  if (newVal === oldVal) return
  if (tagSearchDebounce) window.clearTimeout(tagSearchDebounce)
  tagSearchDebounce = window.setTimeout(() => {
    if (kbId.value) void loadTags(kbId.value, true)
  }, 300)
})

watch(docSearchKeyword, (newVal, oldVal) => {
  if (newVal === oldVal) return
  if (docSearchDebounce) window.clearTimeout(docSearchDebounce)
  docSearchDebounce = window.setTimeout(() => {
    if (kbId.value) {
      resetPage()
      void loadKnowledgeFiles(kbId.value)
    }
  }, 300)
})

watch(selectedFileType, (newVal, oldVal) => {
  if (newVal !== oldVal && kbId.value) {
    resetPage()
    void loadKnowledgeFiles(kbId.value)
  }
})

watch(
  [selectedParseStatus, selectedSource, updatedTimeRange],
  () => {
    if (kbId.value) {
      resetPage()
      void loadKnowledgeFiles(kbId.value)
    }
  },
  { deep: true },
)

watch(selectedFolderPath, () => {
  if (!kbId.value || isFAQ.value) return
  clearSelection()
  resetPage()
  void loadKnowledgeFiles(kbId.value)
})

const handleFileUploaded = (event: CustomEvent<{ kbId?: string; settled?: boolean }>) => {
  const uploadedKbId = event.detail?.kbId
  if (uploadedKbId && uploadedKbId === kbId.value && !isFAQ.value) {
    const midBatch = event.detail.settled === false
    if (midBatch && page.value > 1) return
    resetPage()
    void loadKnowledgeFiles(uploadedKbId)
    void loadFolderTree(uploadedKbId)
    if (midBatch) return
    void loadTags(uploadedKbId)
    scheduleWikiStatusProbes()
  }
}

const handleOpenURLImportDialog = (event: CustomEvent<{ kbId?: string }>) => {
  const eventKbId = event.detail?.kbId
  if (eventKbId && eventKbId === kbId.value && !isFAQ.value && ensureDocumentKbReady()) {
    toolbarRef.value?.openUrlDialog()
  }
}

const handleKnowledgeFileDrop = (event: CustomEvent<{ kbId?: string; files?: File[] }>) => {
  const eventKbId = event.detail?.kbId
  const files = Array.isArray(event.detail?.files) ? event.detail.files : []
  if (eventKbId !== kbId.value || isFAQ.value || files.length === 0) return
  handleUploadSourceFiles(files)
}

watch(
  () => route.query.knowledge_id,
  (newId) => {
    if (typeof newId !== 'string' || !newId) return
    pendingKnowledgeId.value = newId
    void tryAutoOpenDocument()
  },
)

const handleOpenKnowledgeEvent = (e: Event) => {
  const detail = (e as CustomEvent<{ kbId: string; knowledgeId: string }>).detail
  if (!detail?.knowledgeId || (detail.kbId && detail.kbId !== kbId.value)) return
  pendingKnowledgeId.value = detail.knowledgeId
  void tryAutoOpenDocument()
}

onMounted(() => {
  void loadKnowledgeList()
  void editorResources.ensureParserEngines()
  window.addEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener)
  window.addEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener)
  window.addEventListener('genyee:knowledge-file-drop', handleKnowledgeFileDrop as EventListener)
  window.addEventListener('genyee:open-knowledge', handleOpenKnowledgeEvent as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener)
  window.removeEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener)
  window.removeEventListener('genyee:knowledge-file-drop', handleKnowledgeFileDrop as EventListener)
  window.removeEventListener('genyee:open-knowledge', handleOpenKnowledgeEvent as EventListener)
  autoOpenRequest++
  if (tagSearchDebounce) clearTimeout(tagSearchDebounce)
  if (docSearchDebounce) clearTimeout(docSearchDebounce)
  stopMovePoll()
  abortBatchDownload()
  bumpDeleteGeneration()
  disposeStatusPolling()
})

const openSourceDoc = (knowledgeId: string) => {
  isCardDetails.value = true
  getCardDetails({ id: knowledgeId })
}

const handleScroll = () => {
  if (isFAQ.value || docListLoading.value || scrollLoading) return
  const currentKbId = kbId.value
  const element = knowledgeScroll.value
  if (!currentKbId || !element || element.clientHeight <= 0) return
  const { scrollTop, scrollHeight, clientHeight } = element
  if (scrollTop + clientHeight < scrollHeight - 10) return
  const previousCount = cardList.value.length
  if (previousCount >= total.value || page.value >= Math.ceil(total.value / pageSize)) return

  const nextPage = ++page.value
  const generation = documentPageGeneration
  scrollLoading = true
  getKnowled({ page: nextPage, page_size: pageSize, ...filterParams.value }, currentKbId).finally(async () => {
    if (!isCurrentKb(currentKbId) || generation !== documentPageGeneration) return
    scrollLoading = false
    if (cardList.value.length <= previousCount) {
      page.value--
      return
    }
    await nextTick()
    if (generation === documentPageGeneration) fillDocumentViewport()
  })
}

const fillDocumentViewport = () => {
  const element = knowledgeScroll.value
  if (element && element.clientHeight > 0 && element.scrollHeight <= element.clientHeight) {
    handleScroll()
  }
}

watch([knowledgeScroll, docListLoading, viewMode], fillDocumentViewport, { flush: 'post' })

const getDoc = (targetPage: number) => {
  getfDetails(details.id, targetPage)
}

watch(viewMode, (mode) => {
  if (mode === 'grid' && selectedIds.value.size > 0) batchMode.value = true
})

watch(
  [selectedTagIds, docSearchKeyword, selectedFileType, selectedParseStatus, selectedSource, updatedTimeRange, kbId],
  () => clearSelection(),
)

watch(cardList, () => clampSelectionIndices(), { deep: false })

const handleKBEditorSuccess = (kbIdValue: string) => {
  chatResources.invalidateKnowledgeBaseDetail(kbIdValue)
  chatResources.invalidate('knowledgeBases')
  void loadKnowledgeList()
  if (kbIdValue === kbId.value) void loadKnowledgeBaseInfo(kbIdValue, true)
}

const onToggleTagFilter = (tagId: string, checked: boolean) => {
  handleTagFilterChange(
    checked ? [...selectedTagIds.value, tagId] : selectedTagIds.value.filter((id) => id !== tagId),
  )
}

// Keep composable state bound in setup scope for unit-test introspection.
void [kbLoading, pendingReparseAck]
</script>

<template>
  <template v-if="!isFAQ">
    <div class="knowledge-layout">
      <KBDocumentHeader
        :kb-id="kbId"
        :kb-info="kbInfo"
        :knowledge-list="knowledgeList"
        :kb-view-tabs="kbViewTabs"
        :shown-kb-tab="shownKbTab"
        :is-lite-mode="authStore.isLiteMode"
        :supported-file-types="[...supportedFileTypes]"
        :unsupported-file-types="unsupportedFileTypes"
        :can-edit="canEdit"
        :can-manage="canManage"
        :missing-storage-engine="missingStorageEngine"
        @navigate-kb-list="handleNavigateToKbList"
        @navigate-current-kb="handleNavigateToCurrentKB"
        @switch-kb="handleKnowledgeDropdownSelect"
        @select-tab="activeKbTab = $event"
        @open-settings="handleOpenKBSettings"
        @open-parser-settings="goToParserSettings"
      />

      <div v-if="isWiki && (activeKbTab === 'wiki' || activeKbTab === 'graph')" class="wiki-main-area">
        <WikiBrowser
          v-if="kbId"
          :knowledge-base-id="kbId"
          :view="activeKbTab === 'graph' ? 'graph' : 'browser'"
          :can-edit="canEdit"
          @open-source-doc="openSourceDoc"
          @status-change="onWikiStatusChange"
          @view-graph="onViewWikiInGraph"
        />
      </div>

      <ImageGallery
        v-if="activeKbTab === 'gallery' && kbId"
        :knowledge-base-id="kbId"
        :can-edit="canEdit"
        @open-source-doc="openSourceDoc"
      />

      <template v-if="activeKbTab === 'documents' || (!isWiki && activeKbTab !== 'gallery')">
        <div class="knowledge-main">
          <KbFolderTree
            v-if="showFolderTree && !folderTreeCollapsed"
            :tree="folderTree"
            :selected-path="selectedFolderPath"
            :loading="folderTreeLoading"
            :can-edit="canEdit"
            :root-label="kbInfo?.name"
            @select="handleFolderSelect"
            @update:collapsed="handleFolderTreeCollapsedChange"
            @rename="handleFolderRename"
          />
          <div class="tag-content">
            <div class="doc-card-area">
              <KBDocumentToolbar
                ref="toolbarRef"
                :kb-name="kbInfo?.name"
                :show-folder-tree="showFolderTree"
                :folder-tree-collapsed="folderTreeCollapsed"
                :folder-breadcrumbs="folderBreadcrumbs"
                :doc-list-loading="docListLoading"
                :is-filtering="isFiltering"
                :show-folder-contents-count="showFolderContentsCount"
                :folder-contents-count="folderContentsCount"
                :folder-contents-message="folderContentsMessage"
                :doc-search-keyword="docSearchKeyword"
                :selected-file-type="selectedFileType"
                :selected-parse-status="selectedParseStatus"
                :selected-source="selectedSource"
                :updated-time-range="updatedTimeRange"
                :selected-tag-ids="selectedTagIds"
                :tag-search-query="tagSearchQuery"
                :tag-loading="tagLoading"
                :has-loaded-tags="tagList.length > 0"
                :filter-tag-options="filterTagOptions"
                :tag-has-more="tagHasMore"
                :tag-loading-more="tagLoadingMore"
                :view-mode="viewMode"
                :show-batch-toggle="canDownloadKnowledge || canMutateKnowledge"
                :has-documents="cardList.length > 0"
                :batch-mode="batchMode"
                :batch-action-busy="batchDeleting || batchReparsing || batchTagging || batchDownloading"
                :selected-document-sort="selectedDocumentSort"
                :can-edit="canEdit"
                :accept-file-types="acceptFileTypes"
                :supported-file-types="[...supportedFileTypes]"
                @expand-folder-tree="handleFolderTreeCollapsedChange(false)"
                @select-folder="handleFolderSelect"
                @update:doc-search-keyword="docSearchKeyword = $event"
                @refresh-files="kbId && loadKnowledgeFiles(kbId)"
                @clear-filters="clearDocumentFilters"
                @update:selected-file-type="selectedFileType = $event"
                @update:selected-parse-status="selectedParseStatus = $event"
                @update:selected-source="selectedSource = $event"
                @update:updated-time-range="updatedTimeRange = $event"
                @update:tag-search-query="tagSearchQuery = $event"
                @toggle-tag-filter="onToggleTagFilter"
                @load-more-tags="kbId && loadTags(kbId)"
                @toggle-batch-mode="toggleBatchMode"
                @select-sort="handleDocumentSortSelect"
                @update:view-mode="viewMode = $event"
                @upload-files="handleUploadSourceFiles"
                @upload-url="handleUploadSourceUrl"
                @manual-create="handleManualCreate"
              />

              <KBDocumentListPane
                :kb-id="kbId"
                :card-list="cardList"
                :folder-options="folderOptions"
                :visible-sub-folders="visibleSubFolders"
                :selected-ids="selectedIds"
                :batch-mode="batchMode"
                :view-mode="viewMode"
                :doc-list-loading="docListLoading"
                :can-edit="canEdit"
                :can-download-knowledge="canDownloadKnowledge"
                :can-mutate-knowledge="canMutateKnowledge"
                :trace-available-by-id="traceAvailableById"
                :move-menu-mode="moveMenuMode"
                :move-target-kbs="moveTargetKbs"
                :move-targets-loading="moveTargetsLoading"
                :move-selected-target-name="moveSelectedTargetName"
                :move-mode="moveMode"
                :move-submitting="moveSubmitting"
                :show-document-folder-path="showDocumentFolderPath"
                :folder-empty-state="folderEmptyState"
                :folder-subfolder-document-count="folderSubfolderDocumentCount"
                :doc-marquee-visible="Boolean(docMarqueeVisible)"
                :doc-marquee-mode="String(docMarqueeMode || '')"
                :doc-marquee-box-style="docMarqueeBoxStyle || {}"
                :batch-deleting="batchDeleting"
                :batch-reparsing="batchReparsing"
                :batch-tagging="batchTagging"
                :batch-downloading="batchDownloading"
                :selected-document-assistant-actions="selectedDocumentAssistantActions"
                @scroll-el-change="knowledgeScroll = $event"
                @scroll="handleScroll"
                @marquee-mousedown="onDocMarqueeMouseDown"
                @open="openKnowledgeItem"
                @open-folder="handleFolderSelect"
                @move-to-folder="(ids, path) => moveKnowledgeIntoFolder(ids, path)"
                @toggle-card-checkbox="onCardGridCheckboxChange"
                @card-menu-visible-change="onCardMoreVisibleChange"
                @card-action="handleCardAction"
                @toggle-row="toggleSelectRow"
                @toggle-all="toggleSelectAll"
                @list-action="handleListAction"
                @probe-trace="probeTraceAvailable"
                @tags-changed="onTagCatalogChanged"
                @move-select-target="handleMoveSelectTarget"
                @move-back="handleMoveBack"
                @move-confirm="handleMoveConfirm"
                @update:move-mode="moveMode = $event"
                @reset-move-state="moveMenuMode = 'normal'"
                @batch-cancel="handleBatchCancel"
                @batch-delete="confirmBatchDelete"
                @batch-reparse="confirmBatchReparse"
                @batch-tag="handleBatchTag"
                @batch-download="handleBatchDownload"
                @select-loaded="toggleSelectAll(true)"
              />
            </div>
          </div>
        </div>
      </template>

      <DocContent
        ref="docContentRef"
        :visible="isCardDetails"
        :details="details"
        :canEditKB="canEdit"
        :canDownloadKB="canDownloadKnowledge"
        :kbId="kbId"
        @closeDoc="closeDoc"
        @getDoc="getDoc"
        @summaryStateChange="syncDocumentSummaryState"
      />
    </div>
  </template>
  <template v-else>
    <div class="faq-manager-wrapper">
      <FAQEntryManager v-if="kbId" :kb-id="kbId" />
    </div>
  </template>

  <KnowledgeBaseEditorModal
    :visible="uiStore.showKBEditorModal"
    :mode="uiStore.kbEditorMode"
    :kb-id="uiStore.currentKBId || undefined"
    :initial-type="uiStore.kbEditorType"
    @update:visible="(val) => val ? null : uiStore.closeKBEditor()"
    @success="handleKBEditorSuccess"
  />

  <ContextualGuide tour="kbDetail" :when="showKbDetailContextualGuide" />

  <BatchTagDialog
    :visible="batchTagDialogVisible"
    :count="selectedIds.size"
    :kb-id="kbId"
    :pre-selected-tag-ids="batchTagPreSelectedIds"
    :confirm-loading="batchTagging"
    @tags-changed="onTagCatalogChanged"
    @update:visible="batchTagDialogVisible = $event"
    @confirm="onBatchTagConfirm"
  />
</template>

<style>
.document-filter-popup .t-popup__content {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  box-shadow: var(--gy-shadow-lg);
}
</style>

<style scoped lang="less">
@import './knowledge-base.less';
</style>
