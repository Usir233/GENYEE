<script setup lang="ts">
import type { ModelConfig } from '@/api/model';
import { formatContextWindow, isDefaultContextWindow } from '@/utils/contextWindow';
import { EMBEDDED_AUTO_MODEL, modelDisplayName } from './useChatInputModelReasoning';

defineProps<{
  visible: boolean;
  dropdownStyle: Record<string, string>;
  embeddedMode: boolean;
  embeddedDefaultModel?: ModelConfig;
  followsDefaultModel: boolean;
  availableModels: ModelConfig[];
  composerModelId: string;
  contextWindowTitle: (tokens?: number) => string;
}>();

const emit = defineEmits<{
  close: [];
  selectModel: [value: string];
}>();
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="model-selector-overlay" @click="emit('close')">
      <div class="model-selector-dropdown" :style="dropdownStyle" @click.stop>
        <div class="model-selector-header">
          <span>{{ $t('conversationSettings.models.chatGroupLabel') }}</span>
          <button class="model-selector-add" type="button" @click="emit('selectModel', '__add_model__')">
            <span class="add-icon">+</span>
            <span class="add-text">{{ $t('input.addModel') }}</span>
          </button>
        </div>
        <div class="model-selector-content">
          <button
            v-if="embeddedMode && embeddedDefaultModel"
            type="button"
            class="model-option"
            :class="{ selected: followsDefaultModel }"
            :title="$t('input.modelAutoHint')"
            @click="emit('selectModel', EMBEDDED_AUTO_MODEL)"
          >
            <div class="model-option-left">
              <div class="model-option-name-wrap">
                <span class="model-option-name">{{ $t('input.modelFollowDefault') }}</span>
                <span class="model-option-raw-name">{{ modelDisplayName(embeddedDefaultModel) }}</span>
              </div>
            </div>
            <span class="model-option-check">
              <t-icon v-if="followsDefaultModel" name="check" size="14px" />
            </span>
          </button>

          <button
            v-for="model in availableModels"
            :key="model.id"
            type="button"
            class="model-option"
            :class="{ selected: !followsDefaultModel && model.id === composerModelId }"
            @click="emit('selectModel', model.id || '')"
          >
            <div class="model-option-left">
              <div class="model-option-name-wrap">
                <span class="model-option-name">{{ modelDisplayName(model) }}</span>
                <span v-if="model.display_name" class="model-option-raw-name">{{ model.name }}</span>
              </div>
            </div>
            <span
              class="model-option-ctx"
              :class="{ 'is-default': isDefaultContextWindow(model.parameters?.context_window) }"
              :title="contextWindowTitle(model.parameters?.context_window)"
            >{{ formatContextWindow(model.parameters?.context_window) }}</span>
            <span class="model-option-check">
              <t-icon v-if="model.id === composerModelId" name="check" size="14px" />
            </span>
          </button>

          <div v-if="availableModels.length === 0" class="model-option empty">
            {{ $t('input.noModel') }}
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped lang="less">
.model-selector-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  background: transparent;
  touch-action: none;
}

.model-selector-dropdown {
  position: fixed !important;
  z-index: 10000;
  background: var(--td-bg-color-container);
  border: 0.5px solid var(--td-component-border);
  border-radius: var(--app-radius-lg);
  box-shadow: var(--td-shadow-2);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  margin: 0 !important;
  padding: 0 !important;
  transform: none !important;
  transform-origin: top left;
  animation: modelSelectorFadeIn 0.15s ease-out;
}

@keyframes modelSelectorFadeIn {
  from {
    opacity: 0;
    transform: scale(0.98);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

.model-selector-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 8px 8px 16px;
  border-bottom: 0.5px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.model-selector-content {
  flex: 1;
  min-height: 0;
  max-height: 260px;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 6px 8px;
}

.model-selector-add {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--app-radius-sm);
  border: 0.5px solid transparent;
  background: transparent;
  color: var(--td-brand-color);
  font-size: var(--app-text-sm);
  font-weight: 500;
  cursor: pointer;
  transition: all var(--app-motion-instant);

  .add-icon {
    font-size: var(--app-text-base);
    line-height: 1;
    font-weight: 400;
  }

  &:hover {
    color: var(--td-brand-color-hover);
    background: var(--td-bg-color-secondarycontainer);
  }
}

.model-option {
  width: 100%;
  border: 0;
  background: transparent;
  font: inherit;
  text-align: left;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 8px;
  cursor: pointer;
  transition: background var(--app-motion-instant);
  border-radius: var(--app-radius-sm);
  margin-bottom: 2px;

  &:last-child {
    margin-bottom: 0;
  }

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.selected .model-option-name,
  &.selected .model-option-check {
    color: var(--td-brand-color);
    font-weight: 500;
  }

  &.empty {
    color: var(--td-text-color-placeholder);
    cursor: default;
    text-align: center;
    justify-content: center;
    padding: 20px 8px;

    &:hover {
      background: transparent;
    }
  }
}

.model-option-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
}

.model-option-check {
  width: 14px;
  height: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.model-option-name-wrap {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.model-option-name {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.4;
}

.model-option-raw-name {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.model-option-ctx {
  flex-shrink: 0;
  font-size: var(--app-text-xs);
  font-variant-numeric: tabular-nums;
  color: var(--td-text-color-placeholder);
  line-height: 18px;

  &.is-default {
    color: var(--td-text-color-placeholder);
  }
}
</style>
