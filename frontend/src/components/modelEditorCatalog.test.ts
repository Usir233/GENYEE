import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { nextTick } from 'vue'
import type { ResolvedModelCatalog } from '@/api/initialization'
import { combinedEditorTemplates, deferred, fixture, plain } from './modelEditorTestHarness.ts'
import type { CanonicalModelProviderOption } from './model-editor/types'

const settingsSource = readFileSync(new URL('../views/settings/ModelSettings.vue', import.meta.url), 'utf8')
const saveStart = settingsSource.indexOf('const handleModelSave =')
const saveEnd = settingsSource.indexOf('// 删除模型', saveStart)
const saveScript = ts.transpileModule(settingsSource.slice(saveStart, saveEnd), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

const catalogProviders: Array<Partial<CanonicalModelProviderOption> & { value: string }> = [{
  value: 'lkeap',
  label: 'Tencent Cloud LKEAP',
  labels: { 'zh-CN': '腾讯云 LKEAP' },
  description: '',
  order: 1,
  icon: 'data:image/svg+xml;base64,PHN2Zy8+',
  requires_auth: true,
  default_urls: { chat: 'https://api.lkeap.cloud.tencent.com/v1', rerank: 'https://lkeap.tencentcloudapi.com' },
  model_types: ['chat', 'rerank'],
  credential_labels: [
    { label: 'SecretId', placeholder: 'Enter SecretId', hint: 'Tencent Cloud CAM SecretId', model_types: ['Rerank'], required: true },
  ],
  extra_fields: [
    { key: 'secret_key', label: 'Secret Key', type: 'password', required: true, model_types: ['Rerank'], secret: true },
    { key: 'region', label: 'Region', type: 'string', default: 'ap-guangzhou', model_types: ['Rerank'] },
    { key: 'api_version', label: 'API Version', type: 'string', default: '2024-10-21' },
  ],
  models: [
    { id: 'deepseek-v3.1', name: 'DeepSeek V3.1', type: 'chat', reasoning: true, input: ['text', 'image'], context_window: 128000, max_output_tokens: 8192, thinking_levels: ['off', 'auto'] },
  ],
  thinking: { format: 'thinking.type', levels: ['off', 'auto'] },
}]

test('vendor extra fields and credential_labels: snake_case ModelProviderDTO fields populate defaults, labels, and app_secret', async () => {
  const f = await fixture({ type: 'rerank', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.handleProviderChange('lkeap')
    await nextTick()
    assert.equal(f.vm.formData.baseUrl, 'https://lkeap.tencentcloudapi.com')
    assert.equal(f.vm.apiKeyLabel, 'SecretId')
    assert.equal(f.vm.apiKeyPlaceholder, 'Enter SecretId')
    assert.equal(f.vm.apiKeyHint, 'Tencent Cloud CAM SecretId')
    assert.equal(f.vm.apiKeyRequired, true)
    assert.deepEqual(f.vm.plainExtraFields.map((x) => x.key), ['region', 'api_version'])
    assert.equal(f.vm.secretExtraField?.key, 'secret_key')
    assert.equal(f.vm.formData.extraConfig.region, 'ap-guangzhou')
    assert.equal(f.vm.formData.extraConfig.api_version, '2024-10-21')
    assert.equal(f.vm.credentialFields.some((x) => x.key === 'app_secret'), true)

    f.vm.formData.modelName = 'lke-reranker-base'
    f.vm.formData.appSecret = ' sk-secret '
    f.vm.setExtraConfig('region', ' ap-beijing ')
    await f.vm.checkRemoteAPI()
    assert.equal(f.requests[0].appSecret, 'sk-secret')
    assert.deepEqual(plain(f.requests[0].extraConfig), { region: 'ap-beijing', api_version: '2024-10-21' })

    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.deepEqual(plain(f.saves[0].extraConfig), { region: 'ap-beijing', api_version: '2024-10-21' })
    assert.equal(f.saves[0].appSecret, ' sk-secret ')
  } finally {
    f.close()
  }
})

test('required vendor extra fields block saving until filled', async () => {
  const f = await fixture({ type: 'rerank', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.handleProviderChange('lkeap')
    await nextTick()
    f.vm.formData.modelName = 'lke-reranker-base'
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 0, 'missing required secret_key must not save')
    assert.equal(f.toasts.length, 1)
    f.vm.formData.appSecret = 'sk'
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
  } finally {
    f.close()
  }
})

test('picking a catalog model fills blank capability fields only', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    assert.equal(f.vm.apiKeyRequired, true, 'requires_auth marks create-mode key required')
    assert.deepEqual(f.vm.catalogModelOptions.map((o) => o.value), ['draft-model', 'deepseek-v3.1'])
    f.vm.formData.contextWindow = 32000
    f.vm.formData.modelName = 'deepseek-v3.1'
    f.vm.handleCatalogModelChange('deepseek-v3.1')
    assert.equal(f.vm.formData.contextWindow, 32000, 'user value wins')
    assert.equal(f.vm.formData.maxOutputTokens, 8192)
    assert.equal(f.vm.formData.supportsVision, true)
    f.vm.handleCatalogModelCreate(' my-custom-model ')
    assert.equal(f.vm.formData.modelName, 'my-custom-model')
    assert.equal(f.vm.catalogModelOptions[0].value, 'my-custom-model', 'free text stays selectable')
  } finally {
    f.close()
  }
})

