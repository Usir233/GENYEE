import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const backendStep = readFileSync(
  new URL('./sandbox-config/SandboxBackendStep.vue', import.meta.url),
  'utf8',
)
const templateStep = readFileSync(
  new URL('./sandbox-config/SandboxTemplateSelectorStep.vue', import.meta.url),
  'utf8',
)
const templatesPoll = readFileSync(
  new URL('./sandbox-config/useSandboxTemplatesPoll.ts', import.meta.url),
  'utf8',
)

test('connection step has no desktop-image switch', () => {
  const connection = backendStep.slice(
    backendStep.indexOf('v-if="isRemoteBackend"'),
    backendStep.indexOf('v-if="!isRemoteBackend"'),
  )
  assert.doesNotMatch(
    connection,
    /desktopEnabled/,
    'desktop_enabled is derived from the template the admin picks, not a connection-step switch',
  )
})

test('desktop templates have a sibling create offer like CLI, and listing does not auto-ensure them', () => {
  assert.ok(templateStep.includes('canCreateStandard'), 'CLI create offer remains a fallback if ensure failed')
  assert.ok(templateStep.includes('canCreateDesktop'), 'desktop create offer is opt-in; XFCE images are much heavier')
  assert.ok(templateStep.includes('createDesktopTemplate'), 'admin must click create to start the desktop Hub build')
  assert.ok(templateStep.includes('genyeeDesktopTemplate'), 'the offer row names the desktop image')
  assert.ok(
    templateStep.includes('item.desktop'),
    'listed cards still mark the desktop image so picking one can set desktop_enabled',
  )
})

test('listing Cube/E2B templates does not auto-ensure the desktop image', () => {
  assert.match(
    templatesPoll,
    /ensure_desktop:\s*Boolean\(opts\.ensureDesktop\)/,
    'catalog load must not send ensure_desktop unless the admin clicked create',
  )
  assert.match(
    templatesPoll,
    /opts\.ensureDesktop && desktopID/,
    'clicking create must also select the desktop template so desktop_enabled is saved as true',
  )
  assert.doesNotMatch(
    templatesPoll,
    /ensure_desktop:\s*opts\.ensureDesktop \|\| \(ensureFirstParty && !opts\.replaceDesktop\)/,
    'opening settings must not provision a 4CPU/4GB desktop template as a side effect',
  )
})

test('saving records desktop_enabled from the selected catalog card', () => {
  assert.match(
    templatesPoll,
    /function collectedDesktopEnabled/,
    'derivation must be a named helper so snapshot vs catalog-miss cannot drift',
  )
  assert.match(
    templatesPoll,
    /Boolean\(selectedTemplate\.value\.desktop\)/,
    'selecting the CLI card must persist desktop_enabled=false, not omit the field',
  )
  assert.match(
    templatesPoll,
    /templatesLoaded\.value && !retargetFrozen\.value/,
    'an unmatched catalog ID must not keep a stale desktop_enabled from a previous save',
  )
  assert.match(
    templatesPoll,
    /effectiveRecord\.value\?\.config\?\.desktop_enabled/,
    'a skill-snapshot UUID is not a catalog card; keep the stored desktop_enabled bit',
  )
})
