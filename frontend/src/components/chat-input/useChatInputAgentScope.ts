import { computed, h, nextTick, ref, watch, type Ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  BUILTIN_QUICK_ANSWER_ID,
  BUILTIN_SMART_REASONING_ID,
  type AgentConfig,
  type CustomAgent,
} from '@/api/agent';
import { listMCPServices, type MCPService } from '@/api/mcp-service';
import type { ModelConfig } from '@/api/model';
import type { WebSearchProviderEntity } from '@/api/web-search-provider';
import { useSettingsStore } from '@/stores/settings';
import { useOrganizationStore } from '@/stores/organization';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useEditorResourcesStore } from '@/stores/editorResources';
import { useUIStore } from '@/stores/ui';
import { useBrowserConnectionStore } from '@/stores/browserConnection';
import type { MentionItem } from '@/types/mention';
import {
  isAgentWebSearchEnabled,
  isAgentWebSearchReady,
  isTenantWebSearchReady,
} from '@/utils/agentWebSearch';
import {
  canLocallyConfigureAgent,
  getAgentNotReadyReasonKeys,
  resolveAgentNotReadyHighlight,
  resolveAgentNotReadySection,
  type AgentNotReadyReasonKey,
} from '@/utils/agent-readiness';
import { customAgentStreamMode } from '@/utils/agent-mode';
import { formatLocalizedList } from '@/utils/format-list';
import { kbSatisfiesAgentRequirements, type ScopeCapabilities } from '@/utils/tool-capabilities';
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom';
import { toolboxLocation } from '@/config/toolbox';
import { readLastChatModelID } from './useChatInputModelReasoning';
import type { KnowledgeBaseRecordLike, SharedAgentWrapperLike } from './types';

export type SelectionMode = 'all' | 'selected' | 'none';

export const normalizeSelectionMode = (mode?: string): SelectionMode =>
  mode === 'all' || mode === 'selected' || mode === 'none' ? mode : 'none';

export const toMCPMentionItem = (svc: MCPService): MentionItem => ({
  id: svc.id,
  name: svc.name,
  type: 'mcp',
  description: svc.usage_instructions || svc.description || '',
  toolCount: svc.catalog?.tool_count,
  catalogStale: Boolean(svc.catalog?.stale),
  catalogSynced: Boolean(svc.catalog),
});

export const kbToScopeCaps = (kb?: KnowledgeBaseRecordLike | null): Partial<ScopeCapabilities> => {
  if (kb?.capabilities) {
    return {
      vector: Boolean(kb.capabilities.vector),
      keyword: Boolean(kb.capabilities.keyword),
      wiki: Boolean(kb.capabilities.wiki),
      graph: Boolean(kb.capabilities.graph),
      faq: Boolean(kb.capabilities.faq),
    };
  }
  const s = kb?.indexing_strategy;
  return {
    vector: s ? Boolean(s.vector_enabled) : false,
    keyword: s ? Boolean(s.keyword_enabled) : false,
    wiki: s ? Boolean(s.wiki_enabled) : false,
    graph: s ? Boolean(s.graph_enabled) : false,
    faq: kb?.type === 'faq',
  };
};

