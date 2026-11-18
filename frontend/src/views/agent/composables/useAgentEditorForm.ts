import { ref, computed, watch, toRaw } from 'vue'
import type {
  CustomAgent,
  CustomAgentConfig,
  AgentType,
  AgentTypePreset,
  AgentToolCatalog,
  KBCapabilities,
  QuestionSuggestionConfig,
} from '@/api/agent'
import type { AssistantAccessLevel } from '@/api/assistant'
import type { ModelConfig } from '@/api/model'
import type {
  PromptTemplate,
  PromptTemplatesConfig,
  StorageEngineStatusItem,
} from '@/api/system'
import type { WebSearchProviderEntity } from '@/api/web-search-provider'
import type { ParserEngineRule } from '@/views/knowledge/settings/KBParserSettings.vue'
import { selectInitialModelId } from '../../../utils/modelDefaults'
import { hydrateAgentPromptRefs } from '../../../utils/agentPromptTemplates'
import { agentRequiresRerankModel } from '../../../utils/agent-readiness'
import { normalizeLegacyToolNames } from '../../../utils/legacy-tool-names'
import type { ScopeCapabilities } from '../../../utils/tool-capabilities'
import {
  allowedToolsToSave as allowedToolsForSave,
  resolveLoadedAllowedTools,
} from '../../../utils/agent-tool-list'
import {
  clampLevel,
  levelEnablesThinking,
  levelFromLegacy,
  modelCanThink,
  modelCannotDisableThinking,
  optionsFor,
  type ReasoningLevel,
} from '../../../utils/reasoningEffort'

export interface AgentEditorFormConfig extends CustomAgentConfig {
  agent_mode: 'quick-answer' | 'smart-reasoning'
  agent_type: AgentType
  system_prompt: string
  system_prompt_id: string
  context_template: string
  model_id: string
  rerank_model_id: string
  temperature: number
  max_completion_tokens: number
  thinking: boolean
  reasoning_effort: string
  citation_enabled: boolean
  max_iterations: number
  llm_call_timeout: number
  allowed_tools: string[] | null
  reflection_enabled: boolean
  mcp_selection_mode: 'all' | 'selected' | 'none'
  mcp_services: string[]
  mcp_auth_wait_timeout: number
  access_level: AssistantAccessLevel
  skills_selection_mode: 'all' | 'selected' | 'none'
  selected_skills: string[]
  sandbox_config_id: string
  kb_selection_mode: 'all' | 'selected' | 'none'
  knowledge_bases: string[]
  retrieve_kb_only_when_mentioned: boolean
  image_upload_enabled: boolean
  vlm_model_id: string
  image_storage_provider: string
  audio_upload_enabled?: boolean
  asr_model_id?: string
  attachment_image_understanding: boolean
  attachment_ocr_max_pages: number
  attachment_parse_wait_timeout_sec: number
  chat_parser_engine_rules: ParserEngineRule[]
  supported_file_types: string[]
  data_analysis_enabled: boolean
  faq_priority_enabled: boolean
  faq_direct_answer_threshold: number
  faq_score_boost: number
  web_search_enabled: boolean
  web_search_provider_id?: string
  web_search_max_results: number
  web_fetch_enabled?: boolean
  web_fetch_top_n?: number
  multi_turn_enabled: boolean
  history_turns: number
  retain_retrieval_history: boolean
  memory_enabled: boolean
  embedding_top_k: number
  keyword_threshold: number
  vector_threshold: number
  rerank_top_k: number
  rerank_threshold: number
  enable_query_expansion: boolean
  enable_rewrite: boolean
  query_understand_model_id: string
  rewrite_prompt_system: string
  rewrite_prompt_user: string
  fallback_strategy: 'fixed' | 'model'
  fallback_response: string
  fallback_prompt: string
  question_suggestions: QuestionSuggestionConfig
  welcome_message: string
}

export interface AgentEditorFormData {
  id?: string
  name: string
  description: string
  avatar?: string
  is_builtin: boolean
  config: AgentEditorFormConfig
}

export interface AgentKbOption {
  label: string
  value: string
  type?: 'document' | 'faq'
  count?: number
  shared?: boolean
  orgName?: string
  ragEnabled?: boolean
  wikiEnabled?: boolean
  capabilities?: KBCapabilities
}

export interface RawKnowledgeBaseItem {
  id: string
  name: string
  type?: 'document' | 'faq'
  chunk_count?: number
  knowledge_count?: number
  indexing_strategy?: {
    vector_enabled?: boolean
    keyword_enabled?: boolean
    wiki_enabled?: boolean
  }
  capabilities?: KBCapabilities
}

