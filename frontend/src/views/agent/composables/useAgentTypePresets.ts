import { computed, type ComputedRef, type Ref } from 'vue'
import type {
  AgentType,
  AgentTypePreset,
  AgentTypeKBFilter,
  KBCapabilities,
} from '@/api/agent'
import type { PromptTemplate } from '@/api/system'
import { normalizeLegacyToolNames } from '../../../utils/legacy-tool-names'
import { deriveKbFilterFromTools } from '../../../utils/tool-capabilities'
import type { AgentEditorFormData, AgentKbOption } from './useAgentEditorForm'

export type FilteredKbOption = AgentKbOption & {
  disabled: boolean
  disabledReason: string
}

const PRESET_KB_MISMATCH_KEY_MAP: Record<string, string> = {
  'rag-qa': 'ragQa',
  'wiki-qa': 'wikiQa',
  'hybrid-rag-wiki': 'hybridRagWiki',
  'data-analysis': 'dataAnalysis',
}

export interface UseAgentTypePresetsOptions {
  formData: Ref<AgentEditorFormData>
  agentMode: ComputedRef<'quick-answer' | 'smart-reasoning'>
  isAgentMode: ComputedRef<boolean>
  kbSelectionMode: Ref<'all' | 'selected' | 'none'>
  kbOptions: Ref<AgentKbOption[]>
  agentTypePresets: Ref<AgentTypePreset[]>
  agentSystemPromptTemplates: Ref<PromptTemplate[]>
  i18nLocale: { readonly value: string }
  t: (key: string, params?: Record<string, unknown>) => string
  onWarn: (content: string, duration?: number) => void
}

