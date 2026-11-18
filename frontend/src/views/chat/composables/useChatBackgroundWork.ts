import { computed, ref, watch, type Ref } from 'vue';
import {
  cancelBackgroundWork,
  listBackgroundWork,
  type BackgroundWorkItem,
} from '@/api/chat/background-work';
import { createBackgroundWorkPoller, mergeBackgroundWork } from '@/utils/backgroundWork';
import type { ChatMessageRow } from '../types/chatView';

export function useChatBackgroundWork(
  sessionIdRef: Ref<string>,
  messagesList: ChatMessageRow[],
) {
  const backgroundItems = ref<BackgroundWorkItem[]>([]);
  const viewingWorkId = ref('');
  const workOutputVisible = ref(false);
  const viewingWorkTitle = computed(() => {
    const item = backgroundItems.value.find((row) => row.work_id === viewingWorkId.value);
    return item?.title || viewingWorkId.value || '';
  });

  function replaceBackgroundItems(incoming: BackgroundWorkItem[]) {
    const merged = mergeBackgroundWork(backgroundItems.value, incoming);
    const ids = new Set(incoming.map((item) => item.work_id));
    const next = merged.filter((item) => ids.has(item.work_id));
    if (
      next.length === backgroundItems.value.length &&
      next.every((item, index) => item === backgroundItems.value[index])
    ) {
      return;
    }
    backgroundItems.value = next;
  }

  const backgroundPoller = createBackgroundWorkPoller({
    sessionId: () => String(sessionIdRef.value || ''),
    list: (id, signal) => listBackgroundWork(id, signal),
    onUpdate: replaceBackgroundItems,
  });

  function startBackgroundWork() {
    if (!sessionIdRef.value) {
      backgroundItems.value = [];
      return;
    }
    backgroundPoller.start();
  }

  async function refreshBackgroundWork() {
    if (!sessionIdRef.value) return;
    await backgroundPoller.refresh();
  }

  function openWorkOutput(workId: string) {
    viewingWorkId.value = workId;
    workOutputVisible.value = true;
  }

  async function onCancelWork(workId: string) {
    const id = String(sessionIdRef.value || '');
    if (!id || !workId) return;
    try {
      await cancelBackgroundWork(id, workId);
    } catch {
      // Refresh anyway so a rejected stop can leave the row running.
    }
    try {
      if (String(sessionIdRef.value || '') === id) await refreshBackgroundWork();
    } catch {
      // Keep the last chip.
    }
  }

  watch(sessionIdRef, (id) => {
    backgroundPoller.stop();
    backgroundItems.value = [];
    viewingWorkId.value = '';
    workOutputVisible.value = false;
    if (id) backgroundPoller.start();
  });

  const backgroundWorkStamp = computed(() =>
    messagesList
      .map((message) => {
        const work = message?.background_work;
        if (!work || typeof work !== 'object') return '';
        return Object.entries(work)
          .map(([id, row]) => `${id}:${row?.state || ''}`)
          .join(',');
      })
      .join('|'),
  );

  watch(backgroundWorkStamp, (stamp, previous) => {
    if (!stamp || stamp === previous) return;
    refreshBackgroundWork().catch(() => {});
  });

  return {
    backgroundItems,
    viewingWorkId,
    workOutputVisible,
    viewingWorkTitle,
    backgroundPoller,
    startBackgroundWork,
    openWorkOutput,
    onCancelWork,
  };
}
