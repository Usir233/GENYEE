import enUS from '../i18n/locales/en-US.ts'
import jaJP from '../i18n/locales/ja-JP.ts'
import koKR from '../i18n/locales/ko-KR.ts'
import ruRU from '../i18n/locales/ru-RU.ts'
import zhCN from '../i18n/locales/zh-CN.ts'
import zhTW from '../i18n/locales/zh-TW.ts'

const ANSWER_TAG_RE =
  /^\s*<(answer|final_answer|final-answer)\b[^>]*>([\s\S]*?)<\/\1>\s*$/i

const FENCED_ANSWER_RE =
  /^\s*```(?:final_answer|answer)\s*\n?([\s\S]*?)\n?```\s*$/i

function buildAnswerPrefixRegex(): RegExp {
  const prefixes = [zhCN, zhTW, enUS, jaJP, koKR, ruRU]
    .flatMap((bundle) => bundle.chat.finalAnswerPrefixList.split(','))
    .map((token) => token.trim())
    .filter(Boolean)
  const uniqueSorted = [...new Set(prefixes)].toSorted((a, b) => b.length - a.length)
  const alternation = uniqueSorted
    .map((token) =>
      token
        .replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
        .replace(/\s+/g, '\\s*'),
    )
    .join('|')
  return new RegExp(`^\\s*(?:${alternation})\\s*[:\\uff1a]\\s*`, 'i')
}

const ANSWER_PREFIX_RE = buildAnswerPrefixRegex()

/**
 * Remove common "final answer" wrappers that some models wrap their
 * plain-text answer in. Returns the original string
 * (trimmed only when stripping happens) when no wrapper is detected.
 */
export function unwrapFinalAnswerWrappers(content: string): string {
  if (!content || typeof content !== 'string') {
    return content ?? ''
  }

  let result = content
  let changed = false

  for (let i = 0; i < 3; i++) {
    const tagMatch = result.match(ANSWER_TAG_RE)
    if (!tagMatch) break
    result = tagMatch[2]
    changed = true
  }

  const fencedMatch = result.match(FENCED_ANSWER_RE)
  if (fencedMatch) {
    result = fencedMatch[1]
    changed = true
  }

  const prefixMatch = result.match(ANSWER_PREFIX_RE)
  if (prefixMatch) {
    result = result.slice(prefixMatch[0].length)
    changed = true
  }

  return changed ? result.trim() : result
}

const THINK_BLOCK_RE = /<think\b[^>]*>[\s\S]*?<\/think>/gi

/**
 * Normalise content for cross-event comparison. Strips <think>…</think>
 * blocks, strips final-answer wrappers, and collapses whitespace.
 */
export function normaliseForComparison(content: string): string {
  if (!content || typeof content !== 'string') return ''
  const stripped = content.replace(THINK_BLOCK_RE, '')
  const unwrapped = unwrapFinalAnswerWrappers(stripped)
  return unwrapped.replace(/\s+/g, ' ').trim()
}

/**
 * Returns true when `thinking` and `answer` represent the same final answer
 * after normalisation.
 */
export function thinkingEqualsAnswer(thinking: string, answer: string): boolean {
  const a = normaliseForComparison(thinking)
  const b = normaliseForComparison(answer)
  if (!a || !b) return false
  return a === b
}
