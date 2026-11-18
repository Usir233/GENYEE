<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { isValidURL } from '@/utils/security';
import { formatMetadataValue } from './useDocSummaryPolling';
import type {
  KnowledgeDocDetails,
  KnowledgeTagItem,
  MetadataDraftRow,
  MetadataValueType,
} from './types';

const props = defineProps<{
  details: KnowledgeDocDetails;
  canEditContent: boolean;
  canEditSummary: boolean;
  showSummarySection: boolean;
  summaryStatusRefreshing: boolean;
  metadataEditing: boolean;
  metadataDraft: MetadataDraftRow[];
  metadataSaving: boolean;
  metadataTypeOptions: Array<{ label: string; value: string }>;
  summaryRefreshing: boolean;
  summaryEditing: boolean;
  summaryDraft: string;
  summarySaving: boolean;
}>();

const emit = defineEmits<{
  startMetadataEdit: [];
  cancelMetadataEdit: [];
  addMetadataRow: [];
  removeMetadataRow: [id: number];
  updateMetadataRow: [id: number, patch: Partial<Pick<MetadataDraftRow, 'key' | 'value' | 'type'>>];
  saveMetadata: [];
  startSummaryEdit: [];
  cancelSummaryEdit: [];
  'update:summaryDraft': [value: string];
  saveSummary: [];
  refreshSummary: [];
}>();

const { t } = useI18n();

const channelLabelMap: Record<string, string> = {
  web: 'knowledgeBase.channelWeb',
  api: 'knowledgeBase.channelApi',
  browser_extension: 'knowledgeBase.channelBrowserExtension',
  wechat: 'knowledgeBase.channelWechat',
  wecom: 'knowledgeBase.channelWecom',
  feishu: 'knowledgeBase.channelFeishu',
  gitlab: 'knowledgeBase.channelGitLab',
  seafile: 'knowledgeBase.channelSeafile',
  nextcloud: 'knowledgeBase.channelNextcloud',
  confluence: 'knowledgeBase.channelConfluence',
  feishu_drive: 'knowledgeBase.channelFeishuDrive',
  lark_drive: 'knowledgeBase.channelLarkDrive',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
  ima: 'knowledgeBase.channelIma',
};

const getChannelLabel = (channel: string): string => {
  const key = channelLabelMap[channel];
  return key ? t(key) : t('knowledgeBase.channelUnknown');
};

const getTypeLabel = (): string => {
  switch (props.details.type) {
    case 'url':
      return t('knowledgeBase.typeURL');
    case 'manual':
      return t('knowledgeBase.typeManual');
    case 'file':
      return props.details.file_type ? props.details.file_type.toUpperCase() : t('knowledgeBase.typeFile');
    default:
      return '';
  }
};

const getTypeTheme = (): 'primary' | 'success' | 'default' => {
  switch (props.details.type) {
    case 'url':
      return 'primary';
    case 'manual':
      return 'success';
    case 'file':
    default:
      return 'default';
  }
};

const getTimeLabel = (): string => {
  switch (props.details.type) {
    case 'url':
      return t('knowledgeBase.importTime');
    case 'manual':
      return t('knowledgeBase.createTime');
    case 'file':
    default:
      return t('knowledgeBase.uploadTime');
  }
};

const detailTags = computed<KnowledgeTagItem[]>(() => {
  const tags = props.details?.tags;
  return Array.isArray(tags) ? tags : [];
});

const summaryExpanded = ref(false);
const summaryRef = ref<HTMLElement>();
const summaryOverflow = ref(false);

const checkSummaryOverflow = (): void => {
  void nextTick(() => {
    const el = summaryRef.value;
    if (!el) {
      summaryOverflow.value = false;
      return;
    }
    summaryOverflow.value = el.scrollHeight > el.clientHeight + 1;
  });
};

let summaryResizeObserver: ResizeObserver | null = null;

watch(summaryRef, (el) => {
  if (summaryResizeObserver) {
    summaryResizeObserver.disconnect();
    summaryResizeObserver = null;
  }
  if (el && typeof ResizeObserver !== 'undefined') {
    summaryResizeObserver = new ResizeObserver(() => {
      checkSummaryOverflow();
    });
    summaryResizeObserver.observe(el);
  }
  checkSummaryOverflow();
});

watch(
  () => props.details?.description,
  () => {
    summaryExpanded.value = false;
    checkSummaryOverflow();
  },
);

onUnmounted(() => {
  if (summaryResizeObserver) {
    summaryResizeObserver.disconnect();
    summaryResizeObserver = null;
  }
});
</script>

