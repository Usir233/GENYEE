<template>
    <div ref="containerRef" class="user_msg_container" :class="{ 'is-embedded': embeddedMode, 'is-actions-pinned': rewindConfirmOpen }">
        <div v-if="mentioned_items && mentioned_items.length > 0" class="mentioned_items">
            <span v-for="item in mentioned_items" :key="item.id" class="mentioned_tag" :class="[mentionTagClass(item)]">
                <span class="tag_icon">
                    <t-icon v-if="item.type === 'kb'" :name="item.kb_type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                    <t-icon v-else :name="mentionTagIcon(item)" />
                </span>
                <span class="tag_name">{{ item.name }}</span>
            </span>
        </div>
        <div v-if="hasImages" class="user_images">
            <img
                v-for="(img, idx) in props.images"
                :key="idx"
                :src="img.url"
                :alt="img.caption || ''"
                class="user_image_thumb"
                @click="previewImage($event)"
            />
        </div>
        <div v-if="hasAttachments" class="user_attachments">
            <div
                v-for="(att, idx) in props.attachments"
                :key="idx"
                class="user_attachment_card"
                :class="{ 'is-previewable': canPreviewAttachment(att) }"
                :role="canPreviewAttachment(att) ? 'button' : undefined"
                :tabindex="canPreviewAttachment(att) ? 0 : undefined"
                @click="openAttachmentPreview(att)"
                @keydown.enter.prevent="openAttachmentPreview(att)"
                @keydown.space.prevent="openAttachmentPreview(att)"
            >
                <ArtifactFileIcon :file-name="att.file_name" />
                <div class="attachment_card_info">
                    <div class="attachment_card_name" :title="att.file_name">{{ att.file_name }}</div>
                    <div v-if="att.file_size" class="attachment_card_meta">{{ formatFileSize(att.file_size) }}</div>
                </div>
            </div>
        </div>
        <div class="user_msg">
            {{ content }}
        </div>
        <div v-if="timestamp || content || canFork || canRewind" class="user_msg_meta">
            <time v-if="timestamp" class="user_msg_time" :datetime="timestamp.datetime" :title="fullTimestamp">
                {{ timestamp.kind === 'today' ? timestamp.time : fullTimestamp }}
            </time>
            <div v-if="content || canFork || canRewind" class="user_msg_actions">
                <t-tooltip v-if="content" :content="t('agent.copy')">
                    <button type="button" class="user_msg_action" :aria-label="t('agent.copy')" @click="handleCopy">
                        <ChatActionIcon name="copy" />
                    </button>
                </t-tooltip>
                <t-tooltip v-if="canFork" :content="forkTooltip">
                    <button type="button" class="user_msg_action" :aria-label="forkTooltip" @click="emit('fork', messageId)">
                        <ChatActionIcon name="git-branch" />
                    </button>
                </t-tooltip>
                <t-popconfirm
                    v-if="canRewind"
                    :confirm-btn="{ content: t('chat.rewind.confirmButton'), theme: 'danger', size: 'small' }"
                    :cancel-btn="{ content: t('chat.rewind.cancelButton'), size: 'small' }"
                    theme="warning"
                    placement="top"
                    :show-arrow="false"
                    overlay-class-name="gy-popover chat-rewind-popconfirm"
                    @visible-change="(visible: boolean) => (rewindConfirmOpen = visible)"
                    @confirm="emit('rewind', messageId)"
                >
                    <template #content>
                        <div class="chat-rewind-confirm__title">{{ t('chat.rewind.confirmTitle') }}</div>
                        <div class="chat-rewind-confirm__body">{{ t('chat.rewind.confirmBody') }}</div>
                    </template>
                    <t-tooltip :content="rewindTooltip">
                        <button type="button" class="user_msg_action" :class="{ 'is-active': rewindConfirmOpen }" :aria-label="rewindTooltip" @click.stop>
                            <ChatActionIcon name="rollback" />
                        </button>
                    </t-tooltip>
                </t-popconfirm>
            </div>
        </div>
        <div v-if="steerFailed" class="steer-failure" role="status">
            <span>{{ t('input.messages.steerFailed') }}</span>
            <t-tooltip :content="t('input.steerRetry')">
                <button type="button" :aria-label="t('input.steerRetry')" @click="emit('retry-steer')"><t-icon name="refresh" /></button>
            </t-tooltip>
            <t-tooltip :content="t('common.remove')">
                <button type="button" :aria-label="t('common.remove')" @click="emit('remove-steer')"><t-icon name="close" /></button>
            </t-tooltip>
        </div>
        <PicturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg" />
    </div>
