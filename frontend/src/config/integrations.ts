import type { DeploymentCapabilityKey } from './deploymentCapabilities'

export function getChromeExtensionUrl(): string {
  if (typeof window === 'undefined') return ''
  return String((window as any).__RUNTIME_CONFIG__?.GENYEE_CHROME_EXTENSION_URL || '').trim()
}

export function getClawhubSkillUrl(): string {
  if (typeof window === 'undefined') return ''
  return String((window as any).__RUNTIME_CONFIG__?.GENYEE_CLAWHUB_SKILL_URL || '').trim()
}

export const CHROME_EXTENSION_URL = getChromeExtensionUrl()

export const CLAWHUB_SKILL_URL = getClawhubSkillUrl()

export type IntegrationTab = 'im' | 'embed' | 'api' | 'mcpserver' | 'cli' | 'chrome' | 'claw'

export const INTEGRATION_TABS: IntegrationTab[] = ['im', 'embed', 'api', 'mcpserver', 'cli', 'chrome', 'claw']

export function isIntegrationTabEnabled(tab: IntegrationTab): boolean {
  if (tab === 'chrome') return Boolean(getChromeExtensionUrl())
  if (tab === 'claw') return Boolean(getClawhubSkillUrl())
  return true
}

/** Aligns with Settings.vue SECTION_MIN_ROLE.api and router.go g.Owner() on /api-principal-config. */
export type IntegrationTabRole = 'viewer' | 'contributor' | 'admin' | 'owner'

export const INTEGRATION_TAB_MIN_ROLE: Partial<Record<IntegrationTab, IntegrationTabRole>> = {
  api: 'owner',
}

export const INTEGRATION_TAB_CAPABILITY: Partial<Record<IntegrationTab, DeploymentCapabilityKey>> = {
  im: 'integrations.im',
  embed: 'integrations.embed',
  api: 'integrations.api',
  mcpserver: 'integrations.mcpserver',
}

export type IntegrationPreviewIcon =
  | { type: 'icon'; name: string }
  | { type: 'emoji'; value: string }

/** Sidebar hover preview + Integrations modal nav — add new entries here. */
export const INTEGRATION_PREVIEW_ITEMS: Array<{
  key: IntegrationTab
  icon: IntegrationPreviewIcon
}> = [
  { key: 'im', icon: { type: 'icon', name: 'chat-message' } },
  { key: 'embed', icon: { type: 'icon', name: 'code' } },
  { key: 'api', icon: { type: 'icon', name: 'secured' } },
  { key: 'mcpserver', icon: { type: 'icon', name: 'tools' } },
  { key: 'cli', icon: { type: 'icon', name: 'code' } },
  { key: 'chrome', icon: { type: 'icon', name: 'extension' } },
  { key: 'claw', icon: { type: 'emoji', value: '🦞' } },
]

export const INTEGRATION_ITEMS = INTEGRATION_PREVIEW_ITEMS
