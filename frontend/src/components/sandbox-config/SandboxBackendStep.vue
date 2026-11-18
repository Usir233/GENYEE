<template>
  <section class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionBasic') }}</h4>
    <t-form-item :label="$t('settings.sandbox.backendType')">
      <t-select
        :value="backend"
        :placeholder="$t('settings.sandbox.backendTypePlaceholder')"
        class="backend-select"
        :popup-props="{ overlayClassName: 'sandbox-backend-popup' }"
        :disabled="retargetFrozen"
        @change="(v: unknown) => emit('select-backend', String(v ?? ''))"
      >
        <t-option v-for="opt in backendOptions" :key="opt" :value="opt" :label="backendLabel(opt)">
          <span class="backend-choice">
            <SandboxBackendBadge :type="opt" size="sm" />
            <span class="backend-choice__text">
              <span class="backend-choice__name">{{ backendLabel(opt) }}</span>
              <span class="backend-choice__desc">
                {{ $t(`settings.sandbox.backendDescriptions.${opt}`) }}
              </span>
            </span>
          </span>
        </t-option>
      </t-select>
      <p v-if="backend" class="section-help section-help--field">
        {{ $t(`settings.sandbox.backendDescriptions.${backend}`) }}
      </p>
    </t-form-item>
    <t-alert
      v-if="backend === 'docker' && !dockerBackendEnabled"
      theme="warning"
      class="compact-alert"
      :message="$t('settings.sandbox.dockerDisabledAlert')"
    >
      <template #description>
        <p>{{ $t('settings.sandbox.dockerDisabledHint') }}</p>
      </template>
    </t-alert>
    <t-form-item
      :label="$t('settings.sandbox.configName')"
      :status="nameError ? 'error' : undefined"
      :tips="nameError || undefined"
    >
      <t-input
        :model-value="name"
        :placeholder="$t('settings.sandbox.configNamePlaceholder')"
        @update:model-value="(v: string | number) => emit('update:name', String(v))"
      />
    </t-form-item>
    <t-form-item :label="$t('settings.sandbox.configDescription')">
      <t-input
        :model-value="description"
        :placeholder="$t('settings.sandbox.configDescriptionPlaceholder')"
        @update:model-value="(v: string | number) => emit('update:description', String(v))"
      />
    </t-form-item>
  </section>

  <section v-if="isRemoteBackend" class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionConnection') }}</h4>
    <t-alert
      v-if="hasSkillSnapshot"
      theme="info"
      class="identity-hint compact-alert"
      :message="$t('settings.sandbox.connectionLockedBySkills')"
    />
    <t-alert
      v-else-if="hasInFlightSkill"
      theme="info"
      class="identity-hint compact-alert"
      :message="$t('settings.sandbox.connectionLockedByInFlight')"
    />
    <t-alert
      v-else-if="hasRecord"
      theme="info"
      class="identity-hint compact-alert"
      :message="$t('settings.sandbox.identityFieldHint')"
    />

    <template v-if="backend === 'cube'">
      <t-form-item :label="requiredLabel('apiUrl')" :status="fieldStatus('api_url')" :tips="fieldTip('api_url')">
        <t-input
          :model-value="cube.api_url"
          placeholder="http://cube.example.com:33000"
          :disabled="retargetFrozen"
          @update:model-value="(v: string | number) => updateCube('api_url', String(v))"
          @input="emit('connection-input', 'api_url')"
        />
      </t-form-item>
      <div class="form-grid form-grid--two">
        <t-form-item
          :label="requiredLabel('proxyUrl')"
          :status="fieldStatus('proxy_url')"
          :tips="fieldTip('proxy_url')"
        >
          <t-input
            :model-value="cube.proxy_url"
            placeholder="http://cube.example.com:80"
            :disabled="retargetFrozen"
            @update:model-value="(v: string | number) => updateCube('proxy_url', String(v))"
            @input="emit('connection-input', 'proxy_url')"
          />
        </t-form-item>
        <t-form-item
          :label="requiredLabel('sandboxDomain')"
          :status="fieldStatus('sandbox_domain')"
          :tips="fieldTip('sandbox_domain')"
        >
          <t-input
            :model-value="cube.sandbox_domain"
            placeholder="cube.app"
            :disabled="retargetFrozen"
            @update:model-value="(v: string | number) => updateCube('sandbox_domain', String(v))"
            @input="emit('connection-input', 'sandbox_domain')"
          />
        </t-form-item>
      </div>
      <t-form-item :label="$t('settings.sandbox.apiKey')">
        <t-input
          :model-value="cube.api_key"
          type="password"
          :placeholder="secretInputPlaceholder('cube')"
          :disabled="retargetFrozen"
          @update:model-value="(v: string | number) => updateCube('api_key', String(v))"
          @input="emit('invalidate-connection')"
        />
        <div class="field-hints">
          <p class="section-help">
            {{ storedSecrets.cube
              ? $t('settings.sandbox.secretConfigured')
              : $t('settings.sandbox.cubeApiKeyOptional') }}
          </p>
          <a
            v-if="clusterGuideUrl"
            class="inline-guide-link"
            :href="clusterGuideUrl"
            target="_blank"
            rel="noopener noreferrer"
          >
            <t-icon name="link" />
            {{ $t('settings.sandbox.cubeApiKeyWhere') }}
          </a>
        </div>
      </t-form-item>
      <t-form-item
        :label="$t('settings.sandbox.cubeDnsServers')"
        :help="$t('settings.sandbox.cubeDnsServersHelp')"
      >
        <t-tag-input
          :model-value="cube.dns_servers"
          :placeholder="$t('settings.sandbox.cubeDnsServersPlaceholder')"
          :disabled="retargetFrozen"
          clearable
          @update:model-value="(v: Array<string | number>) => { updateCubeDns(v.map(String)); emit('invalidate-connection') }"
        />
      </t-form-item>
    </template>

    <template v-else-if="backend === 'e2b'">
      <t-form-item
        :label="requiredLabel('apiKey')"
        :status="fieldStatus('api_key')"
        :tips="fieldTip('api_key')"
      >
        <t-input
          :model-value="e2b.api_key"
          type="password"
          :placeholder="secretInputPlaceholder('e2b')"
          :disabled="retargetFrozen"
          @update:model-value="(v: string | number) => updateE2b('api_key', String(v))"
          @input="emit('connection-input', 'api_key')"
        />
        <div class="field-hints">
          <p class="section-help">
            {{ storedSecrets.e2b
              ? $t('settings.sandbox.secretConfigured')
              : $t('settings.sandbox.e2bApiKeyHelp') }}
          </p>
          <a class="inline-guide-link" :href="e2bApiKeysUrl" target="_blank" rel="noopener noreferrer">
            <t-icon name="link" />
            {{ $t('settings.sandbox.e2bApiKeyWhere') }}
          </a>
        </div>
      </t-form-item>
      <div class="form-grid form-grid--two">
        <t-form-item :label="$t('settings.sandbox.apiUrl')" :help="$t('settings.sandbox.e2bApiUrlOptional')">
          <t-input
            :model-value="e2b.api_url"
            placeholder="https://api.e2b.app"
            :disabled="retargetFrozen"
            @update:model-value="(v: string | number) => updateE2b('api_url', String(v))"
            @input="emit('invalidate-connection')"
          />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.sandboxDomain')" :help="$t('settings.sandbox.e2bDomainOptional')">
          <t-input
            :model-value="e2b.sandbox_domain"
            placeholder="e2b.app"
            :disabled="retargetFrozen"
            @update:model-value="(v: string | number) => updateE2b('sandbox_domain', String(v))"
            @input="emit('invalidate-connection')"
          />
        </t-form-item>
      </div>
      <t-form-item :label="$t('settings.sandbox.proxyUrl')" :help="$t('settings.sandbox.e2bProxyUrlOptional')">
        <t-input
          :model-value="e2b.proxy_url"
          placeholder="http://sandbox-gateway.example.com"
          :disabled="retargetFrozen"
          @update:model-value="(v: string | number) => updateE2b('proxy_url', String(v))"
          @input="emit('invalidate-connection')"
        />
      </t-form-item>
    </template>
    <div class="private-endpoint-row">
      <div>
        <p class="private-endpoint-row__title">{{ $t('settings.sandbox.allowPrivateEndpoints') }}</p>
        <p class="section-help">{{ $t('settings.sandbox.allowPrivateEndpointsHint') }}</p>
      </div>
      <t-switch
        :model-value="allowPrivateEndpoints"
        :disabled="retargetFrozen"
        @update:model-value="(v: string | number | boolean) => { emit('update:allowPrivateEndpoints', Boolean(v)); emit('invalidate-connection') }"
      />
    </div>
  </section>

  <section v-if="!isRemoteBackend" class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionRuntimeEnvironment') }}</h4>
    <div class="genyee-template-card" :class="{ 'is-active': isStandardTemplateImage(docker.image) }">
      <SandboxBackendBadge type="docker" />
      <div class="genyee-template-card__content">
        <div class="genyee-template-card__title-row">
          <span class="genyee-template-card__title">{{ $t('settings.sandbox.genyeeDockerImage') }}</span>
          <t-tag theme="primary" variant="light" size="small">{{ $t('settings.sandbox.recommendedTag') }}</t-tag>
        </div>
        <p>{{ $t('settings.sandbox.genyeeDockerImageHint') }}</p>
      </div>
    </div>
    <t-form-item :label="requiredLabel('dockerImage')" :status="fieldStatus('image')" :tips="fieldTip('image')">
      <t-input
        :model-value="docker.image"
        :placeholder="defaultDockerImage"
        :disabled="retargetFrozen"
        @update:model-value="(v: string | number) => updateDocker('image', String(v))"
        @input="emit('field-input', 'image')"
      />
      <p v-if="retargetFrozen" class="section-help section-help--field">
        {{ hasSkillSnapshot
          ? $t('settings.sandbox.templateLockedBySkills')
          : $t('settings.sandbox.templateLockedByInFlight') }}
      </p>
    </t-form-item>
    <t-form-item :label="$t('settings.sandbox.dockerHost')" :help="$t('settings.sandbox.dockerHostHelp')">
      <t-input
        :model-value="docker.host"
        placeholder="unix:///var/run/docker.sock"
        :disabled="retargetFrozen"
        @update:model-value="(v: string | number) => updateDocker('host', String(v))"
        @input="emit('field-input', 'host')"
      />
    </t-form-item>
    <t-form-item
      :label="$t('settings.sandbox.dockerTlsCertPath')"
      :help="$t('settings.sandbox.dockerTlsCertPathHelp')"
    >
      <t-input
        :model-value="docker.tls_cert_path"
        placeholder="/etc/genyee/docker-certs"
        :disabled="retargetFrozen"
        @update:model-value="(v: string | number) => updateDocker('tls_cert_path', String(v))"
        @input="emit('field-input', 'tls_cert_path')"
      />
    </t-form-item>
    <t-alert theme="warning" class="compact-alert" :message="$t('settings.sandbox.dockerHostRisk')" />
    <div class="private-endpoint-row">
      <div>
        <p class="private-endpoint-row__title">{{ $t('settings.sandbox.allowPrivateEndpoints') }}</p>
        <p class="section-help">{{ $t('settings.sandbox.allowPrivateEndpointsHint') }}</p>
      </div>
      <t-switch
        :model-value="allowPrivateEndpoints"
        :disabled="retargetFrozen"
        @update:model-value="(v: string | number | boolean) => { emit('update:allowPrivateEndpoints', Boolean(v)); emit('invalidate-connection') }"
      />
    </div>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import type { SandboxCubeConfig, SandboxDockerConfig, SandboxE2BConfig } from '@/api/system'