const embeddingProviders: Array<Partial<CanonicalModelProviderOption> & { value: string }> = [{
  value: 'volcengine',
  label: 'Volcengine',
  description: '',
  order: 1,
  icon: '',
  default_urls: { embedding: 'https://ark.cn-beijing.volces.com/api/v3' },
  model_types: ['embedding'],
  models: [
    { id: 'doubao-embedding-vision-251215', name: 'Doubao Embedding Vision', type: 'embedding', input: ['text', 'image', 'video'], dimension: 2048 },
    { id: 'doubao-embedding-text', name: 'Doubao Embedding Text', type: 'embedding', dimension: 2048 },
  ],
}]

test('embedding image input follows the catalog and stores only a differing declaration', async () => {
  const f = await fixture({ type: 'embedding', providers: embeddingProviders })
  try {
    f.vm.formData.provider = 'volcengine'
    f.vm.formData.modelName = 'doubao-embedding-vision-251215'
    f.vm.handleCatalogModelChange('doubao-embedding-vision-251215')
    await nextTick()
    assert.equal(f.vm.embeddingAcceptsImages, true, 'catalogued multimodal model')
    assert.equal(f.vm.formData.spec, null, 'the catalog answer is not copied into the row')

    f.vm.embeddingAcceptsImages = false
    assert.deepEqual(plain(f.vm.formData.spec), { input: ['text'] }, 'narrowing the catalog is a declaration')
    f.vm.embeddingAcceptsImages = true
    assert.equal(f.vm.formData.spec, null, 'back to the catalog answer drops it again')

    f.vm.formData.spec = { compat: { encoding_format: 'float' } }
    f.vm.handleCatalogModelCreate('my-clip')
    await nextTick()
    assert.equal(f.vm.embeddingAcceptsImages, false)
    f.vm.embeddingAcceptsImages = true
    assert.deepEqual(plain(f.vm.formData.spec), { compat: { encoding_format: 'float' }, input: ['text', 'image'] })

    f.vm.formData.modelName = 'doubao-embedding-text'
    f.vm.handleCatalogModelChange('doubao-embedding-text')
    await nextTick()
    assert.equal(f.vm.embeddingAcceptsImages, false)
    assert.deepEqual(plain(f.vm.formData.spec), { compat: { encoding_format: 'float' } })
  } finally {
    f.close()
  }
})

