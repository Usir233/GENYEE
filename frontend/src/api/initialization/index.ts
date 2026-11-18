import { get, post, put, postUpload } from '../../utils/request';
import i18n from '@/i18n';
import type { ModelCapabilities, ModelSpecOverride, ReasoningEffortLevel } from '../model';
export type { ModelCapabilities, ModelSpecOverride, ReasoningEffortLevel };

const t = (key: string) => i18n.global.t(key);

// GET /initialization/config/:kb_id exposes credential presence, not values.
export interface ModelCredentialStatus {
    apiKey?: boolean;
}

export interface COSCredentialStatus {
    secretId?: boolean;
    secretKey?: boolean;
}

export interface Node {
    name: string;
    attributes: string[];
}

export interface Relation {
    node1: string;
    node2: string;
    type: string;
}

// Initialization configuration view model used by frontend forms.
// Serialized to / deserialized from pure snake_case wire JSON at the HTTP boundary.
export interface InitializationConfig {
    llm: {
        source: string;
        modelName: string;
        baseUrl?: string;
        /** @deprecated Use credentials.apiKey from GET responses */
        apiKey?: string;
        credentials?: ModelCredentialStatus;
    };
    embedding: {
        source: string;
        modelName: string;
        baseUrl?: string;
        /** @deprecated Use credentials.apiKey from GET responses */
        apiKey?: string;
        dimension?: number;
        credentials?: ModelCredentialStatus;
    };
    rerank: {
        modelName: string;
        baseUrl: string;
        /** @deprecated Use credentials.apiKey from GET responses */
        apiKey?: string;
        enabled: boolean;
        credentials?: ModelCredentialStatus;
    };
    multimodal: {
        enabled: boolean;
        storageType: 'cos' | 'minio' | string;
        descriptionLanguage?: string;
        customInstructions?: string;
        vlm?: {
            modelName: string;
            baseUrl: string;
            /** @deprecated Use credentials.apiKey from GET responses */
            apiKey?: string;
            interfaceType?: string;
            modelId?: string;
            credentials?: ModelCredentialStatus;
        };
        cos?: {
            region: string;
            bucketName: string;
            appId: string;
            pathPrefix?: string;
            /** @deprecated Use credentials from GET responses */
            secretId?: string;
            /** @deprecated Use credentials from GET responses */
            secretKey?: string;
            credentials?: COSCredentialStatus;
        };
        minio?: {
            bucketName: string;
            pathPrefix?: string;
        };
    };
    documentSplitting: {
        chunkSize: number;
        chunkOverlap: number;
        separators: string[];
        strategy?: string;
        tokenLimit?: number;
        languages?: string[];
        tableMetadataInstructions?: string;
    };
    // Frontend-only hint for storage selection UI
    storageType?: 'cos' | 'minio';
    nodeExtract: {
        enabled: boolean;
        text: string;
        tags: string[];
        nodes: Node[];
        relations: Relation[];
        customInstructions?: string;
    };
    questionGeneration?: {
        enabled: boolean;
        questionCount?: number;
        modelId?: string;
        customInstructions?: string;
    };
}

export interface DownloadTask {
    id: string;
    modelName: string;
    status: 'pending' | 'downloading' | 'completed' | 'failed';
    progress: number;
    message: string;
    startTime: string;
    endTime?: string;
}

export interface KBParserEngineRule {
    file_types: string[];
    engine: string;
    xlsx_first_row_as_header?: boolean;
}

export interface KBModelConfigRequest {
    llmModelId: string;
    embeddingModelId: string;
    vlm_config?: {
        enabled: boolean;
        model_id?: string;
        description_language?: string;
        custom_instructions?: string;
    };
    asr_config?: {
        enabled: boolean;
        model_id?: string;
        language?: string;
    };
    documentSplitting: {
        chunkSize: number;
        chunkOverlap: number;
        separators: string[];
        parserEngineRules?: KBParserEngineRule[];
        enableParentChild?: boolean;
        parentChunkSize?: number;
        childChunkSize?: number;
        // Pointer-based DTOs on the backend:
        // - undefined / omitted in payload -> no change on server
        // - "" / 0 / [] explicitly sent    -> clears the value
        strategy?: string;
        tokenLimit?: number;
        languages?: string[];
        tableMetadataInstructions?: string;
    };
    multimodal: {
        enabled: boolean;
    };
    storageBackendId?: string;
    storageProvider?: string;
    nodeExtract: {
        enabled: boolean;
        text: string;
        tags: string[];
        nodes: Node[];
        relations: Relation[];
        customInstructions?: string;
    };
    questionGeneration?: {
        modelId?: string;
        enabled: boolean;
        questionCount: number;
        customInstructions?: string;
    };
}

// ---- Pure snake_case wire DTOs & mappers -----------------------------------

