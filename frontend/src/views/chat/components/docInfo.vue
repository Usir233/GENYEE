<template>
    <div
        v-if="session?.knowledge_references && session.knowledge_references.length"
        class="refer"
        :class="{ 'refer-timeline': timelineMode }"
    >
        <button
            v-if="!contentOnly"
            type="button"
            class="refer_header"
            :aria-expanded="showReferBox"
            @click="referBoxSwitch"
        >
            <span class="refer_title">
                <t-icon v-if="!timelineMode" name="file" class="refer-title-icon" />
                <span>{{ headerText }}</span>
                <span class="refer_show_icon">
                    <t-icon :name="showReferBox ? 'chevron-down' : 'chevron-right'" />
                </span>
            </span>
        </button>
        <div v-show="contentOnly || showReferBox" class="refer_box">
            <div v-for="(item, index) in webSearchRefs" :key="'web-' + index">
                <a
                    :href="getWebSearchUrl(item)"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="doc doc-web"
                    @click.stop
                >
                    {{ webSearchRefs.length < 2 ? getWebSearchDisplayText(item) : `${index + 1}. ${getWebSearchDisplayText(item)}` }}
                </a>
            </div>

            <div v-for="(group, gIdx) in groupedKnowledgeRefs" :key="'grp-' + gIdx" class="doc-group">
                <div class="doc-group-header">
                    <button
                        type="button"
                        class="doc-group-left"
                        :aria-expanded="Boolean(expandedGroups[group.key])"
                        @click="toggleGroup(group.key)"
                    >
                        <t-icon name="file" size="14px" class="doc-group-icon" />
                        <span class="doc-group-title" :title="group.title">{{ group.title }}</span>
                        <span class="doc-group-count">{{ $t('chat.referenceChunkCount', { count: group.chunks.length }) }}</span>
                    </button>
                    <div v-if="!embeddedMode && group.knowledgeBaseId" class="doc-group-actions" @click.stop>
                        <t-tooltip :content="$t('chat.navigateToDocument')">
                            <a
                                class="doc-group-navigate"
                                :href="getDocumentHref(group)"
                                :aria-label="$t('chat.navigateToDocument')"
                                target="_blank"
                                rel="noopener noreferrer"
                            >
                                <t-icon name="jump" size="14px" />
                            </a>
                        </t-tooltip>
                    </div>
                </div>
                <div v-show="expandedGroups[group.key]" class="doc-group-chunks">
                    <div v-for="(chunk, cIdx) in group.chunks" :key="'chunk-' + cIdx" class="doc-chunk-item">
                        <t-popup
                            overlayClassName="refer-to-layer"
                            placement="bottom-left"
                            width="400"
                            :showArrow="false"
                            trigger="click"
                        >
                            <template #content>
                                <ContentPopup :content="safeProcessContent(chunk.content)" :is-html="true" />
                            </template>
                            <span class="doc-chunk-text">
                                <span class="doc-chunk-index">{{ $t('chat.chunkLabel', { index: cIdx + 1 }) }}</span>
                                {{ truncateContent(chunk.content, 80) }}
                            </span>
                        </t-popup>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { useChatReferencesDrawer } from '@/composables/useChatReferencesDrawer';
import type { KnowledgeReferenceLike } from '@/utils/referenceSources';
import { sanitizeHTML } from '@/utils/security';
import ContentPopup from './tool-results/ContentPopup.vue';

export interface DocInfoReference extends KnowledgeReferenceLike {
    knowledge_filename?: string;
    metadata?: {
        url?: string;
        title?: string;
        [key: string]: unknown;
    };
}

export interface DocInfoGroup {
    key: string;
    title: string;
    knowledgeId?: string;
    knowledgeBaseId?: string;
    chunks: DocInfoReference[];
}

const router = useRouter();
const { t } = useI18n();
const referencesDrawer = useChatReferencesDrawer();

const props = withDefaults(
    defineProps<{
        content?: string;
        session?: { knowledge_references?: DocInfoReference[] };
        embeddedMode?: boolean;
        timelineMode?: boolean;
        contentOnly?: boolean;
    }>(),
    {
        content: '',
        session: () => ({}),
        embeddedMode: false,
        timelineMode: false,
        contentOnly: false,
    },
);

const showReferBox = ref(false);
const expandedGroups = reactive<Record<string, boolean>>({});

const referBoxSwitch = () => {
    const refs = props.session?.knowledge_references;
    if (referencesDrawer && refs?.length) {
        referencesDrawer.open({ references: refs });
        return;
    }
    showReferBox.value = !showReferBox.value;
};

const toggleGroup = (key: string) => {
    expandedGroups[key] = !expandedGroups[key];
};

const webSearchRefs = computed<DocInfoReference[]>(() => {
    if (!props.session?.knowledge_references) return [];
    return props.session.knowledge_references.filter((item) => item.chunk_type === 'web_search');
});

const knowledgeRefs = computed<DocInfoReference[]>(() => {
    if (!props.session?.knowledge_references) return [];
    return props.session.knowledge_references.filter((item) => item.chunk_type !== 'web_search');
});

