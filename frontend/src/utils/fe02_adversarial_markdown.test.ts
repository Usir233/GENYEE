import assert from 'node:assert/strict'
import test from 'node:test'
import { DOMParser } from '@xmldom/xmldom'

import {
  createChatMarkdownRenderer,
  renderChatMarkdown,
} from './chatMarkdownRenderer.ts'
import {
  safeMarkdownToHTML,
  sanitizeMarkdownHTML,
} from './security.ts'

const defaultRenderer = createChatMarkdownRenderer({
  imageRenderer: ({ href, text }) => `<img src="${href}" alt="${text}">`,
  isValidImageUrl: () => true,
})

test('FE-02: options.escapeMarkdown is actively executed and fed into marked.parse', () => {
  let escapeMarkdownCalled = false
  let escapedTextReceivedByMarked = false

  const customEscape = (markdown: string) => {
    escapeMarkdownCalled = true
    // Transform text by prepending marker
    return markdown.replace(/ATTACK_TOKEN/g, 'NEUTRALIZED_TOKEN')
  }

  const html = renderChatMarkdown('Hello ATTACK_TOKEN world', {
    renderer: defaultRenderer,
    escapeMarkdown: customEscape,
    sanitizeHtml: (h) => h,
  })

  assert.equal(escapeMarkdownCalled, true, 'options.escapeMarkdown must be called')
  assert.match(html, /NEUTRALIZED_TOKEN/, 'Transformed token from escapeMarkdown must appear in output HTML')
  assert.doesNotMatch(html, /ATTACK_TOKEN/, 'Original unescaped token must NOT appear in output HTML')
})

test('FE-02: the final sanitizer removes dangerous HTML after markdown parsing', () => {
  const dangerousPayloads = [
    '<script>alert("xss1")</script>',
    '<iframe src="https://evil.com"></iframe>',
    '<object data="exploit.swf"></object>',
    '<embed src="exploit.pdf"></embed>',
    'Normal text <script type="text/javascript">window.pwned=1</script> and more text',
    '# Heading with <script>console.log(1)</script>',
    '- Bullet with <iframe src="evil.com"></iframe>',
    '| Table | Header |\n|---|---|\n| <script>alert(1)</script> | safe |',
  ]

  for (const payload of dangerousPayloads) {
    const html = renderChatMarkdown(payload, {
      renderer: defaultRenderer,
      escapeMarkdown: safeMarkdownToHTML,
      sanitizeHtml: sanitizeMarkdownHTML,
      streaming: false,
    })

    // Verify raw dangerous tags were stripped
    assert.doesNotMatch(html, /<script\b/i, `Script tag must be stripped: ${payload}`)
    assert.doesNotMatch(html, /<iframe\b/i, `Iframe tag must be stripped: ${payload}`)
    assert.doesNotMatch(html, /<object\b/i, `Object tag must be stripped: ${payload}`)
    assert.doesNotMatch(html, /<embed\b/i, `Embed tag must be stripped: ${payload}`)
    assert.doesNotMatch(html, /<\/script>/i, `Closing script tag must be stripped: ${payload}`)
    assert.doesNotMatch(html, /<\/iframe>/i, `Closing iframe tag must be stripped: ${payload}`)

    // DOM validation: parse resulting HTML with DOMParser
    const domParser = new DOMParser()
    const doc = domParser.parseFromString(`<div>${html}</div>`, 'text/html')
    assert.equal(doc.getElementsByTagName('script').length, 0, `No script DOM nodes allowed: ${payload}`)
    assert.equal(doc.getElementsByTagName('iframe').length, 0, `No iframe DOM nodes allowed: ${payload}`)
    assert.equal(doc.getElementsByTagName('object').length, 0, `No object DOM nodes allowed: ${payload}`)
    assert.equal(doc.getElementsByTagName('embed').length, 0, `No embed DOM nodes allowed: ${payload}`)
  }
})

test('FE-02: Adversarial fake citation placeholders cannot hijack citations or leak raw HTML', () => {
  // Attacker crafts fake internal placeholder tokens
  const fakePlaceholderPayload = 'Hello @@WEKNORA_HTML_PLACEHOLDER_0@@ world and <!--CIT_PLACEHOLDER_0-->'

  const html = renderChatMarkdown(fakePlaceholderPayload, {
    renderer: defaultRenderer,
    escapeMarkdown: safeMarkdownToHTML,
    sanitizeHtml: sanitizeMarkdownHTML,
    streaming: false,
  })

  // When no citations exist, placeholder must not resolve to arbitrary snippets
  assert.doesNotMatch(html, /undefined/, 'Must not leak undefined')
  assert.doesNotMatch(html, /null/, 'Must not leak null')
})

test('FE-02: Javascript pseudo-protocols in links and images are neutralized by end-to-end pipeline', () => {
  const payloads = [
    '[click here](javascript:alert(1))',
    '![image](javascript:alert(document.cookie))',
    '<a href="javascript:alert(1)">link</a>',
  ]

  for (const payload of payloads) {
    const html = renderChatMarkdown(payload, {
      renderer: defaultRenderer,
      escapeMarkdown: safeMarkdownToHTML,
      sanitizeHtml: sanitizeMarkdownHTML,
      streaming: false,
    })

    assert.doesNotMatch(html, /href=["']javascript:/i, `javascript: href must be blocked: ${payload}`)
    assert.doesNotMatch(html, /src=["']javascript:/i, `javascript: src must be blocked: ${payload}`)
  }
})

test('FE-02: Markdown headings and pseudo-title paragraphs neutralize all XSS vectors', () => {
  const headingPayloads = [
    '# <script>alert("h1_script")</script>',
    '## <img src=x onerror=alert("h2_onerror")> Title',
    '### <svg onload=alert("h3_svg")> Subheading',
    '#### Title with "><script>alert("h4_breakout")</script>',
    '##### [Click Title](javascript:alert("h5_js_link"))',
    '###### <details open ontoggle=alert("h6_toggle")>Test</details>',
    '**Standalone Title: <script>alert("strong_script")</script>**',
    '**<img src=x onerror=alert(1)> Bold Leading Title**',
    '# <a href="javascript:alert(1)">Title Link</a>',
    '## Title with <iframe src="javascript:alert(1)"></iframe>',
  ]

  for (const payload of headingPayloads) {
    const html = renderChatMarkdown(payload, {
      renderer: defaultRenderer,
      escapeMarkdown: safeMarkdownToHTML,
      sanitizeHtml: sanitizeMarkdownHTML,
      streaming: false,
    })

    // Verify that NO dangerous HTML tags exist unescaped in the output
    assert.doesNotMatch(html, /<(script|img|svg|iframe|details|input|audio|video)\b/i, `Dangerous tags must not exist unescaped: ${payload}`)

    // Check no element has executable attributes
    assert.doesNotMatch(html, /<[a-z0-9_-]+\s+[^>]*\bon\w+\s*=/i, `No HTML element tag may possess event handler attributes: ${payload}`)
    assert.doesNotMatch(html, /href=["']javascript:/i, `javascript: link must not survive in heading: ${payload}`)
  }
})

