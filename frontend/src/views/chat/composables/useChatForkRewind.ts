import { MessagePlugin } from 'tdesign-vue-next';
import { computed, ref, type ComputedRef, type Ref } from 'vue';
import type { Router } from 'vue-router';
import { forkSession, rewindSession } from '@/api/chat/index';
import { useMenuStore } from '@/stores/menu';
import { persistedAssistantId } from '@/utils/steerStreamFork';
import { resolveForkAffordance } from '../forkPoint';
import { rewindSkipMessage } from '../rewindNotice';
import {
  canReplaceRewindTranscript,
  keepMessagesThroughRewindPoint,
  rewindBlockedByOutgoingWork,
  rewindConflictI18nKey,
  rewindHistoryHasMore,
  rewindHttpConflictCode,
  rewindPrefillText,
  rewindableMessageIds,
  shouldApplyRewindLocally,
} from '../rewindView';
import type {
  ChatMessageRow,
  ChatSessionDetail,
  InputFieldExposed,
  SteerQueueItem,
} from '../types/chatView';
import type { TranslateFn } from './useAgentToolPresentation';

const FORK_PREFILL_KEY = 'genyee:fork-prefill';

function isConflictError(err: unknown): boolean {
  if (!err || typeof err !== 'object') return false;
  const record = err as { status?: unknown; $httpStatus?: unknown };
  return record.status === 409 || record.$httpStatus === 409;
}

export interface UseChatForkRewindOptions {
  sessionIdRef: Ref<string>;
  embeddedMode: () => boolean;
  isAutomationSession: ComputedRef<boolean>;
  currentSession: Ref<ChatSessionDetail | null>;
  messagesList: ChatMessageRow[];
  steerQueue: Ref<SteerQueueItem[]>;
  inputFieldRef: Ref<InputFieldExposed | undefined>;
  isReplying: Ref<boolean>;
  isStreaming: Ref<boolean>;
  isImRecovering: Ref<boolean>;
  historyLoading: Ref<boolean>;
  hasMoreHistory: Ref<boolean>;
  createdAtRef: Ref<string>;
  limitRef: Ref<number>;
  fetchMessageList: (query: { session_id: string; created_at: string; limit: number }) => Promise<{ data?: ChatMessageRow[] }>;
  handleMsgList: (batch: ChatMessageRow[], isScrollType?: boolean, scrollHeight?: number) => Promise<void>;
  router: Router;
  t: TranslateFn;
}

