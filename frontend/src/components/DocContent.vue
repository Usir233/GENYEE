<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import {
  downKnowledgeDetails,
  KNOWLEDGE_CHUNK_PAGE_SIZE,
  previewKnowledgeFile,
} from '@/api/knowledge-base/index';
import { useAuthStore } from '@/stores/auth';
import { useAssistantStore } from '@/stores/assistant';
import AssistantActionsButton from '@/components/assistant/AssistantActionsButton.vue';
import DocumentPreview from '@/components/DocumentPreview.vue';
import DocumentFileIcon from '@/views/knowledge/components/DocumentFileIcon.vue';
import KnowledgeProcessingTimeline from '@/components/KnowledgeProcessingTimeline.vue';
import { resolveKnowledgeDownloadFileName } from '@/views/knowledge/knowledgeDownloadFileName';
import { isKnownPreviewableExt, resolveFilePreviewExt } from '@/utils/filePreview';

import DocSummaryMetadataHeader from './doc-content/DocSummaryMetadataHeader.vue';
import DocChunkListRenderer from './doc-content/DocChunkListRenderer.vue';
import { useDrawerSplitResize } from './doc-content/useDrawerSplitResize';
import { mergeChunks, useChunkMarkdownPipeline } from './doc-content/useChunkMarkdownPipeline';
import { useDocSummaryPolling } from './doc-content/useDocSummaryPolling';
import type {
  GeneratedQuestion,
  KnowledgeChunkItem,
  KnowledgeDocDetails,
  ProcessedChunkView,
  SummaryStateChangePayload,
  TimelineSummaryState,
} from './doc-content/types';

const props = defineProps<{
  visible?: boolean;
  details: KnowledgeDocDetails;
  knowledgeType?: string;
  sourceInfo?: unknown;
  canEditKB?: boolean;
  canDownloadKB?: boolean;
  parse_status?: string;
  kbId?: string;
}>();

const emit = defineEmits<{
  closeDoc: [visible: boolean];
  getDoc: [page: number];
  questionDeleted: [payload: { chunkId: string; questionId: string }];
  summaryStateChange: [payload: SummaryStateChangePayload];
}>();

const { t } = useI18n();
const authStore = useAuthStore();
const assistant = useAssistantStore();

const canDeleteGeneratedQuestion = computed<boolean>(() => {
  if (props.canEditKB === true) return true;
  return authStore.hasRole('admin');
});
const canEditContent = canDeleteGeneratedQuestion;

const mdContentWrap = ref<HTMLElement | null>(null);
const docMarkdownRoot = ref<HTMLElement | null>(null);
const getMarkdownRenderRoot = (): ParentNode | null =>
  docMarkdownRoot.value ?? mdContentWrap.value ?? null;

const {
  timelineDrawerWidth,
  timelineDrawerResizing,
  onTraceDrawerResizeStart,
  mainDrawerWidth,
  mainDrawerResizing,
  onMainDrawerResizeStart,
} = useDrawerSplitResize();

const { processMarkdown, runMarkdownPostRenderPipeline } = useChunkMarkdownPipeline({
  t: (key, params) => t(key, params ?? {}),
  getRenderRoot: getMarkdownRenderRoot,
  getKbId: () => props.kbId,
  getDocTitle: () => props.details?.title,
});

const {
  metadataEditing,
  metadataDraft,
  metadataSaving,
  metadataTypeOptions,
  startMetadataEdit,
  cancelMetadataEdit,
  addMetadataRow,
  removeMetadataRow,
  updateMetadataRow,
  saveMetadata,
  summaryRefreshing,
  summaryEditing,
  summaryDraft,
  summarySaving,
  summaryStatusRefreshing,
  canEditSummary,
  showSummarySection,
  startSummaryEdit,
  cancelSummaryEdit,
  saveSummary,
  refreshSummary,
  applySummaryState,
  stopSummaryStatusPolling,
} = useDocSummaryPolling({
  getDetails: () => props.details,
  isVisible: () => Boolean(props.visible),
  canEditContent,
  t: (key, params) => t(key, params ?? {}),
  onSummaryStateChange: (payload) => emit('summaryStateChange', payload),
});

