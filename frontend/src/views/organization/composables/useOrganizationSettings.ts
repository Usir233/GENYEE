import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import type {
  OrganizationInfo,
  OrganizationMember,
  KnowledgeBaseShare,
  AgentShareResponse,
  JoinRequestResponse,
  TenantInviteCandidate,
  ReviewJoinRequestPayload,
  InviteMemberRequest,
  RequestRoleUpgradePayload,
  UpdateOrganizationRequest,
} from '@/api/organization'

export type OrgRole = 'admin' | 'editor' | 'viewer'
export type OrgRolePerm = { key: string; has: boolean }

export interface OrganizationSettingsFormData {
  name: string
  description: string
  avatar: string
  require_approval: boolean
  searchable: boolean
  invite_code_validity_days: number
  member_limit: number
}

export const AVATAR_EMOJI_OPTIONS = [
  '🚀', '📁', '👥', '🏢', '💡', '📚', '🌟', '🔧', '📌', '🎯',
  '📂', '🔒', '🌐', '⚡', '🎨', '📊', '🤝', '💼', '📧', '🏠',
  '🔑', '📈', '✨', '📋', '🌍', '💬', '🔔', '📦', '🎉', '🌈',
]

export const ORG_ROLE_MATRIX_ORDER: OrgRole[] = ['admin', 'editor', 'viewer']

export const ORG_ROLE_MATRIX: Record<OrgRole, OrgRolePerm[]> = {
  admin: [
    { key: 'viewerPerm1', has: true },
    { key: 'editorPerm1', has: true },
    { key: 'useSharedAgentsPerm', has: true },
    { key: 'shareKBPerm', has: true },
    { key: 'adminPerm1', has: true },
  ],
  editor: [
    { key: 'viewerPerm1', has: true },
    { key: 'editorPerm1', has: true },
    { key: 'useSharedAgentsPerm', has: true },
    { key: 'shareKBPerm', has: true },
    { key: 'adminPerm1', has: false },
  ],
  viewer: [
    { key: 'viewerPerm1', has: true },
    { key: 'editorPerm1', has: false },
    { key: 'useSharedAgentsPerm', has: true },
    { key: 'shareKBPerm', has: false },
    { key: 'adminPerm1', has: false },
  ],
}

export function orgRoleIcon(role: OrgRole): string {
  switch (role) {
    case 'admin':
      return 'user-safety'
    case 'editor':
      return 'edit'
    default:
      return 'browse'
  }
}

export function getRoleTheme(role: string): 'primary' | 'warning' | 'default' {
  switch (role) {
    case 'admin':
      return 'primary'
    case 'editor':
      return 'warning'
    default:
      return 'default'
  }
}

export function getPermissionTheme(permission: string): 'primary' | 'warning' | 'default' {
  return getRoleTheme(permission)
}

export function formatOrgDate(dateStr: string): string {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function getErrorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const msg = (err as { message?: unknown }).message
    if (typeof msg === 'string' && msg) return msg
  }
  return fallback
}

export interface OrganizationStorePort {
  currentOrganization: OrganizationInfo | null
  currentMembers: OrganizationMember[]
  error?: string | null
  clearCurrentOrganizationContext: () => void
  setCurrentOrganization?: (org: OrganizationInfo) => void
  fetchMembers: (orgId: string) => Promise<unknown>
  reviewOrganizationJoinRequest?: (
    orgId: string,
    requestId: string,
    payload: ReviewJoinRequestPayload,
    options?: { requestType?: 'join' | 'upgrade' },
  ) => Promise<{ success: boolean; message?: string }>
  create?: (name: string, description: string, avatar?: string) => Promise<unknown>
  updateOrganization?: (orgId: string, payload: UpdateOrganizationRequest) => Promise<unknown>
  changeMemberRole?: (orgId: string, tenantId: number, role: OrgRole) => Promise<boolean>
  kickMember?: (orgId: string, tenantId: number) => Promise<boolean>
  requestOrganizationRoleUpgrade?: (
    orgId: string,
    payload: RequestRoleUpgradePayload,
  ) => Promise<{ success: boolean; message?: string }>
  inviteOrganizationMember?: (
    orgId: string,
    payload: InviteMemberRequest,
  ) => Promise<{ success: boolean; message?: string }>
  refreshInviteCode?: (orgId: string) => Promise<string | null | undefined>
  unshareKnowledgeBase?: (
    kbId: string,
    shareId: string,
    orgId: string,
  ) => Promise<{ success: boolean; message?: string }>
  unshareAgent?: (
    agentId: string,
    shareId: string,
    orgId: string,
  ) => Promise<{ success: boolean; message?: string }>
}

