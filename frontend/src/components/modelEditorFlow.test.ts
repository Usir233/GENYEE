import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { nextTick } from 'vue'
import { deferred, fixture, type CheckResult } from './modelEditorTestHarness.ts'

for (const initiallyVisible of [true, false]) {
  for (const type of ['chat', 'embedding']) {
    test(`${type}: assistant draft is filled ${initiallyVisible ? 'on first navigation with the dialog already open' : 'when opening an existing settings page'}`, async () => {
      const draft: Record<string, string> = {
        source: 'remote',
        provider: 'vendor',
        modelName: `${type}-model`,
        displayName: 'Assistant suggestion',
        baseUrl: 'https://draft.example.com/v1',
      }
      const f = await fixture({
        type,
        initiallyVisible,
        draft,
        providersByType: {
          [type]: [{
            value: 'vendor',
            label: 'Vendor',
            model_types: [type],
            models: [{ id: `${type}-model`, name: `${type}-model`, type }],
          }],
        },
      })
      try {
        assert.equal(f.vm.activeModelType, type)
        for (const [key, value] of Object.entries(draft)) {
          assert.equal(f.vm.formData[key], value, `${key} is prefilled without user input`)
        }
        assert.deepEqual(f.catalogRequests, [{ type, force: true }])
        assert.deepEqual(Array.from(f.vm.providerOptions, (p) => p.value), ['vendor'])
        assert.deepEqual(Array.from(f.vm.catalogModelOptions, (m) => m.value), [`${type}-model`])
        assert.equal(f.vm.formData.apiKey, '')
        assert.equal(f.vm.formData.appSecret, '')
        assert.equal(f.requests.length, 0, 'prefilling does not test the connection')
        assert.equal(f.saves.length, 0, 'prefilling does not save the model')
      } finally {
        f.close()
      }
    })
  }
}

for (const type of ['embedding', 'rerank', 'vllm', 'asr']) {
  test(`${type}: opening from its settings tab loads the matching providers and models`, async () => {
    const provider = {
      value: 'vendor',
      label: 'Vendor',
      model_types: [type],
      models: [{ id: `${type}-model`, name: `${type}-model`, type: type === 'vllm' ? 'chat' : type }],
    }
    const f = await fixture({ initialType: 'chat', type, providersByType: { [type]: [provider] } })
    try {
      assert.deepEqual(f.catalogRequests, [{ type, force: true }])
      assert.equal(f.vm.activeModelType, type)
      assert.deepEqual(Array.from(f.vm.providerOptions, (p) => p.value), ['vendor'])
      f.vm.formData.provider = 'vendor'
      f.vm.formData.modelName = ''
      assert.deepEqual(Array.from(f.vm.catalogModelOptions, (m) => m.value), [`${type}-model`])
    } finally {
      f.close()
    }
  })
}

test('reopening after changing the draft type refreshes the newly selected settings tab', async () => {
  const f = await fixture()
  try {
    await f.vm.selectModelType('rerank')
    f.props.visible = false
    await nextTick()
    f.catalogRequests.length = 0
    f.props.modelType = 'embedding'
    f.props.visible = true
    await nextTick()
    assert.deepEqual(f.catalogRequests, [{ type: 'embedding', force: true }])
    assert.equal(f.vm.activeModelType, 'embedding')
  } finally {
    f.close()
  }
})

