<template>
  <div v-show="currentSection === 'joinRequests'" class="section">
    <div class="section-header">
      <h2>{{ $t('organization.settings.joinRequests') }}</h2>
      <p class="section-description">{{ $t('organization.settings.joinRequestsDesc') }}</p>
    </div>

    <div class="members-list-wrap join-requests-wrap">
      <div class="members-list-header">
        <div class="members-list-titlewrap">
          <span class="members-list-title">{{ $t('organization.joinRequests.listTitle') }}</span>
          <span class="members-list-count-badge">{{ filteredJoinRequests.length }}</span>
        </div>
        <div class="members-list-actions">
          <div class="members-list-search">
            <t-input
              :model-value="joinRequestSearchQuery"
              size="small"
              :placeholder="$t('organization.joinRequests.searchPlaceholder')"
              clearable
              @update:model-value="(val: unknown) => emit('update:joinRequestSearchQuery', String(val ?? ''))"
            >
              <template #prefix-icon>
                <t-icon name="search" />
              </template>
            </t-input>
          </div>
        </div>
      </div>

      <div v-if="joinRequestsLoading && totalJoinRequestsCount === 0" class="loading-inline">
        <t-loading size="small" />
        <span>{{ $t('organization.joinRequests.loading') }}</span>
      </div>
      <div v-else-if="filteredJoinRequests.length === 0" class="empty-state">
        <t-empty
          :description="
            joinRequestSearchQuery.trim()
              ? $t('organization.joinRequests.emptySearch', { q: joinRequestSearchQuery })
              : $t('organization.settings.noPendingRequests')
          "
        />
      </div>
      <div v-else class="data-table-shell join-requests-table">
        <t-table
          row-key="id"
          :data="filteredJoinRequests"
          :columns="joinRequestColumns"
          size="medium"
          hover
          stripe
          :loading="joinRequestsLoading"
        >
          <template #applicant="{ row }">
            <div class="member-cell">
              <span class="member-name">{{ joinRequestApplicantLabel(row) }}</span>
              <span v-if="joinRequestApplicantSecondary(row)" class="member-email">
                {{ joinRequestApplicantSecondary(row) }}
              </span>
            </div>
          </template>
          <template #request_type="{ row }">
            <t-tag
              size="small"
              :theme="row.request_type === 'upgrade' ? 'warning' : 'primary'"
              variant="light"
            >
              {{
                row.request_type === 'upgrade'
                  ? $t('organization.joinRequests.typeUpgrade')
                  : $t('organization.joinRequests.typeJoin')
              }}
            </t-tag>
          </template>
          <template #requested_role="{ row }">
            <span v-if="row.request_type === 'upgrade' && row.prev_role" class="join-request-role-change">
              {{ roleLabel(row.prev_role) }}
              <t-icon name="arrow-right" size="12px" />
              {{ roleLabel(row.requested_role) }}
            </span>
            <t-tag v-else size="small" :theme="getRoleTheme(row.requested_role)" variant="light">
              {{ roleLabel(row.requested_role) }}
            </t-tag>
          </template>
          <template #message="{ row }">
            <span class="join-request-message" :title="row.message || undefined">
              {{ row.message || '—' }}
            </span>
          </template>
          <template #created_at="{ row }">{{ formatOrgDate(row.created_at) }}</template>
          <template #actions="{ row }">
            <div class="join-request-actions">
              <t-popup
                :visible="approvePopupRequestId === row.id"
                placement="left-start"
                destroy-on-close
                overlay-class-name="org-approve-request-popup-overlay"
                @visible-change="(visible: boolean) => emit('approvePopupVisibleChange', visible, row)"
              >
                <t-tooltip :content="$t('organization.settings.approve')" placement="top">
                  <t-button
                    theme="primary"
                    variant="text"
                    shape="square"
                    size="small"
                    :loading="reviewingRequestId === row.id"
                    :aria-label="$t('organization.settings.approve')"
                    @click.stop="emit('openApprovePopup', row)"
                  >
                    <template #icon><t-icon name="check" /></template>
                  </t-button>
                </t-tooltip>
                <template #content>
                  <div class="org-approve-request-popup-inner" @click.stop>
                    <div class="member-invite-popup-title">{{ $t('organization.joinRequests.approveTitle') }}</div>
                    <p class="add-member-tip">
                      {{ $t('organization.joinRequests.approveDesc', { name: joinRequestApplicantLabel(row) }) }}
                    </p>
                    <div class="org-upgrade-field org-upgrade-field--last">
                      <label class="org-upgrade-field-label">{{ $t('organization.settings.assignRole') }}</label>
                      <t-select
                        :model-value="approveAssignRole"
                        size="medium"
                        :options="orgRoleOptions"
                        @update:model-value="onAssignRoleChange"
                      />
                    </div>
                    <div class="invite-popup-footer">
                      <t-button
                        variant="outline"
                        :disabled="reviewingRequestId === row.id"
                        @click="emit('closeApprovePopup')"
                      >
                        {{ $t('common.cancel') }}
                      </t-button>
                      <t-button
                        theme="primary"
                        :loading="reviewingRequestId === row.id"
                        @click="emit('confirmApproveRequest', row)"
                      >
                        {{ $t('organization.settings.approve') }}
                      </t-button>
                    </div>
                  </div>
                </template>
              </t-popup>
              <t-popconfirm
                :content="$t('organization.joinRequests.rejectConfirm')"
                :confirm-btn="{ content: $t('organization.settings.reject'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }"
                placement="left"
                @confirm="emit('rejectRequest', row)"
              >
                <t-tooltip :content="$t('organization.settings.reject')" placement="top">
                  <t-button
                    theme="danger"
                    variant="text"
                    shape="square"
                    size="small"
                    :loading="reviewingRequestId === row.id"
                    :aria-label="$t('organization.settings.reject')"
                    @click.stop
                  >
                    <template #icon><t-icon name="close" /></template>
                  </t-button>
                </t-tooltip>
              </t-popconfirm>
            </div>
          </template>
        </t-table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { JoinRequestResponse } from '@/api/organization'
