<template>
  <div class="tenant-members">
    <div class="section-header">
      <div class="section-header-row">
        <div class="section-header-titlewrap">
          <h2>{{ $t('tenantMember.title') }}</h2>
          <t-popup
            placement="bottom-left"
            trigger="hover"
            show-arrow
            overlay-class-name="gy-popover permissions-popup-overlay"
            :overlay-inner-style="permissionsPopupInnerStyle"
          >
            <button type="button" class="permissions-trigger-btn" :aria-label="$t('tenantMember.permissions.title')">
              <t-icon name="help-circle" size="16px" />
            </button>
            <template #content>
              <div class="permissions-compact permissions-compact--popover">
                <div class="permissions-compact-header">
                  <span class="permissions-compact-title">{{ $t('tenantMember.permissions.title') }}</span>
                  <span class="permissions-compact-desc">{{ $t('tenantMember.permissions.desc') }}</span>
                </div>
                <div class="permissions-compact-grid">
                  <div
                    v-for="r in roleMatrixOrder"
                    :key="r"
                    :class="['perm-role-block', r, { 'is-me': currentRole === r }]"
                  >
                    <div class="perm-role-tag">
                      <t-icon :name="roleMatrixIcon(r)" size="12px" />
                      <span>{{ $t('tenantMember.role.' + r) }}</span>
                      <span v-if="currentRole === r" class="me-badge">{{ $t('common.me') }}</span>
                    </div>
                    <div class="perm-items">
                      <span
                        v-for="(perm, i) in roleMatrix[r]"
                        :key="i"
                        :class="['perm-item', perm.has ? 'has' : 'no']"
                      >
                        <t-icon :name="perm.has ? 'check' : 'close'" size="12px" />
                        {{ $t('tenantMember.permissions.' + perm.key) }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
        <div class="members-header-actions">
          <t-button v-if="canViewAudit" variant="text" size="small" class="header-audit-btn" @click="openAuditDrawer">
            <template #icon><t-icon name="history" /></template>
            {{ $t('tenantMember.audit.tabLabel') }}
          </t-button>
          <t-popup
            v-if="canManage"
            v-model="invitePopupVisible"
            trigger="click"
            placement="bottom-end"
            destroy-on-close
            overlay-class-name="gy-popover gy-popover--form member-invite-popup-overlay"
          >
            <t-button
              theme="default"
              variant="text"
              size="small"
              class="members-list-add-btn"
              :title="$t('tenantMember.add.button')"
              :aria-label="$t('tenantMember.add.button')"
            >
              <template #icon><t-icon name="user-add" /></template>
              {{ $t('tenantMember.add.button') }}
            </t-button>
            <template #content>
              <div class="member-invite-popup-inner" @click.stop>
                <div class="member-invite-popup-title">
                  {{
                    addDialogStep === 'form'
                      ? $t('tenantMember.add.dialogTitle')
                      : $t('tenantInvitation.confirmInviteTitle')
                  }}
                </div>
                <t-form
                  v-if="addDialogStep === 'form'"
                  ref="addFormRef"
                  :data="addForm"
                  :rules="addFormRules"
                  :label-width="80"
                  class="member-invite-form"
                >
                  <t-form-item :label="$t('tenantMember.add.emailLabel')" name="email">
                    <t-input
                      v-model="addForm.email"
                      :placeholder="$t('tenantMember.add.emailPlaceholder')"
                      clearable
                    />
                  </t-form-item>
                  <t-form-item :label="$t('tenantMember.add.roleLabel')" name="role">
                    <t-select
                      v-model="addForm.role"
                      :options="roleOptions"
                      :popup-props="roleSelectPopupProps"
                    />
                  </t-form-item>
                </t-form>
                <div v-else class="invite-confirm-body">
                  {{
                    $t('tenantInvitation.confirmInviteBody', {
                      email: addConfirmEmail,
                      role: addConfirmRoleLabel,
                    })
                  }}
                </div>
                <div class="invite-popup-footer">
                  <t-button
                    v-if="addDialogStep === 'form'"
                    variant="outline"
                    :disabled="adding"
                    @click="invitePopupVisible = false"
                  >
                    {{ $t('common.cancel') }}
                  </t-button>
                  <t-button v-else variant="outline" :disabled="adding" @click="goBackToForm">
                    {{ $t('common.back') }}
                  </t-button>
                  <t-button theme="primary" :loading="adding" @click="submitAdd">
                    {{ dialogConfirmLabel }}
                  </t-button>
                </div>
              </div>
            </template>
          </t-popup>
          <t-popup
            v-if="canManage"
            v-model="shareLinkPopupVisible"
            trigger="click"
            placement="bottom-end"
            destroy-on-close
            overlay-class-name="gy-popover gy-popover--form member-invite-popup-overlay"
          >
            <t-button
              theme="default"
              variant="text"
              size="small"
              class="members-list-add-btn"
              :title="$t('tenantInvitation.shareLink.button')"
              :aria-label="$t('tenantInvitation.shareLink.button')"
            >
              <template #icon><t-icon name="link" /></template>
              {{ $t('tenantInvitation.shareLink.button') }}
            </t-button>
            <template #content>
              <div class="member-invite-popup-inner" @click.stop>
                <div class="member-invite-popup-title">
                  {{
                    shareLinkResult
                      ? $t('tenantInvitation.shareLink.resultTitle')
                      : $t('tenantInvitation.shareLink.dialogTitle')
                  }}
                </div>
                <div v-if="!shareLinkResult" class="member-invite-form">
                  <p class="invite-confirm-body">
                    {{ $t('tenantInvitation.shareLink.description', { days: INVITATION_TTL_DAYS }) }}
                  </p>
                  <t-form :data="shareLinkForm" :label-width="80">
                    <t-form-item :label="$t('tenantMember.add.roleLabel')" name="role">
                      <t-select
                        v-model="shareLinkForm.role"
                        :options="roleOptions"
                        :popup-props="roleSelectPopupProps"
                      />
                    </t-form-item>
                  </t-form>
                </div>
                <div v-else class="share-link-result">
                  <p class="invite-confirm-body">
                    {{ $t('tenantInvitation.shareLink.resultBody') }}
                  </p>
                  <div class="share-link-row">
                    <input
                      class="share-link-row__input"
                      :value="absoluteInviteURL(shareLinkResult.invite_url || '')"
                      :aria-label="$t('tenantInvitation.copyLink')"
                      readonly
                      @click="($event.target as HTMLInputElement).select()"
                    />
                    <t-button
                      size="small"
                      theme="primary"
                      variant="outline"
                      @click="copyText(absoluteInviteURL(shareLinkResult.invite_url || ''))"
                    >
                      <template #icon><t-icon name="copy" /></template>
                      {{ $t('tenantInvitation.copyLink') }}
                    </t-button>
                  </div>
                </div>
                <div class="invite-popup-footer">
                  <t-button
                    v-if="!shareLinkResult"
                    variant="outline"
                    :disabled="creatingShareLink"
                    @click="shareLinkPopupVisible = false"
                  >
                    {{ $t('common.cancel') }}
                  </t-button>
                  <t-button v-else variant="outline" @click="shareLinkPopupVisible = false">
                    {{ $t('common.close') }}
                  </t-button>
                  <t-button
                    v-if="!shareLinkResult"
                    theme="primary"
                    :loading="creatingShareLink"
                    @click="submitShareLink"
                  >
                    {{ $t('tenantInvitation.shareLink.generate') }}
                  </t-button>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
      </div>
      <p class="section-description">
        {{ $t('tenantMember.sectionDescription') }}
        <a
          v-if="docsUrl('tenantAuth')"
          class="doc-link"
          :href="docsUrl('tenantAuth')"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ $t('tenantMember.learnRbacGuide') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </p>
    </div>

    <div class="members-tabbar">
      <t-tabs v-model="activeListTab" class="members-tabs">
        <t-tab-panel value="members">
          <template #label>
            <span class="members-tab-label">
              <t-icon name="usergroup" size="16px" />
              {{ $t('tenantMember.listTitle') }}
              <span class="members-list-count-badge">{{ membersTotal }}</span>
            </span>
          </template>
        </t-tab-panel>
        <t-tab-panel v-if="canManage" value="invitations">
          <template #label>
            <span class="members-tab-label">
              <t-icon name="mail" size="16px" />
              {{ $t('tenantInvitation.pendingSectionTitle') }}
              <span class="members-list-count-badge">{{ invitationsTotal }}</span>
            </span>
          </template>
        </t-tab-panel>
      </t-tabs>
      <t-input
        v-show="activeListTab === 'members'"
        v-model="searchQuery"
        class="members-list-search"
        :placeholder="$t('tenantMember.searchPlaceholder')"
        :aria-label="$t('tenantMember.searchPlaceholder')"
        clearable
      >
        <template #prefix-icon><t-icon name="search" size="16px" /></template>
      </t-input>
    </div>

    <div class="members-tab-layout">
      <TenantInvitationsPanel
        v-if="canManage"
        v-show="activeListTab === 'invitations'"
        v-model:page="invitationsPage"
        v-model:page-size="invitationsPageSize"
        :invitations="invitations"
        :loading="invitationsLoading"
        :error="invitationsError"
        :total="invitationsTotal"
        :can-manage="canManage"
        @retry="loadInvitations"
        @page-change="onInvitationsPageChange"
        @copy-link="(url) => copyText(absoluteInviteURL(url))"
        @revoke="doRevokeInvitation"
      />

      <TenantMembersTable
        v-show="activeListTab === 'members'"
        v-model:page="membersPage"
        v-model:page-size="membersPageSize"
        :members="members"
        :loading="loading"
        :error="error"
        :members-total="membersTotal"
        :search-query="searchQuery"
        :can-manage="canManage"
        :current-user-id="currentUserId"
        :role-options="roleOptions"
        @retry="loadMembers"
        @page-change="onMembersPageChange"
        @role-change="onRoleChange"
        @remove="removeRow"
      />
    </div>

    <TenantAuditLogDrawer
      v-if="canViewAudit"
      v-model:visible="auditDrawerVisible"
      :entries="auditEntries"
      :loading="auditLoading"
      :error="auditError"
      :has-more="auditHasMore"
      :expanded-row-keys="auditExpandedRowKeys"
      :actor-display-name="actorDisplayName"
      :format-audit-action="formatAuditAction"
      :audit-target-subject="auditTargetSubject"
      :audit-target-diff="auditTargetDiff"
      @reload="reloadAuditLog"
      @load-more="loadAuditLog(false)"
      @expand-change="onAuditExpandChange"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, type FormInstanceFunctions, type FormRules } from 'tdesign-vue-next'