for (const type of ['chat', 'embedding', 'rerank', 'vllm', 'asr']) {
  test(`${type}: changing type resets the whole connection form and its results`, async () => {
    const f = await fixture({
      type: type === 'chat' ? 'rerank' : 'chat',
      providers: [{
        value: 'vendor',
        model_types: ['chat', 'embedding', 'rerank', 'vllm', 'asr'],
        models: [],
      }],
    })
    try {
      Object.assign(f.vm.formData, {
        source: 'local',
        provider: 'vendor',
        modelName: 'old-model',
        displayName: 'Old display',
        baseUrl: 'https://example.com/rerank',
        apiKey: 'old-key',
        appSecret: 'old-secret',
        customHeaders: [{ key: 'X-Old', value: 'old' }],
        extraConfig: { api: 'old-api', remote_model_name: 'old-name' },
        dimension: 1024,
        supportsDimensionOverride: true,
        supportsVision: true,
        interfaceType: 'ollama',
        contextWindow: 64000,
        maxOutputTokens: 4096,
        maxConcurrency: 8,
        isDefault: true,
        thinkingControl: 'old-thinking',
        spec: { context_window: 64000 },
        specCompat: '{"old":true}',
      })
      await nextTick()
      f.vm.remoteChecked = true
      f.vm.remoteAvailable = true
      f.vm.remoteMessage = 'old result'
      f.vm.dimensionChecked = true
      f.vm.dimensionSuccess = true
      f.vm.dimensionMessage = 'old dimension'
      f.vm.modelChecked = true
      f.vm.modelAvailable = true
      f.vm.catalogFilled = { contextWindow: 64000 }
      f.vm.advancedOpen = true
      f.vm.saveError = 'old error'
      let cleared = 0
      f.vm.formRef = { clearValidate: () => { cleared++ } }

      await f.vm.selectModelType(type)
      await nextTick()
      assert.equal(f.vm.activeModelType, type)
      assert.equal(f.vm.formData.source, 'remote')
      assert.equal(f.vm.formData.provider, 'generic')
      for (const field of ['modelName', 'displayName', 'baseUrl', 'apiKey', 'appSecret', 'thinkingControl', 'specCompat']) {
        assert.equal(f.vm.formData[field], '', field)
      }
      for (const field of ['dimension', 'interfaceType', 'contextWindow', 'maxOutputTokens', 'maxConcurrency']) {
        assert.equal(f.vm.formData[field], undefined, field)
      }
      for (const field of ['supportsDimensionOverride', 'supportsVision', 'isDefault']) {
        assert.equal(f.vm.formData[field], false, field)
      }
      assert.equal(f.vm.formData.spec, null)
      assert.equal(f.vm.formData.customHeaders?.length ?? 0, 0)
      assert.equal(Object.keys(f.vm.formData.extraConfig).length, 0)
      assert.equal(Object.keys(f.vm.catalogFilled).length, 0)
      for (const field of ['remoteChecked', 'remoteAvailable', 'remoteStale', 'dimensionChecked', 'dimensionSuccess', 'modelChecked', 'modelAvailable', 'advancedOpen']) {
        assert.equal(f.vm[field], false, field)
      }
      for (const field of ['remoteMessage', 'dimensionMessage', 'saveError']) {
        assert.equal(f.vm[field], '', field)
      }
      assert.ok(cleared > 0)
    } finally {
      f.close()
    }
  })
}

test('a delayed type catalog cannot rewrite a newer connection draft', async () => {
  const pending = deferred<void>()
  const f = await fixture({
    loadProviders: (type) => (type === 'embedding' ? pending.promise : Promise.resolve()),
    providersByType: { asr: [{ value: 'vendor', default_urls: { asr: 'https://default.example.com' }, models: [] }] },
  })
  try {
    const earlier = f.vm.selectModelType('embedding')
    assert.equal(f.vm.formData.apiKey, '', 'reset must not wait for the catalog')
    await f.vm.selectModelType('asr')
    f.vm.formData.provider = 'vendor'
    f.vm.formData.baseUrl = 'https://custom.example.com'
    f.vm.formData.extraConfig = { region: 'custom' }
    pending.resolve()
    await earlier
    assert.equal(f.vm.activeModelType, 'asr')
    assert.equal(f.vm.formData.baseUrl, 'https://custom.example.com')
    assert.equal(f.vm.formData.extraConfig.region, 'custom')
  } finally {
    pending.resolve()
    f.close()
  }
})

