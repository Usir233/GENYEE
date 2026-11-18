import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

type DynamicExports = ReturnType<typeof JSON.parse>

interface RecordedCall {
  method: string
  url: string
  body?: unknown
  config?: unknown
}

function compileModule(relPath: string): string {
  const source = readFileSync(new URL(relPath, import.meta.url), 'utf8')
  return ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
}

function toPlain<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function collectObjectKeys(value: unknown, skipMapUnderKey?: string): string[] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    if (Array.isArray(value)) {
      return value.flatMap((item) => collectObjectKeys(item, skipMapUnderKey))
    }
    return []
  }
  const obj = value as Record<string, unknown>
  const keys: string[] = []
  for (const [k, v] of Object.entries(obj)) {
    keys.push(k)
    if (k !== skipMapUnderKey) {
      keys.push(...collectObjectKeys(v, skipMapUnderKey))
    }
  }
  return keys
}

function createApiSandbox() {
  const calls: RecordedCall[] = []
  let nextResponse: unknown = { success: true, data: {} }

  const requestStub = {
    get: async (url: string, config?: unknown) => {
      calls.push({ method: 'GET', url, config })
      return nextResponse
    },
    post: async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ method: 'POST', url, body, config })
      return nextResponse
    },
    put: async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ method: 'PUT', url, body, config })
      return nextResponse
    },
    patch: async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ method: 'PATCH', url, body, config })
      return nextResponse
    },
    del: async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ method: 'DELETE', url, body, config })
      return nextResponse
    },
    postChat: async (url: string, body?: unknown, config?: unknown) => {
      calls.push({ method: 'POST_CHAT', url, body, config })
      return nextResponse
    },
    postUpload: async (url: string, body?: unknown, _onUploadProgress?: unknown, config?: unknown) => {
      calls.push({ method: 'POST_UPLOAD', url, body, config })
      return nextResponse
    },
  }

  function loadModule(relPath: string, extraDeps: Record<string, unknown> = {}): DynamicExports {
    const exports: DynamicExports = {}
    vm.runInNewContext(compileModule(relPath), {
      exports,
      FormData,
      Blob,
      File,
      URL,
      URLSearchParams,
      AbortController,
      AbortSignal,
      Promise,
      Object,
      Array,
      String,
      Number,
      Boolean,
      JSON,
      Math,
      Date,
      Error,
      encodeURIComponent,
      console: { log() {}, warn() {}, error() {} },
      localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
      require(id: string) {
        if (id in extraDeps) return extraDeps[id]
        if (id === '../../utils/request' || id === '../utils/request' || id === '@/utils/request') {
          return requestStub
        }
        if (id === '@/i18n') {
          return { default: { global: { t: (key: string) => key } }, global: { t: (key: string) => key } }
        }
        if (id === './knowledge-file-list-query') {
          return loadModule('../knowledge-base/knowledge-file-list-query.ts')
        }
        if (id === './knowledge-read-options') {
          return loadModule('../knowledge-base/knowledge-read-options.ts')
        }
        throw new Error(`Unexpected dependency in contract test (${relPath}): ${id}`)
      },
    })
    return exports
  }

  return {
    calls,
    setNextResponse(resp: unknown) {
      nextResponse = resp
    },
    loadModule,
  }
}

