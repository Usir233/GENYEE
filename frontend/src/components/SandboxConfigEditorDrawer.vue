<template>
  <SettingDrawer
    class="sandbox-config-drawer"
    :visible="visible"
    :title="record ? $t('settings.sandbox.editTitle') : $t('settings.sandbox.createTitle')"
    :description="stepDescription"
    icon="code"
    width="680px"
    :min-width="560"
    :max-width="920"
    storage-key="setting-drawer:width:sandbox-config-v2"
    :confirm-loading="saving || checking || templatesLoading"
    :confirm-disabled="primaryDisabled"
    :confirm-text="primaryText"
    @confirm="handlePrimaryAction"
    @cancel="close"
    @update:visible="(v: boolean) => emit('update:visible', v)"
  >
    <template #footer-left>
      <t-button v-if="wizardStep > 0" variant="outline" @click="previousStep">
        {{ $t('settings.sandbox.back') }}
      </t-button>
      <t-popconfirm
        v-if="canDeepCheck"
        :content="$t('settings.sandbox.deepCheckConfirm')"
        @confirm="runCheck(true)"
      >
        <t-button variant="outline" :loading="checking">
          {{ lastCheckWasDeep ? $t('settings.sandbox.recheck') : $t('settings.sandbox.deepCheck') }}
        </t-button>
      </t-popconfirm>
    </template>

    <template #header-extra>
      <nav class="sandbox-steps" :aria-label="$t('settings.sandbox.setupProgress')">
        <component
          :is="canJumpTo(index) ? 'button' : 'div'"
          v-for="(item, index) in wizardSteps"
          :key="item.key"
          :type="canJumpTo(index) ? 'button' : undefined"
          :class="['sandbox-step', {
            'is-active': wizardStep === index,
            'is-done': wizardStep > index,
            'is-clickable': canJumpTo(index),
          }]"
          :aria-current="wizardStep === index ? 'step' : undefined"
          @click="goToStep(index)"
        >
          <span class="sandbox-step__marker">
            <t-icon v-if="wizardStep > index" name="check" />
            <template v-else>{{ index + 1 }}</template>
          </span>
          <span class="sandbox-step__title">{{ item.title }}</span>
          <span v-if="index < wizardSteps.length - 1" class="sandbox-step__line" aria-hidden="true" />
        </component>
      </nav>
    </template>

    <div v-if="conflict" ref="conflictAlertRef" class="blocked blocked-top">
      <t-alert
        v-if="conflict.code === 'sandboxes_still_live'"
        theme="warning"
        :message="$t('settings.sandbox.sandboxesStillLive', { count: conflict.inventory?.sandbox_count ?? 0 })"
      >
        <template #description>
          <p v-if="affectedSessionCount">{{ $t('settings.sandbox.affectedSessions', { count: affectedSessionCount }) }}</p>
          <p v-if="conflict.inventory?.agent_names?.length">
            {{ $t('settings.sandbox.affectedAgents', { names: conflict.inventory.agent_names.join(', ') }) }}
          </p>
          <p>{{ $t('settings.sandbox.blockedHint') }}</p>
        </template>
      </t-alert>
      <t-alert
        v-else-if="conflict.code === 'skill_snapshot_blocks_template'"
        theme="warning"
        :message="$t('settings.sandbox.templateLockedBySkills')"
      />
      <t-alert v-else theme="warning" :message="$t('settings.sandbox.unverifiableBlocked')">
        <template #description>
          <p>{{ $t('settings.sandbox.unverifiableSaveHint') }}</p>
        </template>
      </t-alert>
    </div>

    <t-form label-align="top" class="sandbox-editor-form">
      <SandboxBackendStep
        v-if="currentStepKey === 'connection'"
        v-model:name="name"
        v-model:description="description"
        v-model:allow-private-endpoints="allowPrivateEndpoints"
        :backend="backend"
        :backend-options="backendOptions"
        :docker-backend-enabled="dockerBackendEnabled"
        :is-remote-backend="isRemoteBackend"
        :retarget-frozen="retargetFrozen"
        :has-skill-snapshot="hasSkillSnapshot"
        :has-in-flight-skill="hasInFlightSkill"
        :has-record="Boolean(record)"
        :name-error="nameError"
        :cube="cube"
        :e2b="e2b"
        :docker="docker"
        :stored-secrets="storedSecrets"
        :field-errors="fieldErrors"
        :default-docker-image="defaultDockerImage"
        :is-standard-template-image="isStandardTemplateImage"
        :cluster-guide-url="clusterGuideUrl"
        :e2b-api-keys-url="e2bApiKeysUrl"
        :update-cube="updateCubeField"
        :update-cube-dns="updateCubeDnsServers"
        :update-e2b="updateE2bField"
        :update-docker="updateDockerField"
        @select-backend="selectBackend"
        @field-input="onFieldInput"
        @connection-input="onConnectionInput"
        @invalidate-connection="invalidateConnection"
      />

      <SandboxTemplateSelectorStep
        v-if="currentStepKey === 'template'"
        :templates="templates"
        :templates-loading="templatesLoading"
        :templates-loaded="templatesLoaded"
        :templates-error="templatesError"
        :current-template-id="currentTemplateId"
        :can-create-standard="canCreateStandard"
        :can-create-desktop="canCreateDesktop"
        :retarget-frozen="retargetFrozen"
        :has-skill-snapshot="hasSkillSnapshot"
        :has-in-flight-skill="hasInFlightSkill"
        :cluster-guide-url="clusterGuideUrl"
        :on-template-card-click="onTemplateCardClick"
        :can-rebuild-template="canRebuildTemplate"
        :template-display-name="templateDisplayName"
        :template-field-rows="templateFieldRows"
        :template-failure-reason="templateFailureReason"
        :template-status-theme="templateStatusTheme"
        :template-status-label="templateStatusLabel"
        :create-standard-template="createStandardTemplate"
        :create-desktop-template="createDesktopTemplate"
        :replace-first-party-template="replaceFirstPartyTemplate"
        @reload="loadTemplates()"
      />

      <section v-if="currentStepKey === 'runtime'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionRuntime') }}</h4>
        <div class="runtime-fields">
          <div class="form-grid form-grid--two">
            <template v-if="isRemoteBackend">
              <t-form-item
                :label="$t('settings.sandbox.httpTimeout')"
                :tips="$t('settings.sandbox.httpTimeoutHelp')"
              >
                <t-input-number
                  v-if="backend === 'cube'"
                  v-model="cube.http_timeout_sec"
                  :min="0"
                  theme="column"
                  placeholder="30"
                />
                <t-input-number
                  v-else
                  v-model="e2b.http_timeout_sec"
                  :min="0"
                  theme="column"
                  placeholder="30"
                />
              </t-form-item>
              <t-form-item
                :label="$t('settings.sandbox.sandboxTtl')"
                :tips="$t('settings.sandbox.sandboxTtlHelp')"
              >
                <t-input-number
                  v-if="backend === 'cube'"
                  v-model="cube.cube_sandbox_ttl_seconds"
                  :min="0"
                  theme="column"
                  placeholder="1800"
                />
                <t-input-number
                  v-else
                  v-model="e2b.e2b_sandbox_ttl_seconds"
                  :min="0"
                  theme="column"
                  placeholder="300"
                />
              </t-form-item>
            </template>
            <template v-if="backend === 'docker'">
              <t-form-item
                :label="$t('settings.sandbox.dockerIdleTtl')"
                :tips="$t('settings.sandbox.dockerIdleTtlHelp')"
              >
                <t-input-number
                  v-model="docker.idle_ttl_seconds"
                  :min="0"
                  theme="column"
                  placeholder="1800"
                />
              </t-form-item>
              <t-form-item
                :label="$t('settings.sandbox.dockerCpuLimit')"
                :tips="$t('settings.sandbox.dockerCpuLimitHelp')"
              >
                <t-input-number
                  v-model="docker.cpu_limit"
                  :min="0"
                  :step="0.5"
                  theme="column"
                  placeholder="2"
                />
              </t-form-item>
              <t-form-item
                :label="$t('settings.sandbox.dockerMemoryLimit')"
                :tips="$t('settings.sandbox.dockerMemoryLimitHelp')"
              >
                <t-input-number
                  v-model="docker.memory_limit_mb"
                  :min="0"
                  theme="column"
                  placeholder="2048"
                />
              </t-form-item>
              <t-form-item
                :label="$t('settings.sandbox.dockerPidsLimit')"
                :tips="$t('settings.sandbox.dockerPidsLimitHelp')"
              >
                <t-input-number
                  v-model="docker.pids_limit"
                  :min="0"
                  theme="column"
                  placeholder="512"
                />
              </t-form-item>
            </template>
            <t-form-item
              :label="$t('settings.sandbox.defaultTimeout')"
              :tips="$t('settings.sandbox.defaultTimeoutHelp')"
            >
              <t-input-number v-model="defaultTimeoutSec" :min="0" theme="column" placeholder="60" />
            </t-form-item>
            <t-form-item
              :label="$t('settings.sandbox.terminalIdleDisconnect')"
              :tips="$t('settings.sandbox.terminalIdleDisconnectHelp')"
            >
              <t-input-number
                v-model="terminalIdleDisconnectSec"
                :min="0"
                :max="86400"
                theme="column"
                placeholder="900"
              />
            </t-form-item>
          </div>
        </div>
      </section>

      <SandboxNetworkPolicyEditor
        v-if="currentStepKey === 'runtime'"
        v-model:deny-egress-by-default="denyEgressByDefault"
        :backend="backend"
        :docker="docker"
        :set-docker-network-mode="setDockerNetworkMode"
        :allow-out-rows="allowOutRows"
        :deny-out-rows="denyOutRows"
        :cube-rules="cubeRules"
        :e2b-host-rules="e2bHostRules"
        :domain-allow-needs-deny-all="domainAllowNeedsDenyAll"
        :add-allow-out-row="addAllowOutRow"
        :set-allow-out-row="setAllowOutRow"
        :remove-allow-out-row="removeAllowOutRow"
        :add-deny-out-row="addDenyOutRow"
        :set-deny-out-row="setDenyOutRow"
        :remove-deny-out-row="removeDenyOutRow"
        :add-cube-rule="addCubeRule"
        :move-cube-rule="moveCubeRule"
        :remove-cube-rule="removeCubeRule"
        :add-e2-b-host-rule="addE2BHostRule"
        :remove-e2-b-host-rule="removeE2BHostRule"
      />

      <section v-if="currentStepKey === 'runtime'" class="setting-drawer__section">
        <div class="section-title-row">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionEnvironment') }}</h4>
          <t-button variant="text" size="small" @click="envRows.push({ key: '', value: '' })">
            <template #icon><t-icon name="add" /></template>
            {{ $t('settings.sandbox.addRow') }}
          </t-button>
        </div>
        <p class="section-help section-help--under-title">{{ $t('settings.sandbox.envVarsHint') }}</p>
        <div v-if="envRows.length" class="env-rows">
          <div v-for="(row, index) in envRows" :key="index" class="env-row">
            <t-input v-model="row.key" :placeholder="$t('settings.sandbox.envKey')" class="env-key" />
            <t-input
              v-model="row.value"
              type="password"
              :placeholder="isStoredEnvRowRecoverable(row) ? $t('settings.sandbox.secretKeepHint') : $t('settings.sandbox.envValue')"
              class="env-value"
            />
            <t-button
              variant="text"
              shape="square"
              size="small"
              :aria-label="$t('common.delete')"
              @click="envRows.splice(index, 1)"
            >
              <t-icon name="close" />
            </t-button>
          </div>
        </div>
        <div v-else class="env-empty">{{ $t('settings.sandbox.noEnvVars') }}</div>
      </section>

      <section v-if="currentStepKey === 'runtime' && allocationMode === 'session'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillRollout') }}</h4>
        <p class="section-help section-help--under-title">{{ $t('settings.sandbox.skillRolloutHint') }}</p>
        <t-radio-group v-model="skillRollout" class="skill-rollout-group">
          <t-radio value="next_turn">{{ $t('settings.sandbox.skillRolloutNextTurn') }}</t-radio>
          <t-radio value="new_session">{{ $t('settings.sandbox.skillRolloutNewSession') }}</t-radio>
        </t-radio-group>
      </section>

      <section v-if="currentStepKey === 'runtime'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.allocationMode') }}</h4>
        <p class="section-help section-help--under-title">{{ $t('settings.sandbox.allocationModeHint') }}</p>
        <t-radio-group v-model="allocationMode" class="allocation-mode-group">
          <t-radio value="session">{{ $t('settings.sandbox.allocationModeSession') }}</t-radio>
          <t-radio value="user" :disabled="!userAllocationSupported && allocationMode !== 'user'">
            {{ $t('settings.sandbox.allocationModeUser') }}
          </t-radio>
        </t-radio-group>
        <p v-if="!userAllocationSupported" class="section-help">{{ $t('settings.sandbox.allocationModeUnavailable') }}</p>
        <template v-if="allocationMode === 'user'">
          <div ref="userAllocationPanelRef">
            <t-form-item :label="$t('settings.sandbox.userSandboxRetentionDays')">
              <t-input-number v-model="userSandboxRetentionDays" :min="1" :max="90" :step="1" />
            </t-form-item>
            <p class="section-help">{{ $t('settings.sandbox.userSandboxRetentionHint') }}</p>
          </div>
        </template>
      </section>
    </t-form>

    <div
      v-if="checkResult && showCheckResult"
      ref="checkResultRef"
      class="check-result"
    >
      <p :class="['check-result__title', checkResult.ok ? 'is-success' : 'is-error']">
        <t-icon :name="checkResult.ok ? 'check-circle-filled' : 'close-circle-filled'" />
        {{ checkResult.ok ? $t('settings.sandbox.checkPassed') : $t('settings.sandbox.checkFailed') }}
      </p>
      <p class="check-result__subtitle">{{ checkScopeHint }}</p>
      <ul class="check-list">
        <li v-for="item in reportedChecks" :key="item.name" class="check-item">
          <t-icon
            :name="item.ok === true ? 'check-circle-filled'
              : item.ok === false ? 'close-circle-filled' : 'minus-circle'"
            :class="item.ok === true ? 'ok' : item.ok === false ? 'err' : 'skip'"
          />
          <span class="check-name">{{ checkLabel(item.name) }}</span>
          <span v-if="item.latency_ms" class="check-latency">{{ item.latency_ms }} ms</span>
          <span v-if="checkDetail(item)" class="check-message">{{ checkDetail(item) }}</span>
        </li>
      </ul>
      <p v-if="pendingCheckNames.length" class="check-result__hint">
        {{ $t('settings.sandbox.checkPendingHint', { names: pendingCheckNames.join(', ') }) }}
      </p>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import SandboxBackendStep from './sandbox-config/SandboxBackendStep.vue'
