import { onBeforeUnmount, reactive, ref, watch, type ComputedRef } from 'vue'
import type {
  ListMembersParams,
  ListMembersResponse,
  TenantMember,
  TenantRole,
} from '@/api/tenant/members'

export const MEMBER_SEARCH_DEBOUNCE_MS = 320
export const DEFAULT_MEMBERS_PAGE_SIZE = 20
export const MEMBERS_PAGE_SIZE_OPTIONS = [10, 20, 50, 100]
export const INVITATIONS_PAGE_SIZE_OPTIONS = [10, 20, 50, 100]
export const INVITATION_TTL_DAYS = 7

export const roleSelectPopupProps = {
  zIndex: 6200,
  overlayClassName: 'tenant-members-role-select-popup',
}

export type RolePerm = { key: string; has: boolean }

export const roleMatrixOrder: TenantRole[] = ['owner', 'admin', 'contributor', 'viewer']

export const roleMatrix: Record<TenantRole, RolePerm[]> = {
  owner: [
    { key: 'manageMembers', has: true },
    { key: 'manageTenantConfig', has: true },
    { key: 'manageInfra', has: true },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  admin: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: true },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  contributor: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: false },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  viewer: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: false },
    { key: 'createOwnKB', has: false },
    { key: 'readAll', has: true },
  ],
}

export function roleMatrixIcon(role: TenantRole): string {
  switch (role) {
    case 'owner':
      return 'user-vip-filled'
    case 'admin':
      return 'user-safety'
    case 'contributor':
      return 'edit'
    default:
      return 'browse'
  }
}

export function roleIcon(role: TenantRole | string): string {
  if (role === 'owner' || role === 'admin' || role === 'contributor' || role === 'viewer') {
    return roleMatrixIcon(role)
  }
  return 'user'
}

export function roleTagTheme(role: TenantRole): 'primary' | 'warning' | 'success' | 'default' {
  switch (role) {
    case 'owner':
      return 'primary'
    case 'admin':
      return 'warning'
    case 'contributor':
      return 'success'
    default:
      return 'default'
  }
}

export function memberPrimary(row: { username?: string; email?: string }): string {
  return row.username?.trim() || row.email?.trim() || '—'
}

export function memberSecondary(row: { username?: string; email?: string }): string {
  const name = row.username?.trim()
  const mail = row.email?.trim()
  if (name && mail) return mail
  return ''
}

export function formatTenantDate(s: string | undefined, currentLocale: string): string {
  if (!s) return '-'
  try {
    const d = new Date(s)
    return new Intl.DateTimeFormat(currentLocale || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(d)
  } catch {
    return s
  }
}

export function extractErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  if (typeof err === 'object' && err !== null && 'message' in err) {
    const msg = (err as { message?: unknown }).message
    if (typeof msg === 'string') return msg
  }
  return ''
}

export function extractErrorStatus(err: unknown): number | undefined {
  if (typeof err === 'object' && err !== null && 'status' in err) {
    const st = (err as { status?: unknown }).status
    if (typeof st === 'number') return st
  }
  return undefined
}

export interface AuthMembershipLike {
  tenant_id?: number | string
  role?: string
  [key: string]: unknown
}

export interface UseTenantMembersListDeps {
  activeTenantId: ComputedRef<number>
  currentUserId: ComputedRef<string>
  authStore: {
    memberships?: AuthMembershipLike[]
    setMemberships?: (list: AuthMembershipLike[]) => void
  }
  t: (key: string, params?: Record<string, unknown>) => string
  onSuccess: (message: string) => void
  onError: (message: string) => void
  listMembers: (tenantId: number, params?: ListMembersParams) => Promise<ListMembersResponse>
  updateMemberRole?: (
    tenantId: number,
    userId: string,
    role: TenantRole,
  ) => Promise<{ success: boolean; message?: string }>
  removeMember?: (
    tenantId: number,
    userId: string,
  ) => Promise<{ success: boolean; message?: string }>
}

