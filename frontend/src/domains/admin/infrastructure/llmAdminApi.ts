import client from '@/shared/api/client'

export type LlmProviderType = 'openai' | 'openai_compatible' | 'anthropic' | 'google' | 'grok'

export interface LlmProvider {
  id: string
  name: string
  type: LlmProviderType
  base_url?: string
  model: string
  timeout_seconds?: number
  max_tokens?: number
  temperature?: number | null
  input_price_per_1m?: number
  output_price_per_1m?: number
  cache_read_price_per_1m?: number
  currency?: string
  /** Never the key itself: only whether one is stored and a mask. */
  api_key_set: boolean
  api_key_mask?: string
}

export interface LlmTranslationSettings {
  enabled: boolean
  mode: 'traditional' | 'llm'
  user_daily_limit: number
  daily_limit: number
  user_per_minute: number
  allow_private: boolean
  traditional_provider?: string
}

export interface LlmSettings {
  providers: LlmProvider[]
  services: Record<string, string>
  provider_types: LlmProviderType[]
  service_names: string[]
  translation: LlmTranslationSettings
}

export interface LlmProviderInput extends Omit<LlmProvider, 'api_key_set' | 'api_key_mask'> {
  /** Write-only; omitted keeps the stored key. */
  api_key?: string
  clear_api_key?: boolean
}

export interface LlmTestResult { ok: boolean; code?: string; latency_ms: number; model: string; reply?: string }

export interface LlmUsageDay {
  day: string
  calls: number
  errors: number
  input_tokens: number
  output_tokens: number
  cached_tokens: number
  cost: Record<string, number>
}
export interface LlmUsage { days: LlmUsageDay[]; today: { day: string; translation_calls: number } }

const base = '/api/v1/admin/settings/external/llm'
export const llmAdminApi = {
  async get() { return (await client.get<LlmSettings>(base)).data },
  async save(body: { providers: LlmProviderInput[]; services: Record<string, string>; translation: LlmTranslationSettings }) {
    return (await client.put<LlmSettings>(base, body)).data
  },
  async test(id: string) { return (await client.post<LlmTestResult>(`${base}/test`, { id }, { timeout: 120000 })).data },
  async usage(days = 30) { return (await client.get<LlmUsage>(`${base}/usage`, { params: { days } })).data },
}
