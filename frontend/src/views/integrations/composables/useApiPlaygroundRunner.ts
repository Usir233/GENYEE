import { computed, reactive, ref, watch, type ComputedRef, type Ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { ComposerTranslation } from 'vue-i18n'
import { BUILTIN_SMART_REASONING_ID, type CustomAgent } from '@/api/agent'
import { createAPIPrincipalTestToken, type APIPrincipalMode } from '@/api/tenant'
import { consumeApiPlaygroundSSE } from '../apiPlaygroundSSE'

export type PlaygroundStatus = '' | 'running' | 'success' | 'failed' | 'stopped'

export interface PlaygroundState {
  agent_id: string
  query: string
  external_user_id: string
  signed_token: string
  running: boolean
  session_status: PlaygroundStatus
  chat_status: PlaygroundStatus
  session_response: string
  stream_output: string
  final_answer: string
  error: string
}

interface SessionCreateWirePayload {
  success?: boolean
  message?: string
  error?: { message?: string }
  data?: { id?: string; ID?: string }
}

interface ChatResourcesAgentsLike {
  agents: ReadonlyArray<CustomAgent>
  ensureAgents: () => Promise<unknown>
}

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  if (err && typeof err === 'object' && 'message' in err && typeof (err as { message?: unknown }).message === 'string') {
    return (err as { message: string }).message
  }
  return ''
}

function getErrorName(err: unknown): string {
  if (err && typeof err === 'object' && 'name' in err && typeof (err as { name?: unknown }).name === 'string') {
    return (err as { name: string }).name
  }
  return ''
}