test('resolve panel is refreshed once per 400ms burst of edits and ignored while hidden', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.resolves.length = 0
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    f.vm.formData.modelName = 'deepseek-v3'
    await nextTick()
    f.vm.formData.modelName = 'deepseek-v3.1'
    await nextTick()
    assert.equal(f.resolves.length, 0, 'debounced')
    await new Promise((resolve) => setTimeout(resolve, 450))
    assert.equal(f.resolves.length, 1)
    assert.equal(f.resolves[0].model, 'deepseek-v3.1')
    assert.equal(f.resolves[0].model_type, 'chat')
    assert.equal(f.vm.resolved?.api, 'openai-completions')
    f.props.visible = false
    await nextTick()
    f.vm.formData.modelName = 'other'
    await new Promise((resolve) => setTimeout(resolve, 450))
    assert.equal(f.resolves.length, 1, 'no resolve while the drawer is closed')
  } finally {
    f.close()
  }
})

for (const type of ['embedding', 'rerank', 'asr']) {
  test(`${type}: the catalog diagnosis panel is hidden and never fetched`, async () => {
    const f = await fixture({ type, providers: catalogProviders })
    try {
      f.resolves.length = 0
      f.vm.formData.provider = 'lkeap'
      await nextTick()
      f.vm.formData.modelName = 'whatever'
      await new Promise((resolve) => setTimeout(resolve, 450))
      assert.equal(f.vm.showResolvedPanel, false, 'protocol / thinking levels are meaningless here')
      assert.equal(f.vm.resolved, null)
      assert.equal(f.resolves.length, 0, 'no request for a panel that is not rendered')
    } finally {
      f.close()
    }
  })
}

test('vllm keeps the catalog diagnosis panel', async () => {
  const f = await fixture({ type: 'vllm', providers: catalogProviders })
  try {
    f.resolves.length = 0
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 450))
    assert.equal(f.vm.showResolvedPanel, true)
    assert.equal(f.resolves.length, 1)
    assert.equal(f.resolves[0].model_type, 'vllm')
  } finally {
    f.close()
  }
})

test('an out-of-order resolve response cannot overwrite a newer one', async () => {
  const pendings: Array<ReturnType<typeof deferred<Partial<ResolvedModelCatalog>>>> = []
  const f = await fixture({
    type: 'chat',
    providers: catalogProviders,
    resolve: (params) => {
      const slot = deferred<Partial<ResolvedModelCatalog>>()
      pendings.push(slot)
      return slot.promise.then((value) => ({ ...value, remote_model: String(params.model ?? '') }))
    },
  })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.formData.modelName = 'first-model'
    await new Promise((resolve) => setTimeout(resolve, 450))
    f.vm.formData.modelName = 'second-model'
    await new Promise((resolve) => setTimeout(resolve, 450))
    assert.equal(pendings.length, 2, 'both requests are in flight')
    pendings[1].resolve({ provider: 'lkeap', api: 'openai-completions', cataloged: true })
    await nextTick()
    pendings[0].resolve({ provider: 'lkeap', api: 'anthropic-messages', cataloged: false })
    await new Promise((resolve) => setTimeout(resolve, 10))
    assert.equal(f.vm.resolved?.remote_model, 'second-model', 'the stale answer must be dropped')
    assert.equal(f.vm.resolved?.api, 'openai-completions')
    assert.equal(f.vm.resolving, false)
  } finally {
    f.close()
  }
})

test('a resolve failure without a readable message shows no [object Object]', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders, resolve: async () => { throw { code: 500 } } })
  try {
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 450))
    assert.equal(f.vm.resolveFailed, true)
    assert.equal(f.vm.resolveError, '', 'no detail beats a stringified object')
    assert.equal(f.vm.resolved, null)
  } finally {
    f.close()
  }
})

