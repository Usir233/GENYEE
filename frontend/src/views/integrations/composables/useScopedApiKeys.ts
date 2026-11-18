import { computed, reactive, ref, watch, type Ref } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import type { ComposerTranslation } from 'vue-i18n'
import {
  createTenantAPIKey,
  deleteTenantAPIKey,
  listTenantAPIKeys,
  updateTenantAPIKey,
  type TenantAPIKey,
  type TenantAPIKeyCapability,
} from '@/api/tenant'
import {
  DEFAULT_TENANT_API_KEY_CAPABILITIES,
  KB_SCOPED_API_KEY_CAPABILITIES,
  TENANT_API_KEY_CAPABILITIES,
  TENANT_API_KEY_CAPABILITY_GROUPS,
  type ApiKeyCapabilityGroup,
} from '@/config/apiKeyCapabilities'
import { normalizeAPIKeyKnowledgeBaseIDs } from '../apiKeyScope'

export interface KnowledgeBaseOptionItem {
  id: string
  name: string
}

interface ChatResourcesLike {
  rawKnowledgeBases: ReadonlyArray<{ id: string | number; name?: string }>
  ensureKnowledgeBases: () => Promise<unknown>
}

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  if (err && typeof err === 'object' && 'message' in err && typeof (err as { message?: unknown }).message === 'string') {
    return (err as { message: string }).message
  }
  return ''
}

