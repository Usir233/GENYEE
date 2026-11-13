export type AuthTokens = { token: string; refreshToken: string }

const listeners = new Set<(tokens: AuthTokens) => void>()

/** Same-page storage writes do not emit the browser's `storage` event. */
export function subscribeAuthTokens(listener: (tokens: AuthTokens) => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

export function notifyAuthTokensChanged(tokens: AuthTokens): void {
  for (const listener of listeners) listener(tokens)
}

export function readAuthTokens(): AuthTokens {
  return {
    token: localStorage.getItem('genyee_token') || '',
    refreshToken: localStorage.getItem('genyee_refresh_token') || '',
  }
}

/** Publish the pair only after both writes complete, including refresh rotation. */
export function writeAuthTokens(tokens: Partial<AuthTokens>): void {
  if (tokens.token !== undefined) localStorage.setItem('genyee_token', tokens.token)
  if (tokens.refreshToken !== undefined) localStorage.setItem('genyee_refresh_token', tokens.refreshToken)
  notifyAuthTokensChanged(readAuthTokens())
}
