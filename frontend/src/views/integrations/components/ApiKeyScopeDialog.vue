<template>
  <SettingDrawer
    :visible="visible"
    :class="mode === 'create' ? 'api-key-create-drawer' : 'api-key-edit-drawer'"
    :title="mode === 'create' ? $t('integrations.api.createApiKey') : $t('integrations.api.editApiKeyScope')"
    :description="drawerDescription"
    :icon="mode === 'create' ? 'lock-on' : 'edit-1'"
    width="560px"
    :min-width="mode === 'create' ? 480 : 440"
    :max-width="mode === 'create' ? 920 : 760"
    :storage-key="mode === 'create' ? 'setting-drawer:width:api-key-create' : 'setting-drawer:width:api-key-scope'"
    :close-on-overlay-click="mode !== 'create'"
    :confirm-text="mode === 'create' ? $t('integrations.api.createApiKey') : $t('common.save')"
    :confirm-loading="confirmLoading"
    @update:visible="(v: boolean) => emit('update:visible', v)"
    @confirm="emit('confirm')"
  >
    <div class="api-key-dialog">
      <div class="api-key-dialog-row">
        <div class="api-key-dialog-row__label">
          <label>{{ $t('integrations.api.apiKeyName') }}</label>
        </div>
        <t-input
          :model-value="name"
          :placeholder="$t('integrations.api.apiKeyNamePlaceholder')"
          @update:model-value="(val: string) => emit('update:name', val)"
        />
      </div>

      <div class="api-key-dialog-row">
        <div class="api-key-dialog-row__label">
          <label>{{ $t('integrations.api.apiKeyAccessType') }}</label>
        </div>
        <t-radio-group
          :model-value="accessMode"
          class="mode-radio api-key-access-type-radio"
          @update:model-value="(val: string | number | boolean) => emit('update:accessMode', val === 'full' ? 'full' : 'scoped')"
        >
          <t-radio-button value="scoped">{{ $t('integrations.api.apiKeyScopedAccess') }}</t-radio-button>
          <t-radio-button value="full">{{ $t('integrations.api.capabilityTenantFull') }}</t-radio-button>
        </t-radio-group>
        <p class="scope-hint">
          {{
            fullAccessEnabled
              ? $t('integrations.api.capabilityTenantFullHint')
              : $t('integrations.api.apiKeyAccessTypeHint')
          }}
        </p>
      </div>

      <div v-if="!fullAccessEnabled" class="api-key-dialog-row">
        <div class="api-key-dialog-row__label">
          <label>{{ $t('integrations.api.apiKeyCapabilities') }}</label>
        </div>
        <div class="api-key-capability-list">
          <div
            v-for="group in capabilityGroups"
            :key="group.key"
            class="api-key-capability-group"
          >
            <div class="api-key-capability-group__header">
              <span>{{ $t(group.labelKey) }}</span>
              <t-button
                size="small"
                variant="text"
                @click="emit('toggle-group', group, !groupAllSelected(group))"
              >
                {{
                  groupAllSelected(group)
                    ? $t('integrations.api.apiKeyCapabilityClearGroup')
                    : $t('integrations.api.apiKeyCapabilitySelectGroup')
                }}
              </t-button>
            </div>
            <div class="api-key-capability-group__items">
              <div
                v-for="capability in group.capabilities"
                :key="capability.value"
                class="api-key-capability-item"
              >
                <t-checkbox
                  :model-value="capabilitySelections[capability.value]"
                  @change="(checked: boolean) => emit('change-capability', capability.value, checked)"
                >
                  {{ $t(capability.labelKey) }}
                </t-checkbox>
                <p class="scope-hint">{{ $t(capability.hintKey) }}</p>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="knowledgeScopeApplies" class="api-key-dialog-row">
        <div class="api-key-dialog-row__label">
          <label>{{ $t('integrations.api.apiKeyKnowledgeScope') }}</label>
        </div>
        <t-select
          :model-value="knowledgeBaseIds"
          multiple
          filterable
          clearable
          :loading="knowledgeBasesLoading"
          :options="knowledgeBaseOptions"
          :placeholder="$t('integrations.api.apiKeyKnowledgeScopePlaceholder')"
          @update:model-value="(val: unknown) => emit('update:knowledgeBaseIds', Array.isArray(val) ? val.map(String) : [])"
        />
        <p v-if="mode === 'edit'" class="scope-hint">{{ $t('integrations.api.editApiKeyScopeHint') }}</p>
      </div>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import type { TenantAPIKeyCapability } from '@/api/tenant'
