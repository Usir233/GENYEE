import { computed, nextTick, onUnmounted, reactive, ref, watch, type ComputedRef, type Ref } from 'vue'
import type { FormInstanceFunctions, FormRules } from 'tdesign-vue-next'

export interface FAQEntry {
  id: number
  chunk_id: string
  knowledge_id: string
  knowledge_base_id: string
  tag_id?: number
  is_enabled: boolean
  is_recommended: boolean
  standard_question: string
  similar_questions: string[]
  negative_questions: string[]
  answers: string[]
  updated_at: string
  showMore?: boolean
  score?: number
  match_type?: string
  matched_question?: string
  expanded?: boolean
  similarCollapsed?: boolean
  negativeCollapsed?: boolean
  answersCollapsed?: boolean
}

export interface FAQEntryPayload {
  standard_question: string
  similar_questions: string[]
  negative_questions: string[]
  answers: string[]
  tag_id?: number
  tag_name?: string
  is_enabled?: boolean
  is_recommended?: boolean
  answer_strategy?: 'all' | 'random'
}

export type FAQViewMode = 'grid' | 'list'
export type FAQStatusAction = 'enable' | 'disable' | null

export interface FAQTagItem {
  id: string
  seq_id: number
  name: string
  color?: string
  sort_order?: number
}

export interface FAQKnowledgeBaseInfo {
  id: string
  name: string
  type?: string
  creator_id?: string
  my_permission?: string
}

export interface FAQKnowledgeSwitchOption {
  id: string
  name: string
  type?: string
}

export interface FAQSelectOption {
  label: string
  value: number
}

export interface FAQDropdownItem {
  content: string
  value: string
}

export interface FAQMessageApi {
  success: (msg: string) => unknown
  error: (msg: string) => unknown
  warning: (msg: string) => unknown
}

export interface FAQStorageLike {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
  removeItem?: (key: string) => void
}

export interface FAQTimersLike {
  setTimeout: typeof setTimeout
  clearTimeout: typeof clearTimeout
  requestAnimationFrame?: (cb: FrameRequestCallback) => number
}

export interface FAQEntriesApiDeps {
  listFAQEntries: (
    kbId: string,
    params?: {
      page?: number
      page_size?: number
      tag_id?: number
      tag_ids?: string
      keyword?: string
      is_enabled?: boolean
    },
  ) => Promise<unknown>
  createFAQEntry: (kbId: string, data: FAQEntryPayload) => Promise<unknown>
  updateFAQEntry: (kbId: string, entryId: number, data: FAQEntryPayload) => Promise<unknown>
  updateFAQEntryFieldsBatch: (
    kbId: string,
    data: { by_id: Record<number, { is_enabled?: boolean }> },
  ) => Promise<unknown>
  deleteFAQEntries: (kbId: string, ids: number[]) => Promise<unknown>
  listKnowledgeTags: (
    kbId: string,
    params?: { page?: number; page_size?: number; keyword?: string },
  ) => Promise<unknown>
  updateFAQEntryTagBatch: (
    kbId: string,
    data: { updates: Record<number, number | null> },
  ) => Promise<unknown>
}

export interface UseFAQEntriesOptions {
  getKbId: () => string
  canEdit: ComputedRef<boolean>
  canManage: ComputedRef<boolean>
  t: (key: string, params?: Record<string, unknown>) => string
  message: FAQMessageApi
  confirmDelete: (opts: { body: string; onConfirm: () => Promise<void> | void }) => void
  uiStore: {
    clearSelectedTagIds: () => void
    toggleSelectedTagId?: (id: string) => void
  }
  api: FAQEntriesApiDeps
  storage?: FAQStorageLike
  timers?: FAQTimersLike
}

const FAQ_VIEW_MODE_KEY = 'genyee.kb.faq.viewMode'
const PAGE_SIZE = 20
const TAG_PAGE_SIZE = 20

export function unwrapResponseData<T>(raw: unknown): T | undefined {
  if (raw && typeof raw === 'object' && 'data' in raw) {
    return (raw as { data?: T }).data
  }
  return raw as T | undefined
}

export function extractErrorMessage(err: unknown): string | undefined {
  if (err && typeof err === 'object' && 'message' in err) {
    const msg = (err as { message?: unknown }).message
    if (typeof msg === 'string' && msg.trim()) return msg
  }
  return undefined
}