test('a provider id missing from the catalog still shows in the selector', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'retired-vendor'
    await nextTick()
    assert.equal(f.vm.selectedProvider, undefined)
    assert.equal(f.vm.selectedProviderDisplayLabel, 'retired-vendor')
    assert.deepEqual(f.vm.plainExtraFields, [], 'an unknown vendor declares no extra fields')
  } finally {
    f.close()
  }
})

test('editing preserves extra_config keys the current vendor does not declare', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.props.visible = false
    await nextTick()
    f.props.modelData = {
      id: 'legacy-extra',
      modelName: 'deepseek-v3.1',
      name: '',
      source: 'remote',
      baseUrl: 'https://api.lkeap.cloud.tencent.com/v1',
      provider: 'lkeap',
      isDefault: false,
      extraConfig: { api_version: 'v1', retired_knob: 'keep-me' },
      spec: { reasoning: true, context_window: 64000, compat: { max_tokens_field: 'max_tokens' } },
    }
    f.props.visible = true
    await nextTick()
    await nextTick()
    assert.deepEqual(plain(f.vm.formData.extraConfig), { api_version: 'v1', retired_knob: 'keep-me' })
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.deepEqual(plain(f.saves[0].extraConfig), { api_version: 'v1', retired_knob: 'keep-me' })
    assert.deepEqual(
      plain(f.saves[0].spec),
      { reasoning: true, context_window: 64000, compat: { max_tokens_field: 'max_tokens' } },
      'spec fields other than compat survive the round-trip',
    )
  } finally {
    f.close()
  }
})

test('advanced overrides: invalid compat JSON blocks save, valid JSON and protocol override are persisted', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.setExtraConfig('api', 'openai-responses')
    f.vm.setExtraConfig('remote_model_name', '')
    f.vm.formData.specCompat = '{ not json'
    await nextTick()
    assert.notEqual(f.vm.specCompatError, '')
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 0)
    assert.equal(f.vm.advancedOpen, true, 'the advanced section opens to show the error')
    f.vm.formData.specCompat = '["array"]'
    await nextTick()
    assert.equal(f.vm.specCompatError, 'model.editor.advanced.compat.mustBeObject')
    f.vm.formData.specCompat = '{"max_tokens_field": "max_tokens"}'
    await nextTick()
    assert.equal(f.vm.specCompatError, '')
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.deepEqual(plain(f.saves[0].extraConfig), { api: 'openai-responses' })
    assert.equal(f.saves[0].specCompat, '{"max_tokens_field": "max_tokens"}')
  } finally {
    f.close()
  }
})

test('editing a legacy row keeps thinking_control until the user clears it', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.props.visible = false
    await nextTick()
    f.props.modelData = {
      id: 'legacy',
      modelName: 'qwen3-32b',
      name: '',
      source: 'remote',
      baseUrl: 'https://example.com/v1',
      provider: 'aliyun',
      isDefault: false,
      thinkingControl: 'enable_thinking',
      extraConfig: { thinking_control: 'enable_thinking', api_version: 'v1' },
      spec: { compat: { max_tokens_field: 'max_tokens' }, context_window: 1 },
    }
    f.props.visible = true
    await nextTick()
    await nextTick()
    assert.equal(f.vm.showLegacyThinkingControl, true)
    assert.equal(f.vm.formData.thinkingControl, 'enable_thinking')
    assert.deepEqual(plain(f.vm.formData.extraConfig), { api_version: 'v1' })
    assert.equal(f.vm.formData.specCompat, JSON.stringify({ max_tokens_field: 'max_tokens' }, null, 2))
    assert.deepEqual(plain(f.vm.buildExtraConfig()), { api_version: 'v1', thinking_control: 'enable_thinking' })
    f.vm.formData.thinkingControl = ''
    assert.equal(f.vm.showLegacyThinkingControl, true, 'select stays visible after clearing')
    assert.deepEqual(plain(f.vm.buildExtraConfig()), { api_version: 'v1' })
  } finally {
    f.close()
  }
})

