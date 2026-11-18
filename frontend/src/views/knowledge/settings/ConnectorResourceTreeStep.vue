<template>
  <section v-if="step === 2" class="setting-drawer__section ds-resource-section">
    <template v-if="isGitLabConnector(form.type)">
      <h4 class="setting-drawer__section-title">{{ t('datasource.gitlab.projects') }}</h4>
      <p class="ds-resource-hint">{{ t('datasource.gitlab.projectsHint') }}</p>
      <div class="gitlab-project-list">
        <div v-for="(project, index) in gitlabProjects" :key="index" class="gitlab-project-row">
          <div class="gitlab-project-row__header">
            <strong>{{ t('datasource.gitlab.project') }} {{ index + 1 }}</strong>
            <t-button
              variant="text"
              size="small"
              theme="danger"
              :aria-label="t('common.delete')"
              @click="emit('remove-gitlab-project', index)"
            >
              <t-icon name="delete" />
            </t-button>
          </div>
          <label class="form-label required">{{ t('datasource.gitlab.projectId') }}</label>
          <t-input
            :value="project.project_id"
            :placeholder="t('datasource.gitlab.projectIdPlaceholder')"
            @change="(val: string) => emit('update:gitlab-project', index, 'project_id', String(val ?? ''))"
          />
          <label class="form-label">{{ t('datasource.gitlab.ref') }}</label>
          <t-input
            :value="project.ref"
            :placeholder="t('datasource.gitlab.refPlaceholder')"
            @change="(val: string) => emit('update:gitlab-project', index, 'ref', String(val ?? ''))"
          />
          <label class="form-label">{{ t('datasource.gitlab.paths') }}</label>
          <t-textarea
            :value="project.pathsText"
            :placeholder="t('datasource.gitlab.pathsPlaceholder')"
            :autosize="{ minRows: 2, maxRows: 5 }"
            @change="(val: string) => emit('update:gitlab-project', index, 'pathsText', String(val ?? ''))"
          />
        </div>
        <t-button variant="outline" @click="emit('add-gitlab-project')">
          <template #icon><t-icon name="add" /></template>
          {{ t('datasource.gitlab.addProject') }}
        </t-button>
      </div>
    </template>
    <template v-else>
      <h4 class="setting-drawer__section-title">{{ t('datasource.step.resources') }}</h4>
      <p class="ds-resource-hint">{{ t('datasource.resourceHint') }}</p>

      <div v-if="isDriveConnector(form.type)" class="drive-folder-input">
        <label class="drive-folder-input__label required">
          {{ t('datasource.drive.folderTokenLabel') }}
          <t-tooltip :content="t('datasource.drive.rootNotSupportedHint')" placement="top">
            <t-icon name="help-circle" class="drive-folder-input__help" />
          </t-tooltip>
        </label>
        <div class="drive-folder-input__row">
          <t-input
            :value="driveFolderToken"
            :placeholder="t('datasource.drive.folderTokenPlaceholder')"
            :status="driveFolderTokenError ? 'error' : 'default'"
            :tips="driveFolderTokenError ? driveFolderTokenError : t('datasource.drive.shareHint')"
            clearable
            @enter="emit('load-drive-root')"
            @change="(val: string) => emit('update:drive-folder-token', String(val ?? ''))"
            @input="emit('clear-drive-folder-error')"
          />
          <t-button theme="primary" :loading="loadingResources" @click="emit('load-drive-root')">
            {{ t('datasource.drive.load') }}
          </t-button>
        </div>
      </div>

      <div
        v-if="isDriveConnector(form.type) && !driveRootLoaded && !loadingResources"
        class="ds-resource-empty ds-drive-placeholder"
      >
        <p class="ds-empty-title">{{ t('datasource.drive.placeholderTitle') }}</p>
        <p class="ds-empty-desc">{{ t('datasource.drive.placeholderDesc') }}</p>
      </div>

      <div v-else-if="loadingResources" class="ds-loading-center"><t-loading /></div>
      <div v-else-if="resources.length > 0" class="resource-picker">
        <div class="resource-picker__toolbar">
          <span class="resource-picker__count">
            {{ t('knowledgeBase.selectedCount', { count: selectedResourceCount }) }}
          </span>
          <div v-if="hasExpandableNodes" class="resource-picker__actions">
            <button type="button" class="resource-picker__action" @click="emit('expand-all')">
              {{ t('knowledgeStages.expandBranch') }}
            </button>
            <span class="resource-picker__action-sep" aria-hidden="true">·</span>
            <button type="button" class="resource-picker__action" @click="emit('collapse-all')">
              {{ t('knowledgeStages.collapseBranch') }}
            </button>
          </div>
        </div>
        <div class="resource-picker__list" role="tree">
          <template
            v-for="{ resource: r, depth, noticeAfter } in visibleTree"
            :key="noticeAfter ? `${r.external_id}__notice` : r.external_id"
          >
            <div
              v-if="!noticeAfter"
              class="resource-picker__row"
              :class="{
                'is-checked': resourceRowState(r.external_id) === 'checked',
                'is-indeterminate': resourceRowState(r.external_id) === 'indeterminate',
              }"
              :style="{ '--depth': depth }"
              role="treeitem"
              tabindex="0"
              :aria-expanded="r.has_children ? expandedResourceIds.has(r.external_id) : undefined"
              :aria-selected="resourceRowState(r.external_id) === 'checked'"
              @click="emit('toggle-resource', r.external_id)"
              @keydown.enter.prevent="emit('toggle-resource', r.external_id)"
              @keydown.space.prevent="emit('toggle-resource', r.external_id)"
            >
              <button
                v-if="r.has_children"
                type="button"
                class="resource-picker__expand"
                :aria-label="expandedResourceIds.has(r.external_id)
                  ? t('knowledgeStages.collapseBranch')
                  : t('knowledgeStages.expandBranch')"
                @click.stop="emit('toggle-expand', r.external_id)"
              >
                <t-loading v-if="loadingChildrenIds.has(r.external_id)" size="12px" />
                <t-icon
                  v-else
                  :name="expandedResourceIds.has(r.external_id) ? 'chevron-down' : 'chevron-right'"
                  size="12px"
                />
              </button>
              <span v-else class="resource-picker__expand-spacer" aria-hidden="true" />
              <span
                class="resource-picker__check"
                :class="{
                  'is-checked': resourceRowState(r.external_id) === 'checked',
                  'is-indeterminate': resourceRowState(r.external_id) === 'indeterminate',
                }"
                aria-hidden="true"
              >
                <svg
                  v-if="resourceRowState(r.external_id) === 'checked'"
                  width="10"
                  height="10"
                  viewBox="0 0 12 12"
                  fill="none"
                >
                  <path
                    d="M10 3L4.5 8.5L2 6"
                    stroke="var(--gy-text-inverse)"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  />
                </svg>
              </span>
              <span class="resource-picker__icon" aria-hidden="true">
                <t-icon :name="resourceIconName(r)" size="16px" />
              </span>
              <span class="resource-picker__label">
                <span class="resource-picker__name" :title="r.name || t('datasource.untitled')">
                  {{ r.name || t('datasource.untitled') }}
                </span>
                <span
                  v-if="shouldShowResourceType(r.type)"
                  class="resource-picker__type"
                >{{ resourceTypeLabel(r.type) }}</span>
              </span>
            </div>
            <p v-else class="resource-picker__notice" :style="{ '--depth': depth }">
              {{ t('datasource.confluence.cloudFolderLimitation') }}
            </p>
          </template>
        </div>
      </div>
      <div v-else class="ds-resource-empty">
        <t-icon name="info-circle" size="32px" style="color: var(--td-warning-color); margin-bottom: 8px;" />
        <p class="ds-empty-title">{{ t('datasource.noResources') }}</p>
        <p class="ds-empty-desc">{{ t(`datasource.noResourcesDesc_${form.type}`, t('datasource.noResourcesDesc')) }}</p>
        <div class="ds-guide-steps">
          <div class="ds-guide-step">
            <span class="ds-guide-num">1</span>
            <span>{{ t(`datasource.guideStep1_${form.type}`, t('datasource.guideStep1')) }}</span>
          </div>
          <div class="ds-guide-step">
            <span class="ds-guide-num">2</span>
            <span>{{ t(`datasource.guideStep2_${form.type}`, t('datasource.guideStep2')) }}</span>
          </div>
          <div class="ds-guide-step">
            <span class="ds-guide-num">3</span>
            <span>{{ t(`datasource.guideStep3_${form.type}`, t('datasource.guideStep3')) }}</span>
          </div>
        </div>
        <div class="ds-empty-actions">
          <button type="button" class="ds-empty-retry" @click="emit('load-resources')">
            {{ t('datasource.retryLoadResources') }}
          </button>
          <a
            v-if="currentDef?.permissionDocUrl"
            :href="currentDef.permissionDocUrl"
            target="_blank"
            rel="noopener"
            class="doc-link"
          >
            {{ t('datasource.permissionDocLink') }}
            <t-icon name="link" class="link-icon" />
          </a>
        </div>
      </div>
    </template>
  </section>

  <template v-if="step === 3">
    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.syncScheduleLabel') }}</h4>
      <t-select
        :value="form.sync_schedule"
        @change="(val: string | number) => emit('update:sync-schedule', String(val))"
      >
        <t-option v-for="p in schedulePresets" :key="p.value" :value="p.value" :label="p.label" />
      </t-select>
    </section>

    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.syncModeLabel') }}</h4>
      <div class="form-item form-item--flat">
        <div class="option-group" role="radiogroup" :aria-label="t('datasource.syncModeLabel')">
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': form.sync_mode === 'incremental' }"
            role="radio"
            :aria-checked="form.sync_mode === 'incremental'"
            @click="emit('update:sync-mode', 'incremental')"
          >
            {{ t('datasource.syncMode.incremental') }}
          </button>
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': form.sync_mode === 'full' }"
            role="radio"
            :aria-checked="form.sync_mode === 'full'"
            @click="emit('update:sync-mode', 'full')"
          >
            {{ t('datasource.syncMode.full') }}
          </button>
        </div>
      </div>

      <div class="form-item form-item--flat">
        <label class="form-label">{{ t('datasource.conflictLabel') }}</label>
        <div class="option-group" role="radiogroup" :aria-label="t('datasource.conflictLabel')">
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': form.conflict_strategy === 'overwrite' }"
            role="radio"
            :aria-checked="form.conflict_strategy === 'overwrite'"
            @click="emit('update:conflict-strategy', 'overwrite')"
          >
            {{ t('datasource.conflict.overwrite') }}
          </button>
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': form.conflict_strategy === 'skip' }"
            role="radio"
            :aria-checked="form.conflict_strategy === 'skip'"
            @click="emit('update:conflict-strategy', 'skip')"
          >
            {{ t('datasource.conflict.skip') }}
          </button>
        </div>
      </div>

      <div v-if="isFeishuFamilyConnector(form.type)" class="form-item form-item--flat">
        <label class="form-label">{{ t('datasource.parseModeLabel') }}</label>
        <div class="option-group" role="radiogroup" :aria-label="t('datasource.parseModeLabel')">
          <button
            v-if="isEdit"
            type="button"
            class="option-pill"
            :class="{ 'is-active': feishuParseMode === '' }"
            role="radio"
            :aria-checked="feishuParseMode === ''"
            @click="emit('update:feishu-parse-mode', '')"
          >
            {{ t('datasource.parseMode.inherit') }}
          </button>
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': feishuParseMode === 'blocks' }"
            role="radio"
            :aria-checked="feishuParseMode === 'blocks'"
            @click="emit('update:feishu-parse-mode', 'blocks')"
          >
            {{ t('datasource.parseMode.blocks') }}
          </button>
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': feishuParseMode === 'export' }"
            role="radio"
            :aria-checked="feishuParseMode === 'export'"
            @click="emit('update:feishu-parse-mode', 'export')"
          >
            {{ t('datasource.parseMode.export') }}
          </button>
        </div>
        <p class="form-desc">{{ t('datasource.parseModeHint') }}</p>
      </div>

      <div class="form-item form-item--flat">
        <t-checkbox
          :checked="form.sync_deletions"
          @change="(val: boolean) => emit('update:sync-deletions', Boolean(val))"
        >
          {{ t('datasource.syncDeletions') }}
        </t-checkbox>
      </div>
    </section>

    <section v-if="form.type === 'dingtalk'" class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.dingtalkIngestLabel') }}</h4>
      <div class="form-item form-item--flat">
        <t-checkbox
          :checked="dingtalkIncludeUploadedFiles"
          @change="(val: boolean) => emit('update:dingtalk-uploaded-files', Boolean(val))"
        >
          {{ t('datasource.dingtalkIncludeUploadedFiles') }}
        </t-checkbox>
      </div>
      <p class="form-desc">{{ t('datasource.dingtalkIncludeUploadedFilesHint') }}</p>
    </section>

    <section v-if="form.type === 'yuque'" class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.yuqueFolderModeLabel') }}</h4>
      <div class="form-item form-item--flat">
        <div class="option-group" role="radiogroup" :aria-label="t('datasource.yuqueFolderModeLabel')">
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': yuqueFolderMode === 'toc' }"
            role="radio"
            :aria-checked="yuqueFolderMode === 'toc'"
            @click="emit('update:yuque-folder-mode', 'toc')"
          >
            {{ t('datasource.yuqueFolderModeToc') }}
          </button>
          <button
            type="button"
            class="option-pill"
            :class="{ 'is-active': yuqueFolderMode === 'none' }"
            role="radio"
            :aria-checked="yuqueFolderMode === 'none'"
            @click="emit('update:yuque-folder-mode', 'none')"
          >
            {{ t('datasource.yuqueFolderModeNone') }}
          </button>
        </div>
      </div>
      <p class="form-desc">{{ t('datasource.yuqueFolderModeHint') }}</p>

      <div class="form-item form-item--flat">
        <t-checkbox
          :checked="yuqueTOCOnly"
          :disabled="yuqueFolderMode !== 'toc'"
          @change="(val: boolean) => emit('update:yuque-toc-only', Boolean(val))"
        >
          {{ t('datasource.yuqueTOCOnly') }}
        </t-checkbox>
      </div>
      <p class="form-desc">{{ t('datasource.yuqueTOCOnlyHint') }}</p>
    </section>
  </template>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { Resource } from '@/api/datasource'