export function useApiPlaygroundRunner(options: {
  t: ComposerTranslation
  tenantId: Ref<number>
  apiKey: Ref<string>
  apiBaseUrl: ComputedRef<string>
  mode: () => APIPrincipalMode
  directHeaderName: ComputedRef<string>
  tokenHeaderName: ComputedRef<string>
  chatResources: ChatResourcesAgentsLike
}) {
  const {
    t,
    tenantId,
    apiKey,
    apiBaseUrl,
    mode,
    directHeaderName,
    tokenHeaderName,
    chatResources,
  } = options

  const agents = ref<CustomAgent[]>([])
  const agentsLoading = ref(false)
  const agentsError = ref('')
  const playgroundDrawerVisible = ref(false)
  const playgroundController = ref<AbortController | null>(null)

  const playground = reactive<PlaygroundState>({
    agent_id: '',
    query: 'hello',
    external_user_id: 'user_123',
    signed_token: '',
    running: false,
    session_status: '',
    chat_status: '',
    session_response: '',
    stream_output: '',
    final_answer: '',
    error: '',
  })

  const agentOptions = computed(() => agents.value.map((agent) => ({
    label: `${agent.name}${agent.is_builtin ? ` · ${t('integrations.api.playgroundBuiltin')}` : ''}`,
    value: agent.id,
  })))

  const externalUserHint = computed(() => {
    const currentMode = mode()
    if (currentMode === 'tenant') return t('integrations.api.playgroundTenantModeHint')
    if (currentMode === 'direct_header') {
      return t('integrations.api.playgroundDirectModeHint', { headerName: directHeaderName.value })
    }
    return t('integrations.api.playgroundSignedModeHint', { headerName: tokenHeaderName.value })
  })

  function buildPlaygroundHeaders(maskSecrets: boolean) {
    const currentMode = mode()
    const commonHeaders: Record<string, string> = {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-API-Key': maskSecrets ? '<API_KEY>' : apiKey.value,
    }
    if (currentMode === 'direct_header' && playground.external_user_id.trim()) {
      commonHeaders[directHeaderName.value] = playground.external_user_id.trim()
    }
    if (currentMode === 'signed_token') {
      commonHeaders[tokenHeaderName.value] = maskSecrets ? '<JWT>' : playground.signed_token.trim()
    }
    return {
      sessionHeaders: commonHeaders,
      chatHeaders: { ...commonHeaders, Accept: 'text/event-stream' },
    }
  }

  const playgroundRequestPreview = computed(() => {
    const body = {
      query: playground.query || '<query>',
      agent_enabled: true,
      agent_id: playground.agent_id || '<agent_id>',
      channel: 'api',
    }
    const headers = buildPlaygroundHeaders(true)
    return [
      'POST /api/v1/sessions',
      JSON.stringify({ headers: headers.sessionHeaders, body: {} }, null, 2),
      '',
      'POST /api/v1/agent-chat/<session_id>',
      JSON.stringify({ headers: headers.chatHeaders, body }, null, 2),
    ].join('\n')
  })

  const playgroundDisabledReason = computed(() => {
    if (playground.running) return ''
    if (!apiKey.value) return t('integrations.api.playgroundNeedApiKey')
    if (!playground.agent_id) return t('integrations.api.playgroundNeedAgent')
    if (!playground.query.trim()) return t('integrations.api.playgroundNeedQuestion')
    if (mode() === 'signed_token' && !playground.external_user_id.trim()) {
      return t('integrations.api.playgroundNeedExternalUser')
    }
    return ''
  })

  const canRunPlayground = computed(() => !playground.running && !playgroundDisabledReason.value)

  const hasPlaygroundResult = computed(() => Boolean(
    playground.signed_token || playground.session_response || playground.stream_output || playground.final_answer,
  ))

  function ensurePlaygroundAgent() {
    if (playground.agent_id && agents.value.some((agent) => agent.id === playground.agent_id)) return
    const smartReasoning = agents.value.find((agent) => agent.id === BUILTIN_SMART_REASONING_ID)
    playground.agent_id = smartReasoning?.id || agents.value[0]?.id || ''
  }

  async function loadAgents() {
    agentsLoading.value = true
    agentsError.value = ''
    try {
      await chatResources.ensureAgents()
      agents.value = [...chatResources.agents]
      ensurePlaygroundAgent()
    } catch (err: unknown) {
      agentsError.value = getErrorMessage(err) || t('integrations.api.playgroundAgentsLoadFailed')
    } finally {
      agentsLoading.value = false
    }
  }

  function stopPlayground() {
    playgroundController.value?.abort()
  }

  watch(playgroundDrawerVisible, (visible) => {
    if (!visible) {
      stopPlayground()
    }
  })

  function openPlaygroundDrawer() {
    ensurePlaygroundAgent()
    playgroundDrawerVisible.value = true
  }

  function handlePlaygroundDrawerCancel() {
    stopPlayground()
    playgroundDrawerVisible.value = false
  }

  function compactText(text: string, max = 12000) {
    if (text.length <= max) return text
    return `${text.slice(0, max)}\n...`
  }

  function formatJSON(value: unknown) {
    try {
      return compactText(JSON.stringify(value, null, 2))
    } catch {
      return String(value)
    }
  }

  async function readResponseBody(resp: Response) {
    const text = await resp.text()
    if (!text) return ''
    try {
      return formatJSON(JSON.parse(text))
    } catch {
      return compactText(text)
    }
  }

  async function ensurePlaygroundSignedToken() {
    if (mode() !== 'signed_token') return
    if (!tenantId.value) throw new Error(t('integrations.api.loadFailed'))
    const resp = await createAPIPrincipalTestToken(tenantId.value, {
      external_user_id: playground.external_user_id.trim(),
      expires_in_seconds: 900,
    })
    if (!resp.success || !resp.data?.token) {
      throw new Error(resp.message || t('integrations.api.playgroundMintTokenFailed'))
    }
    playground.signed_token = resp.data.token
  }

  function createRequestSignal(controller: AbortController, timeoutMs: number): AbortSignal {
    if (typeof AbortSignal !== 'undefined' && typeof AbortSignal.any === 'function' && typeof AbortSignal.timeout === 'function') {
      return AbortSignal.any([controller.signal, AbortSignal.timeout(timeoutMs)])
    }
    return controller.signal
  }

  async function runPlayground() {
    if (!canRunPlayground.value) return
    const controller = new AbortController()
    playgroundController.value = controller
    const signal = createRequestSignal(controller, 120_000)
    playground.running = true
    playground.error = ''
    playground.session_status = 'running'
    playground.chat_status = ''
    playground.session_response = ''
    playground.stream_output = ''
    playground.final_answer = ''
    playground.signed_token = ''

    const startedAt = performance.now()
    try {
      await ensurePlaygroundSignedToken()
      const headers = buildPlaygroundHeaders(false).sessionHeaders
      const sessionResp = await fetch(`${apiBaseUrl.value}/sessions`, {
        method: 'POST',
        headers,
        body: '{}',
        signal,
        credentials: 'omit',
      })
      const sessionRaw = await sessionResp.text()
      let sessionPayload: SessionCreateWirePayload | null = null
      try {
        sessionPayload = sessionRaw ? (JSON.parse(sessionRaw) as SessionCreateWirePayload) : null
      } catch {
        sessionPayload = null
      }
      playground.session_response = sessionPayload ? formatJSON(sessionPayload) : compactText(sessionRaw)
      if (!sessionResp.ok || sessionPayload?.success === false) {
        playground.session_status = 'failed'
        throw new Error(sessionPayload?.message || sessionPayload?.error?.message || `HTTP ${sessionResp.status}`)
      }
      playground.session_status = 'success'
      const sessionID = sessionPayload?.data?.id || sessionPayload?.data?.ID
      if (!sessionID) {
        throw new Error(t('integrations.api.playgroundMissingSessionId'))
      }

      playground.chat_status = 'running'
      const chatResp = await fetch(`${apiBaseUrl.value}/agent-chat/${encodeURIComponent(sessionID)}`, {
        method: 'POST',
        headers: buildPlaygroundHeaders(false).chatHeaders,
        body: JSON.stringify({
          query: playground.query.trim(),
          agent_enabled: true,
          agent_id: playground.agent_id,
          channel: 'api',
        }),
        signal,
        credentials: 'omit',
      })
      if (!chatResp.ok) {
        playground.chat_status = 'failed'
        const body = await readResponseBody(chatResp)
        playground.stream_output = body
        throw new Error(body || `HTTP ${chatResp.status}`)
      }
      if (!chatResp.body) {
        throw new Error(t('integrations.api.playgroundNoStream'))
      }

      const result = await consumeApiPlaygroundSSE(chatResp.body, ({ raw, answer }) => {
        playground.stream_output = compactText(raw)
        playground.final_answer = answer
      })
      playground.stream_output = compactText(result.raw)
      playground.final_answer = result.answer
      if (result.status === 'failed') {
        playground.chat_status = 'failed'
        throw new Error(result.error || t('integrations.api.playgroundFailed'))
      }
      playground.chat_status = 'success'
      MessagePlugin.success(t('integrations.api.playgroundSuccess', {
        ms: Math.round(performance.now() - startedAt),
      }))
    } catch (err: unknown) {
      const errName = getErrorName(err)
      if (errName === 'AbortError') {
        if (playground.session_status === 'running') playground.session_status = 'stopped'
        if (playground.chat_status === 'running') playground.chat_status = 'stopped'
        playground.error = t('integrations.api.playgroundStopped')
      } else {
        if (playground.session_status === 'running') playground.session_status = 'failed'
        if (playground.chat_status === 'running') playground.chat_status = 'failed'
        playground.error = getErrorMessage(err) || t('integrations.api.playgroundFailed')
      }
    } finally {
      playground.running = false
      playgroundController.value = null
    }
  }

  return {
    agents,
    agentsLoading,
    agentsError,
    agentOptions,
    playground,
    playgroundDrawerVisible,
    externalUserHint,
    playgroundRequestPreview,
    playgroundDisabledReason,
    canRunPlayground,
    hasPlaygroundResult,
    loadAgents,
    ensurePlaygroundAgent,
    openPlaygroundDrawer,
    handlePlaygroundDrawerCancel,
    runPlayground,
    stopPlayground,
  }
}
