<template>
  <aside class="wiki-sidebar">
    <div class="wiki-sidebar-header">
      <div v-if="stats && (stats.pending_tasks > 0 || stats.is_active)" class="wiki-queue-status">
        <t-loading size="small" />
        <span class="queue-text">
          {{ $t('knowledgeEditor.wikiBrowser.queueStatus', { count: stats.pending_tasks || 0 }) }}
        </span>
      </div>

      <button
        v-if="stats && stats.pending_issues > 0"
        type="button"
        class="wiki-global-issues-status"
        @click="emit('open-global-issues')"
      >
        <t-icon name="error-circle" class="wiki-warning-icon" />
        <span class="queue-text">
          {{ $t('knowledgeEditor.wikiBrowser.globalIssuesCount', { count: stats.pending_issues }) }}
        </span>
      </button>

      <t-input
        :model-value="searchQuery"
        :placeholder="$t('knowledgeEditor.wikiBrowser.searchPlaceholder')"
        clearable
        @update:model-value="(v: string | number) => emit('update:searchQuery', String(v ?? ''))"
        @enter="emit('search')"
        @clear="emit('clear-search')"
      >
        <template #prefixIcon><t-icon name="search" /></template>
      </t-input>
    </div>

    <div :ref="bindPageListEl" class="wiki-page-list">
      <template v-if="searchResults !== null">
        <button
          v-for="page in searchResults"
          :key="page.id"
          type="button"
          :class="['wiki-page-item', 'wiki-page-item--btn', { active: selectedPageId === page.id }]"
          @click="emit('select-page', page)"
        >
          <span class="wiki-page-item-title">{{ page.title }}</span>
          <span class="wiki-page-item-summary">{{ page.summary }}</span>
          <span class="wiki-page-item-meta">
            <span>{{ formatDate(page.updated_at) }}</span>
          </span>
        </button>
        <div v-if="searchResults.length === 0 && !loading" class="wiki-empty-state">
          <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.searchNoResults') }}</p>
        </div>
      </template>

      <template v-else>
        <button
          v-if="indexAvailable"
          type="button"
          :class="['wiki-nav-item', { active: activeSystemView === 'index' }]"
          @click="emit('open-index')"
        >
          <t-icon name="catalog" class="wiki-nav-icon" />
          <span class="wiki-nav-text">{{ $t('knowledgeEditor.wikiBrowser.indexTitle') }}</span>
        </button>

        <div v-if="indexAvailable" class="wiki-sidebar-divider"></div>

        <div v-if="visibleTabs.length > 0 || activeGroup" class="wiki-tree-panel">
          <div v-if="visibleTabs.length > 0" class="wiki-tab-bar">
            <div class="wiki-tab-bar-scroll" role="tablist">
              <button
                v-for="tab in visibleTabs"
                :key="tab.type"
                type="button"
                role="tab"
                :aria-selected="activeTab === tab.type"
                :class="['wiki-tab', { active: activeTab === tab.type }]"
                @click="emit('select-tab', tab.type)"
              >
                <span class="wiki-tab-label">{{ tab.label }}</span>
                <span class="wiki-tab-count">{{ tab.total }}</span>
              </button>
            </div>
            <div class="wiki-tab-bar-actions">
              <div
                class="wiki-view-toggle"
                role="group"
                :aria-label="$t('knowledgeEditor.wikiBrowser.viewModeToggle')"
              >
                <t-tooltip :content="$t('knowledgeEditor.wikiBrowser.viewTree')" placement="top">
                  <button
                    type="button"
                    class="wiki-view-toggle-btn"
                    :class="{ active: sidebarViewMode === 'tree' }"
                    :aria-pressed="sidebarViewMode === 'tree'"
                    :aria-label="$t('knowledgeEditor.wikiBrowser.viewTree')"
                    :disabled="sidebarViewSwitching"
                    @click="emit('switch-view-mode', 'tree')"
                  >
                    <t-icon name="tree-list" />
                  </button>
                </t-tooltip>
                <t-tooltip :content="$t('knowledgeEditor.wikiBrowser.viewList')" placement="top">
                  <button
                    type="button"
                    class="wiki-view-toggle-btn"
                    :class="{ active: sidebarViewMode === 'list' }"
                    :aria-pressed="sidebarViewMode === 'list'"
                    :aria-label="$t('knowledgeEditor.wikiBrowser.viewList')"
                    :disabled="sidebarViewSwitching"
                    @click="emit('switch-view-mode', 'list')"
                  >
                    <t-icon name="view-list" />
                  </button>
                </t-tooltip>
              </div>
              <t-tooltip
                v-if="canEdit"
                :content="$t('knowledgeEditor.wikiBrowser.newRootFolder')"
                placement="top"
              >
                <button
                  type="button"
                  class="wiki-tab-bar-action"
                  :disabled="sidebarViewMode !== 'tree' || sidebarViewSwitching || sidebarTabSwitching"
                  :aria-label="$t('knowledgeEditor.wikiBrowser.newRootFolder')"
                  @click.stop="emit('start-create-root-folder')"
                >
                  <t-icon name="folder-add" />
                </button>
              </t-tooltip>
              <t-tooltip
                v-if="canEdit"
                :content="$t('knowledgeEditor.wikiBrowser.newPageBtn')"
                placement="top"
              >
                <button
                  type="button"
                  class="wiki-tab-bar-action"
                  :aria-label="$t('knowledgeEditor.wikiBrowser.newPageBtn')"
                  @click.stop="emit('open-create-page')"
                >
                  <t-icon name="file-add" />
                </button>
              </t-tooltip>
            </div>
          </div>

          <template v-if="activeGroup && sidebarViewMode === 'tree'">
            <div
              :ref="bindTreeListEl"
              role="tree"
              :class="['wiki-tree-list', { 'wiki-tree-list--root-drop': dropTargetKey === '__root__' }]"
              @dragover.prevent="emit('root-drag-over', $event)"
              @dragleave="emit('directory-drag-leave', '__root__')"
              @drop.prevent="emit('drop-directory', $event, '', [])"
            >
              <div
                v-if="creatingRootFolder"
                class="wiki-directory-item wiki-directory-item--editing"
                :style="{ '--wiki-tree-depth': 0 }"
                @click.stop
              >
                <input
                  ref="creatingRootInputRef"
                  :value="creatingRootFolderName"
                  class="wiki-directory-rename-input"
                  :placeholder="$t('knowledgeEditor.wikiBrowser.folderNamePlaceholder')"
                  @input="onRootFolderNameInput"
                  @keydown.enter="emit('submit-create-root-folder')"
                  @keydown.esc="emit('cancel-create-root-folder')"
                />
                <div class="wiki-tree-trailing wiki-folder-inline-actions">
                  <t-button
                    variant="text"
                    theme="default"
                    size="small"
                    class="wiki-folder-action-btn confirm"
                    :aria-label="$t('common.confirm')"
                    @click.stop="emit('submit-create-root-folder')"
                  >
                    <t-icon name="check" size="16px" />
                  </t-button>
                  <t-button
                    variant="text"
                    theme="default"
                    size="small"
                    class="wiki-folder-action-btn cancel"
                    :aria-label="$t('common.cancel')"
                    @click.stop="emit('cancel-create-root-folder')"
                  >
                    <t-icon name="close" size="16px" />
                  </t-button>
                </div>
              </div>

              <template v-for="item in activeTreeRows" :key="item.rowKey">
                <div
                  v-if="item.kind === 'directory'"
                  role="treeitem"
                  tabindex="0"
                  :aria-expanded="!item.collapsed"
                  :class="['wiki-directory-item', { 'wiki-directory-item--drop': dropTargetKey === item.pathKey }]"
                  :style="{ '--wiki-tree-depth': item.depth }"
                  :draggable="Boolean(canEdit) && editingFolderId !== item.folderId"
                  @click="emit('toggle-directory', item.pathKey)"
                  @keydown.enter.self.prevent="emit('toggle-directory', item.pathKey)"
                  @keydown.space.self.prevent="emit('toggle-directory', item.pathKey)"
                  @dragstart="emit('folder-drag-start', $event, item.folderId, item.path)"
                  @dragend="emit('drag-end')"
                  @dragover.prevent.stop="emit('directory-drag-over', $event, item.pathKey)"
                  @dragleave.stop="emit('directory-drag-leave', item.pathKey)"
                  @drop.prevent.stop="emit('drop-directory', $event, item.folderId, item.path)"
                >
                  <t-icon :name="item.collapsed ? 'chevron-right' : 'chevron-down'" class="wiki-directory-toggle" />
                  <input
                    v-if="editingFolderId === item.folderId"
                    :value="editingName"
                    class="wiki-directory-rename-input"
                    :placeholder="$t('knowledgeEditor.wikiBrowser.folderNamePlaceholder')"
                    @click.stop
                    @input="onEditingNameInput"
                    @keydown.enter="emit('commit-rename-folder', item.folderId, item.label)"
                    @keydown.esc="emit('cancel-rename-folder')"
                    @blur="emit('commit-rename-folder', item.folderId, item.label)"
                  />
                  <template v-else>
                    <span class="wiki-directory-title">{{ item.label }}</span>
                    <div class="wiki-tree-trailing">
                      <span class="wiki-directory-count">{{ item.count }}</span>
                      <WikiFolderActions
                        v-if="canEdit && item.folderId"
                        :name="item.label"
                        :page-count="item.count"
                        :has-children="item.hasChildren"
                        @create="(name: string) => emit('create-folder', item.folderId, item.path, name)"
                        @rename="() => emit('start-rename-folder', item.folderId, item.label)"
                        @delete="() => emit('delete-folder', item.folderId)"
                      />
                    </div>
                  </template>
                </div>

                <button
                  v-else-if="item.kind === 'load-more'"
                  type="button"
                  class="wiki-directory-load-more"
                  :data-loadmore-key="item.rowKey"
                  :style="{ '--wiki-tree-depth': item.depth }"
                  @click="emit('load-more-directory', item.type, item.path)"
                >
                  <t-loading v-if="item.loading" size="small" />
                  <template v-else>
                    <t-icon name="chevron-down" />
                    <span>{{ $t('knowledgeEditor.wikiBrowser.loadMoreShort') }}</span>
                  </template>
                </button>

                <button
                  v-else
                  type="button"
                  :class="['wiki-page-item', 'wiki-page-item--tree', { active: selectedPageId === item.page.id }]"
                  :style="{ '--wiki-tree-depth': item.depth }"
                  :title="item.page.title"
                  :draggable="Boolean(canEdit)"
                  @click="emit('select-page', item.page)"
                  @dragstart="emit('page-drag-start', $event, item.page)"
                  @dragend="emit('drag-end')"
                >
                  <t-icon
                    :name="getPageIcon(item.page)"
                    :class="['wiki-page-file-icon', `wiki-page-file-icon--${item.page.page_type}`]"
                  />
                  <span class="wiki-page-item-title">{{ item.page.title }}</span>
                </button>
              </template>
            </div>

            <div
              v-if="activeGroup.hasMore"
              :ref="bindGroupSentinelEl"
              class="wiki-group-sentinel"
              :data-type="activeGroup.type"
            ></div>
            <div v-if="activeGroup.loading" class="wiki-group-loading">
              <t-loading size="small" />
            </div>
          </template>

          <template v-else-if="activeGroup">
            <RecycleScroller
              v-slot="{ item }"
              class="wiki-group-scroller"
              :items="activeFlatPages"
              :item-size="WIKI_PAGE_ITEM_HEIGHT"
              key-field="id"
              :buffer="400"
              page-mode
            >
              <button
                type="button"
                :class="[
                  'wiki-page-item',
                  'wiki-page-item--list',
                  'wiki-page-item--btn',
                  { active: selectedPageId === item.id },
                ]"
                @click="emit('select-page', item)"
              >
                <span class="wiki-page-item-title">{{ item.title }}</span>
                <span class="wiki-page-item-summary">{{ item.summary }}</span>
                <span class="wiki-page-item-meta">
                  <span>{{ formatDate(item.updated_at) }}</span>
                </span>
              </button>
            </RecycleScroller>
            <div
              v-if="activeFlatState?.hasMore"
              :ref="bindGroupSentinelEl"
              class="wiki-group-sentinel"
              :data-type="activeGroup.type"
            ></div>
            <div v-if="activeFlatState?.loading" class="wiki-group-loading">
              <t-loading size="small" />
            </div>
          </template>
        </div>

        <div v-if="!hasContentPages && !loading" class="wiki-empty-state">
          <div class="wiki-empty-icon">
            <t-icon name="file-unknown" size="36px" />
          </div>
          <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.emptyTitle') }}</p>
          <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.emptyDesc') }}</p>
        </div>
      </template>
    </div>

    <Teleport to="body">
      <div v-if="pendingMove" class="wiki-move-confirm-mask" @click="emit('cancel-pending-move')">
        <div
          class="wiki-move-confirm anchored-form-popup-card"
          :style="{ left: `${pendingMove.x}px`, top: `${pendingMove.y}px` }"
          @click.stop
        >
          <div class="anchored-form-popup-title">
            {{ $t('knowledgeEditor.wikiBrowser.moveConfirmTitle') }}
          </div>
          <div class="anchored-form-popup-body">
            {{ $t('knowledgeEditor.wikiBrowser.moveConfirm', { target: pendingMove.targetLabel }) }}
          </div>
          <div class="anchored-form-popup-footer">
            <t-button variant="outline" @click="emit('cancel-pending-move')">
              {{ $t('common.cancel') }}
            </t-button>
            <t-button theme="primary" @click="emit('confirm-pending-move')">
              {{ $t('common.confirm') }}
            </t-button>
          </div>
        </div>
      </div>
    </Teleport>
  </aside>
