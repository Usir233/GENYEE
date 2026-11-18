<template>
  <div>
    <!-- Shared Knowledge Bases -->
    <div v-show="currentSection === 'sharedKb'" class="section">
      <div class="section-header">
        <div class="section-header-row">
          <div class="section-header-titlewrap">
            <h2>{{ $t('organization.share.sharedKnowledgeBase') }}</h2>
            <t-popup
              placement="bottom-start"
              trigger="hover"
              overlay-class-name="gy-popover org-permissions-popup-overlay"
              :overlay-inner-style="permissionsHintPopupInnerStyle"
            >
              <button
                type="button"
                class="permissions-trigger-btn"
                :aria-label="$t('organization.settings.permissionCalcFormula')"
                :title="$t('organization.settings.permissionCalcFormula')"
              >
                <t-icon name="info-circle" size="16px" />
              </button>
              <template #content>
                <div class="permission-hint-popover">
                  <p class="permission-hint-title">{{ $t('organization.settings.sharePermissionLabel') }}</p>
                  <p class="permission-hint-desc">{{ $t('organization.settings.permissionCalcTip') }}</p>
                </div>
              </template>
            </t-popup>
          </div>
        </div>
        <p class="section-description">{{ $t('organization.settings.sharedDesc') }}</p>
      </div>

      <div class="shared-resources-wrap">
        <div class="members-list-header">
          <div class="members-list-titlewrap">
            <span class="members-list-title">{{ $t('organization.sharedResources.kbListTitle') }}</span>
            <span class="members-list-count-badge">{{ sharedKnowledgeBases.length }}</span>
          </div>
        </div>

        <div v-if="sharesLoading && sharedKnowledgeBases.length === 0" class="loading-inline">
          <t-loading size="small" />
          <span>{{ $t('organization.sharedResources.loading') }}</span>
        </div>
        <div v-else-if="sharedKnowledgeBases.length === 0" class="empty-state">
          <t-empty>
            <template #description>
              <p class="empty-state-title">{{ $t('organization.settings.noSharedKB') }}</p>
              <p class="empty-state-desc">{{ $t('organization.settings.noSharedKBTip') }}</p>
            </template>
          </t-empty>
        </div>
        <div v-else class="data-table-shell shared-resources-table">
          <t-table
            row-key="id"
            :data="sharedKnowledgeBases"
            :columns="sharedKbColumns"
            size="medium"
            hover
            stripe
            :loading="sharesLoading"
            class="shared-kb-table"
          >
            <template #name="{ row }">
              <span class="resource-name" :title="row.knowledge_base_name">{{ row.knowledge_base_name }}</span>
            </template>
            <template #shared_by="{ row }">
              <span class="resource-meta">{{ row.shared_by_username || '—' }}</span>
            </template>
            <template #created_at="{ row }">{{ formatOrgDate(row.created_at) }}</template>
            <template #space_permission="{ row }">
              <t-tag size="small" :theme="getPermissionTheme(row.permission)" variant="light">
                {{ sharePermissionLabel(row.permission) }}
              </t-tag>
            </template>
            <template #my_permission="{ row }">
              <t-tag
                size="small"
                :theme="getPermissionTheme(row.my_permission ?? row.permission)"
                variant="light"
              >
                {{ sharePermissionLabel(row.my_permission ?? row.permission) }}
              </t-tag>
            </template>
            <template #actions="{ row }">
              <div class="resource-row-actions">
                <t-tooltip :content="$t('knowledgeList.detail.goToKb')" placement="top">
                  <t-button
                    theme="primary"
                    shape="square"
                    variant="text"
                    size="small"
                    :aria-label="$t('knowledgeList.detail.goToKb')"
                    @click.stop="emit('shareClick', row)"
                  >
                    <template #icon><t-icon name="browse" /></template>
                  </t-button>
                </t-tooltip>
                <t-popconfirm
                  v-if="isAdmin"
                  :content="$t('organization.settings.removeShareConfirm', { name: row.knowledge_base_name || row.knowledge_base_id })"
                  :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                  :cancel-btn="{ content: $t('common.cancel') }"
                  placement="left"
                  @confirm="emit('removeShare', row)"
                >
                  <t-tooltip :content="$t('organization.settings.removeShareFromOrg')" placement="top">
                    <t-button
                      theme="danger"
                      shape="square"
                      variant="text"
                      size="small"
                      :aria-label="$t('organization.settings.removeShareFromOrg')"
                      @click.stop
                    >
                      <template #icon><t-icon name="delete" /></template>
                    </t-button>
                  </t-tooltip>
                </t-popconfirm>
              </div>
            </template>
          </t-table>
        </div>
      </div>
    </div>

    <!-- Shared Agents -->
    <div v-show="currentSection === 'sharedAgents'" class="section">
      <div class="section-header">
        <div class="section-header-row">
          <div class="section-header-titlewrap">
            <h2>{{ $t('organization.settings.sharedAgents') }}</h2>
            <t-popup
              placement="bottom-start"
              trigger="hover"
              overlay-class-name="gy-popover org-permissions-popup-overlay"
              :overlay-inner-style="permissionsHintPopupInnerStyle"
            >
              <button
                type="button"
                class="permissions-trigger-btn"
                :aria-label="$t('organization.settings.sharedAgentsKbHintShort')"
                :title="$t('organization.settings.sharedAgentsKbHintShort')"
              >
                <t-icon name="info-circle" size="16px" />
              </button>
              <template #content>
                <div class="permission-hint-popover">
                  <p class="permission-hint-title">{{ $t('organization.settings.sharedAgents') }}</p>
                  <p class="permission-hint-desc">{{ $t('organization.settings.sharedAgentsKbHint') }}</p>
                </div>
              </template>
            </t-popup>
          </div>
        </div>
        <p class="section-description">{{ $t('organization.settings.sharedAgentsDesc') }}</p>
      </div>

      <div class="shared-resources-wrap">
        <div class="members-list-header">
          <div class="members-list-titlewrap">
            <span class="members-list-title">{{ $t('organization.sharedResources.agentListTitle') }}</span>
            <span class="members-list-count-badge">{{ sharedAgents.length }}</span>
          </div>
        </div>

        <div v-if="sharedAgents.length === 0" class="empty-state">
          <t-empty>
            <template #description>
              <p class="empty-state-title">{{ $t('organization.settings.noSharedAgents') }}</p>
              <p class="empty-state-desc">{{ $t('organization.settings.noSharedAgentsTip') }}</p>
            </template>
          </t-empty>
        </div>
        <div v-else class="data-table-shell shared-resources-table">
          <t-table
            row-key="id"
            :data="sharedAgents"
            :columns="sharedAgentColumns"
            size="medium"
            hover
            stripe
            class="shared-agent-table"
          >
            <template #name="{ row }">
              <span class="resource-name" :title="row.agent_name || row.agent_id">
                {{ row.agent_name || row.agent_id }}
              </span>
            </template>
            <template #shared_by="{ row }">
              <span class="resource-meta">{{ row.shared_by_username || '—' }}</span>
            </template>
            <template #created_at="{ row }">{{ formatOrgDate(row.created_at) }}</template>
            <template #scope_kb="{ row }">
              <span class="resource-meta" :title="agentKbScopeLabel(row)">{{ agentKbScopeLabel(row) }}</span>
            </template>
            <template #scope_web_search="{ row }">
              <span class="resource-meta">{{ agentWebSearchScopeLabel(row) }}</span>
            </template>
            <template #scope_mcp="{ row }">
              <span class="resource-meta" :title="agentMcpScopeLabel(row)">{{ agentMcpScopeLabel(row) }}</span>
            </template>
            <template #permission>
              <t-tag size="small" theme="default" variant="light">
                {{ $t('organization.share.permissionReadonly') }}
              </t-tag>
            </template>
            <template #actions="{ row }">
              <t-popconfirm
                v-if="isAdmin"
                :content="$t('organization.settings.removeAgentShareConfirm', { name: row.agent_name || row.agent_id })"
                :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }"
                placement="left"
                @confirm="emit('removeAgentShare', row)"
              >
                <t-tooltip :content="$t('organization.settings.removeShareFromOrg')" placement="top">
                  <t-button
                    theme="danger"
                    shape="square"
                    variant="text"
                    size="small"
                    :aria-label="$t('organization.settings.removeShareFromOrg')"
                    @click.stop
                  >
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-tooltip>
              </t-popconfirm>
            </template>
          </t-table>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeBaseShare, AgentShareResponse } from '@/api/organization'
