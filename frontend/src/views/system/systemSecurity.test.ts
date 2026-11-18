import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import * as modelCatalogState from './modelCatalogState'
import { isSettingValueDirty, resolveCurrentSetting } from './systemSettingsEdit'
import { STORAGE_QUOTA_KEYS, formatStorageQuota, resolveStorageQuota } from './storageQuota'

const require = createRequire(import.meta.url)

function compileVue(relativePath: string) {
  const filename = fileURLToPath(new URL(relativePath, import.meta.url))
  const raw = readFileSync(filename, 'utf8')
  const { descriptor } = parse(raw, { filename })
  const script = compileScript(descriptor, { id: `sys-test-${relativePath}` }).content
    .replace('__expose();', '')
    .replace('return __returned__', '__expose(__returned__); return __returned__')
  const outputText = ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  return { outputText, template: descriptor.template?.content ?? '' }
}

function mountVue(outputText: string, stubs: Record<string, unknown>) {
  const exports: any = {}
  runInNewContext(outputText, {
    exports,
    URL,
    Date,
    Set,
    Map,
    setTimeout,
    clearTimeout,
    console: { ...console, error() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name in stubs) return stubs[name]
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
  return { vm: instance.value, unmount: () => app.unmount() }
}

const emptyState = {
  builtin: [],
  deployment: [],
  effective: [],
  overlay: { providers: {} },
  history: [],
}

test('ModelCatalog only exposes http(s) model source URLs for external links', async () => {
  const { outputText, template } = compileVue('./ModelCatalog.vue')
  const { vm, unmount } = mountVue(outputText, {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k, locale: ref('en-US') }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {}, warning() {} } },
    '@/utils/reasoningEffort': { levelLabelKey: (l: string) => l },
    '@/api/system/model-catalog': {
      getModelCatalog: async () => emptyState,
      previewModelCatalog: async () => ({ effective: [] }),
      publishModelCatalog: async () => emptyState,
    },
    '@/stores/modelProviders': { useModelProvidersStore: () => ({ invalidate() {} }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({ invalidateModels() {} }) },
    './modelCatalogState': modelCatalogState,
  })

  try {
    await nextTick()
    vm.selected = {
      key: 'openai:gpt-test:KnowledgeQA',
      provider: 'openai',
      model: { id: 'gpt-test', type: 'KnowledgeQA', source: 'javascript:alert(1)' },
    }
    await nextTick()
    assert.equal(vm.safeSourceUrl, '')

    vm.selected = {
      key: 'openai:gpt-test:KnowledgeQA',
      provider: 'openai',
      model: { id: 'gpt-test', type: 'KnowledgeQA', source: 'https://platform.openai.com/docs/models' },
    }
    await nextTick()
    assert.equal(vm.safeSourceUrl, 'https://platform.openai.com/docs/models')
    assert.match(template, /v-if="safeSourceUrl"\s+:href="safeSourceUrl"/)
  } finally {
    unmount()
  }
})

test('SystemSettings masks secret placeholders and binds password input type when item.is_secret is true', async () => {
  const { outputText, template } = compileVue('./SystemSettings.vue')
  const { vm, unmount } = mountVue(outputText, {
    'vue-i18n': {
      useI18n: () => ({
        t: (k: string) => k,
        tm: () => ({}),
        te: () => false,
        locale: ref('en-US'),
      }),
    },
    'vue-router': {
      useRoute: () => reactive({ query: {} }),
      useRouter: () => ({ replace() {} }),
    },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {}, warning() {} } },
    '@/api/system': {
      listSystemSettings: async () => [],
      updateSystemSetting: async () => ({}),
      deleteSystemSettingOverride: async () => ({}),
      applyDefaultStorageQuotaToAllTenants: async () => ({ updated: 0 }),
    },
    '@/stores/deploymentCapabilities': {
      useDeploymentCapabilitiesStore: () => reactive({ refresh: async () => {} }),
    },
    '@/utils/passwordPolicy': { PASSWORD_SPECIAL_CHARS: '!@#$%' },
    './systemSettingsEdit': { isSettingValueDirty, resolveCurrentSetting },
    './storageQuota': { STORAGE_QUOTA_KEYS, formatStorageQuota, resolveStorageQuota },
  })

  try {
    await nextTick()
    const secretPlaceholder = vm.placeholderFor({
      key: 'custom.api_secret',
      value: 'super-secret-token-value',
      value_type: 'string',
      is_secret: true,
    })
    assert.equal(secretPlaceholder.includes('super-secret-token-value'), false)
    assert.equal(secretPlaceholder, '••••••••')

    const emptySecretPlaceholder = vm.placeholderFor({
      key: 'custom.api_secret',
      value: '',
      value_type: 'string',
      is_secret: true,
    })
    assert.equal(emptySecretPlaceholder, '')

    const plainPlaceholder = vm.placeholderFor({
      key: 'custom.public_host',
      value: 'kb.example.com',
      value_type: 'string',
      is_secret: false,
    })
    assert.equal(plainPlaceholder, 'kb.example.com')

    assert.match(template, /<t-input\b[\s\S]*?:type="item\.is_secret \? 'password' : 'text'"/)
  } finally {
    unmount()
  }
})