export interface KBModelConfigWireRequest {
    llm_model_id: string;
    embedding_model_id: string;
    vlm_config?: KBModelConfigRequest['vlm_config'];
    asr_config?: KBModelConfigRequest['asr_config'];
    document_splitting: {
        chunk_size: number;
        chunk_overlap: number;
        separators: string[];
        parser_engine_rules?: KBParserEngineRule[];
        enable_parent_child?: boolean;
        parent_chunk_size?: number;
        child_chunk_size?: number;
        strategy?: string;
        token_limit?: number;
        languages?: string[];
        table_metadata_instructions?: string;
    };
    multimodal: {
        enabled: boolean;
    };
    storage_backend_id?: string;
    storage_provider?: string;
    node_extract: {
        enabled: boolean;
        text: string;
        tags: string[];
        nodes: Node[];
        relations: Relation[];
        custom_instructions?: string;
    };
    question_generation?: {
        model_id?: string;
        enabled: boolean;
        question_count: number;
        custom_instructions?: string;
    };
}

export function serializeKBModelConfigRequest(config: KBModelConfigRequest): KBModelConfigWireRequest {
    const ds = config.documentSplitting;
    const documentSplitting: KBModelConfigWireRequest['document_splitting'] = {
        chunk_size: ds.chunkSize,
        chunk_overlap: ds.chunkOverlap,
        separators: ds.separators,
    };
    if (ds.parserEngineRules !== undefined) documentSplitting.parser_engine_rules = ds.parserEngineRules;
    if (ds.enableParentChild !== undefined) documentSplitting.enable_parent_child = ds.enableParentChild;
    if (ds.parentChunkSize !== undefined) documentSplitting.parent_chunk_size = ds.parentChunkSize;
    if (ds.childChunkSize !== undefined) documentSplitting.child_chunk_size = ds.childChunkSize;
    if (ds.strategy !== undefined) documentSplitting.strategy = ds.strategy;
    if (ds.tokenLimit !== undefined) documentSplitting.token_limit = ds.tokenLimit;
    if (ds.languages !== undefined) documentSplitting.languages = ds.languages;
    if (ds.tableMetadataInstructions !== undefined) {
        documentSplitting.table_metadata_instructions = ds.tableMetadataInstructions;
    }

    const ne = config.nodeExtract;
    const nodeExtract: KBModelConfigWireRequest['node_extract'] = {
        enabled: ne.enabled,
        text: ne.text,
        tags: ne.tags,
        nodes: ne.nodes,
        relations: ne.relations,
    };
    if (ne.customInstructions !== undefined) nodeExtract.custom_instructions = ne.customInstructions;

    const wire: KBModelConfigWireRequest = {
        llm_model_id: config.llmModelId,
        embedding_model_id: config.embeddingModelId,
        document_splitting: documentSplitting,
        multimodal: { enabled: config.multimodal.enabled },
        node_extract: nodeExtract,
    };
    if (config.vlm_config !== undefined) wire.vlm_config = config.vlm_config;
    if (config.asr_config !== undefined) wire.asr_config = config.asr_config;
    if (config.storageBackendId !== undefined) wire.storage_backend_id = config.storageBackendId;
    if (config.storageProvider !== undefined) wire.storage_provider = config.storageProvider;
    if (config.questionGeneration !== undefined) {
        const qg = config.questionGeneration;
        wire.question_generation = {
            enabled: qg.enabled,
            question_count: qg.questionCount,
            ...(qg.modelId !== undefined ? { model_id: qg.modelId } : {}),
            ...(qg.customInstructions !== undefined ? { custom_instructions: qg.customInstructions } : {}),
        };
    }
    return wire;
}

export interface InitializationWireRequest {
    llm: {
        source: string;
        model_name: string;
        base_url?: string;
        api_key?: string;
    };
    embedding: {
        source: string;
        model_name: string;
        base_url?: string;
        api_key?: string;
        dimension?: number;
    };
    rerank: {
        enabled: boolean;
        model_name: string;
        base_url: string;
        api_key?: string;
    };
    multimodal: {
        enabled: boolean;
        storage_type: string;
        vlm?: {
            model_name: string;
            base_url: string;
            api_key?: string;
            interface_type?: string;
        };
        cos?: {
            secret_id?: string;
            secret_key?: string;
            region: string;
            bucket_name: string;
            app_id: string;
            path_prefix?: string;
        };
        minio?: {
            bucket_name: string;
            path_prefix?: string;
        };
    };
    document_splitting: {
        chunk_size: number;
        chunk_overlap: number;
        separators: string[];
        strategy?: string;
        token_limit?: number;
        languages?: string[];
    };
    node_extract: {
        enabled: boolean;
        text: string;
        tags: string[];
        nodes: Node[];
        relations: Relation[];
    };
    question_generation?: {
        enabled: boolean;
        question_count?: number;
    };
}

