import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.ts'
import ruRU from './locales/ru-RU.ts'
import enUS from './locales/en-US.ts'
import koKR from './locales/ko-KR.ts'
import jaJP from './locales/ja-JP.ts'
import zhTW from './locales/zh-TW.ts'
import {
  BUILT_IN_DEFAULT,
  SUPPORTED_LOCALES,
  resolveDefaultLocale,
  type SupportedLocale,
} from './resolveDefaultLocale.ts'
import { pluralRules } from './pluralizationRules.ts'

const messages = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  'en-US': enUS,
  'ru-RU': ruRU,
  'ko-KR': koKR,
  'ja-JP': jaJP
}

export function resolveInitialLocale(
  storedLocale?: string | null,
  runtimeLocale?: string | null,
  buildTimeLocale?: string | null,
): SupportedLocale {
  const candidate = typeof storedLocale === 'string' ? storedLocale.trim() : ''
  if ((SUPPORTED_LOCALES as readonly string[]).includes(candidate)) {
    return candidate as SupportedLocale
  }
  return resolveDefaultLocale(runtimeLocale, buildTimeLocale)
}

function readStoredLocale(): string | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage.getItem('locale') : null
  } catch {
    return null
  }
}

// User's explicit past choice wins if supported; otherwise use the deployment default.
const savedLocale = resolveInitialLocale(
  readStoredLocale(),
  typeof window !== 'undefined' ? window.__RUNTIME_CONFIG__?.DEFAULT_LOCALE : undefined,
  import.meta.env?.VITE_DEFAULT_LOCALE,
)

const i18n = createI18n({
  legacy: false,
  locale: savedLocale,
  fallbackLocale: BUILT_IN_DEFAULT,
  globalInjection: true,
  pluralRules,
  // Some translations intentionally embed `<strong>` markup (e.g. agent step summaries).
  // We render them via v-html with our own sanitization, so silence vue-i18n's HTML warning
  // to avoid flooding the console and slowing renders during history loads.
  warnHtmlMessage: false,
  messages
})

export default i18n
