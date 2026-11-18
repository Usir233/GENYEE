import { marked } from 'marked';
import { MessagePlugin } from 'tdesign-vue-next';
import { computed, nextTick, ref, watch, type ComputedRef, type Ref } from 'vue';
import { useProtectedImageRecovery } from '@/composables/useProtectedImageRecovery';
import { useStreamingHeightFloor } from '@/composables/useStreamingHeightFloor';
import { useTypewriter } from '@/composables/useTypewriter';
import { useUIStore } from '@/stores/ui';
import { renderChatMarkdown } from '@/utils/chatMarkdownRenderer';
import {
  buildManualMarkdown,
  collectManualPreferredKbIds,
  extractMermaidCodes,
  formatManualTitle,
  injectCachedMermaidSvg,
  prepareStreamingMermaidMarkdown,
  replaceIncompleteMermaidWithPlaceholder,
  type CachedMermaidSvgHtml,
} from '@/utils/chatMessageShared';
import { copyWithToast } from '@/utils/clipboard';
import { unwrapFinalAnswerWrappers } from '@/utils/finalAnswer';
import { refreshMarkdownEnhancements } from '@/utils/markdownEnhancements';
import {
  appendMermaidSvgCache,
  createMermaidCodeRenderer,
  enhanceMarkdownContainer,
} from '@/utils/mermaidShared';
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess';
import type { KnowledgeReferenceLike } from '@/utils/referenceSources';
import {
  installArtifactLinkRenderer,
  renderArtifactReference,
} from '@/utils/sandboxArtifactRefs';
import { sanitizeMarkdownHTML } from '@/utils/security';
import type {
  AgentSessionData,
  AgentTimelineEvent,
  IndexedSessionArtifact,
} from '../types/agentStream';

export interface UseAgentAnswerMarkdownOptions {
  rootElement: Ref<HTMLElement | null>;
  eventStream: ComputedRef<AgentTimelineEvent[]>;
  isSegmentDone: ComputedRef<boolean>;
  artifactList: ComputedRef<IndexedSessionArtifact[]>;
  artifactRefLabels: ComputedRef<{ previewHint: string; missingHint: string; deletedHint: string }>;
  artifactRefContext: ComputedRef<{ sessionId: string; messageId: string } | null>;
  protectedFileAccess: ComputedRef<ProtectedFileAccessContext | undefined>;
  getSession: () => AgentSessionData | undefined;
  getUserQuery: () => string | undefined;
  getReferencesForDrawer: (refsOverride?: KnowledgeReferenceLike[] | null) => KnowledgeReferenceLike[];
  onRenderCompleteChange: (ready: boolean) => void;
  t: (key: string, params?: Record<string, unknown>) => string;
}

function hasCachedMermaidSvg(cached: CachedMermaidSvgHtml): boolean {
  if (!cached) return false;
  if (typeof cached === 'string') return cached.length > 0;
  return cached.some((svg) => Boolean(svg));
}

