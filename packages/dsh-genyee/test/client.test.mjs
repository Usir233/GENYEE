import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { GenyeeApiError, GenyeeClient } from '../dist/client.js'
import { resolveConfig } from '../dist/config.js'
import { startMockGenyee } from './helpers/mock-genyee.mjs'

const never = new AbortController().signal

/** Start a mock backend and a client wired to it, closed after the test file. */
async function harness(overrides = {}, mockOptions = {}) {
  const mock = await startMockGenyee(mockOptions)
  after(() => mock.close())
  const config = resolveConfig({ baseUrl: mock.url, ...overrides })
  return { mock, client: new GenyeeClient(config), config }
}

test('lists knowledge bases and sends the API key header', async () => {
  const { mock, client } = await harness({ apiKey: 'secret-key' }, { apiKey: 'secret-key' })
  const bases = await client.listKnowledgeBases(never)
  assert.deepEqual(bases.map(base => base.id), ['kb-product', 'kb-ops'])
  assert.equal(mock.requests.at(-1).headers['x-api-key'], 'secret-key')
})

test('a rejected credential surfaces the status and the server reason', async () => {
  const { client } = await harness({ apiKey: 'wrong-key' }, { apiKey: 'right-key' })
  await assert.rejects(client.listKnowledgeBases(never), error => {
    assert.ok(error instanceof GenyeeApiError)
    assert.equal(error.status, 401)
    assert.match(error.message, /HTTP 401: invalid api key/)
    return true
  })
})

test('search posts the documented body and returns ranked hits', async () => {
  const { mock, client } = await harness()
  const results = await client.search(
    { query: '混合检索 的 阈值 是多少', knowledgeBaseIds: ['kb-product'], knowledgeIds: [] },
    never,
  )
  assert.ok(results.length > 0)
  assert.equal(mock.requests.at(-1).path, '/api/v1/knowledge-search')
  assert.deepEqual(mock.requests.at(-1).body.knowledge_base_ids, ['kb-product'])
  assert.ok(results[0].score >= results.at(-1).score)
})

test('resourceUrls=public is forwarded as a query parameter', async () => {
  const { mock, client } = await harness({ resourceUrls: 'public' })
  await client.listKnowledgeBases(never)
  assert.equal(mock.requests.at(-1).query.resource_urls, 'public')
})

test('resourceUrls defaults to public so figures are loadable', async () => {
  const { mock, client } = await harness()
  await client.listKnowledgeBases(never)
  assert.equal(mock.requests.at(-1).query.resource_urls, 'public')
})

test('handle mode omits the resource_urls query parameter', async () => {
  const { mock, client } = await harness({ resourceUrls: 'handle' })
  await client.listKnowledgeBases(never)
  assert.equal(mock.requests.at(-1).query.resource_urls, undefined)
})

test('a knowledge-base-restricted key falls back from public URLs to handles', async () => {
  const { mock, client } = await harness({}, { forbidPublicResourceUrls: true })
  const bases = await client.listKnowledgeBases(never)
  assert.equal(bases.length, 2)
  assert.equal(mock.requests[0].query.resource_urls, 'public')
  assert.equal(mock.requests[1].query.resource_urls, undefined)
  await client.listKnowledgeBases(never)
  assert.equal(mock.requests.at(-1).query.resource_urls, undefined, 'later calls must not retry public mode')
})

test('chunk pages carry their pagination envelope', async () => {
  const { client } = await harness()
  const page = await client.listChunks({ knowledgeId: 'doc-retrieval-pipeline', page: 1, pageSize: 2 }, never)
  assert.equal(page.total, 3)
  assert.equal(page.chunks.length, 2)
  assert.equal(page.pageSize, 2)
})

