<template>
  <section v-if="step === 0" class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ t('datasource.step.selectType') }}</h4>
    <div class="ds-type-grid">
      <button
        v-for="def in connectorDefs"
        :key="def.type"
        type="button"
        :class="['ds-type-card', { disabled: !def.available }]"
        :disabled="!def.available"
        @click="emit('select-type', def)"
      >
        <div class="ds-type-header">
          <DataSourceTypeIcon :type="def.type" :size="20" />
          <span class="ds-type-name">{{ t(`datasource.connector.${def.type}`) }}</span>
          <span v-if="!def.available" class="ds-type-soon">{{ t('datasource.comingSoon') }}</span>
        </div>
        <div class="ds-type-desc">{{ t(`datasource.connectorDesc.${def.type}`) }}</div>
      </button>
    </div>
  </section>

  <template v-if="step === 1">
    <div
      v-if="currentDef && currentDef.requiredPermissions.length > 0"
      class="ds-setup-guide ds-setup-guide--standalone"
    >
      <button
        type="button"
        class="ds-setup-guide__toggle"
        :aria-expanded="prereqExpanded"
        @click="prereqExpanded = !prereqExpanded"
      >
        <t-icon name="info-circle-filled" size="15px" class="ds-setup-guide__icon" />
        <span class="ds-setup-guide__summary">
          {{ t(`datasource.prereqBarText_${form.type}`, t('datasource.prereqBarText')) }}
        </span>
        <t-icon
          :name="prereqExpanded ? 'chevron-up' : 'chevron-down'"
          size="14px"
          class="ds-setup-guide__chevron"
        />
      </button>
      <div v-if="prereqExpanded" class="ds-setup-guide__body">
        <ol class="ds-setup-steps">
          <li class="ds-setup-step">
            <span class="ds-setup-step__title">
              {{ t(`datasource.prereqStep1Brief_${form.type}`, t('datasource.prereqBotBrief')) }}
            </span>
            <span class="ds-setup-step__desc">
              {{ t(`datasource.prereqStep1Desc_${form.type}`, t('datasource.prereqBotDesc')) }}
            </span>
          </li>
          <li class="ds-setup-step">
            <span class="ds-setup-step__title">
              {{ t(`datasource.prereqStep2Brief_${form.type}`, t('datasource.prereqPermBrief')) }}
            </span>
            <span class="ds-setup-step__desc">
              <template v-if="!t(`datasource.prereqStep2Desc_${form.type}`)">
                <code
                  v-for="perm in currentDef.requiredPermissions"
                  :key="perm"
                  class="ds-perm-tag"
                >{{ perm }}</code>
              </template>
              <template v-else>{{ t(`datasource.prereqStep2Desc_${form.type}`) }}</template>
            </span>
          </li>
          <li class="ds-setup-step">
            <span class="ds-setup-step__title">
              {{ t(`datasource.prereqStep3Brief_${form.type}`, t('datasource.prereqMemberBrief')) }}
            </span>
            <span class="ds-setup-step__desc">
              {{ t(`datasource.prereqStep3Desc_${form.type}`, t('datasource.prereqMemberDesc')) }}
            </span>
          </li>
        </ol>
        <a
          v-if="currentDef.permissionPageUrl"
          :href="currentDef.permissionPageUrl"
          target="_blank"
          rel="noopener"
          class="doc-link ds-setup-guide__link"
        >
          {{ t(`datasource.prereqOpenConsole_${form.type}`, t('datasource.prereqOpenConsole')) }}
          <t-icon name="link" class="link-icon" />
        </a>
      </div>
    </div>

    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.sectionBasic') }}</h4>

      <div v-if="currentDef?.docUrl" class="inline-alert">
        <t-icon name="info-circle-filled" class="inline-alert__icon" />
        <span class="inline-alert__text">{{ t('datasource.docHint') }}</span>
        <a
          :href="currentDef.docUrl"
          target="_blank"
          rel="noopener"
          class="inline-alert__action doc-link"
        >
          {{ t('datasource.openDoc') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </div>

      <div class="form-item">
        <label class="form-label required">{{ t('datasource.nameLabel') }}</label>
        <t-input
          :value="form.name"
          :placeholder="t('datasource.namePlaceholder')"
          @change="(val: string) => emit('update:name', String(val ?? ''))"
        />
      </div>
    </section>

    <section v-if="form.type === 'rss'" class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.field.feedUrls') }}</h4>
      <div class="form-item">
        <label class="form-label required">{{ t('datasource.field.feedUrls') }}</label>
        <t-textarea
          :value="String(form.config.settings.feed_urls ?? '')"
          placeholder="https://example.com/feed.xml"
          :autosize="{ minRows: 2, maxRows: 6 }"
          autocomplete="off"
          spellcheck="false"
          @change="(val: string) => emit('update:setting', 'feed_urls', String(val ?? ''))"
        />
        <p class="form-desc">{{ t('datasource.field.feedUrlsHint') }}</p>
      </div>
    </section>

    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.credentialsLabel') }}</h4>

      <div v-if="isEdit && credentialsConfigured && !replaceCredentialsMode" class="form-item">
        <div
          class="credential-faux-input"
          :class="{ 'is-confirm-remove': pendingRemoveCredentials }"
          :title="pendingRemoveCredentials ? '' : t('credential.configured')"
        >
          <template v-if="pendingRemoveCredentials">
            <t-icon name="error-circle-filled" class="credential-status-icon warn" />
            <span class="credential-faux-text danger">{{ t('credential.confirmRemovePrompt') }}</span>
            <div class="credential-actions">
              <t-button size="small" variant="text" @click="emit('cancel-remove-credentials')">
                {{ t('common.cancel') }}
              </t-button>
              <span class="action-divider" />
              <t-button
                size="small"
                variant="text"
                theme="danger"
                :loading="removingCredentials"
                @click="emit('confirm-remove-credentials')"
              >
                {{ t('credential.confirmRemove') }}
              </t-button>
            </div>
          </template>
          <template v-else>
            <t-icon name="check-circle-filled" class="credential-status-icon success" />
            <span class="credential-faux-text">{{ t('credential.configured') }}</span>
            <div class="credential-actions">
              <t-button size="small" variant="text" @click="emit('enter-replace-credentials')">
                {{ t('credential.update') }}
              </t-button>
              <span class="action-divider" />
              <t-button size="small" variant="text" theme="danger" @click="emit('request-remove-credentials')">
                {{ t('credential.remove') }}
              </t-button>
            </div>
          </template>
        </div>
      </div>

      <div
        v-else-if="isEdit && !credentialsConfigured && !replaceCredentialsMode"
        class="form-item"
      >
        <div
          class="credential-faux-input is-empty"
          role="button"
          tabindex="0"
          @click="emit('enter-replace-credentials')"
          @keydown.enter.prevent="emit('enter-replace-credentials')"
          @keydown.space.prevent="emit('enter-replace-credentials')"
        >
          <t-icon name="lock-on" class="credential-status-icon muted" />
          <span class="credential-faux-text muted">{{ t('credential.unconfigured') }}</span>
          <div class="credential-actions">
            <t-button size="small" variant="text" theme="primary" @click.stop="emit('enter-replace-credentials')">
              {{ t('credential.configure') }}
            </t-button>
          </div>
        </div>
      </div>

      <template v-else-if="credentialsInputVisible">
        <div v-if="form.type === 'confluence'" class="form-item">
          <label class="form-label">{{ t('datasource.field.confluenceEdition') }}</label>
          <t-select
            :value="form.config.credentials.edition"
            @change="(val: string | number) => emit('update:credential', 'edition', String(val ?? 'server'))"
          >
            <t-option value="server" :label="t('datasource.field.confluenceEditionServer')" />
            <t-option value="cloud" :label="t('datasource.field.confluenceEditionCloud')" />
          </t-select>
        </div>
        <div
          v-for="field in displayedCredentialFields"
          :key="field.key"
          class="form-item"
        >
          <template v-if="field.fieldType === 'custom_headers'">
            <div class="custom-headers-header">
              <label class="form-label" style="margin-bottom: 0;">{{ t(field.labelKey) }}</label>
              <t-button variant="text" size="small" theme="primary" @click="emit('add-rss-header')">
                <template #icon><t-icon name="add" /></template>
                {{ t('model.editor.customHeadersAdd') }}
              </t-button>
            </div>
            <p v-if="field.hintKey" class="form-desc custom-headers-desc">{{ t(field.hintKey) }}</p>
            <div v-if="rssAuthHeaders.length > 0" class="custom-headers-list">
              <div v-for="(item, idx) in rssAuthHeaders" :key="idx" class="custom-header-row">
                <t-input
                  :value="item.key"
                  :placeholder="t('model.editor.customHeadersKeyPlaceholder')"
                  class="custom-header-key"
                  autocomplete="off"
                  spellcheck="false"
                  @change="(val: string) => emit('update:rss-header', idx, 'key', String(val ?? ''))"
                />
                <t-input
                  :value="item.value"
                  :placeholder="t('model.editor.customHeadersValuePlaceholder')"
                  class="custom-header-value"
                  autocomplete="off"
                  spellcheck="false"
                  @change="(val: string) => emit('update:rss-header', idx, 'value', String(val ?? ''))"
                />
                <t-button
                  variant="text"
                  shape="square"
                  size="small"
                  class="custom-header-remove"
                  :aria-label="t('common.delete')"
                  @click="emit('remove-rss-header', idx)"
                >
                  <t-icon name="close" />
                </t-button>
              </div>
            </div>
          </template>
          <template v-else>
            <label class="form-label" :class="{ required: !field.optional }">
              {{ t(field.labelKey) }}
            </label>
            <t-textarea
              v-if="field.multiline"
              :value="form.config.credentials[field.key] ?? ''"
              :placeholder="field.placeholder || t('credential.inputPlaceholder')"
              :autosize="{ minRows: 2, maxRows: 6 }"
              autocomplete="off"
              spellcheck="false"
              @change="(val: string) => emit('update:credential', field.key, String(val ?? ''))"
            />
            <t-input
              v-else
              :value="form.config.credentials[field.key] ?? ''"
              :placeholder="field.placeholder || t('credential.inputPlaceholder')"
              :type="field.secret ? 'password' : 'text'"
              autocomplete="off"
              spellcheck="false"
              @change="(val: string) => emit('update:credential', field.key, String(val ?? ''))"
            >
              <template v-if="field.secret" #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
            <p v-if="field.hintKey" class="form-desc">{{ t(field.hintKey) }}</p>
          </template>
        </div>
        <div v-if="isEdit && replaceCredentialsMode" class="credential-edit-actions">
          <t-button size="small" variant="text" @click="emit('cancel-replace-credentials')">
            {{ t('common.cancel') }}
          </t-button>
        </div>
      </template>
    </section>

    <section v-if="form.type === 'nextcloud'" class="setting-drawer__section">
      <div class="form-item">
        <label class="form-label">{{ t('datasource.nextcloud.includeExtensions') }}</label>
        <t-input
          :value="String(form.config.settings.include_extensions ?? '')"
          placeholder=".pdf, .md"
          @change="(val: string) => emit('update:setting', 'include_extensions', String(val ?? ''))"
        />
      </div>
      <div class="form-item">
        <label class="form-label">{{ t('datasource.nextcloud.excludeExtensions') }}</label>
        <t-input
          :value="String(form.config.settings.exclude_extensions ?? '')"
          placeholder=".html"
          @change="(val: string) => emit('update:setting', 'exclude_extensions', String(val ?? ''))"
        />
      </div>
      <p class="form-desc">{{ t('datasource.nextcloud.extensionsHint') }}</p>
    </section>
  </template>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DataSourceTypeIcon from './DataSourceTypeIcon.vue'
import type {
  ConnectorDef,
  ConnectorFieldDef,
  CustomHeaderItem,
  DataSourceFormState,
} from './composables/useConnectorDefs'

const props = defineProps<{
  step: number
  connectorDefs: ConnectorDef[]
  currentDef?: ConnectorDef
  form: DataSourceFormState
  isEdit: boolean
  credentialsConfigured: boolean
  replaceCredentialsMode: boolean
  credentialsInputVisible: boolean
  pendingRemoveCredentials: boolean
  removingCredentials: boolean
  displayedCredentialFields: ConnectorFieldDef[]
  rssAuthHeaders: CustomHeaderItem[]
  resetCounter: number
}>()

const emit = defineEmits<{
  'select-type': [def: ConnectorDef]
  'update:name': [name: string]
  'update:credential': [key: string, value: string]
  'update:setting': [key: string, value: string]
  'enter-replace-credentials': []
  'cancel-replace-credentials': []
  'request-remove-credentials': []
  'cancel-remove-credentials': []
  'confirm-remove-credentials': []
  'add-rss-header': []
  'remove-rss-header': [index: number]
  'update:rss-header': [index: number, field: 'key' | 'value', value: string]
}>()

const { t } = useI18n()
const prereqExpanded = ref(false)

watch(
  () => props.resetCounter,
  () => {
    prereqExpanded.value = false
  },
)
</script>

<style scoped lang="less">
@import './datasource-surface.less';
@import './datasource-editor-dialog.less';
</style>
