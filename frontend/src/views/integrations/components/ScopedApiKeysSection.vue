<template>
  <div class="api-key-section">
    <div class="api-key-section__header">
      <div class="api-key-section__title">
        <label>{{ $t('integrations.api.apiKeys') }}</label>
        <p>{{ $t('integrations.api.apiKeysDesc') }}</p>
      </div>
      <t-button size="small" variant="outline" @click="emit('create')">
        <template #icon><t-icon name="add" /></template>
        {{ $t('integrations.api.createApiKey') }}
      </t-button>
    </div>
    <div class="api-key-section__body">
      <div class="api-key-list" :class="{ 'api-key-list--loading': loading }">
        <div v-if="loading" class="api-key-list__empty">
          <t-loading size="small" />
          <span>{{ $t('integrations.api.loading') }}</span>
        </div>
        <div v-else-if="apiKeys.length === 0" class="api-key-list__empty">
          {{ $t('integrations.api.noApiKeys') }}
        </div>
        <div v-else class="api-key-table-wrap">
          <table class="api-key-table">
            <thead>
              <tr>
                <th scope="col">{{ $t('integrations.api.apiKeyName') }}</th>
                <th scope="col">{{ $t('integrations.api.apiKeyValue') }}</th>
                <th scope="col">{{ $t('integrations.api.apiKeyAccessMode') }}</th>
                <th scope="col">{{ $t('integrations.api.apiKeyKnowledgeScope') }}</th>
                <th scope="col">{{ $t('integrations.api.createdAt') }}</th>
                <th scope="col" class="api-key-table__actions-heading">{{ $t('integrations.api.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="key in apiKeys" :key="key.id">
                <td>
                  <span class="api-key-name">{{ key.name }}</span>
                </td>
                <td>
                  <code class="api-key-fingerprint">{{ formatKeyMaskedValue(key) }}</code>
                </td>
                <td>
                  <div class="api-key-access-cell" :title="formatApiKeyCapabilitiesTitle(key)">
                    <span
                      v-if="key.full_access"
                      class="api-key-access-mode api-key-access-mode--full"
                    >
                      {{ formatApiKeyAccessModeLabel(key) }}
                    </span>
                    <div v-if="!key.full_access" class="api-key-capability-chips">
                      <span
                        v-for="label in keyCapabilityLabels(key)"
                        :key="label"
                        class="api-key-capability-chip"
                      >
                        {{ label }}
                      </span>
                    </div>
                  </div>
                </td>
                <td>
                  <span class="api-key-knowledge-scope">
                    {{ formatKeyKnowledgeScope(key.knowledge_base_ids) }}
                  </span>
                </td>
                <td>
                  <span class="api-key-created-at">{{ formatDate(key.created_at) }}</span>
                </td>
                <td>
                  <div class="api-key-table__actions">
                    <t-button
                      shape="square"
                      variant="text"
                      :title="$t('integrations.api.editApiKeyScope')"
                      :aria-label="`${key.name} · ${$t('integrations.api.editApiKeyScope')}`"
                      @click="emit('edit', key)"
                    >
                      <t-icon name="edit-1" />
                    </t-button>
                    <t-button
                      shape="square"
                      variant="text"
                      :title="$t('integrations.api.copy')"
                      :aria-label="`${key.name} · ${$t('integrations.api.copy')}`"
                      @click="emit('copy', key.api_key)"
                    >
                      <t-icon name="file-copy" />
                    </t-button>
                    <t-button
                      shape="square"
                      variant="text"
                      theme="danger"
                      :title="$t('integrations.api.deleteApiKey')"
                      :aria-label="`${key.name} · ${$t('integrations.api.deleteApiKey')}`"
                      @click="emit('delete', key.id)"
                    >
                      <t-icon name="delete" />
                    </t-button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { TenantAPIKey } from '@/api/tenant'

defineProps<{
  apiKeys: TenantAPIKey[]
  loading: boolean
  keyCapabilityLabels: (key: TenantAPIKey) => string[]
  formatApiKeyCapabilitiesTitle: (key: TenantAPIKey) => string
  formatApiKeyAccessModeLabel: (key: TenantAPIKey) => string
  formatKeyKnowledgeScope: (ids: readonly string[] | null | undefined) => string
  formatKeyMaskedValue: (key: TenantAPIKey) => string
  formatDate: (value?: string) => string
}>()

const emit = defineEmits<{
  create: []
  edit: [key: TenantAPIKey]
  copy: [apiKey: string]
  delete: [id: number]
}>()
</script>

<style scoped lang="less">
.api-key-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 20px 0;
  border-bottom: 1px solid var(--gy-border-subtle);
}

.api-key-section__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.api-key-section__title {
  min-width: 0;

  label {
    display: block;
    margin-bottom: 6px;
    color: var(--gy-text-primary);
    font-size: var(--gy-text-lg);
    font-weight: 600;
    line-height: 1.4;
  }

  p {
    margin: 0;
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-md);
    line-height: 1.55;
  }
}

.api-key-section__body {
  min-width: 0;
}

.api-key-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-md);
  background: var(--gy-surface);
  overflow: hidden;
}

