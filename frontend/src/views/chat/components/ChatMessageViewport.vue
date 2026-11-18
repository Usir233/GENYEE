<template>
  <div ref="scrollBoxRef" class="chat_scroll_box" @scroll="$emit('scroll')">
    <div class="chat_scroll_content">
      <div class="msg_list" :class="{ 'is-embedded': embeddedMode }">
        <!-- Message list loading skeleton -->
        <div v-if="historyLoading && messagesList.length === 0" class="msg-skeleton-list">
          <div class="msg-skeleton msg-skeleton-user">
            <t-skeleton animation="gradient" :row-col="[{ width: '45%', height: '36px', type: 'rect' }]" />
          </div>
          <div class="msg-skeleton msg-skeleton-bot">
            <t-skeleton
              animation="gradient"
              :row-col="[{ width: '80%', height: '16px' }, { width: '100%', height: '16px' }, { width: '60%', height: '16px' }]"
            />
          </div>
          <div class="msg-skeleton msg-skeleton-user">
            <t-skeleton animation="gradient" :row-col="[{ width: '35%', height: '36px', type: 'rect' }]" />
          </div>
          <div class="msg-skeleton msg-skeleton-bot">
            <t-skeleton animation="gradient" :row-col="[{ width: '70%', height: '16px' }, { width: '90%', height: '16px' }]" />
          </div>
        </div>

        <ChatWelcomeSuggestions
          v-if="!embeddedMode && hasCurrentSession && !isAutomationSession && messagesList.length === 0 && !loading"
          :questions="suggestedQuestions"
          :loading="suggestedQuestionsLoading"
          @refresh="$emit('refresh-suggested-questions')"
          @select="(item) => $emit('select-suggested-question', item)"
        />

        <!-- Key by session.id so upward history pagination never remounts existing messages. -->
        <div
          v-for="(session, index) in messagesList"
          :key="session.id || `${session.role}-${session.created_at}-${index}`"
          class="msg-item-wrapper"
          :class="{
            'is-steer-prefix': session.steerForked,
            'is-empty-segment': session.role === 'assistant' && !shouldRenderAssistantMessage(session),
          }"
        >
          <MessageTimestamp
            v-if="shouldShowConversationTimestamp(messagesList, index)"
            :value="session.created_at"
          />

          <div v-if="session.role === 'user'" class="message-row" :data-message-id="session.id || undefined">
            <usermsg
              :content="session.content"
              :mentioned_items="session.mentioned_items"
              :images="session.images"
              :attachments="session.attachments"
              :embeddedMode="embeddedMode"
              :session-id="sessionId"
              :message-id="session.id"
              :created-at="session.created_at"
              :can-fork="!embeddedMode && forkAffordanceOf(session.id).canFork"
              :can-rewind="canRewindMessage(session.id)"
              :steer-failed="Boolean(session._steerFailed)"
              @retry-steer="$emit('retry-steer', session.steer_id)"
              @remove-steer="$emit('remove-steer', session.steer_id)"
              @fork="(id) => $emit('fork', id)"
              @rewind="(id) => $emit('rewind', id)"
            />
          </div>

          <div
            v-if="session.role === 'assistant' && shouldRenderAssistantMessage(session)"
            class="message-row"
            :data-message-id="session.id || undefined"
          >
            <botmsg
              :content="session.content"
              :session="session"
              :session-id="sessionId"
              :user-query="getUserQuery(index)"
              :isFirstEnter="isFirstEnter"
              :embeddedMode="embeddedMode"
              :follow-up-loading="Boolean(session.suggestionLoading && !session.suggestionSet?.questions?.length)"
              :can-fork="!embeddedMode && forkAffordanceOf(session.id).canFork"
              :can-rewind="canRewindMessage(session.id)"
              :can-ask-assistant="!embeddedMode"
              @scroll-bottom="$emit('scroll-bottom')"
              @fork="(id) => $emit('fork', id)"
              @rewind="(id) => $emit('rewind', id)"
              @ask-assistant="(id) => $emit('ask-assistant', id)"
              @render-complete-change="(ready) => $emit('render-complete-change', session, ready)"
            />
            <FollowUpSuggestions v-if="!isAutomationSession && session.answerFullyRendered && !session.steerForked && !session.suggestionsDismissed"
              :suggestion-set="session.suggestionSet || null"
              :loading="Boolean(session.suggestionLoading)"
              :allow-regenerate="session.suggestionSet?.allow_regenerate"
              @select="(item) => $emit('follow-up-select', session, item)"
              @regenerate="$emit('follow-up-regenerate', session)"
              @impression="(set) => $emit('follow-up-impression', session, set)"
              @dismiss="(set) => $emit('follow-up-dismiss', session, set)"
            />
          </div>
        </div>

        <div
          v-if="showGlobalTypingIndicator"
          class="chat-global-wait"
          role="status"
          :aria-label="t('chat.thinkingAlt')"
        >
          <span class="chat-global-wait__spinner" aria-hidden="true"></span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import type { SuggestedQuestion } from '@/api/agent';
