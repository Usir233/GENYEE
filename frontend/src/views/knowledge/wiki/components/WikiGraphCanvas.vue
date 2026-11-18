<template>
  <div class="wiki-graph">
    <div :ref="bindGraphEl" class="wiki-graph-canvas"></div>

    <div v-if="graphReady" class="graph-navigation-hint">
      {{ $t('knowledgeEditor.wikiBrowser.graphNavigationHint') }}
    </div>

    <div
      v-if="graphReady && graphFocusGroup"
      class="graph-focus-bar"
      :class="{ 'focus-bar-shifted': graphDrawerVisible }"
    >
      <t-dropdown
        v-if="graphGroupMenu.length"
        :options="graphGroupMenu"
        trigger="click"
        placement="top"
        attach="body"
        :max-height="320"
        @click="onGroupDropdownClick"
      >
        <button
          type="button"
          class="graph-focus-name"
          :title="$t('knowledgeEditor.wikiBrowser.focusGroupSwitch')"
        >
          <span>{{ $t('knowledgeEditor.wikiBrowser.focusGroupViewing', { name: graphFocusGroup.name }) }}</span>
          <t-icon name="chevron-up" />
        </button>
      </t-dropdown>
      <span v-else class="graph-focus-name" :title="graphFocusGroup.name">
        {{ $t('knowledgeEditor.wikiBrowser.focusGroupViewing', { name: graphFocusGroup.name }) }}
      </span>
      <button type="button" class="graph-focus-back" @click="emit('exit-focus')">
        <t-icon name="rollback" />
        <span>{{ $t('knowledgeEditor.wikiBrowser.focusGroupBack') }}</span>
        <kbd>Esc</kbd>
      </button>
    </div>

    <div v-if="graphControlsVisible" class="wiki-graph-search-container">
      <div class="wiki-graph-search-row">
        <div class="wiki-graph-search">
          <t-select
            :model-value="searchValue"
            filterable
            :options="searchOptions"
            :loading="searchLoading"
            :on-search="onRemoteSearch"
            :placeholder="$t('knowledgeEditor.wikiBrowser.searchPlaceholder')"
            :popup-props="{ zIndex: 100 }"
            class="graph-search-select"
            @update:model-value="onSearchModelUpdate"
            @change="onSearchChange"
            @enter="onSearchEnter"
          >
            <template #prefixIcon><t-icon name="search" /></template>
          </t-select>
        </div>
        <t-popup trigger="click" placement="bottom-right" :show-arrow="true" overlay-class-name="wiki-graph-help-popup">
          <button
            type="button"
            class="wiki-graph-help-trigger"
            :title="$t('knowledgeEditor.wikiBrowser.helpButtonTitle')"
            :aria-label="$t('knowledgeEditor.wikiBrowser.helpButtonTitle')"
          >
            <t-icon name="help-circle" />
          </button>
          <template #content>
            <div class="wiki-graph-help">
              <div class="help-section-title">{{ $t('knowledgeEditor.wikiBrowser.helpTitle') }}</div>
              <div class="help-rows">
                <div v-for="row in helpRows" :key="row.action" class="help-row">
                  <span class="help-key">{{ row.action }}</span>
                  <span class="help-desc">{{ row.desc }}</span>
                </div>
              </div>
            </div>
          </template>
        </t-popup>
      </div>
      <button
        v-if="pendingIssuesCount > 0"
        type="button"
        class="wiki-global-issues-status graph-issues-badge"
        @click="emit('open-global-issues')"
      >
        <t-icon name="error-circle" class="wiki-warning-icon" />
        <span class="queue-text">
          {{ $t('knowledgeEditor.wikiBrowser.globalIssuesCount', { count: pendingIssuesCount }) }}
        </span>
      </button>
    </div>

    <div v-if="graphControlsVisible" class="wiki-graph-legend" :class="{ 'legend-shifted': graphDrawerVisible }">
      <div class="legend-items">
        <button
          v-for="item in legendFilterItems"
          :key="item.type"
          type="button"
          class="legend-item clickable"
          :class="{ disabled: !graphFilterTypes.has(item.type) }"
          :aria-pressed="graphFilterTypes.has(item.type)"
          @click="emit('toggle-filter-type', item.type)"
        >
          <span :class="['legend-dot', `legend-dot--${item.type}`]"></span>
          <span>{{ $t(item.labelKey) }}</span>
        </button>
        <div v-if="graphFamiliarCount > 0" class="legend-item">
          <span class="legend-familiar-ring"></span>
          <span>{{ $t('knowledgeEditor.wikiBrowser.legendFamiliar') }}</span>
        </div>
      </div>

      <div class="legend-divider"></div>

      <div class="legend-actions">
        <button
          v-if="unlinkedCount > 0"
          type="button"
          class="legend-action graph-unlinked-toggle"
          :aria-pressed="showUnlinkedNodes"
          @click="emit('toggle-unlinked')"
        >
          <span class="legend-action-icon"><t-icon :name="showUnlinkedNodes ? 'browse-off' : 'browse'" /></span>
          <span>
            {{
              $t(
                showUnlinkedNodes
                  ? 'knowledgeEditor.wikiBrowser.hideUnlinked'
                  : 'knowledgeEditor.wikiBrowser.showUnlinked',
                { count: unlinkedCount },
              )
            }}
          </span>
        </button>

        <button
          type="button"
          class="legend-action"
          :title="$t('knowledgeEditor.wikiBrowser.fitView')"
          @click="emit('fit-view')"
        >
          <span class="legend-action-icon"><t-icon name="focus" /></span>
          <span>{{ $t('knowledgeEditor.wikiBrowser.fitView') }}</span>
        </button>

        <button
          type="button"
          class="legend-action"
          :aria-pressed="showArrows"
          @click="emit('toggle-arrows')"
        >
          <span class="legend-action-icon"><t-icon :name="showArrows ? 'browse-off' : 'browse'" /></span>
          <span>
            {{
              showArrows
                ? $t('knowledgeEditor.wikiBrowser.hideArrows')
                : $t('knowledgeEditor.wikiBrowser.showArrows')
            }}
          </span>
        </button>

        <button
          v-if="graphMode === 'ego' && graphFrontierCount > 0"
          type="button"
          class="legend-action"
          :title="$t('knowledgeEditor.wikiBrowser.growFrontierTitle', { count: graphFrontierCount })"
          @click="emit('grow-frontier')"
        >
          <span class="legend-action-icon"><t-icon name="chart-bubble" /></span>
          <span>{{ $t('knowledgeEditor.wikiBrowser.growFrontier', { count: graphFrontierCount }) }}</span>
        </button>

        <button v-if="graphMode === 'ego'" type="button" class="legend-action" @click="emit('back-to-overview')">
          <span class="legend-action-icon"><t-icon name="rollback" /></span>
          <span>{{ $t('knowledgeEditor.wikiBrowser.backToOverview') }}</span>
        </button>
      </div>

      <div v-if="graphMode === 'overview'" class="legend-detail">
        <div class="legend-detail-label">
          <span>{{ $t('knowledgeEditor.wikiBrowser.graphDetailLevel') }}</span>
          <span class="legend-detail-value">{{ detailLabel }}</span>
        </div>
        <t-slider
          :model-value="detailLevel"
          :min="0"
          :max="detailMax"
          :step="1"
          :marks="detailMarks"
          :label="detailLabel"
          @update:model-value="onDetailLevelChange"
        />
        <div class="legend-detail-hint">{{ $t('knowledgeEditor.wikiBrowser.graphDetailHint') }}</div>
      </div>

      <div v-if="statusCard" class="wiki-graph-status-card">
        <div class="status-card-header">
          <t-icon :name="statusCard.icon" />
          <span class="status-card-title">{{ statusCard.title }}</span>
        </div>
        <div class="status-card-primary" :title="statusCard.primary">
          {{ statusCard.primary }}
        </div>
        <div v-if="statusCard.secondary" class="status-card-secondary">
          {{ statusCard.secondary }}
        </div>
      </div>
    </div>

    <div v-if="!graphReady" class="wiki-reader-empty wiki-graph-empty">
      <t-loading v-if="graphLoading" />
      <div v-else class="wiki-empty-icon">
        <t-icon name="chart-bubble" size="48px" />
      </div>
      <p class="wiki-empty-desc">
        {{
          graphLoading
            ? $t('knowledgeEditor.wikiBrowser.graphEmpty')
            : $t('knowledgeEditor.wikiBrowser.graphNoData')
        }}
      </p>
    </div>

    <slot name="drawer" />
  </div>
