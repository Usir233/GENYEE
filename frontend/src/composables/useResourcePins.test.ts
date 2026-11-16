import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import type { useResourcePins as UseResourcePinsFn } from './useResourcePins.ts'

const source = readFileSync(new URL('./useResourcePins.ts', import.meta.url), 'utf8')

function createHarness() {
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const authState = vue.reactive({
    effectiveTenantId: 101 as number | null,
    userId: 'user-1',
  })

  const pendingCalls: Array<{
    type: string
    tenantId: number | null
    userId: string
    resolve: (items: Array<{ resource_type: string; resource_id: string; created_at?: string }>) => void
    reject: (err: Error) => void
  }> = []

  const storage = new Map<string, string>()
  const exports: { useResourcePins?: typeof UseResourcePinsFn } = {}

  vm.runInNewContext(compiled, {
    exports,
    console: { warn: () => {} },
    Date,
    Promise,
    window: { addEventListener: () => {} },
    require(id: string) {
      if (id === 'vue') return vue
      if (id === '@/stores/auth') {
        return {
          useAuthStore: () => ({
            get effectiveTenantId() { return authState.effectiveTenantId },
            get user() { return authState.userId ? { id: authState.userId } : null },
          }),
        }
      }
      if (id === './preferenceStorage') {
        return {
          safeGetItem: (k: string) => storage.get(k) ?? null,
          safeSetItem: (k: string, v: string) => { storage.set(k, v) },
          readUserId: () => authState.userId || 'anon',
        }
      }
      if (id === '@/api/user-favorites') {
        return {
          listFavorites: (type: string) => new Promise((resolve, reject) => {
            pendingCalls.push({
              type,
              tenantId: authState.effectiveTenantId,
              userId: authState.userId,
              resolve: (data) => resolve({ success: true, data }),
              reject,
            })
          }),
          addFavorite: async () => ({ success: true }),
          removeFavorite: async () => ({ success: true }),
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  return {
    useResourcePins: exports.useResourcePins!,
    authState,
    pendingCalls,
  }
}

const flush = async () => {
  await Promise.resolve()
  await vue.nextTick()
  await Promise.resolve()
}

test('useResourcePins keeps watching tenant and user switches after the first caller scope disposes', async () => {
  const { useResourcePins, authState, pendingCalls } = createHarness()

  // Component A mounts and immediately unmounts (disposing its effectScope)
  const scopeA = vue.effectScope()
  scopeA.run(() => useResourcePins())
  while (pendingCalls.length > 0) {
    const req = pendingCalls.shift()!
    req.resolve([{ resource_type: req.type, resource_id: `${req.type}-t101` }])
  }
  await flush()
  scopeA.stop()

  // Component B mounts in the same tenant and sees cached data
  const scopeB = vue.effectScope()
  const pinsB = scopeB.run(() => useResourcePins())!
  assert.equal(pinsB.isFavorite('kb', 'kb-t101'), true)

  // Tenant switches to 202 AFTER scopeA was disposed: must clear t101 favorites and refetch!
  authState.effectiveTenantId = 202
  await flush()
  assert.equal(pinsB.isFavorite('kb', 'kb-t101'), false)
  assert.ok(pendingCalls.some(c => c.tenantId === 202 && c.type === 'kb'))

  while (pendingCalls.length > 0) {
    const req = pendingCalls.shift()!
    req.resolve([{ resource_type: req.type, resource_id: `${req.type}-t202` }])
  }
  await flush()
  assert.equal(pinsB.isFavorite('kb', 'kb-t202'), true)

  // User switches on the same device: must also clear user-1's favorites and refetch!
  authState.userId = 'user-2'
  await flush()
  assert.equal(pinsB.isFavorite('kb', 'kb-t202'), false)
  assert.ok(pendingCalls.some(c => c.userId === 'user-2' && c.type === 'kb'))

  scopeB.stop()
})

test('useResourcePins discards stale in-flight listFavorites responses from an earlier tenant', async () => {
  const { useResourcePins, authState, pendingCalls } = createHarness()

  const scope = vue.effectScope()
  const pins = scope.run(() => useResourcePins())!
  assert.equal(pendingCalls.length, 2)
  const staleKb = pendingCalls.find(c => c.tenantId === 101 && c.type === 'kb')!
  const staleAgent = pendingCalls.find(c => c.tenantId === 101 && c.type === 'agent')!

  // Switch tenant BEFORE tenant 101's listFavorites settles
  authState.effectiveTenantId = 303
  await flush()

  const freshKb = pendingCalls.find(c => c.tenantId === 303 && c.type === 'kb')
  assert.ok(freshKb, 'must issue a new kb fetch for tenant 303 instead of reusing tenant 101 inFlight promise')

  // Fresh tenant 303 resolves first, then stale tenant 101 resolves late
  freshKb!.resolve([{ resource_type: 'kb', resource_id: 'kb-303' }])
  const freshAgent = pendingCalls.find(c => c.tenantId === 303 && c.type === 'agent')!
  freshAgent.resolve([])
  await flush()
  assert.equal(pins.isFavorite('kb', 'kb-303'), true)

  staleKb.resolve([{ resource_type: 'kb', resource_id: 'kb-101-stale' }])
  staleAgent.resolve([{ resource_type: 'agent', resource_id: 'agent-101-stale' }])
  await flush()

  assert.equal(pins.isFavorite('kb', 'kb-101-stale'), false)
  assert.equal(pins.isFavorite('kb', 'kb-303'), true)
  scope.stop()
})
