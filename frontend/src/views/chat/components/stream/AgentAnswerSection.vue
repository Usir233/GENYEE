<template>
  <div class="answer-event">
    <div v-if="event.content && event.content.trim()" class="answer-content markdown-content">
      <div v-stable-html="renderedAnswerHtml"></div>
    </div>
    <div v-if="isPinnedAnswer && (pinnedPageLinks.length > 0 || pinnedPageEdits.length > 0)" class="answer-page-links">
      <template v-for="link in pinnedPageLinks" :key="link.tool_call_id">
        <OpenPageCard v-if="link.tool_data" :link="asPageLink(link.tool_data)" />
      </template>
      <template v-for="edit in pinnedPageEdits" :key="edit.tool_call_id">
        <PageEditCard
          v-if="edit.tool_data && edit.tool_call_id"
          :fill="edit.tool_data"
          :tool-call-id="edit.tool_call_id"
        />
      </template>
    </div>
    <div
      v-if="answerFullyRendered && event.done && event.content && event.content.trim()"
      class="answer-toolbar"
      :class="{ 'answer-toolbar--persistent': hasArtifacts || artifactsCollecting }"
    >
      <t-tooltip v-if="canFork" :content="forkTooltip">
        <t-button size="small" variant="outline" shape="round" :aria-label="forkTooltip" @click.stop="$emit('fork')">
          <ChatActionIcon name="git-branch" />
        </t-button>
      </t-tooltip>
      <t-popconfirm
        v-if="canRewind"
        :confirm-btn="{ content: t('chat.rewind.confirmButton'), theme: 'danger', size: 'small' }"
        :cancel-btn="{ content: t('chat.rewind.cancelButton'), size: 'small' }"
        theme="warning"
        placement="top"
        :show-arrow="false"
        overlay-class-name="gy-popover chat-rewind-popconfirm"
        @confirm="$emit('rewind')"
      >
        <template #content>
          <div class="chat-rewind-confirm__title">{{ t('chat.rewind.confirmTitle') }}</div>
          <div class="chat-rewind-confirm__body">{{ t('chat.rewind.confirmBody') }}</div>
        </template>
        <t-tooltip :content="rewindTooltip">
          <t-button size="small" variant="outline" shape="round" :aria-label="rewindTooltip" @click.stop>
            <ChatActionIcon name="rollback" />
          </t-button>
        </t-tooltip>
      </t-popconfirm>
      <t-button
        size="small"
        variant="outline"
        shape="round"
        :title="t('agent.copy')"
        :aria-label="t('agent.copy')"
        @click.stop="$emit('copy', event)"
      >
        <ChatActionIcon name="copy" />
      </t-button>
      <t-button
        v-if="!embeddedMode"
        size="small"
        variant="outline"
        shape="round"
        :title="t('agent.addToKnowledgeBase')"
        :aria-label="t('agent.addToKnowledgeBase')"
        @click.stop="$emit('add-to-knowledge', event)"
      >
        <ChatActionIcon name="bookmark-add" />
      </t-button>
      <!-- Includes files delivered by child agents. Embedded assistants
           use the same entry and open the artifact drawer. -->
      <span
        v-if="hasArtifacts || artifactsCollecting"
        class="answer-toolbar__artifact"
        :class="{ 'is-collecting': artifactButtonCollecting, 'is-arrived': artifactArrived }"
        @animationend="$emit('artifact-arrive-end')"
      >
        <t-button
          size="small"
          variant="outline"
          shape="round"
          :disabled="artifactButtonCollecting"
          :title="hasArtifacts ? t('agent.artifactDrawer.buttonTitle') : t('agent.artifactDrawer.collecting')"
          :aria-label="hasArtifacts ? t('agent.artifactDrawer.buttonTitle') : t('agent.artifactDrawer.collecting')"
          @click.stop="$emit('open-artifacts')"
        >
          <t-icon v-if="artifactButtonCollecting" name="loading" class="answer-toolbar__artifact-spinner" />
          <ChatActionIcon v-else name="folder" />
        </t-button>
        <span v-if="hasArtifacts" class="answer-toolbar__artifact-count" aria-hidden="true">{{ artifactCount }}</span>
      </span>
      <t-tooltip v-if="!embeddedMode && event.is_fallback" :content="t('chat.fallbackHint')" placement="top">
        <t-button
          size="small"
          variant="outline"
          shape="round"
          class="fallback-icon-btn"
          :aria-label="t('chat.fallbackHint')"
        >
          <ChatActionIcon name="info-circle" />
        </t-button>
      </t-tooltip>
      <t-tooltip v-if="!embeddedMode && event.truncated" :content="t('chat.truncatedHint')" placement="top">
        <t-button
          size="small"
          variant="outline"
          shape="round"
          class="fallback-icon-btn"
          :aria-label="t('chat.truncatedHint')"
        >
          <ChatActionIcon name="info-circle" />
        </t-button>
      </t-tooltip>
      <ChatRequestInfoButton
        v-if="showRequestInfo && isConversationDone"
        :session="session"
        :session-id="sessionId"
      />
      <transition name="follow-up-toolbar-loading">
        <span
          v-if="!embeddedMode && followUpLoading"
          class="answer-toolbar__follow-up-loading"
          role="status"
          aria-live="polite"
        >
          <t-icon name="lightbulb" />
          <span class="answer-toolbar__follow-up-label">{{ t('chat.followUpQuestionsLoading') }}</span>
        </span>
      </transition>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import ChatActionIcon from '@/components/ChatActionIcon.vue';