test('ask creates a session, streams the answer, and collects citations', async () => {
  const { mock, client } = await harness()
  const sessionId = await client.createSession('dsh: 阈值', never)
  const answer = await client.ask(
    { sessionId, query: '默认的检索阈值是多少', knowledgeBaseIds: ['kb-product'], agentId: undefined, webSearch: false },
    never,
  )
  assert.equal(answer.sessionId, 'session-mock-1')
  assert.match(answer.answer, /vector_threshold|向量/)
  assert.ok(answer.references.length > 0)
  assert.deepEqual(answer.toolCalls, [])
  assert.equal(mock.requests.at(-1).path, `/api/v1/knowledge-chat/${sessionId}`)
  assert.equal(mock.requests.at(-1).body.channel, 'api')
})

test('an agent id routes to agent-chat and reports the server-side tools', async () => {
  const { mock, client } = await harness()
  const answer = await client.ask(
    { sessionId: 's1', query: '部署方式有哪些', knowledgeBaseIds: [], agentId: 'agent-9', webSearch: true },
    never,
  )
  assert.equal(mock.requests.at(-1).path, '/api/v1/agent-chat/s1')
  assert.equal(mock.requests.at(-1).body.agent_id, 'agent-9')
  assert.equal(mock.requests.at(-1).body.agent_enabled, true)
  assert.equal(mock.requests.at(-1).body.web_search_enabled, true)
  assert.deepEqual(answer.toolCalls, ['knowledge_search'])
})

test('a streamed error event becomes a failed call, not an empty answer', async () => {
  const { client } = await harness({}, { streamError: true })
  await assert.rejects(
    client.ask({ sessionId: 's1', query: 'x', knowledgeBaseIds: [], agentId: undefined, webSearch: false }, never),
    /streamed an error: model provider unavailable/,
  )
})

// A stream that stops before `complete` was cut short. Returning what arrived
// would present a truncated answer to the model as a whole one.
test('a stream that ends before completing becomes a failed call', async () => {
  const { client } = await harness({}, { streamTruncated: true })
  await assert.rejects(
    client.ask(
      { sessionId: 's1', query: '默认的检索阈值是多少', knowledgeBaseIds: ['kb-product'], agentId: undefined, webSearch: false },
      never,
    ),
    /ended before GENYEE completed the answer/,
  )
})

test('caller cancellation aborts an in-flight call', async () => {
  const { client } = await harness()
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(client.listKnowledgeBases(controller.signal), /the call was cancelled/)
})

test('an unreachable backend reports a transport failure', async () => {
  const client = new GenyeeClient(resolveConfig({ baseUrl: 'http://127.0.0.1:1/api/v1', requestTimeoutMs: 1000 }))
  await assert.rejects(client.listKnowledgeBases(never), GenyeeApiError)
})

test('blocks dot-segment and slash path traversal in document and session IDs before network I/O', async () => {
  const { mock, client } = await harness({ apiKey: 'secret-key' }, { apiKey: 'secret-key' })
  const before = mock.requests.length
  for (const bad of ['', '   ', '.', '..', '../tenants/1/api-keys', 'a/b', 'a\\b', 'a?b=1', 'a#frag', 'a%2fb']) {
    await assert.rejects(client.getDocument(bad, never), GenyeeApiError)
    await assert.rejects(client.listChunks({ knowledgeId: bad, page: 1, pageSize: 10 }, never), GenyeeApiError)
    await assert.rejects(
      client.ask({ sessionId: bad, query: 'q', knowledgeBaseIds: [], agentId: undefined, webSearch: false }, never),
      GenyeeApiError,
    )
  }
  assert.equal(mock.requests.length, before)
})

test('refuses cross-origin redirects so X-API-Key and X-Tenant-ID never leak', async () => {
  const http = await import('node:http')
  let leakedHeaders = null
  const evil = http.createServer((req, res) => {
    leakedHeaders = req.headers
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end('{"data":[]}')
  })
  await new Promise(resolve => evil.listen(0, '127.0.0.1', resolve))
  const evilPort = evil.address().port

  const redirector = http.createServer((req, res) => {
    res.writeHead(302, { Location: `http://127.0.0.1:${evilPort}/captured` })
    res.end()
  })
  await new Promise(resolve => redirector.listen(0, '127.0.0.1', resolve))
  const redirectPort = redirector.address().port

  try {
    const client = new GenyeeClient(
      resolveConfig({
        baseUrl: `http://127.0.0.1:${redirectPort}/api/v1`,
        apiKey: 'sk-must-not-leak',
        tenantId: '42',
      }),
    )
    await assert.rejects(client.listKnowledgeBases(never), GenyeeApiError)
    assert.equal(leakedHeaders, null, 'redirect target must never receive a request or API key')
  } finally {
    evil.close()
    redirector.close()
  }
})

