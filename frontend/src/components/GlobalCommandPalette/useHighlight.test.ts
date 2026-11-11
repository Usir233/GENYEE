import assert from 'node:assert/strict'
import test from 'node:test'

import {
  highlightRegex,
  highlightText,
  isUnsafeHighlightRegex,
} from './useHighlight.ts'

test('isUnsafeHighlightRegex flags catastrophic backtracking patterns and accepts benign regexes', () => {
  assert.equal(isUnsafeHighlightRegex('(a+)+$'), true)
  assert.equal(isUnsafeHighlightRegex('([a-zA-Z]+)*$'), true)
  assert.equal(isUnsafeHighlightRegex('(a|aa)+$'), true)
  assert.equal(isUnsafeHighlightRegex('(.*a){1,10}'), true)
  assert.equal(isUnsafeHighlightRegex('(foo)\\1'), true)
  assert.equal(isUnsafeHighlightRegex('a'.repeat(129)), true)

  assert.equal(isUnsafeHighlightRegex('foo\\d+'), false)
  assert.equal(isUnsafeHighlightRegex('error|warning'), false)
  assert.equal(isUnsafeHighlightRegex('[A-Z]{2,5}-\\d+'), false)
})

test('highlightRegex degrades unsafe ReDoS patterns to literal search in bounded time', () => {
  const input = `${'a'.repeat(60)}!`
  const start = performance.now()
  const rendered = highlightRegex(input, '(a+)+$')
  const elapsedMs = performance.now() - start

  assert.ok(elapsedMs < 50, `Expected < 50ms, took ${elapsedMs.toFixed(2)}ms`)
  assert.equal(rendered, input)
})

test('highlightRegex highlights safe patterns, escapes HTML, and handles zero-length matches', () => {
  assert.equal(
    highlightRegex('Order ABC-123 <img src=x>', '[A-Z]{3}-\\d+'),
    'Order <mark class="search-highlight">ABC-123</mark> &lt;img src=x&gt;',
  )
  assert.equal(highlightRegex('abc', '^'), 'abc')
  assert.equal(
    highlightText('<b>alpha</b> beta', 'alpha beta'),
    '&lt;b&gt;<mark class="search-highlight">alpha</mark>&lt;/b&gt; <mark class="search-highlight">beta</mark>',
  )
})
