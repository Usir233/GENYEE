import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test, { type TestContext } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import type { useEmbedChatSession as EmbedChatSession } from './useEmbedChatSession.ts'

const require = createRequire(import.meta.url)
const compile = (path: string) => ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const compiled = compile('./useEmbedChatSession.ts')
const streamCompiled = compile('./useChatStreamHandler.ts')
const flush = async () => { await vue.nextTick(); await new Promise<void>(resolve => setImmediate(resolve)) }

// Use the production embed and stream state machines; stub network and mounting.
function setup(t: TestContext, agentId = 'agent', initialHistory: Record<string, unknown>[] = []) {
  const effects = vue.effectScope()
  t.after(() => effects.stop())
  const sessionId = vue.ref('session-1'), sessionSig = vue.ref('signature-1'), error = vue.ref('')
  const history = { rows: initialHistory }
  const relays: Array<{ sessionId: string; sessionSig: string; type: string; message_id: string }> = []
  const starts: Array<Record<string, unknown>> = [], sent: string[] = []
  const timers = new Map<number, () => unknown>()
  let timerId = 0, relaySucceeds = true
  let onChunk: (chunk: Record<string, unknown>) => void = () => {}
  let resume: (pendingId: string) => void = () => {}
  const modules: Record<string, unknown> = {
    vue: { ...vue, onMounted: (callback: () => void) => callback(), onUnmounted() {}, provide: (_key: unknown, callback: typeof resume) => { resume = callback } },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/utils/embedToast': { embedToast() {} },
    '@/utils/index': { MAX_FILE_SIZE_BYTES: 50 * 1024 * 1024, MAX_FILE_SIZE_MB: 50 },
    '@/utils/embedFile': { fileToDataURI: async () => '' },
    '@/composables/useStickyBottomOnResize': { useStickyBottomOnResize() {} },
    '@/api/chat/streame': { useStream: () => ({
      onChunk: (callback: typeof onChunk) => { onChunk = callback }, error,
      startStream: async (params: Record<string, unknown>) => { starts.push(params) }, stopStream() {},
    }) },
    '@/api/embed': {
      getEmbedMessageList: async () => ({ data: history.rows.map(row => ({ ...row })) }),
      postEmbedMessageSent: (_channel: string, _session: string, query: string) => sent.push(query),
      postEmbedMessageReceived() {}, stopEmbedSession: async () => {},
      relayEmbedWebhookEvent: async (_channel: string, _token: string, sid: string, sig: string, body: { type: string; message_id: string }) => {
        relays.push({ sessionId: sid, sessionSig: sig, ...body }); return relaySucceeds
      },
    },
  }
  for (const path of ['historyPagination', 'suspendedRun', 'embedContext', 'inlineUploadSize', 'finalArtifactContent', 'rag-pipeline-history', 'messageTimestamp', 'steerStreamFork', 'assistantDataChange', 'thinkBlocks']) {
    modules[`@/utils/${path}`] = require(`../utils/${path}.ts`)
  }
  modules['@/views/chat/components/hostApproval'] = require('../views/chat/components/hostApproval.ts')
  const load = (code: string) => {
    const exports: Record<string, any> = {}
    vm.runInNewContext(code, {
      exports, console: { log() {}, warn() {}, error() {} },
      setTimeout: (callback: () => unknown) => { timers.set(++timerId, callback); return timerId },
      clearTimeout: (id: number) => timers.delete(id),
      require: (id: string) => { assert.ok(id in modules, `unexpected import ${id}`); return modules[id] },
    })
    return exports
  }
  modules['@/composables/useChatStreamHandler'] = load(streamCompiled)
  const useEmbedChatSession = load(compiled).useEmbedChatSession as typeof EmbedChatSession
  const session = effects.run(() => useEmbedChatSession({
    sessionId, sessionSig, visitorId: vue.ref('visitor'), channelId: 'channel', token: 'token', agentId, kbIds: [],
    hostContext: vue.ref({ userId: 21 }),
  }))!
  const emit = (response_type: string, data: Record<string, unknown> = {}, extras: Record<string, unknown> = {}) => {
    onChunk({ response_type, id: 'request-1', session_id: sessionId.value, data, ...extras })
  }
  const ack = (user = 'user-1', assistant = 'assistant-1', request = 'request-1') => emit('agent_query', {
    user_message_id: user, assistant_message_id: assistant, session_id: sessionId.value,
  }, { assistant_message_id: assistant, id: request })
  const reload = async () => {
    sessionId.value = 'another-session'; await flush()
    sessionId.value = 'session-1'; await flush()
  }
  return { session, error, history, relays, starts, sent, emit, ack, reload,
    setRelaySuccess: (success: boolean) => { relaySucceeds = success }, resume: (id: string) => resume(id),
    runTimer: async () => {
      const next = timers.entries().next().value
      assert.ok(next, 'expected resumed-history poll')
      timers.delete(next[0]); await next[1](); await flush()
    },
  }
}

