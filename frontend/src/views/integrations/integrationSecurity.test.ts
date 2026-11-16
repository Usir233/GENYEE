import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, reactive, ref } from 'vue'
import { buildClaudeCodeCommand } from './mcpServerIntegration'
import { buildCLIConnectCommand } from './cliIntegration'
import { normalizeAPIKeyKnowledgeBaseIDs } from './apiKeyScope'
import { consumeApiPlaygroundSSE } from './apiPlaygroundSSE'

const require = createRequire(import.meta.url)

function mountSfc(relativePath: string, stubs: Record<string, unknown>, winExtra: Record<string, unknown> = {}) {
  const filename = fileURLToPath(new URL(relativePath, import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: `test-${relativePath}` }).content
    .replace('__expose();', '')
    .replace('return __returned__', '__expose(__returned__); return __returned__')
  const compiled = ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const openedWindows: Array<{ url: string; target?: string; features?: string }> = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    URL,
    AbortController,
    Uint8Array,
    btoa,
    setTimeout,
    clearTimeout,
    console: { ...console, error() {} },
    window: {
      location: { origin: 'https://genyee.example.com' },
      crypto: { getRandomValues: (arr: Uint8Array) => arr },
      open: (url: string, target?: string, features?: string) => {
        openedWindows.push({ url, target, features })
        return null
      },
      ...winExtra,
    },
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
  return { vm: instance.value, openedWindows, unmount: () => app.unmount() }
}

