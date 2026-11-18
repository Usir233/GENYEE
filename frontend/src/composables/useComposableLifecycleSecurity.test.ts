import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'

function transpile(relPath: string): string {
  const src = readFileSync(new URL(relPath, import.meta.url), 'utf8')
    .replaceAll('import.meta.env.BASE_URL', "'/'")
  return ts.transpileModule(src, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
}

const flush = async () => {
  await Promise.resolve()
  await vue.nextTick()
  await new Promise<void>((r) => setImmediate(r))
}

test('useCitationPopover blocks javascript: and data: URLs in openWeb while keeping https:', async () => {
  const code = transpile('./useCitationPopover.ts')
  const exports: any = {}
  const listeners = new Map<string, (e: any) => void>()
  const rootEl = {
    addEventListener: (name: string, fn: (e: any) => void) => listeners.set(name, fn),
    removeEventListener: (name: string) => listeners.delete(name),
  }
  vm.runInNewContext(code, {
    exports,
    URL,
    Date,
    window: {
      scrollX: 0,
      scrollY: 0,
      innerWidth: 1200,
      addEventListener: () => {},
      removeEventListener: () => {},
      setTimeout: (cb: () => void) => { cb(); return 1 },
      clearTimeout: () => {},
    },
    document: { querySelector: () => null },
    require(id: string) {
      if (id === 'vue') return { ...vue, onBeforeUnmount: () => {}, onMounted: (cb: () => void) => cb() }
      if (id === '@/utils/citationMarkdown') return { resolveCitationChunkId: () => '' }
      if (id === '@/utils/citationChunkCache') return { getCitationChunkCache: () => null, setCitationChunkCache: () => {} }
      if (id === '@/utils/citationAnchor') return { citationAnchorText: () => '' }
      if (id === './useChatReferencesDrawer' || id === '@/composables/useChatReferencesDrawer') {
        return { useChatReferencesDrawer: () => null }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  const rootRef = vue.ref<any>(rootEl)
  const pop = exports.useCitationPopover(rootRef, {
    mode: 'chat',
    getCacheScope: () => 's1',
    fetchChunk: async () => ({ data: { content: '' } }),
    loadError: () => 'err',
  })
  await flush()

  const makeTarget = (url: string) => ({
    closest: (sel: string) => (sel.includes('.citation-web') ? {
      classList: { contains: (c: string) => c === 'citation-web' },
      getAttribute: (attr: string) => (attr === 'data-url' ? url : null),
      querySelector: () => ({ textContent: 'Result' }),
      getBoundingClientRect: () => ({ bottom: 10, left: 10 }),
    } : null),
  })

  listeners.get('mouseover')!({ target: makeTarget('javascript:alert(1)') })
  assert.equal(pop.float.value.url, '')

  listeners.get('mouseover')!({ target: makeTarget('data:text/html,<script>alert(1)</script>') })
  assert.equal(pop.float.value.url, '')

  listeners.get('mouseover')!({ target: makeTarget('https://docs.example.com/guide') })
  assert.equal(pop.float.value.url, 'https://docs.example.com/guide')
})

test('useSandboxDesktop ignores async disconnect from retired RFB client and aborts stale sessionId ticket', async () => {
  // Replace dynamic import('@novnc/novnc') with CommonJS-friendly global hook in sandbox
  const raw = readFileSync(new URL('./useSandboxDesktop.ts', import.meta.url), 'utf8')
    .replaceAll('import.meta.env.BASE_URL', "'/'")
    .replace("await import('@novnc/novnc')", 'await __importNoVnc()')
  const code = ts.transpileModule(raw, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const rfbInstances: any[] = []
  class FakeRFB {
    handlers = new Map<string, Array<() => void>>()
    disconnected = false
    constructor(public target: any, public socket: any) {
      rfbInstances.push(this)
    }
    addEventListener(name: string, cb: () => void) {
      const list = this.handlers.get(name) || []
      list.push(cb)
      this.handlers.set(name, list)
    }
    removeEventListener() {}
    disconnect() {
      this.disconnected = true
      // Intentionally DO NOT fire 'disconnect' synchronously — real WebSocket close is async!
    }
    emit(name: string) {
      for (const cb of this.handlers.get(name) || []) cb()
    }
  }

  const tickets: Array<{ sid: string; resolve: (t: string) => void }> = []
  const sockets: string[] = []
  class FakeWebSocket {
    static CONNECTING = 0
    static OPEN = 1
    readyState = 1
    constructor(public url: string) { sockets.push(url) }
    addEventListener() {}
    close() { this.readyState = 3 }
  }

  const exports: any = {}
  vm.runInNewContext(code, {
    exports,
    URLSearchParams,
    WebSocket: FakeWebSocket,
    __importNoVnc: async () => ({ default: FakeRFB }),
    setTimeout: () => 1,
    clearTimeout: () => {},
    document: { addEventListener: () => {}, removeEventListener: () => {}, activeElement: null },
    window: { location: { protocol: 'http:', host: 'localhost:5173' } },
    require(id: string) {
      if (id === 'vue') return { ...vue, onUnmounted: () => {} }
      if (id === '@/utils/request') {
        return {
          post: (url: string) => new Promise((resolve) => {
            const match = url.match(/\/sessions\/([^/]+)\//)
            tickets.push({ sid: match?.[1] || '', resolve: (t: string) => resolve({ data: { ticket: t } }) })
          }),
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  const sessionId = vue.ref('sid-1')
  const desktop = exports.useSandboxDesktop(
    sessionId,
    vue.ref('agent-1'),
    vue.ref(undefined),
  )
  const fakeContainer = { addEventListener: () => {}, removeEventListener: () => {}, contains: () => false } as any

  desktop.connect(fakeContainer)
  await flush()
  tickets.shift()!.resolve('tok-1')
  await flush()
  assert.equal(rfbInstances.length, 1)
  rfbInstances[0].emit('connect')
  assert.equal(desktop.status.value, 'connected')

  // Re-connect: retires rfbInstances[0], starts a second dial
  desktop.connect(fakeContainer)
  await flush()
  assert.equal(desktop.status.value, 'starting')

  // Retired client fires its async 'disconnect' event LATE: must NOT clobber 'starting' to 'error'!
  rfbInstances[0].emit('disconnect')
  assert.equal(desktop.status.value, 'starting')

  // Now switch sessionId while tok-2 is still in-flight: resolving tok-2 must NOT open a socket for sid-1!
  const socketsBefore = sockets.length
  sessionId.value = 'sid-2'
  tickets.shift()!.resolve('tok-2')
  await flush()
  assert.equal(sockets.length, socketsBefore)
})

test('useSandboxTerminal aborts stale ticket on session switch and scopes stored PTY id to socket sid', async () => {
  const code = transpile('./useSandboxTerminal.ts')
  const tickets: Array<{ sid: string; resolve: (t: string) => void }> = []
  const sockets: FakeWS[] = []
  const sessionStore = new Map<string, string>()

  class FakeWS {
    static OPEN = 1
    readyState = 1
    binaryType = 'blob'
    onopen: (() => void) | null = null
    onmessage: ((ev: any) => void) | null = null
    onclose: ((ev: any) => void) | null = null
    onerror: (() => void) | null = null
    constructor(public url: string) { sockets.push(this) }
    send() {}
    close() { this.readyState = 3 }
  }

  const exports: any = {}
  vm.runInNewContext(code, {
    exports,
    URLSearchParams,
    TextEncoder,
    ArrayBuffer,
    Uint8Array,
    WebSocket: FakeWS,
    sessionStorage: {
      getItem: (k: string) => sessionStore.get(k) ?? null,
      setItem: (k: string, v: string) => { sessionStore.set(k, v) },
      removeItem: (k: string) => { sessionStore.delete(k) },
    },
    setTimeout: () => 1,
    clearTimeout: () => {},
    setInterval: () => 1,
    clearInterval: () => {},
    window: { location: { protocol: 'http:', host: 'localhost:5173' } },
    require(id: string) {
      if (id === 'vue') return { ...vue, onUnmounted: () => {} }
      if (id === '@/utils/request') {
        return {
          post: (url: string) => new Promise((resolve) => {
            const match = url.match(/\/sessions\/([^/]+)\//)
            tickets.push({ sid: match?.[1] || '', resolve: (t: string) => resolve({ data: { ticket: t } }) })
          }),
        }
      }
      if (id === '@/utils/sandboxPtyId') {
        return {
          readStoredPtyId: (sid: string) => Number(sessionStore.get(sid) || 0) || null,
          writeStoredPtyId: (sid: string, pid: number | null) => {
            if (pid == null) sessionStore.delete(sid)
            else sessionStore.set(sid, String(pid))
          },
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  const sessionId = vue.ref('term-sid-1')
  const term = exports.useSandboxTerminal(
    sessionId,
    vue.ref('agent-1'),
    vue.ref(undefined),
  )

  term.connect()
  await flush()
  assert.equal(tickets.length, 1)

  // Session switches BEFORE mintTerminalTicket resolves: must NOT open a WebSocket for term-sid-1!
  sessionId.value = 'term-sid-2'
  tickets.shift()!.resolve('stale-ticket')
  await flush()
  assert.equal(sockets.length, 0)
})

test('useEmbedBridge does not set allowed origins or post ready if unmounted during bootstrap', async () => {
  const raw = readFileSync(new URL('./useEmbedBridge.ts', import.meta.url), 'utf8')
    .replaceAll('import.meta.env.DEV', 'true')
  const code = ts.transpileModule(raw, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  let mountedCb: (() => void) | null = null
  let unmountedCb: (() => void) | null = null
  let resolveConfig!: (v: any) => void
  const allowedOriginsCalls: any[] = []
  const readyCalls: string[] = []

  const exports: any = {}
  const win: any = {
    localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    addEventListener: () => {},
    removeEventListener: () => {},
  }
  win.parent = win

  vm.runInNewContext(code, {
    exports,
    Date,
    Promise,
    window: win,
    require(id: string) {
      if (id === 'vue') {
        return {
          ...vue,
          onMounted: (cb: () => void) => { mountedCb = cb },
          onUnmounted: (cb: () => void) => { unmountedCb = cb },
        }
      }
      if (id === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k, locale: vue.ref('en-US') }) }
      if (id === 'vue-router') return { useRoute: () => ({ params: { channelId: 'ch-1' }, query: { token: 'ese_tok' } }) }
      if (id === '@/api/embed') {
        return {
          createEmbedSession: async () => ({ data: { id: 'new-sess', sig: 'sig-1' } }),
          embedChatSessionStorageKey: (cid: string) => `embed-sess:${cid}`,
          exchangeEmbedSession: async () => ({ data: { session_token: 'ese_tok' } }),
          getEmbedConfig: () => new Promise((r) => { resolveConfig = r }),
          getEmbedMessageList: async () => ({ data: [] }),
          getOrCreateEmbedVisitorId: () => 'vis-1',
          isEmbedSessionToken: () => true,
          onEmbedHostContext: () => () => {},
          onEmbedHostLocale: () => () => {},
          onEmbedHostToken: () => () => {},
          parseEmbedTokenFromLocation: () => '',
          postEmbedBootstrapRequest: () => {},
          postEmbedReady: (id: string) => readyCalls.push(id),
          setEmbedAllowedOrigins: (origins: any) => allowedOriginsCalls.push(origins),
        }
      }
      if (id === '@/utils/embedLocale' || id === '@/i18n/embed') {
        return {
          applyEmbedLocale: () => {},
          readEmbedLocaleFromUrl: () => '',
          syncEmbedLocaleFromUrl: () => {},
        }
      }
      if (id === '@/utils/embedSessionHistory') {
        return {
          clearEmbedHistory: () => [],
          embedHistoryRevisionKey: () => 'rev',
          embedHistoryStorageKey: () => 'hist',
          readEmbedHistory: () => [],
          readEmbedHistoryRevision: () => '0',
          rememberEmbedSession: (_s: any, e: any) => [e],
          saveEmbedHistory: (_s: any, items: any) => items,
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  exports.useEmbedBridge(vue.ref('ch-1'))
  mountedCb!()
  await flush()

  // Unmount while getEmbedConfig is still pending
  unmountedCb!()
  assert.deepEqual(allowedOriginsCalls, [null])

  // Late resolution of getEmbedConfig MUST NOT re-enable allowedOrigins or postEmbedReady!
  resolveConfig({
    success: true,
    data: { agent_id: 'a1', allowed_origins: ['https://host.example'] },
  })
  await flush()
  await flush()

  assert.deepEqual(allowedOriginsCalls, [null])
  assert.deepEqual(readyCalls, [])
})

test('useVoiceInput suppresses onText and onError when component unmounts during transcription', async () => {
  const code = transpile('./useVoiceInput.ts')
  let beforeUnmountCb: (() => void) | null = null
  let now = 1000
  let resolveTranscribe!: (text: string) => void
  let activeRecorder: FakeMediaRecorder | null = null
  const captureRecorder = (rec: FakeMediaRecorder) => {
    activeRecorder = rec
  }

  class FakeMediaRecorder {
    state = 'inactive'
    mimeType = 'audio/webm'
    ondataavailable: ((e: any) => void) | null = null
    onstart: (() => void) | null = null
    onstop: (() => void) | null = null
    start() {
      this.state = 'recording'
      captureRecorder(this)
      this.onstart?.()
    }
    stop() {
      this.state = 'inactive'
      this.ondataavailable?.({ data: new Blob(['audio']) })
      this.onstop?.()
    }
  }

  const exports: any = {}
  vm.runInNewContext(code, {
    exports,
    Blob,
    console: { debug: () => {} },
    Date: { now: () => now },
    navigator: {
      mediaDevices: {
        getUserMedia: async () => ({
          getAudioTracks: () => [{ readyState: 'live' }],
          getTracks: () => [{ stop: () => {} }],
        }),
      },
    },
    MediaRecorder: FakeMediaRecorder,
    OfflineAudioContext: class {},
    window: { isSecureContext: true },
    setTimeout: () => 1,
    clearTimeout: () => {},
    setInterval: () => 1,
    clearInterval: () => {},
    require(id: string) {
      if (id === 'vue') {
        return {
          ...vue,
          getCurrentInstance: () => ({}),
          onBeforeUnmount: (cb: () => void) => { beforeUnmountCb = cb },
        }
      }
      if (id === '../utils/wavEncoder.ts') return { recordingToWav: async () => ({}) }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  const texts: string[] = []
  const errors: string[] = []
  const voice = exports.useVoiceInput({
    target: () => ({ agentId: 'a1' }),
    toWav: async () => ({ wav: new Blob(['wav']), speech: true, stats: {} }),
    transcribe: () => new Promise<string>((r) => { resolveTranscribe = r }),
    onText: (t: string) => texts.push(t),
    onError: (e: string) => errors.push(e),
  })

  await voice.start()
  now += 1200
  activeRecorder.stop()
  await flush()
  assert.equal(voice.state.value, 'transcribing')

  // Unmount BEFORE options.transcribe resolves
  beforeUnmountCb!()
  resolveTranscribe('hello after unmount')
  await flush()

  assert.deepEqual(texts, [])
  assert.deepEqual(errors, [])
})

test('useConfigSkillInstallProgress aborts active SSE controllers on effectScope disposal', async () => {
  const code = transpile('./useConfigSkillInstallProgress.ts')
  const signals: AbortSignal[] = []
  const exports: any = {}

  vm.runInNewContext(code, {
    exports,
    AbortController,
    localStorage: { getItem: () => null },
    require(id: string) {
      if (id === 'vue') return vue
      if (id === '@microsoft/fetch-event-source') {
        return {
          fetchEventSource: (_url: string, opts: { signal: AbortSignal }) => {
            signals.push(opts.signal)
            return new Promise<void>(() => {})
          },
        }
      }
      if (id === '@/api/system') return { configSkillInstallEventsUrl: (c: string, s: string) => `/cfg/${c}/skills/${s}` }
      if (id === '@/i18n') return { __esModule: true, default: { global: { locale: { value: 'en-US' } } } }
      if (id === '@/utils') return { generateRandomString: () => 'req123' }
      if (id === '@/utils/api-base') return { getApiBaseUrl: () => '' }
      if (id === './skillInstallProgress') {
        return {
          clampInstallPercent: (n: number) => n,
          formatBusyInstallStatus: () => '',
          installRunFinished: () => false,
          liveInstallPercent: () => null,
          progressKey: (c: string, s: string) => `${c}:${s}`,
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  const scope = vue.effectScope()
  scope.run(() => {
    const prog = exports.useConfigSkillInstallProgress()
    prog.follow('cfg-1', 'skill-1')
  })
  assert.equal(signals.length, 1)
  assert.equal(signals[0].aborted, false)

  scope.stop()
  assert.equal(signals[0].aborted, true)
})
