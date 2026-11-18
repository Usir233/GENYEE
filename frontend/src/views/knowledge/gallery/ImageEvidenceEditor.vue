<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { get } from '@/utils/request'
import { getChunkByIdOnly, updateDocumentChunk } from '@/api/knowledge-base'
import type { ImageAsset } from '@/api/image-gallery'

interface EvidenceImageInfo {
  url?: string
  original_url?: string
}

interface EvidenceChunkRow {
  id: string
  knowledge_id: string
  chunk_type: string
  content: string
  content_revision?: number
  image_info?: string
  index_status?: string
}

interface EvidenceChunkListResponse {
  data?: EvidenceChunkRow[]
  total?: number
}

interface EvidenceChunkSingleResponse {
  data?: EvidenceChunkRow
}

const props = defineProps<{ image: ImageAsset; field: 'caption' | 'ocr_text' }>()
const emit = defineEmits<{ (e: 'saved'): void }>()
const { t } = useI18n()
const editing = ref(false)
const loading = ref(false)
const saving = ref(false)
const draft = ref('')
const error = ref('')
const chunk = ref<EvidenceChunkRow | null>(null)
let sequence = 0

function reset() {
  sequence++
  editing.value = false
  loading.value = false
  saving.value = false
  chunk.value = null
  draft.value = ''
  error.value = ''
}
watch(() => [props.image.id, props.field], reset)
onBeforeUnmount(reset)

function matchesImage(row: Pick<EvidenceChunkRow, 'image_info'> | null | undefined, image: ImageAsset): boolean {
  try {
    const images: unknown = JSON.parse(row?.image_info || '[]')
    return Array.isArray(images) && images.some((info: EvidenceImageInfo | null | undefined) => (info?.url || info?.original_url) === image.url)
  } catch {
    return false
  }
}

async function beginEdit() {
  const token = ++sequence
  const image = props.image
  const kind = props.field === 'caption' ? 'image_caption' : 'image_ocr'
  editing.value = true
  loading.value = true
  error.value = ''
  chunk.value = null
  try {
    // Reuse the existing typed chunk listing; the gallery's winning copy
    // can itself be a source-text or visual-vector chunk and is not editable.
    for (let page = 1; ; page++) {
      const query = new URLSearchParams({ chunk_type: kind, page: String(page), page_size: '100' })
      const result = (await get(`/api/v1/chunks/${encodeURIComponent(image.knowledge_id)}?${query}`)) as unknown as EvidenceChunkListResponse
      if (token !== sequence) return
      const rows = Array.isArray(result.data) ? result.data : []
      const found = rows.find((row) => matchesImage(row, image))
      if (found) {
        const response = (await getChunkByIdOnly(found.id)) as unknown as EvidenceChunkSingleResponse
        if (token !== sequence) return
        const current = response.data
        if (current?.knowledge_id !== image.knowledge_id || current.chunk_type !== kind || !matchesImage(current, image)) {
          throw new Error(t('common.noData'))
        }
        chunk.value = current
        draft.value = current.content
        return
      }
      if (!rows.length || page * 100 >= (result.total ?? 0)) throw new Error(t('common.noData'))
    }
  } catch (e: unknown) {
    const errObj = e as { message?: string } | null
    if (token === sequence) error.value = errObj?.message || t('common.error')
  } finally {
    if (token === sequence) loading.value = false
  }
}

async function save() {
  if (!chunk.value || saving.value || chunk.value.knowledge_id !== props.image.knowledge_id) return
  if (!draft.value.trim()) {
    error.value = t('knowledgeBase.chunkContentRequired')
    return
  }
  const token = sequence
  const current = chunk.value
  saving.value = true
  error.value = ''
  try {
    const result = (await updateDocumentChunk(current.knowledge_id, current.id, {
      content: draft.value, expected_revision: current.content_revision,
    })) as unknown as EvidenceChunkSingleResponse
    if (token !== sequence) return
    chunk.value = result.data ?? null
    if (result.data?.index_status === 'failed') {
      error.value = t('knowledgeBase.chunkSavedIndexFailed')
      return
    }
    editing.value = false
    emit('saved')
  } catch (e: unknown) {
    if (token !== sequence) return
    const errObj = e as { message?: string; status?: number } | null
    error.value = errObj?.message || t('common.saveFailed')
    if (errObj?.status === 409) chunk.value = null
  } finally {
    if (token === sequence) saving.value = false
  }
}

defineExpose({ beginEdit, editing })
</script>

<template>
  <div v-if="editing" class="image-evidence-editor" :aria-busy="loading || saving">
    <div v-if="loading" class="image-evidence-editor__loading">
      <t-loading size="small" />
    </div>
    <label v-else-if="chunk" class="image-evidence-editor__field">
      <span class="image-evidence-editor__label">{{ t(`knowledgeEditor.wikiBrowser.gallery.${field === 'caption' ? 'caption' : 'ocr'}`) }}</span>
      <t-textarea
        v-model="draft"
        :autosize="{ minRows: 6, maxRows: 16 }"
        :disabled="saving"
        autofocus
      />
    </label>
    <p v-if="error" class="image-evidence-editor__error" role="alert">{{ error }}</p>
    <div class="image-evidence-editor__actions">
      <t-button size="small" theme="default" variant="outline" :disabled="saving" @click="reset">{{ t('common.cancel') }}</t-button>
      <t-button v-if="chunk" size="small" theme="primary" :loading="saving" @click="save">
        {{ t(chunk.index_status === 'failed' ? 'knowledgeBase.retryIndex' : 'common.save') }}
      </t-button>
      <t-button v-else-if="!loading" size="small" theme="primary" @click="beginEdit">{{ t('common.refresh') }}</t-button>
    </div>
  </div>
</template>

<style scoped lang="less">
.image-evidence-editor {
  &__field { display: block; }

  &__label {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  :deep(.t-textarea__inner) {
    font-size: var(--app-text-md);
    line-height: 1.75;
  }

  &__loading {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 140px;
  }

  &__error {
    margin: 8px 0 0;
    color: var(--td-error-color);
    font-size: var(--app-text-sm);
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  &__actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 12px;
  }
}
</style>
