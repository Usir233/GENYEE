<template>
  <SettingDrawer
    :visible="visible"
    :title="$t('tenantMember.audit.tabLabel')"
    width="1120px"
    :min-width="720"
    :max-width="1600"
    storage-key="setting-drawer:width:tenant-members-audit"
    :hide-footer="true"
    drawer-class-name="tenant-members-audit-drawer"
    @update:visible="(val: boolean) => emit('update:visible', val)"
  >
    <div class="audit-drawer-inner audit-panel audit-panel--drawer">
      <div class="audit-header">
        <span class="audit-desc">{{ $t('tenantMember.audit.description') }}</span>
        <t-button
          variant="text"
          size="small"
          class="audit-refresh-btn"
          :loading="loading"
          :disabled="loading"
          @click="emit('reload')"
        >
          <template #icon><t-icon name="refresh" /></template>
          {{ $t('tenantMember.audit.refresh') }}
        </t-button>
      </div>

      <div class="audit-drawer-fill">
        <div v-if="error" class="audit-drawer-branch audit-drawer-branch--error">
          <div class="error-inline">
            <t-alert theme="error" :message="error">
              <template #operation>
                <t-button size="small" @click="emit('reload')">
                  {{ $t('tenantMember.retry') }}
                </t-button>
              </template>
            </t-alert>
          </div>
        </div>

        <div
          v-else-if="!loading && entries.length === 0"
          class="audit-drawer-branch audit-drawer-branch--empty empty-state empty-state--audit"
        >
          <t-empty :description="$t('tenantMember.audit.empty')" />
        </div>

        <div v-else ref="auditScrollRoot" class="audit-scroll-area narrow-scrollbar audit-drawer-branch">
          <div class="data-table-shell audit-table-shell">
            <t-table
              row-key="id"
              :data="entries"
              :columns="auditColumns"
              size="medium"
              hover
              expand-on-row-click
              :expanded-row-keys="expandedRowKeys"
              @expand-change="(val: Array<string | number>) => emit('expand-change', val)"
            >
              <template #created_at="{ row }">
                <div class="audit-time">
                  <span class="audit-time-date">{{ formatAuditDatePart(row.created_at, locale) }}</span>
                  <span class="audit-time-clock">{{ formatAuditTimePart(row.created_at, locale) }}</span>
                </div>
              </template>
              <template #actor="{ row }">
                <div class="audit-actor">
                  <span class="audit-actor-name">
                    {{
                      row.actor_user_id
                        ? actorDisplayName(row.actor_user_id)
                        : $t('tenantMember.audit.systemActor')
                    }}
                  </span>
                  <span v-if="row.actor_role" class="audit-actor-role">
                    {{ $t('tenantMember.role.' + row.actor_role) }}
                  </span>
                </div>
              </template>
              <template #action="{ row }">
                <t-tag :theme="auditActionTheme(row.action)" size="small" variant="light-outline">
                  {{ formatAuditAction(row.action) }}
                </t-tag>
              </template>
              <template #target="{ row }">
                <div class="audit-target">
                  <span v-if="auditTargetSubject(row)" class="audit-target-key">{{
                    auditTargetSubject(row)
                  }}</span>
                  <span v-if="auditTargetDiff(row)" class="audit-target-diff">{{
                    auditTargetDiff(row)
                  }}</span>
                  <span v-else-if="!auditTargetSubject(row)" class="audit-target-empty">—</span>
                </div>
              </template>
              <template #request_path="{ row }">
                <span v-if="row.request_path" class="audit-path">
                  <span v-if="row.request_method" class="audit-method">{{ row.request_method }}</span>
                  {{ row.request_path }}
                </span>
                <span v-else class="audit-target-empty">—</span>
              </template>
              <template #outcome="{ row }">
                <t-tag :theme="auditOutcomeTheme(row.outcome)" size="small" variant="light">
                  {{ $t('tenantMember.audit.outcome.' + row.outcome) }}
                </t-tag>
              </template>
              <template #expandedRow="{ row }">
                <div class="audit-expanded">
                  <div class="audit-expanded-grid">
                    <div class="audit-expanded-cell">
                      <span class="audit-expanded-label">{{ $t('tenantMember.audit.expanded.actorId') }}</span>
                      <span class="audit-expanded-value mono">{{ row.actor_user_id || '—' }}</span>
                    </div>
                    <div v-if="row.target_user_id" class="audit-expanded-cell">
                      <span class="audit-expanded-label">{{
                        $t('tenantMember.audit.expanded.targetUserId')
                      }}</span>
                      <span class="audit-expanded-value mono">{{ row.target_user_id }}</span>
                    </div>
                    <div v-if="row.target_type" class="audit-expanded-cell">
                      <span class="audit-expanded-label">{{
                        $t('tenantMember.audit.expanded.targetType')
                      }}</span>
                      <span class="audit-expanded-value mono">{{ row.target_type }}</span>
                    </div>
                    <div v-if="row.target_id" class="audit-expanded-cell">
                      <span class="audit-expanded-label">{{ $t('tenantMember.audit.expanded.targetId') }}</span>
                      <span class="audit-expanded-value mono">{{ row.target_id }}</span>
                    </div>
                  </div>
                  <div class="audit-expanded-details">
                    <span class="audit-expanded-label">{{ $t('tenantMember.audit.expanded.details') }}</span>
                    <pre class="audit-expanded-json mono">{{ auditDetailsJSON(row) }}</pre>
                  </div>
                </div>
              </template>
            </t-table>
          </div>

          <div ref="auditLoadSentinelEl" class="audit-load-sentinel" aria-hidden="true" />

          <div v-if="loading && entries.length > 0" class="audit-loading-more">
            <t-loading size="small" />
            <span>{{ $t('tenantMember.loading') }}</span>
          </div>

          <p v-if="!hasMore && entries.length > 0 && !loading" class="audit-end-hint">
            {{ $t('tenantMember.audit.end') }}
          </p>
        </div>
      </div>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import type { AuditAction, AuditLog } from '@/api/tenant/audit-log'