import {
  isDriveConnector,
  isFeishuFamilyConnector,
  isGitLabConnector,
  resourceIconName,
  RESOURCE_TYPE_LABEL_MAP,
  type CheckState,
  type ConnectorDef,
  type DataSourceFormState,
  type GitLabProjectInput,
  type VisibleTreeRow,
} from './composables/useConnectorDefs'

defineProps<{
  step: number
  isEdit: boolean
  form: DataSourceFormState
  currentDef?: ConnectorDef
  gitlabProjects: GitLabProjectInput[]
  driveFolderToken: string
  driveFolderTokenError: string
  driveRootLoaded: boolean
  loadingResources: boolean
  resources: Resource[]
  selectedResourceCount: number
  hasExpandableNodes: boolean
  visibleTree: VisibleTreeRow[]
  expandedResourceIds: Set<string>
  loadingChildrenIds: Set<string>
  resourceRowState: (id: string) => CheckState
  schedulePresets: Array<{ label: string; value: string }>
  feishuParseMode: string
  dingtalkIncludeUploadedFiles: boolean
  yuqueFolderMode: string
  yuqueTOCOnly: boolean
}>()

const emit = defineEmits<{
  'add-gitlab-project': []
  'remove-gitlab-project': [index: number]
  'update:gitlab-project': [index: number, field: keyof GitLabProjectInput, value: string]
  'update:drive-folder-token': [token: string]
  'clear-drive-folder-error': []
  'load-drive-root': []
  'load-resources': []
  'expand-all': []
  'collapse-all': []
  'toggle-expand': [id: string]
  'toggle-resource': [id: string]
  'update:sync-schedule': [value: string]
  'update:sync-mode': [mode: 'incremental' | 'full']
  'update:conflict-strategy': [strategy: 'overwrite' | 'skip']
  'update:feishu-parse-mode': [mode: string]
  'update:sync-deletions': [enabled: boolean]
  'update:dingtalk-uploaded-files': [enabled: boolean]
  'update:yuque-folder-mode': [mode: string]
  'update:yuque-toc-only': [enabled: boolean]
}>()

const { t } = useI18n()

function shouldShowResourceType(type: string): boolean {
  return Boolean(RESOURCE_TYPE_LABEL_MAP[type])
}

function resourceTypeLabel(type: string): string {
  const key = RESOURCE_TYPE_LABEL_MAP[type]
  return key ? t(key) : ''
}
</script>

<style scoped lang="less">
@import './datasource-surface.less';
@import './datasource-editor-dialog.less';
</style>
