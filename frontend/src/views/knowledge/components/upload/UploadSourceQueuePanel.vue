<template>
  <aside class="files-panel">
    <div class="sidebar-header">
      <div class="sidebar-header-row">
        <h2 class="sidebar-title">{{ dialogTitle }}</h2>
        <div v-if="mode === 'file'" class="sidebar-header-actions">
          <span class="files-count">{{ batchItemCount }}</span>
          <KbUploadSourceDropdown
            :accept-file-types="acceptFileTypes"
            :supported-file-types="supportedFileTypes"
            :tooltip="t('uploadConfirm.continueAdd')"
            placement="bottom-left"
            @files="emit('append-files', $event)"
            @url="emit('append-url', $event)"
          />
        </div>
      </div>

      <div v-if="mode === 'file'" class="destination-row">
        <t-popup
          v-model:visible="destinationPickerVisible"
          trigger="click"
          placement="bottom-left"
          attach="body"
          :z-index="3100"
          overlay-class-name="upload-destination-popup"
          destroy-on-close
        >
          <button
            type="button"
            class="destination-crumb"
            :title="destinationFullLabel"
            :aria-label="t('uploadConfirm.destinationChange')"
            :aria-expanded="destinationPickerVisible"
          >
            <span class="destination-crumb__label">{{ t('uploadConfirm.destinationLabel') }}</span>
            <span class="destination-crumb__path">{{ destinationBreadcrumb }}</span>
            <t-icon name="chevron-down" class="destination-crumb__caret" />
          </button>
          <template #content>
            <div class="card-menu" @click.stop>
              <FolderPickerMenu
                :options="pickerFolderOptions"
                :current-path="targetFolder"
                allow-reselect
                @create="onDestinationCreated"
                @confirm="onDestinationPicked"
              />
            </div>
          </template>
        </t-popup>
      </div>
    </div>

    <div class="files-list-wrap">
      <div v-if="mode === 'manual' && manualPreview" class="manual-source-panel">
        <p class="manual-source-title" :title="manualPreview.title">{{ manualPreview.title }}</p>
        <p class="manual-source-meta">
          {{ t('uploadConfirm.manualCharCount', { count: manualCharCount }) }}
        </p>
      </div>
      <div v-else-if="mode === 'reparse' && reparsePreview" class="manual-source-panel">
        <p class="manual-source-title" :title="reparsePreview.fileName">
          {{ reparsePreview.fileName || t('uploadConfirm.reparseSource') }}
        </p>
        <p class="manual-source-meta">{{ t('uploadConfirm.reparseHint') }}</p>
      </div>
      <ul v-else-if="mode === 'file' && batchItemCount > 0" class="files-list">
        <li v-for="(url, index) in urls" :key="`url-${url}-${index}`" class="file-item">
          <span class="file-icon-wrap">
            <t-icon name="link" class="file-icon" />
          </span>
          <div class="file-meta">
            <span class="file-name" :title="url">{{ url }}</span>
            <span class="file-size">{{ t('uploadConfirm.urlItemLabel') }}</span>
          </div>
          <button
            type="button"
            class="file-remove"
            :aria-label="t('common.remove')"
            @click="emit('remove-url', index)"
          >
            <t-icon name="close" />
          </button>
        </li>
        <li v-for="(file, index) in files" :key="`${file.name}-${index}`" class="file-item">
          <span class="file-icon-wrap">
            <t-icon :name="getFileIcon(file.name)" class="file-icon" />
          </span>
          <div class="file-meta">
            <span class="file-name" :title="fileDisplayTitle(file)">{{ file.name }}</span>
            <span class="file-size">
              <template v-if="fileRelativeDir(file)">
                <span class="file-relative-dir" :title="fileRelativeDir(file)">{{ fileRelativeDir(file) }}</span>
                <span class="file-meta-sep">·</span>
              </template>
              {{ formatFileSize(file.size) }}
            </span>
          </div>
          <button
            type="button"
            class="file-remove"
            :aria-label="t('common.remove')"
            @click="emit('remove-file', index)"
          >
            <t-icon name="close" />
          </button>
        </li>
      </ul>
      <div v-else-if="mode === 'file'" class="files-empty">{{ t('uploadConfirm.noItems') }}</div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatFileSize, getFileIcon } from '@/utils/files'
import KbUploadSourceDropdown from '../KbUploadSourceDropdown.vue'
import FolderPickerMenu, { type FolderOption } from '../FolderPickerMenu.vue'
import { folderOptionFromPath, sortFolderOptions } from '../../folderTree'
import type {
  UploadConfirmManualSource,
  UploadConfirmMode,
  UploadConfirmReparseSource,
} from '@/stores/uploadConfirm'

const props = defineProps<{
  dialogTitle: string
  mode: UploadConfirmMode
  files: File[]
  urls: string[]
  manualPreview?: UploadConfirmManualSource | null
  reparsePreview?: UploadConfirmReparseSource | null
  acceptFileTypes?: string
  supportedFileTypes?: string[]
  targetFolder: string
  folderOptions: FolderOption[]
  resetCounter: number
}>()

const emit = defineEmits<{
  'append-files': [files: File[]]
  'append-url': [url: string]
  'remove-file': [index: number]
  'remove-url': [index: number]
  'update:targetFolder': [path: string]
}>()

const { t } = useI18n()

const destinationPickerVisible = ref(false)
// Folders created in this dialog before upload; the server tree only gains them
// once files land, so keep them here across picker open/close cycles.
const pendingFolderPaths = ref<string[]>([])

watch(
  () => props.resetCounter,
  () => {
    pendingFolderPaths.value = []
    destinationPickerVisible.value = false
  },
)

const pickerFolderOptions = computed(() => {
  const byPath = new Map<string, FolderOption>()
  ;(props.folderOptions || []).forEach((option) => byPath.set(option.path, option))
  pendingFolderPaths.value.forEach((path) => {
    if (!byPath.has(path)) byPath.set(path, folderOptionFromPath(path))
  })
  return sortFolderOptions([...byPath.values()])
})

const batchItemCount = computed(() => props.files.length + props.urls.length)
const manualCharCount = computed(() => props.manualPreview?.content?.length ?? 0)

const destinationBreadcrumb = computed(() => {
  if (!props.targetFolder) return t('knowledgeBase.folderTree.rootRow')
  const parts = props.targetFolder.split('/').filter(Boolean)
  return [t('knowledgeBase.folderTree.rootRow'), ...parts].join(' / ')
})

const destinationFullLabel = computed(
  () => props.targetFolder || t('knowledgeBase.folderTree.rootRow'),
)

function fileRelativeDir(file: File): string {
  const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  if (!relativePath) return ''
  return relativePath.split('/').filter(Boolean).slice(0, -1).join('/')
}

function fileDisplayTitle(file: File): string {
  const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  return relativePath || file.name
}

function onDestinationCreated(path: string) {
  if (!path || pendingFolderPaths.value.includes(path)) return
  pendingFolderPaths.value = [...pendingFolderPaths.value, path]
}

function onDestinationPicked(path: string) {
  emit('update:targetFolder', path)
  destinationPickerVisible.value = false
}
</script>

<style lang="less" scoped>
@import '../upload-confirm-dialog.less';
</style>
