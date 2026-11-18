<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import {
  deleteGeneratedQuestion,
  getChunkByIdOnly,
  listChunkRevisions,
  regenerateGeneratedQuestions,
  revertDocumentChunk,
  updateDocumentChunk,
  upsertGeneratedQuestion,
} from '@/api/knowledge-base/index';
import { diffWikiLines, type WikiDiffLine } from '@/utils/wikiLineDiff';
import {
  extractErrorMessage,
  extractErrorStatus,
  type ChunkRevisionEntry,
  type GeneratedQuestion,
  type KnowledgeChunkItem,
  type ProcessedChunkView,
} from './types';

interface ChunkMutationResponse {
  data?: Partial<KnowledgeChunkItem>;
  summary_status?: string;
  description?: string;
}

interface ChunkRevisionsResponse {
  data?: ChunkRevisionEntry[];
}

interface QuestionUpsertResponse {
  data?: GeneratedQuestion;
}

interface QuestionRegenerateResponse {
  data?: GeneratedQuestion[];
}

interface ParentChunkResponse {
  success?: boolean;
  data?: { content?: string };
}

type ChunkDiffLine = WikiDiffLine | { type: 'skip'; text: string };

const props = defineProps<{
  detailsId?: string;
  processedChunks: ProcessedChunkView[];
  loadedChunkPage: number;
  chunkPageSize: number;
  canEditContent: boolean;
  canDeleteGeneratedQuestion: boolean;
  processMarkdown: (text?: string) => string;
}>();

const emit = defineEmits<{
  reloadChunks: [];
  questionDeleted: [payload: { chunkId: string; questionId: string }];
  summaryStateUpdate: [payload: { summaryStatus?: string; description?: string }];
  requestPostRender: [];
}>();

const { t } = useI18n();

const editingChunkId = ref('');
const chunkDraft = ref('');
const savingChunkId = ref('');
const chunkStatusLoading = ref('');

const chunkHistoryPopup = ref('');
const chunkHistoryLoading = ref('');
const chunkHistories = ref<Record<string, ChunkRevisionEntry[]>>({});
const selectedChunkRevision = ref<Record<string, number | null>>({});
const chunkHistoryRequestSequence = ref<Record<string, number>>({});
const revertingRevision = ref('');

const questionDrafts = ref<Record<string, string>>({});
const savingQuestionChunk = ref('');
const regeneratingQuestionChunk = ref('');
const questionPopupChunk = ref('');
const questionComposerChunk = ref('');
const editingQuestionKey = ref('');
const questionEditDraft = ref('');
const savingQuestionKey = ref('');
const deletingQuestion = ref<{ chunkIndex: number; questionId: string } | null>(null);

const parentContextPopup = ref('');
const parentContextCache = ref<Map<string, string>>(new Map());
const parentContextLoading = ref<Set<number>>(new Set());

watch(
  () => props.detailsId,
  () => {
    editingChunkId.value = '';
    chunkDraft.value = '';
    chunkHistoryPopup.value = '';
    chunkHistoryLoading.value = '';
    chunkHistories.value = {};
    selectedChunkRevision.value = {};
    chunkHistoryRequestSequence.value = {};
    parentContextPopup.value = '';
    questionPopupChunk.value = '';
    questionComposerChunk.value = '';
    editingQuestionKey.value = '';
  },
);

const getChunkMetadata = (item: KnowledgeChunkItem): Record<string, unknown> =>
  typeof item.metadata === 'string'
    ? (JSON.parse(item.metadata || '{}') as Record<string, unknown>)
    : { ...(item.metadata || {}) };

const setChunkMetadata = (item: KnowledgeChunkItem, metadata: Record<string, unknown>): void => {
  item.metadata = metadata;
};

const hasStaleGeneratedQuestions = (item: KnowledgeChunkItem, questions: GeneratedQuestion[]): boolean => {
  if (!questions.length) return false;
  try {
    const metadata = getChunkMetadata(item);
    const fallbackRevision = Number(metadata.generated_questions_revision || 0);
    const currentRevision = item.content_revision || 0;
    return questions.some((question) => (question.content_revision ?? fallbackRevision) !== currentRevision);
  } catch {
    return false;
  }
};

