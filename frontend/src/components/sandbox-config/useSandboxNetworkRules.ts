import { computed, ref } from 'vue'
import type { SandboxConfig, SandboxNetworkPolicy } from '@/api/system'

export const secretPlaceholder = '***'
export const isMaskedSecret = (value?: string): boolean => value === secretPlaceholder

export interface EnvVarRow {
  key: string
  value: string
  stored?: boolean
  originalKey?: string
}

export interface CubeRuleInjectForm {
  header: string
  secret: string
  format: string
  stored?: boolean
  originalRuleName?: string
  originalHeader?: string
}

export interface CubeRuleForm {
  key: string
  name: string
  scheme?: string
  sni?: string
  host?: string
  methodsText: string
  path?: string
  deny: boolean
  audit?: string
  expanded: boolean
  inject: CubeRuleInjectForm[]
}

export interface E2BRuleHeaderForm {
  name: string
  value: string
  stored?: boolean
  originalHost?: string
  originalName?: string
}

export interface E2BRuleForm {
  host: string
  expanded: boolean
  headers: E2BRuleHeaderForm[]
}

export function isStoredEnvRowRecoverable(row: { key: string; stored?: boolean; originalKey?: string }): boolean {
  return Boolean(row.stored && row.originalKey && row.originalKey === row.key.trim())
}

export function isStoredNetworkSecretRecoverable(
  row: { stored?: boolean },
  originalParentIdentity: string | undefined,
  originalChildIdentity: string | undefined,
  currentParentIdentity: string,
  currentChildIdentity: string,
): boolean {
  return row.stored === true
    && originalParentIdentity === currentParentIdentity.trim()
    && originalChildIdentity === currentChildIdentity.trim()
}

// Mirrors types.DenyOutCoversAllIPv4: net.ParseCIDR collapses any IPv4 /0
// onto 0.0.0.0/0, so 1.2.3.4/0 is a real deny-all, not a false warning.
export function denyOutRowCoversAllIPv4(row: string): boolean {
  const value = row.trim()
  if (value === '0.0.0.0/0') return true
  return /^\d{1,3}(?:\.\d{1,3}){3}\/0$/.test(value)
}