const readingAssistantActions = computed(() => {
  const source = JSON.stringify({ knowledge_base_id: props.kbId, knowledge_id: props.details?.id });
  return [
    { key: 'summary', label: t('assistant.actions.readSummary'), query: t('assistant.prompts.readSummary', { source }) },
    { key: 'steps', label: t('assistant.actions.readSteps'), query: t('assistant.prompts.readSteps', { source }) },
    { key: 'faq', label: t('assistant.actions.extractFaq'), query: t('assistant.prompts.extractFaq', { source }) },
  ];
});

const headerIconFileName = computed(() => {
  const detail = props.details;
  return detail?.file_type
    ? `document.${detail.file_type.toLowerCase()}`
    : detail?.original_file_name || detail?.file_name || detail?.title || '';
});

const hasTimelineSpans = ref(false);
const timelineDrawerVisible = ref(false);
const timelineSummary = ref<TimelineSummaryState>({
  totalMs: 0,
  status: '',
  stageIndex: 0,
  stageTotal: 0,
  stageLabel: '',
});

function formatTimelineDuration(ms: number): string {
  if (!ms || ms < 0) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(2)}s`;
  const mins = Math.floor(ms / 60000);
  const rem = ((ms % 60000) / 1000).toFixed(1);
  return `${mins}m${rem}s`;
}

function openTimeline(): void {
  timelineDrawerVisible.value = true;
}

function closeTimeline(): void {
  timelineDrawerVisible.value = false;
}

defineExpose({ openTimeline });

const traceEntryTheme = computed<'success' | 'danger' | 'warning' | 'default'>(() => {
  const s = timelineSummary.value.status || '';
  switch (s) {
    case 'done':
    case 'completed':
      return 'success';
    case 'failed':
      return 'danger';
    case 'running':
    case 'processing':
    case 'pending':
      return 'warning';
    default:
      return 'default';
  }
});

const traceEntryTitle = computed(() => {
  let tip = t('knowledgeStages.viewTrace');
  if (timelineSummary.value.totalMs > 0) {
    tip += ` · ${formatTimelineDuration(timelineSummary.value.totalMs)}`;
  } else if (timelineSummary.value.stageTotal > 0) {
    tip += ` · ${timelineSummary.value.stageIndex}/${timelineSummary.value.stageTotal}`;
  }
  return tip;
});

const CHUNK_PAGE_SIZE = KNOWLEDGE_CHUNK_PAGE_SIZE;
const chunkPage = ref(1);
const loadedChunkPage = ref(1);
let pendingChunkPage: number | null = null;
const isChunkPageTransition = computed(
  () => Boolean(props.details?.chunkLoading) && chunkPage.value !== loadedChunkPage.value,
);

const downloadBlobUrl = ref('');
const viewMode = ref<'chunks' | 'merged' | 'preview'>('merged');

const audioExtensions = new Set(['mp3', 'wav', 'm4a', 'flac', 'ogg']);
const isAudioFile = (fileType?: string): boolean => {
  if (!fileType) return false;
  return audioExtensions.has(fileType.toLowerCase());
};
const audioBlobUrl = ref('');
const audioLoading = ref(false);
let audioLoadSeq = 0;
let docContentUnmounted = false;

const loadAudioPreview = async (): Promise<void> => {
  const targetId = props.details?.id;
  if (!targetId || audioBlobUrl.value || docContentUnmounted) return;
  const seq = ++audioLoadSeq;
  audioLoading.value = true;
  try {
    const blob = await previewKnowledgeFile(targetId);
    if (docContentUnmounted || seq !== audioLoadSeq || props.details?.id !== targetId) {
      return;
    }
    audioBlobUrl.value = URL.createObjectURL(blob);
  } catch (err) {
    if (seq === audioLoadSeq && !docContentUnmounted) {
      console.error('Audio preview load failed:', err);
    }
  } finally {
    if (seq === audioLoadSeq && !docContentUnmounted) {
      audioLoading.value = false;
    }
  }
};

const canPreview = (): boolean => {
  if (props.details?.type !== 'file') return false;
  const ft = resolveFilePreviewExt(props.details?.title, props.details?.file_type);
  if (!ft) return false;
  if (audioExtensions.has(ft)) return false;
  return isKnownPreviewableExt(ft);
};

const getChunkClass = (index: number): string => (index % 2 !== 0 ? 'chunk-odd' : 'chunk-even');

const getChunkMeta = (item: KnowledgeChunkItem): string => {
  if (!item) return '';
  const parts: string[] = [];
  if (item.char_count) {
    parts.push(`${item.char_count} ${t('knowledgeBase.characters')}`);
  }
  if (item.token_count) {
    parts.push(`${item.token_count} tokens`);
  }
  return parts.join(' · ');
};

const getGeneratedQuestions = (item: KnowledgeChunkItem): GeneratedQuestion[] => {
  if (!item || !item.metadata) return [];
  try {
    const metadata =
      typeof item.metadata === 'string'
        ? (JSON.parse(item.metadata) as Record<string, unknown>)
        : item.metadata;
    const questions = Array.isArray(metadata.generated_questions)
      ? (metadata.generated_questions as Array<string | GeneratedQuestion>)
      : [];
    return questions.map((q, index) => (typeof q === 'string' ? { id: `legacy-${index}`, question: q } : q));
  } catch {
    return [];
  }
};

const mergedContent = computed(() => {
  const newChunks = props.details?.md;
  if (newChunks && newChunks.length > 0) {
    return mergeChunks(newChunks);
  }
  return '';
});

const processedChunks = computed<ProcessedChunkView[]>(() =>
  (props.details?.md || []).map((item: KnowledgeChunkItem, index: number) => ({
    original: item,
    processedContent: processMarkdown(item.content),
    questions: getGeneratedQuestions(item),
    meta: getChunkMeta(item),
    hasParent: Boolean(item?.parent_chunk_id),
    chunkClass: getChunkClass(index),
  })),
);

watch(
  () => props.details?.id,
  (newId) => {
    hasTimelineSpans.value = false;
    timelineDrawerVisible.value = false;
    timelineSummary.value = { totalMs: 0, status: '', stageIndex: 0, stageTotal: 0, stageLabel: '' };
    chunkPage.value = 1;
    loadedChunkPage.value = 1;
    pendingChunkPage = null;

    audioLoadSeq += 1;
    audioLoading.value = false;
    if (audioBlobUrl.value) {
      URL.revokeObjectURL(audioBlobUrl.value);
      audioBlobUrl.value = '';
    }
    if (!newId) return;
    if (isAudioFile(props.details?.file_type)) {
      viewMode.value = 'merged';
      void loadAudioPreview();
    } else if (props.details?.type === 'file' && canPreview()) {
      viewMode.value = 'preview';
    } else {
      viewMode.value = 'merged';
    }
  },
);

watch(
  () => props.details?.chunkLoading,
  (val) => {
    if (val === false && pendingChunkPage !== null) {
      if (props.details?.chunkLoadError) {
        chunkPage.value = loadedChunkPage.value;
        MessagePlugin.warning(props.details.chunkLoadError);
      } else {
        loadedChunkPage.value = pendingChunkPage;
      }
      pendingChunkPage = null;
    }
  },
);

watch(
  () => props.details?.md,
  () => {
    void runMarkdownPostRenderPipeline();
  },
  { immediate: true, deep: true, flush: 'post' },
);

watch(
  () => viewMode.value,
  (mode) => {
    if ((mode === 'chunks' || mode === 'merged') && props.visible) {
      void runMarkdownPostRenderPipeline();
    }
  },
  { flush: 'post' },
);

watch(
  () => props.visible,
  (visible) => {
    if (visible && (viewMode.value === 'chunks' || viewMode.value === 'merged')) {
      void runMarkdownPostRenderPipeline();
    }
  },
  { flush: 'post' },
);

onUnmounted(() => {
  docContentUnmounted = true;
  audioLoadSeq += 1;
  stopSummaryStatusPolling();
  if (audioBlobUrl.value) {
    URL.revokeObjectURL(audioBlobUrl.value);
    audioBlobUrl.value = '';
  }
  if (downloadBlobUrl.value) {
    URL.revokeObjectURL(downloadBlobUrl.value);
    downloadBlobUrl.value = '';
  }
});

const handleClose = (): void => {
  emit('closeDoc', false);
  const scrollEl = document.querySelector('.doc-main-drawer .t-drawer__body') as HTMLElement | null;
  if (scrollEl) scrollEl.scrollTop = 0;
  viewMode.value = 'merged';
};

const getDisplayTitle = (): string => {
  if (!props.details?.title) return '';
  if (props.details.type === 'file') {
    const lastDotIndex = props.details.title.lastIndexOf('.');
    return lastDotIndex > 0 ? props.details.title.substring(0, lastDotIndex) : props.details.title;
  }
  return props.details.title;
};

const getContentLabel = (): string => {
  switch (props.details?.type) {
    case 'url':
      return t('knowledgeBase.webContent');
    case 'manual':
      return t('knowledgeBase.documentContent');
    case 'file':
    default:
      return t('knowledgeBase.fileContent');
  }
};

const reloadChunksFromStart = (): void => {
  chunkPage.value = 1;
  pendingChunkPage = 1;
  emit('getDoc', 1);
};

const handleChunkPageChange = (pageInfo: { current: number }): void => {
  if (props.details?.chunkLoading || pageInfo.current === loadedChunkPage.value) return;
  pendingChunkPage = pageInfo.current;
  emit('getDoc', pageInfo.current);
};

const downloadFile = (): void => {
  if (!props.details?.id) return;
  downKnowledgeDetails(props.details.id)
    .then((result) => {
      if (result) {
        if (downloadBlobUrl.value) {
          URL.revokeObjectURL(downloadBlobUrl.value);
        }
        downloadBlobUrl.value = URL.createObjectURL(result);
        const link = document.createElement('a');
        link.style.display = 'none';
        link.setAttribute('href', downloadBlobUrl.value);
        link.setAttribute('download', resolveKnowledgeDownloadFileName(props.details));
        document.body.appendChild(link);
        link.click();
        void nextTick(() => {
          document.body.removeChild(link);
          URL.revokeObjectURL(downloadBlobUrl.value);
          downloadBlobUrl.value = '';
        });
      }
    })
    .catch(() => {
      MessagePlugin.error(t('file.downloadFailed'));
    });
};
</script>

<template>
  <div ref="mdContentWrap" class="doc_content">
    <teleport to="body">
      <div
        v-if="visible"
        class="doc-drawer-resize-handle"
        :style="{ right: `${mainDrawerWidth}px` }"
        role="separator"
        aria-orientation="vertical"
        :aria-label="$t('knowledgeStages.resizeDrawer')"
        @mousedown.prevent="onMainDrawerResizeStart"
      >
        <div class="doc-drawer-resize-line" />
      </div>
    </teleport>

    <t-drawer
      :visible="visible"
      :z-index="2000"
      :size="`${mainDrawerWidth}px`"
      attach="body"
      :close-btn="true"
      :footer="false"
      :class="['doc-main-drawer', { 'doc-main-drawer--resizing': mainDrawerResizing }]"
      @close="handleClose"
    >
      <template #header>
        <div class="doc-drawer-header">
          <div class="doc-drawer-header-icon">
            <DocumentFileIcon :source-type="details.type" :file-name="headerIconFileName" />
          </div>
          <div class="doc-drawer-header-text">
            <div class="doc-drawer-header-title">{{ getDisplayTitle() }}</div>
          </div>
          <div class="header-actions">
            <t-button
              v-if="canDownloadKB && (details.type === 'file' || details.type === 'manual')"
              class="header-action-btn"
              size="small"
              variant="text"
              shape="square"
              theme="default"
              :title="$t('common.download') || 'Download'"
              :aria-label="$t('common.download') || 'Download'"
              @click="downloadFile()"
            >
              <template #icon>
                <t-icon name="download" size="16px" />
              </template>
            </t-button>
            <t-button
              v-if="details.id && hasTimelineSpans"
              class="header-action-btn trace-entry-btn"
              size="small"
              variant="text"
              shape="square"
              :theme="traceEntryTheme"
              :title="traceEntryTitle"
              :aria-label="traceEntryTitle"
              @click="openTimeline"
            >
              <template #icon>
                <t-icon name="chart-line" size="16px" />
              </template>
            </t-button>
            <AssistantActionsButton
              v-if="details.id"
              icon-only
              :label="$t('assistant.askAboutDocument', { name: assistant.displayName || $t('assistant.title') })"
              :actions="readingAssistantActions"
            />
          </div>
        </div>
      </template>

      <div class="kp-trigger-shadow" aria-hidden="true">
        <KnowledgeProcessingTimeline
          v-if="details.id"
          :knowledge-id="details.id"
          :parse-status="details.parse_status"
          :compact="true"
          :grace-poll="false"
          @update:has-spans="hasTimelineSpans = $event"
          @update:summary="timelineSummary = $event"
        />
      </div>

      <teleport to="body">
        <div
          v-if="timelineDrawerVisible"
          class="trace-drawer-resize-handle"
          :style="{ right: `${timelineDrawerWidth}px` }"
          role="separator"
          aria-orientation="vertical"
          :aria-label="$t('knowledgeStages.resizeDrawer')"
          :title="$t('knowledgeStages.resizeDrawer')"
          @mousedown.prevent="onTraceDrawerResizeStart"
        >
          <div class="trace-drawer-resize-line" />
        </div>
      </teleport>

      <t-drawer
        :visible="timelineDrawerVisible"
        :z-index="2100"
        :size="`${timelineDrawerWidth}px`"
        attach="body"
        :close-btn="false"
        :footer="false"
        :header="false"
        :show-overlay="true"
        :close-on-overlay-click="true"
        placement="right"
        :class="['kp-secondary-drawer', { 'kp-secondary-drawer--resizing': timelineDrawerResizing }]"
        @close="closeTimeline"
      >
        <div class="kp-drawer-shell" :class="{ 'kp-drawer-shell--resizing': timelineDrawerResizing }">
          <KnowledgeProcessingTimeline
            v-if="details.id && timelineDrawerVisible"
            :knowledge-id="details.id"
            :parse-status="details.parse_status"
            :doc-title="details.title"
            show-close
            @close="closeTimeline"
          />
        </div>
      </t-drawer>

      <div ref="docMarkdownRoot" class="doc-markdown-root doc-drawer-body setting-drawer__body">
        <DocSummaryMetadataHeader
          :details="details"
          :can-edit-content="canEditContent"
          :can-edit-summary="canEditSummary"
          :show-summary-section="showSummarySection"
          :summary-status-refreshing="summaryStatusRefreshing"
          :metadata-editing="metadataEditing"
          :metadata-draft="metadataDraft"
          :metadata-saving="metadataSaving"
          :metadata-type-options="metadataTypeOptions"
          :summary-refreshing="summaryRefreshing"
          :summary-editing="summaryEditing"
          :summary-draft="summaryDraft"
          :summary-saving="summarySaving"
          @start-metadata-edit="startMetadataEdit"
          @cancel-metadata-edit="cancelMetadataEdit"
          @add-metadata-row="addMetadataRow"
          @remove-metadata-row="removeMetadataRow"
          @update-metadata-row="updateMetadataRow"
          @save-metadata="saveMetadata"
          @start-summary-edit="startSummaryEdit"
          @cancel-summary-edit="cancelSummaryEdit"
          @update:summary-draft="summaryDraft = $event"
          @save-summary="saveSummary"
          @refresh-summary="refreshSummary"
        />

        <section class="setting-drawer__section doc-content-section">
          <div class="doc-content-section-head">
            <div class="doc-content-section-head-left">
              <h4 class="setting-drawer__section-title">{{ getContentLabel() }}</h4>
              <span v-if="(details.total ?? 0) > 0" class="chunk-count">
                {{ $t('knowledgeBase.chunkCount', { count: details.total }) }}
              </span>
            </div>
            <div class="view-mode-buttons">
              <t-button
                v-if="canPreview()"
                size="small"
                :variant="viewMode === 'preview' ? 'base' : 'outline'"
                :theme="viewMode === 'preview' ? 'primary' : 'default'"
                class="view-mode-btn"
                @click="viewMode = 'preview'"
              >
                {{ $t('preview.tab') }}
              </t-button>
              <t-button
                v-if="!canPreview()"
                size="small"
                :variant="viewMode === 'merged' ? 'base' : 'outline'"
                :theme="viewMode === 'merged' ? 'primary' : 'default'"
                class="view-mode-btn"
                @click="viewMode = 'merged'"
              >
                {{ $t('knowledgeBase.viewMerged') }}
              </t-button>
              <t-button
                size="small"
                :variant="viewMode === 'chunks' ? 'base' : 'outline'"
                :theme="viewMode === 'chunks' ? 'primary' : 'default'"
                class="view-mode-btn"
                @click="viewMode = 'chunks'"
              >
                {{ $t('knowledgeBase.viewChunks') }}
              </t-button>
            </div>
          </div>

          <div v-if="isAudioFile(details.file_type)" class="audio-player-section">
            <div v-if="audioLoading" class="audio-loading">
              <t-loading size="small" />
              <span>{{ $t('preview.audioLoading') }}</span>
            </div>
            <audio v-else-if="audioBlobUrl" controls class="audio-player" :src="audioBlobUrl">
              {{ $t('preview.audioNotSupported') }}
            </audio>
          </div>

          <div v-if="viewMode === 'merged'">
            <div v-if="isChunkPageTransition" class="chunk-page-loading">
              <t-loading size="small" />
              <span>{{ $t('common.loading') }}</span>
            </div>
            <template v-else>
              <div v-if="!mergedContent" class="no_content">{{ $t('common.noData') }}</div>
              <div v-else class="md-content doc-merged-content" v-html="processMarkdown(mergedContent)" />
            </template>
          </div>

          <div v-else-if="viewMode === 'chunks'">
            <div v-if="isChunkPageTransition" class="chunk-page-loading">
              <t-loading size="small" />
              <span>{{ $t('common.loading') }}</span>
            </div>
            <DocChunkListRenderer
              v-else
              :details-id="details.id"
              :processed-chunks="processedChunks"
              :loaded-chunk-page="loadedChunkPage"
              :chunk-page-size="CHUNK_PAGE_SIZE"
              :can-edit-content="canEditContent"
              :can-delete-generated-question="canDeleteGeneratedQuestion"
              :process-markdown="processMarkdown"
              @reload-chunks="reloadChunksFromStart"
              @question-deleted="emit('questionDeleted', $event)"
              @summary-state-update="applySummaryState($event.summaryStatus, $event.description)"
              @request-post-render="runMarkdownPostRenderPipeline()"
            />
          </div>

          <div
            v-if="(viewMode === 'merged' || viewMode === 'chunks') && (details.total ?? 0) > CHUNK_PAGE_SIZE"
            class="chunk-pagination"
          >
            <t-pagination
              v-model="chunkPage"
              :total="details.total ?? 0"
              :page-size="CHUNK_PAGE_SIZE"
              size="small"
              show-jumper
              show-page-number
              :show-page-size="false"
              :disabled="Boolean(details.chunkLoading)"
              @change="handleChunkPageChange"
            />
          </div>

          <div v-else-if="viewMode === 'preview'">
            <DocumentPreview
              :knowledge-id="details.id || ''"
              :file-type="details.file_type || ''"
              :file-name="details.title || ''"
              :active="viewMode === 'preview'"
            />
          </div>
        </section>
      </div>
    </t-drawer>
  </div>
</template>

<style scoped lang="less">
@import "./css/markdown.less";
@import "./doc-content/doc-content.less";
</style>

<style lang="less">
.t-drawer.doc-main-drawer {
  .t-drawer__header {
    padding: 14px 18px;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .t-drawer__body {
    padding: 16px 18px;
  }
}

.doc-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  margin-right: var(--assistant-dock-width, 0px);
  cursor: col-resize;
  z-index: 2001;
  display: flex;
  align-items: center;
  justify-content: center;
}

.doc-drawer-resize-handle .doc-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity var(--app-motion-fast) ease, background var(--app-motion-fast) ease;
}

.doc-drawer-resize-handle:hover .doc-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

.t-drawer.doc-main-drawer--resizing .t-drawer__content {
  transition: none !important;
}

.trace-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  margin-right: var(--assistant-dock-width, 0px);
  cursor: col-resize;
  z-index: 2101;
  display: flex;
  align-items: center;
  justify-content: center;
}

.trace-drawer-resize-handle .trace-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity var(--app-motion-fast) ease, background var(--app-motion-fast) ease;
}

.trace-drawer-resize-handle:hover .trace-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

.t-drawer.kp-secondary-drawer--resizing .trace-drawer-resize-line,
body:has(.t-drawer.kp-secondary-drawer--resizing) .trace-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

.t-drawer.kp-secondary-drawer .t-drawer__body {
  padding: 0 !important;
}

.t-drawer.kp-secondary-drawer .t-drawer__content {
  background: var(--td-bg-color-container);
}

.t-drawer.kp-secondary-drawer--resizing .t-drawer__content {
  transition: none !important;
}
</style>
