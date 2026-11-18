<template>
  <t-drawer
    :visible="visible"
    :header="$t('knowledgeEditor.wikiBrowser.globalIssuesTitle')"
    size="480px"
    :footer="false"
    class="wiki-global-issues-drawer"
    @update:visible="(val: boolean) => emit('update:visible', val)"
  >
    <div class="wiki-issue-popup-list">
      <div v-for="issue in issues" :key="issue.id" class="wiki-issue-popup-item">
        <div class="wiki-issue-popup-main">
          <div class="wiki-issue-popup-tags">
            <t-tag v-if="issue.issue_type === 'mixed_entities'" theme="warning" variant="light" size="small">
              {{ $t('knowledgeEditor.wikiBrowser.issueMixed') }}
            </t-tag>
            <t-tag v-else-if="issue.issue_type === 'contradictory_facts'" theme="danger" variant="light" size="small">
              {{ $t('knowledgeEditor.wikiBrowser.issueConflict') }}
            </t-tag>
            <t-tag v-else-if="issue.issue_type === 'out_of_date'" theme="default" variant="light" size="small">
              {{ $t('knowledgeEditor.wikiBrowser.issueOutdated') }}
            </t-tag>
            <t-tag v-else theme="primary" variant="light" size="small">
              {{ $t('knowledgeEditor.wikiBrowser.issueAttention') }}
            </t-tag>
          </div>
          <div class="wiki-issue-popup-desc">
            <button
              type="button"
              class="wiki-issue-page-link"
              @click="emit('navigate-fix', issue.slug)"
            >
              <t-icon name="link" size="12px" />
              <span>{{ $t('knowledgeEditor.wikiBrowser.issuePagePrefix') }}{{ slugDisplayName(issue.slug) }}</span>
            </button>
            <div>{{ issue.description }}</div>
          </div>
          <div class="wiki-issue-popup-meta">
            <span class="wiki-issue-popup-reporter">
              {{
                issue.reported_by === 'wiki-researcher-agent'
                  ? $t('knowledgeEditor.wikiBrowser.issueAiLinter')
                  : $t('knowledgeEditor.wikiBrowser.issueReportedBy', { reporter: issue.reported_by })
              }}
            </span>
            <div v-if="canEdit" class="wiki-issue-popup-actions">
              <button
                type="button"
                class="wiki-issue-popup-action wiki-issue-popup-action--strong"
                @click="emit('navigate-fix', issue.slug)"
              >
                <t-icon name="arrow-right-circle" class="wiki-issue-go-icon" />
                <span>{{ $t('knowledgeEditor.wikiBrowser.issueGoFix') }}</span>
              </button>
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
      <div v-if="issues.length === 0" class="wiki-global-issues-empty">
        {{ $t('knowledgeEditor.wikiBrowser.globalIssuesEmpty') }}
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import type { WikiPageIssue } from '@/api/wiki'

defineProps<{
  visible: boolean
  issues: WikiPageIssue[]
  canEdit?: boolean
  slugDisplayName: (slug: string) => string
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'navigate-fix', slug: string): void
  (e: 'ignore-issue', issueId: string): void
}>()
</script>
