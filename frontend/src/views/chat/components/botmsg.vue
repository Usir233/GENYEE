<template>
    <div class="bot_msg" :class="{ 'is-embedded': embeddedMode }">
        <div style="display: flex;flex-direction: column; gap:8px">
            <div v-if="!session?.isAgentMode && mentionedItems && mentionedItems.length > 0" class="mentioned_items">
                <span v-for="item in mentionedItems" :key="item.id" class="mentioned_tag" :class="[mentionTagClass(item)]">
                    <span class="tag_icon">
                        <t-icon v-if="item.type === 'kb'" :name="item.kb_type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                        <t-icon v-else :name="mentionTagIcon(item)" />
                    </span>
                    <span class="tag_name">{{ item.name }}</span>
                </span>
            </div>
            <div v-if="session?.isRagMode" class="rag-answer-stack">
                <RagPipelineProgress :session="session" :embedded-mode="embeddedMode" />
                <AgentStreamDisplay
                    v-if="session?.isAgentMode"
                    :session="session"
                    :session-id="sessionId"
                    :user-query="userQuery"
                    :rag-mode="true"
                    :follow-up-loading="followUpLoading"
                    :embedded-mode="embeddedMode"
                    defer-tool-approvals
                    :can-fork="canFork"
                    :can-rewind="canRewind"
                    @fork="emit('fork', $event)"
                    @rewind="emit('rewind', $event)"
                    @render-complete-change="emit('render-complete-change', $event)"
                />
            </div>
            <template v-else>
                <RagPipelineProgress v-if="!session.isAgentMode && session.used_memories?.length"
                    :session="session"
                    :embedded-mode="embeddedMode"
                    memory-only
                />
                <docInfo v-if="session?.knowledge_references?.length" :session="session" />
                <AgentStreamDisplay
                    v-if="session?.isAgentMode && session"
                    :session="session"
                    :session-id="sessionId"
                    :user-query="userQuery"
                    :follow-up-loading="followUpLoading"
                    :embedded-mode="embeddedMode"
                    defer-tool-approvals
                    :can-fork="canFork"
                    :can-rewind="canRewind"
                    @fork="emit('fork', $event)"
                    @rewind="emit('rewind', $event)"
                    @render-complete-change="emit('render-complete-change', $event)"
                />
            </template>
            <deepThink v-if="session?.showThink && !session?.isAgentMode" :deepSession="session" />
        </div>
        <div ref="parentMd" v-if="!session?.hideContent && !session?.isAgentMode" @click.capture="handleMarkdownImageClick">
            <div v-if="hasActualContent" class="content-wrapper">
                <div class="ai-markdown-template markdown-content" v-stable-html="renderedHTML"></div>
            </div>
            <div
                v-if="answerFullyRendered && (content || session.content)"
                class="answer-toolbar"
                :class="{ 'answer-toolbar--persistent': hasArtifacts || artifactsCollecting }"
            >
                <t-tooltip v-if="canFork" :content="forkTooltip">
                    <t-button size="small" variant="outline" shape="round" :aria-label="forkTooltip" @click.stop="emitFork">
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
                    @confirm="emitRewind"
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
                <AssistantActionsButton
                    v-if="canAskAssistant"
                    icon-only
                    :label="askAssistantTitle"
                    :actions="answerAssistantActions"
                    @before-open="emitAskAssistant"
                />
                <t-button
                    size="small"
                    variant="outline"
                    shape="round"
                    :title="$t('agent.copy')"
                    :aria-label="$t('agent.copy')"
                    @click.stop="handleCopyAnswer"
                >
                    <ChatActionIcon name="copy" />
                </t-button>
                <t-button
                    size="small"
                    variant="outline"
                    shape="round"
                    :title="$t('agent.addToKnowledgeBase')"
                    :aria-label="$t('agent.addToKnowledgeBase')"
                    @click.stop="handleAddToKnowledge"
                >
                    <ChatActionIcon name="bookmark-add" />
                </t-button>
                <span
                    v-if="hasArtifacts || artifactsCollecting"
                    class="answer-toolbar__artifact"
                    :class="{ 'is-collecting': artifactButtonCollecting, 'is-arrived': artifactArrived }"
                    @animationend="onArtifactArriveEnd"
                >
                    <t-button
                        size="small"
                        variant="outline"
                        shape="round"
                        :disabled="artifactButtonCollecting"
                        :title="hasArtifacts ? $t('agent.artifactDrawer.buttonTitle') : $t('agent.artifactDrawer.collecting')"
                        :aria-label="hasArtifacts ? $t('agent.artifactDrawer.buttonTitle') : $t('agent.artifactDrawer.collecting')"
                        @click.stop="openArtifactDrawer()"
                    >
                        <t-icon v-if="artifactButtonCollecting" name="loading" class="answer-toolbar__artifact-spinner" />
                        <ChatActionIcon v-else name="folder" />
                    </t-button>
                    <span v-if="hasArtifacts" class="answer-toolbar__artifact-count" aria-hidden="true">{{ artifactCount }}</span>
                </span>
                <t-tooltip v-if="session?.is_fallback" :content="$t('chat.fallbackHint')" placement="top">
                    <t-button size="small" variant="outline" shape="round" class="fallback-icon-btn" :aria-label="$t('chat.fallbackHint')">
                        <ChatActionIcon name="info-circle" />
                    </t-button>
                </t-tooltip>
                <t-tooltip v-if="session?.truncated" :content="$t('chat.truncatedHint')" placement="top">
                    <t-button size="small" variant="outline" shape="round" class="fallback-icon-btn" :aria-label="$t('chat.truncatedHint')">
                        <ChatActionIcon name="info-circle" />
                    </t-button>
                </t-tooltip>
                <ChatRequestInfoButton v-if="showRequestInfo && session" :session="session" :session-id="sessionId" />
                <transition name="follow-up-toolbar-loading">
                    <span v-if="followUpLoading" class="answer-toolbar__follow-up-loading" role="status" aria-live="polite">
                        <t-icon name="lightbulb" />
                        <span class="answer-toolbar__follow-up-label">{{ t('chat.followUpQuestionsLoading') }}</span>
                    </span>
                </transition>
            </div>
            <div v-if="isImgLoading" class="img_loading">
                <t-loading size="small" />
                <span>{{ $t('common.loading') }}</span>
            </div>
        </div>
        <PicturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg" />
        <Teleport to="body">
            <ChatCitationFloat :float="citationFloat" :on-enter="cancelCitationClose" :on-leave="scheduleCitationClose" />
        </Teleport>
        <ChatArtifactsDrawer
            v-if="hasArtifacts && embeddedMode"
            v-model:visible="showArtifactDrawer"
            :session-id="sessionId"
            :message-id="messageIdForArtifacts"
            :artifacts="liveArtifacts"
            :preview-index="artifactPreviewIndex"
        />
    </div>
