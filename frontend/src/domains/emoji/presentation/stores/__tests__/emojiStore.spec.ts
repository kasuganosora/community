import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import client from '@/shared/api/client'
import { EMOJI_CATALOGUE_STORAGE_KEY } from '../../../domain/emojiCatalogue'
import { useEmojiStore } from '../emojiStore'
import type { MastodonEmoji } from '@/shared/types/mastodon'

const wave: MastodonEmoji = { shortcode: 'wave', url: 'https://cdn.test/wave.png', static_url: 'https://cdn.test/wave.png', visible_in_picker: true }
const kinoko: MastodonEmoji = { shortcode: 'Kinoko7_27', url: 'https://cdn.test/kinoko.png', static_url: 'https://cdn.test/kinoko.png', visible_in_picker: true, aliases: ['蘑菇'], is_sensitive: false }
const revision = 'W/"1.2.3.4"'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

describe('custom emoji catalogue', () => {
  beforeEach(() => localStorage.clear())

  it('retries a failed load, then revalidates with the saved revision', async () => {
    const get = vi.spyOn(client, 'get')
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ status: 200, data: [], headers: { etag: 'W/"0.0.0.0"' } })
      .mockResolvedValueOnce({ status: 304, data: [kinoko], headers: { etag: 'W/"0.0.0.0"' } })
    const store = useEmojiStore(createPinia())
    await store.load(); expect(store.error).toContain('加载失败')
    await store.load(); expect(store.error).toBeNull()
    await store.load(true)
    expect(get).toHaveBeenCalledTimes(3)
    expect(get).toHaveBeenLastCalledWith('/api/v1/custom_emojis', expect.objectContaining({
      headers: expect.objectContaining({ 'If-None-Match': 'W/"0.0.0.0"' }),
    }))
    expect(store.emojis).toEqual([])
  })

  it('clears loading and populates emojis after a successful load', async () => {
    vi.spyOn(client, 'get').mockResolvedValueOnce({ status: 200, data: [wave, kinoko], headers: { etag: revision } })
    const store = useEmojiStore(createPinia())
    const pending = store.load()
    expect(store.loading).toBe(true)
    await pending
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
    expect(store.emojis).toEqual([wave, kinoko])
    expect(JSON.parse(localStorage.getItem(EMOJI_CATALOGUE_STORAGE_KEY) || '{}')).toMatchObject({ v: 1, revision })
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
    const first = deferred<{ data: MastodonEmoji[], status: number, headers: { etag: string } }>()
    const second = deferred<{ data: MastodonEmoji[], status: number, headers: { etag: string } }>()
    const third = deferred<{ data: MastodonEmoji[], status: number, headers: { etag: string } }>()
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

    first.resolve({ data: [wave], status: 200, headers: { etag: 'W/"old"' } })
    await initial
    await flushPromises()
    expect(store.loading).toBe(true)
    expect(store.emojis).toEqual([])

    second.resolve({ data: [wave], status: 200, headers: { etag: 'W/"old"' } })
    await flushPromises()
    expect(store.loading).toBe(true)
    expect(store.emojis).toEqual([])

    third.resolve({ data: [kinoko], status: 200, headers: { etag: revision } })
    await refresh
    await repeated
    await flushPromises()
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
    expect(store.emojis).toEqual([kinoko])
    await store.load()
    expect(get).toHaveBeenCalledTimes(3)
    expect(store.emojis).toEqual([kinoko])
  })

  it('paints a saved catalogue immediately and keeps it when the revision matches', async () => {
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({ v: 1, revision, emojis: [wave] }))
    const pending = deferred<{ status: number, data: unknown, headers: { etag: string } }>()
    const get = vi.spyOn(client, 'get').mockReturnValueOnce(pending.promise as never)
    const store = useEmojiStore(createPinia())
    expect(store.loading).toBe(false)
    expect(store.emojis).toEqual([wave])
    const run = store.load()
    expect(store.loading).toBe(false)
    expect(store.emojis).toEqual([wave])
    pending.resolve({ status: 304, data: [kinoko], headers: { etag: revision } })
    await run
    expect(get).toHaveBeenCalledTimes(1)
    expect(store.emojis).toEqual([wave])
    expect(store.error).toBeNull()
  })

  it('replaces the saved catalogue when the server adds an emoji', async () => {
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({ v: 1, revision, emojis: [wave] }))
    vi.spyOn(client, 'get').mockResolvedValueOnce({ status: 200, data: [wave, kinoko], headers: { etag: 'W/"2.2.2.2"' } })
    const store = useEmojiStore(createPinia())
    await store.load()
    expect(store.emojis).toEqual([wave, kinoko])
    expect(store.emojis[1]?.aliases).toEqual(['蘑菇'])
    expect(JSON.parse(localStorage.getItem(EMOJI_CATALOGUE_STORAGE_KEY) || '{}').revision).toBe('W/"2.2.2.2"')
  })

  it('keeps the saved catalogue when a background refresh fails', async () => {
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({ v: 1, revision, emojis: [wave] }))
    vi.spyOn(client, 'get').mockRejectedValueOnce(new Error('offline'))
    const store = useEmojiStore(createPinia())
    await store.load()
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
    expect(store.emojis).toEqual([wave])
  })

  it('checks once for reaction rows, and again with If-None-Match when the picker refreshes', async () => {
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({ v: 1, revision, emojis: [wave] }))
    const get = vi.spyOn(client, 'get')
      .mockResolvedValueOnce({ status: 304, data: [kinoko], headers: { etag: revision } })
      .mockResolvedValueOnce({ status: 200, data: [kinoko], headers: { etag: 'W/"9.9.9.9"' } })
    const store = useEmojiStore(createPinia())
    await store.load()
    await store.load()
    expect(get).toHaveBeenCalledTimes(1)
    expect(store.emojis).toEqual([wave])
    await store.load(true)
    expect(get).toHaveBeenCalledTimes(2)
    expect(get).toHaveBeenLastCalledWith('/api/v1/custom_emojis', expect.objectContaining({
      headers: expect.objectContaining({ 'If-None-Match': revision }),
    }))
    expect(store.emojis).toEqual([kinoko])
  })
})
