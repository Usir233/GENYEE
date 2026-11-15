// A queued follow-up attaches via continue-stream while the previous POST may
// still be open (it waits up to 3s for the session title). An older stream
// closing or failing must not cut off the stream that replaced it.
import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

type Call = {
  url: string
  options: { signal: AbortSignal, onmessage: (ev: { data: string }) => void, onclose: () => void, onopen: (response: Response) => Promise<void> }
  resolve: () => void
  reject: (err: unknown) => void
}

let server: ViteDevServer
let useStream: typeof import('./streame').useStream
let authRefresh: typeof import('@/utils/authRefresh')
let authApiMock: { setRefreshHandler: (fn: (rt: string) => Promise<unknown>) => void }
let transport: { calls: Call[] }
const storageMap = new Map<string, string>()
const mockLocation = { pathname: '/chat', href: 'http://localhost/chat' }
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')

function resetSessionStorage() {
  storageMap.clear()
  storageMap.set('genyee_token', 'test-token')
  storageMap.set('genyee_refresh_token', 'test-refresh')
  mockLocation.pathname = '/chat'
  mockLocation.href = 'http://localhost/chat'
  authRefresh?.resetAuthRefreshStateForTests()
}

before(async () => {
  resetSessionStorage()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => storageMap.get(key) ?? null,
    setItem: (key: string, value: string) => { storageMap.set(key, String(value)) },
    removeItem: (key: string) => { storageMap.delete(key) },
  } })
  Object.defineProperty(globalThis, 'window', { configurable: true, value: {
    location: mockLocation,
  } })
  // Each request stays pending until the test closes or fails it.
  const mocks: Record<string, string> = {
    '@microsoft/fetch-event-source': `export const calls = [];
      export function fetchEventSource(url, options) {
        return new Promise((resolve, reject) => { calls.push({ url, options, resolve, reject }) })
      }`,
    '@/utils/index': `export const generateRandomString = () => 'test-request'; export const MAX_FILE_SIZE_BYTES = 50 * 1024 * 1024; export const MAX_FILE_SIZE_MB = 50;`,
    '@/i18n': `export default { global: { t: key => key, locale: { value: 'zh-CN' } } };`,
    'auth-api': `let handler = async () => ({ success: true, data: { token: 'refreshed-token', refreshToken: 'refreshed-rt' } });
      export function setRefreshHandler(fn) { handler = fn; }
      export function refreshToken(rt) { return handler(rt); }`,
  }
  server = await createServer({
    configFile: false,
    plugins: [{ name: 'stream-overlap-test', enforce: 'pre',
      resolveId(id) {
        if (id.startsWith('\0mock:')) return id
        if (id.endsWith('/api/auth/index') || id.endsWith('/api/auth/index.ts')) return '\0mock:auth-api'
      },
      load(id) { if (id.startsWith('\0mock:')) return mocks[id.slice(6)] },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: [
      ...Object.keys(mocks).map(find => ({ find, replacement: '\0mock:' + find })),
      { find: '@', replacement: fileURLToPath(new URL('../../', import.meta.url)) },
    ] },
    // No file watching needed in tests (also avoids ENOSPC on hosts with few inotify watches).
    server: { middlewareMode: true, hmr: false, ws: false, watch: null }, appType: 'custom',
  })
  ;({ useStream } = await server.ssrLoadModule('/src/api/chat/streame.ts'))
  authRefresh = await server.ssrLoadModule('/src/utils/authRefresh.ts') as typeof authRefresh
  authApiMock = await server.ssrLoadModule('\0mock:auth-api') as typeof authApiMock
  transport = await server.ssrLoadModule('\0mock:@microsoft/fetch-event-source') as typeof transport
})
after(async () => {
  await server?.close()
  if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else Reflect.deleteProperty(globalThis, 'window')
})