</template>

<script setup lang="ts">
import type { ComponentPublicInstance } from 'vue'
import type { GraphStatusCard } from '../composables/useWikiGraphInteraction'

const legendFilterItems = [
  { type: 'summary', labelKey: 'knowledgeEditor.wikiBrowser.filterSummary' },
  { type: 'entity', labelKey: 'knowledgeEditor.wikiBrowser.filterEntity' },
  { type: 'concept', labelKey: 'knowledgeEditor.wikiBrowser.filterConcept' },
  { type: 'synthesis', labelKey: 'knowledgeEditor.wikiBrowser.filterSynthesis' },
  { type: 'comparison', labelKey: 'knowledgeEditor.wikiBrowser.filterComparison' },
] as const

const props = defineProps<{
  graphReady: boolean
  graphLoading: boolean
  graphControlsVisible: boolean
  graphDrawerVisible: boolean
  graphFocusGroup: { key: string; name: string } | null
  graphGroupMenu: Array<{ value: string; content: string }>
  searchValue: string
  searchOptions: Array<{ label: string; value: string }>
  searchLoading: boolean
  onRemoteSearch: (keyword: string) => void
  helpRows: Array<{ action: string; desc: string }>
  pendingIssuesCount: number
  graphFilterTypes: Set<string>
  graphFamiliarCount: number
  unlinkedCount: number
  showUnlinkedNodes: boolean
  showArrows: boolean
  graphMode: 'overview' | 'ego'
  graphFrontierCount: number
  detailLevel: number
  detailMax: number
  detailMarks: Record<number, string>
  detailLabel: string | undefined
  statusCard: GraphStatusCard | null
  setGraphEl: (el: HTMLElement | null) => void
}>()

