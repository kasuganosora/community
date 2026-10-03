import client from '@/shared/api/client'

export interface AdminEmoji {
  id: string
  shortcode: string
  url: string
  static_url: string
  domain: string | null
  reaction_role_ids: string[]
  disabled: boolean
  aliases: string[]
  category: string
  license: string
  local_only: boolean
  is_sensitive: boolean
  visible_in_picker: boolean
}
export type EmojiFields = Pick<AdminEmoji, 'shortcode' | 'aliases' | 'category' | 'license' | 'local_only' | 'is_sensitive' | 'visible_in_picker' | 'disabled' | 'reaction_role_ids'>
const base = '/api/v1/admin/custom_emojis'
export const emojiAdminApi = {
  async roles() {
    const response = await client.get<{ id: string | number; name: string }[]>('/api/v1/admin/emoji_roles')
    return response.data.map(role => ({ ...role, id: String(role.id) }))
  },
  async list(params: { origin: 'local' | 'remote'; q: string; category: string; domain: string; page: number; limit: number }) {
    // An empty category/domain is an explicit uncategorized/local filter in the API.
    // The blank search controls mean no filter, so omit them.
    const response = await client.get<AdminEmoji[]>(base, { params: { ...params,
      category: params.category.trim() || undefined,
      domain: params.origin === 'remote' ? params.domain.trim() || undefined : undefined,
    } })
    return { items: response.data, total: Number(response.headers['x-total-count'] || response.data.length) }
  },
  async upload(file: File) {
    const data = new FormData()
    data.append('file', file)
    return (await client.post<{ id: string; url: string }>(`${base}/upload`, data, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })).data
  },
  async save(id: string | null, fields: Partial<EmojiFields> & { image_id?: string }) {
    return id ? (await client.put<AdminEmoji>(`${base}/${id}`, fields)).data
      : (await client.post<AdminEmoji>(base, fields)).data
  },
  async remove(id: string) { await client.delete(`${base}/${id}`) },
  async copy(id: string, shortcode: string) {
    return (await client.post<AdminEmoji>(`${base}/${id}/copy`, { shortcode })).data
  },
  async bulk(ids: string[], action: string, fields: { value?: boolean; category?: string; license?: string; aliases?: string[] } = {}) {
    return (await client.post<{ updated: number }>(`${base}/batch`, { ids, action, ...fields })).data
  },
}
