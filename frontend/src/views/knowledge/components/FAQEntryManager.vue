<template>
  <div class="faq-manager">
    <div class="faq-content">
      <FAQHeaderToolbar
        :kb-id="props.kbId"
        :kb-info="kbInfo"
        :knowledge-list="knowledgeList"
        :is-lite-mode="Boolean(authStore.isLiteMode)"
        :can-edit="canEdit"
        :can-manage="canManage"
        :show-import-result-badge="showImportResultBadge"
        :import-result="importResult"
        v-model:import-result-expanded="importResultExpanded"
        :import-result-summary="importResultSummary"
        :is-import-in-progress="isImportInProgress"
        :import-task-status="importState.taskStatus"
        :import-progress-icon="importProgressIcon"
        :import-progress-text="importProgressText"
        :format-import-time="formatImportTime"
        v-model:search-keyword="entrySearchKeyword"
        v-model:tag-search-query="tagSearchQuery"
        v-model:tag-filter-cleared="tagFilterCleared"
        :tag-list="tagList"
        :selected-tag-ids="selectedTagIds"
        :tag-total="tagTotal"
        :tag-loading="tagLoading"
        :tag-loading-more="tagLoadingMore"
        :tag-has-more="tagHasMore"
        v-model:view-mode="viewMode"
        :export-loading="exportLoading"
        @navigate-kb-list="handleNavigateToKbList"
        @navigate-current-kb="handleNavigateToCurrentKB"
        @switch-kb="handleSwitchKnowledgeBase"
        @open-kb-settings="handleOpenKBSettings"
        @download-failed-entries="downloadFailedEntries"
        @close-import-result="closeImportResult"
        @reload-entries="loadEntries()"
        @tag-filter-change="handleTagFilterChange"
        @load-more-tags="loadTags()"
        @open-tag-manage="openTagManageDrawer"
        @faq-action="handleFaqAction"
      />

      <div class="faq-main">
        <div class="faq-card-area">
          <div
            ref="scrollContainer"
            class="faq-scroll-container"
            :class="{ 'has-batch-bar': selectedRowKeys.length > 0 && canSelectEntries }"
          >
            <div
              v-if="loading && entries.length === 0"
              class="faq-skeleton-grid"
              :class="{ 'faq-skeleton-list': viewMode === 'list' }"
            >
              <div v-for="n in 6" :key="'faq-skel-' + n" class="faq-card faq-card-skeleton">
                <div class="faq-card-header">
                  <t-skeleton animation="gradient" :row-col="[{ width: '80%', height: '16px' }]" />
                </div>
                <div class="faq-card-body">
                  <t-skeleton
                    animation="gradient"
                    :row-col="[{ width: '100%', height: '13px' }, { width: '90%', height: '13px' }, { width: '60%', height: '13px' }]"
                  />
                </div>
                <div class="faq-skel-footer">
                  <t-skeleton
                    animation="gradient"
                    :row-col="[[{ width: '50px', height: '18px', type: 'rect' }, { width: '60px', height: '18px', type: 'rect' }]]"
                  />
                </div>
              </div>
            </div>

            <template v-else-if="entries.length > 0">
              <FAQEntryCardGrid
                v-if="viewMode === 'grid'"
                :entries="entries"
                :selected-row-keys="selectedRowKeys"
                :can-select-entries="canSelectEntries"
                :can-edit="canEdit"
                :tag-list="tagList"
                :tag-dropdown-options="tagDropdownOptions"
                :entry-status-loading="entryStatusLoading"
                :get-tag-name="getTagName"
                :set-card-list-el="setCardListEl"
                @toggle-select="handleCardSelect"
                @edit="handleMenuEdit"
                @delete="handleMenuDelete"
                @change-tag="handleEntryTagChange"
                @change-status="handleEntryStatusChange"
              />
              <FAQEntryTableList
                v-else
                :entries="entries"
                :selected-row-keys="selectedRowKeys"
                :can-select-entries="canSelectEntries"
                :batch-action-loading="batchActionLoading"
                :can-edit="canEdit"
                :tag-list="tagList"
                :tag-dropdown-options="tagDropdownOptions"
                :entry-status-loading="entryStatusLoading"
                :get-tag-name="getTagName"
                @toggle-select="handleCardSelect"
                @edit="handleMenuEdit"
                @delete="handleMenuDelete"
                @change-tag="handleEntryTagChange"
                @change-status="handleEntryStatusChange"
              />
            </template>

            <template v-else-if="!loading">
              <div class="faq-empty-state">
                <div class="empty-content">
                  <t-icon name="file-add" size="48px" class="empty-icon" />
                  <div class="empty-text">{{ $t('knowledgeEditor.faq.emptyTitle') }}</div>
                  <div class="empty-desc">{{ $t('knowledgeEditor.faq.emptyDesc') }}</div>
                </div>
              </div>
            </template>

            <div v-if="hasMore" ref="loadMoreSentinelRef" class="faq-load-sentinel" aria-hidden="true" />
            <div v-if="loadingMore" class="faq-load-more">
              <t-loading size="small" :text="$t('common.loading')" />
            </div>
            <div v-if="hasMore === false && entries.length > 0" class="faq-no-more">
              {{ $t('common.noMoreData') }}
            </div>
          </div>

          <div class="faq-batch-bar-anchor">
            <FAQBatchBar
              :count="selectedRowKeys.length"
              :enabled-count="selectedEnabledCount"
              :disabled-count="selectedDisabledCount"
              :can-edit="canEdit"
              :can-manage="canManage"
              :tag-loading="batchTagLoading"
              :status-action="batchStatusAction"
              :delete-loading="batchDeleteLoading"
              @cancel="clearFAQSelection"
              @batch-tag="openBatchTagDialog"
              @enable="handleBatchStatusChange(true)"
              @disable="handleBatchStatusChange(false)"
              @delete="handleBatchDelete"
            />
          </div>
        </div>
      </div>
    </div>

    <FAQEntryEditorDrawer
      v-model:visible="editorVisible"
      :editor-mode="editorMode"
      :editor-form="editorForm"
      :editor-rules="editorRules"
      :saving-entry="savingEntry"
      v-model:similar-input="similarInput"
      v-model:negative-input="negativeInput"
      v-model:answer-input="answerInput"
      :faq-assistant-selection="faqAssistantSelection"
      :faq-answer-selection="faqAnswerSelection"
      :tag-select-options="tagSelectOptions"
      :set-form-ref="setEditorFormRef"
      @close="handleEditorClose"
      @update:standard-question="(v) => (editorForm.standard_question = v)"
      @update:tag-id="(v) => (editorForm.tag_id = v)"
      @add-similar="addSimilar"
      @remove-similar="removeSimilar"
      @add-negative="addNegative"
      @remove-negative="removeNegative"
      @add-answer="addAnswer"
      @remove-answer="removeAnswer"
      @submit="handleSubmitEntry"
    />

    <FAQImportModal
      v-model:visible="importVisible"
      :import-state="importState"
      :download-example-options="downloadExampleOptions"
      @update:mode="(v) => (importState.mode = v)"
      @select-file="processFile"
      @download-example="handleDownloadExample"
      @cancel="handleCancelImport"
      @confirm="handleImport"
    />

    <FAQBatchTagModal
      v-model:visible="batchTagDialogVisible"
      v-model="batchTagValue"
      :selected-count="selectedRowKeys.length"
      :tag-select-options="tagSelectOptions"
      :loading="batchTagLoading"
      :disabled="batchActionLoading"
      @confirm="handleBatchTag"
    />

    <FAQSearchTestDrawer
      v-model:visible="searchDrawerVisible"
      :search-form="searchForm"
      :searching="searching"
      :has-searched="hasSearched"
      :search-results="searchResults"
      @update:query="(v) => (searchForm.query = v)"
      @update:vector-threshold="(v) => (searchForm.vectorThreshold = v)"
      @update:match-count="(v) => (searchForm.matchCount = v)"
      @search="handleSearch"
      @toggle-result="toggleResult"
    />

    <KbTagManageDrawer
      v-model:visible="tagManageDrawerVisible"
      :kb-id="props.kbId"
      :is-faq="true"
      @changed="onTagManageChanged"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { MessagePlugin, type FormInstanceFunctions } from 'tdesign-vue-next'
