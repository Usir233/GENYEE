<template>
  <div class="api-integration">
    <div v-if="loading" class="state-row">
      <t-loading size="small" />
      <span>{{ $t('integrations.api.loading') }}</span>
    </div>

    <t-alert v-else-if="error" theme="error" :message="error">
      <template #operation>
        <t-button size="small" @click="load">{{ $t('integrations.api.retry') }}</t-button>
      </template>
    </t-alert>

    <div v-else class="api-settings">
      <section class="settings-band">
        <DesktopLanApiSection
          :api-base-url="apiBaseUrl"
          :lan-base-url="wailsApiLanBaseURL"
          :show-desktop-port-setting="showDesktopPortSetting"
          :show-desktop-bind-public-setting="showDesktopBindPublicSetting"
          :desktop-port="desktopPortInput"
          :desktop-bind-public="desktopBindPublicInput"
          :show-lan-url-unavailable-hint="showLanUrlUnavailableHint"
          :has-api-doc="Boolean(docsUrl('apiOverview'))"
          @copy="copy"
          @update:desktop-port="(val) => { desktopPortInput = val }"
          @save-desktop-port="saveDesktopPort"
          @change-desktop-bind-public="onDesktopBindPublicChange"
          @open-doc="openApiDoc"
        />

        <ScopedApiKeysSection
          :api-keys="apiKeys"
          :loading="apiKeysLoading"
          :key-capability-labels="keyCapabilityLabels"
          :format-api-key-capabilities-title="formatApiKeyCapabilitiesTitle"
          :format-api-key-access-mode-label="formatApiKeyAccessModeLabel"
          :format-key-knowledge-scope="formatKeyKnowledgeScope"
          :format-key-masked-value="formatKeyMaskedValue"
          :format-date="formatDate"
          @create="openCreateAPIKeyDialog"
          @edit="openEditAPIKeyScope"
          @copy="copy"
          @delete="confirmDeleteAPIKey"
        />
      </section>

      <section class="settings-band principal-section">
        <div class="principal-section__header">
          <label>{{ $t('integrations.api.principalMode') }}</label>
          <p>{{ $t('integrations.api.principalModeDesc') }}</p>
          <p class="principal-section__scope">{{ $t('integrations.api.principalScope') }}</p>
        </div>

        <t-radio-group
          v-model="form.mode"
          class="mode-radio"
          @change="(val: string | number | boolean) => handlePrincipalModeChange(val as APIPrincipalMode)"
        >
          <t-radio-button value="tenant">{{ $t('integrations.api.modeTenant') }}</t-radio-button>
          <t-radio-button value="direct_header">{{ $t('integrations.api.modeDirect') }}</t-radio-button>
          <t-radio-button value="signed_token">{{ $t('integrations.api.modeSigned') }}</t-radio-button>
        </t-radio-group>

        <div v-if="form.mode !== 'tenant'" class="mode-detail">
          <div
            v-if="form.mode === 'direct_header'"
            class="mode-callout mode-callout--warning"
          >
            <div class="mode-callout__body">
              <strong>{{ $t('integrations.api.directWarning') }}</strong>
              <p>{{ $t('integrations.api.directWarningDetail') }}</p>
            </div>
          </div>
          <div
            v-else-if="form.mode === 'signed_token'"
            class="mode-callout"
          >
            <div class="mode-callout__body">
              <strong>{{ $t('integrations.api.signedRecommended') }}</strong>
              <p>{{ $t('integrations.api.signedFlowDetail') }}</p>
            </div>
          </div>

          <div v-if="form.mode === 'direct_header'" class="principal-config">
            <div class="config-row">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.directHeader') }}</label>
              </div>
              <code class="fixed-header-name">{{ directHeaderName }}</code>
            </div>
            <div class="config-row config-row--switch">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.requireDirectHeader') }}</label>
                <p>{{ $t('integrations.api.requireDirectHeaderDesc') }}</p>
              </div>
              <div class="config-row__action">
                <t-switch
                  v-model="form.require_direct_header"
                  size="small"
                  @change="(val: string | number | boolean) => handleRequireDirectHeaderChange(Boolean(val))"
                />
              </div>
            </div>
          </div>

          <div v-else-if="form.mode === 'signed_token'" class="principal-config">
            <div class="config-row">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.tokenHeader') }}</label>
                <p>{{ $t('integrations.api.tokenHeaderDesc') }}</p>
              </div>
              <code class="fixed-header-name">{{ tokenHeaderName }}</code>
            </div>
            <div class="config-row config-row--secret">
              <div class="config-row__text">
                <label>{{ $t('integrations.api.hmacSecret') }}</label>
                <p>{{ $t('integrations.api.hmacSecretDesc') }}</p>
              </div>
              <div class="secret-field">
                <div class="secret-control">
                  <t-input
                    v-model="secretInput"
                    :type="secretInputType"
                    class="mono-input secret-mono-input"
                    :placeholder="config?.has_hmac_secret && !secretInput.trim() ? $t('integrations.api.secretConfigured') : ''"
                    @blur="triggerAutoSave"
                  />
                  <t-button
                    v-if="secretInput.trim()"
                    size="small"
                    variant="text"
                    :aria-label="$t('integrations.api.hmacSecret')"
                    @click="showHMACSecret = !showHMACSecret"
                  >
                    <t-icon :name="showHMACSecret ? 'browse-off' : 'browse'" />
                  </t-button>
                  <t-button
                    v-if="secretInput.trim()"
                    size="small"
                    variant="text"
                    :title="$t('integrations.api.copy')"
                    :aria-label="$t('integrations.api.copy')"
                    @click="copy(secretInput)"
                  >
                    <t-icon name="file-copy" />
                  </t-button>
                  <t-button
                    size="small"
                    variant="text"
                    theme="danger"
                    :title="$t('integrations.api.generateSecret')"
                    :aria-label="$t('integrations.api.generateSecret')"
                    :loading="saving"
                    @click="confirmGenerateSecret"
                  >
                    <t-icon name="refresh" />
                  </t-button>
                </div>
                <p v-if="showSecretSavedHint" class="secret-saved-hint">
                  {{ $t('integrations.api.secretSavedCopyHint') }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <div class="examples">
          <t-tabs
            v-if="form.mode === 'signed_token'"
            v-model="exampleTab"
            class="snippet-tabs"
          >
            <t-tab-panel value="jwt" :label="$t('integrations.api.tokenSignExample')" />
            <t-tab-panel value="curl" :label="$t('integrations.api.requestExample')" />
          </t-tabs>
          <div class="code-panel">
            <div class="code-panel__toolbar">
              <span class="code-panel__label">{{ activeExampleLabel }}</span>
              <t-button size="small" variant="text" class="code-panel__copy" @click="copy(activeExampleText)">
                <template #icon><t-icon name="file-copy" /></template>
                {{ $t('integrations.api.copy') }}
              </t-button>
            </div>
            <pre class="code-panel__pre">{{ activeExampleText }}</pre>
          </div>
        </div>

        <div class="playground-entry">
          <div class="playground-entry__info">
            <label>{{ $t('integrations.api.playgroundTitle') }}</label>
            <p>{{ $t('integrations.api.playgroundDesc') }}</p>
          </div>
          <t-button variant="outline" @click="openPlaygroundDrawer">
            <template #icon><t-icon name="code" /></template>
            {{ $t('integrations.api.playgroundOpen') }}
          </t-button>
        </div>
      </section>
    </div>

    <ApiPlaygroundDrawer
      v-model:visible="playgroundDrawerVisible"
      :mode="form.mode"
      :playground="playground"
      :agent-options="agentOptions"
      :agents-loading="agentsLoading"
      :agents-error="agentsError"
      :external-user-hint="externalUserHint"
      :playground-request-preview="playgroundRequestPreview"
      :playground-disabled-reason="playgroundDisabledReason"
      :can-run-playground="canRunPlayground"
      :has-playground-result="hasPlaygroundResult"
      @update-playground="(patch) => Object.assign(playground, patch)"
      @run="runPlayground"
      @stop="stopPlayground"
      @cancel="handlePlaygroundDrawerCancel"
      @copy="copy"
    />

    <ApiKeyScopeDialog
      v-model:visible="apiKeyDialogVisible"
      mode="create"
      :name="apiKeyForm.name"
      :access-mode="apiKeyAccessMode"
      :full-access-enabled="apiKeyFullAccessEnabled"
      :knowledge-scope-applies="apiKeyKnowledgeScopeApplies"
      :knowledge-base-ids="apiKeyForm.knowledge_base_ids"
      :knowledge-bases-loading="knowledgeBasesLoading"
      :knowledge-base-options="knowledgeBaseOptions"
      :capability-groups="apiKeyCapabilityGroups"
      :capability-selections="capabilitySelections"
      :group-all-selected="capabilityGroupAllSelected"
      :confirm-loading="apiKeyCreating"
      @update:name="(val) => { apiKeyForm.name = val }"
      @update:access-mode="(val) => { apiKeyAccessMode = val }"
      @update:knowledge-base-ids="(val) => { apiKeyForm.knowledge_base_ids = val }"
      @toggle-group="toggleCapabilityGroup"
      @change-capability="handleCapabilityChange"
      @confirm="createScopedAPIKey"
    />

    <ApiKeyScopeDialog
      v-model:visible="apiKeyScopeDialogVisible"
      mode="edit"
      :editing-key-name="editingAPIKey?.name || ''"
      :name="editingAPIKeyForm.name"
      :access-mode="editingAPIKeyAccessMode"
      :full-access-enabled="editingAPIKeyFullAccessEnabled"
      :knowledge-scope-applies="editingKnowledgeScopeApplies"
      :knowledge-base-ids="editingAPIKeyForm.knowledge_base_ids"
      :knowledge-bases-loading="knowledgeBasesLoading"
      :knowledge-base-options="knowledgeBaseOptions"
      :capability-groups="apiKeyCapabilityGroups"
      :capability-selections="editingCapabilitySelections"
      :group-all-selected="editingCapabilityGroupAllSelected"
      :confirm-loading="apiKeyScopeSaving"
      @update:name="(val) => { editingAPIKeyForm.name = val }"
      @update:access-mode="(val) => { editingAPIKeyAccessMode = val }"
      @update:knowledge-base-ids="(val) => { editingAPIKeyForm.knowledge_base_ids = val }"
      @toggle-group="toggleEditingCapabilityGroup"
      @change-capability="(cap, checked) => { editingCapabilitySelections[cap] = checked }"
      @confirm="saveAPIKeyConfiguration"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { copyWithToast } from '@/utils/clipboard'
import { BUILTIN_SMART_REASONING_ID } from '@/api/agent'
import { useAuthStore } from '@/stores/auth'
import { useChatResourcesStore } from '@/stores/chatResources'
import {
  getAPIPrincipalConfig,
  updateAPIPrincipalConfig,
  type APIPrincipalConfig,
  type APIPrincipalMode,
} from '@/api/tenant'
import { getApiBaseUrl } from '@/utils/api-base'
import { docsUrl } from '@/utils/docsUrl'
import DesktopLanApiSection from './components/DesktopLanApiSection.vue'
import ScopedApiKeysSection from './components/ScopedApiKeysSection.vue'
import ApiKeyScopeDialog from './components/ApiKeyScopeDialog.vue'
import ApiPlaygroundDrawer from './components/ApiPlaygroundDrawer.vue'
import { useScopedApiKeys } from './composables/useScopedApiKeys'
import { useApiPlaygroundRunner } from './composables/useApiPlaygroundRunner'

type GENYEEDesktopWindow = Window & {
  __GENYEE_API_BASE__?: string
  __GENYEE_API_LAN_BASE__?: string
  go?: {
    main?: {
      App?: {
        GetAPIBaseURL?: () => Promise<string> | string
        GetAPILanBaseURL?: () => Promise<string> | string
        GetDesktopHTTPPortSetting?: () => Promise<number> | number
        GetDesktopHTTPBindPublicSetting?: () => Promise<boolean> | boolean
        GetDesktopListenPublicActive?: () => Promise<boolean> | boolean
        SetDesktopHTTPPortSetting?: (port: number) => Promise<void> | void
        SetDesktopHTTPBindPublicSetting?: (v: boolean) => Promise<void> | void
      }
    }
  }
}

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  if (err && typeof err === 'object' && 'message' in err && typeof (err as { message?: unknown }).message === 'string') {
    return (err as { message: string }).message
  }
  return ''
}