test('parent save serializes extra_config, spec.compat and max_output_tokens from the editor payload', async () => {
  const payloads: Array<{ parameters: Record<string, unknown> }> = []
  const save = runInNewContext(saveScript + '\nhandleModelSave', {
    URL,
    console: { error() {} },
    currentModelType: { value: 'chat' },
    editingModel: { value: null },
    getModelType: () => 'KnowledgeQA',
    t: (key: string) => key,
    createModel: async (data: { parameters: Record<string, unknown> }) => { payloads.push(data) },
    updateModelAPI: async () => {},
    MessagePlugin: { success() {}, error() {}, warning() {} },
    loadModels: async () => {},
  }) as (payload: Record<string, unknown>) => Promise<void>
  await save({
    modelName: 'gpt-5',
    source: 'remote',
    baseUrl: 'https://api.openai.com/v1',
    provider: 'openai',
    extraConfig: { api: 'openai-responses', remote_model_name: ' gpt-5-2025 ', empty: '   ' },
    thinkingControl: '',
    specCompat: '{"max_tokens_field":"max_completion_tokens"}',
    spec: { compat: { old: true }, reasoning: true },
    contextWindow: 400000,
    maxOutputTokens: 16384,
  })
  assert.equal(payloads.length, 1)
  const params = payloads[0].parameters
  assert.deepEqual(plain(params.extra_config), { api: 'openai-responses', remote_model_name: 'gpt-5-2025' })
  assert.deepEqual(plain(params.spec), { reasoning: true, compat: { max_tokens_field: 'max_completion_tokens' } })
  assert.equal(params.max_output_tokens, 16384)
  assert.equal(params.context_window, 400000)
  await assert.rejects(
    save({
      modelName: 'gpt-5',
      source: 'remote',
      baseUrl: 'https://api.openai.com/v1',
      specCompat: '[1]',
    }),
    /compat\.invalid/,
  )
})

test('parent save sends an empty extra_config so cleared vendor fields really clear', async () => {
  const updates: Array<{ parameters: Record<string, unknown> }> = []
  const creates: Array<Record<string, unknown>> = []
  const save = runInNewContext(saveScript + '\nhandleModelSave', {
    URL,
    console: { error() {} },
    currentModelType: { value: 'chat' },
    editingModel: { value: { id: 'saved-model' } },
    getModelType: () => 'KnowledgeQA',
    t: (key: string) => key,
    createModel: async (data: Record<string, unknown>) => { creates.push(data) },
    updateModelAPI: async (_id: string, data: { parameters: Record<string, unknown> }) => { updates.push(data) },
    MessagePlugin: { success() {}, error() {}, warning() {} },
    loadModels: async () => {},
  }) as (payload: Record<string, unknown>) => Promise<void>
  await save({
    modelName: 'gpt-5',
    source: 'remote',
    baseUrl: 'https://api.openai.com/v1',
    provider: 'openai',
    extraConfig: {},
    thinkingControl: '',
  })
  assert.deepEqual(plain(updates[0].parameters.extra_config), {})
  await save({ modelName: 'llama3', source: 'local', extraConfig: {}, thinkingControl: '' })
  assert.equal('extra_config' in updates[1].parameters, false)
  assert.equal(creates.length, 0)
})

const switchProviders: Array<Partial<CanonicalModelProviderOption> & { value: string }> = [
  {
    value: 'vendor-a',
    label: 'Vendor A',
    description: '',
    order: 1,
    model_types: ['chat'],
    default_urls: { chat: 'https://a.example.com/v1' },
    models: [
      { id: 'shared-model', name: 'Shared', type: 'chat', context_window: 128000, max_output_tokens: 8192 },
      { id: 'only-on-a', name: 'Only on A', type: 'chat', context_window: 1000000, input: ['text', 'image'] },
    ],
  },
  {
    value: 'vendor-b',
    label: 'Vendor B',
    description: '',
    order: 2,
    model_types: ['chat'],
    default_urls: { chat: 'https://b.example.com/v1' },
    models: [
      { id: 'shared-model', name: 'Shared', type: 'chat', context_window: 128000 },
    ],
  },
]

