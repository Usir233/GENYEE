import { MessagePlugin } from 'tdesign-vue-next';
import { ref, type ComputedRef, type Ref } from 'vue';
import { getMessageList } from '@/api/chat/index';
import { listSteerSession, promoteSteerSession, removeSteerSession, steerSession } from '@/api/chat/steer';
import { isAwaitingInput } from '@/composables/useChatStreamHandler';
import { makeSteerClientId } from '@/utils/steerId';
import {
  discardSteerPreview,
  previewSteerMessage,
  reconcileSteerMessageId,
} from '@/utils/steerStreamFork';
import type { ChatMessageRow, MentionedItemInput, SteerQueueItem } from '../types/chatView';
import type { TranslateFn } from './useAgentToolPresentation';

function getErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const record = error as { status?: unknown; $httpStatus?: unknown };
  if (typeof record.status === 'number') return record.status;
  if (typeof record.$httpStatus === 'number') return record.$httpStatus;
  return undefined;
}

function getErrorMessage(error: unknown): string | undefined {
  if (error instanceof Error && error.message) return error.message;
  if (error && typeof error === 'object' && 'message' in error) {
    const msg = (error as { message?: unknown }).message;
    if (typeof msg === 'string' && msg) return msg;
  }
  return undefined;
}

export interface UseChatSteerQueueOptions {
  sessionIdRef: Ref<string>;
  getLoadVersion: () => number;
  messagesList: ChatMessageRow[];
  isReplying: Ref<boolean>;
  isStreaming: Ref<boolean>;
  loading: Ref<boolean>;
  currentAssistantMessageId: Ref<string>;
  composerLocked: ComputedRef<boolean> | Ref<boolean>;
  scrollToBottom: (force?: boolean) => void;
  sendMsg: (value: string, modelId?: string, mentionedItems?: MentionedItemInput[]) => Promise<void>;
  startStream: (params: Record<string, unknown>) => Promise<void>;
  t: TranslateFn;
}

