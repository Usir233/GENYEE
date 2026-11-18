<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  createDataSource,
  updateDataSource,
  triggerSync,
  validateConnection,
  validateCredentials,
  listResources,
  resolveResourceAncestors,
  deleteDataSource,
  putDataSourceCredentials,
  deleteDataSourceCredentials,
  type DataSource,
} from '@/api/datasource'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { getDatasourceIconUrl } from './datasourceIcons'
import ConnectorAuthForm from './ConnectorAuthForm.vue'
import ConnectorResourceTreeStep from './ConnectorResourceTreeStep.vue'
import {
  CONNECTOR_DEFS,
  extractErrorMessage,
  hydrateRssFeedUrlsFromConfig,
  isDriveConnector,
  isFeishuFamilyConnector,
  isGitLabConnector,
  isSeafileConnector,
  resourceIconName as getResourceIconName,
  RESOURCE_TYPE_LABEL_MAP,
  serializeAuthHeaders,
  type ConnectorDef,
  type CustomHeaderItem,
  type DataSourceFormState,
  type GitLabProjectInput,
} from './composables/useConnectorDefs'
import { useConnectorResourceTree, type CreateDataSourceApiResponse } from './composables/useConnectorResourceTree'

const props = defineProps<{
  kbId: string
  dataSource: DataSource | null
}>()

const visible = defineModel<boolean>('visible', { default: false })
const emit = defineEmits<{ saved: [] }>()
const { t } = useI18n()

const isEdit = computed(() => !!props.dataSource)
const step = ref(0)
const submitting = ref(false)
const drawerOpenSeq = ref(0)

const credentialsConfigured = ref(false)
const replaceCredentialsMode = ref(false)
const credentialsInputVisible = computed(() => {
  if (!isEdit.value) return true
  if (!credentialsConfigured.value) return true
  return replaceCredentialsMode.value
})

function refreshCredentialsStatus() {
  if (!isEdit.value || !props.dataSource) {
    credentialsConfigured.value = false
    return
  }
  credentialsConfigured.value = props.dataSource.credentials?.credentials?.configured === true
}

const pendingRemoveCredentials = ref(false)
const removingCredentials = ref(false)

function requestRemoveCredentials() {
  pendingRemoveCredentials.value = true
}

function cancelPendingRemoveCredentials() {
  pendingRemoveCredentials.value = false
}

async function confirmRemoveCredentials() {
  if (!props.dataSource?.id) return
  removingCredentials.value = true
  try {
    await deleteDataSourceCredentials(props.dataSource.id)
    credentialsConfigured.value = false
    replaceCredentialsMode.value = false
    pendingRemoveCredentials.value = false
    form.value.config.credentials = {}
    MessagePlugin.success(t('credential.removedToast'))
  } catch (e: unknown) {
    MessagePlugin.error(extractErrorMessage(e) || t('credential.removeFailed'))
  } finally {
    removingCredentials.value = false
  }
}

const rssAuthHeaders = ref<CustomHeaderItem[]>([])

const form = ref<DataSourceFormState>({
  name: '',
  type: '',
  config: {
    credentials: {},
    resource_ids: [],
    settings: {},
  },
  sync_schedule: '0 0 */6 * * *',
  sync_mode: 'incremental',
  conflict_strategy: 'overwrite',
  sync_deletions: true,
})

function syncRssAuthHeadersToCredentials() {
  if (form.value.type !== 'rss') return
  const serialized = serializeAuthHeaders(rssAuthHeaders.value)
  if (serialized) {
    form.value.config.credentials.auth_headers = serialized
  } else {
    delete form.value.config.credentials.auth_headers
  }
}

function addRssAuthHeader() {
  rssAuthHeaders.value.push({ key: '', value: '' })
}

function removeRssAuthHeader(idx: number) {
  rssAuthHeaders.value.splice(idx, 1)
}

function updateRssAuthHeader(idx: number, field: 'key' | 'value', val: string) {
  const row = rssAuthHeaders.value[idx]
  if (row) row[field] = val
}

function needsConnectionTest(): boolean {
  return !(isEdit.value && credentialsConfigured.value && !replaceCredentialsMode.value)
}

