<template>
  <dialog
    ref="dialogRef"
    v-bind="{ closedby: 'any' }"
    :aria-labelledby="titleId"
    class="faq-import-dialog"
    @click="handleBackdropClick"
    @close="onNativeClose"
  >
    <div class="faq-import-modal">
      <button
        type="button"
        class="close-btn"
        :aria-label="$t('general.close')"
        @click="requestClose"
      >
        <svg aria-hidden="true" width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
          <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
        </svg>
      </button>

      <div class="faq-import-container">
        <div class="faq-import-header">
          <h2 :id="titleId" class="import-title">{{ $t('knowledgeEditor.faqImport.title') }}</h2>
        </div>

        <div class="faq-import-content">
          <div class="import-form-item">
            <label class="import-form-label required">{{ $t('knowledgeEditor.faqImport.modeLabel') }}</label>
            <t-radio-group
              :model-value="importState.mode"
              class="import-radio-group"
              @update:model-value="onModeChange"
            >
              <t-radio-button value="append">{{ $t('knowledgeEditor.faqImport.appendMode') }}</t-radio-button>
              <t-radio-button value="replace">{{ $t('knowledgeEditor.faqImport.replaceMode') }}</t-radio-button>
            </t-radio-group>
          </div>

          <div class="import-form-item">
            <div class="file-label-row">
              <label class="import-form-label required">{{ $t('knowledgeEditor.faqImport.fileLabel') }}</label>
              <t-dropdown
                :options="downloadExampleOptions"
                placement="bottom-right"
                trigger="click"
                class="download-example-dropdown"
                @click="(data: { value?: string | number }) => emit('download-example', { value: String(data.value ?? '') })"
              >
                <t-button theme="default" variant="outline" size="small" class="download-example-btn">
                  <t-icon name="download" size="16px" />
                  <span>{{ $t('knowledgeEditor.faqImport.downloadExample') }}</span>
                </t-button>
              </t-dropdown>
            </div>

            <div class="file-upload-wrapper">
              <input
                ref="fileInputRef"
                type="file"
                accept=".json,.csv,.xlsx,.xls"
                class="file-input-hidden"
                @change="handleFileChange"
              />
              <button
                type="button"
                class="file-upload-area"
                :class="{ 'has-file': !!importState.file }"
                @click="fileInputRef?.click()"
                @dragover.prevent
                @dragenter.prevent
                @drop.prevent="handleFileDrop"
              >
                <span class="file-upload-content">
                  <t-icon name="upload" size="32px" class="upload-icon" />
                  <span class="upload-text">
                    <span v-if="!importState.file" class="upload-primary-text">
                      {{ $t('knowledgeEditor.faqImport.clickToUpload') }}
                    </span>
                    <span v-else class="upload-file-name">
                      {{ importState.file.name }}
                    </span>
                    <span v-if="!importState.file" class="upload-secondary-text">
                      {{ $t('knowledgeEditor.faqImport.dragDropTip') }}
                    </span>
                  </span>
                </span>
              </button>
              <p class="import-form-tip">{{ $t('knowledgeEditor.faqImport.fileTip') }}</p>
            </div>
          </div>

          <div v-if="importState.preview.length" class="import-preview">
            <div class="preview-header">
              <t-icon name="file-view" size="16px" class="preview-icon" />
              <span class="preview-title">
                {{ $t('knowledgeEditor.faqImport.previewCount', { count: importState.preview.length }) }}
              </span>
            </div>
            <div class="preview-list">
              <div
                v-for="(item, index) in importState.preview.slice(0, 5)"
                :key="index"
                class="preview-item"
              >
                <span class="preview-index">{{ index + 1 }}</span>
                <span class="preview-question">{{ item.standard_question }}</span>
              </div>
            </div>
            <p v-if="importState.preview.length > 5" class="preview-more">
              {{ $t('knowledgeEditor.faqImport.previewMore', { count: importState.preview.length - 5 }) }}
            </p>
          </div>
        </div>

        <div class="faq-import-footer">
          <t-button
            theme="default"
            variant="outline"
            :disabled="importState.importing && importState.taskStatus?.status === 'running'"
            @click="emit('cancel')"
          >
            {{ $t('common.cancel') }}
          </t-button>
          <t-button
            theme="primary"
            :loading="importState.importing && !importState.taskId"
            :disabled="importState.taskStatus?.status === 'running'"
            @click="emit('confirm')"
          >
            {{
              importState.taskStatus?.status === 'success'
                ? $t('common.close')
                : importState.taskStatus?.status === 'failed'
                  ? $t('common.retry')
                  : $t('knowledgeEditor.faqImport.importButton')
            }}
          </t-button>
        </div>
      </div>
    </div>
  </dialog>
</template>

<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import type { FAQDropdownItem } from '../../composables/useFAQEntries'
import type { FAQImportFileLike, FAQImportState } from '../../composables/useFAQImportExport'

let nextImportDialogSeq = 0

const props = defineProps<{
  visible: boolean
  importState: FAQImportState
  downloadExampleOptions: FAQDropdownItem[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'update:mode', val: 'append' | 'replace'): void
  (e: 'select-file', file: FAQImportFileLike): void
  (e: 'download-example', data: { value: string }): void
  (e: 'cancel'): void
  (e: 'confirm'): void
}>()

const titleId = `faq-import-dialog-title-${++nextImportDialogSeq}`
const dialogRef = ref<HTMLDialogElement | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)

