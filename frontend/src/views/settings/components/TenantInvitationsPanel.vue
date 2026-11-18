<template>
  <div class="pending-invitations-section">
    <div class="pending-invitations-header">
      <span class="pending-invitations-desc">
        {{ $t('tenantInvitation.pendingSectionDesc', { days: INVITATION_TTL_DAYS }) }}
      </span>
    </div>
    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('tenantMember.loading') }}</span>
    </div>
    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="emit('retry')">{{ $t('tenantMember.retry') }}</t-button>
        </template>
      </t-alert>
    </div>
    <div v-else-if="total === 0" class="pending-invitations-empty">
      {{ $t('tenantInvitation.pendingEmpty') }}
    </div>
    <div v-else class="data-table-shell data-table-shell--with-footer pending-invitations-table">
      <div class="data-table-shell__scroll">
        <t-table row-key="id" :data="invitations" :columns="invitationColumns" size="medium" hover>
          <template #invitee="{ row }">
            <div class="member-cell">
              <template v-if="row.is_share_link">
                <span class="member-name share-link-title">
                  <t-icon name="link" size="14px" />
                  {{ $t('tenantInvitation.shareLink.cellTitle') }}
                </span>
                <span class="member-email">
                  {{
                    (row.accepted_count ?? 0) > 0
                      ? $t('tenantInvitation.shareLink.cellAccepted', { count: row.accepted_count })
                      : $t('tenantInvitation.shareLink.cellEmpty')
                  }}
                </span>
              </template>
              <template v-else>
                <span class="member-name">{{ inviteePrimary(row) }}</span>
                <span v-if="row.invitee_email && row.invitee_name" class="member-email">
                  {{ row.invitee_email }}
                </span>
              </template>
            </div>
          </template>
          <template #role="{ row }">
            <t-tag :theme="roleTagTheme(row.role)" size="small">
              {{ $t('tenantMember.role.' + row.role) }}
            </t-tag>
          </template>
          <template #inviter="{ row }">
            <span>{{ inviterPrimary(row) }}</span>
          </template>
          <template #expires_at="{ row }">{{ formatTenantDate(row.expires_at, locale) }}</template>
          <template #status="{ row }">
            <t-tag :theme="invitationStatusTheme(row.status)" size="small">
              {{
                row.is_share_link && row.status === 'pending'
                  ? $t('tenantInvitation.status.shareLinkActive')
                  : $t('tenantInvitation.status.' + row.status)
              }}
            </t-tag>
          </template>
          <template #actions="{ row }">
            <t-tooltip
              v-if="row.status === 'pending' && row.invite_url"
              :content="$t('tenantInvitation.copyLink')"
              placement="top"
            >
              <t-button
                shape="square"
                variant="text"
                size="small"
                :aria-label="$t('tenantInvitation.copyLink')"
                @click="emit('copy-link', row.invite_url)"
              >
                <template #icon><t-icon name="copy" /></template>
              </t-button>
            </t-tooltip>
            <t-popconfirm
              v-if="row.status === 'pending'"
              theme="warning"
              :content="
                row.is_share_link
                  ? $t('tenantInvitation.shareLink.revokeConfirm')
                  : $t('tenantInvitation.revoke.confirmBody', {
                      email: row.invitee_email || row.invitee_user_id,
                    })
              "
              :confirm-btn="{ content: $t('tenantInvitation.revoke.confirm'), theme: 'danger' }"
              :cancel-btn="$t('common.cancel')"
              placement="left"
              @confirm="emit('revoke', row)"
            >
              <t-tooltip :content="$t('tenantInvitation.revoke.button')" placement="top">
                <t-button
                  theme="danger"
                  shape="square"
                  variant="text"
                  size="small"
                  :aria-label="$t('tenantInvitation.revoke.button')"
                >
                  <template #icon><t-icon name="close" /></template>
                </t-button>
              </t-tooltip>
            </t-popconfirm>
          </template>
        </t-table>
      </div>
      <div v-if="total > 0" class="data-table-shell__pager">
        <t-pagination
          :current="page"
          :page-size="pageSize"
          :total="total"
          size="small"
          :show-jumper="false"
          show-page-number
          :show-page-size="total > 10"
          :page-size-options="INVITATIONS_PAGE_SIZE_OPTIONS"
          @current-change="(val: number) => emit('update:page', val)"
          @page-size-change="(val: number) => emit('update:pageSize', val)"
          @change="emit('page-change')"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TenantInvitation } from '@/api/tenant/invitations'
import {
  INVITATIONS_PAGE_SIZE_OPTIONS,
  INVITATION_TTL_DAYS,
  formatTenantDate,
  roleTagTheme,
} from '../composables/useTenantMembersList'

const props = defineProps<{
  invitations: TenantInvitation[]
  loading: boolean
  error: string
  total: number
  page: number
  pageSize: number
  canManage: boolean
}>()

const emit = defineEmits<{
  retry: []
  'update:page': [value: number]
  'update:pageSize': [value: number]
  'page-change': []
  'copy-link': [inviteUrl: string]
  revoke: [row: TenantInvitation]
}>()

const { t, locale } = useI18n()

const invitationColumns = computed(() => [
  { colKey: 'invitee', title: t('tenantInvitation.columns.invitee'), ellipsis: true, minWidth: 160 },
  { colKey: 'role', title: t('tenantInvitation.columns.role'), width: 110 },
  { colKey: 'inviter', title: t('tenantInvitation.columns.inviter'), ellipsis: true, minWidth: 140 },
  { colKey: 'expires_at', title: t('tenantInvitation.columns.expiresAt'), width: 160 },
  { colKey: 'status', title: t('tenantInvitation.columns.status'), width: 100 },
  ...(props.canManage
    ? [{ colKey: 'actions', title: t('tenantInvitation.columns.operations'), width: 120, align: 'left' as const }]
    : []),
])

function invitationStatusTheme(
  s: TenantInvitation['status'],
): 'primary' | 'success' | 'warning' | 'danger' | 'default' {
  switch (s) {
    case 'pending':
      return 'primary'
    case 'accepted':
      return 'success'
    case 'declined':
    case 'revoked':
      return 'warning'
    case 'expired':
      return 'danger'
    default:
      return 'default'
  }
}

function inviteePrimary(row: TenantInvitation): string {
  return row.invitee_name?.trim() || row.invitee_email?.trim() || row.invitee_user_id
}

function inviterPrimary(row: TenantInvitation): string {
  return row.inviter_name?.trim() || row.inviter_email?.trim() || row.invited_by || '—'
}
</script>

<style lang="less" scoped>
@import '../tenant-members.less';

.share-link-title {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--gy-brand);
  font-weight: 500;
}

.pending-invitations-section {
  display: flex;
  flex-direction: column;
  gap: 12px;

  .pending-invitations-desc {
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
  }

  .pending-invitations-empty {
    padding: 40px 16px;
    border: 1px solid var(--gy-border-subtle);
    border-radius: var(--gy-radius-lg);
    text-align: center;
    color: var(--gy-text-placeholder);
    font-size: var(--gy-text-md);
  }
}

.pending-invitations-table.data-table-shell.data-table-shell--with-footer > .data-table-shell__pager {
  background-color: transparent;
}
</style>
