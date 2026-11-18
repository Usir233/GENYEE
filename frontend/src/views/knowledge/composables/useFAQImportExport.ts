import { computed, onUnmounted, reactive, ref } from 'vue'
import {
  extractErrorMessage,
  unwrapResponseData,
  type FAQDropdownItem,
  type FAQEntryPayload,
  type FAQMessageApi,
  type FAQStorageLike,
} from './useFAQEntries'

export interface FAQImportTaskStatus {
  status: string
  progress: number
  total: number
  processed: number
  message?: string
  error?: string
}

export interface FAQImportFileLike {
  name: string
  size?: number
  text: () => Promise<string>
  arrayBuffer: () => Promise<ArrayBuffer>
}

export interface FAQImportState {
  mode: 'append' | 'replace'
  file: FAQImportFileLike | null
  preview: FAQEntryPayload[]
  importing: boolean
  taskId: string | null
  taskStatus: FAQImportTaskStatus | null
  pollingInterval: ReturnType<typeof setInterval> | null
}

export interface FAQImportSuccessEntry {
  index: number
  seq_id: number
  tag_id?: number
  tag_name?: string
  standard_question: string
}

export interface FAQImportResultView {
  total_entries: number
  success_count: number
  failed_count: number
  skipped_count: number
  partial_failed_count: number
  merged_count: number
  added_count: number
  import_mode: string
  imported_at: string
  task_id: string
  processing_time: number
  message?: string
  failed_entries_url?: string
  success_entries?: FAQImportSuccessEntry[]
  display_status: string
}

export interface FAQImportProgressDTO {
  status?: string
  progress?: number
  total?: number
  processed?: number
  error?: string
  message?: string
  display_status?: string
  success_count?: number
  failed_count?: number
  skipped_count?: number
  partial_failed_count?: number
  merged_count?: number
  added_count?: number
  import_mode?: string
  imported_at?: string
  task_id?: string
  failed_entries_url?: string
  success_entries?: FAQImportSuccessEntry[]
  processing_time?: number
}

export interface PapaParseLike {
  parse: (
    input: string,
    config: {
      header: boolean
      skipEmptyLines: boolean
      delimiter: string
      quoteChar: string
      escapeChar: string
      transformHeader: (header: string) => string
      complete: (results: { data: unknown[] }) => void
      error: (error: Error) => void
    },
  ) => void
}

export interface XLSXLike {
  read: (data: ArrayBuffer, opts: { type: 'array' }) => {
    SheetNames: string[]
    Sheets: Record<string, unknown>
  }
  utils: {
    sheet_to_json: <T = Record<string, string>>(
      worksheet: unknown,
      opts: { defval: string; raw: boolean },
    ) => T[]
    json_to_sheet: (rows: Array<Record<string, string>>) => unknown
    book_new: () => unknown
    book_append_sheet: (workbook: unknown, worksheet: unknown, name: string) => void
  }
  writeFile: (workbook: unknown, filename: string) => void
}

export interface FAQImportExportApiDeps {
  upsertFAQEntries: (
    kbId: string,
    data: { entries: FAQEntryPayload[]; mode: 'append' | 'replace' },
  ) => Promise<unknown>
  getFAQImportProgress: (taskId: string) => Promise<unknown>
  updateFAQImportResultDisplayStatus: (
    kbId: string,
    displayStatus: 'open' | 'close',
  ) => Promise<unknown>
  exportFAQEntries: (kbId: string, format?: 'csv' | 'json') => Promise<Blob>
}

export interface FAQWindowLike {
  location?: { origin?: string }
  open?: (url: string, target?: string, features?: string) => unknown
}

export interface UseFAQImportExportOptions {
  getKbId: () => string
  getGeneration: () => number
  isUnmounted: () => boolean
  t: (key: string, params?: Record<string, unknown>) => string
  message: FAQMessageApi
  papa: PapaParseLike
  xlsx: XLSXLike
  api: FAQImportExportApiDeps
  storage?: FAQStorageLike
  timers?: {
    setInterval: typeof setInterval
    clearInterval: typeof clearInterval
    setTimeout: typeof setTimeout
    clearTimeout: typeof clearTimeout
  }
  getWindow?: () => FAQWindowLike | undefined
  onRefreshData: () => Promise<void>
  onImportCompleted: () => Promise<void>
}

const MAX_FAQ_IMPORT_FILE_BYTES = 20 * 1024 * 1024
const PARENTHETICAL_NOTE_RE = /\([^)]*\)|\uff08[^\uff09]*\uff09/g

