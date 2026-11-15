import assert from 'node:assert/strict'
import test from 'node:test'
import { marked } from 'marked'
import { renderChatMarkdown } from './chatMarkdownRenderer.ts'
import { renderArtifactFileIcon } from './artifactFileIcon'

import {
  installArtifactLinkRenderer,
  isArtifactRefHref,
  normalizeSandboxArtifactRefs,
  renderArtifactReference,
  resolveArtifactRef,
  type ArtifactRefMeta,
} from './sandboxArtifactRefs.ts'

const labels = { previewHint: '点击预览', missingHint: '文件不可用', deletedHint: '文件已删除' }

test('inline file cards reuse the drawer icon and keep filenames escaped', () => {
  for (const name of ['report.pdf', 'table.xlsx', 'notes.docx', 'slides.pptx', 'chart.html', 'bad.<img src=x onerror=alert(1)>']) {
    const icon = renderArtifactFileIcon(name)
    const html = renderArtifactReference({ href: 'sandbox:' + name, artifacts: [{ index: 3, file_name: name }], labels })
    assert.ok(html?.includes(icon))
    assert.ok(html?.includes('data-artifact-index="3"'))
    assert.doesNotMatch(icon, /<img|onerror/)
    assert.doesNotMatch(html!, /<img src=x/)
  }
  assert.match(renderArtifactFileIcon('report.pdf'), /kind-file-pdf/)
  assert.match(renderArtifactFileIcon('report.pdf'), />PDF<\/text>/)
})

// 22 位句柄，与后端 types.ResourceHandleLength 一致。
const handleFor = (i: number) => `art${i}`.padEnd(22, 'x')
const refFor = (i: number) => `resource://${handleFor(i)}`

const artifacts: ArtifactRefMeta[] = [
  { index: 0, handle: refFor(0), file_name: '市场画像评分_e7edba.html', file_type: 'text/html' },
  { index: 1, handle: refFor(1), file_name: 'trend.png', file_type: 'image/png' },
  { index: 2, handle: refFor(2), file_name: 'diagram.svg', file_type: 'image/svg+xml' },
  { index: 3, handle: refFor(3), file_name: '腾讯控股(00700) 成交量_838ccc.html', file_type: 'text/html' },
]

// 知识库检索图：形式与产物完全相同，但不属于这条消息。
const foreignRef = `resource://${'z'.repeat(22)}`

test('isArtifactRefHref matches handle and sandbox forms only', () => {
  assert.equal(isArtifactRefHref(refFor(0)), true)
  assert.equal(isArtifactRefHref('sandbox:trend.png'), true)
  assert.equal(isArtifactRefHref('sandbox://trend.png'), true)
  assert.equal(isArtifactRefHref('https://example.com/trend.png'), false)
  // 长度不对的不是句柄。
  assert.equal(isArtifactRefHref('resource://tenant/1/trend.png'), false)
  assert.equal(isArtifactRefHref('trend.png'), false)
  assert.equal(isArtifactRefHref(''), false)
})

test('resolveArtifactRef resolves both the handle and the name form', () => {
  assert.equal(resolveArtifactRef(refFor(1), artifacts)?.file_name, 'trend.png')
  assert.equal(resolveArtifactRef('sandbox:trend.png', artifacts)?.index, 1)
  assert.equal(
    resolveArtifactRef('sandbox:/workspace/output/trend.png', artifacts)?.index,
    1,
  )
  assert.equal(
    resolveArtifactRef('sandbox:%E5%B8%82%E5%9C%BA%E7%94%BB%E5%83%8F%E8%AF%84%E5%88%86_e7edba.html', artifacts)?.index,
    0,
  )
  assert.equal(resolveArtifactRef(foreignRef, artifacts), null)
  assert.equal(resolveArtifactRef('sandbox:missing.csv', artifacts), null)
})