export function useChatSteerQueue(options: UseChatSteerQueueOptions) {
  const {
    sessionIdRef,
    getLoadVersion,
    messagesList,
    isReplying,
    isStreaming,
    loading,
    currentAssistantMessageId,
    composerLocked,
    scrollToBottom,
    sendMsg,
    startStream,
    t,
  } = options;

  const steerQueue = ref<SteerQueueItem[]>([]);

  const hydrateSteerQueue = async ({ onlyWhenLive = false } = {}) => {
    const sid = sessionIdRef.value;
    const version = getLoadVersion();
    if (!sid) return;
    try {
      const res = await listSteerSession(sid);
      if (version !== getLoadVersion() || sid !== sessionIdRef.value) return;
      if (onlyWhenLive && !res?.assistant_message_id) return;
      const items = Array.isArray(res?.items) ? res.items : [];
      const mapped: SteerQueueItem[] = items.map((item) => ({
        steer_id: item.steer_id,
        content: item.content || '',
        delivery: item.delivery === 'inject' ? 'inject' : 'after',
        mentioned_items: (item.mentioned_items as MentionedItemInput[] | undefined) || [],
        expected_assistant_message_id: res.assistant_message_id,
      }));
      steerQueue.value = mapped.concat(
        steerQueue.value.filter(
          (item) => item.failed && !items.some((remote) => remote.steer_id === item.steer_id),
        ),
      );
      for (const item of steerQueue.value) {
        if (item.delivery === 'inject') previewSteerMessage(messagesList, item);
      }
    } catch (e: unknown) {
      if (version !== getLoadVersion() || sid !== sessionIdRef.value) return;
      console.warn('[Steer] Failed to restore queue:', e);
    }
  };

  const handleStopConfirmed = () => {
    for (const item of steerQueue.value) discardSteerPreview(messagesList, item.steer_id);
    steerQueue.value = [];
  };

  const handleStopFailed = () => {
    isReplying.value = true;
    loading.value = true;
  };

  const dropSteerQueueItem = (steerId?: string) => {
    if (!steerId) return;
    const idx = steerQueue.value.findIndex((item) => item.steer_id === steerId);
    if (idx >= 0) steerQueue.value.splice(idx, 1);
  };

  const findSteerQueueItem = (steerId?: string): SteerQueueItem | undefined =>
    steerId ? steerQueue.value.find((item) => item.steer_id === steerId) : undefined;

  const handleSteerMsg = async (
    value: string,
    mentionedItems: MentionedItemInput[] = [],
    delivery: 'after' | 'inject' = 'after',
    retryId = '',
  ) => {
    if (composerLocked.value) return;
    if (!sessionIdRef.value || !value?.trim()) return;
    if (!isReplying.value && !retryId) {
      await sendMsg(value, '', mentionedItems);
      return;
    }
    const requestSessionId = sessionIdRef.value;
    const clientId = retryId || makeSteerClientId();
    const retryItem = retryId ? findSteerQueueItem(retryId) : undefined;
    const expectedId = retryItem?.expected_assistant_message_id || currentAssistantMessageId.value;
    if (retryItem) {
      retryItem.pending = true;
      retryItem.failed = false;
    } else {
      steerQueue.value.push({
        steer_id: clientId,
        client_id: clientId,
        expected_assistant_message_id: expectedId,
        content: value,
        delivery,
        mentioned_items: mentionedItems,
        pending: true,
      });
    }
    if (delivery === 'inject') {
      const queuedEntry = findSteerQueueItem(clientId);
      if (queuedEntry) {
        const preview = previewSteerMessage(messagesList, queuedEntry) as ChatMessageRow;
        delete preview._steerFailed;
        scrollToBottom(true);
      }
    }
    try {
      const res = await steerSession(
        requestSessionId,
        value,
        mentionedItems,
        delivery,
        expectedId,
        clientId,
      );
      if (sessionIdRef.value !== requestSessionId) return;
      const serverId = res?.steer_id || clientId;
      const received = reconcileSteerMessageId(messagesList, clientId, serverId) as ChatMessageRow | null;
      const queued = findSteerQueueItem(clientId);
      if (received && !received._steerPending) {
        dropSteerQueueItem(clientId);
      } else if (queued) {
        queued.steer_id = serverId;
      }
      if (res?.status === 'already_injected') {
        dropSteerQueueItem(serverId);
        const preview = messagesList.find((m) => m.steer_id === serverId);
        if (preview) delete preview._steerPending;
        MessagePlugin.info(t('input.messages.steerAlreadyInjected'));
        return;
      }
      if (res?.status === 'new_run') {
        const item = findSteerQueueItem(serverId);
        if (isReplying.value || isStreaming.value) {
          if (item) {
            item.pending = false;
            item.awaitingIdleSend = true;
          }
          return;
        }
        dropSteerQueueItem(serverId);
        discardSteerPreview(messagesList, serverId);
        await sendMsg(value, '', mentionedItems);
        return;
      }
      const item = findSteerQueueItem(serverId);
      if (item) {
        item.pending = false;
      }
    } catch (e: unknown) {
      console.error('[Steer] Failed to queue message:', e);
      if (sessionIdRef.value !== requestSessionId) return;
      const item = findSteerQueueItem(clientId);
      if (!item) return;
      item.pending = false;
      item.failed = true;
      const preview = messagesList.find((m) => m.steer_id === clientId && m._steerPending);
      if (preview) preview._steerFailed = true;
      if (getErrorStatus(e) === 409) {
        item.expected_assistant_message_id = currentAssistantMessageId.value;
      }
      MessagePlugin.error(getErrorMessage(e) || t('input.messages.steerFailed'));
    }
  };

  const handleRetrySteer = async (steerId?: string) => {
    const item = findSteerQueueItem(steerId);
    if (!item || item.pending) return;
    await handleSteerMsg(item.content, item.mentioned_items || [], item.delivery, steerId);
  };

  const handlePromoteSteer = async (steerId?: string) => {
    if (!sessionIdRef.value || !steerId) return;
    const item =
      findSteerQueueItem(steerId) ||
      steerQueue.value.find((entry) => entry.client_id === steerId);
    if (!item || item.delivery === 'inject') return;
    if (item.pending || item.promoting || item.failed) return;
    const requestSessionId = sessionIdRef.value;
    item.promoting = true;
    item.delivery = 'inject';
    previewSteerMessage(messagesList, item);
    scrollToBottom(true);
    try {
      const res = await promoteSteerSession(requestSessionId, item.steer_id);
      if (sessionIdRef.value !== requestSessionId) return;
      if (res?.status === 'already_injected') {
        dropSteerQueueItem(item.steer_id);
        const preview = messagesList.find((m) => m.steer_id === item.steer_id);
        if (preview) delete preview._steerPending;
        MessagePlugin.info(t('input.messages.steerAlreadyInjected'));
        return;
      }
      if (res?.status === 'new_run') {
        if (isReplying.value || isStreaming.value) {
          item.awaitingIdleSend = true;
          return;
        }
        const content = item.content;
        const mentions = item.mentioned_items || [];
        dropSteerQueueItem(steerId);
        discardSteerPreview(messagesList, steerId);
        await sendMsg(content, '', mentions);
        return;
      }
      item.delivery = 'inject';
    } catch (e: unknown) {
      console.error('[Steer] Failed to promote queued message:', e);
      if (sessionIdRef.value !== requestSessionId) return;
      if (!findSteerQueueItem(steerId)) return;
      item.delivery = 'after';
      discardSteerPreview(messagesList, steerId);
      MessagePlugin.error(getErrorMessage(e) || t('input.messages.steerPromoteFailed'));
    } finally {
      item.promoting = false;
    }
  };

  const handleRemoveSteer = async (steerId?: string) => {
    if (!steerId) return;
    const item = findSteerQueueItem(steerId);
    if (!item || item.pending) return;
    item.promoting = true;
    try {
      if (sessionIdRef.value) {
        const res = await removeSteerSession(sessionIdRef.value, item.steer_id);
        if (res?.status === 'already_injected') {
          MessagePlugin.info(t('input.messages.steerAlreadyInjected'));
          dropSteerQueueItem(steerId);
          return;
        }
        if (res?.status === 'gone') {
          discardSteerPreview(messagesList, steerId);
          dropSteerQueueItem(steerId);
          return;
        }
        if (res && res.removed === false && !item.failed) {
          MessagePlugin.error(t('input.messages.steerRemoveFailed'));
          return;
        }
      }
      discardSteerPreview(messagesList, steerId);
      dropSteerQueueItem(steerId);
    } catch (e: unknown) {
      console.error('[Steer] Failed to remove queued message:', e);
      MessagePlugin.error(getErrorMessage(e) || t('input.messages.steerRemoveFailed'));
    } finally {
      if (findSteerQueueItem(steerId)) item.promoting = false;
    }
  };

  let attachingSteerFollowUp = false;

  const attachSteerFollowUp = async (completedAssistantId?: string) => {
    const queued = steerQueue.value.filter((item) => !item.failed);
    if (!queued.length || attachingSteerFollowUp || !sessionIdRef.value) return;
    const sessionId = sessionIdRef.value;
    const version = getLoadVersion();
    attachingSteerFollowUp = true;
    isReplying.value = true;
    loading.value = true;
    let attached = false;
    let attachedAssistantId = '';
    const sessionChanged = () => version !== getLoadVersion() || sessionIdRef.value !== sessionId;
    try {
      for (let attempt = 0; attempt < 40; attempt++) {
        if (sessionChanged()) return;
        const res = await getMessageList({ session_id: sessionId, limit: 30, created_at: '' });
        if (sessionChanged()) return;
        const batch = ((res as { data?: ChatMessageRow[] } | undefined)?.data) || [];
        const newAssistant = batch.findLast(
          (m) =>
            m.role === 'assistant' &&
            !m.is_completed &&
            !isAwaitingInput(m) &&
            Boolean(m.id) &&
            m.id !== completedAssistantId,
        );
        if (newAssistant && newAssistant.id) {
          const claimed = new Set<string>();
          for (const persisted of batch) {
            if (persisted.role !== 'user' || !persisted.id) continue;
            if (persisted.request_id !== newAssistant.request_id) continue;
            if (messagesList.some((existing) => existing.id === persisted.id)) continue;
            const queuedMatch = queued.find(
              (q) => q.content === persisted.content && !claimed.has(q.steer_id),
            );
            if (queuedMatch) claimed.add(queuedMatch.steer_id);
            const userRow: ChatMessageRow = {
              ...persisted,
              mentioned_items: queuedMatch?.mentioned_items?.length
                ? queuedMatch.mentioned_items
                : persisted.mentioned_items,
            };
            const preview =
              queuedMatch &&
              messagesList.find((m) => m.steer_id === queuedMatch.steer_id && m._steerPending);
            if (preview) {
              delete preview._steerPending;
              delete preview._steerFailed;
              delete preview.isSteer;
              Object.assign(preview, userRow);
            } else {
              messagesList.push(userRow);
            }
          }
          for (const steerId of claimed) dropSteerQueueItem(steerId);
          if (sessionChanged()) return;
          await hydrateSteerQueue({ onlyWhenLive: true });
          if (sessionChanged()) return;

          currentAssistantMessageId.value = newAssistant.id;
          attachedAssistantId = newAssistant.id;
          await startStream({
            session_id: sessionId,
            query: newAssistant.id,
            method: 'GET',
            url: '/api/v1/sessions/continue-stream',
          });
          attached = true;
          return;
        }
        await new Promise((r) => setTimeout(r, 200));
      }
    } catch (e: unknown) {
      console.error('[Steer] Failed to attach follow-up run:', e);
    } finally {
      attachingSteerFollowUp = false;
      if (sessionChanged()) {
        // Session changed; leave the active chat's loading state untouched.
      } else if (!attached) {
        loading.value = false;
        isReplying.value = false;
        MessagePlugin.error(t('input.messages.steerFollowUpTimeout'));
      } else if (steerQueue.value.some((item) => !item.failed)) {
        void attachSteerFollowUp(attachedAssistantId);
      }
    }
  };

  const flushSteerAfterTurn = async (completedAssistantId?: string) => {
    const awaiting = steerQueue.value.filter((item) => item.awaitingIdleSend);
    if (awaiting.length) {
      const batch = awaiting.slice();
      for (const item of batch) discardSteerPreview(messagesList, item.steer_id);
      steerQueue.value = steerQueue.value.filter((item) => !item.awaitingIdleSend);
      const first = batch[0];
      await sendMsg(first.content, '', first.mentioned_items || []);
      for (const rest of batch.slice(1)) {
        await handleSteerMsg(rest.content, rest.mentioned_items || [], rest.delivery || 'after');
      }
      return;
    }
    void attachSteerFollowUp(completedAssistantId);
  };

  return {
    steerQueue,
    hydrateSteerQueue,
    handleStopConfirmed,
    handleStopFailed,
    dropSteerQueueItem,
    findSteerQueueItem,
    handleSteerMsg,
    handleRetrySteer,
    handlePromoteSteer,
    handleRemoveSteer,
    flushSteerAfterTurn,
    attachSteerFollowUp,
  };
}