function extractResponseStatus(err: unknown): number | undefined {
  if (!err || typeof err !== 'object') return undefined
  if ('status' in err && typeof (err as { status?: unknown }).status === 'number') {
    return (err as { status: number }).status
  }
  if ('response' in err) {
    const response = (err as { response?: unknown }).response
    if (response && typeof response === 'object' && 'status' in response) {
      const status = (response as { status?: unknown }).status
      if (typeof status === 'number') return status
    }
  }
  return undefined
}

export function cleanImportHeader(header: string): string {
  return header.trim().replace(PARENTHETICAL_NOTE_RE, '').trim().toLowerCase()
}

export function splitFAQByDelimiter(value?: string): string[] {
  if (!value) return []
  const trimmedValue = value.trim()
  if (!trimmedValue) return []
  if (trimmedValue.includes('##')) {
    return trimmedValue
      .split('##')
      .map((item) => item.trim())
      .filter(Boolean)
  }
  return [trimmedValue]
}

export function normalizePositiveTagId(val: unknown): number | undefined {
  const num =
    typeof val === 'number'
      ? val
      : typeof val === 'string' && val.trim()
        ? Number(val.trim())
        : Number.NaN
  return Number.isSafeInteger(num) && num > 0 ? num : undefined
}

export function useFAQImportExport(options: UseFAQImportExportOptions) {
  const { getKbId, getGeneration, isUnmounted, t, message, papa, xlsx, api } = options
  const timers = options.timers ?? {
    setInterval,
    clearInterval,
    setTimeout,
    clearTimeout,
  }

  const importVisible = ref(false)
  const importState = reactive<FAQImportState>({
    mode: 'append',
    file: null,
    preview: [],
    importing: false,
    taskId: null,
    taskStatus: null,
    pollingInterval: null,
  })

  const importResult = ref<FAQImportResultView | null>(null)
  const importResultExpanded = ref(false)
  const exportLoading = ref(false)
  let progressCloseTimer: ReturnType<typeof setTimeout> | null = null

  const showImportResultBadge = computed(
    () =>
      !!importResult.value &&
      importResult.value.display_status === 'open' &&
      !importState.taskId,
  )

  const isImportInProgress = computed(() => {
    const status = importState.taskStatus?.status
    return !!importState.taskId && (status === 'running' || status === 'pending')
  })

  const importResultSummary = computed(() => {
    const result = importResult.value
    if (!result) return ''
    if (result.message?.trim()) {
      return result.message.trim()
    }
    const parts: string[] = [`${t('faqManager.import.totalData')} ${result.total_entries}`]
    if (result.merged_count > 0) {
      if (result.added_count > 0) {
        parts.push(`${t('faqManager.import.added')} ${result.added_count}`)
      }
      parts.push(`${t('faqManager.import.merged')} ${result.merged_count}`)
    } else if (result.success_count > 0) {
      parts.push(`${t('faqManager.import.success')} ${result.success_count}`)
    }
    if (result.partial_failed_count > 0) {
      parts.push(`${t('faqManager.import.partialFailed')} ${result.partial_failed_count}`)
    }
    if (result.failed_count > 0) {
      parts.push(`${t('faqManager.import.failed')} ${result.failed_count}`)
    }
    if (result.skipped_count > 0) {
      parts.push(`${t('faqManager.import.skipped')} ${result.skipped_count}`)
    }
    return parts.join(' · ')
  })

  const importProgressTitle = computed(() => {
    const status = importState.taskStatus?.status
    if (status === 'running') return t('faqManager.import.importing')
    if (status === 'success') return t('faqManager.import.importDone')
    if (status === 'failed') return t('faqManager.import.importFailed')
    return t('faqManager.import.waiting')
  })

  const importProgressIcon = computed(() => {
    const status = importState.taskStatus?.status
    if (status === 'running') return 'loading'
    if (status === 'success') return 'check-circle-filled'
    if (status === 'failed') return 'error-circle-filled'
    return 'time-filled'
  })

  const importProgressText = computed(() => {
    const status = importState.taskStatus
    if (!status) return ''
    if (status.error) return status.error
    if (status.message?.trim()) return status.message.trim()
    return importProgressTitle.value
  })

  const resolveHeaderAliases = (i18nKey: string, defaults: readonly string[]): string[] => {
    const set = new Set<string>(defaults.map((item) => item.toLowerCase()))
    const translated = t(i18nKey)
    if (translated && translated !== i18nKey) {
      translated
        .split(',')
        .map((part) => cleanImportHeader(part))
        .filter(Boolean)
        .forEach((part) => set.add(part))
    }
    return [...set]
  }

  const pickRecordField = (record: Record<string, string>, aliases: readonly string[]): string => {
    for (const alias of aliases) {
      if (Object.prototype.hasOwnProperty.call(record, alias)) {
        const val = record[alias]
        if (val) return val
      }
    }
    return ''
  }

  const parseBooleanField = (
    value: string | undefined,
    fallbackValue?: boolean,
  ): boolean | undefined => {
    if (!value) return undefined
    const normalized = value.trim().toUpperCase()
    if (!normalized) return undefined

    const trueSet = new Set<string>(['TRUE', '1', 'YES'])
    const rawTrue = t('knowledgeEditor.faqImport.booleanTrueAliases')
    if (rawTrue && rawTrue !== 'knowledgeEditor.faqImport.booleanTrueAliases') {
      rawTrue
        .split(',')
        .map((s) => s.trim().toUpperCase())
        .filter(Boolean)
        .forEach((s) => trueSet.add(s))
    }
    if (trueSet.has(normalized)) return true

    const falseSet = new Set<string>(['FALSE', '0', 'NO'])
    const rawFalse = t('knowledgeEditor.faqImport.booleanFalseAliases')
    if (rawFalse && rawFalse !== 'knowledgeEditor.faqImport.booleanFalseAliases') {
      rawFalse
        .split(',')
        .map((s) => s.trim().toUpperCase())
        .filter(Boolean)
        .forEach((s) => falseSet.add(s))
    }
    if (falseSet.has(normalized)) return false

    return fallbackValue
  }

  const normalizeStringList = (val: unknown): string[] => {
    if (Array.isArray(val)) {
      return val
        .filter((item): item is string => typeof item === 'string')
        .map((item) => item.trim())
        .filter(Boolean)
    }
    if (typeof val === 'string') {
      return splitFAQByDelimiter(val)
    }
    return []
  }

  const normalizeOptionalBoolean = (val: unknown): boolean | undefined => {
    if (typeof val === 'boolean') return val
    if (typeof val === 'string') return parseBooleanField(val, undefined)
    return undefined
  }

  const normalizePayload = (payload: Partial<Record<keyof FAQEntryPayload, unknown>>): FAQEntryPayload => ({
    standard_question:
      typeof payload.standard_question === 'string' ? payload.standard_question.trim() : '',
    answers: normalizeStringList(payload.answers),
    similar_questions: normalizeStringList(payload.similar_questions),
    negative_questions: normalizeStringList(payload.negative_questions),
    tag_id: normalizePositiveTagId(payload.tag_id),
    tag_name: typeof payload.tag_name === 'string' ? payload.tag_name.trim() : '',
    is_enabled: normalizeOptionalBoolean(payload.is_enabled),
    answer_strategy:
      payload.answer_strategy === 'all' || payload.answer_strategy === 'random'
        ? payload.answer_strategy
        : undefined,
    is_recommended: normalizeOptionalBoolean(payload.is_recommended),
  })

  const buildRowPayload = (record: Record<string, string>): FAQEntryPayload => {
    const disabledAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.disabled',
      ['disabled', 'is_disabled'],
    )
    const replyAllAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.replyAll',
      ['reply all', 'answer_all'],
    )
    const notRecommendedAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.notRecommended',
      ['exclude from recommendations', 'not_recommended'],
    )
    const questionAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.question',
      ['question', 'standard_question'],
    )
    const answersAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.answers',
      ['bot answers', 'answers'],
    )
    const similarAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.similarQuestions',
      ['similar questions', 'similar_questions'],
    )
    const negativeAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.negativeQuestions',
      ['negative questions', 'negative_questions'],
    )
    const tagAliases = resolveHeaderAliases(
      'knowledgeEditor.faqImport.importHeaderAliases.tag',
      ['tag', 'category', 'tag_name'],
    )

    const isDisabled = parseBooleanField(pickRecordField(record, disabledAliases), false)
    const answerAll = parseBooleanField(pickRecordField(record, replyAllAliases), false)
    const notRecommended = parseBooleanField(pickRecordField(record, notRecommendedAliases), false)

    return normalizePayload({
      standard_question: pickRecordField(record, questionAliases),
      answers: splitFAQByDelimiter(pickRecordField(record, answersAliases)),
      similar_questions: splitFAQByDelimiter(pickRecordField(record, similarAliases)),
      negative_questions: splitFAQByDelimiter(pickRecordField(record, negativeAliases)),
      tag_id: normalizePositiveTagId(
        Object.prototype.hasOwnProperty.call(record, 'tag_id') ? record.tag_id : undefined,
      ),
      tag_name: pickRecordField(record, tagAliases),
      is_enabled: isDisabled !== undefined ? !isDisabled : undefined,
      answer_strategy: answerAll === undefined ? undefined : answerAll ? 'all' : 'random',
      is_recommended: notRecommended === undefined ? undefined : !notRecommended,
    })
  }

  const parseJSONFile = async (file: FAQImportFileLike): Promise<FAQEntryPayload[]> => {
    const text = await file.text()
    const data: unknown = JSON.parse(text)
    if (
      !Array.isArray(data) ||
      data.some((item) => !item || typeof item !== 'object' || Array.isArray(item))
    ) {
      throw new Error(t('knowledgeEditor.faqImport.invalidJSON'))
    }
    return data.map((item) => normalizePayload(item as Partial<Record<keyof FAQEntryPayload, unknown>>))
  }

  const parseCSVFile = async (file: FAQImportFileLike): Promise<FAQEntryPayload[]> => {
    const text = await file.text()
    return new Promise((resolve, reject) => {
      papa.parse(text, {
        header: true,
        skipEmptyLines: true,
        delimiter: '',
        quoteChar: '"',
        escapeChar: '"',
        transformHeader: cleanImportHeader,
        complete: (results) => {
          try {
            const payloads: FAQEntryPayload[] = []
            results.data.forEach((rawRow) => {
              if (!rawRow || typeof rawRow !== 'object') return
              const rowObj = rawRow as Record<string, unknown>
              const record: Record<string, string> = Object.create(null) as Record<string, string>
              Object.keys(rowObj).forEach((key) => {
                record[key] = String(rowObj[key] ?? '').trim()
              })
              payloads.push(buildRowPayload(record))
            })
            resolve(payloads)
          } catch (error) {
            reject(error)
          }
        },
        error: (error: Error) => {
          reject(new Error(`CSV parse failed: ${error.message}`))
        },
      })
    })
  }

  const parseExcelFile = async (file: FAQImportFileLike): Promise<FAQEntryPayload[]> => {
    const data = await file.arrayBuffer()
    const workbook = xlsx.read(data, { type: 'array' })
    const sheetName = workbook.SheetNames[0]
    const worksheet = sheetName ? workbook.Sheets[sheetName] : undefined
    if (!worksheet) return []
    const json = xlsx.utils.sheet_to_json<Record<string, string>>(worksheet, {
      defval: '',
      raw: false,
    })
    return json.map((row) => {
      const normalizedRow: Record<string, string> = Object.create(null) as Record<string, string>
      Object.keys(row).forEach((key) => {
        const finalKey = cleanImportHeader(key)
        normalizedRow[finalKey] = String(row[key] || '').trim()
      })
      return buildRowPayload(normalizedRow)
    })
  }

  const processFile = async (file: FAQImportFileLike) => {
    importState.file = file
    if (typeof file.size === 'number' && file.size > MAX_FAQ_IMPORT_FILE_BYTES) {
      message.warning(t('knowledgeEditor.faqImport.parseFailed'))
      importState.preview = []
      return
    }

    try {
      let parsed: FAQEntryPayload[] = []
      const lowerName = String(file.name || '').toLowerCase()
      if (lowerName.endsWith('.json')) {
        parsed = await parseJSONFile(file)
      } else if (lowerName.endsWith('.csv')) {
        parsed = await parseCSVFile(file)
      } else if (lowerName.endsWith('.xlsx') || lowerName.endsWith('.xls')) {
        parsed = await parseExcelFile(file)
      } else {
        message.warning(t('knowledgeEditor.faqImport.unsupportedFormat'))
        importState.preview = []
        return
      }
      importState.preview = parsed
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('knowledgeEditor.faqImport.parseFailed'))
      importState.preview = []
    }
  }

  const getStorageKey = (targetKbId = getKbId()) => `faq_import_task_${targetKbId}`
  const getLastCompletedTaskKey = (targetKbId = getKbId()) => `faq_import_last_completed_${targetKbId}`

  const saveTaskIdToStorage = (taskId: string, targetKbId = getKbId()) => {
    if (!targetKbId) return
    try {
      options.storage?.setItem(getStorageKey(targetKbId), taskId)
    } catch (error) {
      console.error('Failed to save taskId to localStorage:', error)
    }
  }

  const getTaskIdFromStorage = (targetKbId = getKbId()): string | null => {
    if (!targetKbId) return null
    try {
      return options.storage?.getItem(getStorageKey(targetKbId)) ?? null
    } catch {
      return null
    }
  }

  const clearTaskIdFromStorage = (targetKbId = getKbId()) => {
    if (!targetKbId) return
    try {
      options.storage?.removeItem?.(getStorageKey(targetKbId))
    } catch (error) {
      console.error('Failed to clear taskId from localStorage:', error)
    }
  }

  const saveLastCompletedTaskId = (taskId: string, targetKbId = getKbId()) => {
    if (!targetKbId) return
    try {
      options.storage?.setItem(getLastCompletedTaskKey(targetKbId), taskId)
    } catch (error) {
      console.error('Failed to save last completed taskId:', error)
    }
  }

  const getLastCompletedTaskId = (targetKbId = getKbId()): string | null => {
    if (!targetKbId) return null
    try {
      return options.storage?.getItem(getLastCompletedTaskKey(targetKbId)) ?? null
    } catch {
      return null
    }
  }

  const stopPolling = () => {
    if (importState.pollingInterval) {
      timers.clearInterval(importState.pollingInterval)
      importState.pollingInterval = null
    }
    if (progressCloseTimer) {
      timers.clearTimeout(progressCloseTimer)
      progressCloseTimer = null
    }
  }

  const handleCloseProgress = () => {
    stopPolling()
    importState.taskId = null
    importState.taskStatus = null
    clearTaskIdFromStorage()
  }

  const loadImportResult = async () => {
    const targetKbId = getKbId()
    const gen = getGeneration()
    if (!targetKbId || isUnmounted()) return

    const lastTaskId = getLastCompletedTaskId(targetKbId)
    if (!lastTaskId) {
      importResult.value = null
      return
    }

    try {
      const res = await api.getFAQImportProgress(lastTaskId)
      if (isUnmounted() || gen !== getGeneration() || getKbId() !== targetKbId) return
      const data = unwrapResponseData<FAQImportProgressDTO>(res)
      if (data && data.status === 'completed') {
        if (data.display_status === 'close') {
          importResult.value = null
          return
        }
        importResult.value = {
          total_entries: data.total || 0,
          success_count: data.success_count || 0,
          failed_count: data.failed_count || 0,
          skipped_count: data.skipped_count || 0,
          partial_failed_count: data.partial_failed_count || 0,
          merged_count: data.merged_count || 0,
          added_count: data.added_count || 0,
          message: data.message || '',
          import_mode: data.import_mode || 'append',
          imported_at: data.imported_at || '',
          task_id: data.task_id || lastTaskId,
          failed_entries_url: data.failed_entries_url,
          success_entries: data.success_entries,
          display_status: data.display_status || 'open',
          processing_time: data.processing_time || 0,
        }
      } else {
        importResult.value = null
      }
    } catch (error) {
      if (isUnmounted() || gen !== getGeneration() || getKbId() !== targetKbId) return
      console.error('Failed to load FAQ import result:', error)
      importResult.value = null
    }
  }

  const startPolling = (taskId: string) => {
    const pollingKbId = getKbId()
    const gen = getGeneration()
    if (!pollingKbId || isUnmounted()) return
    stopPolling()
    saveTaskIdToStorage(taskId, pollingKbId)

    let lastProcessed = 0
    importState.pollingInterval = timers.setInterval(async () => {
      if (isUnmounted() || gen !== getGeneration() || getKbId() !== pollingKbId) {
        stopPolling()
        return
      }
      try {
        const res = await api.getFAQImportProgress(taskId)
        if (isUnmounted() || gen !== getGeneration() || getKbId() !== pollingKbId) {
          stopPolling()
          return
        }
        const progressData = unwrapResponseData<FAQImportProgressDTO>(res)
        if (!progressData) return

        let status = progressData.status || 'pending'
        if (status === 'processing') status = 'running'
        else if (status === 'completed') status = 'success'

        const progress = progressData.progress || 0
        const total = progressData.total || 0
        const processed = progressData.processed || 0
        const errorText = progressData.error || ''
        const msgText = progressData.message || ''

        importState.taskStatus = {
          status,
          progress,
          total,
          processed,
          message: msgText,
          error: errorText,
        }

        if (processed > lastProcessed) {
          lastProcessed = processed
          await options.onRefreshData()
          if (isUnmounted() || gen !== getGeneration() || getKbId() !== pollingKbId) return
        }

        if (status === 'success' || status === 'failed') {
          stopPolling()
          if (status === 'success') {
            if (importState.taskId) {
              saveLastCompletedTaskId(importState.taskId, pollingKbId)
            }
            message.success(progressData.message || t('knowledgeEditor.faqImport.importSuccess'))
            await options.onImportCompleted()
            await loadImportResult()
            progressCloseTimer = timers.setTimeout(() => {
              progressCloseTimer = null
              if (!isUnmounted() && gen === getGeneration() && importState.taskStatus?.status === 'success') {
                handleCloseProgress()
              }
            }, 3000)
          } else {
            message.error(errorText || t('common.operationFailed'))
          }
        }
      } catch (error: unknown) {
        if (isUnmounted() || gen !== getGeneration() || getKbId() !== pollingKbId) return
        console.error('Failed to poll task status:', error)
        const status = extractResponseStatus(error)
        const errMsg = extractErrorMessage(error)
        if (status === 404 || errMsg?.includes('not found')) {
          clearTaskIdFromStorage(pollingKbId)
          stopPolling()
          importState.taskId = null
          importState.taskStatus = null
        }
      }
    }, 3000)
  }

  const restoreImportTask = async () => {
    const targetKbId = getKbId()
    const gen = getGeneration()
    if (!targetKbId || isUnmounted()) return

    const savedTaskId = getTaskIdFromStorage(targetKbId)
    if (!savedTaskId) return

    try {
      const res = await api.getFAQImportProgress(savedTaskId)
      if (isUnmounted() || gen !== getGeneration() || getKbId() !== targetKbId) return
      const progressData = unwrapResponseData<FAQImportProgressDTO>(res)
      if (progressData) {
        let status = progressData.status || 'pending'
        if (status === 'processing') status = 'running'
        else if (status === 'completed') status = 'success'

        importState.taskId = savedTaskId
        importState.taskStatus = {
          status,
          progress: progressData.progress || 0,
          total: progressData.total || 0,
          processed: progressData.processed || 0,
          message: progressData.message || '',
          error: progressData.error || '',
        }

        if (status === 'pending' || status === 'running') {
          startPolling(savedTaskId)
        } else {
          clearTaskIdFromStorage(targetKbId)
        }
      } else {
        clearTaskIdFromStorage(targetKbId)
      }
    } catch (error: unknown) {
      if (isUnmounted() || gen !== getGeneration() || getKbId() !== targetKbId) return
      console.error('Failed to restore import task:', error)
      const status = extractResponseStatus(error)
      const errMsg = extractErrorMessage(error)
      if (status === 404 || errMsg?.includes('not found')) {
        clearTaskIdFromStorage(targetKbId)
      }
    }
  }

  const openImportDialog = () => {
    if (importState.taskStatus?.status === 'running') {
      message.warning(t('faqManager.import.importInProgress'))
      return
    }
    stopPolling()
    importVisible.value = true
    importState.file = null
    importState.preview = []
    importState.mode = 'append'
    importState.importing = false
  }

  const handleCancelImport = () => {
    stopPolling()
    importState.importing = false
    importState.taskId = null
    importState.taskStatus = null
    importVisible.value = false
  }

  const handleImport = async () => {
    if (!importState.file || !importState.preview.length) {
      message.warning(t('knowledgeEditor.faqImport.selectFile'))
      return
    }

    if (importState.taskStatus?.status === 'success' || importState.taskStatus?.status === 'failed') {
      if (importState.taskStatus.status === 'success') {
        handleCancelImport()
      } else {
        importState.taskId = null
        importState.taskStatus = null
        importState.importing = false
      }
      return
    }

    const kbId = getKbId()
    if (!kbId) return
    importState.importing = true
    try {
      const res = await api.upsertFAQEntries(kbId, {
        entries: importState.preview,
        mode: importState.mode,
      })
      const data = unwrapResponseData<{ task_id?: string }>(res)
      const taskId = data?.task_id
      if (taskId) {
        importState.taskId = taskId
        importState.taskStatus = {
          status: 'pending',
          progress: 0,
          total: importState.preview.length,
          processed: 0,
          message: t('faqManager.import.progressHint'),
        }
        startPolling(taskId)
        importVisible.value = false
        importState.file = null
        importState.preview = []
        importState.importing = false
      } else {
        message.success(t('knowledgeEditor.faqImport.importSuccess'))
        importVisible.value = false
        await options.onRefreshData()
        importState.importing = false
      }
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
      importState.importing = false
      stopPolling()
    }
  }

  const closeImportResult = async () => {
    importResultExpanded.value = false
    const kbId = getKbId()
    if (!kbId) return
    try {
      await api.updateFAQImportResultDisplayStatus(kbId, 'close')
      if (importResult.value) {
        importResult.value.display_status = 'close'
      }
    } catch (error) {
      console.error('Failed to close import result:', error)
    }
  }

  const downloadFailedEntries = () => {
    const rawUrl = String(importResult.value?.failed_entries_url || '').trim()
    if (!rawUrl) {
      message.warning(t('faqManager.import.noFailedRecords'))
      return
    }
    const win = options.getWindow?.() ?? (typeof window !== 'undefined' ? window : undefined)
    let safeUrl = ''
    try {
      if (rawUrl.startsWith('/') && !rawUrl.startsWith('//')) {
        safeUrl = rawUrl
      } else if (typeof URL === 'function') {
        const base = win?.location?.origin || 'https://localhost'
        const parsed = new URL(rawUrl, base)
        if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
          safeUrl = rawUrl
        }
      } else if (/^https?:\/\/[^\s"'<>]+$/i.test(rawUrl)) {
        safeUrl = rawUrl
      }
    } catch {
      safeUrl = ''
    }
    if (!safeUrl) {
      message.warning(t('faqManager.import.noFailedRecords'))
      return
    }
    win?.open?.(safeUrl, '_blank', 'noopener,noreferrer')
  }

  const formatImportTime = (timeStr?: string): string => {
    if (!timeStr) return ''
    try {
      const date = new Date(timeStr)
      if (Number.isNaN(date.getTime())) return timeStr
      return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
    } catch {
      return timeStr
    }
  }

  const downloadExampleOptions = computed<FAQDropdownItem[]>(() => [
    { content: t('knowledgeEditor.faqImport.downloadExampleJSON'), value: 'json' },
    { content: t('knowledgeEditor.faqImport.downloadExampleCSV'), value: 'csv' },
    { content: t('knowledgeEditor.faqImport.downloadExampleExcel'), value: 'excel' },
  ])

  const buildExampleData = (): FAQEntryPayload[] => [
    {
      standard_question: t('knowledgeEditor.faqImport.examples.q1Question'),
      answers: [
        t('knowledgeEditor.faqImport.examples.q1Answer1'),
        t('knowledgeEditor.faqImport.examples.q1Answer2'),
      ],
      similar_questions: [
        t('knowledgeEditor.faqImport.examples.q1Similar1'),
        t('knowledgeEditor.faqImport.examples.q1Similar2'),
      ],
      negative_questions: [
        t('knowledgeEditor.faqImport.examples.q1Negative1'),
        t('knowledgeEditor.faqImport.examples.q1Negative2'),
      ],
      tag_name: t('knowledgeEditor.faqImport.examples.q1Tag'),
    },
    {
      standard_question: t('knowledgeEditor.faqImport.examples.q2Question'),
      answers: [
        t('knowledgeEditor.faqImport.examples.q2Answer1'),
        t('knowledgeEditor.faqImport.examples.q2Answer2'),
        t('knowledgeEditor.faqImport.examples.q2Answer3'),
      ],
      similar_questions: [
        t('knowledgeEditor.faqImport.examples.q2Similar1'),
        t('knowledgeEditor.faqImport.examples.q2Similar2'),
      ],
      negative_questions: [],
      tag_name: t('knowledgeEditor.faqImport.examples.q2Tag'),
    },
  ]

  const triggerBlobDownload = (blob: Blob, fileName: string) => {
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = fileName
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
  }

  const downloadJSONExample = () => {
    const jsonStr = JSON.stringify(buildExampleData(), null, 2)
    triggerBlobDownload(
      new Blob([jsonStr], { type: 'application/json;charset=utf-8' }),
      'faq_example.json',
    )
  }

  const buildExportHeaderLabels = () => ({
    tag: t('knowledgeEditor.faqImport.exportHeaders.tag'),
    question: t('knowledgeEditor.faqImport.exportHeaders.question'),
    similarQuestions: t('knowledgeEditor.faqImport.exportHeaders.similarQuestions'),
    negativeQuestions: t('knowledgeEditor.faqImport.exportHeaders.negativeQuestions'),
    answers: t('knowledgeEditor.faqImport.exportHeaders.answers'),
    replyAll: t('knowledgeEditor.faqImport.exportHeaders.replyAll'),
    disabled: t('knowledgeEditor.faqImport.exportHeaders.disabled'),
    notRecommended: t('knowledgeEditor.faqImport.exportHeaders.notRecommended'),
  })

  const downloadCSVExample = () => {
    const hLabels = buildExportHeaderLabels()
    const headers = [
      hLabels.tag,
      hLabels.question,
      hLabels.similarQuestions,
      hLabels.negativeQuestions,
      hLabels.answers,
      hLabels.replyAll,
      hLabels.disabled,
      hLabels.notRecommended,
    ]
    const rows = buildExampleData().map((item) => [
      item.tag_name || '',
      item.standard_question,
      item.similar_questions.join('##'),
      item.negative_questions.join('##'),
      item.answers.join('##'),
      'FALSE',
      'FALSE',
      'FALSE',
    ])
    const csvContent = [
      headers.join('\t'),
      ...rows.map((row) =>
        row
          .map((cell) =>
            cell.includes('\t') || cell.includes('\n') || cell.includes('"')
              ? `"${cell.replace(/"/g, '""')}"`
              : cell,
          )
          .join('\t'),
      ),
    ].join('\n')
    triggerBlobDownload(
      new Blob([`\ufeff${csvContent}`], { type: 'text/csv;charset=utf-8' }),
      'faq_example.csv',
    )
  }

  const downloadExcelExample = () => {
    const hLabels = buildExportHeaderLabels()
    const worksheet = xlsx.utils.json_to_sheet(
      buildExampleData().map((item) => ({
        [hLabels.tag]: item.tag_name || '',
        [hLabels.question]: item.standard_question,
        [hLabels.similarQuestions]: item.similar_questions.join('##'),
        [hLabels.negativeQuestions]: item.negative_questions.join('##'),
        [hLabels.answers]: item.answers.join('##'),
        [hLabels.replyAll]: 'FALSE',
        [hLabels.disabled]: 'FALSE',
        [hLabels.notRecommended]: 'FALSE',
      })),
    )
    const workbook = xlsx.utils.book_new()
    xlsx.utils.book_append_sheet(workbook, worksheet, 'FAQ')
    xlsx.writeFile(workbook, 'faq_example.xlsx')
  }

  const handleDownloadExample = (data: { value: string }) => {
    switch (data.value) {
      case 'json':
        downloadJSONExample()
        break
      case 'csv':
        downloadCSVExample()
        break
      case 'excel':
        downloadExcelExample()
        break
    }
  }

  const handleExportFAQ = async (format: 'csv' | 'json') => {
    const kbId = getKbId()
    if (!kbId) {
      message.warning(t('knowledgeBase.selectKnowledgeBase'))
      return
    }
    exportLoading.value = true
    try {
      const blob = await api.exportFAQEntries(kbId, format)
      triggerBlobDownload(blob, `faq_export_${new Date().toISOString().slice(0, 10)}.${format}`)
      message.success(t('knowledgeEditor.faqExport.exportSuccess'))
    } catch (error: unknown) {
      console.error('Export failed:', error)
      message.error(t('knowledgeEditor.faqExport.exportFailed'))
    } finally {
      exportLoading.value = false
    }
  }

  const handleExportCSV = () => handleExportFAQ('csv')
  const handleExportJSON = () => handleExportFAQ('json')

  const resetImportForKbSwitch = () => {
    stopPolling()
    importState.importing = false
    importState.taskId = null
    importState.taskStatus = null
    importResult.value = null
    importResultExpanded.value = false
    importVisible.value = false
  }

  onUnmounted(stopPolling)

  return {
    importVisible,
    importState,
    importResult,
    importResultExpanded,
    exportLoading,
    showImportResultBadge,
    isImportInProgress,
    importResultSummary,
    importProgressIcon,
    importProgressText,
    downloadExampleOptions,
    openImportDialog,
    processFile,
    handleImport,
    handleCancelImport,
    handleCloseProgress,
    startPolling,
    stopPolling,
    restoreImportTask,
    loadImportResult,
    closeImportResult,
    downloadFailedEntries,
    formatImportTime,
    handleDownloadExample,
    handleExportCSV,
    handleExportJSON,
    resetImportForKbSwitch,
  }
}