import { getRoleTheme, formatOrgDate, type OrgRole } from '../composables/useOrganizationSettings'

defineProps<{
  currentSection: string
  totalJoinRequestsCount: number
  filteredJoinRequests: JoinRequestResponse[]
  joinRequestsLoading: boolean
  joinRequestSearchQuery: string
  reviewingRequestId: string | null
  approvePopupRequestId: string | null
  approveAssignRole: OrgRole
}>()

const emit = defineEmits<{
  (e: 'update:joinRequestSearchQuery', value: string): void
  (e: 'update:approveAssignRole', value: OrgRole): void
  (e: 'openApprovePopup', req: JoinRequestResponse): void
  (e: 'closeApprovePopup'): void
  (e: 'approvePopupVisibleChange', visible: boolean, req: JoinRequestResponse): void
  (e: 'confirmApproveRequest', req: JoinRequestResponse): void
  (e: 'rejectRequest', req: JoinRequestResponse): void
}>()

const { t } = useI18n()

const orgRoleOptions = computed(() => [
  { label: t('organization.role.viewer'), value: 'viewer' },
  { label: t('organization.role.editor'), value: 'editor' },
  { label: t('organization.role.admin'), value: 'admin' },
])

const joinRequestColumns = computed(() => [
  { colKey: 'applicant', title: t('organization.joinRequests.columns.applicant'), ellipsis: true, minWidth: 160 },
  { colKey: 'request_type', title: t('organization.joinRequests.columns.type'), width: 88 },
  { colKey: 'requested_role', title: t('organization.joinRequests.columns.requestedRole'), width: 140 },
  { colKey: 'message', title: t('organization.joinRequests.columns.message'), ellipsis: true, minWidth: 120 },
  { colKey: 'created_at', title: t('organization.joinRequests.columns.appliedAt'), width: 154 },
  { colKey: 'actions', title: t('organization.members.columns.operations'), width: 88, align: 'left' as const },
])

function joinRequestApplicantLabel(req: JoinRequestResponse): string {
  return req.username || req.email || req.user_id
}

function joinRequestApplicantSecondary(req: JoinRequestResponse): string {
  const primary = joinRequestApplicantLabel(req)
  if (req.email && req.email !== primary) return req.email
  return ''
}

function roleLabel(role: string): string {
  if (role === 'admin') return t('organization.role.admin')
  if (role === 'editor') return t('organization.role.editor')
  return t('organization.role.viewer')
}

function onAssignRoleChange(val: unknown) {
  if (val === 'admin' || val === 'editor' || val === 'viewer') {
    emit('update:approveAssignRole', val)
  }
}
</script>
