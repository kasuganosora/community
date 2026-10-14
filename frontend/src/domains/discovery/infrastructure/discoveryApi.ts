import client from '@/shared/api/client'
import type { MastodonStatus } from '@/shared/types/mastodon'

export type ReadingPreference = 'off' | 'generic' | 'personalized'
export interface DiscoveryPreference { preference: ReadingPreference; revision: number; languages?: string[]; enabled?: boolean }
export interface DiscoveryReason { code: string; label: string }
export interface DiscoveryItem { status: MastodonStatus; reason?: DiscoveryReason | null }
export interface DiscoveryPage {
  items: DiscoveryItem[]
  next_cursor: string | null
  generation: string
  mode: 'rules' | 'meili_lexical' | 'meili_hybrid' | 'chronological_fallback' | 'disabled'
  algorithm: string
  preference: ReadingPreference
  preference_revision: number
  disabled: boolean
}
export type FeedbackAction = 'hide_status' | 'hide_author' | 'hide_tag' | 'dismiss_suggestion'
export type TargetKind = 'status' | 'account' | 'tag'
export interface DiscoveryFeedback {
  id: string | number
  action: FeedbackAction
  target_kind: TargetKind
  target_key: string
  target_label?: string
  created_at: string
}
export interface FeedbackInput { action: FeedbackAction; target_kind: TargetKind; target_key: string }
export interface TagItem {
  name: string; url: string; posts_count: number; authors_count: number
  growth: number | null; growth_state: 'rising' | 'falling' | 'steady' | 'no_previous' | 'unknown'; new_entry: boolean
}
export interface TagsPage {
  items: TagItem[]; next_cursor: string | null; as_of: string; checked_at: string
  window: '24h' | '7d'; scope: 'local' | 'federated'; kind: 'trending' | 'recent'
  version: string; coverage_complete: boolean; disabled?: boolean
}
export const discoveryApi = {
  async statuses(cursor?: string, signal?: AbortSignal) {
    return (await client.get<DiscoveryPage>('/api/v1/discovery/statuses', { params: { limit: 20, cursor }, signal })).data
  },
  async preferences() { return (await client.get<DiscoveryPreference>('/api/v1/discovery/preferences')).data },
  async setPreference(preference: ReadingPreference, languages?: string[]) { return (await client.put<DiscoveryPreference>('/api/v1/discovery/preferences', { preference, ...(languages ? { languages } : {}) })).data },
  async feedback() { return (await client.get<DiscoveryFeedback[]>('/api/v1/discovery/feedback')).data },
  async addFeedback(input: FeedbackInput) { return (await client.post<DiscoveryPreference>('/api/v1/discovery/feedback', input)).data },
  async removeFeedback(id: string | number) { return (await client.delete<DiscoveryPreference>(`/api/v1/discovery/feedback/${encodeURIComponent(id)}`)).data },
  async tags(params: { window: '24h' | '7d'; scope: 'local' | 'federated'; limit?: number; cursor?: string }, signal?: AbortSignal) {
    return (await client.get<TagsPage>('/api/v1/discovery/tags', { params, signal })).data
  },
}

export function isDiscoveryReset(error: unknown): boolean {
  return (error as { response?: { data?: { code?: string } } })?.response?.data?.code === 'reset_required'
}