import SandboxNetworkPolicyEditor from './sandbox-config/SandboxNetworkPolicyEditor.vue'
import SandboxTemplateSelectorStep from './sandbox-config/SandboxTemplateSelectorStep.vue'
import {
  isMaskedSecret,
  isStoredEnvRowRecoverable,
  secretPlaceholder,
  useSandboxNetworkRules,
} from './sandbox-config/useSandboxNetworkRules'
import {
  isTemplateSelectable,
  useSandboxTemplatesPoll,
} from './sandbox-config/useSandboxTemplatesPoll'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { docsUrl } from '@/utils/docsUrl'
import {
  checkSandboxConfig,
  createSandboxConfig,
  isNamedSandboxBackend,
  listConfigSkills,
  NAMED_SANDBOX_BACKEND_TYPES,
  parseSandboxConflict,
  updateSandboxConfigById,
  type SandboxCheckItem,
  type SandboxCheckResult,
  type SandboxConfig,
  type SandboxConfigRecord,
  type SandboxConflict,
  type SandboxCubeConfig,
  type SandboxDockerConfig,
  type SandboxE2BConfig,
} from '@/api/system'

type SandboxStepKey = 'connection' | 'template' | 'runtime'

const props = defineProps<{
  visible: boolean
  record: SandboxConfigRecord | null
  presetType?: string
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'saved'): void
}>()

