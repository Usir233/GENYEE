<template>
  <div v-show="currentSection === 'basic'" class="section">
    <div v-if="formData" class="section-content">
      <div class="section-header">
        <h3 class="section-title">{{ $t('knowledgeEditor.basic.title') }}</h3>
        <p class="section-desc">{{ $t('knowledgeEditor.basic.description') }}</p>
      </div>
      <div class="section-body">
        <div v-if="editorMode === 'edit' && activeKbId" class="form-item">
          <label class="form-label">{{ $t('knowledgeEditor.basic.kbId') }}</label>
          <p class="form-tip">
            {{ isPostCreateSession ? $t('knowledgeEditor.postCreateHint.followUpDesc') : $t('knowledgeEditor.basic.kbIdDesc') }}
          </p>
          <div class="kb-id-field">
            <code class="kb-id-value" :title="activeKbId">{{ activeKbId }}</code>
            <t-tooltip :content="$t('common.copy')" placement="top">
              <t-button theme="default" size="small" variant="text" class="kb-id-copy" @click="emit('copy-kb-id')">
                <t-icon name="file-copy" />
              </t-button>
            </t-tooltip>
          </div>
        </div>

        <div class="form-item">
          <label class="form-label required">{{ $t('knowledgeEditor.basic.typeLabel') }}</label>
          <t-radio-group
            :value="formData.type"
            :disabled="editorMode === 'edit'"
            data-guide="kb-create-type"
            @change="(val: string | number | boolean) => emit('update:type', val === 'faq' ? 'faq' : 'document')"
          >
            <t-radio-button value="document">{{ $t('knowledgeEditor.basic.typeDocument') }}</t-radio-button>
            <t-radio-button value="faq">{{ $t('knowledgeEditor.basic.typeFAQ') }}</t-radio-button>
          </t-radio-group>
          <p class="form-tip">{{ $t('knowledgeEditor.basic.typeDescription') }}</p>
        </div>

        <div v-if="!isFAQ" class="form-item">
          <label class="form-label required">{{ $t('knowledgeEditor.indexing.title') }}</label>
          <p class="form-tip">{{ $t('knowledgeEditor.indexing.description') }}</p>
          <div class="indexing-checks" :class="{ 'is-locked': isIndexingLocked }" data-guide="kb-create-indexing">
            <div
              class="indexing-check-item"
              :class="{ 'is-checked': formData.indexingStrategy.vectorEnabled, 'is-disabled': isIndexingLocked }"
              role="checkbox"
              :tabindex="isIndexingLocked ? -1 : 0"
              :aria-checked="formData.indexingStrategy.vectorEnabled"
              :aria-disabled="isIndexingLocked"
              @click="emit('toggle-vector-indexing')"
              @keydown.enter.prevent="emit('toggle-vector-indexing')"
              @keydown.space.prevent="emit('toggle-vector-indexing')"
            >
              <t-checkbox
                :checked="formData.indexingStrategy.vectorEnabled"
                :disabled="isIndexingLocked"
                class="indexing-check-box"
              >{{ $t('knowledgeEditor.indexing.searchTitle') }}</t-checkbox>
              <p class="indexing-check-desc">{{ $t('knowledgeEditor.indexing.searchDesc') }}</p>
            </div>
            <div
              class="indexing-check-item"
              :class="{ 'is-checked': formData.indexingStrategy.wikiEnabled, 'is-disabled': isIndexingLocked }"
              role="checkbox"
              :tabindex="isIndexingLocked ? -1 : 0"
              :aria-checked="formData.indexingStrategy.wikiEnabled"
              :aria-disabled="isIndexingLocked"
              @click="emit('toggle-wiki-indexing')"
              @keydown.enter.prevent="emit('toggle-wiki-indexing')"
              @keydown.space.prevent="emit('toggle-wiki-indexing')"
            >
              <t-checkbox
                :checked="formData.indexingStrategy.wikiEnabled"
                :disabled="isIndexingLocked"
                class="indexing-check-box"
              >
                <span class="indexing-check-title">
                  {{ $t('knowledgeEditor.indexing.wikiTitle') }}
                  <span class="indexing-new-badge">NEW</span>
                </span>
              </t-checkbox>
              <p class="indexing-check-desc">{{ $t('knowledgeEditor.indexing.wikiDesc') }}</p>
            </div>
          </div>
          <p v-if="isIndexingLocked" class="locked-tip">
            <t-icon name="lock-on" class="locked-tip-icon" />
            <span>{{ $t('knowledgeEditor.indexing.lockedTip') }}</span>
          </p>
        </div>

        <div v-if="!isFAQ && formData.indexingStrategy.wikiEnabled" class="form-item">
          <label class="form-label" for="wiki-auto-generate">{{ $t('knowledgeEditor.wiki.autoGenerateLabel') }}</label>
          <p class="form-tip">{{ $t('knowledgeEditor.wiki.autoGenerateTip') }}</p>
          <t-switch
            id="wiki-auto-generate"
            :value="formData.wikiConfig.autoGenerate"
            @change="(val: string | number | boolean) => updateWiki({ autoGenerate: Boolean(val) })"
          />
        </div>

        <div v-if="!isFAQ && formData.indexingStrategy.wikiEnabled" class="form-item">
          <label class="form-label">{{ $t('knowledgeEditor.wiki.extractionGranularityLabel') }}</label>
          <p class="form-tip">{{ $t('knowledgeEditor.wiki.extractionGranularityTip') }}</p>
          <t-radio-group
            :value="resolvedGranularity"
            class="granularity-radio-group"
            @change="handleGranularityChange"
          >
            <t-radio-button value="focused">
              {{ $t('knowledgeEditor.wiki.granularityFocused') }}
            </t-radio-button>
            <t-radio-button value="standard">
              {{ $t('knowledgeEditor.wiki.granularityStandard') }}
            </t-radio-button>
            <t-radio-button value="exhaustive">
              {{ $t('knowledgeEditor.wiki.granularityExhaustive') }}
            </t-radio-button>
          </t-radio-group>
          <p class="form-tip granularity-hint">{{ granularityHint }}</p>
        </div>

        <div v-if="!isFAQ && formData.indexingStrategy.wikiEnabled" class="form-item">
          <label class="form-label">{{ $t('knowledgeEditor.wiki.contentInstructionsLabel') }}</label>
          <p class="form-tip">{{ $t('knowledgeEditor.wiki.contentInstructionsTip') }}</p>
          <div class="wiki-instructions-input">
            <t-textarea
              :value="formData.wikiConfig.contentInstructions"
              :placeholder="$t('knowledgeEditor.wiki.contentInstructionsPlaceholder')"
              :maxlength="4000"
              :autosize="{ minRows: 3, maxRows: 8 }"
              @change="(val: string) => updateWiki({ contentInstructions: String(val ?? '') })"
            />
            <div class="wiki-instructions-footer">
              <AssistantActionButton
                class="wiki-instructions-action"
                :label="$t(formData.wikiConfig.contentInstructions.trim() ? 'assistant.actions.refineRules' : 'assistant.actions.writePrompt')"
                :query="$t(formData.wikiConfig.contentInstructions.trim() ? 'assistant.prompts.refineWikiRules' : 'assistant.prompts.writeWikiRules', { field: $t('knowledgeEditor.wiki.contentInstructionsLabel') })"
                :selection="{ field: $t('knowledgeEditor.wiki.contentInstructionsLabel'), text: formData.wikiConfig.contentInstructions }"
                :disabled="saving || loading"
              />
              <span class="wiki-instructions-counter">{{ formData.wikiConfig.contentInstructions.length }}/4000</span>
            </div>
          </div>
        </div>

        <div v-if="!isFAQ && formData.indexingStrategy.wikiEnabled" class="form-item">
          <label class="form-label">{{ $t('knowledgeEditor.wiki.extractionInstructionsLabel') }}</label>
          <p class="form-tip">{{ $t('knowledgeEditor.wiki.extractionInstructionsTip') }}</p>
          <div class="wiki-instructions-input">
            <t-textarea
              :value="formData.wikiConfig.extractionInstructions"
              :placeholder="$t('knowledgeEditor.wiki.extractionInstructionsPlaceholder')"
              :maxlength="4000"
              :autosize="{ minRows: 3, maxRows: 8 }"
              @change="(val: string) => updateWiki({ extractionInstructions: String(val ?? '') })"
            />
            <div class="wiki-instructions-footer">
              <AssistantActionButton
                class="wiki-instructions-action"
                :label="$t(formData.wikiConfig.extractionInstructions.trim() ? 'assistant.actions.refineRules' : 'assistant.actions.writePrompt')"
                :query="$t(formData.wikiConfig.extractionInstructions.trim() ? 'assistant.prompts.refineWikiRules' : 'assistant.prompts.writeWikiRules', { field: $t('knowledgeEditor.wiki.extractionInstructionsLabel') })"
                :selection="{ field: $t('knowledgeEditor.wiki.extractionInstructionsLabel'), text: formData.wikiConfig.extractionInstructions }"
                :disabled="saving || loading"
              />
              <span class="wiki-instructions-counter">{{ formData.wikiConfig.extractionInstructions.length }}/4000</span>
            </div>
          </div>
        </div>

        <div class="form-item" data-guide="kb-create-name">
          <label class="form-label required">{{ $t('knowledgeEditor.basic.nameLabel') }}</label>
          <t-input
            :value="formData.name"
            :placeholder="$t('knowledgeEditor.basic.namePlaceholder')"
            :maxlength="50"
            @change="(val: string) => emit('update:name', String(val ?? ''))"
          />
        </div>
        <div class="form-item">
          <label class="form-label">{{ $t('knowledgeEditor.basic.descriptionLabel') }}</label>
          <t-textarea
            :value="formData.description"
            :placeholder="$t('knowledgeEditor.basic.descriptionPlaceholder')"
            :maxlength="200"
            :autosize="{ minRows: 3, maxRows: 6 }"
            @change="(val: string) => emit('update:description', String(val ?? ''))"
          />
        </div>

        <div v-if="editorMode === 'edit' && !isFAQ" class="form-item">
          <label class="form-label">{{ $t('knowledgeEditor.basic.profile.title') }}</label>
          <div class="kb-profile-card" :class="{ 'is-empty': !generatedProfileHasText }">
            <template v-if="generatedProfileHasText">
              <p v-if="generatedProfile?.gist" class="kb-profile-gist">{{ generatedProfile.gist }}</p>
              <div v-if="generatedProfile?.topics?.length" class="kb-profile-topics">
                <t-tag
                  v-for="topic in generatedProfile.topics"
                  :key="topic"
                  size="small"
                  theme="primary"
                  variant="light"
                >{{ topic }}</t-tag>
              </div>
              <div v-if="generatedProfile?.typical_questions?.length" class="kb-profile-questions">
                <p class="kb-profile-subtitle">{{ $t('knowledgeEditor.basic.profile.questions') }}</p>
                <ul>
                  <li v-for="q in generatedProfile.typical_questions" :key="q">{{ q }}</li>
                </ul>
              </div>
              <p v-if="generatedProfile?.status === 'failed'" class="kb-profile-error">
                {{ $t('knowledgeEditor.basic.profile.failed', { error: generatedProfile.error || '' }) }}
              </p>
              <div class="kb-profile-footer">
                <span class="kb-profile-meta">{{ generatedProfileMeta }}</span>
                <div class="kb-profile-actions">
                  <t-button
                    v-if="generatedProfile?.gist"
                    size="small"
                    variant="text"
                    theme="primary"
                    @click="emit('adopt-profile-gist')"
                  >
                    <template #icon><t-icon name="enter" /></template>
                    {{ $t('knowledgeEditor.basic.profile.adopt') }}
                  </t-button>
                  <t-button
                    size="small"
                    variant="text"
                    :loading="generatingProfile"
                    @click="emit('generate-profile')"
                  >
                    <template #icon><t-icon name="refresh" /></template>
                    {{ $t('knowledgeEditor.basic.profile.regenerate') }}
                  </t-button>
                </div>
              </div>
            </template>
            <template v-else>
              <div class="kb-profile-empty">
                <span class="kb-profile-empty-icon"><t-icon name="ai-edit-1" /></span>
                <div class="kb-profile-empty-text">
                  <p>
                    {{ generatedProfile?.status === 'empty'
                      ? $t('knowledgeEditor.basic.profile.noDocuments')
                      : $t('knowledgeEditor.basic.profile.empty') }}
                  </p>
                  <p v-if="generatedProfile?.status === 'failed'" class="kb-profile-error">
                    {{ $t('knowledgeEditor.basic.profile.failed', { error: generatedProfile.error || '' }) }}
                  </p>
                </div>
                <t-button
                  size="small"
                  theme="primary"
                  variant="outline"
                  :loading="generatingProfile"
                  @click="emit('generate-profile')"
                >
                  <template #icon><t-icon name="ai-edit-1" /></template>
                  {{ $t('knowledgeEditor.basic.profile.generate') }}
                </t-button>
              </div>
            </template>
          </div>
          <p class="form-tip">{{ $t('knowledgeEditor.basic.profile.hint') }}</p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeBaseProfile } from '@/api/knowledge-base'
