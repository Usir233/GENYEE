<template>
  <section class="setting-drawer__section">
    <div class="section-title-row">
      <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionTemplate') }}</h4>
      <t-button variant="text" size="small" :loading="templatesLoading" @click="emit('reload')">
        <template #icon><t-icon name="refresh" /></template>
        {{ $t('settings.sandbox.refreshTemplates') }}
      </t-button>
    </div>
    <t-alert
      v-if="hasSkillSnapshot"
      theme="info"
      class="compact-alert"
      :message="$t('settings.sandbox.templateLockedBySkills')"
    />
    <t-alert
      v-else-if="hasInFlightSkill"
      theme="info"
      class="compact-alert"
      :message="$t('settings.sandbox.templateLockedByInFlight')"
    />
    <div v-if="templatesLoading && !templatesLoaded" class="template-loading">
      <t-loading size="small" />
      <span>{{ $t('settings.sandbox.loadingTemplates') }}</span>
    </div>
    <div v-else class="template-list" role="radiogroup" :aria-label="$t('settings.sandbox.sectionTemplate')">
      <div v-if="canCreateStandard" class="template-row template-row--offer">
        <div class="template-row__main">
          <div class="template-row__head">
            <span class="template-row__title">{{ $t('settings.sandbox.genyeeStandardTemplate') }}</span>
            <t-tag theme="primary" variant="outline" size="small">
              {{ $t('settings.sandbox.recommendedTag') }}
            </t-tag>
            <span class="template-row__spacer" />
            <t-button
              theme="primary"
              variant="outline"
              size="small"
              :loading="templatesLoading"
              @click="createStandardTemplate"
            >
              {{ $t('settings.sandbox.createStandardTemplate') }}
            </t-button>
          </div>
          <p class="template-row__hint">{{ $t('settings.sandbox.createStandardTemplateHint') }}</p>
        </div>
      </div>
      <div v-if="canCreateDesktop" class="template-row template-row--offer">
        <div class="template-row__main">
          <div class="template-row__head">
            <span class="template-row__title">{{ $t('settings.sandbox.genyeeDesktopTemplate') }}</span>
            <t-tag theme="warning" variant="outline" size="small">
              {{ $t('settings.sandbox.desktopTemplateTag') }}
            </t-tag>
            <span class="template-row__spacer" />
            <t-button
              theme="primary"
              variant="outline"
              size="small"
              :loading="templatesLoading"
              @click="createDesktopTemplate"
            >
              {{ $t('settings.sandbox.createDesktopTemplate') }}
            </t-button>
          </div>
          <p class="template-row__hint">{{ $t('settings.sandbox.createDesktopTemplateHint') }}</p>
        </div>
      </div>
      <div
        v-for="item in templates"
        :key="item.id"
        class="template-row"
        :class="{
          'is-active': currentTemplateId === item.id,
          'is-pending': isTemplatePending(item),
          'is-disabled': !isTemplateSelectable(item) || (retargetFrozen && currentTemplateId !== item.id),
        }"
        role="radio"
        :aria-checked="currentTemplateId === item.id"
        :aria-disabled="!isTemplateSelectable(item) || (retargetFrozen && currentTemplateId !== item.id)"
        :tabindex="isTemplateSelectable(item) && !(retargetFrozen && currentTemplateId !== item.id) ? 0 : -1"
        @click="onTemplateCardClick(item)"
        @keydown.enter.prevent="onTemplateCardClick(item)"
        @keydown.space.prevent="onTemplateCardClick(item)"
      >
        <span class="template-row__marker" aria-hidden="true" />
        <div class="template-row__main">
          <div class="template-row__head">
            <span class="template-row__title" :title="templateDisplayName(item)">
              {{ templateDisplayName(item) }}
            </span>
            <t-tag v-if="item.standard" theme="primary" variant="outline" size="small">
              {{ $t('settings.sandbox.recommendedTag') }}
            </t-tag>
            <t-tag v-else-if="item.desktop" theme="warning" variant="outline" size="small">
              {{ $t('settings.sandbox.desktopTemplateTag') }}
            </t-tag>
            <span class="template-row__spacer" />
            <t-tag :theme="templateStatusTheme(item)" variant="outline" size="small">
              {{ templateStatusLabel(item) }}
            </t-tag>
            <span v-if="canRebuildTemplate(item)" class="template-row__rebuild" @click.stop>
              <t-popconfirm
                theme="warning"
                :content="item.desktop
                  ? $t('settings.sandbox.replaceDesktopTemplateConfirm')
                  : $t('settings.sandbox.replaceStandardTemplateConfirm')"
                @confirm="replaceFirstPartyTemplate(item)"
              >
                <t-button variant="text" size="small" :loading="templatesLoading">
                  {{ $t('settings.sandbox.replaceStandardTemplate') }}
                </t-button>
              </t-popconfirm>
            </span>
          </div>
          <dl v-if="templateFieldRows(item).length" class="template-row__fields">
            <div v-for="field in templateFieldRows(item)" :key="field.key" class="template-row__field">
              <dt>{{ field.label }}</dt>
              <dd :class="{ 'is-mono': field.mono }" :title="field.value">{{ field.value }}</dd>
            </div>
          </dl>
          <p v-if="isTemplateUntagged(item)" class="template-row__hint template-row__hint--error">
            {{ $t('settings.sandbox.templateUntaggedHint') }}
          </p>
          <p v-else-if="templateFailureReason(item)" class="template-row__hint template-row__hint--error">
            {{ templateFailureReason(item) }}
          </p>
          <p v-else-if="isTemplatePending(item) && (item.standard || item.desktop)" class="template-row__hint">
            {{ $t('settings.sandbox.templateBuildingHint') }}
          </p>
        </div>
      </div>
      <div
        v-if="templatesLoaded && !templates.length && !canCreateStandard && !templatesError"
        class="env-empty"
      >
        {{ $t('settings.sandbox.noTemplates') }}
      </div>
    </div>
    <t-alert v-if="templatesError" theme="warning" class="compact-alert" :message="templatesError" />
    <a
      v-if="clusterGuideUrl"
      class="inline-guide-link"
      :href="clusterGuideUrl"
      target="_blank"
      rel="noopener noreferrer"
    >
      <t-icon name="link" />
      {{ $t('settings.sandbox.howToBuildTemplate') }}
    </a>
  </section>