function onModeChange(val: unknown) {
  emit('update:mode', val === 'replace' ? 'replace' : 'append')
}

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

function handleFileChange(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file) return
  emit('select-file', file)
  target.value = ''
}

function handleFileDrop(event: DragEvent) {
  const file = event.dataTransfer?.files[0]
  if (!file) return
  emit('select-file', file)
}
</script>

<style lang="less">
.faq-import-dialog {
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
    width: min(600px, calc(100vw - 40px));
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

  .faq-import-modal {
    position: relative;
    width: 100%;
    max-height: 90vh;
    background: var(--gy-surface);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .close-btn {
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

  .faq-import-container {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }

  .faq-import-header {
    padding: var(--gy-space-6) var(--gy-space-6) var(--gy-space-4);
    border-bottom: 1px solid var(--gy-border-subtle);
    flex-shrink: 0;

    .import-title {
      margin: 0;
      font-size: var(--gy-text-2xl);
      font-weight: 600;
      color: var(--gy-text-primary);
    }
  }

  .faq-import-content {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    padding: var(--gy-space-6);
    min-height: 0;
    max-height: calc(90vh - 140px);
  }

  .faq-import-footer {
    padding: var(--gy-space-4) var(--gy-space-6);
    border-top: 1px solid var(--gy-border-subtle);
    display: flex;
    justify-content: flex-end;
    gap: var(--gy-space-3);
    flex-shrink: 0;
  }

  .import-form-item {
    margin-bottom: var(--gy-space-6);

    &:last-child {
      margin-bottom: 0;
    }
  }

  .file-label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
    gap: var(--gy-space-3);
  }

  .import-form-label {
    display: block;
    margin-bottom: 0;
    font-size: var(--gy-text-base);
    font-weight: 500;
    color: var(--gy-text-primary);
    flex: 1;

    &.required::after {
      content: '*';
      color: var(--gy-danger);
      margin-left: var(--gy-space-1);
      font-weight: 600;
    }
  }

  .file-input-hidden {
    position: absolute;
    width: 0;
    height: 0;
    opacity: 0;
    overflow: hidden;
    pointer-events: none;
  }

  .file-upload-area {
    position: relative;
    width: 100%;
    min-height: 120px;
    padding: var(--gy-space-4);
    border: 2px dashed var(--gy-border-subtle);
    border-radius: var(--gy-radius-md);
    background: var(--gy-surface-subtle);
    font: inherit;
    cursor: pointer;
    transition: border-color var(--gy-motion-slow) ease, background-color var(--gy-motion-slow) ease;
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover {
      border-color: var(--gy-brand);
      background: var(--gy-success-soft);
    }

    &:focus-visible {
      outline: 2px solid var(--gy-border-focus);
      outline-offset: 2px;
    }

    &.has-file {
      border-color: var(--gy-brand);
      background: var(--gy-success-soft);
      border-style: solid;
    }
  }

  .file-upload-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--gy-space-3);
    text-align: center;
  }

  .upload-icon {
    color: var(--gy-brand);
    transition: transform var(--gy-motion-base) ease;
  }

  .file-upload-area:hover .upload-icon {
    transform: translateY(-2px);
  }

  .upload-text {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-1);
  }

  .upload-primary-text {
    font-size: var(--gy-text-base);
    font-weight: 500;
    color: var(--gy-text-primary);
  }

  .upload-secondary-text {
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
  }

  .upload-file-name {
    font-size: var(--gy-text-base);
    font-weight: 500;
    color: var(--gy-brand);
    word-break: break-all;
  }

  .import-form-tip {
    margin-top: var(--gy-space-2);
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
    line-height: 18px;
  }

  .import-preview {
    margin-top: var(--gy-space-5);
    padding: var(--gy-space-4);
    background: var(--gy-surface-subtle);
    border: 1px solid var(--gy-border-subtle);
    border-radius: var(--gy-radius-md);
  }

  .preview-header {
    display: flex;
    align-items: center;
    gap: var(--gy-space-2);
    margin-bottom: var(--gy-space-3);
    padding-bottom: var(--gy-space-3);
    border-bottom: 1px solid var(--gy-border-subtle);
  }

  .preview-icon {
    color: var(--gy-brand);
    flex-shrink: 0;
  }

  .preview-title {
    font-size: var(--gy-text-base);
    font-weight: 500;
    color: var(--gy-text-primary);
  }

  .preview-list {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-2);
    margin-bottom: var(--gy-space-2);
  }

  .preview-item {
    display: flex;
    align-items: flex-start;
    gap: var(--gy-space-3);
    padding: 10px var(--gy-space-3);
    background: var(--gy-surface);
    border: 1px solid var(--gy-border-subtle);
    border-radius: var(--gy-radius-sm);
    transition: border-color var(--gy-motion-base) ease;

    &:hover {
      border-color: var(--gy-brand);
    }
  }

  .preview-index {
    flex-shrink: 0;
    width: 20px;
    height: 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--gy-brand);
    color: var(--gy-text-inverse);
    border-radius: var(--gy-radius-xs);
    font-size: var(--gy-text-sm);
    font-weight: 600;
  }

  .preview-question {
    flex: 1;
    font-size: var(--gy-text-md);
    color: var(--gy-text-primary);
    line-height: 1.5;
    word-break: break-word;
  }

  .preview-more {
    margin-top: var(--gy-space-2);
    padding-top: var(--gy-space-2);
    border-top: 1px solid var(--gy-border-subtle);
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
    text-align: center;
  }
}
</style>
