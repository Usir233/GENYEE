import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import axios, { type InternalAxiosRequestConfig } from 'axios'
import ts from 'typescript'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}
const flush = () => new Promise<void>(resolve => setImmediate(resolve))
type RefreshResult = { success: boolean; data?: { token: string; refreshToken: string }; message?: string }

/** Production Axios interceptors and refresh state machine; only HTTP/browser storage are fake. */
function fixture(refreshResult?: () => Promise<RefreshResult>) {
  const storage = new Map([
    ['genyee_token', 'access-A'], ['genyee_refresh_token', 'refresh-A'], ['genyee_selected_tenant_id', '1'],
  ])
  const localStorage = {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => { storage.set(key, value) },
    removeItem: (key: string) => { storage.delete(key) },
  }
  const location = { pathname: '/platform/settings', href: '/platform/settings' }
  function evaluate(path: string, dependencies: Record<string, unknown>) {
    const exports: any = {}
    runInNewContext(ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText, {
      exports, console, Blob, localStorage, window: { location },
      require(name: string) {
        assert.ok(name in dependencies, `Unexpected dependency: ${name}`)
        return dependencies[name]
      },
    })
    return exports
  }
  const tokens = evaluate('./authTokens.ts', {})
  const refreshed: string[] = []
  let protectedCacheClears = 0
  const refresh = evaluate('./authRefresh.ts', {
    './api-base': { getApiBaseUrl: () => '' }, './authTokens': tokens,
    './security': {
      clearProtectedFileCache: () => { protectedCacheClears += 1 },
      setProtectedFileCacheScope: () => {},
    },
    '../api/auth/index': {
      refreshToken: async (token: string) => {
        refreshed.push(token)
        return refreshResult ? refreshResult() : {
          success: true, data: { token: 'access-fresh', refreshToken: 'refresh-rotated' },
        }
      },
    },
  })
  const request = evaluate('./request.ts', {
    axios,
    './index': { generateRandomString: () => 'request-id', MAX_FILE_SIZE_MB: 50, MAX_SKILL_BUNDLE_SIZE_MB: 50 },
    '@/i18n': { global: { t: (key: string) => key, locale: { value: 'en-US' } } },
    './api-base': { getApiBaseUrl: () => '' }, './uploadLimit': {}, './requestTimeouts': evaluate('./requestTimeouts.ts', {}), './authRefresh': refresh,
  })
  const calls: Array<{ token: string; tenant: string; body: string }> = []
  const responses: Array<ReturnType<typeof deferred<number>>> = []
  const send = (signal?: AbortSignal) => request.put('/api/v1/tenants/kv/storage-engine-config', {
    default_provider: 'local', local: { path_prefix: 'account-a-files' },
  }, {
    signal,
    adapter: async (config: InternalAxiosRequestConfig) => {
      calls.push({ token: String(config.headers.Authorization), tenant: String(config.headers['X-Tenant-ID']), body: config.data })
      const response = deferred<number>()
      responses.push(response)
      const status = await response.promise
      const result = { config, status, statusText: String(status), headers: {}, data: { success: status === 200 } }
      if (status !== 200) throw new axios.AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, undefined, result)
      return result
    },
  }) as Promise<unknown>
  return { storage, refresh, refreshed, send, calls, responses, assertUntouched: () => assert.equal(location.href, '/platform/settings') }
}
const isSessionChanged = (error: any) => error?.name === 'AuthSessionChangedError'

test('an authenticated mutation refreshes and retries once in the same account and tenant', async () => {
  const f = fixture(), result = f.send()
  await flush()
  f.responses[0].resolve(401)
  await flush()
  assert.deepEqual(f.refreshed, ['refresh-A'])
  assert.equal(f.calls[1].token, 'Bearer access-fresh')
  assert.equal(f.calls[1].tenant, '1')
  assert.equal(f.calls[1].body, f.calls[0].body)
  f.responses[1].resolve(200)
  await result
  f.assertUntouched()
})

for (const explicitLogout of [false, true]) {
  test(`a delayed 401 cannot replay an old mutation as another login (${explicitLogout ? 'logout' : 'another tab'})`, async () => {
    const f = fixture(), result = f.send()
    const rejected = assert.rejects(result, isSessionChanged)
    await flush()
    if (explicitLogout) f.refresh.invalidateAuthRefresh()
    f.storage.set('genyee_token', 'access-B')
    f.storage.set('genyee_refresh_token', 'refresh-B')
    f.storage.set('genyee_selected_tenant_id', '2')
    f.responses[0].resolve(401)
    await flush()
    // Settle a buggy retry too so the regression fails instead of hanging.
    f.responses[1]?.resolve(200)
    await rejected
    assert.equal(f.calls.length, 1)
    assert.equal(f.refreshed.length, 0)
    assert.equal(f.storage.get('genyee_token'), 'access-B')
    f.assertUntouched()
  })
}