export interface SharedKnowledgeBaseEntry {
  org_name?: string
  knowledge_base?: RawKnowledgeBaseItem | null
}

function unwrapReactive<T>(value: T): T {
  const raw = toRaw(value)
  if (Array.isArray(raw)) {
    return raw.map((item) => unwrapReactive(item)) as unknown as T
  }
  if (raw && typeof raw === 'object' && Object.getPrototypeOf(raw) === Object.prototype) {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(raw)) {
      out[k] = unwrapReactive(v)
    }
    return out as T
  }
  return raw
}

export function clonePlain<T>(value: T): T {
  return structuredClone(unwrapReactive(value))
}

export function createDefaultAgentFormData(): AgentEditorFormData {
  return {
    name: '',
    description: '',
    is_builtin: false,
    config: {
      agent_mode: 'smart-reasoning',
      system_prompt: '',
      context_template: '',
      model_id: '',
      rerank_model_id: '',
      temperature: 0.7,
      max_completion_tokens: 0,
      thinking: false,
      reasoning_effort: 'off',
      citation_enabled: true,
      max_iterations: 10,
      llm_call_timeout: 120,
      allowed_tools: [],
      reflection_enabled: false,
      mcp_selection_mode: 'none',
      mcp_services: [],
      mcp_auth_wait_timeout: 600,
      access_level: 'auto_write',
      skills_selection_mode: 'none',
      selected_skills: [],
      sandbox_config_id: '',
      kb_selection_mode: 'all',
      knowledge_bases: [],
      retrieve_kb_only_when_mentioned: false,
      agent_type: 'rag-qa',
      system_prompt_id: '',
      image_upload_enabled: false,
      vlm_model_id: '',
      image_storage_provider: '',
      attachment_image_understanding: false,
      attachment_ocr_max_pages: 0,
      attachment_parse_wait_timeout_sec: 0,
      chat_parser_engine_rules: [],
      supported_file_types: [],
      data_analysis_enabled: false,
      faq_priority_enabled: true,
      faq_direct_answer_threshold: 0.9,
      faq_score_boost: 1.2,
      web_search_enabled: false,
      web_search_max_results: 5,
      multi_turn_enabled: false,
      history_turns: 5,
      retain_retrieval_history: false,
      memory_enabled: true,
      embedding_top_k: 10,
      keyword_threshold: 0.3,
      vector_threshold: 0.5,
      rerank_top_k: 5,
      rerank_threshold: 0.5,
      enable_query_expansion: true,
      enable_rewrite: true,
      query_understand_model_id: '',
      rewrite_prompt_system: '',
      rewrite_prompt_user: '',
      fallback_strategy: 'model',
      fallback_response: '',
      fallback_prompt: '',
      question_suggestions: {
        starters: {
          enabled: true,
          mode: 'hybrid',
          items: [],
          count: 6,
        },
        follow_ups: {
          enabled: false,
          mode: 'hybrid',
          count: 3,
          model_id: '',
          additional_instruction: '',
          categories: ['clarify', 'deepen', 'action'],
          max_context_turns: 2,
          suppress_on_fallback: true,
          suppress_when_answer_asks_question: true,
          knowledge_fallback: true,
          allow_regenerate: false,
        },
      },
      welcome_message: '',
    },
  }
}

const DEFAULT_QUICK_ANSWER_MAX_TOKENS = 2048
const DEFAULT_SMART_REASONING_MAX_TOKENS = 24576
const DEFAULT_SANDBOX_WRITE_MAX_TOKENS = 24576
const KNOWLEDGE_BASE_TOOLS = ['search_knowledge', 'read_document', 'list_documents']
const WIKI_READ_TOOLS = ['wiki_search', 'wiki_read_page', 'read_document', 'wiki_flag_issue']

function defaultMaxCompletionTokensFor(mode: string, sandboxConfigId?: string): number {
  if (mode === 'smart-reasoning') {
    return sandboxConfigId ? DEFAULT_SANDBOX_WRITE_MAX_TOKENS : DEFAULT_SMART_REASONING_MAX_TOKENS
  }
  return DEFAULT_QUICK_ANSWER_MAX_TOKENS
}

function hasPlaceholder(text: string | undefined, placeholder: string): boolean {
  if (!text) return false
  return text.includes(`{{${placeholder}}}`)
}

