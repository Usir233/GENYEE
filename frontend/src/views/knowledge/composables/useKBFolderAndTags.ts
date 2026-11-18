import { computed, ref, type ComputedRef, type Ref } from 'vue'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
import {
  canMoveFolderTo,
  childFolders,
  folderBreadcrumbs as buildFolderBreadcrumbs,
  folderDocumentCounts,
  folderPathExists as folderExistsInTree,
  isFilteringDocuments,
  ROOT_FOLDER_PATH,
} from '../folderTree'
import type { KBDocumentTag } from './useKBDocumentStatusPoll'

const FOLDER_TREE_COLLAPSED_KEY = 'genyee.kbDirectorySidebarCollapsed'
const TAG_PAGE_SIZE = 50

function readStoredFlag(key: string, fallback = false): boolean {
  try {
    const raw = localStorage.getItem(key)
    return raw === null ? fallback : raw === 'true'
  } catch {
    return fallback
  }
}

function writeStoredFlag(key: string, value: boolean) {
  try {
    localStorage.setItem(key, String(value))
  } catch {
    // Private-mode storage failures must not break navigation.
  }
}

export function useKBFolderAndTags(deps: {
  kbId: ComputedRef<string>
  isFAQ: ComputedRef<boolean>
  total: Ref<number>
  docSearchKeyword: Ref<string>
  selectedFileType: Ref<string>
  selectedParseStatus: Ref<string>
  selectedSource: Ref<string>
  updatedTimeRange: Ref<string[]>
  isCurrentKb: (targetKbId: string) => boolean
  resetPage: () => void
  clearSelection: () => void
  batchMode: Ref<boolean>
  loadKnowledgeFiles: (kbIdValue: string) => Promise<void>
  listKnowledgeFolders: (kbId: string) => Promise<unknown>
  moveKnowledgeToFolder: (kbId: string, ids: string[], folderPath: string) => Promise<unknown>
  renameKnowledgeFolder: (kbId: string, from: string, to: string) => Promise<unknown>
  listKnowledgeTags: (
    kbId: string,
    params: { page: number; page_size: number; keyword?: string },
  ) => Promise<unknown>
  uiStore: {
    clearSelectedTagIds: () => void
    toggleSelectedTagId: (id: string) => void
  }
  message: {
    warning: (msg: string) => void
    success: (msg: string) => void
    error: (msg: string) => void
  }
  t: (key: string, params?: Record<string, unknown>) => string
}) {
  const folderTree = ref<KnowledgeFolderTree | null>(null)
  const folderTreeLoading = ref(false)
  const selectedFolderPath = ref<string>(ROOT_FOLDER_PATH)
  const folderTreeCollapsed = ref(readStoredFlag(FOLDER_TREE_COLLAPSED_KEY))

  const selectedTagIds = ref<string[]>([])
  const selectedTagNames = ref<Record<string, string>>({})
  const tagList = ref<KBDocumentTag[]>([])
  const tagLoading = ref(false)
  const tagSearchQuery = ref('')
  const tagPage = ref(1)
  const tagHasMore = ref(false)
  const tagLoadingMore = ref(false)
  const tagTotal = ref(0)

  const filterTagOptions = computed<KBDocumentTag[]>(() => [
    ...tagList.value,
    ...selectedTagIds.value
      .filter((id) => !tagList.value.some((tag) => tag.id === id))
      .map((id) => ({
        id,
        name: selectedTagNames.value[id] || deps.t('knowledgeBase.columnTag'),
      })),
  ])

  const tagMap = computed<Record<string, KBDocumentTag>>(() => {
    const map: Record<string, KBDocumentTag> = {}
    tagList.value.forEach((tag) => {
      map[tag.id] = tag
    })
    return map
  })

  const hasFolders = computed(() => (folderTree.value?.folders?.length ?? 0) > 0)
  const showFolderTree = computed(() => !deps.isFAQ.value && hasFolders.value)

  const isFiltering = computed(() =>
    isFilteringDocuments({
      keyword: deps.docSearchKeyword.value,
      tagIds: selectedTagIds.value,
      fileType: deps.selectedFileType.value,
      parseStatus: deps.selectedParseStatus.value,
      source: deps.selectedSource.value,
      timeRange: deps.updatedTimeRange.value,
    }),
  )

  const showDocumentFolderPath = computed(() => hasFolders.value && isFiltering.value)

  const visibleSubFolders = computed(() => {
    if (isFiltering.value) return []
    if (showFolderTree.value && !folderTreeCollapsed.value) return []
    return childFolders(folderTree.value, selectedFolderPath.value)
  })

  const folderBreadcrumbs = computed(() => buildFolderBreadcrumbs(selectedFolderPath.value))

  const folderContentsCount = computed<{ documents: number; folders: number }>(() => ({
    documents: deps.total.value,
    folders: isFiltering.value
      ? 0
      : childFolders(folderTree.value, selectedFolderPath.value).length,
  }))

  const showFolderContentsCount = computed(
    () => !isFiltering.value && folderContentsCount.value.folders > 0,
  )

  const folderContentsMessage = computed(() => ({
    documents: folderContentsCount.value.documents,
    folders: folderContentsCount.value.folders,
    count: folderContentsCount.value.folders,
  }))

  const folderSubfolderDocumentCount = computed(() => {
    const counts = folderDocumentCounts(folderTree.value, selectedFolderPath.value)
    if (!counts) return 0
    return Math.max(0, counts.total - counts.direct)
  })

  const folderEmptyState = computed<'search' | 'subfolders' | 'folder' | 'none'>(() => {
    if (isFiltering.value) return 'search'
    if (folderSubfolderDocumentCount.value > 0) return 'subfolders'
    return hasFolders.value || selectedFolderPath.value ? 'folder' : 'none'
  })

  const folderOptions = computed(() => {
    const result: Array<{ path: string; name: string; depth: number }> = []
    const walk = (nodes: KnowledgeFolderTree['folders'], depth: number) => {
      nodes.forEach((node) => {
        result.push({ path: node.path, name: node.name, depth })
        walk(node.children || [], depth + 1)
      })
    }
    walk(folderTree.value?.folders || [], 0)
    return result
  })

  const loadFolderTree = async (kbIdValue: string) => {
    if (!kbIdValue || deps.isFAQ.value) {
      folderTree.value = null
      return
    }
    folderTreeLoading.value = true
    try {
      const res = (await deps.listKnowledgeFolders(kbIdValue)) as {
        data?: KnowledgeFolderTree | null
      } | null
      if (!deps.isCurrentKb(kbIdValue)) return
      folderTree.value = res?.data || null
      if (!folderExistsInTree(folderTree.value?.folders || [], selectedFolderPath.value)) {
        selectedFolderPath.value = ROOT_FOLDER_PATH
      }
    } catch (error) {
      if (!deps.isCurrentKb(kbIdValue)) return
      console.error('Failed to load knowledge folders', error)
      folderTree.value = null
    } finally {
      if (deps.isCurrentKb(kbIdValue)) {
        folderTreeLoading.value = false
      }
    }
  }

  const handleFolderSelect = (path: string) => {
    if (selectedFolderPath.value === path) return
    selectedFolderPath.value = path
  }

  const moveKnowledgeIntoFolder = async (ids: string[], folderPath: string) => {
    if (!deps.kbId.value || ids.length === 0) return
    try {
      await deps.moveKnowledgeToFolder(deps.kbId.value, ids, folderPath)
      deps.message.success(deps.t('knowledgeBase.moveToFolder.success', { count: ids.length }))
      deps.clearSelection()
      deps.batchMode.value = false
      deps.resetPage()
      await deps.loadKnowledgeFiles(deps.kbId.value)
      await loadFolderTree(deps.kbId.value)
    } catch (error: unknown) {
      deps.message.error(
        (error as { message?: string } | null)?.message ||
          deps.t('knowledgeBase.moveToFolder.failed'),
      )
    }
  }

  const handleFolderRename = async ({ from, to }: { from: string; to: string }) => {
    if (!deps.kbId.value || !to || from === to) return
    if (!canMoveFolderTo(from, to)) {
      deps.message.warning(deps.t('knowledgeBase.folderTree.renameInvalid'))
      return
    }
    try {
      const res = (await deps.renameKnowledgeFolder(deps.kbId.value, from, to)) as {
        data?: { moved_count?: number }
      } | null
      const movedCount = res?.data?.moved_count ?? 0
      if (movedCount === 0) {
        deps.message.warning(deps.t('knowledgeBase.folderTree.renameFailed'))
        await loadFolderTree(deps.kbId.value)
        return
      }
      deps.message.success(deps.t('knowledgeBase.folderTree.renameSuccess'))
      if (selectedFolderPath.value === from) {
        selectedFolderPath.value = to
      } else if (selectedFolderPath.value.startsWith(`${from}/`)) {
        selectedFolderPath.value = to + selectedFolderPath.value.slice(from.length)
      }
      deps.resetPage()
      await deps.loadKnowledgeFiles(deps.kbId.value)
      await loadFolderTree(deps.kbId.value)
    } catch (error: unknown) {
      deps.message.error(
        (error as { message?: string } | null)?.message ||
          deps.t('knowledgeBase.folderTree.renameFailed'),
      )
    }
  }

  const handleFolderTreeCollapsedChange = (value: boolean) => {
    folderTreeCollapsed.value = value
    writeStoredFlag(FOLDER_TREE_COLLAPSED_KEY, value)
  }

  const loadTags = async (kbIdValue: string, reset = false) => {
    if (!kbIdValue) {
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

    const currentPage = tagPage.value || 1
    tagLoading.value = currentPage === 1
    tagLoadingMore.value = currentPage > 1

    try {
      const res = (await deps.listKnowledgeTags(kbIdValue, {
        page: currentPage,
        page_size: TAG_PAGE_SIZE,
        keyword: tagSearchQuery.value || undefined,
      })) as {
        data?: {
          data?: Array<{ id: string | number; name?: string; color?: string }>
          total?: number
        }
      } | null
      if (!deps.isCurrentKb(kbIdValue)) return

      const pageData = res?.data || {}
      const pageTags: KBDocumentTag[] = (pageData.data || []).map((tag) => ({
        ...tag,
        id: String(tag.id),
        name: String(tag.name || ''),
      }))

      tagList.value = currentPage === 1 ? pageTags : [...tagList.value, ...pageTags]
      tagTotal.value = pageData.total || tagList.value.length
      tagHasMore.value = tagList.value.length < tagTotal.value
      if (tagHasMore.value) {
        tagPage.value = currentPage + 1
      }
    } catch (error) {
      if (!deps.isCurrentKb(kbIdValue)) return
      console.error('Failed to load tags', error)
    } finally {
      if (deps.isCurrentKb(kbIdValue)) {
        tagLoading.value = false
        tagLoadingMore.value = false
      }
    }
  }

  const handleTagFilterChange = (tagIds: string[]) => {
    selectedTagNames.value = Object.fromEntries(
      tagIds.map((id) => [
        id,
        tagMap.value[id]?.name || selectedTagNames.value[id] || deps.t('knowledgeBase.columnTag'),
      ]),
    )
    selectedTagIds.value = tagIds
    deps.uiStore.clearSelectedTagIds()
    tagIds.forEach((id) => deps.uiStore.toggleSelectedTagId(id))
    deps.resetPage()
  }

  const onTagCatalogChanged = (payload?: { deletedTagId?: string }) => {
    void loadTags(deps.kbId.value, true)
    if (payload?.deletedTagId) {
      handleTagFilterChange(selectedTagIds.value.filter((id) => id !== payload.deletedTagId))
    }
    deps.resetPage()
    void deps.loadKnowledgeFiles(deps.kbId.value)
  }

  return {
    folderTree,
    folderTreeLoading,
    selectedFolderPath,
    folderTreeCollapsed,
    hasFolders,
    showFolderTree,
    isFiltering,
    showDocumentFolderPath,
    visibleSubFolders,
    folderBreadcrumbs,
    folderContentsCount,
    showFolderContentsCount,
    folderContentsMessage,
    folderSubfolderDocumentCount,
    folderEmptyState,
    folderOptions,
    loadFolderTree,
    handleFolderSelect,
    moveKnowledgeIntoFolder,
    handleFolderRename,
    handleFolderTreeCollapsedChange,
    selectedTagIds,
    selectedTagNames,
    tagList,
    filterTagOptions,
    tagLoading,
    tagSearchQuery,
    tagPage,
    tagHasMore,
    tagLoadingMore,
    tagMap,
    loadTags,
    handleTagFilterChange,
    onTagCatalogChanged,
  }
}