export function serializeInitializationConfig(config: InitializationConfig): InitializationWireRequest {
    const mm = config.multimodal;
    const multimodal: InitializationWireRequest['multimodal'] = {
        enabled: mm.enabled,
        storage_type: mm.storageType,
    };
    if (mm.vlm) {
        multimodal.vlm = {
            model_name: mm.vlm.modelName,
            base_url: mm.vlm.baseUrl,
            ...(mm.vlm.apiKey !== undefined ? { api_key: mm.vlm.apiKey } : {}),
            ...(mm.vlm.interfaceType !== undefined ? { interface_type: mm.vlm.interfaceType } : {}),
        };
    }
    if (mm.cos) {
        multimodal.cos = {
            region: mm.cos.region,
            bucket_name: mm.cos.bucketName,
            app_id: mm.cos.appId,
            ...(mm.cos.pathPrefix !== undefined ? { path_prefix: mm.cos.pathPrefix } : {}),
            ...(mm.cos.secretId !== undefined ? { secret_id: mm.cos.secretId } : {}),
            ...(mm.cos.secretKey !== undefined ? { secret_key: mm.cos.secretKey } : {}),
        };
    }
    if (mm.minio) {
        multimodal.minio = {
            bucket_name: mm.minio.bucketName,
            ...(mm.minio.pathPrefix !== undefined ? { path_prefix: mm.minio.pathPrefix } : {}),
        };
    }

    const ds = config.documentSplitting;
    const documentSplitting: InitializationWireRequest['document_splitting'] = {
        chunk_size: ds.chunkSize,
        chunk_overlap: ds.chunkOverlap,
        separators: ds.separators,
        ...(ds.strategy !== undefined ? { strategy: ds.strategy } : {}),
        ...(ds.tokenLimit !== undefined ? { token_limit: ds.tokenLimit } : {}),
        ...(ds.languages !== undefined ? { languages: ds.languages } : {}),
    };

    const wire: InitializationWireRequest = {
        llm: {
            source: config.llm.source,
            model_name: config.llm.modelName,
            ...(config.llm.baseUrl !== undefined ? { base_url: config.llm.baseUrl } : {}),
            ...(config.llm.apiKey !== undefined ? { api_key: config.llm.apiKey } : {}),
        },
        embedding: {
            source: config.embedding.source,
            model_name: config.embedding.modelName,
            ...(config.embedding.baseUrl !== undefined ? { base_url: config.embedding.baseUrl } : {}),
            ...(config.embedding.apiKey !== undefined ? { api_key: config.embedding.apiKey } : {}),
            ...(config.embedding.dimension !== undefined ? { dimension: config.embedding.dimension } : {}),
        },
        rerank: {
            enabled: config.rerank.enabled,
            model_name: config.rerank.modelName,
            base_url: config.rerank.baseUrl,
            ...(config.rerank.apiKey !== undefined ? { api_key: config.rerank.apiKey } : {}),
        },
        multimodal,
        document_splitting: documentSplitting,
        node_extract: {
            enabled: config.nodeExtract.enabled,
            text: config.nodeExtract.text,
            tags: config.nodeExtract.tags,
            nodes: config.nodeExtract.nodes,
            relations: config.nodeExtract.relations,
        },
    };
    if (config.questionGeneration) {
        wire.question_generation = {
            enabled: config.questionGeneration.enabled,
            ...(config.questionGeneration.questionCount !== undefined
                ? { question_count: config.questionGeneration.questionCount }
                : {}),
        };
    }
    return wire;
}

interface WireDownloadTask {
    id?: string;
    model_name?: string;
    status?: DownloadTask['status'];
    progress?: number;
    message?: string;
    start_time?: string;
    end_time?: string;
}

function deserializeDownloadTask(raw: WireDownloadTask | null | undefined): DownloadTask {
    const task: DownloadTask = {
        id: raw?.id ?? '',
        modelName: raw?.model_name ?? '',
        status: raw?.status ?? 'pending',
        progress: typeof raw?.progress === 'number' ? raw.progress : 0,
        message: raw?.message ?? '',
        startTime: raw?.start_time ?? '',
    };
    if (raw?.end_time !== undefined) {
        task.endTime = raw.end_time;
    }
    return task;
}

