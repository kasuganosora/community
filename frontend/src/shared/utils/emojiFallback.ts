// Custom emoji that fail to load are retried a few times (the media proxy answers
// 503 + Retry-After while a remote server rate-limits us), then replaced by
// their ':shortcode:' text so no broken-image icon or empty box remains.

// Delays of the retries; the proxy's Retry-After is at most a few seconds
// while a remote server throttles us, and its disk cache then serves them.
const RETRY_DELAYS_MS = [3000, 8000, 15000]

function retryUrl(src: string): string {
  try {
    const u = new URL(src, window.location.href)
    u.searchParams.set('retry', String(Number(u.searchParams.get('retry') || 0) + 1))
    return u.toString()
  } catch {
    return ''
  }
}

export function replaceWithShortcode(img: HTMLImageElement): void {
  const text = img.alt || img.title
  if (!text || !img.parentNode) { img.remove(); return }
  const span = document.createElement('span')
  span.className = 'custom-emoji-fallback'
  span.textContent = text
  img.replaceWith(span)
}

export function handleEmojiError(img: HTMLImageElement, delays: number[] = RETRY_DELAYS_MS): void {
  const tries = Number(img.dataset.emojiRetried || 0)
  if (tries >= delays.length) { replaceWithShortcode(img); return }
  img.dataset.emojiRetried = String(tries + 1)
  const next = retryUrl(img.currentSrc || img.src)
  if (!next) { replaceWithShortcode(img); return }
  // Hide while waiting so the line does not show a broken icon.
  img.style.visibility = 'hidden'
  window.setTimeout(() => {
    if (!img.isConnected) return
    img.addEventListener('load', () => { img.style.visibility = '' }, { once: true })
    img.src = next
  }, delays[tries])
}

let installed = false

/** Installs one capture-phase listener for every img.custom-emoji. */
export function installEmojiFallback(root: Document = document): void {
  if (installed) return
  installed = true
  root.addEventListener('error', (event) => {
    const img = event.target
    if (img instanceof HTMLImageElement && img.classList.contains('custom-emoji')) handleEmojiError(img)
  }, true)
}
