<template>
  <SettingsModalShell
    :visible="visible"
    :title="modalTitle"
    v-model="currentSection"
    :nav-groups="navGroups"
    :overlay-class="isCreateMode ? 'org-settings-shell organization-create-overlay' : 'org-settings-shell'"
    :z-index="2000"
    @close="modalShell.requestClose"
  >
    <template #nav-icon="{ item, active }">
      <img
        v-if="item.key === 'sharedAgents'"
        :src="active ? agentIconActiveSrc : agentIconSrc"
        class="nav-icon nav-icon-img"
        alt=""
        aria-hidden="true"
      />
      <t-icon v-else :name="item.icon" class="nav-icon" />
    </template>

    <div ref="contentWrapperRef" class="content-wrapper" :class="{ 'is-create': isCreateMode }">
      <div v-if="showTenantRoleHint" class="tenant-role-hint">
        <t-icon name="info-circle" size="16px" />
        <span>{{ $t('organization.rbac.needTenantAdminTip') }}</span>
      </div>

      <OrgGeneralInviteSection
        :current-section="currentSection"
        :is-create-mode="isCreateMode"
        :is-admin="isAdmin"
        :org-id="orgId"
        :member-count="orgInfo?.member_count ?? 0"
        :form-data="formData"
        v-model:avatar-popover-visible="avatarPopoverVisible"
        :invite-code="inviteCode"
        :invite-link="inviteLink"
        :remaining-validity-text="remainingValidityText"
        :refreshing-code="refreshingCode"
        :invite-validity-options="inviteValidityOptions"
        @update-form="(patch) => Object.assign(formData, patch)"
        @select-avatar-emoji="selectAvatarEmoji"
        @clear-avatar-emoji="clearAvatarEmoji"
        @copy-invite-code="copyInviteCode"
        @refresh-invite-code="refreshInviteCode"
        @copy-invite-link="copyInviteLink"
        @validity-change="handleValidityChange"
        @approval-toggle="handleApprovalToggle"
        @searchable-toggle="handleSearchableToggle"
      />

      <OrgMembersSection
        :current-section="currentSection"
        :is-admin="isAdmin"
        :my-role="orgInfo?.my_role"
        :current-user-id="authStore.currentUserId"
        :total-members-count="members.length"
        :filtered-members="filteredMembers"
        :members-loading="membersLoading"
        v-model:member-search-query="memberSearchQuery"
        :can-request-upgrade="canRequestUpgrade"
        :has-pending-upgrade="hasPendingUpgrade"
        v-model:upgrade-popup-visible="upgradePopupVisible"
        :upgrade-submitting="upgradeSubmitting"
        :upgrade-form="upgradeForm"
        :upgrade-role-options="upgradeRoleOptions"
        v-model:add-member-popup-visible="addMemberPopupVisible"
        :add-member-submitting="addMemberSubmitting"
        :tenant-search-loading="tenantSearchLoading"
        v-model:selected-tenant-id="selectedTenantId"
        :tenant-search-options="tenantSearchOptions"
        v-model:add-member-role="addMemberRole"
        :add-member-role-options="addMemberRoleOptions"
        :role-options="roleOptions"
        :is-owner-member="isOwnerMember"
        @update-upgrade-form="(patch) => Object.assign(upgradeForm, patch)"
        @submit-upgrade="handleSubmitUpgrade"
        @tenant-search="handleTenantSearch"
        @tenant-input-change="handleTenantInputChange"
        @add-member="handleAddMember"
        @role-change="handleRoleChange"
        @remove-member="confirmRemoveMember"
      />

      <OrgJoinRequestsSection
        :current-section="currentSection"
        :total-join-requests-count="joinRequests.length"
        :filtered-join-requests="filteredJoinRequests"
        :join-requests-loading="joinRequestsLoading"
        v-model:join-request-search-query="joinRequestSearchQuery"
        :reviewing-request-id="reviewingRequestId"
        :approve-popup-request-id="approvePopupRequestId"
        v-model:approve-assign-role="approveAssignRole"
        @open-approve-popup="openApprovePopup"
        @close-approve-popup="closeApprovePopup"
        @approve-popup-visible-change="handleApprovePopupVisibleChange"
        @confirm-approve-request="confirmApproveRequest"
        @reject-request="handleRejectRequest"
      />

      <OrgSharedResourcesSection
        :current-section="currentSection"
        :is-admin="isAdmin"
        :shared-knowledge-bases="sharedKnowledgeBases"
        :shared-agents="sharedAgents"
        :shares-loading="sharesLoading"
        @share-click="handleShareClick"
        @remove-share="handleRemoveShare"
        @remove-agent-share="handleRemoveAgentShare"
      />
    </div>

    <template #footer>
      <t-button variant="outline" @click="modalShell.requestClose">{{ $t('common.cancel') }}</t-button>
      <t-button v-if="isAdmin" theme="primary" :loading="submitting" @click="handleSave">
        {{ isCreateMode ? $t('common.create') : $t('common.save') }}
      </t-button>
    </template>
  </SettingsModalShell>
</template>