<template>
  <section v-if="details.id" class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.detailSectionMeta') }}</h4>
    <div class="doc-detail-rows">
      <div v-if="details.time" class="doc-detail-row">
        <span class="doc-detail-label">{{ getTimeLabel() }}</span>
        <span class="doc-detail-value">{{ details.time }}</span>
      </div>
      <div v-if="details.type" class="doc-detail-row">
        <span class="doc-detail-label">{{ $t('knowledgeBase.infoCard.type') }}</span>
        <span class="doc-detail-value">
          <t-tag size="small" :theme="getTypeTheme()" variant="light">{{ getTypeLabel() }}</t-tag>
        </span>
      </div>
      <div v-if="details.channel && details.channel !== 'web'" class="doc-detail-row">
        <span class="doc-detail-label">{{ $t('knowledgeBase.infoCard.source') }}</span>
        <span class="doc-detail-value">
          <t-tag size="small" variant="light" theme="warning">{{ getChannelLabel(details.channel) }}</t-tag>
        </span>
      </div>
      <div v-if="detailTags.length > 0" class="doc-detail-row">
        <span class="doc-detail-label">{{ $t('knowledgeBase.tagLabel') }}</span>
        <span class="doc-detail-value doc-tag-chips">
          <t-tag
            v-for="tag in detailTags"
            :key="tag.id"
            size="small"
            variant="light-outline"
            class="doc-tag-chip"
          >
            <span class="tag-text">{{ tag.name }}</span>
          </t-tag>
        </span>
      </div>
    </div>
  </section>

  <section v-if="details.id" class="setting-drawer__section metadata-section">
    <div class="section-title-actions">
      <h4 class="setting-drawer__section-title">
        <span>{{ $t('knowledgeBase.customMetadata') }}</span>
        <t-tooltip :content="$t('knowledgeBase.metadataCapabilityHint')" placement="top">
          <t-icon name="info-circle" size="14px" class="metadata-capability-icon" />
        </t-tooltip>
        <span v-if="Object.keys(details.custom_metadata || {}).length" class="metadata-count">
          {{ Object.keys(details.custom_metadata || {}).length }}/20
        </span>
      </h4>
      <t-tooltip v-if="canEditContent && !metadataEditing" :content="$t('common.edit')" placement="top">
        <t-button
          class="icon-action-btn"
          size="small"
          variant="text"
          shape="square"
          :aria-label="$t('common.edit')"
          @click="emit('startMetadataEdit')"
        >
          <template #icon><t-icon name="edit" size="15px" /></template>
        </t-button>
      </t-tooltip>
    </div>

    <div v-if="!metadataEditing" class="metadata-display">
      <div v-if="Object.keys(details.custom_metadata || {}).length" class="metadata-grid">
        <div v-for="(value, key) in (details.custom_metadata || {})" :key="key" class="metadata-item">
          <span class="metadata-item-key">{{ key }}</span>
          <span class="metadata-item-value">{{ formatMetadataValue(value) }}</span>
        </div>
      </div>
      <button v-else-if="canEditContent" type="button" class="metadata-empty-action" @click="emit('startMetadataEdit')">
        <t-icon name="add" size="15px" />
        <span>{{ $t('knowledgeBase.addMetadataField') }}</span>
      </button>
      <span v-else class="metadata-empty">{{ $t('knowledgeBase.noCustomMetadata') }}</span>
    </div>

    <div v-else class="metadata-editor">
      <div v-for="row in metadataDraft" :key="row.id" class="metadata-editor-row">
        <t-input
          :model-value="row.key"
          class="metadata-key-input"
          :placeholder="$t('knowledgeBase.metadataKeyPlaceholder')"
          @update:model-value="(val: string | number) => emit('updateMetadataRow', row.id, { key: String(val ?? '') })"
        />
        <t-select
          :model-value="row.type"
          class="metadata-type-select"
          :options="metadataTypeOptions"
          @update:model-value="(val: unknown) => emit('updateMetadataRow', row.id, { type: (val as MetadataValueType) || 'text' })"
        />
        <t-select
          v-if="row.type === 'boolean'"
          :model-value="row.value"
          class="metadata-value-input"
          :options="[{ label: 'true', value: 'true' }, { label: 'false', value: 'false' }]"
          @update:model-value="(val: unknown) => emit('updateMetadataRow', row.id, { value: String(val ?? 'true') })"
        />
        <t-input
          v-else-if="row.type !== 'null'"
          :model-value="row.value"
          class="metadata-value-input"
          :placeholder="$t('knowledgeBase.metadataValuePlaceholder')"
          @update:model-value="(val: string | number) => emit('updateMetadataRow', row.id, { value: String(val ?? '') })"
        />
        <div v-else class="metadata-null-value">null</div>
        <t-tooltip :content="$t('common.delete')" placement="top">
          <t-button
            class="icon-action-btn metadata-remove-btn"
            size="small"
            variant="text"
            shape="square"
            :aria-label="$t('common.delete')"
            @click="emit('removeMetadataRow', row.id)"
          >
            <template #icon><t-icon name="delete" size="15px" /></template>
          </t-button>
        </t-tooltip>
      </div>
      <button v-if="metadataDraft.length < 20" type="button" class="metadata-add-row" @click="emit('addMetadataRow')">
        <t-icon name="add" size="15px" />
        <span>{{ $t('knowledgeBase.addMetadataField') }}</span>
      </button>
      <div class="metadata-actions">
        <t-button size="small" variant="outline" :disabled="metadataSaving" @click="emit('cancelMetadataEdit')">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button size="small" theme="primary" :loading="metadataSaving" @click="emit('saveMetadata')">
          {{ $t('common.save') }}
        </t-button>
      </div>
    </div>
  </section>

  <section v-if="details.type === 'url'" class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.urlSource') }}</h4>
    <div class="url_link_box">
      <a
        v-if="details.source && isValidURL(details.source)"
        :href="details.source"
        target="_blank"
        rel="noopener noreferrer"
        class="url_link"
      >
        <t-icon name="link" size="14px" />
        <span class="url_text">{{ details.source }}</span>
        <t-icon name="jump" size="14px" class="jump-icon" />
      </a>
      <span v-else class="url_link">
        <t-icon name="link" size="14px" />
        <span class="url_text">{{ details.source }}</span>
      </span>
    </div>
  </section>

  <section v-if="showSummarySection" class="setting-drawer__section summary-section">
    <div class="section-title-actions">
      <div class="summary-section-heading">
        <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.documentSummary') }}</h4>
        <span v-if="details.description && summaryStatusRefreshing" class="summary-refreshing-indicator">
          <t-loading size="small" />
          <span>{{ $t('knowledgeBase.generatingSummary') }}</span>
        </span>
      </div>
      <div v-if="canEditContent && !summaryEditing" class="summary-title-actions">
        <t-tooltip v-if="canEditSummary" :content="$t('common.edit')" placement="top">
          <t-button
            class="icon-action-btn"
            size="small"
            variant="text"
            shape="square"
            :aria-label="$t('common.edit')"
            @click="emit('startSummaryEdit')"
          >
            <template #icon><t-icon name="edit" size="15px" /></template>
          </t-button>
        </t-tooltip>
        <t-tooltip :content="$t('knowledgeBase.regenerateSummary')" placement="top">
          <t-button
            class="icon-action-btn"
            size="small"
            variant="text"
            shape="square"
            :aria-label="$t('knowledgeBase.regenerateSummary')"
            :loading="summaryRefreshing"
            @click="emit('refreshSummary')"
          >
            <template #icon><t-icon name="refresh" size="15px" /></template>
          </t-button>
        </t-tooltip>
      </div>
    </div>

    <div v-if="summaryEditing" class="summary_editor">
      <t-textarea
        :model-value="summaryDraft"
        :autosize="{ minRows: 4, maxRows: 10 }"
        :placeholder="$t('knowledgeBase.noDocumentSummary')"
        @update:model-value="(val: string | number) => emit('update:summaryDraft', String(val ?? ''))"
      />
      <div class="summary_editor_actions">
        <t-button size="small" variant="outline" :disabled="summarySaving" @click="emit('cancelSummaryEdit')">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button size="small" theme="primary" :loading="summarySaving" @click="emit('saveSummary')">
          {{ $t('common.save') }}
        </t-button>
      </div>
    </div>

    <div
      v-else-if="details.description"
      class="summary_wrapper"
      :class="{ summary_clickable: summaryOverflow || summaryExpanded }"
      @click="(summaryOverflow || summaryExpanded) && (summaryExpanded = !summaryExpanded)"
    >
      <div ref="summaryRef" :class="['summary_content', { summary_collapsed: !summaryExpanded }]">{{
        details.description
      }}</div>
      <div
        v-if="(summaryOverflow && !summaryExpanded) || summaryExpanded"
        class="summary_fade"
        :class="{ summary_fade_expanded: summaryExpanded }"
      >
        <t-icon :name="summaryExpanded ? 'chevron-up' : 'chevron-down'" size="14px" class="summary_fade_icon" />
      </div>
    </div>

    <div v-else class="summary_loading">
      <template v-if="details.summary_status === 'pending' || details.summary_status === 'processing'">
        <t-loading size="small" />
        <span>{{ $t('knowledgeBase.generatingSummary') }}</span>
      </template>
      <template v-else>
        <t-icon name="file-unknown" size="18px" />
        <span>{{ $t('knowledgeBase.noDocumentSummary') }}</span>
        <t-button v-if="canEditContent" size="small" variant="text" :loading="summaryRefreshing" @click="emit('refreshSummary')">
          <template #icon><t-icon name="refresh" size="14px" /></template>
          {{ $t('knowledgeBase.generateSummary') }}
        </t-button>
      </template>
    </div>
  </section>