import type { ApiKeyCapabilityGroup } from '@/config/apiKeyCapabilities'

const props = defineProps<{
  visible: boolean
  mode: 'create' | 'edit'
  editingKeyName?: string
  name: string
  accessMode: 'scoped' | 'full'
  fullAccessEnabled: boolean
  knowledgeScopeApplies: boolean
  knowledgeBaseIds: string[]
  knowledgeBasesLoading: boolean
  knowledgeBaseOptions: Array<{ label: string; value: string }>
  capabilityGroups: readonly ApiKeyCapabilityGroup[]
  capabilitySelections: Record<TenantAPIKeyCapability, boolean>
  groupAllSelected: (group: ApiKeyCapabilityGroup) => boolean
  confirmLoading: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  'update:name': [value: string]
  'update:accessMode': [value: 'scoped' | 'full']
  'update:knowledgeBaseIds': [value: string[]]
  'toggle-group': [group: ApiKeyCapabilityGroup, selected: boolean]
  'change-capability': [capability: TenantAPIKeyCapability, checked: boolean]
  confirm: []
}>()

const { t } = useI18n()

const drawerDescription = computed(() => (
  props.mode === 'create'
    ? t('integrations.api.createApiKeyDialogDesc')
    : t('integrations.api.editApiKeyScopeDesc', { name: props.editingKeyName || '' })
))
</script>

<style scoped lang="less">
.api-key-dialog {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 0;
  border-bottom: 1px solid var(--gy-border-subtle);
}

.api-key-dialog-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 0 16px;
  border-bottom: 1px solid var(--gy-border-subtle);

  &:first-child {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
  }

  &__label {
    min-width: 0;

    label {
      display: flex;
      align-items: center;
      gap: 8px;
      color: var(--gy-text-primary);
      font-size: var(--gy-text-base);
      font-weight: 600;
      line-height: 1.45;

      &::before {
        content: '';
        flex-shrink: 0;
        width: 3px;
        height: 14px;
        border-radius: var(--gy-radius-xs);
        background: var(--gy-brand);
      }
    }

    p {
      margin: 2px 0 0;
      color: var(--gy-text-tertiary);
      font-size: var(--gy-text-sm);
      line-height: 1.5;
    }
  }

  :deep(.t-input),
  :deep(.t-select__wrap) {
    border-radius: var(--gy-radius-xs);
  }

  :deep(.t-input) {
    background-color: var(--gy-surface-subtle);
    border-color: transparent;
    box-shadow: none;
  }

  :deep(.t-input:hover),
  :deep(.t-input.t-is-focused) {
    border-color: var(--gy-border);
    background-color: var(--gy-surface);
  }
}

.mode-radio {
  width: fit-content;
  max-width: 100%;
}

.scope-hint {
  margin: 8px 0 0;
  color: var(--gy-text-tertiary);
  font-size: var(--gy-text-sm);
  line-height: 18px;
}

.api-key-capability-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.api-key-capability-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 0 2px;
}

.api-key-capability-group + .api-key-capability-group {
  border-top: 1px solid var(--gy-border-subtle);
}

.api-key-capability-group__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 24px;
  color: var(--gy-text-primary);
  font-size: var(--gy-text-md);
  font-weight: 600;
}

.api-key-capability-group__items {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.api-key-capability-item .scope-hint {
  margin-top: 2px;
}
</style>
