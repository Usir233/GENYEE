import { computed, ref, type ComputedRef, type Ref } from 'vue'
import type { Resource } from '@/api/datasource'
import {
  extractDriveFolderToken,
  extractErrorMessage,
  isSeafileConnector,
  seafileLibraryOf,
  seafileWithin,
  type CheckState,
  type DataSourceFormState,
  type VisibleTreeRow,
} from './useConnectorDefs'

export interface ResourceListApiResponse {
  data?: Resource[]
}

export interface AncestorsApiResponse {
  data?: { ancestors?: string[] }
  ancestors?: string[]
}

export interface CreateDataSourceApiResponse {
  data?: { id?: string }
  id?: string
}

export interface UseConnectorResourceTreeDeps {
  kbId: () => string
  isEdit: ComputedRef<boolean>
  form: Ref<DataSourceFormState>
  t: (key: string) => string
  api: {
    createDataSource: (payload: Record<string, unknown>) => Promise<CreateDataSourceApiResponse | unknown>
    updateDataSource: (id: string, payload: Record<string, unknown>) => Promise<unknown>
    listResources: (id: string, parentId?: string) => Promise<ResourceListApiResponse | Resource[] | unknown>
    resolveResourceAncestors: (id: string, ids: string[]) => Promise<AncestorsApiResponse | unknown>
    deleteDataSource: (id: string) => Promise<unknown>
    putDataSourceCredentials: (id: string, creds: Record<string, string>) => Promise<unknown>
  }
  messageError: (msg: string) => void
  messageWarning: (msg: string) => void
  syncRssAuthHeadersToCredentials: () => void
  syncConfluencePublicFieldsToSettings: () => void
  warn: (msg: string) => void
}

function unwrapResources(res: unknown): Resource[] {
  if (Array.isArray(res)) return res as Resource[]
  const wrapped = res as ResourceListApiResponse | null | undefined
  return Array.isArray(wrapped?.data) ? wrapped.data : []
}

