import { ref, reactive, computed, watch, nextTick, type ComputedRef, type Ref } from 'vue'
import type { AgentType, AgentTypePreset, PlaceholderDefinition } from '@/api/agent'
import type { PromptTemplate } from '@/api/system'
import { getRootZoom, rectToCssPx } from '../../../utils/zoom'
import type { AgentEditorFormData } from './useAgentEditorForm'

export type PromptFieldType =
  | 'system'
  | 'context'
  | 'intent'
  | 'rewriteSystem'
  | 'rewriteUser'
  | 'fallback'

export interface PromptPlaceholderData {
  system_prompt: PlaceholderDefinition[]
  agent_system_prompt: PlaceholderDefinition[]
  context_template: PlaceholderDefinition[]
  rewrite_system_prompt: PlaceholderDefinition[]
  rewrite_prompt: PlaceholderDefinition[]
  fallback_prompt: PlaceholderDefinition[]
}

export interface PromptPopupEntry {
  show: boolean
  selectedIndex: number
  prefix: string
  style: { top: string; left: string }
}

export interface PromptNavItem {
  key: string
  label: string
  customized?: boolean
}

const PROMPT_FIELD_TYPES: PromptFieldType[] = [
  'system',
  'context',
  'intent',
  'rewriteSystem',
  'rewriteUser',
  'fallback',
]

function resolveTextareaElement(candidate: unknown): HTMLTextAreaElement | null {
  if (!candidate || typeof candidate !== 'object') return null
  if (typeof HTMLTextAreaElement !== 'undefined' && candidate instanceof HTMLTextAreaElement) {
    return candidate
  }
  const maybeEl = (candidate as { $el?: unknown }).$el
  if (maybeEl && typeof maybeEl === 'object' && 'querySelector' in maybeEl) {
    const queryFn = (maybeEl as { querySelector?: (sel: string) => Element | null }).querySelector
    if (typeof queryFn === 'function') {
      const found = queryFn.call(maybeEl, 'textarea')
      return typeof HTMLTextAreaElement !== 'undefined' && found instanceof HTMLTextAreaElement
        ? found
        : (found as HTMLTextAreaElement | null)
    }
  }
  return null
}

export interface UseAgentPromptPlaceholdersOptions {
  props: {
    readonly visible: boolean
    readonly readOnly?: boolean
  }
  formData: Ref<AgentEditorFormData>
  currentSection: Ref<string>
  isAgentMode: ComputedRef<boolean>
  hasKnowledgeBase: ComputedRef<boolean>
  agentType: ComputedRef<AgentType>
  agentTypePresets: Ref<AgentTypePreset[]>
  agentSystemPromptTemplates: Ref<PromptTemplate[]>
  intentPromptTemplates: Ref<PromptTemplate[]>
  t: (key: string, params?: Record<string, unknown>) => string
}

