<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { postprocessElapsedMs } from '@/utils/knowledgeTrace'
import { formatDuration, isPlaceholder } from './useSpanWaterfallMetrics'
import type { FlatRow, SpanNode, SpansResponse } from './types'

const props = defineProps<{
  data: SpansResponse | null
  loading: boolean
  detailOpen: boolean
  showRuler: boolean
  gridStepPct: string
  rulerTicks: Array<{ left: string; label: string }>
  flatRows: FlatRow[]
  selectedSpanId: string | null
  expandedRows: Set<string>
  nowMarkerPct: number | null
  postprocessDisplayNow: () => number
  isNotRun: (node: SpanNode) => boolean
  rowLabel: (row: FlatRow) => string
  rowKindLabel: (row: FlatRow) => string
  liveElapsedMs: (node: SpanNode) => number
  formatSpanDuration: (node: SpanNode, includeChildren?: boolean) => string
  wrapStyle: (node: SpanNode) => Record<string, string> | null
  wrapDurationMs: (node: SpanNode) => number
  barStyle: (node: SpanNode) => Record<string, string>
  barOffsetPct: (node: SpanNode) => number | null
  barOffsetMs: (node: SpanNode) => number
  localizedStatus: (status: string) => string
}>()

const emit = defineEmits<{
  selectRow: [row: FlatRow]
  toggleTree: [row: FlatRow, ev: MouseEvent]
}>()

const { t } = useI18n()
const scrollRef = ref<HTMLElement | null>(null)

function isRowExpanded(key: string): boolean {
  return props.expandedRows.has(key)
}

function treeToggleAriaLabel(row: FlatRow): string {
  if (!row.hasChildren || row.isRoot) return ''
  return isRowExpanded(row.key)
    ? t('knowledgeStages.collapseBranch')
    : t('knowledgeStages.expandBranch')
}

