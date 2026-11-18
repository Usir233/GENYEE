import type { CustomAgent } from '@/api/agent';
import type { MentionItem } from '@/types/mention';
import type { ScopeCapabilities } from '@/utils/tool-capabilities';

export interface UploadedImagePreview {
  file: File;
  preview: string;
}

export interface KnowledgeIndexingStrategyLike {
  vector_enabled?: boolean;
  keyword_enabled?: boolean;
  wiki_enabled?: boolean;
  graph_enabled?: boolean;
}

export interface KnowledgeBaseRecordLike {
  id: string;
  name: string;
  type?: string;
  knowledge_count?: number;
  chunk_count?: number;
  org_name?: string;
  capabilities?: Partial<ScopeCapabilities>;
  indexing_strategy?: KnowledgeIndexingStrategyLike;
}

export interface SharedKnowledgeBaseWrapperLike {
  knowledge_base?: KnowledgeBaseRecordLike | null;
  org_name?: string;
}

export interface SharedAgentWrapperLike {
  agent?: CustomAgent;
  source_tenant_id: string | number;
  org_name?: string;
  shared_by_username?: string;
  web_search_ready?: boolean;
}

export interface SelectedTagRecord {
  id: string;
  name: string;
  kbId?: string;
  kbName?: string;
}

export interface SelectedChipDisplayItem extends MentionItem {
  org_name?: string;
  isAgentConfigured?: boolean;
}

export interface KnowledgeTagApiItem {
  id: string;
  name: string;
}

export interface KnowledgeFileSearchRecord {
  id: string;
  title?: string;
  file_name?: string;
  knowledge_base_id?: string | number;
  kb_id?: string | number;
  knowledge_base_name?: string;
}

export interface SearchKnowledgeEnvelope {
  data?: KnowledgeFileSearchRecord[];
  total?: number;
  has_more?: boolean;
}

export interface BatchKnowledgeEnvelope {
  data?: Array<{ id: string; title?: string; file_name?: string }>;
}