import {
  auditActionTheme,
  auditDetailsJSON,
  auditOutcomeTheme,
  formatAuditDatePart,
  formatAuditTimePart,
} from '../composables/useTenantAuditLog'

const props = defineProps<{
  visible: boolean
  entries: AuditLog[]
  loading: boolean
  error: string
  hasMore: boolean
  expandedRowKeys: number[]
  actorDisplayName: (userId: string) => string
  formatAuditAction: (action: AuditAction) => string
  auditTargetSubject: (row: AuditLog) => string
  auditTargetDiff: (row: AuditLog) => string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  reload: []
  'load-more': []
  'expand-change': [value: Array<string | number>]
}>()

const { t, locale } = useI18n()

const auditScrollRoot = ref<HTMLElement | null>(null)
const auditLoadSentinelEl = ref<HTMLElement | null>(null)
let auditScrollObserver: IntersectionObserver | null = null

const auditColumns = computed(() => [
  { colKey: 'created_at', title: t('tenantMember.audit.columns.time'), width: 120 },
  { colKey: 'actor', title: t('tenantMember.audit.columns.actor'), width: 180 },
  { colKey: 'action', title: t('tenantMember.audit.columns.action'), width: 130 },
  { colKey: 'target', title: t('tenantMember.audit.columns.target'), minWidth: 200 },
  { colKey: 'request_path', title: t('tenantMember.audit.columns.path'), minWidth: 160 },
  { colKey: 'outcome', title: t('tenantMember.audit.columns.outcome'), width: 80, align: 'center' as const },
])

function detachAuditInfiniteScroll() {
  auditScrollObserver?.disconnect()
  auditScrollObserver = null
}

function attachAuditInfiniteScroll() {
  detachAuditInfiniteScroll()
  if (typeof IntersectionObserver === 'undefined') return
  const root = auditScrollRoot.value
  const sentinel = auditLoadSentinelEl.value
  if (!root || !sentinel) return

  auditScrollObserver = new IntersectionObserver(
    (entries) => {
      const hitBottom = entries.some((e) => e.isIntersecting)
      if (!hitBottom || !props.hasMore || props.loading) return
      emit('load-more')
    },
    { root, rootMargin: '100px 0px', threshold: 0 },
  )
  auditScrollObserver.observe(sentinel)
}

watch(
  [() => props.visible, () => props.error, () => props.entries.length > 0],
  async ([open, err]) => {
    if (!open || err) {
      detachAuditInfiniteScroll()
      return
    }
    await nextTick()
    attachAuditInfiniteScroll()
  },
  { flush: 'post' },
)

