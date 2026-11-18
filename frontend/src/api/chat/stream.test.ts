import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer, type ViteDevServer } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let server: ViteDevServer
let useStream: typeof import('./stream').useStream
let transport: { requests: Array<{ url: string; options: { body: string } }> }
let uploadLimit: { setRequestLimit: (bytes: number) => void }
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
before(async () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => key === 'genyee_token' ? 'test-token' : null,
  } })
  const mocks: Record<string, string> = {
    '@microsoft/fetch-event-source': `export const requests = []; export async function fetchEventSource(url, options) { requests.push({url, options}); }`,
    '@/utils/index': `export const generateRandomString = () => 'test-request'; export let MAX_FILE_SIZE_BYTES = 50 * 1024 * 1024; export const MAX_FILE_SIZE_MB = 50; export const setRequestLimit = bytes => { MAX_FILE_SIZE_BYTES = bytes; };`,
    '@/i18n': `export default { global: { t: key => key, locale: { value: 'zh-CN' } } };`,
  }
  server = await createServer({
    configFile: false,
    plugins: [{ name: 'stream-request-test', enforce: 'pre',
      resolveId(id) { if (id.startsWith('\0mock:')) return id },
      load(id) { if (id.startsWith('\0mock:')) return mocks[id.slice(6)] },
    }],
    optimizeDeps: { noDiscovery: true, entries: [] },
    resolve: { alias: [
      ...Object.keys(mocks).map(find => ({ find, replacement: '\0mock:' + find })),
      { find: '@', replacement: fileURLToPath(new URL('../../', import.meta.url)) },
    ] },
    server: { middlewareMode: true, hmr: false, ws: false, watch: null }, appType: 'custom',
  })
  ;({ useStream } = await server.ssrLoadModule('/src/api/chat/stream.ts'))
  transport = await server.ssrLoadModule('\0mock:@microsoft/fetch-event-source') as typeof transport
  uploadLimit = await server.ssrLoadModule('\0mock:@/utils/index') as typeof uploadLimit
})
after(async () => {
  await server?.close()
  if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
  else Reflect.deleteProperty(globalThis, 'localStorage')
})

test('oversized UTF-8 JSON is rejected before the SSE transport sends a request', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  const requestsBefore = transport.requests.length
  uploadLimit.setRequestLimit(256)
  try {
    await stream.startStream({
      session_id: 'session', query: '汉'.repeat(100), method: 'POST', url: '/api/v1/agent-chat',
    })
    assert.equal(transport.requests.length, requestsBefore)
    assert.equal(stream.error.value, 'error.fileSizeExceeded')
    assert.equal(stream.isStreaming.value, false)
    assert.equal(stream.isLoading.value, false)
  } finally {
    uploadLimit.setRequestLimit(50 * 1024 * 1024)
    stream.stopStream()
  }
})

test('approval reattach identifies the resumed segment without changing ordinary replay', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  try {
    for (const pendingId of [undefined, 'pending/a+b']) {
      await stream.startStream({
        session_id: 'session', query: 'message', method: 'GET',
        url: '/api/v1/sessions/continue-stream', resume_pending_id: pendingId,
      })
      assert.equal(stream.error.value, null)
      const request = transport.requests.at(-1)!
      assert.equal(request.url, '/api/v1/sessions/continue-stream/session?message_id=message' +
        (pendingId ? '&resume_pending_id=pending%2Fa%2Bb' : ''))
    }
  } finally { stream.stopStream() }
})

for (const effort of [undefined, '', 'off', 'auto', 'high', 'max']) {
  test(`SSE body carries only explicit reasoning overrides: ${effort}`, async () => {
    let stream!: ReturnType<typeof useStream>
    await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
    try {
      await stream.startStream({
        session_id: 'session', query: 'question', method: 'POST', url: '/api/v1/agent-chat',
        reasoning_effort: effort,
      })
      assert.equal(stream.error.value, null)
      const body = JSON.parse(transport.requests.at(-1)!.options.body)
      assert.equal(body.reasoning_effort, effort || undefined)
      assert.equal(Object.hasOwn(body, 'reasoning_effort'), !!effort)
    } finally { stream.stopStream() }
  })
}

for (const selected of [true, false, undefined]) {
  test(`SSE HTTP body preserves browser selection ${selected} alongside other tools`, async () => {
    let stream!: ReturnType<typeof useStream>
    await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
    try {
      await stream.startStream({
        session_id: 'new-session', query: '查一下腾讯股价', method: 'POST', url: '/api/v1/agent-chat',
        agent_enabled: true, local_browser_enabled: selected, web_search_enabled: true,
        mcp_service_ids: ['mcp-1'], skill_names: ['report'],
      })
      assert.equal(stream.error.value, null)
      const request = transport.requests.at(-1)!
      assert.equal(request.url, '/api/v1/agent-chat/new-session')
      const body = JSON.parse(request.options.body)
      assert.equal(body.local_browser_enabled, selected)
      assert.equal(Object.hasOwn(body, 'local_browser_enabled'), selected !== undefined)
      assert.equal(body.web_search_enabled, true)
      assert.deepEqual(body.mcp_service_ids, ['mcp-1'])
      assert.deepEqual(body.skill_names, ['report'])
      assert.equal(stream.lastStreamRequest.value?.body?.local_browser_enabled, selected)
    } finally { stream.stopStream() }
  })
}

test('SSE body carries an agent-only page context', async () => {
  let stream!: ReturnType<typeof useStream>
  await renderToString(createSSRApp({ setup() { stream = useStream(); return () => null } }))
  try {
    await stream.startStream({
      session_id: 'session', query: 'Check', method: 'POST', url: '/api/v1/agent-chat',
      agent_enabled: true, page_context: { agent_id: 'agent-a' },
    })
    assert.equal(stream.error.value, null)
    const body = JSON.parse(transport.requests.at(-1)!.options.body)
    assert.deepEqual(body.page_context, { agent_id: 'agent-a' })
    assert.equal(body.assistant_scope, undefined)
  } finally { stream.stopStream() }
})