const { t } = useI18n()

const DEFAULT_DIRECT_HEADER_NAME = 'X-External-User-ID'
const DEFAULT_TOKEN_HEADER_NAME = 'X-External-User-Token'

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const authStore = useAuthStore()
const chatResources = useChatResourcesStore()
const tenantId = ref(0)
const config = ref<APIPrincipalConfig | null>(null)
const secretInput = ref('')
const lastSavedSecretInput = ref('')
const exampleTab = ref<'jwt' | 'curl'>('curl')
const showHMACSecret = ref(false)
const wailsApiBaseURL = ref<string | null>(null)
const wailsApiLanBaseURL = ref<string | null>(null)
const showDesktopPortSetting = ref(false)
const showDesktopBindPublicSetting = ref(false)
const desktopPortInput = ref<number | undefined>(0)
const desktopBindPublicInput = ref(false)
const desktopListenPublicActive = ref(false)

const form = reactive({
  mode: 'tenant' as APIPrincipalMode,
  direct_header_name: DEFAULT_DIRECT_HEADER_NAME,
  signed_token_header_name: DEFAULT_TOKEN_HEADER_NAME,
  require_direct_header: false,
})

const apiBaseUrl = computed(() => {
  if (wailsApiBaseURL.value) {
    return wailsApiBaseURL.value
  }
  const configured = getApiBaseUrl().trim().replace(/\/$/, '')
  const origin = typeof window !== 'undefined' && window.location.origin !== 'null' ? window.location.origin : ''
  return `${configured || origin}/api/v1`
})

