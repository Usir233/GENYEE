// 用 mermaid 自带的解析器校验文档里所有 ```mermaid 代码块。
// 站点构建不会检查图表语法——语法错的图只在浏览器里报错，
// 所以这里在 CI/本地提前拦住。
import { spawnSync } from 'node:child_process'
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative, dirname, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')

function resolveFallbackFrontendModules() {
  const candidates = [resolve(root, '..', 'frontend', 'node_modules')]
  const commonGit = spawnSync('git', ['rev-parse', '--git-common-dir'], { cwd: root, encoding: 'utf8' })
  if (commonGit.status === 0 && commonGit.stdout.trim()) {
    candidates.push(resolve(root, commonGit.stdout.trim(), '..', 'frontend', 'node_modules'))
  }
  for (const candidate of candidates) {
    if (
      existsSync(resolve(candidate, '@xmldom/xmldom/lib/index.js'))
      && existsSync(resolve(candidate, 'mermaid/dist/mermaid.core.mjs'))
    ) {
      return candidate
    }
  }
  return null
}

async function loadMermaid() {
  try {
    const { JSDOM } = await import('jsdom')
    const dom = new JSDOM('<!DOCTYPE html><body></body>', { pretendToBeVisual: true })
    globalThis.window = dom.window
    globalThis.document = dom.window.document
    Object.defineProperty(globalThis, 'navigator', {
      value: dom.window.navigator,
      configurable: true,
    })
    const { default: mermaid } = await import('mermaid')
    return mermaid
  } catch (primaryErr) {
    const fallbackModules = resolveFallbackFrontendModules()
    if (!fallbackModules) throw primaryErr
    const xmldomEntry = resolve(fallbackModules, '@xmldom/xmldom/lib/index.js')
    const mermaidEntry = resolve(fallbackModules, 'mermaid/dist/mermaid.core.mjs')
    const { DOMParser } = await import(pathToFileURL(xmldomEntry).href)
    function SilentDOMParser() {
      return new DOMParser({ onError: () => {} })
    }
    function Node() {}
    function Element() {}
    function DocumentFragment() {}
    function HTMLTemplateElement() {}
    function NamedNodeMap() {}
    const doc = new SilentDOMParser().parseFromString('<!DOCTYPE html><html><body></body></html>', 'text/html')
    doc.createNodeIterator = () => ({ nextNode: () => null })
    doc.implementation.createHTMLDocument = () => doc
    globalThis.document = doc
    globalThis.window = {
      document: doc,
      DOMParser: SilentDOMParser,
      Node,
      Element,
      DocumentFragment,
      HTMLTemplateElement,
      NamedNodeMap,
      NodeFilter: { SHOW_ELEMENT: 1, SHOW_COMMENT: 128, SHOW_TEXT: 4 },
      addEventListener() {},
      getComputedStyle: () => ({ getPropertyValue: () => '' }),
    }
    const { default: mermaid } = await import(pathToFileURL(mermaidEntry).href)
    return mermaid
  }
}

const mermaid = await loadMermaid()
mermaid.initialize({ startOnLoad: false, securityLevel: 'loose' })

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (['node_modules', 'homepage', 'shared', 'scripts', 'deploy', 'static-site', 'releases', 'THIRD_PARTY_NOTICES.md'].includes(name) || name.startsWith('.')) continue
    const path = join(dir, name)
    if (statSync(path).isDirectory()) walk(path, out)
    else if (name.endsWith('.md')) out.push(path)
  }
  return out
}

/** 取出每个 mermaid 块及其起始行号，行号用于定位报错。 */
function blocksOf(text) {
  const lines = text.split('\n')
  const blocks = []
  let start = -1
  let buffer = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (start === -1 && /^\s*```mermaid\s*$/.test(line)) {
      start = i + 1
      buffer = []
    } else if (start !== -1 && /^\s*```\s*$/.test(line)) {
      blocks.push({ line: start + 1, code: buffer.join('\n') })
      start = -1
    } else if (start !== -1) {
      buffer.push(line)
    }
  }
  return blocks
}

let total = 0
const failures = []

for (const file of walk(root)) {
  for (const block of blocksOf(readFileSync(file, 'utf-8'))) {
    total++
    try {
      await mermaid.parse(block.code)
    } catch (error) {
      failures.push({
        file: relative(root, file),
        line: block.line,
        message: String(error?.message ?? error).split('\n').slice(0, 6).join('\n'),
      })
    }
  }
}

console.log(`检查 ${total} 个 mermaid 图，失败 ${failures.length} 个`)
for (const failure of failures) {
  console.log(`\n--- ${failure.file}:${failure.line}\n${failure.message}`)
}
process.exit(failures.length === 0 ? 0 : 1)