watch(
  () => props.selectedSpanId,
  (key) => {
    if (!key) return
    void nextTick(() => {
      const root = scrollRef.value
      if (!root) return
      const safeKey =
        typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(key) : key.replace(/"/g, '\\"')
      const el = root.querySelector(`.kp-row[data-span-key="${safeKey}"]`)
      el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    })
  },
)
</script>

<template>
  <div class="kp-body" :class="{ 'kp-body-with-detail': detailOpen }">
    <div v-if="loading && !data" class="kp-state">
      <t-loading size="small" />
    </div>
    <div v-else-if="!data && !loading" class="kp-state kp-state-empty">
      <span>{{ t('knowledgeStages.noActivity') }}</span>
    </div>

    <template v-else-if="data">
      <div v-if="showRuler" class="kp-ruler">
        <div class="kp-ruler-spacer-name" />
        <div class="kp-ruler-spacer-meta" />
        <div class="kp-ruler-track">
          <span
            v-for="(tick, i) in rulerTicks"
            :key="i"
            class="kp-tick"
            :class="{ 'kp-tick-first': i === 0, 'kp-tick-last': i === rulerTicks.length - 1 }"
            :style="{ left: tick.left }"
          >
            <span class="kp-tick-line" />
            <span class="kp-tick-label kp-mono">{{ tick.label }}</span>
          </span>
        </div>
      </div>

      <div ref="scrollRef" class="kp-scroll">
        <div
          class="kp-rows"
          :class="{ 'kp-rows-gridless': !showRuler }"
          :style="{ '--kp-grid-step': gridStepPct }"
        >
          <div
            v-for="row in flatRows"
            :key="row.key"
            class="kp-row"
            :data-span-key="row.key"
            :class="{
              'kp-row-active': selectedSpanId === row.key,
              'kp-row-root': row.isRoot,
              'kp-row-stage': row.isStage,
              'kp-row-span': !row.isRoot && !row.isStage,
              'kp-row-expandable': row.hasChildren && !row.isRoot,
              'kp-row-inert': isNotRun(row.node),
            }"
            :title="row.hasChildren && !row.isRoot ? t('knowledgeStages.rowSelectHint') : undefined"
            @click="emit('selectRow', row)"
          >
            <div class="kp-cell-name">
              <div class="kp-name-inner">
                <span v-for="d in row.depth" :key="d" class="kp-name-guide" aria-hidden="true" />
                <button
                  v-if="row.hasChildren && !row.isRoot"
                  type="button"
                  class="kp-tree-toggle"
                  :aria-expanded="isRowExpanded(row.key)"
                  :aria-label="treeToggleAriaLabel(row)"
                  @click="emit('toggleTree', row, $event)"
                >
                  <t-icon :name="isRowExpanded(row.key) ? 'chevron-down' : 'chevron-right'" size="14px" />
                </button>
                <span v-else class="kp-tree-toggle-spacer" />
                <span
                  class="kp-status-dot"
                  :class="['kp-dot-' + row.node.status, { 'kp-dot-placeholder': isPlaceholder(row.node) }]"
                />
                <span
                  class="kp-name-text"
                  :title="rowLabel(row)"
                  :class="{ 'kp-name-root': row.isRoot, 'kp-name-mono': !row.isRoot && !row.isStage }"
                >{{ rowLabel(row) }}</span>
                <span v-if="rowKindLabel(row)" class="kp-name-kind">{{ rowKindLabel(row) }}</span>
              </div>
            </div>

            <div class="kp-cell-dur" :class="isNotRun(row.node) ? 'kp-dur-inert' : 'kp-mono'">
              <template v-if="isNotRun(row.node)">
                {{ t('knowledgeStages.notRun') }}
              </template>
              <template v-else-if="row.node.status === 'running'">
                <span class="kp-running-time">{{ formatDuration(liveElapsedMs(row.node)) }}</span>
              </template>
              <template v-else>
                <span
                  :title="
                    postprocessElapsedMs(row.node, postprocessDisplayNow()) !== null
                      ? t('knowledgeStages.detail.includingChildren')
                      : undefined
                  "
                >
                  {{ formatSpanDuration(row.node, true) }}
                </span>
              </template>
            </div>

            <div class="kp-cell-bar">
              <span
                v-if="nowMarkerPct !== null && row.isRoot"
                class="kp-now-marker"
                :style="{ left: nowMarkerPct + '%' }"
              />
              <div v-if="isPlaceholder(row.node) && !isNotRun(row.node)" class="kp-bar kp-bar-placeholder" />
              <template v-else>
                <div
                  v-if="wrapStyle(row.node)"
                  class="kp-bar-wrap"
                  :class="['kp-bar-wrap-' + row.node.status]"
                  :style="wrapStyle(row.node) || {}"
                >
                  <span class="kp-bar-tip">
                    <span class="kp-bar-tip-name">{{ rowLabel(row) }}</span>
                    <span class="kp-bar-tip-sep">·</span>
                    <span class="kp-mono">{{ formatDuration(wrapDurationMs(row.node)) }}</span>
                    <span class="kp-bar-tip-sep">·</span>
                    <span>{{ t('knowledgeStages.detail.includingChildren') }}</span>
                  </span>
                </div>
                <div
                  class="kp-bar"
                  :class="['kp-bar-' + row.node.status, { 'kp-bar-running-anim': row.node.status === 'running' }]"
                  :style="barStyle(row.node)"
                >
                  <span class="kp-bar-tip">
                    <span class="kp-bar-tip-name">{{ rowLabel(row) }}</span>
                    <span class="kp-bar-tip-sep">·</span>
                    <span class="kp-mono">{{
                      row.node.status === 'running'
                        ? formatDuration(liveElapsedMs(row.node))
                        : formatSpanDuration(row.node)
                    }}</span>
                    <span class="kp-bar-tip-sep">·</span>
                    <span>{{ localizedStatus(row.node.status) }}</span>
                  </span>
                </div>
                <span
                  v-if="barOffsetPct(row.node) !== null && barOffsetMs(row.node) > 0"
                  class="kp-bar-offset kp-mono"
                  :style="{ '--kp-offset-position': barOffsetPct(row.node) + '%' }"
                  :title="'+' + formatDuration(barOffsetMs(row.node))"
                >
                  +{{ formatDuration(barOffsetMs(row.node)) }}
                </span>
              </template>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
@import "./timeline-gantt.less";
</style>