const showLanUrlUnavailableHint = computed(() => (
  showDesktopBindPublicSetting.value
  && desktopListenPublicActive.value
  && !wailsApiLanBaseURL.value
))

const tokenHeaderName = computed(() => DEFAULT_TOKEN_HEADER_NAME)
const directHeaderName = computed(() => DEFAULT_DIRECT_HEADER_NAME)

const {
  apiKey,
  apiKeys,
  apiKeysLoading,
  apiKeyDialogVisible,
  apiKeyCreating,
  apiKeyScopeDialogVisible,
  apiKeyScopeSaving,
  editingAPIKey,
  knowledgeBasesLoading,
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
} = useScopedApiKeys({
  t,
  tenantId,
  chatResources,
})

const {
  agentsLoading,
  agentsError,
  agentOptions,
  playground,
  playgroundDrawerVisible,
  externalUserHint,
  playgroundRequestPreview,
  playgroundDisabledReason,
  canRunPlayground,
  hasPlaygroundResult,
  loadAgents,
  ensurePlaygroundAgent,
  openPlaygroundDrawer,
  handlePlaygroundDrawerCancel,
  runPlayground,
  stopPlayground,
} = useApiPlaygroundRunner({
  t,
  tenantId,
  apiKey,
  apiBaseUrl,
  mode: () => form.mode,
  directHeaderName,
  tokenHeaderName,
  chatResources,
})