export interface OrganizationApiPort {
  getOrganization: (id: string) => Promise<{ success: boolean; data?: OrganizationInfo }>
  listOrgShares: (id: string) => Promise<{ success: boolean; data?: { shares?: KnowledgeBaseShare[] } }>
  listOrgAgentShares: (id: string) => Promise<{ success: boolean; data?: { shares?: AgentShareResponse[] } }>
  listJoinRequests: (id: string) => Promise<{ success: boolean; data?: { requests?: JoinRequestResponse[] } }>
  searchTenantsForInvite: (
    orgId: string,
    query: string,
    limit: number,
  ) => Promise<{ success: boolean; data?: TenantInviteCandidate[] }>
}

export interface UseOrganizationSettingsOptions {
  props: {
    readonly visible: boolean
    readonly orgId?: string
    readonly mode?: 'view' | 'edit' | 'create'
  }
  emitSaved: () => void
  closeModal: () => void
  markModalClean: () => void
  scrollContentToTop: () => Promise<void>
  lockBackgroundScroll: () => void
  unlockBackgroundScroll: () => void
  getWindowOrigin: () => string
  navigateTo: (path: string) => void
  hasTenantAdminRole: () => boolean
  t: (key: string, params?: Record<string, unknown>) => string
  message: {
    success: (content: string) => unknown
    error: (content: string) => unknown
    warning: (content: string) => unknown
  }
  copyWithToast: (text: string, i18nKey: string) => Promise<unknown>
  orgStore: OrganizationStorePort
  api: OrganizationApiPort
}

const TENANT_SEARCH_DEBOUNCE_MS = 300
const TENANT_SEARCH_LIMIT = 10
const TENANT_ID_PATTERN = /^[1-9]\d*$/

