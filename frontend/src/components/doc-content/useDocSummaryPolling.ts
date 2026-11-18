import { computed, onUnmounted, ref, watch, type ComputedRef } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  getKnowledgeDetails,
  regenerateKnowledgeSummary,
  updateKnowledgeMetadata,
  updateKnowledgeSummary,
} from '@/api/knowledge-base/index';
import {
  extractErrorMessage,
  type KnowledgeDocDetails,
  type MetadataDraftRow,
  type MetadataValueType,
  type SummaryStateChangePayload,
} from './types';

interface SummaryApiPayload {
  success?: boolean;
  data?: {
    summary_status?: string;
    description?: string;
  };
}

export const formatMetadataValue = (value: unknown): string => {
  if (value === null) return 'null';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  return String(value);
};

export const isSummaryStatusInFlight = (status?: string): boolean =>
  status === 'pending' || status === 'processing';

export function useDocSummaryPolling(options: {
  getDetails: () => KnowledgeDocDetails | undefined;
  isVisible: () => boolean;
  canEditContent: ComputedRef<boolean>;
  t: (key: string, params?: Record<string, unknown>) => string;
  onSummaryStateChange: (payload: SummaryStateChangePayload) => void;
}) {
  const { getDetails, isVisible, canEditContent, t, onSummaryStateChange } = options;

  let metadataRowSeed = 0;
  const metadataEditing = ref(false);
  const metadataDraft = ref<MetadataDraftRow[]>([]);
  const metadataSaving = ref(false);
  const summaryRefreshing = ref(false);
  const summaryEditing = ref(false);
  const summaryDraft = ref('');
  const summarySaving = ref(false);

  let summaryStatusPollTimer: ReturnType<typeof setTimeout> | null = null;
  let summaryStatusPollGeneration = 0;

  const metadataTypeOptions = computed(() => [
    { label: t('knowledgeBase.metadataTypeText'), value: 'text' },
    { label: t('knowledgeBase.metadataTypeNumber'), value: 'number' },
    { label: t('knowledgeBase.metadataTypeBoolean'), value: 'boolean' },
    { label: t('knowledgeBase.metadataTypeNull'), value: 'null' },
  ]);

  const summaryStatusRefreshing = computed(() =>
    isSummaryStatusInFlight(getDetails()?.summary_status),
  );
  const canEditSummary = computed(() => canEditContent.value && !summaryStatusRefreshing.value);

  const showSummarySection = computed(() => {
    const details = getDetails();
    return (
      Boolean(details?.description)
      || details?.summary_status === 'pending'
      || details?.summary_status === 'processing'
      || Boolean(details?.id && canEditContent.value)
    );
  });

  const makeMetadataRow = (key = '', value: unknown = ''): MetadataDraftRow => {
    let type: MetadataValueType = 'text';
    if (value === null) type = 'null';
    else if (typeof value === 'number') type = 'number';
    else if (typeof value === 'boolean') type = 'boolean';
    return {
      id: ++metadataRowSeed,
      key,
      value: value === null ? '' : String(value),
      type,
    };
  };

  const syncMetadataDraft = (): void => {
    metadataDraft.value = Object.entries(getDetails()?.custom_metadata || {}).map(
      ([key, value]) => makeMetadataRow(key, value),
    );
  };

  const startMetadataEdit = (): void => {
    syncMetadataDraft();
    if (!metadataDraft.value.length) metadataDraft.value.push(makeMetadataRow());
    metadataEditing.value = true;
  };

  const cancelMetadataEdit = (): void => {
    metadataEditing.value = false;
    syncMetadataDraft();
  };

  const addMetadataRow = (): void => {
    if (metadataDraft.value.length >= 20) return;
    metadataDraft.value.push(makeMetadataRow());
  };

  const removeMetadataRow = (id: number): void => {
    metadataDraft.value = metadataDraft.value.filter((row) => row.id !== id);
  };

  const updateMetadataRow = (
    id: number,
    patch: Partial<Pick<MetadataDraftRow, 'key' | 'value' | 'type'>>,
  ): void => {
    const target = metadataDraft.value.find((row) => row.id === id);
    if (!target) return;
    if (patch.key !== undefined) target.key = patch.key;
    if (patch.type !== undefined) {
      target.type = patch.type;
      if (patch.type === 'boolean' && target.value !== 'true' && target.value !== 'false') {
        target.value = 'true';
      } else if (patch.type === 'null') {
        target.value = '';
      }
    }
    if (patch.value !== undefined) target.value = patch.value;
  };

  const parseMetadataValue = (row: MetadataDraftRow): unknown => {
    if (row.type === 'null') return null;
    if (row.type === 'boolean') return row.value === 'true';
    if (row.type === 'number') {
      const value = Number(row.value);
      if (!row.value.trim() || !Number.isFinite(value)) {
        throw new Error(t('knowledgeBase.metadataNumberRequired', { key: row.key }));
      }
      return value;
    }
    return row.value;
  };

  const applySummaryState = (summaryStatus?: string, description?: string): void => {
    const details = getDetails();
    if (!details) return;
    if (typeof summaryStatus === 'string' && summaryStatus) {
      details.summary_status = summaryStatus;
    }
    if (typeof description === 'string') {
      details.description = description;
    }
    if (details.id) {
      onSummaryStateChange({
        id: details.id,
        summary_status: details.summary_status,
        description: details.description,
      });
    }
  };

  const saveMetadata = async (): Promise<void> => {
    const details = getDetails();
    if (!details?.id) return;
    try {
      const value: Record<string, unknown> = {};
      for (const row of metadataDraft.value) {
        const key = row.key.trim();
        if (!key) throw new Error(t('knowledgeBase.metadataKeyRequired'));
        if (Object.prototype.hasOwnProperty.call(value, key)) {
          throw new Error(t('knowledgeBase.metadataKeyDuplicate', { key }));
        }
        value[key] = parseMetadataValue(row);
      }
      metadataSaving.value = true;
      const result = (await updateKnowledgeMetadata(details.id, value)) as SummaryApiPayload;
      details.custom_metadata = value;
      metadataEditing.value = false;
      if (result?.data) {
        applySummaryState(result.data.summary_status, result.data.description);
      }
      MessagePlugin.success(t('common.saveSuccess'));
    } catch (error: unknown) {
      MessagePlugin.error(extractErrorMessage(error, t('common.saveFailed')));
    } finally {
      metadataSaving.value = false;
    }
  };

  const startSummaryEdit = (): void => {
    summaryDraft.value = getDetails()?.description || '';
    summaryEditing.value = true;
  };

  const cancelSummaryEdit = (): void => {
    summaryEditing.value = false;
    summaryDraft.value = '';
  };

  const saveSummary = async (): Promise<void> => {
    const details = getDetails();
    if (!details?.id) return;
    summarySaving.value = true;
    try {
      const description = summaryDraft.value.trim();
      const result = (await updateKnowledgeSummary(details.id, description)) as SummaryApiPayload;
      applySummaryState(result?.data?.summary_status, result?.data?.description ?? description);
      summaryEditing.value = false;
      MessagePlugin.success(t('common.saveSuccess'));
    } catch (error: unknown) {
      MessagePlugin.error(extractErrorMessage(error, t('common.saveFailed')));
    } finally {
      summarySaving.value = false;
    }
  };

  const refreshSummary = async (): Promise<void> => {
    const details = getDetails();
    if (!details?.id) return;
    summaryRefreshing.value = true;
    try {
      const result = (await regenerateKnowledgeSummary(details.id)) as SummaryApiPayload;
      if (result?.data) {
        applySummaryState(result.data.summary_status, result.data.description);
      }
      const status = result?.data?.summary_status;
      if (status === 'pending' || status === 'processing') {
        MessagePlugin.success(t('knowledgeBase.summaryRefreshQueued'));
      } else {
        MessagePlugin.success(t('knowledgeBase.summaryRefreshed'));
      }
    } catch (error: unknown) {
      MessagePlugin.error(extractErrorMessage(error, t('common.error')));
    } finally {
      summaryRefreshing.value = false;
    }
  };

  const stopSummaryStatusPolling = (): void => {
    summaryStatusPollGeneration++;
    if (summaryStatusPollTimer !== null) {
      clearTimeout(summaryStatusPollTimer);
      summaryStatusPollTimer = null;
    }
  };

  const scheduleSummaryStatusPoll = (): void => {
    const details = getDetails();
    if (
      summaryStatusPollTimer !== null
      || !isVisible()
      || !details?.id
      || !isSummaryStatusInFlight(details.summary_status)
    ) {
      return;
    }

    const knowledgeID = details.id;
    const generation = summaryStatusPollGeneration;
    summaryStatusPollTimer = setTimeout(async () => {
      summaryStatusPollTimer = null;
      const latestDetails = getDetails();
      if (generation !== summaryStatusPollGeneration || !isVisible() || latestDetails?.id !== knowledgeID) {
        return;
      }
      try {
        const result = (await getKnowledgeDetails(knowledgeID)) as SummaryApiPayload;
        if (generation !== summaryStatusPollGeneration || getDetails()?.id !== knowledgeID) return;
        if (result?.success && result.data) {
          applySummaryState(result.data.summary_status, result.data.description);
        }
      } catch {
        // Keep the current status visible and retry while the drawer remains open.
      }
      const current = getDetails();
      if (
        generation === summaryStatusPollGeneration
        && isVisible()
        && current?.id === knowledgeID
        && isSummaryStatusInFlight(current.summary_status)
      ) {
        scheduleSummaryStatusPoll();
      }
    }, 1500);
  };

  watch(
    () => [isVisible(), getDetails()?.id, getDetails()?.summary_status] as const,
    ([visible, knowledgeID, summaryStatus]) => {
      if (visible && knowledgeID && isSummaryStatusInFlight(summaryStatus)) {
        scheduleSummaryStatusPoll();
      } else {
        stopSummaryStatusPolling();
      }
      if (!visible) {
        cancelSummaryEdit();
      }
    },
    { immediate: true },
  );

  watch(
    () => getDetails()?.id,
    () => {
      metadataEditing.value = false;
      cancelSummaryEdit();
      syncMetadataDraft();
    },
    { immediate: true },
  );

  watch(summaryStatusRefreshing, (refreshing) => {
    if (refreshing) {
      cancelSummaryEdit();
    }
  });

  onUnmounted(() => {
    stopSummaryStatusPolling();
  });

  return {
    metadataEditing,
    metadataDraft,
    metadataSaving,
    metadataTypeOptions,
    startMetadataEdit,
    cancelMetadataEdit,
    addMetadataRow,
    removeMetadataRow,
    updateMetadataRow,
    saveMetadata,
    summaryRefreshing,
    summaryEditing,
    summaryDraft,
    summarySaving,
    summaryStatusRefreshing,
    canEditSummary,
    showSummarySection,
    startSummaryEdit,
    cancelSummaryEdit,
    saveSummary,
    refreshSummary,
    applySummaryState,
    stopSummaryStatusPolling,
  };
}
