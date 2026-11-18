<template>
  <div class="doc-filter-bar">
    <nav class="doc-folder-path" :aria-label="$t('knowledgeBase.folderTree.title')">
      <button
        v-if="showFolderTree && folderTreeCollapsed"
        type="button"
        class="doc-folder-path__tree-toggle"
        :aria-expanded="false"
        :title="$t('knowledgeBase.folderTree.expand')"
        :aria-label="$t('knowledgeBase.folderTree.expand')"
        @click="emit('expand-folder-tree')"
      >
        <t-icon name="view-list" size="16px" />
      </button>
      <button
        v-if="folderBreadcrumbs.length"
        type="button"
        class="doc-folder-path__crumb"
        :title="kbName"
        @click="emit('select-folder', '')"
      >
        {{ kbName }}
      </button>
      <span
        v-else
        class="doc-folder-path__crumb is-current"
        :title="kbName"
        aria-current="page"
      >{{ kbName }}</span>
      <template v-for="(crumb, index) in folderBreadcrumbs" :key="crumb.path">
        <t-icon name="chevron-right" class="doc-folder-path__sep" />
        <span
          v-if="index === folderBreadcrumbs.length - 1"
          class="doc-folder-path__crumb is-current"
          :title="crumb.name"
          aria-current="page"
        >{{ crumb.name }}</span>
        <button
          v-else
          type="button"
          class="doc-folder-path__crumb"
          :title="crumb.name"
          @click="emit('select-folder', crumb.path)"
        >{{ crumb.name }}</button>
      </template>
      <span v-if="!docListLoading" class="doc-folder-path__count">
        <template v-if="isFiltering">{{ $t('knowledgeBase.folderTree.filteredCount', { count: folderContentsCount.documents }) }}</template>
        <template v-else-if="showFolderContentsCount">{{ $t('knowledgeBase.folderTree.folderContentsCount', folderContentsMessage) }}</template>
        <template v-else>{{ $t('knowledgeBase.documentCount', { count: folderContentsCount.documents }) }}</template>
      </span>
      <t-tooltip v-if="showFolderTree && isFiltering" :content="$t('knowledgeBase.folderTree.searchingSubtree')">
        <t-icon name="info-circle" class="doc-folder-path__sep" />
      </t-tooltip>
    </nav>

    <div class="doc-filter-bar__trailing">
      <t-input
        :value="docSearchKeyword"
        :placeholder="$t('knowledgeBase.docSearchPlaceholder')"
        :aria-label="$t('knowledgeBase.docSearchPlaceholder')"
        clearable
        class="doc-search-input"
        @change="(val: string) => emit('update:docSearchKeyword', String(val ?? '').trim())"
        @clear="emit('refresh-files')"
        @enter="emit('refresh-files')"
      >
        <template #prefix-icon>
          <t-icon name="search" size="16px" />
        </template>
      </t-input>

      <t-popup
        v-model:visible="filtersExpanded"
        trigger="click"
        placement="bottom-right"
        overlay-class-name="document-filter-popup"
        :overlay-inner-style="{ padding: 0 }"
      >
        <button
          type="button"
          class="doc-filter-toggle"
          :class="{ active: filtersExpanded || activeFilterCount > 0 }"
          :aria-expanded="filtersExpanded"
          aria-controls="document-filters"
        >
          <t-icon name="filter" size="16px" />
          {{ $t('knowledgeBase.filters') }}
          <span v-if="activeFilterCount" class="doc-filter-count">{{ activeFilterCount }}</span>
        </button>
        <template #content>
          <section id="document-filters" class="doc-filter-panel" :aria-label="$t('knowledgeBase.filters')">
            <header class="doc-filter-panel__header">
              <strong>{{ $t('knowledgeBase.filters') }}</strong>
              <button type="button" :disabled="!activeFilterCount" @click="emit('clear-filters')">
                {{ $t('knowledgeBase.clearFilters') }}
              </button>
            </header>
            <div class="doc-filter-panel__fields">
              <div class="doc-filter-field">
                <span>{{ $t('knowledgeBase.fileTypeFilter') }}</span>
                <t-select
                  :value="selectedFileType"
                  :options="fileTypeOptions"
                  :placeholder="$t('knowledgeBase.fileTypeFilter')"
                  clearable
                  @change="(val: string | number) => emit('update:selectedFileType', String(val ?? ''))"
                />
              </div>
              <div class="doc-filter-field">
                <span>{{ $t('knowledgeBase.parseStatusFilter') }}</span>
                <t-select
                  :value="selectedParseStatus"
                  :options="parseStatusOptions"
                  :placeholder="$t('knowledgeBase.parseStatusFilter')"
                  clearable
                  @change="(val: string | number) => emit('update:selectedParseStatus', String(val ?? ''))"
                />
              </div>
              <div class="doc-filter-field">
                <span>{{ $t('knowledgeBase.sourceFilter') }}</span>
                <t-select
                  :value="selectedSource"
                  :options="sourceOptions"
                  :placeholder="$t('knowledgeBase.sourceFilter')"
                  clearable
                  @change="(val: string | number) => emit('update:selectedSource', String(val ?? ''))"
                />
              </div>
              <div class="doc-filter-field">
                <span>{{ $t('knowledgeBase.columnUpdatedAt') }}</span>
                <t-date-range-picker
                  :value="updatedTimeRange"
                  :placeholder="[$t('knowledgeBase.updatedTimeFrom'), $t('knowledgeBase.updatedTimeTo')]"
                  :disable-date="disableFutureDate"
                  clearable
                  allow-input
                  @change="(val: string[]) => emit('update:updatedTimeRange', Array.isArray(val) ? val : [])"
                />
              </div>
            </div>
            <div class="doc-filter-tags">
              <div class="doc-filter-tags__heading">
                <span>
                  {{ $t('knowledgeBase.columnTag') }}
                  <span v-if="selectedTagIds.length" class="doc-filter-tags__count">{{ selectedTagIds.length }}</span>
                </span>
              </div>
              <t-input
                :value="tagSearchQuery"
                :placeholder="$t('knowledgeBase.tagSearchPlaceholder')"
                clearable
                @change="(val: string) => emit('update:tagSearchQuery', String(val ?? '').trim())"
              >
                <template #prefix-icon><t-icon name="search" size="14px" /></template>
              </t-input>
              <div class="doc-filter-tags__list">
                <t-loading v-if="tagLoading && !hasLoadedTags" size="small" />
                <t-checkbox
                  v-for="tag in filterTagOptions"
                  :key="tag.id"
                  class="doc-filter-tag"
                  :title="tag.name"
                  :checked="selectedTagIds.includes(tag.id)"
                  @change="(checked: boolean) => emit('toggle-tag-filter', tag.id, checked)"
                >
                  <span>{{ tag.name }}</span>
                </t-checkbox>
                <span v-if="!tagLoading && !filterTagOptions.length" class="doc-filter-tags__empty">
                  {{ $t(tagSearchQuery ? 'knowledgeBase.tagEmptyResult' : 'knowledgeBase.noTags') }}
                </span>
              </div>
              <t-button
                v-if="tagHasMore"
                variant="text"
                size="small"
                :loading="tagLoadingMore"
                @click="emit('load-more-tags')"
              >
                {{ $t('tenant.loadMore') }}
              </t-button>
            </div>
          </section>
        </template>
      </t-popup>

      <button
        v-if="viewMode === 'grid' && showBatchToggle && hasDocuments"
        type="button"
        class="doc-filter-toggle doc-batch-toggle"
        :class="{ active: batchMode }"
        :aria-pressed="batchMode"
        :disabled="batchActionBusy"
        @click="emit('toggle-batch-mode')"
      >
        <t-icon :name="batchMode ? 'close' : 'check-rectangle'" size="16px" />
        {{ $t(batchMode ? 'common.cancel' : 'menu.batchManage') }}
      </button>

      <t-popup
        v-model:visible="documentSortPanelVisible"
        trigger="click"
        placement="bottom-right"
        overlay-class-name="document-sort-popup"
        :overlay-inner-style="{ padding: 0 }"
      >
        <template #content>
          <div class="document-sort-panel" role="menu" :aria-label="$t('knowledgeBase.sort.title')">
            <section v-for="group in documentSortGroups" :key="group.key" class="document-sort-group">
              <div class="document-sort-group__heading">
                <div class="document-sort-group__label">{{ group.label }}</div>
                <div class="document-sort-group__description">{{ group.description }}</div>
              </div>
              <div class="document-sort-group__options">
                <button
                  v-for="option in group.options"
                  :key="option.value"
                  type="button"
                  class="document-sort-option"
                  :class="{ active: selectedDocumentSort === option.value }"
                  role="menuitemradio"
                  :aria-checked="selectedDocumentSort === option.value"
                  @click.stop="onSelectSort(option.value)"
                >
                  <span>{{ documentSortOptionLabel(option) }}</span>
                  <t-icon v-if="selectedDocumentSort === option.value" name="check" size="14px" />
                </button>
              </div>
            </section>
          </div>
        </template>
        <button
          type="button"
          class="doc-sort-trigger"
          :class="{ active: documentSortPanelVisible }"
          :title="`${$t('knowledgeBase.sort.title')}: ${activeDocumentSortLabel}`"
          :aria-label="`${$t('knowledgeBase.sort.title')}: ${activeDocumentSortLabel}`"
        >
          <t-icon name="filter-sort" size="16px" />
          <span class="doc-sort-trigger__label">
            {{ $t('knowledgeBase.sort.title') }} · {{ activeDocumentSortLabel }}
          </span>
          <t-icon
            name="chevron-down"
            size="14px"
            class="doc-sort-trigger__caret"
            :class="{ open: documentSortPanelVisible }"
          />
        </button>
      </t-popup>

      <div class="doc-view-toggle" role="group" :aria-label="$t('knowledgeBase.viewModeToggle')">
        <t-tooltip :content="$t('knowledgeBase.viewModeGrid')" placement="top">
          <button
            type="button"
            class="doc-view-toggle-btn"
            :class="{ active: viewMode === 'grid' }"
            :aria-label="$t('knowledgeBase.viewModeGrid')"
            :aria-pressed="viewMode === 'grid'"
            @click="emit('update:viewMode', 'grid')"
          >
            <t-icon name="view-module" size="16px" />
          </button>
        </t-tooltip>
        <t-tooltip :content="$t('knowledgeBase.viewModeList')" placement="top">
          <button
            type="button"
            class="doc-view-toggle-btn"
            :class="{ active: viewMode === 'list' }"
            :aria-label="$t('knowledgeBase.viewModeList')"
            :aria-pressed="viewMode === 'list'"
            @click="emit('update:viewMode', 'list')"
          >
            <t-icon name="view-list" size="16px" />
          </button>
        </t-tooltip>
      </div>

      <div v-if="canEdit" class="doc-filter-actions">
        <KbUploadSourceDropdown
          ref="uploadSourceRef"
          :accept-file-types="acceptFileTypes"
          :supported-file-types="supportedFileTypes"
          include-manual
          trigger-icon="add"
          :trigger-label="t('knowledgeBase.addDocument')"
          trigger-class="content-bar-icon-btn"
          data-guide="kb-detail-add-doc"
          :tooltip="t('knowledgeBase.addDocument')"
          placement="bottom-right"
          @files="emit('upload-files', $event)"
          @url="emit('upload-url', $event)"
          @manual="emit('manual-create')"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import KbUploadSourceDropdown from './KbUploadSourceDropdown.vue'
