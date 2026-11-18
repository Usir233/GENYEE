import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { subscribeAuthTokens } from './authTokens.ts'

import {
  AuthSessionChangedError,
  StreamAuthError,
  captureAuthRequestContext,
  clearAuthStorage,
  forceReloginRedirect,
  isAuthSessionChangedError,
  isStreamAuthError,
  refreshAccessTokenShared,
  resetAuthRefreshStateForTests,
  runStreamWithAuthRetry,
  type TokenRefreshResult,
} from './authRefresh.ts'
import { resolveProtectedFileCacheScope, setProtectedFileCacheScope } from './security.ts'

type Store = Record<string, string>

function installBrowser(pathname = '/chat') {
  const store: Store = {}
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: (key: string) => (key in store ? store[key] : null),
      setItem: (key: string, value: string) => {
        store[key] = String(value)
      },
      removeItem: (key: string) => {
        delete store[key]
      },
    },
    configurable: true,
    writable: true,
  })
  const location = { pathname, href: `http://localhost${pathname}` }
  Object.defineProperty(globalThis, 'window', {
    value: { location },
    configurable: true,
    writable: true,
  })
  return { store, location }
}

test.afterEach(() => {
  resetAuthRefreshStateForTests()
})

test('isStreamAuthError matches the class and name-only wrappers', () => {
  assert.equal(isStreamAuthError(new StreamAuthError(401)), true)
  assert.equal(isStreamAuthError({ name: 'StreamAuthError', message: 'HTTP 401' }), true)
  assert.equal(isStreamAuthError(new Error('HTTP 401')), false)
  assert.equal(isStreamAuthError('HTTP 401'), false)
})

test('concurrent 401s share a single refresh call', async () => {
  const { store } = installBrowser()
  store.genyee_refresh_token = 'rt-1'
  let calls = 0
  let release!: (value: { success: true; data: { token: string; refreshToken: string } }) => void
  const pending = new Promise<{ success: true; data: { token: string; refreshToken: string } }>(
    (resolve) => {
      release = resolve
    },
  )
  const refresh = async () => {
    calls += 1
    return pending
  }

  const first = refreshAccessTokenShared({ refresh })
  await Promise.resolve()
  const second = refreshAccessTokenShared({ refresh })
  assert.equal(calls, 1)

  release({ success: true, data: { token: 'access-2', refreshToken: 'rt-2' } })
  assert.deepEqual(await Promise.all([first, second]), ['access-2', 'access-2'])
  assert.equal(store.genyee_token, 'access-2')
  assert.equal(store.genyee_refresh_token, 'rt-2')
  assert.equal(calls, 1)
})

test('refresh publishes the rotated pair and a failed session publishes cleared tokens', async () => {
  const { store } = installBrowser()
  store.genyee_token = 'expired'
  store.genyee_refresh_token = 'refresh-1'
  store.__genyee_token_sync_installed = 'true'
  const received: Array<{ token: string; refreshToken: string }> = []
  const unsubscribe = subscribeAuthTokens(tokens => received.push(tokens))
  try {
    await refreshAccessTokenShared({
      refresh: async () => ({ success: true, data: { token: 'access-2', refreshToken: 'refresh-2' } }),
    })
    assert.deepEqual(received, [{ token: 'access-2', refreshToken: 'refresh-2' }])
    await assert.rejects(refreshAccessTokenShared({ refresh: async () => ({ success: false }) }))
    assert.deepEqual(received.at(-1), { token: '', refreshToken: '' })
  } finally {
    unsubscribe()
  }
})