import ChatRequestInfoButton from '@/components/ChatRequestInfoButton.vue';
import { vStableHtml } from '@/directives/stableHtml';
import type { AssistantPageLink } from '@/utils/assistantPageLink';
import type { AgentSessionData, AgentTimelineEvent, AgentToolData } from '../../types/agentStream';
import OpenPageCard from '../OpenPageCard.vue';
import PageEditCard from '../PageEditCard.vue';

const props = defineProps<{
  event: AgentTimelineEvent;
  renderedAnswerHtml: string;
  isPinnedAnswer: boolean;
  pinnedPageLinks: AgentTimelineEvent[];
  pinnedPageEdits: AgentTimelineEvent[];
  answerFullyRendered: boolean;
  hasArtifacts: boolean;
  artifactsCollecting: boolean;
  artifactButtonCollecting: boolean;
  artifactArrived: boolean;
  artifactCount: number;
  canFork: boolean;
  canRewind: boolean;
  embeddedMode?: boolean;
  showRequestInfo: boolean;
  isConversationDone: boolean;
  session: AgentSessionData;
  sessionId?: string;
  followUpLoading?: boolean;
}>();

defineEmits<{
  (event: 'fork'): void;
  (event: 'rewind'): void;
  (event: 'copy', answerEvent: AgentTimelineEvent): void;
  (event: 'add-to-knowledge', answerEvent: AgentTimelineEvent): void;
  (event: 'open-artifacts'): void;
  (event: 'artifact-arrive-end'): void;
}>();

const { t } = useI18n();
const forkTooltip = computed(() => t('chat.forkFromAnswerTooltip'));
const rewindTooltip = computed(() => t('chat.rewind.tooltip'));
const asPageLink = (data: AgentToolData): AssistantPageLink => data as AssistantPageLink;
void props;
</script>

<style lang="less" scoped>
@import '../../../../components/css/chat-markdown.less';
@import '../../../../components/css/chat-message-shared.less';
@import '../../../../components/css/chat-citations.less';

@keyframes fadeInUp {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.answer-event {
  animation: fadeInUp var(--gy-motion-base) ease-out;
  min-height: 20px;

  :deep(.t-button.fallback-icon-btn) {
    color: var(--gy-text-disabled);
    border-color: var(--gy-border-stroke);

    &:hover {
      color: var(--gy-text-placeholder);
      border-color: var(--gy-border);
    }
  }

  .answer-content {
    &.markdown-content {
      .chat-markdown-typography();
      .chat-citation-pills();

      :deep(img) {
        background-color: var(--gy-surface-subtle);
      }
    }
  }

  .answer-toolbar {
    margin-top: 10px;
  }

  .answer-page-links {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 12px;

    :deep(.open-page-card) {
      margin-top: 0;
    }
  }
}
</style>