const upsertChunkGeneratedQuestion = (item: KnowledgeChunkItem, questionData: GeneratedQuestion): void => {
  const metadata = getChunkMetadata(item);
  const existing = Array.isArray(metadata.generated_questions)
    ? (metadata.generated_questions as Array<string | GeneratedQuestion>)
    : [];
  const questions: GeneratedQuestion[] = existing.map((q, index) =>
    typeof q === 'string' ? { id: `legacy-${index}`, question: q } : q,
  );
  const idx = questions.findIndex((q) => q.id === questionData.id);
  if (idx >= 0) {
    questions[idx] = questionData;
  } else {
    questions.push(questionData);
  }
  metadata.generated_questions = questions;
  setChunkMetadata(item, metadata);
};

const notifyChunkMutationOutcome = (
  item: KnowledgeChunkItem,
  result: ChunkMutationResponse,
  successMessage?: string,
): void => {
  if (result.data) {
    Object.assign(item, result.data);
  }
  emit('summaryStateUpdate', {
    summaryStatus: result.summary_status,
    description: result.description,
  });
  if (item.index_status === 'failed') {
    MessagePlugin.warning(t('knowledgeBase.chunkSavedIndexFailed'));
    return;
  }
  if (successMessage) {
    MessagePlugin.success(successMessage);
  }
};

const handleChunkEditError = (error: unknown, fallbackMessage: string): void => {
  if (extractErrorStatus(error) === 409) {
    MessagePlugin.warning(extractErrorMessage(error, t('knowledgeBase.chunkEditConflict')));
    editingChunkId.value = '';
    chunkDraft.value = '';
    emit('reloadChunks');
    return;
  }
  MessagePlugin.error(extractErrorMessage(error, fallbackMessage));
};

const startChunkEdit = (item: KnowledgeChunkItem): void => {
  if (editingChunkId.value === item.id) {
    editingChunkId.value = '';
    chunkDraft.value = '';
    return;
  }
  editingChunkId.value = item.id;
  chunkDraft.value = item.content || '';
};

const loadChunkHistory = async (item: KnowledgeChunkItem, force = false, silent = false): Promise<void> => {
  if (!props.detailsId) return;
  if (!force && Object.prototype.hasOwnProperty.call(chunkHistories.value, item.id)) return;

  const requestSequence = (chunkHistoryRequestSequence.value[item.id] || 0) + 1;
  chunkHistoryRequestSequence.value[item.id] = requestSequence;
  chunkHistoryLoading.value = item.id;
  try {
    const result = (await listChunkRevisions(props.detailsId, item.id)) as ChunkRevisionsResponse;
    if (chunkHistoryRequestSequence.value[item.id] === requestSequence) {
      chunkHistories.value[item.id] = result?.data || [];
    }
  } catch (error: unknown) {
    if (!silent) MessagePlugin.error(extractErrorMessage(error, t('common.error')));
  } finally {
    if (chunkHistoryRequestSequence.value[item.id] === requestSequence) {
      chunkHistoryLoading.value = '';
    }
  }
};

const refreshChunkHistoryAfterMutation = async (item: KnowledgeChunkItem): Promise<void> => {
  const wasLoaded = Object.prototype.hasOwnProperty.call(chunkHistories.value, item.id);
  const shouldReload = wasLoaded || chunkHistoryPopup.value === item.id;
  chunkHistoryRequestSequence.value[item.id] = (chunkHistoryRequestSequence.value[item.id] || 0) + 1;
  delete chunkHistories.value[item.id];
  selectedChunkRevision.value[item.id] = null;
  if (shouldReload) {
    await loadChunkHistory(item, true, true);
  } else if (chunkHistoryLoading.value === item.id) {
    chunkHistoryLoading.value = '';
  }
};

const saveChunkEdit = async (item: KnowledgeChunkItem): Promise<void> => {
  if (!props.detailsId) return;
  if (!chunkDraft.value.trim()) {
    MessagePlugin.warning(t('knowledgeBase.chunkContentRequired'));
    return;
  }
  savingChunkId.value = item.id;
  try {
    const result = (await updateDocumentChunk(props.detailsId, item.id, {
      content: chunkDraft.value,
      expected_revision: item.content_revision || 0,
    })) as ChunkMutationResponse;
    editingChunkId.value = '';
    notifyChunkMutationOutcome(item, result, t('common.saveSuccess'));
    void refreshChunkHistoryAfterMutation(item);
  } catch (error: unknown) {
    handleChunkEditError(error, t('common.saveFailed'));
  } finally {
    savingChunkId.value = '';
  }
};

