import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'

function runWidgetInSandbox(origin = 'https://host.example.com') {
  const widgetCode = readFileSync(new URL('./public/genyee-widget.js', import.meta.url), 'utf8')
  const appended: any[] = []
  const warnings: any[][] = []

  const makeEl = (tag: string) => {
    const attrs = new Map<string, string>()
    const children: any[] = []
    return {
      tagName: tag.toUpperCase(),
      type: '',
      textContent: '',
      src: '',
      style: { cssText: '', display: '', opacity: '' },
      parentNode: null as any,
      children,
      contentWindow: {
        postMessage() {},
      },
      setAttribute(k: string, v: string) {
        attrs.set(k, String(v))
      },
      getAttribute(k: string) {
        return attrs.get(k) ?? null
      },
      appendChild(child: any) {
        child.parentNode = this
        children.push(child)
        return child
      },
      removeChild(child: any) {
        const idx = children.indexOf(child)
        if (idx >= 0) children.splice(idx, 1)
        child.parentNode = null
      },
      addEventListener() {},
    }
  }

  const body = makeEl('body')
  const origAppend = body.appendChild.bind(body)
  body.appendChild = (child: any) => {
    appended.push(child)
    return origAppend(child)
  }

  const sandbox: any = {
    URL,
    encodeURIComponent,
    Number,
    Math,
    Promise,
    setTimeout: () => 1,
    clearTimeout: () => {},
    console: {
      warn: (...args: any[]) => warnings.push(args),
      error: () => {},
    },
    document: {
      currentScript: null,
      createElement: makeEl,
      body,
    },
    location: {
      origin,
      href: `${origin}/page`,
    },
    addEventListener() {},
    removeEventListener() {},
  }
  sandbox.window = sandbox

  vm.runInNewContext(widgetCode, sandbox)
  return { GENYEE: sandbox.GENYEE, body, appended, warnings }
}

test('W6-BR-01: genyee-widget.js rejects javascript:, data:, and protocol-less baseUrl and non-finite dimensions', () => {
  for (const unsafeBase of [
    'javascript:alert(1)//',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'ftp://files.example.com',
  ]) {
    const { GENYEE, body } = runWidgetInSandbox()
    const inst = GENYEE.init({
      channel: 'chan-1',
      token: 'pub-tok',
      baseUrl: unsafeBase,
    })
    assert.equal(inst, null, `Expected unsafe baseUrl ${unsafeBase} to be rejected`)
    assert.equal(body.children.length, 0, `No DOM nodes should be mounted for ${unsafeBase}`)
  }

  const { GENYEE, body } = runWidgetInSandbox()
  const ok = GENYEE.init({
    channel: 'chan-1',
    token: 'pub-tok',
    baseUrl: 'https://embed.example.com',
    width: Infinity,
    height: NaN,
  })
  assert.ok(ok, 'Valid https baseUrl must initialize widget')
  const panel = body.children.find((el: any) => el.tagName === 'DIV')
  assert.ok(panel)
  assert.doesNotMatch(panel.style.cssText, /Infinity|NaN/)
  assert.match(panel.style.cssText, /width:400px/)
  assert.match(panel.style.cssText, /height:600px/)
  ok.destroy()
})

test('W6-BR-02: index.html Lite restore preserves URL hash/search (e.g. OIDC callback) and package.json.md5 matches package.json', () => {
  const indexHtml = readFileSync(new URL('./index.html', import.meta.url), 'utf8')
  const scripts = [...indexHtml.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1])
  const liteRestoreScript = scripts.find((s) => s.includes('genyee_lite_last_path'))
  assert.ok(liteRestoreScript, 'Expected Lite last-path restore script in index.html')

  const runLiteRestore = (loc: { pathname: string; search: string; hash: string }) => {
    const replaceCalls: string[] = []
    const ctx: any = {
      localStorage: {
        getItem: (k: string) => (k === 'genyee_lite_mode' ? 'true' : null),
      },
      sessionStorage: {
        getItem: (k: string) => (k === 'genyee_lite_last_path' ? '/platform/agents' : null),
      },
      window: {
        location: loc,
        history: {
          state: null,
          replaceState(_state: any, _title: string, url: string) {
            replaceCalls.push(url)
          },
        },
      },
    }
    vm.runInNewContext(liteRestoreScript, ctx)
    return replaceCalls
  }

  // Must NOT clobber an OIDC callback hash or explicit query deep-link on `/`
  assert.deepEqual(
    runLiteRestore({ pathname: '/', search: '', hash: '#oidc_result=eyJzdWNjZXNzIjp0cnVlfQ' }),
    [],
    'Lite restore must not wipe #oidc_result callback hash',
  )
  assert.deepEqual(
    runLiteRestore({ pathname: '/platform/knowledge-bases', search: '?cmdk=budget', hash: '' }),
    [],
    'Lite restore must not clobber explicit search params',
  )
  // Bare `/` with no search/hash still restores saved Lite path
  assert.deepEqual(
    runLiteRestore({ pathname: '/', search: '', hash: '' }),
    ['/platform/agents'],
  )

  // Verify package.json.md5 matches package.json
  const pkgBytes = readFileSync(new URL('./package.json', import.meta.url))
  const expectedMd5 = createHash('md5').update(pkgBytes).digest('hex')
  const recordedMd5 = readFileSync(new URL('./package.json.md5', import.meta.url), 'utf8').trim()
  assert.equal(recordedMd5, expectedMd5, 'package.json.md5 must match current package.json MD5')
})
