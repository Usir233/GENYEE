/** Minimal GENYEE REST client: JSON endpoints plus the SSE chat streams. */

import { redactSecrets, type ResolvedConfig } from './config.ts'

/** One retrieval hit, as returned by GENYEE's `SearchResult`. */
export interface SearchResult {
  id?: string
  content?: string
  knowledge_id?: string
  chunk_index?: number
  knowledge_title?: string
  knowledge_filename?: string
  score?: number
  match_type?: number | string
  [key: string]: unknown
}

/** A knowledge base as listed by `GET /knowledge-bases`. */
export interface KnowledgeBaseSummary {
  id?: string
  name?: string
  description?: string
  [key: string]: unknown
}

/** One stored chunk as returned by `GET /chunks/:knowledge_id`. */
export interface ChunkRecord {
  id?: string
  content?: string
  chunk_index?: number
  knowledge_id?: string
  [key: string]: unknown
}

/** A document as returned by the by-name search and by `GET /knowledge/:id`. */
export interface DocumentRecord {
  id?: string
  title?: string
  file_name?: string
  file_type?: string
  description?: string
  knowledge_base_id?: string
  knowledge_base_name?: string
  [key: string]: unknown
}

/** A page of chunks plus its pagination envelope. */
export interface ChunkPage {
  chunks: ChunkRecord[]
  total: number
  page: number
  pageSize: number
}

/** The assembled outcome of one streamed answer. */
export interface StreamedAnswer {
  answer: string
  references: SearchResult[]
  sessionId: string
  toolCalls: string[]
}

const MAX_ERROR_BODY_BYTES = 1 * 1024 * 1024
const MAX_JSON_RESPONSE_BYTES = 16 * 1024 * 1024
const MAX_SSE_EVENT_BYTES = 4 * 1024 * 1024
const MAX_SSE_STREAM_BYTES = 16 * 1024 * 1024

/** Raised for any non-2xx response or transport failure, with the key redacted. */
export class GenyeeApiError extends Error {
  readonly status: number | undefined

  constructor(message: string, status?: number, extraSecret?: string) {
    super(redactSecrets(message, extraSecret))
    this.name = 'GenyeeApiError'
    this.status = status
  }
}

interface Envelope<T> {
  success?: boolean
  data?: T
  error?: unknown
  message?: string
  total?: number
  page?: number
  page_size?: number
}

/** Trim a response body for an error message without leaking a whole page. */
function snippet(body: string): string {
  const collapsed = body.replace(/\s+/g, ' ').trim()
  return collapsed.length > 300 ? `${collapsed.slice(0, 300)}…` : collapsed
}

/** Extract a human-readable reason from GENYEE's error envelope. */
function reasonOf(body: string): string {
  try {
    const parsed = JSON.parse(body) as { error?: { message?: string } | string, message?: string }
    if (typeof parsed.error === 'string') return parsed.error
    if (parsed.error?.message !== undefined) return parsed.error.message
    if (parsed.message !== undefined) return parsed.message
  } catch {
    // Fall through to the raw snippet: a proxy may answer with HTML.
  }
  return snippet(body)
}

/**
 * Combine the caller's cancellation with this call's deadline. The tool body
 * must abort when the harness aborts, and a hung backend must not hold a turn
 * open forever.
 */
function deadline(signal: AbortSignal, timeoutMs: number): AbortSignal {
  return AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)])
}

/**
 * Validate and percent-encode a single URL path segment so untrusted or
 * hallucinated IDs ("", ".", "..", "a/b", "%2e%2e") cannot traverse routes.
 */
export function encodePathSegment(value: string, field: string): string {
  if (typeof value !== 'string') {
    throw new GenyeeApiError(`Invalid ${field}: expected non-empty string`)
  }
  const trimmed = value.trim()
  if (trimmed === '' || trimmed !== value) {
    throw new GenyeeApiError(`Invalid ${field}: must not be empty or padded with whitespace`)
  }
  if (trimmed === '.' || trimmed === '..' || trimmed.includes('..')) {
    throw new GenyeeApiError(`Invalid ${field}: dot-segment path traversal is forbidden`)
  }
  for (let i = 0; i < trimmed.length; i++) {
    const code = trimmed.charCodeAt(i)
    const ch = trimmed[i]!
    if (code < 32 || code === 127 || ch === '/' || ch === '\\' || ch === '?' || ch === '#' || ch === '%') {
      throw new GenyeeApiError(`Invalid ${field}: contains disallowed URL character`)
    }
  }
  return encodeURIComponent(trimmed)
}

