<template>
  <div v-show="currentSection === 'members'" class="section">
    <div class="section-header">
      <div class="section-header-row">
        <div class="section-header-titlewrap">
          <h2>{{ $t('organization.manageMembers') }}</h2>
          <t-popup
            placement="bottom-start"
            trigger="hover"
            overlay-class-name="gy-popover org-permissions-popup-overlay"
            :overlay-inner-style="permissionsPopupInnerStyle"
          >
            <button
              type="button"
              class="permissions-trigger-btn"
              :aria-label="$t('organization.editor.permissionsTitle')"
              :title="$t('organization.settings.permissionsIconHint')"
            >
              <t-icon name="info-circle" size="16px" />
            </button>
            <template #content>
              <div class="permissions-compact permissions-compact--popover">
                <div class="permissions-compact-header">
                  <span class="permissions-compact-title">{{ $t('organization.editor.permissionsTitle') }}</span>
                  <span class="permissions-compact-desc">{{ $t('organization.editor.permissionsDesc') }}</span>
                </div>
                <div class="permissions-compact-grid">
                  <div
                    v-for="role in ORG_ROLE_MATRIX_ORDER"
                    :key="role"
                    :class="['perm-role-block', role, { 'is-me': myRole === role }]"
                  >
                    <div class="perm-role-tag">
                      <t-icon :name="orgRoleIcon(role)" size="12px" />
                      <span>{{ $t(`organization.role.${role}`) }}</span>
                      <span v-if="myRole === role" class="me-badge">{{ $t('common.me') }}</span>
                    </div>
                    <div class="perm-items">
                      <span
                        v-for="(perm, idx) in ORG_ROLE_MATRIX[role]"
                        :key="idx"
                        :class="['perm-item', perm.has ? 'has' : 'no']"
                      >
                        <t-icon :name="perm.has ? 'check' : 'close'" size="12px" />
                        {{ $t(`organization.editor.${perm.key}`) }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
      </div>
      <p class="section-description">{{ $t('organization.settings.membersDesc') }}</p>
    </div>

    <div class="members-list-wrap">
      <div class="members-list-header">
        <div class="members-list-titlewrap">
          <span class="members-list-title">{{ $t('organization.members.listTitle') }}</span>
          <span class="members-list-count-badge">{{ filteredMembers.length }}</span>
        </div>
        <div class="members-list-actions">
          <div class="members-list-search">
            <t-input
              :model-value="memberSearchQuery"
              size="small"
              :placeholder="$t('organization.members.searchPlaceholder')"
              clearable
              @update:model-value="(val: unknown) => emit('update:memberSearchQuery', String(val ?? ''))"
            >
              <template #prefix-icon>
                <t-icon name="search" />
              </template>
            </t-input>
          </div>
          <t-popup
            v-if="canRequestUpgrade"
            :visible="upgradePopupVisible"
            trigger="click"
            placement="bottom-end"
            destroy-on-close
            overlay-class-name="org-upgrade-popup-overlay"
            @visible-change="emit('update:upgradePopupVisible', $event)"
          >
            <t-button
              variant="outline"
              shape="square"
              size="small"
              class="members-list-upgrade-btn"
              :disabled="hasPendingUpgrade"
              :title="hasPendingUpgrade ? $t('organization.upgrade.pending') : $t('organization.upgrade.requestUpgrade')"
              :aria-label="hasPendingUpgrade ? $t('organization.upgrade.pending') : $t('organization.upgrade.requestUpgrade')"
            >
              <template #icon><t-icon name="arrow-up" /></template>
            </t-button>
            <template #content>
              <div class="org-upgrade-popup-inner" @click.stop>
                <div class="member-invite-popup-title">{{ $t('organization.upgrade.dialogTitle') }}</div>
                <p class="add-member-tip">{{ $t('organization.upgrade.dialogDesc') }}</p>

                <div class="upgrade-current-role-bar">
                  <span class="upgrade-current-role-label">{{ $t('organization.upgrade.currentRole') }}</span>
                  <t-tag size="small" :theme="getRoleTheme(myRole || 'viewer')" variant="light">
                    {{ $t(`organization.role.${myRole || 'viewer'}`) }}
                  </t-tag>
                </div>

                <div class="org-upgrade-fields">
                  <div class="org-upgrade-field">
                    <label class="org-upgrade-field-label">{{ $t('organization.upgrade.selectRole') }}</label>
                    <div class="upgrade-role-pills">
                      <button
                        v-for="opt in upgradeRoleOptions"
                        :key="opt.value"
                        type="button"
                        :class="['upgrade-role-pill', { active: upgradeForm.requested_role === opt.value }]"
                        @click="emit('updateUpgradeForm', { requested_role: opt.value })"
                      >
                        {{ opt.label }}
                      </button>
                    </div>
                  </div>
                  <div class="org-upgrade-field org-upgrade-field--last">
                    <label class="org-upgrade-field-label">{{ $t('organization.upgrade.reason') }}</label>
                    <t-textarea
                      :model-value="upgradeForm.message"
                      size="medium"
                      :placeholder="$t('organization.upgrade.reasonPlaceholder')"
                      :autosize="{ minRows: 2, maxRows: 4 }"
                      :maxlength="500"
                      @update:model-value="(val: unknown) => emit('updateUpgradeForm', { message: String(val ?? '') })"
                    />
                  </div>
                </div>

                <div class="invite-popup-footer">
                  <t-button
                    variant="outline"
                    :disabled="upgradeSubmitting"
                    @click="emit('update:upgradePopupVisible', false)"
                  >
                    {{ $t('common.cancel') }}
                  </t-button>
                  <t-button theme="primary" :loading="upgradeSubmitting" @click="emit('submitUpgrade')">
                    {{ $t('organization.upgrade.submitBtn') }}
                  </t-button>
                </div>
              </div>
            </template>
          </t-popup>
          <t-popup
            v-if="isAdmin"
            :visible="addMemberPopupVisible"
            trigger="click"
            placement="bottom-end"
            destroy-on-close
            overlay-class-name="org-add-member-popup-overlay"
            @visible-change="emit('update:addMemberPopupVisible', $event)"
          >
            <t-button
              theme="primary"
              variant="outline"
              shape="square"
              size="small"
              class="members-list-add-btn"
              :title="$t('organization.addMember.button')"
              :aria-label="$t('organization.addMember.button')"
            >
              <template #icon><t-icon name="user-add" /></template>
            </t-button>
            <template #content>
              <div class="member-invite-popup-inner" @click.stop>
                <div class="member-invite-popup-title">{{ $t('organization.addMember.dialogTitle') }}</div>
                <p class="add-member-tip">{{ $t('organization.addMember.tipTenant') }}</p>
                <t-form layout="vertical" class="member-invite-form">
                  <t-form-item :label="$t('organization.addMember.searchTenant')">
                    <div class="member-form-control">
                      <t-select
                        :model-value="selectedTenantId ?? undefined"
                        :placeholder="$t('organization.addMember.searchTenantPlaceholder')"
                        filterable
                        :filter="() => true"
                        :loading="tenantSearchLoading"
                        clearable
                        :options="tenantSearchOptions"
                        @update:model-value="onTenantSelectChange"
                        @search="(val: string) => emit('tenantSearch', val)"
                        @input-change="(val: string) => emit('tenantInputChange', val)"
                        @clear="emit('tenantInputChange', '')"
                      />
                      <p class="field-hint">{{ $t('organization.addMember.searchTenantHint') }}</p>
                    </div>
                  </t-form-item>
                  <t-form-item :label="$t('organization.addMember.selectRole')">
                    <t-select
                      :model-value="addMemberRole"
                      :options="addMemberRoleOptions"
                      :placeholder="$t('organization.addMember.selectRole')"
                      @update:model-value="onAddMemberRoleChange"
                    />
                  </t-form-item>
                </t-form>
                <div class="invite-popup-footer">
                  <t-button
                    variant="outline"
                    :disabled="addMemberSubmitting"
                    @click="emit('update:addMemberPopupVisible', false)"
                  >
                    {{ $t('common.cancel') }}
                  </t-button>
                  <t-button
                    theme="primary"
                    :loading="addMemberSubmitting"
                    :disabled="selectedTenantId == null"
                    @click="emit('addMember')"
                  >
                    {{ $t('organization.addMember.confirmBtn') }}
                  </t-button>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
      </div>

      <div v-if="membersLoading && totalMembersCount === 0" class="loading-inline">
        <t-loading size="small" />
        <span>{{ $t('organization.members.loading') }}</span>
      </div>
      <div v-else-if="filteredMembers.length === 0" class="empty-state">
        <t-empty
          :description="
            memberSearchQuery.trim()
              ? $t('organization.members.emptySearch', { q: memberSearchQuery })
              : $t('organization.noMembers')
          "
        />
      </div>
      <div v-else class="data-table-shell members-table-shell">
        <t-table
          row-key="id"
          :data="filteredMembers"
          :columns="memberColumns"
          size="medium"
          hover
          stripe
          :loading="membersLoading"
        >
          <template #member="{ row }">
            <div class="member-cell">
              <span class="member-name">
                {{ memberPrimaryLabel(row) }}
                <span v-if="isOwnerMember(row)" class="owner-tag">{{ $t('organization.owner') }}</span>
                <span v-if="row.user_id === currentUserId" class="me-tag">{{ $t('common.me') }}</span>
              </span>
              <span v-if="memberSecondaryLabel(row)" class="member-email">
                {{ memberSecondaryLabel(row) }}
              </span>
            </div>
          </template>
          <template #role="{ row }">
            <div class="role-cell">
              <t-select
                v-if="isAdmin && !isOwnerMember(row)"
                :model-value="row.role"
                class="member-role-select"
                size="small"
                :options="roleOptions"
                @change="(val: unknown) => emit('roleChange', row, String(val))"
              />
              <t-tag v-else size="small" :theme="getRoleTheme(row.role)">
                {{ $t(`organization.role.${row.role}`) }}
              </t-tag>
            </div>
          </template>
          <template #joined_at="{ row }">{{ formatOrgDate(row.joined_at) }}</template>
          <template #actions="{ row }">
            <t-popconfirm
              v-if="isAdmin && !isOwnerMember(row)"
              :content="$t('organization.detail.removeMemberConfirm', { name: memberPrimaryLabel(row) })"
              :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
              :cancel-btn="{ content: $t('common.cancel') }"
              placement="left"
              @confirm="emit('removeMember', row)"
            >
              <t-tooltip :content="$t('organization.detail.removeMember')" placement="top">
                <t-button
                  theme="danger"
                  shape="square"
                  variant="text"
                  size="small"
                  :aria-label="$t('organization.detail.removeMember')"
                  @click.stop
                >
                  <template #icon><t-icon name="user-clear" /></template>
                </t-button>
              </t-tooltip>
            </t-popconfirm>
          </template>
        </t-table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrganizationMember } from '@/api/organization'