const emit = defineEmits<{
  (e: 'update:searchValue', val: string): void
  (e: 'update:detailLevel', val: number): void
  (e: 'search-select', val: string): void
  (e: 'search-enter', ctx: { inputValue: string }): void
  (e: 'focus-group', key: string): void
  (e: 'exit-focus'): void
  (e: 'open-global-issues'): void
  (e: 'toggle-filter-type', type: string): void
  (e: 'toggle-unlinked'): void
  (e: 'fit-view'): void
  (e: 'toggle-arrows'): void
  (e: 'grow-frontier'): void
  (e: 'back-to-overview'): void
}>()

function bindGraphEl(el: Element | ComponentPublicInstance | null) {
  props.setGraphEl(el instanceof HTMLElement ? el : null)
}

function onGroupDropdownClick(item: { value?: string | number }) {
  if (item.value !== undefined) {
    emit('focus-group', String(item.value))
  }
}

function onSearchModelUpdate(val: unknown) {
  emit('update:searchValue', typeof val === 'string' ? val : String(val ?? ''))
}

function onSearchChange(val: unknown) {
  if (typeof val === 'string' && val) {
    emit('search-select', val)
  }
}

function onSearchEnter(ctx: { inputValue: string }) {
  emit('search-enter', ctx)
}

function onDetailLevelChange(val: number | number[]) {
  const num = Array.isArray(val) ? val[0] : val
  if (typeof num === 'number' && Number.isFinite(num)) {
    emit('update:detailLevel', num)
  }
}
</script>

<style lang="less">
.wiki-graph {
  flex: 1;
  position: relative;
  overflow: hidden;
  width: 100%;
  height: 100%;
}

.wiki-graph-canvas {
  width: 100%;
  height: 100%;
  min-height: 500px;

  .graph-cluster-outline {
    fill-opacity: 0.45;
    stroke-opacity: 0;
    transition:
      fill-opacity var(--gy-motion-fast) ease,
      stroke-opacity var(--gy-motion-fast) ease,
      opacity var(--gy-motion-fast) ease;

    &.is-hovered {
      fill-opacity: 0.8;
      stroke-opacity: 0.8;
    }

    &.is-dimmed {
      opacity: 0.3;
    }

    &.is-dimmed.is-hovered {
      opacity: 1;
    }

    @media (prefers-reduced-motion: reduce) {
      transition: none;
    }
  }

  .hide-edge-markers line {
    marker-start: none;
    marker-end: none;
  }

  .graph-cluster-caption {
    cursor: zoom-in;
    outline: none;

    text {
      paint-order: stroke;
      stroke: var(--gy-surface);
      stroke-width: 4px;
      stroke-linejoin: round;
    }

    .graph-caption-pill {
      display: none;
    }

    &:hover text,
    &:focus-visible text {
      fill: var(--gy-brand);
      text-decoration: underline;
    }

    &.is-outside-focus {
      cursor: pointer;

      .graph-caption-pill {
        display: inline;
        fill: var(--gy-surface);
        stroke: var(--gy-border);
        stroke-width: 1px;
        transition: stroke var(--gy-motion-instant) ease;
      }

      text {
        fill: var(--gy-text-secondary);
        font-weight: 500;
        stroke: none;
      }

      &:hover,
      &:focus-visible {
        .graph-caption-pill {
          stroke: var(--gy-brand);
        }

        text {
          fill: var(--gy-brand);
          text-decoration: none;
        }
      }
    }
  }
}

