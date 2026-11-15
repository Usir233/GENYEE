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
    ['genyee_user', JSON.stringify({ id: 'alice' })],
    ['GENYEE_anon_theme', 'dark'],
  ])
  assert.equal(api.loadPreference('theme'), 'dark')
  assert.equal(values.has('GENYEE_anon_theme'), false)
  values.set('genyee_user', JSON.stringify({ id: 'bob' }))
  api.resetMigrationLatch()
  api.migratePreferencesIntoUser()
  assert.equal(api.loadPreference('theme'), null)
})

test('does not adopt unscoped preferences or overwrite user choices', () => {
  const { values, api } = fixture([
    ['genyee_user', JSON.stringify({ id: 'alice' })],
    ['GENYEE_theme', 'unscoped'],
    ['GENYEE_font_size', '99'],
    ['GENYEE_anon_theme', 'dark'],
    ['GENYEE_alice_theme', 'light'],
  ])
  assert.equal(api.loadPreference('theme'), 'light')
  assert.equal(api.loadPreference('font_size'), null)
  assert.equal(values.has('GENYEE_anon_theme'), false)
})

function runIndexHtmlBootstrap(entries: [string, string][], prefersDark = false) {
  const html = readFileSync(new URL('../../index.html', import.meta.url), 'utf8')
  const inlineScripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m => m[1])
  assert.equal(inlineScripts.length >= 3, true, 'expected inline bootstrap scripts in index.html')

  const storage = new Map(entries)
  const htmlAttrs = new Map<string, string>()
  const htmlStyle: Record<string, string> = {}
  const bodyStyle: Record<string, string> = {}
  const appStyle: Record<string, string> = {}
  let darkCalls = 0
  let lightCalls = 0

  const context = {
    localStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
    },
    sessionStorage: {
      getItem: () => null,
    },
    window: {
      location: { pathname: '/platform/chat', search: '' },
      history: { state: null, replaceState() {} },
      matchMedia: () => ({ matches: prefersDark }),
      runtime: {
        WindowSetDarkTheme: () => { darkCalls += 1 },
        WindowSetLightTheme: () => { lightCalls += 1 },
        WindowSetBackgroundColour() {},
      },
    },
    document: {
      documentElement: {
        setAttribute: (k: string, v: string) => htmlAttrs.set(k, v),
        style: htmlStyle,
      },
      body: { style: bodyStyle },
      getElementById: (id: string) => (id === 'app' ? { style: appStyle } : null),
    },
  }

  for (const script of inlineScripts) {
    runInNewContext(script, context)
  }

  return {
    themeMode: htmlAttrs.get('theme-mode'),
    htmlBg: htmlStyle.background,
    bodyBg: bodyStyle.background,
    appBg: appStyle.background,
    darkCalls,
    lightCalls,
  }
}

test('index.html initial theme bootstrap matches preferenceStorage user/anon resolution', () => {
  // 1. User-scoped dark preference wins without white flash
  const userDark = runIndexHtmlBootstrap([
    ['genyee_user', JSON.stringify({ id: 'u-42' })],
    ['GENYEE_u-42_theme', 'dark'],
    ['GENYEE_anon_theme', 'light'],
  ])
  assert.equal(userDark.themeMode, 'dark')
  assert.equal(userDark.htmlBg, '#181818')
  assert.equal(userDark.bodyBg, '#181818')
  assert.equal(userDark.appBg, '#181818')
  assert.equal(userDark.darkCalls, 1)
  assert.equal(userDark.lightCalls, 0)

  // 2. Logged-in user with no user-scoped theme yet inherits GENYEE_anon_theme
  const adoptAnon = runIndexHtmlBootstrap([
    ['genyee_user', JSON.stringify({ id: 'u-42' })],
    ['GENYEE_anon_theme', 'dark'],
  ])
  assert.equal(adoptAnon.themeMode, 'dark')
  assert.equal(adoptAnon.appBg, '#181818')

  // 3. User-scoped light preference is not overridden by GENYEE_anon_theme
  const userLight = runIndexHtmlBootstrap([
    ['genyee_user', JSON.stringify({ id: 'u-42' })],
    ['GENYEE_u-42_theme', 'light'],
    ['GENYEE_anon_theme', 'dark'],
  ])
  assert.equal(userLight.themeMode, 'light')
  assert.equal(userLight.htmlBg, '#eee')
  assert.equal(userLight.appBg, '#eee')
  assert.equal(userLight.lightCalls, 1)

  // 4. Corrupt genyee_user JSON falls back to anon key and ignores legacy unscoped GENYEE_theme
  const corruptAnon = runIndexHtmlBootstrap([
    ['genyee_user', '{corrupt'],
    ['GENYEE_theme', 'dark'],
  ])
  assert.equal(corruptAnon.themeMode, 'light')
  assert.equal(corruptAnon.appBg, '#eee')

  // 5. System mode resolves via matchMedia
  const systemDark = runIndexHtmlBootstrap([
    ['GENYEE_anon_theme', 'system'],
  ], true)
  assert.equal(systemDark.themeMode, 'dark')
  assert.equal(systemDark.appBg, '#181818')
})
