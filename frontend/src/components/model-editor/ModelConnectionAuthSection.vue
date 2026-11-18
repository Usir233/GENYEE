<template>
  <div class="form-item">
    <label class="form-label required">{{ $t('model.editor.baseUrlLabel') }}</label>
    <t-input
      :model-value="baseUrl || ''"
      :placeholder="baseUrlPlaceholder"
      @update:model-value="(v: string | number) => emit('update:baseUrl', String(v))"
    />
  </div>

  <div class="form-item">
    <label class="form-label" :class="{ required: apiKeyRequired }">{{ apiKeyLabel }}</label>
    <CredentialResource
      v-if="isEdit && modelId"
      :api="credentialApi"
      :fields="credentialFields"
      :meta="credentialMeta"
      @changed="emit('invalidate-connection-test')"
    />
    <t-input
      v-else
      :model-value="apiKey || ''"
      type="password"
      :placeholder="apiKeyPlaceholder"
      class="api-key-input"
      autocomplete="new-password"
      spellcheck="false"
      @update:model-value="(v: string | number) => emit('update:apiKey', String(v))"
    >
      <template #prefix-icon><t-icon name="lock-on" /></template>
    </t-input>
    <p v-if="apiKeyHint" class="form-desc">{{ apiKeyHint }}</p>
  </div>

  <div v-if="secretExtraField && !isEdit" class="form-item">
    <label class="form-label" :class="{ required: secretExtraField.required }">
      {{ extraFieldDisplayLabel(secretExtraField) }}
    </label>
    <t-input
      :model-value="appSecret || ''"
      type="password"
      :placeholder="extraFieldDisplayPlaceholder(secretExtraField)"
      autocomplete="new-password"
      spellcheck="false"
      @update:model-value="(v: string | number) => emit('update:appSecret', String(v))"
    >
      <template #prefix-icon><t-icon name="lock-on" /></template>
    </t-input>
    <p v-if="extraFieldDisplayPlaceholder(secretExtraField)" class="form-desc">
      {{ extraFieldDisplayPlaceholder(secretExtraField) }}
    </p>
  </div>

  <div v-for="field in plainExtraFields" :key="field.key" class="form-item">
    <label class="form-label" :class="{ required: field.required }">{{ extraFieldDisplayLabel(field) }}</label>
    <div v-if="field.type === 'boolean'" class="vision-toggle">
      <t-switch
        :model-value="extraConfigBool(field.key)"
        @update:model-value="(v: string | number | boolean) => setExtraConfig(field.key, v ? 'true' : 'false')"
      />
      <span v-if="extraFieldDisplayPlaceholder(field)" class="form-desc form-desc--inline">
        {{ extraFieldDisplayPlaceholder(field) }}
      </span>
    </div>
    <t-select
      v-else-if="field.type === 'select'"
      :model-value="extraConfig[field.key] || ''"
      :placeholder="extraFieldDisplayPlaceholder(field)"
      clearable
      @update:model-value="(v: unknown) => setExtraConfig(field.key, String(v ?? ''))"
    >
      <t-option
        v-for="opt in (field.options || [])"
        :key="opt.value"
        :value="opt.value"
        :label="extraFieldDisplayOptionLabel(opt)"
      />
    </t-select>
    <t-input
      v-else-if="field.type === 'number'"
      :model-value="extraConfig[field.key] || ''"
      type="number"
      :placeholder="extraFieldDisplayPlaceholder(field)"
      @update:model-value="(v: string | number) => setExtraConfig(field.key, String(v ?? ''))"
    />
    <t-input
      v-else-if="field.type === 'password'"
      :model-value="extraConfig[field.key] || ''"
      type="password"
      :placeholder="extraFieldDisplayPlaceholder(field)"
      autocomplete="new-password"
      spellcheck="false"
      @update:model-value="(v: string | number) => setExtraConfig(field.key, String(v))"
    >
      <template #prefix-icon><t-icon name="lock-on" /></template>
    </t-input>
    <t-input
      v-else
      :model-value="extraConfig[field.key] || ''"
      :placeholder="extraFieldDisplayPlaceholder(field)"
      @update:model-value="(v: string | number) => setExtraConfig(field.key, String(v))"
    />
  </div>

  <div class="form-item">
    <div class="custom-headers-header">
      <label class="form-label" style="margin-bottom: 0;">{{ $t('model.editor.customHeadersLabel') }}</label>
      <t-button variant="text" size="small" theme="primary" @click="addCustomHeader">
        <template #icon><t-icon name="add" /></template>
        {{ $t('model.editor.customHeadersAdd') }}
      </t-button>
    </div>
    <p class="form-desc custom-headers-desc">{{ $t('model.editor.customHeadersDesc') }}</p>
    <div v-if="customHeaders && customHeaders.length > 0" class="custom-headers-list">
      <div v-for="(item, idx) in customHeaders" :key="idx" class="custom-header-row">
        <t-input
          v-model="item.key"
          :placeholder="$t('model.editor.customHeadersKeyPlaceholder')"
          class="custom-header-key"
        />
        <t-input
          v-model="item.value"
          :placeholder="$t('model.editor.customHeadersValuePlaceholder')"
          class="custom-header-value"
        />
        <t-button
          variant="text"
          shape="square"
          size="small"
          class="custom-header-remove"
          :aria-label="$t('common.delete')"
          @click="removeCustomHeader(idx)"
        >
          <t-icon name="close" />
        </t-button>
      </div>
    </div>
  </div>

  <div v-if="showResolvedPanel" class="form-item">
    <div class="resolved-panel" aria-live="polite">
      <div class="resolved-panel__header">
        <t-icon name="system-code" class="resolved-panel__icon" />
        <span class="resolved-panel__title">{{ $t('model.editor.resolved.title') }}</span>
        <t-icon v-if="resolving" name="loading" class="spinning resolved-panel__loading" />
      </div>
      <p v-if="!resolved && !resolving && !resolveFailed" class="form-desc">{{ $t('model.editor.resolved.empty') }}</p>
      <p v-else-if="resolveFailed" class="form-desc form-desc--warn">
        {{ $t('model.editor.resolved.failed') }}<template v-if="resolveError">: {{ resolveError }}</template>
      </p>
      <dl v-else-if="resolved" class="resolved-panel__grid">
        <dt>{{ $t('model.editor.resolved.protocol') }}</dt>
        <dd><code>{{ resolved.api }}</code></dd>
        <dt>{{ $t('model.editor.resolved.catalog') }}</dt>
        <dd>
          <span class="catalog-badge" :class="{ 'catalog-badge--accent': resolved.cataloged }">
            {{ resolved.cataloged ? $t('model.editor.resolved.catalogedYes') : $t('model.editor.resolved.catalogedNo') }}
          </span>
        </dd>
        <template v-if="resolved.url">
          <dt>{{ $t('model.editor.resolved.endpoint') }}</dt>
          <dd><code class="resolved-endpoint">{{ resolved.url }}</code></dd>
        </template>
        <template v-if="resolved.remote_model && resolved.remote_model !== modelName">
          <dt>{{ $t('model.editor.advanced.remoteModelName.label') }}</dt>
          <dd><code>{{ resolved.remote_model }}</code></dd>
        </template>
        <template v-if="isChatLike">
          <dt>{{ $t('model.editor.resolved.thinkingFormat') }}</dt>
          <dd><code>{{ resolved.capabilities?.thinking_format || '-' }}</code></dd>
          <dt>{{ $t('model.editor.resolved.thinkingLevels') }}</dt>
          <dd>
            <template v-if="resolvedThinkingLevels.length > 0">
              <span v-for="level in resolvedThinkingLevels" :key="level" class="catalog-badge">
                {{ $t(levelLabelKey(level)) }}
              </span>
            </template>
            <span v-else class="form-desc form-desc--inline">{{ $t('model.editor.resolved.noThinking') }}</span>
          </dd>
          <template v-if="resolved.capabilities?.context_window">
            <dt>{{ $t('model.editor.contextWindowLabel') }}</dt>
            <dd>{{ formatTokenCount(resolved.capabilities.context_window) }}</dd>
          </template>
          <template v-if="resolved.capabilities?.max_output_tokens">
            <dt>{{ $t('model.editor.maxOutputTokensLabel') }}</dt>
            <dd>{{ formatTokenCount(resolved.capabilities.max_output_tokens) }}</dd>
          </template>
        </template>
      </dl>
    </div>
  </div>