import { copyWithToast } from '@/utils/clipboard'
import { useAuthStore } from '@/stores/auth'
import { AUDIT_ACTION_I18N_ROOTS } from '@/i18n/auditActionRegistry'
import { auditActionLabel } from '@/i18n/auditActionLabel'
import {
  listMembers,
  updateMemberRole,
  removeMember,
  type TenantRole,
} from '@/api/tenant/members'
import {
  listTenantInvitations,
  createInvitation,
  createInviteLink,
  revokeInvitation,
  type TenantInvitation,
} from '@/api/tenant/invitations'
import { docsUrl } from '@/utils/docsUrl'
import { listAuditLog, type AuditAction } from '@/api/tenant/audit-log'
import TenantMembersTable from './components/TenantMembersTable.vue'
import TenantInvitationsPanel from './components/TenantInvitationsPanel.vue'
import TenantAuditLogDrawer from './components/TenantAuditLogDrawer.vue'
import {
  INVITATION_TTL_DAYS,
  extractErrorMessage,
  extractErrorStatus,
  roleMatrix,
  roleMatrixIcon,
  roleMatrixOrder,
  roleSelectPopupProps,
  useTenantMembersList,
} from './composables/useTenantMembersList'
import { useTenantAuditLog } from './composables/useTenantAuditLog'