interface WireInitializationConfigResponse {
    has_files?: boolean;
    llm?: {
        source?: string;
        model_name?: string;
        base_url?: string;
        credentials?: { api_key?: boolean };
    };
    embedding?: {
        source?: string;
        model_name?: string;
        base_url?: string;
        dimension?: number;
        credentials?: { api_key?: boolean };
    };
    rerank?: {
        enabled?: boolean;
        model_name?: string;
        base_url?: string;
        credentials?: { api_key?: boolean };
    };
    multimodal?: {
        enabled?: boolean;
        storage_type?: string;
        description_language?: string;
        custom_instructions?: string;
        vlm?: {
            model_name?: string;
            base_url?: string;
            interface_type?: string;
            model_id?: string;
            credentials?: { api_key?: boolean };
        };
        cos?: {
            region?: string;
            bucket_name?: string;
            app_id?: string;
            path_prefix?: string;
            credentials?: { secret_id?: boolean; secret_key?: boolean };
        };
        minio?: {
            bucket_name?: string;
            path_prefix?: string;
        };
    };
    document_splitting?: {
        chunk_size?: number;
        chunk_overlap?: number;
        separators?: string[];
        strategy?: string;
        token_limit?: number;
        languages?: string[];
        table_metadata_instructions?: string;
    };
    node_extract?: {
        enabled?: boolean;
        text?: string;
        tags?: string[];
        nodes?: Node[];
        relations?: Relation[];
        custom_instructions?: string;
    };
    question_generation?: {
        enabled?: boolean;
        question_count?: number;
        model_id?: string;
        custom_instructions?: string;
    };
}

export function deserializeInitializationConfigResponse(
    raw: WireInitializationConfigResponse | null | undefined,
): InitializationConfig & { hasFiles: boolean } {
    const r = raw ?? {};
    const mm = r.multimodal;
    const ds = r.document_splitting;
    const ne = r.node_extract;
    const qg = r.question_generation;

    const multimodal: InitializationConfig['multimodal'] = {
        enabled: Boolean(mm?.enabled),
        storageType: mm?.storage_type ?? 'minio',
    };
    if (mm?.description_language !== undefined) multimodal.descriptionLanguage = mm.description_language;
    if (mm?.custom_instructions !== undefined) multimodal.customInstructions = mm.custom_instructions;
    if (mm?.vlm) {
        multimodal.vlm = {
            modelName: mm.vlm.model_name ?? '',
            baseUrl: mm.vlm.base_url ?? '',
            ...(mm.vlm.interface_type !== undefined ? { interfaceType: mm.vlm.interface_type } : {}),
            ...(mm.vlm.model_id !== undefined ? { modelId: mm.vlm.model_id } : {}),
            ...(mm.vlm.credentials ? { credentials: { apiKey: Boolean(mm.vlm.credentials.api_key) } } : {}),
        };
    }
    if (mm?.cos) {
        multimodal.cos = {
            region: mm.cos.region ?? '',
            bucketName: mm.cos.bucket_name ?? '',
            appId: mm.cos.app_id ?? '',
            ...(mm.cos.path_prefix !== undefined ? { pathPrefix: mm.cos.path_prefix } : {}),
            ...(mm.cos.credentials
                ? {
                    credentials: {
                        secretId: Boolean(mm.cos.credentials.secret_id),
                        secretKey: Boolean(mm.cos.credentials.secret_key),
                    },
                }
                : {}),
        };
    }
    if (mm?.minio) {
        multimodal.minio = {
            bucketName: mm.minio.bucket_name ?? '',
            ...(mm.minio.path_prefix !== undefined ? { pathPrefix: mm.minio.path_prefix } : {}),
        };
    }

    const result: InitializationConfig & { hasFiles: boolean } = {
        hasFiles: Boolean(r.has_files),
        llm: {
            source: r.llm?.source ?? '',
            modelName: r.llm?.model_name ?? '',
            ...(r.llm?.base_url !== undefined ? { baseUrl: r.llm.base_url } : {}),
            ...(r.llm?.credentials ? { credentials: { apiKey: Boolean(r.llm.credentials.api_key) } } : {}),
        },
        embedding: {
            source: r.embedding?.source ?? '',
            modelName: r.embedding?.model_name ?? '',
            ...(r.embedding?.base_url !== undefined ? { baseUrl: r.embedding.base_url } : {}),
            ...(r.embedding?.dimension !== undefined ? { dimension: r.embedding.dimension } : {}),
            ...(r.embedding?.credentials ? { credentials: { apiKey: Boolean(r.embedding.credentials.api_key) } } : {}),
        },
        rerank: {
            enabled: Boolean(r.rerank?.enabled),
            modelName: r.rerank?.model_name ?? '',
            baseUrl: r.rerank?.base_url ?? '',
            ...(r.rerank?.credentials ? { credentials: { apiKey: Boolean(r.rerank.credentials.api_key) } } : {}),
        },
        multimodal,
        documentSplitting: {
            chunkSize: ds?.chunk_size ?? 0,
            chunkOverlap: ds?.chunk_overlap ?? 0,
            separators: ds?.separators ?? [],
            ...(ds?.strategy !== undefined ? { strategy: ds.strategy } : {}),
            ...(ds?.token_limit !== undefined ? { tokenLimit: ds.token_limit } : {}),
            ...(ds?.languages !== undefined ? { languages: ds.languages } : {}),
            ...(ds?.table_metadata_instructions !== undefined
                ? { tableMetadataInstructions: ds.table_metadata_instructions }
                : {}),
        },
        nodeExtract: {
            enabled: Boolean(ne?.enabled),
            text: ne?.text ?? '',
            tags: ne?.tags ?? [],
            nodes: ne?.nodes ?? [],
            relations: ne?.relations ?? [],
            ...(ne?.custom_instructions !== undefined ? { customInstructions: ne.custom_instructions } : {}),
        },
    };
    if (qg) {
        result.questionGeneration = {
            enabled: Boolean(qg.enabled),
            ...(qg.question_count !== undefined ? { questionCount: qg.question_count } : {}),
            ...(qg.model_id !== undefined ? { modelId: qg.model_id } : {}),
            ...(qg.custom_instructions !== undefined ? { customInstructions: qg.custom_instructions } : {}),
        };
    }
    return result;
}