const groupedKnowledgeRefs = computed<DocInfoGroup[]>(() => {
    const refs = knowledgeRefs.value;
    if (!refs.length) return [];

    const groupMap = new Map<string, DocInfoGroup>();
    for (const item of refs) {
        const key = String(item.knowledge_id || item.knowledge_title || item.id || '');
        let existing = groupMap.get(key);
        if (!existing) {
            existing = {
                key,
                title: String(item.knowledge_title || item.knowledge_filename || key),
                knowledgeId: item.knowledge_id,
                knowledgeBaseId: item.knowledge_base_id,
                chunks: [],
            };
            groupMap.set(key, existing);
        }
        existing.chunks.push(item);
    }
    return Array.from(groupMap.values());
});

const headerText = computed(() => {
    const total = props.session?.knowledge_references?.length ?? 0;
    const docCount = groupedKnowledgeRefs.value.length;
    const webCount = webSearchRefs.value.length;
    if (docCount > 0 && webCount > 0) {
        return t('chat.referencesDocAndWebCount', { docCount, webCount });
    }
    if (docCount > 0) {
        return t('chat.referencesDocCount', { count: docCount });
    }
    if (webCount > 0) {
        return t('chat.referencesWebCount', { count: webCount });
    }
    return t('chat.referencesTitle', { count: total });
});

const safeProcessContent = (content?: string): string => {
    if (!content) return '';
    const sanitized = sanitizeHTML(content);
    return sanitized.replace(/\n/g, '<br/>');
};

const truncateContent = (content: string | undefined, maxLen: number): string => {
    if (!content) return '';
    const text = content.replace(/\n/g, ' ').trim();
    if (text.length <= maxLen) return text;
    return `${text.slice(0, maxLen)}...`;
};

const getDocumentHref = (group: DocInfoGroup): string => {
    if (!group.knowledgeBaseId) return '';
    const query: Record<string, string> = {};
    if (group.knowledgeId) {
        query.knowledge_id = group.knowledgeId;
    }
    return router.resolve({
        path: `/platform/knowledge-bases/${group.knowledgeBaseId}`,
        query,
    }).href;
};

const getWebSearchUrl = (item?: DocInfoReference | null): string => {
    const rawUrl = typeof item?.metadata?.url === 'string' ? item.metadata.url.trim() : '';
    if (rawUrl && (rawUrl.startsWith('http://') || rawUrl.startsWith('https://'))) {
        return rawUrl;
    }
    const itemId = typeof item?.id === 'string' ? item.id : '';
    if (itemId && (itemId.startsWith('http://') || itemId.startsWith('https://'))) {
        return itemId;
    }
    return '#';
};

const getWebSearchDisplayText = (item: DocInfoReference): string => {
    if (item.knowledge_title) {
        return item.knowledge_title;
    }
    if (typeof item.metadata?.title === 'string' && item.metadata.title) {
        return item.metadata.title;
    }
    const url = getWebSearchUrl(item);
    if (url && url !== '#') {
        try {
            const urlObj = new URL(url);
            return urlObj.hostname;
        } catch {
            return url;
        }
    }
    return 'Web Search Result';
};
</script>
<style lang="less" scoped>
.refer {
    --refer-brand-4: color-mix(in oklab, var(--gy-brand) 4%, transparent);
    --refer-brand-8: color-mix(in oklab, var(--gy-brand) 8%, transparent);
    --refer-brand-12: color-mix(in oklab, var(--gy-brand) 12%, transparent);

    display: flex;
    flex-direction: column;
    font-size: var(--gy-text-xs);
    width: 100%;
    border-radius: var(--gy-radius-md);
    background-color: var(--gy-surface);
    border: 0.5px solid var(--gy-border-stroke);
    box-shadow: 0 2px 4px var(--refer-brand-8);
    box-sizing: border-box;
    overflow: hidden;
    transition: border-color var(--gy-motion-base) cubic-bezier(0.4, 0, 0.2, 1),
        box-shadow var(--gy-motion-base) cubic-bezier(0.4, 0, 0.2, 1);
    margin-bottom: 8px;

    &.refer-timeline {
        --td-brand-color: var(--td-text-color-placeholder);
        --gy-brand: var(--gy-text-placeholder);
        --embed-primary: var(--gy-text-placeholder);
        --refer-brand-4: color-mix(in oklab, var(--gy-text-primary) 4%, transparent);
        --refer-brand-8: color-mix(in oklab, var(--gy-text-primary) 6%, transparent);
        --refer-brand-12: color-mix(in oklab, var(--gy-text-primary) 8%, transparent);

        width: 100%;
        border-radius: 0;
        background-color: transparent;
        border: 0;
        box-shadow: none;
        margin-bottom: 0;
        font-size: var(--gy-text-md);

        .refer_header {
            padding: 0;
            font-weight: 400;
            color: var(--gy-text-secondary);

            .refer_title span {
                font-size: var(--gy-text-base);
                white-space: normal;
            }
        }

        .refer_header:hover {
            background-color: transparent;
        }

        .refer_show_icon {
            color: var(--gy-text-placeholder);
        }

        .refer_box {
            padding: 2px 0 0 0;
            border-top: 0;
        }

        .doc-group {
            margin-top: 2px;
        }

        .doc-group-header {
            padding: 2px 0;
        }

        .doc-group-title {
            color: var(--gy-text-secondary);
            font-weight: 400;
            font-size: var(--gy-text-md);
            max-width: min(520px, 100%);
        }

        .doc-group-count,
        .doc-chunk-text,
        .doc-chunk-index {
            font-size: var(--gy-text-xs);
        }

        .doc-group-icon {
            color: var(--gy-text-placeholder);
        }

        .doc-group-navigate {
            color: var(--gy-text-placeholder);
        }

        .doc {
            color: var(--gy-text-secondary);
            border-bottom-color: color-mix(in oklab, var(--gy-text-secondary) 30%, transparent);
        }

        .doc-chunk-item .doc-chunk-text:hover {
            background-color: var(--refer-brand-4);
            color: var(--gy-text-secondary);
        }
    }

    .refer_header {
        display: flex;
        align-items: center;
        width: 100%;
        padding: 6px 14px;
        border: 0;
        background: transparent;
        font: inherit;
        text-align: start;
        color: var(--gy-text-primary);
        font-weight: 500;
        cursor: pointer;

        .refer_title {
            display: inline-flex;
            align-items: center;
            gap: 4px;
            min-width: 0;

            span {
                white-space: nowrap;
                font-size: var(--gy-text-xs);
            }
        }

        .refer_show_icon {
            font-size: var(--gy-text-base);
            padding: 0 2px 1px 2px;
            color: var(--gy-brand);
            flex-shrink: 0;
        }
    }

    .refer_header:hover {
        background-color: var(--refer-brand-4);
    }

    .refer_box {
        padding: 4px 14px 8px 14px;
        flex-direction: column;
        border-top: 1px solid var(--gy-surface-subtle);
    }
}

