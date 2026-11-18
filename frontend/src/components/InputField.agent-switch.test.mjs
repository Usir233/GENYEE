import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'
import { chatSubmitShortcut } from '../utils/chatSubmitShortcut.ts'

const inputField = readFileSync(new URL('./InputField.vue', import.meta.url), 'utf8')
const agentScope = readFileSync(new URL('./chat-input/useChatInputAgentScope.ts', import.meta.url), 'utf8')
const controlBar = readFileSync(new URL('./chat-input/InputControlBar.vue', import.meta.url), 'utf8')
const steerQueue = readFileSync(new URL('./chat-input/SteerQueueBar.vue', import.meta.url), 'utf8')
const settingsStore = readFileSync(new URL('../stores/settings.ts', import.meta.url), 'utf8')

test('composer shortcuts queue Enter, inject drafts, and promote the first queued message when empty', () => {
  const code = inputField.slice(
    inputField.indexOf('const steerShortcutLabel ='),
    inputField.indexOf('// End onKeydown'),
  )
  const sends = [], promotes = []
  const query = ref('draft')
  const composing = ref(false)
  const props = reactive({
    isReplying: true,
    canSteer: true,
    queuedSteers: [
      { steer_id: 'pending', delivery: 'after', pending: true },
      { steer_id: 'first', delivery: 'after' },
      { steer_id: 'second', delivery: 'after' },
    ],
  })
  const { onKeydown } = vm.runInNewContext(ts.transpile(`${code}\n({ onKeydown })`), {
    Boolean,
    navigator: { platform: 'MacIntel' },
    computed,
    props,
    query,
    chatSubmitShortcut,
    isComposing: composing,
    showMention: ref(false),
    getTextareaEl: () => null,
    allSelectedItems: ref([]),
    removeSelectedItem: () => {},
    mentionSelectorRef: ref(null),
    createSession: (...args) => sends.push(args),
    emit: (...args) => promotes.push(args),
  })
  const enter = { key: 'Enter', keyCode: 13, preventDefault() {}, shiftKey: false, ctrlKey: false, altKey: false }
  onKeydown(query.value, { e: enter })
  onKeydown(query.value, { e: { ...enter, altKey: true } })
  onKeydown(query.value, { e: { ...enter, metaKey: true } })
  assert.deepEqual(sends, [['draft', 'after'], ['draft', 'inject'], ['draft', 'inject']])
  query.value = ''
  onKeydown('', { e: { ...enter, metaKey: true } })
  assert.deepEqual(promotes, [['promote-steer', 'first']])
  composing.value = true
  onKeydown('', { e: { ...enter, altKey: true } })
  assert.equal(promotes.length, 1, 'IME confirmation must not promote a message')
})

test('selecting an agent leaves web search off until the user enables it', () => {
  const selectAgentStart = settingsStore.indexOf('selectAgent(agentId: string')
  const getSelectedAgentStart = settingsStore.indexOf('getSelectedAgentId()', selectAgentStart)
  const selectAgentAction = settingsStore.slice(selectAgentStart, getSelectedAgentStart)

  assert.notEqual(selectAgentStart, -1)
  assert.notEqual(getSelectedAgentStart, -1)
  assert.match(selectAgentAction, /this\.settings\.webSearchEnabled = false/)

  const handleSelectAgentStart = agentScope.indexOf('const handleSelectAgent = async')
  const handleSelectAgentEnd = agentScope.indexOf('const handleGoToAgentSettings =', handleSelectAgentStart)
  const handleSelectAgent = agentScope.slice(handleSelectAgentStart, handleSelectAgentEnd)

  assert.notEqual(handleSelectAgentStart, -1)
  assert.notEqual(handleSelectAgentEnd, -1)
  assert.match(handleSelectAgent, /settingsStore\.selectAgent\(agent\.id, sourceTenantId\)/)
  assert.doesNotMatch(handleSelectAgent, /agentWebSearch/)
  assert.doesNotMatch(handleSelectAgent, /settingsStore\.toggleWebSearch/)
})

