import { ref, computed, watch, type Ref } from 'vue'
import type { SkillCatalogItem } from '@/api/skill'
import type { SandboxConfigRecord } from '@/api/system'
import { installUpgradable, servedPreviousText, upgradeVersions } from '../../../utils/skillUpgrade'
import { HOST_SKILL_TARGET_ID, hostSkillTargetRecord, hostSkillsOnly } from '../../../utils/skillTarget'
import { toolboxLocation } from '../../../config/toolbox'
import type { AgentEditorFormData } from './useAgentEditorForm'

export type CatalogSkillRow = SkillCatalogItem & {
  installed: boolean
  selectable: boolean
  installStatus: string
  installEnabled: boolean
  upgradable: boolean
  installVersion: string
  servedNote: string
}

export interface CatalogSkillGroup {
  key: 'ready' | 'pending'
  label: string
  skills: CatalogSkillRow[]
}

function getErrorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const msg = (err as { message?: unknown }).message
    if (typeof msg === 'string' && msg) return msg
  }
  return fallback
}

export interface UseAgentSkillsSandboxOptions {
  props: {
    readonly visible: boolean
  }
  formData: Ref<AgentEditorFormData>
  t: (key: string, params?: Record<string, unknown>) => string
  message: {
    success: (content: string) => unknown
    warning: (content: string) => unknown
    error: (content: string) => unknown
  }
  hasAdminRole: () => boolean
  isRemoteSandboxSupported: () => boolean
  isHostSandboxSupported: () => boolean
  getSandboxConfigs: () => SandboxConfigRecord[]
  isNamedSandboxBackend: (sandboxType?: string) => boolean
  ensureSkills: (configId: string, sourceTenantId: undefined, force: boolean) => Promise<unknown>
  ensureSkillCatalog: (force: boolean) => Promise<unknown>
  getSkillCatalog: () => SkillCatalogItem[]
  installSkillCatalog: (
    catalogId: string,
    sandboxIds: string[],
  ) => Promise<{ data?: { errors?: Record<string, string> } } | undefined>
  requestCloseAndNavigate: (location: ReturnType<typeof toolboxLocation>) => void
  setIntervalFn: (handler: () => void, timeoutMs: number) => unknown
  clearIntervalFn: (handle: unknown) => void
}