const { t, tm, locale } = useI18n()
const authStore = useAuthStore()

const permissionsPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(520px, calc(100vw - 24px))',
  maxWidth: 'min(520px, calc(100vw - 24px))',
  maxHeight: 'min(400px, 65vh)',
  overflow: 'hidden',
}

const activeListTab = ref('members')
const currentRole = computed<TenantRole | ''>(
  () => (authStore.currentTenantRole || '') as TenantRole | '',
)
const canManage = computed(
  () => currentRole.value === 'owner' || authStore.canAccessAllTenants === true,
)
watch(canManage, (allowed) => {
  if (!allowed) activeListTab.value = 'members'
})

const canViewAudit = computed(
  () =>
    currentRole.value === 'owner' ||
    currentRole.value === 'admin' ||
    authStore.canAccessAllTenants === true,
)
const currentUserId = computed(() => authStore.user?.id ?? '')
const activeTenantId = computed(() => Number(authStore.currentTenantId ?? 0))

const roleOptions = computed<Array<{ label: string; value: TenantRole }>>(() => [
  { label: t('tenantMember.role.owner'), value: 'owner' },
  { label: t('tenantMember.role.admin'), value: 'admin' },
  { label: t('tenantMember.role.contributor'), value: 'contributor' },
  { label: t('tenantMember.role.viewer'), value: 'viewer' },
])