test('shared-agent web search button waits for source readiness metadata', () => {
  const showWebSearchStart = agentScope.indexOf('const showWebSearchButton = computed')
  const showWebSearchEnd = agentScope.indexOf('const showImageUploadButton', showWebSearchStart)
  const showWebSearchButton = agentScope.slice(showWebSearchStart, showWebSearchEnd)

  assert.notEqual(showWebSearchStart, -1)
  assert.notEqual(showWebSearchEnd, -1)
  assert.match(showWebSearchButton, /isWebSearchReadinessKnown/)
  assert.match(showWebSearchButton, /selectedSharedAgent\.value\?\.web_search_ready/)
})

test('one composer action switches between stop and send', () => {
  const controlsStart = controlBar.indexOf('class="control-right"')
  const controls = controlBar.slice(controlsStart, controlsStart + 2200)
  assert.notEqual(controlsStart, -1)
  assert.doesNotMatch(`${inputField}\n${controlBar}`, /steer-delivery-toggle/)
  assert.doesNotMatch(`${inputField}\n${controlBar}`, /setSteerDelivery/)
  assert.match(inputField, /emit\('steer-msg', val\.trim\(\), steerMentions, delivery\)/)
  assert.match(controls, /v-if="isReplying && \(!canSteer \|\| !query\.trim\(\)\)"/)
  assert.match(inputField, /@stop="handleStop"/)
  assert.match(inputField, /@send="createSession\(query\)"/)
  assert.match(controls, /v-else[\s\S]*send-btn[\s\S]*emit\('send'\)/)
  assert.match(controls, /<t-icon name="arrow-up"/)
  assert.doesNotMatch(controls, /steer-inject-btn|<t-icon[^>]*'time'/)
})

test('mid-run send is limited to agent sessions', () => {
  const chatView = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')
  assert.match(chatView, /:canSteer="isAgentStreamSession\(\)"/)

  const fnStart = inputField.indexOf('const createSession = async')
  const fnEnd = inputField.indexOf('// End createSession', fnStart)
  const fn = inputField.slice(fnStart, fnEnd)
  assert.notEqual(fnStart, -1)
  assert.ok(fnEnd > fnStart)
  assert.match(fn, /if \(!props\.canSteer\)/)
  assert.ok(fn.includes("emit('steer-msg'"))
  assert.ok(
    fn.indexOf('props.canSteer') < fn.indexOf("emit('steer-msg'"),
    'canSteer must be checked before emitting steer-msg',
  )
})

test('queued follow-ups sit in one block with send-now and remove', () => {
  assert.match(steerQueue, /class="steer-queue"/)
  assert.match(steerQueue, /queuedSteers/)
  assert.match(steerQueue, /emit\('promote-steer', item\.steer_id\)/)
  assert.match(steerQueue, /emit\('remove-steer', item\.steer_id\)/)
  assert.match(steerQueue, /class="steer-queue-action steer-queue-remove"/)
  assert.match(steerQueue, /\$t\('input\.steerQueueSendNow'\)/)
  assert.match(steerQueue, /:disabled="item\.promoting \|\| item\.pending"/)
  assert.match(steerQueue, /\.steer-queue-item \+ \.steer-queue-item/)
})

test('mid-run send refuses attachments that have already uploaded', () => {
  const fnStart = inputField.indexOf('const createSession = async')
  const fn = inputField.slice(fnStart, fnStart + 2200)
  assert.notEqual(fnStart, -1)
  assert.match(fn, /steerHasAttachments/)
  assert.match(fn, /uploadedImages\.value\.length/)
})

test('stop waits for the API before confirming discard', () => {
  const fnStart = inputField.indexOf('const handleStop = async')
  const fnEnd = inputField.indexOf('onBeforeRouteUpdate', fnStart)
  assert.notEqual(fnStart, -1)
  assert.notEqual(fnEnd, -1)
  const fn = inputField.slice(fnStart, fnEnd)
  assert.match(fn, /emit\('stop-generation'\)/)
  assert.match(fn, /emit\('stop-confirmed'\)/)
  assert.match(fn, /emit\('stop-failed'\)/)
  assert.ok(
    fn.indexOf("emit('stop-generation')") < fn.indexOf('await stopSession'),
    'UI stop must fire before the stop API',
  )
  assert.ok(
    fn.indexOf('await stopSession') < fn.indexOf("emit('stop-confirmed')"),
    'queue discard must wait for stop API success',
  )
})