import {
  ORG_ROLE_MATRIX,
  ORG_ROLE_MATRIX_ORDER,
  orgRoleIcon,
  getRoleTheme,
  formatOrgDate,
  type OrgRole,
} from '../composables/useOrganizationSettings'

const props = defineProps<{
  currentSection: string
  isAdmin: boolean
  myRole?: string
  currentUserId?: string
  totalMembersCount: number
  filteredMembers: OrganizationMember[]
  membersLoading: boolean
  memberSearchQuery: string
  canRequestUpgrade: boolean
  hasPendingUpgrade: boolean
  upgradePopupVisible: boolean
  upgradeSubmitting: boolean
  upgradeForm: { requested_role: OrgRole; message: string }
  upgradeRoleOptions: Array<{ label: string; value: 'editor' | 'admin' }>
  addMemberPopupVisible: boolean
  addMemberSubmitting: boolean
  tenantSearchLoading: boolean
  selectedTenantId: number | null
  tenantSearchOptions: Array<{ label: string; value: number }>
  addMemberRole: OrgRole
  addMemberRoleOptions: Array<{ label: string; value: string }>
  roleOptions: Array<{ label: string; value: string }>
  isOwnerMember: (member: OrganizationMember) => boolean
}>()

const emit = defineEmits<{
  (e: 'update:memberSearchQuery', value: string): void
  (e: 'update:upgradePopupVisible', value: boolean): void
  (e: 'updateUpgradeForm', patch: Partial<{ requested_role: OrgRole; message: string }>): void
  (e: 'submitUpgrade'): void
  (e: 'update:addMemberPopupVisible', value: boolean): void
  (e: 'update:selectedTenantId', value: number | null): void
  (e: 'update:addMemberRole', value: OrgRole): void
  (e: 'tenantSearch', query: string): void
  (e: 'tenantInputChange', query: string): void
  (e: 'addMember'): void
  (e: 'roleChange', member: OrganizationMember, newRole: string): void
  (e: 'removeMember', member: OrganizationMember): void
}>()