watch(() => form.mode, (mode) => {
  if (mode === 'signed_token') {
    exampleTab.value = 'curl'
  }
})

const secretInputType = computed(() => {
  if (!secretInput.value.trim()) return 'text'
  return showHMACSecret.value ? 'text' : 'password'
})

const canAutoSave = computed(() => {
  if (!tenantId.value) return false
  if (form.mode === 'signed_token') {
    return config.value?.has_hmac_secret === true || secretInput.value.trim() !== ''
  }
  return true
})

const hasUnsavedSecretChange = computed(() => {
  const trimmed = secretInput.value.trim()
  if (!trimmed) return false
  return trimmed !== lastSavedSecretInput.value
})

const showSecretSavedHint = computed(() => {
  const trimmed = secretInput.value.trim()
  return trimmed !== '' && trimmed === lastSavedSecretInput.value
})

const hasUnsavedPrincipalChanges = computed(() => {
  const cfg = config.value
  if (!cfg) return false
  return (
    form.mode !== cfg.mode
    || form.require_direct_header !== cfg.require_direct_header
    || hasUnsavedSecretChange.value
  )
})

const tokenSignExample = computed(() => {
  const tid = tenantId.value || 10000
  const headerName = tokenHeaderName.value
  return `import (
\t"time"

\t"github.com/golang-jwt/jwt/v5"
)

func signExternalUserToken(hmacSecret, externalUserID string, tenantID uint64) (string, error) {
\tclaims := jwt.MapClaims{
\t\t"sub":       externalUserID, // e.g. "user_123"
\t\t"tenant_id": float64(tenantID),
\t\t"aud":       "genyee",
\t\t"exp":       time.Now().Add(time.Hour).Unix(),
\t}
\treturn jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
\t\tSignedString([]byte(hmacSecret))
}

// Send on each GENYEE API request:
//   ${headerName}: <JWT from signExternalUserToken>
// Tenant ID for this workspace: ${tid}`
})