function hydrateConfluenceCredentialsFromSettings() {
  if (form.value.type !== 'confluence') return
  const settings = form.value.config.settings || {}
  const creds = form.value.config.credentials || {}
  form.value.config.credentials = {
    ...creds,
    edition: creds.edition || String(settings.edition || 'server'),
    base_url: creds.base_url || String(settings.base_url || ''),
    username: creds.username || String(settings.username || ''),
  }
}

function syncConfluencePublicFieldsToSettings() {
  if (form.value.type !== 'confluence') return
  const creds = form.value.config.credentials || {}
  form.value.config.settings = {
    ...(form.value.config.settings || {}),
    ...(creds.edition ? { edition: creds.edition } : {}),
    ...(creds.base_url ? { base_url: creds.base_url } : {}),
    ...(creds.username ? { username: creds.username } : {}),
  }
}

const testing = ref(false)
const testResult = ref<'success' | 'error' | ''>('')
const testErrorMsg = ref('')

function cancelReplaceCredentials() {
  replaceCredentialsMode.value = false
  pendingRemoveCredentials.value = false
  form.value.config.credentials = {}
  rssAuthHeaders.value = []
  testResult.value = credentialsConfigured.value ? 'success' : ''
  testErrorMsg.value = ''
}

function enterReplaceCredentials() {
  pendingRemoveCredentials.value = false
  replaceCredentialsMode.value = true
  hydrateConfluenceCredentialsFromSettings()
  testResult.value = ''
  testErrorMsg.value = ''
}

const yuqueFolderMode = computed<'none' | 'toc'>({
  get: () => (form.value.config.settings?.folder_mode === 'toc' ? 'toc' : 'none'),
  set: (mode: 'none' | 'toc') => {
    form.value.config.settings = { ...form.value.config.settings, folder_mode: mode }
  },
})

const yuqueTOCOnly = computed({
  get: () => form.value.config.settings?.toc_only === true,
  set: (on: boolean) => {
    form.value.config.settings = { ...form.value.config.settings, toc_only: on }
  },
})

const dingtalkIncludeUploadedFiles = computed({
  get: () => form.value.config.settings?.include_uploaded_files === true,
  set: (on: boolean) => {
    form.value.config.settings = { ...form.value.config.settings, include_uploaded_files: on }
  },
})

const feishuParseMode = computed({
  get: () => String(form.value.config.settings.parse_mode || ''),
  set: (mode: string) => {
    const settings = { ...form.value.config.settings }
    if (mode) settings.parse_mode = mode
    else delete settings.parse_mode
    form.value.config.settings = settings
  },
})

const gitlabProjects = ref<GitLabProjectInput[]>([])
function syncGitLabProjectsToSettings() {
  if (!isGitLabConnector(form.value.type)) return
  form.value.config.settings.projects = gitlabProjects.value
    .filter((project) => project.project_id.trim())
    .map((project) => ({
      project_id: project.project_id.trim(),
      ref: project.ref.trim(),
      paths: project.pathsText
        .split(/[\n,]/)
        .map((path) => path.trim())
        .filter(Boolean),
    }))
}
function addGitLabProject() {
  gitlabProjects.value.push({ project_id: '', ref: '', pathsText: '' })
}
function removeGitLabProject(index: number) {
  gitlabProjects.value.splice(index, 1)
  syncGitLabProjectsToSettings()
}
function updateGitLabProject(index: number, field: keyof GitLabProjectInput, val: string) {
  const project = gitlabProjects.value[index]
  if (project) project[field] = val
}

const {
  resources,
  loadingResources,
  selectedResourceIds,
  expandedResourceIds,
  loadedChildrenIds,
  loadingChildrenIds,
  treeFullyLoaded,
  driveFolderToken,
  driveFolderTokenError,
  driveRootLoaded,
  tempDsId,
  visibleTree,
  checkStates,
  selectedResourceCount,
  hasExpandableNodes,
  markTempCredentialsDirty,
  bumpSession,
  resetTreeState,
  ensureTempDataSource,
  ensureChildrenLoaded,
  loadDriveRoot,
  loadResources,
  toggleExpand,
  toggleResource,
  expandAllNodes,
  collapseAllNodes,
  resourceRowState,
} = useConnectorResourceTree({
  kbId: () => props.kbId,
  isEdit,
  form,
  t,
  api: {
    createDataSource: (payload) =>
      createDataSource(payload as unknown as Parameters<typeof createDataSource>[0]),
    updateDataSource: (id, payload) =>
      updateDataSource(id, payload as unknown as Parameters<typeof updateDataSource>[1]),
    listResources,
    resolveResourceAncestors,
    deleteDataSource,
    putDataSourceCredentials,
  },
  messageError: (msg) => MessagePlugin.error(msg),
  messageWarning: (msg) => MessagePlugin.warning(msg),
  syncRssAuthHeadersToCredentials,
  syncConfluencePublicFieldsToSettings,
  warn: (msg) => console.warn(msg),
})

