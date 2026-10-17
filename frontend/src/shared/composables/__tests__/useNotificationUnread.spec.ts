import { beforeEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { useNotificationUnread } from '../useNotificationUnread'
vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn(), post: vi.fn() } }))

describe('notification unread badge', () => {
  beforeEach(() => { vi.clearAllMocks(); useNotificationUnread().resetUnread() })

  it('counts every type newer than the marker, including poll results', async () => {
    vi.mocked(client.get).mockImplementation(async (url: string) => url === '/api/v1/markers'
      ? { data: { notifications: { last_read_id: '100' } } }
      : { data: [{ id: '103', type: 'poll' }, { id: '102', type: 'favourite' }, { id: '100', type: 'mention' }] })
    const { unread, refreshUnread } = useNotificationUnread()
    await refreshUnread()
    expect(unread.value).toBe(2)
    expect(client.get).toHaveBeenCalledWith('/api/v1/notifications', { params: { limit: 40, since_id: '100' } })
  })

  it('clears the badge and saves the marker when read', async () => {
    vi.mocked(client.get).mockImplementation(async (url: string) => url === '/api/v1/markers' ? { data: {} } : { data: [{ id: '5' }] })
    vi.mocked(client.post).mockResolvedValue({ data: {} })
    const { unread, refreshUnread, markRead } = useNotificationUnread()
    await refreshUnread()
    expect(unread.value).toBe(1)
    await markRead('5')
    expect(unread.value).toBe(0)
    expect(client.post).toHaveBeenCalledWith('/api/v1/markers', { notifications: { last_read_id: '5' } })
    await refreshUnread()
    expect(client.get).toHaveBeenLastCalledWith('/api/v1/notifications', { params: { limit: 40, since_id: '5' } })
  })
})