function unwrapNestedError(error: unknown): unknown {
    if (error && typeof error === 'object' && 'error' in error) {
        const inner = (error as { error?: unknown }).error;
        if (inner !== undefined && inner !== null) return inner;
    }
    return error;
}

function extractErrMessage(error: unknown): string | undefined {
    if (!error || typeof error !== 'object') return undefined;
    const obj = error as { error?: { message?: unknown } | string; message?: unknown };
    if (obj.error && typeof obj.error === 'object' && typeof obj.error.message === 'string' && obj.error.message) {
        return obj.error.message;
    }
    if (typeof obj.message === 'string' && obj.message) {
        return obj.message;
    }
    return undefined;
}

export function updateKBConfig(kbId: string, config: KBModelConfigRequest) {
    const wire = serializeKBModelConfigRequest(config);
    return put(`/api/v1/initialization/config/${encodeURIComponent(kbId)}`, wire).catch((error: unknown) => {
        console.error('Failed to update KB config:', error);
        return Promise.reject(unwrapNestedError(error));
    });
}

export function initializeSystemByKB(kbId: string, config: InitializationConfig) {
    const wire = serializeInitializationConfig(config);
    return post(`/api/v1/initialization/initialize/${encodeURIComponent(kbId)}`, wire).catch((error: unknown) => {
        console.error('Failed to update KB config:', error);
        return Promise.reject(unwrapNestedError(error));
    });
}

export function checkOllamaStatus(): Promise<{ available: boolean; version?: string; error?: string; baseUrl?: string }> {
    return get<{
        data?: { available?: boolean; version?: string; error?: string; base_url?: string };
    }>('/api/v1/initialization/ollama/status')
        .then((response) => {
            const raw = response.data;
            if (!raw) return { available: false };
            return {
                available: Boolean(raw.available),
                ...(raw.version !== undefined ? { version: raw.version } : {}),
                ...(raw.error !== undefined ? { error: raw.error } : {}),
                ...(raw.base_url !== undefined ? { baseUrl: raw.base_url } : {}),
            };
        })
        .catch((error: unknown) => {
            console.error('Failed to check Ollama status:', error);
            return { available: false, error: extractErrMessage(error) || t('error.initialization.checkFailed') };
        });
}

export interface OllamaModelInfo {
    name: string;
    size: number;
    digest: string;
    modified_at: string;
}

export function listOllamaModels(): Promise<OllamaModelInfo[]> {
    return get<{ data?: { models?: OllamaModelInfo[] } }>('/api/v1/initialization/ollama/models')
        .then((response) => response.data?.models || [])
        .catch((error: unknown) => {
            console.error('Failed to list Ollama models:', error);
            return [];
        });
}

export function checkOllamaModels(models: string[]): Promise<{ models: Record<string, boolean> }> {
    return post<{ data?: { models: Record<string, boolean> } }>('/api/v1/initialization/ollama/models/check', { models })
        .then((response) => response.data || { models: {} })
        .catch((error: unknown) => {
            console.error('Failed to check Ollama models:', error);
            return Promise.reject(error);
        });
}

export function downloadOllamaModel(modelName: string): Promise<{ taskId: string; modelName: string; status: string; progress: number }> {
    return post<{
        data?: { task_id?: string; model_name?: string; status?: string; progress?: number };
    }>('/api/v1/initialization/ollama/models/download', { model_name: modelName })
        .then((response) => {
            const raw = response.data;
            if (!raw) return { taskId: '', modelName, status: 'failed', progress: 0 };
            return {
                taskId: raw.task_id ?? '',
                modelName: raw.model_name ?? modelName,
                status: raw.status ?? 'failed',
                progress: typeof raw.progress === 'number' ? raw.progress : 0,
            };
        })
        .catch((error: unknown) => {
            console.error('Failed to start Ollama model download:', error);
            return Promise.reject(error);
        });
}

