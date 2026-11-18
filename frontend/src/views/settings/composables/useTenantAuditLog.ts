import { ref, watch, type ComputedRef, type Ref } from 'vue'
import type {
  AuditAction,
  AuditLog,
  AuditOutcome,
  ListAuditLogParams,
  ListAuditLogResponse,
} from '@/api/tenant/audit-log'
import { extractErrorMessage, extractErrorStatus } from './useTenantMembersList'

const AUDIT_PAGE_SIZE = 50

export function auditActionTheme(
  action: AuditAction,
): 'success' | 'warning' | 'danger' | 'primary' | 'default' {
  switch (action) {
    case 'rbac.access_denied':
      return 'danger'
    case 'rbac.member_added':
      return 'success'
    case 'rbac.member_removed':
    case 'rbac.member_left':
    case 'rbac.member_role_changed':
      return 'warning'
    default:
      return 'default'
  }
}

export function auditOutcomeTheme(outcome: AuditOutcome): 'success' | 'danger' | 'default' {
  if (outcome === 'denied') return 'danger'
  if (outcome === 'success') return 'success'
  return 'default'
}

export function formatAuditDatePart(s: string | undefined, currentLocale: string): string {
  if (!s) return '-'
  try {
    return new Intl.DateTimeFormat(currentLocale || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(s))
  } catch {
    return s
  }
}

export function formatAuditTimePart(s: string | undefined, currentLocale: string): string {
  if (!s) return ''
  try {
    return new Intl.DateTimeFormat(currentLocale || 'zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date(s))
  } catch {
    return ''
  }
}

export function auditDetailsObject(row: AuditLog): Record<string, unknown> | null {
  if (row.details && typeof row.details === 'object') {
    return row.details as Record<string, unknown>
  }
  return null
}

export function auditDetailsJSON(row: AuditLog): string {
  if (row.details === null || row.details === undefined) return '{}'
  if (typeof row.details === 'string') return row.details
  try {
    return JSON.stringify(row.details, null, 2)
  } catch {
    return String(row.details)
  }
}

export interface UseTenantAuditLogDeps {
  activeTenantId: ComputedRef<number>
  canViewAudit: ComputedRef<boolean>
  locale: Ref<string>
  t: (key: string, params?: Record<string, unknown>) => string
  formatAction: (action: AuditAction) => string
  actorDisplayName: (userId: string) => string
  listAuditLog?: (tenantId: number, params?: ListAuditLogParams) => Promise<ListAuditLogResponse>
}

export function useTenantAuditLog(deps: UseTenantAuditLogDeps) {
  const auditDrawerVisible = ref(false)
  const auditEntries = ref<AuditLog[]>([])
  const auditLoading = ref(false)
  const auditError = ref('')
  const auditCursor = ref<number>(0)
  const auditHasMore = ref(true)
  const auditLoadedOnce = ref(false)
  const auditExpandedRowKeys = ref<number[]>([])

  function onAuditExpandChange(value: (string | number)[]) {
    auditExpandedRowKeys.value = value
      .map((v) => (typeof v === 'number' ? v : Number(v)))
      .filter((v) => Number.isFinite(v))
  }

  function auditTargetSubject(row: AuditLog): string {
    if (row.target_user_id) return deps.actorDisplayName(row.target_user_id)
    if (row.target_id) {
      return row.target_type ? `${row.target_type}:${row.target_id}` : row.target_id
    }
    return ''
  }

  function auditTargetDiff(row: AuditLog): string {
    const d = auditDetailsObject(row)
    if (!d) return ''
    if (row.action === 'rbac.member_role_changed') {
      if (d.old_role && d.new_role) return `${d.old_role} → ${d.new_role}`
    }
    if (row.action === 'rbac.access_denied') {
      if (typeof d.required_role === 'string') {
        return deps.t('tenantMember.audit.requiredRole', { role: d.required_role })
      }
    }
    if (row.action === 'rbac.invitation_sent' || row.action === 'rbac.invitation_revoked') {
      if (typeof d.role === 'string') return String(d.role)
    }
    return ''
  }

  async function loadAuditLog(reset: boolean) {
    if (!deps.listAuditLog || !deps.activeTenantId.value || !deps.canViewAudit.value) return
    if (auditLoading.value) return
    if (!reset && !auditHasMore.value) return

    auditLoading.value = true
    auditError.value = ''
    try {
      const resp = await deps.listAuditLog(deps.activeTenantId.value, {
        after_id: reset ? undefined : auditCursor.value || undefined,
        limit: AUDIT_PAGE_SIZE,
      })
      if (resp.success) {
        const rows = resp.data || []
        auditEntries.value = reset ? rows : [...auditEntries.value, ...rows]
        auditCursor.value = resp.next_cursor || 0
        auditHasMore.value = !!resp.next_cursor && rows.length > 0
        auditLoadedOnce.value = true
      } else {
        auditError.value = resp.message || deps.t('tenantMember.errors.generic')
      }
    } catch (err: unknown) {
      const status = extractErrorStatus(err)
      if (status === 403) {
        auditError.value = deps.t('tenantMember.audit.forbidden')
      } else {
        auditError.value = extractErrorMessage(err) || deps.t('tenantMember.errors.generic')
      }
    } finally {
      auditLoading.value = false
    }
  }

  function reloadAuditLog() {
    auditCursor.value = 0
    auditHasMore.value = true
    void loadAuditLog(true)
  }

  function openAuditDrawer() {
    auditDrawerVisible.value = true
    if (!auditLoadedOnce.value) {
      void loadAuditLog(true)
    }
  }

  watch(deps.activeTenantId, () => {
    auditDrawerVisible.value = false
    auditEntries.value = []
    auditCursor.value = 0
    auditHasMore.value = true
    auditLoadedOnce.value = false
    auditError.value = ''
    auditExpandedRowKeys.value = []
  })

  return {
    auditDrawerVisible,
    auditEntries,
    auditLoading,
    auditError,
    auditCursor,
    auditHasMore,
    auditLoadedOnce,
    auditExpandedRowKeys,
    loadAuditLog,
    reloadAuditLog,
    openAuditDrawer,
    onAuditExpandChange,
    auditTargetSubject,
    auditTargetDiff,
    formatAuditAction: deps.formatAction,
  }
}