import { getPermissionTheme, formatOrgDate } from '../composables/useOrganizationSettings'

const props = defineProps<{
  currentSection: string
  isAdmin: boolean
  sharedKnowledgeBases: KnowledgeBaseShare[]
  sharedAgents: AgentShareResponse[]
  sharesLoading: boolean
}>()

const emit = defineEmits<{
  (e: 'shareClick', share: KnowledgeBaseShare): void
  (e: 'removeShare', share: KnowledgeBaseShare): void
  (e: 'removeAgentShare', share: AgentShareResponse): void
}>()

const { t } = useI18n()

const permissionsHintPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(400px, calc(100vw - 24px))',
  maxWidth: 'min(400px, calc(100vw - 24px))',
  maxHeight: 'min(280px, 65vh)',
  overflow: 'hidden',
}

const sharedKbColumns = computed(() => [
  { colKey: 'name', title: t('organization.sharedResources.columns.name'), ellipsis: true, minWidth: 180 },
  { colKey: 'shared_by', title: t('organization.sharedResources.columns.sharedBy'), width: 120, ellipsis: true },
  { colKey: 'created_at', title: t('organization.sharedResources.columns.sharedAt'), width: 154 },
  { colKey: 'space_permission', title: t('organization.settings.sharePermissionLabel'), width: 108 },
  { colKey: 'my_permission', title: t('organization.settings.myPermissionLabel'), width: 96 },
  {
    colKey: 'actions',
    title: t('organization.members.columns.operations'),
    width: props.isAdmin ? 96 : 64,
    align: 'left' as const,
  },
])

