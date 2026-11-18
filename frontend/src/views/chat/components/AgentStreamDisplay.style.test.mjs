import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'
import { useDeferredTimelineCompletion } from '../../../composables/useChatScrollFollow.ts'
import { collaborationActivity, collaborationWaitState } from '../../../utils/collaborationDisplay.ts'
import { isAssistantTurnComplete } from '../../../utils/steerStreamFork.ts'

const here = dirname(fileURLToPath(import.meta.url))
const displayVue = readFileSync(join(here, 'AgentStreamDisplay.vue'), 'utf8')
const wikiDrawerVue = readFileSync(join(here, 'stream/AgentWikiPageDrawer.vue'), 'utf8')
const displayLess = readFileSync(join(here, 'agent-stream-display.less'), 'utf8')
const toolDetailsVue = readFileSync(join(here, 'stream/AgentToolCallDetails.vue'), 'utf8')
const timelineSource = readFileSync(join(here, '../composables/useAgentTimelineEvents.ts'), 'utf8')
const presentationSource = readFileSync(join(here, '../composables/useAgentToolPresentation.ts'), 'utf8')
const source = [displayVue, wikiDrawerVue, displayLess, toolDetailsVue, timelineSource, presentationSource].join('\n')

test('streamed responses do not mount a hidden Wiki drawer that steals composer focus', () => {
  assert.match(displayVue, /<AgentWikiPageDrawer[\s\S]*?v-model:visible="wikiDrawerVisible"/)
  assert.match(wikiDrawerVue, /<t-drawer[\s\S]*?v-if="visible"[\s\S]*?:visible="visible"/)
})

test('steer seals animation while keeping the preceding timeline open without completion', () => {
  const stateBlock = timelineSource.slice(
    timelineSource.indexOf('  const isConversationDone ='),
    timelineSource.indexOf('  const wasUserStopped ='),
  )
  const collapseBlock = timelineSource.slice(
    timelineSource.indexOf('  const shouldShowCollapsedSteps ='),
    timelineSource.indexOf('  // Once the steps collapse'),
  )
  const js = ts.transpileModule(
    `${stateBlock}\n${collapseBlock}\n({ isConversationDone, isSegmentDone, shouldShowCollapsedSteps })`,
    { compilerOptions: { target: ts.ScriptTarget.ES2022, alwaysStrict: false } },
  ).outputText
  const props = reactive({ ragMode: false, session: { steerForked: true, is_completed: true } })
  const state = vm.runInNewContext(js, {
    props,
    computed,
    isAssistantTurnComplete,
    intermediateStepsCount: { value: 2 },
    useDeferredTimelineCompletion,
    readingHistory: ref(false),
    eventStream: ref([]),
    collaborationWaitState,
    collaborationActivity,
    isAwaitingInput: (m) => m?.run_state === 'awaiting_input',
  })
  assert.equal(state.isSegmentDone.value, true)
  assert.equal(state.isConversationDone.value, false)
  assert.equal(state.shouldShowCollapsedSteps.value, false)
  props.session.steerForked = false
  assert.equal(state.isConversationDone.value, true)
  assert.equal(state.shouldShowCollapsedSteps.value, true)
  props.session.run_state = 'awaiting_input'
  assert.equal(state.isConversationDone.value, false)
  assert.equal(state.shouldShowCollapsedSteps.value, false)
})

