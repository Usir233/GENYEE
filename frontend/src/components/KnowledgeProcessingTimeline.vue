<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyWithToast } from '@/utils/clipboard'
import ProcessingOverridesBanner from './processing-timeline/ProcessingOverridesBanner.vue'
import ProcessingGanttTree from './processing-timeline/ProcessingGanttTree.vue'
import ProcessingSpanDetailPane from './processing-timeline/ProcessingSpanDetailPane.vue'
import { useProcessingSpansPoll } from './processing-timeline/useProcessingSpansPoll'
import { formatDuration, useSpanWaterfallMetrics } from './processing-timeline/useSpanWaterfallMetrics'
import type { DetailTabKey, FlatRow, TimelineSummaryPayload } from './processing-timeline/types'

const props = withDefaults(
  defineProps<{
    knowledgeId: string
    parseStatus?: string
    autoPoll?: boolean
    compact?: boolean
    gracePoll?: boolean
    docTitle?: string
    showClose?: boolean
  }>(),
  {
    autoPoll: true,
    compact: false,
    gracePoll: true,
    docTitle: '',
    showClose: false,
  },
)

const emit = defineEmits<{
  (e: 'update:hasSpans', has: boolean): void
  (e: 'update:summary', summary: TimelineSummaryPayload): void
  (e: 'close'): void
}>()

const { t } = useI18n()
const detailTab = ref<DetailTabKey>('overview')

function autoSelectSpan(): void {
  if (props.compact) return
  if (!autoSelectPending.value || selectedSpanId.value) return
  const rows = flatRows.value
  if (rows.length === 0) return
  const failed = rows.find((r) => !r.isRoot && r.node.status === 'failed')
  selectedSpanId.value = (failed || rows[0]).key
  detailTab.value = 'overview'
  autoSelectPending.value = false
}

const {
  data,
  processOverrides,
  currentKnowledgeFileType,
  loading,
  refreshing,
  cancelling,
  selectedAttempt,
  expandedRows,
  selectedSpanId,
  autoSelectPending,
  userToggledRows,
  nowTick,
  lastFetchedAt,
  failedAttempts,
  lastFetchOk,
  attemptStatuses,
  traceActive,
  isLive,
  shouldPollNow,
  stalledMin,
  stalledStageLabel,
  canCancelParse,
  processConfigLines,
  fetchSpans,
  onRetry,
  onManualRefresh,
  onCancelParseConfirm,
  onAttemptChange,
} = useProcessingSpansPoll({
  getProps: () => props,
  t: (key, params) => t(key, params ?? {}),
  onHasSpansChange: (has) => emit('update:hasSpans', has),
  onAfterSpansLoaded: autoSelectSpan,
})

const {
  stages,
  currentStageLabel,
  currentStageIndex,
  postprocessDisplayNow,
  formatSpanDuration,
  totalMs,
  showRuler,
  gridStepPct,
  rulerTicks,
  nowMarkerPct,
  flatRows,
  selectedRow,
  detailOpen,
  isNotRun,
  barStyle,
  wrapStyle,
  wrapDurationMs,
  barOffsetPct,
  barOffsetMs,
  liveElapsedMs,
  localizedStatus,
  localizedErrorTitle,
  localizedErrorSuggestion,
  rowLabel,
  rowKindLabel,
  attemptTabs,
  headerStatus,
  headerStatusText,
  headerStatusTheme,
  showLastError,
  errorHeadline,
  showToolbarRetry,
  headStats,
  stageBreakdown,
  identityFields,
} = useSpanWaterfallMetrics({
  data,
  selectedAttempt,
  expandedRows,
  selectedSpanId,
  nowTick,
  lastFetchedAt,
  lastFetchOk,
  attemptStatuses,
  traceActive,
  isLive,
  stalledMin,
  shouldPollNow,
  t: (key, params) => t(key, params ?? {}),
})

// Keep reactive bindings exposed for setupState test assertions.
void processOverrides
void currentKnowledgeFileType
void failedAttempts
void fetchSpans

const primaryHeadTitle = computed(() => props.docTitle || t('knowledgeStages.title'))

watch(
  [
    () => totalMs.value,
    () => headerStatus.value,
    () => currentStageIndex.value,
    () => currentStageLabel.value,
    () => stages.value.length,
  ],
  () => {
    emit('update:summary', {
      totalMs: totalMs.value,
      status: headerStatus.value,
      stageIndex: currentStageIndex.value,
      stageTotal: stages.value.length,
      stageLabel: currentStageLabel.value,
    })
  },
  { immediate: true },
)

function selectRow(row: FlatRow): void {
  if (selectedSpanId.value === row.key) return
  selectedSpanId.value = row.key
  detailTab.value = 'overview'
}

function toggleTree(row: FlatRow, ev?: MouseEvent): void {
  if (ev) ev.stopPropagation()
  if (!row.hasChildren) return
  const wasExpanded = expandedRows.value.has(row.key)
  const next = new Set(expandedRows.value)
  if (next.has(row.key)) next.delete(row.key)
  else next.add(row.key)
  expandedRows.value = next

  const touched = new Set(userToggledRows.value)
  touched.add(row.key)
  userToggledRows.value = touched

  if (!wasExpanded && next.has(row.key)) {
    selectRow(row)
  }
}

function closeDetail(): void {
  selectedSpanId.value = null
  autoSelectPending.value = false
}