</template>

<script setup lang="ts">
import type { SandboxTemplate } from '@/api/system'
import {
  isTemplatePending,
  isTemplateSelectable,
  isTemplateUntagged,
  type TemplateFieldRow,
} from './useSandboxTemplatesPoll'

defineProps<{
  templates: SandboxTemplate[]
  templatesLoading: boolean
  templatesLoaded: boolean
  templatesError: string
  currentTemplateId: string
  canCreateStandard: boolean
  canCreateDesktop: boolean
  retargetFrozen: boolean
  hasSkillSnapshot: boolean
  hasInFlightSkill: boolean
  clusterGuideUrl: string
  onTemplateCardClick: (item: SandboxTemplate) => void
  canRebuildTemplate: (item: SandboxTemplate) => boolean
  templateDisplayName: (item: SandboxTemplate) => string
  templateFieldRows: (item: SandboxTemplate) => TemplateFieldRow[]
  templateFailureReason: (item: SandboxTemplate) => string
  templateStatusTheme: (item: SandboxTemplate) => 'success' | 'warning' | 'danger' | 'default'
  templateStatusLabel: (item: SandboxTemplate) => string
  createStandardTemplate: () => Promise<boolean>
  createDesktopTemplate: () => Promise<boolean>
  replaceFirstPartyTemplate: (item: SandboxTemplate) => Promise<boolean> | undefined
}>()

const emit = defineEmits<{
  (e: 'reload'): void
}>()
</script>

<style lang="less" scoped src="./sandbox-config-editor-drawer.less"></style>
