/** Plugin configuration: shape, defaults, and load-time validation. */

/** Which tools the plugin registers. Omitted entries keep their default. */
export interface ToolToggles {
  listKnowledgeBases?: boolean
  search?: boolean
  readDocument?: boolean
  ask?: boolean
}

/** User-supplied configuration, as written in a Cordis patch row. */
export interface Config {
  /** GENYEE API root, with or without the `/api/v1` suffix. */
  baseUrl?: string
  /** Tenant or platform API key, sent as `X-API-Key`. */
  apiKey?: string
  /** Workspace id, required only for platform-scoped API keys (`X-Tenant-ID`). */
  tenantId?: string
  /** Default knowledge-base scope when a call names none. */
  knowledgeBaseIds?: string[]
  /** Default custom agent for `genyee_ask`; omitted uses the RAG pipeline. */
  agentId?: string
  /** Default and maximum number of chunks a search returns. */
  maxResults?: number
  /** Per-chunk character budget before the plugin truncates content. */
  maxChunkChars?: number
  /** Timeout for non-streaming requests. */
  requestTimeoutMs?: number
  /** Timeout for a streamed answer, which includes model and tool time. */
  chatTimeoutMs?: number
  /**
   * `public` asks GENYEE for directly loadable file URLs in answers and
   * citations (the default: dsh can render `https://` Markdown images, not
   * `resource://` handles). `handle` keeps the internal references.
   */
  resourceUrls?: 'handle' | 'public'
  /** Tool-name prefix, so two instances can serve two deployments. */
  toolPrefix?: string
  /** Per-tool registration switches. */
  tools?: ToolToggles
}

/** Configuration after defaults and validation. */
export interface ResolvedConfig {
  baseUrl: string
  apiKey: string | undefined
  tenantId: string | undefined
  knowledgeBaseIds: string[]
  agentId: string | undefined
  maxResults: number
  maxChunkChars: number
  requestTimeoutMs: number
  chatTimeoutMs: number
  resourceUrls: 'handle' | 'public'
  toolPrefix: string
  tools: Required<ToolToggles>
}

const DEFAULTS = {
  baseUrl: 'http://localhost:8080/api/v1',
  maxResults: 8,
  maxChunkChars: 1200,
  requestTimeoutMs: 30_000,
  chatTimeoutMs: 300_000,
  resourceUrls: 'public',
  toolPrefix: 'genyee',
} as const

/** Signed 32-bit timer ceiling so `AbortSignal.timeout` never overflows to 1ms. */
const MAX_TIMEOUT_MS = 2_147_483_647

const SECRET_PATTERNS: [RegExp, string][] = [
  [/([a-z][a-z0-9+.-]*:\/\/)[^/\s@"']+@/gi, '$1[REDACTED]@'],
  [/\bBearer\s+\S+/gi, 'Bearer [REDACTED]'],
  [/\bgy_[A-Za-z0-9_-]+/g, '[REDACTED]'],
]

function hasControlChars(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i)
    if (code <= 0x1f || code === 0x7f) return true
  }
  return false
}

/** Redact credentials and bearer/API tokens before formatting error messages. */
export function redactSecrets(text: string, extraSecret?: string): string {
  let out = text
  if (extraSecret !== undefined && extraSecret !== '') {
    out = out.replaceAll(extraSecret, '[REDACTED]')
  }
  for (const [pattern, replacement] of SECRET_PATTERNS) {
    out = out.replace(pattern, replacement)
  }
  return out
}

/** Thrown when a patch row configures this plugin in a way it cannot serve. */
export class ConfigError extends Error {
  constructor(violations: string[]) {
    super(`dsh-genyee configuration is invalid:\n  - ${violations.map(v => redactSecrets(v)).join('\n  - ')}`)
    this.name = 'ConfigError'
  }
}

const API_ROOT = /\/api\/v\d+$/
const TOOL_PREFIX = /^[a-z][a-z0-9_]*$/
const IPV4_LOOPBACK = /^127(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}$/

