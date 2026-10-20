import { describe, expect, it, vi } from 'vitest'
import { handleEmojiError } from '../emojiFallback'

function emoji() {
  const p = document.createElement('p')
  p.innerHTML = 'hi <img class="custom-emoji" alt=":fictional_blob:" src="https://c.test/proxy/media/tok">!'
  document.body.append(p)
  return { p, img: p.querySelector('img')! }
}

describe('custom emoji fallback', () => {
  it('retries with fresh URLs, then shows the shortcode text', () => {
    vi.useFakeTimers()
    const { p, img } = emoji()
    handleEmojiError(img, [10, 10])
    expect(img.style.visibility).toBe('hidden')
    vi.advanceTimersByTime(20)
    expect(img.src).toContain('retry=1')
    handleEmojiError(img, [10, 10])
    vi.advanceTimersByTime(20)
    expect(img.src).toContain('retry=2')
    handleEmojiError(img, [10, 10])
    expect(p.querySelector('img')).toBeNull()
    expect(p.textContent).toBe('hi :fictional_blob:!')
    expect(p.querySelector('.custom-emoji-fallback')).not.toBeNull()
    vi.useRealTimers()
  })
})
