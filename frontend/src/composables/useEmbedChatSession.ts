import {
  ref,
  reactive,
  watch,
  nextTick,
  onMounted,
  onUnmounted,
  provide,
  type Ref,
} from 'vue'
import { useStream } from '@/api/chat/stream'
import {
  getEmbedMessageList,
  postEmbedMessageSent,
  postEmbedMessageReceived,
  relayEmbedWebhookEvent,
  stopEmbedSession,
} from '@/api/embed'
import { embedToast } from '@/utils/embedToast'
import { buildQueryWithHostContext, stripHostContextFromQuery } from '@/utils/embedContext'
import { fileToDataURI } from '@/utils/embedFile'
import { MAX_FILE_SIZE_BYTES, MAX_FILE_SIZE_MB } from '@/utils/index'
import { inlineUploadsFitRequest } from '@/utils/inlineUploadSize'
import { historyHasMoreAfterAttempt } from '@/utils/historyPagination'
import { useI18n } from 'vue-i18n'
import { useChatStreamHandler, isPromptCard } from '@/composables/useChatStreamHandler'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'
import { useStickyBottomOnResize } from '@/composables/useStickyBottomOnResize'

export function useEmbedChatSession(options: {
  sessionId: Ref<string>
  sessionSig: Ref<string>
  visitorId: Ref<string>
  channelId: string
  token: string
  agentId: string
  kbIds: string[]
  allowWebSearch?: boolean
  allowFileUpload?: boolean
  hostContext?: Ref<Record<string, unknown>>
  onMessagesChange?: (has: boolean) => void
  onSessionTitle?: (title: string) => void
  onTurnComplete?: (message: Record<string, unknown>) => void
  onMessagesLoaded?: (messages: Record<string, unknown>[]) => void
}) {
  const { t } = useI18n()
  const { onChunk, error, startStream, stopStream } = useStream()

  const isAgentStreamSession = () =>
    !!(options.agentId && options.agentId !== 'builtin-quick-answer')

  const limit = ref(20)
  const messagesList = reactive<Record<string, unknown>[]>([])
  watch(
    () => messagesList.length,
    (len) => options.onMessagesChange?.(len > 0),
    { immediate: true },
  )

  const isReplying = ref(false)
  const currentAssistantMessageId = ref('')
  const isFirstEnter = ref(true)
  const loading = ref(false)
  const historyLoading = ref(true)
  const historyLoadingMore = ref(false)
  const hasMoreHistory = ref(true)
  const created_at = ref('')
  const fullContent = ref('')
  let pendingSuggestionAttribution: { suggestion_set_id: string; question_id: string } | null = null
  const scrollContainer = ref<HTMLElement | null>(null)
  const userHasScrolledUp = ref(false)
  const SCROLL_BOTTOM_THRESHOLD = 80

  const isNearBottom = () => {
    if (!scrollContainer.value) return true
    const { scrollTop, scrollHeight, clientHeight } = scrollContainer.value
    return scrollHeight - scrollTop - clientHeight < SCROLL_BOTTOM_THRESHOLD
  }

  const getUserQuery = (index: number) => {
    if (index <= 0) return ''
    const previous = messagesList[index - 1]
    if (previous && previous.role === 'user') {
      return stripHostContextFromQuery(String(previous.content || ''))
    }
    return ''
  }

  const scrollToBottom = (force = false) => {
    if (!force && userHasScrolledUp.value) return
    nextTick(() => {
      if (scrollContainer.value) {
        scrollContainer.value.scrollTop = scrollContainer.value.scrollHeight
      }
    })
  }

  const onClickScrollToBottom = () => {
    userHasScrolledUp.value = false
    scrollToBottom(true)
  }

  useStickyBottomOnResize(scrollContainer, userHasScrolledUp)

  let sessionEpoch = 0
  let disposed = false

  const debounce = <T extends (...args: never[]) => void>(fn: T, delay: number) => {
    let timer: ReturnType<typeof setTimeout> | null = null
    const run = (...args: Parameters<T>) => {
      if (timer !== null) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = null
        fn(...args)
      }, delay)
    }
    run.cancel = () => {
      if (timer !== null) {
        clearTimeout(timer)
        timer = null
      }
    }
    return run
  }

  const notifyEmbedReceived = (content: string) => {
    if (!content?.trim()) return
    postEmbedMessageReceived(options.channelId, options.sessionId.value, content)
  }

  type WebhookType = 'message_sent' | 'message_received'
  const pendingWebhooks = new Set<string>()
  const relayingWebhooks = new Set<string>()
  const relayedWebhooks = new Set<string>()
  const streamAssistantIds = new Map<string, string>()
  const webhookKey = (type: WebhookType, messageId: string, sessionId: string) =>
    `${sessionId}|${type}|${messageId}`
  const relayPersistedMessage = (type: WebhookType, messageId: string, sessionId: string, sessionSig: string) => {
    if (!messageId) return
    const key = webhookKey(type, messageId, sessionId)
    if (relayingWebhooks.has(key) || relayedWebhooks.has(key)) return
    pendingWebhooks.add(key)
    relayingWebhooks.add(key)
    void relayEmbedWebhookEvent(options.channelId, options.token, sessionId, sessionSig, {
      type, message_id: messageId,
    }).then((success) => {
      if (success) {
        pendingWebhooks.delete(key)
        relayedWebhooks.add(key)
      }
    }).finally(() => relayingWebhooks.delete(key))
  }

  // History can settle a turn started by this page after a disconnect/resume.
  // Never replay arbitrary old history: server deduplication is best-effort.
  const recoverPendingWebhook = (message: Record<string, unknown>, sessionId: string, sessionSig: string) => {
    if (!message.is_completed || message.run_state === 'awaiting_input' || !String(message.content || '').trim()) return
    const type = message.role === 'user' ? 'message_sent' : message.role === 'assistant' ? 'message_received' : null
    const messageId = String(message.id || '')
    if (type && pendingWebhooks.has(webhookKey(type, messageId, sessionId))) {
      relayPersistedMessage(type, messageId, sessionId, sessionSig)
    }
  }

  const {
    shouldRenderAssistantMessage,
    shouldShowGlobalTypingIndicator,
    handleMsgList,
    processStreamChunk,
    prepareForNewOutgoingMessage,
    prepareResumedMessage,
    markInFlightAssistantStopped,
  } = useChatStreamHandler({
    messagesList,
    loading,
    isReplying,
    currentAssistantMessageId,
    fullContent,
    isAgentStreamSession,
    scrollToBottom,
    onReplyComplete: notifyEmbedReceived,
    onTurnComplete: options.onTurnComplete,
    onAfterMsgList: () => options.onMessagesLoaded?.(messagesList),
    onError: embedToast,
    isFirstEnter,
    scrollContainer,
  })

  const onChatScrollTop = () => {
    if (historyLoadingMore.value || !hasMoreHistory.value) return
    if (!scrollContainer.value) return
    const { scrollTop, scrollHeight } = scrollContainer.value
    isFirstEnter.value = false
    if (scrollTop <= 0) {
      getmsgList(
        {
          session_id: options.sessionId.value,
          created_at: created_at.value,
          limit: limit.value,
        },
        true,
        scrollHeight,
      )
    }
  }

  const debouncedScrollTop = debounce(onChatScrollTop, 500)

  let lastScrollTop = 0
  const handleScroll = () => {
    const el = scrollContainer.value
    if (el) {
      const currentTop = el.scrollTop
      // Only an actual upward scroll detaches from the live edge. Content that
      // grows after a chunk (images, diagrams) keeps scrollTop fixed and would
      // otherwise fire a stale scroll event that falsely marks the user as
      // scrolled up, killing the auto-follow during streaming.
      if (currentTop < lastScrollTop - 1) {
        userHasScrolledUp.value = !isNearBottom()
      } else if (isNearBottom()) {
        userHasScrolledUp.value = false
      }
      lastScrollTop = currentTop
    }
    debouncedScrollTop()
  }

  const getmsgList = (
    data: { session_id: string; created_at?: string; limit: number },
    isScrollType = false,
    scrollHeight?: number,
  ) => {
    const epoch = sessionEpoch
    const sessionSig = options.sessionSig.value
    const isCurrentRequest = () => !disposed && epoch === sessionEpoch && options.sessionId.value === data.session_id
    if (isScrollType) {
      if (historyLoadingMore.value || !hasMoreHistory.value) return
      historyLoadingMore.value = true
    }

    getEmbedMessageList(
      options.channelId,
      options.token,
      data.session_id,
      data.limit,
      data.created_at || undefined,
      sessionSig,
    )
      .then(async (res) => {
        if (!isCurrentRequest()) return
        const batch = res?.data as Record<string, unknown>[] | undefined
        if (!batch?.length) {
          // No (more) server history. Crucially this also covers the initial
          // load of a brand-new session: leaving hasMoreHistory true here would
          // let a later scroll-to-top re-fetch with an empty cursor (= "latest"),
          // pulling back the just-sent messages and duplicating them.
          hasMoreHistory.value = historyHasMoreAfterAttempt(hasMoreHistory.value, {
            status: 'loaded',
            itemCount: 0,
            pageSize: limit.value,
            cursorAdvanced: false,
          })
          return
        }
        batch.forEach((message) => recoverPendingWebhook(message, data.session_id, sessionSig))
        const nextCursor = String(batch[0].created_at)
        const cursorAdvanced = !(isScrollType && created_at.value && nextCursor === created_at.value)
        hasMoreHistory.value = historyHasMoreAfterAttempt(hasMoreHistory.value, {
          status: 'loaded',
          itemCount: batch.length,
          pageSize: limit.value,
          cursorAdvanced,
        })
        if (!cursorAdvanced) {
          return
        }
        created_at.value = nextCursor
        await handleMsgList(batch, isScrollType, scrollHeight)
      })
      .catch((err) => {
        if (!isCurrentRequest()) return
        console.error('Failed to load messages:', err)
        if (isScrollType) {
          hasMoreHistory.value = historyHasMoreAfterAttempt(hasMoreHistory.value, {
            status: 'failed',
          })
        }
      })
      .finally(() => {
        if (!isCurrentRequest()) return
        historyLoading.value = false
        historyLoadingMore.value = false
      })
  }

  const handleStopGeneration = () => {
    stopStream()
    markInFlightAssistantStopped(currentAssistantMessageId.value)
    const messageId = currentAssistantMessageId.value
    if (messageId) {
      pendingWebhooks.delete(webhookKey('message_received', messageId, options.sessionId.value))
      stopEmbedSession(
        options.channelId,
        options.token,
        options.sessionId.value,
        messageId,
        options.sessionSig.value,
      ).catch((err) => console.error('Failed to stop embed generation:', err))
    }
    loading.value = false
    isReplying.value = false
  }

  const sendMsg = async (
    value: string,
    opts: {
      webSearchEnabled?: boolean
      imageFiles?: File[]
      attachmentFiles?: File[]
      onPrepared?: () => void
    } = {},
  ) => {
    const outboundQuery = buildQueryWithHostContext(value, options.hostContext?.value)
    const visitorWebSearchEnabled = opts.webSearchEnabled ?? false
    const imageFiles = (options.allowFileUpload ? opts.imageFiles : undefined) || []
    const attachmentFiles = (options.allowFileUpload ? opts.attachmentFiles : undefined) || []
    if (!inlineUploadsFitRequest(imageFiles, attachmentFiles, MAX_FILE_SIZE_BYTES, outboundQuery)) {
      embedToast(t('error.fileSizeExceeded', { size: MAX_FILE_SIZE_MB }))
      return
    }
    const sendEpoch = sessionEpoch
    const targetSessionId = options.sessionId.value
    const targetSessionSig = options.sessionSig.value
    stopStream()
    prepareForNewOutgoingMessage()
    isReplying.value = true
    loading.value = true

    const imageAttachments: Array<{ data: string }> = []
    const displayImages: Array<{ url: string }> = []
    const attachmentUploads: Array<{ data: string; file_name: string; file_size: number }> = []
    const displayAttachments: Array<{ file_name: string; file_size: number }> = []
    try {
      for (const file of imageFiles) {
        const dataURI = String(await fileToDataURI(file))
        if (disposed || sendEpoch !== sessionEpoch || options.sessionId.value !== targetSessionId) return
        imageAttachments.push({ data: dataURI })
        displayImages.push({ url: dataURI })
      }
      for (const file of attachmentFiles) {
        const dataURI = String(await fileToDataURI(file))
        if (disposed || sendEpoch !== sessionEpoch || options.sessionId.value !== targetSessionId) return
        attachmentUploads.push({ data: dataURI, file_name: file.name, file_size: file.size })
        displayAttachments.push({ file_name: file.name, file_size: file.size })
      }
    } catch (err) {
      if (disposed || sendEpoch !== sessionEpoch || options.sessionId.value !== targetSessionId) return
      console.error('Failed to read attachment:', err)
      embedToast(t('chat.imageReadFailed'))
      isReplying.value = false
      loading.value = false
      return
    }

    if (disposed || sendEpoch !== sessionEpoch || options.sessionId.value !== targetSessionId) return

    messagesList.push({
      content: value,
      role: 'user',
      mentioned_items: [],
      images: displayImages,
      attachments: displayAttachments,
      channel: 'embed',
      created_at: new Date().toISOString(),
    })
    opts.onPrepared?.()
    postEmbedMessageSent(options.channelId, targetSessionId, value)
    userHasScrolledUp.value = false
    scrollToBottom(true)

    const agentEnabled = isAgentStreamSession()
    const endpoint = agentEnabled
      ? `/api/v1/embed/${options.channelId}/agent-chat`
      : `/api/v1/embed/${options.channelId}/knowledge-chat`

    const suggestionAttribution = pendingSuggestionAttribution
    pendingSuggestionAttribution = null
    await startStream({
      session_id: targetSessionId,
      knowledge_base_ids: options.kbIds,
      knowledge_ids: [],
      agent_enabled: agentEnabled,
      agent_id: options.agentId,
      web_search_enabled: (options.allowWebSearch ?? false) && visitorWebSearchEnabled,
      summary_model_id: '',
      mcp_service_ids: [],
      mentioned_items: [],
      images: imageAttachments.length > 0 ? imageAttachments : undefined,
      attachment_uploads: attachmentUploads.length > 0 ? attachmentUploads : undefined,
      query: outboundQuery,
      suggestion_attribution: suggestionAttribution || undefined,
      method: 'POST',
      url: endpoint,
      embed_token: options.token,
      embed_session_sig: targetSessionSig,
      embed_visitor_id: options.visitorId.value,
    })
  }

  // Embed has no stream to re-attach to, so a resumed run is followed by
  // polling history until its message settles, then reloading it.
  let resumePoll: ReturnType<typeof setTimeout> | null = null
  const stopResumePoll = () => {
    if (resumePoll) clearTimeout(resumePoll)
    resumePoll = null
  }
  const followResumedRun = (pendingId: string) => {
    const sid = options.sessionId.value
    const message = messagesList.find((m) => m.role === 'assistant' && ((m.agentEventStream as Record<string, unknown>[] | undefined) || []).some(
      (e) => isPromptCard(e) && e.pending_id === pendingId,
    ))
    const messageId = String(message?.id || '')
    if (!message || !sid || !messageId || isReplying.value) return
    pendingWebhooks.add(webhookKey('message_received', messageId, sid))
    const sessionSig = options.sessionSig.value
    prepareResumedMessage(message, pendingId)
    isReplying.value = true
    stopResumePoll()
    const deadline = Date.now() + 30 * 60 * 1000
    const poll = async () => {
      if (options.sessionId.value !== sid) return
      try {
        const res = await getEmbedMessageList(
          options.channelId, options.token, sid, limit.value, undefined, sessionSig,
        )
        if (options.sessionId.value !== sid) return
        const latest = (res?.data as Record<string, unknown>[] | undefined)?.find((m) => m.id === messageId)
        if (latest) recoverPendingWebhook(latest, sid, sessionSig)
        if (!latest || latest.is_completed || latest.run_state === 'awaiting_input' || Date.now() > deadline) {
          resetAndLoad(sid)
          return
        }
      } catch (err) {
        console.error('Failed to poll resumed run:', err)
      }
      resumePoll = setTimeout(poll, 2000)
    }
    resumePoll = setTimeout(poll, 1000)
  }
  provide(resumeSuspendedRunKey, followResumedRun)

  watch(error, (newError) => {
    if (newError) {
      embedToast(newError)
      isReplying.value = false
      loading.value = false
      currentAssistantMessageId.value = ''
    }
  })

  onChunk((data) => {
    const chunkSessionId = data.session_id || (data.data as { session_id?: string })?.session_id
    if (chunkSessionId && chunkSessionId !== options.sessionId.value) {
      return
    }
    if (data.response_type === 'session_title') {
      const title = String(data.content || (data.data as { title?: string })?.title || '').trim()
      if (title) {
        options.onSessionTitle?.(title)
      }
      return
    }
    const sessionId = options.sessionId.value
    const sessionSig = options.sessionSig.value
    const metadata = (data.data || {}) as Record<string, unknown>
    const requestId = String(data.id || '')
    const assistantId = String(data.assistant_message_id || metadata.assistant_message_id || streamAssistantIds.get(`${sessionId}|${requestId}`) || '')
    if (data.response_type === 'agent_query') {
      // Both quick-answer and agent streams acknowledge the persisted user row
      // here. Sending before this event races the chat request's DB writes.
      relayPersistedMessage('message_sent', String(metadata.user_message_id || ''), sessionId, sessionSig)
      if (assistantId) {
        streamAssistantIds.set(`${sessionId}|${requestId}`, assistantId)
        const key = webhookKey('message_received', assistantId, sessionId)
        if (!relayedWebhooks.has(key)) pendingWebhooks.add(key)
      }
    }
    if (data.response_type === 'stop') pendingWebhooks.delete(webhookKey('message_received', assistantId, sessionId))
    processStreamChunk(data)
    // answer.done only closes a segment; complete follows the successful DB
    // write. A suspended run still has an incomplete assistant row.
    if (data.response_type === 'complete' && !metadata.suspended &&
        pendingWebhooks.has(webhookKey('message_received', assistantId, sessionId))) {
      relayPersistedMessage('message_received', assistantId, sessionId, sessionSig)
    }
  })

  const resetAndLoad = (sid: string) => {
    sessionEpoch++
    debouncedScrollTop.cancel()
    stopResumePoll()
    stopStream()
    messagesList.splice(0)
    historyLoading.value = true
    historyLoadingMore.value = false
    hasMoreHistory.value = true
    created_at.value = ''
    loading.value = false
    isReplying.value = false
    currentAssistantMessageId.value = ''
    userHasScrolledUp.value = false
    isFirstEnter.value = true
    fullContent.value = ''
    if (!sid) {
      historyLoading.value = false
      return
    }
    getmsgList({ session_id: sid, created_at: '', limit: limit.value })
  }

  watch(
    () => options.sessionId.value,
    (sid) => resetAndLoad(sid),
    { immediate: true },
  )

  onMounted(() => {
    loading.value = false
    isReplying.value = false
  })

  onUnmounted(() => {
    disposed = true
    sessionEpoch++
    debouncedScrollTop.cancel()
    stopResumePoll()
    stopStream()
    fullContent.value = ''
  })

  return {
    messagesList,
    loading,
    isReplying,
    historyLoading,
    scrollContainer,
    userHasScrolledUp,
    isFirstEnter,
    shouldRenderAssistantMessage,
    shouldShowGlobalTypingIndicator,
    getUserQuery,
    handleScroll,
    scrollToBottom,
    onClickScrollToBottom,
    sendMsg,
    handleStopGeneration,
    setSuggestionAttribution: (suggestionSetId: string, questionId: string) => {
      pendingSuggestionAttribution = { suggestion_set_id: suggestionSetId, question_id: questionId }
    },
  }
}