export function useAgentTypePresets(options: UseAgentTypePresetsOptions) {
  const {
    formData,
    agentMode,
    isAgentMode,
    kbSelectionMode,
    kbOptions,
    agentTypePresets,
    agentSystemPromptTemplates,
    i18nLocale,
    t,
    onWarn,
  } = options

  const agentType = computed<AgentType>({
    get: () => formData.value.config.agent_type || 'custom',
    set: (val: AgentType) => {
      formData.value.config.agent_type = val
    },
  })

  const activeAgentTypePreset = computed<AgentTypePreset | null>(() => {
    if (!isAgentMode.value) return null
    const id = agentType.value
    if (!id || id === 'custom') return null
    return agentTypePresets.value.find((p) => p.id === id) || null
  })

  const agentTypePresetLabel = (p: AgentTypePreset): string => {
    const locale = i18nLocale.value || 'default'
    return p.i18n?.[locale]?.label || p.i18n?.default?.label || p.id
  }

  const agentTypePresetDescription = (p: AgentTypePreset): string => {
    const locale = i18nLocale.value || 'default'
    return p.i18n?.[locale]?.description || p.i18n?.default?.description || ''
  }

  const agentTypeSelectOptions = computed(() =>
    agentTypePresets.value.map((p) => ({
      value: p.id,
      label: agentTypePresetLabel(p),
      desc: agentTypePresetDescription(p),
    })),
  )

  const getPresetDefaultName = (preset: AgentTypePreset | null): string => {
    if (!preset || preset.id === 'custom') return ''
    return t('agentEditor.agentType.defaultNamePattern', { label: agentTypePresetLabel(preset) })
  }

  const getPresetDefaultDescription = (preset: AgentTypePreset | null): string => {
    if (!preset) return ''
    return agentTypePresetDescription(preset)
  }

  const isNameSystemGenerated = (name: string): boolean => {
    if (!name) return true
    return agentTypePresets.value.some((p) => getPresetDefaultName(p) === name)
  }

  const isDescriptionSystemGenerated = (desc: string): boolean => {
    if (!desc) return true
    return agentTypePresets.value.some((p) => getPresetDefaultDescription(p) === desc)
  }

  const presetKbMismatchReason = (preset: AgentTypePreset): string => {
    const subKey = PRESET_KB_MISMATCH_KEY_MAP[preset.id]
    if (subKey) return t(`agentEditor.agentType.kbMismatch.${subKey}`)
    return t('agentEditor.agentType.kbMismatch.generic')
  }

  const effectiveKbFilter = (preset: AgentTypePreset | null): AgentTypeKBFilter | null => {
    if (!preset) return null
    const derived = deriveKbFilterFromTools(preset.config?.allowed_tools || [])
    const yaml = preset.kb_filter
    const anyOf = yaml?.any_of && yaml.any_of.length > 0 ? yaml.any_of : (derived?.any_of ?? [])
    const allOf = yaml?.all_of ?? []
    const noneOf = yaml?.none_of ?? []
    if (anyOf.length === 0 && allOf.length === 0 && noneOf.length === 0) return null
    return { any_of: anyOf, all_of: allOf, none_of: noneOf }
  }

  const kbSatisfiesPresetFilter = (
    kb: { capabilities?: KBCapabilities; ragEnabled?: boolean; wikiEnabled?: boolean; type?: string },
    preset: AgentTypePreset | null,
  ): { ok: boolean; reason: string } => {
    const filter = effectiveKbFilter(preset)
    if (!preset || !filter) return { ok: true, reason: '' }
    const caps = kb.capabilities || {
      vector: Boolean(kb.ragEnabled),
      keyword: Boolean(kb.ragEnabled),
      wiki: Boolean(kb.wikiEnabled),
      graph: false,
      faq: kb.type === 'faq',
    }
    const has = (name: string): boolean => {
      switch (name) {
        case 'vector':
          return Boolean(caps.vector)
        case 'keyword':
          return Boolean(caps.keyword)
        case 'wiki':
          return Boolean(caps.wiki)
        case 'graph':
          return Boolean(caps.graph)
        case 'faq':
          return Boolean(caps.faq)
        default:
          return false
      }
    }
    const reason = presetKbMismatchReason(preset)
    if (filter.all_of && filter.all_of.length > 0) {
      for (const n of filter.all_of) {
        if (!has(n)) return { ok: false, reason }
      }
    }
    if (filter.any_of && filter.any_of.length > 0) {
      if (!filter.any_of.some((n) => has(n))) {
        return { ok: false, reason }
      }
    }
    if (filter.none_of && filter.none_of.length > 0) {
      for (const n of filter.none_of) {
        if (has(n)) return { ok: false, reason }
      }
    }
    return { ok: true, reason: '' }
  }

  const kbSatisfiesQuickAnswerMode = (kb: {
    capabilities?: KBCapabilities
    ragEnabled?: boolean
  }): { ok: boolean; reason: string } => {
    if (agentMode.value !== 'quick-answer') return { ok: true, reason: '' }
    const hasRag = kb.capabilities
      ? Boolean(kb.capabilities.vector || kb.capabilities.keyword)
      : Boolean(kb.ragEnabled)
    if (hasRag) return { ok: true, reason: '' }
    return { ok: false, reason: t('agentEditor.agentType.kbMismatch.quickAnswer') }
  }

  const filteredKbOptionsForPreset = computed<FilteredKbOption[]>(() => {
    const preset = activeAgentTypePreset.value
    return kbOptions.value.map((kb) => {
      const presetResult = kbSatisfiesPresetFilter(kb, preset)
      const modeResult = kbSatisfiesQuickAnswerMode(kb)
      const ok = presetResult.ok && modeResult.ok
      const reason = !presetResult.ok ? presetResult.reason : (!modeResult.ok ? modeResult.reason : '')
      return { ...kb, disabled: !ok, disabledReason: reason }
    })
  })

  const filteredMyKbOptions = computed(() =>
    filteredKbOptionsForPreset.value.filter((kb) => !kb.shared),
  )
  const filteredSharedKbOptions = computed(() =>
    filteredKbOptionsForPreset.value.filter((kb) => kb.shared),
  )

  const incompatibleSelectedKbCount = computed(() => {
    if (kbSelectionMode.value !== 'selected') return 0
    const selected = new Set(formData.value.config.knowledge_bases || [])
    return filteredKbOptionsForPreset.value.filter((kb) => selected.has(kb.value) && kb.disabled).length
  })

  const applyAgentTypePreset = (preset: AgentTypePreset | null) => {
    if (!preset || !preset.config) return
    const c = preset.config
    const target = formData.value.config
    if (c.system_prompt_id !== undefined) {
      target.system_prompt_id = c.system_prompt_id
      const tmpl = agentSystemPromptTemplates.value.find((item) => item.id === c.system_prompt_id)
      if (tmpl && typeof tmpl.content === 'string') {
        target.system_prompt = tmpl.content
      } else {
        target.system_prompt = ''
        if (c.system_prompt_id) {
          console.warn(
            `[AgentType] system_prompt_id "${c.system_prompt_id}" not found in agent_system_prompt templates`,
          )
        }
      }
    }
    if (typeof c.temperature === 'number') target.temperature = c.temperature
    if (typeof c.max_iterations === 'number') target.max_iterations = c.max_iterations
    if (Array.isArray(c.allowed_tools)) target.allowed_tools = normalizeLegacyToolNames(c.allowed_tools)
    if (typeof c.retain_retrieval_history === 'boolean') {
      target.retain_retrieval_history = c.retain_retrieval_history
    }
    if (typeof c.faq_priority_enabled === 'boolean') {
      target.faq_priority_enabled = c.faq_priority_enabled
    }
    if (typeof c.web_search_enabled === 'boolean') {
      target.web_search_enabled = c.web_search_enabled
    }
    if (Array.isArray(c.supported_file_types)) {
      target.supported_file_types = [...c.supported_file_types]
    } else {
      target.supported_file_types = []
    }
    if (c.kb_selection_mode) {
      target.kb_selection_mode = c.kb_selection_mode
      kbSelectionMode.value = c.kb_selection_mode
    }
  }

  const onAgentTypeChange = (val: AgentType) => {
    const canOverrideName = isNameSystemGenerated(formData.value.name)
    const canOverrideDesc = isDescriptionSystemGenerated(formData.value.description)

    agentType.value = val
    const preset = agentTypePresets.value.find((p) => p.id === val) || null
    if (val !== 'custom') {
      applyAgentTypePreset(preset)
    }

    if (canOverrideName) {
      formData.value.name = getPresetDefaultName(preset)
    }
    if (canOverrideDesc) {
      formData.value.description = getPresetDefaultDescription(preset)
    }

    if (incompatibleSelectedKbCount.value > 0) {
      onWarn(
        t('agentEditor.agentType.kbIncompatibleWarn', { count: incompatibleSelectedKbCount.value }),
        4000,
      )
    }
  }

  return {
    agentType,
    activeAgentTypePreset,
    agentTypePresetDescription,
    agentTypeSelectOptions,
    getPresetDefaultName,
    getPresetDefaultDescription,
    filteredMyKbOptions,
    filteredSharedKbOptions,
    applyAgentTypePreset,
    onAgentTypeChange,
  }
}