async function nextCall(count: number): Promise<Call> {
  for (let i = 0; i < 100 && transport.calls.length < count; i++) await new Promise(r => setTimeout(r, 5))
  assert.equal(transport.calls.length, count, 'fetchEventSource was not called')
  return transport.calls[count - 1]
}

async function overlapping() {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const chunks: unknown[] = []
  stream.onChunk(data => chunks.push(data))
  const base = transport.calls.length
  // First question: the POST stays open after its answer, waiting for the title.
  const first = stream.startStream({ session_id: 's', query: 'q1', method: 'POST', url: '/api/v1/agent-chat' })
  const a = await nextCall(base + 1)
  // The queued follow-up attaches while that POST is still open.
  const second = stream.startStream({ session_id: 's', query: 'assistant-2', method: 'GET', url: '/api/v1/sessions/continue-stream' })
  const b = await nextCall(base + 2)
  assert.match(b.url, /continue-stream\/s\?message_id=assistant-2$/)
  return { stream, chunks, a, b, first, second }
}

test('an older stream closing does not cut the newer follow-up stream', async () => {
  const { stream, chunks, a, b, first, second } = await overlapping()
  a.options.onclose()
  a.resolve()
  await first
  assert.equal(b.options.signal.aborted, false, 'old onclose aborted the new stream')
  assert.equal(stream.isStreaming.value, true)
  b.options.onmessage({ data: JSON.stringify({ response_type: 'answer', id: 'r2', content: 'hi' }) })
  assert.equal(chunks.length, 1, 'new stream events were dropped after the old one closed')
  // The current stream closing still stops as before.
  b.options.onclose()
  b.resolve()
  await second
  assert.equal(stream.isStreaming.value, false)
  assert.equal(b.options.signal.aborted, true)
  assert.equal(stream.error.value, null)
})

test('an older stream failing does not report an error or cut the newer stream', async () => {
  const { stream, chunks, a, b, first, second } = await overlapping()
  a.reject(new Error('old connection reset'))
  await first
  assert.equal(stream.error.value, null, 'old stream error leaked into the current one')
  assert.equal(b.options.signal.aborted, false)
  b.options.onmessage({ data: JSON.stringify({ response_type: 'complete', id: 'r2' }) })
  assert.equal(chunks.length, 1)
  b.options.onclose()
  b.resolve()
  await second
})

test('the current stream failing still reports the error and stops', async () => {
  const { stream, a, b, first, second } = await overlapping()
  b.reject(new Error('current connection reset'))
  await second
  assert.match(String(stream.error.value), /current connection reset/)
  assert.equal(stream.isStreaming.value, false)
  assert.equal(b.options.signal.aborted, true)
  a.resolve()
  await first
})

test('a disconnected accepted agent POST resumes by GET without executing the query twice', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 'parent', query: 'spawn children', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'answer-1' }) })
  post.reject(new Error('server restarted'))
  await new Promise(r => setTimeout(r, 1050))
  const resumed = await nextCall(base + 2)
  assert.match(resumed.url, /\/sessions\/continue-stream\/parent\?message_id=answer-1$/)
  assert.equal((resumed.options as any).method, 'GET')
  assert.equal((resumed.options as any).body, null)
  assert.equal(stream.isStreaming.value, true)
  assert.equal(stream.error.value, null)
  resumed.options.onmessage({ data: JSON.stringify({ response_type: 'complete', id: 'r1' }) })
  resumed.options.onclose()
  resumed.resolve()
  await run
  assert.equal(stream.isStreaming.value, false)
})

test('stop during reconnect backoff prevents the old parent from attaching again', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 'parent', query: 'spawn children', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'answer-1' }) })
  post.reject(new Error('connection reset'))
  await new Promise(r => setTimeout(r, 20))
  stream.stopStream()
  await run
  await new Promise(r => setTimeout(r, 1050))
  assert.equal(transport.calls.length, base + 1)
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
})

