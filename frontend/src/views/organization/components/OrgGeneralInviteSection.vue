<template>
  <div>
    <!-- Basic Information -->
    <div v-show="currentSection === 'basic'" class="section">
      <div class="section-header">
        <h2>{{ $t('organization.editor.basicTitle') }}</h2>
        <p class="section-description">{{ $t('organization.editor.basicDesc') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('organization.name') }} <span class="required">*</span></label>
            <p class="desc">{{ $t('organization.editor.nameTip') }}</p>
          </div>
          <div class="setting-control">
            <div class="name-input-wrapper">
              <t-popup
                :visible="avatarPopoverVisible"
                trigger="click"
                placement="bottom-left"
                :disabled="!isAdmin"
                overlay-class-name="avatar-emoji-popover"
                @visible-change="emit('update:avatarPopoverVisible', $event)"
              >
                <button
                  type="button"
                  class="avatar-trigger-wrap"
                  :disabled="!isAdmin"
                  :aria-label="$t('organization.avatar')"
                >
                  <SpaceAvatar :name="formData.name || '?'" :avatar="formData.avatar" size="medium" />
                  <span v-if="isAdmin" class="avatar-change-hint">{{ $t('organization.avatar') }}</span>
                </button>
                <template #content>
                  <div class="avatar-popover-content" @click.stop>
                    <p class="avatar-popover-title">{{ $t('organization.avatarPickerHint') }}</p>
                    <div class="avatar-emoji-grid">
                      <button
                        v-for="emoji in AVATAR_EMOJI_OPTIONS"
                        :key="emoji"
                        type="button"
                        class="avatar-emoji-btn"
                        :class="{ 'is-selected': formData.avatar === 'emoji:' + emoji }"
                        :aria-label="emoji"
                        @click="emit('selectAvatarEmoji', emoji)"
                      >
                        {{ emoji }}
                      </button>
                    </div>
                    <t-button
                      v-if="formData.avatar"
                      variant="text"
                      size="small"
                      class="avatar-clear-btn"
                      @click="emit('clearAvatarEmoji')"
                    >
                      {{ $t('organization.avatarClear') }}
                    </t-button>
                  </div>
                </template>
              </t-popup>
              <t-input
                :model-value="formData.name"
                :placeholder="$t('organization.namePlaceholder')"
                :disabled="!isAdmin"
                class="name-input"
                @update:model-value="(val: unknown) => emit('updateForm', { name: String(val ?? '') })"
              />
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('organization.description') }}</label>
            <p class="desc">{{ $t('organization.editor.descriptionTip') }}</p>
          </div>
          <div class="setting-control">
            <t-textarea
              :model-value="formData.description"
              :placeholder="$t('organization.descriptionPlaceholder')"
              :autosize="{ minRows: 3, maxRows: 6 }"
              :maxlength="500"
              :disabled="!isAdmin"
              @update:model-value="(val: unknown) => emit('updateForm', { description: String(val ?? '') })"
            />
          </div>
        </div>

        <div v-if="isAdmin && orgId" class="setting-row setting-row-vertical">
          <div class="setting-info full-width">
            <label>{{ $t('organization.settings.inviteMembers') }}</label>
            <p class="desc">{{ $t('organization.settings.inviteMembersDesc') }}</p>
          </div>
          <div class="setting-control full-width">
            <div class="invite-card">
              <div class="invite-method">
                <div class="invite-method-header">
                  <t-icon name="qrcode" class="invite-icon" />
                  <span class="invite-method-title">{{ $t('organization.inviteCode') }}</span>
                </div>
                <div class="invite-code-box">
                  <span class="invite-code-value">{{ inviteCode }}</span>
                  <div class="invite-code-actions">
                    <t-tooltip :content="$t('common.copy')">
                      <t-button
                        variant="text"
                        size="small"
                        :aria-label="$t('common.copy')"
                        @click="emit('copyInviteCode')"
                      >
                        <t-icon name="file-copy" />
                      </t-button>
                    </t-tooltip>
                    <t-tooltip :content="$t('organization.refreshInviteCode')">
                      <t-button
                        variant="text"
                        size="small"
                        :loading="refreshingCode"
                        :aria-label="$t('organization.refreshInviteCode')"
                        @click="emit('refreshInviteCode')"
                      >
                        <t-icon name="refresh" />
                      </t-button>
                    </t-tooltip>
                  </div>
                </div>
                <p v-if="inviteCode" class="invite-remaining">{{ remainingValidityText }}</p>
              </div>

              <div class="invite-divider"></div>

              <div class="invite-method">
                <div class="invite-method-header">
                  <t-icon name="time" class="invite-icon" />
                  <span class="invite-method-title">{{ $t('organization.settings.inviteLinkValidity') }}</span>
                </div>
                <p class="invite-validity-desc">{{ $t('organization.settings.inviteLinkValidityDesc') }}</p>
                <t-select
                  :model-value="formData.invite_code_validity_days"
                  :options="inviteValidityOptions"
                  size="small"
                  class="invite-validity-select"
                  :disabled="!isAdmin"
                  @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateForm', { invite_code_validity_days: val })"
                  @change="onValiditySelectChange"
                />
              </div>

              <div class="invite-divider"></div>

              <div class="invite-method">
                <div class="invite-method-header">
                  <t-icon name="link" class="invite-icon" />
                  <span class="invite-method-title">{{ $t('organization.settings.inviteLink') }}</span>
                </div>
                <div class="invite-link-box">
                  <span class="invite-link-value">{{ inviteLink }}</span>
                  <t-tooltip :content="$t('common.copy')">
                    <t-button
                      variant="text"
                      size="small"
                      :aria-label="$t('common.copy')"
                      @click="emit('copyInviteLink')"
                    >
                      <t-icon name="file-copy" />
                    </t-button>
                  </t-tooltip>
                </div>
              </div>

              <div class="invite-divider"></div>

              <div class="invite-method">
                <div class="invite-method-header">
                  <t-icon name="check-circle" class="invite-icon" />
                  <span class="invite-method-title">{{ $t('organization.settings.requireApproval') }}</span>
                </div>
                <div class="approval-toggle">
                  <t-switch
                    :model-value="formData.require_approval"
                    @update:model-value="(val: unknown) => emit('updateForm', { require_approval: Boolean(val) })"
                    @change="(val: unknown) => emit('approvalToggle', Boolean(val))"
                  />
                  <span class="approval-desc">{{ $t('organization.settings.requireApprovalDesc') }}</span>
                </div>
              </div>

              <div class="invite-divider"></div>

              <div class="invite-method">
                <div class="invite-method-header">
                  <t-icon name="search" class="invite-icon" />
                  <span class="invite-method-title">{{ $t('organization.settings.searchable') }}</span>
                </div>
                <div class="approval-toggle">
                  <t-switch
                    :model-value="formData.searchable"
                    @update:model-value="(val: unknown) => emit('updateForm', { searchable: Boolean(val) })"
                    @change="(val: unknown) => emit('searchableToggle', Boolean(val))"
                  />
                  <span class="approval-desc">{{ $t('organization.settings.searchableDesc') }}</span>
                </div>
              </div>

              <div class="invite-divider"></div>

              <div class="invite-method">
                <div class="invite-method-header">
                  <t-icon name="user-add" class="invite-icon" />
                  <span class="invite-method-title">{{ $t('organization.settings.memberLimit') }}</span>
                </div>
                <p class="invite-validity-desc">{{ $t('organization.settings.memberLimitDesc') }}</p>
                <div class="member-limit-input-row">
                  <t-input-number
                    :model-value="formData.member_limit"
                    :min="0"
                    :max="10000"
                    :placeholder="$t('organization.settings.memberLimitPlaceholder')"
                    theme="normal"
                    style="width: 140px;"
                    @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateForm', { member_limit: val })"
                  />
                  <span class="member-limit-hint">
                    {{ $t('organization.settings.memberLimitHint', { count: memberCount }) }}
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Create Mode Permissions Matrix -->
    <div v-if="isCreateMode" v-show="currentSection === 'permissions'" class="section">
      <div class="section-header">
        <h2>{{ $t('organization.editor.permissionsTitle') }}</h2>
        <p class="section-description">{{ $t('organization.editor.permissionsDesc') }}</p>
      </div>

      <div class="permission-comparison">
        <table class="permission-table" :aria-label="$t('organization.editor.permissionsTitle')">
          <thead>
            <tr>
              <th scope="col">{{ $t('organization.editor.permissionFeature') }}</th>
              <th v-for="role in ORG_ROLE_MATRIX_ORDER" :key="role" scope="col">
                {{ $t(`organization.role.${role}`) }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="permission in ORG_ROLE_MATRIX.admin" :key="permission.key">
              <th scope="row">{{ $t(`organization.editor.${permission.key}`) }}</th>
              <td v-for="role in ORG_ROLE_MATRIX_ORDER" :key="role">
                <span
                  v-if="ORG_ROLE_MATRIX[role].find((item) => item.key === permission.key)?.has"
                  class="permission-allowed"
                  role="img"
                  :aria-label="$t('common.yes')"
                  :title="$t('common.yes')"
                >
                  <t-icon name="check" size="16px" />
                </span>
                <span
                  v-else
                  class="permission-unavailable"
                  role="img"
                  :aria-label="$t('common.no')"
                  :title="$t('common.no')"
                >—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="info-notice">
        <t-icon name="info-circle" />
        <span>{{ $t('organization.editor.ownerNote') }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import SpaceAvatar from '@/components/SpaceAvatar.vue'
import {
  AVATAR_EMOJI_OPTIONS,
  ORG_ROLE_MATRIX,
  ORG_ROLE_MATRIX_ORDER,
  type OrganizationSettingsFormData,
} from '../composables/useOrganizationSettings'

defineProps<{
  currentSection: string
  isCreateMode: boolean
  isAdmin: boolean
  orgId?: string
  memberCount: number
  formData: OrganizationSettingsFormData
  avatarPopoverVisible: boolean
  inviteCode: string
  inviteLink: string
  remainingValidityText: string
  refreshingCode: boolean
  inviteValidityOptions: Array<{ label: string; value: number }>
}>()

const emit = defineEmits<{
  (e: 'updateForm', patch: Partial<OrganizationSettingsFormData>): void
  (e: 'update:avatarPopoverVisible', value: boolean): void
  (e: 'selectAvatarEmoji', emoji: string): void
  (e: 'clearAvatarEmoji'): void
  (e: 'copyInviteCode'): void
  (e: 'refreshInviteCode'): void
  (e: 'copyInviteLink'): void
  (e: 'validityChange', value: number): void
  (e: 'approvalToggle', value: boolean): void
  (e: 'searchableToggle', value: boolean): void
}>()

function onValiditySelectChange(value: unknown) {
  if (typeof value === 'number') {
    emit('validityChange', value)
  }
}
</script>
