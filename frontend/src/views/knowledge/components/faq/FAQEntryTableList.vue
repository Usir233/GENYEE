<template>
  <div class="faq-entry-list">
    <div
      v-for="entry in entries"
      :key="entry.id"
      class="faq-list-row"
      :class="{ selected: selectedRowKeys.includes(entry.id), 'is-selectable': canSelectEntries }"
      @click="emit('toggle-select', entry.id, !selectedRowKeys.includes(entry.id))"
    >
      <div v-if="canSelectEntries" class="faq-list-check" @click.stop>
        <t-checkbox
          :checked="selectedRowKeys.includes(entry.id)"
          :disabled="batchActionLoading"
          :aria-label="entry.standard_question"
          @change="(checked: boolean) => emit('toggle-select', entry.id, checked)"
        />
      </div>

      <div class="faq-list-main">
        <button
          type="button"
          class="faq-list-question"
          :aria-expanded="!!entry.expanded"
          :aria-controls="`faq-list-details-${entry.id}`"
          @click.stop="entry.expanded = !entry.expanded"
        >
          <span>{{ entry.standard_question }}</span>
          <t-icon :name="entry.expanded ? 'chevron-up' : 'chevron-down'" size="14px" />
        </button>
        <div class="faq-list-tag" @click.stop>
          <t-dropdown
            v-if="canEdit && tagList.length"
            :options="tagDropdownOptions"
            trigger="click"
            @click="(data: { value?: string | number }) => emit('change-tag', entry.id, String(data.value ?? ''))"
          >
            <t-tag size="small" variant="light" :title="getTagName(entry.tag_id) || $t('knowledgeBase.untagged')">
              <span class="faq-list-tag-text">{{ getTagName(entry.tag_id) || $t('knowledgeBase.untagged') }}</span>
            </t-tag>
          </t-dropdown>
          <t-tag
            v-else
            size="small"
            variant="light"
            :title="getTagName(entry.tag_id) || $t('knowledgeBase.untagged')"
          >
            <span class="faq-list-tag-text">{{ getTagName(entry.tag_id) || $t('knowledgeBase.untagged') }}</span>
          </t-tag>
        </div>
      </div>

      <div class="faq-list-meta">
        <div class="faq-list-counts">
          <span>{{ entry.similar_questions?.length || 0 }} {{ $t('knowledgeEditor.faq.similarQuestions') }}</span>
          <span>{{ entry.negative_questions?.length || 0 }} {{ $t('knowledgeEditor.faq.negativeQuestions') }}</span>
          <span>{{ entry.answers?.length || 0 }} {{ $t('knowledgeEditor.faq.answers') }}</span>
        </div>
        <div class="faq-list-updated">
          {{ $t('knowledgeBase.columnUpdatedAt') }}
          <time :datetime="entry.updated_at">{{ formatFAQEntryDate(entry.updated_at) }}</time>
        </div>
      </div>

      <div class="faq-list-actions" @click.stop>
        <t-tooltip
          :content="entry.is_enabled ? $t('knowledgeEditor.faq.statusEnabled') : $t('knowledgeEditor.faq.statusDisabled')"
          placement="top"
        >
          <t-switch
            size="small"
            :value="entry.is_enabled"
            :loading="!!entryStatusLoading[entry.id]"
            :disabled="!!entryStatusLoading[entry.id] || !canEdit"
            :aria-label="entry.is_enabled ? $t('knowledgeEditor.faq.statusEnabled') : $t('knowledgeEditor.faq.statusDisabled')"
            @change="(value: boolean) => emit('change-status', entry, value)"
          />
        </t-tooltip>
        <t-popup
          v-if="canEdit"
          v-model="entry.showMore"
          overlayClassName="card-more-popup"
          trigger="click"
          destroy-on-close
          placement="bottom-right"
        >
          <button type="button" class="card-more-btn faq-list-more" :aria-label="$t('knowledgeBase.columnActions')">
            <t-icon name="more" size="16px" />
          </button>
          <template #content>
            <div class="popup-menu" @click.stop>
              <button type="button" class="popup-menu-item" @click.stop="emit('edit', entry)">
                <t-icon class="menu-icon" name="edit" />
                <span>{{ $t('common.edit') }}</span>
              </button>
              <button type="button" class="popup-menu-item delete" @click.stop="emit('delete', entry)">
                <t-icon class="menu-icon" name="delete" />
                <span>{{ $t('common.delete') }}</span>
              </button>
            </div>
          </template>
        </t-popup>
      </div>

      <div v-if="entry.expanded" :id="`faq-list-details-${entry.id}`" class="faq-list-details" @click.stop>
        <div
          v-for="section in detailSections(entry)"
          :key="section.key"
          class="faq-list-detail-section"
        >
          <div class="faq-list-detail-label">{{ $t(`knowledgeEditor.faq.${section.key}`) }}</div>
          <ul>
            <li v-for="(value, index) in section.values" :key="index">{{ value }}</li>
          </ul>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import {
  formatFAQEntryDate,
  type FAQDropdownItem,
  type FAQEntry,
  type FAQTagItem,
} from '../../composables/useFAQEntries'

defineProps<{
  entries: FAQEntry[]
  selectedRowKeys: number[]
  canSelectEntries: boolean
  batchActionLoading: boolean
  canEdit: boolean
  tagList: FAQTagItem[]
  tagDropdownOptions: FAQDropdownItem[]
  entryStatusLoading: Record<number, boolean>
  getTagName: (tagId?: number) => string
}>()

const emit = defineEmits<{
  (e: 'toggle-select', entryId: number, checked: boolean): void
  (e: 'edit', entry: FAQEntry): void
  (e: 'delete', entry: FAQEntry): void
  (e: 'change-tag', entryId: number, tagValue: string): void
  (e: 'change-status', entry: FAQEntry, isEnabled: boolean): void
}>()

function detailSections(entry: FAQEntry): Array<{ key: string; values: string[] }> {
  return [
    { key: 'similarQuestions', values: entry.similar_questions || [] },
    { key: 'negativeQuestions', values: entry.negative_questions || [] },
    { key: 'answers', values: entry.answers || [] },
  ].filter((section) => section.values.length > 0)
}
</script>