function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase()
  return normalized === 'localhost'
    || normalized === '[::1]'
    || normalized === '::1'
    || IPV4_LOOPBACK.test(normalized)
}

function validateBaseUrl(raw: string): { baseUrl: string, violations: string[] } {
  if (hasControlChars(raw)) {
    return { baseUrl: DEFAULTS.baseUrl, violations: ['baseUrl must not contain control characters'] }
  }
  const trimmed = raw.trim()
  let parsed: URL
  try {
    parsed = new URL(trimmed)
  } catch {
    return {
      baseUrl: DEFAULTS.baseUrl,
      violations: [`baseUrl must be an absolute URL, received ${JSON.stringify(redactSecrets(raw))}`],
    }
  }

  const violations: string[] = []
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    violations.push(`baseUrl must use http or https, received ${parsed.protocol}`)
  } else if (parsed.protocol === 'http:' && !isLoopbackHostname(parsed.hostname)) {
    violations.push(`baseUrl must use https for non-loopback hosts, received ${parsed.origin}`)
  }
  if (parsed.username !== '' || parsed.password !== '') {
    violations.push('baseUrl must not include credentials')
  }
  if (parsed.search !== '' || parsed.hash !== '' || /[?#]/.test(trimmed)) {
    violations.push(`baseUrl must not include query or fragment, received ${JSON.stringify(redactSecrets(raw))}`)
  }
  if (violations.length > 0) {
    return { baseUrl: DEFAULTS.baseUrl, violations }
  }

  const pathname = parsed.pathname.replace(/\/+$/, '')
  parsed.pathname = API_ROOT.test(pathname) ? pathname : `${pathname}/api/v1`
  return { baseUrl: parsed.toString().replace(/\/+$/, ''), violations: [] }
}

/**
 * Normalize a GENYEE base URL to an API root. Both `https://kb.example.com`
 * and `https://kb.example.com/api/v1` are accepted, because a deployment's
 * documented address is usually the bare origin.
 */
export function normalizeBaseUrl(raw: string): string {
  const { baseUrl, violations } = validateBaseUrl(raw)
  if (violations.length > 0) {
    throw new ConfigError(violations)
  }
  return baseUrl
}

function positiveInt(
  value: unknown,
  field: string,
  violations: string[],
  max: number = Number.MAX_SAFE_INTEGER,
): number | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 1 || value > max) {
    violations.push(`${field} must be a positive number between 1 and ${max}, received ${JSON.stringify(value)}`)
    return undefined
  }
  return Math.floor(value)
}

function stringList(value: unknown, field: string, violations: string[]): string[] | undefined {
  if (value === undefined) return undefined
  if (!Array.isArray(value) || value.some(entry => typeof entry !== 'string' || entry.trim() === '')) {
    violations.push(`${field} must be an array of non-empty strings`)
    return undefined
  }
  const entries = value as string[]
  if (entries.some(entry => hasControlChars(entry))) {
    violations.push(`${field} must not contain control characters`)
    return undefined
  }
  return entries.map(entry => entry.trim())
}