const { t } = useI18n()
const deploymentCapabilities = useDeploymentCapabilitiesStore()
const dockerBackendEnabled = computed(() =>
  deploymentCapabilities.isSupported('settings.sandbox.docker'),
)
const userAllocationSupported = computed(() =>
  deploymentCapabilities.isSupported('settings.sandbox.user_allocation'),
)

// Mirrors DefaultDockerImage on the server.
const defaultDockerImage = 'genyee/genyee-sandbox:latest'

const standardDockerRepositories = new Set([
  'genyee/genyee-sandbox',
])

function isStandardTemplateImage(image?: string): boolean {
  const raw = (image ?? '').trim()
  if (!raw) return true
  const withoutDigest = raw.split('@')[0] ?? ''
  const lastSlash = withoutDigest.lastIndexOf('/')
  const lastColon = withoutDigest.lastIndexOf(':')
  const repo = (lastColon > lastSlash ? withoutDigest.slice(0, lastColon) : withoutDigest).toLowerCase()
  return standardDockerRepositories.has(repo) || repo.endsWith('/genyee-sandbox')
}

function safeGuideUrl(raw: string): string {
  const trimmed = String(raw || '').trim()
  if (!trimmed) return ''
  if (/^\/(?!\/)/.test(trimmed)) return trimmed
  if (!/^https?:\/\//i.test(trimmed)) return ''
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? trimmed : ''
  } catch {
    return ''
  }
}