export function useAgentAnswerMarkdown(options: UseAgentAnswerMarkdownOptions) {
  const {
    rootElement,
    eventStream,
    isSegmentDone,
    artifactList,
    artifactRefLabels,
    artifactRefContext,
    protectedFileAccess,
    getSession,
    getUserQuery,
    getReferencesForDrawer,
    onRenderCompleteChange,
    t,
  } = options;

  const uiStore = useUIStore();
  const streamingMermaidSvgCache = ref<string[]>([]);
  useStreamingHeightFloor(rootElement, computed(() => !isSegmentDone.value));
  let streamingMermaidRenderTask: Promise<void> | null = null;

  const activeAnswerMarkdown = computed(() => {
    const stream = eventStream.value;
    if (!stream?.length) return '';
    const answers = stream.filter((e) => e.type === 'answer' && !e.superseded);
    const active = answers.find((e) => !e.done) ?? answers[answers.length - 1];
    return typeof active?.content === 'string' ? active.content : '';
  });

  const activeAnswerEventRef = computed(() => {
    const stream = eventStream.value;
    if (!stream?.length) return null;
    const answers = stream.filter((e) => e.type === 'answer' && !e.superseded);
    return answers.find((e) => !e.done) ?? answers[answers.length - 1] ?? null;
  });

  const { displayed: typedAnswer } = useTypewriter(
    () => activeAnswerMarkdown.value,
    () => isSegmentDone.value,
  );

  const cacheStreamingMermaidSvg = async () => {
    const codes = extractMermaidCodes(activeAnswerMarkdown.value);
    if (codes.length <= streamingMermaidSvgCache.value.length) return;

    if (!streamingMermaidRenderTask) {
      streamingMermaidRenderTask = (async () => {
        streamingMermaidSvgCache.value = await appendMermaidSvgCache(
          extractMermaidCodes(activeAnswerMarkdown.value),
          streamingMermaidSvgCache.value,
          'mermaid-agent-stream',
        );
      })().finally(() => {
        streamingMermaidRenderTask = null;
      });
    }

    await streamingMermaidRenderTask;

    if (extractMermaidCodes(activeAnswerMarkdown.value).length > streamingMermaidSvgCache.value.length) {
      await cacheStreamingMermaidSvg();
    }
  };

  watch(isSegmentDone, (done) => {
    if (!done) {
      streamingMermaidSvgCache.value = [];
      streamingMermaidRenderTask = null;
    }
  });

  watch(streamingMermaidSvgCache, () => {
    nextTick(() => refreshMarkdownEnhancements(rootElement.value));
  });

  watch(activeAnswerMarkdown, () => {
    void cacheStreamingMermaidSvg();
  });

  const answerFullyRendered = computed(
    () =>
      !getSession()?.steerForked &&
      isSegmentDone.value &&
      typedAnswer.value.length >= activeAnswerMarkdown.value.length,
  );

  useProtectedImageRecovery(
    () => rootElement.value,
    () => protectedFileAccess.value,
    () => !getSession()?.persistence_error && answerFullyRendered.value,
  );

  watch(
    answerFullyRendered,
    (ready) => {
      onRenderCompleteChange(ready);
      if (!ready) return;
      nextTick(async () => {
        await enhanceMarkdownContainer(rootElement.value);
      });
    },
    { immediate: true },
  );

  const entityLinkLabels = computed(() => ({
    kb: t('agentStream.entityLink.kb'),
    doc: t('agentStream.entityLink.doc'),
    chunk: t('agentStream.entityLink.chunk'),
  }));

  const agentRenderer = new marked.Renderer();
  agentRenderer.code = createMermaidCodeRenderer('mermaid-agent');

  const defaultImageRenderer = new marked.Renderer().image;
  agentRenderer.image = function agentImageRenderer(token) {
    const artifactHtml = renderArtifactReference({
      href: token.href || '',
      alt: token.text || '',
      artifacts: artifactList.value,
      labels: artifactRefLabels.value,
      context: artifactRefContext.value,
      streaming: !isSegmentDone.value,
    });
    if (artifactHtml !== null) return artifactHtml;
    return defaultImageRenderer.call(this, token);
  };

  installArtifactLinkRenderer(agentRenderer, () => ({
    artifacts: artifactList.value,
    labels: artifactRefLabels.value,
    context: artifactRefContext.value,
    streaming: !isSegmentDone.value,
  }));

  const prepareAgentMarkdown = (markdown: string, cachedSvgHtml?: CachedMermaidSvgHtml): string => {
    const cache = cachedSvgHtml ?? streamingMermaidSvgCache.value;
    const mermaidSafe =
      !isSegmentDone.value || hasCachedMermaidSvg(cache)
        ? prepareStreamingMermaidMarkdown(markdown, cache)
        : replaceIncompleteMermaidWithPlaceholder(markdown);
    return mermaidSafe.replace(/<(?:kb|web)\b[^>]*$/i, '');
  };

  const renderAgentMarkdown = (
    content: unknown,
    escapeMarkdown: (markdown: string) => string,
  ): string => {
    const contentStr = typeof content === 'string' ? content : String(content || '');
    if (!contentStr.trim()) return '';

    return renderChatMarkdown(contentStr, {
      renderer: agentRenderer,
      escapeMarkdown,
      sanitizeHtml: sanitizeMarkdownHTML,
      streaming: !isSegmentDone.value,
      knowledgeReferences: getReferencesForDrawer(),
      cachedMermaidSvgHtml: streamingMermaidSvgCache.value,
      prepareMarkdown: prepareAgentMarkdown,
      injectCachedMermaidSvg,
      entityLinkLabels: entityLinkLabels.value,
    });
  };

  const getActualContent = (answerEvent: AgentTimelineEvent): string => {
    const answerContent = (answerEvent?.content || '').trim();
    if (answerContent) {
      return unwrapFinalAnswerWrappers(answerContent).trim();
    }
    const stream = eventStream.value;
    if (stream && Array.isArray(stream)) {
      const thinkingEvents = stream.filter((e) => e.type === 'thinking' && Boolean(e.content?.trim()));
      if (thinkingEvents.length > 0) {
        const lastThinking = thinkingEvents[thinkingEvents.length - 1];
        return unwrapFinalAnswerWrappers((lastThinking.content || '').trim()).trim();
      }
    }
    return '';
  };

  const handleCopyAnswer = async (answerEvent: AgentTimelineEvent) => {
    const content = getActualContent(answerEvent);
    if (!content) {
      MessagePlugin.warning(t('agentStream.copy.emptyContent'));
      return;
    }
    await copyWithToast(content, 'agentStream.copy.success', 'agentStream.copy.failed');
  };

  const handleAddToKnowledge = (answerEvent: AgentTimelineEvent) => {
    const content = getActualContent(answerEvent);
    if (!content) {
      MessagePlugin.warning(t('agentStream.saveToKb.emptyContent'));
      return;
    }
    const question = (getUserQuery() || '').trim();
    const manualContent = buildManualMarkdown(question, content);
    const manualTitle = formatManualTitle(question);

    uiStore.openManualEditor({
      mode: 'create',
      preferredKbIds: collectManualPreferredKbIds(getSession()?.knowledge_references),
      title: manualTitle,
      content: manualContent,
      status: 'draft',
    });
    MessagePlugin.info(t('agentStream.saveToKb.editorOpened'));
  };

  return {
    activeAnswerEventRef,
    typedAnswer,
    answerFullyRendered,
    renderAgentMarkdown,
    handleCopyAnswer,
    handleAddToKnowledge,
  };
}