</template>
<script setup lang="ts">
import 'katex/dist/katex.min.css';
import { MessagePlugin } from 'tdesign-vue-next';
import { computed, nextTick, onMounted, onUpdated, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import AssistantActionsButton from '@/components/assistant/AssistantActionsButton.vue';
import ChatActionIcon from '@/components/ChatActionIcon.vue';
import ChatCitationFloat from '@/components/ChatCitationFloat.vue';
import ChatRequestInfoButton from '@/components/ChatRequestInfoButton.vue';
import PicturePreview from '@/components/PicturePreview.vue';
import { useArtifactArriveMotion } from '@/composables/useArtifactArriveMotion';
import { useChatCitationPopover } from '@/composables/useChatCitationPopover';
import { useChatSandboxPanel } from '@/composables/useChatSandboxPanel';
import { useProtectedImageRecovery } from '@/composables/useProtectedImageRecovery';
import { useTypewriter } from '@/composables/useTypewriter';
import { vStableHtml } from '@/directives/stableHtml';
import { useAssistantStore } from '@/stores/assistant';
import { useUIStore } from '@/stores/ui';
import { SKILL_ICON } from '@/types/mention';
import { createChatMarkdownRenderer, renderChatMarkdown } from '@/utils/chatMarkdownRenderer';
import {
    buildManualMarkdown,
    collectManualPreferredKbIds,
    formatManualTitle,
} from '@/utils/chatMessageShared';
import { copyWithToast } from '@/utils/clipboard';
import { refreshMarkdownEnhancements } from '@/utils/markdownEnhancements';
import {
    createMermaidCodeRenderer,
    enhanceMarkdownContainer,
    ensureMermaidInitialized,
    renderMermaidInContainer,
} from '@/utils/mermaidShared';
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess';
import {
    artifactIndexFromEventTarget,
    hydrateArtifactImages,
    installArtifactLinkRenderer,
    isArtifactRefHref,
    renderArtifactReference,
} from '@/utils/sandboxArtifactRefs';
import {
    createSafeImage,
    hydrateProtectedFileImages,
    isValidImageURL,
    safeMarkdownToHTML,
    sanitizeMarkdownHTML,
} from '@/utils/security';
import { isCollectingSkillArtifacts } from '@/utils/skillArtifacts';
import { persistedAssistantId } from '@/utils/steerStreamFork';
import type { AgentSessionData, IndexedSessionArtifact } from '../types/agentStream';
import type { MentionedItemInput } from '../types/chatView';
import AgentStreamDisplay from './AgentStreamDisplay.vue';
import ChatArtifactsDrawer from './ChatArtifactsDrawer.vue';
import deepThink from './deepThink.vue';
import docInfo from './docInfo.vue';
import RagPipelineProgress from './RagPipelineProgress.vue';

ensureMermaidInitialized();

export interface BotMessageSession extends AgentSessionData {
    content?: string;
    isRagMode?: boolean;
    showThink?: boolean;
    thinking?: boolean;
    thinkContent?: string;
    hideContent?: boolean;
    is_fallback?: boolean;
    truncated?: boolean;
    mentioned_items?: MentionedItemInput[];
}

const mentionTagClass = (item: MentionedItemInput): string => {
    if (item.type === 'kb') return item.kb_type === 'faq' ? 'faq-tag' : 'kb-tag';
    return `${item.type || 'file'}-tag`;
};

const mentionTagIcon = (item: MentionedItemInput): string => {
    if (item.type === 'tag') return 'tag';
    if (item.type === 'mcp') return 'tools';
    if (item.type === 'skill') return SKILL_ICON;
    return 'file';
};

const emit = defineEmits<{
    (event: 'scroll-bottom'): void;
    (event: 'render-complete-change', ready: boolean): void;
    (event: 'fork', messageId: string): void;
    (event: 'rewind', messageId: string): void;
    (event: 'ask-assistant', messageId: string): void;
}>();

const { t } = useI18n();
const uiStore = useUIStore();
const parentMd = ref<HTMLElement | null>(null);
const reviewUrl = ref('');
const reviewImg = ref(false);
const isImgLoading = ref(false);

const props = withDefaults(
    defineProps<{
        content?: string;
        session?: BotMessageSession;
        userQuery?: string;
        isFirstEnter?: boolean;
        embeddedMode?: boolean;
        sessionId?: string;
        followUpLoading?: boolean;
        canFork?: boolean;
        canRewind?: boolean;
        canAskAssistant?: boolean;
    }>(),
    {
        content: '',
        session: () => ({}),
        userQuery: '',
        isFirstEnter: false,
        embeddedMode: false,
        sessionId: '',
        followUpLoading: false,
        canFork: false,
        canRewind: false,
        canAskAssistant: false,
    },
);

const {
    float: citationFloat,
    rebind: rebindCitations,
    cancelClose: cancelCitationClose,
    scheduleClose: scheduleCitationClose,
} = useChatCitationPopover(parentMd, {
    getKnowledgeReferences: () => props.session?.knowledge_references,
    sessionId: () => props.sessionId,
});

const canFork = computed(() => props.canFork === true && !props.embeddedMode);
const canRewind = computed(() => props.canRewind === true && !props.embeddedMode);
const forkTooltip = computed(() => t('chat.forkFromAnswerTooltip'));
const rewindTooltip = computed(() => t('chat.rewind.tooltip'));

const emitFork = () => {
    const messageId = persistedAssistantId(props.session) || props.session?.id;
    if (messageId) emit('fork', messageId);
};
const emitRewind = () => {
    const messageId = persistedAssistantId(props.session) || props.session?.id;
    if (messageId) emit('rewind', messageId);
};

const assistant = useAssistantStore();
const canAskAssistant = computed(() => props.canAskAssistant === true && !props.embeddedMode);
const askAssistantTitle = computed(() =>
    t('assistant.askAboutAnswer', { name: assistant.displayName || t('assistant.title') }),
);
const emitAskAssistant = () => {
    const messageId = persistedAssistantId(props.session) || props.session?.id;
    if (messageId) emit('ask-assistant', messageId);
};
const answerAssistantActions = computed(() => {
    const source = JSON.stringify({
        session_id: props.sessionId,
        message_id: persistedAssistantId(props.session) || props.session?.id,
    });
    return [
        { key: 'document', label: t('assistant.actions.answerToDocument'), query: t('assistant.prompts.answerToDocument', { source }) },
        { key: 'faq', label: t('assistant.actions.answerToFaq'), query: t('assistant.prompts.answerToFaq', { source }) },
        { key: 'automation', label: t('assistant.actions.answerToAutomation'), query: t('assistant.prompts.answerToAutomation', { source }) },
    ];
});

const showRequestInfo = computed(() => !!(props.session?.request_id || props.session?.id));

const showArtifactDrawer = ref(false);
const sandboxPanel = useChatSandboxPanel();
const artifactList = computed<IndexedSessionArtifact[]>(() => {
    const raw = props.session?.artifacts;
    const list = Array.isArray(raw) ? raw : [];
    return list.map((a, i) => ({ index: i, ...a }));
});
const liveArtifacts = computed(() => artifactList.value.filter((a) => !a.deleted_at));
const hasArtifacts = computed(() => liveArtifacts.value.length > 0);
const artifactCount = computed(() => liveArtifacts.value.length);
const { artifactArrived, onArtifactArriveEnd } = useArtifactArriveMotion(artifactCount);
const artifactsCollecting = computed(() => isCollectingSkillArtifacts(props.session));
const artifactButtonCollecting = computed(() => artifactsCollecting.value && !hasArtifacts.value);
const messageIdForArtifacts = computed(() => {
    return persistedAssistantId(props.session) || String(props.session?.request_id || '');
});
// Set when the drawer is opened by clicking an inline artifact card, so it
// lands directly on that file's preview instead of the list.
const artifactPreviewIndex = ref<number | null>(null);
function openArtifactDrawer(previewIndex: number | null = null) {
    if (!hasArtifacts.value) return;
    if (sandboxPanel && !props.embeddedMode) {
        if (previewIndex == null) {
            sandboxPanel.toggleArtifacts(messageIdForArtifacts.value);
        } else {
            sandboxPanel.open('artifacts', {
                messageId: messageIdForArtifacts.value,
                previewIndex,
            });
        }
        return;
    }
    artifactPreviewIndex.value = previewIndex;
    showArtifactDrawer.value = true;
}

const artifactRefContext = computed(() => {
    const messageId = messageIdForArtifacts.value;
    if (!props.sessionId || !messageId) return null;
    return { sessionId: props.sessionId, messageId };
});

const protectedFileAccess = computed<ProtectedFileAccessContext | undefined>(() => {
    const messageId = persistedAssistantId(props.session);
    if (!props.sessionId || !messageId) return undefined;
    return { mode: 'message', sessionId: props.sessionId, messageId };
});

const artifactRefLabels = computed(() => ({
    previewHint: t('agent.artifactDrawer.inlinePreviewHint'),
    missingHint: t('agent.artifactDrawer.inlineMissing'),
    deletedHint: t('agent.artifactDrawer.inlineDeleted'),
}));

const preview = (url: string) => {
    nextTick(() => {
        reviewUrl.value = url;
        reviewImg.value = true;
    });
};

const closePreImg = () => {
    reviewImg.value = false;
    reviewUrl.value = '';
};

const markdownRenderer = createChatMarkdownRenderer({
    codeRenderer: createMermaidCodeRenderer('mermaid-botmsg'),
    imageRenderer: ({ href, title, text }) => {
        const artifactHtml = renderArtifactReference({
            href,
            alt: text || '',
            artifacts: artifactList.value,
            labels: artifactRefLabels.value,
            context: artifactRefContext.value,
            streaming: !props.session?.is_completed,
        });
        if (artifactHtml !== null) return artifactHtml;
        return createSafeImage(href, text || '', title || '');
    },
    invalidImageHtml: () => `<p>${t('error.invalidImageLink')}</p>`,
    isValidImageUrl: (href) => isArtifactRefHref(href) || isValidImageURL(href),
});

installArtifactLinkRenderer(markdownRenderer, () => ({
    artifacts: artifactList.value,
    labels: artifactRefLabels.value,
    context: artifactRefContext.value,
    streaming: !props.session?.is_completed,
}));

const mentionedItems = computed(() => props.session?.mentioned_items || []);

const answerText = computed(() => {
    const text = props.content || props.session?.content || '';
    return typeof text === 'string' ? text : '';
});
const { displayed: typedAnswer } = useTypewriter(
    () => answerText.value,
    () => Boolean(props.session?.is_completed),
);

const answerFullyRendered = computed(() =>
    Boolean(props.session?.is_completed) && typedAnswer.value.length >= answerText.value.length,
);
useProtectedImageRecovery(
    () => parentMd.value,
    () => protectedFileAccess.value,
    () => !props.session?.isAgentMode && !props.session?.persistence_error && answerFullyRendered.value,
);

watch(
    answerFullyRendered,
    (ready) => {
        if (!props.session?.isAgentMode) emit('render-complete-change', ready);
    },
    { immediate: true },
);

const renderedHTML = computed(() => {
    const text = typedAnswer.value;
    if (!text || typeof text !== 'string') return '';
    return renderChatMarkdown(text, {
        renderer: markdownRenderer,
        escapeMarkdown: safeMarkdownToHTML,
        sanitizeHtml: sanitizeMarkdownHTML,
        streaming: !props.session?.is_completed,
        knowledgeReferences: props.session?.knowledge_references,
    });
});

const hasActualContent = computed(() => {
    const text = props.content || props.session?.content || '';
    return Boolean(text && text.trim().length > 0);
});

const getActualContent = (): string => {
    return (props.content || props.session?.content || '').trim();
};

const handleCopyAnswer = async () => {
    const content = getActualContent();
    if (!content) {
        MessagePlugin.warning(t('chat.emptyContentWarning'));
        return;
    }
    await copyWithToast(content, 'common.copySuccess', 'common.copyFailed');
};

const handleAddToKnowledge = () => {
    const content = getActualContent();
    if (!content) {
        MessagePlugin.warning(t('chat.emptyContentWarning'));
        return;
    }

    const question = (props.userQuery || '').trim();
    const manualContent = buildManualMarkdown(question, content);
    const manualTitle = formatManualTitle(question);

    uiStore.openManualEditor({
        mode: 'create',
        preferredKbIds: collectManualPreferredKbIds(props.session?.knowledge_references),
        title: manualTitle,
        content: manualContent,
        status: 'draft',
    });

    MessagePlugin.info(t('chat.editorOpened'));
};

const handleMarkdownImageClick = (e: MouseEvent) => {
    const target = e.target as HTMLElement | null;
    const artifactIndex = artifactIndexFromEventTarget(target);
    if (artifactIndex !== null) {
        e.preventDefault();
        e.stopPropagation();
        openArtifactDrawer(artifactIndex);
        return;
    }
    if (target && target.tagName === 'IMG') {
        const src = target.getAttribute('src');
        if (src) {
            e.preventDefault();
            e.stopPropagation();
            preview(src);
        }
    }
};

watch(renderedHTML, () => {
    nextTick(() => {
        rebindCitations();
    });
});

onUpdated(() => {
    nextTick(async () => {
        await hydrateProtectedFileImages(parentMd.value, protectedFileAccess.value);
        await hydrateArtifactImages(parentMd.value, artifactRefContext.value);
        refreshMarkdownEnhancements(parentMd.value);
        if (props.session?.is_completed) {
            await renderMermaidInContainer(parentMd.value);
        }
    });
});

onMounted(async () => {
    nextTick(async () => {
        rebindCitations();
        await hydrateProtectedFileImages(parentMd.value, protectedFileAccess.value);
        await hydrateArtifactImages(parentMd.value, artifactRefContext.value);
        await enhanceMarkdownContainer(parentMd.value);
    });
});
</script>
<style lang="less" scoped>
@import '../../../components/css/chat-markdown.less';
@import '../../../components/css/chat-message-shared.less';
@import '../../../components/css/chat-citations.less';

.bot_msg {
    border-radius: var(--gy-radius-xs);
    color: var(--gy-text-primary);
    font-size: var(--gy-text-lg);
    margin-right: auto;
    width: 100%;
    min-width: 0;
    max-width: 100%;
    box-sizing: border-box;

    &.is-embedded {
        width: 100%;

        :deep(.agent-stream-display) {
            width: 100%;
        }
    }
}

.rag-answer-stack {
    display: flex;
    flex-direction: column;
    gap: 0;
}

.content-wrapper {
    padding: 2px 0;
}

.markdown-content {
    .chat-markdown-typography();
    .chat-citation-pills();
}

.mentioned_items {
    .chat-mentioned-items();
}

.mentioned_tag {
    .chat-mentioned-tag();
}

:deep(.t-button.fallback-icon-btn) {
    color: var(--gy-text-disabled);
    border-color: var(--gy-border-stroke);

    &:hover {
        color: var(--gy-text-placeholder);
        border-color: var(--gy-border);
    }
}

.ai-markdown-img {
    max-width: 80%;
    max-height: 300px;
    width: auto;
    height: auto;
    border-radius: var(--gy-radius-md);
    display: block;
    cursor: pointer;
    object-fit: contain;
    margin: 8px 0 8px 16px;
    border: 0.5px solid var(--gy-border-stroke);
    transition: transform var(--gy-motion-base) ease;

    &:hover {
        transform: scale(1.02);
    }
}

.img_loading {
    background: var(--gy-surface-hover);
    height: 230px;
    width: 230px;
    color: var(--gy-text-placeholder);
    display: flex;
    align-items: center;
    justify-content: center;
    flex-direction: column;
    font-size: var(--gy-text-xs);
    gap: 4px;
    margin-left: 16px;
    border-radius: var(--gy-radius-md);
}

:deep(.t-loading .t-loading__gradient-conic) {
    background: conic-gradient(from 90deg at 50% 50%, var(--gy-surface) 0deg, var(--gy-text-secondary) 360deg);
}
</style>