</template>

<script setup lang="ts">
import type {
  ModelProviderExtraField,
  ModelProviderExtraFieldOption,
  ReasoningEffortLevel,
  ResolvedModelCatalog,
} from '@/api/initialization'
import type { CredentialFieldMeta, ModelCredentialField } from '@/api/model'
import CredentialResource, {
  type CredentialFieldDef,
  type CredentialResourceApi,
} from '@/components/credentials/CredentialResource.vue'
import { formatTokenCount } from '@/utils/contextWindow'
import { levelLabelKey } from '@/utils/reasoningEffort'
import type { CustomHeaderItem } from './types'

defineProps<{
  isEdit: boolean
  modelId?: string
  modelName: string
  baseUrl?: string
  baseUrlPlaceholder: string
  apiKey?: string
  apiKeyLabel: string
  apiKeyRequired: boolean
  apiKeyPlaceholder: string
  apiKeyHint: string
  appSecret?: string
  credentialApi: CredentialResourceApi<ModelCredentialField>
  credentialFields: CredentialFieldDef<ModelCredentialField>[]
  credentialMeta: Partial<Record<ModelCredentialField, CredentialFieldMeta>>
  secretExtraField?: ModelProviderExtraField
  plainExtraFields: ModelProviderExtraField[]
  extraConfig: Record<string, string>
  extraConfigBool: (key: string) => boolean
  setExtraConfig: (key: string, value: string | null | undefined) => void
  extraFieldDisplayLabel: (field: ModelProviderExtraField) => string
  extraFieldDisplayPlaceholder: (field: ModelProviderExtraField) => string
  extraFieldDisplayOptionLabel: (option: ModelProviderExtraFieldOption) => string
  customHeaders?: CustomHeaderItem[]
  addCustomHeader: () => void
  removeCustomHeader: (idx: number) => void
  showResolvedPanel: boolean
  resolving: boolean
  resolved: ResolvedModelCatalog | null
  resolveFailed: boolean
  resolveError: string
  isChatLike: boolean
  resolvedThinkingLevels: ReasoningEffortLevel[]
}>()

const emit = defineEmits<{
  (e: 'update:baseUrl', value: string): void
  (e: 'update:apiKey', value: string): void
  (e: 'update:appSecret', value: string): void
  (e: 'invalidate-connection-test'): void
}>()
</script>

<style lang="less" scoped src="./model-editor-dialog.less"></style>