export class GenyeeClient {
  /**
   * A knowledge-base-restricted API key rejects `resource_urls=public` with
   * 403. After the first such refusal this client stays on handle mode so
   * later calls do not pay the same round trip.
   */
  private publicModeBlocked = false

  constructor(private readonly config: ResolvedConfig) {}

  private headers(): Record<string, string> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Accept': 'application/json',
    }
    if (this.config.apiKey !== undefined) headers['X-API-Key'] = this.config.apiKey
    if (this.config.tenantId !== undefined) headers['X-Tenant-ID'] = this.config.tenantId
    return headers
  }

  private resourceMode(): 'handle' | 'public' {
    return this.config.resourceUrls === 'public' && !this.publicModeBlocked ? 'public' : 'handle'
  }

  private url(path: string, query: Record<string, string | number | undefined> | undefined, usePublic: boolean): string {
    const url = new URL(`${this.config.baseUrl}${path}`)
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value !== undefined) url.searchParams.set(key, String(value))
    }
    if (usePublic) url.searchParams.set('resource_urls', 'public')
    return url.toString()
  }

  private isPublicModeForbidden(error: unknown, usedPublic: boolean): boolean {
    return usedPublic
      && error instanceof GenyeeApiError
      && error.status === 403
      && error.message.includes('resource_urls=public')
  }

  private async fetchJson<T>(
    path: string,
    init: { method: 'GET' | 'POST', body?: unknown, query?: Record<string, string | number | undefined> },
    signal: AbortSignal,
  ): Promise<Envelope<T>> {
    const usedPublic = this.resourceMode() === 'public'
    try {
      return await this.fetchJsonOnce(path, init, signal, usedPublic)
    } catch (error) {
      if (!this.isPublicModeForbidden(error, usedPublic)) throw error
      this.publicModeBlocked = true
      return await this.fetchJsonOnce(path, init, signal, false)
    }
  }

  private async fetchJsonOnce<T>(
    path: string,
    init: { method: 'GET' | 'POST', body?: unknown, query?: Record<string, string | number | undefined> },
    signal: AbortSignal,
    usePublic: boolean,
  ): Promise<Envelope<T>> {
    const url = this.url(path, init.query, usePublic)
    const label = `${init.method} ${path}`
    let response: Response
    try {
      response = await fetch(url, {
        method: init.method,
        headers: this.headers(),
        redirect: 'error',
        ...init.body === undefined ? {} : { body: JSON.stringify(init.body) },
        signal: deadline(signal, this.config.requestTimeoutMs),
      })
    } catch (cause) {
      throw new GenyeeApiError(`${label} failed: ${describeTransportFailure(cause, signal)}`, undefined, this.config.apiKey)
    }
    if (!response.ok) {
      const text = await readBoundedText(response, MAX_ERROR_BODY_BYTES, true, label, this.config.apiKey)
      throw new GenyeeApiError(`${label} failed with HTTP ${response.status}: ${reasonOf(text)}`, response.status, this.config.apiKey)
    }
    const text = await readBoundedText(response, MAX_JSON_RESPONSE_BYTES, false, label, this.config.apiKey)
    try {
      return JSON.parse(text) as Envelope<T>
    } catch {
      throw new GenyeeApiError(`${label} returned a non-JSON body: ${snippet(text)}`, response.status, this.config.apiKey)
    }
  }

  /** List the knowledge bases this credential can retrieve from. */
  async listKnowledgeBases(signal: AbortSignal): Promise<KnowledgeBaseSummary[]> {
    const envelope = await this.fetchJson<KnowledgeBaseSummary[]>('/knowledge-bases', { method: 'GET' }, signal)
    return Array.isArray(envelope.data) ? envelope.data : []
  }

  /** Hybrid (vector + keyword) retrieval across one or more knowledge bases. */
  async search(
    input: { query: string, knowledgeBaseIds: string[], knowledgeIds: string[] },
    signal: AbortSignal,
  ): Promise<SearchResult[]> {
    const envelope = await this.fetchJson<SearchResult[]>('/knowledge-search', {
      method: 'POST',
      body: {
        query: input.query,
        ...input.knowledgeBaseIds.length > 0 ? { knowledge_base_ids: input.knowledgeBaseIds } : {},
        ...input.knowledgeIds.length > 0 ? { knowledge_ids: input.knowledgeIds } : {},
      },
    }, signal)
    return Array.isArray(envelope.data) ? envelope.data : []
  }

  /**
   * Find documents by title or filename. Unlike `search` this spans every
   * knowledge base the credential can see and needs no scope, so it is what
   * answers "where is the document called X".
   */
  async findDocuments(
    input: { keyword: string, limit: number },
    signal: AbortSignal,
  ): Promise<DocumentRecord[]> {
    const envelope = await this.fetchJson<DocumentRecord[]>('/knowledge/search', {
      method: 'GET',
      query: { keyword: input.keyword, limit: input.limit },
    }, signal)
    return Array.isArray(envelope.data) ? envelope.data : []
  }

  /** A document's metadata, including the generated summary GENYEE stores as `description`. */
  async getDocument(knowledgeId: string, signal: AbortSignal): Promise<DocumentRecord> {
    const envelope = await this.fetchJson<DocumentRecord>(
      `/knowledge/${encodePathSegment(knowledgeId, 'knowledgeId')}`,
      { method: 'GET' },
      signal,
    )
    return envelope.data ?? {}
  }

  /** One page of a document's stored chunks, in storage order. */
  async listChunks(
    input: { knowledgeId: string, page: number, pageSize: number },
    signal: AbortSignal,
  ): Promise<ChunkPage> {
    const envelope = await this.fetchJson<ChunkRecord[]>(`/chunks/${encodePathSegment(input.knowledgeId, 'knowledgeId')}`, {
      method: 'GET',
      query: { page: input.page, page_size: input.pageSize },
    }, signal)
    const chunks = Array.isArray(envelope.data) ? envelope.data : []
    return {
      chunks,
      total: typeof envelope.total === 'number' ? envelope.total : chunks.length,
      page: typeof envelope.page === 'number' ? envelope.page : input.page,
      pageSize: typeof envelope.page_size === 'number' ? envelope.page_size : input.pageSize,
    }
  }

  /** Create a conversation container to hold one or more asked questions. */
  async createSession(title: string, signal: AbortSignal): Promise<string> {
    const envelope = await this.fetchJson<{ id?: string }>('/sessions', {
      method: 'POST',
      body: { title },
    }, signal)
    const id = envelope.data?.id
    if (typeof id !== 'string' || id === '') {
      throw new GenyeeApiError('POST /sessions returned no session id')
    }
    return id
  }

  /**
   * Ask a question and assemble the streamed answer. `agentId` selects the
   * ReAct pipeline (`/agent-chat`); without it the RAG pipeline answers
   * (`/knowledge-chat`).
   */
  async ask(
    input: {
      sessionId: string
      query: string
      knowledgeBaseIds: string[]
      agentId: string | undefined
      webSearch: boolean
    },
    signal: AbortSignal,
  ): Promise<StreamedAnswer> {
    const route = input.agentId === undefined ? 'knowledge-chat' : 'agent-chat'
    const path = `/${route}/${encodePathSegment(input.sessionId, 'sessionId')}`
    const body = {
      query: input.query,
      channel: 'api',
      ...input.knowledgeBaseIds.length > 0 ? { knowledge_base_ids: input.knowledgeBaseIds } : {},
      ...input.agentId === undefined ? {} : { agent_id: input.agentId, agent_enabled: true },
      ...input.webSearch ? { web_search_enabled: true } : {},
    }
    const open = async (usePublic: boolean): Promise<Response> => {
      const combined = deadline(signal, this.config.chatTimeoutMs)
      try {
        return await fetch(this.url(path, undefined, usePublic), {
          method: 'POST',
          headers: { ...this.headers(), Accept: 'text/event-stream' },
          redirect: 'error',
          body: JSON.stringify(body),
          signal: combined,
        })
      } catch (cause) {
        throw new GenyeeApiError(`POST ${path} failed: ${describeTransportFailure(cause, signal)}`, undefined, this.config.apiKey)
      }
    }
    const usedPublic = this.resourceMode() === 'public'
    let response = await open(usedPublic)
    if (!response.ok) {
      const text = await readBoundedText(response, MAX_ERROR_BODY_BYTES, true, `POST ${path}`, this.config.apiKey)
      const error = new GenyeeApiError(`POST ${path} failed with HTTP ${response.status}: ${reasonOf(text)}`, response.status, this.config.apiKey)
      if (!this.isPublicModeForbidden(error, usedPublic)) throw error
      this.publicModeBlocked = true
      response = await open(false)
    }
    if (!response.ok) {
      const text = await readBoundedText(response, MAX_ERROR_BODY_BYTES, true, `POST ${path}`, this.config.apiKey)
      throw new GenyeeApiError(`POST ${path} failed with HTTP ${response.status}: ${reasonOf(text)}`, response.status, this.config.apiKey)
    }
    if (response.body === null) {
      throw new GenyeeApiError(`POST ${path} returned no response body`, undefined, this.config.apiKey)
    }
    return await assembleStream(response.body, input.sessionId, path, signal, this.config.apiKey)
  }
}