test('initialization wire payloads serialize 100% snake_case keys and preserve pointer-clear semantics', async () => {
  const { calls, setNextResponse, loadModule } = createApiSandbox()
  const initApi = loadModule('./index.ts')

  await initApi.updateKBConfig('kb-1', {
    llmModelId: 'llm-1',
    embeddingModelId: 'emb-1',
    vlm_config: {
      enabled: true,
      model_id: 'vlm-1',
      description_language: 'en',
      custom_instructions: 'describe tables',
    },
    asr_config: {
      enabled: true,
      model_id: 'asr-1',
      language: 'en',
    },
    documentSplitting: {
      chunkSize: 512,
      chunkOverlap: 64,
      separators: ['\n\n', '\n'],
      parserEngineRules: [{ file_types: ['pdf'], engine: 'builtin' }],
      enableParentChild: true,
      parentChunkSize: 1024,
      childChunkSize: 256,
      strategy: '',
      tokenLimit: 0,
      languages: [],
      tableMetadataInstructions: '',
    },
    multimodal: { enabled: true },
    storageBackendId: 'storage-1',
    storageProvider: 'minio',
    nodeExtract: {
      enabled: true,
      text: 'sample',
      tags: ['entity'],
      nodes: [{ name: 'Org', attributes: ['title'] }],
      relations: [{ node1: 'Org', node2: 'Org', type: 'parent' }],
      customInstructions: 'extract orgs',
    },
    questionGeneration: {
      modelId: 'qg-1',
      enabled: true,
      questionCount: 3,
      customInstructions: 'concise questions',
    },
  })

  const kbConfigCall = calls.at(-1)!
  assert.equal(kbConfigCall.method, 'PUT')
  assert.equal(kbConfigCall.url, '/api/v1/initialization/config/kb-1')
  const kbKeys = collectObjectKeys(kbConfigCall.body)
  assert.deepEqual(
    kbKeys.filter((k) => /[A-Z]/.test(k)),
    [],
    `updateKBConfig wire body must contain zero camelCase keys, got: ${kbKeys.join(', ')}`,
  )
  const kbWire = kbConfigCall.body as Record<string, Record<string, unknown>>
  assert.equal(kbWire.llm_model_id, 'llm-1')
  assert.equal(kbWire.embedding_model_id, 'emb-1')
  assert.equal(kbWire.storage_backend_id, 'storage-1')
  assert.equal(kbWire.storage_provider, 'minio')
  // Pointer-clear values ("", 0, []) must be preserved on wire, not stripped
  assert.equal(kbWire.document_splitting.strategy, '')
  assert.equal(kbWire.document_splitting.token_limit, 0)
  assert.deepEqual(toPlain(kbWire.document_splitting.languages), [])
  assert.equal(kbWire.document_splitting.table_metadata_instructions, '')
  assert.equal(kbWire.question_generation.question_count, 3)
  assert.equal(kbWire.question_generation.model_id, 'qg-1')

  // Undefined pointer fields must be omitted so server leaves stored values untouched
  const minimalWire = initApi.serializeKBModelConfigRequest({
    llmModelId: 'llm-2',
    embeddingModelId: 'emb-2',
    documentSplitting: { chunkSize: 256, chunkOverlap: 32, separators: ['\n'] },
    multimodal: { enabled: false },
    nodeExtract: { enabled: false, text: '', tags: [], nodes: [], relations: [] },
  })
  assert.equal('strategy' in minimalWire.document_splitting, false)
  assert.equal('token_limit' in minimalWire.document_splitting, false)
  assert.equal('languages' in minimalWire.document_splitting, false)
  assert.equal('table_metadata_instructions' in minimalWire.document_splitting, false)

  await initApi.initializeSystemByKB('kb-init', {
    llm: { source: 'remote', modelName: 'gpt-4o', baseUrl: 'https://api.example.com/v1', apiKey: 'sk-1' },
    embedding: { source: 'remote', modelName: 'text-embedding-3-small', baseUrl: 'https://api.example.com/v1', apiKey: 'sk-2', dimension: 1536 },
    rerank: { enabled: true, modelName: 'bge-reranker', baseUrl: 'https://api.example.com/v1', apiKey: 'sk-3' },
    multimodal: {
      enabled: true,
      storageType: 'cos',
      vlm: { modelName: 'gpt-4o', baseUrl: 'https://api.example.com/v1', apiKey: 'sk-4', interfaceType: 'openai' },
      cos: { region: 'ap-guangzhou', bucketName: 'docs', appId: '125000', pathPrefix: 'kb/', secretId: 'sid', secretKey: 'skey' },
      minio: { bucketName: 'local-bucket', pathPrefix: 'prefix/' },
    },
    documentSplitting: { chunkSize: 500, chunkOverlap: 50, separators: ['\n'], strategy: 'semantic', tokenLimit: 1024, languages: ['en'] },
    nodeExtract: { enabled: false, text: '', tags: [], nodes: [], relations: [] },
    questionGeneration: { enabled: true, questionCount: 5 },
  })
  const initCall = calls.at(-1)!
  assert.equal(initCall.url, '/api/v1/initialization/initialize/kb-init')
  assert.deepEqual(collectObjectKeys(initCall.body).filter((k) => /[A-Z]/.test(k)), [])

  setNextResponse({ data: { available: true, dimension: 1024 } })
  await initApi.testEmbeddingModel({
    source: 'remote',
    modelName: 'bge-m3',
    baseUrl: 'https://emb.example.com/v1',
    apiKey: 'sk-emb',
    dimension: 1024,
    supportsDimensionOverride: true,
    provider: 'openai',
    interfaceType: 'openai',
    appSecret: 'sec-1',
    modelId: 'mid-1',
    customHeaders: { 'X-Trace-Header': '1' },
    extraConfig: { region_code: 'us' },
  })
  const embTestCall = calls.at(-1)!
  assert.equal(embTestCall.url, '/api/v1/initialization/embedding/test')
  assert.deepEqual(
    collectObjectKeys(embTestCall.body, 'custom_headers').filter((k) => /[A-Z]/.test(k)),
    [],
  )
  const embWire = embTestCall.body as Record<string, unknown>
  assert.equal(embWire.model_name, 'bge-m3')
  assert.equal(embWire.base_url, 'https://emb.example.com/v1')
  assert.equal(embWire.api_key, 'sk-emb')
  assert.equal(embWire.supports_dimension_override, true)
  assert.equal(embWire.interface_type, 'openai')
  assert.equal(embWire.app_secret, 'sec-1')
  assert.equal(embWire.model_id, 'mid-1')

  for (const [fnName, expectedUrl] of [
    ['checkRemoteModel', '/api/v1/initialization/remote/check'],
    ['checkRerankModel', '/api/v1/initialization/rerank/check'],
    ['checkASRModel', '/api/v1/initialization/asr/check'],
  ] as const) {
    setNextResponse({ data: { available: true } })
    await initApi[fnName]({ modelName: 'm', baseUrl: 'https://example.com', apiKey: 'k' })
    const c = calls.at(-1)!
    assert.equal(c.url, expectedUrl)
    assert.deepEqual(collectObjectKeys(c.body).filter((k) => /[A-Z]/.test(k)), [])
  }
})