import type { MessageSuggestionQuestion, MessageSuggestionSet } from '@/api/message-suggestion';
import FollowUpSuggestions from '@/components/chat/FollowUpSuggestions.vue';
import MessageTimestamp from '@/components/chat/MessageTimestamp.vue';
import { shouldShowConversationTimestamp } from '@/utils/messageTimestamp';
import type { ChatMessageRow } from '../types/chatView';
import botmsg from './botmsg.vue';
import ChatWelcomeSuggestions from './ChatWelcomeSuggestions.vue';
import usermsg from './usermsg.vue';

const props = defineProps<{
  messagesList: ChatMessageRow[];
  sessionId: string;
  embeddedMode: boolean;
  hasCurrentSession: boolean;
  isAutomationSession: boolean;
  historyLoading: boolean;
  loading: boolean;
  isFirstEnter: boolean;
  showGlobalTypingIndicator: boolean;
  suggestedQuestions: SuggestedQuestion[];
  suggestedQuestionsLoading: boolean;
  shouldRenderAssistantMessage: (session: ChatMessageRow) => boolean;
  forkAffordanceOf: (messageId?: string) => { canFork: boolean };
  canRewindMessage: (messageId?: string) => boolean;
}>();

defineEmits<{
  (event: 'scroll'): void;
  (event: 'scroll-bottom'): void;
  (event: 'refresh-suggested-questions'): void;
  (event: 'select-suggested-question', item: SuggestedQuestion): void;
  (event: 'retry-steer', steerId?: string): void;
  (event: 'remove-steer', steerId?: string): void;
  (event: 'fork', messageId: string): void;
  (event: 'rewind', messageId: string): void;
  (event: 'ask-assistant', messageId: string): void;
  (event: 'render-complete-change', session: ChatMessageRow, ready: boolean): void;
  (event: 'follow-up-select', session: ChatMessageRow, item: MessageSuggestionQuestion): void;
  (event: 'follow-up-regenerate', session: ChatMessageRow): void;
  (event: 'follow-up-impression', session: ChatMessageRow, set: MessageSuggestionSet): void;
  (event: 'follow-up-dismiss', session: ChatMessageRow, set: MessageSuggestionSet): void;
}>();

const { t } = useI18n();
const scrollBoxRef = ref<HTMLElement | null>(null);

const getUserQuery = (index: number): string => {
  if (index <= 0) return '';
  const previous = props.messagesList[index - 1];
  if (previous && previous.role === 'user') {
    return previous.content || '';
  }
  return '';
};

defineExpose({
  scrollBoxRef,
});
</script>

<style lang="less" scoped>
@import '../../../components/css/chat-scrollbar.less';

@keyframes contentFadeIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.chat_scroll_box {
  flex: 1;
  min-height: 0;
  width: 100%;
  padding: 8px 0 0;
  box-sizing: border-box;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  scroll-padding-bottom: var(--chat-composer-height, 0px);
  .chat-scrollbar();
}

.chat_scroll_content {
  display: flex;
  flex-direction: column;
  min-height: 100%;

  &::after {
    content: '';
    flex: 0 0 var(--chat-composer-height, 0px);
  }
}

.msg-skeleton-list {
  display: flex;
  flex-direction: column;
  gap: 20px;
  max-width: 960px;
  padding: 16px 0;
  animation: contentFadeIn var(--gy-motion-slow) ease-out;
}

.msg-skeleton-user {
  display: flex;
  justify-content: flex-end;
}

.msg-skeleton-bot {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-left: 4px;
}

.msg_list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 960px;
  flex: 1;
  margin: 0 auto;
  width: 100%;
  box-sizing: border-box;

  &:not(.is-embedded) {
    padding: 0 var(--chat-content-inset, 20px);
    max-width: calc(960px + 2 * var(--chat-content-inset, 20px));
  }

  &.is-embedded {
    padding: 0 16px;
  }

  .msg-item-wrapper {
    contain: layout style;
    &.is-empty-segment {
      display: none;
    }
    &.is-steer-prefix {
      margin-bottom: -4px;
    }
  }

  .message-row {
    display: flex;
    flex-direction: column;
    width: 100%;
  }

  .chat-global-wait {
    display: flex;
    align-items: center;
    min-height: 28px;
    padding-left: 4px;
  }

  .chat-global-wait__spinner {
    width: 12px;
    height: 12px;
    box-sizing: border-box;
    border: 1.5px solid var(--gy-border-stroke);
    border-top-color: var(--gy-text-secondary);
    border-radius: var(--gy-radius-circle);
    animation: gy-spin 0.8s linear infinite;
  }
}

@media (prefers-reduced-motion: reduce) {
  .msg_list .chat-global-wait__spinner {
    animation: none;
  }
}
</style>