const clusterGuideUrl = safeGuideUrl(docsUrl('sandboxDeployment'))
const e2bApiKeysUrl = 'https://e2b.dev/dashboard?tab=keys'

const backend = ref('')
const backendOptions = computed(() => {
  const types = [...NAMED_SANDBOX_BACKEND_TYPES]
  if (dockerBackendEnabled.value || backend.value === 'docker') return types
  return types.filter((type) => type !== 'docker')
})

const saving = ref(false)
const checking = ref(false)
const checkResult = ref<SandboxCheckResult | null>(null)
const lastCheckWasDeep = ref(false)
const conflict = ref<SandboxConflict | null>(null)
const conflictAlertRef = ref<HTMLElement | null>(null)
const checkResultRef = ref<HTMLElement | null>(null)
const nameError = ref('')

const name = ref('')
const description = ref('')
const defaultTimeoutSec = ref<number | undefined>(undefined)
const terminalIdleDisconnectSec = ref<number | undefined>(undefined)
const allowPrivateEndpoints = ref(false)
const cube = reactive<SandboxCubeConfig>({})
const e2b = reactive<SandboxE2BConfig>({})
const docker = reactive<SandboxDockerConfig>({})
const storedSecrets = reactive({ cube: false, e2b: false })
const fieldErrors = ref<Record<string, string>>({})
let drawerSessionEpoch = 0
let checkReqEpoch = 0