const requestExample = computed(() => {
  const apiKeyHeader = `  -H "X-API-Key: ${apiKey.value ? '<API_KEY>' : '<YOUR_API_KEY>'}"`
  const contentType = '  -H "Content-Type: application/json"'
  const principalHeaders: string[] = []
  if (form.mode === 'direct_header') {
    principalHeaders.push(`  -H "${directHeaderName.value}: user_123"`)
  }
  if (form.mode === 'signed_token') {
    principalHeaders.push(`  -H "${tokenHeaderName.value}: ${t('integrations.api.requestExampleJwtPlaceholder')}"`)
  }
  const commonHeaders = [apiKeyHeader, contentType, ...principalHeaders].join(' \\\n')
  const agentID = playground.agent_id || BUILTIN_SMART_REASONING_ID

  const lines: string[] = []
  if (form.mode === 'signed_token') {
    lines.push(
      t('integrations.api.signedRequestStep0', { tenantId: tenantId.value || '<tenant_id>' }),
      t('integrations.api.signedRequestStep0Hint', { headerName: tokenHeaderName.value }),
      '',
    )
  }
  lines.push(
    t('integrations.api.requestExampleCreateSession'),
    `curl -X POST ${apiBaseUrl.value}/sessions \\`,
    commonHeaders,
    `  -d '{}'`,
    '',
    t('integrations.api.requestExampleAgentChat'),
    `curl -N -X POST ${apiBaseUrl.value}/agent-chat/<session_id> \\`,
    commonHeaders,
    `  -d '{"query":"hello","agent_enabled":true,"agent_id":"${agentID}","channel":"api"}'`,
  )
  return lines.join('\n')
})

const activeExampleText = computed(() => (
  form.mode === 'signed_token' && exampleTab.value === 'jwt'
    ? tokenSignExample.value
    : requestExample.value
))

const activeExampleLabel = computed(() => (
  form.mode === 'signed_token' && exampleTab.value === 'jwt'
    ? t('integrations.api.tokenSignExample')
    : t('integrations.api.requestExample')
))

async function load() {
  loading.value = true
  error.value = ''
  try {
    await loadAgents()
    const activeTenantId = authStore.effectiveTenantId
    if (!activeTenantId) {
      throw new Error(t('integrations.api.loadFailed'))
    }
    tenantId.value = Number(activeTenantId)
    await Promise.all([
      loadAPIKeys(),
      loadKnowledgeBaseOptions(),
    ])

    const cfgResp = await getAPIPrincipalConfig(tenantId.value)
    if (!cfgResp.success || !cfgResp.data) {
      throw new Error(cfgResp.message || t('integrations.api.loadFailed'))
    }
    config.value = cfgResp.data
    form.mode = cfgResp.data.mode || 'tenant'
    form.direct_header_name = DEFAULT_DIRECT_HEADER_NAME
    form.signed_token_header_name = DEFAULT_TOKEN_HEADER_NAME
    form.require_direct_header = cfgResp.data.require_direct_header === true
    secretInput.value = ''
    lastSavedSecretInput.value = ''
    ensurePlaygroundAgent()
  } catch (err: unknown) {
    error.value = getErrorMessage(err) || t('integrations.api.loadFailed')
  } finally {
    loading.value = false
  }
}

function handlePrincipalModeChange(mode: APIPrincipalMode) {
  form.mode = mode
  void saveIfNeeded()
}

function handleRequireDirectHeaderChange(checked: boolean) {
  form.require_direct_header = checked
  void saveIfNeeded()
}

function triggerAutoSave() {
  void saveIfNeeded()
}

function confirmGenerateSecret() {
  const dialog = DialogPlugin.confirm({
    header: t('integrations.api.hmacSecretResetConfirmTitle'),
    body: t('integrations.api.hmacSecretResetConfirmBody'),
    confirmBtn: { content: t('integrations.api.hmacSecretResetConfirmOk'), theme: 'danger' },
    cancelBtn: t('integrations.api.hmacSecretResetConfirmCancel'),
    onConfirm: async () => {
      await generateSecret()
      dialog.destroy()
    },
    onClose: () => dialog.destroy(),
  })
}

