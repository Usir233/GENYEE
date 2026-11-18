<script setup lang="ts">
import { SKILL_ICON, type MentionItem } from '@/types/mention';
import type { SelectedChipDisplayItem, UploadedImagePreview } from './types';

defineProps<{
  uploadedImages: UploadedImagePreview[];
  embeddedMode: boolean;
  allSelectedItems: SelectedChipDisplayItem[];
}>();

const emit = defineEmits<{
  removeImage: [index: number];
  removeSelectedItem: [item: MentionItem];
}>();

const getImgSrc = (url: string): string => new URL(`/src/assets/img/${url}`, import.meta.url).href;

const getMentionIcon = (item: MentionItem): string => {
  switch (item.type) {
    case 'file':
      return 'file';
    case 'tag':
      return 'tag';
    case 'mcp':
      return 'tools';
    case 'skill':
      return SKILL_ICON;
    default:
      return 'folder';
  }
};

const getMentionChipClass = (item: MentionItem): string => {
  if (item.type === 'kb') return item.kbType === 'faq' ? 'mention-chip--faq' : 'mention-chip--kb';
  return `mention-chip--${item.type}`;
};
</script>

<template>
  <div v-if="uploadedImages.length > 0" class="image-preview-bar">
    <div v-for="(img, idx) in uploadedImages" :key="idx" class="image-preview-item">
      <img :src="img.preview" class="image-preview-thumb" alt="" />
      <button
        type="button"
        class="image-preview-remove"
        :aria-label="$t('common.remove')"
        @click="emit('removeImage', idx)"
      >
        ×
      </button>
    </div>
  </div>

  <div v-if="$slots.chips || (!embeddedMode && allSelectedItems.length > 0)" class="selected-tags-inline">
    <slot name="chips" />
    <span
      v-for="item in (embeddedMode ? [] : allSelectedItems)"
      :key="`${item.type}:${item.id}`"
      class="mention-chip"
      :class="[getMentionChipClass(item), { 'mention-chip--agent': item.isAgentConfigured }]"
    >
      <span class="mention-chip__icon-wrap" :class="{ 'has-org': item.org_name }">
        <span class="mention-chip__icon">
          <t-icon v-if="item.type === 'kb'" :name="item.kbType === 'faq' ? 'chat-bubble-help' : 'folder'" />
          <t-icon v-else :name="getMentionIcon(item)" />
        </span>
        <span v-if="item.org_name" class="mention-chip__org-badge">
          <img
            :src="getImgSrc(item.type === 'file' ? 'organization-grey.svg' : 'organization-green.svg')"
            class="mention-chip__org-img"
            alt=""
            aria-hidden="true"
          />
        </span>
      </span>
      <span class="mention-chip__name" :title="item.name">{{ item.name }}</span>
      <button
        type="button"
        class="mention-chip__remove"
        :aria-label="$t('common.remove')"
        @click.stop="emit('removeSelectedItem', item)"
      >
        ×
      </button>
    </span>
  </div>
</template>

<style scoped lang="less">
@import '../css/chat-resource-chips.less';

.image-preview-bar {
  display: flex;
  gap: 8px;
  padding: 8px 12px 4px;
  flex-wrap: wrap;
}

.image-preview-item {
  position: relative;
  width: 60px;
  height: 60px;
  border-radius: var(--app-radius-md);
  overflow: hidden;
  border: 1px solid var(--gy-border-subtle);

  .image-preview-thumb {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .image-preview-remove {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 16px;
    height: 16px;
    padding: 0;
    border: 0;
    background: color-mix(in oklab, var(--td-gray-color-14) 58%, transparent);
    color: var(--gy-text-inverse);
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: var(--app-text-sm);
    cursor: pointer;
    line-height: 1;

    &:hover {
      background: color-mix(in oklab, var(--td-gray-color-14) 76%, transparent);
    }
  }
}

.selected-tags-inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 12px 16px 0;
  background: var(--td-bg-color-container);
  border-radius: 11px 11px 0 0;
}

.mention-chip {
  .chat-resource-chip-surface();

  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 26px;
  padding: 3px 7px 3px 6px;
  border-radius: var(--td-radius-medium);
  box-sizing: border-box;
  font-size: var(--app-text-sm);
  font-weight: 500;
  cursor: default;
  transition: background var(--app-motion-fast), border-color var(--app-motion-fast);
  line-height: 18px;

  &:hover {
    .chat-resource-chip-hover();
  }
}

.mention-chip__icon-wrap {
  position: relative;
  display: inline-flex;
  width: 16px;
  height: 16px;
  flex: 0 1 auto;
  min-width: 0;
  align-items: center;
  justify-content: center;
}

.mention-chip__icon {
  font-size: var(--app-text-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  color: inherit;
}

.mention-chip__org-badge {
  position: absolute;
  right: -1px;
  bottom: -1px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  box-shadow: 0 0 0 1px var(--gy-border-overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

.mention-chip__org-img {
  width: 5px;
  height: 5px;
  object-fit: contain;
}

.mention-chip__name {
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: currentColor;
}

.mention-chip__remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 1px;
  padding: 0;
  border: 0;
  background: transparent;
  border-radius: 50%;
  font-size: var(--app-text-base);
  line-height: 1;
  font-weight: 400;
  cursor: pointer;
  opacity: 0.5;
  transition: opacity var(--app-motion-fast), background var(--app-motion-fast), color var(--app-motion-fast);
  color: currentColor;
  flex-shrink: 0;
}

.mention-chip:hover .mention-chip__remove {
  opacity: 0.85;
}

.mention-chip__remove:hover {
  opacity: 1;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-primary);
}

.mention-chip--kb,
.mention-chip--faq,
.mention-chip--file,
.mention-chip--tag,
.mention-chip--mcp,
.mention-chip--tool {
  color: var(--td-text-color-primary);
}

.mention-chip--kb .mention-chip__icon-wrap {
  color: var(--td-brand-color);
}

.mention-chip--faq .mention-chip__icon-wrap {
  color: var(--genyee-faq-color, var(--gy-accent-blue));
}

.mention-chip--file .mention-chip__icon-wrap {
  color: var(--td-text-color-secondary);
}

.mention-chip--tag .mention-chip__icon-wrap {
  color: var(--gy-accent-purple);
}

.mention-chip--mcp .mention-chip__icon-wrap {
  color: var(--gy-accent-teal);
}

.mention-chip--tool .mention-chip__icon-wrap {
  color: var(--gy-warning);
}

.mention-chip--agent {
  border-style: dashed;
  border-color: var(--td-component-border);
}
</style>