export function formatFAQEntryDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

export function useFAQEntries(options: UseFAQEntriesOptions) {
  const { getKbId, canEdit, canManage, t, message, confirmDelete, uiStore, api } = options
  const timers: FAQTimersLike = options.timers ?? {
    setTimeout,
    clearTimeout,
    requestAnimationFrame: typeof requestAnimationFrame === 'function' ? requestAnimationFrame : undefined,
  }
  const scheduleFrame = (cb: () => void) => {
    if (timers.requestAnimationFrame) {
      timers.requestAnimationFrame(() => cb())
    } else {
      timers.setTimeout(cb, 16)
    }
  }

  const initViewMode = (): FAQViewMode => {
    try {
      return options.storage?.getItem(FAQ_VIEW_MODE_KEY) === 'list' ? 'list' : 'grid'
    } catch {
      return 'grid'
    }
  }

  const loading = ref(true)
  const loadingMore = ref(false)
  const entries = ref<FAQEntry[]>([])
  const viewMode = ref<FAQViewMode>(initViewMode())

  const canSelectEntries = computed(() => canEdit.value || canManage.value)

  const entryStatusLoading = reactive<Record<number, boolean>>({})
  const selectedRowKeys = ref<number[]>([])
  const batchDeleteLoading = ref(false)
  const batchTagLoading = ref(false)
  const batchStatusAction = ref<FAQStatusAction>(null)

  const selectedEntries = computed(() => {
    const selectedIds = new Set(selectedRowKeys.value)
    return entries.value.filter((entry) => selectedIds.has(entry.id))
  })
  const selectedEnabledCount = computed(() =>
    selectedEntries.value.filter((entry) => entry.is_enabled !== false).length,
  )
  const selectedDisabledCount = computed(
    () => selectedEntries.value.length - selectedEnabledCount.value,
  )
  const batchActionLoading = computed(
    () => batchDeleteLoading.value || batchTagLoading.value || batchStatusAction.value != null,
  )

  const scrollContainer = ref<HTMLElement | null>(null)
  const cardListRef = ref<HTMLElement | null>(null)
  const loadMoreSentinelRef = ref<HTMLElement | null>(null)
  const hasMore = ref(true)
  let currentPage = 1
  const entrySearchKeyword = ref('')
  let entrySearchDebounce: ReturnType<typeof setTimeout> | null = null

  const tagList = ref<FAQTagItem[]>([])
  const tagLoading = ref(false)
  const selectedTagIds = ref<string[]>([])
  const tagFilterCleared = ref(false)
  const tagManageDrawerVisible = ref(false)
  const overallFAQTotal = ref(0)
  const tagSearchQuery = ref('')
  const tagPage = ref(1)
  const tagHasMore = ref(false)
  const tagLoadingMore = ref(false)
  const tagTotal = ref(0)
  let tagSearchDebounce: ReturnType<typeof setTimeout> | null = null

  const tagMapBySeqId = computed<Record<number, FAQTagItem>>(() => {
    const map: Record<number, FAQTagItem> = {}
    tagList.value.forEach((tag) => {
      map[tag.seq_id] = tag
    })
    return map
  })

  const tagDropdownOptions = computed<FAQDropdownItem[]>(() =>
    tagList.value.map((tag) => ({ content: tag.name, value: String(tag.seq_id) })),
  )
  const tagSelectOptions = computed<FAQSelectOption[]>(() =>
    tagList.value.map((tag) => ({ label: tag.name, value: tag.seq_id })),
  )

  const getTagName = (tagId?: number): string => {
    if (!tagId) return t('knowledgeBase.untagged')
    return tagMapBySeqId.value[tagId]?.name || t('knowledgeBase.untagged')
  }

  const editorVisible = ref(false)
  const editorMode = ref<'create' | 'edit'>('create')
  const currentEntryId = ref<number | null>(null)
  const editorForm = reactive<FAQEntryPayload>({
    standard_question: '',
    similar_questions: [],
    negative_questions: [],
    answers: [],
    tag_id: undefined,
  })
  const editorFormRef: Ref<FormInstanceFunctions | undefined> = ref()
  const savingEntry = ref(false)

  const answerInput = ref('')
  const similarInput = ref('')
  const negativeInput = ref('')

  const faqAnswerSelection = computed(() => ({
    field: t('knowledgeEditor.faq.answers'),
    text: JSON.stringify(
      {
        standard_question: editorForm.standard_question,
        draft_answer: answerInput.value,
        existing_answers: editorForm.answers,
      },
      null,
      2,
    ),
  }))

  const faqAssistantSelection = computed(() => ({
    field: t('knowledgeEditor.faq.similarQuestions'),
    text: JSON.stringify(
      {
        standard_question: editorForm.standard_question,
        existing_similar_questions: [...editorForm.similar_questions, similarInput.value].filter(Boolean),
        negative_questions: [...editorForm.negative_questions, negativeInput.value].filter(Boolean),
        answers: [...editorForm.answers, answerInput.value].filter(Boolean),
      },
      null,
      2,
    ),
  }))

  const editorRules: FormRules<FAQEntryPayload> = {
    standard_question: [{ required: true, message: t('knowledgeEditor.messages.nameRequired') }],
    answers: [
      {
        validator: (val: string[]) => Array.isArray(val) && val.length > 0,
        message: t('knowledgeEditor.faq.answerRequired'),
      },
    ],
  }

  let arrangeCardsTimer: ReturnType<typeof setTimeout> | null = null
  let cardTransitionCleanupTimer: ReturnType<typeof setTimeout> | null = null

  const arrangeCards = () => {
    if (viewMode.value !== 'grid' || !cardListRef.value) return
    const container = cardListRef.value
    const cards = container.querySelectorAll<HTMLElement>('.faq-card')
    if (cards.length === 0) return

    const containerWidth = container.offsetWidth
    const gap = 12
    let columnCount = 1
    if (containerWidth >= 2560) columnCount = 12
    else if (containerWidth >= 1920) columnCount = 10
    else if (containerWidth >= 1536) columnCount = 8
    else if (containerWidth >= 1280) columnCount = 6
    else if (containerWidth >= 1024) columnCount = 5
    else if (containerWidth >= 768) columnCount = 4
    else if (containerWidth >= 640) columnCount = 3

    const columnWidth = (containerWidth - gap * (columnCount - 1)) / columnCount
    const columnHeights = new Array<number>(columnCount).fill(0)

    scheduleFrame(() => {
      if (viewMode.value !== 'grid' || cardListRef.value !== container) return
      cards.forEach((card) => {
        if (card.style.position !== 'absolute') {
          card.style.position = 'absolute'
        }
        card.style.width = `${columnWidth}px`
      })

      scheduleFrame(() => {
        if (viewMode.value !== 'grid' || cardListRef.value !== container) return
        const cardHeights: number[] = []
        cards.forEach((card) => {
          const height = card.offsetHeight || card.getBoundingClientRect().height
          cardHeights.push(height)
        })

        const newPositions: Array<{ top: number; left: number }> = []
        cardHeights.forEach((height) => {
          const shortestColumnIndex = columnHeights.indexOf(Math.min(...columnHeights))
          const top = columnHeights[shortestColumnIndex] ?? 0
          const left = shortestColumnIndex * (columnWidth + gap)
          newPositions.push({ top, left })
          columnHeights[shortestColumnIndex] = top + height + gap
        })

        cards.forEach((card, index) => {
          const pos = newPositions[index]
          if (!pos) return
          const currentTop = Number.parseFloat(card.style.top) || 0
          const currentLeft = Number.parseFloat(card.style.left) || 0
          if (Math.abs(currentTop - pos.top) > 1 || Math.abs(currentLeft - pos.left) > 1) {
            card.style.willChange = 'top, left'
            card.style.transition =
              'top var(--gy-motion-slow) cubic-bezier(0.4, 0, 0.2, 1), left var(--gy-motion-slow) cubic-bezier(0.4, 0, 0.2, 1)'
          }
          card.style.position = 'absolute'
          card.style.top = `${pos.top}px`
          card.style.left = `${pos.left}px`
          card.style.width = `${columnWidth}px`
        })

        const maxHeight = Math.max(...columnHeights)
        container.style.height = `${maxHeight}px`
        container.style.position = 'relative'

        if (cardTransitionCleanupTimer) timers.clearTimeout(cardTransitionCleanupTimer)
        cardTransitionCleanupTimer = timers.setTimeout(() => {
          cardTransitionCleanupTimer = null
          cards.forEach((card) => {
            card.style.transition = ''
            card.style.willChange = ''
          })
        }, 300)
      })
    })
  }

  const debounceArrangeCards = (delay = 100) => {
    if (arrangeCardsTimer) timers.clearTimeout(arrangeCardsTimer)
    arrangeCardsTimer = timers.setTimeout(() => {
      arrangeCardsTimer = null
      arrangeCards()
    }, delay)
  }

  const sentinelInViewport = (): boolean => {
    const sentinel = loadMoreSentinelRef.value
    const root = scrollContainer.value
    if (!sentinel || !root || typeof sentinel.getBoundingClientRect !== 'function') return false
    const sRect = sentinel.getBoundingClientRect()
    const rRect = root.getBoundingClientRect()
    return sRect.top <= rRect.bottom + 200
  }

  const loadEntries = async (append = false) => {
    const kbId = getKbId()
    if (!kbId) return
    if (append) {
      if (loadingMore.value || loading.value || !hasMore.value) return
      loadingMore.value = true
    } else {
      loading.value = true
      currentPage = 1
      entries.value = []
      selectedRowKeys.value = []
      Object.keys(entryStatusLoading).forEach((key) => {
        delete entryStatusLoading[Number(key)]
      })
    }

    try {
      if (overallFAQTotal.value === 0 && !append) {
        const totalRes = await api.listFAQEntries(kbId, { page: 1, page_size: 1 })
        const totalData = unwrapResponseData<{ total?: number }>(totalRes)
        overallFAQTotal.value = totalData?.total || 0
      }

      const res = await api.listFAQEntries(kbId, {
        page: currentPage,
        page_size: PAGE_SIZE,
        tag_ids: selectedTagIds.value.length > 0 ? selectedTagIds.value.join(',') : undefined,
        keyword: entrySearchKeyword.value ? entrySearchKeyword.value.trim() : undefined,
      })
      const pageData = unwrapResponseData<{ data?: FAQEntry[]; total?: number }>(res)
      const newEntries = (pageData?.data || []).map((entry) => ({
        ...entry,
        showMore: false,
        similarCollapsed: true,
        negativeCollapsed: true,
        answersCollapsed: true,
        is_enabled: entry.is_enabled !== false,
      }))

      entries.value = append ? [...entries.value, ...newEntries] : newEntries
      hasMore.value = entries.value.length < (pageData?.total || 0)
      currentPage++

      await nextTick()
      arrangeCards()
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    } finally {
      loading.value = false
      loadingMore.value = false
    }

    await nextTick()
    if (hasMore.value && !loadingMore.value && sentinelInViewport()) {
      void loadEntries(true)
    }
  }

  // IntersectionObserver-driven infinite scroll (replaces manual scroll listener math)
  let sentinelObserver: IntersectionObserver | null = null
  const disconnectSentinelObserver = () => {
    if (sentinelObserver) {
      sentinelObserver.disconnect()
      sentinelObserver = null
    }
  }

  watch(
    [loadMoreSentinelRef, scrollContainer, hasMore],
    ([sentinel, root, canLoadMore]) => {
      disconnectSentinelObserver()
      if (!sentinel || !canLoadMore || typeof IntersectionObserver !== 'function') return
      sentinelObserver = new IntersectionObserver(
        (items) => {
          for (const item of items) {
            if (item.isIntersecting && hasMore.value && !loading.value && !loadingMore.value) {
              void loadEntries(true)
            }
          }
        },
        { root: root ?? null, rootMargin: '200px' },
      )
      sentinelObserver.observe(sentinel)
    },
    { flush: 'post' },
  )

  // ResizeObserver on scrollContainer / cardListRef for responsive masonry re-layout
  let resizeObserver: ResizeObserver | null = null
  let resizeDebounceTimer: ReturnType<typeof setTimeout> | null = null
  const disconnectResizeObserver = () => {
    if (resizeObserver) {
      resizeObserver.disconnect()
      resizeObserver = null
    }
  }

  watch(
    [scrollContainer, cardListRef],
    ([scroller, grid]) => {
      disconnectResizeObserver()
      if (typeof ResizeObserver !== 'function') return
      const target = grid || scroller
      if (!target) return
      resizeObserver = new ResizeObserver(() => {
        if (resizeDebounceTimer) timers.clearTimeout(resizeDebounceTimer)
        resizeDebounceTimer = timers.setTimeout(() => {
          resizeDebounceTimer = null
          arrangeCards()
          if (hasMore.value && !loading.value && !loadingMore.value && sentinelInViewport()) {
            void loadEntries(true)
          }
        }, 150)
      })
      resizeObserver.observe(target)
    },
    { flush: 'post' },
  )

  const loadTags = async (reset = false) => {
    const kbId = getKbId()
    if (!kbId) {
      tagList.value = []
      tagTotal.value = 0
      tagHasMore.value = false
      tagPage.value = 1
      return
    }

    if (reset) {
      tagPage.value = 1
      tagList.value = []
      tagTotal.value = 0
      tagHasMore.value = false
    } else if (tagLoading.value || tagLoadingMore.value) {
      return
    }

    const currentTagPage = tagPage.value || 1
    tagLoading.value = currentTagPage === 1
    tagLoadingMore.value = currentTagPage > 1

    try {
      const res = await api.listKnowledgeTags(kbId, {
        page: currentTagPage,
        page_size: TAG_PAGE_SIZE,
        keyword: tagSearchQuery.value || undefined,
      })
      const pageData = unwrapResponseData<{
        data?: Array<Omit<FAQTagItem, 'id'> & { id: string | number }>
        total?: number
      }>(res)
      const pageTags: FAQTagItem[] = (pageData?.data || []).map((tag) => ({
        ...tag,
        id: String(tag.id),
      }))

      tagList.value = currentTagPage === 1 ? pageTags : [...tagList.value, ...pageTags]
      tagTotal.value = pageData?.total || tagList.value.length
      tagHasMore.value = tagList.value.length < tagTotal.value
      if (tagHasMore.value) {
        tagPage.value = currentTagPage + 1
      }
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    } finally {
      tagLoading.value = false
      tagLoadingMore.value = false
    }
  }

  const handleTagFilterChange = (tagIds: string[]) => {
    selectedTagIds.value = tagIds
    uiStore.clearSelectedTagIds()
    tagIds.forEach((id) => uiStore.toggleSelectedTagId?.(id))
  }

  const openTagManageDrawer = () => {
    tagManageDrawerVisible.value = true
  }

  const onTagManageChanged = (payload?: { deletedTagId?: string }) => {
    if (!getKbId()) return
    void loadTags(true)
    if (payload?.deletedTagId && selectedTagIds.value.includes(payload.deletedTagId)) {
      selectedTagIds.value = selectedTagIds.value.filter((id) => id !== payload.deletedTagId)
      handleTagFilterChange([...selectedTagIds.value])
    }
    currentPage = 1
    entries.value = []
    selectedRowKeys.value = []
    void loadEntries()
  }

  const handleEntryTagChange = async (entryId: number, value?: string) => {
    const kbId = getKbId()
    if (!kbId) return
    const targetEntry = entries.value.find((item) => item.id === entryId)
    const previousTagId = targetEntry ? targetEntry.tag_id : undefined
    const normalizedValue = value ? Number(value) : null
    if (normalizedValue === previousTagId) return
    try {
      await api.updateFAQEntryTagBatch(kbId, { updates: { [entryId]: normalizedValue } })
      message.success(t('knowledgeEditor.messages.updateSuccess'))
      await loadEntries()
      await loadTags(true)
    } catch (error: unknown) {
      if (targetEntry) {
        targetEntry.tag_id = previousTagId
      }
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    }
  }

  const handleEntryStatusChange = async (entry: FAQEntry, value: boolean) => {
    const kbId = getKbId()
    if (!kbId) return
    const entryIndex = entries.value.findIndex((e) => e.id === entry.id)
    if (entryIndex === -1) return
    const actualEntry = entries.value[entryIndex]
    if (!actualEntry) return
    const previous = actualEntry.is_enabled
    if (previous === value) return
    actualEntry.is_enabled = value
    entryStatusLoading[entry.id] = true
    try {
      await api.updateFAQEntryFieldsBatch(kbId, { by_id: { [entry.id]: { is_enabled: value } } })
      message.success(
        t(value ? 'knowledgeEditor.faq.statusEnableSuccess' : 'knowledgeEditor.faq.statusDisableSuccess'),
      )
    } catch (error: unknown) {
      actualEntry.is_enabled = previous
      message.error(extractErrorMessage(error) || t('knowledgeEditor.faq.statusUpdateFailed'))
    } finally {
      entryStatusLoading[entry.id] = false
    }
  }

  const handleCardSelect = (entryId: number, checked: boolean) => {
    if (!canSelectEntries.value || batchActionLoading.value) return
    if (checked) {
      if (!selectedRowKeys.value.includes(entryId)) {
        selectedRowKeys.value.push(entryId)
      }
    } else {
      const index = selectedRowKeys.value.indexOf(entryId)
      if (index > -1) {
        selectedRowKeys.value.splice(index, 1)
      }
    }
  }

  const clearFAQSelection = () => {
    if (batchActionLoading.value) return
    selectedRowKeys.value = []
  }

  const resetEditorForm = () => {
    editorForm.standard_question = ''
    editorForm.similar_questions = []
    editorForm.negative_questions = []
    editorForm.answers = []
    editorForm.tag_id = undefined
    answerInput.value = ''
    similarInput.value = ''
    negativeInput.value = ''
  }

  const openEditor = (entry?: FAQEntry) => {
    if (entry) {
      editorMode.value = 'edit'
      currentEntryId.value = entry.id
      editorForm.standard_question = entry.standard_question
      editorForm.similar_questions = [...(entry.similar_questions || [])]
      editorForm.negative_questions = [...(entry.negative_questions || [])]
      editorForm.answers = [...(entry.answers || [])]
      editorForm.tag_id = entry.tag_id || undefined
    } else {
      editorMode.value = 'create'
      currentEntryId.value = null
      resetEditorForm()
    }
    answerInput.value = ''
    similarInput.value = ''
    negativeInput.value = ''
    editorVisible.value = true
  }

  const handleEditorClose = () => {
    resetEditorForm()
    editorFormRef.value?.clearValidate?.()
  }

  const addAnswer = () => {
    const trimmed = answerInput.value.trim()
    if (trimmed && editorForm.answers.length < 5 && !editorForm.answers.includes(trimmed)) {
      editorForm.answers.push(trimmed)
      answerInput.value = ''
    }
  }

  const removeAnswer = (index: number) => {
    editorForm.answers.splice(index, 1)
  }

  const addSimilar = () => {
    const trimmed = similarInput.value.trim()
    if (trimmed && editorForm.similar_questions.length < 10 && !editorForm.similar_questions.includes(trimmed)) {
      editorForm.similar_questions.push(trimmed)
      similarInput.value = ''
    }
  }

  const removeSimilar = (index: number) => {
    editorForm.similar_questions.splice(index, 1)
  }

  const addNegative = () => {
    const trimmed = negativeInput.value.trim()
    if (trimmed && editorForm.negative_questions.length < 10 && !editorForm.negative_questions.includes(trimmed)) {
      editorForm.negative_questions.push(trimmed)
      negativeInput.value = ''
    }
  }

  const removeNegative = (index: number) => {
    editorForm.negative_questions.splice(index, 1)
  }

  const handleSubmitEntry = async () => {
    if (!editorFormRef.value) return
    const result = await editorFormRef.value.validate?.()
    if (result !== true) return

    const kbId = getKbId()
    if (!kbId) return
    savingEntry.value = true
    try {
      const payload: FAQEntryPayload = {
        standard_question: editorForm.standard_question,
        similar_questions: [...editorForm.similar_questions],
        negative_questions: [...editorForm.negative_questions],
        answers: [...editorForm.answers],
        tag_id: editorForm.tag_id || undefined,
      }
      if (editorMode.value === 'create') {
        await api.createFAQEntry(kbId, payload)
        message.success(t('knowledgeEditor.messages.createSuccess'))
      } else if (currentEntryId.value) {
        await api.updateFAQEntry(kbId, currentEntryId.value, payload)
        message.success(t('knowledgeEditor.messages.updateSuccess'))
      }
      editorVisible.value = false
      await loadEntries()
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    } finally {
      savingEntry.value = false
    }
  }

  const handleBatchDelete = async () => {
    const kbId = getKbId()
    if (!canManage.value || !selectedRowKeys.value.length || !kbId || batchActionLoading.value) return
    const selectedIds = [...selectedRowKeys.value]
    batchDeleteLoading.value = true
    try {
      await api.deleteFAQEntries(kbId, selectedIds)
      message.success(t('knowledgeEditor.faq.batchDeleteSuccess', { count: selectedIds.length }))
      selectedRowKeys.value = []
      await loadEntries()
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    } finally {
      batchDeleteLoading.value = false
    }
  }

  const batchTagDialogVisible = ref(false)
  const batchTagValue = ref<number | undefined>(undefined)

  const openBatchTagDialog = () => {
    if (!canEdit.value || !selectedRowKeys.value.length || batchActionLoading.value) return
    batchTagValue.value = undefined
    batchTagDialogVisible.value = true
  }

  const handleBatchTag = async () => {
    const kbId = getKbId()
    if (!canEdit.value || !selectedRowKeys.value.length || !kbId || batchActionLoading.value) return
    const selectedIds = [...selectedRowKeys.value]
    batchTagLoading.value = true
    try {
      const updates: Record<number, number | null> = {}
      selectedIds.forEach((id) => {
        updates[id] = batchTagValue.value != null ? Number(batchTagValue.value) : null
      })
      await api.updateFAQEntryTagBatch(kbId, { updates })
      message.success(t('knowledgeEditor.messages.updateSuccess'))
      batchTagDialogVisible.value = false
      selectedRowKeys.value = []
      await loadEntries()
      await loadTags(true)
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    } finally {
      batchTagLoading.value = false
    }
  }

  const handleBatchStatusChange = async (isEnabled: boolean) => {
    const kbId = getKbId()
    if (!canEdit.value || !selectedRowKeys.value.length || !kbId || batchActionLoading.value) return
    const selectedIds = [...selectedRowKeys.value]
    batchStatusAction.value = isEnabled ? 'enable' : 'disable'
    try {
      const by_id: Record<number, { is_enabled: boolean }> = {}
      selectedIds.forEach((id) => {
        by_id[id] = { is_enabled: isEnabled }
      })
      await api.updateFAQEntryFieldsBatch(kbId, { by_id })
      message.success(
        t(isEnabled ? 'knowledgeEditor.faq.statusEnableSuccess' : 'knowledgeEditor.faq.statusDisableSuccess'),
      )
      selectedRowKeys.value = []
      await loadEntries()
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
    } finally {
      batchStatusAction.value = null
    }
  }

  const handleMenuEdit = (entry: FAQEntry) => {
    entry.showMore = false
    openEditor(entry)
  }

  const handleMenuDelete = (entry: FAQEntry) => {
    entry.showMore = false
    confirmDelete({
      body: t('knowledgeEditor.faq.confirmDelete'),
      onConfirm: async () => {
        const kbId = getKbId()
        if (!kbId) return
        try {
          await api.deleteFAQEntries(kbId, [entry.id])
          message.success(t('knowledgeEditor.faqImport.deleteSuccess'))
          await loadEntries()
        } catch (error: unknown) {
          message.error(extractErrorMessage(error) || t('common.operationFailed'))
        }
      },
    })
  }

  const resetFiltersAfterImport = () => {
    selectedTagIds.value = []
    tagFilterCleared.value = false
    uiStore.clearSelectedTagIds()
    entrySearchKeyword.value = ''
    overallFAQTotal.value = 0
  }

  const resetForKbSwitch = () => {
    editorVisible.value = false
    batchTagDialogVisible.value = false
    entries.value = []
    selectedRowKeys.value = []
    if (tagSearchDebounce) {
      timers.clearTimeout(tagSearchDebounce)
      tagSearchDebounce = null
    }
    if (entrySearchDebounce) {
      timers.clearTimeout(entrySearchDebounce)
      entrySearchDebounce = null
    }
    currentPage = 1
    hasMore.value = true
    selectedTagIds.value = []
    tagFilterCleared.value = false
    uiStore.clearSelectedTagIds()
    overallFAQTotal.value = 0
    tagSearchQuery.value = ''
  }

  watch(selectedTagIds, (newVal, oldVal) => {
    if (oldVal === undefined) return
    if (newVal.join(',') !== oldVal.join(',')) {
      currentPage = 1
      entries.value = []
      selectedRowKeys.value = []
      void loadEntries()
    }
  })

  watch(tagSearchQuery, (newVal, oldVal) => {
    if (newVal === oldVal) return
    if (tagSearchDebounce) timers.clearTimeout(tagSearchDebounce)
    tagSearchDebounce = timers.setTimeout(() => {
      tagSearchDebounce = null
      void loadTags(true)
    }, 300)
  })

  watch(entrySearchKeyword, (newVal, oldVal) => {
    if (newVal === oldVal) return
    if (entrySearchDebounce) timers.clearTimeout(entrySearchDebounce)
    entrySearchDebounce = timers.setTimeout(() => {
      entrySearchDebounce = null
      void loadEntries()
    }, 300)
  })

  watch(viewMode, async (mode) => {
    try {
      options.storage?.setItem(FAQ_VIEW_MODE_KEY, mode)
    } catch {
      // Storage unavailable
    }
    entries.value.forEach((entry) => {
      entry.showMore = false
    })
    await nextTick()
    scrollContainer.value?.scrollTo?.({ top: 0 })
    arrangeCards()
    if (hasMore.value && !loading.value && !loadingMore.value && sentinelInViewport()) {
      void loadEntries(true)
    }
  })

  watch(
    () => entries.value.length,
    () => {
      nextTick(() => {
        arrangeCards()
      })
    },
  )

  watch(
    () =>
      entries.value.map((e) => ({
        id: e.id,
        similarCollapsed: e.similarCollapsed,
        negativeCollapsed: e.negativeCollapsed,
        answersCollapsed: e.answersCollapsed,
      })),
    () => {
      nextTick(() => {
        scheduleFrame(() => {
          scheduleFrame(() => {
            debounceArrangeCards(250)
          })
        })
      })
    },
    { deep: true },
  )

  const disposeEntries = () => {
    disconnectSentinelObserver()
    disconnectResizeObserver()
    if (tagSearchDebounce) {
      timers.clearTimeout(tagSearchDebounce)
      tagSearchDebounce = null
    }
    if (entrySearchDebounce) {
      timers.clearTimeout(entrySearchDebounce)
      entrySearchDebounce = null
    }
    if (arrangeCardsTimer) {
      timers.clearTimeout(arrangeCardsTimer)
      arrangeCardsTimer = null
    }
    if (cardTransitionCleanupTimer) {
      timers.clearTimeout(cardTransitionCleanupTimer)
      cardTransitionCleanupTimer = null
    }
    if (resizeDebounceTimer) {
      timers.clearTimeout(resizeDebounceTimer)
      resizeDebounceTimer = null
    }
  }

  onUnmounted(disposeEntries)

  return {
    loading,
    loadingMore,
    entries,
    viewMode,
    canSelectEntries,
    entryStatusLoading,
    selectedRowKeys,
    batchDeleteLoading,
    batchTagLoading,
    batchStatusAction,
    selectedEnabledCount,
    selectedDisabledCount,
    batchActionLoading,
    scrollContainer,
    cardListRef,
    loadMoreSentinelRef,
    hasMore,
    entrySearchKeyword,
    tagList,
    tagLoading,
    selectedTagIds,
    tagFilterCleared,
    tagManageDrawerVisible,
    overallFAQTotal,
    tagSearchQuery,
    tagHasMore,
    tagLoadingMore,
    tagTotal,
    tagDropdownOptions,
    tagSelectOptions,
    getTagName,
    loadEntries,
    loadTags,
    handleTagFilterChange,
    openTagManageDrawer,
    onTagManageChanged,
    handleEntryTagChange,
    handleEntryStatusChange,
    handleCardSelect,
    clearFAQSelection,
    editorVisible,
    editorMode,
    editorForm,
    editorFormRef,
    savingEntry,
    answerInput,
    similarInput,
    negativeInput,
    faqAnswerSelection,
    faqAssistantSelection,
    editorRules,
    openEditor,
    handleEditorClose,
    addAnswer,
    removeAnswer,
    addSimilar,
    removeSimilar,
    addNegative,
    removeNegative,
    handleSubmitEntry,
    handleBatchDelete,
    batchTagDialogVisible,
    batchTagValue,
    openBatchTagDialog,
    handleBatchTag,
    handleBatchStatusChange,
    handleMenuEdit,
    handleMenuDelete,
    resetFiltersAfterImport,
    resetForKbSwitch,
    arrangeCards,
    disposeEntries,
  }
}
