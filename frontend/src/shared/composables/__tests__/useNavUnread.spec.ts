import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
vi.mock('@/shared/api/client', () => ({ default: { get: (...a: unknown[]) => get(...a), post: vi.fn() } }))

import { useNavUnread } from '../useNavUnread'
import { useNotificationUnread } from '../useNotificationUnread'

describe('useNavUnread', () => {
  beforeEach(() => { get.mockReset(); useNavUnread().setOpenPage(null); useNavUnread().resetNavUnread() })

  it('fills all three badges from one request', async () => {
    get.mockResolvedValue({ data: { notifications: 3, announcements: 1, conversations: 2 } })
    const nav = useNavUnread()
    expect(await nav.refreshNavUnread()).toBe(true)
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith('/api/v1/unread_counts')
    expect(useNotificationUnread().unread.value).toBe(3)
    expect(nav.announcements.value).toBe(1)
    expect(nav.conversations.value).toBe(2)
  })

  it('keeps the open page dot cleared across refetches', async () => {
    get.mockResolvedValue({ data: { notifications: 3, announcements: 1, conversations: 2 } })
    const nav = useNavUnread()
    await nav.refreshNavUnread()
    nav.setOpenPage('conversations')
    expect(nav.conversations.value).toBe(0)
    await nav.refreshNavUnread()
    expect(nav.conversations.value).toBe(0)
    expect(nav.announcements.value).toBe(1)
  })

  it('ignores junk and keeps counts on failure', async () => {
    const nav = useNavUnread()
    get.mockResolvedValueOnce({ data: { notifications: -1, announcements: 'x', conversations: 1 } })
    await nav.refreshNavUnread()
    expect(useNotificationUnread().unread.value).toBe(0)
    expect(nav.announcements.value).toBe(0)
    get.mockRejectedValueOnce(new Error('offline'))
    expect(await nav.refreshNavUnread()).toBe(false)
    expect(nav.conversations.value).toBe(1)
  })
})
