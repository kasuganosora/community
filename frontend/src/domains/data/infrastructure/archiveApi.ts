import client from '@/shared/api/client'
import type { MastodonAttachment } from '@/shared/types/mastodon'
export interface ArchivedPost {
  id: string; source_id: string; author_id: string; text: string; html: string; spoiler: string
  created_at: string; source_uri: string; private_archive: true
  media: { source_id: string; id?: string; available: boolean; name?: string; content_type?: string; size?: number; description?: string }[]
}
export const archiveApi = {
  async list(maxId?: string, signal?: AbortSignal) { return (await client.get<{ items: ArchivedPost[]; next_max_id: string | null }>('/api/v1/data/archive', { params: { limit: 20, max_id: maxId }, signal })).data },
  async media(id: string, signal?: AbortSignal) { return (await client.get<MastodonAttachment>(`/api/v1/media/${encodeURIComponent(id)}`, { signal })).data },
}
