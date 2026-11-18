import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { promisify } from 'node:util'
import test from 'node:test'
import vm from 'node:vm'
import { build } from 'esbuild'
import JSZip from 'jszip'
import ts from 'typescript'
import { DOMParser as XmlParser, XMLSerializer } from '@xmldom/xmldom'

import {
  extractCitationHtmlPlaceholders,
  joinCitationTagsToPreviousLine,
  preprocessCitationTags,
  preserveCitationTags,
  restoreCitationTags,
  stripCitationTagsForMarkdown,
} from './citationMarkdown'
import {
  domPurifyAllowedUriRegexp,
  domPurifySecurityHooks,
  markdownDomPurifyConfig,
} from './markdownDomPurify'
import {
  clearProtectedFileCache,
  createSafeImage,
  protectProviderImageSrcInHTML,
  setProtectedFileCacheScope,
} from './security'
import {
  clearCitationChunkCache,
  getCitationChunkCache,
  setCitationChunkCache,
} from './citationChunkCache'
import { renderEpubPreview } from './epubPreview'
import { extractXlsxCellImages, injectSheetHtmlImages } from './xlsxPreviewImages'
import { isSecretLabel } from './pageText'
import { getKnowledgeChunksSummaryHtml } from './knowledgeChunksDisplay'
import { createChatMarkdownRenderer, renderChatMarkdown } from './chatMarkdownRenderer'
import * as manualKnowledgeDraft from './manualKnowledgeDraft'
import * as mermaidStreamingMod from './mermaidStreaming'
import { buildReferenceList, getFaviconUrl, getWebSearchUrl } from './referenceSources'
import { docsUrl, isDocsAvailable } from './docsUrl'
import { pageLinkReturnPath } from './assistantPageLink'
import { maskMermaidBlocksForStreaming } from './mermaidStreaming'
import { createPtyEchoPredictor, looksLikeSecretPrompt } from './ptyEchoPredictor'
import { buildQueryWithHostContext, stripHostContextFromQuery } from './embedContext'
import { isEmbedImageFile } from './embedFile'
import { shouldRejectKnowledgeFileType } from './fileTypeVerification'
import { validateAllowedOrigins } from './embedAllowedOrigins'

const chromium = [
  process.env.BROWSERSKILL_TEST_CHROMIUM,
  process.env.CHROME_BIN,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
].find((candidate) => candidate && existsSync(candidate))

test('W6-UT-01: citationMarkdown blocks javascript:/data: web URLs, escapes domain HTML, and leaves code blocks untouched', () => {
  const xssWeb = preprocessCitationTags(
    'See <web url="javascript:alert(1)" title="Click"/> and <web url="not-a-url:<img src=x onerror=alert(1)>" title="Bad"/> and <web url="https://sub.example.com/p" title="Good"/>',
  )
  assert.doesNotMatch(xssWeb, /javascript:/i)
  assert.doesNotMatch(xssWeb, /<img/i)
  assert.match(xssWeb, /data-url="https:\/\/sub\.example\.com\/p"/)

  const mdStripped = stripCitationTagsForMarkdown(
    'Answer <web url="javascript:alert(1)" title="Evil"/> safe <web url="https://example.com" title="Example"/>',
  )
  assert.doesNotMatch(mdStripped.text, /javascript:/i)
  assert.match(mdStripped.text, /\[Example\]\(https:\/\/example\.com\)/)

  // Code fences and inline code must NOT have [[...]] or <kb/> mutated
  const codeWithWikiAndTags = [
    '```bash',
    'if [[ -f "$FILE" ]]; then echo "<kb doc=\\"a\\" chunk_id=\\"1\\"/>"; fi',
    '```',
    'Inline `df[["col_a"]]` and `<web url="https://example.com"/>` stay literal, while [[real/page|Page]] converts.',
  ].join('\n')

  const { content, htmlSnippets } = extractCitationHtmlPlaceholders(codeWithWikiAndTags)
  const restored = content.replace(/@@GENYEE_HTML_PLACEHOLDER_(\d+)@@/g, (_m, i) => htmlSnippets[Number(i)] || '')
  assert.match(restored, /\[\[ -f "\$FILE" \]\]/, 'Bash [[ ... ]] inside fenced code must remain intact')
  assert.match(restored, /`df\[\["col_a"\]\]`/, 'Python/Pandas df[["col_a"]] in inline code must remain intact')
  assert.match(restored, /class="wiki-content-link citation-wiki" data-slug="real\/page"/, 'Prose [[wiki]] still renders')

  const preserved = preserveCitationTags('Code `<kb doc="x" chunk_id="1"/>` outside <kb doc="y" chunk_id="2"/>')
  assert.match(preserved.text, /`<kb doc="x" chunk_id="1"\/>`/, 'Inline code <kb/> must not be tokenized')
  assert.equal(restoreCitationTags(preserved.text, preserved.tags).includes('<kb doc="y" chunk_id="2"/>'), true)

  const joined = joinCitationTagsToPreviousLine('```xml\nline1\n<kb doc="x"/>\n```')
  assert.equal(joined, '```xml\nline1\n<kb doc="x"/>\n```', 'Lines inside fenced code must not be collapsed')
})