test('resolveArtifactRef accepts the url field carried by persisted messages', () => {
  const persisted: ArtifactRefMeta[] = [{ index: 0, url: refFor(7), file_name: 'a.csv' }]
  assert.equal(resolveArtifactRef(refFor(7), persisted)?.file_name, 'a.csv')
})

test('renderArtifactReference leaves ordinary images to the default renderer', () => {
  assert.equal(
    renderArtifactReference({ href: 'https://example.com/a.png', artifacts, labels }),
    null,
  )
  assert.equal(
    renderArtifactReference({ href: 'resource://tenant/1/a.png', artifacts, labels }),
    null,
  )
})

test('a handle from outside this message falls back to protected-image rendering', () => {
  // 知识库检索图与产物同形。此时必须交回默认渲染，而不是显示「文件不可用」。
  assert.equal(
    renderArtifactReference({ href: foreignRef, artifacts, labels }),
    null,
  )
  assert.equal(
    renderArtifactReference({ href: foreignRef, artifacts: [], labels, streaming: true }),
    null,
  )
})

test('renderArtifactReference renders image artifacts inline', () => {
  const html = renderArtifactReference({
    href: refFor(1),
    alt: '走势',
    artifacts,
    labels,
  })
  assert.ok(html?.includes('class="markdown-image artifact-ref-image"'))
  assert.ok(html?.includes('data-artifact-index="1"'))
  assert.ok(html?.includes('data-img-loading="1"'))
  assert.ok(html?.includes('alt="走势"'))
})

test('renderArtifactReference renders non-image artifacts as a clickable card', () => {
  const html = renderArtifactReference({
    href: refFor(0),
    alt: '市场画像评分',
    artifacts,
    labels,
  })
  assert.ok(html?.includes('class="artifact-ref-card"'))
  assert.ok(html?.includes('data-artifact-index="0"'))
  assert.ok(html?.includes('role="button"'))
  assert.ok(html?.includes('市场画像评分_e7edba.html'))
  assert.ok(html?.includes('点击预览'))
  assert.ok(!html?.includes('<img'))
})

test('svg artifacts go through the sandboxed preview rather than inline rendering', () => {
  const html = renderArtifactReference({ href: refFor(2), artifacts, labels })
  assert.ok(html?.includes('artifact-ref-card'))
  assert.ok(!html?.includes('artifact-ref-image'))
})

test('while streaming an unresolved reference shows the image skeleton', () => {
  // Artifacts are only collected once the turn ends, so mid-stream every
  // reference is unresolved; a card there would flash a half-written name.
  const html = renderArtifactReference({
    href: 'sandbox:市场画像评分_e7edba.html',
    artifacts: [],
    labels,
    streaming: true,
  })
  assert.ok(html?.includes('streaming-image-loading'))
  assert.ok(!html?.includes('artifact-ref-card'))
})

test('after the turn ends an unresolved reference says so instead of hanging', () => {
  const html = renderArtifactReference({
    href: 'sandbox:市场画像评分_e7edba.html',
    alt: '市场画像评分',
    artifacts: [],
    labels,
  })
  assert.ok(html?.includes('artifact-ref-card--pending'))
  assert.ok(html?.includes('文件不可用'))
  assert.ok(!html?.includes('data-artifact-index'))
  assert.ok(!html?.includes('role="button"'))
})

test('an empty image destination is skipped instead of rendering a broken img', () => {
  const html = renderArtifactReference({
    href: '',
    alt: '根目录示例文件',
    artifacts: [],
    labels,
  })
  assert.equal(html, '')
})

test('file names with spaces and parentheses resolve end to end', () => {
  const raw = '![成交量](sandbox:腾讯控股(00700) 成交量_838ccc.html)'
  const normalized = normalizeSandboxArtifactRefs(raw)

  // marked would split the raw destination at the first space; after
  // normalization it is a single token.
  const href = normalized.slice(normalized.indexOf('](') + 2, normalized.lastIndexOf(')'))
  assert.ok(!href.includes(' '))
  assert.equal(resolveArtifactRef(href, artifacts)?.index, 3)

  const html = renderArtifactReference({ href, alt: '成交量', artifacts, labels })
  assert.ok(html?.includes('腾讯控股(00700) 成交量_838ccc.html'))
  assert.ok(html?.includes('data-artifact-index="3"'))
})

