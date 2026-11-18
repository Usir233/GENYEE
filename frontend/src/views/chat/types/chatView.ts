import type { SuggestedQuestion } from '@/api/agent';
import type { AssistantAccessLevel } from '@/api/assistant';
import type { PageContextPayload } from '@/api/chat/stream';
import type { CollaborationStatus } from '@/api/chat/stream-types';
import type { MessageSuggestionQuestion, MessageSuggestionSet } from '@/api/message-suggestion';
import type { AssistantPageContext } from '@/composables/useAssistantPageContext';
import type { QuestionOrigin } from '@/utils/questionOrigin';
import type { KnowledgeReferenceLike } from '@/utils/referenceSources';
import type { SessionArtifact } from '@/utils/sessionArtifacts';
import type { AgentTimelineEvent } from './agentStream';

export interface MentionedItemInput {
  id: string;
  name: string;
  type: 'kb' | 'file' | 'tag' | 'mcp' | 'skill' | string;
  kb_type?: 'document' | 'faq' | string;
  kb_id?: string;
  kb_name?: string;
  service_id?: string;
  skill_name?: string;
  count?: number;
}

export interface AttachmentUploadItem {
  id?: string;
  documentId?: string;
  name: string;
  size: number;
  status?: 'pending' | 'uploading' | 'parsing' | 'ready' | 'failed' | string;
  file: File;
}

export interface MessageAttachmentDisplay {
  id?: string;
  file_name: string;
  file_size?: number;
  file_type?: string;
}

export interface MessageImageDisplay {
  url: string;
  caption?: string;
}

export interface ChatMessageRow {
  id?: string;
  assistant_message_id?: string;
  request_id?: string;
  role?: 'user' | 'assistant' | string;
  content?: string;
  created_at?: string;
  channel?: string;
  is_completed?: boolean;
  is_fallback?: boolean;
  truncated?: boolean;
  steerForked?: boolean;
  steer_id?: string;
  isSteer?: boolean;
  _steerPending?: boolean;
  _steerFailed?: boolean;
  collaborationStatus?: CollaborationStatus;
  collaboration_status?: CollaborationStatus;
  mentioned_items?: MentionedItemInput[];
  images?: MessageImageDisplay[];
  attachments?: MessageAttachmentDisplay[];
  artifacts?: SessionArtifact[];
  agentEventStream?: AgentTimelineEvent[];
  knowledge_references?: KnowledgeReferenceLike[];
  background_work?: Record<string, { state?: string }>;
  answerFullyRendered?: boolean;
  suggestionLoading?: boolean;
  suggestionSet?: MessageSuggestionSet | null;
  suggestionsDismissed?: boolean;
  debugRequest?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface ChatSessionDetail {
  id: string;
  title?: string;
  kind?: string;
  last_request_state?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface SteerQueueItem {
  steer_id: string;
  client_id?: string;
  expected_assistant_message_id?: string;
  content: string;
  delivery: 'after' | 'inject';
  mentioned_items?: MentionedItemInput[];
  pending?: boolean;
  promoting?: boolean;
  failed?: boolean;
  awaitingIdleSend?: boolean;
}

export interface SendMessageOptions {
  questionOrigin?: QuestionOrigin | null;
}

export interface HostScopeSnapshot {
  knowledgeBaseIds?: string[];
  pageContext?: PageContextPayload | AssistantPageContext | Record<string, unknown> | null;
  assistantAccess?: AssistantAccessLevel;
}

export interface InputFieldExposed {
  prefill?: (text: string) => void;
  triggerSend?: (text: string, options?: SendMessageOptions) => void;
  getComposerModelId?: () => string;
  focusInput?: () => void;
}

export type { MessageSuggestionQuestion, MessageSuggestionSet, SuggestedQuestion };
