import { computed, ref, type ComputedRef } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  querySandboxTemplates,
  type SandboxConfig,
  type SandboxConfigRecord,
  type SandboxCubeConfig,
  type SandboxE2BConfig,
  type SandboxTemplate,
} from '@/api/system'

export interface TemplateFieldRow {
  key: string
  label: string
  value: string
  mono?: boolean
}

export function isTemplateSelectable(item: SandboxTemplate): boolean {
  const status = item.status?.trim().toLowerCase()
  if (!status) return true
  return ['ready', 'available', 'complete', 'completed', 'success', 'succeeded'].includes(status)
}

export function isTemplatePending(item: SandboxTemplate): boolean {
  const status = item.status?.trim().toLowerCase()
  return ['building', 'waiting', 'pending', 'queued', 'processing', 'running'].includes(status || '')
}

export function isTemplateUntagged(item: SandboxTemplate): boolean {
  return item.status?.trim().toLowerCase() === 'untagged'
}

export function isTemplateFailed(item: SandboxTemplate): boolean {
  const status = item.status?.trim().toLowerCase()
  return ['failed', 'failure', 'error', 'cancelled', 'canceled'].includes(status || '')
}

function formatTemplateTime(value?: string): string {
  const raw = value?.trim()
  if (!raw) return ''
  const ms = Date.parse(raw)
  if (Number.isNaN(ms)) return raw
  return new Date(ms).toLocaleString()
}

