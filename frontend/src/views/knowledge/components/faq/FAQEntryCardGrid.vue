<template>
  <div :ref="bindCardListRef" class="faq-card-list">
    <div
      v-for="entry in entries"
      :key="entry.id"
      class="faq-card"
      :class="{ selected: selectedRowKeys.includes(entry.id), 'is-selectable': canSelectEntries }"
      @click="emit('toggle-select', entry.id, !selectedRowKeys.includes(entry.id))"
    >
      <div class="faq-card-header">
        <div class="faq-header-top">
          <div class="faq-question" :title="entry.standard_question">
            {{ entry.standard_question }}
          </div>
          <div class="faq-card-actions">
            <t-popup
              v-if="canEdit"
              v-model="entry.showMore"
              overlayClassName="card-more-popup"
              trigger="click"
              destroy-on-close
              placement="bottom-right"
              @visible-change="(visible: boolean) => (entry.showMore = visible)"
            >
              <button
                type="button"
                class="card-more-btn"
                :aria-label="$t('knowledgeBase.columnActions')"
                @click.stop
              >
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
        </div>
      </div>

      <div class="faq-card-body">
        <div v-if="entry.similar_questions?.length" class="faq-section similar">
          <button
            type="button"
            class="faq-section-label clickable"
            :aria-expanded="!entry.similarCollapsed"
            @click.stop="entry.similarCollapsed = !entry.similarCollapsed"
          >
            <span>{{ $t('knowledgeEditor.faq.similarQuestions') }}</span>
            <span class="section-count">({{ entry.similar_questions.length }})</span>
            <t-icon
              :name="entry.similarCollapsed ? 'chevron-right' : 'chevron-down'"
              class="collapse-icon"
            />
          </button>
          <Transition name="slide-down">
            <div v-if="!entry.similarCollapsed" class="faq-tags">
              <FAQTagTooltip
                v-for="question in entry.similar_questions"
                :key="question"
                :content="question"
                type="similar"
                placement="top"
              >
                <t-tag size="small" variant="light-outline" class="question-tag">
                  {{ question }}
                </t-tag>
              </FAQTagTooltip>
            </div>
          </Transition>
        </div>

        <div v-if="entry.negative_questions?.length" class="faq-section negative">
          <button
            type="button"
            class="faq-section-label clickable"
            :aria-expanded="!entry.negativeCollapsed"
            @click.stop="entry.negativeCollapsed = !entry.negativeCollapsed"
          >
            <span>{{ $t('knowledgeEditor.faq.negativeQuestions') }}</span>
            <span class="section-count">({{ entry.negative_questions.length }})</span>
            <t-icon
              :name="entry.negativeCollapsed ? 'chevron-right' : 'chevron-down'"
              class="collapse-icon"
            />
          </button>
          <Transition name="slide-down">
            <div v-if="!entry.negativeCollapsed" class="faq-tags">
              <FAQTagTooltip
                v-for="question in entry.negative_questions"
                :key="question"
                :content="question"
                type="negative"
                placement="top"
              >
                <t-tag size="small" theme="warning" variant="light-outline" class="question-tag">
                  {{ question }}
                </t-tag>
              </FAQTagTooltip>
            </div>
          </Transition>
        </div>

        <div class="faq-section answers">
          <button
            type="button"
            class="faq-section-label clickable"
            :aria-expanded="!entry.answersCollapsed"
            @click.stop="entry.answersCollapsed = !entry.answersCollapsed"
          >
            <span>{{ $t('knowledgeEditor.faq.answers') }}</span>
            <span v-if="entry.answers?.length" class="section-count">({{ entry.answers.length }})</span>
            <t-icon
              :name="entry.answersCollapsed ? 'chevron-right' : 'chevron-down'"
              class="collapse-icon"
            />
          </button>
          <Transition name="slide-down">
            <div v-if="!entry.answersCollapsed" class="faq-tags">
              <FAQTagTooltip
                v-for="answer in entry.answers"
                :key="answer"
                :content="answer"
                type="answer"
                placement="top"
              >
                <t-tag size="small" theme="success" variant="light-outline" class="question-tag">
                  {{ answer }}
                </t-tag>
              </FAQTagTooltip>
            </div>
          </Transition>
        </div>
      </div>

      <div class="faq-card-footer">
        <div class="faq-card-tag" @click.stop>
          <template v-if="canEdit && tagList.length">
            <t-dropdown
              :options="tagDropdownOptions"
              trigger="click"
              @click="(data: { value?: string | number }) => emit('change-tag', entry.id, String(data.value ?? ''))"
            >
              <t-tag size="small" variant="light-outline" class="faq-tag-chip">
                <span class="tag-text">{{ getTagName(entry.tag_id) || $t('knowledgeBase.untagged') }}</span>
              </t-tag>
            </t-dropdown>
          </template>
          <template v-else>
            <t-tag size="small" variant="light-outline" class="faq-tag-chip">
              <span class="tag-text">{{ getTagName(entry.tag_id) || $t('knowledgeBase.untagged') }}</span>
            </t-tag>
          </template>
        </div>
        <div class="faq-card-status" @click.stop>
          <t-tooltip
            :content="entry.is_enabled ? $t('knowledgeEditor.faq.statusEnabled') : $t('knowledgeEditor.faq.statusDisabled')"
            placement="top"
          >
            <div class="status-item-compact">
              <t-switch
                :key="`${entry.id}-${entry.is_enabled}`"
                size="small"
                :value="entry.is_enabled"
                :loading="!!entryStatusLoading[entry.id]"
                :disabled="!!entryStatusLoading[entry.id] || !canEdit"
                :aria-label="entry.is_enabled ? $t('knowledgeEditor.faq.statusEnabled') : $t('knowledgeEditor.faq.statusDisabled')"
                @click.stop
                @change="(value: boolean) => emit('change-status', entry, value)"
              />
            </div>
          </t-tooltip>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { ComponentPublicInstance } from 'vue'
import FAQTagTooltip from '@/components/FAQTagTooltip.vue'
import type { FAQDropdownItem, FAQEntry, FAQTagItem } from '../../composables/useFAQEntries'

const props = defineProps<{
  entries: FAQEntry[]
  selectedRowKeys: number[]
  canSelectEntries: boolean
  canEdit: boolean
  tagList: FAQTagItem[]
  tagDropdownOptions: FAQDropdownItem[]
  entryStatusLoading: Record<number, boolean>
  getTagName: (tagId?: number) => string
  setCardListEl: (el: HTMLElement | null) => void
}>()

const emit = defineEmits<{
  (e: 'toggle-select', entryId: number, checked: boolean): void
  (e: 'edit', entry: FAQEntry): void
  (e: 'delete', entry: FAQEntry): void
  (e: 'change-tag', entryId: number, tagValue: string): void
  (e: 'change-status', entry: FAQEntry, isEnabled: boolean): void
}>()

function bindCardListRef(el: Element | ComponentPublicInstance | null) {
  props.setCardListEl(el instanceof HTMLElement ? el : null)
}
</script>
