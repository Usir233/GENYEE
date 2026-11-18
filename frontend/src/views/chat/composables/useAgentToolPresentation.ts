import i18n from '@/i18n';
import type { DisplayType } from '@/types/tool-results';
import { getAgentToolIconName } from '@/utils/agent-tool-icons';
import { countSearchResultFiles, getQueryText, getWikiPageText } from '@/utils/agent-tool-display';
import { getAttachmentParsingSummaryHtml } from '@/utils/attachmentParsingDisplay';
import { browserToolTitle } from '@/utils/browserToolDisplay';
import { getCatalogToolDisplayType, getCatalogToolTitle } from '@/utils/catalogToolDisplay';
import { countGrepDocuments, groupGrepChunkResults } from '@/utils/grepResultsGroup';
import { getKnowledgeChunksSummaryHtml } from '@/utils/knowledgeChunksDisplay';
import { getMcpToolDisplayType, getMcpToolTitle, mcpToolResultOutput } from '@/utils/mcpToolDisplay';
import { readDocumentReferences } from '@/utils/readDocumentReferences';
import { mergeDocumentReferences, type KnowledgeReferenceLike } from '@/utils/referenceSources';
import { previewShellCommand } from '@/utils/shellExecResult';
import {
  formatToolTitleWithDetail,
  getAttachmentReadTarget,
  getEventSkillName,
  getReadSkillTarget,
  getSandboxToolPath,
  skillScriptTitleCommand,
} from '@/utils/skillToolDisplay';
import { parseWikiToolReferences } from '@/utils/wikiToolReferences';
import type { AgentTimelineEvent, AgentToolData, PlanStatusItem } from '../types/agentStream';

export type TranslateFn = (key: string, params?: Record<string, unknown>) => string;

export const TOOL_NAME_KEYS: Record<string, string> = {
  discover_mcp_tools: 'agentStream.mcp.discoverTools',
  call_mcp_tool: 'agentStream.mcp.callTool',
  search_knowledge: 'agentStream.tools.searchKnowledge',
  read_document: 'agentStream.tools.readDocument',
  list_documents: 'agentStream.tools.listDocuments',
  // Retired names still present in stored chat history
  knowledge_search: 'agentStream.tools.searchKnowledge',
  grep_chunks: 'agentStream.tools.grepChunks',
  web_search: 'agentStream.tools.webSearch',
  web_fetch: 'agentStream.tools.webFetch',
  get_document_info: 'agentStream.tools.getDocumentInfo',
  list_knowledge_chunks: 'agentStream.tools.listKnowledgeChunks',
  get_related_documents: 'agentStream.tools.getRelatedDocuments',
  get_document_content: 'agentStream.tools.getDocumentContent',
  wiki_search: 'agentEditor.tools.wikiSearch',
  wiki_read_page: 'agentEditor.tools.wikiReadPage',
  wiki_read_source_doc: 'agentStream.tools.wikiReadSourceDoc',
  wiki_flag_issue: 'agentEditor.tools.wikiFlagIssue',
  wiki_write_page: 'agentEditor.tools.wikiWritePage',
  wiki_replace_text: 'agentEditor.tools.wikiReplaceText',
  wiki_rename_page: 'agentEditor.tools.wikiRenamePage',
  wiki_delete_page: 'agentEditor.tools.wikiDeletePage',
  wiki_read_issue: 'agentEditor.tools.wikiReadIssue',
  wiki_update_issue: 'agentEditor.tools.wikiUpdateIssue',
  todo_write: 'agentStream.tools.todoWrite',
  ask_user: 'agentStream.tools.askUser',
  request_secure_form: 'agentStream.tools.requestSecureForm',
  browser_fill_secret: 'agentStream.tools.browserFillSecret',
  knowledge_graph_extract: 'agentStream.tools.knowledgeGraphExtract',
  thinking: 'agentStream.tools.thinking',
  attachment_parsing: 'agentStream.tools.attachmentParsing',
  image_analysis: 'agentStream.tools.imageAnalysis',
  query_understand: 'agentStream.tools.queryUnderstand',
  query_knowledge_graph: 'agentStream.tools.queryKnowledgeGraph',
  read_skill: 'agentStream.tools.readSkill',
  read_file: 'agentStream.tools.readFile',
  execute_skill_script: 'agentStream.tools.executeSkillScript',
  list_sandbox_files: 'agentStream.tools.listSandboxFiles',
  read_sandbox_file: 'agentStream.tools.readSandboxFile',
  write_sandbox_file: 'agentStream.tools.writeSandboxFile',
  edit_sandbox_file: 'agentStream.tools.editSandboxFile',
  shell_exec: 'agentStream.tools.shellExec',
  shell_exec_async: 'agentStream.tools.shellExec',
  local_browser: 'localBrowser.local',
  data_analysis: 'agentStream.tools.dataAnalysis',
  data_schema: 'agentStream.tools.dataSchema',
  database_query: 'agentStream.tools.databaseQuery',
  find_operations: 'agentStream.tools.findOperations',
  run_operation: 'agentStream.tools.runOperation',
  spawn_agent: 'agentStream.tools.spawn_agent',
  list_agents: 'agentStream.tools.list_agents',
  send_message: 'agentStream.tools.send_message',
  followup_task: 'agentStream.tools.followup_task',
  interrupt_agent: 'agentStream.tools.interrupt_agent',
  wait_agent: 'agentStream.tools.wait_agent',
  open_page: 'agentStream.tools.openPage',
  view_page: 'agentStream.tools.viewPage',
  read_page: 'agentStream.tools.readPage',
  select_tab: 'agentStream.tools.selectTab',
  fill_page: 'agentStream.tools.fillPage',
  search_genyee_docs: 'agentStream.tools.searchGENYEEDocs',
  read_genyee_doc: 'agentStream.tools.readGENYEEDoc',
};