test('EOF without complete attaches to the original message rather than declaring completion', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 'parent', query: 'work', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'answer-1' }) })
  try { post.options.onclose(); assert.fail('an incomplete EOF must request recovery') }
  catch (error) { post.reject(error) }
  await new Promise(r => setTimeout(r, 1050))
  const resumed = await nextCall(base + 2)
  assert.equal((resumed.options as any).method, 'GET')
  resumed.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  resumed.options.onclose()
  resumed.resolve()
  await run
  assert.equal(stream.error.value, null)
})

test('a new stream supersedes an old reconnect without being stopped or receiving old output', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const first = stream.startStream({ session_id: 'parent', query: 'old', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'old-answer' }) })
  post.reject(new Error('server restarted'))
  await new Promise(r => setTimeout(r, 20))
  const second = stream.startStream({ session_id: 'another-session', query: 'new', method: 'POST', url: '/api/v1/agent-chat' })
  const current = await nextCall(base + 2)
  await first
  assert.equal(transport.calls.length, base + 2)
  assert.equal(current.options.signal.aborted, false)
  assert.equal(stream.isStreaming.value, true)
  assert.equal(stream.error.value, null)
  current.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  current.options.onclose()
  current.resolve()
  await second
})

test('a silent broken proxy connection times out and resumes by GET; heartbeats stay out of chat', async (t) => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const chunks: unknown[] = []
  stream.onChunk(value => chunks.push(value))
  const base = transport.calls.length
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const run = stream.startStream({ session_id: 'parent', query: 'work', method: 'POST', url: '/api/v1/agent-chat' })
  const post = transport.calls[base]
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'answer-1' }) })
  t.mock.timers.tick(20000)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'heartbeat' }) })
  assert.equal(chunks.length, 1)
  t.mock.timers.tick(20000)
  assert.equal(post.options.signal.aborted, false)
  t.mock.timers.tick(10001)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  assert.equal(post.options.signal.aborted, true)
  t.mock.timers.tick(1000)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  const resumed = transport.calls[base + 1]
  assert.ok(resumed)
  assert.equal((resumed.options as any).method, 'GET')
  resumed.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  resumed.options.onclose()
  resumed.resolve()
  await run
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
})


test('a reopened turn recovers when its replay connection drops before the first event', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const chunks: unknown[] = []
  stream.onChunk(data => chunks.push(data))
  const base = transport.calls.length
  const initial = stream.startStream({ session_id: 's', query: 'question', method: 'POST', url: '/api/v1/agent-chat' })
  const post = await nextCall(base + 1)
  post.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' }) })
  post.options.onmessage({ data: JSON.stringify({ response_type: 'tool_call', id: 'r1', data: { tool_call_id: 'call-1', tool_name: 'search', arguments: {} } }) })
  post.options.onmessage({ data: JSON.stringify({ response_type: 'tool_result', id: 'r1', data: { tool_call_id: 'call-1', tool_name: 'search', success: true, output: 'found source' } }) })
  assert.equal(chunks.length, 3)
  // Route leave aborts only the browser stream. The server's turn continues.
  stream.stopStream()
  post.resolve()
  await initial
  const reopened = stream.startStream({ session_id: 's', query: 'a1', method: 'GET', url: '/api/v1/sessions/continue-stream', resume_pending_id: 'approved/a+b' })
  const replay = await nextCall(base + 2)
  await replay.options.onopen(new Response('', { status: 200 }))
  replay.reject(new Error('connection reset before first replay event'))
  await new Promise(resolve => setTimeout(resolve, 1050))
  try {
    const retry = await nextCall(base + 3)
    assert.equal(retry.url, replay.url, 'reattach must preserve its approval segment')
    assert.equal((retry.options as any).method, 'GET')
    assert.equal((retry.options as any).body, null)
    retry.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', id: 'r1', assistant_message_id: 'a1' }) })
    retry.options.onmessage({ data: JSON.stringify({ response_type: 'tool_call', id: 'r1', data: { tool_call_id: 'call-1', tool_name: 'search', arguments: {} } }) })
    retry.options.onmessage({ data: JSON.stringify({ response_type: 'tool_result', id: 'r1', data: { tool_call_id: 'call-1', tool_name: 'search', success: true, output: 'found source' } }) })
    retry.options.onmessage({ data: JSON.stringify({ response_type: 'answer', id: 'r1', content: 'continued answer' }) })
    retry.options.onmessage({ data: JSON.stringify({ response_type: 'complete', id: 'r1' }) })
    retry.resolve()
    await reopened
    assert.equal(stream.error.value, null)
    assert.equal(transport.calls.length, base + 3, 'recovery must not repeat the question POST')
    assert.equal(chunks.length, 8)
  } finally { stream.stopStream(); transport.calls.at(-1)?.resolve(); await reopened }
})


