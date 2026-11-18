<template>
  <t-drawer
    :visible="visible"
    :header="editorMode === 'create' ? $t('knowledgeEditor.faq.editorCreate') : $t('knowledgeEditor.faq.editorEdit')"
    :close-btn="true"
    size="520px"
    placement="right"
    class="faq-editor-drawer"
    @update:visible="(val: boolean) => emit('update:visible', val)"
    @close="emit('close')"
  >
    <div class="faq-editor-drawer-content">
      <t-form
        :ref="bindFormRef"
        :data="editorForm"
        :rules="editorRules"
        layout="vertical"
        :label-width="0"
        class="faq-editor-form"
      >
        <div class="settings-group">
          <div class="setting-row vertical setting-row-primary">
            <div class="setting-info">
              <label class="required-label" for="faq-editor-standard-question">
                {{ $t('knowledgeEditor.faq.standardQuestion') }}
                <span class="required-mark">*</span>
              </label>
              <p class="desc">{{ $t('knowledgeEditor.faq.standardQuestionDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                id="faq-editor-standard-question"
                :model-value="editorForm.standard_question"
                :maxlength="200"
                class="full-width-input"
                @update:model-value="(v: string | number) => emit('update:standardQuestion', String(v ?? ''))"
              />
            </div>
          </div>

          <div class="setting-row vertical setting-row-optional setting-row-similar">
            <div class="setting-info">
              <div class="setting-label-row">
                <label class="optional-label" for="faq-editor-similar-input">
                  {{ $t('knowledgeEditor.faq.similarQuestions') }}
                </label>
                <AssistantActionButton
                  v-if="editorForm.standard_question.trim()"
                  :label="$t('assistant.actions.suggestFaqQuestions')"
                  :query="$t('assistant.prompts.suggestFaqQuestions', { count: Math.max(0, 10 - editorForm.similar_questions.length) })"
                  :selection="faqAssistantSelection"
                  :disabled="savingEntry || editorForm.similar_questions.length >= 10"
                />
              </div>
              <p class="desc optional-desc">{{ $t('knowledgeEditor.faq.similarQuestionsDesc') }}</p>
            </div>
            <div class="setting-control">
              <div class="full-width-input-wrapper">
                <t-input
                  id="faq-editor-similar-input"
                  :model-value="similarInput"
                  :placeholder="$t('knowledgeEditor.faq.similarPlaceholder')"
                  class="full-width-input"
                  @update:model-value="(v: string | number) => emit('update:similarInput', String(v ?? ''))"
                  @enter="emit('add-similar')"
                />
                <t-button
                  theme="primary"
                  variant="outline"
                  :disabled="!similarInput.trim() || editorForm.similar_questions.length >= 10"
                  :aria-label="$t('knowledgeEditor.faq.similarQuestions')"
                  class="add-item-btn"
                  size="small"
                  @click="emit('add-similar')"
                >
                  <t-icon name="add" size="16px" />
                </t-button>
              </div>
              <div v-if="editorForm.similar_questions.length > 0" class="item-list">
                <div v-for="(question, index) in editorForm.similar_questions" :key="index" class="item-row">
                  <div class="item-content">{{ question }}</div>
                  <t-button
                    theme="default"
                    variant="text"
                    size="small"
                    :aria-label="$t('common.delete')"
                    class="remove-item-btn"
                    @click="emit('remove-similar', index)"
                  >
                    <t-icon name="close" size="16px" />
                  </t-button>
                </div>
              </div>
            </div>
          </div>

          <div class="setting-row vertical setting-row-optional setting-row-negative">
            <div class="setting-info">
              <label class="optional-label" for="faq-editor-negative-input">
                {{ $t('knowledgeEditor.faq.negativeQuestions') }}
              </label>
              <p class="desc optional-desc">{{ $t('knowledgeEditor.faq.negativeQuestionsDesc') }}</p>
            </div>
            <div class="setting-control">
              <div class="full-width-input-wrapper">
                <t-input
                  id="faq-editor-negative-input"
                  :model-value="negativeInput"
                  :placeholder="$t('knowledgeEditor.faq.negativePlaceholder')"
                  class="full-width-input"
                  @update:model-value="(v: string | number) => emit('update:negativeInput', String(v ?? ''))"
                  @enter="emit('add-negative')"
                />
                <t-button
                  theme="primary"
                  variant="outline"
                  :disabled="!negativeInput.trim() || editorForm.negative_questions.length >= 10"
                  :aria-label="$t('knowledgeEditor.faq.negativeQuestions')"
                  class="add-item-btn"
                  size="small"
                  @click="emit('add-negative')"
                >
                  <t-icon name="add" size="16px" />
                </t-button>
              </div>
              <div v-if="editorForm.negative_questions.length > 0" class="item-list">
                <div v-for="(question, index) in editorForm.negative_questions" :key="index" class="item-row negative">
                  <div class="item-content">{{ question }}</div>
                  <t-button
                    theme="default"
                    variant="text"
                    size="small"
                    :aria-label="$t('common.delete')"
                    class="remove-item-btn"
                    @click="emit('remove-negative', index)"
                  >
                    <t-icon name="close" size="16px" />
                  </t-button>
                </div>
              </div>
            </div>
          </div>

          <div class="setting-row vertical setting-row-primary setting-row-answer">
            <div class="setting-info">
              <label class="required-label" for="faq-editor-answer-input">
                {{ $t('knowledgeEditor.faq.answers') }}
                <span class="required-mark">*</span>
              </label>
              <p class="desc">{{ $t('knowledgeEditor.faq.answersDesc') }}</p>
            </div>
            <div class="setting-control">
              <div class="textarea-container">
                <div class="full-width-input-wrapper textarea-wrapper">
                  <div class="faq-answer-draft">
                    <t-textarea
                      id="faq-editor-answer-input"
                      :model-value="answerInput"
                      :placeholder="$t('knowledgeEditor.faq.answerPlaceholder')"
                      :autosize="{ minRows: 3, maxRows: 6 }"
                      class="full-width-textarea"
                      @update:model-value="(v: string | number) => emit('update:answerInput', String(v ?? ''))"
                      @keydown.ctrl.enter="emit('add-answer')"
                      @keydown.meta.enter="emit('add-answer')"
                    />
                    <AssistantActionButton
                      v-if="editorForm.standard_question.trim()"
                      class="faq-answer-action"
                      :label="$t(answerInput.trim() ? 'assistant.actions.refineAnswer' : 'assistant.actions.writeAnswer')"
                      :query="$t(answerInput.trim() ? 'assistant.prompts.refineFaqAnswer' : 'assistant.prompts.writeFaqAnswer')"
                      :selection="faqAnswerSelection"
                      :disabled="savingEntry || editorForm.answers.length >= 5"
                    />
                  </div>
                  <t-button
                    theme="primary"
                    variant="outline"
                    :disabled="!answerInput.trim() || editorForm.answers.length >= 5"
                    :aria-label="$t('knowledgeEditor.faq.answers')"
                    class="add-item-btn"
                    size="small"
                    @click="emit('add-answer')"
                  >
                    <t-icon name="add" size="16px" />
                  </t-button>
                </div>
                <div class="item-count">{{ editorForm.answers.length }}/5</div>
              </div>
              <div v-if="editorForm.answers.length > 0" class="item-list">
                <div v-for="(answer, index) in editorForm.answers" :key="index" class="item-row answer-row">
                  <div class="item-content">{{ answer }}</div>
                  <t-button
                    theme="default"
                    variant="text"
                    size="small"
                    :aria-label="$t('common.delete')"
                    class="remove-item-btn"
                    @click="emit('remove-answer', index)"
                  >
                    <t-icon name="close" size="16px" />
                  </t-button>
                </div>
              </div>
            </div>
          </div>

          <div class="setting-row vertical">
            <div class="setting-info">
              <label for="faq-editor-tag-select">{{ $t('knowledgeBase.tagLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.faq.tagDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-select
                id="faq-editor-tag-select"
                :model-value="editorForm.tag_id"
                class="full-width-input"
                :options="tagSelectOptions"
                clearable
                :placeholder="$t('knowledgeEditor.faq.tagPlaceholder')"
                @update:model-value="onTagSelectChange"
              />
            </div>
          </div>
        </div>
      </t-form>
    </div>

    <template #footer>
      <div class="faq-editor-drawer-footer">
        <t-button theme="default" variant="outline" @click="emit('update:visible', false)">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" :loading="savingEntry" @click="emit('submit')">
          {{ editorMode === 'create' ? $t('knowledgeEditor.faq.editorCreate') : $t('common.save') }}
        </t-button>
      </div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import type { ComponentPublicInstance } from 'vue'
import type { FormInstanceFunctions, FormRules } from 'tdesign-vue-next'
import AssistantActionButton from '@/components/assistant/AssistantActionButton.vue'
import type { FAQEntryPayload, FAQSelectOption } from '../../composables/useFAQEntries'

const props = defineProps<{
  visible: boolean
  editorMode: 'create' | 'edit'
  editorForm: FAQEntryPayload
  editorRules: FormRules<FAQEntryPayload>
  savingEntry: boolean
  similarInput: string
  negativeInput: string
  answerInput: string
  faqAssistantSelection: { field: string; text: string }
  faqAnswerSelection: { field: string; text: string }
  tagSelectOptions: FAQSelectOption[]
  setFormRef: (inst: FormInstanceFunctions | undefined) => void
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'close'): void
  (e: 'update:standardQuestion', val: string): void
  (e: 'update:tagId', val: number | undefined): void
  (e: 'update:similarInput', val: string): void
  (e: 'update:negativeInput', val: string): void
  (e: 'update:answerInput', val: string): void
  (e: 'add-similar'): void
  (e: 'remove-similar', index: number): void
  (e: 'add-negative'): void
  (e: 'remove-negative', index: number): void
  (e: 'add-answer'): void
  (e: 'remove-answer', index: number): void
  (e: 'submit'): void
}>()

function onTagSelectChange(val: unknown) {
  if (val === undefined || val === null || val === '') {
    emit('update:tagId', undefined)
    return
  }
  const num = Number(val)
  emit('update:tagId', Number.isFinite(num) ? num : undefined)
}

function bindFormRef(inst: Element | ComponentPublicInstance | null) {
  if (inst && typeof inst === 'object' && 'validate' in inst) {
    props.setFormRef(inst as unknown as FormInstanceFunctions)
  } else {
    props.setFormRef(undefined)
  }
}
</script>

<style lang="less">
.faq-editor-drawer {
  .t-drawer__body {
    padding: var(--gy-space-5);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .t-drawer__header {
    padding: var(--gy-space-5) var(--gy-space-6);
    border-bottom: 1px solid var(--gy-border-subtle);
    font-size: var(--gy-text-2xl);
    font-weight: 600;
    color: var(--gy-text-primary);
  }

  .t-drawer__footer {
    padding: var(--gy-space-4) var(--gy-space-6);
    border-top: 1px solid var(--gy-border-subtle);
  }

  .faq-editor-drawer-content {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    min-height: 0;
  }

  .faq-editor-drawer-footer {
    display: flex;
    justify-content: flex-end;
    gap: var(--gy-space-3);
  }

  .faq-answer-draft {
    position: relative;
    flex: 1;
    min-width: 0;

    .faq-answer-action {
      position: absolute;
      left: var(--gy-space-2);
      bottom: var(--gy-space-2);
    }

    .t-textarea__inner {
      padding-bottom: 44px;
    }
  }

  .full-width-input-wrapper {
    display: flex;
    gap: var(--gy-space-2);
    align-items: center;
    width: 100%;

    .full-width-input,
    .full-width-textarea {
      flex: 1;
      min-width: 0;
    }

    &.textarea-wrapper {
      align-items: flex-start;
    }

    .add-item-btn {
      flex-shrink: 0;
      width: 32px;
      height: 32px;
      min-width: 32px;
      padding: 0;
      border-radius: var(--gy-radius-md);
      background: var(--gy-brand);
      border-color: var(--gy-brand);
      color: var(--gy-text-inverse);

      &:disabled {
        background: var(--gy-surface-component-disabled);
        border-color: var(--gy-border-subtle);
        color: var(--gy-text-placeholder);
      }
    }
  }

  .textarea-container {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-2);
    width: 100%;
  }

  .item-count {
    font-size: var(--gy-text-md);
    color: var(--gy-text-secondary);
    font-weight: 500;
    text-align: right;
    padding-right: var(--gy-space-10);
    line-height: 1;
  }

  .item-list {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-2);
    width: 100%;
    margin-top: var(--gy-space-2);
  }

  .item-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    background: var(--gy-surface);
    border: 1px solid var(--gy-border-subtle);
    border-radius: var(--gy-radius-md);
    transition: border-color var(--gy-motion-base) ease, background-color var(--gy-motion-base) ease;

    &.answer-row {
      align-items: flex-start;
      padding: var(--gy-space-3) 14px;
    }

    &:hover {
      background: var(--gy-surface-subtle);
      border-color: var(--gy-brand);
    }

    &.negative {
      background: var(--gy-warning-soft);
      border-color: color-mix(in oklab, var(--gy-warning) 35%, transparent);

      &:hover {
        border-color: var(--gy-warning);
      }
    }

    .item-content {
      flex: 1;
      font-size: var(--gy-text-base);
      line-height: 1.6;
      color: var(--gy-text-primary);
      white-space: pre-wrap;
      word-break: break-word;
    }

    .remove-item-btn {
      flex-shrink: 0;
      color: var(--gy-text-placeholder);
      padding: 0;
      width: 24px;
      height: 24px;
      min-width: 24px;
      border-radius: var(--gy-radius-sm);

      &:hover {
        color: var(--gy-danger);
        background: var(--gy-danger-soft);
      }
    }
  }

  .settings-group {
    display: flex;
    flex-direction: column;
  }

  .setting-row {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-3);
    padding: var(--gy-space-4) 0;
    border-bottom: 1px solid var(--gy-border-subtle);

    &:last-child {
      border-bottom: none;
    }

    &.setting-row-primary,
    &.setting-row-optional {
      padding-left: var(--gy-space-3);
      position: relative;

      &::before {
        content: '';
        position: absolute;
        left: 0;
        top: var(--gy-space-4);
        width: 3px;
        height: calc(100% - 32px);
        background: var(--gy-brand);
        border-radius: var(--gy-radius-xs);
      }
    }

    &.setting-row-negative::before {
      background: var(--gy-warning);
    }
  }

  .setting-info {
    width: 100%;

    .setting-label-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--gy-space-2);
      margin-bottom: var(--gy-space-1);
    }

    label {
      font-size: var(--gy-text-base);
      font-weight: 600;
      color: var(--gy-text-primary);
      display: inline-flex;
      align-items: center;
      gap: var(--gy-space-1);
      margin-bottom: var(--gy-space-1);
    }

    .required-mark {
      color: var(--gy-danger);
    }

    .desc {
      font-size: var(--gy-text-sm);
      color: var(--gy-text-secondary);
      margin: 0;
      line-height: 1.5;
    }
  }

  .setting-control {
    width: 100%;
    display: flex;
    flex-direction: column;
  }

  .full-width-input,
  .full-width-textarea {
    width: 100%;
  }
}
</style>