import * as XLSX from 'xlsx'
import Papa from 'papaparse'
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { useAuthStore } from '@/stores/auth'
import { useOrganizationStore } from '@/stores/organization'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useUIStore } from '@/stores/ui'
import {
  listFAQEntries,
  upsertFAQEntries,
  createFAQEntry,
  updateFAQEntry,
  updateFAQEntryFieldsBatch,
  deleteFAQEntries,
  searchFAQEntries,
  exportFAQEntries,
  listKnowledgeTags,
  updateFAQEntryTagBatch,
  getKnowledgeBaseById,
  getFAQImportProgress,
  updateFAQImportResultDisplayStatus,
} from '@/api/knowledge-base'
import FAQBatchBar from './FAQBatchBar.vue'
import KbTagManageDrawer from './KbTagManageDrawer.vue'
import FAQHeaderToolbar from './faq/FAQHeaderToolbar.vue'
import FAQEntryCardGrid from './faq/FAQEntryCardGrid.vue'
import FAQEntryTableList from './faq/FAQEntryTableList.vue'
import FAQEntryEditorDrawer from './faq/FAQEntryEditorDrawer.vue'
import FAQImportModal from './faq/FAQImportModal.vue'
import FAQBatchTagModal from './faq/FAQBatchTagModal.vue'
import FAQSearchTestDrawer from './faq/FAQSearchTestDrawer.vue'
import {
  unwrapResponseData,
  useFAQEntries,
  type FAQKnowledgeBaseInfo,
  type FAQKnowledgeSwitchOption,
} from '../composables/useFAQEntries'
import { useFAQImportExport, type PapaParseLike, type XLSXLike } from '../composables/useFAQImportExport'
import { useFAQSearchTest } from '../composables/useFAQSearchTest'