export function useAgentPromptPlaceholders(options: UseAgentPromptPlaceholdersOptions) {
  const {
    props,
    formData,
    currentSection,
    isAgentMode,
    hasKnowledgeBase,
    agentType,
    agentTypePresets,
    agentSystemPromptTemplates,
    intentPromptTemplates,
    t,
  } = options

  const placeholderData = ref<PromptPlaceholderData>({
    system_prompt: [],
    agent_system_prompt: [],
    context_template: [],
    rewrite_system_prompt: [],
    rewrite_prompt: [],
    fallback_prompt: [],
  })

  const availablePlaceholders = computed(() =>
    isAgentMode.value ? placeholderData.value.agent_system_prompt : placeholderData.value.system_prompt,
  )
  const contextTemplatePlaceholders = computed(() => placeholderData.value.context_template)
  const rewriteSystemPlaceholders = computed(() => placeholderData.value.rewrite_system_prompt)
  const rewritePlaceholders = computed(() => placeholderData.value.rewrite_prompt)
  const fallbackPlaceholders = computed(() => placeholderData.value.fallback_prompt)

  const selectedIntent = ref('')
  const intentEditorValue = ref('')
  const intentPromptsSyncing = ref(false)

  const currentIntentTemplate = computed(() =>
    intentPromptTemplates.value.find((template) => template.id === selectedIntent.value),
  )
  const currentIntentTemplateDesc = computed(() => currentIntentTemplate.value?.description || '')

  const isIntentCustomized = (intentId: string): boolean => {
    const overrides = formData.value.config.intent_prompts || {}
    const override = overrides[intentId]
    if (!override?.trim()) return false
    const template = intentPromptTemplates.value.find((item) => item.id === intentId)
    return override.trim() !== (template?.content || '').trim()
  }

  const hasAnyIntentCustomized = computed(() =>
    intentPromptTemplates.value.some((item) => isIntentCustomized(item.id)),
  )

  const activePromptAnchor = ref('system')

  const showRewritePrompts = computed(
    () =>
      !isAgentMode.value &&
      formData.value.config.multi_turn_enabled &&
      formData.value.config.enable_rewrite,
  )

  const promptNavItems = computed<PromptNavItem[]>(() => {
    const items: PromptNavItem[] = [
      {
        key: 'system',
        label: t('agentEditor.promptNav.system'),
        customized: Boolean(formData.value.config.system_prompt?.trim()),
      },
    ]
    if (!isAgentMode.value) {
      items.push(
        {
          key: 'context',
          label: t('agentEditor.promptNav.context'),
          customized: Boolean(formData.value.config.context_template?.trim()),
        },
        {
          key: 'intent',
          label: t('agentEditor.promptNav.intent'),
          customized: hasAnyIntentCustomized.value,
        },
      )
      if (showRewritePrompts.value) {
        items.push(
          {
            key: 'rewrite-system',
            label: t('agentEditor.promptNav.rewriteSystem'),
            customized: Boolean(formData.value.config.rewrite_prompt_system?.trim()),
          },
          {
            key: 'rewrite-user',
            label: t('agentEditor.promptNav.rewriteUser'),
            customized: Boolean(formData.value.config.rewrite_prompt_user?.trim()),
          },
        )
      }
      if (hasKnowledgeBase.value) {
        items.push({
          key: 'fallback',
          label: t('agentEditor.promptNav.fallback'),
        })
      }
    }
    return items
  })

  const syncActivePromptAnchor = () => {
    const items = promptNavItems.value
    if (!items.length) return
    if (!items.some((item) => item.key === activePromptAnchor.value)) {
      activePromptAnchor.value = items[0].key
    }
  }

  watch(promptNavItems, syncActivePromptAnchor)
  watch(currentSection, (section) => {
    if (section === 'prompts') syncActivePromptAnchor()
  })

  const promptPopups = reactive<Record<PromptFieldType, PromptPopupEntry>>({
    system: { show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' } },
    context: { show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' } },
    intent: { show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' } },
    rewriteSystem: { show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' } },
    rewriteUser: { show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' } },
    fallback: { show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' } },
  })

  const popupTimers: Record<PromptFieldType, ReturnType<typeof setTimeout> | null> = {
    system: null,
    context: null,
    intent: null,
    rewriteSystem: null,
    rewriteUser: null,
    fallback: null,
  }

  const textareaRefs: Record<PromptFieldType, unknown> = {
    system: null,
    context: null,
    intent: null,
    rewriteSystem: null,
    rewriteUser: null,
    fallback: null,
  }
  const boundKeydownElements = new WeakSet<HTMLTextAreaElement>()

  const setPromptTextareaRef = (type: PromptFieldType, el: unknown) => {
    textareaRefs[type] = el
    const textarea = resolveTextareaElement(el)
    if (textarea) bindTextareaKeydown(type, textarea)
  }

  const getPlaceholdersForField = (type: PromptFieldType): PlaceholderDefinition[] => {
    switch (type) {
      case 'system':
        return availablePlaceholders.value
      case 'context':
        return contextTemplatePlaceholders.value
      case 'intent':
        return placeholderData.value.system_prompt
      case 'rewriteSystem':
        return rewriteSystemPlaceholders.value
      case 'rewriteUser':
        return rewritePlaceholders.value
      case 'fallback':
        return fallbackPlaceholders.value
    }
  }

  const getFilteredPlaceholders = (type: PromptFieldType): PlaceholderDefinition[] => {
    const base = getPlaceholdersForField(type)
    const prefix = promptPopups[type].prefix.toLowerCase()
    if (!prefix) return base
    return base.filter((p) => p.name.toLowerCase().startsWith(prefix))
  }

  const filteredPlaceholdersByType = computed<Record<PromptFieldType, PlaceholderDefinition[]>>(() => ({
    system: getFilteredPlaceholders('system'),
    context: getFilteredPlaceholders('context'),
    intent: getFilteredPlaceholders('intent'),
    rewriteSystem: getFilteredPlaceholders('rewriteSystem'),
    rewriteUser: getFilteredPlaceholders('rewriteUser'),
    fallback: getFilteredPlaceholders('fallback'),
  }))

  const getFieldValue = (type: PromptFieldType): string => {
    switch (type) {
      case 'system':
        return formData.value.config.system_prompt || ''
      case 'context':
        return formData.value.config.context_template || ''
      case 'intent':
        return intentEditorValue.value || ''
      case 'rewriteSystem':
        return formData.value.config.rewrite_prompt_system || ''
      case 'rewriteUser':
        return formData.value.config.rewrite_prompt_user || ''
      case 'fallback':
        return formData.value.config.fallback_prompt || ''
    }
  }

  const setFieldValue = (type: PromptFieldType, value: string) => {
    switch (type) {
      case 'system':
        formData.value.config.system_prompt = value
        break
      case 'context':
        formData.value.config.context_template = value
        break
      case 'intent':
        intentEditorValue.value = value
        break
      case 'rewriteSystem':
        formData.value.config.rewrite_prompt_system = value
        break
      case 'rewriteUser':
        formData.value.config.rewrite_prompt_user = value
        break
      case 'fallback':
        formData.value.config.fallback_prompt = value
        break
    }
  }

  const calculateCaretPosition = (textarea: HTMLTextAreaElement, fieldValue: string) => {
    if (typeof window === 'undefined' || typeof document === 'undefined') {
      return { top: 0, left: 0 }
    }
    const cursorPos = textarea.selectionStart
    const textBeforeCursor = fieldValue.substring(0, cursorPos)
    const lines = textBeforeCursor.split('\n')
    const currentLine = lines.length - 1
    const currentLineText = lines[currentLine]

    const textareaRect = rectToCssPx(textarea.getBoundingClientRect(), getRootZoom())
    const style = window.getComputedStyle(textarea)
    const lineHeight = parseFloat(style.lineHeight) || 20
    const paddingTop = parseFloat(style.paddingTop) || 0
    const paddingLeft = parseFloat(style.paddingLeft) || 0

    const span = document.createElement('span')
    span.style.font = style.font
    span.style.visibility = 'hidden'
    span.style.position = 'absolute'
    span.style.whiteSpace = 'pre'
    span.textContent = currentLineText
    document.body.appendChild(span)
    const textWidth = span.offsetWidth
    document.body.removeChild(span)

    const top = textareaRect.top + paddingTop + currentLine * lineHeight - textarea.scrollTop + lineHeight + 4
    const left = textareaRect.left + paddingLeft + textWidth - textarea.scrollLeft
    return { top, left }
  }

  const checkAndShowPopup = (type: PromptFieldType) => {
    const textarea = resolveTextareaElement(textareaRefs[type])
    if (!textarea) return
    const fieldValue = getFieldValue(type)
    const cursorPos = textarea.selectionStart
    const textBeforeCursor = fieldValue.substring(0, cursorPos)

    let lastOpenPos = -1
    for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
      if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
        const textAfterOpen = textBeforeCursor.substring(i + 1)
        if (!textAfterOpen.includes('}}')) {
          lastOpenPos = i - 1
          break
        }
      }
    }

    const popup = promptPopups[type]
    if (lastOpenPos === -1) {
      popup.show = false
      popup.prefix = ''
      return
    }

    popup.prefix = textBeforeCursor.substring(lastOpenPos + 2)
    const filtered = getFilteredPlaceholders(type)
    if (filtered.length > 0) {
      void nextTick(() => {
        const position = calculateCaretPosition(textarea, fieldValue)
        popup.style = { top: `${position.top}px`, left: `${position.left}px` }
        popup.show = true
        popup.selectedIndex = 0
      })
    } else {
      popup.show = false
    }
  }

  const handlePromptFieldInput = (type: PromptFieldType) => {
    const existing = popupTimers[type]
    if (existing) clearTimeout(existing)
    popupTimers[type] = setTimeout(() => {
      popupTimers[type] = null
      checkAndShowPopup(type)
    }, 50)
  }

  const insertPromptPlaceholder = (type: PromptFieldType, placeholderName: string, fromPopup = false) => {
    const textarea = resolveTextareaElement(textareaRefs[type])
    if (!textarea) return
    const popup = promptPopups[type]
    popup.show = false
    popup.prefix = ''
    popup.selectedIndex = 0

    void nextTick(() => {
      const cursorPos = textarea.selectionStart
      const currentValue = getFieldValue(type)
      const textBeforeCursor = currentValue.substring(0, cursorPos)
      const textAfterCursor = currentValue.substring(cursorPos)

      if (fromPopup) {
        let lastOpenPos = -1
        for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
          if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
            lastOpenPos = i - 1
            break
          }
        }
        if (lastOpenPos !== -1) {
          const textBeforeOpen = currentValue.substring(0, lastOpenPos)
          const newValue = `${textBeforeOpen}{{${placeholderName}}}${textAfterCursor}`
          setFieldValue(type, newValue)
          void nextTick(() => {
            const newCursorPos = textBeforeOpen.length + placeholderName.length + 4
            textarea.setSelectionRange(newCursorPos, newCursorPos)
            textarea.focus()
          })
          return
        }
      }

      const newValue = `${textBeforeCursor}{{${placeholderName}}}${textAfterCursor}`
      setFieldValue(type, newValue)
      void nextTick(() => {
        const newCursorPos = cursorPos + placeholderName.length + 4
        textarea.setSelectionRange(newCursorPos, newCursorPos)
        textarea.focus()
      })
    })
  }

  const handlePlaceholderClick = (type: PromptFieldType, placeholderName: string) => {
    insertPromptPlaceholder(type, placeholderName, false)
  }

  function bindTextareaKeydown(type: PromptFieldType, textarea: HTMLTextAreaElement) {
    if (boundKeydownElements.has(textarea)) return
    boundKeydownElements.add(textarea)
    textarea.addEventListener(
      'keydown',
      (e: KeyboardEvent) => {
        const popup = promptPopups[type]
        const filtered = getFilteredPlaceholders(type)
        if (!popup.show || filtered.length === 0) return
        if (e.key === 'ArrowDown') {
          e.preventDefault()
          e.stopPropagation()
          popup.selectedIndex = popup.selectedIndex < filtered.length - 1 ? popup.selectedIndex + 1 : 0
        } else if (e.key === 'ArrowUp') {
          e.preventDefault()
          e.stopPropagation()
          popup.selectedIndex = popup.selectedIndex > 0 ? popup.selectedIndex - 1 : filtered.length - 1
        } else if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault()
          e.stopPropagation()
          const selected = filtered[popup.selectedIndex]
          if (selected) insertPromptPlaceholder(type, selected.name, true)
        } else if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          popup.show = false
          popup.prefix = ''
        }
      },
      true,
    )
  }

  const setupAllPromptTextareaListeners = () => {
    void nextTick(() => {
      for (const type of PROMPT_FIELD_TYPES) {
        const textarea = resolveTextareaElement(textareaRefs[type])
        if (textarea) bindTextareaKeydown(type, textarea)
      }
    })
  }

  watch(
    () => props.visible,
    (val) => {
      if (val) setupAllPromptTextareaListeners()
    },
  )

  const closeAllPlaceholderPopups = () => {
    for (const type of PROMPT_FIELD_TYPES) {
      promptPopups[type].show = false
    }
  }

  const clearAllPlaceholderTimers = () => {
    for (const type of PROMPT_FIELD_TYPES) {
      const timer = popupTimers[type]
      if (timer) {
        clearTimeout(timer)
        popupTimers[type] = null
      }
    }
  }

  const hasOpenPlaceholderPopup = () => PROMPT_FIELD_TYPES.some((type) => promptPopups[type].show)

  const syncIntentEditorFromSelection = () => {
    const key = selectedIntent.value
    if (!key) {
      intentEditorValue.value = ''
      return
    }
    const overrides = formData.value.config.intent_prompts || {}
    intentEditorValue.value = overrides[key] ?? currentIntentTemplate.value?.content ?? ''
  }

  watch(selectedIntent, () => {
    promptPopups.intent.show = false
    promptPopups.intent.prefix = ''
    syncIntentEditorFromSelection()
  })

  watch(
    () => intentPromptTemplates.value,
    (templates) => {
      if (!selectedIntent.value && templates.length > 0) {
        selectedIntent.value = templates[0].id
      } else if (selectedIntent.value) {
        syncIntentEditorFromSelection()
      }
    },
    { immediate: true },
  )

  watch(intentEditorValue, (value) => {
    const key = selectedIntent.value
    if (!key || intentPromptsSyncing.value) return
    const defaultContent = currentIntentTemplate.value?.content || ''
    const next = value.trim()
    if (!next || next === defaultContent.trim()) {
      if (formData.value.config.intent_prompts) {
        const { [key]: _removed, ...rest } = formData.value.config.intent_prompts
        if (Object.keys(rest).length === 0) {
          delete formData.value.config.intent_prompts
        } else {
          formData.value.config.intent_prompts = rest
        }
      }
      return
    }
    formData.value.config.intent_prompts = {
      ...(formData.value.config.intent_prompts || {}),
      [key]: value,
    }
  })

  watch(
    () => formData.value.config.intent_prompts,
    () => {
      intentPromptsSyncing.value = true
      syncIntentEditorFromSelection()
      intentPromptsSyncing.value = false
    },
    { deep: true },
  )

  const resetCurrentIntentPrompt = () => {
    const key = selectedIntent.value
    if (!key || !formData.value.config.intent_prompts) return
    const { [key]: _removed, ...rest } = formData.value.config.intent_prompts
    if (Object.keys(rest).length === 0) {
      delete formData.value.config.intent_prompts
    } else {
      formData.value.config.intent_prompts = rest
    }
    syncIntentEditorFromSelection()
  }

  const handleSystemPromptTemplateSelect = (template: PromptTemplate) => {
    formData.value.config.system_prompt = template.content
    formData.value.config.system_prompt_id = template.id
  }

  const handleAgentSystemPromptResetDefault = (fallback: PromptTemplate) => {
    const typeId = agentType.value
    if (typeId && typeId !== 'custom') {
      const preset = agentTypePresets.value.find((p) => p.id === typeId)
      const presetPromptId = preset?.config?.system_prompt_id
      if (presetPromptId) {
        const tmpl = agentSystemPromptTemplates.value.find((item) => item.id === presetPromptId)
        if (tmpl && typeof tmpl.content === 'string') {
          formData.value.config.system_prompt = tmpl.content
          formData.value.config.system_prompt_id = tmpl.id
          return
        }
      }
    }
    formData.value.config.system_prompt = fallback.content
    formData.value.config.system_prompt_id = fallback.id
  }

  const handleContextTemplateSelect = (template: PromptTemplate) => {
    formData.value.config.context_template = template.content
    formData.value.config.context_template_id = template.id
  }

  const handleRewriteTemplateSelect = (template: PromptTemplate) => {
    formData.value.config.rewrite_prompt_system = template.content
    if (template.user) {
      formData.value.config.rewrite_prompt_user = template.user
    }
  }

  const handleFallbackResponseTemplateSelect = (template: PromptTemplate) => {
    formData.value.config.fallback_response = template.content
  }

  const handleFallbackPromptTemplateSelect = (template: PromptTemplate) => {
    formData.value.config.fallback_prompt = template.content
  }

  return {
    placeholderData,
    availablePlaceholders,
    contextTemplatePlaceholders,
    rewriteSystemPlaceholders,
    rewritePlaceholders,
    fallbackPlaceholders,
    activePromptAnchor,
    promptNavItems,
    selectedIntent,
    intentEditorValue,
    currentIntentTemplate,
    currentIntentTemplateDesc,
    isIntentCustomized,
    resetCurrentIntentPrompt,
    promptPopups,
    filteredPlaceholdersByType,
    setPromptTextareaRef,
    handlePromptFieldInput,
    insertPromptPlaceholder,
    handlePlaceholderClick,
    closeAllPlaceholderPopups,
    clearAllPlaceholderTimers,
    hasOpenPlaceholderPopup,
    handleSystemPromptTemplateSelect,
    handleAgentSystemPromptResetDefault,
    handleContextTemplateSelect,
    handleRewriteTemplateSelect,
    handleFallbackResponseTemplateSelect,
    handleFallbackPromptTemplateSelect,
  }
}