.graph-navigation-hint {
  position: absolute;
  left: 20px;
  bottom: 16px;
  pointer-events: none;
  color: var(--gy-text-placeholder);
  font-size: var(--gy-text-sm);
  padding: 5px 9px;
  background: var(--gy-surface);
  border-radius: var(--gy-radius-xs);
  z-index: 1;
}

.graph-focus-bar {
  position: absolute;
  left: 50%;
  bottom: 16px;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: calc(100% - 360px);
  padding: 6px 6px 6px 14px;
  background: var(--gy-surface);
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-pill);
  box-shadow: var(--gy-shadow-sm);
  font-size: var(--gy-text-md);
  color: var(--gy-text-primary);
  z-index: 2;
  transition: left var(--gy-motion-base) ease;

  &.focus-bar-shifted {
    left: calc(50% - 240px);
  }
}

.graph-focus-name {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
  padding: 2px 6px;
  border: 0;
  border-radius: var(--gy-radius-sm);
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &:hover {
    background: var(--gy-surface-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 1px;
  }
}

span.graph-focus-name {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;

  &:hover {
    background: transparent;
  }
}

.graph-focus-back {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border: 0;
  border-radius: var(--gy-radius-pill);
  background: var(--gy-brand-soft);
  color: var(--gy-brand);
  font: inherit;
  cursor: pointer;

  &:hover {
    background: var(--gy-brand-focus);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 1px;
  }

  kbd {
    padding: 0 4px;
    border: 1px solid currentColor;
    border-radius: var(--gy-radius-xs);
    font: inherit;
    font-size: var(--gy-text-xs);
    line-height: 16px;
    opacity: 0.7;
  }
}

.wiki-graph-empty {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  z-index: 20;
  background: var(--gy-surface);
}

.wiki-graph-help {
  min-width: 240px;
  max-width: 320px;

  .help-section-title {
    font-size: var(--gy-text-xs);
    line-height: 14px;
    color: var(--gy-text-placeholder);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 8px;
    user-select: none;
  }

  .help-rows {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .help-row {
    display: grid;
    grid-template-columns: 110px 1fr;
    gap: 12px;
    font-size: var(--gy-text-sm);
    line-height: 16px;
  }

  .help-key {
    color: var(--gy-text-primary);
    font-weight: 500;
    white-space: nowrap;
  }

  .help-desc {
    color: var(--gy-text-secondary);
  }
}

.wiki-graph-search-container {
  position: absolute;
  top: 16px;
  left: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 10;
  width: 320px;
}

.wiki-graph-search {
  width: 100%;
  box-shadow: var(--gy-shadow-sm);
  border-radius: var(--gy-radius-xs);
}

.graph-issues-badge {
  box-shadow: var(--gy-shadow-sm);
  opacity: 0.95;
}

.graph-search-select {
  background: var(--gy-surface);
  opacity: 0.95;
}

.wiki-graph-search-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;

  .t-popup__reference {
    display: inline-flex;
  }

  .wiki-graph-search {
    flex: 1;
    min-width: 0;
  }
}

.wiki-graph-help-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  flex-shrink: 0;
  background: transparent;
  border: none;
  border-radius: var(--gy-radius-xs);
  color: var(--gy-text-placeholder);
  font-size: var(--gy-text-2xl);
  cursor: pointer;
  user-select: none;
  transition: color var(--gy-motion-fast) ease;

  &:hover {
    color: var(--gy-brand);
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }
}

