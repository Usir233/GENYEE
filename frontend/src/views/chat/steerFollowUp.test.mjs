import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const indexSource = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const steerComposable = readFileSync(new URL('./composables/useChatSteerQueue.ts', import.meta.url), 'utf8')
const source = [indexSource, steerComposable].join('\n')
const steerApi = readFileSync(new URL('../../api/chat/steer.ts', import.meta.url), 'utf8')

function attachSteerFollowUpSource() {
  const start = steerComposable.indexOf('const attachSteerFollowUp = async')
  const end = steerComposable.indexOf('const flushSteerAfterTurn = async', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  return steerComposable.slice(start, end)
}

test('refresh restores the overlay queue from the live run', () => {
  assert.match(steerApi, /export async function listSteerSession/)
  assert.match(steerComposable, /listSteerSession/)
  assert.match(source, /hydrateSteerQueue/)
  assert.match(indexSource, /if \(!steerQueue\.value\.length\)/)
})

test('resume and stop address the persisted assistant id, not a fork id', () => {
  const fork = readFileSync(new URL('../../utils/steerStreamFork.ts', import.meta.url), 'utf8')
  assert.match(fork, /export function persistedAssistantId/)

  const start = indexSource.indexOf('onAfterMsgList: async ()')
  const end = indexSource.indexOf('onAgentQuery:', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const hook = indexSource.slice(start, end)

  assert.match(hook, /persistedAssistantId\(lastMessage\)/)
  assert.match(hook, /currentAssistantMessageId\.value = resumeId/)
  assert.match(hook, /query: resumeId/)
  assert.doesNotMatch(hook, /query: lastMessage\.id/)

  assert.match(hook, /findLastMessage\(\s*\(message\) => message\.role === 'assistant' && !message\.is_completed,?\s*\)/)
  assert.doesNotMatch(hook, /messagesList\[messagesList\.length - 1\]/)
})

test('stop clears the queue only after the stop API succeeds', () => {
  const genStart = indexSource.indexOf('const handleStopGeneration = ')
  const genEnd = indexSource.indexOf('async function sendMsg(', genStart)
  assert.notEqual(genStart, -1)
  assert.notEqual(genEnd, -1)
  const gen = indexSource.slice(genStart, genEnd)
  assert.doesNotMatch(gen, /steerQueue\.value = \[\]/)
  assert.doesNotMatch(gen, /attachSteerFollowUp/)

  const confirmedStart = steerComposable.indexOf('const handleStopConfirmed = ')
  const failedStart = steerComposable.indexOf('const handleStopFailed = ')
  const end = steerComposable.indexOf('const dropSteerQueueItem', confirmedStart)
  assert.notEqual(confirmedStart, -1)
  assert.notEqual(failedStart, -1)
  assert.notEqual(end, -1)

  const confirmed = steerComposable.slice(confirmedStart, failedStart)
  assert.match(confirmed, /steerQueue\.value = \[\]/)

  const failed = steerComposable.slice(failedStart, end)
  assert.match(failed, /isReplying\.value = true/)
})

test('follow-up attaches persisted users by request_id', () => {
  const fn = attachSteerFollowUpSource()
  assert.match(fn, /persisted\.request_id !== newAssistant\.request_id/)
  assert.match(fn, /hydrateSteerQueue\(\{ onlyWhenLive: true \}\)/)
  assert.doesNotMatch(fn, /m\.content === q\.content/)
  assert.match(fn, /batch\.findLast\(/)
})

test('steer lookup failure does not start a second turn', () => {
  const start = steerComposable.indexOf('const handleSteerMsg = async')
  const end = steerComposable.indexOf('const handleRetrySteer = async', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const fn = steerComposable.slice(start, end)
  const newRun = fn.indexOf("if (res?.status === 'new_run')")
  const catchIdx = fn.indexOf('} catch (e: unknown) {')
  assert.notEqual(newRun, -1)
  assert.notEqual(catchIdx, -1)
  assert.ok(newRun < catchIdx, 'new_run fallback must sit in the try, not the catch')
  const newRunBlock = fn.slice(newRun, catchIdx)
  assert.match(newRunBlock, /awaitingIdleSend/)
  assert.match(newRunBlock, /isReplying/)
  assert.match(newRunBlock, /await sendMsg\(/)
  assert.doesNotMatch(fn.slice(catchIdx), /sendMsg\(/)
})

test('new_run while a stream is live does not abort it', () => {
  const start = steerComposable.indexOf('const handleSteerMsg = async')
  const end = steerComposable.indexOf('const handleRetrySteer = async', start)
  const fn = steerComposable.slice(start, end)
  const newRun = fn.indexOf("if (res?.status === 'new_run')")
  const catchIdx = fn.indexOf('} catch (e: unknown) {')
  const block = fn.slice(newRun, catchIdx)
  assert.match(block, /isStreaming/)
  assert.doesNotMatch(block, /stopStream\(/)
})

test('turn complete flushes idle new_run sends before attaching a follow-up', () => {
  assert.match(steerComposable, /const flushSteerAfterTurn = async/)
  assert.match(steerComposable, /awaitingIdleSend/)
  const start = indexSource.indexOf('onTurnComplete:')
  const end = indexSource.indexOf('});', start)
  const hook = indexSource.slice(start, end)
  assert.match(hook, /flushSteerAfterTurn\(persistedAssistantId\(message\)\)/)
  assert.doesNotMatch(hook, /attachSteerFollowUp\(persistedAssistantId\(message\)\)/)
})

test('remaining overlay after-items chain after a follow-up stream ends', () => {
  const fn = attachSteerFollowUpSource()
  const finallyStart = fn.indexOf('} finally {')
  assert.notEqual(finallyStart, -1, 'attachSteerFollowUp must have a finally block')
  const finallyBlock = fn.slice(finallyStart)
  assert.match(finallyBlock, /steerQueue\.value\.some\(\(item\) => !item\.failed\)/)
  assert.match(finallyBlock, /void attachSteerFollowUp\(/)
})

test('follow-up attach aborts when the session changes', () => {
  const fn = attachSteerFollowUpSource()
  assert.match(fn, /const sessionId = sessionIdRef\.value/)
  assert.match(fn, /sessionIdRef\.value !== sessionId/)
  assert.match(fn, /getMessageList\(\{ session_id: sessionId/)
  assert.match(fn, /session_id: sessionId/)
})

test('follow-up attach keys off the persisted assistant id, not a fork id', () => {
  const start = indexSource.indexOf('onTurnComplete:')
  const end = indexSource.indexOf('});', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const hook = indexSource.slice(start, end)
  assert.match(hook, /flushSteerAfterTurn\(persistedAssistantId\(message\)\)/)
  assert.doesNotMatch(hook, /attachSteerFollowUp\(message\?\.id\)/)
  const flushStart = steerComposable.indexOf('const flushSteerAfterTurn = async')
  const flushEnd = steerComposable.indexOf('return {', flushStart)
  assert.match(steerComposable.slice(flushStart, flushEnd), /attachSteerFollowUp\(completedAssistantId\)/)
})

test('remove drops overlay when the server already injected the item', () => {
  const start = steerComposable.indexOf('const handleRemoveSteer = async')
  const end = steerComposable.indexOf('let attachingSteerFollowUp', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const fn = steerComposable.slice(start, end)
  assert.match(fn, /already_injected/)
  assert.match(fn, /dropSteerQueueItem\(steerId\)/)
  assert.match(fn, /removed === false/)
})

test('remote stop clears the overlay queue', () => {
  assert.match(indexSource, /onGenerationStopped/)
  const start = indexSource.indexOf('onGenerationStopped')
  assert.notEqual(start, -1)
  const snippet = indexSource.slice(start, start + 180)
  assert.match(snippet, /steerQueue\.value = \[\]/)
})