export function useSandboxNetworkRules(backend: { value: string }) {
  const envRows = ref<EnvVarRow[]>([])
  const denyEgressByDefault = ref(false)
  const allowOutRows = ref<string[]>([])
  const denyOutRows = ref<string[]>([])
  const cubeRules = ref<CubeRuleForm[]>([])
  const e2bHostRules = ref<E2BRuleForm[]>([])
  let cubeRuleKeySeq = 0

  function newCubeRuleKey(): string {
    cubeRuleKeySeq += 1
    return `cube-rule-${cubeRuleKeySeq}`
  }

  function addAllowOutRow() {
    allowOutRows.value.push('')
  }

  function setAllowOutRow(index: number, value: string) {
    allowOutRows.value[index] = value
  }

  function removeAllowOutRow(index: number) {
    allowOutRows.value.splice(index, 1)
  }

  function addDenyOutRow() {
    denyOutRows.value.push('')
  }

  function setDenyOutRow(index: number, value: string) {
    denyOutRows.value[index] = value
  }

  function removeDenyOutRow(index: number) {
    denyOutRows.value.splice(index, 1)
  }

  function addCubeRule() {
    for (const rule of cubeRules.value) rule.expanded = false
    cubeRules.value.push({
      key: newCubeRuleKey(),
      name: '', scheme: 'https', sni: '', host: '',
      methodsText: '', path: '', deny: false, audit: '', expanded: true, inject: [],
    })
  }

  function moveCubeRule(index: number, delta: number) {
    const next = index + delta
    if (next < 0 || next >= cubeRules.value.length) return
    const [row] = cubeRules.value.splice(index, 1)
    cubeRules.value.splice(next, 0, row)
  }

  function removeCubeRule(index: number) {
    cubeRules.value.splice(index, 1)
  }

  function addE2BHostRule() {
    for (const rule of e2bHostRules.value) rule.expanded = false
    e2bHostRules.value.push({ host: '', expanded: true, headers: [] })
  }

  function removeE2BHostRule(index: number) {
    e2bHostRules.value.splice(index, 1)
  }

  const domainAllowNeedsDenyAll = computed(() => {
    if (denyEgressByDefault.value) return false
    if (denyOutRows.value.some((row) => denyOutRowCoversAllIPv4(row))) return false
    return allowOutRows.value.some((row) => {
      const value = row.trim()
      if (!value) return false
      return !/^[0-9./]+$/.test(value)
    })
  })

  function resetNetworkAndEnv(cfg: SandboxConfig) {
    envRows.value = Object.entries(cfg.env_vars || {}).map(([key, value]) => (
      isMaskedSecret(value) ? { key, value: '', stored: true, originalKey: key.trim() } : { key, value }
    ))
    const net = cfg.network || {}
    denyEgressByDefault.value = net.deny_egress_by_default === true
    allowOutRows.value = [...(net.allow_out || [])]
    denyOutRows.value = [...(net.deny_out || [])]
    cubeRules.value = (net.cube_rules || []).map((rule) => ({
      key: newCubeRuleKey(),
      name: rule.name || '',
      scheme: rule.scheme || '',
      sni: rule.sni || '',
      host: rule.host || '',
      methodsText: (rule.methods || []).join(', '),
      path: rule.path || '',
      deny: rule.deny === true,
      audit: rule.audit || '',
      expanded: false,
      inject: (rule.inject || []).map((inject) => ({
        header: inject.header || '',
        secret: isMaskedSecret(inject.secret) ? '' : (inject.secret || ''),
        format: inject.format || '',
        stored: isMaskedSecret(inject.secret),
        originalRuleName: rule.name?.trim() || '',
        originalHeader: inject.header?.trim() || '',
      })),
    }))
    e2bHostRules.value = (net.e2b_host_rules || []).map((rule) => ({
      host: rule.host || '',
      expanded: false,
      headers: Object.entries(rule.headers || {}).map(([headerName, value]) => ({
        name: headerName,
        value: isMaskedSecret(value) ? '' : value,
        stored: isMaskedSecret(value),
        originalHost: rule.host?.trim() || '',
        originalName: headerName.trim(),
      })),
    }))
  }

  function collectEnvVars(): Record<string, string> {
    const envVars: Record<string, string> = {}
    for (const row of envRows.value) {
      const key = row.key.trim()
      if (!key) continue
      envVars[key] = isStoredEnvRowRecoverable(row) && row.value === '' ? secretPlaceholder : row.value
    }
    return envVars
  }

  function collectNetworkPolicy(): SandboxNetworkPolicy {
    const policy: SandboxNetworkPolicy = {}
    if (backend.value === 'docker') {
      return policy
    }
    if (denyEgressByDefault.value) policy.deny_egress_by_default = true

    if (backend.value !== 'docker') {
      const allowOut = allowOutRows.value.map((row) => row.trim()).filter(Boolean)
      const denyOut = denyOutRows.value.map((row) => row.trim()).filter(Boolean)
      if (allowOut.length) policy.allow_out = allowOut
      if (denyOut.length) policy.deny_out = denyOut
    }

    if (backend.value === 'cube' && cubeRules.value.length) {
      policy.cube_rules = cubeRules.value.map((rule) => ({
        name: rule.name.trim(),
        scheme: rule.scheme || undefined,
        sni: rule.sni?.trim() || undefined,
        host: rule.host?.trim() || undefined,
        methods: rule.methodsText
          .split(',')
          .map((method) => method.trim().toUpperCase())
          .filter(Boolean),
        path: rule.path?.trim() || undefined,
        deny: rule.deny || undefined,
        audit: rule.audit || undefined,
        inject: rule.deny
          ? undefined
          : rule.inject
            .filter((inject) => inject.header.trim())
            .map((inject) => ({
              header: inject.header.trim(),
              secret: isStoredNetworkSecretRecoverable(
                inject,
                inject.originalRuleName,
                inject.originalHeader,
                rule.name,
                inject.header,
              )
                && inject.secret === ''
                ? secretPlaceholder
                : inject.secret,
              format: inject.format?.trim() || undefined,
            })),
      }))
    }
    if (backend.value === 'e2b' && e2bHostRules.value.length) {
      policy.e2b_host_rules = e2bHostRules.value.map((rule) => {
        const headers: Record<string, string> = {}
        for (const header of rule.headers) {
          const headerName = header.name.trim()
          if (!headerName) continue
          headers[headerName] = isStoredNetworkSecretRecoverable(
            header,
            header.originalHost,
            header.originalName,
            rule.host,
            header.name,
          )
            && header.value === ''
            ? secretPlaceholder
            : header.value
        }
        return { host: rule.host.trim(), headers }
      })
    }
    return policy
  }

  return {
    envRows,
    denyEgressByDefault,
    allowOutRows,
    denyOutRows,
    cubeRules,
    e2bHostRules,
    domainAllowNeedsDenyAll,
    addAllowOutRow,
    setAllowOutRow,
    removeAllowOutRow,
    addDenyOutRow,
    setDenyOutRow,
    removeDenyOutRow,
    addCubeRule,
    moveCubeRule,
    removeCubeRule,
    addE2BHostRule,
    removeE2BHostRule,
    resetNetworkAndEnv,
    collectEnvVars,
    collectNetworkPolicy,
  }
}
