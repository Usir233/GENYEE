import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import Papa from 'papaparse'
import * as XLSX from 'xlsx'
import enUS from '../../../i18n/locales/en-US.ts'

const localRequire = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./FAQEntryManager.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'faq-import-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function lookupLocaleString(key: string): string {
  let cursor: unknown = enUS
  for (const segment of key.split('.')) {
    if (!cursor || typeof cursor !== 'object' || !(segment in cursor)) return key
    cursor = (cursor as Record<string, unknown>)[segment]
  }
  return typeof cursor === 'string' ? cursor : key
}

function fixture() {
  const requests: any[] = []
  const api = new Proxy({}, {
    get: (_, key) => async (...args: any[]) => {
      if (key === 'upsertFAQEntries') requests.push(JSON.parse(JSON.stringify(args)))
      return { data: { items: [], list: [], total: 0 } }
    },
  })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(id: string) {
      if (id === 'vue') return { ...Vue, onMounted() {}, onUnmounted() {} }
      if (id === 'papaparse' || id === 'xlsx') return localRequire(id)
      if (id === 'vue-i18n') return { useI18n: () => ({ t: lookupLocaleString }) }
      if (id === 'vue-router') return { useRouter: () => ({}) }
      if (id === 'tdesign-vue-next') return { MessagePlugin: { warning() {}, error() {}, success() {} } }
      if (id === '@/api/knowledge-base') return api
      if (id === '@/stores/auth') return { useAuthStore: () => ({ hasRole: () => true }) }
      if (id === '@/stores/organization') return { useOrganizationStore: () => ({ sharedKnowledgeBases: [] }) }
      if (id === '@/stores/chatResources') return { useChatResourcesStore: () => ({ rawKnowledgeBases: [] }) }
      if (id === '@/stores/ui') return { useUIStore: () => ({ clearSelectedTagIds() {} }) }
      if (id.includes('useConfirmDelete')) return { useConfirmDelete: () => () => {} }
      if (id.includes('assistantDataChange')) return { useAssistantDataChange() {} }
      if (id.endsWith('.vue')) return { default: {} }
      if (id.startsWith('.')) return localRequire(id)
      return { default: {} }
    },
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    window: { addEventListener() {}, removeEventListener() {} },
    requestAnimationFrame: (callback: () => void) => { callback(); return 0 },
    setTimeout, clearTimeout, setInterval, clearInterval, console,
  })
  const component = exports.default
  component.render = () => null
  const renderer = Vue.createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = Vue.ref<any>()
  const app = renderer.createApp({ render: () => Vue.h(component, { kbId: 'faq-kb', ref: instance }) })
  app.mount({})
  return { vm: instance.value, requests, close: () => app.unmount() }
}

for (const format of ['json', 'csv', 'xlsx']) {
  for (const setting of [true, false, undefined]) {
    test(`${format} import preserves operational fields (${setting}) in the submitted request`, async () => {
      const f = fixture()
      try {
        const entry = {
          standard_question: '如何联系支持？', answers: ['邮件联系', '电话联系'],
          similar_questions: ['怎样寻求帮助？'], negative_questions: [], tag_name: '支持',
          answer_strategy: setting === undefined ? undefined : setting ? 'all' : 'random',
          is_recommended: setting, is_enabled: setting,
        }
        const row = {
          '问题(必填)': entry.standard_question, '机器人回答(必填-多个用##分隔)': entry.answers.join('##'),
          '相似问题': entry.similar_questions.join('##'), '分类': entry.tag_name,
          '是否全部回复': setting === undefined ? '' : String(setting).toUpperCase(),
          '是否禁止被推荐': setting === undefined ? '' : String(!setting).toUpperCase(),
          '是否停用': setting === undefined ? '' : String(!setting).toUpperCase(),
        }
        const workbook = XLSX.utils.book_new()
        XLSX.utils.book_append_sheet(workbook, XLSX.utils.json_to_sheet([row]), 'FAQ')
        const file = {
          name: `import.${format}`,
          text: async () => format === 'json' ? JSON.stringify([entry]) : Papa.unparse([row]),
          arrayBuffer: async () => XLSX.write(workbook, { type: 'array', bookType: 'xlsx' }),
        }
        await f.vm.processFile(file)
        await f.vm.handleImport()
        assert.equal(f.requests.length, 1)
        const [kbId, payload] = f.requests[0]
        assert.equal(kbId, 'faq-kb')
        assert.equal(payload.mode, 'append')
        assert.deepEqual(payload.entries, [JSON.parse(JSON.stringify(entry))])
      } finally { f.close() }
    })
  }
}

test('FAQ import rejects oversized files, handles uppercase extensions, sanitizes malformed JSON fields, and isolates __proto__ CSV headers', async () => {
  const f = fixture()
  try {
    // 1. Oversized file (>20 MiB) must be rejected before reading into memory
    let readAttempted = false
    await f.vm.processFile({
      name: 'huge.json',
      size: 25 * 1024 * 1024,
      text: async () => { readAttempted = true; return '[]' },
    })
    assert.equal(readAttempted, false)
    assert.equal(f.vm.importState.preview.length, 0)

    // 2. Non-object JSON array items must reject cleanly instead of throwing TypeError
    await f.vm.processFile({
      name: 'bad.json',
      size: 10,
      text: async () => '[null]',
    })
    assert.equal(f.vm.importState.preview.length, 0)

    // 3. Uppercase .JSON extension + malformed field types are coerced/sanitized safely
    await f.vm.processFile({
      name: 'IMPORT.JSON',
      size: 200,
      text: async () => JSON.stringify([{
        standard_question: '  如何重置密码？ ',
        answers: '单条字符串回答',
        similar_questions: ['怎么改密码', 123, null, ''],
        negative_questions: { bad: true },
        tag_id: -5,
        tag_name: '账户',
        answer_strategy: 'evil_mode',
        is_enabled: 'false',
        is_recommended: 'yes',
      }]),
    })
    assert.equal(f.vm.importState.preview.length, 1)
    assert.deepEqual(JSON.parse(JSON.stringify(f.vm.importState.preview[0])), {
      standard_question: '如何重置密码？',
      answers: ['单条字符串回答'],
      similar_questions: ['怎么改密码'],
      negative_questions: [],
      tag_name: '账户',
      is_enabled: false,
      is_recommended: true,
    })

    // 4. CSV with __proto__ column header must not pollute record prototype
    await f.vm.processFile({
      name: 'SAFE.CSV',
      size: 120,
      text: async () => '__proto__,问题,机器人回答\n"{"polluted":true}",Q1,A1\n',
    })
    assert.equal(f.vm.importState.preview.length, 1)
    assert.equal(({} as any).polluted, undefined)
    assert.equal(f.vm.importState.preview[0].standard_question, 'Q1')
  } finally {
    f.close()
  }
})
