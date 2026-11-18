<template>
  <t-dialog
    :visible="visible"
    :header="$t('knowledgeEditor.wikiBrowser.newPageTitle')"
    :confirm-btn="{ content: $t('common.confirm'), loading: creating }"
    :cancel-btn="$t('common.cancel')"
    width="520px"
    @update:visible="(val: boolean) => emit('update:visible', val)"
    @confirm="emit('confirm')"
  >
    <div class="wiki-create-page-form">
      <div class="wiki-create-page-field">
        <label for="wiki-create-page-title">{{ $t('knowledgeEditor.wikiBrowser.newPageTitleLabel') }}</label>
        <t-input
          id="wiki-create-page-title"
          :model-value="form.title"
          :placeholder="$t('knowledgeEditor.wikiBrowser.newPageTitlePlaceholder')"
          @update:model-value="(v: string | number) => emit('update:title', String(v ?? ''))"
        />
      </div>
      <div class="wiki-create-page-field">
        <label for="wiki-create-page-slug">{{ $t('knowledgeEditor.wikiBrowser.newPageSlugLabel') }}</label>
        <t-input
          id="wiki-create-page-slug"
          :model-value="form.slug"
          :placeholder="$t('knowledgeEditor.wikiBrowser.newPageSlugPlaceholder')"
          @update:model-value="(v: string | number) => emit('update:slug', String(v ?? ''))"
        />
        <div class="wiki-create-page-hint">{{ $t('knowledgeEditor.wikiBrowser.newPageSlugHint') }}</div>
      </div>
      <div class="wiki-create-page-field">
        <label for="wiki-create-page-type">{{ $t('knowledgeEditor.wikiBrowser.newPageTypeLabel') }}</label>
        <t-select
          id="wiki-create-page-type"
          :model-value="form.pageType"
          @update:model-value="(v: unknown) => emit('update:pageType', String(v ?? 'concept'))"
        >
          <t-option value="concept" :label="$t('knowledgeEditor.wikiBrowser.filterConcept')" />
          <t-option value="entity" :label="$t('knowledgeEditor.wikiBrowser.filterEntity')" />
          <t-option value="synthesis" :label="$t('knowledgeEditor.wikiBrowser.filterSynthesis')" />
          <t-option value="comparison" :label="$t('knowledgeEditor.wikiBrowser.filterComparison')" />
        </t-select>
      </div>
      <div class="wiki-create-page-field">
        <label for="wiki-create-page-content">{{ $t('knowledgeEditor.wikiBrowser.newPageContentLabel') }}</label>
        <t-textarea
          id="wiki-create-page-content"
          :model-value="form.content"
          :autosize="{ minRows: 6, maxRows: 16 }"
          :placeholder="$t('knowledgeEditor.wikiBrowser.editContentPlaceholder')"
          @update:model-value="(v: string | number) => emit('update:content', String(v ?? ''))"
        />
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import type { WikiCreatePageFormState } from '../composables/useWikiPageEditor'

defineProps<{
  visible: boolean
  creating: boolean
  form: WikiCreatePageFormState
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'update:title', val: string): void
  (e: 'update:slug', val: string): void
  (e: 'update:pageType', val: string): void
  (e: 'update:content', val: string): void
  (e: 'confirm'): void
}>()
</script>