const schedulePresets = computed(() => [
  { label: t('datasource.schedule30min'), value: '0 */30 * * * *' },
  { label: t('datasource.schedule1h'), value: '0 0 * * * *' },
  { label: t('datasource.schedule6h'), value: '0 0 */6 * * *' },
  { label: t('datasource.schedule12h'), value: '0 0 */12 * * *' },
  { label: t('datasource.schedule24h'), value: '0 0 2 * * *' },
])

const connectorDefs = computed<ConnectorDef[]>(() => CONNECTOR_DEFS)
const currentDef = computed(() => connectorDefs.value.find((d) => d.type === form.value.type))

const displayedCredentialFields = computed(() => {
  const fields = currentDef.value?.fields || []
  if (form.value.type !== 'confluence') return fields

  return fields
    .filter((field) => {
      if (field.key === 'password' || field.key === 'token') {
        return form.value.config.credentials.edition !== 'cloud'
      }
      if (field.key === 'api_token') return form.value.config.credentials.edition === 'cloud'
      return true
    })
    .map((field) => {
      const hasPAT =
        form.value.config.credentials.edition !== 'cloud' &&
        Boolean(String(form.value.config.credentials.token || '').trim())
      if (field.key === 'username' || field.key === 'password') {
        return { ...field, optional: hasPAT }
      }
      return field
    })
})

onBeforeUnmount(() => {
  bumpSession()
  if (!isEdit.value && tempDsId.value) {
    const orphanId = tempDsId.value
    tempDsId.value = ''
    void (async () => {
      try {
        await deleteDataSource(orphanId)
      } catch {
        // Ignore cleanup errors
      }
    })()
  }
})

watch(visible, async (v) => {
  bumpSession()
  if (!v) {
    if (!isEdit.value && tempDsId.value) {
      try {
        await deleteDataSource(tempDsId.value)
      } catch {
        // Ignore cleanup errors
      }
      tempDsId.value = ''
    }
    return
  }
  drawerOpenSeq.value++
  step.value = isEdit.value ? 1 : 0
  testResult.value = ''
  testErrorMsg.value = ''
  pendingRemoveCredentials.value = false
  resetTreeState()
  rssAuthHeaders.value = []
  gitlabProjects.value = []

  if (isEdit.value && props.dataSource) {
    replaceCredentialsMode.value = false
    credentialsConfigured.value = false
    refreshCredentialsStatus()
    testResult.value = credentialsConfigured.value ? 'success' : ''
    const editConfig = props.dataSource.config || {}
    form.value = {
      name: props.dataSource.name,
      type: props.dataSource.type,
      config: {
        credentials: {},
        resource_ids: editConfig.resource_ids || [],
        settings:
          props.dataSource.type === 'rss'
            ? hydrateRssFeedUrlsFromConfig(editConfig)
            : { ...(editConfig.settings || {}) },
      },
      sync_schedule: props.dataSource.sync_schedule,
      sync_mode: props.dataSource.sync_mode,
      conflict_strategy: props.dataSource.conflict_strategy,
      sync_deletions: props.dataSource.sync_deletions,
    }
    selectedResourceIds.value = form.value.config?.resource_ids || []
    if (isGitLabConnector(form.value.type)) {
      const savedProjects = Array.isArray(form.value.config.settings.projects)
        ? (form.value.config.settings.projects as Array<{ project_id?: string; ref?: string; paths?: string[] }>)
        : []
      gitlabProjects.value = savedProjects.map((project) => ({
        project_id: String(project.project_id || ''),
        ref: String(project.ref || ''),
        pathsText: Array.isArray(project.paths) ? project.paths.join('\n') : '',
      }))
    }
    if (isDriveConnector(form.value.type)) {
      const rids = form.value.config?.resource_ids || []
      if (rids.length > 0) {
        driveFolderToken.value = rids[0].split(':')[0]
      }
    }
    tempDsId.value = props.dataSource.id
  } else {
    replaceCredentialsMode.value = false
    credentialsConfigured.value = false
    form.value = {
      name: '',
      type: '',
      config: { credentials: {}, resource_ids: [], settings: {} },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    }
  }
})

