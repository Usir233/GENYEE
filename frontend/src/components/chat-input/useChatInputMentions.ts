import { computed, nextTick, ref, watch, type ComputedRef, type Ref } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  batchQueryKnowledge,
  listKnowledgeTags,
  searchKnowledge,
} from '@/api/knowledge-base';
import type { AgentConfig } from '@/api/agent';
import type { MCPService } from '@/api/mcp-service';
import { useAuthStore } from '@/stores/auth';
import { useSettingsStore } from '@/stores/settings';
import { useOrganizationStore } from '@/stores/organization';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useEditorResourcesStore } from '@/stores/editorResources';
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities';
import type { BrowserKnowledgeBase } from '@/utils/mentionFileBrowser';
import { getCaretCoordinates } from '@/utils/caret';
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom';
import { hostSkillsOnly, mentionSkillTargetId } from '@/utils/skillTarget';
import { deriveKbFilterForAgent, toolsConsumeFiles } from '@/utils/tool-capabilities';
import type { MentionItem, MentionItemType } from '@/types/mention';
import { toMCPMentionItem, type SelectionMode } from './useChatInputAgentScope';
import type {
  BatchKnowledgeEnvelope,
  KnowledgeBaseRecordLike,
  KnowledgeTagApiItem,
  SearchKnowledgeEnvelope,
  SelectedChipDisplayItem,
  SelectedTagRecord,
  SharedKnowledgeBaseWrapperLike,
} from './types';

const MENTION_PAGE_SIZE = 20;

