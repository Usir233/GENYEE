import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test, { type TestContext } from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref, type Component } from 'vue'
import * as filePreview from '../utils/filePreview'

const require = createRequire(import.meta.url)
const filename = new URL('./DocumentPreview.vue', import.meta.url)
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename: filename.pathname })
const script = compileScript(descriptor, { id: 'preview-request-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const flush = () => new Promise<void>(resolve => setImmediate(resolve))

interface MockElement {
  childNodes: unknown[]
  innerHTML: string
  replaceChildren: (...children: unknown[]) => void
}

interface PreviewProps {
  knowledgeId: string
  fileName: string
  fileType: string
  active: boolean
  sourceBlob?: Blob
}

interface PreviewVm {
  loadPreview: () => Promise<void>
  textContent: string
  error: string
  loading: boolean
  blobUrl: string
  epubHtml: string
  docxContainer: MockElement | null
}

type HostNode = Record<string, unknown> | MockElement

function element(): MockElement {
  return {
    childNodes: [] as unknown[],
    get innerHTML() { return this.childNodes.join('') },
    set innerHTML(value: string) { this.childNodes = value ? [value] : [] },
    replaceChildren(...children: unknown[]) { this.childNodes = children },
  }
}
function fixture(t: TestContext, dependencies: Record<string, unknown> = {}) {
  const exports: { default?: Component & { render?: () => null } } = {}, created: string[] = [], revoked: string[] = []
  const props = reactive<PreviewProps>({ knowledgeId: 'A', fileName: 'a.txt', fileType: 'txt', active: false })
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name in dependencies) return dependencies[name]
      if (name === 'vue' || name === 'highlight.js') return require(name)
      if (name === '@/utils/filePreview') return filePreview
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/utils/security') return { sanitizeHTML: (html: string) => html }
      if (name === '@/utils/sourceImage') return { indexEmbeddedSourceImages: async () => {} }
      return {}
    },
    Blob, TextDecoder, console,
    URL: { createObjectURL: () => { const url = `blob:preview-${created.length}`; created.push(url); return url }, revokeObjectURL: (url: string) => revoked.push(url) },
    document: { createElement: element, addEventListener() {}, removeEventListener() {}, activeElement: {} },
    window: { addEventListener() {}, removeEventListener() {} },
  })
  const component = exports.default!
  component.render = () => null
  const renderer = createRenderer<HostNode, HostNode>({
    createElement: element, createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<PreviewVm>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  let closed = false
  const close = () => { if (!closed) { closed = true; app.unmount() } }
  t.after(close)
  return { vm: instance.value!, props, created, revoked, close }
}

for (const outcome of ['success', 'failure'] as const) {
  test(`a late ${outcome} from A cannot overwrite the current B preview`, async t => {
    const pending = { A: deferred<Blob>(), B: deferred<Blob>() }
    const f = fixture(t, { '@/api/knowledge-base/index': { previewKnowledgeFile: (id: 'A' | 'B') => pending[id].promise } })
    const a = f.vm.loadPreview()
    f.props.knowledgeId = 'B'; f.props.fileName = 'b.txt'; await nextTick()
    const b = f.vm.loadPreview()
    pending.B.resolve(new Blob(['B-current'])); await b
    if (outcome === 'success') pending.A.resolve(new Blob(['A-stale']))
    else pending.A.reject(new Error('A failed late'))
    await a
    assert.equal(f.vm.textContent, 'B-current')
    assert.equal(f.vm.error, '')
    assert.equal(f.vm.loading, false)
  })
}

test('a superseded request cannot clear the spinner while B is still loading', async t => {
  const pending = { A: deferred<Blob>(), B: deferred<Blob>() }
  const f = fixture(t, { '@/api/knowledge-base/index': { previewKnowledgeFile: (id: 'A' | 'B') => pending[id].promise } })
  const a = f.vm.loadPreview()
  f.props.knowledgeId = 'B'; await nextTick()
  const b = f.vm.loadPreview()
  pending.A.resolve(new Blob(['A'])); await a
  assert.equal(f.vm.loading, true)
  pending.B.resolve(new Blob(['B'])); await b
  assert.equal(f.vm.textContent, 'B')
})

test('a new Blob of the same name, MIME type and byte size reloads; the same Blob stays cached', async t => {
  const f = fixture(t)
  f.props.sourceBlob = new Blob(['old'], { type: 'text/plain' }); await nextTick()
  await f.vm.loadPreview()
  const replacement = new Blob(['new'], { type: 'text/plain' })
  let reads = 0
  replacement.text = async () => { reads++; return 'new' }
  f.props.sourceBlob = replacement; await nextTick()
  await f.vm.loadPreview(); await f.vm.loadPreview()
  assert.equal(f.vm.textContent, 'new')
  assert.equal(reads, 1)
})

test('a delayed text decode cannot commit after another preview has rendered', async t => {
  const text = deferred<string>(), old = new Blob(['old'])
  old.text = () => text.promise
  const f = fixture(t)
  f.props.sourceBlob = old; await nextTick()
  const a = f.vm.loadPreview(); await flush()
  f.props.sourceBlob = new Blob(['new']); await nextTick()
  await f.vm.loadPreview()
  text.resolve('old'); await a
  assert.equal(f.vm.textContent, 'new')
})

test('a response arriving after unmount cannot create or retain object URLs', async t => {
  const pending = deferred<Blob>()
  const f = fixture(t, { '@/api/knowledge-base/index': { previewKnowledgeFile: () => pending.promise } })
  f.props.fileType = 'png'; await nextTick()
  const load = f.vm.loadPreview()
  f.close()
  pending.resolve(new Blob(['png'], { type: 'image/png' })); await load
  assert.equal(f.created.length, 0)
  assert.equal(f.vm.blobUrl, '')
})

test('a superseded EPUB render revokes its own resources without replacing current content', async t => {
  const rendered = deferred<{ html: string; objectUrls: string[] }>()
  const f = fixture(t, { '@/utils/epubPreview': { renderEpubPreview: () => rendered.promise } })
  f.props.sourceBlob = new Blob(['epub']); f.props.fileType = 'epub'; await nextTick()
  const a = f.vm.loadPreview(); await flush()
  f.props.sourceBlob = new Blob(['new']); f.props.fileType = 'txt'; await nextTick()
  await f.vm.loadPreview()
  rendered.resolve({ html: 'old-epub', objectUrls: ['blob:epub-old'] }); await a
  assert.equal(f.vm.textContent, 'new')
  assert.equal(f.vm.epubHtml, '')
  assert.ok(f.revoked.includes('blob:epub-old'))
})

test('overlapping DOCX renderers cannot write into the same visible container', async t => {
  const jobs: Array<{ target: ReturnType<typeof element>; done: ReturnType<typeof deferred<void>> }> = []
  const f = fixture(t, { 'docx-preview': { renderAsync: (_blob: Blob, target: ReturnType<typeof element>) => {
    const done = deferred<void>(); jobs.push({ target, done }); return done.promise
  } } })
  const visible = element()
  f.vm.docxContainer = visible
  f.props.sourceBlob = new Blob(['old']); f.props.fileType = 'docx'; await nextTick()
  const a = f.vm.loadPreview(); await flush()
  f.props.sourceBlob = new Blob(['new']); f.props.fileName = 'b.docx'; await nextTick()
  const b = f.vm.loadPreview(); await flush()
  jobs[1].target.innerHTML = 'B-docx'; jobs[1].done.resolve(); await b
  jobs[0].target.innerHTML = 'A-docx'; jobs[0].done.resolve(); await a
  assert.equal(visible.innerHTML, 'B-docx')
})