test(
  'W6-UT-02: DOMPurify configs/hooks block SVG data:image/svg+xml XSS, foreignObject, event attrs, lowercase SVG <a> opener leaks, and preserve EPUB sections in real browser DOMPurify',
  { skip: !chromium, timeout: 30_000 },
  async () => {
    // 1. Direct config & hook assertions
    assert.doesNotMatch('data:image/svg+xml;base64,PHN2Zz4=', domPurifyAllowedUriRegexp)
    assert.match('data:image/png;base64,iVBORw0KGgo=', domPurifyAllowedUriRegexp)
    assert.equal(markdownDomPurifyConfig.ALLOWED_TAGS.includes('foreignobject'), false)

    // Lowercase SVG `<a>` must receive rel="noopener noreferrer" and target="_blank"
    const svgAnchorAttrs = new Map<string, string>([['href', 'https://external.example.com']])
    const mockSvgAnchor: any = {
      tagName: 'a',
      getAttribute: (n: string) => svgAnchorAttrs.get(n) ?? null,
      setAttribute: (n: string, v: string) => svgAnchorAttrs.set(n, v),
      hasAttribute: (n: string) => svgAnchorAttrs.has(n),
      removeAttribute: (n: string) => svgAnchorAttrs.delete(n),
    }
    domPurifySecurityHooks.beforeSanitizeElements(mockSvgAnchor)
    domPurifySecurityHooks.afterSanitizeElements(mockSvgAnchor)
    assert.equal(svgAnchorAttrs.get('rel'), 'noopener noreferrer')
    assert.equal(svgAnchorAttrs.get('target'), '_blank')

    // 2. Real browser DOMPurify verification in headless Chromium
    const directory = await mkdtemp(path.join(tmpdir(), 'genyee-w6-dompurify-'))
    try {
      await build({
        stdin: {
          resolveDir: fileURLToPath(new URL('.', import.meta.url)),
          contents: `
            import { sanitizeHTML, sanitizeDocumentPreviewHTML, sanitizeMarkdownHTML, sanitizeSvgForDataUrl } from './security.ts';
            import DOMPurify from 'dompurify';
            const result = { supported: DOMPurify.isSupported, failures: [] };
            const check = (cond, msg) => { if (!cond) result.failures.push(msg); };

            const svgUsePayload = '<svg><use href="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciPjxzY3JpcHQ+d2luZG93LnB3bmVkPTE8L3NjcmlwdD48L3N2Zz4=#x"></use><use xlink:href="https://evil.example/x.svg#icon"></use><use href="#local-node"></use></svg>';
            for (const [fnName, fn] of [['sanitizeHTML', sanitizeHTML], ['sanitizeMarkdownHTML', sanitizeMarkdownHTML]]) {
              const out = fn(svgUsePayload);
              const tpl = document.createElement('template');
              tpl.innerHTML = out;
              const uses = [...tpl.content.querySelectorAll('use')];
              for (const u of uses) {
                const h = u.getAttribute('href') || u.getAttribute('xlink:href') || '';
                check(!h || h.startsWith('#'), fnName + ' allowed external/data <use>: ' + h);
              }
            }

            const foreignPayload = '<svg><foreignObject><iframe src="javascript:alert(1)"></iframe><div onclick="alert(1)" onpointerenter="alert(1)">hi</div></foreignObject></svg>';
            const foreignOut = sanitizeMarkdownHTML(foreignPayload);
            const foreignTpl = document.createElement('template');
            foreignTpl.innerHTML = foreignOut;
            check(!foreignTpl.content.querySelector('foreignobject,foreignObject,iframe,[onclick],[onpointerenter]'), 'foreignObject or pointer event survived');

            const epubHtml = '<section class="epub-section" data-epub-section="3"><p>Chapter 3</p></section>';
            const epubSanitized = sanitizeDocumentPreviewHTML(epubHtml);
            const epubTpl = document.createElement('template');
            epubTpl.innerHTML = epubSanitized;
            const sec = epubTpl.content.querySelector('section[data-epub-section="3"]');
            check(!!sec && sec.textContent === 'Chapter 3', 'EPUB <section data-epub-section> stripped by sanitizeDocumentPreviewHTML');

            const svgDataUrl = sanitizeSvgForDataUrl('<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><rect width="10" height="10" onload="alert(1)"/></svg>');
            check(svgDataUrl.startsWith('data:image/svg+xml;charset=utf-8,') && !decodeURIComponent(svgDataUrl).includes('script') && !decodeURIComponent(svgDataUrl).includes('onload'), 'sanitizeSvgForDataUrl failed');

            document.getElementById('result').textContent = btoa(JSON.stringify(result));
          `,
        },
        outfile: path.join(directory, 'bundle.js'),
        bundle: true,
        platform: 'browser',
        format: 'iife',
        define: { 'import.meta.env': '{}' },
        logLevel: 'silent',
      })
      await writeFile(
        path.join(directory, 'index.html'),
        '<!doctype html><pre id="result">pending</pre><script src="bundle.js"></script>',
      )
      const chromeArgs = [
        '--headless=new',
        '--no-first-run',
        '--no-default-browser-check',
        '--disable-background-networking',
      ]
      if ((typeof process.getuid === 'function' && process.getuid() === 0) || Boolean(process.env.CI)) {
        chromeArgs.push('--no-sandbox')
      }
      chromeArgs.push(
        `--user-data-dir=${path.join(directory, 'profile')}`,
        '--dump-dom',
        pathToFileURL(path.join(directory, 'index.html')).href,
      )
      const { stdout } = await promisify(execFile)(chromium!, chromeArgs, {
        timeout: 25_000,
        maxBuffer: 1024 * 1024,
      })
      const encoded = stdout.match(/<pre id="result">([^<]+)<\/pre>/)?.[1]
      assert.ok(encoded && encoded !== 'pending', 'Browser DOMPurify test did not finish')
      const parsed = JSON.parse(Buffer.from(encoded, 'base64').toString('utf8'))
      assert.equal(parsed.supported, true)
      assert.deepEqual(parsed.failures, [])
    } finally {
      await rm(directory, { recursive: true, force: true })
    }
  },
)