test('missing refresh token clears credentials, redirects, and throws an Error', async () => {
  const { store, location } = installBrowser()
  store.genyee_token = 'expired'
  store.genyee_user = '{}'
  store.genyee_selected_tenant_id = '9'

  await assert.rejects(
    () => refreshAccessTokenShared({
      refresh: async () => {
        throw new Error('should not refresh')
      },
      messages: { pleaseRelogin: 'please-relogin', tokenRefreshFailed: 'refresh-failed' },
    }),
    (err: unknown) => err instanceof Error && err.message === 'please-relogin',
  )
  assert.equal(store.genyee_token, undefined)
  assert.equal(store.genyee_selected_tenant_id, undefined)
  assert.equal(location.href, '/login')
})

test('a failed refresh clears the newly irrelevant session and redirects', async () => {
  const { store, location } = installBrowser()
  store.genyee_refresh_token = 'rt-dead'
  store.genyee_token = 'expired'
  store.genyee_selected_tenant_id = '9'

  await assert.rejects(
    () => refreshAccessTokenShared({
      refresh: async () => ({ success: false, message: 'revoked' }),
    }),
    /revoked/,
  )
  assert.equal(store.genyee_refresh_token, undefined)
  assert.equal(store.genyee_selected_tenant_id, undefined)
  assert.equal(location.href, '/login')
})

async function settleRefreshes(promises: Promise<string>[]) {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    return await Promise.race([
      Promise.allSettled(promises),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error('refresh callers did not settle')), 1000)
      }),
    ])
  } finally {
    clearTimeout(timer)
  }
}