</template>

<script setup lang="ts">
import { nextTick, ref, watch, type ComponentPublicInstance } from 'vue'
import { RecycleScroller } from 'vue-virtual-scroller'
import type { WikiPage, WikiStats } from '@/api/wiki'
import WikiFolderActions from '../WikiFolderActions.vue'
import {
  WIKI_PAGE_ITEM_HEIGHT,
  type SidebarViewMode,
  type WikiPendingMove,
  type WikiTreeRow,
} from '../composables/useWikiDirectoryTree'
import { formatWikiDate } from '../composables/useWikiPageEditor'

const props = defineProps<{
  stats: WikiStats | null
  canEdit?: boolean
  loading: boolean
  searchQuery: string
  searchResults: WikiPage[] | null
  selectedPageId: string | undefined
  indexAvailable: boolean
  activeSystemView: '' | 'index'
  visibleTabs: Array<{ type: string; label: string; total: number }>
  activeTab: string
  activeGroup: { type: string; label: string; pages: WikiPage[]; total: number; loading: boolean; hasMore: boolean } | null
  sidebarViewMode: SidebarViewMode
  sidebarViewSwitching: boolean
  sidebarTabSwitching: boolean
  activeTreeRows: WikiTreeRow[]
  activeFlatPages: WikiPage[]
  activeFlatState: { loading: boolean; hasMore: boolean } | null
  hasContentPages: boolean
  dropTargetKey: string
  pendingMove: WikiPendingMove | null
  creatingRootFolder: boolean
  creatingRootFolderName: string
  editingFolderId: string
  editingName: string
  getPageIcon: (page: Pick<WikiPage, 'page_type'>) => string
  setPageListEl: (el: HTMLElement | null) => void
  setTreeListEl: (el: HTMLElement | null) => void
  setGroupSentinelEl: (el: HTMLElement | null) => void
}>()

