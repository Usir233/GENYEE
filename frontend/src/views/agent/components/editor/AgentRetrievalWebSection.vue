<template>
  <div>
    <!-- Knowledge Base Configuration -->
    <div v-show="currentSection === 'knowledge'" class="section">
      <div class="section-header">
        <h2>{{ $t('agent.editor.knowledgeConfig') }}</h2>
        <p class="section-description">{{ $t('agent.editor.knowledgeConfigDesc') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row" data-guide="agent-create-knowledge">
          <div class="setting-info">
            <label>{{ $t('agent.editor.knowledgeBases') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.kbScope') }}</p>
          </div>
          <div class="setting-control">
            <t-radio-group
              :model-value="kbSelectionMode"
              @update:model-value="onKbModeChange"
            >
              <t-radio-button value="all">{{ $t('agent.editor.allKnowledgeBases') }}</t-radio-button>
              <t-radio-button value="selected">{{ $t('agent.editor.selectedKnowledgeBases') }}</t-radio-button>
              <t-radio-button value="none">{{ $t('agent.editor.noKnowledgeBase') }}</t-radio-button>
            </t-radio-group>
          </div>
        </div>

        <div v-if="kbSelectionMode === 'selected'" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.selectKnowledgeBases') }}</label>
            <p class="desc">{{ $t('agent.editor.selectKnowledgeBasesDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              :model-value="formData.config.knowledge_bases"
              multiple
              :placeholder="$t('agent.editor.selectKnowledgeBases')"
              filterable
              :min-collapsed-num="3"
              @update:model-value="onKnowledgeBasesChange"
            >
              <t-option-group v-if="filteredMyKbOptions.length" :label="$t('agent.editor.myKnowledgeBases')">
                <t-option
                  v-for="kb in filteredMyKbOptions"
                  :key="kb.value"
                  :value="kb.value"
                  :label="kb.label"
                  :disabled="kb.disabled"
                >
                  <div class="kb-option-item" :title="kb.disabled ? kb.disabledReason : ''">
                    <span class="kb-option-icon" :class="kb.type === 'faq' ? 'faq-icon' : 'doc-icon'">
                      <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                    </span>
                    <span class="kb-option-label">{{ kb.label }}</span>
                    <span v-if="kb.ragEnabled" class="kb-option-tag tag-rag">RAG</span>
                    <span v-if="kb.wikiEnabled" class="kb-option-tag tag-wiki">Wiki</span>
                    <span class="kb-option-count">{{ kb.count || 0 }}</span>
                    <span v-if="kb.disabled" class="kb-option-disabled-hint">{{ kb.disabledReason }}</span>
                  </div>
                </t-option>
              </t-option-group>
              <t-option-group v-if="filteredSharedKbOptions.length" :label="$t('agent.editor.sharedKnowledgeBases')">
                <t-option
                  v-for="kb in filteredSharedKbOptions"
                  :key="kb.value"
                  :value="kb.value"
                  :label="kb.label"
                  :disabled="kb.disabled"
                >
                  <div class="kb-option-item" :title="kb.disabled ? kb.disabledReason : ''">
                    <span class="kb-option-icon" :class="kb.type === 'faq' ? 'faq-icon' : 'doc-icon'">
                      <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                    </span>
                    <span class="kb-option-label">{{ kb.label }}</span>
                    <span v-if="kb.ragEnabled" class="kb-option-tag tag-rag">RAG</span>
                    <span v-if="kb.wikiEnabled" class="kb-option-tag tag-wiki">Wiki</span>
                    <span v-if="kb.orgName" class="kb-option-org">{{ kb.orgName }}</span>
                    <span class="kb-option-count">{{ kb.count || 0 }}</span>
                    <span v-if="kb.disabled" class="kb-option-disabled-hint">{{ kb.disabledReason }}</span>
                  </div>
                </t-option>
              </t-option-group>
            </t-select>
          </div>
        </div>

        <div v-if="hasKnowledgeBase" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.fileTypes.label') }}</label>
            <p class="desc">{{ $t('agentEditor.fileTypes.desc') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              :model-value="formData.config.supported_file_types"
              multiple
              :placeholder="$t('agentEditor.fileTypes.allTypes')"
              :min-collapsed-num="3"
              clearable
              @update:model-value="onFileTypesChange"
            >
              <t-option
                v-for="ft in availableFileTypes"
                :key="ft.value"
                :value="ft.value"
                :label="ft.label"
              />
            </t-select>
          </div>
        </div>

        <div v-if="hasKnowledgeBase" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.retrieveKBOnlyWhenMentioned') }}</label>
            <p class="desc">{{ $t('agent.editor.retrieveKBOnlyWhenMentionedDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.retrieve_kb_only_when_mentioned"
              :aria-label="$t('agent.editor.retrieveKBOnlyWhenMentioned')"
              @update:model-value="(val: unknown) => emit('updateConfig', { retrieve_kb_only_when_mentioned: Boolean(val) })"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- Web Search Configuration -->
    <div v-show="currentSection === 'websearch'" class="section">
      <div class="section-header">
        <h2>{{ $t('agent.editor.webSearchConfig') }}</h2>
        <p class="section-description">{{ $t('agent.editor.webSearchConfigDesc') }}</p>
      </div>

      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.webSearch') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.webSearch') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.web_search_enabled"
              :aria-label="$t('agent.editor.webSearch')"
              @update:model-value="(val: unknown) => emit('updateConfig', { web_search_enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="formData.config.web_search_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.webSearchProvider') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.webSearchProvider') }}</p>
          </div>
          <div class="setting-control">
            <t-select
              :model-value="formData.config.web_search_provider_id || ''"
              clearable
              :placeholder="$t('agent.editor.webSearchProviderPlaceholder')"
              style="width: 240px;"
              @update:model-value="(val: unknown) => emit('updateConfig', { web_search_provider_id: String(val ?? '') })"
            >
              <t-option
                v-for="p in webSearchProviderList"
                :key="p.id"
                :value="p.id"
                :label="p.name"
              >
                <span>{{ p.name }}</span>
                <t-tag v-if="p.is_default" theme="primary" size="small" style="margin-left: 6px;">
                  {{ $t('common.default') }}
                </t-tag>
              </t-option>
            </t-select>
          </div>
        </div>

        <div v-if="formData.config.web_search_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.webSearchMaxResults') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.webSearchMaxResults') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.web_search_max_results"
                :min="1"
                :max="10"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { web_search_max_results: val })"
              />
              <span class="slider-value">{{ formData.config.web_search_max_results }}</span>
            </div>
          </div>
        </div>

        <div v-if="formData.config.web_search_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.webFetchEnabled') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.webFetchEnabled') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="Boolean(formData.config.web_fetch_enabled)"
              :aria-label="$t('agent.editor.webFetchEnabled')"
              @update:model-value="(val: unknown) => emit('updateConfig', { web_fetch_enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div
          v-if="formData.config.web_search_enabled && formData.config.web_fetch_enabled"
          class="setting-row"
        >
          <div class="setting-info">
            <label>{{ $t('agent.editor.webFetchTopN') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.webFetchTopN') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.web_fetch_top_n ?? 3"
                :min="1"
                :max="10"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { web_fetch_top_n: val })"
              />
              <span class="slider-value">{{ formData.config.web_fetch_top_n }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Retrieval Strategy (Quick-Answer + KB only) -->
    <div v-show="currentSection === 'retrieval' && hasKnowledgeBase && !isAgentMode" class="section">
      <div class="section-header">
        <h2>{{ $t('agent.editor.retrievalStrategy') }}</h2>
        <p class="section-description">{{ $t('agentEditor.desc.retrievalSection') }}</p>
      </div>

      <div class="settings-group">
        <div v-if="!isAgentMode" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.enableQueryExpansion') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.queryExpansion') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.enable_query_expansion"
              :aria-label="$t('agent.editor.enableQueryExpansion')"
              @update:model-value="(val: unknown) => emit('updateConfig', { enable_query_expansion: Boolean(val) })"
            />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.embeddingTopK') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.embeddingTopK') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              :model-value="formData.config.embedding_top_k"
              :min="1"
              :max="50"
              theme="column"
              @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { embedding_top_k: val })"
            />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.keywordThreshold') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.keywordThreshold') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.keyword_threshold"
                :min="0"
                :max="1"
                :step="0.01"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { keyword_threshold: val })"
              />
              <span class="slider-value">{{ formData.config.keyword_threshold?.toFixed(2) }}</span>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.vectorThreshold') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.vectorThreshold') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.vector_threshold"
                :min="0"
                :max="1"
                :step="0.01"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { vector_threshold: val })"
              />
              <span class="slider-value">{{ formData.config.vector_threshold?.toFixed(2) }}</span>
            </div>
          </div>
        </div>

        <div v-if="formData.config.rerank_model_id" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.rerankTopK') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.rerankTopK') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              :model-value="formData.config.rerank_top_k"
              :min="1"
              :max="20"
              theme="column"
              @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { rerank_top_k: val })"
            />
          </div>
        </div>

        <div v-if="formData.config.rerank_model_id" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agent.editor.rerankThreshold') }}</label>
            <p class="desc">{{ $t('agentEditor.desc.rerankThreshold') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.rerank_threshold"
                :min="-10"
                :max="10"
                :step="0.01"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { rerank_threshold: val })"
              />
              <span class="slider-value">{{ formData.config.rerank_threshold?.toFixed(1) }}</span>
            </div>
          </div>
        </div>

        <div v-if="hasFaqKnowledgeBase" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.faq.enableLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.faq.enableDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.faq_priority_enabled"
              :aria-label="$t('agentEditor.faq.enableLabel')"
              @update:model-value="(val: unknown) => emit('updateConfig', { faq_priority_enabled: Boolean(val) })"
            />
          </div>
        </div>

        <div v-if="hasFaqKnowledgeBase && formData.config.faq_priority_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.faq.thresholdLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.faq.thresholdDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.faq_direct_answer_threshold"
                :min="0.7"
                :max="1"
                :step="0.05"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { faq_direct_answer_threshold: val })"
              />
              <span class="slider-value">{{ formData.config.faq_direct_answer_threshold?.toFixed(2) }}</span>
            </div>
          </div>
        </div>

        <div v-if="hasFaqKnowledgeBase && formData.config.faq_priority_enabled" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.faq.boostLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.faq.boostDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="slider-wrapper">
              <t-slider
                :model-value="formData.config.faq_score_boost"
                :min="1"
                :max="2"
                :step="0.1"
                @update:model-value="(val: unknown) => typeof val === 'number' && emit('updateConfig', { faq_score_boost: val })"
              />
              <span class="slider-value">{{ formData.config.faq_score_boost?.toFixed(1) }}x</span>
            </div>
          </div>
        </div>

        <div v-if="!isAgentMode" class="setting-row">
          <div class="setting-info">
            <label>{{ $t('agentEditor.dataAnalysis.enableLabel') }}</label>
            <p class="desc">{{ $t('agentEditor.dataAnalysis.enableDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-switch
              :model-value="formData.config.data_analysis_enabled"
              :aria-label="$t('agentEditor.dataAnalysis.enableLabel')"
              @update:model-value="(val: unknown) => emit('updateConfig', { data_analysis_enabled: Boolean(val) })"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WebSearchProviderEntity } from '@/api/web-search-provider'
import type { AgentEditorFormConfig, AgentEditorFormData } from '@/views/agent/composables/useAgentEditorForm'
import type { FilteredKbOption } from '@/views/agent/composables/useAgentTypePresets'

defineProps<{
  currentSection: string
  isAgentMode: boolean
  hasKnowledgeBase: boolean
  hasFaqKnowledgeBase: boolean
  kbSelectionMode: 'all' | 'selected' | 'none'
  filteredMyKbOptions: FilteredKbOption[]
  filteredSharedKbOptions: FilteredKbOption[]
  webSearchProviderList: WebSearchProviderEntity[]
  formData: AgentEditorFormData
}>()

const emit = defineEmits<{
  (e: 'update:kbSelectionMode', mode: 'all' | 'selected' | 'none'): void
  (e: 'updateConfig', patch: Partial<AgentEditorFormConfig>): void
}>()

const { t } = useI18n()

const availableFileTypes = computed(() => [
  { value: 'pdf', label: 'PDF', description: t('agentEditor.fileTypes.pdf') },
  { value: 'docx', label: 'Word', description: t('agentEditor.fileTypes.word') },
  { value: 'txt', label: t('agentEditor.fileTypes.textLabel'), description: t('agentEditor.fileTypes.text') },
  { value: 'md', label: 'Markdown', description: t('agentEditor.fileTypes.markdown') },
  { value: 'csv', label: 'CSV', description: t('agentEditor.fileTypes.csv') },
  { value: 'xlsx', label: 'Excel', description: t('agentEditor.fileTypes.excel') },
  { value: 'jpg', label: t('agentEditor.fileTypes.imageLabel'), description: t('agentEditor.fileTypes.image') },
])

function onKbModeChange(val: unknown) {
  if (val === 'all' || val === 'selected' || val === 'none') {
    emit('update:kbSelectionMode', val)
  }
}

function onKnowledgeBasesChange(val: unknown) {
  if (Array.isArray(val)) {
    emit('updateConfig', { knowledge_bases: val.map((item) => String(item)) })
  }
}

function onFileTypesChange(val: unknown) {
  if (Array.isArray(val)) {
    emit('updateConfig', { supported_file_types: val.map((item) => String(item)) })
  }
}
</script>