import AssistantActionButton from '@/components/assistant/AssistantActionButton.vue'
import type {
  KBEditorFormData,
  KBEditorWikiConfig,
  KBType,
  WikiExtractionGranularity,
} from '../composables/useKBEditorForm'

const props = defineProps<{
  currentSection: string
  formData: KBEditorFormData | null
  editorMode: 'create' | 'edit'
  activeKbId?: string
  isPostCreateSession: boolean
  isFAQ: boolean
  isIndexingLocked: boolean
  saving: boolean
  loading: boolean
  generatedProfile: KnowledgeBaseProfile | null
  generatingProfile: boolean
}>()

const emit = defineEmits<{
  'copy-kb-id': []
  'update:type': [type: KBType]
  'update:name': [name: string]
  'update:description': [description: string]
  'update:wikiConfig': [config: KBEditorWikiConfig]
  'toggle-vector-indexing': []
  'toggle-wiki-indexing': []
  'adopt-profile-gist': []
  'generate-profile': []
}>()

const { t } = useI18n()

const resolvedGranularity = computed<WikiExtractionGranularity>(() => {
  const g = props.formData?.wikiConfig?.extractionGranularity
  if (g === 'focused' || g === 'standard' || g === 'exhaustive') {
    return g
  }
  return 'standard'
})