</template>
<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import ChatActionIcon from '@/components/ChatActionIcon.vue';
import PicturePreview from '@/components/PicturePreview.vue';
import { useChatAttachmentPreviewDrawer } from '@/composables/useChatAttachmentPreviewDrawer';
import { SKILL_ICON } from '@/types/mention';
import { isPreviewableAttachment, resolveAttachmentFileType } from '@/utils/attachmentPreview';
import { copyWithToast } from '@/utils/clipboard';
import { formatMessageTimestamp, getConversationTimestampModel } from '@/utils/messageTimestamp';
import { hydrateProtectedFileImages } from '@/utils/security';
import type {
    MentionedItemInput,
    MessageAttachmentDisplay,
    MessageImageDisplay,
} from '../types/chatView';
import ArtifactFileIcon from './ArtifactFileIcon.vue';

const emit = defineEmits<{
    (event: 'retry-steer'): void;
    (event: 'remove-steer'): void;
    (event: 'fork', messageId: string): void;
    (event: 'rewind', messageId: string): void;
}>();

const { t } = useI18n();

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

const props = withDefaults(
    defineProps<{
        steerFailed?: boolean;
        content?: string;
        mentioned_items?: MentionedItemInput[];
        images?: MessageImageDisplay[];
        attachments?: MessageAttachmentDisplay[];
        channel?: string;
        embeddedMode?: boolean;
        sessionId?: string;
        messageId?: string;
        createdAt?: string;
        canFork?: boolean;
        canRewind?: boolean;
    }>(),
    {
        steerFailed: false,
        content: '',
        mentioned_items: () => [],
        images: () => [],
        attachments: () => [],
        channel: '',
        embeddedMode: false,
        sessionId: '',
        messageId: '',
        createdAt: '',
        canFork: false,
        canRewind: false,
    },
);

const canFork = computed(() => props.canFork === true && !props.embeddedMode);
const canRewind = computed(() => props.canRewind === true && !props.embeddedMode);
const forkTooltip = computed(() => t('chat.forkFromMessageTooltip'));
const timestamp = computed(() => getConversationTimestampModel(props.createdAt));
const fullTimestamp = computed(() => formatMessageTimestamp(props.createdAt));
const handleCopy = () => copyWithToast(props.content, 'common.copySuccess', 'common.copyFailed');
const rewindTooltip = computed(() => t('chat.rewind.tooltip'));
const rewindConfirmOpen = ref(false);

const attachmentPreviewDrawer = useChatAttachmentPreviewDrawer();

const containerRef = ref<HTMLElement | null>(null);
const hasImages = computed(() => props.images && props.images.length > 0);
const hasAttachments = computed(() => props.attachments && props.attachments.length > 0);

const formatFileSize = (bytes?: number): string => {
    if (!bytes) return '';
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
};

const canPreviewAttachment = (attachment: MessageAttachmentDisplay): boolean => {
    return Boolean(props.sessionId) && isPreviewableAttachment(attachment);
};

const openAttachmentPreview = (attachment: MessageAttachmentDisplay) => {
    if (!canPreviewAttachment(attachment) || !attachmentPreviewDrawer || !attachment.id) return;
    attachmentPreviewDrawer.open({
        sessionId: props.sessionId,
        attachmentId: attachment.id,
        fileName: attachment.file_name,
        fileType: resolveAttachmentFileType(attachment.file_name, attachment.file_type),
    });
};

const hydrateImages = async () => {
    await nextTick();
    await hydrateProtectedFileImages(containerRef.value);
};

watch(() => props.images, hydrateImages);
onMounted(hydrateImages);

const reviewImg = ref(false);
const reviewUrl = ref('');

const previewImage = (event: MouseEvent) => {
    const img = event.target as HTMLImageElement | null;
    const src = img?.src;
    if (src) {
        reviewUrl.value = src;
        reviewImg.value = true;
    }
};

const closePreImg = () => {
    reviewImg.value = false;
    reviewUrl.value = '';
};
</script>
<style scoped lang="less">
@import '../../../components/css/chat-resource-chips.less';

.user_msg_container {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 6px;
    width: 100%;
}