export function useChatForkRewind(options: UseChatForkRewindOptions) {
  const {
    sessionIdRef,
    embeddedMode,
    isAutomationSession,
    currentSession,
    messagesList,
    steerQueue,
    inputFieldRef,
    isReplying,
    isStreaming,
    isImRecovering,
    historyLoading,
    hasMoreHistory,
    createdAtRef,
    limitRef,
    fetchMessageList,
    handleMsgList,
    router,
    t,
  } = options;

  const menuStore = useMenuStore();

  let forkInFlight = false;
  const rewindInFlight = ref(false);
  const rewindLockSessionId = ref('');

  const outgoingWorkBlocksRewind = computed(() =>
    rewindBlockedByOutgoingWork({
      isReplying: isReplying.value,
      isStreaming: isStreaming.value,
      isRecovering: isImRecovering.value,
    }),
  );

  const rewindableIds = computed(() =>
    rewindableMessageIds(messagesList, {
      embeddedMode: embeddedMode(),
      outgoingWork: outgoingWorkBlocksRewind.value,
    }),
  );

  function forkAffordanceOf(messageId?: string) {
    if (isAutomationSession.value || !messageId) return { canFork: false };
    return resolveForkAffordance(messagesList, messageId);
  }

  function canRewindMessage(messageId?: string): boolean {
    return (
      !isAutomationSession.value &&
      Boolean(messageId) &&
      rewindableIds.value.has(String(messageId))
    );
  }

  const composerLocked = computed(
    () =>
      isAutomationSession.value ||
      (rewindInFlight.value && String(sessionIdRef.value || '') === rewindLockSessionId.value),
  );

  function stashForkLanding(sessionId: string, text: string) {
    const payload = JSON.stringify({ sessionId, text });
    try {
      sessionStorage.setItem(FORK_PREFILL_KEY, payload);
    } catch {
      // sessionStorage can throw in private mode; landing still navigates.
    }
  }

  function readForkLanding(): { sessionId: string; text: string } | null {
    try {
      const raw = sessionStorage.getItem(FORK_PREFILL_KEY);
      if (!raw) return null;
      const parsed: unknown = JSON.parse(raw);
      if (!parsed || typeof parsed !== 'object') return null;
      const obj = parsed as { sessionId?: unknown; text?: unknown };
      return {
        sessionId: String(obj.sessionId || ''),
        text: String(obj.text || ''),
      };
    } catch {
      return null;
    }
  }

  function clearForkLanding() {
    try {
      sessionStorage.removeItem(FORK_PREFILL_KEY);
    } catch {
      // ignore
    }
  }

  function applyForkLanding(): boolean {
    const landed = readForkLanding();
    if (!landed || landed.sessionId !== String(sessionIdRef.value || '')) {
      return false;
    }
    clearForkLanding();
    inputFieldRef.value?.prefill?.(landed.text);
    return true;
  }

  async function handleFork(messageId: string) {
    if (embeddedMode()) return;
    if (forkInFlight || composerLocked.value) return;
    if (!messageId || !sessionIdRef.value) return;
    const source = messagesList.find((m) => m.id === messageId);
    if (!source) return;
    const sourceSessionId = sessionIdRef.value;

    forkInFlight = true;
    try {
      const res = await forkSession(sourceSessionId, { message_id: messageId });
      const data = (res as { data?: { session_id?: string } } | undefined)?.data;
      if (!data?.session_id) return;

      const prefill = source.role === 'user' ? String(source.content ?? '') : '';
      stashForkLanding(data.session_id, prefill);

      const now = new Date().toISOString();
      const sourceTitle = currentSession.value?.title || t('menu.newSession');
      menuStore.updataMenuChildren({
        id: data.session_id,
        path: `chat/${data.session_id}`,
        title: t('chat.forkedSessionTitle', { title: sourceTitle }),
        parent_session_id: sourceSessionId,
        isMore: false,
        isNoTitle: false,
        created_at: now,
        updated_at: now,
      });

      await router.push(`/platform/chat/${data.session_id}`);
    } catch (err: unknown) {
      if (isConflictError(err)) {
        MessagePlugin.warning(t('chat.forkWaitForTurnEnd'));
        return;
      }
      MessagePlugin.error(t('chat.forkFailed'));
    } finally {
      forkInFlight = false;
    }
  }

  async function handleRewind(messageId: string) {
    if (embeddedMode()) return;
    if (forkInFlight || composerLocked.value) return;
    if (
      rewindBlockedByOutgoingWork({
        isReplying: isReplying.value,
        isStreaming: isStreaming.value,
        isRecovering: isImRecovering.value,
      })
    ) {
      return;
    }
    if (!messageId || !sessionIdRef.value) return;
    const source = messagesList.find(
      (m) => m.id === messageId || persistedAssistantId(m) === messageId,
    );
    if (!source) return;
    const sourceSessionId = sessionIdRef.value;
    const sourceRole = source.role === 'user' ? 'user' : 'assistant';
    const sourceContent = source.content;

    rewindInFlight.value = true;
    rewindLockSessionId.value = sourceSessionId;
    try {
      const res = await rewindSession(sourceSessionId, { message_id: messageId });
      const data = (
        res as { data?: { workspace_reset?: boolean; reason?: string } } | undefined
      )?.data;
      if (!data) return;
      if (!shouldApplyRewindLocally(String(sessionIdRef.value || ''), sourceSessionId)) return;

      let batch: ChatMessageRow[] = [];
      let reloadFailed = false;
      try {
        const history = await fetchMessageList({
          session_id: sourceSessionId,
          created_at: '',
          limit: limitRef.value,
        });
        if (!Array.isArray(history?.data)) {
          throw new Error('rewind history reload returned no list');
        }
        batch = history.data;
      } catch {
        reloadFailed = true;
      }
      if (!shouldApplyRewindLocally(String(sessionIdRef.value || ''), sourceSessionId)) return;

      steerQueue.value = [];
      historyLoading.value = false;
      if (reloadFailed) {
        const kept = keepMessagesThroughRewindPoint(
          [...messagesList],
          messageId,
          sourceRole,
          (m) => m.id === messageId || persistedAssistantId(m) === messageId,
        );
        messagesList.splice(0, messagesList.length, ...kept);
        MessagePlugin.warning(t('chat.rewind.reloadFailed'));
      } else {
        if (!canReplaceRewindTranscript(String(sessionIdRef.value || ''), sourceSessionId, undefined)) {
          return;
        }
        messagesList.splice(0);
        createdAtRef.value = '';
        if (batch.length) {
          createdAtRef.value = batch[0].created_at || '';
          hasMoreHistory.value = rewindHistoryHasMore(batch.length, limitRef.value);
          await handleMsgList(batch, false);
        } else {
          hasMoreHistory.value = false;
        }
      }

      const prefill = rewindPrefillText(sourceRole, sourceContent);
      if (prefill) {
        inputFieldRef.value?.prefill?.(prefill);
      }

      if (reloadFailed) return;
      if (data.workspace_reset) {
        MessagePlugin.success(t('chat.rewind.success'));
        return;
      }
      const skip = rewindSkipMessage(String(data.reason || ''), t);
      if (skip) {
        MessagePlugin.info(skip);
      }
    } catch (err: unknown) {
      const conflictCode = rewindHttpConflictCode(err);
      if (conflictCode || isConflictError(err)) {
        MessagePlugin.warning(t(rewindConflictI18nKey(conflictCode)));
        return;
      }
      MessagePlugin.error(t('chat.rewind.failed'));
    } finally {
      rewindInFlight.value = false;
      rewindLockSessionId.value = '';
    }
  }

  return {
    forkAffordanceOf,
    canRewindMessage,
    composerLocked,
    applyForkLanding,
    handleFork,
    handleRewind,
  };
}