export function getDownloadProgress(taskId: string): Promise<DownloadTask> {
    return get<{ data?: WireDownloadTask }>(`/api/v1/initialization/ollama/download/progress/${encodeURIComponent(taskId)}`)
        .then((response) => deserializeDownloadTask(response.data))
        .catch((error: unknown) => {
            console.error('Failed to get download progress:', error);
            return Promise.reject(error);
        });
}

export function listDownloadTasks(): Promise<DownloadTask[]> {
    return get<{ data?: WireDownloadTask[] }>('/api/v1/initialization/ollama/download/tasks')
        .then((response) => (Array.isArray(response.data) ? response.data.map(deserializeDownloadTask) : []))
        .catch((error: unknown) => {
            console.error('Failed to list download tasks:', error);
            return Promise.reject(error);
        });
}

export function getCurrentConfigByKB(kbId: string): Promise<InitializationConfig & { hasFiles: boolean }> {
    return get<{ data?: WireInitializationConfigResponse }>(`/api/v1/initialization/config/${encodeURIComponent(kbId)}`)
        .then((response) => deserializeInitializationConfigResponse(response.data))
        .catch((error: unknown) => {
            console.error('Failed to get KB config:', error);
            return Promise.reject(error);
        });
}

interface BaseModelTestPayload {
    spec?: ModelSpecOverride;
    customHeaders?: Record<string, string>;
    extraConfig?: Record<string, string>;
    interfaceType?: string;
    appSecret?: string;
}

export interface ModelTestWireRequest {
    spec?: ModelSpecOverride;
    source?: 'local' | 'remote';
    model_name: string;
    base_url?: string;
    api_key?: string;
    provider?: string;
    interface_type?: string;
    dimension?: number;
    supports_dimension_override?: boolean;
    custom_headers?: Record<string, string>;
    extra_config?: Record<string, string>;
    app_secret?: string;
    model_id?: string;
}

export function serializeModelTestRequest(
    modelConfig: {
        source?: 'local' | 'remote';
        modelName: string;
        baseUrl?: string;
        apiKey?: string;
        dimension?: number;
        supportsDimensionOverride?: boolean;
        provider?: string;
        modelId?: string;
    } & BaseModelTestPayload,
): ModelTestWireRequest {
    const wire: ModelTestWireRequest = {
        model_name: modelConfig.modelName,
    };
    if (modelConfig.spec !== undefined) wire.spec = modelConfig.spec;
    if (modelConfig.source !== undefined) wire.source = modelConfig.source;
    if (modelConfig.baseUrl !== undefined) wire.base_url = modelConfig.baseUrl;
    if (modelConfig.apiKey !== undefined) wire.api_key = modelConfig.apiKey;
    if (modelConfig.provider !== undefined) wire.provider = modelConfig.provider;
    if (modelConfig.interfaceType !== undefined) wire.interface_type = modelConfig.interfaceType;
    if (modelConfig.dimension !== undefined) wire.dimension = modelConfig.dimension;
    if (modelConfig.supportsDimensionOverride !== undefined) {
        wire.supports_dimension_override = modelConfig.supportsDimensionOverride;
    }
    if (modelConfig.customHeaders !== undefined) wire.custom_headers = modelConfig.customHeaders;
    if (modelConfig.extraConfig !== undefined) wire.extra_config = modelConfig.extraConfig;
    if (modelConfig.appSecret !== undefined) wire.app_secret = modelConfig.appSecret;
    if (modelConfig.modelId !== undefined) wire.model_id = modelConfig.modelId;
    return wire;
}

export function checkRemoteModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
    provider?: string;
    modelId?: string;
} & BaseModelTestPayload): Promise<{
    available: boolean;
    message?: string;
}> {
    return post<{ data?: { available: boolean; message?: string } }>(
        '/api/v1/initialization/remote/check',
        serializeModelTestRequest(modelConfig),
    )
        .then((response) => response.data || { available: false })
        .catch((error: unknown) => {
            console.error('Failed to check remote model:', error);
            return Promise.reject(error);
        });
}

export function testEmbeddingModel(modelConfig: {
    source: 'local' | 'remote';
    modelName: string;
    baseUrl?: string;
    apiKey?: string;
    dimension?: number;
    supportsDimensionOverride?: boolean;
    provider?: string;
    modelId?: string;
} & BaseModelTestPayload): Promise<{ available: boolean; message?: string; dimension?: number }> {
    return post<{ data?: { available: boolean; message?: string; dimension?: number } }>(
        '/api/v1/initialization/embedding/test',
        serializeModelTestRequest(modelConfig),
    )
        .then((response) => response.data || { available: false })
        .catch((error: unknown) => {
            console.error('Failed to test Embedding model:', error);
            return Promise.reject(error);
        });
}