async function generateSecret() {
  const bytes = new Uint8Array(32)
  window.crypto.getRandomValues(bytes)
  secretInput.value = btoa(String.fromCharCode(...bytes))
  showHMACSecret.value = true
  await saveIfNeeded({ showSuccess: true })
}

async function saveIfNeeded(options: { showSuccess?: boolean } = {}) {
  if (!hasUnsavedPrincipalChanges.value) return true
  if (!canAutoSave.value) {
    MessagePlugin.error(t('integrations.api.autoSaveNeedSecret'))
    return false
  }
  saving.value = true
  try {
    const payload: Parameters<typeof updateAPIPrincipalConfig>[1] = {
      mode: form.mode,
      direct_header_name: DEFAULT_DIRECT_HEADER_NAME,
      signed_token_header_name: DEFAULT_TOKEN_HEADER_NAME,
      require_direct_header: form.require_direct_header,
    }
    const secretBeingSaved = hasUnsavedSecretChange.value ? secretInput.value.trim() : ''
    if (secretBeingSaved) {
      payload.hmac_secret = secretBeingSaved
    }
    const resp = await updateAPIPrincipalConfig(tenantId.value, payload)
    if (!resp.success || !resp.data) {
      throw new Error(resp.message || t('integrations.api.saveFailed'))
    }
    config.value = resp.data
    if (secretBeingSaved) {
      lastSavedSecretInput.value = secretBeingSaved
      showHMACSecret.value = true
    }
    if (options.showSuccess) {
      MessagePlugin.success(
        secretBeingSaved ? t('integrations.api.secretSavedCopyHint') : t('integrations.api.saveSuccess'),
      )
    }
    return true
  } catch (err: unknown) {
    MessagePlugin.error(getErrorMessage(err) || t('integrations.api.saveFailed'))
    return false
  } finally {
    saving.value = false
  }
}

async function copy(text: string) {
  await copyWithToast(text, 'integrations.api.copySuccess')
}

async function tryLoadWailsApiBaseURL() {
  const win = window as GENYEEDesktopWindow
  for (let i = 0; i < 40; i++) {
    const injected = win.__GENYEE_API_BASE__
    if (typeof injected === 'string' && injected.trim()) {
      wailsApiBaseURL.value = injected.trim().replace(/\/$/, '')
      await tryLoadWailsLanHints(win)
      return
    }
    const fn = win.go?.main?.App?.GetAPIBaseURL
    if (typeof fn === 'function') {
      try {
        const raw = await Promise.resolve(fn())
        if (typeof raw === 'string' && raw.trim()) {
          wailsApiBaseURL.value = raw.trim().replace(/\/$/, '')
        }
      } catch {
        /* binding error */
      }
      await tryLoadWailsLanHints(win)
      return
    }
    await new Promise((resolve) => setTimeout(resolve, 50))
  }
  await tryLoadWailsLanHints(win)
}

async function tryLoadWailsLanHints(win: GENYEEDesktopWindow) {
  const injectedLan = win.__GENYEE_API_LAN_BASE__
  if (typeof injectedLan === 'string' && injectedLan.trim()) {
    wailsApiLanBaseURL.value = injectedLan.trim().replace(/\/$/, '')
  }
  const fnLan = win.go?.main?.App?.GetAPILanBaseURL
  if (typeof fnLan === 'function' && !wailsApiLanBaseURL.value) {
    try {
      const raw = await Promise.resolve(fnLan())
      if (typeof raw === 'string' && raw.trim()) {
        wailsApiLanBaseURL.value = raw.trim().replace(/\/$/, '')
      }
    } catch {
      /* binding error */
    }
  }
  const fnAct = win.go?.main?.App?.GetDesktopListenPublicActive
  if (typeof fnAct === 'function') {
    try {
      desktopListenPublicActive.value = !!(await Promise.resolve(fnAct()))
    } catch {
      desktopListenPublicActive.value = false
    }
  }
}