watch(
  () => form.value.config.credentials,
  () => {
    if (!isEdit.value && tempDsId.value) {
      markTempCredentialsDirty()
    }
    if (needsConnectionTest()) {
      testResult.value = ''
      testErrorMsg.value = ''
    }
  },
  { deep: true },
)

watch(
  rssAuthHeaders,
  () => {
    syncRssAuthHeadersToCredentials()
    if (needsConnectionTest()) {
      testResult.value = ''
      testErrorMsg.value = ''
    }
  },
  { deep: true },
)

watch(
  () => form.value.config.settings.feed_urls,
  () => {
    if (needsConnectionTest()) {
      testResult.value = ''
      testErrorMsg.value = ''
    }
  },
)

function selectType(def: ConnectorDef) {
  if (!def.available) return
  if (!isEdit.value && tempDsId.value) {
    const orphanId = tempDsId.value
    tempDsId.value = ''
    bumpSession()
    void (async () => {
      try {
        await deleteDataSource(orphanId)
      } catch {
        // Ignore cleanup errors
      }
    })()
  }
  form.value.type = def.type
  form.value.name = t(`datasource.connector.${def.type}`)
  form.value.config.credentials = def.type === 'confluence' ? { edition: 'server' } : {}
  if (isFeishuFamilyConnector(def.type)) {
    form.value.config.settings.parse_mode = form.value.config.settings.parse_mode || 'blocks'
  } else {
    delete form.value.config.settings.parse_mode
  }
  if (def.type === 'confluence') {
    form.value.config.settings = { ...form.value.config.settings, edition: 'server' }
  }
  if (def.type === 'yuque' && !isEdit.value) {
    form.value.config.settings = { ...form.value.config.settings, folder_mode: 'toc' }
  }
  if (isGitLabConnector(def.type)) addGitLabProject()
  rssAuthHeaders.value = []
  step.value = 1
}

// --- Test connection ---
async function testConnection() {
  syncRssAuthHeadersToCredentials()
  syncConfluencePublicFieldsToSettings()
  if (!validateRssFeedUrls()) return
  if (!isEdit.value || !credentialsConfigured.value || replaceCredentialsMode.value) {
    for (const f of displayedCredentialFields.value) {
      if (f.optional || f.fieldType === 'custom_headers') continue
      if (!form.value.config.credentials[f.key]) {
        MessagePlugin.warning(`${t(f.labelKey)} ${t('datasource.isRequired')}`)
        return
      }
    }
  }

  testing.value = true
  testResult.value = ''
  testErrorMsg.value = ''
  try {
    if (isEdit.value && tempDsId.value && !needsConnectionTest()) {
      await updateDataSource(tempDsId.value, {
        ...form.value,
        knowledge_base_id: props.kbId,
      } as unknown as Parameters<typeof updateDataSource>[1])
      await validateConnection(tempDsId.value)
    } else {
      const creds: Record<string, string> = { ...form.value.config.credentials }
      if (form.value.type === 'rss') {
        creds.feed_urls = String(form.value.config.settings.feed_urls ?? '')
      }
      await validateCredentials(form.value.type, creds)
    }
    testResult.value = 'success'
    MessagePlugin.success(t('datasource.testSuccess'))
  } catch (e: unknown) {
    testResult.value = 'error'
    testErrorMsg.value = extractErrorMessage(e)
    MessagePlugin.error(t('datasource.testFailed'))
  }
  testing.value = false
}

function validateRssFeedUrls(): boolean {
  if (form.value.type !== 'rss') return true
  if (!String(form.value.config.settings.feed_urls || '').trim()) {
    MessagePlugin.warning(`${t('datasource.field.feedUrls')} ${t('datasource.isRequired')}`)
    return false
  }
  return true
}

function validateStep1Fields(): boolean {
  syncRssAuthHeadersToCredentials()
  if (!validateRssFeedUrls()) return false
  if (isEdit.value && credentialsConfigured.value && !replaceCredentialsMode.value) {
    return true
  }
  for (const f of displayedCredentialFields.value) {
    if (f.optional || f.fieldType === 'custom_headers') continue
    if (!form.value.config.credentials[f.key]) {
      MessagePlugin.warning(`${t(f.labelKey)} ${t('datasource.isRequired')}`)
      return false
    }
  }
  return true
}