export function checkRerankModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
    provider?: string;
    modelId?: string;
} & BaseModelTestPayload): Promise<{
    available: boolean;
    message?: string;
}> {
    return post<{ data?: { available: boolean; message?: string } }>(
        '/api/v1/initialization/rerank/check',
        serializeModelTestRequest(modelConfig),
    )
        .then((response) => response.data || { available: false })
        .catch((error: unknown) => {
            console.error('Failed to check Rerank model:', error);
            return Promise.reject(error);
        });
}

export function checkASRModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
    provider?: string;
    modelId?: string;
} & BaseModelTestPayload): Promise<{
    available: boolean;
    message?: string;
}> {
    return post<{ data?: { available: boolean; message?: string } }>(
        '/api/v1/initialization/asr/check',
        serializeModelTestRequest(modelConfig),
    )
        .then((response) => response.data || { available: false })
        .catch((error: unknown) => {
            console.error('Failed to check ASR model:', error);
            return Promise.reject(error);
        });
}

export function testMultimodalFunction(testData: {
    image: File;
    vlm_model: string;
    vlm_base_url: string;
    vlm_api_key?: string;
    vlm_interface_type?: string;
    storage_type?: 'cos' | 'minio';
    cos_secret_id?: string;
    cos_secret_key?: string;
    cos_region?: string;
    cos_bucket_name?: string;
    cos_app_id?: string;
    cos_path_prefix?: string;
    minio_bucket_name?: string;
    minio_path_prefix?: string;
    chunk_size: number;
    chunk_overlap: number;
    separators: string[];
}): Promise<{
    success: boolean;
    caption?: string;
    ocr?: string;
    processing_time?: number;
    message?: string;
}> {
    const formData = new FormData();
    formData.append('image', testData.image);
    formData.append('vlm_model', testData.vlm_model);
    formData.append('vlm_base_url', testData.vlm_base_url);
    if (testData.vlm_api_key) {
        formData.append('vlm_api_key', testData.vlm_api_key);
    }
    if (testData.vlm_interface_type) {
        formData.append('vlm_interface_type', testData.vlm_interface_type);
    }
    if (testData.storage_type) {
        formData.append('storage_type', testData.storage_type);
    }
    if (testData.storage_type === 'cos') {
        if (testData.cos_secret_id) formData.append('cos_secret_id', testData.cos_secret_id);
        if (testData.cos_secret_key) formData.append('cos_secret_key', testData.cos_secret_key);
        if (testData.cos_region) formData.append('cos_region', testData.cos_region);
        if (testData.cos_bucket_name) formData.append('cos_bucket_name', testData.cos_bucket_name);
        if (testData.cos_app_id) formData.append('cos_app_id', testData.cos_app_id);
        if (testData.cos_path_prefix) formData.append('cos_path_prefix', testData.cos_path_prefix);
    }
    if (testData.minio_bucket_name) formData.append('minio_bucket_name', testData.minio_bucket_name);
    if (testData.minio_path_prefix) formData.append('minio_path_prefix', testData.minio_path_prefix);
    formData.append('chunk_size', testData.chunk_size.toString());
    formData.append('chunk_overlap', testData.chunk_overlap.toString());
    formData.append('separators', JSON.stringify(testData.separators));

    return postUpload<{
        success?: boolean;
        data?: {
            success?: boolean;
            caption?: string;
            ocr?: string;
            processing_time?: number;
            message?: string;
        };
        error?: { message?: string } | string;
        message?: string;
    }>('/api/v1/initialization/multimodal/test', formData)
        .then((data) => {
            if (data?.success) {
                return { success: true, ...(data.data || {}) };
            }
            const msg = extractErrMessage(data) || t('error.initialization.testFailed');
            return { success: false, message: msg };
        })
        .catch((error: unknown) => {
            if (error && typeof error === 'object' && ('status' in error || 'error' in error || 'success' in error)) {
                const msg = extractErrMessage(error) || t('error.initialization.testFailed');
                return { success: false, message: msg };
            }
            console.error('Failed multimodal test:', error);
            return Promise.reject(error);
        });
}

export interface TextRelationExtractionRequest {
    text: string;
    tags: string[];
    model_id: string;
}

export interface TextRelationExtractionResponse {
    nodes: Node[];
    relations: Relation[];
}

export function extractTextRelations(request: TextRelationExtractionRequest): Promise<TextRelationExtractionResponse> {
    return post<{ data?: TextRelationExtractionResponse }>('/api/v1/initialization/extract/text-relation', request, { timeout: 60000 })
        .then((response) => response.data || { nodes: [], relations: [] })
        .catch((error: unknown) => {
            console.error('Failed to extract text relations:', error);
            return Promise.reject(error);
        });
}

export interface FabriTextRequest {
    tags: string[];
    model_id: string;
}

export interface FabriTextResponse {
    text: string;
}

