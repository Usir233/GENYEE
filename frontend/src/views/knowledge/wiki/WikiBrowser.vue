<template>
  <div class="wiki-browser">
    <WikiGraphCanvas
      v-if="view === 'graph'"
      :graph-ready="graphReady"
      :graph-loading="graphLoading"
      :graph-controls-visible="graphControlsVisible"
      :graph-drawer-visible="graphDrawerVisible"
      :graph-focus-group="graphFocusGroup"
      :graph-group-menu="graphGroupMenu"
      v-model:search-value="graphSearchValue"
      :search-options="graphSearchEffectiveOptions"
      :search-loading="graphSearchLoading"
      :on-remote-search="handleGraphRemoteSearch"
      :help-rows="graphHelpRows"
      :pending-issues-count="stats?.pending_issues || 0"
      :graph-filter-types="graphFilterTypes"
      :graph-familiar-count="graphFamiliarCount"
      :unlinked-count="graphUnlinkedSlugs.size"
      :show-unlinked-nodes="showUnlinkedNodes"
      :show-arrows="showArrows"
      :graph-mode="graphMode"
      :graph-frontier-count="graphFrontierCount"
      v-model:detail-level="graphDetailLevel"
      :detail-max="GRAPH_DETAIL_MAX"
      :detail-marks="graphDetailMarks"
      :detail-label="graphDetailLabel"
      :status-card="graphStatusCard"
      :set-graph-el="setGraphEl"
      @search-select="handleGraphSearchSelect"
      @search-enter="handleGraphSearchEnter"
      @focus-group="focusGraphGroup"
      @exit-focus="exitGraphFocus"
      @open-global-issues="showGlobalIssuesDrawer = true"
      @toggle-filter-type="toggleGraphFilterType"
      @toggle-unlinked="toggleUnlinkedNodes"
      @fit-view="fitGraphToView"
      @toggle-arrows="toggleArrows"
      @grow-frontier="growFrontier"
      @back-to-overview="loadGraph"
    >
      <template #drawer>
        <WikiGraphDetailDrawer
          v-model:visible="graphDrawerVisible"
          :page="graphDrawerPage"
          :graph-mode="graphMode"
          :graph-center="graphCenter"
          :can-bloom="graphDrawerCanBloom"
          :neighbor-hint="graphDrawerNeighborHint"
          :rendered-html="graphDrawerContent"
          :get-type-theme="getTypeTheme"
          :get-type-label="getTypeLabel"
          :set-drawer-body-el="setDrawerBodyEl"
          @bloom-neighbors="loadBloomNeighbors"
          @expand-neighbors="loadEgoGraph"
          @content-click="handleGraphDrawerClick"
        />
      </template>
    </WikiGraphCanvas>

    <template v-else>
      <WikiSidebarTree
        :stats="stats"
        :can-edit="props.canEdit"
        :loading="loading"
        v-model:search-query="searchQuery"
        :search-results="searchResults"
        :selected-page-id="selectedPage?.id"
        :index-available="indexAvailable"
        :active-system-view="activeSystemView"
        :visible-tabs="visibleTabs"
        :active-tab="activeTab"
        :active-group="activeGroup"
        :sidebar-view-mode="sidebarViewMode"
        :sidebar-view-switching="sidebarViewSwitching"
        :sidebar-tab-switching="sidebarTabSwitching"
        :active-tree-rows="activeTreeRows"
        :active-flat-pages="activeFlatPages"
        :active-flat-state="activeFlatState"
        :has-content-pages="hasContentPages"
        :drop-target-key="dropTargetKey"
        :pending-move="pendingMove"
        :creating-root-folder="creatingRootFolder"
        v-model:creating-root-folder-name="creatingRootFolderName"
        :editing-folder-id="editingFolderId"
        v-model:editing-name="editingName"
        :get-page-icon="getPageIcon"
        :set-page-list-el="setPageListEl"
        :set-tree-list-el="setTreeListEl"
        :set-group-sentinel-el="setGroupSentinelEl"
        @open-global-issues="showGlobalIssuesDrawer = true"
        @search="doSearch"
        @clear-search="searchResults = null"
        @open-index="openIndexView"
        @select-page="selectPage"
        @select-tab="setActiveTab"
        @switch-view-mode="switchSidebarViewMode"
        @start-create-root-folder="startCreateRootFolder"
        @submit-create-root-folder="submitCreateRootFolder"
        @cancel-create-root-folder="cancelCreateRootFolder"
        @open-create-page="openCreatePageDialog"
        @toggle-directory="toggleDirectory"
        @load-more-directory="(type, path) => loadPagesForType(type, { categoryPath: path })"
        @create-folder="createFolder"
        @start-rename-folder="startRenameFolder"
        @commit-rename-folder="commitRenameFolder"
        @cancel-rename-folder="cancelRenameFolder"
        @delete-folder="deleteFolder"
        @page-drag-start="onPageDragStart"
        @folder-drag-start="onFolderDragStart"
        @drag-end="onPageDragEnd"
        @directory-drag-over="onDirectoryDragOver"
        @root-drag-over="onRootDragOver"
        @directory-drag-leave="onDirectoryDragLeave"
        @drop-directory="onDropOnDirectory"
        @cancel-pending-move="cancelPendingMove"
        @confirm-pending-move="confirmPendingMove"
      />

      <WikiPageReader
        :selected-page="selectedPage"
        :can-edit="props.canEdit"
        :has-back-navigation="Boolean(navHistory.length || navFromSystemView)"
        :back-label="backLabel"
        :page-issues="pageIssues"
        v-model:show-issues-box="showIssuesBox"
        :editing-page="editingPage"
        :saving-page="savingPage"
        :edit-form="editForm"
        :edit-conflict-version="editConflictVersion"
        :rendered-content="renderedContent"
        :reading-assistant-actions="readingAssistantActions"
        :parsed-source-refs="parsedSourceRefs"
        :active-system-view="activeSystemView"
        :index-loading="indexLoading"
        :index-markdown="indexMarkdown"
        :rendered-index-markdown="renderedIndexMarkdown"
        :index-has-more="indexHasMore"
        :has-content-pages="hasContentPages"
        :get-page-icon="getPageIcon"
        :get-type-label="getTypeLabel"
        :edit-source-visible="editSourceVisible"
        :edit-source-label="editSourceLabel"
        :edit-source-icon="editSourceIcon"
        :slug-display-name="slugDisplayName"
        :set-reader-body-el="setReaderBodyEl"
        :set-index-body-el="setIndexBodyEl"
        :set-index-sentinel-el="setIndexSentinelEl"
        @go-back="goBack"
        @auto-fix="triggerAutoFix"
        @fix-issue="triggerFixIssue"
        @ignore-issue="handleIssueIgnore"
        @start-edit="startEditPage"
        @cancel-edit="cancelEditPage"
        @save-edit="savePageEdit()"
        @reload-latest="reloadLatestIntoEditor"
        @overwrite-save="overwriteSavePage"
        @update:edit-title="(v) => (editForm.title = v)"
        @update:edit-summary="(v) => (editForm.summary = v)"
        @update:edit-content="(v) => (editForm.content = v)"
        @open-history="openRevisionDrawer"
        @view-graph="(slug) => emit('view-graph', slug)"
        @delete-page="confirmDeletePage"
        @content-click="handleContentClick"
        @navigate-slug="navigateToSlug"
        @open-source-doc="(id) => emit('open-source-doc', id)"
      />
    </template>

    <Teleport to="body">
      <PicturePreview
        v-if="imagePreviewVisible"
        :reviewImg="imagePreviewVisible"
        :reviewUrl="imagePreviewUrl"
        @closePreImg="closeImagePreview"
      />
    </Teleport>

    <WikiGlobalIssuesDrawer
      v-model:visible="showGlobalIssuesDrawer"
      :issues="globalIssues"
      :can-edit="props.canEdit"
      :slug-display-name="slugDisplayName"
      @navigate-fix="navigateToSlugAndFix"
      @ignore-issue="handleGlobalIssueIgnore"
    />

    <WikiRevisionDrawer
      v-model:visible="showRevisionDrawer"
      :kb-id="props.knowledgeBaseId"
      :slug="selectedPage?.slug || ''"
      :current-page="selectedPage"
      :can-edit="props.canEdit"
      @reverted="onPageReverted"
    />

    <WikiCreatePageDialog
      v-model:visible="showCreatePageDialog"
      :creating="creatingPage"
      :form="createPageForm"
      @update:title="updateCreatePageTitle"
      @update:slug="updateCreatePageSlug"
      @update:page-type="updateCreatePageType"
      @update:content="updateCreatePageContent"
      @confirm="submitCreatePage"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { marked, Renderer, type Tokens } from 'marked'