test('agent steps use compact muted timeline styling', () => {
  const theme = readFileSync(join(here, '../../../components/css/chat-timeline-theme.less'), 'utf8')
  assert.match(source, /\.chat-timeline-theme\(\)/)
  assert.match(theme, /--agent-step-text-size:\s*14px/)
  assert.match(theme, /--agent-step-summary-size:\s*13px/)
  assert.match(theme, /--agent-step-icon-color:\s*var\(--agent-step-color\)/)
  assert.match(source, /max-height:\s*none/)
  assert.match(source, /overflow-y:\s*visible/)
  assert.match(source, /\.tree-root \.action-name\s*\{[\s\S]*font-size:\s*(?:14px|var\(--app-text-base\))/)
  assert.match(source, /\.tree-child \.action-title-icon\s*\{[\s\S]*position:\s*absolute/)
  assert.doesNotMatch(source, /maskIconStyle|icon-mask|<BrowserIcon/)
  assert.match(displayVue, /<ChatTimelineIcon v-if="event.tool_name"/)
})

test('expanded agent step log keeps model thinking in the tool timeline', () => {
  assert.match(timelineSource, /visibleIntermediateEvents\s*=\s*computed\(\(\) => attachReasoningToTools\(intermediateEvents\.value, canAttachReasoning\)\)/)
  assert.match(displayVue, /v-for="\(event, index\) in timelineEvents"/)
  assert.match(toolDetailsVue, /class="action-reasoning thinking-detail-content markdown-content"/)
  assert.match(displayVue, /v-for="\(event, index\) in visibleIntermediateEvents"/)
})

test('streaming log renders reasoning alongside tool calls', () => {
  assert.match(timelineSource, /if \(!timelineCompleted\.value\)\s*\{\s*return result;\s*\}/)
})

test('expanded model reasoning stays inline without a separate thinking title', () => {
  assert.match(displayVue, /class="thinking-inline-content markdown-content"/)
  assert.match(displayVue, /class="thinking-inline-markdown" v-html="renderMarkdownContent\(event\.content\)"/)
  assert.match(displayVue, /event\.title && event\.content && isEventExpanded\(event\.event_id\)/)
  assert.match(displayLess, /\.thinking-inline-title\s*\{[\s\S]*align-items:\s*flex-start/)
  assert.match(displayLess, /\.thinking-inline-content\s*\{[\s\S]*margin-top:\s*0/)
  assert.doesNotMatch(displayLess, /\.thinking-inline-title > \.action-title-icon/)
  assert.match(displayLess, /\.tree-child \.thinking-event-card \.action-title\s*\{[\s\S]*position:\s*static/)
})

test('streaming tool log uses the same timeline structure', () => {
  assert.match(displayVue, /'is-streaming-timeline': showStreamingTimeline/)
  assert.match(displayVue, /'tree-child': isStreamingTimelineEvent\(event\)/)
  assert.match(displayVue, /class="tree-child tree-child-last streaming-loading-node"/)
  assert.match(displayLess, /chat-timeline-loading\.less/)
  assert.match(timelineSource, /lastStreamingTimelineEventIndex\s*=\s*computed/)
})

test('final done row uses an existing common translation key', () => {
  assert.match(displayVue, /t\(wasUserStopped \? 'input\.messages\.stopSuccess' : 'common\.finish'\)/)
  assert.doesNotMatch(displayVue, /\$t\('common\.done'\)/)
  assert.match(displayVue, /'tree-child-last': !isSegmentDone && index === visibleIntermediateEvents\.length - 1/)
})

test('tool rows use line icon names instead of legacy asset masks', () => {
  assert.match(presentationSource, /getAgentToolIconName/)
  assert.match(displayVue, /:name="getToolIconName\(event\.tool_name\)"/)
  assert.match(presentationSource, /wiki_search: 'agentEditor\.tools\.wikiSearch'/)
  assert.match(presentationSource, /wiki_read_page: 'agentEditor\.tools\.wikiReadPage'/)
  assert.match(presentationSource, /wiki_read_source_doc: 'agentStream\.tools\.wikiReadSourceDoc'/)
  assert.match(presentationSource, /list_sandbox_files: 'agentStream\.tools\.listSandboxFiles'/)
  assert.match(presentationSource, /read_sandbox_file: 'agentStream\.tools\.readSandboxFile'/)
  assert.match(presentationSource, /write_sandbox_file: 'agentStream\.tools\.writeSandboxFile'/)
  assert.match(presentationSource, /edit_sandbox_file: 'agentStream\.tools\.editSandboxFile'/)
  assert.match(presentationSource, /getReadSkillTarget/)
  assert.match(presentationSource, /tool_name === 'list_sandbox_files'/)
  assert.match(presentationSource, /toolName === 'get_document_content' \|\| toolName === 'wiki_read_source_doc'/)
  assert.doesNotMatch(source, /getToolIcon\(event\.tool_name\)/)
})

test('rag mode delegates pre-answer loading to pipeline and adds no row after answer starts', () => {
  assert.match(timelineSource, /if \(props\.ragMode \|\| hasAnswerStarted\.value\) return false/)
  assert.match(displayVue, /v-if="displayEvents\.length > 0 \|\| showMemoryRow \|\| showAgentActivityIndicator \|\| childActivity"/)
  assert.doesNotMatch(displayVue, /ChatActivityIndicator/)
})

test('rag mode keeps model thinking out of the answer stream component', () => {
  const displayEventsBlock = timelineSource.slice(
    timelineSource.indexOf('const displayEvents = computed'),
    timelineSource.indexOf('// Get unique key for event'),
  )
  assert.match(displayEventsBlock, /if \(props\.ragMode\)\s*\{[\s\S]*e\.type === 'answer'/)
  assert.doesNotMatch(displayEventsBlock, /attachment_parsing/)
  assert.doesNotMatch(
    displayEventsBlock,
    /if \(props\.ragMode\)\s*\{[\s\S]*e\.type === 'answer' \|\| e\.type === 'thinking'/,
  )
})

test('only the collapsed root summary shows an expand chevron', () => {
  assert.match(displayVue, /tree-root-summary[\s\S]*class="action-show-icon"/)
  assert.match(displayVue, /showIntermediateSteps \? 'chevron-down' : 'chevron-right'/)
  assert.doesNotMatch(displayVue, /isEventExpanded\(event\.tool_call_id\) \? 'chevron/)
  assert.doesNotMatch(displayVue, /isEventExpanded\(event\.event_id\) \? 'chevron/)
})

test('recalled memory rides the agent timeline as its leading row', () => {
  const template = displayVue.split('<script')[0]
  assert.equal((template.match(/<ChatMemoryStep/g) || []).length, 2)
  assert.match(template, /<div v-if="showIntermediateSteps" class="tree-children">\s*\n\s*<ChatMemoryStep/)
  assert.match(template, /<ChatMemoryStep\s*\n\s*v-if="showMemoryRow"/)
  assert.match(template, /<ChatMemoryStep[\s\S]*?:is-last="memoryIsLast"/)
  assert.match(timelineSource, /!props\.ragMode && hasMemory\.value && !shouldShowCollapsedSteps\.value/)
  assert.match(
    timelineSource,
    /const memoryIsLast = computed\([\s\S]*lastStreamingTimelineEventIndex\.value === -1/,
  )
})

test('pending tool rows do not render an extra axis dot', () => {
  assert.doesNotMatch(displayLess, /&\.action-pending\s*\{[\s\S]*&::after/)
})

test('agent mode shows a native placeholder before answer whenever nothing is pending', () => {
  assert.match(timelineSource, /if \(isSegmentDone\.value\) return false/)
  assert.match(timelineSource, /return !hasPendingStreamingActivity\.value/)
  assert.match(timelineSource, /const hasPendingStreamingActivity = computed/)
  assert.match(timelineSource, /event\.thinking === true \|\| isThinkingActive\(event\.event_id\)/)
  assert.match(timelineSource, /return isUnresolvedCollaborationPrompt\(event\)/)
  assert.match(displayVue, /class="action-card action-pending"/)
  assert.match(displayVue, /childActivity \? 'agentStream\.collaboration\.waiting' : 'chat\.thinkingAlt'/)
  assert.match(displayLess, /chat-timeline-loading\.less/)
})