export function fabriText(request: FabriTextRequest): Promise<FabriTextResponse> {
    return post<{ data?: FabriTextResponse }>('/api/v1/initialization/extract/fabri-text', request)
        .then((response) => response.data || { text: '' })
        .catch((error: unknown) => {
            console.error('Failed to generate text:', error);
            return Promise.reject(error);
        });
}

export type FabriTagRequest = Record<string, unknown>;

export interface FabriTagResponse {
    tags: string[];
}

export function fabriTag(request: FabriTagRequest): Promise<FabriTagResponse> {
    return post<{ data?: FabriTagResponse }>('/api/v1/initialization/extract/fabri-tag', request)
        .then((response) => response.data || { tags: [] })
        .catch((error: unknown) => {
            console.error('Failed to generate tags:', error);
            return Promise.reject(error);
        });
}

// Vendor extra configuration field (Mirrors internal/models/catalog.ExtraField).
export interface ModelProviderExtraFieldOption {
    label: string;
    labels?: Record<string, string>;
    value: string;
}

/**
 * Renames the primary credential input for vendors whose API does not take a
 * plain API key.
 */
export interface ModelProviderCredentialLabel {
    label: string;
    labels?: Record<string, string>;
    placeholder?: string;
    placeholders?: Record<string, string>;
    hint?: string;
    hints?: Record<string, string>;
    model_types?: string[];
    required?: boolean;
}

export interface ModelProviderExtraField {
    key: string;
    label: string;
    labels?: Record<string, string>;
    type: 'string' | 'number' | 'boolean' | 'select' | 'password' | string;
    required?: boolean;
    default?: string;
    placeholder?: string;
    placeholders?: Record<string, string>;
    options?: ModelProviderExtraFieldOption[];
    model_types?: string[];
    secret?: boolean;
}

// Mirrors handler.ModelCatalogEntryDTO.
export interface ModelCatalogEntry {
    id: string;
    name: string;
    type: string;
    api?: string;
    reasoning?: boolean;
    input?: string[];
    context_window?: number;
    max_output_tokens?: number;
    dimension?: number;
    thinking_levels?: ReasoningEffortLevel[];
    cost?: Record<string, unknown>;
    source?: string;
}

export interface ModelProviderThinking {
    format: string;
    levels: ReasoningEffortLevel[];
}

// Mirrors handler.ModelProviderDTO (pure snake_case wire & TS properties).
export interface ModelProviderOption {
    value: string;
    label: string;
    labels?: Record<string, string>;
    description: string;
    descriptions?: Record<string, string>;
    website?: string;
    icon?: string;
    api?: string;
    auth?: string;
    requires_auth?: boolean;
    default_urls: Record<string, string>;
    model_types: string[];
    extra_fields?: ModelProviderExtraField[];
    credential_labels?: ModelProviderCredentialLabel[];
    models?: ModelCatalogEntry[];
    thinking?: ModelProviderThinking;
    order?: number;
}

export function listModelProviders(modelType?: string): Promise<ModelProviderOption[]> {
    const url = modelType
        ? `/api/v1/models/providers?model_type=${encodeURIComponent(modelType)}`
        : '/api/v1/models/providers';
    return get<{ data?: unknown }>(url).then((response) => {
        const data = response?.data;
        if (!Array.isArray(data)) {
            return Promise.reject(new Error('model providers response is not a list'));
        }
        return data.map((entry) => {
            const raw = entry as Partial<ModelProviderOption>;
            return {
                ...raw,
                value: raw.value ?? '',
                label: raw.label ?? '',
                description: raw.description ?? '',
                default_urls: raw.default_urls ?? {},
                model_types: raw.model_types ?? [],
            };
        });
    });
}

export interface ResolveModelCatalogParams {
    spec?: ModelSpecOverride;
    provider: string;
    model?: string;
    base_url?: string;
    model_type?: string;
    api?: string;
    thinking_control?: string;
    remote_model_name?: string;
    [extraField: string]: string | ModelSpecOverride | undefined;
}

// Mirrors handler.ResolveModelCatalog response data.
export interface ResolvedModelCatalog {
    provider: string;
    api: string;
    base_url?: string;
    url?: string;
    remote_model: string;
    cataloged: boolean;
    model: Record<string, unknown>;
    capabilities: ModelCapabilities;
}

export function resolveModelCatalog(params: ResolveModelCatalogParams): Promise<ResolvedModelCatalog> {
    return post<{ success?: boolean; data?: ResolvedModelCatalog; message?: string }>(`/api/v1/models/catalog/resolve`, params)
        .then((response) => {
            if (response?.success && response?.data) {
                return response.data;
            }
            return Promise.reject(new Error(response?.message || 'resolve failed'));
        })
        .catch((error: unknown) => Promise.reject(unwrapNestedError(error)));
}