const toggleChunkEnabled = async (item: KnowledgeChunkItem, isEnabled: boolean): Promise<void> => {
  if (!props.detailsId) return;
  chunkStatusLoading.value = item.id;
  try {
    const result = (await updateDocumentChunk(props.detailsId, item.id, {
      is_enabled: isEnabled,
      expected_revision: item.content_revision || 0,
    })) as ChunkMutationResponse;
    notifyChunkMutationOutcome(item, result);
    void refreshChunkHistoryAfterMutation(item);
  } catch (error: unknown) {
    handleChunkEditError(error, t('common.error'));
  } finally {
    chunkStatusLoading.value = '';
  }
};

const retryChunkIndex = async (item: KnowledgeChunkItem): Promise<void> => {
  if (!props.detailsId) return;
  try {
    const result = (await updateDocumentChunk(props.detailsId, item.id, {
      expected_revision: item.content_revision || 0,
    })) as ChunkMutationResponse;
    if (result.data) {
      Object.assign(item, result.data);
    }
    if (item.index_status === 'failed') throw new Error(t('knowledgeBase.indexFailed'));
    MessagePlugin.success(t('knowledgeBase.indexRetrySuccess'));
  } catch (error: unknown) {
    MessagePlugin.error(extractErrorMessage(error, t('common.error')));
  }
};

const setChunkHistoryPopupVisible = (item: KnowledgeChunkItem, visible: boolean): void => {
  chunkHistoryPopup.value = visible ? item.id : '';
  if (visible) {
    parentContextPopup.value = '';
    questionPopupChunk.value = '';
    void loadChunkHistory(item);
  }
};

const selectChunkRevision = (item: KnowledgeChunkItem, revision: number): void => {
  selectedChunkRevision.value[item.id] =
    selectedChunkRevision.value[item.id] === revision ? null : revision;
};

const getSelectedChunkRevision = (item: KnowledgeChunkItem): ChunkRevisionEntry | null => {
  const revision = selectedChunkRevision.value[item.id];
  return (chunkHistories.value[item.id] || []).find((entry) => entry.revision === revision) || null;
};

const compactChunkDiff = (item: KnowledgeChunkItem): ChunkDiffLine[] => {
  const revision = getSelectedChunkRevision(item);
  if (!revision) return [];
  const lines = diffWikiLines(revision.content || '', item.content || '');
  const changed = new Set<number>();
  lines.forEach((line, index) => {
    if (line.type !== 'same') {
      for (let i = Math.max(0, index - 2); i <= Math.min(lines.length - 1, index + 2); i++) {
        changed.add(i);
      }
    }
  });
  if (!changed.size) return [];

  const compact: ChunkDiffLine[] = [];
  let lastIndex = -2;
  Array.from(changed)
    .toSorted((a, b) => a - b)
    .forEach((index) => {
      if (index > lastIndex + 1) compact.push({ type: 'skip', text: '…' });
      compact.push(lines[index]);
      lastIndex = index;
    });
  return compact;
};

const diffLinePrefix = (type: ChunkDiffLine['type']): string => {
  if (type === 'add') return '+ ';
  if (type === 'del') return '- ';
  return type === 'same' ? '  ' : '';
};

const revisionStatusChanged = (item: KnowledgeChunkItem, revisionIndex: number): boolean => {
  const revisions = chunkHistories.value[item.id] || [];
  const newerEnabled = revisionIndex === 0 ? item.is_enabled : revisions[revisionIndex - 1]?.is_enabled;
  return revisions[revisionIndex]?.is_enabled !== newerEnabled;
};

const revertChunk = async (item: KnowledgeChunkItem, revision: number): Promise<void> => {
  if (!props.detailsId) return;
  revertingRevision.value = `${item.id}:${revision}`;
  try {
    const result = (await revertDocumentChunk(
      props.detailsId,
      item.id,
      revision,
      item.content_revision || 0,
    )) as ChunkMutationResponse;
    notifyChunkMutationOutcome(item, result, t('knowledgeBase.chunkReverted'));
    await refreshChunkHistoryAfterMutation(item);
  } catch (error: unknown) {
    handleChunkEditError(error, t('common.error'));
  } finally {
    revertingRevision.value = '';
  }
};

const closeQuestionComposer = (item: KnowledgeChunkItem): void => {
  questionComposerChunk.value = '';
  questionDrafts.value[item.id] = '';
};

const cancelQuestionEdit = (): void => {
  editingQuestionKey.value = '';
  questionEditDraft.value = '';
};

const setQuestionPopupVisible = (item: KnowledgeChunkItem, visible: boolean): void => {
  questionPopupChunk.value = visible ? item.id : '';
  if (visible) {
    parentContextPopup.value = '';
    chunkHistoryPopup.value = '';
  } else {
    closeQuestionComposer(item);
    cancelQuestionEdit();
  }
};