const {
  members,
  loading,
  error,
  searchQuery,
  membersTotal,
  membersPage,
  membersPageSize,
  memberDisplayByUserId,
  loadMembers,
  onMembersPageChange,
  onRoleChange,
  removeRow,
  actorDisplayName,
} = useTenantMembersList({
  activeTenantId,
  currentUserId,
  authStore: authStore as {
    memberships?: Array<{ tenant_id?: number | string; role?: string; [key: string]: unknown }>
    setMemberships?: (
      list: Array<{ tenant_id?: number | string; role?: string; [key: string]: unknown }>,
    ) => void
  },
  t: (key, params) => t(key, params ?? {}),
  onSuccess: (msg) => {
    MessagePlugin.success(msg)
  },
  onError: (msg) => {
    MessagePlugin.error(msg)
  },
  listMembers,
  updateMemberRole,
  removeMember,
})
void memberDisplayByUserId

const {
  auditDrawerVisible,
  auditEntries,
  auditLoading,
  auditError,
  auditHasMore,
  auditExpandedRowKeys,
  loadAuditLog,
  reloadAuditLog,
  openAuditDrawer,
  onAuditExpandChange,
  auditTargetSubject,
  auditTargetDiff,
  formatAuditAction,
} = useTenantAuditLog({
  activeTenantId,
  canViewAudit,
  locale,
  t: (key, params) => t(key, params ?? {}),
  formatAction: (action: AuditAction) =>
    auditActionLabel({ tm }, AUDIT_ACTION_I18N_ROOTS.tenantMember, action),
  actorDisplayName,
  listAuditLog,
})

// ---- Pending invitations & invite popups ---------------------------------

const invitations = ref<TenantInvitation[]>([])
const invitationsLoading = ref(false)
const invitationsError = ref('')
const invitationsTotal = ref(0)
const invitationsPage = ref(1)
const invitationsPageSize = ref(20)

const adding = ref(false)
const invitePopupVisible = ref(false)
const shareLinkPopupVisible = ref(false)
const shareLinkForm = reactive<{ role: TenantRole }>({ role: 'contributor' })
const creatingShareLink = ref(false)
const shareLinkResult = ref<TenantInvitation | null>(null)
const addDialogStep = ref<'form' | 'confirm'>('form')
const addFormRef = ref<FormInstanceFunctions | null>(null)
const addForm = reactive<{ email: string; role: TenantRole }>({
  email: '',
  role: 'contributor',
})

const addFormRules: FormRules<{ email: string; role: TenantRole }> = {
  email: [
    { required: true, message: t('tenantMember.errors.emailRequired'), trigger: 'blur' },
    { email: true, message: t('tenantMember.errors.emailFormat'), trigger: 'blur' },
  ],
  role: [{ required: true, message: t('tenantMember.errors.roleRequired'), trigger: 'change' }],
}

