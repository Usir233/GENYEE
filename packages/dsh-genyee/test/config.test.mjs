import assert from 'node:assert/strict'
import { test } from 'node:test'

import { ConfigError, normalizeBaseUrl, resolveConfig } from '../dist/config.js'

test('base URLs are normalized to an API root', () => {
  assert.equal(normalizeBaseUrl('https://kb.example.com'), 'https://kb.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://kb.example.com/'), 'https://kb.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://kb.example.com/api/v1'), 'https://kb.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://kb.example.com/api/v2/'), 'https://kb.example.com/api/v2')
})

test('an empty config resolves to documented defaults', () => {
  const config = resolveConfig(undefined)
  assert.equal(config.baseUrl, 'http://localhost:8080/api/v1')
  assert.equal(config.maxResults, 8)
  assert.equal(config.toolPrefix, 'genyee')
  assert.equal(config.resourceUrls, 'public')
  assert.deepEqual(config.knowledgeBaseIds, [])
  assert.deepEqual(config.tools, { listKnowledgeBases: true, search: true, readDocument: true, ask: true })
})

test('resourceUrls handle remains an explicit opt-out', () => {
  assert.equal(resolveConfig({ resourceUrls: 'handle' }).resourceUrls, 'handle')
})

test('a bad row fails the load and names every violation', () => {
  assert.throws(
    () => resolveConfig({ baseUrl: 'not-a-url', maxResults: -1, resourceUrls: 'nope', toolPrefix: '9bad' }),
    error => {
      assert.ok(error instanceof ConfigError)
      assert.match(error.message, /baseUrl must be an absolute URL/)
      assert.match(error.message, /maxResults must be a positive number/)
      assert.match(error.message, /resourceUrls must be "handle" or "public"/)
      assert.match(error.message, /toolPrefix must match/)
      return true
    },
  )
})

test('disabling every tool is refused', () => {
  assert.throws(
    () => resolveConfig({ tools: { listKnowledgeBases: false, search: false, readDocument: false, ask: false } }),
    /at least one tool must stay enabled/,
  )
})

test('blank strings collapse to unset rather than empty headers', () => {
  const config = resolveConfig({ apiKey: '   ', tenantId: '', agentId: 'agent-7' })
  assert.equal(config.apiKey, undefined)
  assert.equal(config.tenantId, undefined)
  assert.equal(config.agentId, 'agent-7')
})

test('non-loopback http base URLs are rejected while loopback http and remote https succeed', () => {
  assert.throws(
    () => resolveConfig({ baseUrl: 'http://kb.example.com' }),
    /baseUrl must use https for non-loopback hosts/,
  )
  assert.equal(resolveConfig({ baseUrl: 'http://127.0.0.1:8080' }).baseUrl, 'http://127.0.0.1:8080/api/v1')
  assert.equal(resolveConfig({ baseUrl: 'https://kb.example.com' }).baseUrl, 'https://kb.example.com/api/v1')
})

test('base URLs with query strings or fragments are rejected', () => {
  for (const bad of [
    'https://kb.example.com?foo=bar',
    'https://kb.example.com/?foo=bar',
    'https://kb.example.com?',
    'https://kb.example.com#frag',
    'https://kb.example.com/#frag',
    'https://kb.example.com#',
    'https://kb.example.com/api/v1?x=1',
    'https://kb.example.com/api/v1#frag',
  ]) {
    assert.throws(() => normalizeBaseUrl(bad), /baseUrl must not include query or fragment/)
    assert.throws(() => resolveConfig({ baseUrl: bad }), /baseUrl must not include query or fragment/)
  }
})

test('W7-DSH-03: rejects baseUrl credentials without leaking them and rejects control chars in headers', () => {
  for (const credUrl of ['https://admin:supersecret123@kb.example.com', 'http://user:pass999@127.0.0.1:8080/api/v1']) {
    assert.throws(
      () => normalizeBaseUrl(credUrl),
      error => {
        assert.ok(error instanceof ConfigError)
        assert.match(error.message, /baseUrl must not include credentials/)
        assert.equal(error.message.includes('supersecret123'), false)
        assert.equal(error.message.includes('pass999'), false)
        return true
      },
    )
    assert.throws(
      () => resolveConfig({ baseUrl: credUrl }),
      error => {
        assert.ok(error instanceof ConfigError)
        assert.match(error.message, /baseUrl must not include credentials/)
        assert.equal(error.message.includes('supersecret123'), false)
        assert.equal(error.message.includes('pass999'), false)
        return true
      },
    )
  }

  assert.throws(
    () => resolveConfig({ apiKey: 'gy_bad\r\nX-Evil: 1' }),
    error => {
      assert.ok(error instanceof ConfigError)
      assert.match(error.message, /apiKey must not contain control characters/)
      return true
    },
  )
  assert.throws(
    () => resolveConfig({ tenantId: '42\nX-Injected: 1' }),
    error => {
      assert.ok(error instanceof ConfigError)
      assert.match(error.message, /tenantId must not contain control characters/)
      return true
    },
  )
})
