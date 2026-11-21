import { cursorPage, type CursorPage } from '@/shared/utils/pagination'
import client from '@/shared/api/client'
import type { BookmarkFolder, MastodonStatus } from '@/shared/types/mastodon'
import type { PaginationParams } from '@/shared/types/pagination'

export interface BookmarkFolderMutation {
  title?: string
  description?: string
  is_public?: boolean
}

export const bookmarkApi = {
  async list(params?: Pick<PaginationParams, 'max_id' | 'limit'>): Promise<CursorPage<MastodonStatus>> {
    const { data, headers } = await client.get<MastodonStatus[]>('/api/v1/bookmarks', { params })
    return cursorPage(data, headers.link)
  },

  async listFolders(statusId?: string): Promise<BookmarkFolder[]> {
    const params = statusId ? { status_id: statusId } : undefined
    const { data } = await client.get<BookmarkFolder[]>('/api/v1/bookmark_folders', { params })
    return data
  },

  async getFolder(id: string): Promise<BookmarkFolder> {
    const { data } = await client.get<BookmarkFolder>(`/api/v1/bookmark_folders/${encodeURIComponent(id)}`)
    return data
  },

  async createFolder(payload: Required<Pick<BookmarkFolderMutation, 'title'>> & BookmarkFolderMutation): Promise<BookmarkFolder> {
    const { data } = await client.post<BookmarkFolder>('/api/v1/bookmark_folders', payload)
    return data
  },

  async updateFolder(id: string, payload: BookmarkFolderMutation): Promise<BookmarkFolder> {
    const { data } = await client.put<BookmarkFolder>(`/api/v1/bookmark_folders/${encodeURIComponent(id)}`, payload)
    return data
  },

  async deleteFolder(id: string): Promise<void> {
    await client.delete(`/api/v1/bookmark_folders/${encodeURIComponent(id)}`)
  },

  async addToFolder(folderId: string, statusId: string): Promise<BookmarkFolder> {
    const { data } = await client.post<BookmarkFolder>(
      `/api/v1/bookmark_folders/${encodeURIComponent(folderId)}/statuses`,
      { status_id: statusId },
    )
    return data
  },

  async removeFromFolder(folderId: string, statusId: string): Promise<BookmarkFolder> {
    const { data } = await client.delete<BookmarkFolder>(
      `/api/v1/bookmark_folders/${encodeURIComponent(folderId)}/statuses/${encodeURIComponent(statusId)}`,
    )
    return data
  },

  async listFolderStatuses(
    folderId: string,
    params?: Pick<PaginationParams, 'max_id' | 'limit'>,
  ): Promise<CursorPage<MastodonStatus>> {
    const { data, headers } = await client.get<MastodonStatus[]>(
      `/api/v1/bookmark_folders/${encodeURIComponent(folderId)}/statuses`,
      { params },
    )
    return cursorPage(data, headers.link)
  },
}