const emit = defineEmits<{
  (e: 'open-global-issues'): void
  (e: 'update:searchQuery', val: string): void
  (e: 'search'): void
  (e: 'clear-search'): void
  (e: 'open-index'): void
  (e: 'select-page', page: WikiPage): void
  (e: 'select-tab', tab: string): void
  (e: 'switch-view-mode', mode: SidebarViewMode): void
  (e: 'start-create-root-folder'): void
  (e: 'update:creatingRootFolderName', val: string): void
  (e: 'submit-create-root-folder'): void
  (e: 'cancel-create-root-folder'): void
  (e: 'open-create-page'): void
  (e: 'toggle-directory', pathKey: string): void
  (e: 'load-more-directory', type: string, path: string[]): void
  (e: 'create-folder', parentId: string, parentPath: string[], name: string): void
  (e: 'start-rename-folder', folderId: string, label: string): void
  (e: 'update:editingName', val: string): void
  (e: 'commit-rename-folder', folderId: string, label: string): void
  (e: 'cancel-rename-folder'): void
  (e: 'delete-folder', folderId: string): void
  (e: 'page-drag-start', event: DragEvent, page: WikiPage): void
  (e: 'folder-drag-start', event: DragEvent, folderId: string, path: string[]): void
  (e: 'drag-end'): void
  (e: 'directory-drag-over', event: DragEvent, pathKey: string): void
  (e: 'root-drag-over', event: DragEvent): void
  (e: 'directory-drag-leave', pathKey: string): void
  (e: 'drop-directory', event: DragEvent, folderId: string, path: string[]): void
  (e: 'cancel-pending-move'): void
  (e: 'confirm-pending-move'): void
}>()