test('initialization snake_case wire responses deserialize cleanly including ModelProviderDTO', async () => {
  const { setNextResponse, loadModule, calls } = createApiSandbox()
  const initApi = loadModule('./index.ts')

  setNextResponse({
    data: {
      has_files: true,
      llm: { source: 'remote', model_name: 'gpt-4o', base_url: 'https://llm.local', credentials: { api_key: true } },
      embedding: { source: 'local', model_name: 'nomic-embed', dimension: 768, credentials: { api_key: false } },
      rerank: { enabled: true, model_name: 'rerank-1', base_url: 'https://rerank.local', credentials: { api_key: true } },
      multimodal: {
        enabled: true,
        storage_type: 'cos',
        description_language: 'en',
        custom_instructions: 'detail',
        vlm: { model_name: 'qwen-vl', base_url: 'https://vlm.local', interface_type: 'openai', model_id: 'vlm-id', credentials: { api_key: true } },
        cos: { region: 'ap-shanghai', bucket_name: 'bkt', app_id: '99', path_prefix: 'p/', credentials: { secret_id: true, secret_key: false } },
      },
      document_splitting: {
        chunk_size: 800,
        chunk_overlap: 100,
        separators: ['\n'],
        strategy: 'markdown',
        token_limit: 2048,
        languages: ['en', 'zh'],
        table_metadata_instructions: 'headers',
      },
      node_extract: { enabled: true, text: 't', tags: ['a'], nodes: [], relations: [], custom_instructions: 'ne-inst' },
      question_generation: { enabled: true, question_count: 4, model_id: 'qg-m', custom_instructions: 'qg-inst' },
    },
  })
  const cfg = await initApi.getCurrentConfigByKB('kb-read')
  assert.equal(cfg.hasFiles, true)
  assert.equal(cfg.llm.modelName, 'gpt-4o')
  assert.equal(cfg.llm.baseUrl, 'https://llm.local')
  assert.equal(cfg.llm.credentials?.apiKey, true)
  assert.equal(cfg.multimodal.storageType, 'cos')
  assert.equal(cfg.multimodal.vlm?.modelName, 'qwen-vl')
  assert.equal(cfg.multimodal.vlm?.modelId, 'vlm-id')
  assert.equal(cfg.multimodal.cos?.bucketName, 'bkt')
  assert.equal(cfg.multimodal.cos?.pathPrefix, 'p/')
  assert.equal(cfg.multimodal.cos?.credentials?.secretId, true)
  assert.equal(cfg.multimodal.cos?.credentials?.secretKey, false)
  assert.equal(cfg.documentSplitting.chunkSize, 800)
  assert.equal(cfg.documentSplitting.tokenLimit, 2048)
  assert.equal(cfg.questionGeneration?.questionCount, 4)

  setNextResponse({ data: { available: true, version: '0.6.2', base_url: 'http://127.0.0.1:11434' } })
  const ollama = await initApi.checkOllamaStatus()
  assert.deepEqual(toPlain(ollama), { available: true, version: '0.6.2', baseUrl: 'http://127.0.0.1:11434' })

  setNextResponse({ data: { task_id: 't-100', model_name: 'qwen2.5:7b', status: 'downloading', progress: 42 } })
  const dl = await initApi.downloadOllamaModel('qwen2.5:7b')
  assert.deepEqual(toPlain(calls.at(-1)!.body), { model_name: 'qwen2.5:7b' })
  assert.deepEqual(toPlain(dl), { taskId: 't-100', modelName: 'qwen2.5:7b', status: 'downloading', progress: 42 })

  setNextResponse({
    data: {
      id: 't-100',
      model_name: 'qwen2.5:7b',
      status: 'completed',
      progress: 100,
      message: 'done',
      start_time: '2026-11-18T18:00:00Z',
      end_time: '2026-11-18T18:01:00Z',
    },
  })
  const progress = await initApi.getDownloadProgress('t-100')
  assert.equal(progress.modelName, 'qwen2.5:7b')
  assert.equal(progress.startTime, '2026-11-18T18:00:00Z')
  assert.equal(progress.endTime, '2026-11-18T18:01:00Z')

  setNextResponse({
    data: [
      {
        value: 'openai',
        label: 'OpenAI',
        description: 'OpenAI API',
        requires_auth: true,
        default_urls: { chat: 'https://api.openai.com/v1' },
        model_types: ['chat', 'embedding'],
        extra_fields: [{ key: 'org_id', label: 'Org ID', type: 'string', model_types: ['chat'] }],
        credential_labels: [{ label: 'API Key', model_types: ['chat'], required: true }],
      },
    ],
  })
  const providers = await initApi.listModelProviders('chat')
  assert.equal(calls.at(-1)!.url, '/api/v1/models/providers?model_type=chat')
  assert.equal(providers[0].requires_auth, true)
  assert.deepEqual(toPlain(providers[0].default_urls), { chat: 'https://api.openai.com/v1' })
  assert.deepEqual(toPlain(providers[0].model_types), ['chat', 'embedding'])
  assert.equal(providers[0].extra_fields?.[0].key, 'org_id')
  assert.equal(providers[0].credential_labels?.[0].label, 'API Key')
})

