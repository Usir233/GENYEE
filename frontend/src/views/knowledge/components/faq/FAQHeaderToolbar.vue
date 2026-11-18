<template>
  <div class="faq-header">
    <div class="faq-header-title">
      <div class="faq-title-row">
        <h2 class="faq-breadcrumb">
          <button type="button" class="breadcrumb-link" @click="emit('navigate-kb-list')">
            {{ $t('menu.knowledgeBase') }}
          </button>
          <t-icon name="chevron-right" class="breadcrumb-separator" />
          <KBSwitcherDropdown
            v-if="knowledgeList.length"
            :kb-list="knowledgeList"
            :current-kb-id="kbId"
            @select="(id: string) => emit('switch-kb', id)"
          >
            <button type="button" class="breadcrumb-link dropdown" :disabled="!kbId">
              <template v-if="!kbInfo">
                <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '20px' }]" />
              </template>
              <template v-else>
                <span>{{ kbInfo.name }}</span>
                <t-icon name="chevron-down" />
              </template>
            </button>
          </KBSwitcherDropdown>
          <button
            v-else
            type="button"
            class="breadcrumb-link"
            :disabled="!kbId"
            @click="emit('navigate-current-kb')"
          >
            <template v-if="!kbInfo">
              <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '20px' }]" />
            </template>
            <template v-else>
              {{ kbInfo.name }}
            </template>
          </button>
          <t-icon name="chevron-right" class="breadcrumb-separator" />
          <span class="breadcrumb-current">{{ $t('knowledgeEditor.faq.title') }}</span>
        </h2>

        <div class="kb-title-actions">
          <KBInfoPopover v-if="kbInfo && !isLiteMode" :kb-info="kbInfo" />
          <t-tooltip v-if="canManage" :content="$t('knowledgeBase.settings')" placement="top">
            <button
              type="button"
              class="kb-settings-button"
              :aria-label="$t('knowledgeBase.settings')"
              @click="emit('open-kb-settings')"
            >
              <t-icon name="setting" size="16px" />
            </button>
          </t-tooltip>

          <div
            v-if="showImportResultBadge && importResult"
            class="faq-import-host"
            :class="{ 'is-expanded': importResultExpanded }"
          >
            <button
              type="button"
              class="faq-import-trigger"
              :aria-label="$t('faqManager.import.recentResult')"
              :aria-expanded="importResultExpanded"
              @click.stop="emit('update:importResultExpanded', !importResultExpanded)"
            >
              <t-icon name="check-circle-filled" size="16px" />
            </button>
            <div class="faq-import-panel">
              <div class="faq-import-strip faq-import-strip--result faq-import-strip--panel">
                <span class="faq-import-strip__text">{{ importResultSummary }}</span>
                <t-tag
                  size="small"
                  variant="light"
                  :theme="importResult.import_mode === 'append' ? 'primary' : 'warning'"
                >
                  {{
                    importResult.import_mode === 'append'
                      ? $t('faqManager.import.appendMode')
                      : $t('faqManager.import.replaceMode')
                  }}
                </t-tag>
                <t-button
                  v-if="importResult.failed_entries_url && importResult.failed_count > 0"
                  variant="text"
                  theme="danger"
                  size="small"
                  class="faq-import-strip__link"
                  @click="emit('download-failed-entries')"
                >
                  {{ $t('faqManager.import.downloadReasons') }}
                </t-button>
                <span class="faq-import-strip__time">{{ formatImportTime(importResult.imported_at) }}</span>
                <button
                  type="button"
                  class="faq-import-strip__close"
                  :aria-label="$t('common.close')"
                  @click="emit('close-import-result')"
                >
                  <t-icon name="close" size="14px" />
                </button>
              </div>
            </div>
          </div>

          <div
            v-else-if="isImportInProgress && importTaskStatus"
            class="faq-import-strip faq-import-strip--in-title"
            :class="`faq-import-strip--${importTaskStatus.status}`"
          >
            <t-icon
              :name="importProgressIcon"
              size="16px"
              class="faq-import-strip__icon"
              :class="{ 'is-spinning': importTaskStatus.status === 'running' }"
            />
            <span class="faq-import-strip__text">{{ importProgressText }}</span>
            <div class="faq-import-strip__bar">
              <div class="faq-import-strip__bar-fill" :style="{ width: `${importTaskStatus.progress}%` }" />
            </div>
            <span class="faq-import-strip__count">{{ importTaskStatus.processed }}/{{ importTaskStatus.total }}</span>
          </div>
        </div>
      </div>
      <p class="faq-subtitle">{{ $t('knowledgeEditor.faq.subtitle') }}</p>
    </div>
  </div>

  <div class="faq-filter-bar">
    <t-input
      :model-value="searchKeyword"
      :placeholder="$t('knowledgeEditor.faq.searchPlaceholder')"
      clearable
      class="faq-search-input"
      @update:model-value="(val: string | number) => emit('update:searchKeyword', String(val ?? '').trim())"
      @clear="emit('reload-entries')"
      @enter="emit('reload-entries')"
    >
      <template #prefix-icon>
        <t-icon name="search" size="16px" />
      </template>
    </t-input>
    <div class="faq-filter-bar__filters">
      <KnowledgeTagFilter
        :search="tagSearchQuery"
        :cleared="tagFilterCleared"
        :tags="tagList"
        :selected-ids="selectedTagIds"
        :total="tagTotal"
        :loading="tagLoading"
        :loading-more="tagLoadingMore"
        :has-more="tagHasMore"
        :can-manage="canEdit"
        variant="faq"
        @update:search="(val: string) => emit('update:tagSearchQuery', val)"
        @update:cleared="(val: boolean) => emit('update:tagFilterCleared', val)"
        @change="(ids: string[]) => emit('tag-filter-change', ids)"
        @load-more="emit('load-more-tags')"
        @manage="emit('open-tag-manage')"
      />
    </div>
    <div class="faq-filter-bar__trailing">
      <div class="faq-view-toggle" role="group" :aria-label="$t('knowledgeBase.viewModeToggle')">
        <t-tooltip :content="$t('knowledgeBase.viewModeGrid')" placement="top">
          <button
            type="button"
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
            :class="{ active: viewMode === 'list' }"
            :aria-label="$t('knowledgeBase.viewModeList')"
            :aria-pressed="viewMode === 'list'"
            @click="emit('update:viewMode', 'list')"
          >
            <t-icon name="view-list" size="16px" />
          </button>
        </t-tooltip>
      </div>

      <template v-if="faqCreateOptions.length">
        <t-tooltip :content="$t('knowledgeEditor.faq.createGroup')" placement="top">
          <t-dropdown
            :options="faqCreateOptions"
            trigger="click"
            placement="bottom-right"
            @click="(data: { value?: string | number }) => emit('faq-action', String(data.value || ''))"
          >
            <t-button
              variant="text"
              theme="default"
              class="content-bar-icon-btn"
              size="small"
              :aria-label="$t('knowledgeEditor.faq.createGroup')"
            >
              <template #icon><t-icon name="add" size="16px" /></template>
            </t-button>
          </t-dropdown>
        </t-tooltip>
      </template>

      <t-dropdown
        :options="faqExportOptions"
        trigger="click"
        placement="bottom-right"
        @click="(data: { value?: string | number }) => emit('faq-action', String(data.value || ''))"
      >
        <t-tooltip :content="$t('knowledgeEditor.faqExport.exportButton')" placement="top">
          <t-button
            variant="text"
            theme="default"
            class="content-bar-icon-btn"
            size="small"
            :loading="exportLoading"
            :aria-label="$t('knowledgeEditor.faqExport.exportButton')"
          >
            <template #icon><t-icon name="download" size="16px" /></template>
          </t-button>
        </t-tooltip>
      </t-dropdown>

      <t-tooltip :content="$t('knowledgeEditor.faq.searchTest')" placement="top">
        <t-button
          variant="text"
          theme="default"
          class="content-bar-icon-btn"
          size="small"
          :aria-label="$t('knowledgeEditor.faq.searchTest')"
          @click="emit('faq-action', 'search')"
        >
          <template #icon><t-icon name="search" size="16px" /></template>
        </t-button>
      </t-tooltip>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, h, type VNode } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon as TIcon } from 'tdesign-vue-next'
