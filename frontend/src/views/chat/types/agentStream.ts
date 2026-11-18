import type { UsedMemory } from '@/composables/useChatMemoryRow';
import type { DisplayType } from '@/types/tool-results';
import type { AskUserQuestion } from '@/utils/askUser';
import type { AssistantPageLink } from '@/utils/assistantPageLink';
import type { CollaborationTaskView } from '@/utils/collaborationDisplay';
import type { KnowledgeReferenceLike } from '@/utils/referenceSources';
import type { SecureFormRequest } from '@/utils/secureForm';
import type { SessionArtifact } from '@/utils/sessionArtifacts';
import type { ShellCommandProgress } from '@/utils/shellProgress';
import type { HostApprovalPayload, HostApprovalScope } from '../components/hostApproval';
import type { PageFillResult } from '../components/PageEditCard.vue';

export interface AgentPlanStep {
  id?: string;
  description?: string;
  status?: 'in_progress' | 'pending' | 'completed' | string;
}

export interface AgentWebResultItem {
  url?: string;
  title?: string;
  source?: string;
  snippet?: string;
  content?: string;
  summary?: string;
  raw_content?: string;
  status?: string | number;
  result_index?: number;
  chunk_id?: string;
  knowledge_id?: string;
  knowledge_title?: string;
  faq_standard_question?: string;
  faq_question?: string;
  knowledge_base_id?: string;
  chunk_type?: string;
  match_snippet?: string;
}

export interface AgentToolData extends Omit<Partial<AssistantPageLink>, 'display_type'>, Partial<PageFillResult> {
  display_type?: DisplayType | string;
  knowledge_base_id?: string;
  knowledge_base_ids?: string[];
  knowledge_id?: string;
  knowledge_title?: string;
  title?: string;
  slug?: string;
  old_slug?: string;
  new_slug?: string;
  page_type?: string;
  summary?: string;
  old_text?: string;
  new_text?: string;
  affected_pages?: string[] | string;
  results?: AgentWebResultItem[];
  chunk_results?: Array<Record<string, unknown>>;
  knowledge_results?: AgentWebResultItem[];
  count?: number;
  total_matches?: number;
  result_count?: number;
  faq_question?: string;
  fetched_chunks?: number;
  total_chunks?: number;
  steps?: AgentPlanStep[];
  task?: string;
  thought?: string;
  thought_number?: number;
  total_thoughts?: number;
  query?: string | string[];
  queries?: string[];
  pattern?: string;
  patterns?: string[];
  command?: string;
  skill_name?: string;
  script_path?: string;
  args?: unknown[];
  found_kbs?: Record<string, string | string[]>;
  [key: string]: unknown;
}

export interface AgentAttachedReasoning {
  id: string;
  content: string;
}

export interface AgentTimelineEvent {
  type: string;
  event_id?: string;
  tool_call_id?: string;
  tool_name?: string;
  display_type?: DisplayType | string;
  pending?: boolean;
  success?: boolean;
  done?: boolean;
  superseded?: boolean;
  thinking?: boolean;
  title?: string;
  content?: string;
  summary?: string;
  output?: string;
  error?: string;
  arguments?: Record<string, unknown>;
  tool_data?: AgentToolData;
  reasoning?: AgentAttachedReasoning;
  task?: CollaborationTaskView & string;
  progress_history?: string[];
  command_output?: ShellCommandProgress;
  tokens_before?: number;
  tokens_after?: number;
  degraded?: boolean;
  total_duration_ms?: number;
  timestamp?: number | string;
  is_fallback?: boolean;
  truncated?: boolean;
  _mergedContent?: string;
  _promoted_from_thinking?: boolean;

  // HITL / Prompt / Approval event fields
  pending_id?: string;
  delegated_task?: string;
  kind?: string;
  service_id?: string;
  service_name?: string;
  mcp_tool_name?: string;
  description?: string;
  args_json?: string;
  grant_scope?: string;
  grant_lapsed?: string;
  timeout_seconds?: number;
  hot_wait_seconds?: number;
  requested_at?: number;
  resolved?: boolean;
  approved?: boolean;
  authorized?: boolean;
  answered?: boolean;
  canceled?: boolean;
  timed_out?: boolean;
  abandoned?: boolean;
  suspendable?: boolean;
  suspended?: boolean;
  expires_at?: string;
  resolve_reason?: string;
  resolve_scope?: HostApprovalScope;
  request?: AskUserQuestion & SecureFormRequest;
  response?: Record<string, unknown>;
  host?: HostApprovalPayload;
  [key: string]: unknown;
}

export interface IndexedSessionArtifact extends SessionArtifact {
  index: number;
  deleted_at?: string;
}

export interface AgentSessionData {
  id?: string;
  assistant_message_id?: string;
  request_id?: string;
  debugRequest?: Record<string, unknown>;
  isAgentMode?: boolean;
  is_completed?: boolean;
  steerForked?: boolean;
  run_state?: string;
  persistence_error?: boolean;
  collaborationStatus?: unknown;
  used_memories?: UsedMemory[];
  artifacts?: SessionArtifact[];
  agentEventStream?: AgentTimelineEvent[];
  knowledge_references?: KnowledgeReferenceLike[];
  [key: string]: unknown;
}

export interface EmbedAuthProps {
  embeddedMode?: boolean;
  embedChannelId?: string;
  embedToken?: string;
  embedSessionId?: string;
  embedSessionSig?: string;
  embedVisitorId?: string;
}

export interface PlanStatusItem {
  icon: string;
  class: string;
  label: string;
  count: number;
}

export type WailsWindow = Window & {
  runtime?: {
    BrowserOpenURL?: (url: string) => void;
  };
};
