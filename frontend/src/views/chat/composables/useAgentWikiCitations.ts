import { marked } from 'marked';
import { MessagePlugin } from 'tdesign-vue-next';
import { computed, nextTick, ref, watch, type ComputedRef } from 'vue';
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router';
import { getChunkByIdOnly, getKnowledgeDetails } from '@/api/knowledge-base';
import { getWikiPage, type WikiPage } from '@/api/wiki';
import { useAuthStore } from '@/stores/auth';
import { useSettingsStore } from '@/stores/settings';
import { wrapChatMarkdownTables } from '@/utils/chatMarkdownRenderer';
import { citationAnchorText } from '@/utils/citationAnchor';
import { resolveCitationChunkId } from '@/utils/citationMarkdown';
import { GENYEE_ENTITY_LINK_CLASS, type GENYEEEntityKind } from '@/utils/genyeeEntityLinks';
import type { ProtectedFileAccessContext } from '@/utils/protectedFileAccess';
import type { KnowledgeReferenceLike, ReferenceHighlightTarget } from '@/utils/referenceSources';
import { artifactIndexFromEventTarget } from '@/utils/sandboxArtifactRefs';
import { hydrateProtectedFileImages, sanitizeMarkdownHTML } from '@/utils/security';
import type { AgentSessionData, WailsWindow } from '../types/agentStream';
import type { TranslateFn } from './useAgentToolPresentation';