export function useSandboxTemplatesPoll(options: {
  t: (key: string, params?: Record<string, unknown>) => string
  visible: () => boolean
  backend: { value: string }
  cube: SandboxCubeConfig
  e2b: SandboxE2BConfig
  isRemoteBackend: ComputedRef<boolean>
  hasImageCatalog: ComputedRef<boolean>
  currentStepKey: ComputedRef<string>
  retargetFrozen: ComputedRef<boolean>
  effectiveRecord: ComputedRef<SandboxConfigRecord | null>
  connectionReady: () => boolean
  collectPayload: () => SandboxConfig
  onFieldInput: (field: string) => void
  clearTemplateFieldError: () => void
}) {
  const {
    t,
    visible,
    backend,
    cube,
    e2b,
    isRemoteBackend,
    hasImageCatalog,
    currentStepKey,
    retargetFrozen,
    effectiveRecord,
    connectionReady,
    collectPayload,
    onFieldInput,
    clearTemplateFieldError,
  } = options

  const templates = ref<SandboxTemplate[]>([])
  const templatesLoading = ref(false)
  const templatesLoaded = ref(false)
  const templatesError = ref('')
  let templateReqEpoch = 0
  let templatePollTimer: ReturnType<typeof setTimeout> | undefined

  const currentTemplateId = computed(() => (
    backend.value === 'cube' ? cube.template_id : backend.value === 'e2b' ? e2b.template_id : ''
  )?.trim() || '')
  const selectedTemplate = computed(() => templates.value.find((item) => item.id === currentTemplateId.value))
  const clusterStandardTemplate = computed(() => templates.value.find((item) => item.standard && item.id))
  const clusterDesktopTemplate = computed(() => templates.value.find((item) => item.desktop && item.id))
  const canCreateStandard = computed(() => (
    isRemoteBackend.value && templatesLoaded.value && !clusterStandardTemplate.value && !retargetFrozen.value
  ))
  const canCreateDesktop = computed(() => (
    isRemoteBackend.value && templatesLoaded.value && !clusterDesktopTemplate.value && !retargetFrozen.value
  ))
  const hasPendingTemplates = computed(() => templates.value.some(isTemplatePending))

  function selectTemplate(value: string | number) {
    if (backend.value === 'cube') cube.template_id = String(value)
    else e2b.template_id = String(value)
    onFieldInput('template_id')
  }

  function clearTemplateSelection() {
    if (backend.value === 'cube') cube.template_id = ''
    if (backend.value === 'e2b') e2b.template_id = ''
    clearTemplateFieldError()
  }

  function onTemplateCardClick(item: SandboxTemplate) {
    if (retargetFrozen.value && item.id !== currentTemplateId.value) return
    if (!isTemplateSelectable(item)) return
    selectTemplate(item.id)
  }

  function canRebuildTemplate(item: SandboxTemplate): boolean {
    return Boolean((item.standard || item.desktop) && item.id) && !isTemplatePending(item) && !retargetFrozen.value
  }

  function templateDisplayName(item: SandboxTemplate): string {
    if (item.standard) return t('settings.sandbox.genyeeStandardTemplate')
    const itemName = item.name?.trim() || ''
    const id = item.id?.trim() || ''
    if (!itemName || itemName === id) return t('settings.sandbox.templateUnnamed')
    return itemName
  }

  function templateFieldRows(item: SandboxTemplate): TemplateFieldRow[] {
    const rows: TemplateFieldRow[] = []
    const image = item.image?.trim()
    if (image) {
      rows.push({ key: 'image', label: t('settings.sandbox.templateFieldImage'), value: image, mono: true })
    }
    const version = item.version?.trim()
    if (version) {
      rows.push({ key: 'version', label: t('settings.sandbox.templateFieldVersion'), value: version })
    }
    const id = item.id?.trim()
    if (id) {
      rows.push({ key: 'id', label: t('settings.sandbox.templateFieldId'), value: id, mono: true })
    }
    const created = formatTemplateTime(item.created_at)
    if (created) {
      rows.push({ key: 'created', label: t('settings.sandbox.templateFieldCreated'), value: created })
    }
    const instanceType = item.instance_type?.trim()
    if (instanceType) {
      rows.push({ key: 'instance', label: t('settings.sandbox.templateFieldInstance'), value: instanceType })
    }
    const networkType = item.network_type?.trim()
    if (networkType) {
      rows.push({ key: 'network', label: t('settings.sandbox.templateFieldNetwork'), value: networkType })
    }
    if (item.allow_internet_access === true) {
      rows.push({
        key: 'internet',
        label: t('settings.sandbox.templateFieldInternet'),
        value: t('settings.sandbox.templateInternetOn'),
      })
    } else if (item.allow_internet_access === false) {
      rows.push({
        key: 'internet',
        label: t('settings.sandbox.templateFieldInternet'),
        value: t('settings.sandbox.templateInternetOff'),
      })
    }
    return rows
  }

  function templateFailureReason(item: SandboxTemplate): string {
    if (!isTemplateFailed(item)) return ''
    const reason = item.error?.trim()
    return reason ? t('settings.sandbox.templateFailedReason', { reason }) : ''
  }

  function templateStatusTheme(item: SandboxTemplate): 'success' | 'warning' | 'danger' | 'default' {
    if (isTemplateSelectable(item)) return 'success'
    if (isTemplateUntagged(item)) return 'danger'
    if (isTemplatePending(item)) return 'warning'
    if (isTemplateFailed(item)) return 'danger'
    return 'default'
  }

  function templateStatusLabel(item: SandboxTemplate): string {
    if (isTemplateSelectable(item)) return t('settings.sandbox.templateStatuses.ready')
    if (isTemplateUntagged(item)) return t('settings.sandbox.templateStatuses.untagged')
    if (isTemplatePending(item)) return t('settings.sandbox.templateStatuses.building')
    if (isTemplateFailed(item)) return t('settings.sandbox.templateStatuses.failed')
    return t('settings.sandbox.templateStatuses.unknown')
  }

  function stopTemplatePolling() {
    if (templatePollTimer) clearTimeout(templatePollTimer)
    templatePollTimer = undefined
  }

  function scheduleTemplatePolling() {
    stopTemplatePolling()
    if (!visible() || currentStepKey.value !== 'template' || !hasPendingTemplates.value) return
    templatePollTimer = setTimeout(() => {
      void loadTemplates({ silent: true })
    }, 3000)
  }

  function provisionedTemplateMessage(opts: {
    ensureStandard?: boolean
    ensureDesktop?: boolean
    replaceStandard?: boolean
    replaceDesktop?: boolean
  }): string {
    if (opts.replaceDesktop) return t('settings.sandbox.desktopTemplateReplaced')
    if (opts.ensureDesktop) return t('settings.sandbox.desktopTemplateProvisioning')
    if (opts.replaceStandard) return t('settings.sandbox.standardTemplateReplaced')
    return t('settings.sandbox.standardTemplateProvisioning')
  }

  async function loadTemplates(opts: {
    ensureStandard?: boolean
    ensureDesktop?: boolean
    silent?: boolean
    replaceStandard?: boolean
    replaceDesktop?: boolean
  } = {}): Promise<boolean> {
    if (!hasImageCatalog.value) return true
    if (!connectionReady()) return false
    const epoch = ++templateReqEpoch
    if (!opts.silent) templatesLoading.value = true
    templatesError.value = ''
    try {
      // Cube/E2B: listing ensures the published CLI image when it is missing.
      // Desktop is opt-in: XFCE images are much heavier, so they are created
      // only when the admin clicks the desktop offer row.
      const ensureFirstParty = isRemoteBackend.value
      const res = await querySandboxTemplates({
        config: collectPayload(),
        config_id: effectiveRecord.value?.id,
        ensure_standard: opts.ensureStandard || (ensureFirstParty && !opts.replaceStandard && !opts.replaceDesktop && !opts.ensureDesktop),
        ensure_desktop: Boolean(opts.ensureDesktop),
        replace_standard: opts.replaceStandard,
        replace_desktop: opts.replaceDesktop,
      })
      if (epoch !== templateReqEpoch || !visible()) return false
      templates.value = res.data?.templates || []
      templatesLoaded.value = true
      const standardID = res.data?.standard_template_id
      const desktopID = res.data?.desktop_template_id
      const current = templates.value.find((item) => item.id === currentTemplateId.value)
      if (opts.replaceDesktop && desktopID) {
        selectTemplate(desktopID)
      } else if (opts.replaceStandard && standardID) {
        selectTemplate(standardID)
      } else if (opts.ensureDesktop && desktopID) {
        const next = templates.value.find((item) => item.id === desktopID)
        if (next && (isTemplateSelectable(next) || isTemplatePending(next))) {
          selectTemplate(desktopID)
        }
      } else if (opts.ensureStandard && standardID) {
        const next = templates.value.find((item) => item.id === standardID)
        if (next && (isTemplateSelectable(next) || isTemplatePending(next))) {
          selectTemplate(standardID)
        }
      } else if (
        currentTemplateId.value
        && (!current || (!isTemplateSelectable(current) && !isTemplatePending(current)))
        && !retargetFrozen.value
      ) {
        clearTemplateSelection()
      }
      if (!currentTemplateId.value && !retargetFrozen.value) {
        const firstPartyReady = templates.value.filter((item) => (
          (item.standard || item.desktop) && isTemplateSelectable(item)
        ))
        if (firstPartyReady.length === 1) selectTemplate(firstPartyReady[0].id)
      }
      if (res.data?.provisioned && !opts.silent && (
        opts.replaceDesktop || opts.replaceStandard || opts.ensureStandard || opts.ensureDesktop
      )) {
        MessagePlugin.info(provisionedTemplateMessage(opts))
      }
      scheduleTemplatePolling()
      return true
    } catch (err: unknown) {
      if (epoch !== templateReqEpoch || !visible()) return false
      const msg = err && typeof err === 'object' && 'message' in err ? String((err as { message?: unknown }).message || '') : ''
      templatesError.value = msg || t('settings.sandbox.templateLoadFailed')
      return false
    } finally {
      if (!opts.silent && epoch === templateReqEpoch) templatesLoading.value = false
    }
  }

  function createStandardTemplate() {
    return loadTemplates({ ensureStandard: true })
  }

  function createDesktopTemplate() {
    return loadTemplates({ ensureDesktop: true })
  }

  function replaceFirstPartyTemplate(item: SandboxTemplate) {
    if (retargetFrozen.value) return
    if (item.desktop) return loadTemplates({ replaceDesktop: true })
    return loadTemplates({ replaceStandard: true })
  }

  function collectedDesktopEnabled(): boolean | undefined {
    if (selectedTemplate.value) {
      // Persist false when the CLI card is selected; omitempty on the
      // server would otherwise keep a stale true from a previous desktop save.
      return Boolean(selectedTemplate.value.desktop)
    }
    // Skill snapshots replace template_id with a UUID that is not in the
    // catalog. Keep the stored bit. If the catalog loaded and this ID is
    // simply unmatched, do not keep a stale true that could disagree with
    // template_id.
    if (templatesLoaded.value && !retargetFrozen.value) {
      return false
    }
    return effectiveRecord.value?.config?.desktop_enabled || undefined
  }

  function resetTemplatesState() {
    stopTemplatePolling()
    templateReqEpoch += 1
    templatesLoading.value = false
    templates.value = []
    templatesLoaded.value = false
    templatesError.value = ''
  }

  return {
    templates,
    templatesLoading,
    templatesLoaded,
    templatesError,
    currentTemplateId,
    selectedTemplate,
    canCreateStandard,
    canCreateDesktop,
    selectTemplate,
    clearTemplateSelection,
    onTemplateCardClick,
    canRebuildTemplate,
    templateDisplayName,
    templateFieldRows,
    templateFailureReason,
    templateStatusTheme,
    templateStatusLabel,
    stopTemplatePolling,
    scheduleTemplatePolling,
    loadTemplates,
    createStandardTemplate,
    createDesktopTemplate,
    replaceFirstPartyTemplate,
    collectedDesktopEnabled,
    resetTemplatesState,
  }
}
