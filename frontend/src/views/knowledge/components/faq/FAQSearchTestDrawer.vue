<template>
  <t-drawer
    :visible="visible"
    :header="$t('knowledgeEditor.faq.searchTestTitle')"
    :close-btn="true"
    size="420px"
    placement="right"
    class="faq-search-drawer"
    @update:visible="(val: boolean) => emit('update:visible', val)"
  >
    <div class="search-test-content">
      <t-form layout="vertical" class="search-form" :label-width="0">
        <div class="settings-group">
          <div class="setting-row vertical search-first-row">
            <div class="setting-info">
              <label for="faq-search-test-query">{{ $t('knowledgeEditor.faq.queryLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.faq.queryPlaceholder') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                id="faq-search-test-query"
                :model-value="searchForm.query"
                :placeholder="$t('knowledgeEditor.faq.queryPlaceholder')"
                class="full-width-input"
                @update:model-value="(v: string | number) => emit('update:query', String(v ?? ''))"
                @enter="emit('search')"
              />
            </div>
          </div>

          <div class="setting-row vertical">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.faq.similarityThresholdLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.faq.vectorThresholdDesc') }}</p>
            </div>
            <div class="setting-control">
              <div class="slider-wrapper">
                <t-slider
                  :model-value="searchForm.vectorThreshold"
                  :min="0"
                  :max="1"
                  :step="0.1"
                  :show-tooltip="true"
                  :format-tooltip="(val: number) => val.toFixed(2)"
                  @update:model-value="onVectorThresholdChange"
                />
                <div class="slider-value">{{ searchForm.vectorThreshold.toFixed(2) }}</div>
              </div>
            </div>
          </div>

          <div class="setting-row vertical">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.faq.matchCountLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.faq.matchCountDesc') }}</p>
            </div>
            <div class="setting-control">
              <div class="slider-wrapper">
                <t-slider
                  :model-value="searchForm.matchCount"
                  :min="1"
                  :max="50"
                  :step="1"
                  :show-tooltip="true"
                  @update:model-value="onMatchCountChange"
                />
                <div class="slider-value">{{ searchForm.matchCount }}</div>
              </div>
            </div>
          </div>

          <div class="setting-row vertical">
            <div class="setting-control">
              <t-button
                theme="primary"
                block
                :loading="searching"
                class="search-button"
                @click="emit('search')"
              >
                {{ searching ? $t('knowledgeEditor.faq.searching') : $t('knowledgeEditor.faq.searchButton') }}
              </t-button>
            </div>
          </div>
        </div>
      </t-form>

      <div v-if="searchResults.length > 0 || hasSearched" class="search-results">
        <div class="results-header">
          <span>{{ $t('knowledgeEditor.faq.searchResults') }} ({{ searchResults.length }})</span>
        </div>
        <div v-if="searchResults.length === 0" class="no-results">
          {{ $t('knowledgeEditor.faq.noResults') }}
        </div>
        <div v-else class="results-list">
          <div
            v-for="(result, index) in searchResults"
            :key="result.id"
            class="result-card"
            :class="{ expanded: result.expanded }"
          >
            <button
              type="button"
              class="result-header"
              :aria-expanded="!!result.expanded"
              @click="emit('toggle-result', result)"
            >
              <span class="result-question-wrapper">
                <span class="result-main">
                  <span class="result-question">
                    <span class="result-index">{{ index + 1 }}.</span>
                    {{ result.standard_question }}
                  </span>
                  <span
                    v-if="result.matched_question && result.matched_question !== result.standard_question"
                    class="matched-question"
                  >
                    <span class="matched-label">{{ $t('knowledgeEditor.faq.matchedQuestion') }}:</span>
                    <span class="matched-text">{{ result.matched_question }}</span>
                  </span>
                </span>
                <span class="result-meta">
                  <t-tag size="small" variant="light-outline" class="score-tag">
                    {{ (result.score || 0).toFixed(3) }}
                  </t-tag>
                </span>
                <t-icon :name="result.expanded ? 'chevron-up' : 'chevron-down'" class="expand-icon" />
              </span>
            </button>
            <Transition name="slide-down">
              <div v-if="result.expanded" class="result-body">
                <div v-if="result.answers?.length" class="result-section">
                  <div class="section-label">{{ $t('knowledgeEditor.faq.answers') }}</div>
                  <div class="result-tags">
                    <t-tooltip v-for="answer in result.answers" :key="answer" :content="answer" placement="top">
                      <t-tag size="small" theme="success" variant="light" class="answer-tag">
                        {{ answer }}
                      </t-tag>
                    </t-tooltip>
                  </div>
                </div>
                <div v-if="result.similar_questions?.length" class="result-section">
                  <div class="section-label">{{ $t('knowledgeEditor.faq.similarQuestions') }}</div>
                  <div class="result-tags">
                    <t-tooltip
                      v-for="question in result.similar_questions"
                      :key="question"
                      :content="question"
                      placement="top"
                    >
                      <t-tag size="small" variant="light-outline" class="question-tag">
                        {{ question }}
                      </t-tag>
                    </t-tooltip>
                  </div>
                </div>
              </div>
            </Transition>
          </div>
        </div>
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import type { FAQEntry } from '../../composables/useFAQEntries'
import type { FAQSearchFormState } from '../../composables/useFAQSearchTest'

defineProps<{
  visible: boolean
  searchForm: FAQSearchFormState
  searching: boolean
  hasSearched: boolean
  searchResults: FAQEntry[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'update:query', val: string): void
  (e: 'update:vectorThreshold', val: number): void
  (e: 'update:matchCount', val: number): void
  (e: 'search'): void
  (e: 'toggle-result', result: FAQEntry): void
}>()

function onVectorThresholdChange(val: number | number[]) {
  const num = Array.isArray(val) ? val[0] : val
  if (typeof num === 'number' && Number.isFinite(num)) {
    emit('update:vectorThreshold', num)
  }
}

function onMatchCountChange(val: number | number[]) {
  const num = Array.isArray(val) ? val[0] : val
  if (typeof num === 'number' && Number.isFinite(num)) {
    emit('update:matchCount', num)
  }
}
</script>

<style lang="less">
.faq-search-drawer {
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
  }

  .setting-info {
    width: 100%;

    label {
      font-size: var(--gy-text-base);
      font-weight: 600;
      color: var(--gy-text-primary);
      display: inline-flex;
      align-items: center;
      gap: var(--gy-space-1);
      margin-bottom: var(--gy-space-1);
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

  .full-width-input {
    width: 100%;
  }

  .search-test-content {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-4);
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .slider-wrapper {
    display: flex;
    align-items: center;
    gap: var(--gy-space-3);
    width: 100%;

    .t-slider {
      flex: 1;
      min-width: 0;
    }
  }

  .slider-value {
    flex-shrink: 0;
    min-width: 50px;
    text-align: right;
    font-size: var(--gy-text-base);
    font-weight: 500;
    color: var(--gy-text-primary);
    padding: var(--gy-space-1) var(--gy-space-2);
    background: var(--gy-surface-subtle);
    border-radius: var(--gy-radius-sm);
    font-variant-numeric: tabular-nums;
  }

  .search-results {
    display: flex;
    flex-direction: column;
    padding-top: var(--gy-space-5);
    width: 100%;
  }

  .results-header {
    display: flex;
    align-items: center;
    gap: var(--gy-space-2);
    margin-bottom: var(--gy-space-4);
    font-size: var(--gy-text-base);
    font-weight: 600;
    color: var(--gy-text-primary);
  }

  .no-results {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 48px var(--gy-space-4);
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-base);
    text-align: center;
    background: var(--gy-surface);
    border-radius: var(--gy-radius-md);
    border: 1px dashed var(--gy-border-subtle);
  }

  .results-list {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-3);
  }

  .result-card {
    border: 1px solid var(--gy-border-subtle);
    border-radius: var(--gy-radius-md);
    background: var(--gy-surface);
    padding: 14px;
    transition: border-color var(--gy-motion-base) ease;
    width: 100%;
    box-sizing: border-box;

    &:hover {
      border-color: var(--gy-brand);
    }
  }

  .result-header {
    width: 100%;
    border: 0;
    background: transparent;
    font: inherit;
    text-align: left;
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-2);
    cursor: pointer;
    padding: var(--gy-space-1);
    margin: -4px;
    border-radius: var(--gy-radius-sm);

    &:focus-visible {
      outline: 2px solid var(--gy-border-focus);
      outline-offset: 2px;
    }
  }

  .result-card.expanded .result-header {
    margin-bottom: var(--gy-space-3);
    padding-bottom: var(--gy-space-3);
    border-bottom: 1px solid var(--gy-border-subtle);
  }

  .result-question-wrapper {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    width: 100%;
  }

  .result-main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-1);
  }

  .result-question {
    font-size: var(--gy-text-base);
    font-weight: 600;
    color: var(--gy-text-primary);
    line-height: 1.6;
    word-break: break-word;
    display: flex;
    align-items: flex-start;
    gap: 6px;

    .result-index {
      flex-shrink: 0;
      color: var(--gy-brand);
      font-weight: 600;
    }
  }

  .matched-question {
    display: flex;
    align-items: flex-start;
    gap: var(--gy-space-1);
    padding-left: var(--gy-space-5);
    font-size: var(--gy-text-sm);
    line-height: 1.5;

    .matched-label {
      flex-shrink: 0;
      color: var(--gy-warning);
      font-weight: 500;
    }

    .matched-text {
      color: var(--gy-warning);
      background: var(--gy-warning-soft);
      padding: 1px 6px;
      border-radius: var(--gy-radius-xs);
      word-break: break-word;
    }
  }

  .result-meta {
    display: flex;
    gap: var(--gy-space-2);
    flex-shrink: 0;
    margin-left: auto;
  }

  .expand-icon {
    flex-shrink: 0;
    font-size: var(--gy-text-2xl);
    color: var(--gy-text-secondary);
  }

  .result-body {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-3);
    padding-top: var(--gy-space-3);
    border-top: 1px solid var(--gy-border-subtle);
    width: 100%;
  }

  .result-section {
    display: flex;
    flex-direction: column;
    gap: var(--gy-space-2);
  }

  .section-label {
    font-size: var(--gy-text-sm);
    font-weight: 600;
    color: var(--gy-text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .result-tags {
    display: flex;
    flex-wrap: wrap;
    gap: var(--gy-space-1);
    width: 100%;
    min-width: 0;
  }

  .answer-tag {
    background: var(--gy-brand-tint);
    color: var(--gy-brand);
    border-color: color-mix(in oklab, var(--gy-brand) 24%, transparent);
  }
}

.slide-down-enter-active,
.slide-down-leave-active {
  transition:
    opacity var(--gy-motion-base) cubic-bezier(0.4, 0, 0.2, 1),
    transform var(--gy-motion-base) cubic-bezier(0.4, 0, 0.2, 1);
  overflow: hidden;
}

.slide-down-enter-from,
.slide-down-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
