import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'

function compileTs(relUrl: URL): string {
  const src = readFileSync(relUrl, 'utf8')
  return ts.transpileModule(src, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
}

test('W6-API-01: wiki encodeSlugPath neutralizes dot-segment path traversal and wiki endpoints URL-encode kbId/folderId/issueId', async () => {
  const calls: Array<{ method: string; url: string }> = []
  const wikiExports: Record<string, (...args: string[]) => Promise<unknown>> = {}
  vm.runInNewContext(compileTs(new URL('./index.ts', import.meta.url)), {
    exports: wikiExports,
    URLSearchParams,
    encodeURIComponent,
    require(id: string) {
      assert.equal(id, '../../utils/request')
      return {
        get: (url: string) => { calls.push({ method: 'GET', url }); return Promise.resolve({ data: {} }) },
        post: (url: string) => { calls.push({ method: 'POST', url }); return Promise.resolve({ data: {} }) },
        put: (url: string) => { calls.push({ method: 'PUT', url }); return Promise.resolve({ data: {} }) },
        del: (url: string) => { calls.push({ method: 'DELETE', url }); return Promise.resolve({ data: {} }) },
      }
    },
  })

  await wikiExports.getWikiPage('kb/1?x=1', 'docs/../../tenants/secret')
  const pageUrl = calls.at(-1)!.url
  const resolved = new URL(pageUrl, 'https://app.example.com')
  assert.ok(
    resolved.pathname.startsWith('/api/v1/knowledge-bases/kb%2F1%3Fx%3D1/wiki/pages/'),
    `Slug traversal escaped wiki/pages prefix: ${resolved.pathname}`,
  )
  assert.doesNotMatch(pageUrl, /\/\.\.(?:\/|$)/, 'Literal .. segments must be percent-encoded')

  await wikiExports.deleteWikiFolder('kb/1', 'folder/../admin')
  assert.equal(calls.at(-1)!.url, '/api/v1/knowledge-bases/kb%2F1/wiki/folders/folder%2F..%2Fadmin')

  await wikiExports.updateWikiIssueStatus('kb/1', 'issue/../1', 'resolved')
  assert.equal(calls.at(-1)!.url, '/api/v1/knowledge-bases/kb%2F1/wiki/issues/issue%2F..%2F1/status')
})

test('W6-API-02: useStream encodes session_id and message_id in SSE URLs and does not retain unbounded SSE event buffers', async () => {
  const streamSrc = readFileSync(new URL('../chat/stream.ts', import.meta.url), 'utf8')
  assert.doesNotMatch(streamSrc, /\bbuffer\.push\b/, 'stream.ts must not push every SSE event into an unread buffer')

  const capturedUrls: string[] = []
  const streamExports: {
    useStream?: () => {
      startStream: (params: Record<string, unknown>) => Promise<void>
      stopStream: () => void
    }
  } = {}
  vm.runInNewContext(compileTs(new URL('../chat/stream.ts', import.meta.url)), {
    exports: streamExports,
    AbortController,
    AbortSignal,
    TextEncoder,
    Promise,
    Set,
    Map,
    Number,
    String,
    Boolean,
    Object,
    Array,
    JSON,
    Math,
    Date,
    Error,
    encodeURIComponent,
    setTimeout,
    clearTimeout,
    performance: { now: () => 0 },
    console: { log() {}, warn() {}, error() {} },
    localStorage: {
      getItem: (k: string) => (k === 'genyee_token' ? 'jwt-1' : null),
    },
    require(id: string) {
      if (id === 'vue') return { ...vue, onUnmounted() {} }
      if (id === '@microsoft/fetch-event-source') {
        return {
          fetchEventSource: async (url: string) => {
            capturedUrls.push(url)
          },
        }
      }
      if (id === '@/i18n') {
        const global = { t: (k: string) => k, locale: { value: 'en-US' } }
        return { __esModule: true, default: { global }, global }
      }
      if (id === '@/utils/index') {
        return {
          generateRandomString: () => 'req123456789',
          MAX_FILE_SIZE_BYTES: 50 * 1024 * 1024,
          MAX_FILE_SIZE_MB: 50,
        }
      }
      if (id === '@/utils/api-base') return { getApiBaseUrl: () => '' }
      if (id === '@/utils/authRefresh') {
        return {
          StreamAuthError: class extends Error {},
          assertAuthRequestContext: () => 'jwt-1',
          captureAuthRequestContext: () => ({ generation: 1, tenantId: null }),
          isAuthSessionChangedError: () => false,
          isStreamAuthError: () => false,
          refreshAccessTokenShared: async () => 'jwt-1',
          runStreamWithAuthRetry: async (opts: { initialToken: string; run: (token: string) => Promise<void> }) => opts.run(opts.initialToken),
        }
      }
      if (id === '@/utils/chatRequestDebug') {
        return { sanitizeStreamRequestBody: (b: unknown) => b }
      }
      throw new Error(`Unexpected require in stream test: ${id}`)
    },
  })

  const s = streamExports.useStream!()
  await s.startStream({
    session_id: 'sess/1?inject=1#frag',
    query: 'hello',
    method: 'POST',
    url: '/api/v1/agent-chat',
  })
  assert.equal(capturedUrls[0], '/api/v1/agent-chat/sess%2F1%3Finject%3D1%23frag')

  await s.startStream({
    session_id: 'sess/2',
    query: 'msg&resume_pending_id=evil#x',
    method: 'GET',
    url: '/api/v1/sessions/continue-stream',
  })
  assert.equal(capturedUrls[1], '/api/v1/sessions/continue-stream/sess%2F2?message_id=msg%26resume_pending_id%3Devil%23x')
  s.stopStream()
})

test('W6-API-03: embed API URL-encodes suggestion path segments, honours EMBED_BASE_URL fallback, and escapes single quotes in JS snippets', async () => {
  const postCalls: string[] = []
  const embedExports: {
    ensureEmbedMessageSuggestions?: (...args: string[]) => Promise<unknown>
    buildEmbedURL?: (...args: string[]) => string
    buildWidgetSnippet?: (...args: string[]) => string
    buildSecureServerNodeExample?: (...args: string[]) => string
  } = {}
  const embedSrc = readFileSync(new URL('../embed/index.ts', import.meta.url), 'utf8')
    .replace(/import\.meta\.env\.VITE_EMBED_BASE_URL/g, 'undefined')
  const compiledEmbed = ts.transpileModule(embedSrc, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  vm.runInNewContext(compiledEmbed, {
    exports: embedExports,
    AbortController,
    AbortSignal,
    URL,
    URLSearchParams,
    encodeURIComponent,
    String,
    Boolean,
    window: {
      location: {
        origin: 'https://admin.internal.local',
        href: 'https://admin.internal.local/platform/settings',
        search: '',
        hash: '',
      },
      addEventListener() {},
      removeEventListener() {},
    },
    require(id: string) {
      if (id === '@/utils/embedBaseUrl') {
        return { resolveEmbedBaseUrl: () => 'https://embed.public.example.com' }
      }
      if (id === '@/utils/request') {
        return {
          post: (url: string) => { postCalls.push(url); return Promise.resolve({ success: true }) },
          get: () => Promise.resolve({ success: true }),
          put: () => Promise.resolve({ success: true }),
          del: () => Promise.resolve({ success: true }),
        }
      }
      throw new Error(`Unexpected require in embed test: ${id}`)
    },
  })

  await embedExports.ensureEmbedMessageSuggestions!('ch/1', 'tok', 'sess/2', 'msg/3#x', 'sig', 'vis')
  assert.equal(
    postCalls[0],
    '/api/v1/embed/ch%2F1/sessions/sess%2F2/messages/msg%2F3%23x/suggestions',
  )

  // When opts.baseUrl is omitted, snippets and buildEmbedURL must honour resolveEmbedBaseUrl()
  const embedUrl = embedExports.buildEmbedURL!('chan-1', 'tok-1')
  assert.match(embedUrl, /^https:\/\/embed\.public\.example\.com\/embed\/chan-1#token=tok-1$/)

  const widgetSnippet = embedExports.buildWidgetSnippet!('chan-1', 'tok-1')
  assert.match(widgetSnippet, /https:\/\/embed\.public\.example\.com\/genyee-widget\.js/)

  // Single quotes in channelId must not break single-quoted fetch('...') in Node server example
  const nodeExample = embedExports.buildSecureServerNodeExample!("ch'test")
  assert.doesNotMatch(nodeExample, /fetch\('[^']*ch'test/)
  assert.match(nodeExample, /ch%27test/)
})

test('W6-API-04: testMultimodalFunction extracts structured backend error.message and routes through authenticated upload client', async () => {
  let postUploadCalled = false
  const initExports: {
    testMultimodalFunction?: (data: Record<string, unknown>) => Promise<unknown>
  } = {}
  vm.runInNewContext(compileTs(new URL('../initialization/index.ts', import.meta.url)), {
    exports: initExports,
    FormData,
    Blob,
    Promise,
    JSON,
    Object,
    Array,
    Number,
    String,
    Boolean,
    encodeURIComponent,
    console: { error() {} },
    localStorage: { getItem: () => null },
    require(id: string) {
      if (id === '../../utils/request') {
        return {
          get: async () => ({}),
          post: async () => ({}),
          put: async () => ({}),
          postUpload: async (url: string, body: FormData) => {
            postUploadCalled = true
            assert.equal(url, '/api/v1/initialization/multimodal/test')
            assert.ok(body instanceof FormData)
            return Promise.reject({
              status: 400,
              message: 'VLLM credentials invalid',
              success: false,
              error: { code: 4001, message: 'VLLM credentials invalid' },
            })
          },
        }
      }
      if (id === '../../utils/api-base') return { getApiBaseUrl: () => '' }
      if (id === '@/i18n') return { default: { global: { t: (k: string) => k } } }
      throw new Error(`Unexpected require in initialization test: ${id}`)
    },
  })

  const fakeImage = new Blob(['img'], { type: 'image/png' }) as unknown as File
  const result = await initExports.testMultimodalFunction!({
    image: fakeImage,
    vlm_model: 'gpt-4o',
    vlm_base_url: 'https://api.example.com/v1',
    storage_type: 'minio',
    chunk_size: 500,
    chunk_overlap: 50,
    separators: ['\n'],
  })
  assert.equal(postUploadCalled, true, 'testMultimodalFunction should use postUpload (with 401 refresh & upload timeout)')
  assert.deepEqual(JSON.parse(JSON.stringify(result)), { success: false, message: 'VLLM credentials invalid' })
})
