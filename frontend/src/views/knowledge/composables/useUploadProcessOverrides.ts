import { ref, type Ref } from 'vue'
import type { ImageActionsConfig } from '@/api/knowledge-base'
import type { KnowledgeProcessOverrides, ParserEngineRule } from '@/types/knowledgeProcess'

export type ConfigSectionKey =
  | 'tags'
  | 'parser'
  | 'chunking'
  | 'multimodal'
  | 'asr'
  | 'summary'
  | 'question'
  | 'graph'

export type IssueSectionKey = 'multimodal' | 'asr'

export interface ChunkingUIConfig {
  chunkSize: number
  chunkOverlap: number
  separators: string[]
  parserEngineRules?: ParserEngineRule[]
  enableParentChild: boolean
  parentChunkSize: number
  childChunkSize: number
  strategy?: string
  tokenLimit?: number
  languages?: string[]
  tableMetadataInstructions?: string
}

export interface UploadNodeExtractConfig {
  enabled: boolean
  text: string
  tags: string[]
  nodes: Array<{ name: string; attributes: string[] }>
  relations: Array<{ node1: string; node2: string; type: string }>
  customInstructions?: string
}

export interface UploadUIState {
  summaryEnabled: boolean
  chunkingConfig: ChunkingUIConfig
  multimodalConfig: {
    enabled: boolean
    vllmModelId: string
    descriptionLanguage?: string
    customInstructions?: string
  }
  imagePipeline: string
  imagePipelineParams: Record<string, unknown>
  imageVectorEnabled: boolean
  imageActions: ImageActionsConfig
  asrConfig: { enabled: boolean; modelId: string; language: string }
  questionGenerationConfig: { enabled: boolean; questionCount: number; customInstructions?: string }
  nodeExtractConfig: UploadNodeExtractConfig
  graphEnabled: boolean
  pdfForceScanned: boolean
}

export interface UploadKBInfo {
  id?: string
  embedding_model_id?: string
  summary_model_id?: string
  indexing_strategy?: {
    vector_enabled?: boolean
    keyword_enabled?: boolean
    graph_enabled?: boolean
  }
  chunking_config?: {
    chunk_size?: number
    chunk_overlap?: number
    separators?: string[]
    parser_engine_rules?: ParserEngineRule[]
    enable_parent_child?: boolean
    parent_chunk_size?: number
    child_chunk_size?: number
    strategy?: string
    token_limit?: number
    languages?: string[]
    table_metadata_instructions?: string
  }
  vlm_config?: {
    enabled?: boolean
    model_id?: string
    description_language?: string
    custom_instructions?: string
  }
  image_processing_config?: {
    image_attrs_enabled?: boolean
    image_pipeline?: string
    image_pipeline_params?: Record<string, unknown>
    image_vector_enabled?: boolean
    image_actions?: Partial<ImageActionsConfig> | null
  }
  asr_config?: {
    enabled?: boolean
    model_id?: string
    language?: string
  }
  question_generation_config?: {
    enabled?: boolean
    question_count?: number
    custom_instructions?: string
  }
  extract_config?: {
    enabled?: boolean
    text?: string
    tags?: string[]
    nodes?: Array<{ name: string; attributes?: string[] }>
    relations?: Array<{ node1: string; node2: string; type: string }>
    custom_instructions?: string
  }
}

export interface UploadNavItem {
  key: ConfigSectionKey
  icon: string
  label: string
  status: string
  statusFull: string
  statusTone?: 'warning' | 'error' | 'muted'
  issue?: boolean
}

type UploadProcessOverrides = KnowledgeProcessOverrides & {
  enable_multimodal?: boolean
}

export interface UseUploadProcessOverridesDeps {
  mergeImageActions: (raw?: Partial<ImageActionsConfig> | null) => ImageActionsConfig
  resolveImagePipelineFromKb: (kb: UploadKBInfo) => string
  buildPipelineFields: (
    pipelineId: string,
    params: Record<string, unknown>,
  ) => Partial<UploadProcessOverrides>
  normalizeImagePipelineId: (id?: string | null) => string
  imagePipelineDefault: string
  imagePipelineSmartOcr: string
  onLoadImageAttrSchema?: (kbId: string) => void
}