test('buildClaudeCodeCommand quotes shell metacharacters in url and token while keeping clean tokens readable', () => {
  assert.equal(
    buildClaudeCodeCommand('Docs', 'https://h/mcp/1', 'mcp_t'),
    'claude mcp add --transport http genyee-docs https://h/mcp/1 --header "Authorization: Bearer mcp_t"',
  )

  const injected = buildClaudeCodeCommand(
    'Docs',
    'https://h/mcp/1?foo=1&bar=$(touch /tmp/pwn)',
    'tok"$(id)`uname`\'end\nnewline',
  )
  assert.equal(injected.includes('\n'), false)
  assert.match(injected, /'https:\/\/h\/mcp\/1\?foo=1&bar=\$\(touch \/tmp\/pwn\)'/)
  assert.match(injected, /--header 'Authorization: Bearer tok"\$\(id\)`uname`'\\''endnewline'/)
})

test('CliIntegrationLanding validates http(s) GITHUB_URL, avoids .git.git duplication, and quotes clone URL', () => {
  const unsafe = mountSfc(
    './CliIntegrationLanding.vue',
    {
      '@/composables/useApiBaseUrlDisplay': { useApiBaseUrlDisplay: () => ({ apiBaseUrlDisplay: ref('https://kb.example.com/api/v1') }) },
      '@/utils/clipboard': { copyWithToast: () => {} },
      '@/utils/docsUrl': { docsUrl: () => 'https://docs.genyee.example.com/cli' },
      './cliIntegration': { buildCLIConnectCommand },
    },
    { __RUNTIME_CONFIG__: { GITHUB_URL: 'javascript:alert(1)' } },
  )
  try {
    assert.equal(unsafe.vm.steps[0].command.startsWith('cd ./cli\n'), true)
    unsafe.vm.openDocs()
    assert.equal(unsafe.openedWindows[0]?.url, 'https://docs.genyee.example.com/cli')
  } finally {
    unsafe.unmount()
  }

  const withGitSuffix = mountSfc(
    './CliIntegrationLanding.vue',
    {
      '@/composables/useApiBaseUrlDisplay': { useApiBaseUrlDisplay: () => ({ apiBaseUrlDisplay: ref('https://kb.example.com/api/v1') }) },
      '@/utils/clipboard': { copyWithToast: () => {} },
      '@/utils/docsUrl': { docsUrl: () => 'https://docs.genyee.example.com/cli' },
      './cliIntegration': { buildCLIConnectCommand },
    },
    { __RUNTIME_CONFIG__: { GITHUB_URL: 'https://github.com/org/GENYEE.git/' } },
  )
  try {
    assert.equal(withGitSuffix.vm.steps[0].command.includes('.git.git'), false)
    assert.equal(withGitSuffix.vm.steps[0].command.includes('https://github.com/org/GENYEE.git'), true)
    withGitSuffix.vm.openDocs()
    assert.equal(withGitSuffix.openedWindows[0]?.url, 'https://github.com/org/GENYEE/blob/main/cli/README.md')
  } finally {
    withGitSuffix.unmount()
  }
})

test('ClawSkillLanding envExample safely quotes GENYEE_BASE_URL against shell command substitution', () => {
  const apiBaseUrlDisplay = ref('https://host.example/api"$(id)`uname`\'x')
  const ctx = mountSfc('./ClawSkillLanding.vue', {
    'vue-router': { useRouter: () => ({ push() {} }) },
    '@/utils/clipboard': { copyWithToast: () => {} },
    '@/config/integrations': { getClawhubSkillUrl: () => 'https://clawhub.example.com' },
    '@/composables/useApiBaseUrlDisplay': { useApiBaseUrlDisplay: () => ({ apiBaseUrlDisplay }) },
    '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
  })
  try {
    const env = ctx.vm.envExample as string
    assert.equal(env.includes('export GENYEE_BASE_URL="https://host.example/api"$(id)'), false)
    assert.match(env, /^export GENYEE_BASE_URL='https:\/\/host\.example\/api"\$\(id\)`uname`'\\''x'/m)
  } finally {
    ctx.unmount()
  }
})

test('ApiIntegrationSettings rotates or clears apiKey when selected key is revoked and opens docs with noopener,noreferrer', async () => {
  let currentKeys: Array<{ id: string; api_key: string }> = [
    { id: 'k1', api_key: 'sk-active-1' },
    { id: 'k2', api_key: 'sk-active-2' },
  ]
  const ctx = mountSfc('./ApiIntegrationSettings.vue', {
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': {
      MessagePlugin: { error() {}, success() {}, warning() {} },
      DialogPlugin: { confirm: () => ({ destroy() {} }) },
    },
    '@/stores/auth': { useAuthStore: () => reactive({ effectiveTenantId: 7, currentTenantId: 7 }) },
    '@/stores/chatResources': {
      useChatResourcesStore: () => reactive({
        rawKnowledgeBases: [],
        agents: [],
        ensureKnowledgeBases: async () => {},
        ensureAgents: async () => {},
      }),
    },
    '@/api/agent': { BUILTIN_SMART_REASONING_ID: 'builtin-smart' },
    '@/api/tenant': {
      createTenantAPIKey: async () => ({ success: true }),
      deleteTenantAPIKey: async () => ({ success: true }),
      createAPIPrincipalTestToken: async () => ({ success: true }),
      getAPIPrincipalConfig: async () => ({
        success: true,
        data: {
          mode: 'tenant',
          direct_header_name: 'X-External-User-ID',
          signed_token_header_name: 'X-External-User-Token',
          require_direct_header: false,
          has_hmac_secret: false,
        },
      }),
      listTenantAPIKeys: async () => ({ success: true, data: currentKeys }),
      updateTenantAPIKey: async () => ({ success: true }),
      updateAPIPrincipalConfig: async () => ({ success: true }),
    },
    '@/utils/api-base': { getApiBaseUrl: () => '/api/v1' },
    '@/config/apiKeyCapabilities': {
      DEFAULT_TENANT_API_KEY_CAPABILITIES: new Set<string>(),
      KB_SCOPED_API_KEY_CAPABILITIES: new Set<string>(),
      TENANT_API_KEY_CAPABILITIES: [],
      TENANT_API_KEY_CAPABILITY_GROUPS: [],
    },
    './apiKeyScope': { normalizeAPIKeyKnowledgeBaseIDs },
    './apiPlaygroundSSE': { consumeApiPlaygroundSSE },
    '@/utils/docsUrl': { docsUrl: () => 'https://docs.genyee.example.com/api' },
    '@/utils/clipboard': { copyWithToast: async () => {} },
  })

  try {
    ctx.vm.tenantId = 7
    ctx.vm.apiKey = 'sk-revoked-key'
    await ctx.vm.loadAPIKeys()
    assert.equal(ctx.vm.apiKey, 'sk-active-1')

    currentKeys = []
    await ctx.vm.loadAPIKeys()
    assert.equal(ctx.vm.apiKey, '')

    ctx.vm.openApiDoc()
    assert.equal(ctx.openedWindows.length, 1)
    assert.equal(ctx.openedWindows[0]?.url, 'https://docs.genyee.example.com/api')
    assert.equal(ctx.openedWindows[0]?.features, 'noopener,noreferrer')
  } finally {
    ctx.unmount()
  }
})
