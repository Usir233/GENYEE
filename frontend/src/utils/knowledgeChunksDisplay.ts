import type { ComposerTranslation } from 'vue-i18n'

import type { KnowledgeChunksListData } from '@/types/tool-results'

export function getKnowledgeChunksSummaryHtml(
  t: ComposerTranslation,
  toolData: KnowledgeChunksListData | null | undefined,
): string {
  if (!toolData || toolData.fetched_chunks === undefined) {
    return ''
  }

  const query = toolData.query?.trim()
  if (query) {
    const count = Number(toolData.match_count ?? 0)
    const params = {
      query: escapeHtml(query),
      count: `<strong>${count}${toolData.truncated ? '+' : ''}</strong>`,
    }
    return count > 0
      ? t('agentStream.knowledgeChunksList.queryMatches', params)
      : t('agentStream.knowledgeChunksList.queryNoMatch', params)
  }

  const safeFetched = Number.isFinite(Number(toolData.fetched_chunks))
    ? String(Number(toolData.fetched_chunks))
    : '0'
  const safeTotal =
    toolData.total_chunks === undefined || toolData.total_chunks === null
      ? '?'
      : Number.isFinite(Number(toolData.total_chunks))
      ? String(Number(toolData.total_chunks))
      : '?'
  const parts: string[] = [
    t('agentStream.knowledgeChunksList.chunkRange', {
      fetched: `<strong>${safeFetched}</strong>`,
      total: `<strong>${safeTotal}</strong>`,
    }),
  ]

  const total = Number.isFinite(Number(toolData.total_chunks)) ? Number(toolData.total_chunks) : 0
  const fetched = Number.isFinite(Number(toolData.fetched_chunks)) ? Number(toolData.fetched_chunks) : 0
  // read_document pages by offset and may return fewer than page_size chunks
  // to stay within the output budget, so a page number would be misleading.
  if (toolData.offset !== undefined) {
    const rawOffset = Number(toolData.offset)
    if (fetched > 0 && fetched < total && Number.isFinite(rawOffset)) {
      const from = rawOffset + 1
      parts.push(
        t('agentStream.knowledgeChunksList.offsetRange', { from, to: from + fetched - 1 }),
      )
    }
    return parts.join(' · ')
  }

  const pageSize = Number.isFinite(Number(toolData.page_size)) ? Number(toolData.page_size) : 0
  if (total > pageSize && pageSize > 0) {
    const safePage = Number.isFinite(Number(toolData.page)) ? Number(toolData.page) : 1
    parts.push(
      t('agentStream.knowledgeChunksList.page', {
        page: safePage,
        pageSize,
      }),
    )
  }

  return parts.join(' · ')
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}