async function loadInvitations() {
  if (!activeTenantId.value || !canManage.value) {
    invitations.value = []
    invitationsTotal.value = 0
    return
  }
  invitationsLoading.value = true
  invitationsError.value = ''
  try {
    const resp = await listTenantInvitations(activeTenantId.value, {
      page: invitationsPage.value,
      page_size: invitationsPageSize.value,
    })
    if (resp.success && resp.data) {
      const total = resp.data.total ?? 0
      const ps = resp.data.page_size ?? invitationsPageSize.value
      const safePs = Math.max(1, ps)
      const maxPage = Math.max(1, Math.ceil(total / safePs))
      if (invitationsPage.value > maxPage) {
        invitationsPage.value = maxPage
        invitationsLoading.value = false
        await loadInvitations()
        return
      }
      invitations.value = resp.data.invitations ?? []
      invitationsTotal.value = total
      if (typeof resp.data.page === 'number' && resp.data.page > 0) {
        invitationsPage.value = resp.data.page
      }
      if (typeof resp.data.page_size === 'number' && resp.data.page_size > 0) {
        invitationsPageSize.value = resp.data.page_size
      }
    } else {
      invitationsError.value = resp.message || t('tenantInvitation.errors.generic')
    }
  } catch (err: unknown) {
    invitationsError.value = extractErrorMessage(err) || t('tenantInvitation.errors.generic')
  } finally {
    invitationsLoading.value = false
  }
}

function onInvitationsPageChange() {
  void loadInvitations()
}

async function doRevokeInvitation(row: TenantInvitation) {
  try {
    const resp = await revokeInvitation(activeTenantId.value, row.id)
    if (resp.success) {
      await loadInvitations()
      MessagePlugin.success(t('tenantInvitation.revoke.success'))
    } else {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
    }
  } catch (err: unknown) {
    const status = extractErrorStatus(err)
    if (status === 404) {
      MessagePlugin.error(t('tenantInvitation.errors.notFound'))
    } else if (status === 409) {
      MessagePlugin.error(extractErrorMessage(err) || t('tenantInvitation.errors.notPending'))
    } else {
      MessagePlugin.error(extractErrorMessage(err) || t('tenantInvitation.errors.generic'))
    }
  }
}

watch(invitePopupVisible, (open) => {
  if (!open) return
  addForm.email = ''
  addForm.role = 'contributor'
  addDialogStep.value = 'form'
})

watch(shareLinkPopupVisible, (open) => {
  if (!open) return
  shareLinkForm.role = 'contributor'
  shareLinkResult.value = null
})

function absoluteInviteURL(raw: string): string {
  if (!raw) return ''
  if (/^https?:\/\//i.test(raw)) return raw
  const origin =
    (typeof window !== 'undefined' && window.location && window.location.origin) || ''
  return raw.startsWith('/') ? origin + raw : origin + '/' + raw
}

async function copyText(text: string) {
  await copyWithToast(text, 'tenantInvitation.copied', 'tenantInvitation.copyFailed')
}

async function submitShareLink() {
  creatingShareLink.value = true
  try {
    const resp = await createInviteLink(activeTenantId.value, { role: shareLinkForm.role })
    if (!resp.success || !resp.data) {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
      return
    }
    shareLinkResult.value = resp.data
    invitationsPage.value = 1
    await loadInvitations()
  } catch (err: unknown) {
    MessagePlugin.error(extractErrorMessage(err) || t('tenantInvitation.errors.generic'))
  } finally {
    creatingShareLink.value = false
  }
}

const addConfirmEmail = computed(() => addForm.email.trim())
const addConfirmRoleLabel = computed(() => t('tenantMember.role.' + addForm.role))

async function submitAdd() {
  if (addDialogStep.value === 'form') {
    const valid = await addFormRef.value?.validate?.()
    if (valid !== true) return
    if (authStore.autoAcceptInvitation) {
      await sendInvitation(addForm.email.trim(), addForm.role)
      return
    }
    addDialogStep.value = 'confirm'
    return
  }
  await sendInvitation(addForm.email.trim(), addForm.role)
}

function goBackToForm() {
  addDialogStep.value = 'form'
}

const dialogConfirmLabel = computed(() =>
  addDialogStep.value === 'confirm' || authStore.autoAcceptInvitation
    ? t('tenantInvitation.confirmSend')
    : t('tenantInvitation.inviteSubmit'),
)

async function sendInvitation(email: string, role: TenantRole) {
  adding.value = true
  try {
    const resp = await createInvitation(activeTenantId.value, { email, role })
    if (resp.success) {
      const autoJoined = !!resp.data && 'user_id' in resp.data
      invitePopupVisible.value = false
      if (autoJoined) {
        await loadMembers()
      } else {
        invitationsPage.value = 1
        await loadInvitations()
        MessagePlugin.success(t('tenantInvitation.inviteSuccess'))
      }
    } else {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
    }
  } catch (err: unknown) {
    const status = extractErrorStatus(err)
    const msg = extractErrorMessage(err)
    if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.userNotFound'))
    } else if (status === 409) {
      MessagePlugin.error(
        msg ||
          `${t('tenantInvitation.errors.alreadyMember')} / ${t(
            'tenantInvitation.errors.pendingExists',
          )}`,
      )
    } else if (status === 400) {
      MessagePlugin.error(msg || t('tenantMember.errors.invalidRole'))
    } else {
      MessagePlugin.error(msg || t('tenantInvitation.errors.generic'))
    }
  } finally {
    adding.value = false
  }
}

