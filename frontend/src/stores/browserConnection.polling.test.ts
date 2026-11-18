import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as pinia from 'pinia'
import type { BrowserAccountStatus } from './browserConnection'

const compiled = ts.transpileModule(readFileSync(new URL('./browserConnection.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const flush = () => new Promise<void>(resolve => setImmediate(resolve))
const status = (connected = true): BrowserAccountStatus => ({ enabled: true, connected, extension_available: true })
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}
function fixture(getter?: () => Promise<{ data: BrowserAccountStatus }>) {
  const exports: { useBrowserConnectionStore?: (piniaInstance?: pinia.Pinia) => ReturnType<typeof import('./browserConnection')['useBrowserConnectionStore']> } = {}
  const timers = new Map<number, { run: () => void; delay: number }>()
  const listeners = new Set<() => void>(), requests: Array<AbortSignal | undefined> = []
  let id = 0
  const document = {
    hidden: false,
    addEventListener: (_event: string, handler: () => void) => listeners.add(handler),
    removeEventListener: (_event: string, handler: () => void) => listeners.delete(handler),
  }
  runInNewContext(compiled, {
    exports, AbortController, document,
    setTimeout: (run: () => void, delay: number) => { timers.set(++id, { run, delay }); return id },
    clearTimeout: (timer: number) => timers.delete(timer),
    require(name: string) {
      if (name === 'pinia') return pinia
      if (name === '@/utils/request') return { get: (_path: string, config: { signal?: AbortSignal }) => {
        requests.push(config.signal)
        return getter ? getter() : Promise.resolve({ data: status() })
      } }
      throw new Error(`Unexpected dependency: ${name}`)
    },
  })
  const store = () => exports.useBrowserConnectionStore!(pinia.createPinia())
  return {
    store, timers, listeners, requests,
    show() { document.hidden = false; for (const handler of listeners) handler() },
    hide() { document.hidden = true; for (const handler of listeners) handler() },
    fireTimers() { const scheduled = [...timers.values()]; timers.clear(); scheduled.forEach(timer => timer.run()) },
  }
}

test('visibility refreshes retain one timer and the last unsubscribe removes every poll', async () => {
  const f = fixture(), store = f.store()
  store.watchStatus(); await flush()
  for (let i = 0; i < 4; i++) { f.hide(); f.show(); await flush() }
  const pending = f.timers.size
  store.unwatchStatus()
  const requests = f.requests.length
  f.fireTimers(); await flush()
  assert.equal(pending, 1)
  assert.equal(f.timers.size, 0)
  assert.equal(f.requests.length, requests)
  assert.equal(f.listeners.size, 0)
})

test('visibility events during a poll share the pending status request', async () => {
  const response = deferred<{ data: BrowserAccountStatus }>(), f = fixture(() => response.promise), store = f.store()
  store.watchStatus()
  for (let i = 0; i < 4; i++) { f.hide(); f.show() }
  const calls = f.requests.length
  response.resolve({ data: status() }); await flush()
  const timers = f.timers.size
  store.unwatchStatus()
  assert.equal(calls, 1)
  assert.equal(timers, 1)
})

test('stopping and restarting polling ignores the old response and old scheduling continuation', async () => {
  const responses: Array<ReturnType<typeof deferred<{ data: BrowserAccountStatus }>>> = []
  const f = fixture(() => { const response = deferred<{ data: BrowserAccountStatus }>(); responses.push(response); return response.promise })
  const store = f.store()
  store.watchStatus(); store.unwatchStatus(); store.watchStatus()
  responses[1].resolve({ data: status(true) }); await flush()
  responses[0].resolve({ data: status(false) }); await flush()
  const online = store.online, pending = f.timers.size
  store.unwatchStatus()
  assert.equal(f.requests[0]?.aborted, true)
  assert.equal(online, true)
  assert.equal(pending, 1)
  assert.equal(f.timers.size, 0)
})

test('two subscribers share a loop until the final unsubscribe', async () => {
  const f = fixture(), store = f.store()
  store.watchStatus(); store.watchStatus(); await flush()
  store.unwatchStatus()
  assert.equal(f.requests.length, 1)
  assert.equal(f.timers.size, 1)
  assert.equal(f.requests[0]?.aborted, false)
  store.unwatchStatus()
  assert.equal(f.timers.size, 0)
  assert.equal(f.requests[0]?.aborted, true)
})

test('failed status polls keep one retry and resume after visibility changes', async () => {
  const f = fixture(async () => { throw new Error('offline') }), store = f.store()
  store.watchStatus(); await flush()
  f.hide(); f.show(); await flush()
  const delays = [...f.timers.values()].map(timer => timer.delay)
  f.fireTimers(); await flush()
  const pending = f.timers.size
  store.unwatchStatus()
  assert.deepEqual(delays, [3000])
  assert.equal(pending, 1)
})

test('independent store instances do not cancel or orphan each other\'s timers and listeners', async () => {
  const f = fixture(), first = f.store(), second = f.store()
  first.watchStatus(); second.watchStatus(); await flush()
  first.unwatchStatus()
  assert.equal(f.listeners.size, 1)
  assert.equal(f.timers.size, 1)
  f.hide(); f.show(); await flush()
  second.unwatchStatus()
  assert.equal(f.listeners.size, 0)
  assert.equal(f.timers.size, 0)
})

test('a late status poll does not undo a newer pairing response applied by settings', async () => {
  const response = deferred<{ data: BrowserAccountStatus }>(), f = fixture(() => response.promise), store = f.store()
  store.watchStatus()
  store.apply(status(true))
  response.resolve({ data: status(false) }); await flush()
  const online = store.online
  store.unwatchStatus()
  assert.equal(online, true)
})
