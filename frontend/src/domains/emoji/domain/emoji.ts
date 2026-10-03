import type { MastodonEmoji } from '@/shared/types/mastodon'
import { sanitizeHtml } from '@/shared/utils/sanitize'

export function emojiUrl(emoji: MastodonEmoji): string | null {
  const raw = emoji.static_url || emoji.url
  if (!raw) return null
  try {
    const url = new URL(raw, window.location.origin)
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return null
    return url.href
  } catch { return null }
}

export function emojiMap(emojis: MastodonEmoji[] = []): Map<string, MastodonEmoji> {
  return new Map(emojis.filter(e => !e.disabled && /^[a-zA-Z0-9_+-]+$/.test(e.shortcode)).map(e => [e.shortcode, e]))
}

/** Only sanitized text nodes are transformed; attributes and markup never undergo string substitution. */
export function renderEmojiHtml(html: string, emojis: MastodonEmoji[] = [], revealSensitive = false): string {
  const root = document.createElement('div')
  root.innerHTML = sanitizeHtml(html)
  const byCode = emojiMap(emojis)
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const nodes: Text[] = []
  while (walker.nextNode()) nodes.push(walker.currentNode as Text)
  for (const node of nodes) {
    if (node.parentElement?.closest('code, pre')) continue
    const text = node.data
    const fragment = document.createDocumentFragment()
    let offset = 0
    for (const match of text.matchAll(/:([a-zA-Z0-9_+-]+):/g)) {
      const emoji = byCode.get(match[1]!)
      const src = emoji && (!emoji.is_sensitive || revealSensitive) ? emojiUrl(emoji) : null
      if (!src) continue
      fragment.append(document.createTextNode(text.slice(offset, match.index)))
      const img = document.createElement('img')
      img.src = src
      img.alt = match[0]
      img.title = match[0]
      img.className = 'custom-emoji'
      img.loading = 'lazy'
      img.decoding = 'async'
      img.referrerPolicy = 'no-referrer'
      fragment.append(img)
      offset = match.index + match[0].length
    }
    if (offset) { fragment.append(document.createTextNode(text.slice(offset))); node.replaceWith(fragment) }
  }
  return root.innerHTML
}

export function insertShortcode(text: string, shortcode: string, start: number, end: number) {
  const token = `:${shortcode}:`
  return { text: text.slice(0, start) + token + text.slice(end), cursor: start + token.length }
}