import {
  DOCUMENT_SORT_OPTIONS,
  getDocumentSortOption,
  type DocumentSortOption,
  type DocumentSortValue,
} from '../documentSorting'
import type { KBDocumentTag } from '../composables/useKBDocumentStatusPoll'

const props = defineProps<{
  kbName?: string
  showFolderTree: boolean
  folderTreeCollapsed: boolean
  folderBreadcrumbs: Array<{ path: string; name: string }>
  docListLoading: boolean
  isFiltering: boolean
  showFolderContentsCount: boolean
  folderContentsCount: { documents: number; folders: number }
  folderContentsMessage: { documents: number; folders: number; count: number }
  docSearchKeyword: string
  selectedFileType: string
  selectedParseStatus: string
  selectedSource: string
  updatedTimeRange: string[]
  selectedTagIds: string[]
  tagSearchQuery: string
  tagLoading: boolean
  hasLoadedTags: boolean
  filterTagOptions: KBDocumentTag[]
  tagHasMore: boolean
  tagLoadingMore: boolean
  viewMode: 'grid' | 'list'
  showBatchToggle: boolean
  hasDocuments: boolean
  batchMode: boolean
  batchActionBusy: boolean
  selectedDocumentSort: DocumentSortValue
  canEdit: boolean
  acceptFileTypes: string
  supportedFileTypes: string[]
}>()