export function useChatInputAgentScope(options: {
  props: { embeddedMode: boolean };
  agents: Ref<CustomAgent[]>;
  disabledOwnAgentIds: Ref<string[]>;
  allModels: Ref<ModelConfig[]>;
  webSearchProviders: Ref<WebSearchProviderEntity[]>;
  selectedModelId: Ref<string>;
  agentModeButtonRef: Ref<HTMLElement | undefined>;
  loadChatModels: (force?: boolean) => Promise<void>;
  onAgentSwitchedRefreshMention: () => void;
  onAgentDisallowsImages: () => void;
  onCloseOtherPopups: () => void;
  getLocale: () => string;
  t: (key: string, params?: Record<string, unknown>) => string;
}) {
  const {
    props,
    agents,
    disabledOwnAgentIds,
    allModels,
    webSearchProviders,
    selectedModelId,
    agentModeButtonRef,
    loadChatModels,
    onAgentSwitchedRefreshMention,
    onAgentDisallowsImages,
    onCloseOtherPopups,
    getLocale,
    t,
  } = options;

  const route = useRoute();
  const router = useRouter();
  const settingsStore = useSettingsStore();
  const orgStore = useOrganizationStore();
  const chatResources = useChatResourcesStore();
  const editorResources = useEditorResourcesStore();
  const uiStore = useUIStore();
  const browserConnection = useBrowserConnectionStore();

  const showAgentModeSelector = ref(false);
  const agentModeDropdownStyle = ref<Record<string, string>>({});
  const sharedAgentKbList = ref<KnowledgeBaseRecordLike[]>([]);
  const mcpServices = ref<MCPService[]>([]);

  const selectedAgentId = computed({
    get: () => settingsStore.selectedAgentId || BUILTIN_QUICK_ANSWER_ID,
    set: (val: string) => settingsStore.selectAgent(val),
  });

  const sharedAgentsList = computed(
    () => (orgStore.sharedAgents || []) as unknown as SharedAgentWrapperLike[],
  );

  const selectedAgent = computed<CustomAgent>(() => {
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    if (sourceTenantId && sharedAgentsList.value.length) {
      const shared = sharedAgentsList.value.find(
        (s) => s.agent?.id === selectedAgentId.value && String(s.source_tenant_id) === sourceTenantId,
      );
      if (shared?.agent) return shared.agent;
    }
    const mine = agents.value.find((a) => a.id === selectedAgentId.value);
    if (mine) return mine;
    return {
      id: BUILTIN_QUICK_ANSWER_ID,
      name: t('input.normalMode'),
      is_builtin: true,
      config: { agent_mode: 'quick-answer' as const },
    } as CustomAgent;
  });

  const selectedSharedAgent = computed<SharedAgentWrapperLike | undefined>(() => {
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    if (!sourceTenantId) return undefined;
    return sharedAgentsList.value.find(
      (s) => s.agent?.id === selectedAgentId.value && String(s.source_tenant_id) === sourceTenantId,
    );
  });

  const isCustomAgent = computed(() => {
    const agent = selectedAgent.value;
    return Boolean(agent && !agent.is_builtin);
  });

  const hasAgentConfig = computed(() => {
    const agent = selectedAgent.value;
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    if (agent?.is_builtin && !sourceTenantId) {
      const builtinAgent = agents.value.find((a) => a.id === agent.id);
      return Boolean(builtinAgent?.config);
    }
    return Boolean(agent?.config);
  });

  const currentAgentConfig = computed<Partial<AgentConfig>>(() => {
    const agent = selectedAgent.value;
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    if (agent?.is_builtin && !sourceTenantId) {
      const builtinAgent = agents.value.find((a) => a.id === agent.id);
      return builtinAgent?.config || {};
    }
    return agent?.config || {};
  });

  const agentKnowledgeBases = computed<string[]>(() => {
    if (!hasAgentConfig.value) return [];
    return currentAgentConfig.value?.knowledge_bases || [];
  });

  const agentKBSelectionMode = computed<string | null>(() => {
    if (!hasAgentConfig.value) return null;
    return currentAgentConfig.value?.kb_selection_mode || 'all';
  });

  const isImageUploadEnabledByAgent = computed(() => {
    if (!hasAgentConfig.value) return false;
    return currentAgentConfig.value?.image_upload_enabled === true;
  });

  watch(
    [selectedAgentId, agentKnowledgeBases, agentKBSelectionMode],
    ([newAgentId, newAgentKbs, newKbMode], [oldAgentId]) => {
      if (settingsStore._isApplyingSessionState) return;
      if (newAgentId !== oldAgentId && oldAgentId !== undefined && !props.embeddedMode) {
        if (newKbMode === 'none') {
          settingsStore.selectKnowledgeBases([]);
        } else {
          settingsStore.selectKnowledgeBases(
            newAgentKbs && newAgentKbs.length > 0 ? [...newAgentKbs] : [],
          );
        }
        onAgentSwitchedRefreshMention();
        if (!isImageUploadEnabledByAgent.value) {
          onAgentDisallowsImages();
        }
      }
    },
    { immediate: true },
  );

  let sharedAgentKbReqSeq = 0;
  const invalidateSharedAgentKbRequests = (): void => {
    sharedAgentKbReqSeq += 1;
  };

  watch(
    [selectedAgentId, () => settingsStore.selectedAgentSourceTenantId],
    async ([agentId, sourceTenantId]) => {
      const seq = ++sharedAgentKbReqSeq;
      if (sourceTenantId && agentId) {
        try {
          const list = (await chatResources.ensureAgentKnowledgeBases(
            agentId,
            sourceTenantId,
          )) as KnowledgeBaseRecordLike[];
          if (seq !== sharedAgentKbReqSeq) return;
          sharedAgentKbList.value = list.map((kb) => ({
            id: kb.id,
            name: kb.name,
            type: kb.type || 'document',
            knowledge_count: kb.knowledge_count,
            chunk_count: kb.chunk_count,
          }));
        } catch {
          if (seq === sharedAgentKbReqSeq) sharedAgentKbList.value = [];
        }
      } else {
        sharedAgentKbList.value = [];
      }
    },
    { immediate: true },
  );

  const sharedAgentOrgName = computed(() => {
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    const agentId = selectedAgentId.value;
    if (!sourceTenantId || !agentId || !sharedAgentsList.value.length) return '';
    const shared = sharedAgentsList.value.find(
      (s) => s.agent?.id === agentId && String(s.source_tenant_id) === sourceTenantId,
    );
    return shared?.org_name || shared?.shared_by_username || '';
  });

  const isWebSearchEnabledByAgent = computed(() => {
    if (!hasAgentConfig.value) return null;
    return isAgentWebSearchEnabled(currentAgentConfig.value);
  });

  const isWebSearchDisabledByAgent = computed(
    () => hasAgentConfig.value && isWebSearchEnabledByAgent.value !== true,
  );

  const isKnowledgeBaseLockedByAgent = computed(() => {
    if (!hasAgentConfig.value) return false;
    return agentKBSelectionMode.value === 'none';
  });

  const isKnowledgeBaseDisabledByAgent = computed(() => {
    if (!hasAgentConfig.value) return false;
    return agentKBSelectionMode.value === 'none';
  });

  const agentModelId = computed<string | null>(() => {
    if (!hasAgentConfig.value) return null;
    return currentAgentConfig.value?.model_id || null;
  });

  const agentSupportedFileTypes = computed<string[]>(() => {
    if (!hasAgentConfig.value) return [];
    return currentAgentConfig.value?.supported_file_types || [];
  });

  const agentAllowedTools = computed<string[]>(() => {
    if (!hasAgentConfig.value) return [];
    return currentAgentConfig.value?.allowed_tools || [];
  });

  const agentMCPSelectionMode = computed<SelectionMode>(() => {
    if (!settingsStore.isAgentStreamMode || !hasAgentConfig.value) return 'none';
    return normalizeSelectionMode(currentAgentConfig.value?.mcp_selection_mode);
  });

  const agentMCPServiceIds = computed<string[]>(() => {
    if (agentMCPSelectionMode.value !== 'selected') return [];
    return currentAgentConfig.value?.mcp_services || [];
  });

  const isMCPAllowedByAgent = (service: MCPService): boolean => {
    if (!settingsStore.isAgentStreamMode || !service.enabled) return false;
    const mode = agentMCPSelectionMode.value;
    if (mode === 'none') return false;
    if (mode === 'selected') return agentMCPServiceIds.value.includes(service.id);
    return true;
  };

  const agentSkillsSelectionMode = computed<SelectionMode>(() => {
    if (!settingsStore.isAgentStreamMode || !hasAgentConfig.value) return 'none';
    return normalizeSelectionMode(currentAgentConfig.value?.skills_selection_mode);
  });

  const agentSelectedSkills = computed<string[]>(() => {
    if (agentSkillsSelectionMode.value !== 'selected') return [];
    return currentAgentConfig.value?.selected_skills || [];
  });

  const isSkillAllowedByAgent = (skillName: string): boolean => {
    if (!settingsStore.isAgentStreamMode || !editorResources.skillsAvailable) return false;
    const mode = agentSkillsSelectionMode.value;
    if (mode === 'none') return false;
    if (mode === 'selected') return agentSelectedSkills.value.includes(skillName);
    return true;
  };

  watch(
    [selectedAgentId, agentMCPSelectionMode, agentSkillsSelectionMode],
    ([newAgentId], [oldAgentId]) => {
      if (props.embeddedMode || settingsStore._isApplyingSessionState) return;
      if (newAgentId === oldAgentId || oldAgentId === undefined) return;

      const mcpMode = agentMCPSelectionMode.value;
      if (mcpMode === 'none') {
        settingsStore.settings.selectedMCPServices = [];
      } else if (mcpMode === 'selected') {
        const allowed = new Set(agentMCPServiceIds.value);
        settingsStore.settings.selectedMCPServices = (
          settingsStore.settings.selectedMCPServices || []
        ).filter((id) => allowed.has(id));
      }

      const skillsMode = agentSkillsSelectionMode.value;
      if (skillsMode === 'none') {
        settingsStore.settings.selectedSkills = [];
      } else if (skillsMode === 'selected') {
        const allowed = new Set(agentSelectedSkills.value);
        settingsStore.settings.selectedSkills = (
          settingsStore.settings.selectedSkills || []
        ).filter((name) => allowed.has(name));
      }
    },
  );

  const agentMode = computed(() => {
    if (!hasAgentConfig.value) return '';
    return currentAgentConfig.value?.agent_mode || '';
  });

  const isKbCompatibleWithAgent = (kb: KnowledgeBaseRecordLike): boolean => {
    if (!hasAgentConfig.value) return true;
    if (agentKBSelectionMode.value !== 'all') return true;
    return kbSatisfiesAgentRequirements(kbToScopeCaps(kb), agentMode.value, agentAllowedTools.value);
  };

  const isWebSearchConfigured = computed(() => {
    if (hasAgentConfig.value) {
      return isAgentWebSearchReady(
        currentAgentConfig.value,
        webSearchProviders.value,
        selectedSharedAgent.value?.web_search_ready,
      );
    }
    return isTenantWebSearchReady(webSearchProviders.value);
  });

  const isWebSearchReadinessKnown = computed(
    () => !settingsStore.selectedAgentSourceTenantId || selectedSharedAgent.value !== undefined,
  );

  const showWebSearchButton = computed(() => {
    if (hasAgentConfig.value && settingsStore.selectedAgentSourceTenantId && !isWebSearchReadinessKnown.value) {
      return false;
    }
    if (!hasAgentConfig.value) {
      return isTenantWebSearchReady(webSearchProviders.value);
    }
    return isAgentWebSearchReady(
      currentAgentConfig.value,
      webSearchProviders.value,
      selectedSharedAgent.value?.web_search_ready,
    );
  });
  const showImageUploadButton = computed(() => isImageUploadEnabledByAgent.value);

  const currentAgentScope = computed(() => {
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    if (!sourceTenantId || !selectedAgentId.value) return undefined;
    return { agentId: selectedAgentId.value, sourceTenantId };
  });

  const agentScopeKey = (scope?: { agentId: string; sourceTenantId: string | number }): string =>
    scope ? `${scope.sourceTenantId}:${scope.agentId}` : '';

  let mcpServicesRequestKey = '';

  const loadMCPServices = async (): Promise<void> => {
    const scope = currentAgentScope.value;
    const requestKey = agentScopeKey(scope);
    mcpServicesRequestKey = requestKey;
    try {
      const list = await listMCPServices(scope);
      if (mcpServicesRequestKey !== requestKey) return;
      mcpServices.value = list;
    } catch (error) {
      console.error('Failed to load MCP services:', error);
      if (mcpServicesRequestKey !== requestKey) return;
      mcpServices.value = [];
    }
  };

  watch(currentAgentScope, () => {
    void loadMCPServices();
  });

  const browserSourceUnavailableHint = computed(() => {
    if (!browserConnection.enabled) return 'localBrowser.unavailable';
    if (browserConnection.device) return 'localBrowser.reconnectHint';
    return 'localBrowser.settingsHint';
  });

  const openBrowserConnectionSettings = (): void => {
    void router.push(toolboxLocation('browserconnection'));
  };

  const toggleBrowserSource = (): void => {
    onCloseOtherPopups();
    if (browserConnection.knownOffline) {
      openBrowserConnectionSettings();
      return;
    }
    settingsStore.toggleLocalBrowser(!settingsStore.isLocalBrowserEnabled);
  };

  const loadWebSearchConfig = async (force = false): Promise<void> => {
    try {
      await chatResources.ensureWebSearchProviders(force);
      if (isWebSearchReadinessKnown.value && !isWebSearchConfigured.value && settingsStore.isWebSearchEnabled) {
        settingsStore.toggleWebSearch(false);
      }
    } catch (error) {
      console.error('Failed to load web search config:', error);
      chatResources.invalidate('webSearchProviders');
      if (!settingsStore.selectedAgentSourceTenantId && settingsStore.isWebSearchEnabled) {
        settingsStore.toggleWebSearch(false);
      }
    }
  };

  const syncSelectedAgentModeFromConfig = () => {
    if (props.embeddedMode) return;
    const agent = selectedAgent.value;
    if (!agent || agent.id !== selectedAgentId.value) return;
    const shouldEnable = customAgentStreamMode(selectedAgentId.value, agent.config);
    if (shouldEnable === null || shouldEnable === settingsStore.isAgentEnabled) return;
    settingsStore.toggleAgent(shouldEnable);
  };

  const loadAgents = async (force = false) => {
    try {
      await chatResources.ensureAgents(force);
      ensureSelectedAgentNotDisabled();
      syncSelectedAgentModeFromConfig();
    } catch (error) {
      console.error('Failed to load agents:', error);
    }
  };

  watch(
    () => [selectedAgentId.value, currentAgentConfig.value?.agent_mode, settingsStore.isAgentEnabled] as const,
    () => syncSelectedAgentModeFromConfig(),
  );

  // 默认选中的 builtin（builtin-quick-answer）也可能被当前空间管理员停用。
  const ensureSelectedAgentNotDisabled = (): void => {
    if (settingsStore.selectedAgentSourceTenantId) return;
    const currentId = settingsStore.selectedAgentId || BUILTIN_QUICK_ANSWER_ID;
    if (!disabledOwnAgentIds.value.includes(currentId)) return;

    const isEnabled = (id: string) =>
      agents.value.some((a) => a.id === id) && !disabledOwnAgentIds.value.includes(id);

    let fallback: CustomAgent | undefined;
    if (isEnabled(BUILTIN_SMART_REASONING_ID)) {
      fallback = agents.value.find((a) => a.id === BUILTIN_SMART_REASONING_ID);
    } else if (isEnabled(BUILTIN_QUICK_ANSWER_ID)) {
      fallback = agents.value.find((a) => a.id === BUILTIN_QUICK_ANSWER_ID);
    } else {
      fallback = agents.value.find((a) => !disabledOwnAgentIds.value.includes(a.id));
    }
    if (!fallback) return;

    settingsStore.selectAgent(fallback.id);
    if (fallback.id !== BUILTIN_QUICK_ANSWER_ID && fallback.id !== BUILTIN_SMART_REASONING_ID) {
      settingsStore.toggleAgent(fallback.config?.agent_mode === 'smart-reasoning');
    }
  };

  const enabledAgents = computed(() =>
    agents.value.filter((a) => !disabledOwnAgentIds.value.includes(a.id)),
  );

  const updateAgentModeDropdownPosition = (): void => {
    const anchor = agentModeButtonRef.value;
    if (!anchor) {
      agentModeDropdownStyle.value = {
        position: 'fixed',
        top: '50%',
        left: '50%',
        transform: 'translate(-50%, -50%)',
      };
      return;
    }

    const zoom = getRootZoom();
    const rect = rectToCssPx(anchor.getBoundingClientRect(), zoom);
    const dropdownWidth = 200;
    const offsetY = 8;
    const { width: vw, height: vh } = cssViewportSize(zoom);

    let left = Math.floor(rect.left);
    const minLeft = 16;
    const maxLeft = Math.max(16, vw - dropdownWidth - 16);
    left = Math.max(minLeft, Math.min(maxLeft, left));

    const preferredDropdownHeight = 140;
    const minDropdownHeight = 100;
    const topMargin = 20;
    const spaceBelow = vh - rect.bottom;
    const spaceAbove = rect.top;

    if (spaceBelow >= minDropdownHeight + offsetY) {
      const actualHeight = Math.min(preferredDropdownHeight, spaceBelow - offsetY - 16);
      const top = Math.floor(rect.bottom + offsetY);
      agentModeDropdownStyle.value = {
        position: 'fixed !important',
        width: `${dropdownWidth}px`,
        left: `${left}px`,
        top: `${top}px`,
        maxHeight: `${actualHeight}px`,
        transform: 'none !important',
        margin: '0 !important',
        padding: '0 !important',
      };
    } else {
      const availableHeight = spaceAbove - offsetY - topMargin;
      const actualHeight =
        availableHeight >= preferredDropdownHeight
          ? preferredDropdownHeight
          : Math.max(minDropdownHeight, availableHeight);
      const bottom = vh - rect.top + offsetY;
      agentModeDropdownStyle.value = {
        position: 'fixed !important',
        width: `${dropdownWidth}px`,
        left: `${left}px`,
        bottom: `${bottom}px`,
        maxHeight: `${actualHeight}px`,
        transform: 'none !important',
        margin: '0 !important',
        padding: '0 !important',
      };
    }
  };

  const toggleAgentModeSelector = (): void => {
    onCloseOtherPopups();
    showAgentModeSelector.value = !showAgentModeSelector.value;
    if (showAgentModeSelector.value) {
      if (!chatResources.isLoaded('agents')) {
        void loadAgents(true);
      }
      void nextTick(() => {
        updateAgentModeDropdownPosition();
        requestAnimationFrame(() => {
          updateAgentModeDropdownPosition();
          setTimeout(() => {
            updateAgentModeDropdownPosition();
          }, 50);
        });
      });
    }
  };

  const closeAgentModeSelector = (): void => {
    showAgentModeSelector.value = false;
  };

  const formatAgentNotReadyReasons = (
    reasonKeys: AgentNotReadyReasonKey[],
    isBuiltin: boolean,
  ): string[] =>
    reasonKeys.map((key) => {
      if (key === 'summary_model') {
        return isBuiltin
          ? t('input.agentMissingSummaryModel')
          : t('input.customAgentMissingSummaryModel');
      }
      if (key === 'rerank_model') {
        return isBuiltin
          ? t('input.agentMissingRerankModel')
          : t('input.customAgentMissingRerankModel');
      }
      return t('input.agentMissingAllowedTools');
    });

  const collectAgentNotReadyReasons = (
    agent: CustomAgent,
    isAgentMode: boolean,
    sourceTenantId?: string,
  ): { keys: AgentNotReadyReasonKey[]; labels: string[] } => {
    const isSharedAgent = Boolean(sourceTenantId);
    const keys = getAgentNotReadyReasonKeys(agent.config, allModels.value, {
      isAgentMode,
      isSharedAgent,
    });
    return {
      keys,
      labels: formatAgentNotReadyReasons(keys, agent.is_builtin),
    };
  };

  const goToAgentEditor = (
    agent: CustomAgent,
    section = 'model',
    highlight?: AgentNotReadyReasonKey,
    sourceTenantId?: string,
  ): void => {
    void router.push({
      path: '/platform/agents',
      query: {
        edit: agent.id,
        section,
        ...(highlight ? { highlight } : {}),
        ...(sourceTenantId ? { sourceTenantId } : {}),
      },
    });
  };

  const showAgentNotReadyMessage = (
    agent: CustomAgent,
    reasons: string[],
    reasonKeys?: AgentNotReadyReasonKey[],
    sourceTenantId?: string,
  ): void => {
    const reasonsText = formatLocalizedList(reasons, getLocale());
    const isRemoteShared = !canLocallyConfigureAgent(sourceTenantId);

    const messageContent = h(
      'div',
      { style: 'display: flex; flex-direction: column; gap: 8px; max-width: 320px;' },
      [
        h(
          'span',
          { style: 'color: var(--td-text-color-primary); line-height: 1.5;' },
          isRemoteShared
            ? t('input.sharedAgentNotReadyDetail', { agentName: agent.name, reasons: reasonsText })
            : t('input.agentNotReadyDetail', { agentName: agent.name, reasons: reasonsText }),
        ),
        ...(isRemoteShared
          ? []
          : [
              h(
                'a',
                {
                  href: '#',
                  onClick: (e: Event) => {
                    e.preventDefault();
                    const section = resolveAgentNotReadySection(reasonKeys || ['summary_model']);
                    const highlight = resolveAgentNotReadyHighlight(reasonKeys || ['summary_model']);
                    goToAgentEditor(agent, section, highlight, sourceTenantId);
                  },
                  style:
                    'color: var(--td-brand-color); text-decoration: none; font-weight: 500; cursor: pointer; align-self: flex-start;',
                  onMouseenter: (e: Event) => {
                    (e.target as HTMLElement).style.textDecoration = 'underline';
                  },
                  onMouseleave: (e: Event) => {
                    (e.target as HTMLElement).style.textDecoration = 'none';
                  },
                },
                t('input.goToAgentEditor'),
              ),
            ]),
      ],
    );

    MessagePlugin.warning({
      content: () => messageContent,
      duration: 5000,
    });
  };

  const handleAgentNotReady = (
    agent: CustomAgent,
    labels: string[],
    keys: AgentNotReadyReasonKey[],
    sourceTenantId?: string,
  ): void => {
    showAgentNotReadyMessage(agent, labels, keys, sourceTenantId);
  };

  const handleSelectAgent = async (agent: CustomAgent, sourceTenantId?: string) => {
    if (!chatResources.isLoaded('models')) {
      await loadChatModels();
    }

    const isAgentType = agent.config?.agent_mode === 'smart-reasoning';
    const actualAgent =
      agent.is_builtin && !sourceTenantId
        ? agents.value.find((a) => a.id === agent.id) || agent
        : agent;

    const { keys: notReadyKeys, labels: notReadyReasons } = collectAgentNotReadyReasons(
      actualAgent,
      isAgentType,
      sourceTenantId,
    );

    if (notReadyReasons.length > 0) {
      showAgentModeSelector.value = false;
      showAgentNotReadyMessage(actualAgent, notReadyReasons, notReadyKeys, sourceTenantId);
      return;
    }

    settingsStore.selectAgent(agent.id, sourceTenantId);
    settingsStore.toggleAgent(Boolean(isAgentType));

    const agentModel = agent.config?.model_id;
    if (agentModel && agentModel.trim() !== '') {
      selectedModelId.value = agentModel;
    } else {
      const lastPick = readLastChatModelID();
      if (lastPick) {
        selectedModelId.value = lastPick;
      }
    }

    showAgentModeSelector.value = false;

    const isModeBuiltin =
      agent.id === BUILTIN_QUICK_ANSWER_ID || agent.id === BUILTIN_SMART_REASONING_ID;
    const message = isModeBuiltin
      ? isAgentType
        ? t('input.messages.agentSwitchedOn')
        : t('input.messages.agentSwitchedOff')
      : t('input.messages.agentSelected', { name: agent.name });
    MessagePlugin.success(message);
  };

  const handleGoToAgentSettings = (section?: string): void => {
    const agent = selectedAgent.value;
    if (!agent) {
      void router.push('/platform/agents');
      return;
    }
    const query: Record<string, string> = { edit: agent.id };
    if (section) {
      query.section = section;
    }
    void router.push({ path: '/platform/agents', query });
  };

  const handleGoToWebSearchSettings = (): void => {
    uiStore.openSettings('websearch');
    if (route.path !== '/platform/settings') {
      void router.push('/platform/settings');
    }
  };

  const handleGoToWebSearchConfig = (): void => {
    if (hasAgentConfig.value && selectedAgent.value) {
      handleGoToAgentSettings('websearch');
      return;
    }
    handleGoToWebSearchSettings();
  };

  const toggleWebSearch = (): void => {
    onCloseOtherPopups();

    if (isWebSearchDisabledByAgent.value) {
      MessagePlugin.warning(t('input.webSearchDisabledByAgent'));
      return;
    }

    if (!isWebSearchConfigured.value) {
      const messageContent = h(
        'div',
        { style: 'display: flex; flex-direction: column; gap: 6px; max-width: 280px;' },
        [
          h(
            'span',
            { style: 'color: var(--td-text-color-primary); line-height: 1.5;' },
            t('input.messages.webSearchNotConfigured'),
          ),
          h(
            'a',
            {
              href: '#',
              onClick: (e: Event) => {
                e.preventDefault();
                handleGoToWebSearchConfig();
              },
              style:
                'color: var(--td-brand-color); text-decoration: none; font-weight: 500; cursor: pointer; align-self: flex-start;',
              onMouseenter: (e: Event) => {
                (e.target as HTMLElement).style.textDecoration = 'underline';
              },
              onMouseleave: (e: Event) => {
                (e.target as HTMLElement).style.textDecoration = 'none';
              },
            },
            t('input.goToAgentSettings'),
          ),
        ],
      );
      MessagePlugin.warning({
        content: () => messageContent,
        duration: 5000,
      });
      return;
    }

    const newValue = !settingsStore.isWebSearchEnabled;
    settingsStore.toggleWebSearch(newValue);
    MessagePlugin.success(
      newValue ? t('input.messages.webSearchEnabled') : t('input.messages.webSearchDisabled'),
    );
  };

  return {
    showAgentModeSelector,
    agentModeDropdownStyle,
    selectedAgentId,
    selectedAgent,
    isCustomAgent,
    hasAgentConfig,
    currentAgentConfig,
    agentKnowledgeBases,
    agentKBSelectionMode,
    sharedAgentKbList,
    sharedAgentOrgName,
    isKnowledgeBaseLockedByAgent,
    isKnowledgeBaseDisabledByAgent,
    agentModelId,
    agentSupportedFileTypes,
    agentAllowedTools,
    agentMCPSelectionMode,
    isMCPAllowedByAgent,
    agentSkillsSelectionMode,
    isSkillAllowedByAgent,
    agentMode,
    isKbCompatibleWithAgent,
    isImageUploadEnabledByAgent,
    isWebSearchConfigured,
    showWebSearchButton,
    showImageUploadButton,
    enabledAgents,
    currentAgentScope,
    mcpServices,
    browserSourceUnavailableHint,
    openBrowserConnectionSettings,
    toggleBrowserSource,
    loadMCPServices,
    loadWebSearchConfig,
    loadAgents,
    updateAgentModeDropdownPosition,
    toggleAgentModeSelector,
    closeAgentModeSelector,
    collectAgentNotReadyReasons,
    showAgentNotReadyMessage,
    handleAgentNotReady,
    handleSelectAgent,
    handleGoToAgentSettings,
    handleGoToWebSearchConfig,
    toggleWebSearch,
    invalidateSharedAgentKbRequests,
  };
}