function optionalString(value: unknown, field: string, violations: string[]): string | undefined {
  if (value === undefined || value === null || value === '') return undefined
  if (typeof value !== 'string') {
    violations.push(`${field} must be a string`)
    return undefined
  }
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

function toggle(value: unknown, field: string, fallback: boolean, violations: string[]): boolean {
  if (value === undefined) return fallback
  if (typeof value !== 'boolean') {
    violations.push(`${field} must be a boolean`)
    return fallback
  }
  return value
}

/**
 * Apply defaults and reject an unusable row while the plugin loads, so a typo
 * surfaces at boot rather than inside the first tool call the model makes.
 * @param raw - the `config` value from the Cordis row.
 * @returns configuration every consumer can read without re-checking.
 */
export function resolveConfig(raw?: Config): ResolvedConfig
export function resolveConfig(raw: unknown): ResolvedConfig
export function resolveConfig(raw?: unknown): ResolvedConfig {
  const violations: string[] = []
  if (raw !== undefined && (typeof raw !== 'object' || raw === null || Array.isArray(raw))) {
    throw new ConfigError(['config must be an object'])
  }
  const config = (raw ?? {}) as Record<string, unknown>

  const baseUrlInput = optionalString(config['baseUrl'], 'baseUrl', violations) ?? DEFAULTS.baseUrl
  const { baseUrl, violations: urlViolations } = validateBaseUrl(baseUrlInput)
  violations.push(...urlViolations)

  if (typeof config['apiKey'] === 'string' && hasControlChars(config['apiKey'])) {
    violations.push('apiKey must not contain control characters')
  }
  if (typeof config['tenantId'] === 'string' && hasControlChars(config['tenantId'])) {
    violations.push('tenantId must not contain control characters')
  }
  if (typeof config['agentId'] === 'string' && hasControlChars(config['agentId'])) {
    violations.push('agentId must not contain control characters')
  }

  const resourceUrls = config['resourceUrls'] ?? DEFAULTS.resourceUrls
  if (resourceUrls !== 'handle' && resourceUrls !== 'public') {
    violations.push(`resourceUrls must be "handle" or "public", received ${JSON.stringify(resourceUrls)}`)
  }

  const toolPrefix = optionalString(config['toolPrefix'], 'toolPrefix', violations) ?? DEFAULTS.toolPrefix
  if (!TOOL_PREFIX.test(toolPrefix)) {
    violations.push(`toolPrefix must match ${String(TOOL_PREFIX)}, received ${JSON.stringify(toolPrefix)}`)
  }

  const rawToggles = config['tools'] ?? {}
  const togglesValid = typeof rawToggles === 'object' && rawToggles !== null && !Array.isArray(rawToggles)
  if (!togglesValid) {
    violations.push('tools must be an object of booleans')
  }
  const toolToggles = togglesValid ? rawToggles as Record<string, unknown> : {}

  const resolved: ResolvedConfig = {
    baseUrl,
    apiKey: optionalString(config['apiKey'], 'apiKey', violations),
    tenantId: optionalString(config['tenantId'], 'tenantId', violations),
    knowledgeBaseIds: stringList(config['knowledgeBaseIds'], 'knowledgeBaseIds', violations) ?? [],
    agentId: optionalString(config['agentId'], 'agentId', violations),
    maxResults: positiveInt(config['maxResults'], 'maxResults', violations) ?? DEFAULTS.maxResults,
    maxChunkChars: positiveInt(config['maxChunkChars'], 'maxChunkChars', violations) ?? DEFAULTS.maxChunkChars,
    requestTimeoutMs: positiveInt(config['requestTimeoutMs'], 'requestTimeoutMs', violations, MAX_TIMEOUT_MS)
      ?? DEFAULTS.requestTimeoutMs,
    chatTimeoutMs: positiveInt(config['chatTimeoutMs'], 'chatTimeoutMs', violations, MAX_TIMEOUT_MS)
      ?? DEFAULTS.chatTimeoutMs,
    resourceUrls: resourceUrls === 'public' ? 'public' : 'handle',
    toolPrefix,
    tools: {
      listKnowledgeBases: toggle(toolToggles['listKnowledgeBases'], 'tools.listKnowledgeBases', true, violations),
      search: toggle(toolToggles['search'], 'tools.search', true, violations),
      readDocument: toggle(toolToggles['readDocument'], 'tools.readDocument', true, violations),
      ask: toggle(toolToggles['ask'], 'tools.ask', true, violations),
    },
  }

  if (!Object.values(resolved.tools).some(Boolean)) {
    violations.push('at least one tool must stay enabled; remove the plugin row instead of disabling all of them')
  }
  if (violations.length > 0) throw new ConfigError(violations)
  return resolved
}
