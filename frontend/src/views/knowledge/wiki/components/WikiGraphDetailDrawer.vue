<template>
  <t-drawer
    :visible="visible"
    :header="page?.title || ''"
    size="480px"
    :footer="false"
    placement="right"
    :show-overlay="false"
    :close-btn="true"
    destroy-on-close
    class="wiki-graph-drawer"
    @update:visible="(val: boolean) => emit('update:visible', val)"
  >
    <template v-if="page">
      <div class="wiki-reader-meta wiki-graph-drawer-meta">
        <t-tag size="small" :theme="getTypeTheme(page.page_type)" variant="light-outline">
          {{ getTypeLabel(page.page_type) }}
        </t-tag>
        <span class="wiki-reader-meta-text">
          {{ $t('knowledgeEditor.wikiBrowser.version', { ver: page.version }) }}
        </span>
        <t-button
          v-if="graphMode === 'ego' && graphCenter !== page.slug"
          size="small"
          variant="outline"
          theme="default"
          class="wiki-drawer-action-push"
          :disabled="!canBloom"
          @click="emit('bloom-neighbors', page.slug)"
        >
          {{ $t('knowledgeEditor.wikiBrowser.bloomNeighbors') }}
        </t-button>
        <t-button
          v-if="graphMode !== 'ego' || graphCenter !== page.slug"
          size="small"
          variant="outline"
          theme="primary"
          :class="{ 'wiki-drawer-action-push': !(graphMode === 'ego' && graphCenter !== page.slug) }"
          @click="emit('expand-neighbors', page.slug)"
        >
          {{ $t('knowledgeEditor.wikiBrowser.expandNeighbors') }}
        </t-button>
      </div>
      <div v-if="neighborHint" class="wiki-drawer-neighbor-hint">
        {{ neighborHint }}
      </div>
      <!-- eslint-disable-next-line vue/no-v-html -->
      <div :ref="bindDrawerBody" class="wiki-reader-body" v-html="renderedHtml" @click="emit('content-click', $event)"></div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import type { ComponentPublicInstance } from 'vue'
import type { WikiPage } from '@/api/wiki'

const props = defineProps<{
  visible: boolean
  page: WikiPage | null
  graphMode: 'overview' | 'ego'
  graphCenter: string
  canBloom: boolean
  neighborHint: string
  renderedHtml: string
  getTypeTheme: (type: string) => 'primary' | 'success' | 'warning' | 'danger' | 'default'
  getTypeLabel: (type: string) => string
  setDrawerBodyEl: (el: HTMLElement | null) => void
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'bloom-neighbors', slug: string): void
  (e: 'expand-neighbors', slug: string): void
  (e: 'content-click', event: MouseEvent): void
}>()

function bindDrawerBody(el: Element | ComponentPublicInstance | null) {
  props.setDrawerBodyEl(el instanceof HTMLElement ? el : null)
}
</script>
