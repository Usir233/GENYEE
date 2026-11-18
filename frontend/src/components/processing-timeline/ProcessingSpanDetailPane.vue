<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { postprocessElapsedMs } from '@/utils/knowledgeTrace'
import {
  buildKvEntries,
  formatDuration,
  formatTime,
  hasContent,
  isObjectWithKeys,
  prettyJSON,
} from './useSpanWaterfallMetrics'
import type {
  DetailTabKey,
  FlatRow,
  IdentityField,
  SpanNode,
  SpansResponse,
  StageRowSummary,
} from './types'

const props = defineProps<{
  data: SpansResponse | null
  detailOpen: boolean
  selectedRow: FlatRow | null
  detailTab: DetailTabKey
  stageBreakdown: StageRowSummary[]
  identityFields: (row: FlatRow) => IdentityField[]
  rowLabel: (row: FlatRow) => string
  rowKindLabel: (row: FlatRow) => string
  localizedStatus: (status: string) => string
  localizedErrorTitle: (code?: string) => string
  liveElapsedMs: (node: SpanNode) => number
  formatSpanDuration: (node: SpanNode, includeChildren?: boolean) => string
  postprocessDisplayNow: () => number
  barOffsetMs: (node: SpanNode) => number
}>()

const emit = defineEmits<{
  'update:detailTab': [tab: DetailTabKey]
  closeDetail: []
  copyValue: [value: unknown]
}>()

const { t } = useI18n()
const expandedJsonKeys = ref<Set<string>>(new Set())

function tabPayload(tab: 'input' | 'output' | 'metadata'): unknown {
  const node = props.selectedRow?.node
  if (!node) return undefined
  return node[tab]
}

function tabHasContent(tab: 'input' | 'output' | 'metadata'): boolean {
  return hasContent(tabPayload(tab))
}

const traceMetadata = computed(() => {
  const m = props.data?.trace?.metadata
  return hasContent(m) ? m : null
})

watch(
  () => [props.selectedRow?.key, props.detailTab] as const,
  () => {
    if (props.detailTab === 'metadata' && !tabHasContent('metadata')) {
      emit('update:detailTab', 'overview')
    }
  },
)

function jsonExpandKey(section: string, key: string): string {
  return `${props.selectedRow?.key || ''}::${section}::${key}`
}

function toggleJsonKey(section: string, key: string): void {
  const k = jsonExpandKey(section, key)
  const next = new Set(expandedJsonKeys.value)
  if (next.has(k)) next.delete(k)
  else next.add(k)
  expandedJsonKeys.value = next
}

function isJsonExpanded(section: string, key: string): boolean {
  return expandedJsonKeys.value.has(jsonExpandKey(section, key))
}
</script>