const props = defineProps<{
  backend: string
  backendOptions: string[]
  dockerBackendEnabled: boolean
  isRemoteBackend: boolean
  retargetFrozen: boolean
  hasSkillSnapshot: boolean
  hasInFlightSkill: boolean
  hasRecord: boolean
  name: string
  nameError: string
  description: string
  allowPrivateEndpoints: boolean
  cube: SandboxCubeConfig
  e2b: SandboxE2BConfig
  docker: SandboxDockerConfig
  storedSecrets: { cube: boolean; e2b: boolean }
  fieldErrors: Record<string, string>
  defaultDockerImage: string
  isStandardTemplateImage: (image?: string) => boolean
  clusterGuideUrl: string
  e2bApiKeysUrl: string
  updateCube: (key: 'api_url' | 'proxy_url' | 'sandbox_domain' | 'api_key', value: string) => void
  updateCubeDns: (servers: string[]) => void
  updateE2b: (key: 'api_key' | 'api_url' | 'sandbox_domain' | 'proxy_url', value: string) => void
  updateDocker: (key: 'image' | 'host' | 'tls_cert_path', value: string) => void
}>()

const emit = defineEmits<{
  (e: 'select-backend', value: string): void
  (e: 'update:name', value: string): void
  (e: 'update:description', value: string): void
  (e: 'update:allowPrivateEndpoints', value: boolean): void
  (e: 'field-input', field: string): void
  (e: 'connection-input', field: string): void
  (e: 'invalidate-connection'): void
}>()

const { t } = useI18n()

const backendLabel = (value: string) => t(`settings.sandbox.backends.${value}`)
const requiredLabel = (labelKey: string) => `${t(`settings.sandbox.${labelKey}`)} *`
const fieldStatus = (field: string): 'error' | undefined => (props.fieldErrors[field] ? 'error' : undefined)
const fieldTip = (field: string): string | undefined => props.fieldErrors[field] || undefined
const secretInputPlaceholder = (target: 'cube' | 'e2b') => (
  props.storedSecrets[target] ? t('settings.sandbox.secretKeepHint') : t('settings.sandbox.apiKeyPlaceholder')
)
</script>

<style lang="less" scoped src="./sandbox-config-editor-drawer.less"></style>