.api-key-list__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 88px;
  color: var(--gy-text-secondary);
  font-size: var(--gy-text-sm);
}

.api-key-table-wrap {
  width: 100%;
  overflow-x: auto;
}

.api-key-table {
  width: 100%;
  min-width: 960px;
  border-collapse: collapse;
  table-layout: fixed;

  th,
  td {
    padding: 13px 14px;
    border-bottom: 1px solid var(--gy-border-subtle);
    text-align: left;
    vertical-align: middle;
  }

  th {
    background: var(--gy-surface-subtle);
    color: var(--gy-text-tertiary);
    font-size: var(--gy-text-sm);
    font-weight: 500;
    line-height: 1.4;
  }

  td {
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-md);
    line-height: 1.45;
  }

  th:nth-child(1),
  td:nth-child(1) {
    width: 18%;
  }

  th:nth-child(2),
  td:nth-child(2) {
    width: 22%;
  }

  th:nth-child(3),
  td:nth-child(3) {
    width: 24%;
  }

  th:nth-child(4),
  td:nth-child(4) {
    width: 16%;
  }

  th:nth-child(5),
  td:nth-child(5) {
    width: 12%;
  }

  th:nth-child(6),
  td:nth-child(6) {
    width: 112px;
  }

  tbody tr:last-child td {
    border-bottom: none;
  }

  th.api-key-table__actions-heading {
    text-align: right;
  }
}

.api-key-table__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

.api-key-name {
  display: block;
  min-width: 0;
  color: var(--gy-text-primary);
  font-size: var(--gy-text-md);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-fingerprint {
  display: inline-block;
  max-width: 100%;
  color: var(--gy-text-secondary);
  font-family: var(--gy-font-mono);
  font-size: var(--gy-text-sm);
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: top;
  white-space: nowrap;
}

.api-key-access-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: 7px;
}

.api-key-access-mode {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  height: 24px;
  padding: 0 9px;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-sm);
  background: var(--gy-surface-subtle);
  color: var(--gy-text-primary);
  font-size: var(--gy-text-sm);
  font-weight: 600;
  line-height: 22px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-access-mode--full {
  border-color: color-mix(in oklab, var(--gy-brand) 28%, transparent);
  background: color-mix(in oklab, var(--gy-brand) 10%, var(--gy-surface));
  color: var(--gy-brand);
}

.api-key-capability-chips {
  display: flex;
  max-width: 100%;
  flex-wrap: wrap;
  gap: 5px;
}

.api-key-capability-chip {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  height: 22px;
  padding: 0 8px;
  border-radius: var(--gy-radius-sm);
  background: color-mix(in oklab, var(--gy-success) 10%, var(--gy-surface));
  color: var(--gy-success);
  font-size: var(--gy-text-sm);
  font-weight: 500;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-key-knowledge-scope,
.api-key-created-at {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
