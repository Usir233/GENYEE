import { reactive, ref } from 'vue'
import {
  extractErrorMessage,
  unwrapResponseData,
  type FAQEntry,
  type FAQMessageApi,
} from './useFAQEntries'

export interface FAQSearchFormState {
  query: string
  vectorThreshold: number
  matchCount: number
}

export interface UseFAQSearchTestOptions {
  getKbId: () => string
  t: (key: string, params?: Record<string, unknown>) => string
  message: FAQMessageApi
  searchFAQEntries: (
    kbId: string,
    data: { query_text: string; vector_threshold?: number; match_count?: number },
  ) => Promise<unknown>
}

export function useFAQSearchTest(options: UseFAQSearchTestOptions) {
  const { getKbId, t, message, searchFAQEntries } = options

  const searchDrawerVisible = ref(false)
  const searching = ref(false)
  const hasSearched = ref(false)
  const searchResults = ref<FAQEntry[]>([])
  const searchForm = reactive<FAQSearchFormState>({
    query: '',
    vectorThreshold: 0.7,
    matchCount: 10,
  })

  const handleSearch = async () => {
    if (!searchForm.query.trim()) {
      message.warning(t('knowledgeEditor.faq.queryPlaceholder'))
      return
    }
    const kbId = getKbId()
    if (!kbId) return

    searching.value = true
    hasSearched.value = true
    try {
      const res = await searchFAQEntries(kbId, {
        query_text: searchForm.query.trim(),
        vector_threshold: searchForm.vectorThreshold,
        match_count: searchForm.matchCount,
      })
      const rawResults = unwrapResponseData<FAQEntry[]>(res) ?? []
      const mapped = rawResults.map((entry) => ({
        ...entry,
        similarCollapsed: true,
        negativeCollapsed: true,
        answersCollapsed: true,
        expanded: false,
      }))
      searchResults.value = mapped.toSorted((a, b) => (b.score || 0) - (a.score || 0))
    } catch (error: unknown) {
      message.error(extractErrorMessage(error) || t('common.operationFailed'))
      searchResults.value = []
    } finally {
      searching.value = false
    }
  }

  const toggleResult = (result: FAQEntry) => {
    result.expanded = !result.expanded
  }

  const resetSearchTest = () => {
    searchDrawerVisible.value = false
    searching.value = false
    hasSearched.value = false
    searchResults.value = []
  }

  return {
    searchDrawerVisible,
    searching,
    hasSearched,
    searchResults,
    searchForm,
    handleSearch,
    toggleResult,
    resetSearchTest,
  }
}
