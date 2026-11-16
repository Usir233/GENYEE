import assert from 'node:assert/strict'
import test from 'node:test'
import { getChromeExtensionUrl, getClawhubSkillUrl, isIntegrationTabEnabled } from './integrations'

test('W6-CFG-01: getChromeExtensionUrl and getClawhubSkillUrl allow only http(s) URLs', () => {
  const prevWindow = (globalThis as any).window
  try {
    for (const unsafe of [
      'javascript:alert(1)',
      'data:text/html,<script>alert(1)</script>',
      'vbscript:msgbox(1)',
      '//evil.example.com/ext',
      '/relative/path',
    ]) {
      ;(globalThis as any).window = {
        __RUNTIME_CONFIG__: {
          GENYEE_CHROME_EXTENSION_URL: unsafe,
          GENYEE_CLAWHUB_SKILL_URL: unsafe,
        },
      }
      assert.equal(getChromeExtensionUrl(), '', `Expected unsafe Chrome URL ${unsafe} to be rejected`)
      assert.equal(getClawhubSkillUrl(), '', `Expected unsafe Clawhub URL ${unsafe} to be rejected`)
      assert.equal(isIntegrationTabEnabled('chrome'), false)
      assert.equal(isIntegrationTabEnabled('claw'), false)
    }

    ;(globalThis as any).window = {
      __RUNTIME_CONFIG__: {
        GENYEE_CHROME_EXTENSION_URL: 'https://chromewebstore.google.com/detail/genyee/abc',
        GENYEE_CLAWHUB_SKILL_URL: 'http://localhost:8080/skill',
      },
    }
    assert.equal(getChromeExtensionUrl(), 'https://chromewebstore.google.com/detail/genyee/abc')
    assert.equal(getClawhubSkillUrl(), 'http://localhost:8080/skill')
    assert.equal(isIntegrationTabEnabled('chrome'), true)
    assert.equal(isIntegrationTabEnabled('claw'), true)
  } finally {
    if (prevWindow === undefined) delete (globalThis as any).window
    else (globalThis as any).window = prevWindow
  }
})
