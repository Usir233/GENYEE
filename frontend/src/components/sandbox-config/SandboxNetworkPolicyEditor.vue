<template>
  <section class="setting-drawer__section">
    <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionNetwork') }}</h4>
    <p class="section-help section-help--under-title">
      {{ $t('settings.sandbox.networkHint') }}
    </p>

    <template v-if="backend !== 'docker'">
      <t-form-item
        :label="$t('settings.sandbox.egressDefault')"
        :tips="$t('settings.sandbox.egressPrecedence')"
      >
        <t-radio-group
          :model-value="denyEgressByDefault"
          @update:model-value="(v: unknown) => emit('update:denyEgressByDefault', Boolean(v))"
        >
          <t-radio :value="false">{{ $t('settings.sandbox.egressAllowAll') }}</t-radio>
          <t-radio :value="true">{{ $t('settings.sandbox.egressDenyAll') }}</t-radio>
        </t-radio-group>
      </t-form-item>
    </template>

    <template v-if="backend === 'docker'">
      <t-form-item
        :label="$t('settings.sandbox.dockerNetworkMode')"
        :tips="$t('settings.sandbox.dockerNetworkModeHelp')"
      >
        <t-select
          :model-value="docker.network_mode"
          :placeholder="$t('settings.sandbox.dockerNetworkBridge')"
          clearable
          @update:model-value="(v: unknown) => setDockerNetworkMode(v ? String(v) : undefined)"
        >
          <t-option value="bridge" :label="$t('settings.sandbox.dockerNetworkBridge')" />
          <t-option value="none" :label="$t('settings.sandbox.dockerNetworkNone')" />
        </t-select>
      </t-form-item>
    </template>

    <template v-else>
      <div class="net-list">
        <div class="section-title-row">
          <span class="net-list__title">{{ $t('settings.sandbox.allowOut') }}</span>
          <t-button variant="text" size="small" @click="addAllowOutRow()">
            <template #icon><t-icon name="add" /></template>
            {{ $t('settings.sandbox.addTarget') }}
          </t-button>
        </div>
        <div v-for="(rowVal, index) in allowOutRows" :key="`allow-${index}`" class="net-row">
          <t-input
            :model-value="rowVal"
            :placeholder="$t('settings.sandbox.allowOutPlaceholder')"
            @update:model-value="(v: string | number) => setAllowOutRow(index, String(v))"
          />
          <t-button
            variant="text"
            shape="square"
            size="small"
            :aria-label="$t('common.delete')"
            @click="removeAllowOutRow(index)"
          >
            <t-icon name="close" />
          </t-button>
        </div>
        <p class="section-help">{{ $t('settings.sandbox.allowOutHelp') }}</p>
        <t-alert
          v-if="domainAllowNeedsDenyAll"
          theme="warning"
          class="compact-alert"
          :message="$t('settings.sandbox.domainAllowNeedsDenyAll')"
        />
      </div>

      <div class="net-list">
        <div class="section-title-row">
          <span class="net-list__title">{{ $t('settings.sandbox.denyOut') }}</span>
          <t-button variant="text" size="small" @click="addDenyOutRow()">
            <template #icon><t-icon name="add" /></template>
            {{ $t('settings.sandbox.addTarget') }}
          </t-button>
        </div>
        <div v-for="(rowVal, index) in denyOutRows" :key="`deny-${index}`" class="net-row">
          <t-input
            :model-value="rowVal"
            :placeholder="$t('settings.sandbox.denyOutPlaceholder')"
            @update:model-value="(v: string | number) => setDenyOutRow(index, String(v))"
          />
          <t-button
            variant="text"
            shape="square"
            size="small"
            :aria-label="$t('common.delete')"
            @click="removeDenyOutRow(index)"
          >
            <t-icon name="close" />
          </t-button>
        </div>
        <p class="section-help">{{ $t('settings.sandbox.denyOutHelp') }}</p>
      </div>
    </template>

    <div v-if="backend === 'cube'" class="net-list">
      <div class="section-title-row">
        <span class="net-list__title">{{ $t('settings.sandbox.cubeL7Rules') }}</span>
        <t-button variant="text" size="small" @click="addCubeRule()">
          <template #icon><t-icon name="add" /></template>
          {{ $t('settings.sandbox.addRule') }}
        </t-button>
      </div>
      <p class="section-help">{{ $t('settings.sandbox.cubeL7RulesHelp') }}</p>
      <div
        v-for="(rule, index) in cubeRules"
        :key="rule.key"
        class="net-rule net-rule--collapsible"
        :class="{ 'is-open': rule.expanded }"
      >
        <div class="net-rule__bar">
          <button
            type="button"
            class="net-rule__toggle"
            :aria-expanded="rule.expanded"
            :aria-label="rule.expanded
              ? $t('settings.sandbox.collapseRule')
              : $t('settings.sandbox.expandRule')"
            @click="rule.expanded = !rule.expanded"
          >
            <t-icon :name="rule.expanded ? 'chevron-down' : 'chevron-right'" size="14px" />
            <span class="net-rule__name" :class="{ 'is-empty': !rule.name.trim() }">
              {{ rule.name.trim() || $t('settings.sandbox.ruleUntitled') }}
            </span>
          </button>
          <div class="net-rule__actions">
            <button
              type="button"
              class="net-rule__move"
              :disabled="index === 0"
              :aria-label="$t('settings.sandbox.moveRuleUp')"
              @click="moveCubeRule(index, -1)"
            >
              <t-icon name="chevron-up" size="14px" />
            </button>
            <button
              type="button"
              class="net-rule__move"
              :disabled="index === cubeRules.length - 1"
              :aria-label="$t('settings.sandbox.moveRuleDown')"
              @click="moveCubeRule(index, 1)"
            >
              <t-icon name="chevron-down" size="14px" />
            </button>
            <button
              type="button"
              class="net-rule__remove"
              :aria-label="$t('common.delete')"
              @click="removeCubeRule(index)"
            >
              <t-icon name="close" size="14px" />
            </button>
          </div>
        </div>
        <div v-if="rule.expanded" class="net-rule__body">
          <div class="form-grid form-grid--two">
            <t-form-item :label="$t('settings.sandbox.ruleName')">
              <t-input v-model="rule.name" placeholder="allow-payment-api" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.ruleScheme')">
              <t-select v-model="rule.scheme" clearable>
                <t-option value="https" label="https" />
                <t-option value="http" label="http" />
              </t-select>
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.ruleSni')">
              <t-input v-model="rule.sni" placeholder="api.example.com" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.ruleHost')">
              <t-input v-model="rule.host" placeholder="api.example.com" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.ruleMethods')">
              <t-input v-model="rule.methodsText" placeholder="POST, GET" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.rulePath')">
              <t-input v-model="rule.path" placeholder="/v1/*" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.ruleAction')">
              <t-select v-model="rule.deny">
                <t-option :value="false" :label="$t('settings.sandbox.ruleAllow')" />
                <t-option :value="true" :label="$t('settings.sandbox.ruleDeny')" />
              </t-select>
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.ruleAudit')">
              <t-select v-model="rule.audit" clearable>
                <t-option value="metadata" label="metadata" />
                <t-option value="full" label="full" />
                <t-option value="none" label="none" />
              </t-select>
            </t-form-item>
          </div>
          <div v-if="!rule.deny" class="net-inject">
            <span class="net-list__title">{{ $t('settings.sandbox.ruleInject') }}</span>
            <div
              v-for="(inject, injectIndex) in rule.inject"
              :key="`inject-${injectIndex}`"
              class="net-row net-row--triple"
            >
              <t-input v-model="inject.header" :placeholder="$t('settings.sandbox.headerName')" />
              <t-input v-model="inject.secret" type="password"
                :placeholder="isStoredNetworkSecretRecoverable(
                  inject,
                  inject.originalRuleName,
                  inject.originalHeader,
                  rule.name,
                  inject.header,
                )
                  ? $t('settings.sandbox.secretKeepHint')
                  : $t('settings.sandbox.headerValue')" />
              <t-input v-model="inject.format" placeholder="Bearer ${SECRET}" />
              <t-button
                variant="text"
                shape="square"
                size="small"
                :aria-label="$t('common.delete')"
                @click="rule.inject.splice(injectIndex, 1)"
              >
                <t-icon name="close" />
              </t-button>
            </div>
            <t-button
              variant="text"
              size="small"
              @click="rule.inject.push({ header: '', secret: '', format: '' })"
            >
              <template #icon><t-icon name="add" /></template>
              {{ $t('settings.sandbox.addHeader') }}
            </t-button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="backend === 'e2b'" class="net-list">
      <div class="section-title-row">
        <span class="net-list__title">{{ $t('settings.sandbox.e2bHostRules') }}</span>
        <t-button variant="text" size="small" @click="addE2BHostRule()">
          <template #icon><t-icon name="add" /></template>
          {{ $t('settings.sandbox.addRule') }}
        </t-button>
      </div>
      <p class="section-help">{{ $t('settings.sandbox.e2bHostRulesHelp') }}</p>
      <div
        v-for="(rule, index) in e2bHostRules"
        :key="`e2b-rule-${index}`"
        class="net-rule net-rule--collapsible"
        :class="{ 'is-open': rule.expanded }"
      >
        <div class="net-rule__bar">
          <button
            type="button"
            class="net-rule__toggle"
            :aria-expanded="rule.expanded"
            :aria-label="rule.expanded
              ? $t('settings.sandbox.collapseRule')
              : $t('settings.sandbox.expandRule')"
            @click="rule.expanded = !rule.expanded"
          >
            <t-icon :name="rule.expanded ? 'chevron-down' : 'chevron-right'" size="14px" />
            <span class="net-rule__name" :class="{ 'is-empty': !rule.host.trim() }">
              {{ rule.host.trim() || $t('settings.sandbox.ruleUntitled') }}
            </span>
          </button>
          <button
            type="button"
            class="net-rule__remove"
            :aria-label="$t('common.delete')"
            @click="removeE2BHostRule(index)"
          >
            <t-icon name="close" size="14px" />
          </button>
        </div>
        <div v-if="rule.expanded" class="net-rule__body">
          <t-form-item :label="$t('settings.sandbox.ruleHost')">
            <t-input v-model="rule.host" placeholder="api.example.com" />
          </t-form-item>
          <div
            v-for="(header, headerIndex) in rule.headers"
            :key="`header-${headerIndex}`"
            class="net-row net-row--double"
          >
            <t-input v-model="header.name" :placeholder="$t('settings.sandbox.headerName')" />
            <t-input v-model="header.value" type="password"
              :placeholder="isStoredNetworkSecretRecoverable(
                header,
                header.originalHost,
                header.originalName,
                rule.host,
                header.name,
              )
                ? $t('settings.sandbox.secretKeepHint')
                : $t('settings.sandbox.headerValue')" />
            <t-button
              variant="text"
              shape="square"
              size="small"
              :aria-label="$t('common.delete')"
              @click="rule.headers.splice(headerIndex, 1)"
            >
              <t-icon name="close" />
            </t-button>
          </div>
          <t-button
            variant="text"
            size="small"
            @click="rule.headers.push({ name: '', value: '' })"
          >
            <template #icon><t-icon name="add" /></template>
            {{ $t('settings.sandbox.addHeader') }}
          </t-button>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { SandboxDockerConfig } from '@/api/system'
import {
  isStoredNetworkSecretRecoverable,
  type CubeRuleForm,
  type E2BRuleForm,
} from './useSandboxNetworkRules'

defineProps<{
  backend: string
  docker: SandboxDockerConfig
  setDockerNetworkMode: (mode?: string) => void
  denyEgressByDefault: boolean
  allowOutRows: string[]
  denyOutRows: string[]
  cubeRules: CubeRuleForm[]
  e2bHostRules: E2BRuleForm[]
  domainAllowNeedsDenyAll: boolean
  addAllowOutRow: () => void
  setAllowOutRow: (index: number, value: string) => void
  removeAllowOutRow: (index: number) => void
  addDenyOutRow: () => void
  setDenyOutRow: (index: number, value: string) => void
  removeDenyOutRow: (index: number) => void
  addCubeRule: () => void
  moveCubeRule: (index: number, delta: number) => void
  removeCubeRule: (index: number) => void
  addE2BHostRule: () => void
  removeE2BHostRule: (index: number) => void
}>()

const emit = defineEmits<{
  (e: 'update:denyEgressByDefault', value: boolean): void
}>()
</script>

<style lang="less" scoped src="./sandbox-config-editor-drawer.less"></style>
