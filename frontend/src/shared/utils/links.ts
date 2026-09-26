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