const openQuestionComposer = (item: KnowledgeChunkItem): void => {
  questionPopupChunk.value = item.id;
  questionComposerChunk.value = item.id;
};

const addQuestion = async (item: KnowledgeChunkItem): Promise<void> => {
  const question = (questionDrafts.value[item.id] || '').trim();
  if (!question) return;
  savingQuestionChunk.value = item.id;
  try {
    const result = (await upsertGeneratedQuestion(item.id, question)) as QuestionUpsertResponse;
    questionDrafts.value[item.id] = '';
    questionComposerChunk.value = '';
    if (result.data) {
      upsertChunkGeneratedQuestion(item, result.data);
    }
    MessagePlugin.success(t('common.saveSuccess'));
  } catch (error: unknown) {
    MessagePlugin.error(extractErrorMessage(error, t('common.error')));
  } finally {
    savingQuestionChunk.value = '';
  }
};

const startQuestionEdit = (item: KnowledgeChunkItem, question: GeneratedQuestion): void => {
  editingQuestionKey.value = `${item.id}:${question.id}`;
  questionEditDraft.value = question.question;
};

const saveQuestionEdit = async (item: KnowledgeChunkItem, question: GeneratedQuestion): Promise<void> => {
  const value = questionEditDraft.value.trim();
  if (!value) return;
  if (value === question.question) {
    cancelQuestionEdit();
    return;
  }
  savingQuestionKey.value = `${item.id}:${question.id}`;
  try {
    const result = (await upsertGeneratedQuestion(item.id, value, question.id)) as QuestionUpsertResponse;
    if (result?.data) {
      upsertChunkGeneratedQuestion(item, result.data);
    }
    cancelQuestionEdit();
    MessagePlugin.success(t('common.saveSuccess'));
  } catch (error: unknown) {
    MessagePlugin.error(extractErrorMessage(error, t('common.error')));
  } finally {
    savingQuestionKey.value = '';
  }
};

const regenerateQuestions = async (item: KnowledgeChunkItem): Promise<void> => {
  regeneratingQuestionChunk.value = item.id;
  try {
    const result = (await regenerateGeneratedQuestions(item.id)) as QuestionRegenerateResponse;
    const metadata = getChunkMetadata(item);
    metadata.generated_questions = result?.data || [];
    metadata.generated_questions_revision = item.content_revision || 0;
    setChunkMetadata(item, metadata);
    questionComposerChunk.value = '';
    MessagePlugin.success(t('knowledgeBase.questionsRegenerated'));
  } catch (error: unknown) {
    MessagePlugin.error(extractErrorMessage(error, t('common.error')));
  } finally {
    regeneratingQuestionChunk.value = '';
  }
};

const handleDeleteQuestion = async (
  item: KnowledgeChunkItem,
  chunkIndex: number,
  question: GeneratedQuestion,
): Promise<void> => {
  if (!item || !item.id) {
    MessagePlugin.error(t('common.error'));
    return;
  }
  if (question.id.startsWith('legacy-')) {
    MessagePlugin.warning(t('knowledgeBase.legacyQuestionCannotDelete'));
    return;
  }

  deletingQuestion.value = { chunkIndex, questionId: question.id };
  try {
    await deleteGeneratedQuestion(item.id, question.id);
    MessagePlugin.success(t('common.deleteSuccess'));

    const metadata = getChunkMetadata(item);
    if (Array.isArray(metadata.generated_questions)) {
      const questions = metadata.generated_questions as GeneratedQuestion[];
      const idx = questions.findIndex((q) => q.id === question.id);
      if (idx > -1) {
        questions.splice(idx, 1);
      }
      item.metadata = typeof item.metadata === 'string' ? JSON.stringify(metadata) : metadata;
    }

    emit('questionDeleted', { chunkId: item.id, questionId: question.id });
  } catch (error: unknown) {
    MessagePlugin.error(extractErrorMessage(error, t('common.deleteFailed')));
  } finally {
    deletingQuestion.value = null;
  }
};

const isDeleting = (chunkIndex: number, questionId: string): boolean =>
  deletingQuestion.value?.chunkIndex === chunkIndex && deletingQuestion.value?.questionId === questionId;

