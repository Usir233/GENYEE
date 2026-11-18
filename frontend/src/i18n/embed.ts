import { createI18n } from 'vue-i18n'
import enUS from './embed-locales/en-US.ts'
import jaJP from './embed-locales/ja-JP.ts'
import koKR from './embed-locales/ko-KR.ts'
import ruRU from './embed-locales/ru-RU.ts'
import zhCN from './embed-locales/zh-CN.ts'
import zhTW from './embed-locales/zh-TW.ts'
import { pluralRules } from './pluralizationRules.ts'

export const SUPPORTED_LOCALES = ['zh-CN', 'zh-TW', 'en-US', 'ko-KR', 'ja-JP', 'ru-RU'] as const
export type EmbedLocale = (typeof SUPPORTED_LOCALES)[number]

/** Isolated from the main app `locale` key so embed preview never hijacks admin UI language. */
export const EMBED_LOCALE_STORAGE_KEY = 'genyee-embed-locale'

/** Map host-provided locale strings to a supported embed locale tag. */
export function normalizeEmbedLocale(raw: string): EmbedLocale {
  const s = raw.trim().toLowerCase()
  if (s.startsWith('en')) return 'en-US'
  if (s.startsWith('ko')) return 'ko-KR'
  if (s.startsWith('ja')) return 'ja-JP'
  if (s.startsWith('ru')) return 'ru-RU'
  if (
    s.startsWith('zh-tw') ||
    s.startsWith('zh-hk') ||
    s.startsWith('zh-mo') ||
    s.startsWith('zh-hant')
  ) {
    return 'zh-TW'
  }
  if (s.startsWith('zh')) return 'zh-CN'
  const exact = SUPPORTED_LOCALES.find((l) => l.toLowerCase() === s)
  return exact || 'zh-CN'
}

export function readEmbedLocaleFromUrl(): string {
  if (typeof window === 'undefined') return ''
  return new URLSearchParams(window.location.search).get('locale')?.trim() || ''
}

function resolveBrowserEmbedLocale(): EmbedLocale {
  const nav = typeof navigator !== 'undefined' ? navigator.language : ''
  return nav ? normalizeEmbedLocale(nav) : 'zh-CN'
}

function resolveInitialEmbedLocale(): EmbedLocale {
  const fromUrl = readEmbedLocaleFromUrl()
  if (fromUrl) return normalizeEmbedLocale(fromUrl)

  try {
    const saved = typeof localStorage !== 'undefined'
      ? localStorage.getItem(EMBED_LOCALE_STORAGE_KEY)
      : null
    if (saved) return normalizeEmbedLocale(saved)
  } catch {
    // localStorage may be unavailable in private mode.
  }

  return resolveBrowserEmbedLocale()
}

const locale = resolveInitialEmbedLocale()

export const EMBED_MESSAGES = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  'en-US': enUS,
  'ko-KR': koKR,
  'ja-JP': jaJP,
  'ru-RU': ruRU,
} as const

const i18n = createI18n({
  legacy: false,
  locale,
  fallbackLocale: 'en-US',
  globalInjection: true,
  pluralRules,
  warnHtmlMessage: false,
  messages: EMBED_MESSAGES,
})

type LocaleRef = { value: string }

/** Apply locale for the embed surface (isolated storage + optional active vue-i18n ref). */
export function applyEmbedLocale(raw: string, localeRef?: LocaleRef) {
  const next = normalizeEmbedLocale(raw)
  try {
    localStorage.setItem(EMBED_LOCALE_STORAGE_KEY, next)
  } catch {
    // localStorage may be unavailable in private mode.
  }
  if (localeRef) {
    localeRef.value = next
  } else {
    i18n.global.locale.value = next
  }
}

/** Honor `?locale=` on the embed URL for the currently mounted vue-i18n instance. */
export function syncEmbedLocaleFromUrl(localeRef: LocaleRef): boolean {
  const fromUrl = readEmbedLocaleFromUrl()
  if (!fromUrl) return false
  applyEmbedLocale(fromUrl, localeRef)
  return true
}

export default i18n
