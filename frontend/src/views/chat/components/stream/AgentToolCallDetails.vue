<template>
  <div
    v-if="event.reasoning && isEventExpanded(event.reasoning.id)"
    class="action-reasoning thinking-detail-content markdown-content"
    @click.stop
  >
    <div v-html="renderMarkdownContent(event.reasoning.content)"></div>
  </div>

  <div v-if="event.pending && getSandboxFilePreview(event)" class="sandbox-file-preview">
    <pre>{{ getSandboxFilePreview(event) }}</pre>
    <div v-if="sandboxPreviewRemaining(event) > 0" class="sandbox-file-preview-more">
      {{ t('agentStream.sandboxFiles.moreLines', { count: sandboxPreviewRemaining(event) }) }}
    </div>
  </div>

  <div
    v-if="!event.pending && event.tool_name === 'todo_write' && event.tool_data?.steps"
    class="plan-status-summary-fixed"
  >
    <div class="plan-status-text">
      <template v-for="(part, partIndex) in getPlanStatusItems(event)" :key="partIndex">
        <ChatTimelineIcon width="14" height="14" :name="part.icon" :class="['status-icon', part.class]" />
        <span>{{ part.label }} {{ part.count }}</span>
        <span v-if="partIndex < getPlanStatusItems(event).length - 1" class="separator">·</span>
      </template>
    </div>
  </div>

  <div
    v-if="!event.pending && (event.tool_name === 'search_knowledge' || event.tool_name === 'knowledge_search') && event.tool_data"
    class="search-results-summary-fixed"
  >
    <div class="results-summary-text" v-html="getSearchResultsSummary(event)"></div>
  </div>

  <div v-if="!event.pending && event.tool_name === 'web_search' && event.tool_data" class="search-results-summary-fixed">
    <div class="results-summary-text" v-html="t('agent.webSearchFound', { count: getResultsCount(event.tool_data) })"></div>
  </div>

  <div
    v-if="!event.pending && event.tool_name === 'grep_chunks' && event.tool_data"
    class="search-results-summary-fixed grep-summary"
  >
    <div class="results-summary-text" v-html="getGrepResultsSummary(event.tool_data)"></div>
  </div>

  <div
    v-if="!event.pending && (event.tool_name === 'read_document' || event.tool_name === 'list_knowledge_chunks') && event.tool_data"
    class="search-results-summary-fixed knowledge-chunks-summary"
  >
    <div class="results-summary-text" v-html="getKnowledgeChunksSummary(event.tool_data)"></div>
  </div>

  <OpenPageCard v-if="showPageLinkInStep && event.tool_data" :link="asPageLink(event.tool_data)" />
  <PageEditCard
    v-if="showPageEditInStep && event.tool_data && event.tool_call_id"
    :fill="event.tool_data"
    :tool-call-id="event.tool_call_id"
  />
  <div v-if="isPageReadEvent && event.tool_data?.location" class="search-results-summary-fixed">
    <div class="results-summary-text">{{ t('agentStream.pageAccess.viewed', { location: event.tool_data.location }) }}</div>
  </div>

  <SandboxCommandProgress v-if="showLiveShellOutput(event) && event.command_output" :progress="event.command_output" />

  <div
    v-if="!event.pending && event.tool_name === 'attachment_parsing'"
    class="search-results-summary-fixed attachment-parsing-summary"
  >
    <div class="results-summary-text" v-html="getAttachmentParsingSummary(event)"></div>
  </div>

  <div
    v-if="isEventExpanded(event.tool_call_id) && !event.pending && hasExpandableResults(event)"
    class="action-details"
  >
    <BrowserToolDetails v-if="event.tool_name === 'local_browser'" :event="event" />
    <div v-else-if="displayType" class="tool-result-wrapper">
      <ToolResultRenderer
        :display-type="displayType"
        :tool-data="event.tool_data"
        :output="toolResultOutput(event)"
        :arguments="event.arguments"
        :success="event.success"
      />
    </div>
    <div v-else-if="event.output" class="tool-output-wrapper">
      <div class="fallback-header">
        <span class="fallback-label">{{ t('chat.rawOutputLabel') }}</span>
      </div>
      <div class="detail-output-wrapper">
        <div class="detail-output">{{ event.output }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import ChatTimelineIcon from '@/components/ChatTimelineIcon.vue';
import SandboxCommandProgress from '@/components/SandboxCommandProgress.vue';
import type { AssistantPageLink } from '@/utils/assistantPageLink';
import { showLiveShellOutput } from '@/utils/shellProgress';
import { getSandboxFilePreview, sandboxPreviewRemaining } from '@/utils/skillToolDisplay';
import { useAgentToolPresentation } from '../../composables/useAgentToolPresentation';
import type { AgentTimelineEvent, AgentToolData } from '../../types/agentStream';
import BrowserToolDetails from '../BrowserToolDetails.vue';
import OpenPageCard from '../OpenPageCard.vue';
import PageEditCard from '../PageEditCard.vue';
import ToolResultRenderer from '../ToolResultRenderer.vue';

const props = defineProps<{
  event: AgentTimelineEvent;
  isEventExpanded: (eventId?: string) => boolean;
  renderMarkdownContent: (content: unknown) => string;
  showPageLinkInStep: boolean;
  showPageEditInStep: boolean;
  isPageReadEvent: boolean;
}>();

const { t } = useI18n();
const {
  toolResultOutput,
  resolveToolDisplayType,
  hasExpandableResults,
  getPlanStatusItems,
  getSearchResultsSummary,
  getResultsCount,
  getGrepResultsSummary,
  getKnowledgeChunksSummary,
  getAttachmentParsingSummary,
} = useAgentToolPresentation(t);

const displayType = computed(() => resolveToolDisplayType(props.event));
const asPageLink = (data: AgentToolData): AssistantPageLink => data as AssistantPageLink;
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/chat-timeline-theme.less';
@import '../../../../components/css/chat-citations.less';

.thinking-detail-content {
  padding: 7px 0 0 0;
  font-size: var(--agent-step-summary-size);
  color: var(--agent-step-color);
  line-height: 1.6;
  max-height: none;
  overflow-y: visible;

  &.markdown-content {
    .chat-citation-pills();
  }
}

.action-reasoning {
  padding-top: 2px;
  user-select: text;
  cursor: text;
}

.sandbox-file-preview {
  margin: 6px 0 0;
  padding: 8px 10px;
  font-family: var(--app-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: var(--gy-text-xs);
  line-height: 1.5;
  color: var(--gy-text-secondary);
  background: var(--gy-surface-subtle);
  border-radius: var(--gy-radius-xs);

  pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 180px;
    overflow: hidden;
  }
}

.sandbox-file-preview-more {
  margin-top: 4px;
  font-size: var(--gy-text-xs);
  color: var(--gy-text-placeholder);
}

.action-details {
  padding: 0;
  border-top: 0;
  background: transparent;
  display: flex;
  flex-direction: column;
}

.tool-result-wrapper {
  margin: 0;
}

.search-results-summary-fixed {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 2px 0 0 0;
  background: transparent;
  border-top: 0;

  .results-summary-text {
    font-size: var(--agent-step-summary-size);
    font-weight: 400;
    color: var(--agent-step-color);
    line-height: 1.5;

    :deep(strong) {
      color: var(--agent-step-color);
      font-weight: 500;
    }
  }
}

.plan-status-summary-fixed {
  padding: 2px 0 0 0;
  background: transparent;
  border-top: 0;

  .plan-status-text {
    font-size: var(--agent-step-summary-size);
    font-weight: 400;
    color: var(--agent-step-color);
    line-height: 1.5;
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;

    .status-icon {
      font-size: var(--gy-text-md);
      flex-shrink: 0;

      &.in-progress {
        color: var(--gy-brand);
      }

      &.pending {
        color: var(--gy-warning);
      }

      &.completed {
        color: var(--gy-brand);
      }
    }

    .separator {
      color: var(--gy-text-placeholder);
      margin: 0 4px;
    }

    span:not(.separator) {
      display: inline-flex;
      align-items: center;
      gap: 4px;
    }
  }
}

.tool-output-wrapper {
  margin: 10px 0;
  padding: 0 8px;

  .fallback-header {
    display: flex;
    align-items: center;
    margin-bottom: 8px;
    padding: 0 4px;

    .fallback-label {
      font-size: var(--gy-text-2xs);
      color: var(--gy-text-secondary);
      font-weight: 500;
      line-height: 1.5;
    }
  }

  .detail-output-wrapper {
    position: relative;
    background: var(--gy-surface-subtle);
    border: 1px solid var(--gy-border-stroke);
    border-radius: var(--gy-radius-xs);
    overflow: hidden;
    margin: 0;
    padding: 0;

    .detail-output {
      font-family: var(--app-font-family-mono);
      font-size: var(--gy-text-2xs);
      color: var(--gy-text-primary);
      padding: 12px;
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
      line-height: 1.6;
      max-height: 400px;
      overflow-y: auto;
      overflow-x: auto;
      background: var(--gy-surface);
      display: block;
      scrollbar-width: thin;
      scrollbar-color: var(--gy-scrollbar-thumb) var(--gy-scrollbar-track);

      @supports not (scrollbar-color: auto) {
        scrollbar-width: auto;

        &::-webkit-scrollbar {
          width: 6px;
          height: 6px;
        }

        &::-webkit-scrollbar-track {
          background: var(--gy-scrollbar-track);
          border-radius: var(--gy-radius-xs);
        }

        &::-webkit-scrollbar-thumb {
          background: var(--gy-scrollbar-thumb);
          border-radius: var(--gy-radius-xs);

          &:hover {
            background: var(--gy-scrollbar-thumb-hover);
          }
        }
      }
    }
  }
}
</style>
