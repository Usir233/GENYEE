import type { ComposerTranslation } from 'vue-i18n'
import enUS from '../i18n/locales/en-US.ts'
import jaJP from '../i18n/locales/ja-JP.ts'
import koKR from '../i18n/locales/ko-KR.ts'
import ruRU from '../i18n/locales/ru-RU.ts'
import zhCN from '../i18n/locales/zh-CN.ts'
import zhTW from '../i18n/locales/zh-TW.ts'

type AttachmentParsingEvent = {
  success?: boolean
  output?: string
  error?: string
  tool_data?: Record<string, unknown> | null
}

function escapeRegExp(raw: string): string {
  return raw.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

const ATTACHMENT_LOCALE_BAGS = [zhCN, zhTW, enUS, jaJP, koKR, ruRU].map(
  (bundle) => bundle.agentStream.attachmentParsing,
)

const PARSED_COUNT_PATTERNS = ATTACHMENT_LOCALE_BAGS.map(
  (bag) =>
    new RegExp(
      `${escapeRegExp(bag.legacyParsedPrefix)}\\s*(\\d+)\\s*${escapeRegExp(bag.legacyParsedSuffix)}`,
      'i',
    ),
)

const SKIPPED_COUNT_PATTERNS = ATTACHMENT_LOCALE_BAGS.map(
  (bag) => new RegExp(`(\\d+)\\s*${escapeRegExp(bag.legacySkippedSuffix)}`, 'i'),
)

const ERROR_PREFIX_PATTERNS = ATTACHMENT_LOCALE_BAGS.map(
  (bag) => new RegExp(`^${escapeRegExp(bag.errorPrefix)}\\s*`, 'i'),
)

export function resolveAttachmentParsingCounts(event: AttachmentParsingEvent): {
  parsed: number
  skipped: number
} {
  const toolData = event.tool_data
  if (toolData && toolData.parsed_count !== undefined) {
    return {
      parsed: Number(toolData.parsed_count) || 0,
      skipped: Number(toolData.skipped_count) || 0,
    }
  }

  return parseAttachmentOutput(event.output)
}

function parseAttachmentOutput(output?: string): { parsed: number; skipped: number } {
  if (!output) return { parsed: 0, skipped: 0 }

  let parsed = 0
  for (const pattern of PARSED_COUNT_PATTERNS) {
    const match = output.match(pattern)
    if (match?.[1]) {
      parsed = Number(match[1]) || 0
      break
    }
  }

  let skipped = 0
  for (const pattern of SKIPPED_COUNT_PATTERNS) {
    const match = output.match(pattern)
    if (match?.[1]) {
      skipped = Number(match[1]) || 0
      break
    }
  }

  return { parsed, skipped }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

export function getAttachmentParsingSummaryHtml(
  t: ComposerTranslation,
  event: AttachmentParsingEvent,
): string {
  if (event.success === false) {
    const err = String(event.error || event.output || '').trim()
    if (!err) return ''
    let normalized = err
    for (const prefixPattern of ERROR_PREFIX_PATTERNS) {
      normalized = normalized.replace(prefixPattern, '')
    }
    normalized = normalized.trim()
    return escapeHtml(normalized || err)
  }

  const { parsed, skipped } = resolveAttachmentParsingCounts(event)
  if (parsed === 0 && skipped === 0) {
    return t('agentStream.attachmentParsing.noneReady')
  }
  if (skipped > 0) {
    return t('agentStream.attachmentParsing.parsedWithSkipped', {
      parsed: `<strong>${parsed}</strong>`,
      skipped: `<strong>${skipped}</strong>`,
    })
  }
  return t('agentStream.attachmentParsing.parsedSummary', {
    count: `<strong>${parsed}</strong>`,
  })
}
