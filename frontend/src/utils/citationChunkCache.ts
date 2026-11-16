export type CitationChunkCacheValue = { content: string; error?: string; retryable?: boolean; retryAfter?: number }

const MAX_CITATION_CHUNK_CACHE_ENTRIES = 500
const chunkCache = new Map<string, CitationChunkCacheValue>()

export function makeCitationCacheKey(scope: string, chunkId: string): string {
  return `${scope}::${chunkId}`
}

export function getCitationChunkCache(
  scope: string,
  chunkId: string,
): CitationChunkCacheValue | undefined {
  return chunkCache.get(makeCitationCacheKey(scope, chunkId))
}

export function setCitationChunkCache(
  scope: string,
  chunkId: string,
  value: CitationChunkCacheValue,
): void {
  const key = makeCitationCacheKey(scope, chunkId)
  if (chunkCache.has(key)) {
    chunkCache.delete(key)
  } else if (chunkCache.size >= MAX_CITATION_CHUNK_CACHE_ENTRIES) {
    const oldest = chunkCache.keys().next().value
    if (oldest !== undefined) chunkCache.delete(oldest)
  }
  chunkCache.set(key, value)
}

export function clearCitationChunkCache(scope?: string): void {
  if (scope === undefined) {
    chunkCache.clear()
    return
  }
  const prefix = `${scope}::`
  for (const key of chunkCache.keys()) {
    if (key.startsWith(prefix)) {
      chunkCache.delete(key)
    }
  }
}