import { MessagePlugin } from 'tdesign-vue-next'
import { ensureLiteralSingleTildeOnGlobalMarked } from '@/utils/markedLiteralTilde'
import { hydrateProtectedFileImages, sanitizeMarkdownHTML } from '@/utils/security'
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess'
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import { useAssistantStore } from '@/stores/assistant'
import { useAssistantPageContext } from '@/composables/useAssistantPageContext'
import PicturePreview from '@/components/PicturePreview.vue'
import { getKnowledgeDetails } from '@/api/knowledge-base'
import {
  createWikiFolder,
  createWikiPage,
  deleteWikiFolder,
  deleteWikiPage,
  getWikiGraph,
  getWikiIndex,
  getWikiPage,
  getWikiStats,
  listWikiFolders,
  listWikiIssues,
  listWikiPages,
  moveWikiPage,
  searchWikiPages,
  updateWikiFolder,
  updateWikiIssueStatus,
  updateWikiPage,
  type WikiPage,
  type WikiStats,
} from '@/api/wiki'
import WikiRevisionDrawer from './WikiRevisionDrawer.vue'
import WikiGraphCanvas from './components/WikiGraphCanvas.vue'
import WikiGraphDetailDrawer from './components/WikiGraphDetailDrawer.vue'
import WikiSidebarTree from './components/WikiSidebarTree.vue'
import WikiPageReader from './components/WikiPageReader.vue'
import WikiGlobalIssuesDrawer from './components/WikiGlobalIssuesDrawer.vue'
import WikiCreatePageDialog from './components/WikiCreatePageDialog.vue'
import { useWikiGlobalIssues } from './composables/useWikiGlobalIssues'
import { unwrapWikiPayload, useWikiPageEditor } from './composables/useWikiPageEditor'
import { useWikiDirectoryTree } from './composables/useWikiDirectoryTree'
import { useWikiGraphInteraction } from './composables/useWikiGraphInteraction'