test('W4 canonical backend routes, snake_case wire fields, and dead-API removals hold across api/*', async () => {
  assert.equal(existsSync(new URL('../web-search.ts', import.meta.url)), false, 'dead api/web-search.ts must be deleted')

  const { calls, setNextResponse, loadModule } = createApiSandbox()

  // 1. Auth: POST /api/v1/auth/refresh sends {"refresh_token"} and getCurrentTenant is removed
  const authApi = loadModule('../auth/index.ts')
  assert.equal('getCurrentTenant' in authApi, false, 'dead getCurrentTenant must be removed')
  setNextResponse({ success: true, access_token: 'jwt-new', refresh_token: 'rt-new' })
  const refreshed = await authApi.refreshToken('rt-old')
  assert.equal(calls.at(-1)!.method, 'POST')
  assert.equal(calls.at(-1)!.url, '/api/v1/auth/refresh')
  assert.deepEqual(toPlain(calls.at(-1)!.body), { refresh_token: 'rt-old' })
  assert.equal(refreshed.data.token, 'jwt-new')
  assert.equal(refreshed.data.refreshToken, 'rt-new')

  // 2. Organization: dead updateAgentSharePermission is removed & AbortSignal forwards cleanly
  const orgApi = loadModule('../organization/index.ts')
  assert.equal('updateAgentSharePermission' in orgApi, false, 'dead updateAgentSharePermission must be removed')
  const abortCtrl = new AbortController()
  await orgApi.listMyOrganizations(abortCtrl.signal)
  assert.equal((calls.at(-1)!.config as { signal?: AbortSignal })?.signal, abortCtrl.signal)

  // 3. Wiki: /api/v1/knowledge-bases/:kbId/wiki/...
  const wikiApi = loadModule('../wiki/index.ts')
  await wikiApi.getWikiStats('kb-42')
  assert.equal(calls.at(-1)!.url, '/api/v1/knowledge-bases/kb-42/wiki/stats')
  await wikiApi.searchWikiPages('kb-42', 'architecture')
  assert.equal(calls.at(-1)!.url, '/api/v1/knowledge-bases/kb-42/wiki/search?q=architecture')

  // 4. Data sources: /api/v1/data-sources...
  const dsApi = loadModule('../datasource/index.ts')
  await dsApi.listDataSources('kb-42')
  assert.equal(calls.at(-1)!.url, '/api/v1/data-sources?kb_id=kb-42')
  await dsApi.triggerSync('ds-9')
  assert.equal(calls.at(-1)!.url, '/api/v1/data-sources/ds-9/sync')

  // 5. Chat session generate-title: /api/v1/sessions/:id/generate-title
  const chatApi = loadModule('../chat/index.ts')
  await chatApi.generateSessionsTitle('sess-7', { messages: [] })
  assert.equal(calls.at(-1)!.method, 'POST')
  assert.equal(calls.at(-1)!.url, '/api/v1/sessions/sess-7/generate-title')

  // 6. User favorites: /api/v1/me/favorites
  const favApi = loadModule('../user-favorites.ts')
  await favApi.listFavorites('agent')
  assert.equal(calls.at(-1)!.method, 'GET')
  assert.equal(calls.at(-1)!.url, '/api/v1/me/favorites?type=agent')
  await favApi.addFavorite('agent', 'ag-1')
  assert.equal(calls.at(-1)!.method, 'POST')
  assert.equal(calls.at(-1)!.url, '/api/v1/me/favorites')
  await favApi.removeFavorite('agent', 'ag-1')
  assert.equal(calls.at(-1)!.method, 'DELETE')
  assert.equal(calls.at(-1)!.url, '/api/v1/me/favorites/agent/ag-1')

  // 7. Knowledge base: FAQ batch vs single create, enable_multimodal, and multipart file_name
  const kbApi = loadModule('../knowledge-base/index.ts')
  await kbApi.upsertFAQEntries('kb-42', { entries: [], mode: 'append' })
  assert.equal(calls.at(-1)!.method, 'POST')
  assert.equal(calls.at(-1)!.url, '/api/v1/knowledge-bases/kb-42/faq/entries/batch')
  await kbApi.createFAQEntry('kb-42', { standard_question: 'Q', answers: ['A'] })
  assert.equal(calls.at(-1)!.method, 'POST')
  assert.equal(calls.at(-1)!.url, '/api/v1/knowledge-bases/kb-42/faq/entries')

  await kbApi.createKnowledgeFromURL('kb-42', { url: 'https://example.com', enable_multimodal: true })
  assert.deepEqual(toPlain(calls.at(-1)!.body), { url: 'https://example.com', enable_multimodal: true })

  const uploadFile = new File(['hello'], 'raw.pdf', { type: 'application/pdf' })
  await kbApi.uploadKnowledgeFile('kb-42', {
    file: uploadFile,
    file_name: 'display.pdf',
    process_config: { enable_multimodal: true },
  })
  const uploadForm = calls.at(-1)!.body as FormData
  assert.equal(uploadForm.get('file_name'), 'display.pdf')
  assert.equal(uploadForm.get('fileName'), null)
  assert.deepEqual(JSON.parse(String(uploadForm.get('process_config'))), { enable_multimodal: true })

  // 8. System: ParserEngineInfo uses pure snake_case fields
  const sysApi = loadModule('../system/index.ts')
  setNextResponse({
    data: [
      {
        name: 'builtin',
        description: 'Built-in parser',
        file_types: ['pdf', 'docx'],
        available: true,
        unavailable_reason: '',
      },
    ],
  })
  const enginesResp = await sysApi.getParserEngines()
  assert.deepEqual(toPlain(enginesResp.data[0]), {
    name: 'builtin',
    description: 'Built-in parser',
    file_types: ['pdf', 'docx'],
    available: true,
    unavailable_reason: '',
  })
})