test('same-type reopening keeps the draft, but another settings tab starts a fresh form', async () => {
  const f = await fixture()
  try {
    f.props.visible = false
    await nextTick()
    f.props.visible = true
    await nextTick()
    assert.equal(f.vm.formData.modelName, 'draft-model')
    f.props.visible = false
    await nextTick()
    f.props.modelType = 'embedding'
    f.props.visible = true
    await nextTick()
    assert.equal(f.vm.formData.modelName, '')
    assert.equal(f.vm.formData.apiKey, '')
    assert.equal(f.vm.formData.baseUrl, '')
  } finally {
    f.close()
  }
})

test('type reset discards pending connection and catalog diagnosis results', async () => {
  const connection = deferred<CheckResult>()
  const diagnosis = deferred<{ cataloged: boolean; model: Record<string, unknown> }>()
  const f = await fixture({ check: () => connection.promise, resolve: () => diagnosis.promise })
  try {
    const checking = f.vm.checkRemoteAPI()
    const resolving = f.vm.runResolve()
    await f.vm.selectModelType('asr')
    connection.resolve({ available: true })
    diagnosis.resolve({ cataloged: true, model: { id: 'old-model' } })
    await Promise.all([checking, resolving])
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteStale, false)
    assert.equal(f.vm.checking, false)
    assert.equal(f.vm.resolved, null)
    assert.equal(f.vm.resolving, false)
  } finally {
    f.close()
  }
})

test('selecting the current type, editing, or saving does not reset the draft', async () => {
  for (const state of ['same-type', 'editing', 'saving']) {
    const f = await fixture({ edit: state === 'editing' })
    try {
      const original = JSON.stringify(f.vm.formData)
      const requests = f.catalogRequests.length
      if (state === 'saving') f.vm.saving = true
      await f.vm.selectModelType(state === 'same-type' ? 'chat' : 'asr')
      assert.equal(JSON.stringify(f.vm.formData), original, state)
      assert.equal(f.catalogRequests.length, requests, state)
    } finally {
      f.close()
    }
  }
})

for (const type of ['chat', 'embedding', 'rerank', 'vllm', 'asr']) {
  test(`${type}: connection tests use unsaved form values without saving`, async () => {
    const f = await fixture({ type })
    try {
      f.vm.formData.customHeaders = [{ key: ' X-Test ', value: ' draft ' }]
      await f.vm.checkRemoteAPI()
      assert.equal(f.requests[0].modelName, 'draft-model')
      assert.equal(f.requests[0].apiKey, 'draft-key')
      const headers = f.requests[0].customHeaders as Record<string, string>
      assert.equal(headers['X-Test'], 'draft')
      assert.equal(f.saves.length, 0)
      assert.equal(f.props.visible, true)
      assert.equal(f.vm.remoteAvailable, true)
    } finally {
      f.close()
    }
  })
}

test('editing keeps the saved-key fallback and invalidates results after credential changes', async () => {
  const f = await fixture({ edit: true })
  try {
    await f.vm.checkRemoteAPI()
    assert.equal(f.requests[0].modelId, 'saved-model')
    assert.equal(f.requests[0].apiKey, '')
    f.vm.invalidateConnectionTest()
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteStale, true)
  } finally {
    f.close()
  }
})

test('long backend errors remain complete inline without duplicate error toasts', async () => {
  const message = 'Upstream rejected request\n' + 'diagnostic '.repeat(1000)
  for (const rejects of [false, true]) {
    const f = await fixture({
      check: async () => {
        if (rejects) throw new Error(message)
        return { available: false, message }
      },
    })
    try {
      await f.vm.checkRemoteAPI()
      assert.equal(f.vm.remoteChecked, true)
      assert.equal(f.vm.remoteAvailable, false)
      assert.equal(f.vm.remoteMessage, message)
      assert.deepEqual(f.toasts, [])
    } finally {
      f.close()
    }
  }
})

