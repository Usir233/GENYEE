import { ref, watch, type Ref } from 'vue'
import type { WikiPage, WikiPageIssue } from '@/api/wiki'

export interface WikiIssuesAssistantLike {
  visible: boolean
  open: (payload: { query: string; focus?: { wikiIssueIds?: string[] } }) => void
}

export interface WikiIssuesApi {
  listWikiIssues: (kbId: string, slug?: string, status?: string) => Promise<unknown>
  updateWikiIssueStatus: (kbId: string, issueId: string, status: string) => Promise<unknown>
}

export interface UseWikiGlobalIssuesOptions {
  getKbId: () => string
  getView: () => 'browser' | 'graph' | undefined
  selectedPage: Ref<WikiPage | null>
  editingPage: Ref<boolean>
  t: (key: string, params?: Record<string, unknown>) => string
  api: WikiIssuesApi
  assistant: WikiIssuesAssistantLike
  onNavigateToSlug: (slug: string) => Promise<void>
  onGraphSearchSelect: (slug: string) => Promise<void> | void
  onRefreshSelectedPage: () => Promise<void>
  onReloadStats: () => Promise<void> | void
}

function unwrapIssuesList(res: unknown): WikiPageIssue[] {
  if (Array.isArray(res)) return res as WikiPageIssue[]
  if (res && typeof res === 'object' && 'data' in res) {
    const inner = (res as { data?: unknown }).data
    if (Array.isArray(inner)) return inner as WikiPageIssue[]
  }
  return []
}

export function useWikiGlobalIssues(options: UseWikiGlobalIssuesOptions) {
  const pageIssues = ref<WikiPageIssue[]>([])
  const showIssuesBox = ref(false)
  const showGlobalIssuesDrawer = ref(false)
  const globalIssues = ref<WikiPageIssue[]>([])

  async function loadGlobalIssues() {
    try {
      const res = await options.api.listWikiIssues(options.getKbId(), '', 'pending')
      globalIssues.value = unwrapIssuesList(res)
    } catch (e) {
      console.error('Failed to load global wiki issues:', e)
      globalIssues.value = []
    }
  }

  watch(showGlobalIssuesDrawer, (val) => {
    if (val) void loadGlobalIssues()
  })

  async function loadPageIssues(slug: string) {
    try {
      const res = await options.api.listWikiIssues(options.getKbId(), slug, 'pending')
      pageIssues.value = unwrapIssuesList(res)
      showIssuesBox.value = false
    } catch (e) {
      console.error('Failed to load wiki issues:', e)
      pageIssues.value = []
      showIssuesBox.value = false
    }
  }

  async function navigateToSlugAndFix(slug: string) {
    showGlobalIssuesDrawer.value = false
    if (options.getView() === 'graph') {
      await options.onGraphSearchSelect(slug)
    } else {
      await options.onNavigateToSlug(slug)
      showIssuesBox.value = true
    }
  }

  async function handleGlobalIssueIgnore(issueId: string) {
    try {
      await options.api.updateWikiIssueStatus(options.getKbId(), issueId, 'ignored')
      const res = await options.api.listWikiIssues(options.getKbId(), '', 'pending')
      globalIssues.value = unwrapIssuesList(res)
      void options.onReloadStats()
    } catch (e) {
      console.error('Failed to update issue status:', e)
    }
  }

  async function handleIssueIgnore(issueId: string) {
    try {
      await options.api.updateWikiIssueStatus(options.getKbId(), issueId, 'ignored')
      if (options.selectedPage.value) {
        await loadPageIssues(options.selectedPage.value.slug)
      }
    } catch (e) {
      console.error('Failed to update issue status:', e)
    }
  }

  function triggerFixIssue(issue: WikiPageIssue) {
    if (!options.selectedPage.value) return
    showIssuesBox.value = false
    options.assistant.open({
      query: options.t('knowledgeEditor.wikiBrowser.issueFixPromptSingle', { description: issue.description }),
      focus: { wikiIssueIds: [issue.id] },
    })
  }

  function triggerAutoFix() {
    if (!options.selectedPage.value || pageIssues.value.length === 0) return
    showIssuesBox.value = false
    options.assistant.open({
      query: options.t('knowledgeEditor.wikiBrowser.issueFixPromptAll', { count: pageIssues.value.length }),
      focus: { wikiIssueIds: pageIssues.value.map((issue) => issue.id) },
    })
  }

  watch(
    () => options.assistant.visible,
    (visible, wasVisible) => {
      if (!wasVisible || visible || options.editingPage.value) return
      void options.onRefreshSelectedPage()
      void options.onReloadStats()
    },
  )

  function resetIssuesForKbSwitch() {
    showGlobalIssuesDrawer.value = false
    showIssuesBox.value = false
    pageIssues.value = []
    globalIssues.value = []
  }

  return {
    pageIssues,
    showIssuesBox,
    showGlobalIssuesDrawer,
    globalIssues,
    loadGlobalIssues,
    loadPageIssues,
    navigateToSlugAndFix,
    handleGlobalIssueIgnore,
    handleIssueIgnore,
    triggerFixIssue,
    triggerAutoFix,
    resetIssuesForKbSwitch,
  }
}
