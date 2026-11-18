<template>
  <!-- MCP tool human approval -->
  <div v-if="event.type === 'tool_approval_required' && showInlineToolApproval" class="tool-event">
    <ToolApprovalCard
      :pending-id="event.pending_id || ''"
      :delegated-task="event.delegated_task"
      :service-id="event.service_id || ''"
      :service-name="event.service_name || ''"
      :mcp-tool-name="event.mcp_tool_name || ''"
      :description="event.description"
      :args-json="event.args_json"
      :grant-scope="event.grant_scope"
      :grant-lapsed="event.grant_lapsed"
      :timeout-seconds="event.timeout_seconds"
      :requested-at="event.requested_at"
      :resolved="event.resolved"
      :approved="event.approved"
      :canceled="event.canceled"
      :resolve-reason="event.resolve_reason"
      :suspendable="event.suspendable"
      :hot-wait-seconds="event.hot_wait_seconds"
      :suspended="event.suspended"
      :expires-at="event.expires_at"
      :abandoned="event.abandoned"
      v-bind="embedAuthProps"
    />
  </div>

  <!-- Question the agent asked the user (ask_user) -->
  <div v-else-if="event.type === 'agent_prompt_required' && event.kind === 'ask_user'" class="tool-event">
    <AskUserCard
      :pending-id="event.pending_id || ''"
      :request="event.request"
      :timeout-seconds="event.timeout_seconds"
      :hot-wait-seconds="event.hot_wait_seconds"
      :suspendable="event.suspendable"
      :suspended="event.suspended"
      :expires-at="event.expires_at"
      :requested-at="event.requested_at"
      :resolved="event.resolved"
      :answered="event.answered"
      :response="event.response"
      :resolve-reason="event.resolve_reason"
      :timed-out="event.timed_out"
      :canceled="event.canceled"
      :abandoned="event.abandoned"
      v-bind="embedAuthProps"
    />
  </div>

  <!-- Secure credential form -->
  <div v-else-if="event.type === 'agent_prompt_required' && event.kind === 'secure_form'" class="tool-event">
    <SecureFormCard
      :pending-id="event.pending_id || ''"
      :request="event.request"
      :timeout-seconds="event.timeout_seconds"
      :hot-wait-seconds="event.hot_wait_seconds"
      :suspendable="event.suspendable"
      :suspended="event.suspended"
      :expires-at="event.expires_at"
      :requested-at="event.requested_at"
      :resolved="event.resolved"
      :answered="event.answered"
      :resolve-reason="event.resolve_reason"
      :timed-out="event.timed_out"
      :canceled="event.canceled"
      :abandoned="event.abandoned"
    />
  </div>

  <!-- MCP OAuth in-conversation authorization prompt -->
  <div v-else-if="event.type === 'mcp_oauth_required'" class="tool-event">
    <McpOAuthCard
      :pending-id="event.pending_id || ''"
      :service-id="event.service_id || ''"
      :service-name="event.service_name || ''"
      :mcp-tool-name="event.mcp_tool_name || ''"
      :timeout-seconds="event.timeout_seconds"
      :requested-at="event.requested_at"
      :resolved="event.resolved"
      :authorized="event.authorized"
      :resolve-reason="event.resolve_reason"
      :timed-out="event.timed_out"
      :canceled="event.canceled"
      v-bind="embedAuthProps"
    />
  </div>

  <!-- Host command review or approval -->
  <div v-else-if="showInlineHostApproval" class="tool-event">
    <HostReviewRecord
      v-if="isModelReview(event) && event.host"
      :host="event.host"
      :resolved="event.resolved"
      :approved="event.approved"
      :resolve-reason="event.resolve_reason"
    />
    <HostApprovalCard
      v-else
      :pending-id="event.pending_id || ''"
      :host="event.host || { reason: event.kind === 'host_command' ? 'delete' : '' }"
      :timeout-seconds="event.timeout_seconds"
      :requested-at="event.requested_at"
      :resolved="event.resolved"
      :approved="event.approved"
      :resolve-reason="event.resolve_reason"
      :resolve-scope="event.resolve_scope"
    />
  </div>

  <!-- Fallback tool approval -->
  <div v-else-if="showInlineToolApproval" class="tool-event">
    <ToolApprovalCard
      :pending-id="event.pending_id || ''"
      :service-id="event.service_id || ''"
      :service-name="event.service_name || ''"
      :mcp-tool-name="event.mcp_tool_name || ''"
      :description="event.description"
      :args-json="event.args_json"
      :timeout-seconds="event.timeout_seconds"
      :requested-at="event.requested_at"
      :resolved="event.resolved"
      :approved="event.approved"
      :resolve-reason="event.resolve_reason"
      v-bind="embedAuthProps"
    />
  </div>
</template>

<script setup lang="ts">
import type { AgentTimelineEvent, EmbedAuthProps } from '../../types/agentStream';
import AskUserCard from '../AskUserCard.vue';
import HostApprovalCard from '../HostApprovalCard.vue';
import HostReviewRecord from '../HostReviewRecord.vue';
import { isModelReview } from '../hostApproval';
import McpOAuthCard from '../McpOAuthCard.vue';
import SecureFormCard from '../SecureFormCard.vue';
import ToolApprovalCard from '../ToolApprovalCard.vue';

defineProps<{
  event: AgentTimelineEvent;
  embedAuthProps: EmbedAuthProps;
  showInlineHostApproval: boolean;
  showInlineToolApproval: boolean;
}>();
</script>