test('connection edits invalidate results; display-only edits preserve them', async () => {
  const f = await fixture()
  try {
    await f.vm.checkRemoteAPI()
    f.vm.formData.displayName = 'Display only'
    assert.equal(f.vm.remoteChecked, true)
    for (const patch of [
      { modelName: 'other' },
      { baseUrl: 'https://other.example/v1' },
      { apiKey: 'another-key' },
      { customHeaders: [{ key: 'X-Test', value: 'one' }] },
      { dimension: 1024 },
      { supportsDimensionOverride: true },
    ]) {
      Object.assign(f.vm.formData, patch)
      assert.equal(f.vm.remoteChecked, false)
      assert.equal(f.vm.remoteStale, true)
      await f.vm.checkRemoteAPI()
    }
    f.vm.formData.customHeaders![0].value = 'two'
    assert.equal(f.vm.remoteChecked, false, 'nested header edits invalidate immediately')
  } finally {
    f.close()
  }
})

test('late responses cannot overwrite newer results, including after reverting a field', async () => {
  const old = deferred<CheckResult>()
  let calls = 0
  const f = await fixture({
    check: () => (++calls === 1 ? old.promise : Promise.resolve({ available: false, message: 'current failure' })),
  })
  try {
    const pending = f.vm.checkRemoteAPI()
    f.vm.formData.modelName = 'changed'
    f.vm.formData.modelName = 'draft-model'
    assert.equal(f.vm.checking, false)
    await f.vm.checkRemoteAPI()
    old.resolve({ available: true })
    await pending
    assert.equal(f.vm.remoteAvailable, false)
    assert.equal(f.vm.remoteMessage, 'current failure')
  } finally {
    f.close()
  }
})

test('a stale failure does not clear the loading state of a newer test', async () => {
  const old = deferred<CheckResult>()
  const current = deferred<CheckResult>()
  let calls = 0
  const f = await fixture({ check: () => (++calls === 1 ? old.promise : current.promise) })
  try {
    const pending = f.vm.checkRemoteAPI()
    f.vm.formData.baseUrl = 'https://changed.example/v1'
    const newer = f.vm.checkRemoteAPI()
    old.reject(new Error('old failure'))
    await pending
    assert.equal(f.vm.checking, true)
    assert.equal(f.vm.remoteChecked, false)
    current.resolve({ available: true })
    await newer
    assert.equal(f.vm.remoteAvailable, true)
  } finally {
    f.close()
  }
})

test('closing and reopening the editor discards pending test responses', async () => {
  const response = deferred<CheckResult>()
  const f = await fixture({ check: () => response.promise })
  try {
    const pending = f.vm.checkRemoteAPI()
    f.props.visible = false
    await nextTick()
    f.props.visible = true
    await nextTick()
    response.resolve({ available: true })
    await pending
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteStale, false)
  } finally {
    f.close()
  }
})

for (const source of ['remote', 'local'] as const) {
  test(`${source} embedding: detect dimensions without invalidating success or overwriting newer input`, async () => {
    const response = deferred<CheckResult>()
    const f = await fixture({ type: 'embedding', check: () => response.promise })
    try {
      f.vm.formData.source = source
      await nextTick()
      const run = () => (source === 'remote' ? f.vm.checkRemoteAPI() : f.vm.checkOllamaDimension())
      const pending = run()
      f.vm.formData.dimension = 2048
      response.resolve({ available: true, dimension: 1024 })
      await pending
      assert.equal(f.vm.formData.dimension, 2048)
      await run()
      assert.equal(f.vm.formData.dimension, 1024)
      assert.equal(source === 'remote' ? f.vm.remoteAvailable : f.vm.dimensionSuccess, true)
      assert.equal(f.vm.checking, false)
    } finally {
      f.close()
    }
  })
}

