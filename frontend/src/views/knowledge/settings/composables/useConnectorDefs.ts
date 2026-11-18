import type { Resource } from '@/api/datasource'

export interface ConnectorFieldDef {
  key: string
  labelKey: string
  placeholder: string
  secret?: boolean
  optional?: boolean
  hintKey?: string
  multiline?: boolean
  fieldType?: 'custom_headers'
}

export interface ConnectorDef {
  type: string
  available: boolean
  docUrl: string
  permissionDocUrl: string
  permissionPageUrl: string
  requiredPermissions: string[]
  fields: ConnectorFieldDef[]
}

export interface CustomHeaderItem {
  key: string
  value: string
}

export interface GitLabProjectInput {
  project_id: string
  ref: string
  pathsText: string
}

export interface DataSourceFormState {
  name: string
  type: string
  config: {
    credentials: Record<string, string>
    resource_ids: string[]
    settings: Record<string, unknown>
  }
  sync_schedule: string
  sync_mode: 'incremental' | 'full'
  conflict_strategy: 'overwrite' | 'skip'
  sync_deletions: boolean
}

export type CheckState = 'checked' | 'indeterminate' | 'unchecked'

export interface VisibleTreeRow {
  resource: Resource
  depth: number
  noticeAfter?: boolean
}

export const CONNECTOR_DEFS: ConnectorDef[] = [
  {
    type: 'feishu',
    available: true,
    docUrl: 'https://open.feishu.cn/app',
    permissionDocUrl: 'https://open.feishu.cn/document/server-docs/docs/wiki-v2/wiki-overview',
    permissionPageUrl: 'https://open.feishu.cn/app',
    requiredPermissions: [
      'wiki:wiki:readonly',
      'drive:drive:readonly',
      'drive:export:readonly',
      'docx:document:readonly',
    ],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      {
        key: 'base_url',
        labelKey: 'datasource.field.baseUrl',
        placeholder: 'https://open.feishu.cn',
        optional: true,
        hintKey: 'datasource.field.baseUrlHint',
      },
    ],
  },
  {
    type: 'lark',
    available: true,
    docUrl: 'https://open.larksuite.com/app',
    permissionDocUrl: 'https://open.larksuite.com/document/server-docs/docs/wiki-v2/wiki-overview',
    permissionPageUrl: 'https://open.larksuite.com/app',
    requiredPermissions: [
      'wiki:wiki:readonly',
      'drive:drive:readonly',
      'drive:export:readonly',
      'docx:document:readonly',
    ],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      {
        key: 'base_url',
        labelKey: 'datasource.field.baseUrl',
        placeholder: 'https://open.feishu.cn',
        optional: true,
        hintKey: 'datasource.field.baseUrlHint',
      },
    ],
  },
  {
    type: 'feishu_drive',
    available: true,
    docUrl: 'https://open.feishu.cn/app',
    permissionDocUrl: 'https://open.feishu.cn/document/server-docs/docs/drive-v1/file/list',
    permissionPageUrl: 'https://open.feishu.cn/app',
    requiredPermissions: ['drive:drive:readonly', 'drive:export:readonly', 'docx:document:readonly'],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      {
        key: 'base_url',
        labelKey: 'datasource.field.baseUrl',
        placeholder: 'https://open.feishu.cn',
        optional: true,
        hintKey: 'datasource.field.baseUrlHint',
      },
    ],
  },
  {
    type: 'lark_drive',
    available: true,
    docUrl: 'https://open.larksuite.com/app',
    permissionDocUrl: 'https://open.larksuite.com/document/server-docs/docs/drive-v1/file/list',
    permissionPageUrl: 'https://open.larksuite.com/app',
    requiredPermissions: ['drive:drive:readonly', 'drive:export:readonly', 'docx:document:readonly'],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      {
        key: 'base_url',
        labelKey: 'datasource.field.baseUrl',
        placeholder: 'https://open.larksuite.com',
        optional: true,
        hintKey: 'datasource.field.baseUrlHint',
      },
    ],
  },
  {
    type: 'notion',
    available: true,
    docUrl: 'https://www.notion.so/my-integrations',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      { key: 'api_key', labelKey: 'datasource.field.integrationToken', placeholder: 'ntn_xxxx', secret: true },
    ],
  },
  {
    type: 'confluence',
    available: true,
    docUrl: 'https://developer.atlassian.com/cloud/confluence/rest/',
    permissionDocUrl: 'https://developer.atlassian.com/cloud/confluence/rest/',
    permissionPageUrl: 'https://id.atlassian.com/manage-profile/security/api-tokens',
    requiredPermissions: [],
    fields: [
      {
        key: 'base_url',
        labelKey: 'datasource.field.confluenceBaseUrl',
        placeholder: 'https://confluence.example.com or https://team.atlassian.net/wiki',
      },
      { key: 'username', labelKey: 'datasource.field.confluenceUsername', placeholder: 'name or email' },
      {
        key: 'token',
        labelKey: 'datasource.field.confluencePat',
        placeholder: 'Server/DC PAT',
        secret: true,
        optional: true,
        hintKey: 'datasource.field.confluencePatHint',
      },
      { key: 'password', labelKey: 'datasource.field.confluencePassword', placeholder: 'Server/DC password', secret: true },
      { key: 'api_token', labelKey: 'datasource.field.confluenceApiToken', placeholder: 'Cloud API token', secret: true },
    ],
  },
  {
    type: 'yuque',
    available: true,
    docUrl: 'https://www.yuque.com/yuque/developer/api',
    permissionDocUrl: 'https://www.yuque.com/yuque/developer/api',
    permissionPageUrl: 'https://www.yuque.com/settings/tokens',
    requiredPermissions: ['repo:read', 'doc:read'],
    fields: [
      { key: 'api_token', labelKey: 'datasource.field.apiToken', placeholder: '', secret: true },
      {
        key: 'base_url',
        labelKey: 'datasource.field.baseUrl',
        placeholder: 'https://www.yuque.com',
        optional: true,
        hintKey: 'datasource.field.baseUrlHint',
      },
    ],
  },
  {
    type: 'dingtalk',
    available: true,
    docUrl: 'https://open.dingtalk.com/document/development/knowledge-base-overview',
    permissionDocUrl: 'https://open.dingtalk.com/document/development/get-knowledge-base-list',
    permissionPageUrl: 'https://open-dev.dingtalk.com/',
    requiredPermissions: ['Wiki.Workspace.Read', 'Wiki.Node.Read', 'Storage.File.Read'],
    fields: [
      { key: 'client_id', labelKey: 'datasource.field.clientId', placeholder: 'dingxxxxxxxx' },
      { key: 'client_secret', labelKey: 'datasource.field.clientSecret', placeholder: '', secret: true },
      {
        key: 'operator_id',
        labelKey: 'datasource.field.operatorId',
        placeholder: '',
        hintKey: 'datasource.field.operatorIdHint',
      },
    ],
  },
  {
    type: 'ima',
    available: true,
    docUrl: 'https://ima.qq.com/agent-interface',
    permissionDocUrl: 'https://ima.qq.com/agent-interface',
    permissionPageUrl: 'https://ima.qq.com/agent-interface',
    requiredPermissions: [],
    fields: [
      { key: 'client_id', labelKey: 'datasource.field.imaClientId', placeholder: '', secret: true },
      { key: 'api_key', labelKey: 'datasource.field.imaApiKey', placeholder: '', secret: true },
      {
        key: 'base_url',
        labelKey: 'datasource.field.baseUrl',
        placeholder: 'https://ima.qq.com',
        optional: true,
        hintKey: 'datasource.field.baseUrlHint',
      },
    ],
  },
  {
    type: 'rss',
    available: true,
    docUrl: '',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      {
        key: 'auth_headers',
        labelKey: 'datasource.field.authHeaders',
        placeholder: '',
        optional: true,
        hintKey: 'datasource.field.authHeadersHint',
        fieldType: 'custom_headers',
      },
    ],
  },
  {
    type: 'gitlab',
    available: true,
    docUrl: '',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      { key: 'base_url', labelKey: 'datasource.gitlab.baseUrl', placeholder: 'https://gitlab.example.com' },
      { key: 'access_token', labelKey: 'datasource.gitlab.accessToken', placeholder: '', secret: true },
    ],
  },
  {
    type: 'seafile',
    available: true,
    docUrl: 'https://help.seafile.com/',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      { key: 'base_url', labelKey: 'datasource.seafile.baseUrl', placeholder: 'https://seafile.example.com' },
      {
        key: 'api_token',
        labelKey: 'datasource.seafile.apiToken',
        placeholder: '',
        secret: true,
        hintKey: 'datasource.seafile.apiTokenHint',
      },
    ],
  },
  {
    type: 'nextcloud',
    available: true,
    docUrl: 'https://docs.nextcloud.com/server/stable/user_manual/en/files/access_webdav.html',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      {
        key: 'base_url',
        labelKey: 'datasource.nextcloud.baseUrl',
        placeholder: 'https://nextcloud.example.com',
        hintKey: 'datasource.nextcloud.baseUrlHint',
      },
      { key: 'username', labelKey: 'datasource.nextcloud.username', placeholder: 'alice' },
      {
        key: 'app_password',
        labelKey: 'datasource.nextcloud.appPassword',
        placeholder: '',
        secret: true,
        hintKey: 'datasource.nextcloud.appPasswordHint',
      },
    ],
  },
]