<script setup lang="ts">
import { ref, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useModalShell } from '@/composables/useModalShell'
import SettingsModalShell from '@/components/SettingsModalShell.vue'
import { useI18n } from 'vue-i18n'
import { copyWithToast } from '@/utils/clipboard'
import {
  getOrganization,
  listOrgShares,
  listOrgAgentShares,
  listJoinRequests,
  searchTenantsForInvite,
} from '@/api/organization'
import { useOrganizationStore } from '@/stores/organization'
import { useAuthStore } from '@/stores/auth'
import agentIconSrc from '@/assets/img/agent.svg'
import agentIconActiveSrc from '@/assets/img/agent-green.svg'
import OrgGeneralInviteSection from './components/OrgGeneralInviteSection.vue'
import OrgMembersSection from './components/OrgMembersSection.vue'
import OrgJoinRequestsSection from './components/OrgJoinRequestsSection.vue'
import OrgSharedResourcesSection from './components/OrgSharedResourcesSection.vue'
import { useOrganizationSettings } from './composables/useOrganizationSettings'

interface Props {
  visible: boolean
  orgId?: string
  mode?: 'view' | 'edit' | 'create'
}

const props = withDefaults(defineProps<Props>(), {
  mode: 'view',
})

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'saved'): void
}>()

const router = useRouter()
const authStore = useAuthStore()
const { t } = useI18n()
const orgStore = useOrganizationStore()
const contentWrapperRef = ref<HTMLElement | null>(null)

const handleClose = () => {
  if (typeof document !== 'undefined' && typeof HTMLElement !== 'undefined' && document.activeElement instanceof HTMLElement) {
    document.activeElement.blur()
  }
  emit('update:visible', false)
}

const scrollContentToTop = async () => {
  await nextTick()
  contentWrapperRef.value?.scrollTo({ top: 0, behavior: 'auto' })
}

let previousBodyOverflow = ''
let bodyScrollLocked = false

const lockBackgroundScroll = () => {
  if (bodyScrollLocked || typeof document === 'undefined' || !document.body) return
  previousBodyOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  bodyScrollLocked = true
}

const unlockBackgroundScroll = () => {
  if (!bodyScrollLocked || typeof document === 'undefined' || !document.body) return
  document.body.style.overflow = previousBodyOverflow
  bodyScrollLocked = false
}

let markModalCleanRef: () => void = () => {}

const {
  currentSection,
  orgInfo,
  members,
  sharedKnowledgeBases,
  sharedAgents,
  joinRequests,
  joinRequestsLoading,
  joinRequestSearchQuery,
  reviewingRequestId,
  sharesLoading,
  membersLoading,
  memberSearchQuery,
  submitting,
  refreshingCode,
  inviteCode,
  upgradePopupVisible,
  upgradeSubmitting,
  hasPendingUpgrade,
  upgradeForm,
  addMemberPopupVisible,
  addMemberSubmitting,
  tenantSearchLoading,
  tenantSearchResults,
  selectedTenantId,
  addMemberRole,
  formData,
  avatarPopoverVisible,
  selectAvatarEmoji,
  clearAvatarEmoji,
  isCreateMode,
  isAdmin,
  showTenantRoleHint,
  canRequestUpgrade,
  upgradeRoleOptions,
  addMemberRoleOptions,
  roleOptions,
  tenantSearchOptions,
  modalTitle,
  navGroups,
  filteredMembers,
  filteredJoinRequests,
  inviteLink,
  inviteValidityOptions,
  remainingValidityText,
  isOwnerMember,
  approvePopupRequestId,
  approveAssignRole,
  openApprovePopup,
  closeApprovePopup,
  handleApprovePopupVisibleChange,
  fetchOrgDetail,
  fetchMembers,
  fetchSharedKBs,
  fetchJoinRequests,
  confirmApproveRequest,
  handleRejectRequest,
  handleSave,
  handleRoleChange,
  confirmRemoveMember,
  handleSubmitUpgrade,
  handleTenantInputChange,
  handleTenantSearch,
  handleAddMember,
  copyInviteCode,
  copyInviteLink,
  refreshInviteCode,
  handleValidityChange,
  handleApprovalToggle,
  handleSearchableToggle,
  handleShareClick,
  handleRemoveShare,
  handleRemoveAgentShare,
} = useOrganizationSettings({
  props,
  emitSaved: () => emit('saved'),
  closeModal: handleClose,
  markModalClean: () => markModalCleanRef(),
  scrollContentToTop,
  lockBackgroundScroll,
  unlockBackgroundScroll,
  getWindowOrigin: () => (typeof window !== 'undefined' && window.location ? window.location.origin : ''),
  navigateTo: (path: string) => {
    void router.push(path)
  },
  hasTenantAdminRole: () => Boolean(authStore.hasRole?.('admin') || authStore.canAccessAllTenants),
  t: (key, params) => (params ? t(key, params) : t(key)),
  message: MessagePlugin,
  copyWithToast,
  orgStore,
  api: {
    getOrganization,
    listOrgShares,
    listOrgAgentShares,
    listJoinRequests,
    searchTenantsForInvite,
  },
})

const modalShell = useModalShell({
  visible: () => props.visible,
  close: handleClose,
  snapshot: () => formData.value,
})
markModalCleanRef = () => modalShell.markClean()

void fetchOrgDetail
void fetchMembers
void fetchSharedKBs
void fetchJoinRequests
void tenantSearchResults
</script>

<style lang="less" src="./organization-settings-modal.less"></style>
