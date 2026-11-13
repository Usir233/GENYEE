import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import type { relayEmbedWebhookEvent as Relay } from './index.ts'

const compiled = ts.transpileModule(readFileSync(new URL('./index.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

test('webhook API submits only message identity and reports best-effort failures', async () => {
  const calls: unknown[][] = []
  let failure = false
  const exports: { relayEmbedWebhookEvent?: typeof Relay } = {}
  vm.runInNewContext(compiled, { exports, require: (id: string) => {
    if (id === '@/utils/embedBaseUrl') return {}
    assert.equal(id, '@/utils/request')
    return { post: (...args: unknown[]) => {
      calls.push(args)
      return failure ? Promise.reject(new Error('offline')) : Promise.resolve({ success: true })
    } }
  } })
  const relay = exports.relayEmbedWebhookEvent!
  assert.equal(await relay('channel/path', 'token', 'session/path', 'sig', { type: 'message_sent', message_id: 'user-1' }), true)
  assert.deepEqual(JSON.parse(JSON.stringify(calls[0])), [
    '/api/v1/embed/channel%2Fpath/sessions/session%2Fpath/events',
    { type: 'message_sent', session_id: 'session/path', message_id: 'user-1' },
    { headers: { Authorization: 'Embed token', 'X-Embed-Session': 'sig' } },
  ])
  failure = true
  assert.equal(await relay('channel', 'token', 'session', 'sig', { type: 'message_received', message_id: 'assistant-1' }), false)
})