export function useTenantMembersList(deps: UseTenantMembersListDeps) {
  const members = ref<TenantMember[]>([])
  const loading = ref(false)
  const error = ref('')
  const searchQuery = ref('')
  const memberSearchQ = ref('')
  const membersTotal = ref(0)
  const membersPage = ref(1)
  const membersPageSize = ref(DEFAULT_MEMBERS_PAGE_SIZE)
  const memberDisplayByUserId = reactive<Record<string, { username?: string; email?: string }>>({})

  let memberSearchDebounceTimer: ReturnType<typeof setTimeout> | undefined
  let membersRequestSeq = 0
  let membersDisposed = false

  function rememberMembersForAudit(rows: TenantMember[]) {
    for (const m of rows) {
      memberDisplayByUserId[m.user_id] = { username: m.username, email: m.email }
    }
  }

  function actorDisplayName(userId: string): string {
    const cur = members.value.find((x) => x.user_id === userId)
    if (cur?.username?.trim()) return cur.username.trim()
    if (cur?.email?.trim()) return cur.email.trim()
    const memo = memberDisplayByUserId[userId]
    if (memo?.username?.trim()) return memo.username.trim()
    if (memo?.email?.trim()) return memo.email.trim()
    return userId
  }

  function cancelMemberSearchDebounce() {
    if (memberSearchDebounceTimer !== undefined) {
      clearTimeout(memberSearchDebounceTimer)
      memberSearchDebounceTimer = undefined
    }
  }

  function invalidateMembersRequest() {
    membersRequestSeq += 1
    cancelMemberSearchDebounce()
    members.value = []
    membersTotal.value = 0
    error.value = ''
    loading.value = false
  }

  async function loadMembers() {
    const tenantId = deps.activeTenantId.value
    if (membersDisposed || !tenantId) return

    const rawQuery = searchQuery.value
    const query = rawQuery.trim()
    if (memberSearchDebounceTimer !== undefined || memberSearchQ.value !== query) {
      membersPage.value = 1
    }
    cancelMemberSearchDebounce()
    memberSearchQ.value = query
    const requestSeq = ++membersRequestSeq
    const page = membersPage.value
    const pageSize = membersPageSize.value
    const ownsRequest = () =>
      !membersDisposed &&
      requestSeq === membersRequestSeq &&
      tenantId === deps.activeTenantId.value &&
      rawQuery === searchQuery.value &&
      query === memberSearchQ.value
    const matchesRequest = () =>
      ownsRequest() && page === membersPage.value && pageSize === membersPageSize.value
    let responseAccepted = false

    loading.value = true
    error.value = ''
    try {
      const resp = await deps.listMembers(tenantId, {
        page,
        page_size: pageSize,
        q: query || undefined,
      })
      if (!matchesRequest()) return
      responseAccepted = true
      if (resp.success && resp.data) {
        const total = resp.data.total ?? 0
        const ps = resp.data.page_size ?? pageSize
        const safePs = Math.max(1, ps)
        const maxPage = Math.max(1, Math.ceil(total / safePs))
        if (page > maxPage) {
          membersPage.value = maxPage
          await loadMembers()
          return
        }
        members.value = resp.data.members ?? []
        membersTotal.value = total
        if (typeof resp.data.page === 'number' && resp.data.page > 0) {
          membersPage.value = resp.data.page
        }
        if (typeof resp.data.page_size === 'number' && resp.data.page_size > 0) {
          membersPageSize.value = resp.data.page_size
        }
        rememberMembersForAudit(members.value)
      } else {
        error.value = resp.message || deps.t('tenantMember.errors.generic')
      }
    } catch (err: unknown) {
      if (!matchesRequest()) return
      responseAccepted = true
      error.value = extractErrorMessage(err) || deps.t('tenantMember.errors.generic')
    } finally {
      if (ownsRequest() && (responseAccepted || matchesRequest())) {
        loading.value = false
      }
    }
  }

  function onMembersPageChange() {
    void loadMembers()
  }

  async function onRoleChange(row: TenantMember, newRole: string) {
    if (!deps.updateMemberRole) return
    const prev = row.role
    const next = newRole as TenantRole
    if (prev === next) return

    try {
      const resp = await deps.updateMemberRole(deps.activeTenantId.value, row.user_id, next)
      if (resp.success) {
        const idx = members.value.findIndex((m) => m.user_id === row.user_id)
        if (idx >= 0) {
          const merged = { ...members.value[idx], role: next }
          members.value.splice(idx, 1, merged)
          rememberMembersForAudit([merged])
        } else {
          row.role = next
        }
        if (
          row.user_id === deps.currentUserId.value &&
          deps.authStore.memberships?.length &&
          deps.authStore.setMemberships
        ) {
          const tid = deps.activeTenantId.value
          deps.authStore.setMemberships(
            deps.authStore.memberships.map((m) =>
              Number(m.tenant_id) === tid ? { ...m, role: next } : m,
            ),
          )
        }
        deps.onSuccess(deps.t('tenantMember.roleChange.success'))
        return
      }
      deps.onError(resp.message || deps.t('tenantMember.errors.generic'))
    } catch (err: unknown) {
      const status = extractErrorStatus(err)
      if (status === 409) {
        deps.onError(deps.t('tenantMember.errors.lastOwner'))
      } else if (status === 404) {
        deps.onError(deps.t('tenantMember.errors.notFound'))
      } else {
        deps.onError(extractErrorMessage(err) || deps.t('tenantMember.errors.generic'))
      }
    }
  }

  async function removeRow(row: TenantMember) {
    if (!deps.removeMember) return
    try {
      const resp = await deps.removeMember(deps.activeTenantId.value, row.user_id)
      if (resp.success) {
        await loadMembers()
        deps.onSuccess(deps.t('tenantMember.remove.success'))
      } else {
        deps.onError(resp.message || deps.t('tenantMember.errors.generic'))
      }
    } catch (err: unknown) {
      const status = extractErrorStatus(err)
      if (status === 409) {
        deps.onError(deps.t('tenantMember.errors.lastOwner'))
      } else if (status === 404) {
        deps.onError(deps.t('tenantMember.errors.notFound'))
      } else {
        deps.onError(extractErrorMessage(err) || deps.t('tenantMember.errors.generic'))
      }
    }
  }

  watch(
    searchQuery,
    () => {
      invalidateMembersRequest()
      if (membersDisposed || !deps.activeTenantId.value) return
      const scheduledSeq = membersRequestSeq
      memberSearchDebounceTimer = setTimeout(() => {
        if (membersDisposed || scheduledSeq !== membersRequestSeq) return
        memberSearchDebounceTimer = undefined
        memberSearchQ.value = searchQuery.value.trim()
        membersPage.value = 1
        void loadMembers()
      }, MEMBER_SEARCH_DEBOUNCE_MS)
    },
    { flush: 'sync' },
  )

  watch(
    deps.activeTenantId,
    (id) => {
      invalidateMembersRequest()
      searchQuery.value = ''
      memberSearchQ.value = ''
      cancelMemberSearchDebounce()
      membersPage.value = 1
      membersPageSize.value = DEFAULT_MEMBERS_PAGE_SIZE
      for (const userId of Object.keys(memberDisplayByUserId)) {
        delete memberDisplayByUserId[userId]
      }
      if (id) void loadMembers()
    },
    { immediate: true, flush: 'sync' },
  )

  onBeforeUnmount(() => {
    membersDisposed = true
    invalidateMembersRequest()
  })

  return {
    members,
    loading,
    error,
    searchQuery,
    memberSearchQ,
    membersTotal,
    membersPage,
    membersPageSize,
    memberDisplayByUserId,
    loadMembers,
    onMembersPageChange,
    onRoleChange,
    removeRow,
    actorDisplayName,
    rememberMembersForAudit,
  }
}
