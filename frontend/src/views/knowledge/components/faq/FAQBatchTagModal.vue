<template>
  <dialog
    ref="dialogRef"
    v-bind="{ closedby: 'any' }"
    :aria-labelledby="titleId"
    class="batch-tag-dialog"
    @click="handleBackdropClick"
    @close="onNativeClose"
  >
    <div class="batch-tag-modal">
      <button
        type="button"
        class="batch-tag-close-btn"
        :aria-label="$t('general.close')"
        @click="requestClose"
      >
        <svg aria-hidden="true" width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
          <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
        </svg>
      </button>

      <div class="batch-tag-container">
        <div class="batch-tag-header">
          <h2 :id="titleId" class="batch-tag-title">{{ $t('knowledgeEditor.faq.batchUpdateTag') }}</h2>
        </div>

        <div class="batch-tag-content">
          <div class="batch-tag-tip">
            <t-icon name="info-circle" size="16px" class="tip-icon" />
            <span>{{ $t('knowledgeEditor.faq.batchUpdateTagTip', { count: selectedCount }) }}</span>
          </div>
          <t-form layout="vertical" class="batch-tag-form">
            <t-form-item :label="$t('knowledgeBase.tagLabel')">
              <t-select
                :model-value="modelValue"
                :options="tagSelectOptions"
                :placeholder="$t('knowledgeBase.tagPlaceholder')"
                clearable
                filterable
                class="batch-tag-select"
                @update:model-value="onSelectChange"
              >
                <template #empty>
                  <div class="tag-select-empty">
                    {{ $t('knowledgeBase.noTags') }}
                  </div>
                </template>
              </t-select>
            </t-form-item>
          </t-form>
        </div>

        <div class="batch-tag-footer">
          <t-button theme="default" variant="outline" @click="requestClose">
            {{ $t('common.cancel') }}
          </t-button>
          <t-button
            theme="primary"
            :loading="loading"
            :disabled="disabled"
            @click="emit('confirm')"
          >
            {{ $t('common.confirm') }}
          </t-button>
        </div>
      </div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import type { FAQSelectOption } from '../../composables/useFAQEntries'

let nextBatchTagDialogSeq = 0

const props = defineProps<{
  visible: boolean
  modelValue: number | undefined
  selectedCount: number
  tagSelectOptions: FAQSelectOption[]
  loading: boolean
  disabled: boolean
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'update:modelValue', val: number | undefined): void
  (e: 'confirm'): void
}>()

const titleId = `faq-batch-tag-dialog-title-${++nextBatchTagDialogSeq}`
const dialogRef = ref<HTMLDialogElement | null>(null)

function syncDialogOpen(open: boolean) {
  const dialog = dialogRef.value
  if (!dialog) return
  if (open) {
    if (!dialog.open) dialog.showModal()
  } else if (dialog.open) {
    dialog.close()
  }
}

watch(
  () => props.visible,
  (open) => {
    void nextTick(() => syncDialogOpen(open))
  },
)

onMounted(() => {
  if (props.visible) syncDialogOpen(true)
})

function requestClose() {
  dialogRef.value?.close()
  emit('update:visible', false)
}

function onNativeClose() {
  if (dialogRef.value) dialogRef.value.returnValue = ''
  if (props.visible) emit('update:visible', false)
}

function handleBackdropClick(event: MouseEvent) {
  const dialog = dialogRef.value
  if (!dialog || (typeof HTMLDialogElement !== 'undefined' && 'closedBy' in HTMLDialogElement.prototype)) return
  if (event.target !== dialog) return
  const rect = dialog.getBoundingClientRect()
  const isDialogContent =
    rect.top <= event.clientY &&
    event.clientY <= rect.top + rect.height &&
    rect.left <= event.clientX &&
    event.clientX <= rect.left + rect.width
  if (!isDialogContent) dialog.close()
}

function onSelectChange(val: unknown) {
  if (val === undefined || val === null || val === '') {
    emit('update:modelValue', undefined)
    return
  }
  const num = Number(val)
  emit('update:modelValue', Number.isFinite(num) ? num : undefined)
}
</script>

<style lang="less">
.batch-tag-dialog {
  padding: 0;
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-xl);
  background: var(--gy-surface);
  color: var(--gy-text-primary);
  box-shadow: var(--gy-shadow-lg);
  max-height: 90vh;
  overflow: hidden;

  &[open] {
    display: flex;
    flex-direction: column;
    width: min(480px, calc(100vw - 40px));
  }

  &:modal {
    overscroll-behavior: contain;
  }

  &::backdrop {
    overflow: hidden;
    overscroll-behavior: contain;
    background-color: color-mix(in oklab, var(--gy-text-primary) 45%, transparent);
    backdrop-filter: blur(4px);
  }

  .batch-tag-modal {
    position: relative;
    width: 100%;
    background: var(--gy-surface);
    display: flex;
    flex-direction: column;
  }

  .batch-tag-close-btn {
    position: absolute;
    top: var(--gy-space-5);
    right: var(--gy-space-5);
    width: 32px;
    height: 32px;
    border: none;
    background: var(--gy-surface-subtle);
    border-radius: var(--gy-radius-sm);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--gy-text-secondary);
    transition: background-color var(--gy-motion-base) ease, color var(--gy-motion-base) ease;
    z-index: var(--gy-z-sticky);

    &:hover {
      background: var(--gy-surface-subtle-hover);
      color: var(--gy-text-primary);
    }

    &:focus-visible {
      outline: 2px solid var(--gy-border-focus);
      outline-offset: 2px;
    }
  }

  .batch-tag-container {
    display: flex;
    flex-direction: column;
    padding: var(--gy-space-6);
  }

  .batch-tag-header {
    margin-bottom: var(--gy-space-6);
    padding-right: var(--gy-space-10);

    .batch-tag-title {
      margin: 0;
      font-size: var(--gy-text-3xl);
      font-weight: 600;
      color: var(--gy-text-primary);
      line-height: 1.4;
    }
  }

  .batch-tag-tip {
    display: flex;
    align-items: flex-start;
    gap: var(--gy-space-2);
    padding: var(--gy-space-3) var(--gy-space-4);
    margin-bottom: var(--gy-space-5);
    background: var(--gy-brand-soft);
    border: 1px solid var(--gy-brand-focus);
    border-radius: var(--gy-radius-md);
    font-size: var(--gy-text-base);
    color: var(--gy-brand);
    line-height: 1.5;

    .tip-icon {
      flex-shrink: 0;
      margin-top: 2px;
      color: var(--gy-brand);
    }
  }

  .batch-tag-select {
    width: 100%;
  }

  .batch-tag-footer {
    display: flex;
    justify-content: flex-end;
    gap: var(--gy-space-3);
    margin-top: var(--gy-space-6);
    padding-top: var(--gy-space-5);
    border-top: 1px solid var(--gy-border-subtle);
  }

  .tag-select-empty {
    padding: var(--gy-space-2) var(--gy-space-3);
    text-align: center;
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-base);
  }
}
</style>