test('a reopened stream with no first event times out and keeps its approval segment', async (t) => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const run = stream.startStream({ session_id: 's', query: 'a1', method: 'GET', url: '/api/v1/sessions/continue-stream', resume_pending_id: 'pending-1' })
  const replay = transport.calls[base]
  await replay.options.onopen(new Response('', { status: 200 }))
  t.mock.timers.tick(30001)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  assert.equal(replay.options.signal.aborted, true)
  t.mock.timers.tick(1000)
  for (let i = 0; i < 20; i++) await Promise.resolve()
  const retry = transport.calls[base + 1]
  assert.ok(retry)
  assert.equal(retry.url, replay.url)
  retry.options.onmessage({ data: JSON.stringify({ response_type: 'complete' }) })
  retry.resolve()
  await run
  assert.equal(stream.error.value, null)
})

test('an unavailable replay buffer still reports its initial HTTP error', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base = transport.calls.length
  const run = stream.startStream({ session_id: 's', query: 'missing', method: 'GET', url: '/api/v1/sessions/continue-stream' })
  const replay = await nextCall(base + 1)
  await assert.rejects(replay.options.onopen(new Response('', { status: 404 })), /HTTP 404/)
  replay.reject(new Error('HTTP 404'))
  await run
  assert.equal(transport.calls.length, base + 1)
  assert.match(String(stream.error.value), /HTTP 404/)
  assert.equal(stream.isStreaming.value, false)
})

