<template>
  <SettingDrawer
    :visible="visible"
    class="api-playground-drawer"
    :title="$t('integrations.api.playgroundTitle')"
    :description="$t('integrations.api.playgroundDrawerDesc')"
    icon="code"
    width="640px"
    :min-width="560"
    :max-width="960"
    storage-key="setting-drawer:width:api-playground"
    :confirm-text="$t('integrations.api.playgroundRun')"
    :confirm-loading="playground.running"
    :confirm-disabled="!canRunPlayground"
    @update:visible="(v: boolean) => emit('update:visible', v)"
    @confirm="emit('run')"
    @cancel="emit('cancel')"
  >
    <template #footer-left>
      <t-button v-if="playground.running" variant="outline" @click="emit('stop')">
        <template #icon><t-icon name="close-circle" /></template>
        {{ $t('integrations.api.playgroundStop') }}
      </t-button>
      <span v-if="playgroundDisabledReason" class="footer-test-message">
        {{ playgroundDisabledReason }}
      </span>
    </template>

    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ $t('integrations.api.playgroundSectionRequest') }}</h4>

      <div class="drawer-form-item">
        <label class="drawer-form-label">{{ $t('integrations.api.playgroundAgent') }}</label>
        <t-select
          :model-value="playground.agent_id"
          :options="agentOptions"
          :loading="agentsLoading"
          filterable
          :placeholder="$t('integrations.api.playgroundAgentPlaceholder')"
          @update:model-value="(val: unknown) => emit('updatePlayground', { agent_id: String(val ?? '') })"
        />
        <p v-if="agentsError" class="drawer-form-desc drawer-form-desc--error">{{ agentsError }}</p>
      </div>

      <div class="drawer-form-item">
        <label class="drawer-form-label">{{ $t('integrations.api.playgroundExternalUser') }}</label>
        <t-input
          :model-value="playground.external_user_id"
          :disabled="mode === 'tenant'"
          class="mono-input"
          :placeholder="$t('integrations.api.playgroundExternalUserPlaceholder')"
          @update:model-value="(val: unknown) => emit('updatePlayground', { external_user_id: String(val ?? '') })"
        />
        <p class="drawer-form-desc">{{ externalUserHint }}</p>
      </div>

      <div class="drawer-form-item">
        <label class="drawer-form-label">{{ $t('integrations.api.playgroundQuestion') }}</label>
        <t-textarea
          :model-value="playground.query"
          :autosize="{ minRows: 2, maxRows: 4 }"
          :placeholder="$t('integrations.api.playgroundQuestionPlaceholder')"
          @update:model-value="(val: unknown) => emit('updatePlayground', { query: String(val ?? '') })"
        />
      </div>
    </section>

    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ $t('integrations.api.playgroundSectionPreview') }}</h4>
      <div class="code-panel playground-preview">
        <div class="code-panel__toolbar">
          <span class="code-panel__label">{{ $t('integrations.api.playgroundRequestPreview') }}</span>
          <t-button size="small" variant="text" class="code-panel__copy" @click="emit('copy', playgroundRequestPreview)">
            <template #icon><t-icon name="file-copy" /></template>
            {{ $t('integrations.api.copy') }}
          </t-button>
        </div>
        <pre class="code-panel__pre">{{ playgroundRequestPreview }}</pre>
      </div>
    </section>

    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ $t('integrations.api.playgroundSectionResult') }}</h4>
      <t-alert v-if="playground.error" theme="error" :message="playground.error" />

      <div v-if="hasPlaygroundResult" class="playground-results">
        <div v-if="mode === 'signed_token' && playground.signed_token" class="playground-step">
          <div class="playground-step__header">
            <span>{{ $t('integrations.api.playgroundGeneratedToken') }}</span>
            <t-button size="small" variant="text" @click="emit('copy', playground.signed_token)">
              <template #icon><t-icon name="file-copy" /></template>
              {{ $t('integrations.api.copy') }}
            </t-button>
          </div>
          <pre>{{ playground.signed_token }}</pre>
        </div>

        <div class="playground-step">
          <div class="playground-step__header">
            <span>{{ $t('integrations.api.playgroundStepSession') }}</span>
            <t-tag size="small" :theme="playground.session_status === 'success' ? 'success' : 'default'" variant="light">
              {{ playground.session_status || '-' }}
            </t-tag>
          </div>
          <pre>{{ playground.session_response || '-' }}</pre>
        </div>

        <div class="playground-step">
          <div class="playground-step__header">
            <span>{{ $t('integrations.api.playgroundStepChat') }}</span>
            <t-tag size="small" :theme="playground.chat_status === 'success' ? 'success' : 'default'" variant="light">
              {{ playground.chat_status || '-' }}
            </t-tag>
          </div>
          <pre>{{ playground.stream_output || '-' }}</pre>
        </div>

        <div v-if="playground.final_answer" class="playground-step">
          <div class="playground-step__header">
            <span>{{ $t('integrations.api.playgroundFinalAnswer') }}</span>
          </div>
          <pre>{{ playground.final_answer }}</pre>
        </div>
      </div>
      <p v-else class="playground-empty">{{ $t('integrations.api.playgroundEmptyResult') }}</p>
    </section>
  </SettingDrawer>
</template>

<script setup lang="ts">
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import type { APIPrincipalMode } from '@/api/tenant'
import type { PlaygroundState } from '../composables/useApiPlaygroundRunner'

defineProps<{
  visible: boolean
  mode: APIPrincipalMode
  playground: PlaygroundState
  agentOptions: Array<{ label: string; value: string }>
  agentsLoading: boolean
  agentsError: string
  externalUserHint: string
  playgroundRequestPreview: string
  playgroundDisabledReason: string
  canRunPlayground: boolean
  hasPlaygroundResult: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  updatePlayground: [patch: Partial<Pick<PlaygroundState, 'agent_id' | 'external_user_id' | 'query'>>]
  run: []
  stop: []
  cancel: []
  copy: [text: string]
}>()
</script>

<style scoped lang="less">
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

.playground-preview {
  width: 100%;
}

.drawer-form-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.drawer-form-label {
  display: block;
  color: var(--gy-text-primary);
  font-size: var(--gy-text-md);
  font-weight: 500;
  line-height: 1.4;
}

.drawer-form-desc {
  margin: 0;
  color: var(--gy-text-tertiary);
  font-size: var(--gy-text-sm);
  line-height: 1.5;

  &--error {
    color: var(--gy-danger);
  }
}

.footer-test-message {
  min-width: 0;
  color: var(--gy-text-tertiary);
  font-size: var(--gy-text-sm);
  line-height: 1.4;
}

.playground-empty {
  margin: 0;
  color: var(--gy-text-tertiary);
  font-size: var(--gy-text-sm);
  line-height: 1.6;
}

.playground-results {
  display: grid;
  grid-template-columns: 1fr;
  gap: 12px;
}

.playground-step {
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-md);
  background: var(--gy-surface-subtle);
  overflow: hidden;

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--gy-border-subtle);
    background: var(--gy-surface);
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-sm);
    font-weight: 500;
  }

  pre {
    max-height: 240px;
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    color: var(--gy-text-primary);
    font-family: var(--gy-font-mono);
    font-size: var(--gy-text-sm);
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }
}
</style>