async function nextStep() {
  if (step.value === 1) {
    if (!validateStep1Fields()) return
    if (needsConnectionTest() && testResult.value !== 'success') {
      await testConnection()
      if ((testResult.value as string) !== 'success') return
    }
  }
  if (step.value === 2 && isDriveConnector(form.value.type)) {
    if (!driveFolderToken.value.trim()) {
      driveFolderTokenError.value = t('datasource.drive.folderTokenRequired')
      return
    }
    driveFolderTokenError.value = ''
  }
  if (step.value === 2 && isGitLabConnector(form.value.type)) {
    syncGitLabProjectsToSettings()
    if (!gitlabProjects.value.some((project) => project.project_id.trim())) {
      MessagePlugin.warning(t('datasource.gitlab.projectRequired'))
      return
    }
  }
  if (step.value === 2 && isSeafileConnector(form.value.type) && selectedResourceIds.value.length === 0) {
    MessagePlugin.warning(t('datasource.seafile.selectionRequired'))
    return
  }
  if (step.value === 2 && form.value.type === 'nextcloud' && selectedResourceIds.value.length === 0) {
    MessagePlugin.warning(t('datasource.nextcloud.selectionRequired'))
    return
  }
  step.value++
  if (step.value === 2) {
    if (isDriveConnector(form.value.type)) {
      if (!driveRootLoaded.value && driveFolderToken.value.trim()) {
        void loadDriveRoot()
      }
      return
    }
    if (isGitLabConnector(form.value.type)) return
    void loadResources()
  }
}

function prevStep() {
  step.value--
}

function buildConfigPayload(): {
  credentials: Record<string, string>
  resource_ids: string[]
  settings: Record<string, unknown>
} {
  syncGitLabProjectsToSettings()
  syncConfluencePublicFieldsToSettings()
  return {
    credentials: isEdit.value ? {} : { ...form.value.config.credentials },
    resource_ids: form.value.config.resource_ids,
    settings: form.value.config.settings,
  }
}

async function commitCredentialsIfNeeded(dsId: string): Promise<boolean> {
  if (!isEdit.value || !replaceCredentialsMode.value) return true
  syncRssAuthHeadersToCredentials()
  syncConfluencePublicFieldsToSettings()
  const filled = Object.entries(form.value.config.credentials).filter(([, v]) =>
    typeof v === 'string' ? v !== '' : v != null,
  )
  if (filled.length === 0) return true
  try {
    await putDataSourceCredentials(dsId, Object.fromEntries(filled))
    credentialsConfigured.value = true
    replaceCredentialsMode.value = false
    form.value.config.credentials = {}
    rssAuthHeaders.value = []
    return true
  } catch (e: unknown) {
    MessagePlugin.error(extractErrorMessage(e) || t('credential.saveFailed'))
    return false
  }
}

async function handleSubmit() {
  form.value.config.resource_ids = selectedResourceIds.value
  submitting.value = true
  try {
    let dataSourceId = tempDsId.value

    if (tempDsId.value) {
      const credsOk = await commitCredentialsIfNeeded(tempDsId.value)
      if (!credsOk) {
        submitting.value = false
        return
      }
      await updateDataSource(tempDsId.value, {
        ...form.value,
        config: buildConfigPayload(),
        knowledge_base_id: props.kbId,
        status: 'active',
      } as unknown as Parameters<typeof updateDataSource>[1])
    } else {
      const res = (await createDataSource({
        ...form.value,
        config: buildConfigPayload(),
        knowledge_base_id: props.kbId,
        status: 'active',
      } as unknown as Parameters<typeof createDataSource>[0])) as CreateDataSourceApiResponse
      const created = res?.data || res
      dataSourceId = created?.id || ''
      tempDsId.value = dataSourceId
    }

    if (isEdit.value) {
      MessagePlugin.warning(t('datasource.updateSuccessSyncHint'))
    } else {
      try {
        await triggerSync(dataSourceId)
        MessagePlugin.success(t('datasource.createAndSyncSuccess'))
      } catch (e: unknown) {
        MessagePlugin.warning(extractErrorMessage(e) || t('datasource.createButSyncFailed'))
      }
    }

    emit('saved')
    tempDsId.value = ''
    visible.value = false
  } catch (e: unknown) {
    MessagePlugin.error(extractErrorMessage(e) || t('datasource.saveFailed'))
  }
  submitting.value = false
}

