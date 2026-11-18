import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import {
  denyOutRowCoversAllIPv4,
  isStoredNetworkSecretRecoverable,
  useSandboxNetworkRules,
} from './sandbox-config/useSandboxNetworkRules.ts'

const drawerSource = readFileSync(
  new URL('./SandboxConfigEditorDrawer.vue', import.meta.url),
  'utf8',
)
const networkEditorSource = readFileSync(
  new URL('./sandbox-config/SandboxNetworkPolicyEditor.vue', import.meta.url),
  'utf8',
)
const networkRulesSource = readFileSync(
  new URL('./sandbox-config/useSandboxNetworkRules.ts', import.meta.url),
  'utf8',
)

test('network policy lives in the runtime step, right below the runtime config', () => {
  const runtimeSections = drawerSource.indexOf("currentStepKey === 'runtime'")
  const networkSection = drawerSource.indexOf('<SandboxNetworkPolicyEditor')
  const envSection = drawerSource.indexOf('settings.sandbox.sectionEnvironment')

  assert.ok(runtimeSections !== -1, 'runtime step must exist')
  assert.ok(networkSection !== -1, 'network policy component must exist in runtime step')
  assert.ok(
    networkEditorSource.includes('settings.sandbox.sectionNetwork'),
    'network policy section heading must exist',
  )
  assert.ok(
    networkSection > runtimeSections && networkSection < envSection,
    'network policy must sit between the runtime config and the env vars',
  )
  assert.ok(
    !drawerSource.includes("currentStepKey === 'network'"),
    'network policy must not become a fourth wizard step',
  )
})

test('runtime numbers are laid out two per row with per-field tips', () => {
  const runtimeBlock = drawerSource.slice(
    drawerSource.indexOf('settings.sandbox.sectionRuntime'),
    drawerSource.indexOf('<SandboxNetworkPolicyEditor'),
  )
  assert.ok(
    runtimeBlock.includes('form-grid form-grid--two'),
    'timeouts must be compressed into the two-column grid',
  )
  assert.ok(
    runtimeBlock.includes(":tips=\"$t('settings.sandbox.httpTimeoutHelp')\""),
    'http timeout keeps its own explanation, now inline',
  )
  assert.ok(
    runtimeBlock.includes(":tips=\"$t('settings.sandbox.sandboxTtlHelp')\""),
    'sandbox TTL keeps its own explanation, now inline',
  )
  for (const key of [
    'dockerCpuLimitHelp',
    'dockerMemoryLimitHelp',
    'dockerPidsLimitHelp',
  ]) {
    assert.ok(
      runtimeBlock.includes(`:tips="$t('settings.sandbox.${key}')"`),
      `${key} must remain attached to its compressed field`,
    )
  }
})

test('docker network mode moved into the network policy section', () => {
  assert.ok(
    networkEditorSource.includes('settings.sandbox.dockerNetworkMode'),
    'the docker bridge/none selector belongs with the other network controls',
  )
  const runtimeBlock = drawerSource.slice(
    drawerSource.indexOf('settings.sandbox.sectionRuntime'),
    drawerSource.indexOf('<SandboxNetworkPolicyEditor'),
  )
  assert.ok(
    !runtimeBlock.includes('settings.sandbox.dockerNetworkMode'),
    'and must no longer be duplicated in the runtime config',
  )
})

test('inbound is always credential-required and never shown', () => {
  for (const src of [drawerSource, networkEditorSource, networkRulesSource]) {
    assert.doesNotMatch(
      src,
      /settings\.sandbox\.inboundAccess/,
      'the inbound radio must not appear in the form',
    )
    assert.doesNotMatch(
      src,
      /allowPublicInbound/,
      'the form must not keep a public-inbound control',
    )
  }
  const payloadBlock = networkRulesSource.slice(
    networkRulesSource.indexOf('function collectNetworkPolicy'),
  )
  assert.doesNotMatch(
    payloadBlock,
    /allow_public_inbound/,
    'saves must omit allow_public_inbound so the zero value (require credentials) is stored',
  )
  assert.match(
    networkRulesSource,
    /const denyEgressByDefault = ref\(false\)/,
    'egress allowed is the default',
  )
})

