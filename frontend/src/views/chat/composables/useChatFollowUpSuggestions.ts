import { ref, type ComputedRef, type Ref } from 'vue';
import { getSuggestedQuestions, type SuggestedQuestion } from '@/api/agent/index';
import {
  ensureMessageSuggestions,
  getMessageSuggestions,
  recordMessageSuggestionEvent,
  type MessageSuggestionEventType,
  type MessageSuggestionQuestion,
  type MessageSuggestionSet,
} from '@/api/message-suggestion';
import { isAwaitingInput } from '@/composables/useChatStreamHandler';
import { useSettingsStore } from '@/stores/settings';
import type { ChatMessageRow, InputFieldExposed } from '../types/chatView';

export interface SuggestionAttribution {
  suggestion_set_id: string;
  question_id: string;
}

export interface UseChatFollowUpSuggestionsOptions {
  sessionIdRef: Ref<string>;
  getLoadVersion: () => number;
  embeddedMode: () => boolean;
  isAutomationSession: ComputedRef<boolean>;
  historyLoading: Ref<boolean>;
  messagesList: ChatMessageRow[];
  inputFieldRef: Ref<InputFieldExposed | undefined>;
  sendMsg: (value: string) => Promise<void> | void;
}

export function useChatFollowUpSuggestions(options: UseChatFollowUpSuggestionsOptions) {
  const {
    sessionIdRef,
    getLoadVersion,
    embeddedMode,
    isAutomationSession,
    historyLoading,
    messagesList,
    inputFieldRef,
    sendMsg,
  } = options;

  const settingsStore = useSettingsStore();

  const suggestedQuestions = ref<SuggestedQuestion[]>([]);
  const suggestedQuestionsLoading = ref(false);
  let suggestedQuestionsFetchId = 0;
  let suggestedDebounceTimer: ReturnType<typeof setTimeout> | null = null;
  let pendingSuggestionAttribution: SuggestionAttribution | null = null;
  let pendingSuggestionKnowledgeBaseIds: string[] = [];

  const cancelSuggestedQuestionsFetch = () => {
    suggestedQuestionsFetchId++;
    suggestedQuestionsLoading.value = false;
    suggestedQuestions.value = [];
    if (suggestedDebounceTimer) {
      clearTimeout(suggestedDebounceTimer);
      suggestedDebounceTimer = null;
    }
  };

  const fetchSuggestedQuestions = async () => {
    if (historyLoading.value || messagesList.length > 0) {
      return;
    }
    const fetchId = ++suggestedQuestionsFetchId;
    suggestedQuestionsLoading.value = true;
    try {
      const agentId = settingsStore.selectedAgentId;
      if (!agentId) return;
      const res = await getSuggestedQuestions(agentId, settingsStore.getSuggestedQuestionsParams());
      if (fetchId === suggestedQuestionsFetchId) {
        suggestedQuestions.value = res?.data?.questions || [];
      }
    } catch (err: unknown) {
      console.warn('[SuggestedQuestions] Failed to fetch:', err);
      if (fetchId === suggestedQuestionsFetchId) {
        suggestedQuestions.value = [];
      }
    } finally {
      if (fetchId === suggestedQuestionsFetchId) {
        suggestedQuestionsLoading.value = false;
      }
    }
  };

  const fetchSuggestedQuestionsIfNeeded = async () => {
    if (embeddedMode()) return;
    if (historyLoading.value || messagesList.length > 0) {
      if (messagesList.length > 0) {
        cancelSuggestedQuestionsFetch();
      }
      return;
    }
    await fetchSuggestedQuestions();
  };

  const debouncedFetchSuggestions = () => {
    if (historyLoading.value || messagesList.length > 0) return;
    if (suggestedDebounceTimer) clearTimeout(suggestedDebounceTimer);
    suggestedDebounceTimer = setTimeout(() => {
      void fetchSuggestedQuestionsIfNeeded();
    }, 300);
  };

  const resolveAssistantMessageId = (message?: ChatMessageRow | null) =>
    message?.assistant_message_id || message?.id;

  const handleAnswerRenderComplete = (message: ChatMessageRow, ready: boolean) => {
    message.answerFullyRendered = Boolean(ready);
  };

  const loadFollowUpSuggestions = async (
    message: ChatMessageRow,
    ensure = false,
    regenerate = false,
  ) => {
    if (isAutomationSession.value) return;
    const messageId = resolveAssistantMessageId(message);
    const targetSessionId = sessionIdRef.value;
    const version = getLoadVersion();
    if (!messageId || !targetSessionId || message.suggestionsDismissed) return;
    if (isAwaitingInput(message)) return;
    message.suggestionLoading = true;
    try {
      let response = ensure
        ? await ensureMessageSuggestions(targetSessionId, messageId, regenerate)
        : await getMessageSuggestions(targetSessionId, messageId);
      if (
        version !== getLoadVersion() ||
        sessionIdRef.value !== targetSessionId ||
        message.suggestionsDismissed
      ) {
        return;
      }
      let set = response?.data;
      for (let attempt = 0; set?.status === 'generating' && attempt < 120; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 1000));
        if (
          version !== getLoadVersion() ||
          sessionIdRef.value !== targetSessionId ||
          message.suggestionsDismissed
        ) {
          return;
        }
        response = await getMessageSuggestions(targetSessionId, messageId);
        if (
          version !== getLoadVersion() ||
          sessionIdRef.value !== targetSessionId ||
          message.suggestionsDismissed
        ) {
          return;
        }
        set = response?.data;
      }
      message.suggestionSet = set?.status === 'ready' ? set : null;
    } catch (error: unknown) {
      if (version !== getLoadVersion()) return;
      if (ensure) console.warn('[FollowUpSuggestions] Failed to generate:', error);
      message.suggestionSet = null;
    } finally {
      message.suggestionLoading = false;
    }
  };

  const recordSuggestionEvent = (
    _message: ChatMessageRow,
    set: MessageSuggestionSet | null | undefined,
    eventType: MessageSuggestionEventType,
    questionId = '',
  ) => {
    if (!set?.id) return;
    void recordMessageSuggestionEvent(sessionIdRef.value, set.id, eventType, questionId).catch(
      () => undefined,
    );
  };

  const handleFollowUpSelect = (message: ChatMessageRow, item: MessageSuggestionQuestion) => {
    recordSuggestionEvent(message, message.suggestionSet, 'click', item.id);
    if (message.suggestionSet?.id) {
      pendingSuggestionAttribution = {
        suggestion_set_id: message.suggestionSet.id,
        question_id: item.id,
      };
    }
    pendingSuggestionKnowledgeBaseIds = [...new Set(item.knowledge_base_ids || [])];
    if (inputFieldRef.value?.triggerSend) inputFieldRef.value.triggerSend(item.text);
    else void sendMsg(item.text);
  };

  const dismissSuggestions = (message: ChatMessageRow, set: MessageSuggestionSet | null | undefined) => {
    message.suggestionsDismissed = true;
    recordSuggestionEvent(message, set, 'dismiss');
  };

  const consumePendingSuggestionScope = () => {
    const attribution = pendingSuggestionAttribution;
    const kbIds = pendingSuggestionKnowledgeBaseIds;
    pendingSuggestionAttribution = null;
    pendingSuggestionKnowledgeBaseIds = [];
    return { attribution, kbIds };
  };

  return {
    suggestedQuestions,
    suggestedQuestionsLoading,
    cancelSuggestedQuestionsFetch,
    fetchSuggestedQuestions,
    fetchSuggestedQuestionsIfNeeded,
    debouncedFetchSuggestions,
    handleAnswerRenderComplete,
    loadFollowUpSuggestions,
    recordSuggestionEvent,
    handleFollowUpSelect,
    dismissSuggestions,
    consumePendingSuggestionScope,
  };
}
