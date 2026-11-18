<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AssistantActionButton from '@/components/assistant/AssistantActionButton.vue'
import { shownStall } from '@/utils/knowledgeProcessingStall'
import { attemptGlyph } from './useSpanWaterfallMetrics'
import type { AttemptTab, HeadStat, SpansResponse } from './types'

defineProps<{
  knowledgeId: string
  docTitle: string
  primaryHeadTitle: string
  showClose: boolean
  data: SpansResponse | null
  loading: boolean
  refreshing: boolean
  cancelling: boolean
  isLive: boolean
  canCancelParse: boolean
  showToolbarRetry: boolean
  headerStatusText: string
  headerStatusTheme: 'success' | 'danger' | 'warning' | 'default'
  headStats: HeadStat[]
  attemptTabs: AttemptTab[]
  processConfigLines: string[]
  stalledMin: number
  stalledStageLabel: string
  showLastError: boolean
  errorHeadline: string
  localizedErrorSuggestion: (code?: string) => string
}>()

const emit = defineEmits<{
  manualRefresh: []
  cancelParseConfirm: []
  retry: []
  close: []
  attemptChange: [attempt: number]
  copyValue: [value: unknown]
}>()

const { t } = useI18n()
</script>

<template>
  <div class="kp-head">
    <div class="kp-head-toolbar">
      <div class="kp-head-titles">
        <div class="kp-head-overline">{{ t('knowledgeStages.title') }}</div>
        <div class="kp-head-title-row">
          <h2 class="kp-head-doc-title" :title="primaryHeadTitle">{{ primaryHeadTitle }}</h2>
          <t-tag
            v-if="data && headerStatusText"
            size="small"
            :theme="headerStatusTheme"
            variant="light"
            class="kp-head-status-tag"
          >
            {{ headerStatusText }}
          </t-tag>
          <span v-if="isLive" class="kp-live-badge" :title="t('knowledgeStages.liveTooltip')">
            <span class="kp-live-dot" />
            <span class="kp-live-text">{{ t('knowledgeStages.live') }}</span>
          </span>
        </div>
      </div>

      <div class="kp-head-actions">
        <t-popup trigger="hover" placement="bottom-right" :overlay-style="{ maxWidth: '340px' }">
          <button
            type="button"
            class="kp-icon-btn"
            :title="t('knowledgeStages.processConfig.title')"
            :aria-label="t('knowledgeStages.processConfig.title')"
          >
            <t-icon name="info-circle" size="16px" />
          </button>
          <template #content>
            <div class="kp-proccfg-pop">
              <div class="kp-proccfg-title">{{ t('knowledgeStages.processConfig.title') }}</div>
              <div v-for="(line, i) in processConfigLines" :key="i" class="kp-proccfg-line">{{ line }}</div>
            </div>
          </template>
        </t-popup>

        <button
          type="button"
          class="kp-icon-btn"
          :class="{
            'kp-icon-btn-spin': refreshing,
            'kp-icon-btn-autoflow': isLive && !refreshing,
          }"
          :disabled="loading || refreshing"
          :title="isLive ? t('knowledgeStages.autoRefreshOn') : t('knowledgeStages.refresh')"
          :aria-label="isLive ? t('knowledgeStages.autoRefreshOn') : t('knowledgeStages.refresh')"
          @click="emit('manualRefresh')"
        >
          <t-icon name="refresh" size="16px" />
        </button>

        <t-popconfirm
          v-if="canCancelParse"
          theme="warning"
          :content="t('knowledgeBase.cancelParseConfirmBody', { title: docTitle || knowledgeId })"
          :confirm-btn="{ content: t('knowledgeBase.cancelParse'), theme: 'danger' }"
          :cancel-btn="{ content: t('common.cancel') }"
          placement="bottom"
          @confirm="emit('cancelParseConfirm')"
        >
          <button
            type="button"
            class="kp-icon-btn kp-icon-btn-danger"
            :class="{ 'kp-icon-btn-spin': cancelling }"
            :disabled="cancelling"
            :title="t('knowledgeBase.cancelParse')"
            :aria-label="t('knowledgeBase.cancelParse')"
            @click.stop
          >
            <t-icon :name="cancelling ? 'loading' : 'close-circle'" size="15px" />
          </button>
        </t-popconfirm>

        <t-button v-if="showToolbarRetry" size="small" theme="danger" variant="outline" @click="emit('retry')">
          <t-icon name="refresh" size="14px" />
          <span style="margin-left: 4px">{{ t('knowledgeStages.retry') }}</span>
        </t-button>

        <button
          v-if="showClose"
          type="button"
          class="kp-icon-btn"
          :aria-label="t('knowledgeStages.close')"
          :title="t('knowledgeStages.close')"
          @click="emit('close')"
        >
          <t-icon name="close" size="16px" />
        </button>
      </div>
    </div>

    <div v-if="headStats.length > 0" class="kp-stat-strip">
      <span v-for="stat in headStats" :key="stat.key" class="kp-stat">
        <span class="kp-stat-label">{{ stat.label }}</span>
        <span
          class="kp-stat-value"
          :class="{ 'kp-stat-value-text': !stat.mono, 'kp-stat-note': stat.note }"
        >{{ stat.value }}</span>
      </span>
    </div>

    <div v-if="attemptTabs.length > 0" class="kp-attempts">
      <button
        v-for="tab in attemptTabs"
        :key="tab.n"
        type="button"
        class="kp-attempt"
        :class="{ 'kp-attempt-active': tab.active }"
        @click="emit('attemptChange', tab.n)"
      >
        <span class="kp-attempt-num kp-mono">#{{ tab.n }}</span>
        <span class="kp-attempt-glyph" :class="attemptGlyph(tab.status).cls">{{
          attemptGlyph(tab.status).ch
        }}</span>
      </button>
    </div>

    <div
      v-if="shownStall(data?.stall_state, stalledMin) === 'queued'"
      class="kp-stall kp-stall-queued"
      role="status"
    >
      <t-icon name="time" size="16px" class="kp-stall-icon" />
      <div class="kp-stall-body">
        <div class="kp-stall-title">{{ t('knowledgeStages.stall.queuedTitle', { minutes: stalledMin }) }}</div>
        <div class="kp-stall-hint">{{ t('knowledgeStages.stall.queuedHint') }}</div>
      </div>
    </div>
    <div v-else-if="shownStall(data?.stall_state, stalledMin) === 'stalled'" class="kp-stall" role="status">
      <t-icon name="time" size="16px" class="kp-stall-icon" />
      <div class="kp-stall-body">
        <div class="kp-stall-title">{{ t('knowledgeStages.stall.title', { minutes: stalledMin }) }}</div>
        <div class="kp-stall-hint">
          {{
            stalledStageLabel
              ? t('knowledgeStages.stall.hintAtStage', { stage: stalledStageLabel })
              : t('knowledgeStages.stall.hint')
          }}
        </div>
      </div>
      <t-popconfirm
        v-if="canCancelParse"
        theme="warning"
        :content="t('knowledgeBase.cancelParseConfirmBody', { title: docTitle || knowledgeId })"
        :confirm-btn="{ content: t('knowledgeBase.cancelParse'), theme: 'danger' }"
        :cancel-btn="{ content: t('common.cancel') }"
        placement="bottom"
        @confirm="emit('cancelParseConfirm')"
      >
        <button type="button" class="kp-stall-btn" :disabled="cancelling" @click.stop>
          <t-icon :name="cancelling ? 'loading' : 'close-circle'" size="14px" />
          <span>{{ t('knowledgeBase.cancelParse') }}</span>
        </button>
      </t-popconfirm>
    </div>

    <div v-if="showLastError && data?.last_error" class="kp-last-error" role="alert">
      <div class="kp-err-top">
        <span class="kp-err-glyph" aria-hidden="true">!</span>
        <span class="kp-err-where">{{ errorHeadline }}</span>
        <span v-if="data.last_error.error_code" class="kp-err-code kp-mono">{{ data.last_error.error_code }}</span>
        <button
          type="button"
          class="kp-err-copy"
          :title="t('knowledgeStages.copyError')"
          :aria-label="t('knowledgeStages.copyError')"
          @click="emit('copyValue', data.last_error)"
        >
          <t-icon name="copy" size="14px" />
        </button>
      </div>
      <div v-if="data.last_error.error_message" class="kp-err-message">{{ data.last_error.error_message }}</div>
      <div v-if="localizedErrorSuggestion(data.last_error.error_code)" class="kp-err-hint">
        {{ localizedErrorSuggestion(data.last_error.error_code) }}
      </div>
      <div class="kp-err-actions">
        <AssistantActionButton
          :label="t('assistant.actions.diagnose')"
          :query="t('assistant.prompts.documentFailure', { title: docTitle || knowledgeId, id: knowledgeId })"
          :selection="{
            field: errorHeadline,
            text: [data.last_error.error_code, data.last_error.error_message].filter(Boolean).join('\n'),
          }"
        />
        <button v-if="data.parse_status === 'failed'" type="button" class="kp-err-btn" @click="emit('retry')">
          <t-icon name="refresh" size="14px" />
          <span>{{ t('knowledgeStages.retry') }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="less">
@import "./timeline-header.less";
</style>