test('injected header values are password inputs and survive an untouched edit', () => {
  const payloadBlock = networkRulesSource.slice(
    networkRulesSource.indexOf('function collectNetworkPolicy'),
  )
  const templateBlock = networkEditorSource.slice(0, networkEditorSource.indexOf('<script setup'))
  assert.match(
    networkEditorSource,
    /<t-input v-model="inject\.secret" type="password"/,
    'Cube injected credentials must not be rendered in clear text',
  )
  assert.match(
    networkEditorSource,
    /<t-input v-model="header\.value" type="password"/,
    'E2B injected credentials must not be rendered in clear text',
  )
  assert.match(
    payloadBlock,
    /isStoredNetworkSecretRecoverable\(\s*inject,\s*inject\.originalRuleName,\s*inject\.originalHeader,\s*rule\.name,\s*inject\.header,\s*\)[\s\S]*?inject\.secret === ''[\s\S]*?\? secretPlaceholder/,
    'Cube payload preservation must use the shared recoverability decision',
  )
  assert.match(
    payloadBlock,
    /isStoredNetworkSecretRecoverable\(\s*header,\s*header\.originalHost,\s*header\.originalName,\s*rule\.host,\s*header\.name,\s*\)[\s\S]*?header\.value === ''[\s\S]*?\? secretPlaceholder/,
    'E2B payload preservation must use the shared recoverability decision',
  )
  assert.match(
    templateBlock,
    /isStoredNetworkSecretRecoverable\(\s*inject,\s*inject\.originalRuleName,\s*inject\.originalHeader,\s*rule\.name,\s*inject\.header,\s*\)[\s\S]*?\$t\('settings\.sandbox\.secretKeepHint'\)/,
    'Cube configured placeholder must use the shared recoverability decision',
  )
  assert.match(
    templateBlock,
    /isStoredNetworkSecretRecoverable\(\s*header,\s*header\.originalHost,\s*header\.originalName,\s*rule\.host,\s*header\.name,\s*\)[\s\S]*?\$t\('settings\.sandbox\.secretKeepHint'\)/,
    'E2B configured placeholder must use the shared recoverability decision',
  )
})

test('stored network secret recoverability requires the loaded identity', () => {
  const storedRow = { stored: true }
  assert.equal(
    isStoredNetworkSecretRecoverable(storedRow, 'rule-a', 'Authorization', 'rule-a', 'Authorization'),
    true,
    'a stored row under its loaded identity keeps the placeholder',
  )
  assert.equal(
    isStoredNetworkSecretRecoverable(storedRow, 'rule-a', 'Authorization', 'rule-b', 'Authorization'),
    false,
    'a renamed row must request a new value',
  )
})

test('domain allow without deny-all is warned about in place', () => {
  const classifierBlock = networkRulesSource.slice(
    networkRulesSource.indexOf('const domainAllowNeedsDenyAll'),
    networkRulesSource.indexOf('function resetNetworkAndEnv'),
  )
  assert.match(
    networkEditorSource,
    /<t-alert[\s\S]*?v-if="domainAllowNeedsDenyAll"[\s\S]*?:message="\$t\('settings\.sandbox\.domainAllowNeedsDenyAll'\)"/,
    'the in-place warning alert must remain rendered',
  )
  assert.match(
    classifierBlock,
    /if \(denyEgressByDefault\.value\) return false/,
    'deny-by-default must suppress the warning',
  )
  assert.match(
    classifierBlock,
    /denyOutRows\.value\.some\(\(row\) => denyOutRowCoversAllIPv4\(row\)\)\) return false/,
    'an explicit deny-all row must suppress the warning',
  )
  assert.match(
    classifierBlock,
    /return !\/\^\[0-9\.\/\]\+\$\/\.test\(value\)/,
    'only non-IP/CIDR allow rows must trigger the warning',
  )

  assert.equal(denyOutRowCoversAllIPv4('0.0.0.0/0'), true)
  assert.equal(denyOutRowCoversAllIPv4('  1.2.3.4/0  '), true)
  assert.equal(denyOutRowCoversAllIPv4('10.0.0.0/8'), false)
  assert.equal(denyOutRowCoversAllIPv4('0.0.0.0/32'), false)
})

test('docker payload omits hidden allow and deny lists', () => {
  const payloadBlock = networkRulesSource.slice(
    networkRulesSource.indexOf('function collectNetworkPolicy'),
  )
  assert.match(
    payloadBlock,
    /if \(backend\.value === 'docker'\) \{\s*return policy/,
    'Docker must not persist Cube/E2B radios or allow/deny rows',
  )
  assert.match(
    payloadBlock,
    /if \(backend\.value !== 'docker'\) \{[^}]*policy\.allow_out = allowOut[^}]*policy\.deny_out = denyOut[^}]*\}/,
    'Docker must not send allow/deny rows that its form hides',
  )
})

test('docker hides egress radios that it cannot honour', () => {
  assert.match(
    networkEditorSource,
    /v-if="backend !== 'docker'"[\s\S]*settings\.sandbox\.egressDefault/,
    'egress radios must not render for Docker',
  )
})