const emit = defineEmits<{
  'expand-folder-tree': []
  'select-folder': [path: string]
  'update:docSearchKeyword': [val: string]
  'refresh-files': []
  'clear-filters': []
  'update:selectedFileType': [val: string]
  'update:selectedParseStatus': [val: string]
  'update:selectedSource': [val: string]
  'update:updatedTimeRange': [val: string[]]
  'update:tagSearchQuery': [val: string]
  'toggle-tag-filter': [tagId: string, checked: boolean]
  'load-more-tags': []
  'toggle-batch-mode': []
  'select-sort': [value: DocumentSortValue]
  'update:viewMode': [mode: 'grid' | 'list']
  'upload-files': [files: File[]]
  'upload-url': [url: string]
  'manual-create': []
}>()

const { t } = useI18n()

const filtersExpanded = ref(false)
const documentSortPanelVisible = ref(false)
const uploadSourceRef = ref<InstanceType<typeof KbUploadSourceDropdown> | null>(null)
const disableFutureDate = { after: new Date(new Date().setHours(23, 59, 59, 999)) }

const activeFilterCount = computed(
  () =>
    [
      props.selectedFileType,
      props.selectedParseStatus,
      props.selectedSource,
      props.updatedTimeRange?.some(Boolean),
    ].filter(Boolean).length + props.selectedTagIds.length,
)