onUnmounted(() => detachAuditInfiniteScroll())
</script>

<style lang="less" scoped>
@import '../tenant-members.less';

.audit-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-top: 8px;
}

.audit-panel--drawer {
  padding-top: 0;
}

.audit-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--gy-surface-subtle);
  padding: 12px 16px;
  border-radius: var(--gy-radius-md);
  gap: 12px;

  .audit-desc {
    flex: 1;
    min-width: 0;
    font-size: var(--gy-text-md);
    color: var(--gy-text-secondary);
  }

  .audit-refresh-btn {
    flex-shrink: 0;
  }
}

.audit-drawer-inner {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  gap: 14px;
  min-height: 0;
  width: 100%;
  box-sizing: border-box;
}

.audit-drawer-fill {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-drawer-branch {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-drawer-branch--error {
  justify-content: center;

  .error-inline {
    width: 100%;
  }
}

.audit-drawer-branch--empty.empty-state--audit {
  flex: 1 1 auto;
  justify-content: center;
  align-items: center;
  padding: 24px 12px;
  min-height: 0;
}

.audit-scroll-area {
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
}

.audit-load-sentinel {
  height: 1px;
  width: 100%;
  pointer-events: none;
}

.audit-loading-more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 12px;
  font-size: var(--gy-text-sm);
  color: var(--gy-text-secondary);
}

.audit-end-hint {
  text-align: center;
  font-size: var(--gy-text-sm);
  color: var(--gy-text-disabled);
  padding: 8px 0 14px;
  margin: 0;
}

.audit-table-shell {
  &:deep(.t-table td),
  &:deep(.t-table th) {
    vertical-align: middle;
    padding-top: 14px;
    padding-bottom: 14px;
  }

  &:deep(thead th) {
    position: sticky;
    top: 0;
    z-index: 2;
    background-color: var(--gy-surface-subtle);
    box-shadow: inset 0 -1px 0 var(--gy-border-subtle);
  }

  &:deep(.t-table tbody tr:hover > td) {
    background-color: var(--gy-surface-hover);
  }

  &:deep(.t-table tbody tr.t-table__expanded-row > td) {
    padding: 0;
    background-color: transparent;
  }

  &:deep(.t-table__expandable-icon-cell) {
    width: 36px;
  }
}

.audit-time {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;

  .audit-time-date {
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
  }

  .audit-time-clock {
    font-size: var(--gy-text-md);
    font-weight: 500;
    color: var(--gy-text-primary);
    font-variant-numeric: tabular-nums;
  }
}

.audit-actor {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;
  min-width: 0;

  .audit-actor-name {
    font-size: var(--gy-text-md);
    font-weight: 500;
    color: var(--gy-text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .audit-actor-role {
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
  }
}

.audit-target {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.35;
  min-width: 0;
  padding: 2px 0;

  .audit-target-key {
    font-size: var(--gy-text-md);
    color: var(--gy-text-primary);
    word-break: break-all;
  }

  .audit-target-diff {
    font-size: var(--gy-text-sm);
    color: var(--gy-text-secondary);
    font-family: var(--gy-font-mono);
    word-break: break-all;
    line-height: 1.4;
  }

  .audit-target-empty {
    color: var(--gy-text-placeholder);
  }
}

.audit-path {
  font-family: var(--gy-font-mono);
  font-size: var(--gy-text-sm);
  color: var(--gy-text-secondary);
  word-break: break-all;

  .audit-method {
    display: inline-block;
    font-weight: 600;
    color: var(--gy-text-primary);
    margin-right: 4px;
  }
}

.audit-expanded {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px 16px;
  background: var(--gy-surface-hover);
}

.audit-expanded-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 10px 18px;
}

.audit-expanded-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.audit-expanded-label {
  font-size: var(--gy-text-xs);
  font-weight: 600;
  color: var(--gy-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.audit-expanded-value {
  font-size: var(--gy-text-sm);
  color: var(--gy-text-primary);
  word-break: break-all;
}

.audit-expanded-details {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.audit-expanded-json {
  margin: 0;
  padding: 10px 12px;
  font-size: var(--gy-text-sm);
  line-height: 1.55;
  color: var(--gy-text-primary);
  background: var(--gy-surface);
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-sm);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 280px;
  overflow: auto;
}

.mono {
  font-family: var(--gy-font-mono);
}
</style>