export function useOrganizationSettings(options: UseOrganizationSettingsOptions) {
  const {
    props,
    emitSaved,
    closeModal,
    markModalClean,
    scrollContentToTop,
    lockBackgroundScroll,
    unlockBackgroundScroll,
    getWindowOrigin,
    navigateTo,
    hasTenantAdminRole,
    t,
    message,
    copyWithToast,
    orgStore,
    api,
  } = options

  const currentSection = ref('basic')
  const orgInfo = computed(() => orgStore.currentOrganization)
  const members = computed(() => orgStore.currentMembers)
  const sharedKnowledgeBases = ref<KnowledgeBaseShare[]>([])
  const sharedAgents = ref<AgentShareResponse[]>([])
  const joinRequests = ref<JoinRequestResponse[]>([])
  const joinRequestsLoading = ref(false)
  const joinRequestSearchQuery = ref('')
  const reviewingRequestId = ref<string | null>(null)
  const sharesLoading = ref(false)
  const membersLoading = ref(false)
  const memberSearchQuery = ref('')
  const submitting = ref(false)
  const refreshingCode = ref(false)
  const inviteCode = ref('')
  const inviteCodeExpiresAt = ref<string | null>(null)
  const upgradePopupVisible = ref(false)
  const upgradeSubmitting = ref(false)
  const hasPendingUpgrade = ref(false)
  const upgradeForm = ref<{ requested_role: OrgRole; message: string }>({
    requested_role: 'editor',
    message: '',
  })

  const addMemberPopupVisible = ref(false)
  const addMemberSubmitting = ref(false)
  const tenantSearchLoading = ref(false)
  const tenantSearchResults = ref<TenantInviteCandidate[]>([])
  const selectedTenantId = ref<number | null>(null)
  const selectedTenantCandidate = ref<TenantInviteCandidate | null>(null)
  const addMemberRole = ref<OrgRole>('viewer')

  const formData = ref<OrganizationSettingsFormData>({
    name: '',
    description: '',
    avatar: '',
    require_approval: false,
    searchable: false,
    invite_code_validity_days: 7,
    member_limit: 50,
  })

  const avatarPopoverVisible = ref(false)
  function selectAvatarEmoji(emoji: string) {
    formData.value.avatar = `emoji:${emoji}`
    avatarPopoverVisible.value = false
  }
  function clearAvatarEmoji() {
    formData.value.avatar = ''
    avatarPopoverVisible.value = false
  }

  const isCreateMode = computed(() => props.mode === 'create')
  const hasTenantAdmin = computed(() => hasTenantAdminRole())
  const isAdmin = computed(() => {
    if (isCreateMode.value) return hasTenantAdmin.value
    const orgAdmin = orgInfo.value?.my_role === 'admin' || orgInfo.value?.is_owner
    return Boolean(orgAdmin) && hasTenantAdmin.value
  })
  const showTenantRoleHint = computed(() => {
    if (isCreateMode.value) return !hasTenantAdmin.value
    const orgAdmin = orgInfo.value?.my_role === 'admin' || orgInfo.value?.is_owner
    return Boolean(orgAdmin) && !hasTenantAdmin.value
  })
  const canRequestUpgrade = computed(() => {
    if (isCreateMode.value || !props.orgId) return false
    const myRole = orgInfo.value?.my_role
    if (!myRole || myRole === 'admin') return false
    return hasTenantAdmin.value
  })

  const upgradeRoleOptions = computed(() => {
    const myRole = orgInfo.value?.my_role || 'viewer'
    const opts: Array<{ label: string; value: 'editor' | 'admin' }> = []
    if (myRole === 'viewer') {
      opts.push({ label: t('organization.role.editor'), value: 'editor' })
      opts.push({ label: t('organization.role.admin'), value: 'admin' })
    } else if (myRole === 'editor') {
      opts.push({ label: t('organization.role.admin'), value: 'admin' })
    }
    return opts
  })

  const addMemberRoleOptions = computed(() => [
    { label: t('organization.role.viewer'), value: 'viewer' },
    { label: t('organization.role.editor'), value: 'editor' },
    { label: t('organization.role.admin'), value: 'admin' },
  ])

  const roleOptions = computed(() => [
    { label: t('organization.role.admin'), value: 'admin' },
    { label: t('organization.role.editor'), value: 'editor' },
    { label: t('organization.role.viewer'), value: 'viewer' },
  ])

  const tenantSearchOptions = computed(() =>
    tenantSearchResults.value.map((c) => ({
      label: c.tenant_name || `tenant#${c.tenant_id}`,
      value: c.tenant_id,
    })),
  )

  const modalTitle = computed(() =>
    isCreateMode.value ? t('organization.createOrg') : t('organization.settings.editTitle'),
  )

  const navItems = computed(() => {
    const items: Array<{
      key: string
      icon: string
      label: string
      badge?: number
      badgeClass?: string
      showZeroBadge?: boolean
    }> = [{ key: 'basic', icon: 'info-circle', label: t('organization.editor.navBasic') }]
    if (isCreateMode.value) {
      items.push({ key: 'permissions', icon: 'user-safety', label: t('organization.editor.navPermissions') })
    }
    if (props.orgId && !isCreateMode.value) {
      items.push({ key: 'members', icon: 'user', label: t('organization.manageMembers') })
      if (isAdmin.value) {
        const pendingCount = orgInfo.value?.pending_join_request_count ?? 0
        items.push({
          key: 'joinRequests',
          icon: 'user-add',
          label: t('organization.settings.joinRequests'),
          badge: pendingCount > 0 ? pendingCount : undefined,
        })
      }
      items.push({
        key: 'sharedKb',
        badgeClass: 'nav-badge-count',
        showZeroBadge: true,
        icon: 'folder-open',
        label: t('organization.share.sharedKnowledgeBase'),
        badge: sharedKnowledgeBases.value.length,
      })
      items.push({
        key: 'sharedAgents',
        badgeClass: 'nav-badge-count',
        showZeroBadge: true,
        icon: 'control-platform',
        label: t('organization.settings.sharedAgents'),
        badge: sharedAgents.value.length,
      })
    }
    return items
  })

  const navGroups = computed(() => {
    const itemMap = new Map(navItems.value.map((item) => [item.key, item]))
    const pickItems = (keys: string[]) =>
      keys.map((key) => itemMap.get(key)).filter((item): item is NonNullable<typeof item> => Boolean(item))
    if (isCreateMode.value) {
      return [
        {
          key: 'basic',
          label: t('organization.navGroups.basic'),
          items: pickItems(['basic', 'permissions']),
        },
      ].filter((group) => group.items.length > 0)
    }
    return [
      {
        key: 'basic',
        label: t('organization.navGroups.basic'),
        items: pickItems(['basic']),
      },
      {
        key: 'management',
        label: t('organization.navGroups.management'),
        items: pickItems(['members', 'joinRequests']),
      },
      {
        key: 'resources',
        label: t('organization.navGroups.resources'),
        items: pickItems(['sharedKb', 'sharedAgents']),
      },
    ].filter((group) => group.items.length > 0)
  })

  const filteredMembers = computed(() => {
    const query = memberSearchQuery.value.toLowerCase()
    if (!query) return members.value
    return members.value.filter(
      (m) =>
        (m.tenant_name || '').toLowerCase().includes(query) ||
        (m.username || '').toLowerCase().includes(query) ||
        (m.email || '').toLowerCase().includes(query),
    )
  })

  const filteredJoinRequests = computed(() => {
    const query = joinRequestSearchQuery.value.trim().toLowerCase()
    if (!query) return joinRequests.value
    return joinRequests.value.filter((req) => {
      const haystack = [req.username, req.email, req.user_id, req.message]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(query)
    })
  })

  const inviteLink = computed(() => {
    if (!inviteCode.value) return ''
    return `${getWindowOrigin()}/join?code=${encodeURIComponent(inviteCode.value)}`
  })

  const inviteValidityOptions = computed(() => [
    { label: t('organization.settings.validity1Day'), value: 1 },
    { label: t('organization.settings.validity7Days'), value: 7 },
    { label: t('organization.settings.validity30Days'), value: 30 },
    { label: t('organization.settings.validityNever'), value: 0 },
  ])

  const remainingValidityText = computed(() => {
    const at = inviteCodeExpiresAt.value
    if (!at) return t('organization.settings.remainingValidityNever')
    const exp = new Date(at)
    const now = new Date()
    if (exp.getTime() <= now.getTime()) return t('organization.settings.remainingValidityExpired')
    const days = Math.ceil((exp.getTime() - now.getTime()) / (24 * 60 * 60 * 1000))
    return t('organization.settings.remainingValidity', { n: days })
  })

  const isOwnerMember = (member: OrganizationMember): boolean => {
    const ownerTenantID = orgInfo.value?.owner_tenant_id
    if (ownerTenantID && ownerTenantID > 0) {
      return member.tenant_id === ownerTenantID
    }
    return member.user_id === orgInfo.value?.owner_id
  }

  let orgContextLoadGen = 0
  const isStaleOrgLoad = (gen: number, targetOrgId: string | undefined) =>
    gen !== orgContextLoadGen || !props.visible || props.orgId !== targetOrgId

  const fetchOrgDetail = async () => {
    const targetOrgId = props.orgId
    if (!targetOrgId) return
    const gen = orgContextLoadGen
    try {
      const res = await api.getOrganization(targetOrgId)
      if (isStaleOrgLoad(gen, targetOrgId)) return
      if (res.success && res.data) {
        orgStore.setCurrentOrganization?.(res.data)
        const validity = res.data.invite_code_validity_days
        const memberLimit = res.data.member_limit
        formData.value = {
          name: res.data.name,
          description: res.data.description || '',
          avatar: res.data.avatar || '',
          require_approval: res.data.require_approval || false,
          searchable: res.data.searchable || false,
          invite_code_validity_days: typeof validity === 'number' ? validity : 7,
          member_limit: typeof memberLimit === 'number' && memberLimit >= 0 ? memberLimit : 50,
        }
        inviteCode.value = res.data.invite_code || ''
        inviteCodeExpiresAt.value = res.data.invite_code_expires_at ?? null
        markModalClean()
        hasPendingUpgrade.value = res.data.has_pending_upgrade || false
      }
    } catch (error: unknown) {
      console.error('Failed to fetch org:', error)
    }
  }

  const fetchMembers = async () => {
    const targetOrgId = props.orgId
    if (!targetOrgId) return
    const gen = orgContextLoadGen
    membersLoading.value = true
    try {
      await orgStore.fetchMembers(targetOrgId)
    } catch (error: unknown) {
      console.error('Failed to fetch members:', error)
    } finally {
      if (!isStaleOrgLoad(gen, targetOrgId)) {
        membersLoading.value = false
      }
    }
  }

  const fetchSharedKBs = async () => {
    const targetOrgId = props.orgId
    if (!targetOrgId) return
    const gen = orgContextLoadGen
    sharesLoading.value = true
    try {
      const [kbRes, agentRes] = await Promise.all([
        api.listOrgShares(targetOrgId),
        api.listOrgAgentShares(targetOrgId),
      ])
      if (isStaleOrgLoad(gen, targetOrgId)) return
      sharedKnowledgeBases.value = kbRes.success && kbRes.data ? kbRes.data.shares || [] : []
      sharedAgents.value = agentRes.success && agentRes.data ? agentRes.data.shares || [] : []
    } catch (error: unknown) {
      if (isStaleOrgLoad(gen, targetOrgId)) return
      console.error('Failed to fetch shared resources:', error)
      sharedKnowledgeBases.value = []
      sharedAgents.value = []
    } finally {
      if (!isStaleOrgLoad(gen, targetOrgId)) {
        sharesLoading.value = false
      }
    }
  }

  const approvePopupRequestId = ref<string | null>(null)
  const approveAssignRole = ref<OrgRole>('viewer')

  function normalizeJoinRequestRole(role: string): OrgRole {
    if (role === 'admin' || role === 'editor' || role === 'viewer') return role
    return 'viewer'
  }

  function openApprovePopup(req: JoinRequestResponse) {
    approvePopupRequestId.value = req.id
    approveAssignRole.value = normalizeJoinRequestRole(req.requested_role)
  }

  function closeApprovePopup() {
    approvePopupRequestId.value = null
  }

  function handleApprovePopupVisibleChange(visible: boolean, req: JoinRequestResponse) {
    if (visible) {
      openApprovePopup(req)
      return
    }
    if (approvePopupRequestId.value === req.id) {
      closeApprovePopup()
    }
  }

  const fetchJoinRequests = async () => {
    const targetOrgId = props.orgId
    if (!targetOrgId) return
    const gen = orgContextLoadGen
    joinRequestsLoading.value = true
    try {
      const res = await api.listJoinRequests(targetOrgId)
      if (isStaleOrgLoad(gen, targetOrgId)) return
      joinRequests.value = res.success && res.data ? res.data.requests || [] : []
    } catch (error: unknown) {
      if (isStaleOrgLoad(gen, targetOrgId)) return
      console.error('Failed to fetch join requests:', error)
      joinRequests.value = []
    } finally {
      if (!isStaleOrgLoad(gen, targetOrgId)) {
        joinRequestsLoading.value = false
      }
    }
  }

  const refreshOrganizationAfterReview = async () => {
    await Promise.all([
      fetchOrgDetail(),
      fetchMembers()
    ])
  }

  const handleApproveRequest = async (req: JoinRequestResponse, assignRole: OrgRole): Promise<boolean> => {
    if (!props.orgId || !orgStore.reviewOrganizationJoinRequest) return false
    reviewingRequestId.value = req.id
    try {
      const res = await orgStore.reviewOrganizationJoinRequest(
        props.orgId,
        req.id,
        { approved: true, role: assignRole },
        { requestType: req.request_type },
      )
      if (res.success) {
        message.success(t('organization.settings.approveSuccess'))
        joinRequests.value = joinRequests.value.filter((r) => r.id !== req.id)
        await refreshOrganizationAfterReview()
        return true
      }
      message.error(res.message || t('organization.settings.reviewFailed'))
      return false
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.settings.reviewFailed')))
      return false
    } finally {
      reviewingRequestId.value = null
    }
  }

  const confirmApproveRequest = async (req: JoinRequestResponse) => {
    const success = await handleApproveRequest(req, approveAssignRole.value)
    if (success) closeApprovePopup()
  }

  const handleRejectRequest = async (req: JoinRequestResponse) => {
    if (!props.orgId || !orgStore.reviewOrganizationJoinRequest) return
    reviewingRequestId.value = req.id
    try {
      const res = await orgStore.reviewOrganizationJoinRequest(
        props.orgId,
        req.id,
        { approved: false },
        { requestType: req.request_type },
      )
      if (res.success) {
        message.success(t('organization.settings.rejectSuccess'))
        joinRequests.value = joinRequests.value.filter((r) => r.id !== req.id)
        await refreshOrganizationAfterReview()
      } else {
        message.error(res.message || t('organization.settings.reviewFailed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.settings.reviewFailed')))
    } finally {
      reviewingRequestId.value = null
    }
  }

  const handleSave = async () => {
    if (!formData.value.name.trim()) {
      message.warning(t('organization.nameRequired'))
      currentSection.value = 'basic'
      return
    }
    submitting.value = true
    try {
      if (isCreateMode.value) {
        const result = await orgStore.create?.(
          formData.value.name.trim(),
          formData.value.description.trim(),
          formData.value.avatar || undefined,
        )
        if (result) {
          message.success(t('organization.createSuccess'))
          emitSaved()
          closeModal()
        } else {
          message.error(orgStore.error || t('organization.createFailed'))
        }
      } else {
        if (!props.orgId) return
        const result = await orgStore.updateOrganization?.(props.orgId, {
          name: formData.value.name.trim(),
          description: formData.value.description.trim(),
          avatar: formData.value.avatar || undefined,
          require_approval: formData.value.require_approval,
          searchable: formData.value.searchable,
          invite_code_validity_days: formData.value.invite_code_validity_days,
          member_limit: formData.value.member_limit,
        })
        if (result) {
          message.success(t('common.saveSuccess'))
          emitSaved()
          closeModal()
        } else {
          message.error(orgStore.error || t('common.saveFailed'))
        }
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('common.saveFailed')))
    } finally {
      submitting.value = false
    }
  }

  const handleRoleChange = async (member: OrganizationMember, newRole: string) => {
    if (!props.orgId || !orgStore.changeMemberRole) return
    try {
      const success = await orgStore.changeMemberRole(
        props.orgId,
        member.tenant_id,
        newRole as OrgRole,
      )
      if (success) {
        message.success(t('organization.roleUpdated'))
      } else {
        message.error(orgStore.error || t('organization.roleUpdateFailed'))
        void fetchMembers()
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.roleUpdateFailed')))
      void fetchMembers()
    }
  }

  const confirmRemoveMember = async (member: OrganizationMember) => {
    if (!props.orgId || !orgStore.kickMember) return
    try {
      const success = await orgStore.kickMember(props.orgId, member.tenant_id)
      if (success) {
        message.success(t('organization.memberRemoved'))
      } else {
        message.error(orgStore.error || t('organization.memberRemoveFailed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.memberRemoveFailed')))
    }
  }

  watch(upgradePopupVisible, (visible) => {
    if (!visible) return
    upgradeForm.value = {
      requested_role: upgradeRoleOptions.value[0]?.value || 'editor',
      message: '',
    }
  })

  const handleSubmitUpgrade = async () => {
    if (!props.orgId || !orgStore.requestOrganizationRoleUpgrade) return
    upgradeSubmitting.value = true
    try {
      const res = await orgStore.requestOrganizationRoleUpgrade(props.orgId, {
        requested_role: upgradeForm.value.requested_role,
        message: upgradeForm.value.message,
      })
      if (res.success) {
        message.success(t('organization.upgrade.submitSuccess'))
        hasPendingUpgrade.value = true
        upgradePopupVisible.value = false
      } else {
        message.error(res.message || t('organization.upgrade.submitFailed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.upgrade.submitFailed')))
    } finally {
      upgradeSubmitting.value = false
    }
  }

  let tenantSearchTimer: ReturnType<typeof setTimeout> | null = null
  let tenantSearchGeneration = 0
  let tenantSearchInput = ''

  const invalidateTenantSearch = () => {
    tenantSearchGeneration += 1
    if (tenantSearchTimer !== null) {
      clearTimeout(tenantSearchTimer)
      tenantSearchTimer = null
    }
    tenantSearchResults.value = []
    tenantSearchLoading.value = false
  }

  const handleTenantInputChange = (query: string) => {
    tenantSearchInput = query.trim()
    invalidateTenantSearch()
  }

  const handleTenantSearch = (query: string) => {
    const workspaceID = query.trim()
    if (workspaceID !== tenantSearchInput) return
    invalidateTenantSearch()
    const orgId = props.orgId
    if (!TENANT_ID_PATTERN.test(workspaceID) || !orgId || !props.visible || !addMemberPopupVisible.value) {
      return
    }
    const generation = tenantSearchGeneration
    const isCurrentSearch = () =>
      generation === tenantSearchGeneration &&
      props.orgId === orgId &&
      props.visible &&
      addMemberPopupVisible.value

    tenantSearchTimer = setTimeout(async () => {
      tenantSearchTimer = null
      if (!isCurrentSearch()) return
      tenantSearchLoading.value = true
      try {
        const res = await api.searchTenantsForInvite(orgId, workspaceID, TENANT_SEARCH_LIMIT)
        if (isCurrentSearch() && res.success && res.data) {
          tenantSearchResults.value = res.data
        }
      } catch (error: unknown) {
        if (isCurrentSearch()) {
          console.error('Failed to search tenants:', error)
        }
      } finally {
        if (isCurrentSearch()) {
          tenantSearchLoading.value = false
        }
      }
    }, TENANT_SEARCH_DEBOUNCE_MS)
  }

  watch(
    [selectedTenantId, tenantSearchResults],
    ([tenantId, candidates], [previousTenantId]) => {
      const candidate = candidates.find((item) => item.tenant_id === tenantId)
      if (candidate || tenantId !== previousTenantId) {
        selectedTenantCandidate.value = candidate ?? null
      }
    },
    { flush: 'sync' },
  )

  const resetAddMemberDialog = () => {
    handleTenantInputChange('')
    selectedTenantId.value = null
    addMemberRole.value = 'viewer'
  }

  const handleAddMember = async () => {
    if (!props.orgId || selectedTenantId.value == null || !orgStore.inviteOrganizationMember) return
    const candidate =
      tenantSearchResults.value.find((c) => c.tenant_id === selectedTenantId.value) ??
      selectedTenantCandidate.value
    addMemberSubmitting.value = true
    try {
      const res = await orgStore.inviteOrganizationMember(props.orgId, {
        tenant_id: selectedTenantId.value,
        representative_user_id: candidate?.representative_user_id,
        role: addMemberRole.value,
      })
      if (res.success) {
        message.success(t('organization.addMember.success'))
        addMemberPopupVisible.value = false
        resetAddMemberDialog()
        void fetchMembers()
      } else {
        message.error(res.message || t('organization.addMember.failed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.addMember.failed')))
    } finally {
      addMemberSubmitting.value = false
    }
  }

  const copyInviteCode = async () => {
    await copyWithToast(inviteCode.value, 'common.copied')
  }

  const copyInviteLink = async () => {
    await copyWithToast(inviteLink.value, 'common.copied')
  }

  const refreshInviteCode = async () => {
    if (!props.orgId || !orgStore.refreshInviteCode) return
    refreshingCode.value = true
    try {
      const code = await orgStore.refreshInviteCode(props.orgId)
      if (code) {
        inviteCode.value = code
        message.success(t('organization.inviteCodeRefreshed'))
        await fetchOrgDetail()
      } else {
        message.error(orgStore.error || t('organization.inviteCodeRefreshFailed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.inviteCodeRefreshFailed')))
    } finally {
      refreshingCode.value = false
    }
  }

  const handleValidityChange = async (value: number) => {
    if (!props.orgId || !orgStore.updateOrganization) return
    try {
      const result = await orgStore.updateOrganization(props.orgId, {
        invite_code_validity_days: value,
      })
      if (result) {
        message.success(t('common.saveSuccess'))
      } else {
        formData.value.invite_code_validity_days = orgInfo.value?.invite_code_validity_days ?? 7
        message.error(orgStore.error || t('common.saveFailed'))
      }
    } catch (error: unknown) {
      formData.value.invite_code_validity_days = orgInfo.value?.invite_code_validity_days ?? 7
      message.error(getErrorMessage(error, t('common.saveFailed')))
    }
  }

  const handleApprovalToggle = async (value: boolean) => {
    if (!props.orgId || !orgStore.updateOrganization) return
    try {
      const result = await orgStore.updateOrganization(props.orgId, {
        require_approval: value,
      })
      if (result) {
        message.success(t('common.saveSuccess'))
      } else {
        formData.value.require_approval = !value
        message.error(orgStore.error || t('common.saveFailed'))
      }
    } catch (error: unknown) {
      formData.value.require_approval = !value
      message.error(getErrorMessage(error, t('common.saveFailed')))
    }
  }

  const handleSearchableToggle = async (value: boolean) => {
    if (!props.orgId || !orgStore.updateOrganization) return
    try {
      const result = await orgStore.updateOrganization(props.orgId, {
        searchable: value,
      })
      if (result) {
        message.success(t('common.saveSuccess'))
      } else {
        formData.value.searchable = !value
        message.error(orgStore.error || t('common.saveFailed'))
      }
    } catch (error: unknown) {
      formData.value.searchable = !value
      message.error(getErrorMessage(error, t('common.saveFailed')))
    }
  }

  const handleShareClick = (share: KnowledgeBaseShare) => {
    closeModal()
    navigateTo(`/platform/knowledge-bases/${share.knowledge_base_id}`)
  }

  const handleRemoveShare = async (share: KnowledgeBaseShare) => {
    if (!props.orgId || !orgStore.unshareKnowledgeBase) return
    try {
      const res = await orgStore.unshareKnowledgeBase(
        share.knowledge_base_id,
        share.id,
        props.orgId,
      )
      if (res.success) {
        message.success(t('organization.settings.removeShareSuccess'))
        sharedKnowledgeBases.value = sharedKnowledgeBases.value.filter((s) => s.id !== share.id)
      } else {
        message.error(res.message || t('organization.settings.removeShareFailed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.settings.removeShareFailed')))
    }
  }

  const handleRemoveAgentShare = async (share: AgentShareResponse) => {
    if (!props.orgId || !orgStore.unshareAgent) return
    try {
      const res = await orgStore.unshareAgent(share.agent_id, share.id, props.orgId)
      if (res.success) {
        message.success(t('organization.settings.removeShareSuccess'))
        sharedAgents.value = sharedAgents.value.filter((s) => s.id !== share.id)
      } else {
        message.error(res.message || t('organization.settings.removeShareFailed'))
      }
    } catch (error: unknown) {
      message.error(getErrorMessage(error, t('organization.settings.removeShareFailed')))
    }
  }

  watch(() => props.visible, (newVal) => {
    if (newVal) {
      lockBackgroundScroll()
      void scrollContentToTop()
      currentSection.value = 'basic'
      memberSearchQuery.value = ''
      joinRequestSearchQuery.value = ''
      approvePopupRequestId.value = null
      joinRequests.value = []
      if (props.mode === 'create') {
        formData.value = {
          name: '',
          description: '',
          avatar: '',
          require_approval: false,
          searchable: false,
          invite_code_validity_days: 7,
          member_limit: 50,
        }
        markModalClean()
        orgStore.clearCurrentOrganizationContext()
        sharedKnowledgeBases.value = []
        sharedAgents.value = []
        inviteCode.value = ''
        inviteCodeExpiresAt.value = null
      } else if (props.orgId) {
        if (orgStore.currentOrganization?.id !== props.orgId) {
          orgStore.clearCurrentOrganizationContext()
          sharedKnowledgeBases.value = []
          sharedAgents.value = []
        }
        void fetchOrgDetail()
        void fetchMembers()
        void fetchSharedKBs()
      }
    } else {
      unlockBackgroundScroll()
    }
  }, { immediate: true })

  watch(() => props.orgId, (newOrgId, oldOrgId) => {
    if (props.visible) {
      void scrollContentToTop()
      if (props.mode !== 'create' && newOrgId && newOrgId !== oldOrgId) {
        orgStore.clearCurrentOrganizationContext()
        sharedKnowledgeBases.value = []
        sharedAgents.value = []
        joinRequests.value = []
        inviteCode.value = ''
        inviteCodeExpiresAt.value = null
        approvePopupRequestId.value = null
        void fetchOrgDetail()
        void fetchMembers()
        void fetchSharedKBs()
        if (currentSection.value === 'joinRequests') {
          void fetchJoinRequests()
        }
      }
    }
  })

  watch(currentSection, (section) => {
    void scrollContentToTop()
    if (section === 'joinRequests' && props.orgId) {
      void fetchJoinRequests()
    }
  })

  watch([() => props.visible, () => props.orgId], () => {
    orgContextLoadGen += 1
    addMemberPopupVisible.value = false
    resetAddMemberDialog()
  }, { flush: 'sync' })

  onBeforeUnmount(() => {
    unlockBackgroundScroll()
    orgContextLoadGen += 1
    resetAddMemberDialog()
  })

  watch(addMemberPopupVisible, (visible) => {
    if (!visible) {
      resetAddMemberDialog()
    }
  }, { flush: 'sync' })

  return {
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
    inviteCodeExpiresAt,
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
    nextTick,
  }
}