const creatingRootInputRef = ref<HTMLInputElement | null>(null)

watch(
  () => props.creatingRootFolder,
  (creating) => {
    if (creating) {
      void nextTick(() => creatingRootInputRef.value?.focus())
    }
  },
)

function bindPageListEl(el: Element | ComponentPublicInstance | null) {
  props.setPageListEl(el instanceof HTMLElement ? el : null)
}

function bindTreeListEl(el: Element | ComponentPublicInstance | null) {
  props.setTreeListEl(el instanceof HTMLElement ? el : null)
}

function bindGroupSentinelEl(el: Element | ComponentPublicInstance | null) {
  props.setGroupSentinelEl(el instanceof HTMLElement ? el : null)
}

function onRootFolderNameInput(event: Event) {
  emit('update:creatingRootFolderName', (event.target as HTMLInputElement).value)
}

function onEditingNameInput(event: Event) {
  emit('update:editingName', (event.target as HTMLInputElement).value)
}

function formatDate(dateStr: string): string {
  return formatWikiDate(dateStr)
}
</script>

<style lang="less">
.wiki-sidebar {
  width: 280px;
  min-width: 240px;
  border-right: 1px solid var(--gy-border-subtle);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  background: var(--gy-surface);
}

.wiki-sidebar-header {
  padding: 0 10px 8px 8px;
  margin-left: -8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.wiki-queue-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: var(--gy-surface-subtle);
  border-radius: var(--gy-radius-sm);
  color: var(--gy-text-secondary);
  font-size: var(--gy-text-md);

  .queue-text {
    line-height: 1.2;
  }
}

