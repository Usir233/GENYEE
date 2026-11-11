/**
 * Safely highlight query keywords inside a plain-text string by wrapping
 * matches in <mark> tags. The input text is HTML-escaped first so the output
 * is safe to bind with v-html.
 *
 * Extracted from the legacy KnowledgeSearch.vue so it can be reused by the
 * global command palette and KB-scoped search bar.
 */
export function escapeHtml(str: string): string {
  return (str || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

const MAX_REGEX_PATTERN_LENGTH = 128
const MAX_HIGHLIGHT_MATCHES = 250
const MAX_HIGHLIGHT_KEYWORDS = 32

export function escapeRegExp(str: string): string {
  return (str || '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Detect regex constructs prone to catastrophic backtracking (ReDoS), such as
 * backreferences, overly long patterns, or quantified groups that themselves
 * contain quantifiers / unbounded wildcards (e.g. `(a+)+`, `([a-z]*)*`, `(a|aa)+`).
 */
export function isUnsafeHighlightRegex(pattern: string): boolean {
  if (!pattern || pattern.length > MAX_REGEX_PATTERN_LENGTH) return true
  // Backreferences are NP-complete in JS RegExp engines
  if (/\\[1-9]/.test(pattern)) return true

  const groupStack: Array<{ hasQuantifier: boolean; hasAlternation: boolean }> = []
  let inCharClass = false
  let unboundedWildcards = 0

  for (let i = 0; i < pattern.length; i++) {
    const ch = pattern[i]
    if (ch === '\\') {
      i++
      continue
    }
    if (inCharClass) {
      if (ch === ']') inCharClass = false
      continue
    }
    if (ch === '[') {
      inCharClass = true
      continue
    }
    if (ch === '(') {
      if (groupStack.length >= 8) return true
      groupStack.push({ hasQuantifier: false, hasAlternation: false })
      continue
    }
    if (ch === '|') {
      if (groupStack.length > 0) {
        groupStack[groupStack.length - 1].hasAlternation = true
      }
      continue
    }
    if (ch === ')') {
      const group = groupStack.pop()
      const next = pattern[i + 1] || ''
      const isRepeatedGroup =
        next === '+' ||
        next === '*' ||
        (next === '{' && /^\{\d+,\d*\}/.test(pattern.slice(i + 1)))
      if (group && isRepeatedGroup && (group.hasQuantifier || group.hasAlternation)) {
        return true
      }
      if (group && groupStack.length > 0 && (group.hasQuantifier || isRepeatedGroup)) {
        groupStack[groupStack.length - 1].hasQuantifier = true
      }
      continue
    }
    if (ch === '+' || ch === '*' || (ch === '{' && /^\{\d+,\d*\}/.test(pattern.slice(i)))) {
      if (ch === '*' || ch === '+') {
        const prev = i > 0 ? pattern[i - 1] : ''
        if (prev === '.') {
          unboundedWildcards++
          if (unboundedWildcards > 2) return true
        }
      }
      if (groupStack.length > 0) {
        groupStack[groupStack.length - 1].hasQuantifier = true
      }
    }
  }
  return false
}

export function highlightRegex(text: string, pattern: string): string {
  const src = text || ''
  const p = (pattern || '').trim()
  const escaped = escapeHtml(src)
  if (!p) return escaped
  try {
    const safePattern = isUnsafeHighlightRegex(p)
      ? escapeRegExp(p.slice(0, MAX_REGEX_PATTERN_LENGTH))
      : p
    const re = new RegExp(safePattern, 'gi')
    let result = ''
    let lastIndex = 0
    let count = 0
    for (const match of src.matchAll(re)) {
      if (!match[0]) continue
      const idx = match.index ?? 0
      if (idx < lastIndex) continue
      result += escapeHtml(src.slice(lastIndex, idx))
      result += `<mark class="search-highlight">${escapeHtml(match[0])}</mark>`
      lastIndex = idx + match[0].length
      count++
      if (count >= MAX_HIGHLIGHT_MATCHES) break
    }
    result += escapeHtml(src.slice(lastIndex))
    return result
  } catch {
    return escaped
  }
}

export function highlightText(text: string, query: string): string {
  const q = (query || '').trim()
  const escaped = escapeHtml(text || '')
  if (!q) return escaped
  const rawKeywords = Array.from(new Set(q.split(/\s+/).filter(Boolean))).slice(0, MAX_HIGHLIGHT_KEYWORDS)
  if (rawKeywords.length === 0) return escaped
  const escapedKws = rawKeywords
    .map((kw) => escapeRegExp(escapeHtml(kw.slice(0, MAX_REGEX_PATTERN_LENGTH))))
    .filter(Boolean)
    .sort((a, b) => b.length - a.length)
  if (escapedKws.length === 0) return escaped

  const regex = new RegExp(`(${escapedKws.join('|')})`, 'gi')
  return escaped.replace(regex, '<mark class="search-highlight">$1</mark>')
}