</template>

<style scoped lang="less">
.section-title-actions,
.metadata-actions,
.summary-title-actions,
.summary_editor_actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.section-title-actions {
  justify-content: space-between;
}

.summary-title-actions,
.summary_editor_actions {
  gap: 6px;
}

.metadata-actions,
.summary_editor_actions {
  margin-top: 8px;
  justify-content: flex-end;
}

.icon-action-btn {
  width: 28px;
  min-width: 28px;
  height: 28px;
  padding: 0;
  color: var(--td-text-color-secondary);
  border-radius: var(--app-radius-xs);

  &:hover,
  &.is-active {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.doc-detail-rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.doc-detail-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  line-height: 1.6;
}

.doc-detail-label {
  flex: 0 0 72px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.doc-detail-value {
  flex: 1;
  min-width: 0;
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.doc-tag-chips {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}

.doc-tag-chip {
  max-width: 140px;
  height: 20px;
  line-height: 20px;
  border-radius: var(--app-radius-pill);
  border-color: var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  padding: 0 8px;
  background: transparent;

  .tag-text {
    display: inline-block;
    max-width: 100px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
    font-size: var(--app-text-xs);
  }
}

.metadata-capability-icon {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  cursor: help;
}

.metadata-count {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  font-weight: 400;
}

.metadata-display {
  min-width: 0;
}

.metadata-empty {
  color: var(--td-text-color-placeholder);
}

.metadata-grid {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px 18px;
}

.metadata-item {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
  min-width: 0;
}

.metadata-item-key,
.metadata-item-value {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.metadata-item-key {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);

  &::after {
    content: ':';
  }
}

.metadata-item-value {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
}

.metadata-empty-action,
.metadata-add-row {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 5px;
  width: fit-content;
  min-height: 28px;
  padding: 0 4px;
  border: none;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-secondary);
  background: transparent;
  cursor: pointer;
  font: inherit;
  font-size: var(--app-text-sm);

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.metadata-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.metadata-editor-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.metadata-key-input {
  flex: 0 1 30%;
  min-width: 100px;
}

.metadata-type-select {
  flex: 0 0 92px;
}

.metadata-value-input,
.metadata-null-value {
  flex: 1;
  min-width: 110px;
}

.metadata-null-value {
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--td-component-border);
  border-radius: 3px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-component-disabled);
  font-size: var(--app-text-md);
  line-height: 30px;
}

.metadata-remove-btn:hover {
  color: var(--td-error-color);
  background: var(--td-error-color-light);
}

@media (max-width: 720px) {
  .metadata-editor-row {
    flex-wrap: wrap;
  }

  .metadata-key-input,
  .metadata-value-input,
  .metadata-null-value {
    flex: 1 1 calc(50% - 50px);
  }
}

.url_link_box {
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container-hover);
  padding: 8px 12px;

  .url_link {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--td-brand-color);
    text-decoration: none;

    .url_text {
      flex: 1;
      font-size: var(--app-text-md);
      word-break: break-all;
    }

    .jump-icon {
      flex-shrink: 0;
      color: var(--td-brand-color);
    }
  }
}

.summary-section-heading,
.summary-refreshing-indicator {
  display: flex;
  align-items: center;
}

.summary-section-heading {
  min-width: 0;
  gap: 10px;
}

.summary-refreshing-indicator {
  gap: 5px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  white-space: nowrap;
}

.summary_wrapper {
  position: relative;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-sm);

  &.summary_clickable {
    cursor: pointer;
  }
}

.summary_editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.summary_content {
  padding: 12px;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 1.5;
  word-break: break-word;
  white-space: pre-wrap;

  &.summary_collapsed {
    max-height: 4.5em;
    overflow: hidden;
  }
}

.summary_fade {
  display: flex;
  justify-content: center;
  padding-bottom: 4px;
  pointer-events: none;

  &:not(.summary_fade_expanded) {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 28px;
    background: linear-gradient(transparent, var(--td-bg-color-container) 80%);
    border-radius: 0 0 6px 6px;
    align-items: flex-end;
  }
}

.summary_fade_icon {
  color: var(--td-text-color-placeholder);
}

.summary_loading {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
  min-height: 42px;
  background: var(--td-bg-color-container);
  border: 1px dashed var(--td-component-border);
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
}
</style>