export function useConnectorResourceTree(deps: UseConnectorResourceTreeDeps) {
  const resources = ref<Resource[]>([])
  const loadingResources = ref(false)
  const selectedResourceIds = ref<string[]>([])
  const expandedResourceIds = ref(new Set<string>())
  const loadedChildrenIds = ref(new Set<string>())
  const loadingChildrenIds = ref(new Set<string>())
  const treeFullyLoaded = ref(false)
  const driveFolderToken = ref('')
  const driveFolderTokenError = ref('')
  const driveRootLoaded = ref(false)
  const tempDsId = ref('')

  let drawerSession = 0
  let tempDsPromise: Promise<string> | null = null
  let tempDsCredentialsDirty = false

  function markTempCredentialsDirty() {
    tempDsCredentialsDirty = true
  }

  function bumpSession() {
    drawerSession++
    tempDsPromise = null
    tempDsCredentialsDirty = false
  }

  async function cleanupOrphanTempDataSource(background = false) {
    bumpSession()
    if (!deps.isEdit.value && tempDsId.value) {
      const orphanId = tempDsId.value
      tempDsId.value = ''
      const task = async () => {
        try {
          await deps.api.deleteDataSource(orphanId)
        } catch {
          // Ignore cleanup errors
        }
      }
      if (background) {
        void task()
      } else {
        await task()
      }
    }
  }

  function resetTreeState() {
    tempDsId.value = ''
    resources.value = []
    selectedResourceIds.value = []
    expandedResourceIds.value = new Set()
    loadedChildrenIds.value = new Set()
    loadingChildrenIds.value = new Set()
    treeFullyLoaded.value = false
    driveFolderToken.value = ''
    driveFolderTokenError.value = ''
    driveRootLoaded.value = false
  }

  function classifyDriveLoadError(e: unknown): string {
    const raw = extractErrorMessage(e)
    const lower = raw.toLowerCase()
    if (
      lower.includes('status=403') ||
      lower.includes('forbidden') ||
      lower.includes('"code":1061004') ||
      lower.includes('code=1061004')
    ) {
      return deps.t('datasource.drive.loadForbiddenHint')
    }
    if (lower.includes('status=401') || lower.includes('auth') || lower.includes('1061005')) {
      return deps.t('datasource.drive.loadAuthHint')
    }
    if (lower.includes('1061003') || lower.includes('not found')) {
      return deps.t('datasource.drive.loadNotFoundHint')
    }
    return raw || deps.t('datasource.resourceLoadFailed')
  }

  function canCreateTempDataSource(): boolean {
    return (deps.kbId() || '').trim() !== '' && String(deps.form.value.type || '').trim() !== ''
  }

  async function ensureTempDataSource(persist = false): Promise<string> {
    if (tempDsId.value) {
      if (!deps.isEdit.value && tempDsCredentialsDirty) {
        deps.syncRssAuthHeadersToCredentials()
        deps.syncConfluencePublicFieldsToSettings()
        const filled = Object.entries(deps.form.value.config.credentials).filter(([, v]) =>
          typeof v === 'string' ? v !== '' : v != null,
        )
        if (filled.length > 0) {
          await deps.api.putDataSourceCredentials(tempDsId.value, Object.fromEntries(filled))
        }
        tempDsCredentialsDirty = false
      }
      if (persist || !deps.isEdit.value) {
        await deps.api.updateDataSource(tempDsId.value, {
          ...deps.form.value,
          knowledge_base_id: deps.kbId(),
        })
      }
      return tempDsId.value
    }

    if (!canCreateTempDataSource()) {
      deps.warn(
        '[DataSourceEditorDialog] skipped creating a temporary data source: ' +
          `connector type ${JSON.stringify(deps.form.value.type)} or knowledge base id ${JSON.stringify(deps.kbId())} is empty`,
      )
      return ''
    }

    if (!tempDsPromise) {
      const ownSession = drawerSession
      const createTask = (async () => {
        const res = (await deps.api.createDataSource({
          ...deps.form.value,
          knowledge_base_id: deps.kbId(),
          status: 'paused',
        })) as CreateDataSourceApiResponse | null | undefined
        const created = res?.data || res
        const createdId = typeof created?.id === 'string' ? created.id.trim() : ''
        if (drawerSession !== ownSession) {
          if (createdId) {
            try {
              await deps.api.deleteDataSource(createdId)
            } catch {
              // Ignore cleanup errors
            }
          }
          return ''
        }
        if (!createdId) {
          throw new Error(deps.t('datasource.saveFailed'))
        }
        tempDsId.value = createdId
        return createdId
      })()
      const tracked = createTask.finally(() => {
        if (tempDsPromise === tracked) tempDsPromise = null
      })
      tempDsPromise = tracked
    }
    return tempDsPromise
  }

  async function ensureChildrenLoaded(id: string) {
    if (!tempDsId.value) return
    if (loadedChildrenIds.value.has(id) || loadingChildrenIds.value.has(id)) return
    if (treeFullyLoaded.value) {
      loadedChildrenIds.value = new Set(loadedChildrenIds.value).add(id)
      return
    }

    loadingChildrenIds.value = new Set(loadingChildrenIds.value).add(id)
    try {
      const res = await deps.api.listResources(tempDsId.value, id)
      const children = unwrapResources(res)
      if (children.length > 0) {
        const existing = new Set(resources.value.map((r) => r.external_id))
        const merged = resources.value.slice()
        for (const c of children) {
          if (!existing.has(c.external_id)) merged.push(c)
        }
        resources.value = merged
      } else {
        resources.value = resources.value.map((r) =>
          r.external_id === id ? { ...r, has_children: false } : r,
        )
        const expanded = new Set(expandedResourceIds.value)
        expanded.delete(id)
        expandedResourceIds.value = expanded
      }
      loadedChildrenIds.value = new Set(loadedChildrenIds.value).add(id)
    } catch (e: unknown) {
      deps.messageError(extractErrorMessage(e) || deps.t('datasource.resourceLoadFailed'))
      const next = new Set(expandedResourceIds.value)
      next.delete(id)
      expandedResourceIds.value = next
    } finally {
      const s = new Set(loadingChildrenIds.value)
      s.delete(id)
      loadingChildrenIds.value = s
    }
  }

  async function revealExistingSelections(hiddenIds: string[]) {
    if (!tempDsId.value || hiddenIds.length === 0) return
    try {
      const res = (await deps.api.resolveResourceAncestors(
        tempDsId.value,
        hiddenIds,
      )) as AncestorsApiResponse | null | undefined
      const ancestors = res?.data?.ancestors || res?.ancestors || []
      if (ancestors.length === 0) return
      const expanded = new Set(expandedResourceIds.value)
      for (const id of ancestors) expanded.add(id)
      expandedResourceIds.value = expanded
      await Promise.all(ancestors.map((id) => ensureChildrenLoaded(id)))
    } catch (e: unknown) {
      deps.messageError(extractErrorMessage(e) || deps.t('datasource.resourceLoadFailed'))
    }
  }

  function applyListedResources(list: Resource[]) {
    resources.value = list
    const parentsWithChildren = new Set<string>()
    for (const r of resources.value) {
      if (r.parent_id) parentsWithChildren.add(r.parent_id)
    }
    loadedChildrenIds.value = parentsWithChildren
    loadingChildrenIds.value = new Set<string>()
    treeFullyLoaded.value = parentsWithChildren.size > 0
    expandedResourceIds.value = new Set(
      resources.value
        .filter((r) => !r.parent_id && r.has_children && parentsWithChildren.has(r.external_id))
        .map((r) => r.external_id),
    )
    if (deps.isEdit.value && !treeFullyLoaded.value) {
      const loaded = new Set(resources.value.map((r) => r.external_id))
      const hidden = selectedResourceIds.value.filter((id) => !loaded.has(id))
      if (hidden.length > 0) void revealExistingSelections(hidden)
    }
  }

  async function loadDriveRoot() {
    const token = extractDriveFolderToken(driveFolderToken.value)
    if (!token) {
      driveFolderTokenError.value = deps.t('datasource.drive.folderTokenRequired')
      return
    }
    driveFolderTokenError.value = ''
    driveFolderToken.value = token
    deps.form.value.config.resource_ids = [token]
    driveRootLoaded.value = false
    loadingResources.value = true
    try {
      const dsId = await ensureTempDataSource(true)
      if (!dsId) return
      const res = await deps.api.listResources(dsId)
      const list = unwrapResources(res)
      resources.value = list
      if (list.length > 0) {
        applyListedResources(list)
        driveRootLoaded.value = true
      }
    } catch (e: unknown) {
      deps.messageError(classifyDriveLoadError(e))
    } finally {
      loadingResources.value = false
    }
  }

  async function loadResources() {
    loadingResources.value = true
    try {
      deps.syncConfluencePublicFieldsToSettings()
      const dsId = await ensureTempDataSource()
      if (!dsId) return
      const res = await deps.api.listResources(dsId)
      applyListedResources(unwrapResources(res))
    } catch (e: unknown) {
      deps.messageError(extractErrorMessage(e) || deps.t('datasource.resourceLoadFailed'))
    } finally {
      loadingResources.value = false
    }
  }

  const childrenMap = computed(() => {
    const map = new Map<string, Resource[]>()
    for (const r of resources.value) {
      if (r.parent_id) {
        const siblings = map.get(r.parent_id)
        if (siblings) siblings.push(r)
        else map.set(r.parent_id, [r])
      }
    }
    return map
  })

  const parentMap = computed(() => {
    const map = new Map<string, string>()
    for (const r of resources.value) {
      if (r.parent_id) map.set(r.external_id, r.parent_id)
    }
    return map
  })

  const checkStates = computed(() => {
    const states = new Map<string, CheckState>()
    const cover = new Set(selectedResourceIds.value)

    function walk(node: Resource, ancestorChecked: boolean): boolean {
      const selfChecked = ancestorChecked || cover.has(node.external_id)
      let descendantChecked = false
      for (const c of childrenMap.value.get(node.external_id) || []) {
        if (walk(c, selfChecked)) descendantChecked = true
      }
      if (selfChecked) states.set(node.external_id, 'checked')
      else states.set(node.external_id, descendantChecked ? 'indeterminate' : 'unchecked')
      return selfChecked || descendantChecked
    }

    for (const r of resources.value) {
      if (!r.parent_id) walk(r, false)
    }
    return states
  })

  const visibleTree = computed<VisibleTreeRow[]>(() => {
    const roots = resources.value.filter((r) => !r.parent_id)
    const result: VisibleTreeRow[] = []
    function walk(items: Resource[], depth: number) {
      for (const r of items) {
        result.push({ resource: r, depth })
        if (r.has_children && expandedResourceIds.value.has(r.external_id)) {
          walk(childrenMap.value.get(r.external_id) || [], depth + 1)
        }
        if (
          r.metadata?.hierarchy_limitation === 'cloud_top_level_containers' &&
          (expandedResourceIds.value.has(r.external_id) ||
            (!r.has_children && loadedChildrenIds.value.has(r.external_id)))
        ) {
          result.push({ resource: r, depth: depth + 1, noticeAfter: true })
        }
      }
    }
    walk(roots, 0)
    return result
  })

  function toggleExpand(id: string) {
    const next = new Set(expandedResourceIds.value)
    if (next.has(id)) {
      next.delete(id)
      expandedResourceIds.value = next
      return
    }
    next.add(id)
    expandedResourceIds.value = next
    void ensureChildrenLoaded(id)
  }

  function getDescendantIds(id: string): string[] {
    const ids: string[] = []
    for (const c of childrenMap.value.get(id) || []) {
      ids.push(c.external_id)
      ids.push(...getDescendantIds(c.external_id))
    }
    return ids
  }

  function getAncestorChain(id: string): string[] {
    const chain = [id]
    for (let p = parentMap.value.get(id); p; p = parentMap.value.get(p)) {
      chain.push(p)
    }
    return chain
  }

  function isCovered(id: string, cover: Set<string>): boolean {
    for (let cur: string | undefined = id; cur; cur = parentMap.value.get(cur)) {
      if (cover.has(cur)) return true
    }
    return false
  }

  function checkResource(id: string, cover: Set<string>) {
    if (isCovered(id, cover)) return
    const descendants = new Set(getDescendantIds(id))
    for (const d of [...cover]) {
      if (descendants.has(d)) cover.delete(d)
    }
    cover.add(id)
  }

  function uncheckResource(id: string, cover: Set<string>) {
    const chain = getAncestorChain(id)
    let highestIdx = -1
    for (let i = chain.length - 1; i >= 0; i--) {
      if (cover.has(chain[i])) {
        highestIdx = i
        break
      }
    }
    if (highestIdx > 0) {
      cover.delete(chain[highestIdx])
      for (let i = highestIdx; i > 0; i--) {
        const parent = chain[i]
        const next = chain[i - 1]
        for (const sib of childrenMap.value.get(parent) || []) {
          if (sib.external_id !== next) cover.add(sib.external_id)
        }
      }
    }
    cover.delete(id)
    const descendants = new Set(getDescendantIds(id))
    for (const d of [...cover]) {
      if (descendants.has(d)) cover.delete(d)
    }
  }

  function toggleResource(id: string) {
    const cover = new Set(selectedResourceIds.value)
    const seafile = isSeafileConnector(deps.form.value.type)
    if ((checkStates.value.get(id) || 'unchecked') === 'unchecked') {
      checkResource(id, cover)
    } else {
      uncheckResource(id, cover)
      if (seafile || deps.form.value.type === 'nextcloud') {
        for (const sel of [...cover]) {
          if (seafile ? seafileWithin(sel, id) : id === '/' || sel === id || sel.startsWith(id + '/')) {
            cover.delete(sel)
          }
        }
      }
    }
    if (seafile && new Set([...cover].map(seafileLibraryOf)).size > 1) {
      deps.messageWarning(deps.t('datasource.seafile.singleLibraryOnly'))
      return
    }
    selectedResourceIds.value = [...cover]
  }

  function expandAllNodes() {
    const expandable = resources.value.filter((r) => r.has_children)
    expandedResourceIds.value = new Set(expandable.map((r) => r.external_id))
    for (const r of expandable) {
      void ensureChildrenLoaded(r.external_id)
    }
  }

  function collapseAllNodes() {
    expandedResourceIds.value = new Set()
  }

  function resourceRowState(id: string): CheckState {
    return checkStates.value.get(id) || 'unchecked'
  }

  const selectedResourceCount = computed(() => {
    let count = 0
    for (const state of checkStates.value.values()) {
      if (state === 'checked') count++
    }
    return count
  })

  const hasExpandableNodes = computed(() => resources.value.some((r) => r.has_children))

  return {
    resources,
    loadingResources,
    selectedResourceIds,
    expandedResourceIds,
    loadedChildrenIds,
    loadingChildrenIds,
    treeFullyLoaded,
    driveFolderToken,
    driveFolderTokenError,
    driveRootLoaded,
    tempDsId,
    visibleTree,
    checkStates,
    selectedResourceCount,
    hasExpandableNodes,
    markTempCredentialsDirty,
    bumpSession,
    cleanupOrphanTempDataSource,
    resetTreeState,
    ensureTempDataSource,
    ensureChildrenLoaded,
    loadDriveRoot,
    loadResources,
    toggleExpand,
    toggleResource,
    expandAllNodes,
    collapseAllNodes,
    resourceRowState,
  }
}
