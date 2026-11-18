export interface ShellCommandProgress {
  command: string
  started_at: string
  output: string
  done: boolean
  stdout?: string
  stderr?: string
  exit_code?: number
  [key: string]: unknown
}

/** Live tail under a shell card. Async jobs keep streaming after the tool returns. */
export function showLiveShellOutput(event: {
  tool_name?: string
  pending?: boolean
  command_output?: { done?: boolean } | null
} | null | undefined): boolean {
  const output = event?.command_output
  if (!output || output.done) return false
  if (event?.tool_name === 'shell_exec_async') return true
  return event?.tool_name === 'shell_exec' && event.pending === true
}
