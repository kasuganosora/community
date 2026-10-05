import client from '@/shared/api/client'

export interface TagInfo {
  name: string
  following?: boolean
}

export const tagApi = {
  async get(name: string): Promise<TagInfo> {
    const { data } = await client.get<TagInfo>(`/api/v1/tags/${encodeURIComponent(name)}`)
    return data
  },
  async follow(name: string): Promise<TagInfo> {
    const { data } = await client.post<TagInfo>(`/api/v1/tags/${encodeURIComponent(name)}/follow`)
    return data
  },
  async unfollow(name: string): Promise<TagInfo> {
    const { data } = await client.post<TagInfo>(`/api/v1/tags/${encodeURIComponent(name)}/unfollow`)
    return data
  },
  async followed(): Promise<TagInfo[]> {
    const { data } = await client.get<TagInfo[]>('/api/v1/followed_tags', { params: { limit: 40 } })
    return data
  },
}