test('save waits for success, blocks duplicate saves and cancellation, then closes and clears the draft', async () => {
  const response = deferred<void>()
  const f = await fixture({ save: () => response.promise })
  try {
    const pending = f.vm.handleConfirm()
    await nextTick()
    assert.equal(f.vm.saving, true)
    assert.equal(f.props.visible, true)
    assert.equal(f.vm.formData.modelName, 'draft-model')
    await f.vm.handleConfirm()
    f.vm.handleCancel()
    f.vm.dialogVisible = false
    assert.equal(f.saves.length, 1)
    assert.equal(f.visibility.length, 0)
    let stopped = false
    f.vm.handleSaveEscape({ key: 'Escape', preventDefault() {}, stopImmediatePropagation() { stopped = true } })
    assert.equal(stopped, true, 'Escape must not bubble to the outer Settings modal while saving')
    response.resolve()
    await pending
    assert.equal(f.props.visible, false)
    assert.equal(f.vm.formData.modelName, '')
    assert.equal(f.vm.saving, false)
  } finally {
    f.close()
  }
})

test('failed saves retain draft and drawer, show the error and allow a successful retry', async () => {
  let calls = 0
  const f = await fixture({ save: async () => { if (++calls === 1) throw new Error('save rejected') } })
  try {
    await f.vm.handleConfirm()
    assert.equal(f.props.visible, true)
    assert.equal(f.vm.formData.modelName, 'draft-model')
    assert.equal(f.vm.formData.apiKey, 'draft-key')
    assert.equal(f.vm.saveError, 'save rejected')
    assert.equal(f.vm.saving, false)
    await f.vm.handleConfirm()
    assert.equal(f.props.visible, false)
  } finally {
    f.close()
  }
})

test('failed form validation prevents persistence and leaves the drawer open', async () => {
  const f = await fixture()
  try {
    f.vm.formRef = { validate: async () => ({ modelName: [{ result: false }] }) }
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 0)
    assert.equal(f.props.visible, true)
    assert.equal(f.vm.saving, false)
  } finally {
    f.close()
  }
})

test('failed connection test does not prevent saving', async () => {
  const f = await fixture({ check: async () => ({ available: false, message: 'offline' }) })
  try {
    await f.vm.checkRemoteAPI()
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.equal(f.props.visible, false)
  } finally {
    f.close()
  }
})

const settingsSource = readFileSync(new URL('../views/settings/ModelSettings.vue', import.meta.url), 'utf8')
const saveStart = settingsSource.indexOf('const handleModelSave =')
const saveEnd = settingsSource.indexOf('// 删除模型', saveStart)
const saveScript = ts.transpileModule(settingsSource.slice(saveStart, saveEnd), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

for (const edit of [false, true]) {
  test(`${edit ? 'update' : 'create'}: parent propagates API and validation failures to the editor`, async () => {
    let calls = 0
    const api = async () => {
      calls++
      throw new Error('API rejected')
    }
    const save = runInNewContext(saveScript + '\nhandleModelSave', {
      URL,
      console: { error() {} },
      currentModelType: { value: 'chat' },
      editingModel: { value: edit ? { id: 'saved-model' } : null },
      getModelType: () => 'KnowledgeQA',
      t: (key: string) => key,
      createModel: api,
      updateModelAPI: api,
      MessagePlugin: { success() {}, error() {}, warning() {} },
      loadModels: async () => {},
    }) as (payload: Record<string, unknown>) => Promise<void>
    const valid = { modelName: 'model', source: 'remote', baseUrl: 'https://example.com/v1' }
    await assert.rejects(save(valid), /API rejected/)
    assert.equal(calls, 1)
    await assert.rejects(save({ ...valid, displayName: 'x'.repeat(101) }), /displayNameTooLong/)
    await assert.rejects(save({ ...valid, modelType: 'embedding', dimension: 0 }), /dimensionInvalid/)
    assert.equal(calls, 1, 'invalid drafts must not reach persistence')
  })
}