watch(
  activeTenantId,
  (id) => {
    if (id) {
      invitationsPage.value = 1
      invitationsPageSize.value = 20
      invitationsTotal.value = 0
      void loadInvitations()
    }
  },
  { immediate: true },
)
</script>

<style lang="less" scoped>
@import './tenant-members.less';

.tenant-members {
  .settings-list-page();
  container-type: inline-size;
}

.section-header {
  .settings-section-header();
  flex-shrink: 0;
  margin-bottom: 12px;
  padding-right: 32px;
}

.section-header-row {
  margin-bottom: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.section-header-titlewrap {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;

  h2 {
    margin: 0;
  }
}

.members-header-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.header-audit-btn.t-button,
.members-list-add-btn.t-button {
  .settings-list-action();
}

.members-tab-layout {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px 0 32px;
}

.permissions-trigger-btn {
  .settings-title-help();
}

.members-tabbar {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--gy-border-subtle);
}

.members-tabs {
  flex: 1;
  min-width: 0;
  background: transparent;

  :deep(.t-tabs__nav-item) {
    height: 44px;
    margin-right: 24px;
    font-size: var(--gy-text-base);
  }
  :deep(.t-tabs__nav-item-wrapper) {
    margin: 0;
    padding: 0 2px;
  }
  :deep(.t-tabs__nav-item:not(.t-is-disabled):hover .t-tabs__nav-item-wrapper) {
    background: transparent;
  }
  :deep(.t-tabs__nav-container.t-is-top::after),
  :deep(.t-tabs__bar),
  :deep(.t-tabs__content),
  :deep(.t-tabs__operations) {
    display: none;
  }
  :deep(.t-tabs__nav-scroll) {
    overflow-x: auto;
    scrollbar-width: none;
  }
  :deep(.t-tabs__nav-item.t-is-active) {
    color: var(--gy-text-primary);
    text-shadow: none;
    .t-icon {
      color: var(--gy-brand);
    }
    .members-list-count-badge {
      color: var(--gy-brand);
      background: var(--gy-brand-soft);
    }
    &::after {
      content: '';
      position: absolute;
      bottom: 0;
      left: 0;
      right: 0;
      height: 2px;
      background: var(--gy-brand);
      border-radius: var(--gy-radius-pill);
    }
  }
}