test('switching vendor drops a model the new vendor does not serve, and what the catalog filled for it', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    f.vm.formData.modelName = 'only-on-a'
    f.vm.handleCatalogModelChange('only-on-a')
    await nextTick()
    assert.equal(f.vm.formData.modelName, 'only-on-a')
    assert.equal(f.vm.formData.contextWindow, 1000000)
    assert.equal(f.vm.formData.supportsVision, true)

    f.vm.formData.provider = 'vendor-b'
    f.vm.handleProviderChange('vendor-b')
    await nextTick()
    assert.equal(f.vm.formData.modelName, '')
    assert.equal(f.vm.formData.contextWindow, undefined)
    assert.equal(f.vm.formData.supportsVision, false)
    assert.equal(f.vm.formData.baseUrl, 'https://b.example.com/v1')
  } finally {
    f.close()
  }
})

test('switching vendor keeps a model both vendors serve, and never discards typed values', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    f.vm.formData.modelName = 'shared-model'
    f.vm.handleCatalogModelChange('shared-model')
    await nextTick()
    f.vm.formData.contextWindow = 64000
    f.vm.formData.maxOutputTokens = 4096

    f.vm.formData.provider = 'vendor-b'
    f.vm.handleProviderChange('vendor-b')
    await nextTick()
    assert.equal(f.vm.formData.modelName, 'shared-model', 'both vendors serve it')
    assert.equal(f.vm.formData.contextWindow, 64000, 'a typed value is the operator’s')
    assert.equal(f.vm.formData.maxOutputTokens, 4096)
  } finally {
    f.close()
  }
})

test('switching vendor clears a connection result that described the old one', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    f.vm.formData.modelName = 'shared-model'
    await f.vm.checkRemoteAPI()
    assert.equal(f.vm.remoteChecked, true)

    f.vm.formData.provider = 'vendor-b'
    f.vm.handleProviderChange('vendor-b')
    await nextTick()
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteMessage, '')
  } finally {
    f.close()
  }
})

test('ASR vendor changes replace the previous URL and show the selected catalog model as the hint', async () => {
  const f = await fixture({
    type: 'asr',
    providers: [
      { value: 'openai', default_urls: { asr: 'https://api.openai.com/v1' }, models: [{ id: 'whisper-1', name: 'whisper-1', type: 'asr' }] },
      { value: 'zhipu', default_urls: { asr: 'https://open.bigmodel.cn/api/paas/v4' }, models: [{ id: 'glm-asr-2512', name: 'glm-asr-2512', type: 'asr' }] },
      { value: 'generic', models: [] },
    ],
  })
  try {
    for (const [vendor, url, hint] of [
      ['openai', 'https://api.openai.com/v1', 'whisper-1'],
      ['zhipu', 'https://open.bigmodel.cn/api/paas/v4', 'glm-asr-2512'],
      ['generic', '', 'model.editor.modelNamePlaceholder.remoteAsr'],
    ]) {
      f.vm.formData.provider = vendor
      f.vm.handleProviderChange(vendor)
      assert.equal(f.vm.formData.baseUrl, url, vendor)
      assert.equal(f.vm.getModelNamePlaceholder(), hint, vendor)
    }
  } finally {
    f.close()
  }
})

