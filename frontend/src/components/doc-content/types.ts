export type MetadataValueType = 'text' | 'number' | 'boolean' | 'null';

export interface MetadataDraftRow {
  id: number;
  key: string;
  value: string;
  type: MetadataValueType;
}

export interface GeneratedQuestion {
  id: string;
  question: string;
  content_revision?: number;
}

export interface ChunkRevisionEntry {
  id: string;
  revision: number;
  content?: string;
  is_enabled?: boolean;
  edited_at: string;
}

export interface KnowledgeChunkItem {
  id: string;
  content?: string;
  start_at?: number;
  end_at?: number;
  chunk_index?: number;
  char_count?: number;
  token_count?: number;
  metadata?: string | Record<string, unknown>;
  content_revision?: number;
  is_enabled?: boolean;
  index_status?: string;
  parent_chunk_id?: string;
  [key: string]: unknown;
}

export interface ProcessedChunkView {
  original: KnowledgeChunkItem;
  processedContent: string;
  questions: GeneratedQuestion[];
  meta: string;
  hasParent: boolean;
  chunkClass: string;
}

export interface KnowledgeTagItem {
  id: string | number;
  name: string;
}

export interface KnowledgeDocDetails {
  id?: string;
  title?: string;
  type?: string;
  file_type?: string;
  original_file_name?: string;
  file_name?: string;
  time?: string;
  channel?: string;
  source?: string;
  description?: string;
  summary_status?: string;
  parse_status?: string;
  custom_metadata?: Record<string, unknown>;
  tags?: KnowledgeTagItem[];
  md?: KnowledgeChunkItem[];
  total?: number;
  chunkLoading?: boolean;
  chunkLoadError?: string;
  [key: string]: unknown;
}

export interface TimelineSummaryState {
  totalMs: number;
  status: string;
  stageIndex: number;
  stageTotal: number;
  stageLabel: string;
}

export interface SummaryStateChangePayload {
  id: string;
  summary_status?: string;
  description?: string;
}

export function extractErrorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'message' in error) {
    const msg = (error as { message?: unknown }).message;
    if (typeof msg === 'string' && msg.trim()) {
      return msg;
    }
  }
  return fallback;
}

export function extractErrorStatus(error: unknown): number | undefined {
  if (error && typeof error === 'object' && 'status' in error) {
    const status = (error as { status?: unknown }).status;
    if (typeof status === 'number') {
      return status;
    }
  }
  return undefined;
}