.mentioned_items {
    .chat-mentioned-items(flex-end);
}

.mentioned_tag {
    .chat-mentioned-tag();
}

.user_msg_container {
    &.is-embedded {
        .user_msg {
            max-width: 100%;
        }
    }
}

.user_msg {
    width: max-content;
    max-width: min(76%, 820px);
    display: flex;
    padding: 8px 12px;
    flex-direction: column;
    justify-content: center;
    align-items: flex-start;
    gap: 4px;
    flex: 1 0 0;
    border-radius: var(--gy-radius-md);
    background: var(--gy-surface-subtle);
    margin-left: auto;
    color: var(--gy-text-primary);
    font-size: var(--gy-text-lg);
    line-height: 1.6;
    text-align: left;
    word-break: break-word;
    overflow-wrap: anywhere;
    box-sizing: border-box;
    white-space: pre-wrap;
}

.user_images {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    justify-content: flex-end;
    max-width: 100%;
}

.user_attachments {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    justify-content: flex-end;
    max-width: 100%;
}

.user_attachment_card {
    display: flex;
    align-items: center;
    gap: 10px;
    box-sizing: border-box;
    width: 240px;
    max-width: 100%;
    padding: 8px 12px 8px 8px;
    border-radius: var(--gy-radius-md);
    background: var(--gy-surface-subtle);
    cursor: default;

    &.is-previewable {
        cursor: pointer;
        transition: background var(--gy-motion-fast) ease;

        &:hover {
            background: var(--gy-surface-subtle-hover);
        }
    }

    .attachment_card_info {
        flex: 1;
        min-width: 0;
        display: flex;
        flex-direction: column;
    }

    .attachment_card_name {
        font-size: var(--gy-text-md);
        font-weight: 500;
        line-height: 1.35;
        color: var(--gy-text-primary);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .attachment_card_meta {
        margin-top: 2px;
        font-size: var(--gy-text-xs);
        line-height: 1.3;
        color: var(--gy-text-placeholder);
        white-space: nowrap;
    }
}

.user_image_thumb {
    width: 120px;
    height: 120px;
    object-fit: cover;
    border-radius: var(--gy-radius-xs);
    cursor: pointer;
    border: 1px solid var(--gy-border-subtle);
    transition: opacity var(--gy-motion-base);

    &:hover {
        opacity: 0.85;
    }
}
</style>

<style scoped>
.steer-failure { display: flex; align-items: center; justify-content: flex-end; gap: 4px; font-size: var(--gy-text-xs); color: var(--gy-danger); margin: 6px 0 4px; }
.steer-failure button { display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; border: 0; border-radius: var(--gy-radius-xs); background: transparent; color: inherit; cursor: pointer; }
.steer-failure button:hover { background: var(--gy-surface-subtle); }

.user_msg_meta {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    min-height: 28px;
    padding-right: 4px;
    color: var(--gy-text-placeholder);
    opacity: 0;
    pointer-events: none;
    transition: opacity var(--gy-motion-fast) ease;
}

.user_msg_time {
    font-size: var(--gy-text-xs);
    font-variant-numeric: tabular-nums;
    line-height: 20px;
}

.user_msg_actions {
    display: flex;
    align-items: center;
    gap: 2px;
    color: color-mix(in oklab, var(--gy-text-primary) 55%, transparent);
}

.user_msg_container:hover .user_msg_meta,
.user_msg_container:focus-within .user_msg_meta,
.user_msg_container.is-actions-pinned .user_msg_meta {
    opacity: 1;
    pointer-events: auto;
}

.user_msg_action {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: 0;
    border-radius: var(--gy-radius-xs);
    background: transparent;
    color: inherit;
    font-size: var(--gy-text-lg);
    cursor: pointer;
}

.user_msg_action :deep(.chat-action-icon) {
    display: block;
    width: 16px;
    height: 16px;
    flex-shrink: 0;
}

.user_msg_action:hover,
.user_msg_action.is-active {
    color: var(--gy-text-secondary);
    background: var(--gy-surface-hover);
}

.user_msg_action:focus-visible {
    outline: 2px solid var(--gy-text-secondary);
    outline-offset: 2px;
}

@media (hover: none) {
    .user_msg_meta {
        opacity: 1;
        pointer-events: auto;
    }
}

@media (prefers-reduced-motion: reduce) {
    .user_msg_meta {
        transition: none;
    }
}
</style>