const sharedAgentColumns = computed(() => {
  const cols: Array<{
    colKey: string
    title: string
    ellipsis?: boolean
    minWidth?: number
    width?: number
    align?: 'left' | 'center' | 'right'
  }> = [
    { colKey: 'name', title: t('organization.sharedResources.columns.name'), ellipsis: true, minWidth: 160 },
    { colKey: 'shared_by', title: t('organization.sharedResources.columns.sharedBy'), width: 108, ellipsis: true },
    { colKey: 'created_at', title: t('organization.sharedResources.columns.sharedAt'), width: 118 },
    { colKey: 'scope_kb', title: t('agent.shareScope.knowledgeBase'), width: 120, ellipsis: true },
    { colKey: 'scope_web_search', title: t('agent.shareScope.webSearch'), width: 88, ellipsis: true },
    { colKey: 'scope_mcp', title: t('agent.shareScope.mcp'), width: 108, ellipsis: true },
    { colKey: 'permission', title: t('organization.sharedResources.columns.permission'), width: 80 },
  ]
  if (props.isAdmin) {
    cols.push({ colKey: 'actions', title: t('organization.members.columns.operations'), width: 72, align: 'left' })
  }
  return cols
})

function sharePermissionLabel(permission: string): string {
  if (permission === 'editor' || permission === 'admin') {
    return t('organization.share.permissionEditable')
  }
  return t('organization.share.permissionReadonly')
}

function agentKbScopeLabel(share: AgentShareResponse): string {
  if (share.scope_kb === undefined || share.scope_kb === '') return '—'
  if (share.scope_kb === 'all') return t('agent.shareScope.kbAll')
  if (share.scope_kb === 'selected' && (share.scope_kb_count ?? 0) > 0) {
    return t('agent.shareScope.kbSelected', { count: share.scope_kb_count })
  }
  return t('agent.shareScope.kbNone')
}

function agentWebSearchScopeLabel(share: AgentShareResponse): string {
  if (share.scope_web_search === undefined) return '—'
  return share.scope_web_search ? t('agent.shareScope.enabled') : t('agent.shareScope.disabled')
}

function agentMcpScopeLabel(share: AgentShareResponse): string {
  if (share.scope_mcp === undefined || share.scope_mcp === '') return '—'
  if (share.scope_mcp === 'all') return t('agent.shareScope.mcpAll')
  if (share.scope_mcp === 'selected' && (share.scope_mcp_count ?? 0) > 0) {
    return t('agent.shareScope.mcpSelected', { count: share.scope_mcp_count })
  }
  return t('agent.shareScope.mcpNone')
}
</script>