export function useAgentSkillsSandbox(options: UseAgentSkillsSandboxOptions) {
  const {
    props,
    formData,
    t,
    message,
    hasAdminRole,
    isRemoteSandboxSupported,
    isHostSandboxSupported,
    getSandboxConfigs,
    isNamedSandboxBackend,
    ensureSkills,
    ensureSkillCatalog,
    getSkillCatalog,
    installSkillCatalog,
    requestCloseAndNavigate,
    setIntervalFn,
    clearIntervalFn,
  } = options

  const skillCatalog = ref<SkillCatalogItem[]>([])
  const catalogReady = ref(false)
  const installingCatalogId = ref('')
  const skillsSelectionMode = ref<'all' | 'selected' | 'none'>('none')

  const hostOnly = computed(() =>
    hostSkillsOnly(isRemoteSandboxSupported(), isHostSandboxSupported()),
  )
  const skillTargetId = computed(() =>
    hostOnly.value ? HOST_SKILL_TARGET_ID : (formData.value.config.sandbox_config_id || ''),
  )
  const hasSandboxSelected = computed(() => Boolean(skillTargetId.value))

  function namedSandboxConfigs(): SandboxConfigRecord[] {
    return getSandboxConfigs().filter((cfg) => isNamedSandboxBackend(cfg.sandbox_type))
  }

  const canEnableSkills = computed(
    () => hostOnly.value || hasSandboxSelected.value || namedSandboxConfigs().length === 1,
  )
  const canInstallSkills = computed(() => hasAdminRole())

  const catalogSkillRows = computed<CatalogSkillRow[]>(() => {
    const sandboxId = skillTargetId.value
    return skillCatalog.value.map((item) => {
      const inst = sandboxId
        ? (item.installations || []).find((row) => row.sandbox_config_id === sandboxId)
        : undefined
      const installStatus = inst?.status || ''
      const installEnabled = Boolean(inst?.enabled)
      const installed = Boolean(inst) && installStatus !== 'removed'
      const servedNote = inst ? servedPreviousText(t, inst) : ''
      const selectable = installEnabled && (installStatus === 'ready' || Boolean(servedNote))
      const upgradable = Boolean(inst && installUpgradable(item, inst))
      return {
        ...item,
        installed,
        selectable,
        installStatus,
        installEnabled,
        upgradable,
        installVersion: inst?.version || '',
        servedNote,
      }
    })
  })

  const showCatalogSkillList = computed(
    () =>
      skillsSelectionMode.value !== 'none' &&
      hasSandboxSelected.value &&
      catalogSkillRows.value.length > 0,
  )

  const skillsSelectionHint = computed(() => {
    if (hostOnly.value) {
      if (skillsSelectionMode.value === 'all') return t('agent.editor.hostSkillsAllListHint')
      if (skillsSelectionMode.value === 'selected') return t('agent.editor.hostSelectSkillsDesc')
      return t('agent.editor.hostSkillsSelectionDesc')
    }
    if (skillsSelectionMode.value === 'all') return t('agent.editor.skillsAllListHint')
    if (skillsSelectionMode.value === 'selected') return t('agent.editor.selectSkillsDesc')
    return t('agent.editor.skillsSelectionDesc')
  })

  const catalogSkillGroups = computed<CatalogSkillGroup[]>(() => {
    const ready = catalogSkillRows.value.filter((skill) => skill.selectable)
    const pending = catalogSkillRows.value.filter((skill) => !skill.selectable)
    const groups: CatalogSkillGroup[] = []
    if (ready.length) {
      groups.push({
        key: 'ready',
        label: t('agent.editor.skillsGroupAvailable'),
        skills: ready,
      })
    }
    if (pending.length) {
      groups.push({
        key: 'pending',
        label: t('agent.editor.skillsGroupUnavailable'),
        skills: pending,
      })
    }
    return groups
  })

  function skillStatusHint(skill: CatalogSkillRow): string {
    if (!skill.installed) return t('agent.editor.skillNotInstalled')
    if (skill.installStatus === 'installing') return t('settings.sandbox.skillStatusInstalling')
    if (skill.installStatus === 'failed') return t('settings.sandbox.skillStatusFailed')
    if (skill.installStatus === 'removing') return t('settings.sandbox.skillStatusRemoving')
    if (skill.installStatus === 'ready' && !skill.installEnabled) {
      return hostOnly.value ? t('agent.editor.hostSkillDisabled') : t('agent.editor.skillDisabledOnSandbox')
    }
    return t('agent.editor.skillNotReady')
  }

  function skillStatusIcon(skill: CatalogSkillRow): string {
    if (!skill.installed || skill.installStatus === 'failed') return 'download'
    if (skill.installStatus === 'installing' || skill.installStatus === 'removing') return 'refresh'
    if (skill.installStatus === 'ready' && !skill.installEnabled) return 'close-circle'
    return 'time'
  }

  function isSkillBusy(skill: CatalogSkillRow): boolean {
    return skill.installStatus === 'installing' || skill.installStatus === 'removing'
  }

  function canInstallSkillRow(skill: CatalogSkillRow): boolean {
    if (!canInstallSkills.value || !hasSandboxSelected.value) return false
    return !skill.installed || skill.installStatus === 'failed'
  }

  function canUpgradeSkillRow(skill: CatalogSkillRow): boolean {
    return canInstallSkills.value && hasSandboxSelected.value && skill.upgradable
  }

  function installsAnUpgrade(skill: CatalogSkillRow): boolean {
    return skill.upgradable || Boolean(skill.servedNote)
  }

  function skillUpgradeHint(skill: CatalogSkillRow): string {
    const versions = upgradeVersions(skill, { version: skill.installVersion })
    return versions ? t('settings.skills.upgradeFromTo', versions) : t('settings.skills.upgradeAvailable')
  }

  function autoBindSoleSandbox() {
    if (hostOnly.value) return
    if (skillsSelectionMode.value === 'none') return
    if (formData.value.config.sandbox_config_id) return
    const configs = namedSandboxConfigs()
    if (configs.length === 1) {
      formData.value.config.sandbox_config_id = configs[0].id
    }
  }

  function openSkillSettings() {
    const configId = skillTargetId.value
    requestCloseAndNavigate(toolboxLocation('skills', configId || undefined))
  }

  const showSkillProgress = ref(false)
  const skillProgressRecord = ref<SandboxConfigRecord | null>(null)
  const skillProgressId = ref('')
  const skillProgressTitle = ref('')
  const skillProgressDesc = computed(() => {
    const record = skillProgressRecord.value
    if (!record) return ''
    return t('settings.skills.manageDrawerDesc', { name: record.name })
  })

  function sandboxRecordById(configId: string): SandboxConfigRecord | undefined {
    if (hostOnly.value && configId === HOST_SKILL_TARGET_ID) {
      return hostSkillTargetRecord(t('settings.skills.hostTarget'))
    }
    return getSandboxConfigs().find((cfg) => cfg.id === configId)
  }

  function installOnCurrentSandbox(skill: CatalogSkillRow, configId: string) {
    return (skill.installations || []).find((row) => row.sandbox_config_id === configId)
  }

  function pruneSelectedSkills() {
    if (!catalogReady.value) return
    const names = new Set(
      catalogSkillRows.value
        .filter((skill) => skill.selectable || (skill.installed && isSkillBusy(skill)))
        .map((skill) => skill.name),
    )
    const selected: string[] = formData.value.config.selected_skills || []
    const kept = selected.filter((name) => names.has(name))
    if (kept.length !== selected.length) {
      formData.value.config.selected_skills = kept
    }
  }

  async function syncInstalledSkills(force = false) {
    autoBindSoleSandbox()
    const configId = skillTargetId.value
    await ensureSkills(configId, undefined, force)
    try {
      await ensureSkillCatalog(force)
      skillCatalog.value = [...getSkillCatalog()]
      catalogReady.value = true
    } catch {
      catalogReady.value = false
    }
    pruneSelectedSkills()
  }

  async function openSkillInstallProgress(skill: CatalogSkillRow) {
    const configId = skillTargetId.value
    const record = sandboxRecordById(configId)
    if (!record) {
      openSkillSettings()
      return
    }
    let inst = installOnCurrentSandbox(skill, configId)
    if (!inst?.skill_id) {
      await syncInstalledSkills(true)
      const latest = catalogSkillRows.value.find((row) => row.id === skill.id)
      inst = latest ? installOnCurrentSandbox(latest, configId) : undefined
    }
    if (!inst?.skill_id) {
      openSkillSettings()
      return
    }
    skillProgressRecord.value = record
    skillProgressId.value = inst.skill_id
    skillProgressTitle.value = skill.name
    showSkillProgress.value = true
  }

  function onSkillProgressUpdated() {
    void syncInstalledSkills(true)
  }

  function onSkillProgressChanged() {
    void syncInstalledSkills(true)
  }

  async function installCatalogToCurrent(skill: CatalogSkillRow) {
    const configId = skillTargetId.value
    if (!configId || installingCatalogId.value) return
    installingCatalogId.value = skill.id
    const upgrading = installsAnUpgrade(skill)
    try {
      const res = await installSkillCatalog(skill.id, [configId])
      const failed = Object.keys(res?.data?.errors || {}).length
      if (failed > 0) {
        message.warning(t('settings.skills.installPartial', { failed }))
      } else {
        message.success(
          t(upgrading ? 'settings.skills.upgradeAccepted' : 'settings.skills.installAccepted'),
        )
      }
      await syncInstalledSkills(true)
    } catch (err: unknown) {
      message.error(getErrorMessage(err, t('settings.sandbox.skillUploadFailed')))
    } finally {
      installingCatalogId.value = ''
    }
  }

  const sandboxConfigOptions = computed<SandboxConfigRecord[]>(() => {
    const configs = getSandboxConfigs().filter((cfg) => isNamedSandboxBackend(cfg.sandbox_type))
    const selected = formData.value.config.sandbox_config_id
    if (!selected || configs.some((cfg) => cfg.id === selected)) return configs
    return [
      ...configs,
      { id: selected, name: t('agent.editor.sandboxBackendMissing'), sandbox_type: '' } as SandboxConfigRecord,
    ]
  })

  const backendLabel = (type: string) =>
    type ? t(`settings.sandbox.backends.${type}`) : t('common.error')

  function sandboxTargetLine(cfg: SandboxConfigRecord): string {
    if (cfg.sandbox_type === 'docker') {
      return cfg.config?.docker?.image?.trim() || ''
    }
    const remote = cfg.config?.e2b || cfg.config?.cube
    const raw = remote?.api_url?.trim() || ''
    if (!raw) return ''
    try {
      return new URL(raw).host
    } catch {
      return raw
    }
  }

  const selectedSandboxSummary = computed(() => {
    const id = formData.value.config.sandbox_config_id
    const cfg = sandboxConfigOptions.value.find((item) => item.id === id)
    if (!cfg?.sandbox_type) return ''
    const parts = [backendLabel(cfg.sandbox_type), sandboxTargetLine(cfg)]
    const desc = cfg.description?.trim()
    if (desc) parts.push(desc)
    return parts.filter(Boolean).join(' · ')
  })

  const initSkillsSelectionMode = () => {
    if (formData.value.config.skills_selection_mode) {
      skillsSelectionMode.value = formData.value.config.skills_selection_mode
    } else if (formData.value.config.selected_skills?.length > 0) {
      skillsSelectionMode.value = 'selected'
    } else {
      skillsSelectionMode.value = 'none'
    }
    autoBindSoleSandbox()
  }

  let catalogPollTimer: unknown = null

  function stopCatalogPoll() {
    if (catalogPollTimer != null) {
      clearIntervalFn(catalogPollTimer)
      catalogPollTimer = null
    }
  }

  watch(
    [() => props.visible, catalogSkillRows],
    () => {
      const busy = catalogSkillRows.value.some(
        (skill) => skill.installStatus === 'installing' || skill.installStatus === 'removing',
      )
      if (!props.visible || !busy) {
        stopCatalogPoll()
        return
      }
      if (catalogPollTimer != null) return
      catalogPollTimer = setIntervalFn(() => {
        void syncInstalledSkills(true)
      }, 2500)
    },
    { flush: 'post' },
  )

  watch(skillsSelectionMode, (mode) => {
    formData.value.config.skills_selection_mode = mode
    if (mode === 'none') {
      formData.value.config.selected_skills = []
    } else if (mode === 'all') {
      formData.value.config.selected_skills = []
      autoBindSoleSandbox()
    } else {
      autoBindSoleSandbox()
    }
  })

  const resetSkillProgressDrawer = () => {
    showSkillProgress.value = false
    skillProgressRecord.value = null
    skillProgressId.value = ''
  }

  return {
    skillCatalog,
    installingCatalogId,
    skillsSelectionMode,
    hostOnly,
    hasSandboxSelected,
    canEnableSkills,
    canInstallSkills,
    catalogSkillRows,
    showCatalogSkillList,
    skillsSelectionHint,
    catalogSkillGroups,
    skillStatusHint,
    skillStatusIcon,
    isSkillBusy,
    canInstallSkillRow,
    canUpgradeSkillRow,
    installsAnUpgrade,
    skillUpgradeHint,
    autoBindSoleSandbox,
    openSkillSettings,
    showSkillProgress,
    skillProgressRecord,
    skillProgressId,
    skillProgressTitle,
    skillProgressDesc,
    openSkillInstallProgress,
    onSkillProgressUpdated,
    onSkillProgressChanged,
    pruneSelectedSkills,
    syncInstalledSkills,
    installCatalogToCurrent,
    sandboxConfigOptions,
    backendLabel,
    sandboxTargetLine,
    selectedSandboxSummary,
    initSkillsSelectionMode,
    stopCatalogPoll,
    resetSkillProgressDrawer,
  }
}
