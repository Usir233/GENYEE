<template>
  <!-- Mount on demand: a hidden TDesign drawer steals focus when each response mounts. -->
  <t-drawer
    v-if="visible"
    :visible="visible"
    :header="page?.title || ''"
    size="480px"
    :footer="false"
    placement="right"
    attach="body"
    :show-overlay="true"
    :close-btn="true"
    :close-on-overlay-click="true"
    class="wiki-graph-drawer"
    @update:visible="(val: boolean) => emit('update:visible', val)"
  >
    <template v-if="page">
      <div class="wiki-reader-meta">
        <div class="wiki-reader-meta__tags">
          <t-tag size="small" :theme="(getTypeTheme(page.page_type || '') as 'primary' | 'warning' | 'success' | 'default')" variant="light-outline">
            {{ getTypeLabel(page.page_type || '') }}
          </t-tag>
          <span class="wiki-reader-meta-text">
            {{ $t('knowledgeEditor.wikiBrowser.version', { ver: page.version || 1 }) }}
          </span>
        </div>
        <t-link theme="primary" hover="color" :href="wikiGraphHref" target="_blank" rel="noopener noreferrer">
          <template #prefixIcon><t-icon name="chart-bubble" /></template>
          {{ $t('knowledgeEditor.wikiBrowser.viewInGraph') }}
        </t-link>
      </div>
      <div ref="bodyEl" class="wiki-reader-body" v-html="contentHtml" @click="emit('body-click', $event)"></div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import type { WikiPage } from '@/api/wiki';

defineProps<{
  visible: boolean;
  page: WikiPage | null;
  contentHtml: string;
  wikiGraphHref: string;
  getTypeTheme: (type: string) => string;
  getTypeLabel: (type: string) => string;
}>();

const emit = defineEmits<{
  (event: 'update:visible', visible: boolean): void;
  (event: 'body-click', evt: MouseEvent): void;
  (event: 'body-el', el: HTMLElement | null): void;
}>();

const bodyEl = ref<HTMLElement | null>(null);
watch(bodyEl, (el) => emit('body-el', el), { immediate: true });
</script>

<style scoped lang="less">
.wiki-reader-meta {
  margin-bottom: 16px;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.wiki-reader-meta__tags {
  display: flex;
  align-items: center;
  gap: 12px;
}
</style>
