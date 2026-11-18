export interface ArtifactTimelineAnswerEvent {
  type?: string
  event_id?: string
  content?: string
  done?: boolean
  superseded?: boolean
  [key: string]: unknown
}

export interface ArtifactMessageTarget {
  content?: string
  agentEventStream?: ArtifactTimelineAnswerEvent[]
  [key: string]: unknown
}

// Completion is authoritative after sandbox names and version references have
// been reconciled. Update both plain content and the Agent timeline.
export function applyFinalArtifactContent(message: ArtifactMessageTarget, content: unknown): void {
  if (typeof content !== 'string' || content.trim() === '') return;
  message.content = content;
  if (!Array.isArray(message.agentEventStream)) return;
  const answers = message.agentEventStream.filter(
    (e) => e.type === 'answer' && !e.superseded,
  );
  const last = answers.pop();
  answers.forEach((e) => { e.superseded = true; });
  if (last) {
    last.content = content;
    last.done = true;
  } else if (content) {
    message.agentEventStream.push({ type: 'answer', event_id: 'completed-answer', content, done: true });
  }
}