test('ASR inherits the vendor chat base URL from older catalog responses', async () => {
  const f = await fixture({
    type: 'asr',
    providers: [
      { value: 'mimo', default_urls: { chat: 'https://api.xiaomimimo.com/v1' }, models: [{ id: 'mimo-v2.5-asr', name: 'mimo-v2.5-asr', type: 'asr' }] },
      { value: 'zhipu', default_urls: { chat: 'https://open.bigmodel.cn/api/paas/v4' }, models: [] },
      { value: 'custom', default_urls: { chat: 'https://chat.example.com', asr: 'https://speech.example.com' }, models: [] },
    ],
  })
  try {
    for (const [vendor, url] of [
      ['mimo', 'https://api.xiaomimimo.com/v1'],
      ['zhipu', 'https://open.bigmodel.cn/api/paas/v4'],
      ['custom', 'https://speech.example.com'],
    ]) {
      f.vm.formData.provider = vendor
      f.vm.handleProviderChange(vendor)
      assert.equal(f.vm.formData.baseUrl, url, vendor)
    }
  } finally {
    f.close()
  }
})

test('an explicitly empty type URL clears the old endpoint without inheriting the chat URL', async () => {
  const f = await fixture({
    type: 'asr',
    providers: [{ value: 'custom', default_urls: { chat: 'https://chat.example.com', asr: '' } }],
  })
  try {
    f.vm.formData.provider = 'custom'
    f.vm.handleProviderChange('custom')
    assert.equal(f.vm.formData.baseUrl, '')
  } finally {
    f.close()
  }
})

const vlmProviders: Array<Partial<CanonicalModelProviderOption> & { value: string }> = [{
  value: 'vendor-v',
  label: 'Vendor V',
  description: '',
  order: 1,
  model_types: ['chat', 'vllm'],
  default_urls: { chat: 'https://v.example.com/v1', vllm: 'https://v.example.com/v1' },
  models: [
    { id: 'sees-images', name: 'Sees Images', type: 'chat', input: ['text', 'image'], context_window: 128000 },
  ],
}]

test('the vision picker lists the vendor models the backend scoped to it', async () => {
  const f = await fixture({ type: 'vllm', providers: vlmProviders })
  try {
    f.vm.formData.provider = 'vendor-v'
    f.vm.handleProviderChange('vendor-v')
    await nextTick()
    assert.deepEqual(f.vm.catalogModelOptions.map((o) => o.value), ['sees-images'])
    assert.equal(f.vm.catalogModelOptions[0].vision, true)
  } finally {
    f.close()
  }
})

test('rerank and asr pickers list their own entries', async () => {
  for (const [type, id] of [['rerank', 'the-reranker'], ['asr', 'the-transcriber']]) {
    const providers = [{
      value: 'vendor-t',
      label: 'Vendor T',
      description: '',
      order: 1,
      model_types: [type],
      default_urls: { [type]: 'https://t.example.com/v1' },
      models: [{ id, name: id, type }],
    }]
    const f = await fixture({ type, providers })
    try {
      f.vm.formData.provider = 'vendor-t'
      f.vm.handleProviderChange('vendor-t')
      await nextTick()
      assert.deepEqual(f.vm.catalogModelOptions.map((o) => o.value), [id], type)
    } finally {
      f.close()
    }
  }
})

test('a vendor with no catalog falls back to a plain text field', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    assert.ok(f.vm.catalogModelOptions.length > 0, 'a catalogued vendor keeps the picker')

    const f2 = await fixture({
      providers: [{
        value: 'bare',
        label: 'Bare',
        description: '',
        order: 1,
        model_types: ['chat'],
        default_urls: { chat: 'https://bare.example.com/v1' },
        models: [],
      }],
    })
    try {
      f2.vm.formData.provider = 'bare'
      f2.vm.handleProviderChange('bare')
      await nextTick()
      assert.equal(f2.vm.catalogModelOptions.length, 0)
      f2.vm.formData.modelName = 'my-own-model'
      await nextTick()
      assert.equal(f2.vm.formData.modelName, 'my-own-model')
    } finally {
      f2.close()
    }
  } finally {
    f.close()
  }
})