.refer-title-icon {
    width: 16px;
    height: 16px;
    margin-right: 8px;
    flex-shrink: 0;
    color: var(--embed-primary, var(--gy-brand));
}

.doc {
    text-decoration: none;
    color: var(--gy-brand);
    cursor: pointer;
    display: inline-block;
    white-space: nowrap;
    max-width: calc(100% - 24px);
    overflow: hidden;
    text-overflow: ellipsis;
    line-height: 20px;
    padding: 2px 0;
    transition: background-color var(--gy-motion-base) ease, padding-right var(--gy-motion-base) ease;
    border-bottom: 1px dashed var(--gy-brand);

    &:hover {
        background-color: var(--refer-brand-8);
        border-radius: var(--gy-radius-xs);
        padding-right: 4px;
    }

    &.doc-web {
        white-space: normal;
        word-break: break-all;
    }
}

.doc-group {
    margin-top: 4px;

    .doc-group-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        padding: 4px;
        border-radius: var(--gy-radius-xs);
        transition: background-color var(--gy-motion-fast) ease;

        &:hover {
            background-color: var(--refer-brand-4);
        }

        .doc-group-left {
            display: flex;
            align-items: center;
            min-width: 0;
            flex: 1;
            padding: 0;
            border: 0;
            background: transparent;
            font: inherit;
            text-align: start;
            cursor: pointer;
        }

        .doc-group-icon {
            color: var(--gy-brand);
            flex-shrink: 0;
            margin-right: 6px;
        }

        .doc-group-title {
            color: var(--gy-text-primary);
            font-weight: 500;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
            max-width: 200px;
        }

        .doc-group-count {
            color: var(--gy-text-placeholder);
            font-size: var(--gy-text-2xs);
            margin-left: 6px;
            white-space: nowrap;
            flex-shrink: 0;
        }

        .doc-group-actions {
            flex-shrink: 0;
            margin-left: 8px;
        }

        .doc-group-navigate {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            width: 22px;
            height: 22px;
            border-radius: var(--gy-radius-xs);
            color: var(--gy-brand);
            cursor: pointer;
            text-decoration: none;
            transition: background-color var(--gy-motion-fast) ease;

            &:hover {
                background-color: var(--refer-brand-12);
            }
        }
    }

    .doc-group-chunks {
        padding-left: 22px;
    }
}

.doc-chunk-item {
    .doc-chunk-text {
        display: block;
        color: var(--gy-text-secondary);
        font-size: var(--gy-text-xs);
        line-height: 18px;
        padding: 3px 6px;
        border-radius: var(--gy-radius-xs);
        cursor: pointer;
        transition: background-color var(--gy-motion-fast) ease;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;

        &:hover {
            background-color: var(--refer-brand-4);
            color: var(--gy-brand);
        }

        .doc-chunk-index {
            color: var(--gy-text-placeholder);
            font-size: var(--gy-text-2xs);
            margin-right: 4px;
        }
    }
}
</style>

<style>
.refer-to-layer {
    width: 400px;
    max-width: 500px;

    .t-popup__content {
        max-height: 400px;
        max-width: 500px;
        overflow-y: auto;
        overflow-x: hidden;
        word-wrap: break-word;
        word-break: break-word;
    }
}
</style>