async function copyValue(value: unknown): Promise<void> {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2)
  await copyWithToast(text, 'knowledgeStages.copied', 'knowledgeStages.copyDetails')
}

function onKeydown(ev: KeyboardEvent): void {
  if (ev.key === 'Escape' && selectedSpanId.value) {
    closeDetail()
  }
}

onMounted(() => {
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', onKeydown)
  }
})

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', onKeydown)
  }
})
</script>

<template>
  <div class="kp-timeline" :class="{ 'kp-compact': compact }">
    <template v-if="compact">
      <div class="kp-compact-row">
        <span
          v-for="s in stages"
          :key="s.name"
          class="kp-dot"
          :class="['kp-dot-' + s.status]"
          :title="t(`knowledgeStages.stage.${s.name}`) + ' · ' + t(`knowledgeStages.status.${s.status}`)"
        />
      </div>
      <div class="kp-compact-caption">
        <template v-if="totalMs > 0">
          {{ t('knowledgeStages.totalDuration', { d: formatDuration(totalMs) }) }}
        </template>
        <template v-else>
          <span>{{ t('knowledgeStages.title') }}: </span>
          <span class="kp-stage-emph">{{ currentStageIndex }}/{{ stages.length }}</span>
          <span> · {{ currentStageLabel }}</span>
        </template>
      </div>
    </template>

    <template v-else>
      <div class="kp-shell">
        <ProcessingOverridesBanner
          :knowledge-id="knowledgeId"
          :doc-title="docTitle"
          :primary-head-title="primaryHeadTitle"
          :show-close="showClose"
          :data="data"
          :loading="loading"
          :refreshing="refreshing"
          :cancelling="cancelling"
          :is-live="isLive"
          :can-cancel-parse="canCancelParse"
          :show-toolbar-retry="showToolbarRetry"
          :header-status-text="headerStatusText"
          :header-status-theme="headerStatusTheme"
          :head-stats="headStats"
          :attempt-tabs="attemptTabs"
          :process-config-lines="processConfigLines"
          :stalled-min="stalledMin"
          :stalled-stage-label="stalledStageLabel"
          :show-last-error="showLastError"
          :error-headline="errorHeadline"
          :localized-error-suggestion="localizedErrorSuggestion"
          @manual-refresh="onManualRefresh"
          @cancel-parse-confirm="onCancelParseConfirm"
          @retry="onRetry"
          @close="emit('close')"
          @attempt-change="onAttemptChange"
          @copy-value="copyValue"
        />

        <ProcessingGanttTree
          :data="data"
          :loading="loading"
          :detail-open="detailOpen"
          :show-ruler="showRuler"
          :grid-step-pct="gridStepPct"
          :ruler-ticks="rulerTicks"
          :flat-rows="flatRows"
          :selected-span-id="selectedSpanId"
          :expanded-rows="expandedRows"
          :now-marker-pct="nowMarkerPct"
          :postprocess-display-now="postprocessDisplayNow"
          :is-not-run="isNotRun"
          :row-label="rowLabel"
          :row-kind-label="rowKindLabel"
          :live-elapsed-ms="liveElapsedMs"
          :format-span-duration="formatSpanDuration"
          :wrap-style="wrapStyle"
          :wrap-duration-ms="wrapDurationMs"
          :bar-style="barStyle"
          :bar-offset-pct="barOffsetPct"
          :bar-offset-ms="barOffsetMs"
          :localized-status="localizedStatus"
          @select-row="selectRow"
          @toggle-tree="toggleTree"
        />

        <ProcessingSpanDetailPane
          :data="data"
          :detail-open="detailOpen"
          :selected-row="selectedRow"
          :detail-tab="detailTab"
          :stage-breakdown="stageBreakdown"
          :identity-fields="identityFields"
          :row-label="rowLabel"
          :row-kind-label="rowKindLabel"
          :localized-status="localizedStatus"
          :localized-error-title="localizedErrorTitle"
          :live-elapsed-ms="liveElapsedMs"
          :format-span-duration="formatSpanDuration"
          :postprocess-display-now="postprocessDisplayNow"
          :bar-offset-ms="barOffsetMs"
          @update:detail-tab="detailTab = $event"
          @close-detail="closeDetail"
          @copy-value="copyValue"
        />
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
@keyframes kpLivePulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.45;
    transform: scale(0.8);
  }
}

.kp-timeline {
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.kp-shell {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;
  min-height: 0;
  min-width: 0;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.kp-compact {
  max-width: 320px;
  height: auto;
}

.kp-compact-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.kp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder);
  display: inline-block;
}

.kp-dot-done,
.kp-dot-completed {
  background: var(--td-success-color);
}

.kp-dot-running,
.kp-dot-processing {
  background: var(--td-warning-color);
  animation: kpLivePulse 1.4s ease-in-out infinite;
}

.kp-dot-failed {
  background: var(--td-error-color);
}

.kp-dot-cancelled {
  background: transparent;
  border: 1px dashed var(--td-text-color-placeholder);
}

.kp-dot-skipped {
  background: var(--td-text-color-placeholder);
  opacity: 0.4;
}

.kp-dot-pending {
  background: transparent;
  border: 1px solid var(--td-component-border);
}

.kp-dot-unknown {
  background: var(--td-text-color-placeholder);
}

.kp-compact-caption {
  margin-top: 4px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kp-stage-emph {
  color: var(--td-brand-color);
  font-weight: 600;
}
</style>