const fileTypeOptions = computed(() => [
  { label: t('knowledgeBase.allFileTypes'), value: '' },
  { label: 'PDF', value: 'pdf' },
  { label: 'DOCX', value: 'docx' },
  { label: 'DOC', value: 'doc' },
  { label: 'PPTX', value: 'pptx' },
  { label: 'PPT', value: 'ppt' },
  { label: 'EPUB', value: 'epub' },
  { label: 'MHTML', value: 'mhtml' },
  { label: 'TXT', value: 'txt' },
  { label: 'MD', value: 'md' },
  { label: 'URL', value: 'url' },
  { label: t('knowledgeBase.typeManual'), value: 'manual' },
  { label: 'MP3', value: 'mp3' },
  { label: 'WAV', value: 'wav' },
  { label: 'M4A', value: 'm4a' },
  { label: 'FLAC', value: 'flac' },
  { label: 'OGG', value: 'ogg' },
])

const parseStatusOptions = computed(() => [
  { label: t('knowledgeBase.allParseStatuses'), value: '' },
  { label: t('knowledgeBase.parseStatusPending'), value: 'pending' },
  { label: t('knowledgeBase.parseStatusProcessing'), value: 'processing' },
  { label: t('knowledgeBase.parseStatusCompleted'), value: 'completed' },
  { label: t('knowledgeBase.parseStatusFailed'), value: 'failed' },
  { label: t('knowledgeBase.parseStatusCancelled'), value: 'cancelled' },
  { label: t('knowledgeBase.parseStatusFinalizing'), value: 'finalizing' },
  { label: t('knowledgeBase.parseStatusDraft'), value: 'draft' },
])