export function useChatInputMentions(options: {
  props: { embeddedMode: boolean; sessionId?: string };
  query: Ref<string>;
  selectedAgentId: ComputedRef<string>;
  hasAgentConfig: ComputedRef<boolean>;
  currentAgentConfig: ComputedRef<Partial<AgentConfig>>;
  agentKnowledgeBases: ComputedRef<string[]>;
  agentKBSelectionMode: ComputedRef<string | null>;
  agentSupportedFileTypes: ComputedRef<string[]>;
  agentAllowedTools: ComputedRef<string[]>;
  agentMode: ComputedRef<string>;
  isKnowledgeBaseLockedByAgent: ComputedRef<boolean>;
  isKnowledgeBaseDisabledByAgent: ComputedRef<boolean>;
  agentMCPSelectionMode: ComputedRef<SelectionMode>;
  isMCPAllowedByAgent: (service: MCPService) => boolean;
  agentSkillsSelectionMode: ComputedRef<SelectionMode>;
  isSkillAllowedByAgent: (skillName: string) => boolean;
  isKbCompatibleWithAgent: (kb: KnowledgeBaseRecordLike) => boolean;
  sharedAgentKbList: Ref<KnowledgeBaseRecordLike[]>;
  sharedAgentOrgName: ComputedRef<string>;
  currentAgentScope: ComputedRef<{ agentId: string; sourceTenantId: string } | undefined>;
  mcpServices: Ref<MCPService[]>;
  getTextareaEl: () => HTMLTextAreaElement | null;
  focusInput: () => Promise<void>;
  onCloseOtherPopups: () => void;
  t: (key: string, params?: Record<string, unknown>) => string;
}) {
  const {
    props,
    query,
    selectedAgentId,
    hasAgentConfig,
    currentAgentConfig,
    agentKnowledgeBases,
    agentKBSelectionMode,
    agentSupportedFileTypes,
    agentAllowedTools,
    agentMode,
    isKnowledgeBaseLockedByAgent,
    isKnowledgeBaseDisabledByAgent,
    agentMCPSelectionMode,
    isMCPAllowedByAgent,
    agentSkillsSelectionMode,
    isSkillAllowedByAgent,
    isKbCompatibleWithAgent,
    sharedAgentKbList,
    sharedAgentOrgName,
    currentAgentScope,
    mcpServices,
    getTextareaEl,
    focusInput,
    onCloseOtherPopups,
    t,
  } = options;

  const mentionAuth = useAuthStore();
  const settingsStore = useSettingsStore();
  const orgStore = useOrganizationStore();
  const chatResources = useChatResourcesStore();
  const editorResources = useEditorResourcesStore();
  const deploymentCapabilities = useDeploymentCapabilitiesStore();

  const showMention = ref(false);
  const mentionQuery = ref('');
  const mentionItems = ref<MentionItem[]>([]);
  const fileIdToKbId = ref<Record<string, string>>({});
  const fileList = ref<Array<{ id: string; name: string }>>([]);
  const mentionActiveIndex = ref(0);
  const mentionStyle = ref<Record<string, string>>({});
  const mentionStartPos = ref(0);
  const isComposing = ref(false);
  const isMentionTriggeredByButton = ref(false);
  const mentionHasMore = ref(false);
  const mentionGroupCounts = ref<Partial<Record<MentionItemType, number>>>({});
  const mentionAllowedKbIds = ref<Set<string> | null>(null);
  const mentionBrowserKbs = ref<BrowserKnowledgeBase[]>([]);
  const mentionLoading = ref(false);
  const mentionOffset = ref(0);

  const selectedKbIds = computed(() => settingsStore.settings.selectedKnowledgeBases || []);
  const selectedFileIds = computed(() => settingsStore.settings.selectedFiles || []);
  const selectedTags = computed(
    () => (settingsStore.settings.selectedTags || []) as SelectedTagRecord[],
  );
  const selectedMCPServiceIds = computed(() => settingsStore.settings.selectedMCPServices || []);
  const selectedSkillNames = computed(() => settingsStore.settings.selectedSkills || []);

  const knowledgeBases = computed(
    () => chatResources.validKnowledgeBases as unknown as KnowledgeBaseRecordLike[],
  );
  const sharedKbWrappers = computed(
    () => (orgStore.sharedKnowledgeBases || []) as unknown as SharedKnowledgeBaseWrapperLike[],
  );

  const isMentionDisabled = computed(() => {
    if (props.embeddedMode) return true;
    if (settingsStore.isAgentStreamMode && isKnowledgeBaseDisabledByAgent.value) {
      return agentMCPSelectionMode.value === 'none' && agentSkillsSelectionMode.value === 'none';
    }
    return isKnowledgeBaseLockedByAgent.value && !settingsStore.isAgentStreamMode;
  });

  const mentionEmptyHint = computed(() => {
    if (mentionQuery.value) return '';
    if (!hasAgentConfig.value) return '';
    if (agentKBSelectionMode.value !== 'all') return '';
    if (mentionItems.value.length !== 0) return '';
    const filter = deriveKbFilterForAgent(agentMode.value, agentAllowedTools.value);
    if (!filter) return '';
    return t('mentionDetail.noCompatibleKbForAgent');
  });

  const selectedKbs = computed<KnowledgeBaseRecordLike[]>(() => {
    const own = knowledgeBases.value.filter((kb) => selectedKbIds.value.includes(kb.id));
    const sharedMapped = sharedKbWrappers.value
      .filter((s) => s.knowledge_base != null && selectedKbIds.value.includes(s.knowledge_base.id))
      .map((s) => ({
        id: s.knowledge_base!.id,
        name: s.knowledge_base!.name,
        type: s.knowledge_base!.type || 'document',
        knowledge_count: s.knowledge_base!.knowledge_count,
        chunk_count: s.knowledge_base!.chunk_count,
        org_name: s.org_name || '',
      }));
    const ownIds = new Set(own.map((kb) => kb.id));
    const sharedOnly = sharedMapped.filter((kb) => !ownIds.has(kb.id));
    const sharedOnlyIds = new Set(sharedOnly.map((kb) => kb.id));
    const agentOrg = sharedAgentOrgName.value;
    const sharedFromAgent = (sharedAgentKbList.value || [])
      .filter(
        (kb) =>
          selectedKbIds.value.includes(kb.id) && !ownIds.has(kb.id) && !sharedOnlyIds.has(kb.id),
      )
      .map((kb) => ({
        id: kb.id,
        name: kb.name,
        type: kb.type || 'document',
        knowledge_count: kb.knowledge_count,
        chunk_count: kb.chunk_count,
        org_name: agentOrg || '',
      }));
    return [...own, ...sharedOnly, ...sharedFromAgent];
  });

  const selectedFiles = computed(() =>
    selectedFileIds.value.map((id: string) => {
      const found = fileList.value.find((f) => f.id === id);
      return found || { id, name: 'Loading...' };
    }),
  );

  const skillMentionItems = computed<MentionItem[]>(() =>
    selectedSkillNames.value
      .filter((name: string) => isSkillAllowedByAgent(name))
      .map((name: string) => {
        const skill = editorResources.skills.find((s) => s.name === name);
        return {
          id: name,
          name: skill?.name || name,
          type: 'skill' as const,
          skillName: name,
          description: skill?.description || '',
        };
      }),
  );

  const selectedMCPItems = computed<MentionItem[]>(() =>
    selectedMCPServiceIds.value
      .map((id: string) => mcpServices.value.find((service) => service.id === id))
      .filter((svc): svc is MCPService => svc !== undefined && isMCPAllowedByAgent(svc))
      .map(toMCPMentionItem),
  );

  const allSelectedItems = computed<SelectedChipDisplayItem[]>(() => {
    const agentKbIds = agentKnowledgeBases.value;

    const allKbs: SelectedChipDisplayItem[] = selectedKbs.value.map((kb) => ({
      ...kb,
      type: 'kb' as const,
      kbType: kb.type === 'faq' ? 'faq' : 'document',
      isAgentConfigured: agentKbIds.includes(kb.id),
    }));

    const sharedKbOrgMap: Record<string, string> = {};
    sharedKbWrappers.value.forEach((s) => {
      if (s.knowledge_base?.id != null && s.org_name) {
        sharedKbOrgMap[String(s.knowledge_base.id)] = s.org_name;
      }
    });
    if (sharedAgentOrgName.value) {
      (sharedAgentKbList.value || []).forEach((kb) => {
        sharedKbOrgMap[String(kb.id)] = sharedAgentOrgName.value;
      });
    }
    const files: SelectedChipDisplayItem[] = selectedFiles.value.map((f) => {
      const kbId = fileIdToKbId.value[f.id];
      const org_name = kbId ? sharedKbOrgMap[String(kbId)] || '' : '';
      return {
        ...f,
        type: 'file' as const,
        isAgentConfigured: false,
        org_name,
      };
    });

    const agentConfiguredKbs = allKbs.filter((kb) => kb.isAgentConfigured);
    const userSelectedKbs = allKbs.filter((kb) => !kb.isAgentConfigured);
    const tags: SelectedChipDisplayItem[] = selectedTags.value.map((tag) => ({
      id: tag.id,
      name: tag.name,
      type: 'tag' as const,
      kbId: tag.kbId,
      kbName: tag.kbName,
      description: tag.kbName || '',
      isAgentConfigured: false,
    }));

    return [
      ...agentConfiguredKbs,
      ...userSelectedKbs,
      ...files,
      ...tags,
      ...selectedMCPItems.value,
      ...skillMentionItems.value,
    ];
  });

  const removeSelectedItem = (item: MentionItem): void => {
    if (item.type === 'kb') {
      settingsStore.removeKnowledgeBase(item.id);
    } else if (item.type === 'file') {
      settingsStore.removeFile(item.id);
      delete fileIdToKbId.value[item.id];
    } else if (item.type === 'tag') {
      settingsStore.removeTag(item.id, item.kbId);
    } else if (item.type === 'mcp') {
      settingsStore.removeMCPService(item.id);
    } else if (item.type === 'skill') {
      settingsStore.removeSkill(item.skillName || item.id);
    }
  };

  const loadKnowledgeBases = async (force = false): Promise<void> => {
    try {
      await chatResources.ensureKnowledgeBases(force);
      const validKbs = knowledgeBases.value;

      const validKbIds = new Set(validKbs.map((kb) => kb.id));
      const sharedKbIds = new Set(
        sharedKbWrappers.value
          .map((s) => s.knowledge_base?.id)
          .filter((id): id is string => Boolean(id)),
      );
      let sharedAgentKbIdSet = new Set<string>();
      const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
      const agentId = settingsStore.selectedAgentId;
      if (sourceTenantId && agentId) {
        try {
          const list = (await chatResources.ensureAgentKnowledgeBases(
            agentId,
            sourceTenantId,
            force,
          )) as KnowledgeBaseRecordLike[];
          list.forEach((kb) => kb?.id && sharedAgentKbIdSet.add(kb.id));
        } catch {
          sharedAgentKbIdSet = new Set();
        }
      }
      const currentSelectedIds = settingsStore.settings.selectedKnowledgeBases || [];
      const validSelectedIds = currentSelectedIds.filter(
        (id: string) => validKbIds.has(id) || sharedKbIds.has(id) || sharedAgentKbIdSet.has(id),
      );

      if (validSelectedIds.length !== currentSelectedIds.length) {
        settingsStore.selectKnowledgeBases(validSelectedIds);
      }
    } catch (error) {
      console.error('Failed to load knowledge bases:', error);
    }
  };

  const loadFiles = async (): Promise<void> => {
    const ids = selectedFileIds.value;
    if (ids.length === 0) return;

    const missingIds = ids.filter((id: string) => !fileList.value.find((f) => f.id === id));
    if (missingIds.length === 0) return;

    try {
      const byKbId = new Map<string, string[]>();
      const noKbId: string[] = [];
      missingIds.forEach((id: string) => {
        const kbId = fileIdToKbId.value[id];
        if (kbId) {
          if (!byKbId.has(kbId)) byKbId.set(kbId, []);
          byKbId.get(kbId)!.push(id);
        } else {
          noKbId.push(id);
        }
      });

      const allNewFiles: Array<{ id: string; name: string }> = [];
      const agentIdForBatch = settingsStore.selectedAgentSourceTenantId
        ? settingsStore.selectedAgentId
        : undefined;
      const runBatch = async (batchIds: string[], kbId?: string, agentId?: string) => {
        const params = new URLSearchParams();
        batchIds.forEach((id: string) => params.append('ids', id));
        const sourceTenantId = agentId
          ? settingsStore.selectedAgentSourceTenantId ?? undefined
          : undefined;
        const res = (await batchQueryKnowledge(
          params.toString(),
          kbId,
          agentId,
          sourceTenantId,
        )) as BatchKnowledgeEnvelope;
        if (res.data && Array.isArray(res.data)) {
          res.data.forEach((f) =>
            allNewFiles.push({ id: f.id, name: f.title || f.file_name || f.id }),
          );
        }
      };

      for (const [kbId, batchIds] of byKbId) {
        await runBatch(batchIds, kbId);
      }
      if (noKbId.length > 0) {
        await runBatch(noKbId, undefined, agentIdForBatch);
      }
      if (allNewFiles.length > 0) {
        fileList.value = [...fileList.value, ...allNewFiles];
      }
    } catch (e) {
      console.error('Failed to load files', e);
    }
  };

  watch(
    selectedFileIds,
    () => {
      void loadFiles();
    },
    { immediate: true },
  );

  let lastMentionQuery = '';
  let mentionLoadSeq = 0;
  let mentionSearchTimer: ReturnType<typeof setTimeout> | null = null;

  const mentionBrowserContext = computed(() => ({
    key: JSON.stringify([
      mentionAuth.user?.id,
      mentionAuth.selectedTenantId,
      mentionAuth.tenant?.id,
      props.sessionId,
      selectedAgentId.value,
      settingsStore.selectedAgentSourceTenantId,
      agentKBSelectionMode.value,
      agentKnowledgeBases.value.toSorted(),
      agentAllowedTools.value.toSorted(),
      agentSupportedFileTypes.value.toSorted(),
    ]),
    agent_id: settingsStore.selectedAgentSourceTenantId
      ? selectedAgentId.value || undefined
      : undefined,
    agent_source_tenant_id: settingsStore.selectedAgentSourceTenantId || undefined,
  }));

  watch(
    () => mentionBrowserContext.value.key,
    () => {
      showMention.value = false;
      ++mentionLoadSeq;
      mentionBrowserKbs.value = [];
      mentionItems.value = [];
      mentionLoading.value = false;
    },
    { flush: 'sync' },
  );

  watch(
    showMention,
    (visible) => {
      if (!visible) {
        ++mentionLoadSeq;
        if (mentionSearchTimer) clearTimeout(mentionSearchTimer);
        mentionSearchTimer = null;
        mentionBrowserKbs.value = [];
        mentionLoading.value = false;
      }
    },
    { flush: 'sync' },
  );

  const loadMentionItems = async (q: string, resetIndex = true, append = false) => {
    const seq = append ? mentionLoadSeq : ++mentionLoadSeq;
    const isStale = () => seq !== mentionLoadSeq;
    const nextGroupCounts: Partial<Record<MentionItemType, number>> = append
      ? mentionGroupCounts.value
      : {};
    if (!append) {
      mentionOffset.value = 0;
    }

    let kbItems: MentionItem[] = [];
    let tagItems: MentionItem[] = [];
    let mcpItems: MentionItem[] = [];
    let skillItems: MentionItem[] = [];
    if (!append) {
      let availableKbs: KnowledgeBaseRecordLike[];
      const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
      const agentId = selectedAgentId.value;
      if (sourceTenantId && agentId) {
        try {
          const list = (await chatResources.ensureAgentKnowledgeBases(
            agentId,
            sourceTenantId,
          )) as KnowledgeBaseRecordLike[];
          if (isStale()) return;
          const orgLabel = sharedAgentOrgName.value || '';
          availableKbs = list.map((kb) => ({
            id: kb.id,
            name: kb.name,
            type: kb.type || 'document',
            knowledge_count: kb.knowledge_count,
            chunk_count: kb.chunk_count,
            org_name: orgLabel,
            capabilities: kb.capabilities,
            indexing_strategy: kb.indexing_strategy,
          }));
          sharedAgentKbList.value = list.map((kb) => ({
            id: kb.id,
            name: kb.name,
            type: kb.type || 'document',
            knowledge_count: kb.knowledge_count,
            chunk_count: kb.chunk_count,
          }));
        } catch (e) {
          if (isStale()) return;
          console.error('[Mention] listKnowledgeBases(agent_id) error:', e);
          availableKbs = [];
          sharedAgentKbList.value = [];
        }
      } else {
        sharedAgentKbList.value = [];
        availableKbs = [...knowledgeBases.value];
        const sharedList = (orgStore.sharedKnowledgeBases || []) as unknown as SharedKnowledgeBaseWrapperLike[];
        const sharedKbsForMention = sharedList
          .filter((s) => s.knowledge_base != null)
          .map((s) => ({
            id: s.knowledge_base!.id,
            name: s.knowledge_base!.name,
            type: s.knowledge_base!.type || 'document',
            knowledge_count: s.knowledge_base!.knowledge_count,
            chunk_count: s.knowledge_base!.chunk_count,
            org_name: s.org_name || '',
            capabilities: s.knowledge_base!.capabilities,
            indexing_strategy: s.knowledge_base!.indexing_strategy,
          }));
        const ownIds = new Set(availableKbs.map((kb) => kb.id));
        sharedKbsForMention.forEach((kb) => {
          if (!ownIds.has(kb.id)) {
            availableKbs.push(kb);
            ownIds.add(kb.id);
          }
        });
      }

      if (hasAgentConfig.value) {
        const kbMode = agentKBSelectionMode.value;
        const isSharedAgent = Boolean(sourceTenantId && agentId);
        if (kbMode === 'none') {
          availableKbs = [];
        } else if (!isSharedAgent) {
          if (kbMode === 'selected') {
            const configuredKbIds = agentKnowledgeBases.value;
            availableKbs = availableKbs.filter((kb) => configuredKbIds.includes(kb.id));
          } else if (kbMode === 'all') {
            availableKbs = availableKbs.filter((kb) => isKbCompatibleWithAgent(kb));
          }
        }
      }

      mentionAllowedKbIds.value = hasAgentConfig.value
        ? new Set(availableKbs.map((kb) => String(kb.id)))
        : null;

      if (isStale()) return;
      const canBrowseFiles =
        !hasAgentConfig.value
        || (agentKBSelectionMode.value !== 'none' && toolsConsumeFiles(agentAllowedTools.value));
      mentionBrowserKbs.value = canBrowseFiles
        ? availableKbs
            .filter((kb) => !kb.type || kb.type === 'document')
            .map((kb) => ({ id: String(kb.id), name: kb.name, orgName: kb.org_name }))
        : [];
      const kbs = availableKbs.filter(
        (kb) => !q || (kb.name && kb.name.toLowerCase().includes(q.toLowerCase())),
      );
      kbItems = await Promise.all(
        kbs.map(async (kb) => {
          const kbType = kb.type || 'document';
          let count =
            kbType === 'faq' ? Number(kb.chunk_count || 0) : Number(kb.knowledge_count || 0);
          if (!count) {
            const detail = await chatResources.fetchKnowledgeBaseById(kb.id);
            if (detail) {
              count =
                detail.type === 'faq'
                  ? Number(detail.chunk_count || 0)
                  : Number(detail.knowledge_count || 0);
            }
          }
          return {
            id: kb.id,
            name: kb.name,
            type: 'kb' as const,
            kbType: kbType === 'faq' ? ('faq' as const) : ('document' as const),
            count,
            orgName: kb.org_name || sharedAgentOrgName.value || undefined,
          };
        }),
      );
      if (isStale()) return;
      nextGroupCounts.kb = kbItems.length;

      const tagKeyword = q.trim();
      try {
        const tagResults = await Promise.all(
          availableKbs.map(async (kb) => {
            const res = (await listKnowledgeTags(kb.id, {
              page: 1,
              page_size: 20,
              keyword: tagKeyword || undefined,
            })) as { data?: { data?: KnowledgeTagApiItem[] } | KnowledgeTagApiItem[] };
            const payload = res?.data ?? res;
            const nested = (payload as { data?: KnowledgeTagApiItem[] })?.data;
            const list: KnowledgeTagApiItem[] = Array.isArray(nested)
              ? nested
              : Array.isArray(payload)
                ? payload
                : [];
            return list.map((tag) => ({
              id: tag.id,
              name: tag.name,
              type: 'tag' as const,
              kbId: kb.id,
              kbName: kb.name,
            }));
          }),
        );
        tagItems = tagResults.flat();
        nextGroupCounts.tag = tagItems.length;
      } catch (e) {
        console.error('[Mention] listKnowledgeTags error:', e);
        tagItems = [];
      }
      if (isStale()) return;

      const mcpMode = agentMCPSelectionMode.value;
      if (mcpMode !== 'none') {
        mcpItems = mcpServices.value
          .filter((service) => isMCPAllowedByAgent(service))
          .filter(
            (service) =>
              !q
              || service.name?.toLowerCase().includes(q.toLowerCase())
              || (service.usage_instructions || service.description || '')
                .toLowerCase()
                .includes(q.toLowerCase()),
          )
          .map(toMCPMentionItem);
      }

      const skillsMode = agentSkillsSelectionMode.value;
      if (skillsMode !== 'none') {
        await deploymentCapabilities.ensureLoaded();
        if (isStale()) return;
        await editorResources.ensureSkills(
          mentionSkillTargetId(
            hostSkillsOnly(
              deploymentCapabilities.isSupported('settings.sandbox.remote'),
              deploymentCapabilities.isSupported('settings.sandbox.host'),
            ),
            currentAgentConfig.value?.sandbox_config_id,
          ),
          currentAgentScope.value,
        );
        if (isStale()) return;
        skillItems = editorResources.skills
          .filter((skill) => isSkillAllowedByAgent(skill.name))
          .map((skill) => ({
            id: skill.name,
            name: skill.name,
            type: 'skill' as const,
            skillName: skill.name,
            description: skill.description || '',
          }))
          .filter((skill) => {
            if (!q) return true;
            const keyword = q.toLowerCase();
            return (
              skill.name.toLowerCase().includes(keyword)
              || (skill.description || '').toLowerCase().includes(keyword)
            );
          });
      }
    }

    if (isStale()) return;
    let fileItems: MentionItem[] = [];
    const kbModeAllowsFiles = !hasAgentConfig.value || agentKBSelectionMode.value !== 'none';
    const toolsAllowFiles = !hasAgentConfig.value || toolsConsumeFiles(agentAllowedTools.value);
    const shouldLoadFiles = kbModeAllowsFiles && toolsAllowFiles;

    const fileSearchKeyword = q.trim();
    if (shouldLoadFiles) {
      mentionLoading.value = true;
      try {
        const fileTypesParam =
          agentSupportedFileTypes.value.length > 0 ? agentSupportedFileTypes.value : undefined;
        const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
        const agentId = selectedAgentId.value;
        const searchOptions = {
          ...(sourceTenantId && agentId
            ? { agent_id: agentId, agent_source_tenant_id: sourceTenantId }
            : {}),
          recent: !fileSearchKeyword,
        };
        const res = (await searchKnowledge(
          fileSearchKeyword,
          mentionOffset.value,
          MENTION_PAGE_SIZE,
          fileTypesParam,
          searchOptions,
        )) as SearchKnowledgeEnvelope;
        if (isStale()) return;
        if (res.data && Array.isArray(res.data)) {
          let files = res.data;
          const rawTotal = typeof res.total === 'number' ? res.total : undefined;
          const apiPageSize = res.data.length;
          if (mentionAllowedKbIds.value) {
            const allowed = mentionAllowedKbIds.value;
            files = files.filter((f) => {
              const kbId = f.knowledge_base_id ?? f.kb_id;
              return kbId != null && allowed.has(String(kbId));
            });
          }
          const sharedKbOrgMap: Record<string, string> = {};
          const sharedList = (orgStore.sharedKnowledgeBases || []) as unknown as SharedKnowledgeBaseWrapperLike[];
          sharedList.forEach((s) => {
            if (s.knowledge_base?.id != null && s.org_name) {
              sharedKbOrgMap[String(s.knowledge_base.id)] = s.org_name;
            }
          });
          const agentOrgLabel = sourceTenantId && agentId ? sharedAgentOrgName.value : '';
          fileItems = files.map((f) => {
            const kbId = f.knowledge_base_id ?? f.kb_id;
            const kbIdStr = kbId != null ? String(kbId) : '';
            const fileOrgName = agentOrgLabel || (kbIdStr ? sharedKbOrgMap[kbIdStr] : undefined);
            return {
              id: f.id,
              name: f.title || f.file_name || f.id,
              type: 'file' as const,
              kbName: f.knowledge_base_name || '',
              kbId: kbId ? String(kbId) : undefined,
              orgName: fileOrgName || undefined,
            };
          });
          if (!append) {
            const clientFiltered = Boolean(mentionAllowedKbIds.value) && fileItems.length < apiPageSize;
            if (!clientFiltered && rawTotal != null) {
              nextGroupCounts.file = rawTotal;
            } else {
              delete nextGroupCounts.file;
            }
          }
        }
        mentionHasMore.value = res.has_more || false;
        mentionOffset.value += fileItems.length;
      } catch (e) {
        if (isStale()) return;
        console.error('[Mention] searchKnowledge error:', e);
        mentionHasMore.value = false;
      } finally {
        if (!isStale()) mentionLoading.value = false;
      }
    } else {
      if (isStale()) return;
      mentionHasMore.value = false;
    }

    if (isStale()) return;
    if (!append) mentionGroupCounts.value = nextGroupCounts;
    if (append) {
      mentionItems.value = [...mentionItems.value, ...fileItems];
    } else {
      mentionItems.value = [...kbItems, ...tagItems, ...mcpItems, ...skillItems, ...fileItems];
    }

    if (resetIndex || q !== lastMentionQuery) {
      mentionActiveIndex.value = 0;
    }
    if (mentionActiveIndex.value >= mentionItems.value.length) {
      mentionActiveIndex.value = Math.max(0, mentionItems.value.length - 1);
    }
    lastMentionQuery = q;
  };

  const loadMoreMentionItems = () => {
    if (mentionHasMore.value && !mentionLoading.value) {
      void loadMentionItems(lastMentionQuery, false, true);
    }
  };

  const onMentionSearchInput = (q: string): void => {
    mentionQuery.value = q;
    if (mentionSearchTimer) clearTimeout(mentionSearchTimer);
    mentionSearchTimer = setTimeout(() => {
      mentionSearchTimer = null;
      void loadMentionItems(q, true);
    }, 180);
  };

  const closeMentionFromPopup = (): void => {
    showMention.value = false;
    void focusInput();
  };

  const onInput = (val: string | InputEvent): void => {
    if (isComposing.value) return;
    const inputVal = typeof val === 'string' ? val : query.value;
    const textarea = getTextareaEl();
    if (!textarea) return;

    const cursor = textarea.selectionStart;
    const textBeforeCursor = inputVal.slice(0, cursor);

    if (showMention.value) {
      if (!isMentionTriggeredByButton.value) {
        if (
          !inputVal
          || inputVal.length <= mentionStartPos.value
          || inputVal.charAt(mentionStartPos.value) !== '@'
        ) {
          showMention.value = false;
          return;
        }
      }

      if (cursor < mentionStartPos.value) {
        showMention.value = false;
        return;
      }

      const start = isMentionTriggeredByButton.value
        ? mentionStartPos.value
        : mentionStartPos.value + 1;
      const q = inputVal.slice(start, cursor);

      if (q.includes(' ')) {
        showMention.value = false;
        return;
      }
      if (q !== mentionQuery.value) {
        onMentionSearchInput(q);
      }
    } else if (textBeforeCursor.endsWith('@')) {
      if (isMentionDisabled.value) {
        return;
      }

      isMentionTriggeredByButton.value = false;
      mentionStartPos.value = cursor - 1;
      showMention.value = true;
      mentionQuery.value = '';

      const coords = getCaretCoordinates(textarea, cursor);
      const zoom = getRootZoom();
      const rect = rectToCssPx(textarea.getBoundingClientRect(), zoom);
      const { width: vw, height: vh } = cssViewportSize(zoom);
      const scrollTop = textarea.scrollTop;
      const menuHeight = 380;

      let left = rect.left + coords.left;
      if (left + 300 > vw) {
        left = vw - 300 - 10;
      }

      const cursorAbsoluteTop = rect.top + coords.top - scrollTop;
      const lineHeight = coords.height;
      const spaceBelow = vh - (cursorAbsoluteTop + lineHeight);

      if (spaceBelow < menuHeight && cursorAbsoluteTop > menuHeight) {
        const bottom = vh - cursorAbsoluteTop;
        mentionStyle.value = {
          left: `${left}px`,
          bottom: `${bottom}px`,
          top: 'auto',
        };
      } else {
        const top = cursorAbsoluteTop + lineHeight;
        mentionStyle.value = {
          left: `${left}px`,
          top: `${top}px`,
          bottom: 'auto',
        };
      }

      if (mentionSearchTimer) clearTimeout(mentionSearchTimer);
      void loadMentionItems('');
    }
  };

  const onCompositionStart = (): void => {
    isComposing.value = true;
  };

  const onCompositionEnd = (): void => {
    isComposing.value = false;
    void nextTick(() => {
      onInput(query.value);
    });
  };

  const triggerMention = (): void => {
    if (isMentionDisabled.value) {
      const msgKey = isKnowledgeBaseDisabledByAgent.value
        ? 'input.kbDisabledByAgent'
        : 'input.kbLockedByAgent';
      MessagePlugin.warning(t(msgKey));
      return;
    }

    const textarea = getTextareaEl();
    if (!textarea) return;

    onCloseOtherPopups();
    textarea.focus();

    showMention.value = true;
    isMentionTriggeredByButton.value = true;
    mentionQuery.value = '';
    mentionStartPos.value = textarea.selectionStart;

    const zoom = getRootZoom();
    const rect = rectToCssPx(textarea.getBoundingClientRect(), zoom);
    const { height: vh } = cssViewportSize(zoom);
    const menuHeight = 380;
    const spaceAbove = rect.top;
    const spaceBelow = vh - rect.bottom;

    if (spaceAbove > menuHeight || spaceAbove > spaceBelow) {
      mentionStyle.value = {
        left: `${rect.left}px`,
        bottom: `${vh - rect.top + 8}px`,
        top: 'auto',
      };
    } else {
      mentionStyle.value = {
        left: `${rect.left}px`,
        top: `${rect.bottom + 8}px`,
        bottom: 'auto',
      };
    }

    if (mentionSearchTimer) clearTimeout(mentionSearchTimer);
    void loadMentionItems('');
  };

  const onMentionSelect = (item: MentionItem) => {
    if (!showMention.value) return;
    if (item.type === 'kb') {
      settingsStore.addKnowledgeBase(item.id);
    } else if (item.type === 'file') {
      settingsStore.addFile(item.id);
      if (item.kbId) {
        fileIdToKbId.value[item.id] = item.kbId;
        settingsStore.setFileKbMap({ [item.id]: item.kbId });
      }
      if (!fileList.value.find((f) => f.id === item.id)) {
        fileList.value.push({ id: item.id, name: item.name });
      }
    } else if (item.type === 'tag') {
      if (item.kbId) {
        settingsStore.addTag({ id: item.id, name: item.name, kbId: item.kbId, kbName: item.kbName });
      }
    } else if (item.type === 'mcp') {
      settingsStore.addMCPService(item.id);
    } else if (item.type === 'skill') {
      settingsStore.addSkill(item.skillName || item.id);
    }

    const textarea = getTextareaEl();
    if (textarea) {
      if (!isMentionTriggeredByButton.value) {
        const cursor = textarea.selectionStart;
        const textBeforeAt = query.value.slice(0, mentionStartPos.value);
        const textAfterCursor = query.value.slice(cursor);
        query.value = textBeforeAt + textAfterCursor;

        nextTick(() => {
          textarea.selectionStart = textarea.selectionEnd = mentionStartPos.value;
          textarea.focus();
        });
      } else {
        const cursor = textarea.selectionStart;
        if (cursor > mentionStartPos.value) {
          const textBeforeStart = query.value.slice(0, mentionStartPos.value);
          const textAfterCursor = query.value.slice(cursor);
          query.value = textBeforeStart + textAfterCursor;

          nextTick(() => {
            textarea.selectionStart = textarea.selectionEnd = mentionStartPos.value;
            textarea.focus();
          });
        } else {
          textarea.focus();
        }
      }
    }

    showMention.value = false;
  };

  const removeFile = (id: string): void => {
    settingsStore.removeFile(id);
    delete fileIdToKbId.value[id];
  };

  const closeMentionSelector = (e: MouseEvent): void => {
    const target = e.target as HTMLElement | null;
    if (target?.closest('.rich-input-container')) {
      return;
    }
    showMention.value = false;
  };

  const cleanupMentionTimers = (): void => {
    if (mentionSearchTimer) {
      clearTimeout(mentionSearchTimer);
      mentionSearchTimer = null;
    }
  };

  void removeFile;

  return {
    showMention,
    mentionQuery,
    mentionItems,
    fileIdToKbId,
    mentionActiveIndex,
    mentionStyle,
    isComposing,
    isMentionTriggeredByButton,
    mentionHasMore,
    mentionGroupCounts,
    mentionBrowserKbs,
    mentionLoading,
    selectedKbIds,
    selectedFileIds,
    selectedTags,
    isMentionDisabled,
    mentionEmptyHint,
    mentionBrowserContext,
    allSelectedItems,
    removeSelectedItem,
    loadKnowledgeBases,
    loadMentionItems,
    loadMoreMentionItems,
    onMentionSearchInput,
    closeMentionFromPopup,
    onInput,
    onCompositionStart,
    onCompositionEnd,
    triggerMention,
    onMentionSelect,
    closeMentionSelector,
    cleanupMentionTimers,
  };
}