const skillRollout = ref<'next_turn' | 'new_session'>('next_turn')
const allocationMode = ref<'session' | 'user'>('session')
const userAllocationPanelRef = ref<HTMLElement | null>(null)

watch(allocationMode, (mode, previous) => {
  if (mode !== 'user' || previous === 'user') return
  void nextTick(() => {
    requestAnimationFrame(() => {
      userAllocationPanelRef.value?.scrollIntoView({ behavior: 'smooth', block: 'end', inline: 'nearest' })
    })
  })
})
const userSandboxRetentionDays = ref<number>(7)

const {
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
} = useSandboxNetworkRules(backend)

function updateCubeField(key: 'api_url' | 'proxy_url' | 'sandbox_domain' | 'api_key', value: string) {
  cube[key] = value
}

function updateCubeDnsServers(servers: string[]) {
  cube.dns_servers = servers
}

function updateE2bField(key: 'api_key' | 'api_url' | 'sandbox_domain' | 'proxy_url', value: string) {
  e2b[key] = value
}

function updateDockerField(key: 'image' | 'host' | 'tls_cert_path', value: string) {
  docker[key] = value
}

function setDockerNetworkMode(mode?: string) {
  docker.network_mode = mode
}

const inFlightFromSkills = ref(false)
const wizardStep = ref(0)

const isRemoteBackend = computed(() => backend.value === 'cube' || backend.value === 'e2b')
const hasImageCatalog = computed(() => isRemoteBackend.value || backend.value === 'docker')
const wizardSteps = computed<Array<{ key: SandboxStepKey; title: string }>>(() => {
  const steps: Array<{ key: SandboxStepKey; title: string }> = [
    { key: 'connection', title: t('settings.sandbox.stepConnection') },
  ]
  if (isRemoteBackend.value) {
    steps.push({ key: 'template', title: t('settings.sandbox.stepTemplate') })
  }
  steps.push({ key: 'runtime', title: t('settings.sandbox.stepRuntime') })
  return steps
})
const currentStepKey = computed<SandboxStepKey>(() => wizardSteps.value[wizardStep.value]?.key || 'connection')
const stepDescription = computed(() => t(`settings.sandbox.stepDescriptions.${currentStepKey.value}`))
const primaryText = computed(() => {
  if (currentStepKey.value === 'connection') return t('settings.sandbox.connectAndContinue')
  if (currentStepKey.value === 'template') return t('common.next')
  return t('common.save')
})

const savedRecord = ref<SandboxConfigRecord | null>(null)
const effectiveRecord = computed(() => savedRecord.value || props.record)
const hasSkillSnapshot = computed(() => (
  Boolean(effectiveRecord.value?.config?.skill_image?.snapshot_id?.trim())
))
const hasInFlightSkill = computed(() => inFlightFromSkills.value)
const retargetFrozen = computed(() => hasSkillSnapshot.value || hasInFlightSkill.value)

function withStoredSecret<T extends { api_key?: string }>(block: T, stored: boolean): T {
  if (stored && !block.api_key?.trim()) block.api_key = secretPlaceholder
  return block
}