test('normalizeSandboxArtifactRefs leaves everything else alone', () => {
  const untouched = [
    '![远程](https://example.com/a(b).png)',
    '![资源](resource://tenant/1/a.png)',
    '腾讯控股(00700) 的成交量见下图。',
    '写成 `![图](sandbox:a b.html)` 即可',
    '```\n![图](sandbox:a b.html)\n```',
  ]
  for (const markdown of untouched) {
    assert.equal(normalizeSandboxArtifactRefs(markdown), markdown)
  }
})

test('normalizeSandboxArtifactRefs preserves a title and an unterminated tail', () => {
  assert.equal(
    normalizeSandboxArtifactRefs('![图](sandbox:a b.html "说明")'),
    '![图](sandbox:a%20b.html "说明")',
  )
  // Mid-stream the closing paren has not arrived yet; leave the tail for the
  // streaming placeholder guard rather than guessing where it ends.
  assert.equal(
    normalizeSandboxArtifactRefs('![图](sandbox:腾讯控股(00700'),
    '![图](sandbox:腾讯控股(00700',
  )
})

test('artifact names are HTML-escaped', () => {
  const html = renderArtifactReference({
    href: refFor(0),
    artifacts: [{ index: 0, handle: refFor(0), file_name: '<img src=x onerror=alert(1)>.csv' }],
    labels,
  })
  assert.ok(!html?.includes('<img src=x'))
  assert.ok(html?.includes('&lt;img'))
})

test('a deleted artifact renders as a greyed, non-clickable card', () => {
  const html = renderArtifactReference({
    href: refFor(0),
    alt: '市场画像评分',
    artifacts: artifacts.map((a, i) =>
      i === 0 ? { ...a, deleted_at: '2026-09-20T02:00:00Z' } : a,
    ),
    labels,
  })
  assert.ok(html?.includes('artifact-ref-card--deleted'))
  assert.ok(html?.includes('文件已删除'))
  assert.ok(html?.includes('市场画像评分_e7edba.html'), 'the name still says which file it was')
  // Not clickable: the bytes are gone, so opening the preview would 404.
  assert.ok(!html?.includes('data-artifact-index'))
  assert.ok(!html?.includes('role="button"'))
  // And distinguishable from a reference that never resolved at all.
  assert.ok(!html?.includes('artifact-ref-card--pending'))
})

test('a deleted image artifact degrades to the card instead of a broken img', () => {
  // renderImage would emit a placeholder that hydrateArtifactImages then fails
  // to fill, leaving a permanently blank image in the answer.
  const html = renderArtifactReference({
    href: refFor(1),
    alt: '走势',
    artifacts: artifacts.map((a, i) =>
      i === 1 ? { ...a, deleted_at: '2026-09-20T02:00:00Z' } : a,
    ),
    labels,
  })
  assert.ok(html?.includes('artifact-ref-card--deleted'))
  assert.ok(!html?.includes('artifact-ref-image'))
  assert.ok(!html?.includes('<img'))
})

test('a deleted artifact is not mistaken for a foreign handle', () => {
  // Dropping tombstones before rendering would make the handle unresolvable,
  // and an unresolved handle falls through to protected-image rendering — a
  // broken image rather than an honest "deleted" card.
  const live = artifacts.filter((_, i) => i !== 0)
  assert.equal(renderArtifactReference({ href: refFor(0), artifacts: live, labels }), null)
})


