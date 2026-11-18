import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const metricsSource = readFileSync(join(here, 'processing-timeline/useSpanWaterfallMetrics.ts'), 'utf8')
const pollSource = readFileSync(join(here, 'processing-timeline/useProcessingSpansPoll.ts'), 'utf8')
const sfcSource = readFileSync(join(here, 'KnowledgeProcessingTimeline.vue'), 'utf8')

test('counts skipped stages when determining the current stage', () => {
  const currentStageIndex = metricsSource.slice(
    metricsSource.indexOf('const currentStageIndex'),
    metricsSource.indexOf('function formatRelativeTime'),
  )

  assert.match(currentStageIndex, /status === 'done' \|\| s\.status === 'skipped'/)
  assert.match(currentStageIndex, /Math\.min\(traversed \+ 1, stages\.value\.length\)/)
})

test('counts skipped stages in the completed stage total', () => {
  const stagesStatDisplay = metricsSource.slice(
    metricsSource.indexOf('const stagesStatDisplay'),
    metricsSource.indexOf('const postprocessTaskStats'),
  )

  assert.match(stagesStatDisplay, /status === 'done' \|\| s\.status === 'skipped'/)
  assert.match(stagesStatDisplay, /value: `\$\{completedCount\}\/\$\{total\}`/)
})

test('uses canonical snake_case enable_multimodal process override', () => {
  assert.match(pollSource, /enable_multimodal/)
  assert.doesNotMatch(`${sfcSource}\n${pollSource}\n${metricsSource}`, /enable_multimodel/)
})
