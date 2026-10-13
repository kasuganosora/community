import { describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { groupApi } from '../groupApi'
vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn().mockResolvedValue({ data: new Blob(['file']) }), post: vi.fn().mockResolvedValue({ data: {} }) } }))
describe('group API privacy and long polling', () => {
  it('uses a POST body for potentially large sync cursors and milliseconds for server timeout', async () => {
    const signal = new AbortController().signal
    await groupApi.sync('large-cursor', signal)
    expect(client.post).toHaveBeenCalledWith('/api/v1/chat/group-sync', { since: 'large-cursor', timeout: 25000 }, { signal, timeout: 35000 })
  })
  it('never forwards bearer tokens to arbitrary external attachment URLs', async () => {
    await expect(groupApi.media('https://outside.example/file')).rejects.toThrow('附件地址无效')
    await expect(groupApi.media('/api/v1/accounts/private')).rejects.toThrow('附件地址无效')
    expect(client.get).not.toHaveBeenCalled()
    await groupApi.media('/api/v1/chat/groups/id/messages/msg/media/file')
    expect(client.get).toHaveBeenCalledWith('/api/v1/chat/groups/id/messages/msg/media/file', expect.objectContaining({ responseType: 'blob' }))
  })
})