const loadParentContext = async (item: KnowledgeChunkItem, index: number): Promise<void> => {
  const parentId = item.parent_chunk_id;
  if (!parentId) return;
  if (!parentContextCache.value.has(parentId)) {
    parentContextLoading.value.add(index);
    parentContextLoading.value = new Set(parentContextLoading.value);
    try {
      const result = (await getChunkByIdOnly(parentId)) as ParentChunkResponse;
      if (result.success && result.data) {
        parentContextCache.value.set(parentId, result.data.content || '');
        parentContextCache.value = new Map(parentContextCache.value);
      }
    } catch {
      MessagePlugin.error(t('knowledgeBase.parentContextLoadFailed'));
      parentContextPopup.value = '';
      return;
    } finally {
      parentContextLoading.value.delete(index);
      parentContextLoading.value = new Set(parentContextLoading.value);
    }
  }

  await nextTick();
  emit('requestPostRender');
};

const setParentContextPopupVisible = (
  item: KnowledgeChunkItem,
  index: number,
  visible: boolean,
): void => {
  parentContextPopup.value = visible ? item.id : '';
  if (visible) {
    questionPopupChunk.value = '';
    chunkHistoryPopup.value = '';
    void loadParentContext(item, index);
  }
};

const getParentContent = (item: KnowledgeChunkItem): string =>
  (item.parent_chunk_id && parentContextCache.value.get(item.parent_chunk_id)) || '';
</script>

