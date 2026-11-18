import type { ModelCatalogEntry, ModelProviderCredentialLabel, ModelProviderExtraField, ModelProviderOption } from '@/api/initialization'
import type { CredentialFieldMeta, ModelCredentialField, ModelSpecOverride } from '@/api/model'

export type EditorModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'

export interface CustomHeaderItem {
  key: string
  value: string
}

export interface ModelFormData {
  id: string
  name: string
  source: 'local' | 'remote'
  provider?: string
  modelName: string
  displayName?: string
  baseUrl?: string
  apiKey?: string
  dimension?: number
  supportsDimensionOverride?: boolean
  interfaceType?: 'ollama' | 'openai'
  isDefault: boolean
  supportsVision?: boolean
  contextWindow?: number
  maxConcurrency?: number
  maxOutputTokens?: number
  thinkingControl?: string
  extraConfig: Record<string, string>
  specCompat?: string
  spec?: ModelSpecOverride | null
  customHeaders?: CustomHeaderItem[]
  appSecret?: string
  credentials?: Partial<Record<ModelCredentialField, CredentialFieldMeta>>
}

export interface CatalogModelOption {
  value: string
  label: string
  contextWindow: string
  dimension?: number
  reasoning: boolean
  vision: boolean
  entry?: ModelCatalogEntry
}

/**
 * Canonical snake_case W4 ModelProviderDTO shape synced with backend
 * `internal/handler/model_catalog.go` and `frontend/src/api/initialization`.
 */
export interface CanonicalModelProviderOption extends Omit<
  ModelProviderOption,
  'requires_auth' | 'default_urls' | 'model_types' | 'extra_fields' | 'credential_labels'
> {
  requires_auth?: boolean
  default_urls?: Record<string, string>
  model_types?: string[]
  extra_fields?: ModelProviderExtraField[]
  credential_labels?: ModelProviderCredentialLabel[]
}

export const PROTOCOL_OPTIONS = [
  'openai-completions',
  'openai-responses',
  'anthropic-messages',
  'google-generative-ai',
] as const

export const LEGACY_THINKING_CONTROL_VALUES = [
  'none',
  'enable_thinking',
  'thinking_type',
  'chat_template_kwargs',
] as const

export const RESERVED_EXTRA_CONFIG_KEYS = new Set(['thinking_control'])
export const VENDOR_NEUTRAL_EXTRA_CONFIG_KEYS = ['api', 'remote_model_name'] as const