function handleClose() {
  visible.value = false
}

async function handleDrawerConfirm() {
  if (step.value === 1 || step.value === 2) {
    await nextStep()
  } else if (step.value === 3) {
    await handleSubmit()
  }
}

function resourceTypeLabel(type: string): string {
  const key = RESOURCE_TYPE_LABEL_MAP[type]
  return key ? t(key) : ''
}

function shouldShowResourceType(type: string): boolean {
  return Boolean(RESOURCE_TYPE_LABEL_MAP[type])
}

const stepTitles = computed(() => [
  t('datasource.step.selectType'),
  t('datasource.step.credentials'),
  t('datasource.step.resources'),
  t('datasource.step.strategy'),
])

const drawerTitle = computed(() =>
  isEdit.value ? t('datasource.editTitle') : t('datasource.createTitle'),
)
const drawerDescription = computed(() => stepTitles.value[step.value] ?? '')
const drawerConfirmText = computed(() => {
  if (step.value === 3) return isEdit.value ? t('datasource.save') : t('datasource.createAndSync')
  if (step.value >= 1) return t('datasource.next')
  return t('common.save')
})

function handleCredentialUpdate(key: string, val: string) {
  form.value.config.credentials[key] = val
}

function handleSettingUpdate(key: string, val: string) {
  form.value.config.settings = { ...form.value.config.settings, [key]: val }
}

const resourceIconName = getResourceIconName

// Keep composable state/helpers bound in setup scope for SFC unit-test introspection.
void [
  resourceIconName,
  loadedChildrenIds,
  treeFullyLoaded,
  checkStates,
  ensureTempDataSource,
  ensureChildrenLoaded,
  resourceTypeLabel,
  shouldShowResourceType,
]
</script>