test('logout invalidates a delayed request even if the same token pair is restored', async () => {
  const f = fixture(), result = f.send()
  const rejected = assert.rejects(result, isSessionChanged)
  await flush()
  f.refresh.invalidateAuthRefresh()
  f.responses[0].resolve(401)
  await flush()
  f.responses[1]?.resolve(200)
  await rejected
  assert.equal(f.calls.length, 1)
  assert.equal(f.refreshed.length, 0)
})

test('a tenant switch before the 401 prevents retrying the previous tenant mutation', async () => {
  const f = fixture(), result = f.send()
  const rejected = assert.rejects(result, isSessionChanged)
  await flush()
  f.storage.set('genyee_selected_tenant_id', '2')
  f.responses[0].resolve(401)
  await flush()
  f.responses[1]?.resolve(200)
  await rejected
  assert.equal(f.calls.length, 1)
  assert.equal(f.refreshed.length, 0)
  assert.equal(f.storage.get('genyee_token'), 'access-A')
})

test('a tenant switch while refresh is pending prevents retry without clearing the valid login', async () => {
  const rotation = deferred<RefreshResult>(), f = fixture(() => rotation.promise), result = f.send()
  const rejected = assert.rejects(result, isSessionChanged)
  await flush()
  f.responses[0].resolve(401)
  await flush()
  f.storage.set('genyee_selected_tenant_id', '2')
  rotation.resolve({ success: true, data: { token: 'access-fresh', refreshToken: 'refresh-rotated' } })
  await flush()
  f.responses[1]?.resolve(200)
  await rejected
  assert.equal(f.calls.length, 1)
  assert.equal(f.storage.get('genyee_token'), 'access-fresh')
  assert.equal(f.storage.get('genyee_selected_tenant_id'), '2')
  f.assertUntouched()
})

test('concurrent 401s share one refresh and retry within their original context', async () => {
  const rotation = deferred<RefreshResult>(), f = fixture(() => rotation.promise)
  const first = f.send(), second = f.send()
  await flush()
  f.responses[0].resolve(401); f.responses[1].resolve(401)
  await flush()
  assert.equal(f.refreshed.length, 1)
  rotation.resolve({ success: true, data: { token: 'access-fresh', refreshToken: 'refresh-rotated' } })
  await flush()
  assert.equal(f.calls.length, 4)
  for (const call of f.calls.slice(2)) {
    assert.equal(call.token, 'Bearer access-fresh')
    assert.equal(call.tenant, '1')
  }
  f.responses[2].resolve(200); f.responses[3].resolve(200)
  await Promise.all([first, second])
})

test('a later 401 from the same session reuses the completed token rotation', async () => {
  const f = fixture(), first = f.send(), second = f.send()
  await flush()
  f.responses[0].resolve(401)
  await flush()
  f.responses[2].resolve(200)
  await first
  f.responses[1].resolve(401)
  await flush()
  f.responses[3]?.resolve(200)
  await second
  assert.equal(f.refreshed.length, 1, 'the stale token was already rotated by the first request')
  assert.equal(f.calls[3].token, 'Bearer access-fresh')
})

test('aborting before a delayed 401 preserves cancellation and does not refresh', async () => {
  const f = fixture(), controller = new AbortController(), result = f.send(controller.signal)
  const rejected = assert.rejects(result, error => axios.isCancel(error))
  await flush()
  controller.abort()
  f.responses[0].resolve(401)
  await rejected
  assert.equal(f.calls.length, 1)
  assert.equal(f.refreshed.length, 0)
})

test('aborting one refresh waiter settles promptly without cancelling another waiter', async () => {
  const rotation = deferred<RefreshResult>(), f = fixture(() => rotation.promise), controller = new AbortController()
  const first = f.send(controller.signal), second = f.send()
  let aborted = false
  const rejected = assert.rejects(first, error => axios.isCancel(error)).then(() => { aborted = true })
  void rejected.catch(() => {})
  await flush()
  f.responses[0].resolve(401); f.responses[1].resolve(401)
  await flush()
  controller.abort()
  await flush()
  const settledBeforeRotation = aborted
  rotation.resolve({ success: true, data: { token: 'access-fresh', refreshToken: 'refresh-rotated' } })
  await flush()
  f.responses[2]?.resolve(200)
  await Promise.all([rejected, second])
  assert.equal(settledBeforeRotation, true, 'cancelled waiter must not await the shared HTTP refresh')
  assert.equal(f.refreshed.length, 1)
  assert.equal(f.calls.length, 3)
})
