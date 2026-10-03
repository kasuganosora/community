import { describe, expect, it } from 'vitest'
import { emojiUrl, renderEmojiHtml, insertShortcode } from '../emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
const smile: MastodonEmoji = { shortcode: 'smile', url: 'https://images.test/smile.gif', static_url: 'https://images.test/smile.png', visible_in_picker: true }

describe('safe custom emoji rendering', () => {
  it('replaces text only and retains links and code literally', () => {
    const html = renderEmojiHtml('<p><a href="https://example.test/:smile:">Hi :smile:</a><code>:smile:</code></p>', [smile])
    const root = document.createElement('div'); root.innerHTML = html
    expect(root.querySelector('img')?.getAttribute('src')).toBe(smile.static_url)
    expect(root.querySelector('a')?.getAttribute('href')).toBe('https://example.test/:smile:')
    expect(root.querySelector('code')?.textContent).toBe(':smile:')
    expect(root.querySelectorAll('img')).toHaveLength(1)
  })
  it('never interpolates injected HTML or unsafe image schemes', () => {
    const html = renderEmojiHtml('<img src=x onerror=alert(1)><script>alert(1)</script>:smile: &lt;svg onload=alert(2)&gt;', [{ ...smile, static_url: 'javascript:alert(1)' }])
    const root = document.createElement('div'); root.innerHTML = html
    expect(root.querySelector('script, img, svg')).toBeNull()
    expect(root.textContent).toContain(':smile:')
    expect(emojiUrl({ ...smile, static_url: 'data:image/svg+xml,<svg onload=alert(1)>' })).toBeNull()
  })
  it('keeps sensitive images unloaded until explicitly revealed', () => {
    const secret = { ...smile, is_sensitive: true }
    expect(renderEmojiHtml(':smile:', [secret])).toBe(':smile:')
    expect(renderEmojiHtml(':smile:', [secret], true)).toContain('<img')
    expect(renderEmojiHtml(':smile:', [{ ...smile, disabled: true }], true)).toBe(':smile:')
  })
  it('replaces selected text without dropping surrounding draft characters', () => {
    expect(insertShortcode('hello selected world', 'smile', 6, 14)).toEqual({ text: 'hello :smile: world', cursor: 13 })
  })
})
