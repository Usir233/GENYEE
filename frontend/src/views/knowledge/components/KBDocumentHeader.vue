<template>
  <div class="document-header">
    <div class="document-header-title">
      <div class="document-title-row">
        <h2 class="document-breadcrumb">
          <button type="button" class="breadcrumb-link" @click="emit('navigate-kb-list')">
            {{ $t('menu.knowledgeBase') }}
          </button>
          <t-icon name="chevron-right" class="breadcrumb-separator" />
          <KBSwitcherDropdown
            v-if="knowledgeList.length"
            :kb-list="knowledgeList"
            :current-kb-id="kbId"
            @select="(id: string) => emit('switch-kb', id)"
          >
            <button type="button" class="breadcrumb-link dropdown" :disabled="!kbId">
              <template v-if="!kbInfo">
                <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '20px' }]" />
              </template>
              <template v-else>
                <span>{{ kbInfo.name }}</span>
                <t-icon name="chevron-down" />
              </template>
            </button>
          </KBSwitcherDropdown>
          <button
            v-else
            type="button"
            class="breadcrumb-link"
            :disabled="!kbId"
            @click="emit('navigate-current-kb')"
          >
            <template v-if="!kbInfo">
              <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '20px' }]" />
            </template>
            <template v-else>
              {{ kbInfo.name }}
            </template>
          </button>
          <t-icon name="chevron-right" class="breadcrumb-separator" />
          <div class="kb-view-tabs" role="tablist" :aria-label="$t('knowledgeEditor.wikiBrowser.viewTabs')">
            <t-tooltip v-for="tab in kbViewTabs" :key="tab.key" :content="tab.tip" placement="bottom">
              <button
                type="button"
                role="tab"
                class="kb-view-tab"
                :class="{ active: shownKbTab === tab.key, indexing: tab.indexing }"
                :aria-selected="shownKbTab === tab.key"
                @click="emit('select-tab', tab.key)"
              >
                <t-loading v-if="tab.indexing" size="small" class="kb-view-tab__indicator" />
                <t-icon v-else :name="tab.icon" size="16px" />
                <span>{{ tab.label }}</span>
              </button>
            </t-tooltip>
          </div>
        </h2>
        <div class="kb-title-actions">
          <KBInfoPopover
            v-if="kbInfo && !isLiteMode"
            :kb-info="kbInfo"
            :supported-file-types="supportedFileTypes"
          />
          <t-tooltip v-if="canManage" :content="$t('knowledgeBase.settings')" placement="top">
            <button
              type="button"
              class="kb-settings-button"
              :aria-label="$t('knowledgeBase.settings')"
              :disabled="!kbId"
              @click="emit('open-settings')"
            >
              <t-icon name="setting" size="16px" />
            </button>
          </t-tooltip>
        </div>
      </div>
      <p v-if="kbInfo?.description" class="document-subtitle">{{ kbInfo.description }}</p>
      <p v-if="canEdit && unsupportedFileTypes.length" class="parser-hint" role="button" tabindex="0"
        @click="emit('open-parser-settings')" @keydown.enter.prevent="emit('open-parser-settings')"
        @keydown.space.prevent="emit('open-parser-settings')">
        <t-icon name="info-circle" class="parser-hint-icon" />
        <span>{{ $t('knowledgeBase.unsupportedTypesHint', { types: unsupportedFileTypes.map((ext) => '.' + ext).join('、') }) }}</span>
        <span class="parser-hint-link">{{ $t('knowledgeBase.goToParserSettings') }} →</span>
      </p>
      <p v-if="missingStorageEngine" class="storage-engine-warning" role="button" tabindex="0"
        @click="emit('open-settings')" @keydown.enter.prevent="emit('open-settings')"
        @keydown.space.prevent="emit('open-settings')">
        <t-icon name="info-circle" class="warning-icon" />
        <span>{{ $t('knowledgeBase.missingStorageEngine') }}</span>
        <span class="warning-link">{{ $t('knowledgeBase.goToStorageSettings') }} →</span>
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import KBInfoPopover from '@/components/KBInfoPopover.vue'
import KBSwitcherDropdown from '@/components/KBSwitcherDropdown.vue'
import type { KBDetailInfo } from '../composables/useKBDocumentStatusPoll'

export type KbTab = 'documents' | 'wiki' | 'graph' | 'gallery'

defineProps<{
  kbId: string
  kbInfo: KBDetailInfo | null
  knowledgeList: Array<{ id: string; name: string; type?: string }>
  kbViewTabs: Array<{ key: KbTab; icon: string; label: string; tip: string; indexing?: boolean }>
  shownKbTab: KbTab
  isLiteMode: boolean
  supportedFileTypes: string[]
  unsupportedFileTypes: string[]
  canEdit: boolean
  canManage: boolean
  missingStorageEngine: boolean
}>()

const emit = defineEmits<{
  'navigate-kb-list': []
  'navigate-current-kb': []
  'switch-kb': [id: string]
  'select-tab': [tab: KbTab]
  'open-settings': []
  'open-parser-settings': []
}>()
</script>

<style scoped lang="less">
@import '../knowledge-base.less';
</style>