<template>
  <SettingDrawer
    v-model:visible="visible"
    :title="drawerTitle"
    :description="drawerDescription"
    :class="[
      form.type ? `datasource-editor-drawer datasource-editor-drawer--${form.type}` : 'datasource-editor-drawer',
      { 'ds-fixed-step': step === 2 && !isGitLabConnector(form.type) },
    ]"
    :hide-footer="step === 0"
    :confirm-text="drawerConfirmText"
    :confirm-loading="submitting || (step === 1 && testing)"
    storage-key="setting-drawer:width:datasource-editor"
    width="640px"
    @confirm="handleDrawerConfirm"
    @cancel="handleClose"
  >
    <template v-if="form.type && getDatasourceIconUrl(form.type)" #headerIcon>
      <img
        :src="getDatasourceIconUrl(form.type)"
        :alt="form.type"
        class="datasource-header-icon__img"
      >
    </template>

    <template v-if="step === 1" #footer-left>
      <t-button v-if="!isEdit" variant="outline" @click="step = 0">
        {{ t('datasource.back') }}
      </t-button>
      <t-button variant="outline" :loading="testing" @click="testConnection">
        <template #icon>
          <t-icon
            v-if="!testing && testResult === 'success'"
            name="check-circle-filled"
            class="status-icon available"
          />
          <t-icon
            v-else-if="!testing && testResult === 'error'"
            name="close-circle-filled"
            class="status-icon unavailable"
          />
        </template>
        {{ testing ? t('model.editor.testing') : t('datasource.testConnection') }}
      </t-button>
      <span
        v-if="testResult"
        :class="['footer-test-message', testResult === 'success' ? 'success' : 'error']"
        :title="testResult === 'error' ? testErrorMsg : t('datasource.connected')"
      >
        {{
          testResult === 'success'
            ? t('datasource.connected')
            : (testErrorMsg || t('datasource.connectionFailed'))
        }}
      </span>
    </template>

    <template v-else-if="step === 2 || step === 3" #footer-left>
      <t-button variant="outline" @click="prevStep">
        {{ t('datasource.back') }}
      </t-button>
    </template>

    <div class="ds-steps">
      <div
        v-for="(title, i) in stepTitles"
        :key="i"
        :class="['ds-step', { active: step === i, done: step > i }]"
      >
        <span class="ds-step-num">
          <t-icon v-if="step > i" name="check" class="ds-step-check" />
          <template v-else>{{ i + 1 }}</template>
        </span>
        <span class="ds-step-title">{{ title }}</span>
      </div>
    </div>

    <ConnectorAuthForm
      :step="step"
      :connector-defs="connectorDefs"
      :current-def="currentDef"
      :form="form"
      :is-edit="isEdit"
      :credentials-configured="credentialsConfigured"
      :replace-credentials-mode="replaceCredentialsMode"
      :credentials-input-visible="credentialsInputVisible"
      :pending-remove-credentials="pendingRemoveCredentials"
      :removing-credentials="removingCredentials"
      :displayed-credential-fields="displayedCredentialFields"
      :rss-auth-headers="rssAuthHeaders"
      :reset-counter="drawerOpenSeq"
      @select-type="selectType"
      @update:name="form.name = $event"
      @update:credential="handleCredentialUpdate"
      @update:setting="handleSettingUpdate"
      @enter-replace-credentials="enterReplaceCredentials"
      @cancel-replace-credentials="cancelReplaceCredentials"
      @request-remove-credentials="requestRemoveCredentials"
      @cancel-remove-credentials="cancelPendingRemoveCredentials"
      @confirm-remove-credentials="confirmRemoveCredentials"
      @add-rss-header="addRssAuthHeader"
      @remove-rss-header="removeRssAuthHeader"
      @update:rss-header="updateRssAuthHeader"
    />

    <ConnectorResourceTreeStep
      :step="step"
      :is-edit="isEdit"
      :form="form"
      :current-def="currentDef"
      :gitlab-projects="gitlabProjects"
      :drive-folder-token="driveFolderToken"
      :drive-folder-token-error="driveFolderTokenError"
      :drive-root-loaded="driveRootLoaded"
      :loading-resources="loadingResources"
      :resources="resources"
      :selected-resource-count="selectedResourceCount"
      :has-expandable-nodes="hasExpandableNodes"
      :visible-tree="visibleTree"
      :expanded-resource-ids="expandedResourceIds"
      :loading-children-ids="loadingChildrenIds"
      :resource-row-state="resourceRowState"
      :schedule-presets="schedulePresets"
      :feishu-parse-mode="feishuParseMode"
      :dingtalk-include-uploaded-files="dingtalkIncludeUploadedFiles"
      :yuque-folder-mode="yuqueFolderMode"
      :yuque-t-o-c-only="yuqueTOCOnly"
      @add-gitlab-project="addGitLabProject"
      @remove-gitlab-project="removeGitLabProject"
      @update:gitlab-project="updateGitLabProject"
      @update:drive-folder-token="driveFolderToken = $event"
      @clear-drive-folder-error="driveFolderTokenError = ''"
      @load-drive-root="loadDriveRoot"
      @load-resources="loadResources"
      @expand-all="expandAllNodes"
      @collapse-all="collapseAllNodes"
      @toggle-expand="toggleExpand"
      @toggle-resource="toggleResource"
      @update:sync-schedule="form.sync_schedule = $event"
      @update:sync-mode="form.sync_mode = $event"
      @update:conflict-strategy="form.conflict_strategy = $event"
      @update:feishu-parse-mode="feishuParseMode = $event"
      @update:sync-deletions="form.sync_deletions = $event"
      @update:dingtalk-uploaded-files="dingtalkIncludeUploadedFiles = $event"
      @update:yuque-folder-mode="yuqueFolderMode = $event"
      @update:yuque-toc-only="yuqueTOCOnly = $event"
    />
  </SettingDrawer>
</template>

<style scoped lang="less">
@import './datasource-surface.less';
@import './datasource-editor-dialog.less';
</style>

<style lang="less">
.datasource-editor-drawer .setting-drawer__header-icon:has(.datasource-header-icon__img) {
  background: var(--td-bg-color-container);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.datasource-editor-drawer .datasource-header-icon__img {
  display: block;
  width: 24px;
  height: 24px;
  object-fit: contain;
}

.ds-fixed-step {
  .t-drawer__body {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .setting-drawer__body {
    flex: 1;
    min-height: 0;
  }

  .ds-resource-section {
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .resource-picker,
  .ds-drive-placeholder,
  .ds-loading-center,
  .ds-resource-empty {
    flex: 1;
    min-height: 0;
  }

  .ds-loading-center {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .resource-picker__list {
    flex: 1;
    min-height: 0;
    max-height: none;
  }
}
</style>
