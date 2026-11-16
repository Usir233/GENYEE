/** Prefix host-injected context onto the user query for embed chat. */
export function buildQueryWithHostContext(
  query: string,
  hostContext?: Record<string, unknown>,
): string {
  if (!hostContext || !Object.keys(hostContext).length) return query
  const lines = Object.entries(hostContext)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => {
      const safeKey = String(k).replace(/[\r\n]+/g, ' ').trim()
      const safeVal =
        typeof v === 'string'
          ? v.replace(/\r/g, '\\r').replace(/\n/g, '\\n')
          : JSON.stringify(v)
      return `${safeKey}: ${safeVal}`
    })
  if (!lines.length) return query
  return `[Host context]\n${lines.join('\n')}\n\n${query}`
}

/** Remove the injected host-context block from a query for user-visible display. */
export function stripHostContextFromQuery(query: string): string {
  const prefix = '[Host context]\n'
  if (!query.startsWith(prefix)) return query

  const separatorIndex = query.indexOf('\n\n', prefix.length)
  if (separatorIndex === -1) return query
  return query.slice(separatorIndex + 2)
}
