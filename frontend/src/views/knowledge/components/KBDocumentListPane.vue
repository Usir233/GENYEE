<script setup lang="ts">
import { onBeforeUnmount, ref, watch, type CSSProperties } from 'vue'
import EmptyKnowledge from '@/components/EmptyKnowledge.vue'
import AssistantActionsButton from '@/components/assistant/AssistantActionsButton.vue'
import type { AssistantContextAction } from '@/stores/assistant'
import type { FolderEntry } from '../folderTree'
import type { DocumentRowAction, KnowledgeCard, MoveTargetKB } from '../composables/useKBDocumentStatusPoll'
import DocumentBatchBar from './DocumentBatchBar.vue'
import DocumentCardView from './DocumentCardView.vue'
import DocumentListView from './DocumentListView.vue'

defineProps<{
  kbId: string
  cardList: KnowledgeCard[]
  folderOptions: Array<{ path: string; name: string; depth: number }>
  visibleSubFolders: FolderEntry[]
  selectedIds: Set<string>
  batchMode: boolean
  viewMode: 'grid' | 'list'
  docListLoading: boolean
  canEdit: boolean
  canDownloadKnowledge: boolean
  canMutateKnowledge: boolean
  traceAvailableById: Record<string, boolean>
  moveMenuMode: 'normal' | 'targets' | 'confirm'
  moveTargetKbs: MoveTargetKB[]
  moveTargetsLoading: boolean
  moveSelectedTargetName: string
  moveMode: 'reuse_vectors' | 'reparse'
  moveSubmitting: boolean
  showDocumentFolderPath: boolean
  folderEmptyState: 'search' | 'subfolders' | 'folder' | 'none'
  folderSubfolderDocumentCount: number
  docMarqueeVisible: boolean
  docMarqueeMode: string
  docMarqueeBoxStyle: CSSProperties
  batchDeleting: boolean
  batchReparsing: boolean
  batchTagging: boolean
  batchDownloading: boolean
  selectedDocumentAssistantActions: AssistantContextAction[]
}>()

const emit = defineEmits<{
  'scroll-el-change': [el: HTMLElement | null]
  scroll: []
  'marquee-mousedown': [event: MouseEvent]
  open: [item: KnowledgeCard]
  'open-folder': [path: string]
  'move-to-folder': [ids: string[], path: string]
  'toggle-card-checkbox': [id: string, checked: boolean, ctx?: { e?: Event }]
  'card-menu-visible-change': [visible: boolean, item: KnowledgeCard]
  'card-action': [action: DocumentRowAction, item: KnowledgeCard]
  'toggle-row': [id: string, checked: boolean, shiftKey: boolean]
  'toggle-all': [checked: boolean]
  'list-action': [action: DocumentRowAction, item: KnowledgeCard]
  'probe-trace': [item: KnowledgeCard]
  'tags-changed': []
  'move-select-target': [kb: MoveTargetKB]
  'move-back': []
  'move-confirm': []
  'update:moveMode': [mode: 'reuse_vectors' | 'reparse']
  'reset-move-state': []
  'batch-cancel': []
  'batch-delete': []
  'batch-reparse': []
  'batch-tag': []
  'batch-download': []
  'select-loaded': []
}>()

const scrollContainerRef = ref<HTMLElement | null>(null)

watch(scrollContainerRef, (el) => {
  emit('scroll-el-change', el)
})

onBeforeUnmount(() => {
  emit('scroll-el-change', null)
})
</script>