.members-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.members-list-count-badge {
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  box-sizing: border-box;
  border-radius: var(--gy-radius-pill);
  background: var(--gy-surface-subtle);
  color: var(--gy-text-secondary);
  font-size: var(--gy-text-xs);
  line-height: 20px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.members-list-search {
  flex: 0 0 240px;
  width: 240px;
  :deep(.t-input) {
    border-radius: var(--gy-radius-sm);
  }
}

@container (max-width: 700px) {
  .members-tabbar {
    flex-wrap: wrap;
    gap: 0;
  }
  .members-tabs {
    flex-basis: 100%;
  }
  .members-list-search {
    flex: 1 1 100%;
    width: 100%;
    margin: 8px 0;
  }
}

.member-invite-popup-inner {
  max-width: 100%;
}

.member-invite-popup-title {
  font-size: var(--gy-text-lg);
  font-weight: 600;
  color: var(--gy-text-primary);
  margin: 0 0 12px;
  line-height: 1.35;
}

.member-invite-form {
  &:deep(.t-form__item) {
    margin-bottom: 14px;

    &:last-child {
      margin-bottom: 4px;
    }
  }
}

.invite-confirm-body {
  padding: 4px 0 8px;
  color: var(--gy-text-primary);
  font-size: var(--gy-text-base);
  line-height: 1.6;
}

.invite-popup-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

.share-link-result {
  padding: 4px 0 0;
}

.share-link-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.share-link-row__input {
  flex: 1 1 auto;
  min-width: 0;
  padding: 7px 10px;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-sm);
  background: var(--gy-surface-page);
  font-family: var(--gy-font-mono);
  font-size: var(--gy-text-sm);
  color: var(--gy-text-primary);
  outline: none;
}

.share-link-row__input:focus-visible {
  border-color: var(--gy-brand);
}
</style>

<style lang="less">
.permissions-popup-overlay {
  z-index: var(--gy-z-popover);

  .permissions-compact.permissions-compact--popover {
    padding: 12px 14px;
    margin: 0;
    max-height: min(392px, calc(65vh - 8px));
    overflow-x: hidden;
    overflow-y: auto;

    .permissions-compact-header {
      display: flex;
      flex-direction: column;
      gap: 2px;
      margin-bottom: 10px;

      .permissions-compact-title {
        font-size: var(--gy-text-md);
        font-weight: 600;
        color: var(--gy-text-primary);
      }

      .permissions-compact-desc {
        font-size: var(--gy-text-sm);
        line-height: 1.45;
        color: var(--gy-text-secondary);
      }
    }

    .permissions-compact-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
    }

    .perm-role-block {
      border: 1px solid var(--gy-border-subtle);
      border-radius: var(--gy-radius-sm);
      padding: 8px 10px;
      background: var(--gy-surface);

      &.is-me {
        border-color: var(--gy-brand);
        background: var(--gy-brand-soft);
      }

      .perm-role-tag {
        display: flex;
        align-items: center;
        gap: 4px;
        font-size: var(--gy-text-sm);
        font-weight: 600;
        color: var(--gy-text-primary);
        margin-bottom: 6px;

        .me-badge {
          margin-left: auto;
          font-size: var(--gy-text-2xs);
          font-weight: 500;
          color: var(--gy-brand);
          padding: 1px 5px;
          background: var(--gy-brand-soft);
          border-radius: var(--gy-radius-xs);
        }
      }

      .perm-items {
        display: flex;
        flex-direction: column;
        gap: 3px;

        .perm-item {
          display: flex;
          align-items: flex-start;
          gap: 4px;
          font-size: var(--gy-text-xs);
          line-height: 1.35;
          color: var(--gy-text-secondary);

          .t-icon {
            margin-top: 1px;
            flex-shrink: 0;
          }

          &.has .t-icon {
            color: var(--gy-brand);
          }

          &.no {
            color: var(--gy-text-disabled);

            .t-icon {
              color: var(--gy-text-disabled);
            }
          }
        }
      }
    }
  }
}

@media (max-width: 480px) {
  .permissions-popup-overlay .permissions-compact.permissions-compact--popover .permissions-compact-grid {
    grid-template-columns: 1fr;
  }
}

.member-invite-popup-overlay {
  z-index: var(--gy-z-popover);
}

.tenant-members-role-select-popup {
  z-index: calc(var(--gy-z-toast) + 1200);

  .role-option {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .role-option-icon {
    font-size: var(--gy-text-base);
    color: var(--gy-text-secondary);
  }
}

.t-drawer.tenant-members-audit-drawer.t-drawer--right .t-drawer__content-wrapper--right {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  max-width: 100vw;
  max-height: 100vh;
  height: 100%;
}

.t-drawer.tenant-members-audit-drawer .t-drawer__body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  overflow: hidden;
}

.t-drawer.tenant-members-audit-drawer .setting-drawer__body {
  flex: 1 1 auto;
  min-height: 0;
}
</style>