ensureLiteralSingleTildeOnGlobalMarked()

class WikiRenderer extends Renderer {
  override link(token: Tokens.Link): string {
    if (/^(?![a-z][a-z\d+.-]*:|\/)[^?#]*\.md(?:[?#]|$)/i.test(token.href)) {
      return this.parser.parseInline(token.tokens)
    }
    return super.link(token)
  }
}
const wikiRenderer = new WikiRenderer()

const route = useRoute()
const assistant = useAssistantStore()
const { t } = useI18n()

const props = defineProps<{
  knowledgeBaseId: string
  view?: 'browser' | 'graph'
  canEdit?: boolean
}>()

const emit = defineEmits<{
  (e: 'open-source-doc', knowledgeId: string): void
  (e: 'status-change', payload: { pendingTasks: number; isActive: boolean; pendingIssues: number }): void
  (e: 'view-graph', slug: string): void
}>()

const kbFileAccess = computed<ProtectedFileAccessContext>(() => ({
  mode: 'knowledgeBase',
  kbId: props.knowledgeBaseId,
}))

const pages = ref<WikiPage[]>([])
const selectedPage = ref<WikiPage | null>(null)
const stats = ref<WikiStats | null>(null)

let statsTimer: ReturnType<typeof setInterval> | null = null
let isWikiUnmounted = false
let kbEpoch = 0

const getRouteSlug = (): string | undefined =>
  route?.query?.slug && typeof route.query.slug === 'string' ? route.query.slug : undefined

const translate = (key: string, params?: Record<string, unknown>) => (params ? t(key, params) : t(key))

let loadPageIssuesBridge: (slug: string) => Promise<void> = async () => {}
let updateSidebarPageTitleBridge: (slug: string, title: string) => void = () => {}
let reloadPagesBridge: () => Promise<void> = async () => {}
let graphSearchSelectBridge: (slug: string) => Promise<void> = async () => {}

const {
  navHistory,
  navFromSystemView,
  activeSystemView,
  indexMarkdown,
  indexLoading,
  indexAvailable,
  indexBodyRef,
  indexSentinelRef,
  readerBodyRef,
  imagePreviewVisible,
  imagePreviewUrl,
  openImagePreview,
  closeImagePreview,
  editingPage,
  savingPage,
  editForm,
  editConflictVersion,
  showRevisionDrawer,
  showCreatePageDialog,
  creatingPage,
  createPageForm,
  getTypeTheme,
  getTypeLabel,
  getPageIcon,
  slugDisplayName,
  renderMarkdown,
  renderedContent,
  renderedIndexMarkdown,
  indexHasMore,
  backLabel,
  readingAssistantActions,
  parsedSourceRefs,
  handleContentClick,
  loadIndex,
  openIndexView,
  selectPage,
  navigateToSlug,
  goBack,
  refreshSelectedPage,
  startEditPage,
  cancelEditPage,
  savePageEdit,
  overwriteSavePage,
  reloadLatestIntoEditor,
  confirmDeletePage,
  openRevisionDrawer,
  onPageReverted,
  openCreatePageDialog,
  updateCreatePageTitle,
  updateCreatePageSlug,
  updateCreatePageType,
  updateCreatePageContent,
  submitCreatePage,
  editSourceVisible,
  editSourceLabel,
  editSourceIcon,
  hydrateReaderIfVisible,
  resetReaderForKbSwitch,
} = useWikiPageEditor({
  getKbId: () => props.knowledgeBaseId,
  pages,
  selectedPage,
  kbFileAccess,
  t: translate,
  message: MessagePlugin,
  logger: console,
  api: {
    getWikiPage,
    getWikiIndex,
    createWikiPage,
    updateWikiPage,
    deleteWikiPage,
    getKnowledgeDetails,
  },
  parseMarkdown: (preprocessed) => marked.parse(preprocessed, { breaks: true, async: false, renderer: wikiRenderer }),
  sanitizeHtml: sanitizeMarkdownHTML,
  hydrateImages: hydrateProtectedFileImages,
  onLoadPageIssues: (slug) => loadPageIssuesBridge(slug),
  onUpdateSidebarPageTitle: (slug, title) => updateSidebarPageTitleBridge(slug, title),
  onReloadPages: () => reloadPagesBridge(),
})

const hostStorage = typeof localStorage !== 'undefined' ? localStorage : undefined

const {
  loading,
  searchQuery,
  searchResults,
  activeTab,
  sidebarViewMode,
  sidebarViewSwitching,
  sidebarTabSwitching,
  pageListRef,
  treeListRef,
  groupSentinelRef,
  hasContentPages,
  visibleTabs,
  activeGroup,
  activeFlatPages,
  activeFlatState,
  activeTreeRows,
  draggedItem,
  dropTargetKey,
  pendingMove,
  editingFolderId,
  editingName,
  creatingRootFolder,
  creatingRootFolderName,
  toggleDirectory,
  loadPagesForType,
  loadPages,
  doSearch,
  switchSidebarViewMode,
  setActiveTab,
  onPageDragStart,
  onFolderDragStart,
  onPageDragEnd,
  onDirectoryDragOver,
  onRootDragOver,
  onDirectoryDragLeave,
  onDropOnDirectory,
  cancelPendingMove,
  confirmPendingMove,
  createFolder,
  startCreateRootFolder,
  cancelCreateRootFolder,
  submitCreateRootFolder,
  startRenameFolder,
  cancelRenameFolder,
  commitRenameFolder,
  deleteFolder,
  updateSidebarPageTitle,
  resetTreeForKbSwitch,
} = useWikiDirectoryTree({
  getKbId: () => props.knowledgeBaseId,
  canEdit: () => Boolean(props.canEdit),
  getRouteSlug,
  pages,
  selectedPage,
  activeSystemView,
  indexAvailable,
  stats,
  t: translate,
  getTypeLabel,
  message: MessagePlugin,
  api: {
    listWikiPages,
    listWikiFolders,
    createWikiFolder,
    updateWikiFolder,
    deleteWikiFolder,
    moveWikiPage,
    searchWikiPages,
  },
  storage: hostStorage,
  timers: { setTimeout, clearTimeout },
  onLoadIndex: loadIndex,
  onOpenIndexView: openIndexView,
  onNavigateToSlug: navigateToSlug,
})

const {
  GRAPH_DETAIL_MAX,
  graphSearchValue,
  graphRef,
  drawerBodyRef,
  graphLoading,
  graphReady,
  graphControlsVisible,
  showArrows,
  showUnlinkedNodes,
  graphUnlinkedSlugs,
  graphFilterTypes,
  graphMode,
  graphCenter,
  graphDetailLevel,
  graphDetailMarks,
  graphDetailLabel,
  graphDrawerVisible,
  graphDrawerPage,
  graphDrawerContent,
  graphDrawerNeighborHint,
  graphDrawerCanBloom,
  graphFrontierCount,
  graphFamiliarCount,
  graphHelpRows,
  graphStatusCard,
  graphFocusGroup,
  graphGroupMenu,
  graphSearchEffectiveOptions,
  graphSearchLoading,
  focusGraphGroup,
  exitGraphFocus,
  fitGraphToView,
  toggleUnlinkedNodes,
  toggleGraphFilterType,
  toggleArrows,
  loadGraph,
  loadEgoGraph,
  loadBloomNeighbors,
  growFrontier,
  handleGraphDrawerClick,
  handleGraphRemoteSearch,
  handleGraphSearchSelect,
  handleGraphSearchEnter,
  resetGraphForKbSwitch,
} = useWikiGraphInteraction({
  getKbId: () => props.knowledgeBaseId,
  getRouteSlug,
  kbFileAccess,
  t: translate,
  getTypeLabel,
  renderMarkdown,
  hydrateImages: hydrateProtectedFileImages,
  openImagePreview,
  api: {
    getWikiGraph,
    getWikiPage,
    searchWikiPages,
  },
  timers: { setTimeout, clearTimeout },
})

const {
  pageIssues,
  showIssuesBox,
  showGlobalIssuesDrawer,
  globalIssues,
  loadGlobalIssues,
  loadPageIssues,
  navigateToSlugAndFix,
  handleGlobalIssueIgnore,
  handleIssueIgnore,
  triggerFixIssue,
  triggerAutoFix,
  resetIssuesForKbSwitch,
} = useWikiGlobalIssues({
  getKbId: () => props.knowledgeBaseId,
  getView: () => props.view,
  selectedPage,
  editingPage,
  t: translate,
  api: {
    listWikiIssues,
    updateWikiIssueStatus,
  },
  assistant,
  onNavigateToSlug: navigateToSlug,
  onGraphSearchSelect: (slug) => graphSearchSelectBridge(slug),
  onRefreshSelectedPage: refreshSelectedPage,
  onReloadStats: () => loadStats(),
})

loadPageIssuesBridge = loadPageIssues
updateSidebarPageTitleBridge = updateSidebarPageTitle
reloadPagesBridge = loadPages
graphSearchSelectBridge = handleGraphSearchSelect

void draggedItem

useAssistantPageContext(() => {
  const page = selectedPage.value
  if (!page) return null
  return {
    page: 'wiki',
    knowledgeBaseId: props.knowledgeBaseId,
    wikiSlug: page.slug,
    wikiTitle: page.title,
    wikiIssueIds: pageIssues.value.map((issue) => issue.id),
  }
})

function setGraphEl(el: HTMLElement | null) {
  graphRef.value = el
}
function setDrawerBodyEl(el: HTMLElement | null) {
  drawerBodyRef.value = el
}
function setPageListEl(el: HTMLElement | null) {
  pageListRef.value = el
}
function setTreeListEl(el: HTMLElement | null) {
  treeListRef.value = el
}
function setGroupSentinelEl(el: HTMLElement | null) {
  groupSentinelRef.value = el
}
function setReaderBodyEl(el: HTMLElement | null) {
  readerBodyRef.value = el
}
function setIndexBodyEl(el: HTMLElement | null) {
  indexBodyRef.value = el
}
function setIndexSentinelEl(el: HTMLElement | null) {
  indexSentinelRef.value = el
}

async function loadStats() {
  const targetKbId = props.knowledgeBaseId
  const epoch = kbEpoch
  if (!targetKbId || isWikiUnmounted) return
  try {
    const res = await getWikiStats(targetKbId)
    if (isWikiUnmounted || epoch !== kbEpoch || targetKbId !== props.knowledgeBaseId) return
    stats.value = unwrapWikiPayload<WikiStats>(res)

    if (stats.value) {
      emit('status-change', {
        pendingTasks: stats.value.pending_tasks || 0,
        isActive: !!stats.value.is_active,
        pendingIssues: stats.value.pending_issues || 0,
      })
    }

    if (stats.value && (stats.value.pending_tasks > 0 || stats.value.is_active)) {
      if (!statsTimer && !isWikiUnmounted) {
        statsTimer = setInterval(() => {
          void loadStats()
        }, 5000)
      }
    } else if (statsTimer) {
      clearInterval(statsTimer)
      statsTimer = null
      void loadPages()
      void refreshSelectedPage()
      if (props.view === 'graph') {
        void loadGraph()
      }
    }
  } catch {
    // Ignore transient stats poll errors.
  }
}

watch(
  () => props.view,
  (v) => {
    if (v === 'graph') {
      void loadGraph()
    } else if (v === 'browser') {
      void hydrateReaderIfVisible()
    }
  },
)

watch(
  () => route.query.slug,
  (newSlug) => {
    if (newSlug && typeof newSlug === 'string') {
      if (!selectedPage.value || selectedPage.value.slug !== newSlug) {
        if (props.view === 'graph') {
          void handleGraphSearchSelect(newSlug)
        } else {
          void navigateToSlug(newSlug)
        }
      }
    }
  },
)

watch(
  () => props.knowledgeBaseId,
  (nextKb, prevKb) => {
    if (!nextKb || nextKb === prevKb) return
    kbEpoch++
    if (statsTimer) {
      clearInterval(statsTimer)
      statsTimer = null
    }
    resetReaderForKbSwitch()
    resetTreeForKbSwitch()
    resetGraphForKbSwitch()
    resetIssuesForKbSwitch()
    void loadPages()
    void loadStats()
    if (props.view === 'graph') void loadGraph()
  },
)

useAssistantDataChange(
  (changes) => {
    if (!changes.some((change) => change.ids.knowledgeBaseIds.includes(props.knowledgeBaseId))) return
    void loadPages()
    void refreshSelectedPage()
    void loadStats()
    if (showGlobalIssuesDrawer.value) void loadGlobalIssues()
  },
  { areas: ['wiki'] },
)

onMounted(() => {
  void loadPages()
  void loadStats()
  if (props.view === 'graph') void loadGraph()
})

onUnmounted(() => {
  isWikiUnmounted = true
  kbEpoch++
  if (statsTimer) {
    clearInterval(statsTimer)
    statsTimer = null
  }
})
</script>

<style lang="less" src="./wiki-browser.less"></style>
<style lang="less" src="@/components/css/wiki-graph-drawer.less"></style>
