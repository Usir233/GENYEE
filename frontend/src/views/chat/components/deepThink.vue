<template>
    <div class="deep-think">
        <button
            type="button"
            class="think-header"
            :aria-expanded="!isFold || Boolean(deepSession?.thinking)"
            @click="toggleFold"
        >
            <span class="think-title">
                <span v-if="deepSession?.thinking" class="thinking-status">
                    <span class="thinking-indicator">
                        <span class="indicator-dot"></span>
                        <span class="indicator-ring"></span>
                    </span>
                    <span class="thinking-text">{{ $t('chat.thinking') }}</span>
                </span>
                <span v-else class="done-status">
                    <img class="done-icon" src="@/assets/img/Frame3718.svg" :alt="$t('chat.deepThoughtAlt')" />
                    <span class="done-text">{{ $t('chat.deepThoughtCompleted') }}</span>
                </span>
            </span>
            <span class="toggle-icon-wrapper">
                <t-icon :name="isFold ? 'chevron-down' : 'chevron-up'" class="toggle-icon" />
            </span>
        </button>
        <div v-show="!isFold || deepSession?.thinking" class="think-content">
            <div ref="contentInnerRef" class="content-inner">{{ deepSession?.thinkContent }}</div>
        </div>
    </div>
</template>
<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue';

export interface DeepThinkSession {
    thinking?: boolean;
    thinkContent?: string;
}

const isFold = ref(false);
const contentInnerRef = ref<HTMLElement | null>(null);

const props = defineProps<{
    deepSession?: DeepThinkSession;
}>();

onMounted(() => {
    if (props.deepSession?.thinking === false) {
        isFold.value = true;
    }
});

watch(
    () => props.deepSession?.thinking,
    (newVal, oldVal) => {
        if (oldVal === true && newVal === false) {
            isFold.value = true;
        }
    },
);

watch(
    () => props.deepSession?.thinkContent,
    () => {
        if (props.deepSession?.thinking) {
            nextTick(() => {
                if (contentInnerRef.value) {
                    contentInnerRef.value.scrollTop = contentInnerRef.value.scrollHeight;
                }
            });
        }
    },
);

const toggleFold = () => {
    if (!props.deepSession?.thinking) {
        isFold.value = !isFold.value;
    }
};
</script>
<style lang="less" scoped>
.deep-think {
    display: flex;
    flex-direction: column;
    font-size: var(--gy-text-xs);
    width: 100%;
    border-radius: var(--gy-radius-md);
    background-color: var(--gy-surface);
    border: 0.5px solid var(--gy-border-stroke);
    box-shadow: 0 2px 4px color-mix(in oklab, var(--gy-brand) 8%, transparent);
    overflow: hidden;
    box-sizing: border-box;
    transition: border-color var(--gy-motion-base) cubic-bezier(0.4, 0, 0.2, 1),
        box-shadow var(--gy-motion-base) cubic-bezier(0.4, 0, 0.2, 1);
    margin: -8px 0 10px 0;

    .think-header {
        display: flex;
        justify-content: space-between;
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
        user-select: none;

        &:hover {
            background-color: color-mix(in oklab, var(--gy-brand) 4%, transparent);
        }

        .think-title {
            display: flex;
            align-items: center;
        }

        .thinking-status {
            display: flex;
            align-items: center;

            .thinking-indicator {
                position: relative;
                width: 16px;
                height: 16px;
                margin-right: 8px;
                display: flex;
                align-items: center;
                justify-content: center;

                .indicator-dot {
                    width: 6px;
                    height: 6px;
                    border-radius: var(--gy-radius-circle);
                    background: var(--gy-brand);
                    animation: pulse-dot 1.8s ease-in-out infinite;
                }

                .indicator-ring {
                    position: absolute;
                    inset: 0;
                    border-radius: var(--gy-radius-circle);
                    border: 1.5px solid var(--gy-brand);
                    opacity: 0;
                    animation: pulse-ring 1.8s ease-out infinite;
                }
            }

            .thinking-text {
                font-size: var(--gy-text-xs);
                color: var(--gy-text-primary);
                white-space: nowrap;
            }
        }

        .done-status {
            display: flex;
            align-items: center;

            .done-icon {
                width: 16px;
                height: 16px;
                margin-right: 8px;
            }

            .done-text {
                font-size: var(--gy-text-xs);
                color: var(--gy-text-primary);
                white-space: nowrap;
            }
        }

        .toggle-icon-wrapper {
            font-size: var(--gy-text-base);
            padding: 0 2px 1px 2px;
            color: var(--gy-brand);

            .toggle-icon {
                transition: transform var(--gy-motion-base);
            }
        }
    }

    .think-content {
        border-top: 1px solid var(--gy-surface-subtle);

        .content-inner {
            padding: 8px 14px;
            font-size: var(--gy-text-xs);
            line-height: 1.6;
            color: var(--gy-text-secondary);
            max-height: 200px;
            overflow-y: auto;
            word-break: break-word;
            white-space: pre-wrap;
            scrollbar-width: thin;
            scrollbar-color: var(--gy-scrollbar-thumb) var(--gy-scrollbar-track);

            @supports not (scrollbar-color: auto) {
                scrollbar-width: auto;

                &::-webkit-scrollbar {
                    width: 4px;
                }

                &::-webkit-scrollbar-track {
                    background: var(--gy-scrollbar-track);
                }

                &::-webkit-scrollbar-thumb {
                    background: var(--gy-scrollbar-thumb);
                    border-radius: var(--gy-radius-xs);
                }
            }
        }
    }
}

@keyframes pulse-dot {
    0%,
    100% {
        transform: scale(0.85);
        opacity: 0.6;
    }
    50% {
        transform: scale(1.1);
        opacity: 1;
    }
}

@keyframes pulse-ring {
    0% {
        transform: scale(0.5);
        opacity: 0.6;
    }
    100% {
        transform: scale(1.2);
        opacity: 0;
    }
}
</style>