test('W7-DSH-02: rejects oversized JSON responses and handles multi-line SSE frames while rejecting unterminated frames', async () => {
  const http = await import('node:http')
  const server = http.createServer((req, res) => {
    if (req.url?.startsWith('/api/v1/knowledge-bases')) {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.write('{"success":true,"data":["')
      res.write('x'.repeat(17 * 1024 * 1024))
      res.end('"]}')
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/multiline')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write(': comment line\n')
      res.write('event: heartbeat\ndata: {"response_type":"error","content":"must be ignored"}\n\n')
      res.write('data: {"response_type":"answer",\ndata: "content":"multi-line answer","session_id":"multiline"}\n\n')
      res.write('data: {"response_type":"complete","done":true,"session_id":"multiline"}\n\n')
      res.end()
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/unterminated')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write('data: {"response_type":"answer","content":"partial","session_id":"unterminated"}\n\n')
      // Missing trailing blank line on the final frame (e.g. mid-frame connection drop)
      res.write('data: {"response_type":"complete","done":true,"session_id":"unterminated"}')
      res.end()
      return
    }
    res.writeHead(404)
    res.end()
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  try {
    const client = new GenyeeClient(resolveConfig({ baseUrl: `http://127.0.0.1:${port}/api/v1` }))
    await assert.rejects(client.listKnowledgeBases(never), /exceeded .* bytes/)

    const multiline = await client.ask(
      { sessionId: 'multiline', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false },
      never,
    )
    assert.equal(multiline.answer, 'multi-line answer')

    await assert.rejects(
      client.ask({ sessionId: 'unterminated', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false }, never),
      /ended before GENYEE completed the answer/,
    )
  } finally {
    server.close()
  }
})

test('W7-DSH-03: GenyeeApiError redacts configured API keys and bearer/gy_ tokens echoed by servers', async () => {
  const http = await import('node:http')
  const rawKey = 'custom-secret-token-98765'
  const gyKey = 'gy_live_secret_token_abcdef123456'
  const server = http.createServer((req, res) => {
    res.writeHead(401, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      error: { message: `rejected key ${rawKey} and ${gyKey} and Bearer eyJhbGciOiJIUzI1NiJ9.secret` },
    }))
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  try {
    const client = new GenyeeClient(resolveConfig({
      baseUrl: `http://127.0.0.1:${port}/api/v1`,
      apiKey: rawKey,
    }))
    await assert.rejects(client.listKnowledgeBases(never), error => {
      assert.ok(error instanceof GenyeeApiError)
      assert.equal(error.message.includes(rawKey), false, 'configured apiKey must be redacted')
      assert.equal(error.message.includes(gyKey), false, 'gy_ token must be redacted')
      assert.equal(error.message.includes('eyJhbGciOiJIUzI1NiJ9.secret'), false, 'Bearer token must be redacted')
      assert.match(error.message, /\[REDACTED\]/)
      return true
    })
  } finally {
    server.closeAllConnections?.()
    server.close()
  }
})

test('W8-DSH-01: mid-body JSON and SSE stream timeouts surface GenyeeApiError with TimeoutError cause, and custom abort reasons cancel cleanly', async () => {
  const http = await import('node:http')
  const server = http.createServer((req, res) => {
    if (req.url?.startsWith('/api/v1/knowledge-bases')) {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.write('{"success":true,"data":[')
      // Intentionally stall past requestTimeoutMs after headers flush
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/stall')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write('data: {"response_type":"answer","content":"partial","session_id":"stall"}\n\n')
      // Intentionally stall past chatTimeoutMs mid-stream
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/cancel-custom')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write('data: {"response_type":"answer","content":"started","session_id":"cancel-custom"}\n\n')
      return
    }
    res.writeHead(404)
    res.end()
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  try {
    const client = new GenyeeClient(resolveConfig({
      baseUrl: `http://127.0.0.1:${port}/api/v1`,
      requestTimeoutMs: 60,
      chatTimeoutMs: 60,
    }))
    await assert.rejects(client.listKnowledgeBases(never), error => {
      assert.ok(error instanceof GenyeeApiError, `expected GenyeeApiError, got ${error?.constructor?.name}`)
      assert.match(error.message, /the request timed out/)
      assert.equal(error.cause?.name, 'TimeoutError')
      return true
    })
    await assert.rejects(
      client.ask({ sessionId: 'stall', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false }, never),
      error => {
        assert.ok(error instanceof GenyeeApiError)
        assert.match(error.message, /the request timed out/)
        assert.equal(error.cause?.name, 'TimeoutError')
        return true
      },
    )

    const slowClient = new GenyeeClient(resolveConfig({
      baseUrl: `http://127.0.0.1:${port}/api/v1`,
      chatTimeoutMs: 10_000,
    }))
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort('user pressed stop'), 40)
    try {
      await assert.rejects(
        slowClient.ask(
          { sessionId: 'cancel-custom', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false },
          controller.signal,
        ),
        error => {
          assert.ok(error instanceof GenyeeApiError)
          assert.match(error.message, /the call was cancelled/)
          return true
        },
      )
    } finally {
      clearTimeout(timer)
    }
  } finally {
    server.closeAllConnections?.()
    server.close()
  }
})

test('W8-DSH-02: guards non-object JSON envelopes, null/empty SSE frames, multibyte SSE line limits, and lone-surrogate path segments', async () => {
  const http = await import('node:http')
  const server = http.createServer((req, res) => {
    if (req.url?.startsWith('/api/v1/knowledge-bases')) {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end('null')
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/null-frame')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write('data: null\n\n')
      res.write('data: 42\n\n')
      res.write('data: {"response_type":"answer","content":"ok","session_id":"null-frame"}\n\n')
      res.write('data: {"response_type":"complete","done":true,"session_id":"null-frame"}\n\n')
      res.end()
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/empty-error')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write('data: {"response_type":"error","content":"","done":true}\n\n')
      res.end()
      return
    }
    if (req.url?.startsWith('/api/v1/knowledge-chat/multibyte-overflow')) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      // 1.5M 3-byte CJK chars = 4.5 MiB UTF-8 (> 4 MiB MAX_SSE_EVENT_BYTES), but only 1.5M UTF-16 code units (< 4M)
      res.write(`data: ${'中'.repeat(1_500_000)}`)
      res.end()
      return
    }
    res.writeHead(404)
    res.end()
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  try {
    const client = new GenyeeClient(resolveConfig({ baseUrl: `http://127.0.0.1:${port}/api/v1` }))

    await assert.rejects(client.listKnowledgeBases(never), error => {
      assert.ok(error instanceof GenyeeApiError, `expected GenyeeApiError on JSON null, got ${error?.constructor?.name}`)
      return true
    })

    const recovered = await client.ask(
      { sessionId: 'null-frame', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false },
      never,
    )
    assert.equal(recovered.answer, 'ok')

    await assert.rejects(
      client.ask({ sessionId: 'empty-error', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false }, never),
      /streamed an error: unknown error/,
    )

    await assert.rejects(
      client.ask(
        { sessionId: 'multibyte-overflow', query: 'q', knowledgeBaseIds: ['kb-1'], agentId: undefined, webSearch: false },
        never,
      ),
      /SSE event exceeded/,
    )

    await assert.rejects(client.getDocument('doc-\uD800', never), error => {
      assert.ok(error instanceof GenyeeApiError, `expected GenyeeApiError on lone surrogate, got ${error?.constructor?.name}`)
      return true
    })
  } finally {
    server.closeAllConnections?.()
    server.close()
  }
})
