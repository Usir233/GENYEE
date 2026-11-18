<template>
  <div class="wiki-content">
    <div class="wiki-reader">
      <div class="wiki-reader-inner">
        <template v-if="selectedPage">
          <div v-if="hasBackNavigation" class="wiki-nav-bar">
            <a href="#" class="wiki-nav-back" @click.prevent="emit('go-back')">
              <t-icon name="arrow-left" size="14px" />
              <span>{{ backLabel }}</span>
            </a>
          </div>

          <div class="wiki-reader-header">
            <div class="wiki-reader-title-row">
              <div class="wiki-reader-title-block">
                <h2 v-if="!editingPage" class="wiki-reader-title">
                  <span class="wiki-reader-title-text">{{ selectedPage.title }}</span>

                  <t-popup
                    v-if="pageIssues.length > 0"
                    :visible="showIssuesBox"
                    placement="bottom-left"
                    trigger="click"
                    :overlay-inner-style="{
                      padding: 0,
                      boxShadow: 'var(--gy-shadow-md)',
                      borderRadius: 'var(--gy-radius-md)',
                      width: '560px',
                      maxWidth: '90vw',
                    }"
                    @visible-change="(val: boolean) => emit('update:showIssuesBox', val)"
                  >
                    <button
                      type="button"
                      class="wiki-issue-trigger"
                      :title="$t('knowledgeEditor.wikiBrowser.issueTitle', { count: pageIssues.length })"
                      :aria-label="$t('knowledgeEditor.wikiBrowser.issueTitle', { count: pageIssues.length })"
                    >
                      <t-icon name="error-circle-filled" class="wiki-warning-icon" />
                    </button>

                    <template #content>
                      <div class="wiki-issue-popup-content">
                        <div class="wiki-issue-popup-header">
                          <div class="wiki-issue-popup-title">
                            <span>
                              {{ $t('knowledgeEditor.wikiBrowser.issueFixSuggestions', { count: pageIssues.length }) }}
                            </span>
                          </div>
                          <AssistantButton
                            v-if="canEdit"
                            :label="$t('knowledgeEditor.wikiBrowser.issueFixBtn')"
                            @click="emit('auto-fix')"
                          />
                        </div>
                        <div class="wiki-issue-popup-list">
                          <div v-for="issue in pageIssues" :key="issue.id" class="wiki-issue-popup-item">
                            <div class="wiki-issue-popup-main">
                              <div class="wiki-issue-popup-tags">
                                <t-tag
                                  v-if="issue.issue_type === 'mixed_entities'"
                                  theme="warning"
                                  variant="light"
                                  size="small"
                                >
                                  {{ $t('knowledgeEditor.wikiBrowser.issueMixed') }}
                                </t-tag>
                                <t-tag
                                  v-else-if="issue.issue_type === 'contradictory_facts'"
                                  theme="danger"
                                  variant="light"
                                  size="small"
                                >
                                  {{ $t('knowledgeEditor.wikiBrowser.issueConflict') }}
                                </t-tag>
                                <t-tag
                                  v-else-if="issue.issue_type === 'out_of_date'"
                                  theme="default"
                                  variant="light"
                                  size="small"
                                >
                                  {{ $t('knowledgeEditor.wikiBrowser.issueOutdated') }}
                                </t-tag>
                                <t-tag v-else theme="primary" variant="light" size="small">
                                  {{ $t('knowledgeEditor.wikiBrowser.issueAttention') }}
                                </t-tag>
                              </div>
                              <div class="wiki-issue-popup-desc">{{ issue.description }}</div>
                              <div class="wiki-issue-popup-meta">
                                <span class="wiki-issue-popup-reporter">
                                  {{
                                    issue.reported_by === 'wiki-researcher-agent'
                                      ? $t('knowledgeEditor.wikiBrowser.issueAiLinter')
                                      : $t('knowledgeEditor.wikiBrowser.issueReportedBy', { reporter: issue.reported_by })
                                  }}
                                </span>
                                <div v-if="canEdit" class="wiki-issue-popup-actions">
                                  <AssistantButton
                                    :label="$t('knowledgeEditor.wikiBrowser.issueFixSingle')"
                                    @click="emit('fix-issue', issue)"
                                  />
                                  <button
                                    type="button"
                                    class="wiki-issue-popup-action wiki-issue-popup-action--muted"
                                    @click="emit('ignore-issue', issue.id)"
                                  >
                                    {{ $t('knowledgeEditor.wikiBrowser.issueIgnore') }}
                                  </button>
                                </div>
                              </div>
                            </div>
                          </div>
                        </div>
                      </div>
                    </template>
                  </t-popup>
                </h2>
                <t-input
                  v-else
                  :model-value="editForm.title"
                  class="wiki-edit-field wiki-edit-field--title"
                  :placeholder="$t('knowledgeEditor.wikiBrowser.editTitlePlaceholder')"
                  @update:model-value="(v: string | number) => emit('update:editTitle', String(v ?? ''))"
                />

                <div class="wiki-reader-title-badges wiki-reader-title-badges--secondary">
                  <span v-if="editingPage" class="wiki-badge wiki-badge--editing">
                    {{ $t('knowledgeEditor.wikiBrowser.editingBadge') }}
                  </span>
                  <span class="wiki-badge wiki-badge--type">
                    <t-icon :name="getPageIcon(selectedPage)" />
                    {{ getTypeLabel(selectedPage.page_type) }}
                  </span>
                  <span class="wiki-badge wiki-badge--ver">
                    {{ $t('knowledgeEditor.wikiBrowser.version', { ver: selectedPage.version }) }}
                  </span>
                  <t-tooltip
                    v-if="editSourceVisible(selectedPage.last_edit_source)"
                    :content="editSourceLabel(selectedPage.last_edit_source)"
                  >
                    <span class="wiki-badge wiki-badge--source">
                      <t-icon :name="editSourceIcon(selectedPage.last_edit_source)" />
                      {{ editSourceLabel(selectedPage.last_edit_source) }}
                    </span>
                  </t-tooltip>
                  <span
                    v-for="alias in selectedPage.aliases || []"
                    :key="alias"
                    class="wiki-badge wiki-badge--alias"
                    :title="`${$t('knowledgeEditor.wikiBrowser.aliases')} ${alias}`"
                  >
                    <t-icon name="link" />
                    {{ alias }}
                  </span>
                </div>

                <p v-if="!editingPage && selectedPage.summary" class="wiki-reader-lead">
                  {{ selectedPage.summary }}
                </p>
                <t-textarea
                  v-if="editingPage"
                  :model-value="editForm.summary"
                  class="wiki-edit-field wiki-edit-field--summary"
                  :autosize="{ minRows: 2, maxRows: 4 }"
                  :placeholder="$t('knowledgeEditor.wikiBrowser.editSummaryPlaceholder')"
                  @update:model-value="(v: string | number) => emit('update:editSummary', String(v ?? ''))"
                />
              </div>

              <div class="wiki-reader-aside">
                <div class="wiki-reader-actions" role="toolbar" :aria-label="$t('knowledgeEditor.wikiBrowser.pageActions')">
                  <template v-if="editingPage">
                    <t-button theme="primary" size="small" :loading="savingPage" @click="emit('save-edit')">
                      {{ $t('common.save') }}
                    </t-button>
                    <t-button variant="text" size="small" :disabled="savingPage" @click="emit('cancel-edit')">
                      {{ $t('common.cancel') }}
                    </t-button>
                  </template>
                  <template v-else>
                    <AssistantActionsButton
                      icon-only
                      :label="$t('assistant.actions.readContent')"
                      :actions="readingAssistantActions"
                    />
                    <t-tooltip v-if="canEdit" :content="$t('knowledgeEditor.wikiBrowser.editBtn')" placement="top">
                      <button
                        type="button"
                        class="wiki-action-btn"
                        :aria-label="$t('knowledgeEditor.wikiBrowser.editBtn')"
                        @click="emit('start-edit')"
                      >
                        <t-icon name="edit" />
                      </button>
                    </t-tooltip>
                    <t-tooltip :content="$t('knowledgeEditor.wikiBrowser.historyBtn')" placement="top">
                      <button
                        type="button"
                        class="wiki-action-btn"
                        :aria-label="$t('knowledgeEditor.wikiBrowser.historyBtn')"
                        @click="emit('open-history')"
                      >
                        <t-icon name="history" />
                      </button>
                    </t-tooltip>
                    <t-tooltip :content="$t('knowledgeEditor.wikiBrowser.viewInGraph')" placement="top">
                      <button
                        type="button"
                        class="wiki-action-btn"
                        :aria-label="$t('knowledgeEditor.wikiBrowser.viewInGraph')"
                        @click="emit('view-graph', selectedPage.slug)"
                      >
                        <t-icon name="chart-bubble" />
                      </button>
                    </t-tooltip>
                    <t-popconfirm
                      v-if="canEdit"
                      theme="danger"
                      :content="$t('knowledgeEditor.wikiBrowser.deletePageConfirm', { title: selectedPage.title })"
                      @confirm="emit('delete-page')"
                    >
                      <t-tooltip :content="$t('knowledgeEditor.wikiBrowser.deletePageBtn')" placement="top">
                        <button
                          type="button"
                          class="wiki-action-btn wiki-action-btn--danger"
                          :aria-label="$t('knowledgeEditor.wikiBrowser.deletePageBtn')"
                        >
                          <t-icon name="delete" />
                        </button>
                      </t-tooltip>
                    </t-popconfirm>
                  </template>
                </div>

                <div v-if="!editingPage" class="wiki-reader-aside-meta">
                  <span class="wiki-reader-aside-meta-item">
                    <t-icon name="time" size="14px" />
                    {{ formatDate(selectedPage.updated_at) }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- eslint-disable-next-line vue/no-v-html -->
          <div
            v-if="!editingPage"
            :ref="bindReaderBodyEl"
            class="wiki-reader-body"
            v-html="renderedContent"
            @click="emit('content-click', $event)"
          ></div>

          <div v-else class="wiki-page-editor">
            <t-textarea
              :model-value="editForm.content"
              class="wiki-edit-field wiki-edit-field--content"
              :autosize="{ minRows: 16, maxRows: 40 }"
              :placeholder="$t('knowledgeEditor.wikiBrowser.editContentPlaceholder')"
              @update:model-value="(v: string | number) => emit('update:editContent', String(v ?? ''))"
            />
            <t-alert
              v-if="editConflictVersion !== null"
              theme="warning"
              class="wiki-page-editor-conflict"
              :message="$t('knowledgeEditor.wikiBrowser.editConflictHint', { ver: editConflictVersion })"
            >
              <template #operation>
                <span class="wiki-page-editor-conflict-actions">
                  <t-link theme="primary" hover="color" @click="emit('reload-latest')">
                    {{ $t('knowledgeEditor.wikiBrowser.editConflictReload') }}
                  </t-link>
                  <t-link theme="warning" hover="color" @click="emit('overwrite-save')">
                    {{ $t('knowledgeEditor.wikiBrowser.editConflictOverwrite') }}
                  </t-link>
                </span>
              </template>
            </t-alert>
          </div>

          <footer
            v-if="!editingPage && (selectedPage.in_links?.length || parsedSourceRefs.length)"
            class="wiki-reader-footer"
          >
            <div v-if="selectedPage.in_links?.length" class="wiki-reader-footer-row">
              <span class="wiki-reader-footer-label">{{ $t('knowledgeEditor.wikiBrowser.linkedFrom') }}</span>
              <span class="wiki-reader-footer-value">
                <a
                  v-for="link in selectedPage.in_links"
                  :key="'in-' + link"
                  href="#"
                  class="wiki-content-link"
                  @click.prevent="emit('navigate-slug', link)"
                >
                  {{ slugDisplayName(link) }}
                </a>
              </span>
            </div>
            <div v-if="parsedSourceRefs.length" class="wiki-reader-footer-row">
              <span class="wiki-reader-footer-label">{{ $t('knowledgeEditor.wikiBrowser.sources') }}</span>
              <span class="wiki-reader-footer-value">
                <a
                  v-for="refItem in parsedSourceRefs"
                  :key="refItem.id"
                  href="#"
                  class="wiki-content-link"
                  @click.prevent="emit('open-source-doc', refItem.id)"
                >
                  {{ refItem.title }}
                </a>
              </span>
            </div>
          </footer>
        </template>

        <template v-else-if="activeSystemView === 'index'">
          <div class="wiki-reader-header">
            <h2 class="wiki-reader-title">{{ $t('knowledgeEditor.wikiBrowser.indexTitle') }}</h2>
            <div class="wiki-reader-meta">
              <t-tag size="small" theme="default" variant="light-outline">
                {{ $t('knowledgeEditor.wikiBrowser.indexOverviewTag') }}
              </t-tag>
            </div>
          </div>
          <div v-if="indexLoading && !indexMarkdown" class="wiki-reader-empty">
            <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.loading') }}</p>
          </div>
          <template v-else-if="indexMarkdown">
            <!-- eslint-disable-next-line vue/no-v-html -->
            <div
              :ref="bindIndexBodyEl"
              class="wiki-reader-body wiki-index-body"
              v-html="renderedIndexMarkdown"
              @click="emit('content-click', $event)"
            ></div>
            <div v-if="indexHasMore" :ref="bindIndexSentinelEl" class="wiki-index-sentinel">
              <span v-if="indexLoading" class="wiki-index-loading">
                {{ $t('knowledgeEditor.wikiBrowser.loading') }}
              </span>
            </div>
          </template>
          <div v-else-if="!indexLoading" class="wiki-reader-empty">
            <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.indexEmpty') }}</p>
          </div>
        </template>

        <div v-else class="wiki-reader-empty">
          <div class="wiki-empty-icon">
            <t-icon name="browse" size="48px" />
          </div>
          <p v-if="hasContentPages" class="wiki-empty-title">
            {{ $t('knowledgeEditor.wikiBrowser.selectPageHint') }}
          </p>
          <template v-else>
            <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.emptyTitle') }}</p>
            <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.emptyDesc') }}</p>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { ComponentPublicInstance } from 'vue'
import type { WikiPage, WikiPageIssue } from '@/api/wiki'
import AssistantButton from '@/components/assistant/AssistantButton.vue'
import AssistantActionsButton from '@/components/assistant/AssistantActionsButton.vue'
import { formatWikiDate, type WikiEditPageFormState } from '../composables/useWikiPageEditor'

const props = defineProps<{
  selectedPage: WikiPage | null
  canEdit?: boolean
  hasBackNavigation: boolean
  backLabel: string
  pageIssues: WikiPageIssue[]
  showIssuesBox: boolean
  editingPage: boolean
  savingPage: boolean
  editForm: WikiEditPageFormState
  editConflictVersion: number | null
  renderedContent: string
  readingAssistantActions: Array<{ key: string; label: string; query: string }>
  parsedSourceRefs: Array<{ id: string; title: string }>
  activeSystemView: '' | 'index'
  indexLoading: boolean
  indexMarkdown: string
  renderedIndexMarkdown: string
  indexHasMore: boolean
  hasContentPages: boolean
  getPageIcon: (page: Pick<WikiPage, 'page_type'>) => string
  getTypeLabel: (type: string) => string
  editSourceVisible: (source?: string) => boolean
  editSourceLabel: (source?: string) => string
  editSourceIcon: (source?: string) => string
  slugDisplayName: (slug: string) => string
  setReaderBodyEl: (el: HTMLElement | null) => void
  setIndexBodyEl: (el: HTMLElement | null) => void
  setIndexSentinelEl: (el: HTMLElement | null) => void
}>()

const emit = defineEmits<{
  (e: 'go-back'): void
  (e: 'update:showIssuesBox', val: boolean): void
  (e: 'auto-fix'): void
  (e: 'fix-issue', issue: WikiPageIssue): void
  (e: 'ignore-issue', issueId: string): void
  (e: 'start-edit'): void
  (e: 'cancel-edit'): void
  (e: 'save-edit'): void
  (e: 'reload-latest'): void
  (e: 'overwrite-save'): void
  (e: 'update:editTitle', val: string): void
  (e: 'update:editSummary', val: string): void
  (e: 'update:editContent', val: string): void
  (e: 'open-history'): void
  (e: 'view-graph', slug: string): void
  (e: 'delete-page'): void
  (e: 'content-click', event: MouseEvent): void
  (e: 'navigate-slug', slug: string): void
  (e: 'open-source-doc', knowledgeId: string): void
}>()

function bindReaderBodyEl(el: Element | ComponentPublicInstance | null) {
  props.setReaderBodyEl(el instanceof HTMLElement ? el : null)
}

function bindIndexBodyEl(el: Element | ComponentPublicInstance | null) {
  props.setIndexBodyEl(el instanceof HTMLElement ? el : null)
}

function bindIndexSentinelEl(el: Element | ComponentPublicInstance | null) {
  props.setIndexSentinelEl(el instanceof HTMLElement ? el : null)
}

function formatDate(dateStr: string): string {
  return formatWikiDate(dateStr)
}
</script>