function submittedBackendValues(): Record<string, unknown> {
  if (backend.value === 'cube') return withStoredSecret({ ...cube }, storedSecrets.cube)
  if (backend.value === 'e2b') return withStoredSecret({ ...e2b }, storedSecrets.e2b)
  return { ...docker }
}

function connectionReady(): boolean {
  if (!isRemoteBackend.value) return true
  const required = backend.value === 'cube'
    ? ['api_url', 'proxy_url', 'sandbox_domain']
    : ['api_key']
  const values = submittedBackendValues()
  const errors: Record<string, string> = {}
  for (const field of required) {
    if (typeof values[field] !== 'string' || String(values[field]).trim() === '') {
      errors[field] = t('settings.sandbox.fieldRequired')
    }
  }
  fieldErrors.value = { ...fieldErrors.value, ...errors }
  return Object.keys(errors).length === 0
}

function invalidateCheck() {
  checkReqEpoch += 1
  checking.value = false
  checkResult.value = null
}

function onFieldInput(field: string) {
  delete fieldErrors.value[field]
  invalidateCheck()
}

const {
  templates,
  templatesLoading,
  templatesLoaded,
  templatesError,
  currentTemplateId,
  selectedTemplate,
  canCreateStandard,
  canCreateDesktop,
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
} = useSandboxTemplatesPoll({
  t: (key, params) => (params ? t(key, params) : t(key)),
  visible: () => props.visible,
  backend,
  cube,
  e2b,
  isRemoteBackend,
  hasImageCatalog,
  currentStepKey,
  retargetFrozen,
  effectiveRecord,
  connectionReady,
  collectPayload: () => collectPayload(),
  onFieldInput,
  clearTemplateFieldError: () => { delete fieldErrors.value.template_id },
})

const canDeepCheck = computed(() => {
  if (backend.value === 'docker' && !dockerBackendEnabled.value) return false
  if (currentStepKey.value === 'template') return true
  return currentStepKey.value === 'connection' && !isRemoteBackend.value
})
const showCheckResult = computed(() => canDeepCheck.value)
const primaryDisabled = computed(() => (
  (backend.value === 'docker' && !dockerBackendEnabled.value)
  || (
    currentStepKey.value === 'template'
    && (!selectedTemplate.value || !isTemplateSelectable(selectedTemplate.value))
  )
))

function canJumpTo(index: number): boolean {
  if (index === wizardStep.value) return false
  return Boolean(effectiveRecord.value) || index < wizardStep.value
}

function goToStep(index: number) {
  if (!canJumpTo(index)) return
  wizardStep.value = index
  if (currentStepKey.value !== 'template') {
    stopTemplatePolling()
    return
  }
  if (templatesLoaded.value) scheduleTemplatePolling()
  else void loadTemplates()
}

const checkLabel = (probe: string) => t(`settings.sandbox.checks.${probe}`, probe)
const PENDING_SKIP_REASON = 'needs_deep_check'

const reportedChecks = computed(() => (checkResult.value?.checks || []).filter(
  (item) => item.ok !== null || item.reason !== PENDING_SKIP_REASON,
))

const pendingCheckNames = computed(() => (checkResult.value?.checks || [])
  .filter((item) => item.ok === null && item.reason === PENDING_SKIP_REASON)
  .map((item) => checkLabel(item.name)))

const EGRESS_RESTRICTED_REASON = 'egress_restricted_by_policy'
const egressRestrictedByPolicy = computed(() => (checkResult.value?.checks || []).some(
  (item) => item.reason === EGRESS_RESTRICTED_REASON,
))

const checkScopeHint = computed(() => {
  if (pendingCheckNames.value.length) return t('settings.sandbox.checkScopeConnection')
  if (egressRestrictedByPolicy.value) return t('settings.sandbox.checkScopePolicyRestricted')
  return t('settings.sandbox.checkScopeFull')
})

function checkDetail(item: SandboxCheckItem): string {
  if (item.message) return item.message
  if (!item.reason) return ''
  return t(`settings.sandbox.skipReasons.${item.reason}`, item.reason)
}

const REQUIRED_FIELDS: Record<string, string[]> = {
  cube: ['api_url', 'proxy_url', 'sandbox_domain', 'template_id'],
  e2b: ['api_key', 'template_id'],
  docker: ['image'],
}

function validateRequiredFields(includeTemplate = true): boolean {
  const required = (REQUIRED_FIELDS[backend.value] || []).filter((field) => includeTemplate || field !== 'template_id')
  const values = submittedBackendValues()
  const errors: Record<string, string> = {}
  for (const field of required) {
    const value = values[field]
    if (typeof value !== 'string' || value.trim() === '') {
      errors[field] = t('settings.sandbox.fieldRequired')
    }
  }
  fieldErrors.value = errors
  return Object.keys(errors).length === 0
}