export function useUploadProcessOverrides(deps: UseUploadProcessOverridesDeps): {
  uiState: Ref<UploadUIState>
  createDefaultUIState: () => UploadUIState
  initFromKbInfo: (kb: UploadKBInfo | null | undefined) => void
  buildProcessOverrides: () => KnowledgeProcessOverrides
  applyOverridesToState: (o?: UploadProcessOverrides | null) => void
} {
  function createDefaultUIState(): UploadUIState {
    return {
      summaryEnabled: true,
      chunkingConfig: {
        chunkSize: 512,
        chunkOverlap: 80,
        separators: ['\n\n', '\n', '。', '！', '？', ';', '；'],
        parserEngineRules: undefined,
        enableParentChild: true,
        parentChunkSize: 4096,
        childChunkSize: 384,
        strategy: 'auto',
        tokenLimit: 0,
        languages: [],
        tableMetadataInstructions: '',
      },
      multimodalConfig: {
        enabled: false,
        vllmModelId: '',
        descriptionLanguage: '',
        customInstructions: '',
      },
      imagePipeline: deps.imagePipelineDefault,
      imagePipelineParams: {},
      imageVectorEnabled: false,
      imageActions: deps.mergeImageActions(),
      asrConfig: { enabled: false, modelId: '', language: '' },
      questionGenerationConfig: { enabled: true, questionCount: 3, customInstructions: '' },
      nodeExtractConfig: {
        enabled: false,
        text: '',
        tags: [],
        nodes: [],
        relations: [],
        customInstructions: '',
      },
      graphEnabled: false,
      pdfForceScanned: false,
    }
  }

  const uiState = ref<UploadUIState>(createDefaultUIState())

  function initFromKbInfo(kb: UploadKBInfo | null | undefined) {
    if (!kb) {
      uiState.value = createDefaultUIState()
      return
    }

    uiState.value = {
      summaryEnabled: true,
      chunkingConfig: {
        chunkSize: kb.chunking_config?.chunk_size || 512,
        chunkOverlap: kb.chunking_config?.chunk_overlap ?? 80,
        separators: kb.chunking_config?.separators || ['\n\n', '\n', '。', '！', '？', ';', '；'],
        parserEngineRules: kb.chunking_config?.parser_engine_rules || undefined,
        enableParentChild: kb.chunking_config?.enable_parent_child ?? false,
        parentChunkSize: kb.chunking_config?.parent_chunk_size || 4096,
        childChunkSize: kb.chunking_config?.child_chunk_size || 384,
        strategy: kb.chunking_config?.strategy || 'auto',
        tokenLimit: kb.chunking_config?.token_limit || 0,
        languages: kb.chunking_config?.languages || [],
        tableMetadataInstructions: kb.chunking_config?.table_metadata_instructions || '',
      },
      multimodalConfig: {
        enabled: !!kb.vlm_config?.enabled,
        vllmModelId: kb.vlm_config?.model_id || '',
        descriptionLanguage: kb.vlm_config?.description_language || '',
        customInstructions: kb.vlm_config?.custom_instructions || '',
      },
      imagePipeline: deps.resolveImagePipelineFromKb(kb),
      imagePipelineParams:
        (kb.image_processing_config?.image_pipeline_params as Record<string, unknown>) ?? {},
      imageVectorEnabled: !!kb.image_processing_config?.image_vector_enabled,
      imageActions: deps.mergeImageActions(kb.image_processing_config?.image_actions),
      asrConfig: {
        enabled: !!kb.asr_config?.enabled,
        modelId: kb.asr_config?.model_id || '',
        language: kb.asr_config?.language || '',
      },
      questionGenerationConfig: {
        enabled: kb.question_generation_config?.enabled ?? true,
        questionCount: kb.question_generation_config?.question_count || 3,
        customInstructions: kb.question_generation_config?.custom_instructions || '',
      },
      nodeExtractConfig: {
        enabled: !!kb.extract_config?.enabled && !!kb.indexing_strategy?.graph_enabled,
        text: kb.extract_config?.text || '',
        tags: kb.extract_config?.tags || [],
        nodes: (kb.extract_config?.nodes || []).map((node) => ({
          name: node.name,
          attributes: node.attributes || [],
        })),
        relations: kb.extract_config?.relations || [],
        customInstructions: kb.extract_config?.custom_instructions || '',
      },
      graphEnabled: kb.indexing_strategy?.graph_enabled ?? false,
      pdfForceScanned: false,
    }

    if (kb.id) {
      deps.onLoadImageAttrSchema?.(kb.id)
    }
  }

  function buildProcessOverrides(): KnowledgeProcessOverrides {
    const state = uiState.value
    const chunking = state.chunkingConfig

    const overrides: UploadProcessOverrides = {
      summary_enabled: state.summaryEnabled,
      parser_engine_rules: chunking.parserEngineRules,
      chunking_config: {
        chunk_size: chunking.chunkSize,
        chunk_overlap: chunking.chunkOverlap,
        separators: chunking.separators,
        enable_parent_child: chunking.enableParentChild,
        parent_chunk_size: chunking.parentChunkSize,
        child_chunk_size: chunking.childChunkSize,
        strategy: chunking.strategy,
        token_limit: chunking.tokenLimit,
        languages: chunking.languages,
        table_metadata_instructions: chunking.tableMetadataInstructions,
      },
      enable_multimodal: state.multimodalConfig.enabled,
      image_attrs_enabled: state.imagePipeline === deps.imagePipelineSmartOcr,
      image_vector_enabled: state.imageVectorEnabled,
      image_actions: {
        ocr: {
          on: state.imageActions.ocr.on,
        },
      },
      vlm_config: {
        enabled: state.multimodalConfig.enabled,
        model_id: state.multimodalConfig.vllmModelId,
        description_language: state.multimodalConfig.descriptionLanguage,
        custom_instructions: state.multimodalConfig.customInstructions,
      },
      asr_config: {
        enabled: state.asrConfig.enabled,
        model_id: state.asrConfig.modelId,
        language: state.asrConfig.language,
      },
      question_generation_config: {
        enabled: state.questionGenerationConfig.enabled,
        question_count: state.questionGenerationConfig.questionCount,
        custom_instructions: state.questionGenerationConfig.customInstructions,
      },
      graph_enabled: state.nodeExtractConfig.enabled && state.graphEnabled,
      extract_config: {
        enabled: state.nodeExtractConfig.enabled,
        text: state.nodeExtractConfig.text,
        tags: state.nodeExtractConfig.tags,
        nodes: state.nodeExtractConfig.nodes,
        relations: state.nodeExtractConfig.relations,
        custom_instructions: state.nodeExtractConfig.customInstructions,
      },
    }

    Object.assign(
      overrides,
      deps.buildPipelineFields(
        state.multimodalConfig.enabled ? state.imagePipeline : '',
        state.imagePipelineParams,
      ),
    )

    if (state.pdfForceScanned) {
      overrides.parser_engine_overrides = {
        pdf_force_scanned: 'true',
      }
    }

    return overrides
  }

  function applyOverridesToState(o?: UploadProcessOverrides | null) {
    if (!o) return
    const s = uiState.value
    if (o.summary_enabled != null) s.summaryEnabled = o.summary_enabled
    const cc = o.chunking_config
    if (cc) {
      if (cc.chunk_size != null) s.chunkingConfig.chunkSize = cc.chunk_size
      if (cc.chunk_overlap != null) s.chunkingConfig.chunkOverlap = cc.chunk_overlap
      if (cc.separators) s.chunkingConfig.separators = cc.separators
      if (cc.enable_parent_child != null) s.chunkingConfig.enableParentChild = cc.enable_parent_child
      if (cc.parent_chunk_size != null) s.chunkingConfig.parentChunkSize = cc.parent_chunk_size
      if (cc.child_chunk_size != null) s.chunkingConfig.childChunkSize = cc.child_chunk_size
      if (cc.strategy != null) s.chunkingConfig.strategy = cc.strategy
      if (cc.token_limit != null) s.chunkingConfig.tokenLimit = cc.token_limit
      if (cc.languages) s.chunkingConfig.languages = cc.languages
      if (cc.table_metadata_instructions != null) {
        s.chunkingConfig.tableMetadataInstructions = cc.table_metadata_instructions
      }
      if (cc.parser_engine_rules) s.chunkingConfig.parserEngineRules = cc.parser_engine_rules
    }
    if (o.parser_engine_rules) s.chunkingConfig.parserEngineRules = o.parser_engine_rules
    if (o.enable_multimodal != null) s.multimodalConfig.enabled = o.enable_multimodal
    if (o.image_pipeline != null) {
      s.imagePipeline = deps.normalizeImagePipelineId(o.image_pipeline) || deps.imagePipelineDefault
      s.imagePipelineParams = o.image_pipeline_params ?? {}
    } else if (o.image_attrs_enabled != null) {
      const pipeline = o.image_attrs_enabled ? deps.imagePipelineSmartOcr : deps.imagePipelineDefault
      if (pipeline !== s.imagePipeline) s.imagePipelineParams = {}
      s.imagePipeline = pipeline
    }
    if (o.image_vector_enabled != null) s.imageVectorEnabled = o.image_vector_enabled
    if (o.image_actions) s.imageActions = deps.mergeImageActions(o.image_actions)
    if (o.vlm_config) {
      if (o.vlm_config.enabled != null) s.multimodalConfig.enabled = o.vlm_config.enabled
      if (o.vlm_config.model_id != null) s.multimodalConfig.vllmModelId = o.vlm_config.model_id
      if (o.vlm_config.description_language != null) {
        s.multimodalConfig.descriptionLanguage = o.vlm_config.description_language
      }
      if (o.vlm_config.custom_instructions != null) {
        s.multimodalConfig.customInstructions = o.vlm_config.custom_instructions
      }
    }
    if (o.asr_config) {
      if (o.asr_config.enabled != null) s.asrConfig.enabled = o.asr_config.enabled
      if (o.asr_config.model_id != null) s.asrConfig.modelId = o.asr_config.model_id
      if (o.asr_config.language != null) s.asrConfig.language = o.asr_config.language
    }
    const qg = o.question_generation_config
    if (qg) {
      if (qg.enabled != null) s.questionGenerationConfig.enabled = qg.enabled
      if (qg.question_count != null) s.questionGenerationConfig.questionCount = qg.question_count
      if (qg.custom_instructions != null) {
        s.questionGenerationConfig.customInstructions = qg.custom_instructions
      }
    }
    const ec = o.extract_config
    if (ec) {
      if (ec.enabled != null) s.nodeExtractConfig.enabled = ec.enabled
      if (ec.text != null) s.nodeExtractConfig.text = ec.text
      if (ec.tags) s.nodeExtractConfig.tags = ec.tags
      if (ec.nodes) {
        s.nodeExtractConfig.nodes = ec.nodes.map((n) => ({
          name: n.name,
          attributes: n.attributes || [],
        }))
      }
      if (ec.relations) s.nodeExtractConfig.relations = ec.relations
      if (ec.custom_instructions != null) {
        s.nodeExtractConfig.customInstructions = ec.custom_instructions
      }
    }
    if (o.graph_enabled != null) s.graphEnabled = o.graph_enabled
    s.nodeExtractConfig.enabled = s.nodeExtractConfig.enabled && s.graphEnabled
    if (o.parser_engine_overrides && o.parser_engine_overrides.pdf_force_scanned === 'true') {
      s.pdfForceScanned = true
    } else {
      s.pdfForceScanned = false
    }
  }

  return {
    uiState,
    createDefaultUIState,
    initFromKbInfo,
    buildProcessOverrides,
    applyOverridesToState,
  }
}

export function truncateUploadNavText(text: string, max = 18): string {
  if (!text) return text
  if (text.length <= max) return text
  return `${text.slice(0, max - 1)}…`
}
