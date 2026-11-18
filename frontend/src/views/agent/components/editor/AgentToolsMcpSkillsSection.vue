<template>
  <div>
    <!-- Tools Configuration -->
    <div v-show="currentSection === 'tools' && isAgentMode" class="section">
      <div class="section-header">
        <h2>{{ $t('agent.editor.toolsConfig') }}</h2>
        <p class="section-description">{{ $t('agent.editor.toolsConfigDesc') }}</p>
      </div>

      <div class="tools-overview">
        <div class="tools-overview-row">
          <div class="tools-status-chip">
            <t-icon name="folder" />
            <template v-if="kbSelectionMode === 'none'">
              <span>{{ $t('agentEditor.tools.statusNoKb') }}</span>
            </template>
            <template v-else>
              <span class="tools-status-metric">
                <strong>{{ ragKbCount }}</strong> {{ $t('agentEditor.tools.kbMetricRag') }}
              </span>
              <span class="tools-status-sep">·</span>
              <span class="tools-status-metric">
                <strong>{{ wikiKbCount }}</strong> {{ $t('agentEditor.tools.kbMetricWiki') }}
              </span>
            </template>
          </div>
          <div v-if="inactiveToolCount > 0" class="tools-status-chip tools-status-chip--warn">
            <t-icon name="error-circle" />
            <span>{{ $t('agentEditor.tools.statusInactive', { count: inactiveToolCount }) }}</span>
          </div>
        </div>
      </div>

      <div class="settings-group">
        <div
          class="setting-row setting-row-vertical"
          data-agent-field="allowed_tools"
          :class="{ 'setting-row--field-highlight': highlightedField === 'allowed_tools' }"
        >
          <div class="setting-info">
            <label>{{ $t('agent.editor.allowedTools') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.selectTools') }}</p>
          </div>
          <div class="setting-control setting-control-full">
            <t-checkbox-group
              :model-value="formData.config.allowed_tools"
              class="tool-groups"
              @update:model-value="onAllowedToolsChange"
            >
              <section
                v-for="group in groupedAvailableTools"
                :key="group.key"
                :class="['tool-group', `tool-group--${group.key}`]"
              >
                <header class="tool-group-header">
                  <span class="tool-group-bar" />
                  <span class="tool-group-title">{{ group.label }}</span>
                  <span class="tool-group-count">{{ group.tools.length }}</span>
                  <span v-if="group.key === 'wiki_edit'" class="tool-group-warning">
                    <t-icon name="error-circle" />
                    {{ $t('agentEditor.tools.writeWarning') }}
                  </span>
                </header>
                <div class="tool-grid">
                  <t-checkbox
                    v-for="tool in group.tools"
                    :key="tool.value"
                    :value="tool.value"
                    :disabled="tool.disabled"
                    :class="['tool-card', { 'tool-card--disabled': tool.disabled, 'tool-card--danger': tool.danger }]"
                  >
                    <div class="tool-card-body">
                      <div class="tool-card-head">
                        <span class="tool-card-name">{{ tool.label }}</span>
                        <span v-if="tool.danger" class="tool-card-badge">
                          {{ $t('agentEditor.tools.dangerTag') }}
                        </span>
                      </div>
                      <span v-if="tool.description" class="tool-card-desc">{{ tool.description }}</span>
                      <span v-if="tool.unmet && tool.disabledReason" class="tool-card-hint">
                        {{ tool.disabledReason }}
                      </span>
                    </div>
                  </t-checkbox>
                </div>
              </section>
              <section v-if="storedExtras.length > 0" class="tool-group tool-group--stored">
                <header class="tool-group-header">
                  <span class="tool-group-bar" />
                  <span class="tool-group-title">{{ $t('agentEditor.tools.groupStored') }}</span>
                  <span class="tool-group-count">{{ storedExtras.length }}</span>
                </header>
                <div class="tool-grid">
                  <t-checkbox
                    v-for="extra in storedExtras"
                    :key="extra.value"
                    :value="extra.value"
                    :disabled="extra.disabled"
                    :class="['tool-card', { 'tool-card--disabled': extra.disabled }]"
                  >
                    <div class="tool-card-body">
                      <div class="tool-card-head">
                        <span class="tool-card-name">{{ extra.value }}</span>
                      </div>
                      <span class="tool-card-desc">
                        {{
                          extra.kind === 'switch_managed'
                            ? $t('agentEditor.tools.switchManagedDesc')
                            : $t('agentEditor.tools.unrecognizedDesc')
                        }}
                      </span>
                    </div>
                  </t-checkbox>
                </div>
              </section>
            </t-checkbox-group>
          </div>
        </div>

        <div class="setting-row setting-row-vertical">
          <div class="setting-info">
            <label>{{ $t('agentEditor.tools.effectiveLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.tools.effectiveDesc') }}</p>
          </div>
          <div class="setting-control setting-control-full">
            <div class="effective-tools">
              <template v-if="effectiveTools.length === 0">
                <div class="effective-tools-empty">
                  {{ $t('agentEditor.tools.effectiveEmpty') }}
                </div>
              </template>
              <template v-else>
                <span
                  v-for="item in effectiveTools"
                  :key="item.value"
                  :class="['effective-chip', { 'effective-chip--inactive': !item.active }]"
                  :title="item.reason || ''"
                >
                  <span class="effective-chip-label">{{ item.label }}</span>
                  <span v-if="!item.active" class="effective-chip-reason">{{ item.reason }}</span>
                </span>
              </template>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- MCP Configuration -->
    <div v-show="currentSection === 'mcp' && isAgentMode" class="section">
      <div class="section-header">
        <h2>{{ $t('agentEditor.mcp.label') }}</h2>
        <p class="section-description">{{ $t('agentEditor.mcp.desc') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.mcp.label') }}</label>
            <p class="desc">{{ $t('agentEditor.mcp.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-radio-group
              :model-value="mcpSelectionMode"
              @update:model-value="onMcpModeChange"
            >
              <t-radio-button value="all">{{ $t('agentEditor.selection.all') }}</t-radio-button>
              <t-radio-button value="selected">{{ $t('agentEditor.selection.selected') }}</t-radio-button>
              <t-radio-button value="none">{{ $t('agentEditor.selection.disabled') }}</t-radio-button>
            </t-radio-group>
          </div>
        </div>

        <div v-if="mcpSelectionMode === 'selected' && showMcpServiceSelect" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.mcp.selectLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.mcp.selectDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              :model-value="formData.config.mcp_services"
              multiple
              :placeholder="$t('agentEditor.mcp.selectPlaceholder')"
              filterable
              @update:model-value="onMcpServicesChange"
            >
              <t-option
                v-for="mcp in mcpOptions"
                :key="mcp.value"
                :value="mcp.value"
                :label="mcp.label"
                :disabled="mcp.disabled"
              />
            </t-select>
          </div>
        </div>

        <div v-if="mcpSelectionMode !== 'none'" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.mcp.authWaitTimeout') }}</label>
            <p class="desc">{{ $t('agentEditor.mcp.authWaitTimeoutDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              :model-value="formData.config.mcp_auth_wait_timeout"
              :min="5"
              :max="3600"
              theme="column"
              :placeholder="$t('agentEditor.mcp.authWaitTimeoutPlaceholder')"
              @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { mcp_auth_wait_timeout: val })"
            />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.mcp.accessLevel') }}</label>
            <p class="desc">{{ $t('agentEditor.mcp.accessLevelDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              :model-value="formData.config.access_level"
              @update:model-value="onAccessLevelChange"
            >
              <t-option
                v-for="level in ASSISTANT_ACCESS_LEVELS"
                :key="level"
                :value="level"
                :label="$t(`assistant.access.levels.${level}.label`)"
              />
            </t-select>
          </div>
        </div>
      </div>
    </div>

    <!-- Skills & Sandbox Configuration -->
    <div v-show="currentSection === 'skills' && isAgentMode" class="section">
      <div class="section-header">
        <h2>{{ $t('agent.editor.skillsConfig') }}</h2>
        <p class="section-description">
          {{ hostOnly ? $t('agent.editor.hostSkillsConfigDesc') : $t('agent.editor.skillsConfigDesc') }}
        </p>
      </div>

      <div class="settings-group">
        <div v-if="!hostOnly" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.sandboxBackend') }}</label>
            <p class="desc">{{ $t('agent.editor.sandboxBackendHint') }}</p>
          </div>
          <div class="setting-control sandbox-select-control">
            <t-select
              :model-value="formData.config.sandbox_config_id"
              :placeholder="$t('agent.editor.sandboxBackendDefault')"
              class="sandbox-config-select"
              filterable
              :popup-props="{ overlayClassName: 'sandbox-config-select-popup' }"
              @update:model-value="(val: unknown) => emit('updateConfig', { sandbox_config_id: String(val ?? '') })"
            >
              <t-option value="" :label="$t('agent.editor.sandboxBackendDefault')" />
              <t-option
                v-for="cfg in sandboxConfigOptions"
                :key="cfg.id"
                :value="cfg.id"
                :label="cfg.name"
              >
                <div class="sandbox-option">
                  <div class="sandbox-option__row">
                    <span class="sandbox-option__name">{{ cfg.name }}</span>
                    <span v-if="cfg.sandbox_type" class="sandbox-option__type">{{ backendLabel(cfg.sandbox_type) }}</span>
                  </div>
                  <div v-if="sandboxTargetLine(cfg)" class="sandbox-option__target">{{ sandboxTargetLine(cfg) }}</div>
                </div>
              </t-option>
            </t-select>
            <p v-if="selectedSandboxSummary" class="sandbox-selected-meta">{{ selectedSandboxSummary }}</p>
            <div class="sandbox-select-links">
              <button
                type="button"
                class="go-settings-link"
                @click="emit('openSandboxSettings')"
              >
                {{ $t('agent.editor.goSandboxSettings') }}
              </button>
              <template v-if="hasSandboxSelected && canInstallSkills">
                <span class="sandbox-select-links__sep" aria-hidden="true">·</span>
                <button
                  type="button"
                  class="go-settings-link"
                  @click="emit('openSkillSettings')"
                >
                  {{ $t('agent.editor.goSkillSettings') }}
                </button>
              </template>
            </div>
            <p v-if="sandboxConfigOptions.length === 0" class="desc empty-hint">
              {{ $t('agent.editor.sandboxNoConfigs') }}
            </p>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.skillsSelection') }}</label>
            <p class="desc">{{ skillsSelectionHint }}</p>
          </div>
          <div class="setting-control sandbox-select-control">
            <t-radio-group
              :model-value="skillsSelectionMode"
              @update:model-value="onSkillsModeChange"
            >
              <t-radio-button value="all" :disabled="!canEnableSkills">{{ $t('agent.editor.skillsAll') }}</t-radio-button>
              <t-radio-button value="selected" :disabled="!canEnableSkills">{{ $t('agent.editor.skillsSelected') }}</t-radio-button>
              <t-radio-button value="none">{{ $t('agent.editor.skillsNone') }}</t-radio-button>
            </t-radio-group>
            <p v-if="!hasSandboxSelected && sandboxConfigOptions.length > 1" class="desc empty-hint">
              {{ $t('agent.editor.skillsNeedSandbox') }}
            </p>
            <p v-else-if="hasSandboxSelected && skillCatalogCount === 0" class="desc empty-hint">
              <span>{{ $t('agent.editor.noSkillsAvailable') }}</span>
              <button
                v-if="canInstallSkills"
                type="button"
                class="go-settings-link"
                @click="emit('openSkillSettings')"
              >
                {{ $t('agent.editor.goSkillSettings') }}
              </button>
            </p>
          </div>
        </div>

        <div v-if="showCatalogSkillList" class="setting-row setting-row-vertical">
          <div class="setting-control setting-control-full">
            <t-checkbox-group
              :model-value="formData.config.selected_skills"
              class="skill-pick-list"
              @update:model-value="onSelectedSkillsChange"
            >
              <section
                v-for="group in catalogSkillGroups"
                :key="group.key"
                class="skill-pick-group"
                :class="`skill-pick-group--${group.key}`"
              >
                <header class="skill-pick-group__header">
                  <span class="skill-pick-group__bar" />
                  <span class="skill-pick-group__title">{{ group.label }}</span>
                  <span class="skill-pick-group__count">{{ group.skills.length }}</span>
                </header>
                <article
                  v-for="skill in group.skills"
                  :key="skill.name"
                  class="skill-pick"
                  :class="{
                    'skill-pick--ready': skill.selectable,
                    'skill-pick--pending': !skill.selectable,
                    'skill-pick--busy': isSkillBusy(skill),
                  }"
                >
                  <t-checkbox
                    v-if="skillsSelectionMode === 'selected'"
                    :value="skill.name"
                    :disabled="!skill.selectable"
                    class="skill-pick__check"
                  />
                  <div class="skill-pick__badge" aria-hidden="true">
                    <t-icon :name="SKILL_ICON" size="16px" />
                  </div>
                  <div class="skill-pick__body">
                    <div class="skill-pick__title-row">
                      <span class="skill-name" :title="skill.name">{{ skill.name }}</span>
                      <span
                        v-if="!skill.selectable"
                        class="skill-pick__hint"
                        :class="{ 'skill-pick__hint--busy': isSkillBusy(skill) }"
                      >
                        <t-icon :name="skillStatusIcon(skill)" size="14px" />
                        {{ skillStatusHint(skill) }}
                      </span>
                      <span
                        v-if="skill.selectable && skill.servedNote"
                        class="skill-pick__hint"
                        :class="{ 'skill-pick__hint--busy': isSkillBusy(skill) }"
                      >
                        <t-icon :name="isSkillBusy(skill) ? 'refresh' : 'error-circle'" size="14px" />
                        {{ skill.servedNote }}
                      </span>
                      <span
                        v-if="canUpgradeSkillRow(skill)"
                        class="skill-pick__hint skill-pick__hint--upgrade"
                      >
                        <t-icon name="arrow-up" size="14px" />
                        {{ skillUpgradeHint(skill) }}
                      </span>
                    </div>
                    <p
                      v-if="skill.description"
                      class="skill-desc"
                      :title="skill.description"
                    >{{ skill.description }}</p>
                  </div>
                  <t-button
                    v-if="canInstallSkillRow(skill)"
                    size="small"
                    variant="text"
                    theme="primary"
                    :loading="installingCatalogId === skill.id"
                    :title="installsAnUpgrade(skill) ? (hostOnly ? $t('agent.editor.hostUpgradeOnThisComputer') : $t('agent.editor.upgradeOnThisSandbox')) : (hostOnly ? $t('agent.editor.hostInstallToThisComputer') : $t('agent.editor.installToThisSandbox'))"
                    @click.stop="emit('installCatalogSkill', skill)"
                  >
                    {{ installsAnUpgrade(skill) ? $t('settings.skills.upgrade') : $t('agent.editor.installShort') }}
                  </t-button>
                  <t-button
                    v-else-if="canUpgradeSkillRow(skill)"
                    size="small"
                    variant="text"
                    theme="primary"
                    :loading="installingCatalogId === skill.id"
                    :title="hostOnly ? $t('agent.editor.hostUpgradeOnThisComputer') : $t('agent.editor.upgradeOnThisSandbox')"
                    @click.stop="emit('installCatalogSkill', skill)"
                  >
                    {{ $t('settings.skills.upgrade') }}
                  </t-button>
                  <t-button
                    v-else-if="canInstallSkills && isSkillBusy(skill)"
                    size="small"
                    variant="text"
                    theme="primary"
                    :title="$t('agent.editor.viewInstallProgress')"
                    @click.stop="emit('openSkillInstallProgress', skill)"
                  >
                    {{ $t('agent.editor.viewInstallProgress') }}
                  </t-button>
                </article>
              </section>
            </t-checkbox-group>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ASSISTANT_ACCESS_LEVELS, type AssistantAccessLevel } from '@/api/assistant'
import type { AgentToolCatalog } from '@/api/agent'
import type { SandboxConfigRecord } from '@/api/system'
import { SKILL_ICON } from '@/types/mention'
import type { AgentNotReadyReasonKey } from '@/utils/agent-readiness'
import type { RequirementMissKind, ScopeCapabilities } from '@/utils/tool-capabilities'
import {
  buildToolOptions,
  effectiveToolSelection,
  storedExtraTools,
  type ToolText,
} from '@/utils/agent-tool-list'
import type { AgentEditorFormConfig, AgentEditorFormData } from '@/views/agent/composables/useAgentEditorForm'
import type { CatalogSkillGroup, CatalogSkillRow } from '@/views/agent/composables/useAgentSkillsSandbox'

const props = defineProps<{
  currentSection: string
  isAgentMode: boolean
  highlightedField: AgentNotReadyReasonKey | null
  formData: AgentEditorFormData
  kbSelectionMode: 'all' | 'selected' | 'none'
  hasKnowledgeBase: boolean
  ragKbCount: number
  wikiKbCount: number
  scopeCapabilities: ScopeCapabilities
  agentToolCatalog: AgentToolCatalog | null
  storedToolNames: string[]
  allowedToolsToSave: () => string[] | null
  mcpSelectionMode: 'all' | 'selected' | 'none'
  mcpOptions: Array<{ label: string; value: string; disabled?: boolean }>
  showMcpServiceSelect: boolean
  hostOnly: boolean
  hasSandboxSelected: boolean
  canEnableSkills: boolean
  canInstallSkills: boolean
  skillsSelectionMode: 'all' | 'selected' | 'none'
  skillsSelectionHint: string
  skillCatalogCount: number
  showCatalogSkillList: boolean
  catalogSkillGroups: CatalogSkillGroup[]
  installingCatalogId: string
  sandboxConfigOptions: SandboxConfigRecord[]
  selectedSandboxSummary: string
  backendLabel: (type: string) => string
  sandboxTargetLine: (cfg: SandboxConfigRecord) => string
  skillStatusHint: (skill: CatalogSkillRow) => string
  skillStatusIcon: (skill: CatalogSkillRow) => string
  isSkillBusy: (skill: CatalogSkillRow) => boolean
  canInstallSkillRow: (skill: CatalogSkillRow) => boolean
  canUpgradeSkillRow: (skill: CatalogSkillRow) => boolean
  installsAnUpgrade: (skill: CatalogSkillRow) => boolean
  skillUpgradeHint: (skill: CatalogSkillRow) => string
}>()

const emit = defineEmits<{
  (e: 'updateConfig', patch: Partial<AgentEditorFormConfig>): void
  (e: 'update:mcpSelectionMode', mode: 'all' | 'selected' | 'none'): void
  (e: 'update:skillsSelectionMode', mode: 'all' | 'selected' | 'none'): void
  (e: 'openSandboxSettings'): void
  (e: 'openSkillSettings'): void
  (e: 'installCatalogSkill', skill: CatalogSkillRow): void
  (e: 'openSkillInstallProgress', skill: CatalogSkillRow): void
}>()

const { t } = useI18n()

const toolTexts = computed<Record<string, ToolText>>(() => ({
  thinking: { label: t('agentEditor.tools.thinking'), description: t('agentEditor.tools.thinkingDesc') },
  todo_write: { label: t('agentEditor.tools.todoWrite'), description: t('agentEditor.tools.todoWriteDesc') },
  ask_user: { label: t('agentEditor.tools.askUser'), description: t('agentEditor.tools.askUserDesc') },
  search_conversations: { label: t('agentEditor.tools.searchConversations'), description: t('agentEditor.tools.searchConversationsDesc') },
  search_knowledge: { label: t('agentEditor.tools.searchKnowledge'), description: t('agentEditor.tools.searchKnowledgeDesc') },
  read_document: { label: t('agentEditor.tools.readDocument'), description: t('agentEditor.tools.readDocumentDesc') },
  list_documents: { label: t('agentEditor.tools.listDocuments'), description: t('agentEditor.tools.listDocumentsDesc') },
  query_knowledge_graph: { label: t('agentEditor.tools.queryGraph'), description: t('agentEditor.tools.queryGraphDesc') },
  database_query: { label: t('agentEditor.tools.dbQuery'), description: t('agentEditor.tools.dbQueryDesc') },
  wiki_search: { label: t('agentEditor.tools.wikiSearch'), description: t('agentEditor.tools.wikiSearchDesc') },
  wiki_read_page: { label: t('agentEditor.tools.wikiReadPage'), description: t('agentEditor.tools.wikiReadPageDesc') },
  wiki_flag_issue: { label: t('agentEditor.tools.wikiFlagIssue'), description: t('agentEditor.tools.wikiFlagIssueDesc') },
  wiki_write_page: { label: t('agentEditor.tools.wikiWritePage'), description: t('agentEditor.tools.wikiWritePageDesc') },
  wiki_replace_text: { label: t('agentEditor.tools.wikiReplaceText'), description: t('agentEditor.tools.wikiReplaceTextDesc') },
  wiki_rename_page: { label: t('agentEditor.tools.wikiRenamePage'), description: t('agentEditor.tools.wikiRenamePageDesc') },
  wiki_delete_page: { label: t('agentEditor.tools.wikiDeletePage'), description: t('agentEditor.tools.wikiDeletePageDesc') },
  wiki_read_issue: { label: t('agentEditor.tools.wikiReadIssue'), description: t('agentEditor.tools.wikiReadIssueDesc') },
  wiki_update_issue: { label: t('agentEditor.tools.wikiUpdateIssue'), description: t('agentEditor.tools.wikiUpdateIssueDesc') },
  data_analysis: { label: t('agentEditor.tools.dataAnalysis'), description: t('agentEditor.tools.dataAnalysisDesc') },
  data_schema: { label: t('agentEditor.tools.dataSchema'), description: t('agentEditor.tools.dataSchemaDesc') },
}))

const toolGroups = computed(() => [
  { key: 'base', label: t('agentEditor.tools.groupBase') },
  { key: 'rag', label: t('agentEditor.tools.groupRag') },
  { key: 'wiki_read', label: t('agentEditor.tools.groupWikiRead') },
  { key: 'wiki_edit', label: t('agentEditor.tools.groupWikiEdit') },
  { key: 'wiki_issue', label: t('agentEditor.tools.groupWikiIssue') },
  { key: 'data', label: t('agentEditor.tools.groupData') },
])

function missKindToReason(kind: RequirementMissKind): string | undefined {
  switch (kind) {
    case 'needsKb':
      return t('agentEditor.tools.requiresKb')
    case 'needsWiki':
      return t('agentEditor.tools.requiresWikiKb')
    case 'needsRag':
    case 'needsGraph':
    case 'needsFaq':
      return t('agentEditor.tools.requiresRagKb')
    case 'none':
    default:
      return undefined
  }
}

const availableTools = computed(() => {
  const options = buildToolOptions(
    props.agentToolCatalog,
    props.formData.config.allowed_tools || [],
    props.scopeCapabilities,
    props.hasKnowledgeBase,
    (name) => toolTexts.value[name],
  )
  return options.map((tool) => ({
    ...tool,
    disabledReason: tool.unmet ? missKindToReason(tool.missKind) : undefined,
  }))
})

const storedExtras = computed(() =>
  storedExtraTools(
    props.agentToolCatalog,
    [...props.storedToolNames, ...(props.formData.config.allowed_tools || [])],
    props.formData.config.allowed_tools || [],
  ),
)

const groupedAvailableTools = computed(() => {
  const grouped = Object.groupBy(availableTools.value, (tool) => tool.group || 'base')
  const known = new Set(toolGroups.value.map((g) => g.key))
  const extraGroups = Object.keys(grouped)
    .filter((key) => !known.has(key))
    .map((key) => ({ key, label: key }))
  return [...toolGroups.value, ...extraGroups]
    .map((g) => ({
      ...g,
      tools: grouped[g.key] ?? [],
    }))
    .filter((g) => g.tools.length > 0)
})

const effectiveSelection = computed(() =>
  effectiveToolSelection(
    props.agentToolCatalog,
    props.allowedToolsToSave(),
    props.scopeCapabilities,
    props.hasKnowledgeBase,
  ),
)

const effectiveTools = computed(() => {
  const active = new Set(effectiveSelection.value.names)
  const inactive = new Set(effectiveSelection.value.inactive)
  const items: Array<{ value: string; label: string; reason?: string; active: boolean }> = []
  for (const tool of availableTools.value) {
    if (active.has(tool.value)) {
      items.push({ value: tool.value, label: tool.label, active: true })
    } else if (inactive.has(tool.value)) {
      items.push({ value: tool.value, label: tool.label, active: false, reason: tool.disabledReason })
    }
  }
  if (props.formData.config.web_search_enabled) {
    items.push({ value: 'web_search', label: t('agentEditor.tools.webSearch'), active: true })
    items.push({ value: 'web_fetch', label: t('agentEditor.tools.webFetch'), active: true })
  }
  return items
})

const inactiveToolCount = computed(() => effectiveTools.value.filter((i) => !i.active).length)

function onAllowedToolsChange(val: unknown) {
  if (Array.isArray(val)) {
    emit('updateConfig', { allowed_tools: val.map((item) => String(item)) })
  }
}

function onMcpModeChange(val: unknown) {
  if (val === 'all' || val === 'selected' || val === 'none') {
    emit('update:mcpSelectionMode', val)
  }
}

function onMcpServicesChange(val: unknown) {
  if (Array.isArray(val)) {
    emit('updateConfig', { mcp_services: val.map((item) => String(item)) })
  }
}

function onAccessLevelChange(val: unknown) {
  if (typeof val === 'string' && (ASSISTANT_ACCESS_LEVELS as readonly string[]).includes(val)) {
    emit('updateConfig', { access_level: val as AssistantAccessLevel })
  }
}

function onSkillsModeChange(val: unknown) {
  if (val === 'all' || val === 'selected' || val === 'none') {
    emit('update:skillsSelectionMode', val)
  }
}

function onSelectedSkillsChange(val: unknown) {
  if (Array.isArray(val)) {
    emit('updateConfig', { selected_skills: val.map((item) => String(item)) })
  }
}
</script>

<style lang="less">
.settings-modal-shell.agent-editor-shell {
  .tools-overview {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-bottom: 12px;
    padding: 12px 14px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: var(--app-radius-lg);
    border: 1px solid var(--td-component-stroke);
  }

  .tools-overview-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 12px;

    &--preset {
      border-top: 1px dashed var(--td-component-stroke);
      padding-top: 10px;
      justify-content: space-between;
    }
  }

  .tools-status-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-container);
    border-radius: var(--app-radius-pill);
    border: 1px solid var(--td-component-stroke);

    .t-icon {
      color: var(--td-text-color-secondary);
      font-size: var(--app-text-base);
    }

    .tools-status-metric {
      display: inline-flex;
      align-items: baseline;
      gap: 4px;

      strong {
        font-size: var(--app-text-base);
        font-weight: 600;
        color: var(--td-text-color-primary);
      }
    }

    .tools-status-sep {
      color: var(--td-text-color-placeholder);
    }

    &--warn {
      color: var(--td-warning-color);
      background: var(--td-warning-color-1);
      border-color: var(--td-warning-color-light);

      .t-icon {
        color: var(--td-warning-color);
      }
    }
  }

  .tool-groups {
    display: flex;
    flex-direction: column;
    gap: 22px;
    width: 100%;
  }

  .tool-group {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .tool-group-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-bottom: 2px;

    .tool-group-bar {
      display: inline-block;
      width: 3px;
      height: 14px;
      border-radius: 2px;
      background: var(--td-brand-color);
    }

    .tool-group-title {
      font-size: var(--app-text-md);
      font-weight: 600;
      color: var(--td-text-color-primary);
      letter-spacing: 0.2px;
    }

    .tool-group-count {
      min-width: 20px;
      padding: 0 6px;
      font-size: var(--app-text-xs);
      color: var(--td-text-color-secondary);
      background: var(--td-bg-color-secondarycontainer);
      border-radius: var(--app-radius-pill);
      text-align: center;
      line-height: 18px;
    }

    .tool-group-warning {
      margin-left: auto;
      display: inline-flex;
      align-items: center;
      gap: 4px;
      padding: 2px 8px;
      font-size: var(--app-text-sm);
      color: var(--td-warning-color);
      background: var(--td-warning-color-1);
      border: 1px solid var(--td-warning-color-light);
      border-radius: var(--app-radius-pill);

      .t-icon {
        font-size: var(--app-text-md);
      }
    }
  }

  .tool-group--base .tool-group-bar {
    background: var(--td-gray-color-6);
  }

  .tool-group--rag .tool-group-bar {
    background: var(--td-brand-color);
  }

  .tool-group--wiki_read .tool-group-bar {
    background: var(--td-success-color);
  }

  .tool-group--wiki_edit .tool-group-bar {
    background: var(--td-warning-color);
  }

  .tool-group--wiki_issue .tool-group-bar {
    background: var(--gy-accent-purple);
  }

  .tool-group--data .tool-group-bar {
    background: var(--gy-accent-teal);
  }

  .tool-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    width: 100%;

    @media (max-width: 720px) {
      grid-template-columns: 1fr;
    }
  }

  .tool-card {
    margin: 0;
    padding: 12px 14px;
    background: var(--td-bg-color-container);
    border-radius: var(--app-radius-md);
    border: 1px solid var(--td-component-stroke);
    transition: border-color var(--app-motion-base), background var(--app-motion-base);
    cursor: pointer;
    overflow: hidden;

    &:hover:not(.tool-card--disabled) {
      border-color: var(--td-brand-color);
      background: var(--td-brand-color-1);
    }

    .t-checkbox__input {
      margin-top: 2px;
      flex-shrink: 0;
    }

    .t-checkbox__label {
      flex: 1;
      min-width: 0;
      padding-left: 10px;
    }

    &.t-is-checked {
      border-color: var(--td-brand-color);
      background: var(--td-brand-color-1);
    }

    &--disabled {
      cursor: not-allowed;
      opacity: 0.6;
    }

    &--danger {
      border-color: var(--td-warning-color-light);

      &:hover:not(.tool-card--disabled) {
        border-color: var(--td-warning-color);
        background: var(--td-warning-color-1);
      }

      &.t-is-checked {
        border-color: var(--td-warning-color);
        background: var(--td-warning-color-1);
      }
    }
  }

  .tool-card-body {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .tool-card-head {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  .tool-card-name {
    font-size: 13.5px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    line-height: 1.4;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 0 1 auto;
    min-width: 0;
  }

  .tool-card-badge {
    flex: 0 0 auto;
    font-size: 10.5px;
    line-height: 1;
    padding: 3px 6px;
    color: var(--td-warning-color);
    background: transparent;
    border: 1px solid var(--td-warning-color-light);
    border-radius: var(--app-radius-xs);
    letter-spacing: 0.3px;
  }

  .tool-card-desc {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .tool-card-hint {
    font-size: var(--app-text-xs);
    color: var(--td-warning-color);
    font-style: italic;
    line-height: 1.4;
  }

  .tool-card--disabled {
    .tool-card-name,
    .tool-card-desc {
      color: var(--td-text-color-placeholder);
    }
  }

  .effective-tools {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding: 12px;
    background: var(--td-bg-color-container);
    border-radius: var(--app-radius-md);
    border: 1px dashed var(--td-component-stroke);
    min-height: 52px;
    align-items: flex-start;
  }

  .effective-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 10px;
    font-size: var(--app-text-sm);
    line-height: 18px;
    color: var(--td-brand-color);
    background: color-mix(in oklab, var(--td-brand-color) 10%, transparent);
    border: 1px solid color-mix(in oklab, var(--td-brand-color) 22%, transparent);
    border-radius: var(--app-radius-pill);
    max-width: 100%;
  }

  .effective-chip-label {
    font-weight: 500;
  }

  .effective-chip-reason {
    font-size: var(--app-text-xs);
    color: var(--td-warning-color);
    font-style: normal;

    &::before {
      content: '· ';
      color: var(--td-text-color-placeholder);
      margin-right: 2px;
    }
  }

  .effective-chip--inactive {
    color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-secondarycontainer);
    border-color: var(--td-component-stroke);

    .effective-chip-label {
      text-decoration: line-through;
    }
  }

  .effective-tools-empty {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    font-style: italic;
  }
}
</style>
