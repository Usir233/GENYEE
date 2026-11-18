<template>
  <div class="form-item">
    <label class="form-label">{{ $t('model.editor.providerLabel') }}</label>
    <t-select
      :model-value="provider || ''"
      :placeholder="$t('model.editor.providerPlaceholder')"
      :loading="loadingProviders"
      filterable
      :popup-props="{ overlayClassName: 'gy-popover provider-select-popup', overlayInnerStyle: matchTriggerWidth }"
      @update:model-value="(v: unknown) => emit('update:provider', String(v ?? ''))"
      @change="(v: unknown) => handleProviderChange(String(v ?? ''))"
    >
      <template #valueDisplay>
        <span v-if="provider" class="provider-value">
          <img v-if="selectedProviderIcon" :src="selectedProviderIcon" class="provider-icon" alt="" />
          <span class="provider-value__name">{{ selectedProviderDisplayLabel }}</span>
        </span>
      </template>
      <t-option
        v-for="opt in providerOptions"
        :key="opt.value"
        :value="opt.value"
        :label="providerDisplayLabel(opt)"
        :show-overflow-tooltip="false"
      >
        <div class="provider-option">
          <img v-if="providerDisplayIcon(opt)" :src="providerDisplayIcon(opt)" class="provider-icon" alt="" />
          <span v-else class="provider-icon provider-icon--placeholder" aria-hidden="true">
            {{ providerDisplayLabel(opt).slice(0, 1) }}
          </span>
          <div class="provider-option__text">
            <span class="provider-name">{{ providerDisplayLabel(opt) }}</span>
            <span class="provider-desc">{{ providerDisplayDescription(opt) }}</span>
          </div>
        </div>
      </t-option>
    </t-select>
    <p v-if="vendorDocLink" class="form-desc provider-doc-link">
      <a :href="vendorDocLink" target="_blank" rel="noopener noreferrer">
        {{ $t('model.editor.providerDocs', { provider: selectedProviderDisplayLabel }) }}
        <t-icon name="jump" size="12px" />
      </a>
    </p>
  </div>

  <div class="form-item">
    <label class="form-label required">{{ $t('model.modelName') }}</label>
    <t-input
      v-if="catalogModelOptions.length === 0"
      :model-value="modelName"
      :placeholder="modelNamePlaceholder"
      clearable
      autocomplete="off"
      spellcheck="false"
      @update:model-value="(v: string | number) => emit('update:modelName', String(v))"
    />
    <t-select
      v-else
      :model-value="modelName"
      filterable
      creatable
      clearable
      :placeholder="modelNamePlaceholder"
      :popup-props="{ overlayClassName: 'gy-popover catalog-model-select-popup', overlayInnerStyle: matchTriggerWidth }"
      @update:model-value="(v: unknown) => emit('update:modelName', String(v ?? ''))"
      @create="handleCatalogModelCreate"
      @change="handleCatalogModelChange"
    >
      <t-option
        v-for="opt in catalogModelOptions"
        :key="opt.value"
        :value="opt.value"
        :label="opt.label"
        :show-overflow-tooltip="false"
      >
        <div class="catalog-model-option">
          <span class="catalog-model-option__name">{{ opt.label }}</span>
          <span v-if="opt.value !== opt.label" class="catalog-model-option__id">{{ opt.value }}</span>
          <span class="catalog-model-option__badges">
            <span v-if="opt.contextWindow" class="catalog-badge">{{ opt.contextWindow }}</span>
            <span v-if="opt.dimension" class="catalog-badge">{{ $t('model.editor.dimensionLabel') }} {{ opt.dimension }}</span>
            <span v-if="opt.reasoning" class="catalog-badge catalog-badge--accent">{{ $t('model.editor.catalog.reasoning') }}</span>
            <span v-if="opt.vision" class="catalog-badge catalog-badge--accent">{{ $t('model.editor.catalog.vision') }}</span>
          </span>
        </div>
      </t-option>
    </t-select>
    <p v-if="catalogModelOptions.length > 0" class="form-desc">{{ $t('model.editor.catalog.hint') }}</p>
  </div>

  <div class="form-item">
    <label class="form-label">{{ $t('model.editor.displayNameLabel') }}</label>
    <t-input
      :model-value="displayName || ''"
      :placeholder="$t('model.editor.displayNamePlaceholder')"
      @update:model-value="(v: string | number) => emit('update:displayName', String(v))"
    />
    <p class="form-desc">{{ $t('model.editor.displayNameDesc') }}</p>
  </div>
</template>

<script setup lang="ts">
import type { CanonicalModelProviderOption, CatalogModelOption } from './types'

const matchTriggerWidth = (triggerElement: HTMLElement) => ({
  width: `${triggerElement.offsetWidth}px`,
})

defineProps<{
  provider?: string
  modelName: string
  displayName?: string
  loadingProviders: boolean
  providerOptions: CanonicalModelProviderOption[]
  selectedProviderIcon: string
  selectedProviderDisplayLabel: string
  providerDisplayLabel: (p: CanonicalModelProviderOption) => string
  providerDisplayDescription: (p: CanonicalModelProviderOption) => string
  providerDisplayIcon: (p: CanonicalModelProviderOption) => string
  vendorDocLink: string
  catalogModelOptions: CatalogModelOption[]
  modelNamePlaceholder: string
  handleProviderChange: (value: string) => void
  handleCatalogModelCreate: (value: string | number | boolean | bigint) => void
  handleCatalogModelChange: (value: unknown) => void
}>()

const emit = defineEmits<{
  (e: 'update:provider', value: string): void
  (e: 'update:modelName', value: string): void
  (e: 'update:displayName', value: string): void
}>()
</script>

<style lang="less" scoped src="./model-editor-dialog.less"></style>