.wiki-page-list {
  flex: 1;
  overflow-y: auto;
  padding: 0 8px 12px 8px;
  margin-left: -8px;
}

.wiki-tree-list {
  padding: 0 0 4px;
}

.wiki-tree-panel {
  --wiki-tree-depth-indent: 14px;
  padding: 0;
}

.wiki-tab-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0 6px;
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--gy-surface);
}

.wiki-tab-bar-scroll {
  display: flex;
  gap: 16px;
  min-width: 0;
  flex: 1;
  overflow-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.wiki-tab-bar-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
}

.wiki-view-toggle {
  display: inline-flex;
  align-items: center;
  padding: 2px;
  border-radius: var(--gy-radius-sm);
  background: var(--gy-surface);
  border: 1px solid var(--gy-border-subtle);
}

.wiki-view-toggle-btn {
  width: 24px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: var(--gy-radius-xs);
  background: transparent;
  color: var(--gy-text-secondary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background-color var(--gy-motion-instant) ease, color var(--gy-motion-instant) ease;

  &:hover {
    color: var(--gy-text-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }

  &.active {
    color: var(--gy-brand);
    background: var(--gy-surface);
    box-shadow: var(--gy-shadow-xs);
  }

  .t-icon {
    font-size: var(--gy-text-lg);
  }
}

.wiki-tab-bar-action {
  width: 26px;
  height: 26px;
  padding: 0;
  border: 0;
  border-radius: var(--gy-radius-sm);
  background: transparent;
  color: var(--gy-text-secondary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  cursor: pointer;
  transition: background-color var(--gy-motion-fast) ease, color var(--gy-motion-fast) ease;

  .t-icon {
    font-size: var(--gy-text-lg);
  }

  &:hover {
    color: var(--gy-brand);
    background: var(--gy-surface-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }

  &:disabled {
    color: var(--gy-text-placeholder);
    background: transparent;
    cursor: not-allowed;
    opacity: 0.45;
  }
}

.wiki-tree-trailing {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 2px;
  margin-left: auto;
  flex-shrink: 0;
}

.wiki-group-sentinel {
  height: 1px;
  width: 100%;
}

.wiki-group-loading {
  display: flex;
  justify-content: center;
  padding: 6px 0;
}

.wiki-nav-item {
  width: 100%;
  border: 0;
  background: transparent;
  font: inherit;
  text-align: left;
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: var(--wiki-list-row-min-height);
  padding: 0 10px 0 var(--wiki-list-inset-x);
  border-radius: var(--wiki-list-row-radius);
  cursor: pointer;
  margin-bottom: 0;
  transition: background var(--gy-motion-fast) ease, color var(--gy-motion-fast) ease;

  &:hover {
    background: var(--gy-surface-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }

  &.active {
    background: var(--gy-surface-hover);

    .wiki-nav-text {
      color: var(--gy-brand);
      font-weight: 400;
    }

    .wiki-nav-icon {
      color: var(--gy-brand);
    }
  }

  .wiki-nav-icon {
    font-size: var(--gy-text-xl);
    color: var(--gy-text-secondary);
  }

  .wiki-nav-text {
    font-size: var(--gy-text-base);
    font-weight: 400;
    line-height: 20px;
    color: var(--gy-text-primary);
  }
}

.wiki-sidebar-divider {
  height: 1px;
  background: var(--gy-border-subtle);
  margin: 6px 0;
}

.wiki-tab {
  border: 0;
  background: transparent;
  font: inherit;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 7px 2px 8px;
  border-radius: 0;
  font-size: var(--gy-text-md);
  color: var(--gy-text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  position: relative;
  transition: background var(--gy-motion-fast), color var(--gy-motion-fast);

  &:hover {
    color: var(--gy-text-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }

  &.active {
    color: var(--gy-brand);
    font-weight: 600;

    &::after {
      content: '';
      position: absolute;
      left: 2px;
      right: 2px;
      bottom: 1px;
      height: 2px;
      border-radius: var(--gy-radius-xs);
      background: var(--gy-brand);
    }
  }

  .wiki-tab-count {
    font-size: var(--gy-text-xs);
    padding: 0;
    line-height: 1;
    color: var(--gy-text-placeholder);
  }

  &.active .wiki-tab-count {
    color: var(--gy-brand);
    font-weight: 500;
  }
}

.wiki-page-item {
  width: 100%;
  border: 0;
  background: transparent;
  font: inherit;
  text-align: left;
  min-height: var(--wiki-list-row-min-height);
  box-sizing: border-box;
  overflow: hidden;
  padding: 6px 10px 6px var(--wiki-list-inset-x);
  border-radius: var(--wiki-list-row-radius);
  cursor: pointer;
  margin-bottom: 0;
  user-select: none;
  transition: background var(--gy-motion-fast) ease, color var(--gy-motion-fast) ease;

  &:hover {
    background: var(--gy-surface-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }

  &.active {
    background: var(--gy-surface-hover);

    .wiki-page-item-title {
      color: var(--gy-brand);
    }
  }
}

.wiki-page-item--btn {
  display: flex;
  flex-direction: column;
  align-items: stretch;
}

.wiki-group-scroller {
  min-height: 60px;
  margin: 4px 0;
}

.wiki-page-item--list {
  height: 98px;
  padding: 8px 10px 8px var(--wiki-list-inset-x);
}

.wiki-page-item--list .wiki-page-item-title,
.wiki-page-item--btn .wiki-page-item-title {
  display: block;
  font-size: var(--gy-text-base);
  font-weight: 400;
  line-height: 20px;
  margin-bottom: 4px;
  color: var(--gy-text-primary);
  transition: color var(--gy-motion-fast) ease;
}

.wiki-page-item-summary {
  font-size: var(--gy-text-sm);
  line-height: 1.5;
  color: var(--gy-text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin-bottom: 6px;
}

.wiki-page-item-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: var(--gy-text-xs);
  color: var(--gy-text-placeholder);
}

.wiki-directory-item {
  height: 34px;
  box-sizing: border-box;
  overflow: hidden;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px 0 calc(var(--wiki-tree-depth, 0) * var(--wiki-tree-depth-indent, 14px) + var(--wiki-list-inset-x));
  border-radius: var(--wiki-list-row-radius);
  cursor: pointer;
  margin: 0;
  color: var(--gy-text-secondary);
  background: transparent;
  user-select: none;
  transition: background var(--gy-motion-fast) ease, color var(--gy-motion-fast) ease;

  &:hover {
    background: var(--gy-surface-hover);
    color: var(--gy-text-primary);

    .wiki-directory-action--reveal {
      opacity: 1;
    }
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;

    .wiki-directory-action--reveal {
      opacity: 1;
    }
  }
}

.wiki-directory-rename-input {
  flex: 1;
  min-width: 0;
  height: 24px;
  border: 1px solid var(--gy-brand);
  border-radius: var(--gy-radius-xs);
  padding: 0 6px;
  font-size: var(--gy-text-md);
  color: var(--gy-text-primary);
  background: var(--gy-surface);
  outline: none;
}

.wiki-directory-item--drop {
  background: var(--gy-brand-soft);
  box-shadow: inset 0 0 0 1px var(--gy-brand);
}

.wiki-directory-item--editing {
  cursor: default;

  &:hover {
    background: transparent;
    color: var(--gy-text-secondary);
  }
}

.wiki-folder-inline-actions {
  display: flex;
  gap: 2px;
  flex-shrink: 0;

  .t-button {
    padding: 0 4px;
    height: 24px;
  }

  .wiki-folder-action-btn {
    border-radius: var(--gy-radius-xs);
    transition: background-color var(--gy-motion-base) ease, color var(--gy-motion-base) ease;

    .t-icon {
      font-size: var(--gy-text-base);
    }
  }

  .wiki-folder-action-btn.confirm {
    background: transparent;
    color: var(--gy-text-secondary);

    &:hover {
      background: var(--gy-surface-subtle);
      color: var(--gy-brand);
    }
  }

  .wiki-folder-action-btn.cancel {
    background: transparent;
    color: var(--gy-text-secondary);

    &:hover {
      background: var(--gy-surface-subtle);
      color: var(--gy-danger);
    }
  }
}

.wiki-tree-list--root-drop {
  border-radius: var(--gy-radius-sm);
  box-shadow: inset 0 0 0 1px var(--gy-brand);
}

.wiki-move-confirm-mask {
  position: fixed;
  inset: 0;
  z-index: 3500;
}

.wiki-move-confirm {
  position: fixed;
  transform: translate(8px, 8px);
}

.wiki-directory-toggle,
.wiki-page-file-icon {
  flex: 0 0 auto;
  color: var(--gy-text-placeholder);
  font-size: var(--gy-text-lg);
}

.wiki-directory-title,
.wiki-page-item-title {
  min-width: 0;
  flex: 1;
  font-size: var(--gy-text-md);
  color: var(--gy-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wiki-directory-title {
  font-weight: 600;
}

.wiki-directory-count {
  flex: 0 0 auto;
  min-width: 16px;
  text-align: right;
  font-size: var(--gy-text-xs);
  line-height: 18px;
  color: var(--gy-text-placeholder);
  font-variant-numeric: tabular-nums;
}

.wiki-directory-load-more {
  width: 100%;
  border: 0;
  background: transparent;
  font: inherit;
  text-align: left;
  height: 30px;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: calc(var(--wiki-tree-depth, 0) * var(--wiki-tree-depth-indent, 14px));
  border-radius: var(--gy-radius-sm);
  cursor: pointer;
  margin: 1px 0;
  color: var(--gy-brand);
  font-size: var(--gy-text-sm);

  &:hover {
    background: var(--gy-brand-soft);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }
}

.wiki-page-item--tree {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  min-height: 34px;
  padding: 4px 10px 4px calc(var(--wiki-tree-depth, 0) * var(--wiki-tree-depth-indent, 14px) + var(--wiki-list-inset-x));
  border-radius: var(--wiki-list-row-radius);
  margin: 0;

  &.active {
    .wiki-page-file-icon {
      color: var(--gy-brand);
    }
  }
}

.wiki-page-item--tree .wiki-page-item-title {
  font-size: var(--gy-text-base);
  font-weight: 400;
  line-height: 20px;
  color: var(--gy-text-primary);
  transition: color var(--gy-motion-fast) ease;
}
</style>
