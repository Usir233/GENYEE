import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test, { type TestContext } from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, ref } from 'vue'

const require = createRequire(import.meta.url)

test('MarkdownTestPage clears streamTimer and customMermaidTimer on unmount', async (t: TestContext) => {
  t.mock.timers.enable({ apis: ['setInterval', 'setTimeout'] })
  const filename = fileURLToPath(new URL('./MarkdownTestPage.vue', import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: 'markdown-test-page-test' }).content
    .replace('__expose();', '')
    .replace('return __returned__', '__expose(__returned__); return __returned__')
  const compiled = ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  let enhanceCalls = 0
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    setInterval,
    clearInterval,
    setTimeout,
    clearTimeout,
    console,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === '@/utils/security') return { sanitizeMarkdownHTML: (s: string) => s }
      if (name === '@/utils/chatMarkdownRenderer') {
        return {
          createChatMarkdownRenderer: () => ({ render: (s: string) => s }),
          renderChatMarkdown: (_md: unknown, s: string) => s,
        }
      }
      if (name === '@/utils/mermaidShared') {
        return {
          appendMermaidSvgCache: async () => [],
          ensureMermaidInitialized: async () => {},
          enhanceMarkdownContainer: async () => {
            enhanceCalls += 1
          },
          createMermaidCodeRenderer: () => () => '',
        }
      }
      if (name === '@/utils/streamingMermaid') {
        return {
          replaceIncompleteMermaidWithPlaceholder: (s: string) => s,
          prepareStreamingMermaidMarkdown: (s: string) => s,
          extractMermaidCodes: () => [],
          injectCachedMermaidSvg: (s: string) => s,
        }
      }
      if (name === '@/utils/chatMessageShared') {
        return {
          extractMermaidCodes: () => [],
          injectCachedMermaidSvg: (s: string) => s,
        }
      }
      return { default: {} }
    },
  })

  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}),
    createText: () => ({}),
    createComment: () => ({}),
    insert() {},
    remove() {},
    setElementText() {},
    setText() {},
    patchProp() {},
    parentNode: () => null,
    nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ref: instance }) })
  app.mount({})
  const vm = instance.value

  await nextTick()
  const baselineEnhanceCalls = enhanceCalls

  // Start both interval streaming and debounced custom mermaid timer
  vm.startStream()
  t.mock.timers.tick(50)
  await nextTick()
  await nextTick()
  await nextTick()
  const bufferBeforeUnmount = vm.streamBuffer
  assert.ok(bufferBeforeUnmount.length > 0)

  vm.customInput = '```mermaid\ngraph TD\nA-->B\n```'
  await nextTick()

  // Unmount before timers complete
  const enhanceBeforeTick = enhanceCalls
  app.unmount()

  t.mock.timers.tick(500)
  await nextTick()
  await nextTick()

  assert.equal(vm.streamBuffer, bufferBeforeUnmount, 'streamTimer must stop mutating streamBuffer after unmount')
  assert.equal(enhanceCalls, enhanceBeforeTick, 'customMermaidTimer must not run refreshMermaid after unmount')
  assert.ok(baselineEnhanceCalls >= 0)
})