const sourceOptions = computed(() => [
  { label: t('knowledgeBase.allSources'), value: '' },
  { label: t('knowledgeBase.sourceUpload'), value: 'web' },
  { label: t('knowledgeBase.sourceUrl'), value: 'url' },
  { label: t('knowledgeBase.sourceManual'), value: 'manual' },
  { label: t('knowledgeBase.sourceApi'), value: 'api' },
  { label: t('knowledgeBase.sourceBrowserExtension'), value: 'browser_extension' },
  { label: t('knowledgeBase.channelFeishu'), value: 'feishu' },
  { label: t('knowledgeBase.channelFeishuDrive'), value: 'feishu_drive' },
  { label: t('knowledgeBase.channelNotion'), value: 'notion' },
  { label: t('knowledgeBase.channelYuque'), value: 'yuque' },
  { label: t('knowledgeBase.channelConfluence'), value: 'confluence' },
  { label: t('knowledgeBase.channelGitLab'), value: 'gitlab' },
  { label: t('knowledgeBase.channelSeafile'), value: 'seafile' },
  { label: t('knowledgeBase.channelNextcloud'), value: 'nextcloud' },
  { label: t('knowledgeBase.channelIma'), value: 'ima' },
  { label: t('knowledgeBase.channelWechat'), value: 'wechat' },
  { label: t('knowledgeBase.channelWecom'), value: 'wecom' },
  { label: t('knowledgeBase.channelDingtalk'), value: 'dingtalk' },
  { label: t('knowledgeBase.channelSlack'), value: 'slack' },
  { label: t('knowledgeBase.channelIm'), value: 'im' },
])

function documentSortOptionLabel(option: DocumentSortOption) {
  switch (option.labelKey) {
    case 'earliestUpdated':
      return t('knowledgeBase.sort.earliestUpdated')
    case 'newestCreated':
      return t('knowledgeBase.sort.newestCreated')
    case 'earliestCreated':
      return t('knowledgeBase.sort.earliestCreated')
    case 'nameAscending':
      return t('knowledgeBase.sort.nameAscending')
    case 'nameDescending':
      return t('knowledgeBase.sort.nameDescending')
    default:
      return t('knowledgeBase.sort.recentlyUpdated')
  }
}

const documentSortGroups = computed(() => [
  {
    key: 'updated_at',
    label: t('knowledgeBase.sort.updatedTime'),
    description: t('knowledgeBase.sort.updatedTimeDescription'),
    options: DOCUMENT_SORT_OPTIONS.filter((option) => option.sortBy === 'updated_at'),
  },
  {
    key: 'created_at',
    label: t('knowledgeBase.sort.createdTime'),
    description: t('knowledgeBase.sort.createdTimeDescription'),
    options: DOCUMENT_SORT_OPTIONS.filter((option) => option.sortBy === 'created_at'),
  },
  {
    key: 'file_name',
    label: t('knowledgeBase.sort.fileName'),
    description: t('knowledgeBase.sort.fileNameDescription'),
    options: DOCUMENT_SORT_OPTIONS.filter((option) => option.sortBy === 'file_name'),
  },
])

const activeDocumentSortLabel = computed(() => {
  const option = getDocumentSortOption(props.selectedDocumentSort)
  const group = documentSortGroups.value.find((item) => item.key === option.sortBy)
  return `${group?.label || ''} · ${documentSortOptionLabel(option)}`
})

function onSelectSort(value: DocumentSortValue) {
  documentSortPanelVisible.value = false
  emit('select-sort', value)
}

function openUrlDialog() {
  uploadSourceRef.value?.openUrlDialog()
}

defineExpose({ openUrlDialog })
</script>

<style scoped lang="less">
@import '../knowledge-base.less';
</style>
