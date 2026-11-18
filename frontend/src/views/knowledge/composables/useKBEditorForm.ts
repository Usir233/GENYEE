import type { ImageActionsConfig, ImageProcessingConfig } from '@/api/knowledge-base'
import type { KBModelConfigRequest } from '@/api/initialization'
import type { ParserEngineRule } from '@/types/knowledgeProcess'

export type KBType = 'document' | 'faq'
export type WikiExtractionGranularity = 'focused' | 'standard' | 'exhaustive'

export interface KBEditorFaqConfig {
  indexMode: string
  questionIndexMode: string
}

export interface KBEditorModelConfig {
  llmModelId: string
  embeddingModelId: string
  wikiSynthesisModelId: string
  vllmModelId?: string
}

export interface KBEditorChunkingConfig {
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

export interface KBEditorMultimodalConfig {
  enabled: boolean
  vllmModelId: string
  descriptionLanguage: string
  customInstructions: string
}

export interface KBEditorAsrConfig {
  enabled: boolean
  modelId: string
  language: string
}

export interface KBEditorNodeExtractConfig {
  enabled: boolean
  text: string
  tags: string[]
  nodes: Array<{
    name: string
    attributes: string[]
  }>
  relations: Array<{
    node1: string
    node2: string
    type: string
  }>
  customInstructions: string
}

export interface KBEditorQuestionGenerationConfig {
  modelId: string
  enabled: boolean
  questionCount: number
  customInstructions: string
}

export interface KBEditorAutoTagConfig {
  enabled: boolean
  modelId: string
  maxTags: number
  skipIfTagged: boolean
}

export interface KBEditorProfileConfig {
  enabled: boolean
  modelId: string
  customInstructions: string
}

export interface KBEditorWikiConfig {
  autoGenerate: boolean
  synthesisModelId: string
  maxPagesPerIngest: number
  extractionGranularity: WikiExtractionGranularity
  contentInstructions: string
  extractionInstructions: string
}

export interface KBEditorIndexingStrategy {
  vectorEnabled: boolean
  keywordEnabled: boolean
  wikiEnabled: boolean
  graphEnabled: boolean
}

export interface KBEditorVectorStoreInfo {
  source?: string
  name?: string
  engineType?: string
  status?: string
}

export interface KBEditorFormData {
  type: KBType
  name: string
  description: string
  faqConfig: KBEditorFaqConfig
  modelConfig: KBEditorModelConfig
  chunkingConfig: KBEditorChunkingConfig
  storageBackendId: string
  storageProvider: string
  multimodalConfig: KBEditorMultimodalConfig
  imageAttrsEnabled: boolean
  imageVectorEnabled: boolean
  imageActions: ImageActionsConfig
  imagePipeline: string
  imagePipelineParams: Record<string, unknown>
  imageProcessingConfigSnapshot: Record<string, unknown> | null
  asrConfig: KBEditorAsrConfig
  nodeExtractConfig: KBEditorNodeExtractConfig
  questionGenerationConfig: KBEditorQuestionGenerationConfig
  autoTagConfig: KBEditorAutoTagConfig
  profileConfig: KBEditorProfileConfig
  wikiConfig: KBEditorWikiConfig
  indexingStrategy: KBEditorIndexingStrategy
  vectorStoreId: string
  vectorStoreInfo: KBEditorVectorStoreInfo
}

export interface LoadedKnowledgeBaseRecord {
  id?: string
  type?: KBType | string
  name?: string
  description?: string
  creator_id?: string
  tenant_id?: number
  generated_profile?: unknown
  summary_model_id?: string
  embedding_model_id?: string
  storage_backend_id?: string
  storage_provider_config?: { provider?: string }
  storage_config?: { provider?: string }
  faq_config?: {
    index_mode?: string
    question_index_mode?: string
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
  } & Record<string, unknown>
  asr_config?: {
    enabled?: boolean
    model_id?: string
    language?: string
  }
  extract_config?: {
    enabled?: boolean
    text?: string
    tags?: string[]
    nodes?: Array<{ name: string; attributes?: string[] }>
    relations?: Array<{ node1: string; node2: string; type: string }>
    custom_instructions?: string
  }
  question_generation_config?: {
    model_id?: string
    enabled?: boolean
    question_count?: number
    custom_instructions?: string
  }
  auto_tag_config?: {
    enabled?: boolean
    model_id?: string
    max_tags?: number
    skip_if_tagged?: boolean
  }
  profile_config?: {
    enabled?: boolean
    model_id?: string
    custom_instructions?: string
  }
  wiki_config?: {
    auto_generate?: boolean
    synthesis_model_id?: string
    max_pages_per_ingest?: number
    extraction_granularity?: string
    content_instructions?: string
    extraction_instructions?: string
  }
  indexing_strategy?: {
    vector_enabled?: boolean
    keyword_enabled?: boolean
    wiki_enabled?: boolean
    graph_enabled?: boolean
  }
  vector_store_source?: string
  vector_store_name?: string
  vector_store_engine_type?: string
  vector_store_status?: string
}

export interface KBSubmitPayload {
  name: string
  description: string
  type: KBType
  chunking_config: {
    chunk_size: number
    chunk_overlap: number
    separators: string[]
    enable_parent_child: boolean
    parent_chunk_size: number
    child_chunk_size: number
    strategy: string
    token_limit: number
    languages: string[]
    table_metadata_instructions: string
    parser_engine_rules?: ParserEngineRule[]
  }
  embedding_model_id: string
  summary_model_id: string
  vector_store_id?: string
  vlm_config?: {
    enabled: boolean
    model_id: string
    description_language: string
    custom_instructions: string
  }
  image_processing_config?: ImageProcessingConfig
  asr_config?: {
    enabled: boolean
    model_id: string
    language: string
  }
  storage_backend_id?: string
  storage_provider_config?: { provider: string }
  storage_config?: { provider: string }
  question_generation_config?: {
    model_id: string
    enabled: boolean
    question_count: number
    custom_instructions: string
  }
  auto_tag_config?: {
    enabled: boolean
    model_id: string
    max_tags: number
    skip_if_tagged: boolean
  }
  profile_config?: {
    enabled: boolean
    model_id: string
    custom_instructions: string
  }
  faq_config?: {
    index_mode: string
    question_index_mode: string
  }
  wiki_config?: {
    auto_generate: boolean
    synthesis_model_id: string
    max_pages_per_ingest: number
    extraction_granularity: WikiExtractionGranularity
    content_instructions: string
    extraction_instructions: string
  }
  indexing_strategy?: {
    vector_enabled: boolean
    keyword_enabled: boolean
    wiki_enabled: boolean
    graph_enabled: boolean
  }
  extract_config?: {
    enabled: boolean
    text: string
    tags: string[]
    nodes: Array<{ name: string; attributes: string[] }>
    relations: Array<{ node1: string; node2: string; type: string }>
    custom_instructions: string
  }
}

export const WIKI_ONLY_CHUNKING_PRESET = {
  chunkSize: 2048,
  chunkOverlap: 0,
  enableParentChild: false,
} as const

export const DEFAULT_CHUNKING_PRESET = {
  chunkSize: 512,
  chunkOverlap: 80,
  enableParentChild: true,
} as const

export function createInitialKBFormData(
  type: KBType,
  deps: {
    mergeImageActions: (raw?: Partial<ImageActionsConfig> | null) => ImageActionsConfig
    imagePipelineDefault: string
  },
): KBEditorFormData {
  const initFormData = (kbType: KBType = 'document'): KBEditorFormData => ({
    type: kbType,
    name: '',
    description: '',
    faqConfig: {
      indexMode: 'question_only',
      questionIndexMode: 'separate',
    },
    modelConfig: {
      llmModelId: '',
      embeddingModelId: '',
      wikiSynthesisModelId: '',
    },
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
    storageBackendId: '',
    storageProvider: '',
    multimodalConfig: {
      enabled: false,
      vllmModelId: '',
      descriptionLanguage: '',
      customInstructions: '',
    },
    imageAttrsEnabled: false,
    imageVectorEnabled: false,
    imageActions: deps.mergeImageActions(),
    imagePipeline: deps.imagePipelineDefault,
    imagePipelineParams: {},
    imageProcessingConfigSnapshot: null,
    asrConfig: {
      enabled: false,
      modelId: '',
      language: '',
    },
    nodeExtractConfig: {
      enabled: false,
      text: '',
      tags: [],
      nodes: [],
      relations: [],
      customInstructions: '',
    },
    questionGenerationConfig: {
      modelId: '',
      enabled: true,
      questionCount: 3,
      customInstructions: '',
    },
    autoTagConfig: {
      enabled: false,
      modelId: '',
      maxTags: 3,
      skipIfTagged: true,
    },
    profileConfig: {
      enabled: false,
      modelId: '',
      customInstructions: '',
    },
    wikiConfig: {
      autoGenerate: true,
      synthesisModelId: '',
      maxPagesPerIngest: 0,
      extractionGranularity: 'standard',
      contentInstructions: '',
      extractionInstructions: '',
    },
    indexingStrategy: {
      vectorEnabled: true,
      keywordEnabled: true,
      wikiEnabled: false,
      graphEnabled: false,
    },
    vectorStoreId: '',
    vectorStoreInfo: {
      source: undefined,
      name: undefined,
      engineType: undefined,
      status: undefined,
    },
  })

  return initFormData(type)
}

export function mapLoadedKBToFormData(
  kb: LoadedKnowledgeBaseRecord,
  deps: {
    mergeImageActions: (raw?: Partial<ImageActionsConfig> | null) => ImageActionsConfig
    resolveKbImagePipeline: (kb: LoadedKnowledgeBaseRecord) => string
    imagePipelineSmartOcr: string
  },
): KBEditorFormData {
  const kbType: KBType = kb.type === 'faq' ? 'faq' : 'document'
  const resolvedPipeline = deps.resolveKbImagePipeline(kb)
  const granularity: WikiExtractionGranularity =
    kb.wiki_config?.extraction_granularity === 'focused' ||
    kb.wiki_config?.extraction_granularity === 'exhaustive'
      ? kb.wiki_config.extraction_granularity
      : 'standard'

  return {
    type: kbType,
    name: kb.name || '',
    description: kb.description || '',
    faqConfig: {
      indexMode: kb.faq_config?.index_mode || 'question_only',
      questionIndexMode: kb.faq_config?.question_index_mode || 'separate',
    },
    modelConfig: {
      llmModelId: kb.summary_model_id || '',
      embeddingModelId: kb.embedding_model_id || '',
      wikiSynthesisModelId: kb.wiki_config?.synthesis_model_id || '',
    },
    chunkingConfig: {
      chunkSize: kb.chunking_config?.chunk_size || 512,
      chunkOverlap: kb.chunking_config?.chunk_overlap ?? 80,
      separators: kb.chunking_config?.separators || ['\n\n', '\n', '。', '！', '？', ';', '；'],
      parserEngineRules: kb.chunking_config?.parser_engine_rules || undefined,
      enableParentChild: kb.chunking_config?.enable_parent_child || false,
      parentChunkSize: kb.chunking_config?.parent_chunk_size || 4096,
      childChunkSize: kb.chunking_config?.child_chunk_size || 384,
      strategy: kb.chunking_config?.strategy || '',
      tokenLimit: kb.chunking_config?.token_limit || 0,
      languages: kb.chunking_config?.languages || [],
      tableMetadataInstructions: kb.chunking_config?.table_metadata_instructions || '',
    },
    storageBackendId: kb.storage_backend_id || '',
    storageProvider: kb.storage_provider_config?.provider || kb.storage_config?.provider || 'local',
    multimodalConfig: {
      enabled: !!kb.vlm_config?.enabled,
      vllmModelId: kb.vlm_config?.model_id || '',
      descriptionLanguage: kb.vlm_config?.description_language || '',
      customInstructions: kb.vlm_config?.custom_instructions || '',
    },
    imagePipeline: resolvedPipeline,
    imageAttrsEnabled: resolvedPipeline === deps.imagePipelineSmartOcr,
    imageVectorEnabled: !!kb.image_processing_config?.image_vector_enabled,
    imageActions: deps.mergeImageActions(kb.image_processing_config?.image_actions),
    imagePipelineParams: kb.image_processing_config?.image_pipeline_params ?? {},
    imageProcessingConfigSnapshot: kb.image_processing_config || null,
    asrConfig: {
      enabled: !!kb.asr_config?.enabled,
      modelId: kb.asr_config?.model_id || '',
      language: kb.asr_config?.language || '',
    },
    nodeExtractConfig: {
      enabled: kb.extract_config?.enabled || false,
      text: kb.extract_config?.text || '',
      tags: kb.extract_config?.tags || [],
      nodes: (kb.extract_config?.nodes || []).map((node) => ({
        name: node.name,
        attributes: node.attributes || [],
      })),
      relations: kb.extract_config?.relations || [],
      customInstructions: kb.extract_config?.custom_instructions || '',
    },
    questionGenerationConfig: {
      modelId: kb.question_generation_config?.model_id || '',
      enabled: kb.question_generation_config?.enabled || false,
      questionCount: kb.question_generation_config?.question_count || 3,
      customInstructions: kb.question_generation_config?.custom_instructions || '',
    },
    autoTagConfig: {
      enabled: kb.auto_tag_config?.enabled || false,
      modelId: kb.auto_tag_config?.model_id || '',
      maxTags: kb.auto_tag_config?.max_tags || 3,
      skipIfTagged: kb.auto_tag_config?.skip_if_tagged ?? true,
    },
    profileConfig: {
      enabled: kb.profile_config?.enabled || false,
      modelId: kb.profile_config?.model_id || '',
      customInstructions: kb.profile_config?.custom_instructions || '',
    },
    wikiConfig: {
      autoGenerate: kb.wiki_config?.auto_generate ?? true,
      synthesisModelId: kb.wiki_config?.synthesis_model_id || '',
      maxPagesPerIngest: kb.wiki_config?.max_pages_per_ingest || 0,
      extractionGranularity: granularity,
      contentInstructions: kb.wiki_config?.content_instructions || '',
      extractionInstructions: kb.wiki_config?.extraction_instructions || '',
    },
    indexingStrategy: {
      vectorEnabled: kb.indexing_strategy?.vector_enabled ?? true,
      keywordEnabled: kb.indexing_strategy?.keyword_enabled ?? true,
      wikiEnabled: kb.indexing_strategy?.wiki_enabled ?? false,
      graphEnabled: kb.indexing_strategy?.graph_enabled ?? false,
    },
    vectorStoreId: '',
    vectorStoreInfo: {
      source: kb.vector_store_source,
      name: kb.vector_store_name,
      engineType: kb.vector_store_engine_type,
      status: kb.vector_store_status,
    },
  }
}

export function buildKBModelConfigRequest(
  data: KBSubmitPayload,
  formData: KBEditorFormData,
): KBModelConfigRequest {
  return {
    llmModelId: data.summary_model_id,
    embeddingModelId: data.embedding_model_id,
    vlm_config: data.vlm_config,
    asr_config: data.asr_config,
    documentSplitting: {
      chunkSize: data.chunking_config.chunk_size,
      chunkOverlap: data.chunking_config.chunk_overlap,
      separators: data.chunking_config.separators,
      parserEngineRules: data.chunking_config.parser_engine_rules || undefined,
      enableParentChild: data.chunking_config.enable_parent_child || false,
      parentChunkSize: data.chunking_config.parent_chunk_size || 4096,
      childChunkSize: data.chunking_config.child_chunk_size || 384,
      strategy: formData.chunkingConfig.strategy ?? '',
      tokenLimit: formData.chunkingConfig.tokenLimit ?? 0,
      languages: formData.chunkingConfig.languages ?? [],
      tableMetadataInstructions: formData.chunkingConfig.tableMetadataInstructions ?? '',
    },
    multimodal: {
      enabled: !!data.vlm_config?.enabled,
    },
    storageBackendId: formData.storageBackendId || '',
    storageProvider:
      data.storage_provider_config?.provider || data.storage_config?.provider || 'local',
    nodeExtract: {
      enabled: data.extract_config?.enabled || false,
      text: data.extract_config?.text || '',
      tags: data.extract_config?.tags || [],
      nodes: data.extract_config?.nodes || [],
      relations: data.extract_config?.relations || [],
      customInstructions: data.extract_config?.custom_instructions || '',
    },
    questionGeneration: {
      modelId: data.question_generation_config?.model_id || '',
      enabled: data.question_generation_config?.enabled || false,
      questionCount: data.question_generation_config?.question_count || 3,
      customInstructions: data.question_generation_config?.custom_instructions || '',
    },
  }
}