<template>
  <div class="kp-detail" :class="{ 'kp-detail-open': detailOpen }">
    <template v-if="selectedRow">
      <div class="kp-detail-head">
        <div class="kp-detail-title">
          <span class="kp-status-dot kp-detail-dot" :class="['kp-dot-' + selectedRow.node.status]" />
          <span class="kp-detail-name">{{ rowLabel(selectedRow) }}</span>
          <span class="kp-detail-kind">{{ rowKindLabel(selectedRow) }}</span>
          <span class="kp-status-chip" :class="'kp-chip-' + selectedRow.node.status">
            {{ localizedStatus(selectedRow.node.status) }}
          </span>
        </div>
        <div class="kp-detail-actions">
          <button
            type="button"
            class="kp-icon-btn"
            :title="t('knowledgeStages.copyDetails')"
            :aria-label="t('knowledgeStages.copyDetails')"
            @click.stop="emit('copyValue', selectedRow.node)"
          >
            <t-icon name="copy" size="18px" />
          </button>
          <button
            type="button"
            class="kp-icon-btn"
            :title="t('knowledgeStages.close')"
            :aria-label="t('knowledgeStages.close')"
            @click="emit('closeDetail')"
          >
            <t-icon name="close" size="18px" />
          </button>
        </div>
      </div>

      <div class="kp-tabs">
        <button
          type="button"
          class="kp-tab"
          :class="{ 'kp-tab-active': detailTab === 'overview' }"
          @click="emit('update:detailTab', 'overview')"
        >
          {{ t('knowledgeStages.tab.overview') }}
        </button>
        <button
          type="button"
          class="kp-tab"
          :class="{ 'kp-tab-active': detailTab === 'input', 'kp-tab-empty': !tabHasContent('input') }"
          @click="emit('update:detailTab', 'input')"
        >
          {{ t('knowledgeStages.detail.input') }}
        </button>
        <button
          type="button"
          class="kp-tab"
          :class="{ 'kp-tab-active': detailTab === 'output', 'kp-tab-empty': !tabHasContent('output') }"
          @click="emit('update:detailTab', 'output')"
        >
          {{ t('knowledgeStages.detail.output') }}
        </button>
        <button
          v-if="tabHasContent('metadata')"
          type="button"
          class="kp-tab"
          :class="{ 'kp-tab-active': detailTab === 'metadata' }"
          @click="emit('update:detailTab', 'metadata')"
        >
          {{ t('knowledgeStages.detail.metadata') }}
        </button>
        <button
          type="button"
          class="kp-tab"
          :class="{ 'kp-tab-active': detailTab === 'raw' }"
          @click="emit('update:detailTab', 'raw')"
        >
          {{ t('knowledgeStages.tab.raw') }}
        </button>
      </div>

      <div class="kp-detail-body">
        <template v-if="detailTab === 'overview'">
          <div class="kp-section">
            <div class="kp-section-title">{{ t('knowledgeStages.detail.timing') }}</div>
            <div class="kp-kv">
              <div class="kp-kv-row">
                <span class="kp-kv-key">{{ t('knowledgeStages.detail.started') }}</span>
                <span class="kp-kv-val kp-mono">{{ formatTime(selectedRow.node.started_at) }}</span>
              </div>
              <div class="kp-kv-row">
                <span class="kp-kv-key">{{ t('knowledgeStages.detail.finished') }}</span>
                <span class="kp-kv-val kp-mono">
                  <template v-if="selectedRow.node.status === 'running'">
                    <span class="kp-kv-running">{{ t('knowledgeStages.detail.inProgress') }}</span>
                  </template>
                  <template v-else>
                    {{ formatTime(selectedRow.node.finished_at) }}
                  </template>
                </span>
              </div>
              <div class="kp-kv-row">
                <span class="kp-kv-key">{{ t('knowledgeStages.detail.duration') }}</span>
                <span class="kp-kv-val kp-mono">
                  <template v-if="selectedRow.node.status === 'running'">
                    {{ formatDuration(liveElapsedMs(selectedRow.node)) }}
                    <span class="kp-kv-tag-live">{{ t('knowledgeStages.detail.elapsed') }}</span>
                  </template>
                  <template v-else>
                    {{ formatSpanDuration(selectedRow.node) }}
                  </template>
                </span>
              </div>
              <div
                v-if="postprocessElapsedMs(selectedRow.node, postprocessDisplayNow()) !== null"
                class="kp-kv-row"
              >
                <span class="kp-kv-key">{{ t('knowledgeStages.detail.includingChildren') }}</span>
                <span class="kp-kv-val kp-mono">
                  {{ formatDuration(postprocessElapsedMs(selectedRow.node, postprocessDisplayNow()) ?? 0) }}
                </span>
              </div>
              <div v-if="!selectedRow.isRoot && barOffsetMs(selectedRow.node) > 0" class="kp-kv-row">
                <span class="kp-kv-key">{{ t('knowledgeStages.detail.offset') }}</span>
                <span class="kp-kv-val kp-mono">+{{ formatDuration(barOffsetMs(selectedRow.node)) }}</span>
              </div>
            </div>
          </div>

          <div class="kp-section">
            <div class="kp-section-title">{{ t('knowledgeStages.detail.identity') }}</div>
            <div class="kp-kv">
              <div v-for="entry in identityFields(selectedRow)" :key="entry.key" class="kp-kv-row">
                <span class="kp-kv-key">{{ entry.label }}</span>
                <span class="kp-kv-val" :class="{ 'kp-mono': entry.mono, 'kp-kv-truncate': entry.copyable }">
                  <span class="kp-kv-text">{{ entry.value }}</span>
                  <button
                    v-if="entry.copyable"
                    type="button"
                    class="kp-kv-copy"
                    :title="t('knowledgeStages.copy')"
                    :aria-label="t('knowledgeStages.copy')"
                    @click.stop="emit('copyValue', entry.value)"
                  >
                    <t-icon name="copy" size="14px" />
                  </button>
                </span>
              </div>
            </div>
          </div>

          <div v-if="traceMetadata" class="kp-section">
            <div class="kp-section-title">{{ t('knowledgeStages.detail.traceMetadata') }}</div>
            <p class="kp-section-desc">{{ t('knowledgeStages.detail.metadataHint') }}</p>
            <div class="kp-kv">
              <div
                v-for="entry in buildKvEntries(traceMetadata)"
                :key="entry.key"
                class="kp-kv-row kp-kv-row-multiline"
              >
                <span class="kp-kv-key kp-mono">{{ entry.key }}</span>
                <span class="kp-kv-val kp-kv-scalar">{{ entry.display }}</span>
              </div>
            </div>
          </div>

          <div v-if="selectedRow.isRoot" class="kp-section">
            <div class="kp-section-title">{{ t('knowledgeStages.detail.stageBreakdown') }}</div>
            <div class="kp-breakdown">
              <div
                v-for="s in stageBreakdown"
                :key="s.name"
                class="kp-breakdown-row"
                :class="['kp-breakdown-' + s.status]"
              >
                <span class="kp-breakdown-label">
                  <span class="kp-status-dot" :class="['kp-dot-' + s.status]" />
                  {{ s.label }}
                </span>
                <div class="kp-breakdown-track">
                  <div class="kp-breakdown-bar" :class="['kp-bar-' + s.status]" :style="{ width: s.pct + '%' }" />
                </div>
                <span class="kp-breakdown-dur kp-mono">{{
                  s.status === 'skipped' || s.status === 'pending' ? '—' : formatDuration(s.duration_ms)
                }}</span>
              </div>
            </div>
          </div>

          <div
            v-if="
              (selectedRow.node.status === 'failed' || selectedRow.node.status === 'cancelled') &&
              (selectedRow.node.error_code || selectedRow.node.error_message)
            "
            class="kp-error-block"
          >
            <div class="kp-error-head">
              <span class="kp-error-glyph">!</span>
              <span class="kp-error-title">{{
                localizedErrorTitle(selectedRow.node.error_code) || t('knowledgeStages.detail.error')
              }}</span>
              <span v-if="selectedRow.node.error_code" class="kp-error-code kp-mono">{{
                selectedRow.node.error_code
              }}</span>
            </div>
            <pre v-if="selectedRow.node.error_message" class="kp-error-msg kp-mono">{{
              selectedRow.node.error_message
            }}</pre>
          </div>

          <div v-if="!selectedRow.node.span_id && !selectedRow.node.started_at" class="kp-detail-hint">
            {{ t('knowledgeStages.detail.placeholderHint') }}
          </div>
        </template>

        <template v-else-if="detailTab === 'input' || detailTab === 'output' || detailTab === 'metadata'">
          <div v-if="!tabHasContent(detailTab)" class="kp-detail-empty">
            <span>{{
              detailTab === 'metadata' ? t('knowledgeStages.detail.metadataEmpty') : t('knowledgeStages.detail.empty')
            }}</span>
          </div>
          <template v-else>
            <div class="kp-section">
              <div class="kp-section-bar">
                <span class="kp-section-title">{{ t('knowledgeStages.detail.' + detailTab) }}</span>
                <button
                  type="button"
                  class="kp-section-action"
                  @click="emit('copyValue', tabPayload(detailTab))"
                >
                  <t-icon name="copy" size="14px" />
                  <span>{{ t('knowledgeStages.copy') }}</span>
                </button>
              </div>

              <div v-if="isObjectWithKeys(tabPayload(detailTab))" class="kp-kv">
                <div
                  v-for="entry in buildKvEntries(tabPayload(detailTab))"
                  :key="entry.key"
                  class="kp-kv-row kp-kv-row-multiline"
                >
                  <span class="kp-kv-key kp-mono">{{ entry.key }}</span>
                  <div class="kp-kv-val kp-kv-multiline">
                    <span
                      v-if="entry.kind === 'bool'"
                      class="kp-mono"
                      :class="{ 'kp-bool-true': Boolean(entry.raw), 'kp-bool-false': !entry.raw }"
                    >{{ entry.display }}</span>
                    <span v-else-if="entry.kind === 'scalar'" class="kp-kv-scalar">{{ entry.display }}</span>
                    <div v-else-if="entry.defaultExpanded" class="kp-kv-inline">
                      <span class="kp-kv-summary kp-mono kp-kv-summary-static">{{ entry.display }}</span>
                      <pre class="kp-json kp-mono">{{ prettyJSON(entry.raw) }}</pre>
                    </div>
                    <div v-else class="kp-kv-collapsible">
                      <button
                        type="button"
                        class="kp-kv-toggle"
                        @click.stop="toggleJsonKey(detailTab, entry.key)"
                      >
                        <span class="kp-kv-summary kp-mono">{{ entry.display }}</span>
                        <span class="kp-kv-toggle-label">{{
                          isJsonExpanded(detailTab, entry.key)
                            ? t('knowledgeStages.detail.hideJson')
                            : t('knowledgeStages.detail.showJson')
                        }}</span>
                      </button>
                      <pre v-if="isJsonExpanded(detailTab, entry.key)" class="kp-json kp-mono">{{
                        prettyJSON(entry.raw)
                      }}</pre>
                    </div>
                  </div>
                </div>
              </div>
              <pre v-else class="kp-json kp-mono">{{ prettyJSON(tabPayload(detailTab)) }}</pre>
            </div>
          </template>
        </template>

        <template v-else-if="detailTab === 'raw'">
          <div class="kp-section">
            <div class="kp-section-bar">
              <span class="kp-section-title">{{ t('knowledgeStages.tab.raw') }}</span>
              <button
                type="button"
                class="kp-section-action"
                @click="emit('copyValue', selectedRow.node)"
              >
                <t-icon name="copy" size="14px" />
                <span>{{ t('knowledgeStages.copy') }}</span>
              </button>
            </div>
            <pre class="kp-json kp-mono kp-json-large">{{ prettyJSON(selectedRow.node) }}</pre>
          </div>
        </template>
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
@import "./timeline-detail-pane.less";
</style>
