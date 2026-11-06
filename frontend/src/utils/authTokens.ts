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
    token: localStorage.getItem('weknora_token') || '',
    refreshToken: localStorage.getItem('weknora_refresh_token') || '',
  }
}

/** Publish the pair only after both writes complete, including refresh rotation. */
export function writeAuthTokens(tokens: Partial<AuthTokens>): void {
  if (tokens.token !== undefined) localStorage.setItem('weknora_token', tokens.token)
  if (tokens.refreshToken !== undefined) localStorage.setItem('weknora_refresh_token', tokens.refreshToken)
  notifyAuthTokensChanged(readAuthTokens())
}