const { t } = useI18n()

const permissionsPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(520px, calc(100vw - 24px))',
  maxWidth: 'min(520px, calc(100vw - 24px))',
  maxHeight: 'min(400px, 65vh)',
  overflow: 'hidden',
}

const memberColumns = computed(() => {
  const cols: Array<{
    colKey: string
    title: string
    ellipsis?: boolean
    minWidth?: number
    width?: number
    align?: 'left' | 'center' | 'right'
  }> = [
    { colKey: 'member', title: t('organization.members.columns.member'), ellipsis: true, minWidth: 160 },
    { colKey: 'role', title: t('organization.members.columns.role'), width: 132 },
    { colKey: 'joined_at', title: t('organization.members.columns.joinedAt'), width: 154 },
  ]
  if (props.isAdmin) {
    cols.push({ colKey: 'actions', title: t('organization.members.columns.operations'), width: 88, align: 'left' })
  }
  return cols
})

function memberPrimaryLabel(m: OrganizationMember): string {
  return m.tenant_name || m.username || `tenant#${m.tenant_id}`
}

function memberSecondaryLabel(m: OrganizationMember): string {
  if (m.tenant_name && m.username) {
    return m.username
  }
  return ''
}

function onTenantSelectChange(val: unknown) {
  emit('update:selectedTenantId', typeof val === 'number' ? val : null)
}

function onAddMemberRoleChange(val: unknown) {
  if (val === 'admin' || val === 'editor' || val === 'viewer') {
    emit('update:addMemberRole', val)
  }
}
</script>