<template>
  <div v-if="!processedChunks.length" class="no_content">{{ $t('common.noData') }}</div>
  <div v-else class="chunk-list">
    <div
      v-for="(chunk, index) in processedChunks"
      :key="chunk.original.id || index"
      class="chunk-item"
      :class="{ 'chunk-item--disabled': !chunk.original.is_enabled }"
    >
      <div class="chunk-header">
        <div class="chunk-heading">
          <span class="chunk-index">
            {{ $t('knowledgeBase.segment') }} {{ (loadedChunkPage - 1) * chunkPageSize + index + 1 }}
          </span>
          <span class="chunk-meta">{{ chunk.meta }}</span>
        </div>
        <div class="chunk-header-right">
          <t-tooltip
            v-if="chunk.original.index_status === 'failed' && canEditContent"
            :content="$t('knowledgeBase.retryIndex')"
            placement="top"
          >
            <t-button
              class="icon-action-btn"
              size="small"
              theme="danger"
              variant="text"
              shape="square"
              :aria-label="$t('knowledgeBase.retryIndex')"
              @click="retryChunkIndex(chunk.original)"
            >
              <template #icon><t-icon name="refresh" size="15px" /></template>
            </t-button>
          </t-tooltip>

          <t-popup
            v-if="chunk.hasParent"
            :visible="parentContextPopup === chunk.original.id"
            trigger="click"
            placement="bottom-right"
            :show-arrow="true"
            destroy-on-close
            :overlay-inner-style="{ padding: 0 }"
            overlay-class-name="chunk-context-popup-overlay"
            @visible-change="(visible: boolean) => setParentContextPopupVisible(chunk.original, index, visible)"
          >
            <t-button
              class="icon-action-btn"
              :class="{ 'is-active': parentContextPopup === chunk.original.id }"
              size="small"
              variant="text"
              shape="square"
              :title="$t('knowledgeBase.viewParentContext')"
              :aria-label="$t('knowledgeBase.viewParentContext')"
            >
              <template #icon><t-icon name="git-branch" size="15px" /></template>
            </t-button>
            <template #content>
              <div class="chunk-context-popup" @click.stop>
                <div class="chunk-popup-head">
                  <div class="chunk-popup-title">
                    <t-icon name="git-branch" size="15px" />
                    <span>{{ $t('knowledgeBase.viewParentContext') }}</span>
                  </div>
                </div>
                <div v-if="parentContextLoading.has(index)" class="chunk-popup-state">
                  <t-loading size="small" />
                  <span>{{ $t('common.loading') }}</span>
                </div>
                <div
                  v-else
                  class="chunk-context-popup-body md-content"
                  v-html="processMarkdown(getParentContent(chunk.original))"
                />
              </div>
            </template>
          </t-popup>

          <t-popup
            v-if="chunk.questions.length > 0 || canEditContent"
            :visible="questionPopupChunk === chunk.original.id"
            trigger="click"
            placement="bottom-right"
            :show-arrow="true"
            destroy-on-close
            :overlay-inner-style="{ padding: 0 }"
            overlay-class-name="chunk-questions-popup-overlay"
            @visible-change="(visible: boolean) => setQuestionPopupVisible(chunk.original, visible)"
          >
            <t-button
              class="icon-action-btn chunk-question-entry"
              :class="{ 'is-active': questionPopupChunk === chunk.original.id }"
              size="small"
              variant="text"
              shape="square"
              :title="$t('knowledgeBase.generatedQuestions')"
              :aria-label="$t('knowledgeBase.generatedQuestions')"
            >
              <template #icon><t-icon name="help-circle" size="15px" /></template>
            </t-button>
            <template #content>
              <div class="chunk-questions-popup" @click.stop>
                <div class="chunk-popup-head">
                  <div class="chunk-popup-title">
                    <t-icon name="help-circle" size="15px" />
                    <span>{{ $t('knowledgeBase.generatedQuestions') }}</span>
                    <span class="chunk-popup-count">{{ chunk.questions.length }}</span>
                    <span
                      v-if="hasStaleGeneratedQuestions(chunk.original, chunk.questions)"
                      class="chunk-question-stale-hint"
                    >
                      {{ $t('knowledgeBase.staleGeneratedQuestions') }}
                    </span>
                  </div>
                  <div v-if="canEditContent" class="chunk-popup-actions">
                    <t-tooltip :content="$t('knowledgeBase.addGeneratedQuestion')" placement="top">
                      <t-button
                        class="icon-action-btn"
                        size="small"
                        variant="text"
                        shape="square"
                        :aria-label="$t('knowledgeBase.addGeneratedQuestion')"
                        @click.stop="openQuestionComposer(chunk.original)"
                      >
                        <template #icon><t-icon name="add" size="15px" /></template>
                      </t-button>
                    </t-tooltip>
                    <t-tooltip :content="$t('knowledgeBase.regenerateQuestions')" placement="top">
                      <t-button
                        class="icon-action-btn"
                        size="small"
                        variant="text"
                        shape="square"
                        :aria-label="$t('knowledgeBase.regenerateQuestions')"
                        :loading="regeneratingQuestionChunk === chunk.original.id"
                        @click.stop="regenerateQuestions(chunk.original)"
                      >
                        <template #icon><t-icon name="refresh" size="15px" /></template>
                      </t-button>
                    </t-tooltip>
                  </div>
                </div>
                <div class="chunk-questions-popup-body">
                  <div
                    v-if="canEditContent && questionComposerChunk === chunk.original.id"
                    class="question-composer"
                  >
                    <t-input
                      v-model="questionDrafts[chunk.original.id]"
                      autofocus
                      :placeholder="$t('knowledgeBase.addGeneratedQuestion')"
                      @enter="addQuestion(chunk.original)"
                    />
                    <t-tooltip :content="$t('common.cancel')" placement="top">
                      <t-button
                        class="icon-action-btn"
                        size="small"
                        variant="text"
                        shape="square"
                        :aria-label="$t('common.cancel')"
                        :disabled="savingQuestionChunk === chunk.original.id"
                        @click="closeQuestionComposer(chunk.original)"
                      >
                        <template #icon><t-icon name="close" size="14px" /></template>
                      </t-button>
                    </t-tooltip>
                    <t-button
                      size="small"
                      theme="primary"
                      :loading="savingQuestionChunk === chunk.original.id"
                      :disabled="!(questionDrafts[chunk.original.id] || '').trim()"
                      @click="addQuestion(chunk.original)"
                    >
                      {{ $t('common.add') }}
                    </t-button>
                  </div>
                  <div v-if="chunk.questions.length" class="questions-list">
                    <div v-for="question in chunk.questions" :key="question.id" class="question-item">
                      <span class="question-leading-icon"><t-icon name="help-circle" size="14px" /></span>
                      <div
                        v-if="editingQuestionKey === `${chunk.original.id}:${question.id}`"
                        class="question-inline-editor"
                      >
                        <t-input
                          v-model="questionEditDraft"
                          autofocus
                          @enter="saveQuestionEdit(chunk.original, question)"
                        />
                        <t-button size="small" variant="text" @click="cancelQuestionEdit">
                          {{ $t('common.cancel') }}
                        </t-button>
                        <t-button
                          size="small"
                          theme="primary"
                          :loading="savingQuestionKey === `${chunk.original.id}:${question.id}`"
                          @click="saveQuestionEdit(chunk.original, question)"
                        >
                          {{ $t('common.save') }}
                        </t-button>
                      </div>
                      <template v-else>
                        <span class="question-text">{{ question.question }}</span>
                        <div class="question-actions">
                          <t-tooltip
                            v-if="canEditContent && !question.id.startsWith('legacy-')"
                            :content="$t('common.edit')"
                            placement="top"
                          >
                            <t-button
                              class="icon-action-btn"
                              theme="default"
                              variant="text"
                              shape="square"
                              size="small"
                              :aria-label="$t('common.edit')"
                              @click.stop="startQuestionEdit(chunk.original, question)"
                            >
                              <template #icon><t-icon name="edit" size="14px" /></template>
                            </t-button>
                          </t-tooltip>
                          <t-popconfirm
                            v-if="canDeleteGeneratedQuestion && !question.id.startsWith('legacy-')"
                            theme="warning"
                            :content="$t('knowledgeBase.confirmDeleteQuestion')"
                            :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                            @confirm="handleDeleteQuestion(chunk.original, index, question)"
                          >
                            <t-button
                              class="icon-action-btn delete-question-btn"
                              theme="default"
                              variant="text"
                              shape="square"
                              size="small"
                              :aria-label="$t('common.delete')"
                              :loading="isDeleting(index, question.id)"
                            >
                              <template #icon><t-icon name="delete" size="14px" /></template>
                            </t-button>
                          </t-popconfirm>
                        </div>
                      </template>
                    </div>
                  </div>
                  <div v-else-if="questionComposerChunk !== chunk.original.id" class="questions-empty">
                    <t-icon name="chat-bubble-help" size="20px" />
                    <span>{{ $t('knowledgeBase.noGeneratedQuestions') }}</span>
                  </div>
                </div>
              </div>
            </template>
          </t-popup>

          <template v-if="canEditContent">
            <t-tooltip :content="$t('common.edit')" placement="top">
              <t-button
                class="icon-action-btn"
                :class="{ 'is-active': editingChunkId === chunk.original.id }"
                size="small"
                variant="text"
                shape="square"
                :aria-label="$t('common.edit')"
                @click="startChunkEdit(chunk.original)"
              >
                <template #icon><t-icon name="edit" size="15px" /></template>
              </t-button>
            </t-tooltip>
            <t-popup
              :visible="chunkHistoryPopup === chunk.original.id"
              trigger="click"
              placement="bottom-right"
              :show-arrow="true"
              destroy-on-close
              :overlay-inner-style="{ padding: 0 }"
              overlay-class-name="chunk-history-popup-overlay"
              @visible-change="(visible: boolean) => setChunkHistoryPopupVisible(chunk.original, visible)"
            >
              <t-button
                class="icon-action-btn"
                :class="{ 'is-active': chunkHistoryPopup === chunk.original.id }"
                size="small"
                variant="text"
                shape="square"
                :title="$t('knowledgeBase.chunkHistory')"
                :aria-label="$t('knowledgeBase.chunkHistory')"
              >
                <template #icon><t-icon name="history" size="15px" /></template>
              </t-button>
              <template #content>
                <div class="chunk-history-popup" @click.stop>
                  <div class="chunk-history-popup-head">
                    <div>
                      <div class="chunk-history-popup-title">
                        <t-icon name="history" size="15px" />
                        <span>{{ $t('knowledgeBase.chunkHistory') }}</span>
                      </div>
                      <div class="chunk-history-current">
                        v{{ chunk.original.content_revision || 0 }} · {{ $t('knowledgeBase.currentVersion') }} ·
                        {{
                          chunk.original.is_enabled
                            ? $t('knowledgeBase.enabledStatus')
                            : $t('knowledgeBase.disabledStatus')
                        }}
                      </div>
                    </div>
                    <div class="chunk-history-diff-legend">
                      <span class="chunk-history-diff-legend-item chunk-history-diff-legend-item--add">
                        <i />{{ $t('knowledgeBase.diffAddedInCurrent') }}
                      </span>
                      <span class="chunk-history-diff-legend-item chunk-history-diff-legend-item--del">
                        <i />{{ $t('knowledgeBase.diffRemovedFromCurrent') }}
                      </span>
                    </div>
                  </div>
                  <div v-if="chunkHistoryLoading === chunk.original.id" class="chunk-history-popup-state">
                    <t-loading size="small" />
                    <span>{{ $t('common.loading') }}</span>
                  </div>
                  <div
                    v-else-if="!(chunkHistories[chunk.original.id] || []).length"
                    class="chunk-history-popup-state"
                  >
                    {{ $t('knowledgeBase.noChunkHistory') }}
                  </div>
                  <div v-else class="chunk-history-popup-list">
                    <div
                      v-for="(revision, revisionIndex) in chunkHistories[chunk.original.id]"
                      :key="revision.id"
                      class="chunk-history-popup-item"
                      :class="{ 'is-selected': selectedChunkRevision[chunk.original.id] === revision.revision }"
                    >
                      <button
                        type="button"
                        class="chunk-history-version-row"
                        @click="selectChunkRevision(chunk.original, revision.revision)"
                      >
                        <span class="chunk-history-version">v{{ revision.revision }}</span>
                        <span class="chunk-history-time">{{ new Date(revision.edited_at).toLocaleString() }}</span>
                        <span
                          v-if="revisionStatusChanged(chunk.original, revisionIndex)"
                          class="chunk-history-status-change"
                        >
                          <t-icon :name="revision.is_enabled ? 'play-circle' : 'stop-circle'" size="13px" />
                          {{
                            revision.is_enabled
                              ? $t('knowledgeBase.enabledStatus')
                              : $t('knowledgeBase.disabledStatus')
                          }}
                        </span>
                        <t-icon
                          :name="
                            selectedChunkRevision[chunk.original.id] === revision.revision
                              ? 'chevron-up'
                              : 'chevron-down'
                          "
                          size="14px"
                          class="chunk-history-row-chevron"
                        />
                      </button>
                      <div
                        v-if="selectedChunkRevision[chunk.original.id] === revision.revision"
                        class="chunk-history-diff"
                      >
                        <div class="chunk-history-diff-head">
                          <span>{{
                            $t('knowledgeBase.compareRevisionWithCurrent', {
                              revision: revision.revision,
                              current: chunk.original.content_revision || 0,
                            })
                          }}</span>
                          <t-popconfirm
                            theme="warning"
                            :content="$t('knowledgeBase.revertRevisionConfirm', { revision: revision.revision })"
                            @confirm="revertChunk(chunk.original, revision.revision)"
                          >
                            <t-button
                              class="icon-action-btn"
                              size="small"
                              variant="text"
                              shape="square"
                              :title="$t('knowledgeBase.revertRevision')"
                              :aria-label="$t('knowledgeBase.revertRevision')"
                              :loading="revertingRevision === `${chunk.original.id}:${revision.revision}`"
                            >
                              <template #icon><t-icon name="rollback" size="14px" /></template>
                            </t-button>
                          </t-popconfirm>
                        </div>
                        <div v-if="!compactChunkDiff(chunk.original).length" class="chunk-history-no-diff">
                          {{ $t('knowledgeBase.noContentChanges') }}
                        </div>
                        <pre v-else class="chunk-history-diff-body"><span
                          v-for="(line, lineIndex) in compactChunkDiff(chunk.original)"
                          :key="lineIndex"
                          :class="['chunk-history-diff-line', `chunk-history-diff-line--${line.type}`]"
                        >{{ diffLinePrefix(line.type) }}{{ line.text }}
