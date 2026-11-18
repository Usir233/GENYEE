<template>
  <section class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionSource') }}</h4>

    <div class="form-item">
      <div class="source-options" role="radiogroup" :aria-label="$t('model.editor.sourceLabel')">
        <button
          type="button"
          class="source-option"
          :class="{ 'is-active': source === 'remote' }"
          role="radio"
          :aria-checked="source === 'remote'"
          @click="emit('update:source', 'remote')"
        >
          <t-icon name="cloud" class="source-option__icon" />
          <span class="source-option__label">{{ $t('model.editor.sourceRemote') }}</span>
        </button>
        <button
          v-if="activeModelType !== 'rerank'"
          type="button"
          class="source-option"
          :class="{ 'is-active': source === 'local', 'is-disabled': ollamaServiceStatus === false }"
          :disabled="ollamaServiceStatus === false"
          role="radio"
          :aria-checked="source === 'local'"
          @click="emit('update:source', 'local')"
        >
          <t-icon name="server" class="source-option__icon" />
          <span class="source-option__label">{{ $t('model.editor.sourceLocal') }}</span>
        </button>
      </div>

      <div
        v-if="shouldShowOllamaUnavailableTip(source, activeModelType, ollamaServiceStatus)"
        class="ollama-unavailable-tip"
      >
        <t-icon name="error-circle-filled" class="tip-icon" />
        <span class="tip-text">{{ $t('model.editor.ollamaUnavailable') }}</span>
        <t-button variant="text" size="small" class="tip-link" @click="emit('open-ollama-settings')">
          <template #icon><t-icon name="jump" /></template>
          {{ $t('model.editor.goToOllamaSettings') }}
        </t-button>
      </div>
    </div>

    <div v-if="source === 'local'" class="form-item">
      <label class="form-label required">{{ $t('model.modelName') }}</label>
      <div class="model-select-row">
        <t-select
          :model-value="modelName"
          :loading="loadingOllamaModels"
          :class="{ downloading: downloading }"
          :style="downloading ? `--progress: ${downloadProgress}%` : ''"
          filterable
          :filter="handleModelFilter"
          :placeholder="$t('model.searchPlaceholder')"
          @update:model-value="(v: unknown) => emit('update:modelName', String(v ?? ''))"
          @focus="loadOllamaModels"
          @visible-change="handleDropdownVisibleChange"
        >
          <t-option
            v-for="model in filteredOllamaModels"
            :key="model.name"
            :value="model.name"
            :label="model.name"
          >
            <div class="model-option">
              <t-icon name="check-circle-filled" class="downloaded-icon" />
              <span class="model-name">{{ model.name }}</span>
              <span class="model-size">{{ formatModelSize(model.size) }}</span>
            </div>
          </t-option>

          <t-option
            v-if="showDownloadOption"
            :value="`__download__${searchKeyword}`"
            :label="$t('model.editor.downloadLabel', { keyword: searchKeyword })"
            class="download-option"
          >
            <div class="model-option download">
              <t-icon name="download" class="download-icon" />
              <span class="model-name">{{ $t('model.editor.downloadLabel', { keyword: searchKeyword }) }}</span>
            </div>
          </t-option>

          <template v-if="downloading" #suffix>
            <div class="download-suffix">
              <t-icon name="loading" class="spinning" />
              <span class="progress-text">{{ downloadProgress.toFixed(1) }}%</span>
            </div>
          </template>
        </t-select>

        <t-button
          variant="text"
          size="small"
          :loading="loadingOllamaModels"
          class="refresh-btn"
          @click="refreshOllamaModels"
        >
          <t-icon name="refresh" />
          {{ $t('model.editor.refreshList') }}
        </t-button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { OllamaModelInfo } from '@/api/initialization'
import { shouldShowOllamaUnavailableTip } from '@/components/modelEditorSourceState'
import { formatModelSize } from './useOllamaModelManager'
import type { EditorModelType } from './types'

defineProps<{
  source: 'local' | 'remote'
  activeModelType: EditorModelType
  ollamaServiceStatus: boolean | null
  modelName: string
  loadingOllamaModels: boolean
  downloading: boolean
  downloadProgress: number
  searchKeyword: string
  filteredOllamaModels: OllamaModelInfo[]
  showDownloadOption: boolean
  handleModelFilter: (filterWords: string) => boolean
  loadOllamaModels: () => Promise<void>
  refreshOllamaModels: () => Promise<void>
  handleDropdownVisibleChange: (visible: boolean) => void
}>()

const emit = defineEmits<{
  (e: 'update:source', value: 'local' | 'remote'): void
  (e: 'update:modelName', value: string): void
  (e: 'open-ollama-settings'): void
}>()
</script>

<style lang="less" scoped src="./model-editor-dialog.less"></style>