export function mapKbToOption(kb: RawKnowledgeBaseItem, shared: boolean, orgName?: string): AgentKbOption {
  const strategy = kb.indexing_strategy
  const caps = kb.capabilities
  return {
    label: kb.name,
    value: kb.id,
    type: kb.type || 'document',
    count: kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0),
    shared,
    orgName,
    ragEnabled: caps ? (caps.vector || caps.keyword) : (!strategy || strategy.vector_enabled || strategy.keyword_enabled),
    wikiEnabled: caps ? caps.wiki : (strategy?.wiki_enabled || false),
    capabilities: caps,
  }
}

export interface UseAgentEditorFormOptions {
  props: {
    readonly visible: boolean
    readonly mode: 'create' | 'edit'
    readonly agent?: CustomAgent | null
  }
  editorInitializing: { value: boolean }
  currentSection: { value: string }
  t: (key: string, params?: Record<string, unknown>) => string
  onValidationError: (msg: string, targetSection: string) => void
  onSandboxConfigChanged: () => Promise<void>
}

export function useAgentEditorForm(options: UseAgentEditorFormOptions) {
  const { props, editorInitializing, currentSection, t, onValidationError, onSandboxConfigChanged } = options

  const defaultFormData = createDefaultAgentFormData()
  const formData = ref<AgentEditorFormData>(clonePlain(defaultFormData))

  const allModels = ref<ModelConfig[]>([])
  const kbOptions = ref<AgentKbOption[]>([])
  const agentTypePresets = ref<AgentTypePreset[]>([])
  const agentSystemPromptTemplates = ref<PromptTemplate[]>([])
  const promptTemplates = ref<PromptTemplatesConfig | null>(null)
  const intentPromptTemplates = ref<PromptTemplate[]>([])
  const agentToolCatalog = ref<AgentToolCatalog | null>(null)
  const storageEngineStatus = ref<StorageEngineStatusItem[]>([])
  const webSearchProviderList = ref<WebSearchProviderEntity[]>([])

  const storedToolNames = ref<string[]>([])
  const keepAllowedToolsUnset = ref(false)
  const isInitializing = ref(false)
  const kbSelectionMode = ref<'all' | 'selected' | 'none'>('none')
  const mcpSelectionMode = ref<'all' | 'selected' | 'none'>('none')

  const defaultAgentSystemPrompt = ref('')
  const defaultNormalSystemPrompt = ref('')
  const defaultContextTemplate = ref('')
  const defaultRewritePromptSystem = ref('')
  const defaultRewritePromptUser = ref('')
  const defaultFallbackPrompt = ref('')
  const defaultFallbackResponse = ref('')
  const defaultEmbeddingTopK = ref(10)
  const defaultKeywordThreshold = ref(0.3)
  const defaultVectorThreshold = ref(0.5)
  const defaultRerankTopK = ref(5)
  const defaultRerankThreshold = ref(0.5)
  const defaultTemperature = ref(0.7)

  const agentMode = computed({
    get: () => formData.value.config.agent_mode,
    set: (val: 'quick-answer' | 'smart-reasoning') => {
      formData.value.config.agent_mode = val
    },
  })
  const isAgentMode = computed(() => agentMode.value === 'smart-reasoning')
  const isBuiltinAgent = computed(() => formData.value.is_builtin === true)
  const hasKnowledgeBase = computed(() => kbSelectionMode.value !== 'none')

  const kbsInScope = computed(() => {
    if (kbSelectionMode.value === 'none') return []
    if (kbSelectionMode.value === 'all') return kbOptions.value
    const selectedIds = formData.value.config.knowledge_bases || []
    return kbOptions.value.filter((kb) => selectedIds.includes(kb.value))
  })

  const hasRagKnowledgeBase = computed(() => kbsInScope.value.some((kb) => kb.ragEnabled))
  const hasWikiKnowledgeBase = computed(() => kbsInScope.value.some((kb) => kb.wikiEnabled))
  const ragKbCount = computed(() => kbsInScope.value.filter((kb) => kb.ragEnabled).length)
  const wikiKbCount = computed(() => kbsInScope.value.filter((kb) => kb.wikiEnabled).length)

  const hasFaqKnowledgeBase = computed(() => {
    if (kbSelectionMode.value === 'none') return false
    if (kbSelectionMode.value === 'all') {
      return kbOptions.value.some((kb) => kb.type === 'faq')
    }
    const selectedKbIds = formData.value.config.knowledge_bases || []
    return kbOptions.value.some((kb) => selectedKbIds.includes(kb.value) && kb.type === 'faq')
  })

  const scopeCapabilities = computed<ScopeCapabilities>(() => {
    const scope: ScopeCapabilities = { vector: false, keyword: false, wiki: false, graph: false, faq: false }
    for (const kb of kbsInScope.value) {
      const caps = kb.capabilities
      if (caps) {
        if (caps.vector) scope.vector = true
        if (caps.keyword) scope.keyword = true
        if (caps.wiki) scope.wiki = true
        if (caps.graph) scope.graph = true
        if (caps.faq) scope.faq = true
      } else {
        if (kb.ragEnabled) {
          scope.vector = true
          scope.keyword = true
        }
        if (kb.wikiEnabled) scope.wiki = true
        if (kb.type === 'faq') scope.faq = true
      }
    }
    return scope
  })

  const allowedToolsToSave = (): string[] | null =>
    allowedToolsForSave(formData.value.config.allowed_tools || [], keepAllowedToolsUnset.value)

  const showRerankModelField = computed(() => {
    if (!isAgentMode.value) return hasKnowledgeBase.value
    return hasKnowledgeBase.value || agentRequiresRerankModel(formData.value.config)
  })

  const applyDefaultModelsIfEmpty = () => {
    if (props.mode !== 'create' || !formData.value) return
    const chatModelId = selectInitialModelId(allModels.value, 'KnowledgeQA')
    const rerankModelId = selectInitialModelId(allModels.value, 'Rerank')
    const vlmModelId = selectInitialModelId(allModels.value, 'VLLM')
    const asrModelId = selectInitialModelId(allModels.value, 'ASR')
    if (!formData.value.config.model_id && chatModelId) {
      formData.value.config.model_id = chatModelId
    }
    if (!formData.value.config.rerank_model_id && rerankModelId) {
      formData.value.config.rerank_model_id = rerankModelId
    }
    if (!formData.value.config.vlm_model_id && vlmModelId) {
      formData.value.config.vlm_model_id = vlmModelId
    }
    if (!formData.value.config.asr_model_id && asrModelId) {
      formData.value.config.asr_model_id = asrModelId
    }
  }

  const effectiveDefaultMaxCompletionTokens = computed(() =>
    defaultMaxCompletionTokensFor(agentMode.value, formData.value.config.sandbox_config_id),
  )

  const maxCompletionTokensMode = computed({
    get: () => (formData.value.config.max_completion_tokens > 0 ? 'custom' : 'default'),
    set: (mode: 'default' | 'custom') => {
      if (mode === 'default') {
        formData.value.config.max_completion_tokens = 0
        return
      }
      if (!formData.value.config.max_completion_tokens) {
        formData.value.config.max_completion_tokens = effectiveDefaultMaxCompletionTokens.value
      }
    },
  })

  const lastFiniteMaxIterations = ref(10)
  const maxIterationsMode = computed({
    get: () => (formData.value.config.max_iterations < 0 ? 'unlimited' : 'limit'),
    set: (mode: 'limit' | 'unlimited') => {
      if (mode === 'unlimited') {
        if (formData.value.config.max_iterations > 1) {
          lastFiniteMaxIterations.value = formData.value.config.max_iterations
        }
        formData.value.config.max_iterations = -1
        return
      }
      const restored = lastFiniteMaxIterations.value > 1 ? lastFiniteMaxIterations.value : 10
      formData.value.config.max_iterations = restored
    },
  })

  const selectedChatModel = computed(() =>
    allModels.value.find((model) => model.id === formData.value.config.model_id),
  )
  const selectedChatModelCanThink = computed(() => modelCanThink(selectedChatModel.value?.capabilities))
  const selectedChatModelAlwaysThinks = computed(() =>
    modelCannotDisableThinking(selectedChatModel.value?.capabilities),
  )
  const reasoningEffortOptions = computed<ReasoningLevel[]>(() =>
    optionsFor(selectedChatModel.value?.capabilities),
  )
  const reasoningEffortLevel = computed<ReasoningLevel>({
    get: () => levelFromLegacy(formData.value.config.thinking, formData.value.config.reasoning_effort),
    set: (level: ReasoningLevel) => {
      formData.value.config.reasoning_effort = level
      formData.value.config.thinking = levelEnablesThinking(level)
    },
  })

  const clampReasoningEffortToModel = () => {
    if (!selectedChatModel.value) return
    const clamped = clampLevel(reasoningEffortLevel.value, reasoningEffortOptions.value)
    if (clamped !== reasoningEffortLevel.value) reasoningEffortLevel.value = clamped
  }

  watch(
    () => [
      editorInitializing.value,
      formData.value.config.model_id,
      reasoningEffortOptions.value.join(','),
    ].join('|'),
    () => {
      if (!editorInitializing.value) clampReasoningEffortToModel()
    },
    { immediate: true },
  )

  const addStarterSuggestion = () => {
    const items = formData.value.config.question_suggestions.starters.items
    if (items.length < 8) items.push('')
  }

  const removeStarterSuggestion = (index: number) => {
    formData.value.config.question_suggestions.starters.items.splice(index, 1)
  }

  const initKbSelectionMode = () => {
    if (formData.value.config.kb_selection_mode) {
      kbSelectionMode.value = formData.value.config.kb_selection_mode
    } else if (formData.value.config.knowledge_bases?.length > 0) {
      kbSelectionMode.value = 'selected'
    } else {
      kbSelectionMode.value = 'none'
    }
  }

const initMcpSelectionMode = () => {
  if (formData.value.config.mcp_selection_mode) {
    mcpSelectionMode.value = formData.value.config.mcp_selection_mode;
  } else if (formData.value.config.mcp_services?.length > 0) {
    mcpSelectionMode.value = 'selected';
  } else {
    mcpSelectionMode.value = 'all';
  }
};

  const fillBuiltinAgentDefaults = () => {
    const config = formData.value.config
    const isAgent = config.agent_mode === 'smart-reasoning'
    if (isAgent) {
      if (!config.system_prompt && defaultAgentSystemPrompt.value) {
        config.system_prompt = defaultAgentSystemPrompt.value
      }
    } else {
      if (!config.system_prompt && defaultNormalSystemPrompt.value) {
        config.system_prompt = defaultNormalSystemPrompt.value
      }
      if (!config.context_template && defaultContextTemplate.value) {
        config.context_template = defaultContextTemplate.value
      }
    }
    if (!config.rewrite_prompt_system && defaultRewritePromptSystem.value) {
      config.rewrite_prompt_system = defaultRewritePromptSystem.value
    }
    if (!config.rewrite_prompt_user && defaultRewritePromptUser.value) {
      config.rewrite_prompt_user = defaultRewritePromptUser.value
    }
    if (!config.fallback_prompt && defaultFallbackPrompt.value) {
      config.fallback_prompt = defaultFallbackPrompt.value
    }
    if (!config.fallback_response && defaultFallbackResponse.value) {
      config.fallback_response = defaultFallbackResponse.value
    }
  }

  const applyPromptTemplateDefaults = (cfg: PromptTemplatesConfig | null) => {
    promptTemplates.value = cfg
    if (!cfg) return
    if (cfg.agent_system_prompt && Array.isArray(cfg.agent_system_prompt)) {
      agentSystemPromptTemplates.value = cfg.agent_system_prompt
      const ragDefault =
        cfg.agent_system_prompt.find((tmpl) => tmpl.mode === 'rag' && tmpl.default) ||
        cfg.agent_system_prompt.find((tmpl) => tmpl.mode === 'rag')
      if (ragDefault?.content) {
        defaultAgentSystemPrompt.value = ragDefault.content
      }
    }
    const pickDefault = (arr?: PromptTemplate[]): PromptTemplate | undefined =>
      Array.isArray(arr) ? arr.find((tmpl) => tmpl.default) : undefined
    const sysPrompt = pickDefault(cfg.system_prompt)
    if (sysPrompt?.content) defaultNormalSystemPrompt.value = sysPrompt.content
    const ctxTmpl = pickDefault(cfg.context_template)
    if (ctxTmpl?.content) defaultContextTemplate.value = ctxTmpl.content
    const rewriteTmpl = pickDefault(cfg.rewrite)
    if (rewriteTmpl?.content) defaultRewritePromptSystem.value = rewriteTmpl.content
    if (rewriteTmpl?.user) defaultRewritePromptUser.value = rewriteTmpl.user
    const fallbackList = Array.isArray(cfg.fallback) ? cfg.fallback : []
    const fixedFallback = fallbackList.find((tmpl) => tmpl.default && tmpl.mode !== 'model')
    if (fixedFallback?.content) defaultFallbackResponse.value = fixedFallback.content
    const modelFallback =
      fallbackList.find((tmpl) => tmpl.mode === 'model' && tmpl.default) ||
      fallbackList.find((tmpl) => tmpl.mode === 'model')
    if (modelFallback?.content) defaultFallbackPrompt.value = modelFallback.content
    if (Array.isArray(cfg.intent_prompts)) {
      intentPromptTemplates.value = cfg.intent_prompts
    }
  }

  const hydrateEditAgentData = (sourceAgent: CustomAgent): AgentEditorFormData => {
    const rawAgent = clonePlain(sourceAgent)
    const rawConfig = rawAgent.config ? clonePlain(rawAgent.config) : clonePlain(defaultFormData.config)
    const mergedConfig: AgentEditorFormConfig = {
      ...clonePlain(defaultFormData.config),
      ...rawConfig,
      chat_parser_engine_rules: (rawConfig.chat_parser_engine_rules as ParserEngineRule[] | undefined) || [],
    }
    if (mergedConfig.thinking == null) {
      mergedConfig.thinking = false
    }
    mergedConfig.reasoning_effort = levelFromLegacy(mergedConfig.thinking, mergedConfig.reasoning_effort)
    mergedConfig.thinking = levelEnablesThinking(mergedConfig.reasoning_effort as ReasoningLevel)

    mergedConfig.question_suggestions = {
      starters: {
        ...defaultFormData.config.question_suggestions.starters,
        ...(rawConfig.question_suggestions?.starters || {}),
        items: rawConfig.question_suggestions?.starters?.items || [],
      },
      follow_ups: {
        ...defaultFormData.config.question_suggestions.follow_ups,
        ...(rawConfig.question_suggestions?.follow_ups || {}),
        categories:
          rawConfig.question_suggestions?.follow_ups?.categories ||
          [...defaultFormData.config.question_suggestions.follow_ups.categories],
      },
    }
    if (!mergedConfig.knowledge_bases) mergedConfig.knowledge_bases = []
    const storedAllowedTools = rawConfig.allowed_tools
    mergedConfig.allowed_tools = normalizeLegacyToolNames(storedAllowedTools)
    if (!mergedConfig.mcp_services) mergedConfig.mcp_services = []
    if (mergedConfig.mcp_auth_wait_timeout == null || mergedConfig.mcp_auth_wait_timeout <= 0) {
      mergedConfig.mcp_auth_wait_timeout = 600
    }
    if (!mergedConfig.access_level) mergedConfig.access_level = 'auto_write'
    if (!mergedConfig.selected_skills) mergedConfig.selected_skills = []
    if (!mergedConfig.supported_file_types) mergedConfig.supported_file_types = []
    if (!mergedConfig.chat_parser_engine_rules) mergedConfig.chat_parser_engine_rules = []
    if (mergedConfig.attachment_ocr_max_pages == null) mergedConfig.attachment_ocr_max_pages = 0
    if (mergedConfig.attachment_parse_wait_timeout_sec == null) mergedConfig.attachment_parse_wait_timeout_sec = 0
    if (mergedConfig.max_completion_tokens == null) mergedConfig.max_completion_tokens = 0
    if (mergedConfig.memory_enabled == null) mergedConfig.memory_enabled = true

    if (!rawConfig.agent_mode) {
      const inferredAgent =
        mergedConfig.max_iterations < 0 ||
        mergedConfig.max_iterations > 1 ||
        (mergedConfig.allowed_tools && mergedConfig.allowed_tools.length > 0)
      mergedConfig.agent_mode = inferredAgent ? 'smart-reasoning' : 'quick-answer'
    }

    const loadedTools = resolveLoadedAllowedTools(
      storedAllowedTools == null ? null : mergedConfig.allowed_tools,
      mergedConfig.agent_mode,
      agentToolCatalog.value,
    )
    mergedConfig.allowed_tools = loadedTools.tools
    keepAllowedToolsUnset.value = loadedTools.keepUnset
    storedToolNames.value = [...loadedTools.tools]

    const hydratedConfig = hydrateAgentPromptRefs(mergedConfig, promptTemplates.value) as AgentEditorFormConfig
    if (hydratedConfig.max_iterations > 1) {
      lastFiniteMaxIterations.value = hydratedConfig.max_iterations
    }
    return {
      id: rawAgent.id,
      name: rawAgent.name || '',
      description: rawAgent.description || '',
      avatar: rawAgent.avatar,
      is_builtin: Boolean(rawAgent.is_builtin),
      config: hydratedConfig,
    }
  }

  const buildFreshCreateFormData = (): AgentEditorFormData => {
    storedToolNames.value = []
    keepAllowedToolsUnset.value = false
    const fresh = clonePlain(defaultFormData)
    fresh.config.embedding_top_k = defaultEmbeddingTopK.value
    fresh.config.keyword_threshold = defaultKeywordThreshold.value
    fresh.config.vector_threshold = defaultVectorThreshold.value
    fresh.config.rerank_top_k = defaultRerankTopK.value
    fresh.config.rerank_threshold = defaultRerankThreshold.value
    fresh.config.max_completion_tokens = 0
    fresh.config.max_iterations = -1
    fresh.config.temperature = defaultTemperature.value
    if (fresh.config.agent_mode === 'smart-reasoning') {
      if (defaultAgentSystemPrompt.value) {
        fresh.config.system_prompt = defaultAgentSystemPrompt.value
      }
    } else {
      if (defaultNormalSystemPrompt.value) fresh.config.system_prompt = defaultNormalSystemPrompt.value
      if (defaultContextTemplate.value) fresh.config.context_template = defaultContextTemplate.value
      if (defaultRewritePromptSystem.value) fresh.config.rewrite_prompt_system = defaultRewritePromptSystem.value
      if (defaultRewritePromptUser.value) fresh.config.rewrite_prompt_user = defaultRewritePromptUser.value
      if (defaultFallbackPrompt.value) fresh.config.fallback_prompt = defaultFallbackPrompt.value
      if (defaultFallbackResponse.value) fresh.config.fallback_response = defaultFallbackResponse.value
    }
    return fresh
  }

  const validateAgentFormBeforeSave = (): boolean => {
    if (!isBuiltinAgent.value) {
      if (!formData.value.name || !formData.value.name.trim()) {
        onValidationError(t('agent.editor.nameRequired'), 'basic')
        return false
      }
      if (!formData.value.config.system_prompt || !formData.value.config.system_prompt.trim()) {
        onValidationError(t('agent.editor.systemPromptRequired'), 'prompts')
        return false
      }
      if (
        !isAgentMode.value &&
        (!formData.value.config.context_template || !formData.value.config.context_template.trim())
      ) {
        onValidationError(t('agent.editor.contextTemplateRequired'), 'prompts')
        return false
      }
    }

    if (!isAgentMode.value && formData.value.config.multi_turn_enabled && formData.value.config.enable_rewrite) {
      const rewritePrompt = formData.value.config.rewrite_prompt_user || ''
      if (rewritePrompt.trim() && !hasPlaceholder(rewritePrompt, 'query')) {
        onValidationError(t('agent.editor.queryMissingInRewrite'), 'prompts')
        return false
      }
    }

    if (!isAgentMode.value && formData.value.config.fallback_strategy === 'model') {
      const fallbackPrompt = formData.value.config.fallback_prompt || ''
      if (fallbackPrompt.trim() && !hasPlaceholder(fallbackPrompt, 'query')) {
        onValidationError(t('agent.editor.queryMissingInFallback'), 'prompts')
        return false
      }
    }

    if (!formData.value.config.model_id) {
      onValidationError(t('agent.editor.modelRequired'), 'model')
      return false
    }

    if (formData.value.config.image_upload_enabled && !formData.value.config.vlm_model_id) {
      onValidationError(t('agentEditor.imageUpload.vlmModelRequired'), 'multimodal')
      return false
    }

    return true
  }

  watch(kbSelectionMode, (mode) => {
    formData.value.config.kb_selection_mode = mode
    if (mode === 'none' || mode === 'all') {
      formData.value.config.knowledge_bases = []
    }
  })

watch(mcpSelectionMode, (mode) => {
  if (isInitializing.value) return;
  formData.value.config.mcp_selection_mode = mode;
  if (mode === 'none') {
    formData.value.config.mcp_services = [];
  } else if (mode === 'all') {
    formData.value.config.mcp_services = [];
  }
});

watch(() => formData.value.config.mcp_services, () => {
  if (isInitializing.value || mcpSelectionMode.value !== 'selected') return;
  formData.value.config.mcp_selection_mode = 'selected';
}, { deep: true });

watch(() => formData.value.config.sandbox_config_id, async () => {
  if (!props.visible) return;
  await onSandboxConfigChanged();
});

  watch(agentMode, (val) => {
    if (val === 'smart-reasoning') {
      if (!isInitializing.value) keepAllowedToolsUnset.value = false
      if (!isInitializing.value && (formData.value.config.allowed_tools ?? []).length === 0) {
        const tools: string[] = ['ask_user']
        if (hasRagKnowledgeBase.value) tools.push(...KNOWLEDGE_BASE_TOOLS)
        if (hasWikiKnowledgeBase.value) tools.push(...WIKI_READ_TOOLS)
        formData.value.config.allowed_tools = tools
      }
      if (formData.value.config.max_iterations >= 0 && formData.value.config.max_iterations <= 1) {
        formData.value.config.max_iterations = -1
      }
      if (defaultAgentSystemPrompt.value) {
        const isDefaultNormalPrompt = formData.value.config.system_prompt === defaultNormalSystemPrompt.value
        if (!formData.value.config.system_prompt || isDefaultNormalPrompt) {
          formData.value.config.system_prompt = defaultAgentSystemPrompt.value
        }
      }
    } else {
      formData.value.config.allowed_tools = []
      formData.value.config.max_iterations = 1
      if (defaultNormalSystemPrompt.value) {
        const isDefaultAgentPrompt = formData.value.config.system_prompt === defaultAgentSystemPrompt.value
        if (!formData.value.config.system_prompt || isDefaultAgentPrompt) {
          formData.value.config.system_prompt = defaultNormalSystemPrompt.value
        }
      }
      if (!formData.value.config.context_template && defaultContextTemplate.value) {
        formData.value.config.context_template = defaultContextTemplate.value
      }
      if (!formData.value.config.rewrite_prompt_system && defaultRewritePromptSystem.value) {
        formData.value.config.rewrite_prompt_system = defaultRewritePromptSystem.value
      }
      if (!formData.value.config.rewrite_prompt_user && defaultRewritePromptUser.value) {
        formData.value.config.rewrite_prompt_user = defaultRewritePromptUser.value
      }
      if (!formData.value.config.fallback_prompt && defaultFallbackPrompt.value) {
        formData.value.config.fallback_prompt = defaultFallbackPrompt.value
      }
      if (!formData.value.config.fallback_response && defaultFallbackResponse.value) {
        formData.value.config.fallback_response = defaultFallbackResponse.value
      }
    }
  })

  watch(hasKnowledgeBase, (hasKB, oldHasKB) => {
    if (!hasKB && currentSection.value === 'retrieval') {
      currentSection.value = 'basic'
    }
    if (isInitializing.value || !isAgentMode.value) return
    if (hasKB && !oldHasKB && !keepAllowedToolsUnset.value) {
      const currentTools = formData.value.config.allowed_tools || []
      const toolsToAdd = KNOWLEDGE_BASE_TOOLS.filter((tool) => !currentTools.includes(tool))
      formData.value.config.allowed_tools = [...currentTools, ...toolsToAdd]
    }
  })

watch(isAgentMode, (isAgent) => {
  if (isAgent && (currentSection.value === 'advanced' || currentSection.value === 'retrieval')) {
    currentSection.value = 'basic';
  }
  if (!isAgent && (currentSection.value === 'skills' || currentSection.value === 'sandbox')) {
    currentSection.value = 'basic';
  }
});

  const applyTenantRetrievalDefaults = (rc: Record<string, number> | null) => {
    if (rc?.embedding_top_k) defaultEmbeddingTopK.value = rc.embedding_top_k
    if (rc?.keyword_threshold !== undefined) defaultKeywordThreshold.value = rc.keyword_threshold
    if (rc?.vector_threshold !== undefined) defaultVectorThreshold.value = rc.vector_threshold
    if (rc?.rerank_top_k) defaultRerankTopK.value = rc.rerank_top_k
    if (rc?.rerank_threshold !== undefined) defaultRerankThreshold.value = rc.rerank_threshold
  }

  return {
    formData,
    allModels,
    kbOptions,
    agentTypePresets,
    agentSystemPromptTemplates,
    promptTemplates,
    intentPromptTemplates,
    agentToolCatalog,
    storageEngineStatus,
    webSearchProviderList,
    storedToolNames,
    keepAllowedToolsUnset,
    isInitializing,
    kbSelectionMode,
    mcpSelectionMode,
    defaultRewritePromptSystem,
    defaultRewritePromptUser,
    defaultFallbackPrompt,
    defaultFallbackResponse,
    agentMode,
    isAgentMode,
    isBuiltinAgent,
    hasKnowledgeBase,
    ragKbCount,
    wikiKbCount,
    hasFaqKnowledgeBase,
    scopeCapabilities,
    allowedToolsToSave,
    showRerankModelField,
    applyDefaultModelsIfEmpty,
    effectiveDefaultMaxCompletionTokens,
    maxCompletionTokensMode,
    maxIterationsMode,
    selectedChatModel,
    selectedChatModelCanThink,
    selectedChatModelAlwaysThinks,
    reasoningEffortOptions,
    reasoningEffortLevel,
    clampReasoningEffortToModel,
    addStarterSuggestion,
    removeStarterSuggestion,
    initKbSelectionMode,
    initMcpSelectionMode,
    fillBuiltinAgentDefaults,
    applyPromptTemplateDefaults,
    applyTenantRetrievalDefaults,
    hydrateEditAgentData,
    buildFreshCreateFormData,
    validateAgentFormBeforeSave,
  }
}