import KBInfoPopover from '@/components/KBInfoPopover.vue'
import KBSwitcherDropdown from '@/components/KBSwitcherDropdown.vue'
import KnowledgeTagFilter from '../KnowledgeTagFilter.vue'
import type {
  FAQDropdownItem,
  FAQKnowledgeBaseInfo,
  FAQKnowledgeSwitchOption,
  FAQTagItem,
  FAQViewMode,
} from '../../composables/useFAQEntries'
import type { FAQImportResultView, FAQImportTaskStatus } from '../../composables/useFAQImportExport'

interface CreateActionOption extends FAQDropdownItem {
  prefixIcon: () => VNode
}

const props = defineProps<{
  kbId: string
  kbInfo: FAQKnowledgeBaseInfo | null
  knowledgeList: FAQKnowledgeSwitchOption[]
  isLiteMode: boolean
  canEdit: boolean
  canManage: boolean
  showImportResultBadge: boolean
  importResult: FAQImportResultView | null
  importResultExpanded: boolean
  importResultSummary: string
  isImportInProgress: boolean
  importTaskStatus: FAQImportTaskStatus | null
  importProgressIcon: string
  importProgressText: string
  formatImportTime: (timeStr?: string) => string
  searchKeyword: string
  tagSearchQuery: string
  tagFilterCleared: boolean
  tagList: FAQTagItem[]
  selectedTagIds: string[]
  tagTotal: number
  tagLoading: boolean
  tagLoadingMore: boolean
  tagHasMore: boolean
  viewMode: FAQViewMode
  exportLoading: boolean
}>()

const emit = defineEmits<{
  (e: 'navigate-kb-list'): void
  (e: 'navigate-current-kb'): void
  (e: 'switch-kb', kbId: string): void
  (e: 'open-kb-settings'): void
  (e: 'update:importResultExpanded', val: boolean): void
  (e: 'download-failed-entries'): void
  (e: 'close-import-result'): void
  (e: 'update:searchKeyword', val: string): void
  (e: 'reload-entries'): void
  (e: 'update:tagSearchQuery', val: string): void
  (e: 'update:tagFilterCleared', val: boolean): void
  (e: 'tag-filter-change', ids: string[]): void
  (e: 'load-more-tags'): void
  (e: 'open-tag-manage'): void
  (e: 'update:viewMode', mode: FAQViewMode): void
  (e: 'faq-action', action: string): void
}>()

const { t } = useI18n()

const faqExportOptions = computed<FAQDropdownItem[]>(() => [
  { content: t('knowledgeEditor.faqExport.exportCSV'), value: 'export_csv' },
  { content: t('knowledgeEditor.faqExport.exportJSON'), value: 'export_json' },
])

const faqCreateOptions = computed<CreateActionOption[]>(() => {
  if (!props.canEdit) return []
  return [
    {
      content: t('knowledgeEditor.faq.editorCreate'),
      value: 'create',
      prefixIcon: () => h(TIcon, { name: 'add', size: '16px' }),
    },
    {
      content: t('knowledgeEditor.faqImport.importButton'),
      value: 'import',
      prefixIcon: () => h(TIcon, { name: 'upload', size: '16px' }),
    },
  ]
})
</script>