const props = defineProps<{
  kbId: string
}>()

const { t } = useI18n()
const confirmDelete = useConfirmDelete()
const router = useRouter()
const uiStore = useUIStore()
const authStore = useAuthStore()
const orgStore = useOrganizationStore()
const chatResources = useChatResourcesStore()

const kbInfo = ref<FAQKnowledgeBaseInfo | null>(null)
const knowledgeList = ref<FAQKnowledgeSwitchOption[]>([])

let kbSwitchGen = 0
let isFaqUnmounted = false

const isOwner = computed(() => {
  if (!kbInfo.value) return false
  const creatorId = kbInfo.value.creator_id || ''
  const userId = authStore.user?.id || ''
  if (!creatorId) return false
  return creatorId === userId && authStore.hasRole('contributor')
})

const currentSharedKb = computed(
  () => orgStore.sharedKnowledgeBases.find((s) => s.knowledge_base?.id === props.kbId) ?? null,
)
const isViaShare = computed(() => !!currentSharedKb.value)

const effectiveKBPermission = computed(
  () => orgStore.getKBPermission?.(props.kbId) || kbInfo.value?.my_permission || '',
)
const faqPermissionAllowsEdit = (p: string) => p === 'owner' || p === 'admin' || p === 'editor'
const faqPermissionAllowsManage = (p: string) => p === 'owner' || p === 'admin'

const canEdit = computed(() => {
  const permission = effectiveKBPermission.value
  if (permission) return faqPermissionAllowsEdit(permission)
  if (isViaShare.value) return orgStore.canEditKB(props.kbId, false)
  if (isOwner.value) return true
  if (authStore.hasRole('admin')) return true
  return orgStore.canEditKB?.(props.kbId, false) ?? false
})

const canManage = computed(() => {
  const permission = effectiveKBPermission.value
  if (permission) return faqPermissionAllowsManage(permission)
  if (isViaShare.value) return orgStore.canManageKB(props.kbId, false)
  if (isOwner.value) return true
  if (authStore.hasRole('admin')) return true
  return orgStore.canManageKB?.(props.kbId, false) ?? false
})

const hostStorage = typeof localStorage !== 'undefined' ? localStorage : undefined

const {
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
} = useFAQEntries({
  getKbId: () => props.kbId,
  canEdit,
  canManage,
  t: (key, params) => (params ? t(key, params) : t(key)),
  message: MessagePlugin,
  confirmDelete,
  uiStore,
  api: {
    listFAQEntries,
    createFAQEntry,
    updateFAQEntry,
    updateFAQEntryFieldsBatch,
    deleteFAQEntries,
    listKnowledgeTags,
    updateFAQEntryTagBatch,
  },
  storage: hostStorage,
  timers: {
    setTimeout,
    clearTimeout,
    requestAnimationFrame: typeof requestAnimationFrame === 'function' ? requestAnimationFrame : undefined,
  },
})

const {
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
} = useFAQImportExport({
  getKbId: () => props.kbId,
  getGeneration: () => kbSwitchGen,
  isUnmounted: () => isFaqUnmounted,
  t: (key, params) => (params ? t(key, params) : t(key)),
  message: MessagePlugin,
  papa: Papa as unknown as PapaParseLike,
  xlsx: XLSX as unknown as XLSXLike,
  api: {
    upsertFAQEntries,
    getFAQImportProgress,
    updateFAQImportResultDisplayStatus,
    exportFAQEntries,
  },
  storage: hostStorage,
  timers: { setInterval, clearInterval, setTimeout, clearTimeout },
  getWindow: () => (typeof window !== 'undefined' ? window : undefined),
  onRefreshData: async () => {
    await loadEntries()
    await loadTags(true)
  },
  onImportCompleted: async () => {
    resetFiltersAfterImport()
    await loadEntries()
    await loadTags(true)
  },
})

