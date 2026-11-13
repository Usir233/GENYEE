import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { runInNewContext } from 'node:vm'

test('widget exposes a single GENYEE global with its programmatic API', () => {
  const window: Record<string, any> = {}
  runInNewContext(readFileSync(new URL('../../public/genyee-widget.js', import.meta.url), 'utf8'), {
    window,
    document: { currentScript: null },
  })
  assert.deepEqual(Object.keys(window), ['GENYEE'])
  for (const method of ['init', 'open', 'close', 'destroy', 'setContext', 'openWithQuery']) {
    assert.equal(typeof window.GENYEE[method], 'function', method)
  }
})
