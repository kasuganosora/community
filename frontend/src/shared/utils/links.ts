/**
 * Map a link inside status/bio HTML to an in-app route, so mentions and
 * hashtags open the SPA pages instead of navigating away (or to another
 * server). Returns null for ordinary external links.
 */
export function internalRouteForLink(
  href: string,
  className: string,
  localHosts: string[],
): string | null {
  let url: URL
  try {
    url = new URL(href, 'http://relative.invalid')
  } catch {
    return null
  }
  const classes = className.split(/\s+/)
  const segments = url.pathname.split('/').filter(Boolean).map(safeDecode)
  const isLocal = url.host === 'relative.invalid' || localHosts.includes(url.host)

  if (classes.includes('hashtag')) {
    const i = segments.indexOf('tags')
    const tag = i >= 0 ? segments[i + 1] : segments[segments.length - 1]
    return tag ? `/tags/${encodeURIComponent(tag)}` : null
  }

  if (classes.includes('mention')) {
    const first = segments[0] ?? ''
    let user = ''
    if (first.startsWith('@')) user = first.slice(1)
    else if (first === 'users' && segments[1]) user = segments[1]
    if (!user) return null
    if (user.includes('@')) return `/@${user}`
    return isLocal ? `/@${user}` : `/@${user}@${url.host}`
  }

  return null
}

function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

const MAX_LINK_TEXT = 30

/**
 * Display text for a bare URL link (Mastodon style): drop the protocol and a
 * leading "www.", then cut to `max` characters with an ellipsis. Returns null
 * when the link text is not the URL itself (e.g. a labelled link).
 */
export function shortenUrlText(text: string, href: string, max = MAX_LINK_TEXT): string | null {
  const t = text.trim()
  if (!/^https?:\/\//i.test(t)) return null
  if (t !== href && t !== safeDecode(href)) return null
  const display = t.replace(/^https?:\/\//i, '').replace(/^www\./i, '')
  return display.length > max ? `${display.slice(0, max)}…` : display
}

/**
 * Shorten the visible text of bare URL links in already-sanitized HTML. The
 * href is untouched and the full URL goes into the title tooltip. Mentions,
 * hashtags and links using Mastodon's invisible/ellipsis spans are left alone.
 */
export function shortenBareLinks(html: string, max = MAX_LINK_TEXT): string {
  if (!html.includes('<a') || typeof document === 'undefined') return html
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  let changed = false
  tpl.content.querySelectorAll('a').forEach((a) => {
    if (a.classList.contains('mention') || a.classList.contains('hashtag')) return
    if (a.children.length > 0) return
    const href = a.getAttribute('href') ?? ''
    const short = shortenUrlText(a.textContent ?? '', href, max)
    if (short === null) return
    if (short !== a.textContent) {
      a.textContent = short
      changed = true
    }
    if (!a.hasAttribute('title')) {
      a.setAttribute('title', href)
      changed = true
    }
  })
  return changed ? tpl.innerHTML : html
}

/** Explicit outbound links must not contain executable schemes or credentials. */
export function safeHttpLink(value: string | null | undefined): string | null {
  if (!value) return null
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : null
  } catch { return null }
}