export const WIKI_EDIT_TOOL_NAMES = new Set([
  'wiki_write_page',
  'wiki_replace_text',
  'wiki_rename_page',
  'wiki_delete_page',
]);

export const WIKI_ISSUE_TOOL_NAMES = new Set([
  'wiki_flag_issue',
  'wiki_read_issue',
  'wiki_update_issue',
]);

export const formatMCPToolName = (rawName: string): string => {
  const rest = rawName.slice(4);
  const parts = rest.split('_');
  if (parts.length <= 1) return rest;
  return parts.map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
};

export const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
export const ID_LABEL_RE = /\b(knowledge_base_id|knowledge_id|chunk_id|knowledge_base_ids)\s*[:=]\s*/gi;

export const sanitizeForDisplay = (text: string): string => {
  if (!text) return text;
  let result = text;
  for (const [name, i18nKey] of Object.entries(TOOL_NAME_KEYS)) {
    result = result.replaceAll(name, i18n.global.t(i18nKey));
  }
  result = result.replace(/\bmcp_([a-z0-9_]+)/g, (_match, rest: string) => {
    const parts = rest.split('_');
    return parts.map((p: string) => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
  });
  result = result.replace(ID_LABEL_RE, '');
  result = result.replace(UUID_RE, '');
  result = result.replace(/(?<!`)`[ \t]*`(?!`)/g, '');
  result = result.replace(/\(\s*\)/g, '');
  return result;
};

export const formatDuration = (ms?: number): string => {
  if (!ms) return '0s';
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  return `${minutes}m ${remainingSeconds}s`;
};

export function useAgentToolPresentation(t: TranslateFn) {
  const getToolIconName = getAgentToolIconName;

  const getLocalizedToolName = (toolName?: string | null): string => {
    if (!toolName) return t('agent.toolFallback');
    const key = TOOL_NAME_KEYS[toolName];
    if (key) return t(key);
    if (toolName.startsWith('mcp_')) {
      return formatMCPToolName(toolName);
    }
    return toolName;
  };

  const cleanToolOutputContent = (output: unknown): string => {
    const raw = typeof output === 'string' ? output : '';
    return raw
      .replace(/<\/?knowledge_chunks[^>]*>/gi, '')
      .replace(/<\/?chunk[^>]*>/gi, '')
      .trim();
  };

  const getToolKnowledgeBaseId = (toolData?: AgentToolData | null): string | undefined => {
    if (typeof toolData?.knowledge_base_id === 'string' && toolData.knowledge_base_id) {
      return toolData.knowledge_base_id;
    }
    const kbIds = Array.isArray(toolData?.knowledge_base_ids) ? toolData.knowledge_base_ids : [];
    if (kbIds.length === 1 && typeof kbIds[0] === 'string' && kbIds[0]) {
      return kbIds[0];
    }
    return undefined;
  };

  const formatToolResultContent = (value: unknown): string => {
    if (typeof value === 'string') return value.trim();
    if (value == null) return '';
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  };

  const isMcpTool = (toolName?: string | null): boolean => String(toolName || '').startsWith('mcp_');

  const toolResultOutput = (event?: AgentTimelineEvent | null): string | undefined => {
    if (getMcpToolDisplayType(event?.tool_name) || getCatalogToolDisplayType(event?.tool_name)) {
      return event?.output || event?.error;
    }
    return mcpToolResultOutput(event ?? {});
  };

  const resolveToolDisplayType = (event?: AgentTimelineEvent | null): DisplayType | undefined => {
    const mcpType = getMcpToolDisplayType(event?.tool_name);
    if (mcpType) return mcpType;
    const catalogType = getCatalogToolDisplayType(event?.tool_name);
    if (catalogType) return catalogType;
    if (event?.display_type) return event.display_type as DisplayType;
    if (
      event?.tool_name === 'shell_exec' ||
      event?.tool_name === 'shell_exec_async' ||
      event?.tool_name === 'execute_skill_script'
    ) {
      return 'shell_exec';
    }
    if (event?.tool_name === 'work_wait') return 'work_wait';
    if (event?.tool_name === 'list_sandbox_files' && event?.success !== false) {
      return 'list_sandbox_files';
    }
    if (event?.tool_name === 'write_sandbox_file' && event?.success !== false) {
      return 'write_sandbox_file';
    }
    if (event?.tool_name === 'edit_sandbox_file' && event?.success !== false) {
      return 'edit_sandbox_file';
    }
    if (event?.tool_name === 'read_skill' && event?.success !== false) {
      return 'read_skill';
    }
    return undefined;
  };

  const formatWikiEditResultContent = (toolData?: AgentToolData | null): string => {
    const rows: Array<[string, unknown]> = [];
    switch (toolData?.display_type) {
      case 'wiki_write_page':
        rows.push(
          [t('chat.wikiFieldSlug'), toolData.slug],
          [t('chat.wikiFieldTitle'), toolData.title],
          [t('chat.wikiFieldPageType'), toolData.page_type],
          [t('chat.wikiFieldSummary'), toolData.summary],
        );
        break;
      case 'wiki_replace_text':
        rows.push(
          [t('chat.wikiFieldSlug'), toolData.slug],
          [t('chat.wikiFieldTitle'), toolData.title],
          [t('chat.wikiFieldOldText'), toolData.old_text],
          [t('chat.wikiFieldNewText'), toolData.new_text],
        );
        break;
      case 'wiki_rename_page':
        rows.push(
          [t('chat.wikiFieldOldSlug'), toolData.old_slug],
          [t('chat.wikiFieldNewSlug'), toolData.new_slug],
          [t('chat.wikiFieldTitle'), toolData.title],
          [
            t('chat.wikiFieldAffectedPages'),
            Array.isArray(toolData.affected_pages)
              ? toolData.affected_pages.join(', ')
              : toolData.affected_pages,
          ],
        );
        break;
      case 'wiki_delete_page':
        rows.push(
          [t('chat.wikiFieldSlug'), toolData.slug],
          [t('chat.wikiFieldTitle'), toolData.title],
          [
            t('chat.wikiFieldAffectedPages'),
            Array.isArray(toolData.affected_pages)
              ? toolData.affected_pages.join(', ')
              : toolData.affected_pages,
          ],
        );
        break;
    }
    return rows
      .filter(([, value]) => value !== undefined && value !== null && String(value).trim())
      .map(([label, value]) => `${label}: ${String(value).trim()}`)
      .join('\n');
  };

  const getSandboxToolDetail = (event: AgentTimelineEvent): string => {
    const target = getAttachmentReadTarget(event);
    if (!target) return getSandboxToolPath(event);
    switch (target.kind) {
      case 'list':
        return t('agentStream.attachmentTarget.list');
      case 'file':
        return target.name || t('agentStream.attachmentTarget.file');
      case 'images':
        return target.name
          ? t('agentStream.attachmentTarget.images', { name: target.name })
          : t('agentStream.attachmentTarget.imagesUnnamed');
      case 'image':
        return target.name
          ? t('agentStream.attachmentTarget.image', { name: target.name, n: target.number })
          : t('agentStream.attachmentTarget.imageUnnamed', { n: target.number });
    }
  };

  const skillScriptCommandLabel = (event?: AgentTimelineEvent | null): string => {
    const fromData = String(event?.tool_data?.command || event?.arguments?.command || '').trim();
    if (fromData) return fromData;
    const skill = String(event?.tool_data?.skill_name || event?.arguments?.skill_name || '').trim();
    const script = String(event?.tool_data?.script_path || event?.arguments?.script_path || '').trim();
    const path = [skill, script].filter(Boolean).join('/');
    const args = event?.tool_data?.args || event?.arguments?.args;
    if (Array.isArray(args) && args.length) {
      return [path, ...args.map((arg: unknown) => String(arg))].filter(Boolean).join(' ');
    }
    return path;
  };

  const getToolDescription = (event: AgentTimelineEvent): string => {
    if (event.tool_name === 'local_browser') return browserToolTitle(t, event);
    if (event.pending) {
      if (event.tool_name === 'image_analysis') {
        return t('agentStream.toolStatus.imageAnalyzing');
      }
      if (event.tool_name === 'attachment_parsing') {
        return t('agentStream.toolStatus.attachmentParsing');
      }
      if (event.tool_name === 'query_understand') {
        return t('agentStream.toolStatus.queryUnderstanding');
      }
      if (event.tool_name === 'read_skill') {
        const name = getLocalizedToolName(event.tool_name);
        return `${formatToolTitleWithDetail(name, getReadSkillTarget(event))}...`;
      }
      if (event.tool_name === 'execute_skill_script') {
        const name = getLocalizedToolName(event.tool_name);
        return `${formatToolTitleWithDetail(name, getEventSkillName(event))}...`;
      }
      if (
        event.tool_name === 'list_sandbox_files' ||
        event.tool_name === 'read_file' ||
        event.tool_name === 'read_sandbox_file' ||
        event.tool_name === 'write_sandbox_file' ||
        event.tool_name === 'edit_sandbox_file'
      ) {
        const name = getLocalizedToolName(event.tool_name);
        return `${formatToolTitleWithDetail(name, getSandboxToolDetail(event))}...`;
      }
      if (event.tool_name === 'shell_exec' || event.tool_name === 'shell_exec_async') {
        return t('agentStream.toolStatus.shellExecRunning');
      }
      const localizedName = getLocalizedToolName(event.tool_name);
      return t('agentStream.toolStatus.calling', { name: localizedName });
    }

    const success = event.success === true;
    const toolName = event.tool_name;

    if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
      return success ? t('agentStream.toolStatus.searchKb') : t('agentStream.toolStatus.searchKbFailed');
    } else if (toolName === 'wiki_search' || toolName === 'wiki_read_page') {
      const localizedName = getLocalizedToolName(toolName);
      return success ? localizedName : t('agentStream.toolStatus.calledFailed', { name: localizedName });
    } else if (toolName === 'web_search') {
      return success ? t('agentStream.toolStatus.webSearch') : t('agentStream.toolStatus.webSearchFailed');
    } else if (toolName === 'grep_chunks') {
      return success ? t('agentStream.toolStatus.grepSearch') : t('agentStream.toolStatus.grepSearchFailed');
    } else if (toolName === 'get_document_info' || toolName === 'list_documents') {
      return success ? t('agentStream.toolStatus.getDocInfo') : t('agentStream.toolStatus.getDocInfoFailed');
    } else if (toolName === 'read_document' || toolName === 'get_document_content' || toolName === 'wiki_read_source_doc') {
      return success
        ? t('agentStream.toolStatus.viewDocument')
        : t('agentStream.toolStatus.calledFailed', { name: t('agentStream.toolStatus.viewDocument') });
    } else if (toolName === 'thinking') {
      return success ? t('agentStream.toolStatus.thinkingDone') : t('agentStream.toolStatus.thinkingFailed');
    } else if (toolName === 'todo_write') {
      return success ? t('agentStream.toolStatus.updateTodos') : t('agentStream.toolStatus.updateTodosFailed');
    } else if (toolName === 'image_analysis') {
      return success ? t('agentStream.toolStatus.imageAnalysisDone') : t('agentStream.toolStatus.imageAnalysisFailed');
    } else if (toolName === 'attachment_parsing') {
      return success ? t('agentStream.toolStatus.attachmentParsingDone') : t('agentStream.toolStatus.attachmentParsingFailed');
    } else if (toolName === 'query_understand') {
      return success
        ? t('agentStream.toolStatus.queryUnderstandDone')
        : t('agentStream.toolStatus.calledFailed', { name: getLocalizedToolName(toolName) });
    } else if (
      toolName === 'shell_exec' ||
      toolName === 'execute_skill_script' ||
      toolName === 'read_skill' ||
      toolName === 'list_sandbox_files' ||
      toolName === 'read_file' ||
      toolName === 'read_sandbox_file' ||
      toolName === 'write_sandbox_file' ||
      toolName === 'edit_sandbox_file'
    ) {
      const localizedName = getLocalizedToolName(toolName);
      return success ? localizedName : t('agentStream.toolStatus.calledFailed', { name: localizedName });
    }
    const localizedName = getLocalizedToolName(toolName);
    return success
      ? t('agentStream.toolStatus.called', { name: localizedName })
      : t('agentStream.toolStatus.calledFailed', { name: localizedName });
  };

  const getToolSummary = (event?: AgentTimelineEvent | null): string => {
    if (!event || event.pending || !event.success) return '';

    const toolName = event.tool_name;
    const toolData = event.tool_data;

    if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
      return '';
    } else if (toolName === 'get_document_info' || toolName === 'list_documents') {
      if (toolData?.title) {
        return t('agentStream.toolSummary.getDocument', { title: toolData.title });
      }
    } else if (toolName === 'read_document' || toolName === 'list_knowledge_chunks') {
      if (toolData?.faq_question) {
        return t('agentStream.toolSummary.listFaqEntry', { question: toolData.faq_question });
      }
      if (toolData?.fetched_chunks !== undefined) {
        const title = toolData?.knowledge_title || toolData?.knowledge_id || t('agentStream.toolSummary.document');
        return t('agentStream.toolSummary.listChunks', {
          title,
          fetched: toolData.fetched_chunks,
          total: toolData.total_chunks ?? '?',
        });
      }
    } else if (toolName === 'todo_write') {
      const steps = toolData?.steps;
      if (Array.isArray(steps)) {
        const inProgress = steps.filter((s) => s.status === 'in_progress').length;
        const pending = steps.filter((s) => s.status === 'pending').length;
        const completed = steps.filter((s) => s.status === 'completed').length;

        const parts: string[] = [];
        if (inProgress > 0) parts.push(`🚀 ${t('agentStream.plan.inProgress')} ${inProgress}`);
        if (pending > 0) parts.push(`📋 ${t('agentStream.plan.pending')} ${pending}`);
        if (completed > 0) parts.push(`✅ ${t('agentStream.plan.completed')} ${completed}`);

        return parts.join(' · ');
      }
    } else if (toolName === 'thinking') {
      return toolData?.thought ? t('agentStream.toolSummary.deepThinking') : '';
    }

    return '';
  };

  const getToolTitle = (event: AgentTimelineEvent): string => {
    if (event.tool_name === 'local_browser') return browserToolTitle(t, event);
    const mcpTitle = getMcpToolTitle(t, event);
    if (mcpTitle) return mcpTitle;
    const catalogTitle = getCatalogToolTitle(t, event);
    if (catalogTitle) return catalogTitle;
    if (event.pending) {
      if (event.tool_name === 'image_analysis') {
        return t('agentStream.toolStatus.imageAnalyzing');
      }
      if (event.tool_name === 'attachment_parsing') {
        return t('agentStream.toolStatus.attachmentParsing');
      }
      if (event.tool_name === 'wiki_search' || event.tool_name === 'wiki_read_page') {
        return `${getLocalizedToolName(event.tool_name)}...`;
      }
      if (event.tool_name === 'read_skill') {
        const name = getLocalizedToolName(event.tool_name);
        return `${formatToolTitleWithDetail(name, getReadSkillTarget(event))}...`;
      }
      if (event.tool_name === 'execute_skill_script') {
        const name = getLocalizedToolName(event.tool_name);
        return `${formatToolTitleWithDetail(name, getEventSkillName(event))}...`;
      }
      if (
        event.tool_name === 'list_sandbox_files' ||
        event.tool_name === 'read_file' ||
        event.tool_name === 'read_sandbox_file' ||
        event.tool_name === 'write_sandbox_file' ||
        event.tool_name === 'edit_sandbox_file'
      ) {
        const name = getLocalizedToolName(event.tool_name);
        return `${formatToolTitleWithDetail(name, getSandboxToolDetail(event))}...`;
      }
      if (event.tool_name === 'shell_exec' || event.tool_name === 'shell_exec_async') {
        return t('agentStream.toolStatus.shellExecRunning');
      }
      const localizedName = getLocalizedToolName(event.tool_name);
      return t('agentStream.toolStatus.calling', { name: localizedName });
    }

    const toolName = event.tool_name;
    const isSearchTool = toolName === 'search_knowledge' || toolName === 'knowledge_search' || toolName === 'wiki_search';
    const isWebSearchTool = toolName === 'web_search';
    const isGrepTool = toolName === 'grep_chunks';

    if (isSearchTool) {
      const baseTitle = getToolDescription(event);
      const queryText = getQueryText(event.arguments) || getQueryText(event.tool_data);
      if (queryText) {
        return `${baseTitle}：「${queryText}」`;
      }
      return baseTitle;
    }

    if (isWebSearchTool) {
      const baseTitle = getToolDescription(event);
      let queryText = '';
      const rawArgQuery = event.arguments && typeof event.arguments === 'object' ? event.arguments.query : undefined;
      const rawDataQuery = event.tool_data?.query;
      const query = rawArgQuery ?? rawDataQuery;
      if (Array.isArray(query)) {
        queryText = query.filter((q): q is string => Boolean(q) && typeof q === 'string').join('，');
      } else if (typeof query === 'string') {
        queryText = query;
      }
      if (queryText) {
        return `${baseTitle}：「${queryText}」`;
      }
      return baseTitle;
    }

    if (isGrepTool) {
      const baseTitle = getToolDescription(event);
      let patterns: string[] = [];
      const args = event.arguments && typeof event.arguments === 'object' ? event.arguments : undefined;
      const data = event.tool_data;
      if (args) {
        if (Array.isArray(args.queries)) patterns = args.queries.map(String);
        else if (Array.isArray(args.patterns)) patterns = args.patterns.map(String);
        else if (typeof args.query === 'string' && args.query) patterns = [args.query];
        else if (typeof args.pattern === 'string' && args.pattern) patterns = [args.pattern];
      } else if (data) {
        if (Array.isArray(data.queries)) patterns = data.queries.map(String);
        else if (Array.isArray(data.patterns)) patterns = data.patterns.map(String);
        else if (typeof data.query === 'string' && data.query) patterns = [data.query];
        else if (typeof data.pattern === 'string' && data.pattern) patterns = [data.pattern];
      }
      if (patterns.length > 0) {
        const displayPatterns = patterns.slice(0, 2);
        const patternText = displayPatterns.join('、');
        const moreText = patterns.length > 2 ? ` +${patterns.length - 2}` : '';
        return `${baseTitle}：「${patternText}${moreText}」`;
      }
      return baseTitle;
    }

    if (toolName === 'wiki_read_page') {
      const pageLabel = String(
        event.tool_data?.title || getWikiPageText(event.arguments) || getWikiPageText(event.tool_data),
      ).trim();
      const baseTitle = getToolDescription(event);
      return pageLabel ? `${baseTitle}：「${sanitizeForDisplay(pageLabel)}」` : baseTitle;
    }

    if (toolName === 'read_skill') {
      return formatToolTitleWithDetail(getToolDescription(event), getReadSkillTarget(event));
    }

    if (
      toolName === 'list_sandbox_files' ||
      toolName === 'read_file' ||
      toolName === 'read_sandbox_file' ||
      toolName === 'write_sandbox_file' ||
      toolName === 'edit_sandbox_file'
    ) {
      return formatToolTitleWithDetail(getToolDescription(event), getSandboxToolDetail(event));
    }

    if (toolName === 'execute_skill_script') {
      const command = previewShellCommand(skillScriptCommandLabel(event));
      const baseTitle = formatToolTitleWithDetail(getToolDescription(event), getEventSkillName(event));
      const rest = skillScriptTitleCommand(getEventSkillName(event), command);
      return rest ? `${baseTitle}：${rest}` : baseTitle;
    }

    if (toolName === 'shell_exec') {
      const command = previewShellCommand(skillScriptCommandLabel(event));
      const baseTitle = getToolDescription(event);
      return command ? `${baseTitle}：${command}` : baseTitle;
    }

    const summary = getToolSummary(event);
    return summary || getToolDescription(event);
  };

  const buildToolResultReference = (
    event: AgentTimelineEvent,
    content: string,
  ): KnowledgeReferenceLike[] => {
    if (!content) return [];
    const toolName = String(event.tool_name || '');
    const title = getToolTitle(event);
    return [{
      id: event.tool_call_id || toolName,
      chunk_type: 'tool_result',
      knowledge_title: title,
      content,
      metadata: {
        title,
        source: getLocalizedToolName(toolName),
        tool: toolName,
      },
    }];
  };

  function getToolReferenceItems(event?: AgentTimelineEvent | null): KnowledgeReferenceLike[] {
    if (!event || event.pending) return [];
    const toolName = event.tool_name || '';
    const toolData = event.tool_data;

    if (isMcpTool(toolName)) {
      const output = formatToolResultContent(event.output) || formatToolResultContent(toolData);
      return buildToolResultReference(event, output);
    }

    if (WIKI_ISSUE_TOOL_NAMES.has(toolName)) {
      return buildToolResultReference(event, formatToolResultContent(event.output));
    }

    if (!toolData) return [];

    if (toolName === 'wiki_search' || toolName === 'wiki_read_page') {
      return parseWikiToolReferences(toolName, event.output, event.tool_call_id || toolName)
        .map((item) => ({
          id: item.id,
          chunk_type: 'tool_result',
          knowledge_title: item.title,
          content: item.content,
          metadata: {
            title: item.title,
            source: getLocalizedToolName(toolName),
            tool: toolName,
            ...(item.slug ? { slug: item.slug } : {}),
            ...(item.knowledgeBaseId ? { knowledge_base_id: item.knowledgeBaseId } : {}),
          },
        }));
    }

    if (WIKI_EDIT_TOOL_NAMES.has(toolName)) {
      return buildToolResultReference(
        event,
        formatWikiEditResultContent(toolData) || formatToolResultContent(event.output),
      );
    }

    if (toolName === 'web_search') {
      const results = Array.isArray(toolData.results) ? toolData.results : [];
      return results
        .filter((item) => Boolean(item?.url))
        .map((item, index) => ({
          id: item.url as string,
          chunk_type: 'web_search',
          knowledge_title: item.title || item.source || item.url,
          content: item.snippet || item.content || '',
          metadata: {
            url: item.url || '',
            title: item.title || '',
            snippet: item.snippet || item.content || '',
          },
          chunk_index: item.result_index ?? index + 1,
        }));
    }

    if (toolName === 'web_fetch') {
      const results = Array.isArray(toolData.results) ? toolData.results : [];
      return results
        .filter((item) => Boolean(item?.url) && (!item.status || item.status === 'success'))
        .map((item, index) => ({
          id: item.url as string,
          chunk_type: 'web_search',
          knowledge_title: item.url,
          content: item.summary || item.raw_content || '',
          metadata: {
            url: item.url || '',
            title: item.url || '',
            snippet: item.summary || item.raw_content || '',
          },
          chunk_index: index + 1,
        }));
    }

    if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
      const results = Array.isArray(toolData.results) ? toolData.results : [];
      const fallbackKnowledgeBaseId = getToolKnowledgeBaseId(toolData);
      return mergeDocumentReferences(
        results
          .filter((item) => Boolean(item?.chunk_id || item?.knowledge_id))
          .map((item, index) => ({
            id: item.chunk_id || `${item.knowledge_id}-${item.result_index ?? index + 1}`,
            knowledge_id: item.knowledge_id,
            knowledge_title: item.faq_standard_question || item.knowledge_title,
            knowledge_base_id: item.knowledge_base_id || fallbackKnowledgeBaseId,
            chunk_index: item.result_index ?? index + 1,
            chunk_type: item.chunk_type,
            content: item.content || '',
          })),
      );
    }

    if (toolName === 'grep_chunks') {
      const chunkResults = Array.isArray(toolData.chunk_results) ? toolData.chunk_results : [];
      if (chunkResults.length) {
        return groupGrepChunkResults(chunkResults)
          .filter((group) => group.knowledge_id || group.title)
          .map((group, index) => ({
            id: group.knowledge_id || group.key,
            chunk_ids: group.chunks.map((chunk) => chunk.chunk_id).filter(Boolean),
            knowledge_id: group.knowledge_id,
            knowledge_title: group.title,
            knowledge_base_id: group.knowledge_base_id,
            chunk_index: index + 1,
            chunk_type: group.is_faq ? 'faq' : undefined,
            content:
              group.chunks.map((chunk) => chunk.content).filter(Boolean).slice(0, 3).join('\n\n') ||
              group.match_snippet ||
              '',
          }));
      }

      const knowledgeResults = Array.isArray(toolData.knowledge_results) ? toolData.knowledge_results : [];
      return mergeDocumentReferences(
        knowledgeResults
          .filter((item) => Boolean(item?.knowledge_id))
          .map((item, index) => ({
            id: item.knowledge_id as string,
            knowledge_id: item.knowledge_id,
            knowledge_title: item.faq_question || item.knowledge_title,
            knowledge_base_id: item.knowledge_base_id,
            chunk_index: index + 1,
            content: item.match_snippet || '',
          })),
      );
    }

    if (toolName === 'read_document' || toolName === 'list_knowledge_chunks' || toolName === 'wiki_read_source_doc') {
      return readDocumentReferences(toolData, cleanToolOutputContent(event.output), getToolDescription(event));
    }

    return [];
  }

  const isReferenceDrawerTool = (toolName?: string | null): boolean =>
    isMcpTool(toolName) ||
    WIKI_EDIT_TOOL_NAMES.has(String(toolName || '')) ||
    WIKI_ISSUE_TOOL_NAMES.has(String(toolName || '')) ||
    toolName === 'search_knowledge' ||
    toolName === 'knowledge_search' ||
    toolName === 'web_search' ||
    toolName === 'web_fetch' ||
    toolName === 'grep_chunks' ||
    toolName === 'read_document' ||
    toolName === 'list_knowledge_chunks' ||
    toolName === 'wiki_search' ||
    toolName === 'wiki_read_page' ||
    toolName === 'wiki_read_source_doc';

  const hasResults = (event?: AgentTimelineEvent | null): boolean => {
    if (!event || !event.tool_data) return true;
    const toolName = event.tool_name;
    if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
      const count = event.tool_data.results?.length || event.tool_data.count || 0;
      return count > 0;
    }
    if (toolName === 'web_search') {
      const count = event.tool_data.results?.length || event.tool_data.count || 0;
      return count > 0;
    }
    if (toolName === 'grep_chunks') {
      const totalMatches = event.tool_data.total_matches || 0;
      const resultCount = event.tool_data.result_count || 0;
      return totalMatches > 0 || resultCount > 0;
    }
    if (toolName === 'read_document' || toolName === 'list_knowledge_chunks') {
      return false;
    }
    if (toolName === 'attachment_parsing' || toolName === 'image_analysis') {
      return false;
    }
    return true;
  };

  const hasExpandableResults = (event?: AgentTimelineEvent | null): boolean => {
    if (!event) return false;
    if (isReferenceDrawerTool(event.tool_name)) return false;
    if ((event.tool_name === 'open_page' || event.tool_name === 'view_page') && event.success !== false) return false;
    if (event.tool_name === 'fill_page' && event.success !== false) return false;
    if ((event.tool_name === 'read_page' || event.tool_name === 'select_tab') && event.success !== false) return false;
    return hasResults(event);
  };

  const getPlanStatusParts = (event?: AgentTimelineEvent | null) => {
    const steps = event?.tool_data?.steps;
    if (!Array.isArray(steps)) {
      return { inProgress: 0, pending: 0, completed: 0 };
    }
    return {
      inProgress: steps.filter((s) => s.status === 'in_progress').length,
      pending: steps.filter((s) => s.status === 'pending').length,
      completed: steps.filter((s) => s.status === 'completed').length,
    };
  };

  const getPlanStatusItems = (event?: AgentTimelineEvent | null): PlanStatusItem[] => {
    const parts = getPlanStatusParts(event);
    const items: PlanStatusItem[] = [];
    if (parts.inProgress > 0) {
      items.push({
        icon: 'play-circle',
        class: 'in-progress',
        label: t('agentStream.plan.inProgress'),
        count: parts.inProgress,
      });
    }
    if (parts.pending > 0) {
      items.push({
        icon: 'time',
        class: 'pending',
        label: t('agentStream.plan.pending'),
        count: parts.pending,
      });
    }
    if (parts.completed > 0) {
      items.push({
        icon: 'check-circle',
        class: 'completed',
        label: t('agentStream.plan.completed'),
        count: parts.completed,
      });
    }
    return items;
  };

  const compactionSummaryText = (event?: AgentTimelineEvent | null): string => {
    const before = Number(event?.tokens_before) || 0;
    const after = Number(event?.tokens_after) || 0;
    const parts: string[] = [];
    if (before > 0 && after > 0) {
      parts.push(
        t('agent.contextCompactedSummary', {
          before: before.toLocaleString(),
          after: after.toLocaleString(),
        }),
      );
    }
    if (event?.degraded) parts.push(t('agent.contextCompactedDegraded'));
    return parts.join(t('agent.stepSummarySeparator'));
  };

  const getSearchResultsSummary = (event?: AgentTimelineEvent | null): string => {
    if (!event || !event.tool_data) return '';
    const toolData = event.tool_data;
    const count = Number(toolData.results?.length ?? toolData.count ?? 0) || 0;
    if (count === 0) return t('agentStream.search.noResults');
    const fileCount = countSearchResultFiles(toolData.results);
    if (fileCount > 0) {
      return t('agentStream.search.foundResultsFromFiles', {
        count: `<strong>${count}</strong>`,
        files: `<strong>${fileCount}</strong>`,
      });
    }
    return t('agentStream.search.foundResults', { count: `<strong>${count}</strong>` });
  };

  const getResultsCount = (toolData?: AgentToolData | null): number => {
    if (!toolData) return 0;
    return Number(toolData.results?.length ?? toolData.count ?? 0) || 0;
  };

  const getGrepResultsSummary = (toolData?: AgentToolData | null): string => {
    if (!toolData) return '';
    const totalChunks = Number(toolData.total_matches ?? 0) || 0;
    const docCount = countGrepDocuments(toolData);
    if (totalChunks === 0) {
      return t('agentStream.search.noResults');
    }
    return t('agentStream.search.grepSummary', {
      chunks: `<strong>${totalChunks}</strong>`,
      docs: `<strong>${docCount}</strong>`,
    });
  };

  const getKnowledgeChunksSummary = (toolData?: AgentToolData | null): string =>
    getKnowledgeChunksSummaryHtml(t, toolData);

  const getAttachmentParsingSummary = (event?: AgentTimelineEvent | null): string =>
    getAttachmentParsingSummaryHtml(t, event ?? {});

  return {
    getToolIconName,
    getLocalizedToolName,
    toolResultOutput,
    resolveToolDisplayType,
    getToolReferenceItems,
    isReferenceDrawerTool,
    hasExpandableResults,
    getPlanStatusItems,
    compactionSummaryText,
    getSearchResultsSummary,
    getResultsCount,
    getGrepResultsSummary,
    getKnowledgeChunksSummary,
    getAttachmentParsingSummary,
    getToolTitle,
  };
}
