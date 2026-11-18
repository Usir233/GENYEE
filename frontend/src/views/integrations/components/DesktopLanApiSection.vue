<template>
  <div class="desktop-lan-api">
    <div class="row">
      <div class="row-info">
        <label>{{ $t('integrations.api.baseUrl') }}</label>
        <p>{{ $t('integrations.api.baseUrlDesc') }}</p>
      </div>
      <div class="row-control copy-field">
        <t-input :model-value="apiBaseUrl" readonly class="mono-input" />
        <t-button
          variant="text"
          :title="$t('integrations.api.copy')"
          :aria-label="$t('integrations.api.copy')"
          @click="emit('copy', apiBaseUrl)"
        >
          <t-icon name="file-copy" />
        </t-button>
      </div>
    </div>

    <template v-if="showDesktopPortSetting || showDesktopBindPublicSetting">
      <div v-if="showDesktopPortSetting" class="row">
        <div class="row-info">
          <label>{{ $t('tenant.api.desktopPortLabel') }}</label>
          <p>{{ $t('tenant.api.desktopPortDescription') }}</p>
        </div>
        <div class="row-control desktop-api-control">
          <div class="desktop-port-input-wrap">
            <t-input-number
              :model-value="desktopPort"
              :min="0"
              :max="65535"
              theme="normal"
              @update:model-value="(val: number | undefined) => emit('update:desktopPort', val)"
            />
          </div>
          <t-button size="small" variant="text" @click="emit('save-desktop-port')">
            {{ $t('tenant.api.desktopPortSave') }}
          </t-button>
        </div>
      </div>

      <div v-if="showDesktopBindPublicSetting" class="row">
        <div class="row-info">
          <label>{{ $t('tenant.api.desktopBindPublicLabel') }}</label>
          <p>{{ $t('tenant.api.desktopBindPublicDescription') }}</p>
        </div>
        <div class="row-control desktop-bind-public-control">
          <t-switch
            :model-value="desktopBindPublic"
            @change="(val: string | number | boolean) => emit('change-desktop-bind-public', Boolean(val))"
          />
        </div>
      </div>

      <div v-if="lanBaseUrl" class="row">
        <div class="row-info">
          <label>{{ $t('tenant.api.lanUrlLabel') }}</label>
          <p>{{ $t('tenant.api.lanUrlDescription') }}</p>
        </div>
        <div class="row-control copy-field">
          <t-input :model-value="lanBaseUrl" readonly class="mono-input" />
          <t-button
            variant="text"
            :title="$t('tenant.api.lanUrlCopyTitle')"
            :aria-label="$t('tenant.api.lanUrlCopyTitle')"
            @click="emit('copy', lanBaseUrl)"
          >
            <t-icon name="file-copy" />
          </t-button>
        </div>
      </div>

      <div v-if="showLanUrlUnavailableHint" class="row row--single">
        <t-alert theme="warning" :message="$t('tenant.api.lanUrlUnavailable')" />
      </div>
    </template>

    <div v-if="hasApiDoc" class="row row--doc">
      <div class="row-info">
        <label>{{ $t('tenant.api.docLabel') }}</label>
        <p>
          {{ $t('tenant.api.docDescription') }}
          <button type="button" class="doc-link doc-link-btn" @click="emit('open-doc')">
            {{ $t('tenant.api.openDoc') }}
            <t-icon name="link" class="link-icon" />
          </button>
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  apiBaseUrl: string
  lanBaseUrl: string | null
  showDesktopPortSetting: boolean
  showDesktopBindPublicSetting: boolean
  desktopPort: number | undefined
  desktopBindPublic: boolean
  showLanUrlUnavailableHint: boolean
  hasApiDoc: boolean
}>()

const emit = defineEmits<{
  copy: [text: string]
  'update:desktopPort': [port: number | undefined]
  'save-desktop-port': []
  'change-desktop-bind-public': [enabled: boolean]
  'open-doc': []
}>()
</script>

<style scoped lang="less">
.desktop-lan-api {
  display: flex;
  flex-direction: column;
}

.row {
  display: grid;
  grid-template-columns: minmax(220px, 0.8fr) minmax(320px, 1fr);
  gap: 24px;
  padding: 20px 0;
  border-bottom: 1px solid var(--gy-border-subtle);
}

.row--single {
  display: block;
}

.row--doc {
  grid-template-columns: 1fr;
}

.row-info {
  label {
    display: block;
    margin-bottom: 4px;
    color: var(--gy-text-primary);
    font-size: var(--gy-text-lg);
    font-weight: 600;
  }

  p {
    margin: 0;
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-md);
    line-height: 1.5;
  }
}

.row-control,
.copy-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.mono-input {
  flex: 1 1 auto;
  min-width: 0;

  :deep(input) {
    font-family: var(--gy-font-mono);
    font-size: var(--gy-text-sm);
  }
}

.doc-link-btn {
  border: 0;
  padding: 0;
  background: transparent;
  font: inherit;
  cursor: pointer;
}

.link-icon {
  font-size: var(--gy-text-md);
}

.desktop-api-control {
  display: flex;
  align-items: center;
  gap: 8px;
}

.desktop-port-input-wrap {
  flex: 1;
  min-width: 0;

  :deep(.t-input-number),
  :deep(.t-input__wrap) {
    width: 100%;
  }

  :deep(input) {
    font-family: var(--gy-font-mono);
    font-size: var(--gy-text-sm);
  }
}

.desktop-bind-public-control {
  justify-content: flex-end;
  padding-top: 4px;
}

@media (max-width: 780px) {
  .row {
    grid-template-columns: 1fr;
  }
}
</style>
