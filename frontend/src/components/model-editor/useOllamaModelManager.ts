import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  checkOllamaStatus,
  downloadOllamaModel,
  getDownloadProgress,
  listOllamaModels,
  type OllamaModelInfo,
} from '@/api/initialization'
import type { ModelFormData } from './types'

export function formatModelSize(bytes: number): string {
  if (!bytes || bytes === 0) return ''
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}

export function useOllamaModelManager(options: {
  t: (key: string, params?: Record<string, unknown>) => string
  visible: () => boolean
  isEdit: ComputedRef<boolean>
  formData: Ref<ModelFormData>
}) {
  const { t, visible, isEdit, formData } = options

  const ollamaModelList = ref<OllamaModelInfo[]>([])
  const loadingOllamaModels = ref(false)
  const searchKeyword = ref('')
  const downloading = ref(false)
  const downloadProgress = ref(0)
  const currentDownloadModel = ref('')
  const ollamaServiceStatus = ref<boolean | null>(null)
  const checkingOllamaStatus = ref(false)
  let downloadInterval: ReturnType<typeof setInterval> | null = null
  let downloadSeq = 0

  const filteredOllamaModels = computed(() => {
    if (!searchKeyword.value) return ollamaModelList.value
    const lower = searchKeyword.value.toLowerCase()
    return ollamaModelList.value.filter((model) => model.name.toLowerCase().includes(lower))
  })

  const showDownloadOption = computed(() => {
    if (!searchKeyword.value.trim()) return false
    const lower = searchKeyword.value.toLowerCase()
    return !ollamaModelList.value.some((model) => model.name.toLowerCase() === lower)
  })

  const handleModelFilter = (filterWords: string) => {
    searchKeyword.value = filterWords
    return true
  }

  const loadOllamaModels = async () => {
    if (formData.value.source !== 'local') return
    loadingOllamaModels.value = true
    try {
      const models = await listOllamaModels()
      ollamaModelList.value = models
    } catch (error) {
      console.error('Failed to load Ollama models:', error)
      MessagePlugin.error(t('model.editor.loadModelListFailed'))
    } finally {
      loadingOllamaModels.value = false
    }
  }

  const refreshOllamaModels = async () => {
    ollamaModelList.value = []
    await loadOllamaModels()
    MessagePlugin.success(t('model.editor.listRefreshed'))
  }

  const handleDropdownVisibleChange = (open: boolean) => {
    if (!open) searchKeyword.value = ''
  }

  const checkOllamaServiceStatus = async () => {
    checkingOllamaStatus.value = true
    try {
      const result = await checkOllamaStatus()
      ollamaServiceStatus.value = result.available
    } catch (error) {
      console.error('Failed to check Ollama status:', error)
      ollamaServiceStatus.value = false
    } finally {
      checkingOllamaStatus.value = false
    }

    if (ollamaServiceStatus.value === false && !isEdit.value && formData.value.source === 'local') {
      formData.value.source = 'remote'
    }
  }

  function stopDownloadPolling() {
    downloadSeq += 1
    if (downloadInterval) {
      clearInterval(downloadInterval)
      downloadInterval = null
    }
    downloading.value = false
    downloadProgress.value = 0
    currentDownloadModel.value = ''
  }

  const startDownload = async (modelName: string) => {
    stopDownloadPolling()
    const seq = downloadSeq
    downloading.value = true
    downloadProgress.value = 0
    currentDownloadModel.value = modelName

    try {
      const result = await downloadOllamaModel(modelName)
      if (seq !== downloadSeq) return
      const taskId = result.taskId

      MessagePlugin.success(t('model.editor.downloadStarted', { name: modelName }))

      downloadInterval = setInterval(async () => {
        if (seq !== downloadSeq) return
        try {
          const progress = await getDownloadProgress(taskId)
          if (seq !== downloadSeq) return
          downloadProgress.value = progress.progress

          if (progress.status === 'completed') {
            stopDownloadPolling()
            MessagePlugin.success(t('model.editor.downloadCompleted', { name: modelName }))
            await loadOllamaModels()
            if (visible() && formData.value.source === 'local') {
              formData.value.modelName = modelName
            }
          } else if (progress.status === 'failed') {
            stopDownloadPolling()
            MessagePlugin.error(progress.message || t('model.editor.downloadFailed', { name: modelName }))
          }
        } catch (error) {
          console.error('Failed to fetch Ollama download progress:', error)
        }
      }, 1000)
    } catch (error) {
      if (seq !== downloadSeq) return
      stopDownloadPolling()
      console.error('Download start failed:', error)
      MessagePlugin.error(t('model.editor.downloadStartFailed'))
    }
  }

  return {
    ollamaModelList,
    loadingOllamaModels,
    searchKeyword,
    downloading,
    downloadProgress,
    currentDownloadModel,
    ollamaServiceStatus,
    checkingOllamaStatus,
    filteredOllamaModels,
    showDownloadOption,
    handleModelFilter,
    loadOllamaModels,
    refreshOllamaModels,
    handleDropdownVisibleChange,
    checkOllamaServiceStatus,
    stopDownloadPolling,
    startDownload,
  }
}
