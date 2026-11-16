import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import vm from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import { DOMParser } from '@xmldom/xmldom'
import ts from 'typescript'
import * as vue from 'vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import * as security from '../utils/security.ts'

const require = createRequire(import.meta.url)

function readComponent(relPath: string): string {
  return readFileSync(new URL(relPath, import.meta.url), 'utf8')
}

function compileSfcComponent(relPath: string, customRequire: (id: string) => any) {
  const source = readComponent(relPath)
  const { descriptor } = parse(source, { filename: relPath })
  const compiled = compileScript(descriptor, {
    id: `test-${relPath.replace(/\W+/g, '-')}`,
    inlineTemplate: true,
  })
  const js = ts.transpileModule(compiled.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports: any = {}
  vm.runInNewContext(js, {
    exports,
    console: { error: () => {}, warn: () => {} },
    URL,
    atob,
    Uint8Array,
    TextDecoder,
    Promise,
    setTimeout,
    clearTimeout,
    window: { location: { origin: 'http://localhost:5173' }, addEventListener: () => {}, removeEventListener: () => {} },
    document: { body: { style: {} }, addEventListener: () => {}, removeEventListener: () => {} },
    localStorage: { getItem: () => null, setItem: () => {} },
    require: customRequire,
  })
  return exports.default
}

test('IMChannelPanel renders WeChat QR locally without api.qrserver.com and preserves stored credentials on edit', () => {
  const src = readComponent('./IMChannelPanel.vue')
  assert.doesNotMatch(src, /api\.qrserver\.com/, 'must not leak WeChat login QR tokens to external api.qrserver.com')
  assert.match(src, /<t-qrcode\b/, 'must render QR code locally via TDesign <t-qrcode>')
  assert.match(src, /editingCredentialsConfigured/, 'must track whether edited channel already has stored credentials')
  assert.match(src, /credentials:\s*shouldSendCredentials\b/, 'must omit credentials on update when stored credentials were not modified')
  assert.match(src, /watch\(\s*showCreateDialog/, 'must stop WeChat QR polling and reset when drawer closes via overlay/ESC')
})

test('ChatCitationFloat blocks javascript: and data: hrefs while rendering https: links safely', async () => {
  const Comp = compileSfcComponent('./ChatCitationFloat.vue', (id) => {
    if (id === 'vue') return vue
    if (id === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
    throw new Error(`Unexpected import: ${id}`)
  })

  const renderFloat = async (url: string) => {
    const app = createSSRApp({
      render: () => vue.h(Comp, {
        float: {
          visible: true,
          type: 'web',
          top: 10,
          left: 20,
          title: 'Citation',
          content: '',
          url,
          loading: false,
          error: '',
        },
      }),
    })
    const ctx: { teleports?: Record<string, string> } = {}
    const main = await renderToString(app, ctx)
    return main + (ctx.teleports?.body || '')
  }

  const jsHtml = await renderFloat('javascript:alert(1)')
  assert.doesNotMatch(jsHtml, /href="javascript:/i)
  assert.doesNotMatch(jsHtml, /<a\b/i)

  const dataHtml = await renderFloat('data:text/html,<script>alert(1)</script>')
  assert.doesNotMatch(dataHtml, /href="data:/i)

  const safeHtml = await renderFloat('https://docs.example.com/ref')
  assert.match(safeHtml, /<a\b[^>]*href="https:\/\/docs\.example\.com\/ref"/)
  assert.match(safeHtml, /rel="noopener noreferrer"/)
})

test('ChatReferencesDrawer blocks unsafe web item hrefs and never loads external Google favicons', async () => {
  const drawerRef = {
    visible: vue.ref(true),
    references: vue.ref([
      {
        chunk_type: 'web_search',
        content: 'Snippet text',
        metadata: { url: 'javascript:alert(1)', title: 'Injected' },
      },
      {
        chunk_type: 'web_search',
        content: 'Safe snippet',
        metadata: { url: 'https://internal.corp.local/wiki', title: 'Internal Wiki' },
      },
    ]),
    highlight: vue.ref(null),
    source: vue.ref(null),
    panelWidth: vue.ref(420),
    openSource: () => {},
    closeSource: () => {},
    close: () => {},
  }

  const Comp = compileSfcComponent('./ChatReferencesDrawer.vue', (id) => {
    if (id === 'vue') return vue
    if (id === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
    if (id === 'vue-router') return { useRouter: () => ({ resolve: () => ({ href: '/kb' }) }) }
    if (id === '@/composables/useChatReferencesDrawer') {
      return { REFERENCES_PANEL_WIDTH: 420, useChatReferencesDrawer: () => drawerRef }
    }
    if (id === '@/views/chat/components/ArtifactFileIcon.vue') return { render: () => null }
    if (id === '@/components/ChatReferenceSourceView.vue') return { render: () => null }
    if (id === '@/components/PanelResizeHandle.vue') return { render: () => null }
    if (id === '@/utils/referenceSources') return require('../utils/referenceSources.ts')
    throw new Error(`Unexpected import: ${id}`)
  })

  const app = createSSRApp({ render: () => vue.h(Comp, { embeddedMode: true }) })
  app.component('t-icon', { props: ['name'], setup(p: any) { return () => vue.h('i', { 'data-icon': p.name }) } })
  const ctx: { teleports?: Record<string, string> } = {}
  const main = await renderToString(app, ctx)
  const html = main + (ctx.teleports?.body || '')

  assert.doesNotMatch(html, /href="javascript:/i)
  assert.doesNotMatch(html, /google\.com\/s2\/favicons/i)
  assert.match(html, /href="https:\/\/internal\.corp\.local\/wiki"/)
})

test('doc-content escapes HTML in Mermaid/code blocks, adds noopener on source link, and guards audio blob races', () => {
  const src = readComponent('./doc-content.vue')
  assert.doesNotMatch(
    src,
    /return `<div class="mermaid" id="\$\{id\}">\$\{text\}<\/div>`/,
    'Mermaid block must HTML-escape raw diagram text so </div> cannot break out of container',
  )
  assert.doesNotMatch(src, /javascript:void\(0\)/, 'source link must not fall back to javascript:void(0)')
  const urlSection = src.slice(src.lastIndexOf("details.type === 'url'"), src.lastIndexOf('showSummarySection'))
  assert.match(urlSection, /rel="noopener noreferrer"/)
  assert.match(src, /audioLoadSeq/, 'loadAudioPreview must guard against stale document switches and unmount')
})

test('document-preview sanitizes rendered DOCX DOM nodes and escapes Excel sheet names', () => {
  const src = readComponent('./document-preview.vue')
  assert.match(src, /sanitizeRenderedDocxDom\(rendered\)/, 'renderDocx must sanitize rendered DOM before replaceChildren')
  assert.doesNotMatch(
    src,
    /<div class="excel-sheet-name">\$\{name\}<\/div>/,
    'Excel sheet names must be HTML-escaped before building sheet HTML',
  )
})

test('SkillFilesPanel sanitizes SVG data URLs, rejects non-image media_type, and ignores stale file responses', async () => {
  const pendingFiles: Array<{ path: string; resolve: (v: any) => void }> = []
  let listItems: Array<{ path: string }> = [{ path: 'a.md' }, { path: 'b.svg' }]

  const Comp = compileSfcComponent('./SkillFilesPanel.vue', (id) => {
    if (id === 'vue') return vue
    if (id === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
    if (id === 'highlight.js') return { highlight: (t: string) => ({ value: t }), highlightAuto: (t: string) => ({ value: t }), getLanguage: () => true }
    if (id === 'katex/dist/katex.min.css') return {}
    if (id === '@/utils/clipboard') return { copyWithToast: async () => {} }
    if (id === '@/utils/files') return { getFileIcon: () => 'file' }
    if (id === '@/utils/security') {
      return {
        ...security,
        sanitizeSvgForDataUrl: (svg: string) =>
          `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg.replace(/<script[\s\S]*?<\/script>/gi, ''))}`,
      }
    }
    if (id === '@/utils/chatMarkdownRenderer') return { createChatMarkdownRenderer: () => ({}), renderChatMarkdown: (t: string) => `<p>${t}</p>` }
    if (id === '@/utils/mermaidShared') return { createMermaidCodeRenderer: () => () => '', enhanceMarkdownContainer: async () => {} }
    if (id === '@/api/skill') {
      return {
        listCatalogSkillFiles: async () => ({ data: listItems }),
        getCatalogSkillFile: (_cid: string, path: string) => new Promise((resolve) => {
          pendingFiles.push({ path, resolve })
        }),
      }
    }
    if (id === '@/api/system') return { getConfigSkillFile: async () => ({}), listConfigSkillFiles: async () => ({ data: [] }) }
    throw new Error(`Unexpected import: ${id}`)
  })

  // Custom DOMParser global so sanitizeSvgForDataUrl runs in Node
  const prevDOMParser = (globalThis as any).DOMParser
  ;(globalThis as any).DOMParser = DOMParser
  try {
    let instance: any
    const Wrapper = vue.defineComponent({
      setup() {
        return () => vue.h(Comp, {
          catalogId: 'cat-1',
          drawerWidth: 720,
          ref: (el: any) => { instance = el },
        })
      },
    })
    const makeNode = (tag: string, text = '') => vue.markRaw({
      tag,
      text,
      props: {} as Record<string, any>,
      children: [] as any[],
      parent: null as any,
      getBoundingClientRect: () => ({ right: 220 }),
    })
    const root = makeNode('root')
    const renderer = vue.createRenderer<any, any>({
      createElement: (tag) => makeNode(tag),
      createText: (text) => makeNode('#text', text),
      createComment: () => makeNode('#comment'),
      insert: (child, parent, anchor) => {
        if (child.parent) child.parent.children = child.parent.children.filter((c: any) => c !== child)
        child.parent = parent
        const idx = anchor ? parent.children.indexOf(anchor) : -1
        if (idx < 0) parent.children.push(child)
        else parent.children.splice(idx, 0, child)
      },
      remove: (child) => {
        if (child.parent) child.parent.children = child.parent.children.filter((c: any) => c !== child)
        child.parent = null
      },
      setElementText: (el, text) => { el.text = text; el.children = [] },
      setText: (el, text) => { el.text = text },
      patchProp: (el, key, _prev, next) => { el.props[key] = next },
      parentNode: (el) => el.parent,
      nextSibling: (el) => el.parent?.children[el.parent.children.indexOf(el) + 1] || null,
      querySelector: () => root,
    })
    const app = renderer.createApp(Wrapper)
    app.config.globalProperties.$t = (k: string) => k
    app.component('t-loading', { props: ['loading', 'size'], setup(_p: any, { slots }: any) { return () => vue.h('div', slots.default?.()) } })
    app.component('t-icon', { props: ['name', 'size'], render: () => null })
    app.mount(root)
    await new Promise((r) => setImmediate(r))

    // Initial selectFile('a.md') is pending in pendingFiles[0]
    assert.equal(pendingFiles.length, 1)
    assert.equal(pendingFiles[0].path, 'a.md')

    // User clicks 'b.svg' BEFORE 'a.md' finishes: locate button for b.svg and click it
    const findButtons = (node: any, out: any[] = []): any[] => {
      if (node.tag === 'button') out.push(node)
      for (const c of node.children || []) findButtons(c, out)
      return out
    }
    const bBtn = findButtons(root).find((b) => b.props?.title === 'b.svg')
    assert.ok(bBtn, 'expected b.svg tree button')
    bBtn.props.onClick()
    await vue.nextTick()
    assert.equal(pendingFiles.length, 2)

    // b.svg resolves FIRST with an SVG containing <script>alert(1)</script>
    const maliciousSvg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><script>alert(1)</script><rect width="10" height="10"/></svg>'
    pendingFiles[1].resolve({
      data: {
        encoding: 'base64',
        media_type: 'image/svg+xml',
        content: Buffer.from(maliciousSvg, 'utf8').toString('base64'),
      },
    })
    await new Promise((r) => setImmediate(r))
    await vue.nextTick()

    // Late resolution of 'a.md' MUST BE IGNORED so b.svg stays displayed!
    pendingFiles[0].resolve({
      data: {
        encoding: 'utf-8',
        content: '# Stale Markdown From A',
      },
    })
    await new Promise((r) => setImmediate(r))
    await vue.nextTick()

    const findImgs = (node: any, out: any[] = []): any[] => {
      if (node.tag === 'img') out.push(node)
      for (const c of node.children || []) findImgs(c, out)
      return out
    }
    const imgs = findImgs(root)
    assert.equal(imgs.length, 1, 'b.svg preview must remain rendered instead of being overwritten by stale a.md')
    const srcAttr = String(imgs[0].props.src || '')
    assert.ok(srcAttr.startsWith('data:image/svg+xml;charset=utf-8,'), 'SVG must be sanitized via sanitizeSvgForDataUrl')
    assert.doesNotMatch(decodeURIComponent(srcAttr), /<script/i)
    void instance
    app.unmount()
  } finally {
    ;(globalThis as any).DOMParser = prevDOMParser
  }
})

test('UserMenu enforces noopener,noreferrer and safe URL schemes on openDocs/openGithub and clears submenu timer', () => {
  const src = readComponent('./UserMenu.vue')
  assert.doesNotMatch(
    src,
    /window\.open\(url,\s*'_blank'\)/,
    'openDocs must pass noopener,noreferrer to window.open',
  )
  assert.match(src, /toSafeExternalOrAppUrl/, 'UserMenu must validate docsUrl and githubUrl schemes')
  const unmountedBlock = src.slice(src.indexOf('onUnmounted('))
  assert.match(unmountedBlock, /tenantSubmenuHideTimer/, 'onUnmounted must clear tenantSubmenuHideTimer')
})
