import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

function readComponent(relPath: string): string {
  return readFileSync(new URL(relPath, import.meta.url), 'utf8')
}

function compileSfcComponent(relPath: string, customRequire: (id: string) => any, globals: Record<string, any> = {}) {
  const source = readComponent(relPath)
  const { descriptor } = parse(source, { filename: relPath })
  const compiled = compileScript(descriptor, {
    id: `test-${relPath.replace(/\W+/g, '-')}`,
    inlineTemplate: true,
  })
  const js = ts.transpileModule(compiled.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports: any = {}
  vm.runInNewContext(js, {
    exports,
    console: { error: () => {}, warn: () => {} },
    Promise,
    setTimeout,
    clearTimeout,
    ...globals,
    require: customRequire,
  })
  return exports.default
}

function makeHostNode(tag: string, text = '') {
  return vue.markRaw({
    tag,
    text,
    props: {} as Record<string, any>,
    children: [] as any[],
    parent: null as any,
    focus: () => {},
  })
}

function createHostRenderer(root: any) {
  return vue.createRenderer<any, any>({
    createElement: (tag) => makeHostNode(tag),
    createText: (text) => makeHostNode('#text', text),
    createComment: () => makeHostNode('#comment'),
    insert: (child, parent, anchor) => {
      if (child.parent) child.parent.children = child.parent.children.filter((c: any) => c !== child)
      child.parent = parent
      const idx = anchor ? parent.children.indexOf(anchor) : -1
      if (idx < 0) parent.children.push(child)
      else parent.children.splice(idx, 0, child)
    },
    remove: (child) => {
      if (child.parent) child.parent.children = child.parent.children.filter((c: any) => c !== child)
      child.parent = null
    },
    setElementText: (el, text) => { el.text = text; el.children = [] },
    setText: (el, text) => { el.text = text },
    patchProp: (el, key, _prev, next) => { el.props[key] = next },
    parentNode: (el) => el.parent,
    nextSibling: (el) => el.parent?.children[el.parent.children.indexOf(el) + 1] || null,
    querySelector: () => root,
  })
}

const collectText = (el: any): string =>
  (el.text || '') + (el.children || []).map(collectText).join('')

test('CredentialResource preserves configured state when parent switches api and meta in the same flush', async () => {
  const toasts: string[] = []
  const Comp = compileSfcComponent('./credentials/CredentialResource.vue', (id) => {
    if (id === 'vue') return vue
    if (id === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
    if (id === 'tdesign-vue-next') return { MessagePlugin: { success: (m: string) => toasts.push(m), error: (m: string) => toasts.push(m) } }
    throw new Error(`Unexpected import: ${id}`)
  })

  const currentId = vue.ref('model-a')
  const metaMap: Record<string, { api_key: { configured: boolean } }> = {
    'model-a': { api_key: { configured: true } },
    'model-b': { api_key: { configured: true } },
  }

  let pendingSaveResolve: ((v: any) => void) | null = null
  const Parent = vue.defineComponent({
    setup() {
      // Mirror ModelEditorDialog / WebSearchSettings: meta computed is declared before api computed
      const meta = vue.computed(() => metaMap[currentId.value])
      const api = vue.computed(() => {
        const id = currentId.value
        return {
          save: () => new Promise((resolve) => {
            if (id === 'model-a') pendingSaveResolve = resolve
            else resolve(metaMap[id])
          }),
          remove: async () => {},
        }
      })
      return () => vue.h(Comp, {
        fields: [{ key: 'api_key', label: 'API Key' }],
        meta: meta.value,
        api: api.value,
      })
    },
  })

  const root = makeHostNode('root')
  const renderer = createHostRenderer(root)
  const app = renderer.createApp(Parent)
  app.component('t-icon', { render: () => null })
  app.component('t-button', { props: ['onClick'], setup(p: any, { slots }: any) { return () => vue.h('button', { onClick: p.onClick }, slots.default?.()) } })
  app.component('t-input', { props: ['modelValue'], render: () => null })
  app.mount(root)
  await vue.nextTick()

  assert.match(collectText(root), /credential\.configured/)

  // Switch directly from model-a (configured) to model-b (configured) in a single reactive flush!
  currentId.value = 'model-b'
  await vue.nextTick()

  assert.match(
    collectText(root),
    /credential\.configured/,
    'model-b configured credential must NOT be wiped to unconfigured when watch(api) runs after watch(meta)',
  )
  void pendingSaveResolve
  app.unmount()
})

test('TenantSelector does not drop clearSearch while searchTenants is in flight or overwrite setAllTenants with filtered results', async () => {
  const searchCalls: Array<{ keyword?: string; resolve: (items: any[]) => void }> = []
  const allTenantsUpdates: any[][] = []

  const Comp = compileSfcComponent(
    './TenantSelector.vue',
    (id) => {
      if (id === 'vue') return vue
      if (id === 'vue-i18n') return { useI18n: () => ({ t: (k: string) => k }) }
      if (id === 'tdesign-vue-next') return { MessagePlugin: { error: () => {}, info: () => {} } }
      if (id === '@/stores/auth') {
        return {
          useAuthStore: () => ({
            effectiveTenantId: 1,
            defaultTenantId: 1,
            canAccessAllTenants: true,
            canCreateTenant: false,
            memberships: [],
            allTenants: [],
            setAllTenants: (list: any[]) => allTenantsUpdates.push(list),
            setSelectedTenant: () => {},
            refreshFromAuthMe: async () => {},
          }),
        }
      }
      if (id === '@/api/tenant' || id.startsWith('@/api/')) {
        return {
          searchTenants: (params: { keyword?: string }) => new Promise((resolve) => {
            searchCalls.push({
              keyword: params.keyword,
              resolve: (items) => resolve({ success: true, data: { items, total: items.length } }),
            })
          }),
          persistLastActiveTenantPreference: async () => {},
        }
      }
      if (id === '@/composables/useRoleLabel') {
        return { useRoleLabel: () => ({ formatRole: (r: string) => r }) }
      }
      if (id.startsWith('@/utils/') || id.startsWith('@/components/')) {
        return {
          __esModule: true,
          stashTenantSwitchToast: () => {},
          navigateAfterTenantSwitch: () => {},
          persistLastActiveTenantPreference: async () => {},
          default: { render: () => null },
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
    {
      window: { setTimeout, clearTimeout },
      document: { addEventListener: () => {}, removeEventListener: () => {} },
    },
  )

  const root = makeHostNode('root')
  const renderer = createHostRenderer(root)
  const app2 = renderer.createApp(Comp)
  app2.config.globalProperties.$t = (k: string) => k
  for (const stub of ['t-icon', 't-input', 't-loading', 't-tag']) {
    app2.component(stub, { setup(_p: any, { slots }: any) { return () => vue.h('span', slots.default?.()) } })
  }
  app2.mount(root)
  await vue.nextTick()

  assert.equal(searchCalls.length, 1)
  searchCalls[0].resolve([{ id: 1, name: 'Home' }, { id: 2, name: 'Alpha' }])
  await new Promise((r) => setImmediate(r))
  assert.equal(allTenantsUpdates.length, 1)

  const src = readComponent('./TenantSelector.vue')
  assert.match(src, /tenantLoadSeq/, 'TenantSelector must use a sequence counter instead of dropping fresh loads when loading.value is true')
  assert.match(src, /if \(!keyword\)\s*\{\s*authStore\.setAllTenants/, 'TenantSelector must only write unfiltered list to authStore.setAllTenants')
  app2.unmount()
})

test('SandboxConfigEditorDrawer, SandboxSkillsPanel, and ModelEditorDialog enforce secret identity and generation guards', () => {
  const sandboxDrawer = readComponent('./SandboxConfigEditorDrawer.vue')
  assert.match(sandboxDrawer, /originalKey/, 'envRows must track originalKey so renaming a stored env key does not submit ***')
  assert.match(sandboxDrawer, /inFlightFromSkills\.value = false/, 'reset() must clear inFlightFromSkills')

  const skillsPanel = readComponent('./SandboxSkillsPanel.vue')
  const uploadFn = skillsPanel.slice(skillsPanel.indexOf('async function uploadFile'), skillsPanel.indexOf('async function installFromSource'))
  assert.match(uploadFn, /const configId = props\.record\.id/, 'uploadFile must capture configId before awaiting persistInstallerModel')
  assert.match(uploadFn, /panelGeneration/, 'uploadFile must check panelGeneration after awaits')

  const modelDialog = readComponent('./ModelEditorDialog.vue')
  assert.match(modelDialog, /stopDownloadPolling/, 'ModelEditorDialog must stop Ollama download polling on reset/close/re-download')
})

test('PdfSourceViewer, AttachmentUpload, Input-field, ChatHeader, guides, and selectors clean up timers, blobs, and stale loads', () => {
  const pdfViewer = readComponent('./source-preview/PdfSourceViewer.vue')
  assert.match(pdfViewer, /watch\(\s*\(\)\s*=>\s*props\.data/, 'PdfSourceViewer must react when props.data changes')

  const manualEditor = readComponent('./manual-knowledge-editor.vue')
  assert.match(manualEditor, /initGeneration/, 'manual-knowledge-editor must guard initialize/loadKnowledgeContent with generation token')

  const attachmentUpload = readComponent('./AttachmentUpload.vue')
  const pollStatusFn = attachmentUpload.slice(attachmentUpload.indexOf('const pollStatus ='), attachmentUpload.indexOf('const clearPoll ='))
  assert.match(pollStatusFn, /disposed/, 'AttachmentUpload.pollStatus must bail out when disposed')

  const inputField = readComponent('./Input-field.vue')
  const unmountedBlock = inputField.slice(inputField.indexOf('onUnmounted(() => {'), inputField.indexOf('// 监听路由变化'))
  assert.match(unmountedBlock, /revokeObjectURL/, 'Input-field onUnmounted must revoke uploadedImages preview ObjectURLs')

  const chatHeader = readComponent('./ChatHeader.vue')
  assert.match(chatHeader, /editingSessionId/, 'ChatHeader must bind title editing to the session ID that started editing')

  const agentGuide = readComponent('./AgentCreateContextualGuide.vue')
  assert.match(agentGuide, /onBeforeUnmount/, 'AgentCreateContextualGuide must clear timers on unmount')

  const kbGuide = readComponent('./KbCreateContextualGuide.vue')
  assert.match(kbGuide, /onBeforeUnmount/, 'KbCreateContextualGuide must clear timers on unmount')

  const kbSelector = readComponent('./KnowledgeBaseSelector.vue')
  assert.match(kbSelector, /onBeforeUnmount/, 'KnowledgeBaseSelector must remove window listeners on unmount')

  const spotlight = readComponent('./SpotlightGuide.vue')
  assert.match(spotlight, /navEpoch/, 'SpotlightGuide must cancel in-flight goTo/locate steps on dismiss or unmount')

  const skillDrawer = readComponent('./SkillFilesDrawer.vue')
  assert.match(skillDrawer, /if \(!open\) cleanupResize\(\)/, 'SkillFilesDrawer must clean up active drag-resize when drawer closes')

  const sourceFilter = readComponent('./SessionSourceFilter.vue')
  assert.match(sourceFilter, /if \(!open\.value \|\| unmounted\) return/, 'SessionSourceFilter must guard deferred nextTick listener attachment')

  const bubble = readComponent('./assistant/AssistantSelectionBubble.vue')
  assert.match(bubble, /placeEpoch/, 'AssistantSelectionBubble must guard async place() across nextTick')
})
