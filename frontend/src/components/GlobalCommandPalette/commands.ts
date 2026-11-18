import type { Router } from 'vue-router'
import type { Composer } from 'vue-i18n'
import { openNewUserGuide } from '@/config/contextualGuides'
import { useAssistantStore } from '@/stores/assistant'
import { useSettingsStore } from '@/stores/settings'

/**
 * A single command that can be searched and invoked from the palette.
 * Kept intentionally minimal — commands are "just do something now", not
 * entities with detail pages.
 */
export interface CmdkCommand {
  id: string
  /** Localized display label. */
  label: string
  /** TDesign icon name, or "assistant" for the shared assistant icon. */
  icon: string
  /** Extra tokens used purely for fuzzy matching (aliases, synonyms). */
  keywords?: string[]
  /** Executed on primary action. Should close the palette itself if needed. */
  run: () => void
}

export interface CommandContext {
  router: Router
  t: Composer['t']
  /** Closes the palette; typically wires to commandPaletteStore.closePalette. */
  close: () => void
}

function assistantName(t: Composer['t']): string {
  return useAssistantStore().displayName || t('assistant.title')
}

function splitKeywords(raw: string, ...extra: string[]): string[] {
  return [...raw.split(',').map((s) => s.trim()), ...extra.map((s) => s.trim())].filter(Boolean)
}

/**
 * Build the flat command list. Commands are intentionally static — dynamic
 * entities (KBs / agents / sessions) live in their own result groups.
 */
export function buildCommands(ctx: CommandContext): CmdkCommand[] {
  const { router, t, close } = ctx
  return [
    {
      id: 'open-assistant',
      label: t('commandPalette.quick.assistant', { name: assistantName(t) }),
      icon: 'assistant',
      keywords: splitKeywords(t('commandPalette.keywords.assistant'), useAssistantStore().displayName),
      run: () => {
        close()
        useAssistantStore().open()
      },
    },
    {
      id: 'new-chat',
      label: t('commandPalette.quick.newChat'),
      icon: 'chat-add',
      keywords: splitKeywords(t('commandPalette.keywords.newChat')),
      run: () => {
        close()
        // 当前已在新对话页时重复点击命令不会触发路由守卫。
        if (router.currentRoute.value.name === 'globalCreatChat') useSettingsStore().clearChatReferences()
        router.push('/platform/creatChat')
      },
    },
    {
      id: 'open-kb-list',
      label: t('commandPalette.quick.knowledgeBases'),
      icon: 'folder',
      keywords: splitKeywords(t('commandPalette.keywords.knowledgeBases')),
      run: () => {
        close()
        router.push('/platform/knowledge-bases')
      },
    },
    {
      id: 'open-agents',
      label: t('commandPalette.quick.agents'),
      icon: 'user-circle',
      keywords: splitKeywords(t('commandPalette.keywords.agents')),
      run: () => {
        close()
        router.push('/platform/agents')
      },
    },
    {
      id: 'open-organizations',
      label: t('commandPalette.quick.organizations'),
      icon: 'usergroup',
      keywords: splitKeywords(t('commandPalette.keywords.organizations')),
      run: () => {
        close()
        router.push('/platform/organizations')
      },
    },
    {
      id: 'open-settings',
      label: t('commandPalette.quick.settings'),
      icon: 'setting',
      keywords: splitKeywords(t('commandPalette.keywords.settings')),
      run: () => {
        close()
        router.push('/platform/settings')
      },
    },
    {
      id: 'open-product-tour',
      label: t('commandPalette.quick.productTour'),
      icon: 'help-circle',
      keywords: splitKeywords(t('commandPalette.keywords.productTour')),
      run: () => {
        close()
        openNewUserGuide()
      },
    },
  ]
}

/**
 * Filter commands by a free-text query. Matches on label OR any keyword,
 * case-insensitive. Empty query returns the full list unchanged so it can
 * double as a "default" empty-state listing.
 */
export function filterCommands(commands: CmdkCommand[], query: string): CmdkCommand[] {
  const q = (query || '').trim().toLowerCase()
  if (!q) return commands
  return commands.filter((cmd) => {
    if (cmd.label.toLowerCase().includes(q)) return true
    if (cmd.keywords) {
      for (const kw of cmd.keywords) {
        if (kw.toLowerCase().includes(q)) return true
      }
    }
    return false
  })
}