function desktopPortBindingsAvailable(win: GENYEEDesktopWindow) {
  const app = win.go?.main?.App
  return typeof app?.GetDesktopHTTPPortSetting === 'function' && typeof app?.SetDesktopHTTPPortSetting === 'function'
}

function desktopBindPublicBindingsAvailable(win: GENYEEDesktopWindow) {
  const app = win.go?.main?.App
  return (
    typeof app?.GetDesktopHTTPBindPublicSetting === 'function'
    && typeof app?.SetDesktopHTTPBindPublicSetting === 'function'
  )
}

async function loadDesktopApiPrefs() {
  const win = window as GENYEEDesktopWindow
  if (desktopPortBindingsAvailable(win)) {
    showDesktopPortSetting.value = true
    try {
      const port = await Promise.resolve(win.go!.main!.App!.GetDesktopHTTPPortSetting!())
      desktopPortInput.value = typeof port === 'number' ? port : 0
    } catch {
      desktopPortInput.value = 0
    }
  }
  if (desktopBindPublicBindingsAvailable(win)) {
    showDesktopBindPublicSetting.value = true
    try {
      const bind = await Promise.resolve(win.go!.main!.App!.GetDesktopHTTPBindPublicSetting!())
      desktopBindPublicInput.value = !!bind
    } catch {
      desktopBindPublicInput.value = false
    }
  }
}

const onDesktopBindPublicChange = async (value: boolean) => {
  const next = value === true
  const fn = (window as GENYEEDesktopWindow).go?.main?.App?.SetDesktopHTTPBindPublicSetting
  if (typeof fn !== 'function') return
  try {
    await Promise.resolve(fn(next))
    MessagePlugin.success(t('tenant.api.desktopBindPublicSaved'))
  } catch (err: unknown) {
    MessagePlugin.error(err instanceof Error ? err.message : t('tenant.api.desktopBindPublicSaveFailed'))
    desktopBindPublicInput.value = !next
  }
}

const saveDesktopPort = async () => {
  const value = desktopPortInput.value
  const port = typeof value === 'number' && !Number.isNaN(value) ? Math.floor(value) : 0
  if (port < 0 || port > 65535) {
    MessagePlugin.warning(t('tenant.api.desktopPortInvalid'))
    return
  }
  const fn = (window as GENYEEDesktopWindow).go?.main?.App?.SetDesktopHTTPPortSetting
  if (typeof fn !== 'function') return
  try {
    await Promise.resolve(fn(port))
    MessagePlugin.success(t('tenant.api.desktopPortSaved'))
  } catch (err: unknown) {
    MessagePlugin.error(err instanceof Error ? err.message : t('tenant.api.desktopPortSaveFailed'))
  }
}

function openApiDoc() {
  const url = docsUrl('apiOverview')
  if (!url) return
  try {
    const parsed = new URL(url, window.location.origin)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return
    window.open(url, '_blank', 'noopener,noreferrer')
  } catch {
    return
  }
}

onMounted(async () => {
  await tryLoadWailsApiBaseURL()
  await loadDesktopApiPrefs()
  await load()
})
onBeforeUnmount(stopPlayground)
</script>

<style scoped lang="less">
.api-integration {
  width: 100%;
}

.state-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 160px;
  color: var(--gy-text-secondary);
}

.api-settings,
.settings-band {
  display: flex;
  flex-direction: column;
}

.settings-band {
  border-top: 1px solid var(--gy-border-subtle);
}

.principal-section {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 20px 0;
}

.principal-section__header {
  label {
    display: block;
    margin-bottom: 6px;
    color: var(--gy-text-primary);
    font-size: var(--gy-text-lg);
    font-weight: 600;
  }

  p {
    margin: 0;
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-md);
    line-height: 1.55;
  }

  p.principal-section__scope {
    margin-top: 6px;
    color: var(--gy-text-tertiary);
    font-size: var(--gy-text-sm);
  }
}

.mode-radio {
  width: fit-content;
  max-width: 100%;
}

.mode-detail {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}

.mode-callout {
  position: relative;
  padding: 12px 14px;
  border-radius: var(--gy-radius-md);
  border: 1px solid var(--gy-border-subtle);
  background: var(--gy-surface-subtle);
  overflow: hidden;

  &--warning {
    border-color: color-mix(in oklab, var(--gy-warning) 40%, var(--gy-border-subtle));
    background: var(--gy-warning-soft);
  }

  &__body {
    display: block;

    strong {
      display: block;
      margin-bottom: 5px;
      color: var(--gy-text-primary);
      font-size: var(--gy-text-md);
      font-weight: 600;
      line-height: 1.4;
    }

    p {
      margin: 0;
      color: var(--gy-text-secondary);
      font-size: var(--gy-text-sm);
      line-height: 1.6;
    }
  }
}