test('P1-13: logout, account switch, or tenant switch during SSE 401 refresh silently aborts without replay or /login redirect', async (t) => {
  t.after(() => resetSessionStorage())

  // 1. Logout during 401 token refresh
  resetSessionStorage()
  storageMap.set('genyee_token', 'expired-a')
  storageMap.set('genyee_refresh_token', 'rt-a')
  storageMap.set('genyee_selected_tenant_id', '10001')

  authApiMock.setRefreshHandler(async () => {
    authRefresh.clearAuthStorage()
    return { success: false }
  })

  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base1 = transport.calls.length
  const run1 = stream.startStream({ session_id: 'sess-logout', query: 'hello', method: 'POST', url: '/api/v1/agent-chat' })
  const call1 = await nextCall(base1 + 1)
  assert.equal((call1.options as any).headers['X-Tenant-ID'], '10001')
  try {
    await call1.options.onopen(new Response('', { status: 401 }))
    assert.fail('401 must throw StreamAuthError')
  } catch (err) {
    call1.reject(err)
  }
  await run1
  assert.equal(transport.calls.length, base1 + 1, 'logged-out stream must not replay')
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
  assert.equal(mockLocation.href, 'http://localhost/chat', 'must not redirect active view to /login')

  // 2. Account switch during 401 token refresh
  resetSessionStorage()
  storageMap.set('genyee_token', 'expired-a')
  storageMap.set('genyee_refresh_token', 'rt-a')
  storageMap.set('genyee_selected_tenant_id', '10001')

  authApiMock.setRefreshHandler(async () => {
    storageMap.set('genyee_token', 'user-b-token')
    storageMap.set('genyee_refresh_token', 'user-b-rt')
    return { success: true, data: { token: 'stale-a', refreshToken: 'stale-rt' } }
  })

  const base2 = transport.calls.length
  const run2 = stream.startStream({ session_id: 'sess-switch-user', query: 'hello', method: 'POST', url: '/api/v1/agent-chat' })
  const call2 = await nextCall(base2 + 1)
  try {
    await call2.options.onopen(new Response('', { status: 401 }))
    assert.fail('401 must throw StreamAuthError')
  } catch (err) {
    call2.reject(err)
  }
  await run2
  assert.equal(transport.calls.length, base2 + 1, 'switched-user stream must not replay under new user')
  assert.equal(storageMap.get('genyee_token'), 'user-b-token')
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
  assert.equal(mockLocation.href, 'http://localhost/chat')

  // 3. Tenant switch during 401 token refresh
  resetSessionStorage()
  storageMap.set('genyee_token', 'expired-a')
  storageMap.set('genyee_refresh_token', 'rt-a')
  storageMap.set('genyee_selected_tenant_id', '10001')

  authApiMock.setRefreshHandler(async () => {
    storageMap.set('genyee_selected_tenant_id', '20002')
    return { success: true, data: { token: 'fresh-a', refreshToken: 'rt-a-2' } }
  })

  const base3 = transport.calls.length
  const run3 = stream.startStream({ session_id: 'sess-switch-tenant', query: 'hello', method: 'POST', url: '/api/v1/agent-chat' })
  const call3 = await nextCall(base3 + 1)
  try {
    await call3.options.onopen(new Response('', { status: 401 }))
    assert.fail('401 must throw StreamAuthError')
  } catch (err) {
    call3.reject(err)
  }
  await run3
  assert.equal(transport.calls.length, base3 + 1, 'tenant-switched stream must not replay old workspace request')
  assert.equal(storageMap.get('genyee_token'), 'fresh-a')
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
  assert.equal(mockLocation.href, 'http://localhost/chat')
})

test('P1-13: logout or tenant switch during SSE reconnect loop silently aborts without reconnecting or redirecting', async (t) => {
  t.after(() => resetSessionStorage())
  t.mock.timers.enable({ apis: ['setTimeout'] })

  // 1. Workspace switch during reconnect backoff
  resetSessionStorage()
  storageMap.set('genyee_selected_tenant_id', '10001')

  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const base1 = transport.calls.length
  const run1 = stream.startStream({ session_id: 'sess-reconnect-tenant', query: 'work', method: 'POST', url: '/api/v1/agent-chat' })
  const post1 = transport.calls[base1]!
  post1.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'ans-1' }) })
  post1.reject(new Error('network drop'))
  for (let i = 0; i < 10; i++) await Promise.resolve()

  // Switch workspace while reconnect timer is pending
  storageMap.set('genyee_selected_tenant_id', '20002')
  t.mock.timers.tick(1000)
  await run1

  assert.equal(transport.calls.length, base1 + 1, 'must not reconnect continue-stream after tenant switch')
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
  assert.equal(mockLocation.href, 'http://localhost/chat')

  // 2. Logout before disconnect immediately aborts reconnect without waiting
  resetSessionStorage()
  storageMap.set('genyee_selected_tenant_id', '10001')

  const base2 = transport.calls.length
  const run2 = stream.startStream({ session_id: 'sess-reconnect-logout', query: 'work', method: 'POST', url: '/api/v1/agent-chat' })
  const post2 = transport.calls[base2]!
  post2.options.onmessage({ data: JSON.stringify({ response_type: 'agent_query', assistant_message_id: 'ans-2' }) })
  authRefresh.clearAuthStorage()
  post2.reject(new Error('network drop'))
  await run2

  assert.equal(transport.calls.length, base2 + 1, 'must not reconnect continue-stream after logout')
  assert.equal(stream.isStreaming.value, false)
  assert.equal(stream.error.value, null)
  assert.equal(mockLocation.href, 'http://localhost/chat')
})
