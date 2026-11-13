import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'

const compiled = ts.transpileModule(
  readFileSync(new URL('./preferenceStorage.ts', import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.CommonJS } },
).outputText

function fixture(entries: [string, string][]) {
  const values = new Map(entries)
  const exports: Record<string, any> = {}
  runInNewContext(compiled, {
    exports,
    console,
    localStorage: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    },
  })
  return { values, api: exports }
}

test('adopts anonymous preferences once and removes their source', () => {
  const { values, api } = fixture([
    ['weknora_user', JSON.stringify({ id: 'alice' })],
    ['WeKnora_anon_theme', 'dark'],
  ])
  assert.equal(api.loadPreference('theme'), 'dark')
  assert.equal(values.has('WeKnora_anon_theme'), false)
  values.set('weknora_user', JSON.stringify({ id: 'bob' }))
  api.resetMigrationLatch()
  api.migratePreferencesIntoUser()
  assert.equal(api.loadPreference('theme'), null)
})

test('does not adopt unscoped preferences or overwrite user choices', () => {
  const { values, api } = fixture([
    ['weknora_user', JSON.stringify({ id: 'alice' })],
    ['WeKnora_theme', 'unscoped'],
    ['WeKnora_font_size', '99'],
    ['WeKnora_anon_theme', 'dark'],
    ['WeKnora_alice_theme', 'light'],
  ])
  assert.equal(api.loadPreference('theme'), 'light')
  assert.equal(api.loadPreference('font_size'), null)
  assert.equal(values.has('WeKnora_anon_theme'), false)
})