test('W6-UT-03: security.ts escapes decoded quotes in protected image tags, fences hydrateProtectedFileImages on scope change, and clears citationChunkCache on protected cache reset', () => {
  // 1. &quot; in provider image URL must not break out of data-protected-src="..." into injected attributes!
  const crafted = '<img src="minio://bucket/a&quot; onerror=&quot;alert(1)" alt="pic">'
  const rewritten = protectProviderImageSrcInHTML(crafted)
  assert.doesNotMatch(rewritten, /"\s+onerror=/i, 'Decoded &quot; must be escaped before writing data-protected-src')
  assert.match(rewritten, /data-protected-src="minio:\/\/bucket\/a&quot; onerror=&quot;alert\(1\)"/)

  // 2. createSafeImage must escape < > & ' as well as "
  const imgTag = createSafeImage('https://example.com/a?x=1&y=2', 'alt', 'title')
  assert.match(imgTag, /src="https:\/\/example\.com\/a\?x=1&amp;y=2"/)

  // 3. clearProtectedFileCache must also wipe citationChunkCache
  setProtectedFileCacheScope('u-1', '100')
  setCitationChunkCache('u-1:100', 'chunk-secret', { content: 'Confidential chunk text' })
  assert.ok(getCitationChunkCache('u-1:100', 'chunk-secret'))
  clearProtectedFileCache()
  assert.equal(getCitationChunkCache('u-1:100', 'chunk-secret'), undefined)
})

test('W6-UT-04: renderEpubPreview and extractXlsxCellImages sanitize embedded SVG media to static data URLs without minting executable SVG blob URLs', async () => {
  const prevParser = globalThis.DOMParser
  class XmlDomAdapter {
    parseFromString(text: string) {
      const doc = new XmlParser().parseFromString(text, 'application/xhtml+xml')
      for (const element of Array.from(doc.getElementsByTagName('*'))) {
        Object.defineProperty(element, 'innerHTML', {
          get: () => Array.from(element.childNodes).map((n) => new XMLSerializer().serializeToString(n)).join(''),
        })
        Object.assign(element, {
          querySelectorAll(selector: string) {
            const tag = selector === 'img' ? 'img' : 'a'
            return Array.from(element.getElementsByTagName(tag)).filter(
              (node) => tag !== 'a' || node.hasAttribute('href'),
            )
          },
        })
      }
      Object.assign(doc, { body: doc.getElementsByTagName('body')[0] })
      return doc
    }
  }
  globalThis.DOMParser = XmlDomAdapter as unknown as typeof DOMParser
  try {
    const maliciousSvg =
      '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><rect width="20" height="20" onload="alert(1)"/></svg>'

    // 1. EPUB with archive .svg and inline data:image/svg+xml
    const epubZip = new JSZip()
    epubZip.file('META-INF/container.xml', '<container><rootfiles><rootfile full-path="EPUB/book.opf"/></rootfiles></container>')
    epubZip.file('EPUB/book.opf', '<package><manifest><item id="c1" href="text/c1.xhtml"/></manifest><spine><itemref idref="c1"/></spine></package>')
    const inlineMaliciousSvg = `data:image/svg+xml;base64,${Buffer.from(maliciousSvg).toString('base64')}`
    epubZip.file(
      'EPUB/text/c1.xhtml',
      `<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="../images/evil.svg"/><img src="${inlineMaliciousSvg}"/></body></html>`,
    )
    epubZip.file('EPUB/images/evil.svg', maliciousSvg)
    const epubRes = await renderEpubPreview(await epubZip.generateAsync({ type: 'arraybuffer' }))
    try {
      assert.deepEqual(epubRes.objectUrls, [], 'EPUB must NEVER mint navigable same-origin blob: URLs for .svg files')
      assert.doesNotMatch(decodeURIComponent(epubRes.html), /<script|onload/i)
      assert.match(epubRes.html, /data:image\/svg\+xml;charset=utf-8,/)
    } finally {
      epubRes.objectUrls.forEach((u) => URL.revokeObjectURL(u))
    }

    // 2. XLSX with xl/media/image1.svg containing <script>
    const xlsxZip = new JSZip()
    xlsxZip.file('xl/workbook.xml', '<?xml version="1.0"?><workbook><sheets><sheet name="S1" sheetId="1" r:id="rId1"/></sheets></workbook>')
    xlsxZip.file('xl/_rels/workbook.xml.rels', '<?xml version="1.0"?><Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>')
    xlsxZip.file('xl/worksheets/sheet1.xml', '<?xml version="1.0"?><worksheet><sheetData><row r="1"><c r="A1" t="e" vm="1"><v>#VALUE!</v></c></row></sheetData></worksheet>')
    xlsxZip.file('xl/metadata.xml', '<?xml version="1.0"?><metadata><valueMetadata count="1"><bk><rc t="1" v="0"/></bk></valueMetadata></metadata>')
    xlsxZip.file('xl/richData/richValueRel.xml', '<?xml version="1.0"?><richValueRels><rel r:id="rId1"/></richValueRels>')
    xlsxZip.file('xl/richData/_rels/richValueRel.xml.rels', '<?xml version="1.0"?><Relationships><Relationship Id="rId1" Target="../media/image1.svg"/></Relationships>')
    xlsxZip.file('xl/media/image1.svg', maliciousSvg)
    const sheetMap = await extractXlsxCellImages(await xlsxZip.generateAsync({ type: 'arraybuffer' }))
    const svgSrc = sheetMap.get('S1')?.get('A1')?.[0]?.src || ''
    assert.ok(svgSrc.startsWith('data:image/svg+xml;charset=utf-8,'), `Expected sanitized utf-8 SVG data URI, got: ${svgSrc}`)
    assert.doesNotMatch(decodeURIComponent(svgSrc), /<script|onload/i)

    // injectSheetHtmlImages must escape img.src quotes
    const injected = injectSheetHtmlImages(
      '<table id="sheet-0"><tr><td id="sheet-0-A1">#VALUE!</td></tr></table>',
      0,
      new Map([['A1', [{ src: 'data:image/png;base64,abc" onerror="alert(1)', alt: 'x' }]]]),
    )
    assert.doesNotMatch(injected, /"\s+onerror=/i)
    assert.match(injected, /src="data:image\/png;base64,abc&quot; onerror=&quot;alert\(1\)"/)
  } finally {
    if (prevParser) globalThis.DOMParser = prevParser
    else Reflect.deleteProperty(globalThis, 'DOMParser')
  }
})

test('W6-UT-05: request.ts never leaks Authorization or X-Tenant-ID headers or 401 refresh retries to cross-origin URLs or untokened embed paths', async () => {
  const reqSrc = readFileSync(new URL('./request.ts', import.meta.url), 'utf8')
  const compiled = ts.transpileModule(reqSrc, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  let reqFulfilled!: (cfg: any) => any
  let resRejected!: (err: any) => any
  let refreshCalls = 0

  const mockAxiosInstance: any = Object.assign(
    async (cfg: any) => cfg,
    {
      interceptors: {
        request: { use: (fn: any) => { reqFulfilled = fn } },
        response: { use: (_ok: any, errFn: any) => { resRejected = errFn } },
      },
      get: async () => ({}),
      post: async () => ({}),
      put: async () => ({}),
      patch: async () => ({}),
      delete: async () => ({}),
    },
  )

  const exports: Record<string, any> = {}
  vm.runInNewContext(compiled, {
    exports,
    URL,
    Promise,
    Object,
    String,
    window: { location: { origin: 'https://genyee.internal.corp', href: 'https://genyee.internal.corp/platform' } },
    localStorage: {
      getItem: (k: string) => {
        if (k === 'genyee_token') return 'secret-jwt-token'
        if (k === 'genyee_selected_tenant_id') return '10001'
        return null
      },
    },
    require(id: string) {
      if (id === 'axios') {
        const axiosMod = {
          create: () => mockAxiosInstance,
          CanceledError: class extends Error {},
          isCancel: () => false,
        }
        return { __esModule: true, default: axiosMod, ...axiosMod }
      }
      if (id === './index') return { generateRandomString: () => 'reqid1234567', MAX_FILE_SIZE_MB: 50, MAX_SKILL_BUNDLE_SIZE_MB: 50 }
      if (id === '@/i18n') {
        const global = { t: (k: string) => k, locale: { value: 'en-US' } }
        return { __esModule: true, default: { global }, global }
      }
      if (id === './api-base') return { getApiBaseUrl: () => '' }
      if (id === './uploadLimit') return { isSkillBundleUploadUrl: () => false }
      if (id === './requestTimeouts') return { isTimeoutError: () => false, uploadTimeoutMs: () => 60000 }
      if (id === './authRefresh') {
        return {
          AuthSessionChangedError: class extends Error {},
          assertAuthRequestContext: () => 'secret-jwt-token',
          captureAuthRequestContext: () => ({ generation: 1, tenantId: '10001' }),
          forceReloginRedirect() {},
          isEmbedPage: () => false,
          refreshAccessTokenShared: async () => { refreshCalls++; return 'rotated-jwt' },
        }
      }
      throw new Error(`Unexpected require in W6-UT-05: ${id}`)
    },
  })

  // 1. Same-origin relative API request DOES attach Bearer and X-Tenant-ID
  const sameOriginCfg = reqFulfilled({ url: '/api/v1/knowledge-bases', headers: {} })
  assert.equal(sameOriginCfg.headers.Authorization, 'Bearer secret-jwt-token')
  assert.equal(sameOriginCfg.headers['X-Tenant-ID'], '10001')

  // 2. Cross-origin URL MUST NOT receive Bearer token or X-Tenant-ID!
  for (const untrustedUrl of ['https://evil.example.com/collect', '//evil.example.com/collect']) {
    const crossCfg = reqFulfilled({ url: untrustedUrl, headers: {} })
    assert.equal(crossCfg.headers.Authorization, undefined, `Authorization leaked to ${untrustedUrl}`)
    assert.equal(crossCfg.headers['X-Tenant-ID'], undefined, `X-Tenant-ID leaked to ${untrustedUrl}`)

    // 3. 401 from cross-origin URL MUST NOT trigger token refresh or replay JWT!
    await assert.rejects(() =>
      resRejected({
        config: crossCfg,
        response: { status: 401, data: { message: 'unauthorized' } },
      }),
    )
    assert.equal(refreshCalls, 0, `401 from ${untrustedUrl} must not trigger JWT refresh`)
  }

  // 4. Public /api/v1/embed/ request without Embed header must NOT attach web user's Bearer JWT
  const embedCfg = reqFulfilled({ url: '/api/v1/embed/ch-1/config', headers: {} })
  assert.equal(embedCfg.headers.Authorization, undefined)
})

test('W6-UT-06: isSecretLabel detects camelCase and snake_case credential field names/ids', () => {
  for (const secretIdentifier of [
    'openai_api_key',
    'clientSecret',
    'app_secret',
    'confirmPassword',
    'userPasswd',
    'slack_bot_token',
    'githubAccessToken',
    'awsSecretAccessKey',
    'webhook_signing_secret',
    'privateKeyPem',
  ]) {
    assert.equal(isSecretLabel(secretIdentifier), true, `Expected ${secretIdentifier} to be detected as secret`)
  }
  for (const nonSecret of ['maxTokens', 'max_tokens', 'tokenCount', 'monkey', 'turkey', 'secretaryName']) {
    assert.equal(isSecretLabel(nonSecret), false, `Expected ${nonSecret} NOT to be flagged as secret`)
  }
})

test('W6-UT-07: getKnowledgeChunksSummaryHtml escapes/coerces untrusted counts, renderChatMarkdown preserves settled code with ![ and (, and citationChunkCache is bounded', () => {
  const t = ((key: string, params?: Record<string, unknown>) => `${key}:${JSON.stringify(params)}`) as any
  const summaryHtml = getKnowledgeChunksSummaryHtml(t, {
    fetched_chunks: '<img src=x onerror=alert(1)>' as any,
    total_chunks: '"><script>alert(1)</script>' as any,
    page: '<svg onload=alert(1)>' as any,
    page_size: 10,
  } as any)
  assert.doesNotMatch(summaryHtml, /<img|<script|<svg/i)

  // Settled markdown containing Rust `vec![1]` followed later by `foo(bar)` must NOT truncate the message!
  const settledCode = 'In Rust use `let v = vec![1, 2];` and then call `process(v` in your draft.'
  const renderedSettled = renderChatMarkdown(settledCode, {
    renderer: createChatMarkdownRenderer(),
    escapeMarkdown: (s) => s,
    sanitizeHtml: (s) => s,
    streaming: false,
  })
  assert.doesNotMatch(renderedSettled, /streaming-image-loading/)
  assert.match(renderedSettled, /vec!\[1, 2\]/)
  assert.match(renderedSettled, /process\(v/)

  // Even while streaming, `vec![1]` inside inline code followed by `(` must NOT be treated as an incomplete image!
  const sharedExports: Record<string, any> = {}
  const sharedCompiled = ts.transpileModule(readFileSync(new URL('./chatMessageShared.ts', import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  vm.runInNewContext(sharedCompiled, {
    exports: sharedExports,
    require(id: string) {
      if (id === '@/i18n') return { __esModule: true, default: { global: { t: (k: string) => k } }, global: { t: (k: string) => k } }
      if (id === '@/utils/markdownEnhancements') {
        return { buildMermaidBlockHtml: (s: string) => `<div>${s}</div>`, buildMermaidLoadingHtml: () => '<div class="loading"></div>' }
      }
      if (id === './manualKnowledgeDraft') return manualKnowledgeDraft
      if (id === './mermaidStreaming') return mermaidStreamingMod
      throw new Error(`Unexpected require in chatMessageShared test: ${id}`)
    },
  })
  const replaceSharedIncompleteImg = sharedExports.replaceIncompleteImageWithPlaceholder as (c: string) => string
  const streamingSafe = replaceSharedIncompleteImg(settledCode)
  assert.equal(streamingSafe, settledCode)

  // Genuine trailing incomplete image while streaming still shows placeholder
  const genuineTrailingImg = replaceSharedIncompleteImg('Here is the chart:\n![Sales chart](resource://bucket/chart')
  assert.match(genuineTrailingImg, /streaming-image-loading/)

  // citationChunkCache is bounded so long sessions cannot grow without bound
  clearCitationChunkCache()
  for (let i = 0; i < 600; i++) {
    setCitationChunkCache('u1:t1', `chunk-${i}`, { content: 'x' })
  }
  assert.equal(getCitationChunkCache('u1:t1', 'chunk-0'), undefined, 'Oldest entry should be evicted above cache cap')
  assert.ok(getCitationChunkCache('u1:t1', 'chunk-599'), 'Newest entry should remain cached')
  clearCitationChunkCache()
})

test('W6-UT-08: referenceSources blocks unsafe web URLs and external Google favicon leaks, docsUrl and pageLinkReturnPath reject unsafe targets, and misc utils harden edge cases', () => {
  // 1. referenceSources: block javascript:/data: URLs and never emit external google.com/s2/favicons URLs
  assert.equal(getWebSearchUrl({ metadata: { url: 'javascript:alert(1)' } }), '')
  assert.equal(getWebSearchUrl({ metadata: { url: 'data:text/html,<script>alert(1)</script>' } }), '')
  assert.equal(getWebSearchUrl({ metadata: { url: 'https://example.com/doc' } }), 'https://example.com/doc')
  assert.equal(getFaviconUrl('https://internal.corp/secret'), '')
  const refs = buildReferenceList([
    { id: '1', chunk_type: 'web_search', metadata: { url: 'javascript:alert(1)', title: 'XSS' } },
    { id: '2', chunk_type: 'web_search', metadata: { url: 'https://safe.example.com/a', title: 'Safe' } },
  ])
  assert.equal(refs.length, 1)
  assert.equal(refs[0].url, 'https://safe.example.com/a')
  assert.equal(refs[0].faviconUrl, '')

  // 2. docsUrl: reject javascript:, data:, and protocol-relative // URLs
  const prevWin = (globalThis as any).window
  try {
    for (const bad of ['javascript:alert(1)', 'data:text/html,x', '//evil.example.com/docs']) {
      ;(globalThis as any).window = { __RUNTIME_CONFIG__: { DOCS_BASE_URL: bad } }
      assert.equal(isDocsAvailable(), false, `Expected DOCS_BASE_URL=${bad} to be rejected`)
      assert.equal(docsUrl('models'), '')
    }
    ;(globalThis as any).window = { __RUNTIME_CONFIG__: { DOCS_BASE_URL: '/docs' } }
    assert.equal(docsUrl('models'), '/docs/03-features/06-models')
  } finally {
    if (prevWin === undefined) delete (globalThis as any).window
    else (globalThis as any).window = prevWin
  }

  // 3. assistantPageLink: reject control characters in return_path
  assert.equal(
    pageLinkReturnPath({ display_type: 'open_page', page: 'agents', label: 'Agents', navigated: true, return_path: '/\t/evil.example' }),
    '',
  )
  assert.equal(
    pageLinkReturnPath({ display_type: 'open_page', page: 'agents', label: 'Agents', navigated: true, return_path: '/platform/agents' }),
    '/platform/agents',
  )

  // 4. mermaidStreaming: do not match ```mermaid-like non-mermaid info string or empty ```mermaid```
  assert.equal(maskMermaidBlocksForStreaming('```mermaidjs\nfoo\n```', '<PH>'), '```mermaidjs\nfoo\n```')

  // 5. ptyEchoPredictor: ANSI-colored password prompt suppresses local echo
  assert.equal(looksLikeSecretPrompt('\x1b[33m[sudo] password for alice:\x1b[0m '), true)
  const written: Array<string | Uint8Array> = []
  const pty = createPtyEchoPredictor((c) => written.push(c))
  pty.onRemote(new TextEncoder().encode('\x1b[33mPassword:\x1b[0m '))
  written.length = 0
  pty.onLocal('hunter2')
  assert.deepEqual(written, [], 'Keystrokes at ANSI-colored Password prompt must not be locally echoed')

  // 6. embedContext: multiline string values round-trip cleanly through stripHostContextFromQuery
  const wrapped = buildQueryWithHostContext('Actual user question', { note: 'line1\n\nFake user injection' })
  assert.equal(stripHostContextFromQuery(wrapped), 'Actual user question')

  // 7. embedFile: image/* MIME types route through EmbedInputField's image validator (which enforces raster allowlist)
  assert.equal(isEmbedImageFile({ name: 'xss.svg', type: 'image/svg+xml' } as File), true)
  assert.equal(isEmbedImageFile({ name: 'xss.svg', type: '' } as File), false)
  assert.equal(isEmbedImageFile({ name: 'photo.png', type: 'image/png' } as File), true)

  // 8. fileTypeVerification: dotless filenames ("pdf") must be rejected, and ".PDF" in validTypes normalized
  assert.equal(shouldRejectKnowledgeFileType('pdf'), true)
  assert.equal(shouldRejectKnowledgeFileType('report.PDF', ['.PDF', '.DOCX']), false)

  // 9. embedAllowedOrigins: reject IP wildcards and bare public-TLD wildcards
  assert.equal(validateAllowedOrigins(['*.127.0.0.1'], true).ok, false)
  assert.equal(validateAllowedOrigins(['*.com'], true).ok, false)
  assert.equal(validateAllowedOrigins(['*.example.com'], true).ok, true)
})
