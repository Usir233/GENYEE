import assert from 'node:assert/strict'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import type { ProtectedFileAccessContext } from './protectedFileAccess'

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

for (const base of ['/', '/genyee/', '/apps/genyee/']) {
  test(`protected file requests and hydration use the configured Vite base ${base}`, async t => {
    const server = await createServer({
      configFile: false,
      root: fileURLToPath(new URL('../../', import.meta.url)),
      base,
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
      location: { origin: 'http://localhost', pathname: `${base}platform/chat/session` }, addEventListener() {},
    })
    installGlobal('localStorage', {
      getItem: (key: string) => ({ genyee_token: 'jwt', genyee_selected_tenant_id: '1001' } as Record<string, string>)[key] || null,
    })
    const access = await server.ssrLoadModule('/src/utils/protectedFileAccess.ts') as typeof import('./protectedFileAccess')
    const security = await server.ssrLoadModule('/src/utils/security.ts') as typeof import('./security')
    t.after(() => security.clearProtectedFileCache())
    const calls: Array<{ url: string; headers: Record<string, string> }> = []
    t.mock.method(globalThis, 'fetch', async (url: Parameters<typeof fetch>[0], init?: RequestInit) => {
      calls.push({ url: String(url), headers: init?.headers as Record<string, string> })
      return new Response(new Blob(['image bytes'], { type: 'image/png' }))
    })
    const cases: Array<{ context: ProtectedFileAccessContext; path: string; authorization: string }> = [
      { context: { mode: 'tenant' }, path: '/files', authorization: 'Bearer jwt' },
      { context: { mode: 'knowledgeBase', kbId: 'kb' }, path: '/api/v1/knowledge-bases/kb/files', authorization: 'Bearer jwt' },
      { context: { mode: 'message', sessionId: 'session', messageId: 'message' }, path: '/api/v1/sessions/session/messages/message/files', authorization: 'Bearer jwt' },
      { context: { mode: 'embed', channelId: 'channel', token: 'visitor' }, path: '/api/v1/embed/channel/files', authorization: 'Embed visitor' },
    ]
    for (const [index, { context, path, authorization }] of cases.entries()) {
      const source = `local://1001/exports/image-${index}.png`
      const expected = `${base.slice(0, -1)}${path}?${new URLSearchParams({ file_path: source })}`
      const request = access.buildProtectedFileRequest(source, context)!
      assert.equal(request.url, expected)
      assert.equal(request.headers.Authorization, authorization)
      assert.equal(request.headers['X-Tenant-ID'], context.mode === 'embed' ? undefined : '1001')
      const { root, image } = imageRoot(source)
      await security.hydrateProtectedFileImages(root as unknown as ParentNode, context)
      assert.equal(calls.at(-1)?.url, expected)
      assert.equal(calls.at(-1)?.headers.Authorization, authorization)
      assert.match(image.src, /^blob:/)
      assert.equal(access.isProtectedFileProxyPath(`${base.slice(0, -1)}${path}`), true)
    }
    assert.equal(calls.length, 4)
    assert.equal(access.isProtectedFileProxyPath(`${base.slice(0, -1)}other/files`), false)
  })
}
