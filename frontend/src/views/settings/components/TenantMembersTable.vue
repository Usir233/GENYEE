<template>
  <div class="members-list-wrap">
    <div v-if="loading && members.length === 0" class="loading-inline">
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
    <div v-else-if="membersTotal === 0" class="empty-state">
      <t-empty
        :description="
          searchQuery.trim()
            ? $t('tenantMember.emptySearch', { q: searchQuery })
            : $t('tenantMember.empty')
        "
      />
    </div>
    <div v-else class="data-table-shell data-table-shell--with-footer">
      <div class="data-table-shell__scroll">
        <t-table row-key="user_id" :data="members" :columns="columns" size="medium" hover :loading="loading">
          <template #member="{ row }">
            <div class="member-cell">
              <span class="member-name">{{ memberPrimary(row) }}</span>
              <span v-if="memberSecondary(row)" class="member-email">{{ memberSecondary(row) }}</span>
            </div>
          </template>
          <template #role="{ row }">
            <div class="role-cell">
              <t-select
                v-if="canManage"
                :model-value="row.role"
                class="member-role-select"
                :borderless="true"
                :popup-props="roleSelectPopupProps"
                :aria-label="`${memberPrimary(row)} · ${$t('tenantMember.columns.role')}`"
                @change="(val: string | number) => emit('role-change', row, String(val))"
              >
                <template #prefixIcon><t-icon :name="roleIcon(row.role)" size="14px" /></template>
                <t-option v-for="opt in roleOptions" :key="opt.value" :value="opt.value" :label="opt.label">
                  <span class="role-option">
                    <t-icon :name="roleIcon(opt.value)" class="role-option-icon" />
                    <span>{{ opt.label }}</span>
                  </span>
                </t-option>
              </t-select>
              <span v-else class="member-role-label">
                <t-icon :name="roleIcon(row.role)" size="14px" />
                {{ $t('tenantMember.role.' + row.role) }}
              </span>
            </div>
          </template>
          <template #joined_at="{ row }">{{ formatTenantDate(row.joined_at, locale) }}</template>
          <template #actions="{ row }">
            <t-popconfirm
              v-if="canManage && row.user_id !== currentUserId"
              :content="$t('tenantMember.remove.confirmBody', { name: row.username || row.email })"
              :confirm-btn="{ content: $t('tenantMember.remove.confirm'), theme: 'danger' }"
              :cancel-btn="{ content: $t('common.cancel') }"
              placement="left"
              @confirm="emit('remove', row)"
            >
              <t-tooltip :content="$t('tenantMember.remove.button')" placement="top">
                <t-button
                  theme="danger"
                  shape="square"
                  variant="text"
                  size="small"
                  :aria-label="$t('tenantMember.remove.button')"
                  @click.stop
                >
                  <template #icon><t-icon name="user-clear" /></template>
                </t-button>
              </t-tooltip>
            </t-popconfirm>
          </template>
        </t-table>
      </div>
      <div v-if="membersTotal > 0" class="data-table-shell__pager">
        <t-pagination
          :current="page"
          :page-size="pageSize"
          :total="membersTotal"
          size="small"
          :show-jumper="false"
          show-page-number
          :show-page-size="membersTotal > 10"
          :page-size-options="MEMBERS_PAGE_SIZE_OPTIONS"
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
import type { TenantMember, TenantRole } from '@/api/tenant/members'
import {
  MEMBERS_PAGE_SIZE_OPTIONS,
  formatTenantDate,
  memberPrimary,
  memberSecondary,
  roleIcon,
  roleSelectPopupProps,
} from '../composables/useTenantMembersList'

defineProps<{
  members: TenantMember[]
  loading: boolean
  error: string
  membersTotal: number
  page: number
  pageSize: number
  searchQuery: string
  canManage: boolean
  currentUserId: string
  roleOptions: Array<{ label: string; value: TenantRole }>
}>()

const emit = defineEmits<{
  retry: []
  'update:page': [value: number]
  'update:pageSize': [value: number]
  'page-change': []
  'role-change': [row: TenantMember, newRole: string]
  remove: [row: TenantMember]
}>()

const { t, locale } = useI18n()

const columns = computed(() => [
  { colKey: 'member', title: t('tenantMember.columns.member'), ellipsis: true, minWidth: 230 },
  { colKey: 'role', title: t('tenantMember.columns.role'), width: 168 },
  { colKey: 'joined_at', title: t('tenantMember.columns.joinedAt'), width: 154 },
  { colKey: 'actions', title: t('tenantMember.columns.operations'), width: 88, align: 'left' as const },
])
</script>

<style lang="less" scoped>
@import '../tenant-members.less';

.members-list-wrap {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.member-role-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  box-sizing: border-box;
  width: 128px;
  height: 28px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-radius: var(--gy-radius-sm);
  background: var(--gy-surface-subtle);
  color: var(--gy-text-primary);
  font-size: var(--gy-text-md);
  line-height: 26px;

  .t-icon {
    flex-shrink: 0;
    color: var(--gy-text-secondary);
  }
}

.member-role-select {
  width: 128px;
  flex: 0 0 128px;

  :deep(.t-input) {
    height: 28px;
    min-height: 28px;
    padding: 0 8px;
    border: 1px solid transparent;
    border-radius: var(--gy-radius-sm);
    background: var(--gy-surface-subtle);
    font-size: var(--gy-text-md);

    &:hover {
      border-color: var(--gy-border-subtle);
    }
    &.t-is-focused {
      border-color: var(--gy-brand);
    }
  }
  :deep(.t-input__prefix) {
    margin-right: 6px;
    color: var(--gy-text-secondary);
  }
}
</style>