export const isDriveConnector = (type: string) => type === 'feishu_drive' || type === 'lark_drive'
export const isGitLabConnector = (type: string) => type === 'gitlab'
export const isFeishuFamilyConnector = (type: string) =>
  type === 'feishu' || type === 'lark' || type === 'feishu_drive' || type === 'lark_drive'
export const isSeafileConnector = (type: string) => type === 'seafile'
export const seafileLibraryOf = (id: string) => id.split(':')[0]

export function seafileWithin(id: string, parent: string): boolean {
  const [repo, path] = [seafileLibraryOf(id), id.slice(id.indexOf(':') + 1)]
  const parentPath = parent.slice(parent.indexOf(':') + 1)
  return (
    repo === seafileLibraryOf(parent) &&
    (parentPath === '/' || path === parentPath || path.startsWith(parentPath + '/'))
  )
}

export function serializeAuthHeaders(items: CustomHeaderItem[]): string {
  return items
    .filter((h) => h.key.trim())
    .map((h) => `${h.key.trim()}: ${h.value}`)
    .join('\n')
}

export function hydrateRssFeedUrlsFromConfig(config: {
  settings?: Record<string, unknown>
  resource_ids?: string[]
}): Record<string, unknown> {
  const settings = config.settings || {}
  if (String(settings.feed_urls || '').trim()) {
    return { ...settings }
  }
  const ids = config.resource_ids || []
  if (ids.length === 0) {
    return { ...settings }
  }
  return { ...settings, feed_urls: ids.join('\n') }
}

export function extractDriveFolderToken(input: string): string {
  const raw = (input || '').trim()
  if (!raw) return ''
  if (!raw.includes('://') && !raw.includes('/')) return raw
  const match = raw.match(/\/drive\/folder\/([^/?#]+)/)
  if (match && match[1]) return match[1]
  try {
    const u = new URL(raw)
    const segs = u.pathname.split('/').filter(Boolean)
    return segs[segs.length - 1] || raw
  } catch {
    return raw
  }
}

export function resourceIconName(r: Resource): string {
  if (r.type === 'library') return 'root-list'
  if (r.has_children) return 'folder'
  switch (r.type) {
    case 'wiki_space':
      return 'root-list'
    case 'book':
      return 'book'
    case 'doc_category':
      return 'folder-open'
    default:
      return 'file'
  }
}

export const RESOURCE_TYPE_LABEL_MAP: Record<string, string> = {
  wiki_space: 'datasource.resourceType.wikiSpace',
  doc_category: 'datasource.resourceType.docCategory',
  book: 'datasource.resourceType.book',
  library: 'datasource.resourceType.library',
}

export function extractErrorMessage(err: unknown): string {
  const record = err as { message?: string; error?: string } | null | undefined
  return String(record?.message || record?.error || '')
}
