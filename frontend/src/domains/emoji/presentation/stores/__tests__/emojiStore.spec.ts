import { describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import client from '@/shared/api/client'
import { useEmojiStore } from '../emojiStore'
import type { MastodonEmoji } from '@/shared/types/mastodon'

const wave: MastodonEmoji = { shortcode: 'wave', url: 'https://cdn.test/wave.png', static_url: 'https://cdn.test/wave.png', visible_in_picker: true }
const kinoko: MastodonEmoji = { shortcode: 'Kinoko7_27', url: 'https://cdn.test/kinoko.png', static_url: 'https://cdn.test/kinoko.png', visible_in_picker: true }

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

describe('custom emoji catalogue', () => {
  it('retries failed loads and caches successful responses', async () => {
    const get = vi.spyOn(client, 'get').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: [] })
    const store = useEmojiStore(createPinia())
    await store.load(); expect(store.error).toContain('加载失败')
    await store.load(); expect(store.error).toBeNull()
    await store.load(); expect(get).toHaveBeenCalledTimes(2)
    expect(get).toHaveBeenLastCalledWith('/api/v1/custom_emojis')
  })

  it('clears loading and populates emojis after a successful load', async () => {
    vi.spyOn(client, 'get').mockResolvedValueOnce({ data: [wave, kinoko] })
    const store = useEmojiStore(createPinia())
    const pending = store.load()
    expect(store.loading).toBe(true)
    await pending
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
    expect(store.emojis).toEqual([wave, kinoko])
  })

  it('clears loading and shows an error when the load fails', async () => {
    vi.spyOn(client, 'get').mockRejectedValueOnce(new Error('offline'))
    const store = useEmojiStore(createPinia())
    await store.load()
    expect(store.loading).toBe(false)
    expect(store.error).toContain('加载失败')
    expect(store.emojis).toEqual([])
  })

  it('clears loading when the client throws before returning a promise', async () => {
    vi.spyOn(client, 'get').mockImplementation(() => { throw new Error('sync') })
    const store = useEmojiStore(createPinia())
    await expect(store.load()).resolves.toBeUndefined()
    expect(store.loading).toBe(false)
    expect(store.error).toContain('加载失败')
  })

  it('does not let an older load or a repeated load(true) leave loading stuck', async () => {
    const first = deferred<{ data: MastodonEmoji[] }>()
    const second = deferred<{ data: MastodonEmoji[] }>()
    const third = deferred<{ data: MastodonEmoji[] }>()
    const get = vi.spyOn(client, 'get')
      .mockReturnValueOnce(first.promise as never)
      .mockReturnValueOnce(second.promise as never)
      .mockReturnValueOnce(third.promise as never)
    const store = useEmojiStore(createPinia())
    const initial = store.load()
    void store.load()
    await flushPromises()
    expect(get).toHaveBeenCalledTimes(1)

    const refresh = store.load(true)
    const repeated = store.load(true)
    await flushPromises()
    expect(get).toHaveBeenCalledTimes(3)
    expect(store.loading).toBe(true)

    first.resolve({ data: [wave] })
    await initial
    await flushPromises()
    expect(store.loading).toBe(true)
    expect(store.emojis).toEqual([])

    second.resolve({ data: [wave] })
    await flushPromises()
    expect(store.loading).toBe(true)
    expect(store.emojis).toEqual([])

    third.resolve({ data: [kinoko] })
    await refresh
    await repeated
    await flushPromises()
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
    expect(store.emojis).toEqual([kinoko])
    await store.load()
    expect(get).toHaveBeenCalledTimes(3)
  })
})