/** Read a UTF-8 response body up to `maxBytes` without buffering unbounded data. */
async function readBoundedText(
  response: Response,
  maxBytes: number,
  truncateOnOverflow: boolean,
  label: string,
  apiKey?: string,
): Promise<string> {
  if (response.body === null) return ''
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  const parts: string[] = []
  let totalBytes = 0
  let truncated = false
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      if (totalBytes + value.byteLength > maxBytes) {
        if (!truncateOnOverflow) {
          throw new GenyeeApiError(`${label} response exceeded ${maxBytes} bytes`, response.status, apiKey)
        }
        const remaining = Math.max(0, maxBytes - totalBytes)
        if (remaining > 0) {
          parts.push(decoder.decode(value.subarray(0, remaining), { stream: false }))
        }
        truncated = true
        break
      }
      totalBytes += value.byteLength
      parts.push(decoder.decode(value, { stream: true }))
    }
    if (!truncated) parts.push(decoder.decode())
  } finally {
    await reader.cancel().catch(() => undefined)
  }
  return parts.join('')
}

/** Turn an aborted or refused fetch into a message that names the real cause. */
function describeTransportFailure(cause: unknown, callerSignal: AbortSignal): string {
  if (cause instanceof Error && cause.name === 'AbortError') {
    return callerSignal.aborted ? 'the call was cancelled' : 'the request timed out'
  }
  if (cause instanceof Error && cause.name === 'TimeoutError') return 'the request timed out'
  return cause instanceof Error ? cause.message : String(cause)
}