function escapeHtml(value: string): string {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function getWailsBrowserOpenURL(): ((url: string) => void) | undefined {
  if (typeof window === 'undefined') return undefined;
  return (window as WailsWindow).runtime?.BrowserOpenURL;
}

export interface UseAgentWikiCitationsOptions {
  getSession: () => AgentSessionData | undefined;
  isEmbeddedMode: () => boolean;
  hasEmbedToken: () => boolean;
  protectedFileAccess: ComputedRef<ProtectedFileAccessContext | undefined>;
  getReferencesForDrawer: (refsOverride?: KnowledgeReferenceLike[] | null) => KnowledgeReferenceLike[];
  openReferencesDrawer: (
    highlight?: ReferenceHighlightTarget,
    refsOverride?: KnowledgeReferenceLike[] | null,
  ) => boolean;
  openArtifactDrawer: (previewIndex?: number | null) => void;
  openImagePreview: (url: string) => void;
  router: Router;
  route: RouteLocationNormalizedLoaded;
  t: TranslateFn;
}

export function useAgentWikiCitations(options: UseAgentWikiCitationsOptions) {
  const {
    getSession,
    isEmbeddedMode,
    hasEmbedToken,
    protectedFileAccess,
    getReferencesForDrawer,
    openReferencesDrawer,
    openArtifactDrawer,
    openImagePreview,
    router,
    route,
    t,
  } = options;

  const settingsStore = useSettingsStore();
  const authStore = useAuthStore();

  const wikiDrawerVisible = ref(false);
  const wikiDrawerPage = ref<WikiPage | null>(null);
  const wikiDrawerBodyRef = ref<HTMLElement | null>(null);
  const currentWikiKbId = ref<string>('');

  function getTypeTheme(type: string): string {
    const map: Record<string, string> = {
      summary: 'primary',
      entity: 'success',
      concept: 'warning',
      synthesis: 'primary',
      comparison: 'danger',
      index: 'default',
    };
    return map[type] || 'default';
  }

  function getTypeLabel(type: string): string {
    const map: Record<string, string> = {
      summary: t('knowledgeEditor.wikiBrowser.filterSummary'),
      entity: t('knowledgeEditor.wikiBrowser.filterEntity'),
      concept: t('knowledgeEditor.wikiBrowser.filterConcept'),
      synthesis: t('knowledgeEditor.wikiBrowser.filterSynthesis'),
      comparison: t('knowledgeEditor.wikiBrowser.filterComparison'),
      index: 'Index',
    };
    return map[type] || type;
  }

  const wikiDrawerContent = computed(() => {
    if (!wikiDrawerPage.value) return '';
    const content = wikiDrawerPage.value.content || '';

    const preprocessed = content.replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
      const pipeIdx = inner.indexOf('|');
      const slug = pipeIdx > 0 ? inner.substring(0, pipeIdx).trim() : inner.trim();
      let display = slug;
      if (pipeIdx > 0) {
        display = inner.substring(pipeIdx + 1).trim();
      } else {
        const parts = slug.split('/');
        display = parts.length > 1 ? parts.slice(1).join('/') : slug;
      }
      return `<a href="#" class="wiki-content-link citation-wiki" data-slug="${escapeHtml(slug)}">${escapeHtml(display)}</a>`;
    });

    const html = marked.parse(preprocessed, { breaks: true, async: false }) as string;
    return sanitizeMarkdownHTML(wrapChatMarkdownTables(html));
  });

  watch(wikiDrawerContent, async () => {
    await nextTick();
    if (wikiDrawerBodyRef.value) {
      await hydrateProtectedFileImages(wikiDrawerBodyRef.value, protectedFileAccess.value);
    }
  });

  let wikiDrawerRequestSeq = 0;
  let isUnmounted = false;

  const openWikiDrawer = async (kbId: string, slug: string) => {
    if (!kbId || !slug) return;
    const seq = ++wikiDrawerRequestSeq;
    try {
      const res = await getWikiPage(kbId, slug);
      if (isUnmounted || seq !== wikiDrawerRequestSeq) return;
      currentWikiKbId.value = kbId;
      const unwrapped =
        res && typeof res === 'object' && 'data' in res
          ? ((res as { data?: WikiPage }).data ?? null)
          : (res as WikiPage);
      wikiDrawerPage.value = unwrapped;
      wikiDrawerVisible.value = true;
    } catch (e) {
      if (isUnmounted || seq !== wikiDrawerRequestSeq) return;
      console.error(`Failed to load page ${slug}:`, e);
      MessagePlugin.warning(t('agentStream.citation.loadFailed'));
    }
  };

  const wikiGraphHref = computed(() => {
    if (!currentWikiKbId.value || !wikiDrawerPage.value?.slug) return '';
    return router.resolve({
      path: `/platform/knowledge-bases/${currentWikiKbId.value}`,
      query: { tab: 'graph', slug: wikiDrawerPage.value.slug },
    }).href;
  });

  const openRouteInNewTab = (path: string) => {
    const href = router.resolve(path).href;
    window.open(href, '_blank', 'noopener,noreferrer');
  };

  const resolveEntityRoute = async (kind: GENYEEEntityKind, id: string) => {
    if (kind === 'kb') return { path: `/platform/knowledge-bases/${encodeURIComponent(id)}` };
    const res = kind === 'doc' ? await getKnowledgeDetails(id) : await getChunkByIdOnly(id);
    const data =
      res && typeof res === 'object' && 'data' in res
        ? (res as { data?: { knowledge_base_id?: string; knowledge_id?: string } }).data
        : undefined;
    const kbId = data?.knowledge_base_id;
    const knowledgeId = kind === 'doc' ? id : data?.knowledge_id;
    if (!kbId) return null;
    return {
      path: `/platform/knowledge-bases/${encodeURIComponent(kbId)}`,
      query: knowledgeId ? { knowledge_id: knowledgeId } : {},
    };
  };

  const openGENYEEEntity = async (el: HTMLElement) => {
    const kind = el.getAttribute('data-entity-kind') as GENYEEEntityKind | null;
    const id = el.getAttribute('data-entity-id') || '';
    if (!kind || !id || hasEmbedToken()) return;
    const win = kind === 'kb' ? null : window.open('about:blank', '_blank');
    try {
      const targetRoute = await resolveEntityRoute(kind, id);
      if (!targetRoute) throw new Error('entity has no knowledge base');
      const href = router.resolve(targetRoute).href;
      if (win) {
        win.opener = null;
        win.location.href = href;
      } else {
        window.open(href, '_blank', 'noopener,noreferrer');
      }
    } catch (error) {
      win?.close();
      console.error('Failed to open GENYEE entity link:', error);
      MessagePlugin.warning(t('agentStream.citation.loadFailed'));
    }
  };

  const handleWikiDrawerClick = (e: MouseEvent) => {
    const target = e.target as HTMLElement | null;
    if (!target) return;
    if (target.closest('.citation-wiki')) {
      e.preventDefault();
      e.stopPropagation();
      const slug = target.closest('.citation-wiki')?.getAttribute('data-slug');
      if (slug) void openWikiDrawer(currentWikiKbId.value, slug);
    } else if (target.tagName.toLowerCase() === 'img') {
      e.preventDefault();
      const src = target.getAttribute('src');
      if (src) openImagePreview(src);
    } else {
      const aEl = target.closest?.('a') as HTMLAnchorElement | null;
      const browserOpenUrl = getWailsBrowserOpenURL();
      if (aEl && aEl.href && browserOpenUrl) {
        if (aEl.href.startsWith('http://') || aEl.href.startsWith('https://')) {
          e.preventDefault();
          browserOpenUrl(aEl.href);
        }
      }
    }
  };

  const handleCitationActivate = (el: HTMLElement) => {
    const url = el.getAttribute('data-url');
    if (url && openReferencesDrawer({ url })) {
      return;
    }
    if (!url) return;
    let safeUrl = '';
    try {
      const base =
        typeof window !== 'undefined' && window.location?.origin
          ? window.location.origin
          : 'https://localhost';
      const parsed = new URL(url.trim(), base);
      if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
        safeUrl = url.trim();
      }
    } catch {
      safeUrl = '';
    }
    if (!safeUrl) return;
    try {
      const browserOpenUrl = getWailsBrowserOpenURL();
      if (browserOpenUrl) {
        browserOpenUrl(safeUrl);
      } else {
        const newWindow = window.open(safeUrl, '_blank', 'noopener,noreferrer');
        if (!newWindow) {
          window.location.assign(safeUrl);
        }
      }
    } catch {
      window.location.assign(safeUrl);
    }
  };

  const getKbIdForWiki = (slug: string): string => {
    if (route.params.kbId) return route.params.kbId as string;

    const pickKbId = (v: unknown): string => {
      if (!v) return '';
      if (typeof v === 'string') return v;
      if (Array.isArray(v)) {
        for (const item of v) {
          if (typeof item === 'string' && item) return item;
        }
      }
      return '';
    };

    const session = getSession();
    if (session?.agentEventStream) {
      for (let i = session.agentEventStream.length - 1; i >= 0; i--) {
        const event = session.agentEventStream[i];
        const foundKbs = event?.tool_data?.found_kbs;
        if (event.type === 'tool_call' && foundKbs) {
          const hit = pickKbId(foundKbs[slug]);
          if (hit) return hit;
        }
      }
    }

    const selectedKbs = settingsStore.getSelectedKnowledgeBases();
    if (selectedKbs && selectedKbs.length > 0) return selectedKbs[0];

    if (authStore.knowledgeBases && authStore.knowledgeBases.length > 0) {
      return authStore.knowledgeBases[0].id;
    }

    return '';
  };

  const activateKbCitation = (kbEl: HTMLElement) => {
    const rawChunkId = kbEl.getAttribute('data-chunk-id') || '';
    const title = kbEl.getAttribute('data-doc') || '';
    const kbId = kbEl.getAttribute('data-kb-id') || '';
    const chunkId =
      resolveCitationChunkId(
        rawChunkId,
        { doc: title, kbId },
        getReferencesForDrawer(),
      ) || rawChunkId;
    if (
      openReferencesDrawer({
        chunkId,
        documentTitle: title,
        knowledgeBaseId: kbId,
        anchorText: citationAnchorText(kbEl),
        openSource: !isEmbeddedMode(),
      })
    ) {
      return;
    }
    if (kbId) {
      try {
        openRouteInNewTab(`/platform/knowledge-bases/${kbId}`);
      } catch (error) {
        console.error('Failed to navigate to knowledge base:', error);
      }
    }
  };

  const onRootClick = (e: Event) => {
    const target = e.target as HTMLElement | null;
    if (!target) return;

    const artifactIndex = artifactIndexFromEventTarget(target);
    if (artifactIndex !== null) {
      e.preventDefault();
      e.stopPropagation();
      openArtifactDrawer(artifactIndex);
      return;
    }

    if (target.tagName === 'IMG') {
      const imgEl = target as HTMLImageElement;
      if (imgEl.closest('.markdown-content') || imgEl.closest('.answer-content')) {
        const src = imgEl.getAttribute('src');
        if (src) {
          e.preventDefault();
          e.stopPropagation();
          openImagePreview(src);
          return;
        }
      }
    }

    const webEl = target.closest?.('.citation-web') as HTMLElement | null;
    if (webEl && webEl.getAttribute('data-url')) {
      e.preventDefault();
      handleCitationActivate(webEl);
      return;
    }

    const kbEl = target.closest?.('.citation-kb') as HTMLElement | null;
    if (kbEl && kbEl.getAttribute('data-chunk-id')) {
      e.preventDefault();
      e.stopPropagation();
      activateKbCitation(kbEl);
      return;
    }

    const entityEl = target.closest?.(`.${GENYEE_ENTITY_LINK_CLASS}`) as HTMLElement | null;
    if (entityEl) {
      e.preventDefault();
      e.stopPropagation();
      void openGENYEEEntity(entityEl);
      return;
    }

    const wikiEl = target.closest?.('.citation-wiki') as HTMLElement | null;
    if (wikiEl && wikiEl.getAttribute('data-slug')) {
      e.preventDefault();
      e.stopPropagation();
      const slug = wikiEl.getAttribute('data-slug');
      const kbId = getKbIdForWiki(slug || '');
      if (kbId && slug) {
        void openWikiDrawer(kbId, slug);
      } else {
        MessagePlugin.warning(t('agentStream.citation.noKbForWiki'));
      }
      return;
    }

    const aEl = target.closest?.('a') as HTMLAnchorElement | null;
    const browserOpenUrl = getWailsBrowserOpenURL();
    if (aEl && aEl.href && browserOpenUrl) {
      if (aEl.href.startsWith('http://') || aEl.href.startsWith('https://')) {
        e.preventDefault();
        browserOpenUrl(aEl.href);
      }
    }
  };

  const onRootKeydown = (e: KeyboardEvent) => {
    const target = e.target as HTMLElement | null;
    if (!target) return;

    const artifactIndex = artifactIndexFromEventTarget(target);
    if (artifactIndex !== null) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openArtifactDrawer(artifactIndex);
      }
      return;
    }

    const webEl = target.closest?.('.citation-web') as HTMLElement | null;
    if (webEl) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        handleCitationActivate(webEl);
      }
      return;
    }

    const kbEl = target.closest?.('.citation-kb') as HTMLElement | null;
    if (kbEl) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        activateKbCitation(kbEl);
      }
      return;
    }

    const entityEl = target.closest?.(`.${GENYEE_ENTITY_LINK_CLASS}`) as HTMLElement | null;
    if (entityEl) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        void openGENYEEEntity(entityEl);
      }
      return;
    }

    const wikiEl = target.closest?.('.citation-wiki') as HTMLElement | null;
    if (wikiEl) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        const slug = wikiEl.getAttribute('data-slug');
        const kbId = getKbIdForWiki(slug || '');
        if (kbId && slug) {
          void openWikiDrawer(kbId, slug);
        } else {
          MessagePlugin.warning(t('agentStream.citation.noKbForWiki'));
        }
      }
    }
  };

  const disposeWikiCitations = () => {
    isUnmounted = true;
    wikiDrawerRequestSeq++;
    wikiDrawerVisible.value = false;
  };

  return {
    wikiDrawerVisible,
    wikiDrawerPage,
    wikiDrawerBodyRef,
    currentWikiKbId,
    wikiDrawerContent,
    wikiGraphHref,
    getTypeTheme,
    getTypeLabel,
    openWikiDrawer,
    handleWikiDrawerClick,
    handleCitationActivate,
    openRouteInNewTab,
    onRootClick,
    onRootKeydown,
    disposeWikiCitations,
  };
}
