import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as historyPagination from '../utils/historyPagination.ts'
import * as suspendedRun from '../utils/suspendedRun.ts'
import * as embedContext from '../utils/embedContext.ts'
import type { useEmbedChatSession as EmbedChatSession } from './useEmbedChatSession.ts'

const require = createRequire(import.meta.url)
const source = readFileSync(new URL('./useEmbedChatSession.ts', import.meta.url), 'utf8')

function createSessionHarness() {
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  let nextTimerId = 1
  const timers = new Map<number, () => void>()
  const unmountedCallbacks: Array<() => void> = []
  const listCalls: Array<{
    sessionId: string
    resolve: (data: unknown[]) => void
    reject: (err: Error) => void
  }> = []
  const startedStreams: unknown[] = []
  const pendingFileReads: Array<{ resolve: (uri: string) => void }> = []

  const exports: { useEmbedChatSession?: typeof EmbedChatSession } = {}
  vm.runInNewContext(compiled, {
    exports,
    console: { error: () => {} },
    Date,
    Promise,
    setTimeout: (cb: () => void) => {
      const id = nextTimerId++
      timers.set(id, cb)
      return id
    },
    clearTimeout: (id: number) => {
      timers.delete(id)
    },
    require: (id: string) => {
      if (id === 'vue') {
        return {
          ...vue,
          provide: () => {},
          onMounted: (cb: () => void) => cb(),
          onUnmounted: (cb: () => void) => unmountedCallbacks.push(cb),
        }
      }
      if (id === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (id === '@/utils/historyPagination') return historyPagination
      if (id === '@/utils/suspendedRun') return suspendedRun
      if (id === '@/utils/embedToast') return { embedToast: () => {} }
      if (id === '@/utils/embedContext') return embedContext
      if (id === '@/utils/embedFile') {
        return {
          fileToDataURI: () => new Promise<string>((resolve) => {
            pendingFileReads.push({ resolve })
          }),
        }
      }
      if (id === '@/utils/index') return { MAX_FILE_SIZE_BYTES: 50 * 1024 * 1024, MAX_FILE_SIZE_MB: 50 }
      if (id === '@/utils/inlineUploadSize') return require('../utils/inlineUploadSize.ts')
      if (id === '@/composables/useStickyBottomOnResize') return { useStickyBottomOnResize: () => {} }
      if (id === '@/api/chat/stream') {
        return {
          useStream: () => ({
            onChunk: () => {},
            error: vue.ref(''),
            startStream: async (payload: unknown) => { startedStreams.push(payload) },
            stopStream: () => {},
          }),
        }
      }
      if (id === '@/api/embed') {
        return {
          getEmbedMessageList: (_ch: string, _tok: string, sessionId: string) =>
            new Promise((resolve, reject) => {
              listCalls.push({
                sessionId,
                resolve: (data) => resolve({ data }),
                reject,
              })
            }),
          postEmbedMessageSent: () => {},
          postEmbedMessageReceived: () => {},
          relayEmbedWebhookEvent: async () => true,
          stopEmbedSession: async () => {},
        }
      }
      if (id === '@/composables/useChatStreamHandler') {
        return {
          useChatStreamHandler: () => ({
            shouldRenderAssistantMessage: () => true,
            shouldShowGlobalTypingIndicator: vue.computed(() => false),
            handleMsgList: async () => {},
            processStreamChunk: () => {},
            prepareForNewOutgoingMessage: () => {},
            prepareResumedMessage: () => {},
            markInFlightAssistantStopped: () => {},
          }),
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  return {
    useEmbedChatSession: exports.useEmbedChatSession!,
    timers,
    listCalls,
    startedStreams,
    pendingFileReads,
    unmountAll: () => unmountedCallbacks.forEach(cb => cb()),
  }
}

const flush = async () => {
  await Promise.resolve()
  await vue.nextTick()
  await new Promise<void>((resolve) => setImmediate(resolve))
}

test('stale getmsgList settlement from session-a does not clear session-b historyLoading or cancel debounced scroll missing', async () => {
  const { useEmbedChatSession, timers, listCalls } = createSessionHarness()
  const sessionId = vue.ref('session-a')
  const sessionSig = vue.ref('sig-a')
  const visitorId = vue.ref('vis-1')

  const chat = useEmbedChatSession({
    channelId: 'ch-1',
    token: 'tok-1',
    sessionId,
    sessionSig,
    visitorId,
    kbIds: [],
    agentId: 'agent-1',
    allowFileUpload: true,
  })

  assert.equal(listCalls.length, 1)
  assert.equal(listCalls[0].sessionId, 'session-a')
  assert.equal(chat.historyLoading.value, true)

  // Trigger a scroll event on session-a (schedules 500ms debouncedScrollTop)
  chat.scrollContainer.value = { scrollTop: 0, scrollHeight: 400, clientHeight: 200 } as unknown as HTMLElement
  chat.handleScroll()
  assert.ok(timers.size > 0, 'expected debounced scroll timer to be scheduled')

  // Switch to session-b BEFORE session-a's getEmbedMessageList settles or scroll timer fires
  sessionId.value = 'session-b'
  sessionSig.value = 'sig-b'
  await flush()

  assert.equal(listCalls.length, 2)
  assert.equal(listCalls[1].sessionId, 'session-b')
  assert.equal(chat.historyLoading.value, true)
  assert.equal(chat.isFirstEnter.value, true)

  // Fire any remaining timers from before the switch: must NOT flip isFirstEnter or trigger a 3rd list call
  for (const [, cb] of [...timers]) cb()
  timers.clear()
  assert.equal(chat.isFirstEnter.value, true)
  assert.equal(listCalls.length, 2)

  // Stale session-a request rejects: must NOT clear session-b's historyLoading!
  listCalls[0].reject(new Error('session-a aborted'))
  await flush()
  assert.equal(chat.historyLoading.value, true)

  // Now session-b resolves cleanly
  listCalls[1].resolve([])
  await flush()
  assert.equal(chat.historyLoading.value, false)
})

test('sendMsg aborts before pushing message or starting stream when session switches during fileToDataURI', async () => {
  const { useEmbedChatSession, listCalls, pendingFileReads, startedStreams } = createSessionHarness()
  const sessionId = vue.ref('session-a')
  const sessionSig = vue.ref('sig-a')
  const visitorId = vue.ref('vis-1')

  const chat = useEmbedChatSession({
    channelId: 'ch-1',
    token: 'tok-1',
    sessionId,
    sessionSig,
    visitorId,
    kbIds: [],
    agentId: 'agent-1',
    allowFileUpload: true,
  })
  listCalls[0].resolve([])
  await flush()

  const fakeFile = new File(['hello'], 'note.txt', { type: 'text/plain' })
  const sendPromise = chat.sendMsg('hello from a', { attachmentFiles: [fakeFile] })
  await flush()
  assert.equal(pendingFileReads.length, 1)

  // Switch session while FileReader is still pending
  sessionId.value = 'session-b'
  sessionSig.value = 'sig-b'
  await flush()

  pendingFileReads[0].resolve('data:text/plain;base64,aGVsbG8=')
  await sendPromise

  assert.equal(chat.messagesList.length, 0)
  assert.equal(startedStreams.length, 0)
})
