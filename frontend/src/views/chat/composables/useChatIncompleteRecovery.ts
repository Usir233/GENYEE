import { MessagePlugin } from 'tdesign-vue-next';
import { ref, type Ref } from 'vue';
import { getMessageList } from '@/api/chat/index';
import { isPromptCard } from '@/composables/useChatStreamHandler';
import { persistedAssistantId } from '@/utils/steerStreamFork';
import type { ChatMessageRow } from '../types/chatView';
import type { TranslateFn } from './useAgentToolPresentation';

const RECOVER_POLL_INTERVAL = 2500;
const RECOVER_POLL_MAX_ATTEMPTS = 48; // ~2 min

export interface UseChatIncompleteRecoveryOptions {
  sessionIdRef: Ref<string>;
  getLoadVersion: () => number;
  limitRef: Ref<number>;
  createdAtRef: Ref<string>;
  messagesList: ChatMessageRow[];
  isReplying: Ref<boolean>;
  loading: Ref<boolean>;
  currentAssistantMessageId: Ref<string>;
  streamError: Ref<string | null>;
  findLastMessage: (predicate: (msg: ChatMessageRow) => boolean) => ChatMessageRow | undefined;
  prepareResumedMessage: (msg: ChatMessageRow, pendingId: string) => void;
  startStream: (params: Record<string, unknown>) => Promise<void>;
  reloadThread: (query: { session_id: string; limit: number; created_at: string }) => void;
  t: TranslateFn;
}

export function useChatIncompleteRecovery(options: UseChatIncompleteRecoveryOptions) {
  const {
    sessionIdRef,
    getLoadVersion,
    limitRef,
    createdAtRef,
    messagesList,
    isReplying,
    loading,
    currentAssistantMessageId,
    streamError,
    findLastMessage,
    prepareResumedMessage,
    startStream,
    reloadThread,
    t,
  } = options;

  const isAttachingImStream = ref(false);
  const isImRecovering = ref(false);
  let recoverPollTimer: ReturnType<typeof setTimeout> | null = null;
  let attachingResumedRun = false;

  const stopRecoverPoll = () => {
    if (recoverPollTimer) {
      clearTimeout(recoverPollTimer);
      recoverPollTimer = null;
    }
    isImRecovering.value = false;
  };

  const recoverIncompleteMessage = () => {
    const targetSession = sessionIdRef.value;
    const targetMessageId = currentAssistantMessageId.value;
    if (recoverPollTimer) {
      clearTimeout(recoverPollTimer);
      recoverPollTimer = null;
    }
    if (!targetMessageId) {
      isReplying.value = false;
      isImRecovering.value = false;
      return;
    }
    isImRecovering.value = true;
    let attempts = 0;

    const poll = async () => {
      recoverPollTimer = null;
      if (sessionIdRef.value !== targetSession) {
        isReplying.value = false;
        isImRecovering.value = false;
        return;
      }
      attempts++;
      try {
        const res = await getMessageList({
          session_id: targetSession,
          limit: limitRef.value,
          created_at: '',
        });
        const rows = ((res as { data?: ChatMessageRow[] } | undefined)?.data) || [];
        const target = rows.find((m) => m.id === targetMessageId);
        if (target && target.is_completed) {
          createdAtRef.value = '';
          messagesList.splice(0);
          reloadThread({ session_id: targetSession, limit: limitRef.value, created_at: '' });
          isReplying.value = false;
          isImRecovering.value = false;
          currentAssistantMessageId.value = '';
          return;
        }
      } catch (e: unknown) {
        console.warn('[Continue Stream] recovery poll failed:', e);
      }
      if (attempts >= RECOVER_POLL_MAX_ATTEMPTS) {
        MessagePlugin.error(t('error.streamFailed'));
        isReplying.value = false;
        isImRecovering.value = false;
        currentAssistantMessageId.value = '';
        return;
      }
      recoverPollTimer = setTimeout(poll, RECOVER_POLL_INTERVAL);
    };

    recoverPollTimer = setTimeout(poll, RECOVER_POLL_INTERVAL);
  };

  const resumeSuspendedRun = async (pendingId: string) => {
    const sessionId = sessionIdRef.value;
    const version = getLoadVersion();
    const message = findLastMessage(
      (m) =>
        m.role === 'assistant' &&
        (m.agentEventStream || []).some((e) => isPromptCard(e) && e.pending_id === pendingId),
    );
    if (!message || !sessionId || isReplying.value) return;
    const resumeId = persistedAssistantId(message);
    prepareResumedMessage(message, pendingId);
    isReplying.value = true;
    currentAssistantMessageId.value = resumeId;
    attachingResumedRun = true;
    try {
      for (let attempt = 0; attempt < 60; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 500));
        if (version !== getLoadVersion() || sessionIdRef.value !== sessionId) return;
        await startStream({
          session_id: sessionId,
          query: resumeId,
          method: 'GET',
          url: '/api/v1/sessions/continue-stream',
          resume_pending_id: pendingId,
        });
        if (!streamError.value || version !== getLoadVersion()) return;
      }
    } finally {
      attachingResumedRun = false;
    }
    if (streamError.value && version === getLoadVersion() && sessionIdRef.value === sessionId) {
      MessagePlugin.error(streamError.value);
      isReplying.value = false;
      loading.value = false;
      currentAssistantMessageId.value = '';
    }
  };

  const isAttachingResumedRun = () => attachingResumedRun;

  return {
    isAttachingImStream,
    isImRecovering,
    stopRecoverPoll,
    recoverIncompleteMessage,
    resumeSuspendedRun,
    isAttachingResumedRun,
  };
}
