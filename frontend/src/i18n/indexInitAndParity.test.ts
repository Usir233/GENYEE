import assert from 'node:assert/strict'
import test from 'node:test'
import enUS from './locales/en-US.ts'
import jaJP from './locales/ja-JP.ts'
import koKR from './locales/ko-KR.ts'
import ruRU from './locales/ru-RU.ts'
import zhCN from './locales/zh-CN.ts'
import zhTW from './locales/zh-TW.ts'
import embedI18n from './embed.ts'

const LOCALES: Record<string, Record<string, unknown>> = {
  'zh-CN': zhCN as unknown as Record<string, unknown>,
  'zh-TW': zhTW as unknown as Record<string, unknown>,
  'en-US': enUS as unknown as Record<string, unknown>,
  'ru-RU': ruRU as unknown as Record<string, unknown>,
  'ko-KR': koKR as unknown as Record<string, unknown>,
  'ja-JP': jaJP as unknown as Record<string, unknown>,
}

function collectLeafStrings(obj: unknown, prefix = '', out = new Map<string, string>()): Map<string, string> {
  if (typeof obj === 'string') {
    out.set(prefix, obj)
    return out
  }
  if (Array.isArray(obj)) {
    obj.forEach((item, idx) => collectLeafStrings(item, `${prefix}[${idx}]`, out))
    return out
  }
  if (obj && typeof obj === 'object') {
    for (const [key, val] of Object.entries(obj as Record<string, unknown>)) {
      const next = prefix ? `${prefix}.${key}` : key
      collectLeafStrings(val, next, out)
    }
  }
  return out
}

function extractPlaceholders(message: string): string {
  const matches = message.match(/\{[a-zA-Z0-9_]+\}/g) ?? []
  return [...new Set(matches)].sort().join(',')
}

test('i18n index imports safely without browser globals and validates stored locale values', async () => {
  const mod = await import('./index.ts')
  assert.ok(mod.default?.global)
  assert.equal(typeof mod.resolveInitialLocale, 'function')
  assert.equal(mod.resolveInitialLocale('ja-JP', 'en-US', 'zh-CN'), 'ja-JP')
  assert.equal(mod.resolveInitialLocale('invalid-locale', 'en-US', 'zh-CN'), 'en-US')
  assert.equal(mod.resolveInitialLocale(null, undefined, undefined), 'zh-CN')
})

test('embed i18n configures Slavic 3-form pluralRules for ru-RU', () => {
  const rule = (embedI18n.global as any).pluralRules?.['ru-RU']
  assert.equal(typeof rule, 'function', 'Expected pluralRules["ru-RU"] to be configured on embed i18n')
  assert.equal(rule(1, 3), 0)
  assert.equal(rule(2, 3), 1)
  assert.equal(rule(5, 3), 2)
  assert.equal(rule(21, 3), 0)
})

test('all 6 locales maintain 100% interpolation placeholder parity across every translation key', () => {
  const baseMap = collectLeafStrings(LOCALES['zh-CN'])
  const mismatches: string[] = []

  for (const [localeName, localeObj] of Object.entries(LOCALES)) {
    if (localeName === 'zh-CN') continue
    const targetMap = collectLeafStrings(localeObj)
    for (const [key, zhVal] of baseMap.entries()) {
      const targetVal = targetMap.get(key)
      if (typeof targetVal !== 'string') continue
      const zhPlaceholders = extractPlaceholders(zhVal)
      const targetPlaceholders = extractPlaceholders(targetVal)
      if (zhPlaceholders !== targetPlaceholders) {
        mismatches.push(`${localeName}:${key} expected [${zhPlaceholders}] got [${targetPlaceholders}]`)
      }
    }
  }

  assert.deepEqual(mismatches, [])
})
