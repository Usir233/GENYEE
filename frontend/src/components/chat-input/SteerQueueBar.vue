<script setup lang="ts">
import type { SteerQueueItem } from '@/api/chat/steer';

defineProps<{
  queuedSteers: SteerQueueItem[];
  firstQueuedSteerId?: string;
  steerShortcutLabel: string;
}>();

const emit = defineEmits<{
  'promote-steer': [steerId: string];
  'remove-steer': [steerId: string];
  'retry-steer': [steerId: string];
}>();
</script>

<template>
  <div v-if="queuedSteers.length" class="steer-queue" role="list" :aria-label="$t('input.steerQueueWaiting')">
    <div v-for="item in queuedSteers" :key="item.steer_id" class="steer-queue-item" role="listitem">
      <t-tooltip :content="$t('input.steerAfter')">
        <t-icon name="time" class="steer-queue-icon" :aria-label="$t('input.steerAfter')" />
      </t-tooltip>
      <span class="steer-queue-text" :title="item.content">{{ item.content }}</span>
      <div class="steer-queue-actions">
        <t-tooltip v-if="item.failed" :content="$t('input.steerRetry')">
          <button
            type="button"
            class="steer-queue-action"
            :aria-label="$t('input.steerRetry')"
            @click="emit('retry-steer', item.steer_id)"
          >
            <t-icon name="refresh" />
          </button>
        </t-tooltip>
        <t-icon v-else-if="item.pending" name="loading" class="steer-sending" :aria-label="$t('common.loading')" />
        <t-tooltip
          v-else
          :content="`${$t('input.steerQueueSendNow')}${item.steer_id === firstQueuedSteerId ? ` · ${steerShortcutLabel}` : ''}`"
        >
          <button
            type="button"
            class="steer-queue-action"
            :aria-label="$t('input.steerQueueSendNow')"
            :disabled="item.promoting || item.pending"
            @click="emit('promote-steer', item.steer_id)"
          >
            <t-icon name="arrow-up" />
          </button>
        </t-tooltip>
        <t-tooltip :content="$t('common.remove')">
          <button
            type="button"
            class="steer-queue-action steer-queue-remove"
            :aria-label="$t('common.remove')"
            :disabled="item.promoting || item.pending"
            @click="emit('remove-steer', item.steer_id)"
          >
            <t-icon name="close" />
          </button>
        </t-tooltip>
      </div>
    </div>
  </div>
</template>

<style scoped lang="less">
.steer-queue {
  width: calc(100% - 24px);
  max-width: 936px;
  box-sizing: border-box;
  max-height: 140px;
  overflow-y: auto;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-bottom: 0;
  border-radius: 10px 10px 0 0;
}

.steer-queue-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
}

.steer-queue-item + .steer-queue-item {
  border-top: 1px solid var(--td-component-stroke);
}

.steer-queue-text {
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-md);
  line-height: 1.5;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.steer-queue-actions {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  gap: 4px;
}

.steer-queue-icon {
  flex-shrink: 0;
  font-size: var(--app-text-base);
  color: var(--td-text-color-secondary);
}

.steer-queue-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xl);
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &:disabled {
    opacity: 0.4;
    cursor: default;
  }
}

.steer-sending {
  animation: gy-spin 1s linear infinite;
}

@media (prefers-reduced-motion: reduce) {
  .steer-sending {
    animation: none;
  }
}
</style>