const affectedSessionCount = computed(() => conflict.value?.inventory?.session_ids?.length || 0)

function defaultBackendType(): string {
  const fromRecord = props.record?.config?.sandbox_type || props.presetType || ''
  if (isNamedSandboxBackend(fromRecord)) return fromRecord
  return 'cube'
}

function reset() {
  resetTemplatesState()
  const cfg: SandboxConfig = props.record?.config || {}
  name.value = props.record?.name || ''
  description.value = props.record?.description || ''
  backend.value = isNamedSandboxBackend(cfg.sandbox_type || '')
    ? cfg.sandbox_type!
    : defaultBackendType()
  defaultTimeoutSec.value = cfg.default_timeout_sec || undefined
  terminalIdleDisconnectSec.value = cfg.terminal_idle_disconnect_sec || undefined
  allowPrivateEndpoints.value = cfg.allow_private_endpoints === true
  Object.keys(cube).forEach((key) => delete (cube as Record<string, unknown>)[key])
  Object.keys(e2b).forEach((key) => delete (e2b as Record<string, unknown>)[key])
  Object.keys(docker).forEach((key) => delete (docker as Record<string, unknown>)[key])
  Object.assign(cube, cfg.cube || {})
  Object.assign(e2b, cfg.e2b || {})
  Object.assign(docker, cfg.docker || {})
  if (!Array.isArray(cube.dns_servers)) cube.dns_servers = []
  if (backend.value === 'docker' && !docker.image) {
    docker.image = defaultDockerImage
  }
  storedSecrets.cube = isMaskedSecret(cube.api_key)
  storedSecrets.e2b = isMaskedSecret(e2b.api_key)
  if (storedSecrets.cube) cube.api_key = ''
  if (storedSecrets.e2b) e2b.api_key = ''
  skillRollout.value = cfg.skill_rollout === 'new_session' ? 'new_session' : 'next_turn'
  allocationMode.value = cfg.allocation_mode === 'user' ? 'user' : 'session'
  userSandboxRetentionDays.value = cfg.user_sandbox_empty_retention_days || 7
  resetNetworkAndEnv(cfg)
  checkResult.value = null
  inFlightFromSkills.value = false
  checking.value = false
  drawerSessionEpoch += 1
  checkReqEpoch += 1
  conflict.value = null
  nameError.value = ''
  fieldErrors.value = {}
  savedRecord.value = null
  wizardStep.value = 0
}

function selectBackend(value: string) {
  if (backend.value === value) return
  backend.value = value
  if (value === 'docker' && !docker.image) {
    docker.image = defaultDockerImage
  }
  onBackendChange()
}

async function refreshInFlightSkill() {
  const id = props.record?.id
  const epoch = drawerSessionEpoch
  if (!id) {
    inFlightFromSkills.value = false
    return
  }
  try {
    const res = await listConfigSkills(id)
    if (epoch !== drawerSessionEpoch || !props.visible || props.record?.id !== id) return
    inFlightFromSkills.value = (res?.data || []).some(
      (skill) => skill.status === 'installing' || skill.status === 'removing',
    )
  } catch {
    if (epoch === drawerSessionEpoch) inFlightFromSkills.value = false
  }
}

watch(() => [props.visible, props.record?.id] as const, ([open]) => {
  if (open) {
    void deploymentCapabilities.ensureLoaded()
    reset()
    void refreshInFlightSkill()
  } else {
    drawerSessionEpoch += 1
    checkReqEpoch += 1
    checking.value = false
    resetTemplatesState()
    inFlightFromSkills.value = false
  }
})

function collectPayload(): SandboxConfig {
  const payload: SandboxConfig = {
    sandbox_type: backend.value,
    default_timeout_sec: defaultTimeoutSec.value || undefined,
    terminal_idle_disconnect_sec: terminalIdleDisconnectSec.value || undefined,
    allow_private_endpoints: allowPrivateEndpoints.value || undefined,
    desktop_enabled: collectedDesktopEnabled(),
    env_vars: collectEnvVars(),
    skill_rollout: skillRollout.value,
    allocation_mode: allocationMode.value,
    user_sandbox_empty_retention_days:
      allocationMode.value === 'user' ? (userSandboxRetentionDays.value || 7) : undefined,
    network: collectNetworkPolicy(),
  }
  if (backend.value === 'cube') payload.cube = withStoredSecret({ ...cube }, storedSecrets.cube)
  if (backend.value === 'e2b') payload.e2b = withStoredSecret({ ...e2b }, storedSecrets.e2b)
  if (backend.value === 'docker') payload.docker = { ...docker }
  return payload
}

