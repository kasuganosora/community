import { beforeEach, describe, expect, it } from 'vitest'
import { catalogueFromResponse, EMOJI_CATALOGUE_STORAGE_KEY, readCachedEmojiCatalogue, writeCachedEmojiCatalogue } from '../emojiCatalogue'
import type { MastodonEmoji } from '@/shared/types/mastodon'

const wave: MastodonEmoji = {
  shortcode: 'wave', url: 'https://cdn.test/wave.png', static_url: 'https://cdn.test/wave.png',
  visible_in_picker: true, aliases: ['hello', '你好'], category: '问候', is_sensitive: true,
}

describe('custom emoji catalogue cache', () => {
  beforeEach(() => localStorage.clear())

  it('round-trips aliases and the sensitive flag', () => {
    writeCachedEmojiCatalogue({ revision: 'W/"1.1.1.1"', emojis: [wave] })
    expect(localStorage.getItem(EMOJI_CATALOGUE_STORAGE_KEY)).toContain('你好')
    expect(readCachedEmojiCatalogue()).toEqual({ revision: 'W/"1.1.1.1"', emojis: [wave] })
  })

  it('ignores a corrupt or outdated cache', () => {
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, '{')
    expect(readCachedEmojiCatalogue()).toBeNull()
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({ v: 0, revision: 'x', emojis: [wave] }))
    expect(readCachedEmojiCatalogue()).toBeNull()
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({ v: 1, revision: 'x', emojis: [{ shortcode: 'wave' }] }))
    expect(() => readCachedEmojiCatalogue()).not.toThrow()
    expect(readCachedEmojiCatalogue()).toBeNull()
  })

  it('keeps the previous list on 304 and rejects a malformed 200', () => {
    expect(catalogueFromResponse('W/"1"', 304, 'W/"1"', null)).toEqual({ unchanged: true, revision: 'W/"1"', emojis: [] })
    const row = { ...wave, aliases: null, category: '问候', license: '' }
    const parsed = catalogueFromResponse(null, 200, 'W/"2"', [row]).emojis[0]
    expect(parsed?.shortcode).toBe('wave')
    expect(parsed?.is_sensitive).toBe(true)
    expect(parsed?.aliases).toBeUndefined()
    expect(() => catalogueFromResponse(null, 200, 'W/"2"', [{ ...wave, visible_in_picker: 'yes' }])).toThrow(/invalid emoji catalogue/)
    expect(() => catalogueFromResponse(null, 500, null, null)).toThrow(/failed/)
  })
})