for (const mode of ['agent', 'builtin-quick-answer']) {
  test(`${mode}: persisted IDs drive relays with host context and repeated queries`, async t => {
    const h = setup(t, mode)
    await flush(); await h.session.sendMsg('hello')
    assert.equal(h.relays.length, 0, 'optimistic UI cannot relay unpersisted messages')
    assert.equal(h.starts[0].query, '[Host context]\nuserId: 21\n\nhello')
    assert.equal(h.sent[0], 'hello', 'parent receives the user-visible query')
    h.ack(); h.ack(); await flush(); h.ack()
    assert.deepEqual(h.relays.map(r => [r.type, r.message_id]), [['message_sent', 'user-1']])
    h.emit('answer', { event_id: 'answer-1' }, { content: 'same reply' })
    h.emit('answer', { event_id: 'answer-1' }, { done: true })
    assert.equal(h.relays.length, 1, 'answer.done is not persisted completion')
    h.emit('complete', { final_content: 'same reply' }, { done: true })
    await flush(); h.emit('complete', { final_content: 'same reply' }, { done: true })
    assert.deepEqual(h.relays.map(r => [r.type, r.message_id]), [['message_sent', 'user-1'], ['message_received', 'assistant-1']])
    await h.session.sendMsg('hello')
    assert.equal(h.relays.length, 2)
    h.ack('user-2', 'assistant-2', 'request-2')
    h.emit('complete', { final_content: 'same reply' }, { id: 'request-2', done: true })
    await flush()
    assert.deepEqual(h.relays.map(r => r.message_id), ['user-1', 'assistant-1', 'user-2', 'assistant-2'])
    assert.ok(h.relays.every(r => r.sessionId === 'session-1' && r.sessionSig === 'signature-1'))
  })
}

test('errors, suspension and foreign streams cannot announce received; history only recovers pending IDs', async t => {
  const h = setup(t, 'agent', [
    { id: 'old-user', role: 'user', content: 'old query', is_completed: true },
    { id: 'old-assistant', role: 'assistant', content: 'old answer', is_completed: true },
  ])
  await flush()
  assert.equal(h.relays.length, 0, 'initial history must not replay old events')
  await h.session.sendMsg('hello')
  h.emit('agent_query', { user_message_id: 'foreign-user' }, { session_id: 'foreign-session', assistant_message_id: 'foreign-assistant' })
  assert.equal(h.relays.length, 0)
  h.error.value = 'HTTP 503'; await flush()
  assert.equal(h.relays.length, 0, 'rejected request has no accepted message')
  h.error.value = ''; await h.session.sendMsg('hello'); h.ack()
  h.emit('answer', { event_id: 'answer' }, { content: 'partial answer', done: true })
  h.emit('error', { stage: 'message_persistence' }, { content: 'failed to save' })
  await flush()
  assert.deepEqual(h.relays.map(r => r.type), ['message_sent'])
  h.history.rows.push({ id: 'assistant-1', role: 'assistant', content: 'partial answer', is_completed: false })
  await h.reload()
  assert.equal(h.relays.length, 1, 'incomplete history cannot recover received')
  h.history.rows[h.history.rows.length - 1].is_completed = true
  await h.reload()
  assert.deepEqual(h.relays.map(r => r.message_id), ['user-1', 'assistant-1'])
  await h.reload()
  assert.equal(h.relays.length, 2, 'known completion recovers once; old history stays silent')
  await h.session.sendMsg('next'); h.ack('user-2', 'assistant-2', 'request-2')
  h.emit('complete', { final_content: 'need approval', suspended: { pending_id: 'prompt-1' } }, { id: 'request-2', done: true })
  await flush()
  assert.deepEqual(h.relays.map(r => r.message_id), ['user-1', 'assistant-1', 'user-2'])
})

test('failed relays recover by ID without replaying unrelated history', async t => {
  const h = setup(t)
  await flush(); h.setRelaySuccess(false); await h.session.sendMsg('hello'); h.ack(); await flush()
  h.setRelaySuccess(true)
  h.history.rows = [{ id: 'user-1', role: 'user', content: 'hello', is_completed: true }]
  await h.reload()
  assert.deepEqual(h.relays.map(r => r.message_id), ['user-1', 'user-1'])
  await h.reload(); assert.equal(h.relays.length, 2)
})

test('explicit resume relays received only when persisted history completes', async t => {
  const h = setup(t)
  await flush()
  h.session.messagesList.push({ id: 'assistant-resumed', role: 'assistant', content: 'approval needed', is_completed: false,
    run_state: 'awaiting_input', agentEventStream: [{ type: 'tool_approval_required', pending_id: 'prompt-1', suspended: true }] })
  h.history.rows = [{ id: 'assistant-resumed', role: 'assistant', content: 'working', is_completed: false }]
  h.resume('prompt-1'); await h.runTimer()
  assert.equal(h.relays.length, 0)
  h.history.rows[0] = { id: 'assistant-resumed', role: 'assistant', content: 'done', is_completed: true }
  await h.runTimer()
  assert.deepEqual(h.relays.map(r => [r.type, r.message_id]), [['message_received', 'assistant-resumed']])
  await h.reload(); assert.equal(h.relays.length, 1)
})

test('late completions keep their turn ID and stopping a turn suppresses completion recovery', async t => {
  const h = setup(t)
  await flush(); await h.session.sendMsg('first'); h.ack()
  await h.session.sendMsg('second'); h.ack('user-2', 'assistant-2', 'request-2')
  h.emit('complete', { final_content: 'first answer' }, { id: 'request-1', done: true })
  await flush()
  assert.deepEqual(h.relays.map(r => r.message_id), ['user-1', 'user-2', 'assistant-1'])
  h.emit('stop', {}, { id: 'request-2' })
  h.emit('complete', { final_content: 'stopped partial answer' }, { id: 'request-2', done: true })
  h.history.rows = [{ id: 'assistant-2', role: 'assistant', content: 'stopped partial answer', is_completed: true }]
  await h.reload()
  assert.deepEqual(h.relays.map(r => r.message_id), ['user-1', 'user-2', 'assistant-1'])
})