const {
  searchDrawerVisible,
  searching,
  hasSearched,
  searchResults,
  searchForm,
  handleSearch,
  toggleResult,
  resetSearchTest,
} = useFAQSearchTest({
  getKbId: () => props.kbId,
  t: (key, params) => (params ? t(key, params) : t(key)),
  message: MessagePlugin,
  searchFAQEntries,
})

void startPolling

function setCardListEl(el: HTMLElement | null) {
  cardListRef.value = el
}

function setEditorFormRef(inst: FormInstanceFunctions | undefined) {
  editorFormRef.value = inst
}

const loadKnowledgeInfo = async (kbId: string): Promise<FAQKnowledgeBaseInfo | null> => {
  if (!kbId) {
    kbInfo.value = null
    return null
  }
  try {
    const res = await getKnowledgeBaseById(kbId)
    kbInfo.value = unwrapResponseData<FAQKnowledgeBaseInfo>(res) ?? null
    return kbInfo.value
  } catch (error) {
    console.error('Failed to load knowledge base info:', error)
    kbInfo.value = null
    return null
  }
}

const loadKnowledgeList = async () => {
  try {
    await chatResources.ensureKnowledgeBases()
    const rawItems = (chatResources.rawKnowledgeBases || []) as Array<{
      id: string | number
      name: string
      type?: string
    }>
    const myKbs: FAQKnowledgeSwitchOption[] = rawItems.map((item) => ({
      id: String(item.id),
      name: item.name,
      type: item.type,
    }))

    const sharedKbs: FAQKnowledgeSwitchOption[] = (orgStore.sharedKnowledgeBases || [])
      .filter((s) => s.knowledge_base != null)
      .map((s) => ({
        id: String(s.knowledge_base.id),
        name: s.knowledge_base.name,
        type: s.knowledge_base.type,
      }))

    const myKbIds = new Set(myKbs.map((kb) => kb.id))
    const uniqueSharedKbs = sharedKbs.filter((kb) => !myKbIds.has(kb.id))
    knowledgeList.value = [...myKbs, ...uniqueSharedKbs]
  } catch (error) {
    console.error('Failed to load knowledge bases:', error)
  }
}

const handleFaqAction = (action: string) => {
  switch (action) {
    case 'create':
      openEditor()
      break
    case 'import':
      openImportDialog()
      break
    case 'search':
      searchDrawerVisible.value = true
      break
    case 'export_csv':
    case 'export':
      void handleExportCSV()
      break
    case 'export_json':
      void handleExportJSON()
      break
  }
}

const handleNavigateToKbList = () => {
  router.push('/platform/knowledge-bases')
}

const handleNavigateToCurrentKB = () => {
  if (!props.kbId) return
  router.push(`/platform/knowledge-bases/${props.kbId}`)
}

const handleOpenKBSettings = () => {
  if (!props.kbId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  uiStore.openKBSettings(props.kbId)
}

const handleSwitchKnowledgeBase = (nextId: string) => {
  if (!nextId || nextId === props.kbId) return
  router.push(`/platform/knowledge-bases/${nextId}`)
}

watch(
  () => props.kbId,
  async (newKbId) => {
    const gen = ++kbSwitchGen
    resetImportForKbSwitch()
    resetForKbSwitch()
    resetSearchTest()

    if (!newKbId) {
      kbInfo.value = null
      return
    }

    const info = await loadKnowledgeInfo(newKbId)
    if (isFaqUnmounted || gen !== kbSwitchGen || !info || info.type !== 'faq') {
      return
    }

    void loadEntries()
    void loadTags(true)
    await restoreImportTask()
    await loadImportResult()
  },
  { immediate: true },
)

useAssistantDataChange(
  (changes) => {
    if (!changes.some((change) => change.ids.knowledgeBaseIds.includes(props.kbId))) return
    void loadEntries()
    void loadTags(true)
  },
  { areas: ['faq'] },
)

onMounted(async () => {
  await orgStore.fetchSharedKnowledgeBases()
  if (isFaqUnmounted) return
  await loadKnowledgeList()
  if (props.kbId) {
    await restoreImportTask()
    await loadImportResult()
  }
})

onUnmounted(() => {
  isFaqUnmounted = true
  kbSwitchGen++
  stopPolling()
})
</script>

<style lang="less" src="./faq-entry-manager.less"></style>