.principal-config {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-md);
  background: var(--gy-surface);
  overflow: hidden;
}

.config-row {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto;
  align-items: center;
  gap: 16px;
  padding: 14px 16px;

  & + & {
    border-top: 1px solid var(--gy-border-subtle);
  }

  &__text {
    min-width: 0;

    label {
      display: block;
      color: var(--gy-text-primary);
      font-size: var(--gy-text-md);
      font-weight: 600;
      line-height: 1.4;
    }

    p {
      margin: 5px 0 0;
      color: var(--gy-text-tertiary);
      font-size: var(--gy-text-sm);
      line-height: 1.5;
    }
  }

  &__action {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    min-width: 52px;
  }

  &--switch {
    align-items: flex-start;
  }

  &--secret {
    grid-template-columns: minmax(220px, 0.55fr) minmax(360px, 1fr);
    align-items: center;
  }
}

.secret-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.secret-control {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;

  .mono-input {
    flex: 1 1 auto;
    min-width: 0;
  }

  .secret-mono-input {
    :deep(.t-input__suffix) {
      display: none;
    }
  }

  :deep(.t-button) {
    flex: 0 0 auto;
  }
}

.secret-saved-hint {
  margin: 0;
  font-size: var(--gy-text-sm);
  line-height: 1.5;
  color: var(--gy-warning);
}

@media (max-width: 640px) {
  .config-row {
    grid-template-columns: 1fr;
    align-items: stretch;
    gap: 10px;

    &__action {
      justify-content: flex-start;
    }
  }

  .fixed-header-name {
    width: 100%;
  }
}

.examples {
  width: 100%;
}

.snippet-tabs {
  margin-bottom: 8px;

  :deep(.t-tabs__nav) {
    min-height: 36px;
  }

  :deep(.t-tabs__nav-item) {
    font-size: var(--gy-text-md);
    height: 36px;
    line-height: 36px;
    color: var(--gy-text-secondary);
  }

  :deep(.t-tabs__nav-item.t-is-active) {
    color: var(--gy-text-primary);
    font-weight: 500;
  }

  :deep(.t-tabs__bar) {
    background: var(--gy-brand);
  }
}

.code-panel {
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-md);
  background: var(--gy-surface-subtle);
  overflow: hidden;

  &__toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--gy-border-subtle);
    background: var(--gy-surface);
  }

  &__label {
    font-size: var(--gy-text-sm);
    font-weight: 500;
    color: var(--gy-text-secondary);
  }

  &__copy {
    flex-shrink: 0;
  }

  &__pre {
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    font-family: var(--gy-font-mono);
    font-size: var(--gy-text-sm);
    line-height: 1.5;
    color: var(--gy-text-primary);
    background: transparent;
  }
}

.mono-input :deep(input) {
  font-family: var(--gy-font-mono);
  font-size: var(--gy-text-sm);
}

.fixed-header-name {
  width: fit-content;
  max-width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-sm);
  background: var(--gy-surface-subtle);
  color: var(--gy-text-primary);
  font-family: var(--gy-font-mono);
  font-size: var(--gy-text-sm);
  line-height: 18px;
  overflow-wrap: anywhere;
}

.playground-entry {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 14px;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-md);
  background: var(--gy-surface-subtle);

  &__info {
    min-width: 0;

    label {
      display: block;
      margin-bottom: 4px;
      color: var(--gy-text-primary);
      font-size: var(--gy-text-md);
      font-weight: 500;
    }

    p {
      margin: 0;
      color: var(--gy-text-secondary);
      font-size: var(--gy-text-sm);
      line-height: 1.5;
    }
  }
}

@media (max-width: 780px) {
  .mode-radio {
    width: 100%;

    :deep(.t-radio-group) {
      display: flex;
      width: 100%;
    }

    :deep(.t-radio-button) {
      flex: 1 1 0;
      min-width: 0;
    }
  }

  .playground-entry {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
