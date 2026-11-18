<template>
  <section
    v-if="['embedding', 'chat', 'vllm'].includes(activeModelType) || source === 'remote'"
    class="setting-drawer__section"
  >
    <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionAdvanced') }}</h4>

    <div v-if="activeModelType === 'embedding'" class="form-item">
      <label class="form-label">{{ $t('model.editor.dimensionLabel') }}</label>
      <div class="dimension-control">
        <t-input
          :model-value="dimension ?? ''"
          type="number"
          :min="128"
          :max="4096"
          :placeholder="$t('model.editor.dimensionPlaceholder')"
          :disabled="!supportsDimensionOverride || (source === 'local' && checking)"
          @update:model-value="(v: string | number) => emit('update:dimension', parseOptionalNumber(v))"
        />
        <t-button
          v-if="source === 'local' && modelName"
          variant="text"
          size="small"
          :loading="checking"
          class="dimension-check-btn"
          @click="checkOllamaDimension"
        >
          <t-icon name="refresh" />
          {{ $t('model.editor.checkDimension') }}
        </t-button>
      </div>
      <p v-if="dimensionChecked && dimensionMessage" class="dimension-hint" :class="{ success: dimensionSuccess }">
        {{ dimensionMessage }}
      </p>
    </div>

    <div v-if="activeModelType === 'embedding'" class="form-item">
      <label class="form-label">{{ $t('model.editor.dimensionOverrideLabel') }}</label>
      <div class="vision-toggle">
        <t-switch
          :model-value="Boolean(supportsDimensionOverride)"
          @update:model-value="(v: string | number | boolean) => emit('update:supportsDimensionOverride', Boolean(v))"
        />
        <span class="form-desc form-desc--inline">{{ $t('model.editor.dimensionOverrideDesc') }}</span>
      </div>
    </div>

    <div v-if="activeModelType === 'embedding'" class="form-item">
      <label class="form-label">{{ $t('model.editor.embeddingImageInputLabel') }}</label>
      <div class="vision-toggle">
        <t-switch
          :model-value="embeddingAcceptsImages"
          @update:model-value="(v: string | number | boolean) => emit('update:embeddingAcceptsImages', Boolean(v))"
        />
        <span class="form-desc form-desc--inline">{{ $t('model.editor.embeddingImageInputDesc') }}</span>
      </div>
    </div>

    <div v-if="activeModelType === 'chat' || activeModelType === 'vllm'" class="form-item">
      <label class="form-label">{{ $t('model.editor.contextWindowLabel') }}</label>
      <t-input
        :model-value="contextWindow ?? ''"
        type="number"
        :min="1024"
        :max="10000000"
        :placeholder="$t('model.editor.contextWindowPlaceholder', { value: DEFAULT_MODEL_CONTEXT_WINDOW })"
        @update:model-value="(v: string | number) => emit('update:contextWindow', parseOptionalNumber(v))"
      />
      <p class="form-desc">{{ $t('model.editor.contextWindowDesc') }}</p>
    </div>

    <div v-if="activeModelType === 'chat'" class="form-item">
      <label class="form-label">{{ $t('model.editor.supportsVisionLabel') }}</label>
      <div class="vision-toggle">
        <t-switch
          :model-value="Boolean(supportsVision)"
          @update:model-value="(v: string | number | boolean) => emit('update:supportsVision', Boolean(v))"
        />
        <span class="form-desc form-desc--inline">{{ $t('model.editor.supportsVisionDesc') }}</span>
      </div>
    </div>

    <div v-if="activeModelType === 'chat' || activeModelType === 'vllm'" class="form-item">
      <label class="form-label">{{ $t('model.editor.maxOutputTokensLabel') }}</label>
      <t-input
        :model-value="maxOutputTokens ?? ''"
        type="number"
        :min="1"
        :max="10000000"
        :placeholder="$t('model.editor.maxOutputTokensPlaceholder')"
        @update:model-value="(v: string | number) => emit('update:maxOutputTokens', parseOptionalNumber(v))"
      />
      <p class="form-desc">{{ $t('model.editor.maxOutputTokensDesc') }}</p>
    </div>

    <div v-if="['embedding', 'chat', 'vllm'].includes(activeModelType)" class="form-item">
      <label class="form-label">{{ $t('model.editor.maxConcurrencyLabel') }}</label>
      <t-input
        :model-value="maxConcurrency ?? ''"
        type="number"
        :min="0"
        :max="4096"
        :placeholder="$t('model.editor.maxConcurrencyPlaceholder')"
        @update:model-value="(v: string | number) => emit('update:maxConcurrency', parseOptionalNumber(v))"
      />
      <p class="form-desc">{{ $t('model.editor.maxConcurrencyDesc') }}</p>
    </div>

    <template v-if="source === 'remote'">
      <button
        type="button"
        class="advanced-toggle"
        :aria-expanded="advancedOpen"
        @click="emit('update:advancedOpen', !advancedOpen)"
      >
        <t-icon name="chevron-right" class="toggle-arrow" :class="{ open: advancedOpen }" />
        <span>{{ $t('model.editor.advanced.toggle') }}</span>
      </button>

      <template v-if="advancedOpen">
        <div v-if="isChatLike" class="form-item">
          <label class="form-label">{{ $t('model.editor.advanced.api.label') }}</label>
          <t-select
            :model-value="extraConfig.api || ''"
            clearable
            @update:model-value="(v: unknown) => setExtraConfig('api', String(v ?? ''))"
          >
            <t-option value="" :label="$t('model.editor.advanced.api.auto')" />
            <t-option v-for="api in PROTOCOL_OPTIONS" :key="api" :value="api" :label="api" />
          </t-select>
          <p class="form-desc">{{ $t('model.editor.advanced.api.desc') }}</p>
        </div>

        <div class="form-item">
          <label class="form-label">{{ $t('model.editor.advanced.remoteModelName.label') }}</label>
          <t-input
            :model-value="extraConfig.remote_model_name || ''"
            :placeholder="$t('model.editor.advanced.remoteModelName.placeholder')"
            @update:model-value="(v: string | number) => setExtraConfig('remote_model_name', String(v))"
          />
          <p class="form-desc">{{ $t('model.editor.advanced.remoteModelName.desc') }}</p>
        </div>

        <div v-if="showLegacyThinkingControl" class="form-item">
          <label class="form-label">{{ $t('model.editor.advanced.legacyThinking.label') }}</label>
          <t-select
            :model-value="thinkingControl || ''"
            @update:model-value="(v: unknown) => emit('update:thinkingControl', String(v ?? ''))"
          >
            <t-option value="" :label="$t('model.editor.advanced.legacyThinking.catalog')" />
            <t-option
              v-for="opt in LEGACY_THINKING_CONTROL_VALUES"
              :key="opt"
              :value="opt"
              :label="opt === 'none' ? $t('model.editor.advanced.legacyThinking.none') : opt"
            />
          </t-select>
          <p class="form-desc form-desc--warn">{{ $t('model.editor.advanced.legacyThinking.desc') }}</p>
        </div>

        <div class="form-item">
          <label class="form-label">{{ $t('model.editor.advanced.compat.label') }}</label>
          <t-textarea
            :model-value="specCompat || ''"
            :autosize="{ minRows: 3, maxRows: 10 }"
            :placeholder="$t('model.editor.advanced.compat.placeholder')"
            class="compat-textarea"
            :status="specCompatError ? 'error' : undefined"
            @update:model-value="(v: string | number) => emit('update:specCompat', String(v))"
          />
          <p v-if="specCompatError" class="form-desc form-desc--error">
            {{ $t('model.editor.advanced.compat.invalid') }}: {{ specCompatError }}
          </p>
          <p v-else class="form-desc">
            {{ $t('model.editor.advanced.compat.desc') }}
            <a
              v-if="COMPAT_DOC_URL"
              :href="COMPAT_DOC_URL"
              target="_blank"
              rel="noopener noreferrer"
              class="compat-doc-link"
            >
              {{ $t('model.editor.advanced.compat.docLink') }}
              <t-icon name="jump" size="12px" />
            </a>
          </p>
        </div>
      </template>
    </template>
  </section>
