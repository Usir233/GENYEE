import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { promisify } from 'node:util'
import test from 'node:test'
import { build } from 'esbuild'

const chromium = [process.env.BROWSERSKILL_TEST_CHROMIUM, process.env.CHROME_BIN,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser',
].find(candidate => candidate && existsSync(candidate))

test('real DOMPurify preserves Markdown code and KaTeX while rejecting executable HTML', {
  skip: !chromium, timeout: 30_000,
}, async () => {
  const directory = await mkdtemp(path.join(tmpdir(), 'genyee-markdown-security-'))
  try {
    await build({
      stdin: {
        resolveDir: fileURLToPath(new URL('.', import.meta.url)),
        contents: `
          import {createChatMarkdownRenderer, renderChatMarkdown} from './chatMarkdownRenderer.ts';
          import {safeMarkdownToHTML, sanitizeMarkdownHTML} from './security.ts';
          import DOMPurify from 'dompurify';
          import katex from 'katex';
          const render = text => renderChatMarkdown(text, {
            renderer: createChatMarkdownRenderer(), escapeMarkdown: safeMarkdownToHTML,
            sanitizeHtml: sanitizeMarkdownHTML,
          });
          const result = {supported: DOMPurify.isSupported, failures: []};
          const check = (condition, label) => { if (!condition) result.failures.push(label); };
          const code = '<script>greet()</script>\\nfunction greet() { return "12345678-1234-1234-1234-123456789abc"; }';
          const codeHTML = render('~~~html\\n' + code + '\\n~~~');
          const codeDOM = document.createElement('template'); codeDOM.innerHTML = codeHTML;
          check(codeDOM.content.querySelector('pre code')?.textContent.trim() === code, 'code source changed or sanitizer fell back');
          check(!codeDOM.content.querySelector('script'), 'code became executable HTML');
          const payloads = [
            '<script>window.pwned=1</script>', '<iframe src="https://invalid.test"></iframe>',
            '<object data="x"></object>', '<embed src="x">', '<img src=x onerror="window.pwned=1">',
            '<svg onload="window.pwned=1"></svg>', '[bad](javascript:alert(1))',
            '<details open ontoggle="window.pwned=1">text</details>',
          ];
          for (const payload of payloads) {
            const template = document.createElement('template'); template.innerHTML = render(payload);
            check(!template.content.querySelector('script,iframe,object,embed,[onload],[onerror],[ontoggle]'), payload);
            for (const el of template.content.querySelectorAll('[href],[src]')) {
              check(!/^\\s*javascript:/i.test(el.getAttribute('href') || el.getAttribute('src') || ''), payload);
            }
          }
          const mathDOM = document.createElement('template');
          mathDOM.innerHTML = render('$E = mc^2$\\n\\n$$\\\\frac{a}{b}$$');
          check(mathDOM.content.querySelectorAll('.katex').length === 2, 'inline or block formula was lost');
          check(!!mathDOM.content.querySelector('.katex-display .katex-html'), 'display formula lost its rendered layout');
          // GHSA-238p-pmpm-9mq7: inherited trust must not enable raw URL output.
          const inheritedOptions = Object.assign(Object.create({trust: true}), {throwOnError: false});
          const untrustedMathDOM = document.createElement('template');
          untrustedMathDOM.innerHTML = katex.renderToString('\\\\href{javascript:alert(1)}{click}', inheritedOptions);
          check(!untrustedMathDOM.content.querySelector('[href]'), 'KaTeX accepted inherited trust');
          document.getElementById('result').textContent = btoa(JSON.stringify(result));
        `,
      },
      outfile: path.join(directory, 'bundle.js'), bundle: true, platform: 'browser', format: 'iife',
      define: { 'import.meta.env': '{}' }, logLevel: 'silent',
    })
    await writeFile(path.join(directory, 'index.html'), '<!doctype html><pre id="result">pending</pre><script src="bundle.js"></script>')
    const { stdout } = await promisify(execFile)(chromium, [
      '--headless=new', '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
      `--user-data-dir=${path.join(directory, 'profile')}`, '--dump-dom',
      pathToFileURL(path.join(directory, 'index.html')).href,
    ], { timeout: 25_000, maxBuffer: 1024 * 1024 })
    const encoded = stdout.match(/<pre id="result">([^<]+)<\/pre>/)?.[1]
    assert.ok(encoded && encoded !== 'pending', 'browser fixture did not complete')
    const result = JSON.parse(Buffer.from(encoded, 'base64').toString('utf8'))
    assert.equal(result.supported, true, 'must exercise real DOMPurify, not the escaped-text fallback')
    assert.deepEqual(result.failures, [])
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})
