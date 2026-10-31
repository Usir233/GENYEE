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

export function highlightRegex(text: string, pattern: string): string {
  const src = text || ''
  const p = (pattern || '').trim()
  const escaped = escapeHtml(src)
  if (!p) return escaped
  try {
    const re = new RegExp(p, 'gi')
    let result = ''
    let lastIndex = 0
    for (const match of src.matchAll(re)) {
      const idx = match.index ?? 0
      result += escapeHtml(src.slice(lastIndex, idx))
      result += `<mark class="search-highlight">${escapeHtml(match[0])}</mark>`
      lastIndex = idx + match[0].length
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
  const rawKeywords = Array.from(new Set(q.split(/\s+/).filter(Boolean)))
  if (rawKeywords.length === 0) return escaped
  const escapedKws = rawKeywords
    .map((kw) => escapeHtml(kw).replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .filter(Boolean)
    .sort((a, b) => b.length - a.length)
  if (escapedKws.length === 0) return escaped

  const regex = new RegExp(`(${escapedKws.join('|')})`, 'gi')
  return escaped.replace(regex, '<mark class="search-highlight">$1</mark>')
}