</template>

<script setup lang="ts">
import { DEFAULT_MODEL_CONTEXT_WINDOW } from '@/utils/contextWindow'
import { docsUrl } from '@/utils/docsUrl'
import {
  LEGACY_THINKING_CONTROL_VALUES,
  PROTOCOL_OPTIONS,
  type EditorModelType,
} from './types'

const COMPAT_DOC_URL = docsUrl('modelsCompat')

function parseOptionalNumber(raw: string | number): number | undefined {
  if (raw === '' || raw === null || raw === undefined) return undefined
  const num = Number(raw)
  return Number.isFinite(num) ? num : undefined
}

defineProps<{
  activeModelType: EditorModelType
  source: 'local' | 'remote'
  modelName: string
  checking: boolean
  dimension?: number
  supportsDimensionOverride?: boolean
  dimensionChecked: boolean
  dimensionSuccess: boolean
  dimensionMessage: string
  checkOllamaDimension: () => Promise<void>
  embeddingAcceptsImages: boolean
  contextWindow?: number
  supportsVision?: boolean
  maxOutputTokens?: number
  maxConcurrency?: number
  advancedOpen: boolean
  isChatLike: boolean
  extraConfig: Record<string, string>
  setExtraConfig: (key: string, value: string | null | undefined) => void
  showLegacyThinkingControl: boolean
  thinkingControl?: string
  specCompat?: string
  specCompatError: string
}>()

const emit = defineEmits<{
  (e: 'update:dimension', value: number | undefined): void
  (e: 'update:supportsDimensionOverride', value: boolean): void
  (e: 'update:embeddingAcceptsImages', value: boolean): void
  (e: 'update:contextWindow', value: number | undefined): void
  (e: 'update:supportsVision', value: boolean): void
  (e: 'update:maxOutputTokens', value: number | undefined): void
  (e: 'update:maxConcurrency', value: number | undefined): void
  (e: 'update:advancedOpen', value: boolean): void
  (e: 'update:thinkingControl', value: string): void
  (e: 'update:specCompat', value: string): void
}>()
</script>

<style lang="less" scoped src="./model-editor-dialog.less"></style>
