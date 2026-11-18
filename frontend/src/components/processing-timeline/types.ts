import type { KnowledgeTraceNode } from '@/utils/knowledgeTrace'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'

export const STAGES = ['docreader', 'chunking', 'embedding', 'multimodal', 'postprocess'] as const
export type StageName = (typeof STAGES)[number]

export type SpanNode = KnowledgeTraceNode & {
  updated_at?: string
  created_at?: string
  parent_span_id?: string
  input?: unknown
  output?: unknown
  metadata?: Record<string, unknown>
  children?: SpanNode[]
}

export interface LastError {
  name: string
  error_code: string
  error_message: string
  finished_at?: string
}

export interface SpansResponse {
  knowledge_id: string
  attempt: number
  latest_attempt: number
  current_attempt?: number
  parse_status: string
  current_stage?: string
  trace: SpanNode
  last_error?: LastError | null
  last_activity_at?: string
  stall_state?: string
}

export type TimelineProcessOverrides = Omit<KnowledgeProcessOverrides, 'enable_multimodel'> & {
  enable_multimodal?: boolean
  [key: string]: unknown
}

export interface FlatRow {
  key: string
  depth: number
  node: SpanNode
  hasChildren: boolean
  isRoot: boolean
  isStage: boolean
  parentKey?: string
}

export interface AttemptTab {
  n: number
  status: string
  active: boolean
}

export interface HeadStat {
  key: string
  label: string
  value: string
  mono: boolean
  note?: boolean
}

export interface KvEntry {
  key: string
  label: string
  kind: 'scalar' | 'bool' | 'array' | 'object'
  display: string
  raw: unknown
  defaultExpanded: boolean
}

export interface IdentityField {
  key: string
  label: string
  value: string
  mono: boolean
  copyable: boolean
}

export interface StageRowSummary {
  name: string
  label: string
  status: string
  duration_ms?: number
  pct: number
}

export type DetailTabKey = 'overview' | 'input' | 'output' | 'metadata' | 'raw'

export interface TimelineSummaryPayload {
  totalMs: number
  status: string
  stageIndex: number
  stageTotal: number
  stageLabel: string
}

export function extractErrorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'message' in error) {
    const msg = (error as { message?: unknown }).message
    if (typeof msg === 'string' && msg.trim()) {
      return msg
    }
  }
  return fallback
}