const granularityHint = computed<string>(() => {
  switch (resolvedGranularity.value) {
    case 'focused':
      return t('knowledgeEditor.wiki.granularityFocusedHint')
    case 'exhaustive':
      return t('knowledgeEditor.wiki.granularityExhaustiveHint')
    default:
      return t('knowledgeEditor.wiki.granularityStandardHint')
  }
})

const generatedProfileHasText = computed(() => {
  const p = props.generatedProfile
  return !!(p && (p.gist || p.topics?.length || p.typical_questions?.length))
})

const generatedProfileMeta = computed(() => {
  const p = props.generatedProfile
  if (!p || !p.generated_at) return ''
  const when = new Date(p.generated_at)
  const stamp = isNaN(when.getTime()) ? p.generated_at : when.toLocaleString()
  return t('knowledgeEditor.basic.profile.generatedAt', {
    time: stamp,
    count: p.stats?.document_count ?? 0,
  })
})

function updateWiki(patch: Partial<KBEditorWikiConfig>) {
  if (!props.formData) return
  emit('update:wikiConfig', {
    ...props.formData.wikiConfig,
    ...patch,
  })
}

function handleGranularityChange(value: string | number | boolean) {
  const next: WikiExtractionGranularity =
    value === 'focused' || value === 'exhaustive' ? value : 'standard'
  updateWiki({ extractionGranularity: next })
}
</script>

<style scoped lang="less">
@import '../knowledge-base-editor-modal.less';
</style>