test('every vendor-declared string in the form goes through a locale resolver', () => {
  const template = combinedEditorTemplates
  assert.ok(template.length > 0, 'the editor template should have been parsed')

  for (const pattern of [
    /:placeholder="field\.placeholder/,
    /\{\{\s*field\.placeholder\s*\}\}/,
    /v-if="field\.placeholder"/,
  ]) {
    assert.equal(
      pattern.test(template),
      false,
      `template reads field.placeholder directly (${pattern}); use extraFieldDisplayPlaceholder`,
    )
  }

  const optionLoop = /v-for="opt in \(field\.options \|\| \[\]\)"[\s\S]*?\/>/.exec(template)
  assert.ok(optionLoop, 'the extra-field select should loop over field.options')
  assert.match(
    optionLoop[0],
    /:label="extraFieldDisplayOptionLabel\(opt\)"/,
    'extra-field option labels must resolve the locale',
  )
  assert.ok(template.includes('extraFieldDisplayPlaceholder('), 'placeholders must use the resolver')
})

test('the chat protocol override is offered only on chat and vision rows', () => {
  const template = combinedEditorTemplates
  const select = /<div([^>]*)>\s*<label[^>]*>\{\{ \$t\('model\.editor\.advanced\.api\.label'\) \}\}/.exec(template)
  assert.ok(select, 'the protocol override should still be in the template')
  assert.match(select[1], /v-if="isChatLike"/, 'the protocol override must be scoped to chat-like rows')
})

test('extraFieldDisplayPlaceholder resolves the vendor placeholder for the active locale', async () => {
  const f = await fixture({ type: 'rerank', providers: catalogProviders })
  try {
    const field = {
      key: 'score_scale',
      label: 'Rerank score scale',
      type: 'select',
      placeholder: 'match the reranker actually deployed behind this endpoint',
      placeholders: { 'zh-CN': '按这个端点后面实际部署的重排模型选择' },
    }
    const rendered = f.vm.extraFieldDisplayPlaceholder(field)
    assert.ok(
      rendered === field.placeholder || rendered === field.placeholders['zh-CN'],
      `expected one of the declared variants, got ${rendered}`,
    )
    assert.equal(f.vm.extraFieldDisplayPlaceholder({ key: 'k', label: 'k', type: 'string' }), '')
  } finally {
    f.close()
  }
})

for (const type of ['chat', 'embedding', 'rerank', 'vllm', 'asr']) {
  test(`${type}: connection tests include edited spec and clearing compat`, async () => {
    const f = await fixture({ type })
    try {
      f.vm.formData.spec = { context_window: 64000, compat: { old: true } }
      f.vm.formData.specCompat = '{"custom_option": true}'
      await f.vm.checkRemoteAPI()
      assert.deepEqual(plain(f.requests[0].spec), { context_window: 64000, compat: { custom_option: true } })
      f.vm.formData.specCompat = ''
      await nextTick()
      assert.equal(f.vm.remoteChecked, false, 'editing spec invalidates the old connection result')
      await f.vm.checkRemoteAPI()
      assert.deepEqual(plain(f.requests[1].spec), { context_window: 64000 })
    } finally {
      f.close()
    }
  })
}

test('capability preview includes the same edited spec as connection tests', async () => {
  const f = await fixture({ type: 'chat' })
  try {
    f.vm.formData.provider = 'openai'
    f.vm.formData.spec = { api: 'openai-completions' }
    f.vm.formData.specCompat = '{"supports_temperature": false}'
    await f.vm.runResolve()
    assert.deepEqual(plain(f.resolves.at(-1)?.spec), { api: 'openai-completions', compat: { supports_temperature: false } })
  } finally {
    f.close()
  }
})

test('opening the model editor refreshes its catalog candidates', async () => {
  const f = await fixture()
  try {
    assert.equal(f.catalogEvents[0], 'refresh')
    f.props.visible = false
    await nextTick()
    f.catalogEvents.length = 0
    f.props.visible = true
    await nextTick()
    assert.equal(f.catalogEvents[0], 'refresh')
  } finally {
    f.close()
  }
})
