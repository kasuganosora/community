import type { MastodonEmoji } from '@/shared/types/mastodon'
import { sanitizeHtml } from '@/shared/utils/sanitize'

/** `:shortcode:` for a local custom emoji. Remote `:shortcode@host:` is not local. */
export function localReactionCode(name: string): string | null {
  return /^:([^:@]+):$/.exec(name)?.[1] ?? null
}

/**
 * Image source for a reaction pill. Prefer the URL on the reaction itself
 * (timeline payloads and the status returned by the reaction API). When that
 * is missing — the optimistic pill only has the shortcode — resolve it from
 * the custom emoji catalogue.
 */
export function emojiForReaction(
  reaction: { name: string; url?: string; static_url?: string; is_sensitive?: boolean },
  catalogue: readonly MastodonEmoji[] = [],
): MastodonEmoji | null {
  const shortcode = localReactionCode(reaction.name) ?? ''
  const direct = reaction.url || reaction.static_url
  if (direct) {
    return {
      shortcode: shortcode || reaction.name,
      url: reaction.url || direct,
      static_url: reaction.static_url || '',
      visible_in_picker: true,
      is_sensitive: reaction.is_sensitive,
    }
  }
  if (!shortcode) return null
  return catalogue.find(emoji => emoji.shortcode === shortcode && !emoji.disabled) ?? null
}

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

const RECENT_KEY = 'emoji-recent'
const RECENT_LIMIT = 16

export function readRecentEmoji(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
    if (!Array.isArray(raw)) return []
    return raw.filter((item): item is string => typeof item === 'string' && item.length > 0).slice(0, RECENT_LIMIT)
  } catch {
    return []
  }
}

/** Moves the shortcode to the front and persists the list. Storage failures are ignored. */
export function rememberRecentEmoji(shortcode: string, current = readRecentEmoji()): string[] {
  const next = [shortcode, ...current.filter(item => item !== shortcode)].slice(0, RECENT_LIMIT)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(next))
  } catch {
    // Private mode or a full disk should not block inserting the emoji.
  }
  return next
}