/** One decoded `data:` payload of the GENYEE stream. */
interface StreamEvent {
  response_type?: string
  content?: string
  knowledge_references?: SearchResult[]
  session_id?: string
  data?: { tool_name?: unknown }
  tool_calls?: { function?: { name?: string }, name?: string }[]
}

/**
 * Consume GENYEE's `text/event-stream` and assemble the parts a tool result
 * needs: the answer text, its citations, and the tool names the agent used.
 * Server-side `error` events become a thrown failure so the model is never
 * handed a silently empty answer.
 */
async function assembleStream(
  body: ReadableStream<Uint8Array>,
  sessionId: string,
  path: string,
  callerSignal: AbortSignal,
  apiKey?: string,
): Promise<StreamedAnswer> {
  const decoder = new TextDecoder()
  const reader = body.getReader()
  const answer: string[] = []
  const toolCalls: string[] = []
  let references: SearchResult[] = []
  let resolvedSessionId = sessionId
  let buffer = ''
  let eventType = 'message'
  let dataLines: string[] = []
  let frameBytes = 0
  let streamBytes = 0
  let completed = false

  const dispatchFrame = (): void => {
    const currentType = eventType
    const lines = dataLines
    eventType = 'message'
    dataLines = []
    frameBytes = 0
    if (lines.length === 0) return
    if (currentType !== '' && currentType !== 'message') return
    const payload = lines.join('\n').trim()
    if (payload === '' || payload === '[DONE]') return
    let event: StreamEvent
    try {
      event = JSON.parse(payload) as StreamEvent
    } catch {
      return
    }
    if (typeof event.session_id === 'string' && event.session_id !== '') resolvedSessionId = event.session_id
    switch (event.response_type) {
      case 'answer':
        if (typeof event.content === 'string') answer.push(event.content)
        break
      case 'references':
        if (Array.isArray(event.knowledge_references)) references = event.knowledge_references
        break
      case 'tool_call':
        if (typeof event.data?.tool_name === 'string' && event.data.tool_name !== '' && !toolCalls.includes(event.data.tool_name)) {
          toolCalls.push(event.data.tool_name)
        }
        for (const call of event.tool_calls ?? []) {
          const name = call.function?.name ?? call.name
          if (typeof name === 'string' && name !== '' && !toolCalls.includes(name)) toolCalls.push(name)
        }
        break
      case 'error':
        throw new GenyeeApiError(`POST ${path} streamed an error: ${event.content ?? 'unknown error'}`, undefined, apiKey)
      case 'complete':
        completed = true
        break
      default:
        break
    }
  }

  const processLine = (rawLine: string): void => {
    const line = rawLine.endsWith('\r') ? rawLine.slice(0, -1) : rawLine
    if (line === '') {
      dispatchFrame()
      return
    }
    if (line.startsWith(':')) return
    frameBytes += Buffer.byteLength(line, 'utf8') + 1
    if (frameBytes > MAX_SSE_EVENT_BYTES) {
      throw new GenyeeApiError(`POST ${path} SSE event exceeded ${MAX_SSE_EVENT_BYTES} bytes`, undefined, apiKey)
    }
    if (line.startsWith('event:')) {
      eventType = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      dataLines.push(line.startsWith('data: ') ? line.slice(6) : line.slice(5))
    }
  }

  try {
    while (!completed) {
      const { done, value } = await reader.read()
      if (done) {
        buffer += decoder.decode()
        let newline = buffer.indexOf('\n')
        while (newline >= 0) {
          processLine(buffer.slice(0, newline))
          buffer = buffer.slice(newline + 1)
          if (completed) break
          newline = buffer.indexOf('\n')
        }
        break
      }
      streamBytes += value.byteLength
      if (streamBytes > MAX_SSE_STREAM_BYTES) {
        throw new GenyeeApiError(`POST ${path} stream exceeded ${MAX_SSE_STREAM_BYTES} bytes`, undefined, apiKey)
      }
      buffer += decoder.decode(value, { stream: true })
      if (buffer.length > MAX_SSE_EVENT_BYTES) {
        const firstNewline = buffer.indexOf('\n')
        if (firstNewline < 0 || firstNewline > MAX_SSE_EVENT_BYTES) {
          throw new GenyeeApiError(`POST ${path} SSE event exceeded ${MAX_SSE_EVENT_BYTES} bytes`, undefined, apiKey)
        }
      }
      let newline = buffer.indexOf('\n')
      while (newline >= 0) {
        processLine(buffer.slice(0, newline))
        buffer = buffer.slice(newline + 1)
        if (completed) break
        newline = buffer.indexOf('\n')
      }
    }
  } catch (cause) {
    if (cause instanceof GenyeeApiError) throw cause
    throw new GenyeeApiError(`POST ${path} stream failed: ${describeTransportFailure(cause, callerSignal)}`, undefined, apiKey)
  } finally {
    await reader.cancel().catch(() => undefined)
  }

  // GENYEE ends every answer with a `complete` event. Without it the stream was
  // cut short, and handing the model the partial text would present a truncated
  // answer as a whole one.
  if (!completed) {
    throw new GenyeeApiError(
      `POST ${path} ended before GENYEE completed the answer; `
      + `${answer.join('').length} character(s) had streamed`,
      undefined,
      apiKey,
    )
  }

  return { answer: answer.join(''), references, sessionId: resolvedSessionId, toolCalls }
}