export function useScopedApiKeys(options: {
  t: ComposerTranslation
  tenantId: Ref<number>
  chatResources: ChatResourcesLike
}) {
  const { t, tenantId, chatResources } = options

  const apiKey = ref('')
  const apiKeys = ref<TenantAPIKey[]>([])
  const apiKeysLoading = ref(false)
  const apiKeyDialogVisible = ref(false)
  const apiKeyCreating = ref(false)
  const apiKeyScopeDialogVisible = ref(false)
  const apiKeyScopeSaving = ref(false)
  const editingAPIKey = ref<TenantAPIKey | null>(null)
  const knowledgeBasesLoading = ref(false)
  const knowledgeBases = ref<KnowledgeBaseOptionItem[]>([])

  const API_KEY_CAPABILITIES = TENANT_API_KEY_CAPABILITIES
  const DEFAULT_API_KEY_CAPABILITIES = DEFAULT_TENANT_API_KEY_CAPABILITIES
  const KB_SCOPED_CAPABILITIES = KB_SCOPED_API_KEY_CAPABILITIES
  const apiKeyCapabilityGroups = TENANT_API_KEY_CAPABILITY_GROUPS

  const capabilitySelections = reactive<Record<TenantAPIKeyCapability, boolean>>(
    Object.fromEntries(
      API_KEY_CAPABILITIES.map((capability) => [capability, DEFAULT_API_KEY_CAPABILITIES.has(capability)]),
    ) as Record<TenantAPIKeyCapability, boolean>,
  )

  const editingCapabilitySelections = reactive<Record<TenantAPIKeyCapability, boolean>>(
    Object.fromEntries(
      API_KEY_CAPABILITIES.map((capability) => [capability, false]),
    ) as Record<TenantAPIKeyCapability, boolean>,
  )

  const editingAPIKeyForm = reactive({
    name: '',
    knowledge_base_ids: [] as string[],
    tenant_full_enabled: false,
    expires_at_unix: undefined as number | undefined,
  })

  const editingAPIKeyFullAccessEnabled = computed(() => editingAPIKeyForm.tenant_full_enabled)
  const editingAPIKeyAccessMode = computed<'scoped' | 'full'>({
    get: () => (editingAPIKeyForm.tenant_full_enabled ? 'full' : 'scoped'),
    set: (value) => {
      editingAPIKeyForm.tenant_full_enabled = value === 'full'
    },
  })
  const editingSelectedCapabilities = computed(() => (
    API_KEY_CAPABILITIES.filter((capability) => editingCapabilitySelections[capability])
  ))
  const editingKnowledgeScopeApplies = computed(() => (
    !editingAPIKeyFullAccessEnabled.value
    && editingSelectedCapabilities.value.some((capability) => KB_SCOPED_CAPABILITIES.has(capability))
  ))

  watch(editingKnowledgeScopeApplies, (applies) => {
    if (!applies) editingAPIKeyForm.knowledge_base_ids = []
  })

  function editingCapabilityGroupAllSelected(group: ApiKeyCapabilityGroup): boolean {
    return group.capabilities.every((capability) => editingCapabilitySelections[capability.value])
  }

  function toggleEditingCapabilityGroup(group: ApiKeyCapabilityGroup, selected: boolean) {
    group.capabilities.forEach((capability) => {
      editingCapabilitySelections[capability.value] = selected
    })
  }

  const apiKeyForm = reactive({
    name: '',
    knowledge_base_ids: [] as string[],
    tenant_full_enabled: false,
  })

  const apiKeyFullAccessEnabled = computed(() => apiKeyForm.tenant_full_enabled)
  const apiKeyAccessMode = computed<'scoped' | 'full'>({
    get: () => (apiKeyForm.tenant_full_enabled ? 'full' : 'scoped'),
    set: (value) => {
      apiKeyForm.tenant_full_enabled = value === 'full'
    },
  })
  const selectedCapabilityValues = computed(() => (
    API_KEY_CAPABILITIES.filter((capability) => capabilitySelections[capability])
  ))
  const apiKeyKnowledgeScopeApplies = computed(() => (
    !apiKeyFullAccessEnabled.value
    && selectedCapabilityValues.value.some((capability) => KB_SCOPED_CAPABILITIES.has(capability))
  ))

  watch(() => apiKeyForm.tenant_full_enabled, (enabled) => {
    if (enabled) {
      apiKeyForm.knowledge_base_ids = []
    }
  })

  watch(apiKeyKnowledgeScopeApplies, (applies) => {
    if (!applies) {
      apiKeyForm.knowledge_base_ids = []
    }
  })

  const knowledgeBaseOptions = computed(() => knowledgeBases.value.map((kb) => ({
    label: kb.name || kb.id,
    value: kb.id,
  })))

  function selectedCapabilities(): TenantAPIKeyCapability[] {
    return selectedCapabilityValues.value
  }

  function setCapabilitySelected(capability: TenantAPIKeyCapability, selected: boolean) {
    capabilitySelections[capability] = selected
  }

  function handleCapabilityChange(capability: TenantAPIKeyCapability, checked: unknown) {
    setCapabilitySelected(capability, Boolean(checked))
  }

  function capabilityGroupAllSelected(group: ApiKeyCapabilityGroup): boolean {
    return group.capabilities.every((capability) => capabilitySelections[capability.value])
  }

  function toggleCapabilityGroup(group: ApiKeyCapabilityGroup, selected: boolean) {
    group.capabilities.forEach((capability) => {
      capabilitySelections[capability.value] = selected
    })
  }

  function keyCapabilityLabels(key: TenantAPIKey): string[] {
    if (key.full_access) return []
    const labels: Partial<Record<TenantAPIKeyCapability, string>> = {
      retrieve: t('integrations.api.capabilityRetrieve'),
      chat: t('integrations.api.capabilityChat'),
      read_agents: t('integrations.api.capabilityReadAgents'),
      ingest: t('integrations.api.capabilityIngest'),
      manage_kbs: t('integrations.api.capabilityManageKbs'),
      manage_agents: t('integrations.api.capabilityManageAgents'),
      message_history: t('integrations.api.capabilityMessageHistory'),
      manage_models: t('integrations.api.capabilityManageModels'),
      manage_mcp_services: t('integrations.api.capabilityManageMcpServices'),
      manage_datasources: t('integrations.api.capabilityManageDatasources'),
      manage_channels: t('integrations.api.capabilityManageChannels'),
      manage_vector_stores: t('integrations.api.capabilityManageVectorStores'),
      manage_storage_backends: t('integrations.api.capabilityManageStorageBackends'),
      manage_web_search: t('integrations.api.capabilityManageWebSearch'),
      run_evaluations: t('integrations.api.capabilityRunEvaluations'),
      manage_members: t('integrations.api.capabilityManageMembers'),
      manage_spaces: t('integrations.api.capabilityManageSpaces'),
      manage_tenant_settings: t('integrations.api.capabilityManageTenantSettings'),
    }
    return (key.capabilities ?? [])
      .map((c) => labels[c])
      .filter((label): label is string => Boolean(label))
  }

  function formatApiKeyCapabilitiesTitle(key: TenantAPIKey): string {
    if (key.full_access) return t('integrations.api.capabilityTenantFull')
    const labels = keyCapabilityLabels(key)
    return labels.length > 0 ? labels.join(' / ') : t('integrations.api.apiKeyScopedAccess')
  }

  function formatApiKeyAccessModeLabel(key: TenantAPIKey): string {
    return key.full_access
      ? t('integrations.api.capabilityTenantFull')
      : t('integrations.api.apiKeyScopedAccess')
  }

  function formatKeyKnowledgeScope(ids: readonly string[] | null | undefined): string {
    const normalizedIDs = normalizeAPIKeyKnowledgeBaseIDs(ids)
    if (!normalizedIDs.length) return t('integrations.api.allKnowledgeBases')
    const names = normalizedIDs.map((id) => knowledgeBases.value.find((kb) => kb.id === id)?.name || id)
    return names.join(', ')
  }

  function maskAPIKey(value: string): string {
    if (value.length <= 12) return '*'.repeat(value.length)
    return `${value.slice(0, 8)}${'*'.repeat(8)}${value.slice(-6)}`
  }

  function formatKeyMaskedValue(key: TenantAPIKey): string {
    const value = key.api_key || ''
    if (!value) return '-'
    return maskAPIKey(value)
  }

  function formatDate(value?: string): string {
    if (!value) return '-'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return '-'
    return date.toLocaleString()
  }

  async function loadAPIKeys() {
    if (!tenantId.value) return
    apiKeysLoading.value = true
    try {
      const resp = await listTenantAPIKeys(tenantId.value)
      if (!resp.success) {
        throw new Error(resp.message || t('integrations.api.loadApiKeysFailed'))
      }
      apiKeys.value = resp.data || []
      if (!apiKey.value || !apiKeys.value.some((item) => item.api_key === apiKey.value)) {
        apiKey.value = apiKeys.value[0]?.api_key || ''
      }
    } catch (err: unknown) {
      MessagePlugin.error(getErrorMessage(err) || t('integrations.api.loadApiKeysFailed'))
    } finally {
      apiKeysLoading.value = false
    }
  }

  async function loadKnowledgeBaseOptions() {
    knowledgeBasesLoading.value = true
    try {
      await chatResources.ensureKnowledgeBases()
      knowledgeBases.value = chatResources.rawKnowledgeBases.map((item) => ({
        id: String(item.id),
        name: item.name || String(item.id),
      }))
    } catch {
      knowledgeBases.value = []
    } finally {
      knowledgeBasesLoading.value = false
    }
  }

  function openCreateAPIKeyDialog() {
    apiKeyForm.name = ''
    apiKeyForm.knowledge_base_ids = []
    apiKeyForm.tenant_full_enabled = false
    API_KEY_CAPABILITIES.forEach((capability) => {
      capabilitySelections[capability] = DEFAULT_API_KEY_CAPABILITIES.has(capability)
    })
    apiKeyDialogVisible.value = true
    void loadKnowledgeBaseOptions()
  }

  async function createScopedAPIKey() {
    if (!apiKeyForm.name.trim()) {
      MessagePlugin.error(t('integrations.api.apiKeyNameRequired'))
      return
    }
    if (!apiKeyFullAccessEnabled.value && selectedCapabilities().length === 0) {
      MessagePlugin.error(t('integrations.api.apiKeyCapabilitiesRequired'))
      return
    }
    apiKeyCreating.value = true
    try {
      const resp = await createTenantAPIKey(tenantId.value, {
        name: apiKeyForm.name.trim(),
        full_access: apiKeyFullAccessEnabled.value,
        knowledge_base_ids: apiKeyKnowledgeScopeApplies.value ? apiKeyForm.knowledge_base_ids : [],
        capabilities: apiKeyFullAccessEnabled.value ? [] : selectedCapabilities(),
      })
      if (!resp.success || !resp.data?.api_key) {
        throw new Error(resp.message || t('integrations.api.createApiKeyFailed'))
      }
      apiKeyDialogVisible.value = false
      apiKey.value = resp.data.api_key
      MessagePlugin.success(t('integrations.api.apiKeyCreated'))
      await loadAPIKeys()
    } catch (err: unknown) {
      MessagePlugin.error(getErrorMessage(err) || t('integrations.api.createApiKeyFailed'))
    } finally {
      apiKeyCreating.value = false
    }
  }

  function openEditAPIKeyScope(key: TenantAPIKey) {
    editingAPIKey.value = key
    editingAPIKeyForm.name = key.name
    editingAPIKeyForm.tenant_full_enabled = key.full_access
    editingAPIKeyForm.knowledge_base_ids = normalizeAPIKeyKnowledgeBaseIDs(key.knowledge_base_ids)
    const expiresAt = key.expires_at ? Date.parse(key.expires_at) : Number.NaN
    editingAPIKeyForm.expires_at_unix = Number.isNaN(expiresAt)
      ? undefined
      : Math.floor(expiresAt / 1000)
    const currentCapabilities = new Set(key.capabilities || [])
    API_KEY_CAPABILITIES.forEach((capability) => {
      editingCapabilitySelections[capability] = currentCapabilities.has(capability)
    })
    apiKeyScopeDialogVisible.value = true
    void loadKnowledgeBaseOptions()
  }

  async function saveAPIKeyConfiguration() {
    const key = editingAPIKey.value
    if (!key) return
    if (!editingAPIKeyForm.name.trim()) {
      MessagePlugin.error(t('integrations.api.apiKeyNameRequired'))
      return
    }
    if (!editingAPIKeyFullAccessEnabled.value && editingSelectedCapabilities.value.length === 0) {
      MessagePlugin.error(t('integrations.api.apiKeyCapabilitiesRequired'))
      return
    }
    apiKeyScopeSaving.value = true
    try {
      const resp = await updateTenantAPIKey(tenantId.value, key.id, {
        name: editingAPIKeyForm.name.trim(),
        full_access: editingAPIKeyFullAccessEnabled.value,
        capabilities: editingAPIKeyFullAccessEnabled.value ? [] : editingSelectedCapabilities.value,
        knowledge_base_ids: editingKnowledgeScopeApplies.value ? editingAPIKeyForm.knowledge_base_ids : [],
        expires_at_unix: editingAPIKeyForm.expires_at_unix,
      })
      if (!resp.success || !resp.data) {
        throw new Error(resp.message || t('integrations.api.updateApiKeyScopeFailed'))
      }
      apiKeyScopeDialogVisible.value = false
      editingAPIKey.value = null
      MessagePlugin.success(t('integrations.api.updateApiKeyScopeSuccess'))
      await loadAPIKeys()
    } catch (err: unknown) {
      MessagePlugin.error(getErrorMessage(err) || t('integrations.api.updateApiKeyScopeFailed'))
    } finally {
      apiKeyScopeSaving.value = false
    }
  }

  async function deleteScopedAPIKey(id: number) {
    const resp = await deleteTenantAPIKey(tenantId.value, id)
    if (!resp.success) {
      MessagePlugin.error(resp.message || t('integrations.api.deleteApiKeyFailed'))
      return
    }
    MessagePlugin.success(t('integrations.api.deleteApiKeySuccess'))
    await loadAPIKeys()
  }

  function confirmDeleteAPIKey(id: number) {
    const dialog = DialogPlugin.confirm({
      header: t('integrations.api.deleteApiKey'),
      body: t('integrations.api.deleteApiKeyConfirm'),
      confirmBtn: { content: t('integrations.api.deleteApiKey'), theme: 'danger' },
      cancelBtn: t('common.cancel'),
      onConfirm: async () => {
        await deleteScopedAPIKey(id)
        dialog.destroy()
      },
      onClose: () => dialog.destroy(),
    })
  }

  return {
    apiKey,
    apiKeys,
    apiKeysLoading,
    apiKeyDialogVisible,
    apiKeyCreating,
    apiKeyScopeDialogVisible,
    apiKeyScopeSaving,
    editingAPIKey,
    knowledgeBasesLoading,
    knowledgeBases,
    knowledgeBaseOptions,
    apiKeyCapabilityGroups,
    capabilitySelections,
    editingCapabilitySelections,
    apiKeyForm,
    apiKeyAccessMode,
    apiKeyFullAccessEnabled,
    apiKeyKnowledgeScopeApplies,
    editingAPIKeyForm,
    editingAPIKeyAccessMode,
    editingAPIKeyFullAccessEnabled,
    editingKnowledgeScopeApplies,
    handleCapabilityChange,
    capabilityGroupAllSelected,
    toggleCapabilityGroup,
    editingCapabilityGroupAllSelected,
    toggleEditingCapabilityGroup,
    keyCapabilityLabels,
    formatApiKeyCapabilitiesTitle,
    formatApiKeyAccessModeLabel,
    formatKeyKnowledgeScope,
    formatKeyMaskedValue,
    formatDate,
    loadAPIKeys,
    loadKnowledgeBaseOptions,
    openCreateAPIKeyDialog,
    createScopedAPIKey,
    openEditAPIKeyScope,
    saveAPIKeyConfiguration,
    confirmDeleteAPIKey,
  }
}