</span></pre>
                      </div>
                    </div>
                  </div>
                </div>
              </template>
            </t-popup>
            <span class="chunk-toolbar-divider" />
            <t-tooltip
              :content="
                chunk.original.is_enabled
                  ? $t('knowledgeBase.disableChunk')
                  : $t('knowledgeBase.enableChunk')
              "
              placement="top"
            >
              <t-switch
                :key="`${chunk.original.id}-${chunk.original.is_enabled}`"
                size="small"
                :value="Boolean(chunk.original.is_enabled)"
                :loading="chunkStatusLoading === chunk.original.id"
                :disabled="chunkStatusLoading === chunk.original.id"
                @change="(value: unknown) => toggleChunkEnabled(chunk.original, Boolean(value))"
              />
            </t-tooltip>
          </template>
        </div>
      </div>

      <div v-if="editingChunkId === chunk.original.id" class="chunk-editor">
        <div class="chunk-editor-label">
          <t-icon name="edit" size="14px" />
          <span>{{ $t('knowledgeBase.editChunkContent') }}</span>
        </div>
        <t-textarea v-model="chunkDraft" :autosize="{ minRows: 6, maxRows: 20 }" autofocus />
        <div class="chunk-editor-actions">
          <t-button
            size="small"
            variant="outline"
            :disabled="savingChunkId === chunk.original.id"
            @click="editingChunkId = ''"
          >
            {{ $t('common.cancel') }}
          </t-button>
          <t-button
            size="small"
            theme="primary"
            :loading="savingChunkId === chunk.original.id"
            @click="saveChunkEdit(chunk.original)"
          >
            {{ $t('common.save') }}
          </t-button>
        </div>
      </div>
      <div
        v-else
        class="md-content"
        :class="{ 'chunk-disabled': !chunk.original.is_enabled }"
        v-html="chunk.processedContent"
      />
    </div>
  </div>
</template>

<style scoped lang="less">
@import "../css/markdown.less";
@import "./doc-chunk-list.less";
</style>
