import assert from 'node:assert/strict'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

// Regression for the protected-file "clear epoch": a fetch that was in flight
// when clearProtectedFileCache() ran (logout / user or tenant switch) must not
// hydrate images or repopulate the cache after the clear, and must not delete
// the in-flight entry of a newer request started after the clear.

function imageRoot(source: string) {
  const attributes: Record<string, string> = { src: source }
  const image: any = {
    dataset: {}, style: { display: '' },
    get src() { return attributes.src }, set src(value: string) { attributes.src = value },
    getAttribute: (key: string) => attributes[key] || '',
    setAttribute: (key: string, value: string) => { attributes[key] = value },
    removeAttribute: (key: string) => { delete attributes[key] },
  }
  return { root: { querySelectorAll: () => [image] }, image }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(r => { resolve = r })
  return { promise, resolve }
}

test('protected file loads that finish after a cache clear are discarded', async t => {
  const server = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('../../', import.meta.url)),
    base: '/',
    optimizeDeps: { noDiscovery: true, entries: [] },
    server: { middlewareMode: true, hmr: false, ws: false, watch: null },
    appType: 'custom',
  })
  t.after(() => server.close())
  const installGlobal = (key: string, value: unknown) => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, key)
    Object.defineProperty(globalThis, key, { value, configurable: true, writable: true })
    t.after(() => { if (previous) Object.defineProperty(globalThis, key, previous); else Reflect.deleteProperty(globalThis, key) })
  }
  installGlobal('window', {
    location: { origin: 'http://localhost', pathname: '/platform/chat/session' }, addEventListener() {},
  })
  installGlobal('localStorage', {
    getItem: (key: string) => ({ genyee_token: 'jwt', genyee_selected_tenant_id: '1001' } as Record<string, string>)[key] || null,
  })
  const security = await server.ssrLoadModule('/src/utils/security.ts') as typeof import('./security')
  security.clearProtectedFileCache()
  t.after(() => security.clearProtectedFileCache())

  const pending: Array<ReturnType<typeof deferred<Response>>> = []
  t.mock.method(globalThis, 'fetch', () => {
    const d = deferred<Response>()
    pending.push(d)
    return d.promise
  })
  const image = (body: string) => new Response(new Blob([body], { type: 'image/png' }))
  const source = 'local://1001/exports/clear-epoch.png'
  const context = { mode: 'tenant' } as const

  // 1. A load is in flight when the cache is cleared.
  const stale = imageRoot(source)
  const staleHydration = security.hydrateProtectedFileImages(stale.root as unknown as ParentNode, context)
  await new Promise(r => setImmediate(r))
  assert.equal(pending.length, 1)
  security.clearProtectedFileCache()

  // 2. A newer load for the same file starts after the clear: it must issue a
  //    fresh request instead of joining the pre-clear task.
  const fresh = imageRoot(source)
  const freshHydration = security.hydrateProtectedFileImages(fresh.root as unknown as ParentNode, context)
  await new Promise(r => setImmediate(r))
  assert.equal(pending.length, 2, 'post-clear hydration must not reuse the pre-clear request')

  // 3. The pre-clear response arrives first and is discarded.
  pending[0]!.resolve(image('old session bytes'))
  await staleHydration
  assert.doesNotMatch(stale.image.src, /^blob:/, 'pre-clear response must not hydrate the image')
  assert.equal(stale.image.dataset.authHydrated, '0')

  // 4. The post-clear response hydrates normally.
  pending[1]!.resolve(image('new session bytes'))
  await freshHydration
  assert.match(fresh.image.src, /^blob:/)

  // 5. The stale task must not have removed the newer task's cache state: a
  //    third occurrence is served from the cache without another request.
  const cached = imageRoot(source)
  await security.hydrateProtectedFileImages(cached.root as unknown as ParentNode, context)
  assert.equal(pending.length, 2, 'cached post-clear blob must be reused')
  assert.match(cached.image.src, /^blob:/)
})