.wiki-graph-legend {
  position: absolute;
  top: 16px;
  right: 16px;
  width: 256px;
  max-width: calc(100% - 32px);
  box-sizing: border-box;
  background: var(--gy-surface);
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-sm);
  padding: 10px 12px;
  box-shadow: var(--gy-shadow-sm);
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 10;
  opacity: 0.95;
  transition: right var(--gy-motion-slow) cubic-bezier(0.645, 0.045, 0.355, 1);

  &.legend-shifted {
    right: calc(480px + 16px);
  }
}

.legend-items {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.legend-item {
  width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  text-align: left;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--gy-text-xs);
  color: var(--gy-text-secondary);
  border-radius: var(--gy-radius-xs);

  &.clickable {
    cursor: pointer;
    transition: color var(--gy-motion-fast) ease, opacity var(--gy-motion-fast) ease;

    &:hover {
      color: var(--gy-text-primary);
    }

    &:focus-visible {
      outline: 2px solid var(--gy-border-focus);
      outline-offset: 2px;
    }
  }

  &.disabled {
    color: var(--gy-text-placeholder);
    text-decoration: line-through;
    opacity: 0.5;
  }
}

.legend-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  display: inline-block;
  flex-shrink: 0;

  &--summary {
    background: var(--gy-accent-blue);
  }

  &--entity {
    background: var(--gy-success);
  }

  &--concept {
    background: var(--gy-warning);
  }

  &--synthesis {
    background: var(--gy-accent-teal);
  }

  &--comparison {
    background: var(--gy-danger);
  }
}

.legend-familiar-ring {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  display: inline-block;
  flex-shrink: 0;
  box-sizing: border-box;
  border: 2px solid var(--gy-brand);
  background: transparent;
}

.legend-divider {
  height: 1px;
  background: var(--gy-border-subtle);
  margin: 0 -12px;
}

.legend-actions {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.legend-action {
  width: 100%;
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  text-align: left;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--gy-text-xs);
  line-height: 14px;
  color: var(--gy-text-secondary);
  border-radius: var(--gy-radius-xs);
  cursor: pointer;
  user-select: none;
  transition: color var(--gy-motion-fast) ease;

  &:hover,
  &.active {
    color: var(--gy-brand);

    .legend-action-icon {
      color: var(--gy-brand);
    }
  }

  &:focus-visible {
    outline: 2px solid var(--gy-border-focus);
    outline-offset: 2px;
  }
}

.legend-action-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  font-size: var(--gy-text-md);
  line-height: 1;
  color: var(--gy-text-placeholder);
  transition: color var(--gy-motion-fast);

  .t-icon {
    font-size: var(--gy-text-md);
    line-height: 1;
  }
}

.legend-detail {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
  min-width: 0;

  .legend-detail-label {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    font-size: var(--gy-text-xs);
    line-height: 14px;
    color: var(--gy-text-secondary);
  }

  .legend-detail-value {
    color: var(--gy-text-primary);
    font-variant-numeric: tabular-nums;
  }

  .legend-detail-hint {
    font-size: var(--gy-text-xs);
    line-height: 16px;
    color: var(--gy-text-placeholder);
  }

  .t-slider {
    width: 100%;
  }

  .t-slider__container {
    margin-bottom: 24px;
  }

  .t-slider__mark-text {
    font-size: var(--gy-text-xs);
    color: var(--gy-text-secondary);
  }
}

.wiki-graph-status-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 100%;
  padding-top: 8px;
  border-top: 1px dashed var(--gy-border-subtle);
  user-select: none;

  .status-card-header {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: var(--gy-text-xs);
    line-height: 14px;
    color: var(--gy-text-placeholder);

    .t-icon {
      font-size: var(--gy-text-sm);
    }
  }

  .status-card-title {
    font-weight: 500;
  }

  .status-card-primary {
    font-size: var(--gy-text-sm);
    line-height: 16px;
    color: var(--gy-text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .status-card-secondary {
    font-size: var(--gy-text-xs);
    line-height: 14px;
    color: var(--gy-text-secondary);
    overflow-wrap: anywhere;
  }
}

@keyframes node-active-pulse {
  0% {
    transform: scale(1);
    opacity: 0.8;
  }

  100% {
    transform: scale(1.6);
    opacity: 0;
  }
}

.node-active-ring {
  transform-origin: 0 0;
  animation: node-active-pulse 1.5s cubic-bezier(0.25, 0.46, 0.45, 0.94) infinite;
}
</style>