test('cube L7 rules collapse to a name bar', () => {
  const templateBlock = networkEditorSource.slice(0, networkEditorSource.indexOf('<script setup'))
  const cubeBlock = templateBlock.slice(templateBlock.indexOf('settings.sandbox.cubeL7Rules'))
  const e2bBlock = cubeBlock.slice(cubeBlock.indexOf('settings.sandbox.e2bHostRules'))
  const cubeRulesBlock = cubeBlock.slice(0, cubeBlock.indexOf('settings.sandbox.e2bHostRules'))

  assert.match(
    cubeRulesBlock,
    /class="net-rule net-rule--collapsible"/,
    'Cube L7 rules use the compact collapsible card, not the always-open padding',
  )
  assert.match(
    cubeRulesBlock,
    /class="net-rule__bar"/,
    'each Cube L7 rule must render a compact name bar',
  )
  assert.match(
    cubeRulesBlock,
    /rule\.expanded \? 'chevron-down' : 'chevron-right'/,
    'the left control must expand and collapse the rule',
  )
  assert.match(
    cubeRulesBlock,
    /v-if="rule\.expanded"/,
    'rule details must stay hidden until the bar is expanded',
  )
  assert.equal(
    cubeRulesBlock.includes('settings.sandbox.removeRule'),
    false,
    'the expanded body must not grow with a second delete control',
  )
  assert.match(
    e2bBlock,
    /class="net-rule net-rule--collapsible"/,
    'E2B host rules use the same compact collapsible card',
  )
  assert.match(
    e2bBlock,
    /class="net-rule__bar"/,
    'each E2B host rule must render a compact name bar',
  )
  assert.match(
    e2bBlock,
    /v-if="rule\.expanded"/,
    'E2B rule details must stay hidden until the bar is expanded',
  )
  assert.equal(
    e2bBlock.includes('settings.sandbox.removeRule'),
    false,
    'E2B expanded body must not grow with a second delete control',
  )
  assert.match(
    cubeRulesBlock,
    /:key="rule\.key"/,
    'Cube rules must key on a stable id so reorder does not remount the open card',
  )
  assert.match(
    cubeRulesBlock,
    /moveCubeRule\(index, -1\)/,
    'the name bar must offer move-up',
  )
  assert.match(
    cubeRulesBlock,
    /moveCubeRule\(index, 1\)/,
    'the name bar must offer move-down',
  )

  const state = useSandboxNetworkRules({ value: 'cube' })
  state.addCubeRule()
  state.cubeRules.value[0].name = 'a'
  state.addCubeRule()
  state.cubeRules.value[1].name = 'b'
  assert.equal(state.cubeRules.value[0].expanded, false, 'adding a Cube rule collapses previous rules')
  assert.equal(state.cubeRules.value[1].expanded, true, 'newly added Cube rule stays open')
  state.addCubeRule()
  state.cubeRules.value[2].name = 'c'
  state.moveCubeRule(2, -1)
  assert.deepEqual(state.cubeRules.value.map((row) => row.name), ['a', 'c', 'b'])
  state.moveCubeRule(0, -1)
  assert.deepEqual(state.cubeRules.value.map((row) => row.name), ['a', 'c', 'b'], 'the first row cannot move up')
  state.moveCubeRule(2, 1)
  assert.deepEqual(state.cubeRules.value.map((row) => row.name), ['a', 'c', 'b'], 'the last row cannot move down')

  state.addE2BHostRule()
  state.addE2BHostRule()
  assert.equal(state.e2bHostRules.value[0].expanded, false)
  assert.equal(state.e2bHostRules.value[1].expanded, true)

  state.resetNetworkAndEnv({
    network: {
      cube_rules: [{ name: 'saved-cube', methods: ['GET'] }],
      e2b_host_rules: [{ host: 'api.example.com', headers: {} }],
    },
  })
  assert.equal(state.cubeRules.value[0].expanded, false, 'Cube rules loaded from saved config start collapsed')
  assert.equal(state.e2bHostRules.value[0].expanded, false, 'E2B rules loaded from saved config start collapsed')
})

test('deny rules omit hidden injected secrets', () => {
  const payloadBlock = networkRulesSource.slice(
    networkRulesSource.indexOf('function collectNetworkPolicy'),
  )
  assert.match(
    payloadBlock,
    /inject: rule\.deny[\s\S]*?\? undefined[\s\S]*?: rule\.inject/,
    'a deny rule must not send inject entries hidden by the form',
  )
})

test('policy-restricted egress does not claim full outbound verification', () => {
  assert.match(
    drawerSource,
    /EGRESS_RESTRICTED_REASON = 'egress_restricted_by_policy'/,
    'the hint must recognize the skip reason the check API returns',
  )
  assert.match(
    drawerSource,
    /settings\.sandbox\.checkScopePolicyRestricted/,
    'deny-by-default must not reuse the "outbound was verified" copy',
  )
  const hintBlock = drawerSource.slice(
    drawerSource.indexOf('const checkScopeHint = computed'),
    drawerSource.indexOf('function checkDetail'),
  )
  const restrictedAt = hintBlock.indexOf('checkScopePolicyRestricted')
  const fullAt = hintBlock.indexOf('checkScopeFull')
  assert.ok(
    restrictedAt !== -1 && fullAt !== -1 && restrictedAt < fullAt,
    'policy-restricted copy must win over the full-verification claim',
  )
})
