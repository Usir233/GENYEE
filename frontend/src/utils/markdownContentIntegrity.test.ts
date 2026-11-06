import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { createChatMarkdownRenderer, renderChatMarkdown } from './chatMarkdownRenderer.ts'
import { safeMarkdownToHTML } from './security.ts'

const id = '12345678-1234-1234-1234-123456789abc'
const labels = { kb: '知识库', doc: '文档', chunk: '分块' }
const render = (text: string, preprocess = safeMarkdownToHTML) => renderChatMarkdown(text, {
  renderer: createChatMarkdownRenderer(),
  escapeMarkdown: preprocess,
  // Test source fidelity independently of the DOM sanitizer (browser-tested separately).
  sanitizeHtml: html => html,
  entityLinkLabels: labels,
})

test('Markdown code examples preserve function calls, literal HTML and UUIDs', () => {
  const text = '```html\n<script>greet()</script>\n```\n\n' +
    `\`\`\`js\nfunction greet() { return "${id}"; }\ngreet();\n\`\`\``
  const html = render(text)
  assert.match(html, /&lt;script&gt;greet\(\)&lt;\/script&gt;/)
  assert.match(html, /function greet\(\)/)
  assert.match(html, /greet\(\);/)
  assert.match(html, new RegExp(id))
  assert.doesNotMatch(html, /<script>/)
})

test('the Agent answer entry preserves code and navigable UUID links', () => {
  const source = readFileSync(new URL('../views/chat/components/AgentStreamDisplay.vue', import.meta.url), 'utf8')
  const labelsSource = source.slice(source.indexOf('const UUID_RE ='), source.indexOf('// 根元素引用'))
  const entry = source.match(/const renderMarkdownContent = \(content: unknown\): string => \{[\s\S]*?\n\};/)?.[0]
  assert.ok(entry)
  const context: Record<string, any> = {
    TOOL_NAME_KEYS: {},
    i18n: { global: { t: (key: string) => key } },
    safeMarkdownToHTML,
    renderAgentMarkdown: render,
  }
  runInNewContext(ts.transpileModule(`${labelsSource}\n${entry}\nglobalThis.renderAnswer = renderMarkdownContent`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText, context)
  const html = context.renderAnswer(
    `[知识库](genyee://kb/${id})\n\n[文件](https://example.test/files/${id}.pdf)\n\n\`\`\`js\ngreet();\n\`\`\``,
  )
  assert.match(html, new RegExp(`data-entity-id="${id}"`))
  assert.match(html, new RegExp(`href="https://example.test/files/${id}\\.pdf"`))
  assert.match(html, /greet\(\);/)
})