function deferredRefresh() {
  let resolve!: (result: TokenRefreshResult) => void
  let reject!: (error: Error) => void
  const promise = new Promise<TokenRefreshResult>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function assertSessionChanged(result: PromiseSettledResult<string>) {
  assert.equal(result.status, 'rejected')
  if (result.status === 'rejected') {
    assert.equal(result.reason.name, 'AuthSessionChangedError')
  }
}

for (const outcome of ['success', 'failure'] as const) {
  test(`stale refresh ${outcome} leaves a replacement session intact`, async () => {
    const { store, location } = installBrowser()
    store.genyee_token = 'A-expired'
    store.genyee_refresh_token = 'A-refresh'
    const old = deferredRefresh()
    const settled = settleRefreshes([
      refreshAccessTokenShared({ refresh: () => old.promise }),
      refreshAccessTokenShared({ refresh: () => old.promise }),
    ])
    await Promise.resolve()

    // Another tab replaces localStorage without calling this module's logout hook.
    store.genyee_token = 'B-access'
    store.genyee_refresh_token = 'B-refresh'
    store.genyee_user = '{"id":"B"}'
    if (outcome === 'success') {
      old.resolve({ success: true, data: { token: 'A-new', refreshToken: 'A-new-refresh' } })
    } else {
      old.reject(new Error('old session revoked'))
    }

    for (const result of await settled) assertSessionChanged(result)
    assert.equal(store.genyee_token, 'B-access')
    assert.equal(store.genyee_refresh_token, 'B-refresh')
    assert.equal(store.genyee_user, '{"id":"B"}')
    assert.equal(location.href, 'http://localhost/chat')
  })
}

test('replacement-session callers share their own refresh even after the old one finishes', async () => {
  const { store } = installBrowser()
  store.genyee_token = 'A-expired'
  store.genyee_refresh_token = 'A-refresh'
  const old = deferredRefresh()
  const oldSettled = settleRefreshes([
    refreshAccessTokenShared({ refresh: () => old.promise }),
    refreshAccessTokenShared({ refresh: () => old.promise }),
  ])
  await Promise.resolve()

  store.genyee_token = 'B-expired'
  store.genyee_refresh_token = 'B-refresh'
  const current = deferredRefresh()
  let calls = 0
  const refresh = async (refreshToken: string) => {
    assert.equal(refreshToken, 'B-refresh')
    calls += 1
    return current.promise
  }
  const first = refreshAccessTokenShared({ refresh })
  const second = refreshAccessTokenShared({ refresh })
  await Promise.resolve()
  old.resolve({ success: true, data: { token: 'A-new', refreshToken: 'A-new-refresh' } })
  for (const result of await oldSettled) assertSessionChanged(result)

  const third = refreshAccessTokenShared({ refresh })
  current.resolve({ success: true, data: { token: 'B-new', refreshToken: 'B-new-refresh' } })
  assert.deepEqual(await Promise.all([first, second, third]), ['B-new', 'B-new', 'B-new'])
  assert.equal(calls, 1)
  assert.equal(store.genyee_token, 'B-new')
  assert.equal(store.genyee_refresh_token, 'B-new-refresh')
})

test('an old failure cannot clear a newer completed refresh', async () => {
  const { store, location } = installBrowser()
  store.genyee_refresh_token = 'A-refresh'
  const old = deferredRefresh()
  const oldSettled = settleRefreshes([refreshAccessTokenShared({ refresh: () => old.promise })])
  await Promise.resolve()
  store.genyee_refresh_token = 'B-refresh'
  assert.equal(await refreshAccessTokenShared({
    refresh: async () => ({ success: true, data: { token: 'B-new', refreshToken: 'B-new-refresh' } }),
  }), 'B-new')
  old.reject(new Error('A failed late'))
  assertSessionChanged((await oldSettled)[0])
  assert.equal(store.genyee_token, 'B-new')
  assert.equal(store.genyee_refresh_token, 'B-new-refresh')
  assert.equal(location.href, 'http://localhost/chat')
})

test('logout invalidates a refresh even if the same credentials are restored', async () => {
  const { store, location } = installBrowser()
  store.genyee_token = 'access'
  store.genyee_refresh_token = 'refresh'
  const old = deferredRefresh()
  const settled = settleRefreshes([refreshAccessTokenShared({ refresh: () => old.promise })])
  await Promise.resolve()
  clearAuthStorage()
  store.genyee_token = 'access'
  store.genyee_refresh_token = 'refresh'
  old.resolve({ success: true, data: { token: 'old-result', refreshToken: 'old-result-refresh' } })
  assertSessionChanged((await settled)[0])
  assert.equal(store.genyee_token, 'access')
  assert.equal(location.href, 'http://localhost/chat')
})

test('a refresh cannot restore credentials after logout', async () => {
  const { store } = installBrowser()
  store.genyee_refresh_token = 'refresh'
  const old = deferredRefresh()
  const settled = settleRefreshes([refreshAccessTokenShared({ refresh: () => old.promise })])
  await Promise.resolve()
  clearAuthStorage()
  old.resolve({ success: true, data: { token: 'old-result', refreshToken: 'old-result-refresh' } })
  assertSessionChanged((await settled)[0])
  assert.equal(store.genyee_token, undefined)
  assert.equal(store.genyee_refresh_token, undefined)
})

test('changing only the access token also supersedes a pending refresh', async () => {
  const { store } = installBrowser()
  store.genyee_token = 'access'
  store.genyee_refresh_token = 'refresh'
  const old = deferredRefresh()
  const settled = settleRefreshes([refreshAccessTokenShared({ refresh: () => old.promise })])
  await Promise.resolve()
  store.genyee_token = 'replacement-access'
  old.resolve({ success: true, data: { token: 'old-result', refreshToken: 'old-result-refresh' } })
  assertSessionChanged((await settled)[0])
  assert.equal(store.genyee_token, 'replacement-access')
})

test('a storage read failure releases the refresh lock for a later call', async () => {
  const { store, location } = installBrowser()
  store.genyee_refresh_token = 'rt-1'
  const storageError = new DOMException('Storage access denied', 'SecurityError')
  const getItem = localStorage.getItem
  localStorage.getItem = () => { throw storageError }
  let calls = 0
  const refresh = async () => {
    calls += 1
    return { success: true, data: { token: 'fresh', refreshToken: 'rt-2' } }
  }

  await assert.rejects(refreshAccessTokenShared({ refresh }), error => error === storageError)
  assert.equal(calls, 0)
  assert.equal(location.href, '/login')

  localStorage.getItem = getItem
  store.genyee_refresh_token = 'rt-retry'
  const [retry] = await settleRefreshes([refreshAccessTokenShared({ refresh })])
  assert.deepEqual(retry, { status: 'fulfilled', value: 'fresh' })
  assert.equal(calls, 1)
})

test('cleanup failure still rejects every queued caller and attempts the other keys', async () => {
  const { store, location } = installBrowser()
  store.genyee_refresh_token = 'rt-1'
  store.genyee_token = 'expired'
  store.genyee_selected_tenant_id = '9'
  const removeItem = localStorage.removeItem
  localStorage.removeItem = (key: string) => {
    if (key === 'genyee_token') throw new DOMException('Storage access denied', 'SecurityError')
    removeItem(key)
  }
  let fail!: (error: Error) => void
  const pending = new Promise<never>((_, reject) => { fail = reject })
  const refreshError = new Error('refresh provider failed')
  const first = refreshAccessTokenShared({ refresh: () => pending })
  const second = refreshAccessTokenShared({ refresh: () => pending })
  const settled = settleRefreshes([first, second])
  fail(refreshError)

  for (const result of await settled) {
    assert.deepEqual(result, { status: 'rejected', reason: refreshError })
  }
  assert.equal(store.genyee_selected_tenant_id, undefined)
  assert.equal(location.href, '/login')

  localStorage.removeItem = removeItem
  store.genyee_refresh_token = 'rt-retry'
  const [retry] = await settleRefreshes([refreshAccessTokenShared({
    refresh: async () => ({ success: true, data: { token: 'fresh', refreshToken: 'rt-2' } }),
  })])
  assert.deepEqual(retry, { status: 'fulfilled', value: 'fresh' })
})

test('blocked token writes and cleanup reject queued callers instead of returning an unpersisted token', async () => {
  const { store, location } = installBrowser()
  store.genyee_refresh_token = 'rt-1'
  const storageError = new DOMException('Storage quota exceeded', 'QuotaExceededError')
  localStorage.setItem = () => { throw storageError }
  localStorage.removeItem = () => { throw new DOMException('Storage access denied', 'SecurityError') }
  let release!: (result: { success: boolean; data: { token: string; refreshToken: string } }) => void
  const pending = new Promise<{ success: boolean; data: { token: string; refreshToken: string } }>(
    resolve => { release = resolve },
  )
  const first = refreshAccessTokenShared({ refresh: () => pending })
  const second = refreshAccessTokenShared({ refresh: () => pending })
  const settled = settleRefreshes([first, second])
  release({ success: true, data: { token: 'unpersisted', refreshToken: 'rt-2' } })

  for (const result of await settled) {
    assert.deepEqual(result, { status: 'rejected', reason: storageError })
  }
  assert.equal(location.href, '/login')
  assert.equal(store.genyee_token, undefined)
})

test('runStreamWithAuthRetry refreshes once and replays with the new token', async () => {
  installBrowser()
  const tokens: string[] = []
  const result = await runStreamWithAuthRetry({
    initialToken: 'expired',
    isEmbed: false,
    isCurrent: () => true,
    refreshAccessToken: async () => 'fresh',
    reloginMessage: 'please-relogin',
    run: async (token) => {
      tokens.push(token)
      if (token === 'expired') throw new StreamAuthError(401)
      return `ok:${token}`
    },
  })
  assert.deepEqual(tokens, ['expired', 'fresh'])
  assert.equal(result, 'ok:fresh')
})

test('embed visitors are not refreshed or redirected', async () => {
  let refreshed = 0
  await assert.rejects(
    () => runStreamWithAuthRetry({
      initialToken: 'embed-token',
      isEmbed: true,
      isCurrent: () => true,
      refreshAccessToken: async () => {
        refreshed += 1
        return 'nope'
      },
      reloginMessage: 'please-relogin',
      run: async () => {
        throw new StreamAuthError(401)
      },
    }),
    (err: unknown) => err instanceof StreamAuthError,
  )
  assert.equal(refreshed, 0)
})

test('a second handshake 401 after a successful refresh does not wipe tokens', async () => {
  const { store, location } = installBrowser()
  store.genyee_token = 'expired'
  store.genyee_refresh_token = 'rt-1'
  location.href = 'http://localhost/chat'

  await assert.rejects(
    () => runStreamWithAuthRetry({
      initialToken: 'expired',
      isEmbed: false,
      isCurrent: () => true,
      refreshAccessToken: async () => {
        const token = await refreshAccessTokenShared({
          refresh: async () => ({
            success: true,
            data: { token: 'fresh', refreshToken: 'rt-2' },
          }),
        })
        return token
      },
      reloginMessage: 'please-relogin',
      run: async () => {
        throw new StreamAuthError(401)
      },
    }),
    (err: unknown) => err instanceof Error && err.message === 'please-relogin',
  )
  assert.equal(store.genyee_token, 'fresh')
  assert.equal(store.genyee_refresh_token, 'rt-2')
  assert.equal(location.href, 'http://localhost/chat')
})

test('a superseded send skips the replay', async () => {
  let runs = 0
  const result = await runStreamWithAuthRetry({
    initialToken: 'expired',
    isEmbed: false,
    isCurrent: () => false,
    refreshAccessToken: async () => 'fresh',
    reloginMessage: 'please-relogin',
    run: async () => {
      runs += 1
      throw new StreamAuthError(401)
    },
  })
  assert.equal(runs, 1)
  assert.equal(result, undefined)
})

test('forceReloginRedirect does not bounce embed visitors to /login', () => {
  const { store, location } = installBrowser('/embed/ch-1')
  store.genyee_token = 'jwt'
  forceReloginRedirect()
  assert.equal(store.genyee_token, undefined)
  assert.equal(location.href, 'http://localhost/embed/ch-1')
})

test('chat stream wires the shared retry and keeps the abort controller', () => {
  const source = readFileSync(new URL('../api/chat/stream.ts', import.meta.url), 'utf8')
  assert.match(source, /runStreamWithAuthRetry/)
  assert.match(source, /isStreamAuthError/)
  assert.match(source, /streamAbort/)
  assert.doesNotMatch(source, /forceReloginRedirect/)
})

test('P1-13: runStreamWithAuthRetry aborts with AuthSessionChangedError on logout, account switch, or tenant switch without replaying or redirecting', async () => {
  // 1. Logout before 401 refresh starts
  {
    const { store, location } = installBrowser('/chat')
    store.genyee_token = 'expired-a'
    store.genyee_refresh_token = 'rt-a'
    store.genyee_selected_tenant_id = '10001'
    const authContext = captureAuthRequestContext()
    let runs = 0
    let refreshes = 0

    await assert.rejects(
      () => runStreamWithAuthRetry({
        initialToken: 'expired-a',
        isEmbed: false,
        authContext,
        isCurrent: () => true,
        refreshAccessToken: async () => {
          refreshes += 1
          return 'should-not-run'
        },
        reloginMessage: 'please-relogin',
        run: async () => {
          runs += 1
          clearAuthStorage()
          throw new StreamAuthError(401)
        },
      }),
      (err: unknown) => err instanceof AuthSessionChangedError && isAuthSessionChangedError(err),
    )
    assert.equal(runs, 1)
    assert.equal(refreshes, 0)
    assert.equal(location.href, 'http://localhost/chat')
  }

  // 2. Account switch while refreshAccessTokenShared is in flight
  {
    resetAuthRefreshStateForTests()
    const { store, location } = installBrowser('/chat')
    store.genyee_token = 'expired-a'
    store.genyee_refresh_token = 'rt-a'
    store.genyee_selected_tenant_id = '10001'
    const authContext = captureAuthRequestContext()
    let runs = 0

    await assert.rejects(
      () => runStreamWithAuthRetry({
        initialToken: 'expired-a',
        isEmbed: false,
        authContext,
        isCurrent: () => true,
        refreshAccessToken: () => refreshAccessTokenShared({
          refresh: async () => {
            store.genyee_token = 'user-b-token'
            store.genyee_refresh_token = 'user-b-rt'
            return { success: true, data: { token: 'stale-a-token', refreshToken: 'stale-a-rt' } }
          },
        }),
        reloginMessage: 'please-relogin',
        run: async () => {
          runs += 1
          throw new StreamAuthError(401)
        },
      }),
      (err: unknown) => err instanceof AuthSessionChangedError,
    )
    assert.equal(runs, 1, 'must not replay stream under User B session')
    assert.equal(store.genyee_token, 'user-b-token')
    assert.equal(store.genyee_refresh_token, 'user-b-rt')
    assert.equal(location.href, 'http://localhost/chat')
  }

  // 3. Tenant/workspace switch while token refresh is in flight
  {
    resetAuthRefreshStateForTests()
    const { store, location } = installBrowser('/chat')
    store.genyee_token = 'expired-a'
    store.genyee_refresh_token = 'rt-a'
    store.genyee_selected_tenant_id = '10001'
    const authContext = captureAuthRequestContext()
    let runs = 0

    await assert.rejects(
      () => runStreamWithAuthRetry({
        initialToken: 'expired-a',
        isEmbed: false,
        authContext,
        isCurrent: () => true,
        refreshAccessToken: () => refreshAccessTokenShared({
          refresh: async () => {
            store.genyee_selected_tenant_id = '20002'
            return { success: true, data: { token: 'fresh-a', refreshToken: 'rt-a-2' } }
          },
        }),
        reloginMessage: 'please-relogin',
        run: async () => {
          runs += 1
          throw new StreamAuthError(401)
        },
      }),
      (err: unknown) => err instanceof AuthSessionChangedError,
    )
    assert.equal(runs, 1, 'must not replay tenant 10001 stream after switch to tenant 20002')
    assert.equal(store.genyee_token, 'fresh-a', 'same-user refreshed token remains valid for new workspace')
    assert.equal(location.href, 'http://localhost/chat')
  }
})

test('P1-13: runStreamWithAuthRetry reuses an already-rotated token in the same session and tenant without another refresh', async () => {
  const { store } = installBrowser('/chat')
  store.genyee_token = 'expired-1'
  store.genyee_refresh_token = 'rt-1'
  store.genyee_selected_tenant_id = '10001'
  const authContext = captureAuthRequestContext()

  // Another concurrent request in the same session rotates the token first.
  await refreshAccessTokenShared({
    refresh: async () => ({ success: true, data: { token: 'rotated-2', refreshToken: 'rt-2' } }),
  })

  const seenTokens: string[] = []
  let extraRefreshCalls = 0
  const result = await runStreamWithAuthRetry({
    initialToken: 'expired-1',
    isEmbed: false,
    authContext,
    isCurrent: () => true,
    refreshAccessToken: async () => {
      extraRefreshCalls += 1
      return 'unexpected'
    },
    reloginMessage: 'please-relogin',
    run: async (token) => {
      seenTokens.push(token)
      if (token === 'expired-1') throw new StreamAuthError(401)
      return `ok:${token}`
    },
  })

  assert.equal(result, 'ok:rotated-2')
  assert.deepEqual(seenTokens, ['expired-1', 'rotated-2'])
  assert.equal(extraRefreshCalls, 0)
})

test('P1-14: clearAuthStorage clears protected file cache scope', () => {
  installBrowser('/chat')
  setProtectedFileCacheScope('user-42', '10001')
  assert.equal(resolveProtectedFileCacheScope(), 'user-42:10001')

  clearAuthStorage()

  assert.equal(resolveProtectedFileCacheScope(), 'anon:default')
})