function close() {
  stopTemplatePolling()
  emit('update:visible', false)
}

function validateName(): boolean {
  if (!name.value.trim()) {
    nameError.value = t('settings.sandbox.configNameRequired')
    return false
  }
  nameError.value = ''
  return true
}

async function handlePrimaryAction() {
  if (backend.value === 'docker' && !dockerBackendEnabled.value) return
  if (currentStepKey.value === 'connection') {
    if (!validateName() || !validateRequiredFields(false)) return
    if (!(await runCheck(false))) return
    if (isRemoteBackend.value) {
      invalidateCheck()
      wizardStep.value += 1
      await loadTemplates()
      return
    }
    if (backend.value === 'docker') {
      void loadTemplates({ ensureStandard: true })
    }
    invalidateCheck()
    wizardStep.value += 1
    return
  }
  if (currentStepKey.value === 'template') {
    if (!selectedTemplate.value || !isTemplateSelectable(selectedTemplate.value)) {
      fieldErrors.value.template_id = t('settings.sandbox.templateNotReady')
      return
    }
    stopTemplatePolling()
    wizardStep.value += 1
    return
  }
  await save()
}

function previousStep() {
  if (wizardStep.value <= 0) return
  wizardStep.value -= 1
  if (currentStepKey.value === 'template') scheduleTemplatePolling()
  else stopTemplatePolling()
}

async function save() {
  const trimmed = name.value.trim()
  if (!validateName()) return
  if (!validateRequiredFields()) return
  if (isRemoteBackend.value && selectedTemplate.value && !isTemplateSelectable(selectedTemplate.value)) {
    fieldErrors.value.template_id = t('settings.sandbox.templateNotReady')
    return
  }
  saving.value = true
  conflict.value = null
  try {
    const payload = { name: trimmed, description: description.value, config: collectPayload() }
    const existing = effectiveRecord.value
    const res = existing
      ? await updateSandboxConfigById(existing.id, payload)
      : await createSandboxConfig(payload)
    MessagePlugin.success(t('common.saveSuccess'))
    emit('saved')
    const saved = res?.data
    if (saved) savedRecord.value = saved
    close()
  } catch (err: unknown) {
    const refusal = parseSandboxConflict(err)
    if (refusal) {
      conflict.value = refusal
      await nextTick()
      conflictAlertRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      return
    }
    const msg = err && typeof err === 'object' && 'message' in err ? String((err as { message?: unknown }).message || '') : ''
    MessagePlugin.error(msg || t('settings.sandbox.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function runCheck(deep: boolean): Promise<boolean> {
  if (!validateRequiredFields(deep)) return false
  const epoch = ++checkReqEpoch
  checking.value = true
  checkResult.value = null
  try {
    const res = await checkSandboxConfig({
      config: collectPayload(),
      config_id: effectiveRecord.value?.id,
      deep,
    })
    if (epoch !== checkReqEpoch || !props.visible) return false
    checkResult.value = res?.data || null
    lastCheckWasDeep.value = deep
    if (checkResult.value) {
      await nextTick()
      checkResultRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
    return checkResult.value?.ok === true
  } catch (err: unknown) {
    if (epoch !== checkReqEpoch || !props.visible) return false
    const msg = err && typeof err === 'object' && 'message' in err ? String((err as { message?: unknown }).message || '') : ''
    MessagePlugin.error(msg || t('settings.sandbox.checkFailed'))
    return false
  } finally {
    if (epoch === checkReqEpoch) checking.value = false
  }
}

function onConnectionInput(field: string) {
  delete fieldErrors.value[field]
  invalidateConnection()
}

function invalidateConnection() {
  resetTemplatesState()
  invalidateCheck()
  clearTemplateSelection()
}

function onBackendChange() {
  fieldErrors.value = {}
  invalidateConnection()
}

onUnmounted(() => {
  drawerSessionEpoch += 1
  checkReqEpoch += 1
  stopTemplatePolling()
})
</script>

<style lang="less" scoped src="./sandbox-config/sandbox-config-editor-drawer.less"></style>

<style lang="less">
.sandbox-backend-popup {
  .t-select-option {
    height: auto;
    min-height: 44px;
    padding: 6px 8px;
    line-height: 1.4;
  }
}
</style>
