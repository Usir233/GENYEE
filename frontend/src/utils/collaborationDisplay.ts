/** Presentation projection; never expose runtime IDs or raw tool payloads. */
export interface CollaborationTaskView {
  id: string
  name: string
  sessionId: string
  runId: string
  displayName: string
  status: string
  progress?: { phase?: string; tool_name?: string; iteration?: number }
  latestMessage: string
}

export interface CollaborationTimelineEvent {
  type?: string
  event_id?: string
  tool_name?: string
  tool_call_id?: string
  pending?: boolean
  canceled?: boolean
  resolved?: boolean
  abandoned?: boolean
  success?: boolean
  superseded?: boolean
  timestamp?: unknown
  arguments?: Record<string, unknown>
  tool_data?: Record<string, unknown>
  task?: CollaborationTaskView
  [key: string]: unknown
}

interface RawSnapshotTask {
  id?: unknown
  member_id?: unknown
  name?: unknown
  task_name?: unknown
  session_id?: unknown
  run_id?: unknown
  display_name?: unknown
  status?: unknown
  cancel_requested?: unknown
  progress?: { phase?: string; tool_name?: string; iteration?: number }
  latest_message?: string | { content?: unknown; message?: unknown } | null
}

const statuses = new Set(['queued', 'running', 'waiting', 'succeeded', 'failed', 'interrupted', 'cancelled', 'cancelling', 'needs_attention'])

export function collaborationTasks(snapshot: unknown): CollaborationTaskView[] {
  const tasks =
    snapshot && typeof snapshot === 'object'
      ? (snapshot as { tasks?: unknown }).tasks
      : undefined
  if (!Array.isArray(tasks)) return []
  return tasks
    .filter((task): task is RawSnapshotTask => Boolean(task) && typeof task === 'object')
    .map((task, index: number) => {
      const latest = task.latest_message
      const message =
        typeof latest === 'string'
          ? latest
          : latest && typeof latest === 'object'
            ? (latest.content ?? latest.message)
            : undefined
      const rawStatus = typeof task.status === 'string' ? task.status : ''
      return {
        id: String(task.id || task.member_id || task.name || task.task_name || index),
        name: String(task.name || task.task_name || ''),
        sessionId: String(task.session_id || ''),
        runId: String(task.run_id || ''),
        displayName: String(task.display_name || task.name || task.task_name || ''),
        status: task.cancel_requested && ['queued', 'running', 'waiting'].includes(rawStatus)
          ? 'cancelling'
          : statuses.has(rawStatus)
            ? rawStatus
            : 'unknown',
        progress: task.progress && typeof task.progress === 'object' ? task.progress : undefined,
        latestMessage: typeof message === 'string' ? message : '',
      }
    })
}

export function collaborationWaitState(
  events: CollaborationTimelineEvent[] = [],
  settled = false,
): 'waiting' | 'finished' | 'failed' | undefined {
  const waits = events.filter(event => event?.type === 'tool_call' && event.tool_name === 'wait_agent')
  if (!waits.length) return undefined
  if (!settled && waits.some(event => event.pending && !event.canceled)) return 'waiting'
  return waits[waits.length - 1].success === false ? 'failed' : 'finished'
}

/** Current activity belongs at the live response tail, not the first wait call. */
export function collaborationActivity(
  events: CollaborationTimelineEvent[],
  snapshot: unknown,
  settled = false,
): 'waiting' | 'approval' | undefined {
  if (settled || events.some(event => event?.type === 'stop' || event?.type === 'agent_complete')) return undefined
  if (collaborationWaitState(events) !== 'waiting') return undefined
  return events.some(isUnresolvedCollaborationPrompt) || collaborationTasks(snapshot).some(task => task.status === 'waiting')
    ? 'approval' : 'waiting'
}

/** All wait polls share the task panel; they must not multiply timeline rows. */
export function withoutCollaborationWaits<T extends CollaborationTimelineEvent>(events: T[]): T[] {
  return events.filter(event => !(event?.type === 'tool_call' && event.tool_name === 'wait_agent'))
}

export function isCollaborationTaskActive(status: string): boolean {
  return ['queued', 'running', 'waiting', 'cancelling'].includes(status)
}

export function isUnresolvedCollaborationPrompt(event: CollaborationTimelineEvent | null | undefined): boolean {
  return ['tool_approval_required', 'agent_prompt_required', 'mcp_oauth_required'].includes(String(event?.type || ''))
    && !event?.resolved && !event?.canceled && !event?.abandoned
}

/** Count the actual calls, including calls represented by the task panel. */
export function countToolCalls(events: CollaborationTimelineEvent[]): number {
  const calls = events.filter(event => event?.type === 'tool_call')
  const ids = new Set(calls.map(event => event.tool_call_id).filter(Boolean))
  return ids.size + calls.filter(event => !event.tool_call_id).length
}

/** Keep child rows at their spawn/continuation location, including restored snapshots. */
export function collaborationTimeline(
  events: CollaborationTimelineEvent[],
  snapshot: unknown,
  settled = false,
): CollaborationTimelineEvent[] {
  const tasks = collaborationTasks(snapshot)
  const seen = new Set<string>()
  const result: CollaborationTimelineEvent[] = []
  const waitState = collaborationWaitState(events, settled)
  const lastWait = events.filter(event => event?.type === 'tool_call' && event.tool_name === 'wait_agent').at(-1)
  for (const event of events) {
    if (event.type === 'tool_call' && (event.tool_name === 'spawn_agent'
      || (event.tool_name === 'followup_task' && event.success !== false && !event.canceled))) {
      const target = event.arguments?.target
      const task = tasks.find(t => event.tool_name === 'followup_task'
        ? t.id === target || t.name === target || `/root/${t.name}` === target
        : t.id === event.tool_data?.member_id
          || t.name === event.arguments?.task_name || t.name === event.arguments?.name)
      if (task) {
        if (!seen.has(task.id)) result.push({ type: 'child_task', event_id: `child-${task.id}`, task, timestamp: event.timestamp })
        seen.add(task.id)
        continue
      }
    }
    if (tasks.length && event.type === 'tool_call' && event.tool_name === 'list_agents') continue
    if (event.type === 'tool_call' && event.tool_name === 'wait_agent') {
      // Successful polls (including timeouts) are plumbing, not milestones.
      // Preserve an actual failure in the normal tool row for inspection.
      if (event === lastWait && waitState === 'failed') result.push(event)
      continue
    }
    result.push(event)
  }
  // A restored segment may lack the original spawn calls. Keep orphan rows in
  // the transcript before its answer, never in a separate top status panel.
  const missing: CollaborationTimelineEvent[] = tasks
    .filter(t => !seen.has(t.id))
    .map(task => ({ type: 'child_task', event_id: `child-${task.id}`, task }))
  const answer = result.findIndex(e => e.type === 'answer' && !e.superseded)
  result.splice(answer < 0 ? result.length : answer, 0, ...missing)
  return result
}