<template>
  <div
    ref="scrollContainerRef"
    class="doc-scroll-container"
    :class="{
      'is-empty': !cardList.length && !visibleSubFolders.length && !docListLoading,
      'is-marquee-active': docMarqueeVisible,
    }"
    @scroll="emit('scroll')"
    @mousedown="emit('marquee-mousedown', $event)"
  >
    <div
      v-if="docMarqueeVisible"
      class="doc-marquee-box"
      :class="{ 'is-add': docMarqueeMode === 'add', 'is-subtract': docMarqueeMode === 'subtract' }"
      :style="docMarqueeBoxStyle"
      aria-hidden="true"
    />
    <div v-if="docListLoading && !cardList.length && viewMode === 'list'" class="doc-list-skeleton">
      <div v-for="n in 8" :key="n" class="doc-list-skeleton-row">
        <t-skeleton
          animation="gradient"
          :row-col="[[{ width: '32px', height: '38px', type: 'rect' }, { width: '55%', height: '16px' }]]"
        />
      </div>
    </div>
    <div v-else-if="docListLoading && cardList.length === 0" class="doc-card-list doc-card-list-animated">
      <div v-for="n in 8" :key="'doc-skel-' + n" class="knowledge-card knowledge-card-skeleton">
        <div class="card-content">
          <div class="card-content-nav">
            <t-skeleton animation="gradient" :row-col="[{ width: '70%', height: '18px' }]" />
          </div>
          <t-skeleton
            animation="gradient"
            :row-col="[{ width: '100%', height: '14px' }, { width: '60%', height: '14px' }]"
          />
        </div>
        <div class="card-bottom">
          <t-skeleton
            animation="gradient"
            :row-col="[[{ width: '80px', height: '14px' }, { width: '40px', height: '18px', type: 'rect' }]]"
          />
        </div>
      </div>
    </div>
    <template v-else-if="(cardList.length || visibleSubFolders.length) && viewMode === 'grid'">
      <DocumentCardView
        :kb-id="kbId"
        :items="cardList"
        :folder-options="folderOptions"
        :sub-folders="visibleSubFolders"
        :selected-ids="selectedIds"
        :batch-mode="batchMode"
        :can-edit="canEdit"
        :can-download="canDownloadKnowledge"
        :can-mutate-knowledge="canMutateKnowledge"
        :trace-available-by-id="traceAvailableById"
        :move-menu-mode="moveMenuMode"
        :move-target-kbs="moveTargetKbs"
        :move-targets-loading="moveTargetsLoading"
        :move-selected-target-name="moveSelectedTargetName"
        :move-mode="moveMode"
        :move-submitting="moveSubmitting"
        :show-folder-path="showDocumentFolderPath"
        @open="(item) => emit('open', item as KnowledgeCard)"
        @open-folder="(path: string) => emit('open-folder', path)"
        @move-to-folder="(item, path: string) => emit('move-to-folder', [item.id], path)"
        @toggle-checkbox="(id: string, checked: boolean, ctx?: { e?: Event }) => emit('toggle-card-checkbox', id, checked, ctx)"
        @menu-visible-change="(visible: boolean, item) => emit('card-menu-visible-change', visible, item as KnowledgeCard)"
        @action="(action, item) => emit('card-action', action as DocumentRowAction, item as KnowledgeCard)"
        @tags-changed="emit('tags-changed')"
        @move-select-target="(kb: MoveTargetKB) => emit('move-select-target', kb)"
        @move-back="emit('move-back')"
        @move-confirm="emit('move-confirm')"
        @update:move-mode="(mode: 'reuse_vectors' | 'reparse') => emit('update:moveMode', mode)"
      />
    </template>
    <template v-else-if="(cardList.length || visibleSubFolders.length) && viewMode === 'list'">
      <DocumentListView
        :kb-id="kbId"
        :items="cardList"
        :folder-options="folderOptions"
        :sub-folders="visibleSubFolders"
        :selected-ids="selectedIds"
        :can-edit="canEdit"
        :can-download="canDownloadKnowledge"
        :can-mutate-knowledge="canMutateKnowledge"
        :trace-visible-ids="traceAvailableById"
        :move-menu-mode="moveMenuMode"
        :move-target-kbs="moveTargetKbs"
        :move-targets-loading="moveTargetsLoading"
        :move-selected-target-name="moveSelectedTargetName"
        :move-mode="moveMode"
        :move-submitting="moveSubmitting"
        :show-folder-path="showDocumentFolderPath"
        @open-folder="(path: string) => emit('open-folder', path)"
        @move-to-folder="(item, path: string) => emit('move-to-folder', [item.id], path)"
        @open="(item) => emit('open', item as KnowledgeCard)"
        @toggle-row="(id: string, checked: boolean, shift: boolean) => emit('toggle-row', id, checked, shift)"
        @toggle-all="(checked: boolean) => emit('toggle-all', checked)"
        @action="(action, item) => emit('list-action', action as DocumentRowAction, item as KnowledgeCard)"
        @probe-trace="(item) => emit('probe-trace', item as KnowledgeCard)"
        @tags-changed="emit('tags-changed')"
        @move-select-target="(kb: MoveTargetKB) => emit('move-select-target', kb)"
        @move-back="emit('move-back')"
        @move-confirm="emit('move-confirm')"
        @update:move-mode="(mode: 'reuse_vectors' | 'reparse') => emit('update:moveMode', mode)"
        @reset-move-state="emit('reset-move-state')"
      />
    </template>
    <template v-else-if="!docListLoading">
      <div class="doc-empty-state">
        <p v-if="folderEmptyState === 'search'" class="doc-empty-folder">
          {{ $t('knowledgeBase.folderTree.emptySearch') }}
        </p>
        <p v-else-if="folderEmptyState === 'subfolders'" class="doc-empty-folder">
          {{ $t('knowledgeBase.folderTree.emptyFolderWithSubfolders', { count: folderSubfolderDocumentCount }) }}
        </p>
        <p v-else-if="folderEmptyState === 'folder'" class="doc-empty-folder">
          {{ $t('knowledgeBase.folderTree.emptyFolder') }}
        </p>
        <EmptyKnowledge v-else />
      </div>
    </template>
  </div>

  <div v-show="batchMode || selectedIds.size > 0" class="doc-batch-bar-anchor">
    <DocumentBatchBar
      :count="selectedIds.size"
      :delete-loading="batchDeleting"
      :reparse-loading="batchReparsing"
      :tag-loading="batchTagging"
      :download-loading="batchDownloading"
      :can-download="canDownloadKnowledge"
      :can-mutate="canMutateKnowledge"
      :visible="batchMode || selectedIds.size > 0"
      :show-move-to-folder="canEdit"
      :folder-options="folderOptions"
      @cancel="emit('batch-cancel')"
      @delete="emit('batch-delete')"
      @reparse="emit('batch-reparse')"
      @batch-tag="emit('batch-tag')"
      @download="emit('batch-download')"
      @select-loaded="emit('select-loaded')"
      @move-to-folder="(path: string) => emit('move-to-folder', Array.from(selectedIds), path)"
    >
      <template #assistant>
        <AssistantActionsButton
          v-if="selectedIds.size"
          :label="$t('assistant.actions.processSelected')"
          :actions="selectedDocumentAssistantActions"
          :disabled="batchDeleting || batchReparsing || batchTagging || batchDownloading"
        />
      </template>
    </DocumentBatchBar>
  </div>
</template>

<style scoped lang="less">
@import '../knowledge-base.less';
</style>
