import { describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import client from '@/shared/api/client'
import { useEmojiStore } from '../emojiStore'
describe('custom emoji catalogue', () => {
  it('retries failed loads and caches successful responses', async () => {
    const get = vi.spyOn(client, 'get').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: [] })
    const store = useEmojiStore(createPinia())
    await store.load(); expect(store.error).toContain('加载失败')
    await store.load(); expect(store.error).toBeNull()
    await store.load(); expect(get).toHaveBeenCalledTimes(2)
    expect(get).toHaveBeenLastCalledWith('/api/v1/custom_emojis')
  })
})