test('parent Markdown links render as file cards with exact child indices, including refreshed metadata', () => {
  const renderer = new marked.Renderer()
  let files: ArtifactRefMeta[] = []
  installArtifactLinkRenderer(renderer, () => ({ artifacts: files, labels }))
  const body = `[开书设定](${refFor(0)})\n\n[文件](${refFor(1)})\n\n[网页](https://example.com)`
  const render = () => renderChatMarkdown(body, {
    renderer, escapeMarkdown: text => text, sanitizeHtml: html => html,
  })
  assert.doesNotMatch(render(), /data-artifact-index/)
  files = [
    { index: 0, handle: refFor(0), file_name: '开书设定.md', file_type: '.md' },
    { index: 1, handle: refFor(1), file_name: '开书设定.md', file_type: '.md' },
  ]
  const html = render()
  assert.match(html, /data-artifact-index="0"/)
  assert.match(html, /data-artifact-index="1"/)
  assert.equal((html.match(/点击预览/g) || []).length, 2)
  assert.match(html, /href="https:\/\/example.com"/)
  assert.doesNotMatch(html, /href="resource:/)
  files = files.map(({ handle, ...file }) => ({ ...file, url: handle }))
  assert.equal(render(), html, 'history URL metadata and live handle metadata render identically')
  files[0].deleted_at = '2026-10-18'
  assert.match(render(), /文件已删除/)
  assert.doesNotMatch(render(), /data-artifact-index="0"/)
  assert.match(render(), /data-artifact-index="1"/)
})

test('P1-14: artifactBlobCache scopes by user:tenant, revokes blob URLs on clear, and discards pre-clear inflight downloads', async (t) => {
  const { fileURLToPath } = await import('node:url')
  const { createServer } = await import('vite')

  function deferred<T>() {
    let resolve!: (value: T) => void
    const promise = new Promise<T>((r) => { resolve = r })
    return { promise, resolve }
  }

  function artifactImageRoot(index = 1) {
    const attributes: Record<string, string> = {
      src: 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==',
      'data-artifact-index': String(index),
      'data-img-loading': '1',
    }
    const image: any = {
      dataset: {},
      get src() { return attributes.src },
      set src(value: string) { attributes.src = value },
      getAttribute: (key: string) => attributes[key] ?? null,
      setAttribute: (key: string, value: string) => { attributes[key] = value },
      removeAttribute: (key: string) => { delete attributes[key] },
    }
    return {
      root: { querySelectorAll: () => [image] } as unknown as ParentNode,
      image,
    }
  }

  const downloadCalls: Array<{ sessionId: string; messageId: string; index: number; pending: ReturnType<typeof deferred<Blob>> }> = []
  async function waitForDownloads(count: number) {
    for (let i = 0; i < 200 && downloadCalls.length < count; i += 1) {
      await new Promise((r) => setTimeout(r, 5))
    }
    assert.equal(downloadCalls.length, count)
  }
  ;(globalThis as any).__mockDownloadArtifact = (sessionId: string, messageId: string, index: number) => {
    const pending = deferred<Blob>()
    downloadCalls.push({ sessionId, messageId, index, pending })
    return pending.promise
  }
  t.after(() => { delete (globalThis as any).__mockDownloadArtifact })

  const createdUrls: string[] = []
  const revokedUrls: string[] = []
  let urlSeq = 0
  t.mock.method(URL, 'createObjectURL', () => {
    const u = `blob:http://localhost/artifact-test-${++urlSeq}`
    createdUrls.push(u)
    return u
  })
  t.mock.method(URL, 'revokeObjectURL', (u: string) => {
    revokedUrls.push(u)
  })

  const server = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('../../', import.meta.url)),
    base: '/',
    plugins: [{
      name: 'mock-chat-api',
      enforce: 'pre',
      resolveId(id) {
        if (id === '\0mock:chat-api') return '\0mock:chat-api'
      },
      load(id) {
        if (id === '\0mock:chat-api') {
          return 'export function downloadArtifact(s, m, i) { return globalThis.__mockDownloadArtifact(s, m, i); }'
        }
      },
    }],
    resolve: {
      alias: [
        { find: '@/api/chat', replacement: '\0mock:chat-api' },
        { find: '@', replacement: fileURLToPath(new URL('../', import.meta.url)) },
      ],
    },
    optimizeDeps: { noDiscovery: true, entries: [] },
    server: { middlewareMode: true, hmr: false, ws: false, watch: null },
    appType: 'custom',
  })
  t.after(() => server.close())

  const security = await server.ssrLoadModule('/src/utils/security.ts') as typeof import('./security')
  const artifactRefs = await server.ssrLoadModule('/src/utils/sandboxArtifactRefs.ts') as typeof import('./sandboxArtifactRefs')
  t.after(() => {
    security.clearProtectedFileCache()
    security.setProtectedFileCacheScope(null, null)
  })

  const ctx = { sessionId: 'sess-1', messageId: 'msg-1' }

  // 1. Tenant 1001 caches its own blob URL; switching to Tenant 2002 does NOT reuse Tenant 1001's blob URL.
  security.setProtectedFileCacheScope('u-1', 1001)
  const dom1 = artifactImageRoot(1)
  const p1 = artifactRefs.hydrateArtifactImages(dom1.root, ctx)
  await waitForDownloads(1)
  downloadCalls[0]!.pending.resolve(new Blob(['tenant-1001-bytes'], { type: 'image/png' }))
  await p1
  assert.equal(dom1.image.src, 'blob:http://localhost/artifact-test-1')

  // Same tenant re-renders inline with cached blob URL without another download.
  const sameTenantHtml = artifactRefs.renderArtifactReference({
    href: refFor(1),
    artifacts,
    labels,
    context: ctx,
  })
  assert.ok(sameTenantHtml?.includes('src="blob:http://localhost/artifact-test-1"'))

  // Switch to Tenant 2002 (even before explicit clear): scope mismatch prevents cross-tenant blob reuse.
  security.setProtectedFileCacheScope('u-1', 2002)
  const otherTenantHtml = artifactRefs.renderArtifactReference({
    href: refFor(1),
    artifacts,
    labels,
    context: ctx,
  })
  assert.ok(!otherTenantHtml?.includes('blob:http://localhost/artifact-test-1'), 'must not leak tenant 1001 blob URL into tenant 2002')

  const dom2 = artifactImageRoot(1)
  const p2 = artifactRefs.hydrateArtifactImages(dom2.root, ctx)
  await waitForDownloads(2)
  downloadCalls[1]!.pending.resolve(new Blob(['tenant-2002-bytes'], { type: 'image/png' }))
  await p2
  assert.equal(dom2.image.src, 'blob:http://localhost/artifact-test-2')

  // 2. clearProtectedFileCache() invokes clearArtifactBlobCache() and revokes all cached artifact blob URLs.
  security.clearProtectedFileCache()
  assert.deepEqual(
    revokedUrls.sort(),
    ['blob:http://localhost/artifact-test-1', 'blob:http://localhost/artifact-test-2'],
  )

  // 3. In-flight download started before clearEpoch bump is discarded and cannot overwrite post-clear cache.
  const staleDom = artifactImageRoot(1)
  const staleHydration = artifactRefs.hydrateArtifactImages(staleDom.root, ctx)
  await waitForDownloads(3)

  artifactRefs.clearArtifactBlobCache()

  const freshDom = artifactImageRoot(1)
  const freshHydration = artifactRefs.hydrateArtifactImages(freshDom.root, ctx)
  await waitForDownloads(4)

  downloadCalls[2]!.pending.resolve(new Blob(['stale-bytes'], { type: 'image/png' }))
  await staleHydration
  assert.doesNotMatch(staleDom.image.src, /^blob:/, 'pre-clear download must be discarded')
  assert.equal(staleDom.image.dataset.authHydrated, '0')

  downloadCalls[3]!.pending.resolve(new Blob(['fresh-bytes'], { type: 'image/png' }))
  await freshHydration
  assert.equal(freshDom.image.src, 'blob:http://localhost/artifact-test-3')
})
